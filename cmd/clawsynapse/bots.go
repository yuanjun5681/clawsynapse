package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"clawsynapse/internal/config"
	"clawsynapse/internal/migration"
	"clawsynapse/pkg/types"
)

func completeBotID(value string) bool {
	if len(value) != 35 || !strings.HasPrefix(value, "n1-") {
		return false
	}
	for _, r := range value[3:] {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func resolveBot(ctx context.Context, client localAPIClient, selection string) (string, error) {
	selection = strings.TrimSpace(selection)
	if selection == "" {
		return "", fmt.Errorf("explicit --bot is required; use bots to list local identities")
	}
	// Complete algorithm-generated IDs can be used with a Bot-scoped credential.
	if completeBotID(selection) {
		return selection, nil
	}
	result, err := client.Get(ctx, "/v2/bots")
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(result.Data["items"])
	if err != nil {
		return "", err
	}
	var items []struct {
		BotID string `json:"botId"`
	}
	if err = json.Unmarshal(raw, &items); err != nil {
		return "", err
	}
	found := ""
	for _, item := range items {
		if item.BotID == selection {
			return item.BotID, nil
		}
		if strings.HasPrefix(item.BotID, selection) {
			if found != "" {
				return "", fmt.Errorf("ambiguous Bot prefix %q", selection)
			}
			found = item.BotID
		}
	}
	if found == "" {
		return "", fmt.Errorf("unknown Bot %q", selection)
	}
	return found, nil
}

func readBotConfig(path string) (config.BotConfig, error) {
	var cfg config.BotConfig
	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err = config.DecodeStrict(raw, &cfg); err != nil {
		return cfg, err
	}
	return cfg, config.ValidateBot(cfg)
}

func runBot(ctx context.Context, client localAPIClient, selection string, args []string) (types.APIResult, error) {
	if len(args) == 0 {
		return types.APIResult{}, fmt.Errorf("usage: bot create|config|start|stop|archive|restore|export|import|convert")
	}
	action := args[0]
	fs := flag.NewFlagSet("bot "+action, flag.ContinueOnError)
	file := fs.String("file", "", "Bot JSON configuration file (or private export directory)")
	name := fs.String("name", "", "display name for a new Bot")
	dataDir := fs.String("data-dir", "", "daemon data root for offline export/import")
	role := fs.String("role", "", "explicit role for a new Bot: console|agent")
	adapter := fs.String("adapter", "", "explicit Harness adapter for a new Agent Bot")
	output := fs.String("output", "", "destination for an explicitly converted private bundle")
	if err := fs.Parse(args[1:]); err != nil {
		return types.APIResult{}, err
	}
	if len(fs.Args()) != 0 {
		return types.APIResult{}, fmt.Errorf("unexpected bot arguments")
	}
	if action != "create" && (*role != "" || *adapter != "" || *name != "") {
		return types.APIResult{}, fmt.Errorf("--role, --adapter and --name apply only to bot create")
	}
	if action == "convert" {
		if *file == "" || *output == "" || *dataDir != "" {
			return types.APIResult{}, fmt.Errorf("usage: bot convert --file SOURCE --output DESTINATION")
		}
		err := migration.ConvertArchive(*file, *output)
		return types.APIResult{OK: err == nil, Code: "bot.convert", Data: map[string]any{"file": *output}, TS: time.Now().UnixMilli()}, err
	}
	if *output != "" {
		return types.APIResult{}, fmt.Errorf("--output applies only to bot convert")
	}
	if action == "export" || action == "import" {
		if *file == "" {
			return types.APIResult{}, fmt.Errorf("missing --file private bundle directory")
		}
		root := *dataDir
		if root == "" {
			cfg, err := config.LoadDaemon(nil)
			if err != nil {
				return types.APIResult{}, err
			}
			root = cfg.DataDir
		}
		var id string
		var err error
		if action == "export" {
			if !completeBotID(selection) {
				return types.APIResult{}, fmt.Errorf("offline export requires full --bot ID")
			}
			id = selection
			err = migration.Export(root, id, *file)
		} else {
			id, err = migration.Import(*file, root)
		}
		return types.APIResult{OK: err == nil, Code: "bot." + action, Data: map[string]any{"botId": id}, TS: time.Now().UnixMilli()}, err
	}
	if action == "create" {
		var cfg config.BotConfig
		if *file != "" {
			if *role != "" || *adapter != "" {
				return types.APIResult{}, fmt.Errorf("use explicit role and adapter in --file, or use --role/--adapter without --file")
			}
			var err error
			cfg, err = readBotConfig(*file)
			if err != nil {
				return types.APIResult{}, err
			}
		} else {
			if *role == "" {
				return types.APIResult{}, fmt.Errorf("bot create requires --role console|agent or an explicit role in --file")
			}
			cfg = config.DefaultBotConfigForRole(*role)
			if *role == config.RoleAgent {
				if *adapter == "" {
					return types.APIResult{}, fmt.Errorf("agent creation requires --adapter")
				}
				cfg.Harness.Adapter = *adapter
			} else if *adapter != "" {
				return types.APIResult{}, fmt.Errorf("--adapter applies only to role=agent")
			}
		}
		if *name != "" {
			cfg.DisplayName = *name
		}
		if err := config.ValidateBot(cfg); err != nil {
			return types.APIResult{}, err
		}
		return client.Post(ctx, "/v2/bots", cfg)
	}
	id, err := resolveBot(ctx, client, selection)
	if err != nil {
		return types.APIResult{}, err
	}
	endpoint := "/v2/bots/" + id
	switch action {
	case "get":
		return client.Get(ctx, endpoint)
	case "config":
		if *file == "" {
			return client.Get(ctx, endpoint+"/config")
		}
		cfg, err := readBotConfig(*file)
		if err != nil {
			return types.APIResult{}, err
		}
		return client.Put(ctx, endpoint+"/config", cfg)
	case "start", "stop", "archive", "restore":
		return client.Post(ctx, endpoint+"/"+action, nil)
	default:
		return types.APIResult{}, fmt.Errorf("unknown bot action %q", action)
	}
}

func runMigrate(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	legacy := fs.String("legacy-config", "", "explicit legacy configuration file")
	target := fs.String("data-dir", "", "target daemon root (in-place or a separate empty directory)")
	recoverOnly := fs.Bool("recover", false, "explicitly recover an interrupted offline migration")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *target == "" || len(fs.Args()) != 0 || (*recoverOnly && *legacy != "") {
		return fmt.Errorf("usage: migrate --data-dir ROOT [--legacy-config FILE | --recover]")
	}
	if *recoverOnly {
		if err := migration.Recover(*target); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "migration recovered")
		return nil
	}
	if *legacy == "" {
		if err := migration.Upgrade(*target); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "layout upgraded to schemaVersion 3")
		return nil
	}
	cfg, err := config.LoadLegacy(*legacy)
	if err != nil {
		return err
	}
	id, err := migration.Migrate(cfg, *target)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, id)
	return nil
}
