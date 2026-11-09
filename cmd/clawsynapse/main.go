package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"clawsynapse/internal/api"
	"clawsynapse/internal/config"
	"clawsynapse/internal/identity"
	"clawsynapse/pkg/types"
)

var version = "dev"

type stringList []string

type localAPIClient interface {
	Get(ctx context.Context, endpoint string) (types.APIResult, error)
	Post(ctx context.Context, endpoint string, payload any) (types.APIResult, error)
	Put(ctx context.Context, endpoint string, payload any) (types.APIResult, error)
	Delete(ctx context.Context, endpoint string) (types.APIResult, error)
}

func (s *stringList) String() string {
	return strings.Join(*s, ",")
}

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func main() {
	code := run(os.Args[1:], os.Stdout, os.Stderr)
	os.Exit(code)
}

func run(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("clawsynapse", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		printUsage(stderr)
	}

	defaultAPIAddr := ""

	apiAddr := fs.String("api-addr", defaultAPIAddr, "local API address")
	timeout := fs.Duration("timeout", 5*time.Second, "local API timeout")
	asJSON := fs.Bool("json", false, "print raw JSON response")
	bot := fs.String("bot", "", "explicit Bot ID or unambiguous prefix")
	tokenFile := fs.String("token-file", "", "optional Bot-scoped credential file (local management needs no token)")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	rest := fs.Args()
	if len(rest) == 0 {
		printUsage(stderr)
		return 2
	}
	if rest[0] == "context" {
		if *apiAddr == "" {
			*apiAddr = os.Getenv("CLAWSYNAPSE_API_ADDR")
		}
		if *bot == "" {
			*bot = os.Getenv("CLAWSYNAPSE_BOT_ID")
		}
		if *apiAddr == "" || *bot == "" {
			fmt.Fprintln(stderr, "error: context read requires an explicit or invocation-provided daemon endpoint and Bot")
			return 1
		}
	}
	if rest[0] == "migrate" {
		if err := runMigrate(rest[1:], stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	}
	if rest[0] == "init" {
		if err := runInit(rest[1:], os.Stdin, stdout, stderr); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	}
	if rest[0] == "service" {
		if err := runService(rest[1:], stdout, stderr); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	}
	if rest[0] == "dashboard" {
		dashboardArgs := append([]string{}, rest[1:]...)
		if *apiAddr != "" {
			dashboardArgs = append(dashboardArgs, "--api-addr", *apiAddr)
		}
		if *tokenFile != "" {
			dashboardArgs = append(dashboardArgs, "--token-file", *tokenFile)
		}
		if *bot != "" {
			dashboardArgs = append(dashboardArgs, "--bot", *bot)
		}
		if err := runDashboard(dashboardArgs, stdout, stderr); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	}
	if rest[0] == "logs" {
		if err := runLogs(rest[1:], stdout, stderr); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	}
	if rest[0] == "upgrade" {
		if err := runUpgrade(rest[1:], os.Stdin, stdout, stderr, *apiAddr, *tokenFile); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	}
	if rest[0] == "version" {
		fmt.Fprintln(stdout, version)
		return 0
	}

	clientOptions := []api.ClientOption{}
	if *tokenFile != "" {
		clientOptions = append(clientOptions, api.WithTokenFile(*tokenFile))
	}
	if rest[0] == "context" {
		clientOptions = append(clientOptions, api.WithContextToken(os.Getenv("CLAWSYNAPSE_CONTEXT_TOKEN")))
	}
	client := api.NewClient(*apiAddr, *timeout, clientOptions...)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	var result types.APIResult
	var err error
	switch rest[0] {
	case "bots":
		result, err = client.Get(ctx, "/v2/bots")
	case "bot":
		result, err = runBot(ctx, client, *bot, rest[1:])
	case "daemon":
		result, err = client.Get(ctx, "/v2/health")
	default:
		var id string
		id, err = resolveBot(ctx, client, *bot)
		if err == nil {
			scoped := api.NewClient(*apiAddr, *timeout, append(clientOptions, api.WithBot(id))...)
			result, err = dispatch(ctx, scoped, rest)
		}
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		printResult(stdout, stderr, result, *asJSON)
		if strings.TrimSpace(result.Message) == "" {
			fmt.Fprintf(stderr, "error: %v\n", err)
		}
		return 1
	}

	printResult(stdout, stderr, result, *asJSON)
	return 0
}

func dispatch(ctx context.Context, client localAPIClient, args []string) (types.APIResult, error) {
	switch args[0] {
	case "health":
		return client.Get(ctx, "/health")
	case "peers":
		return runPeers(ctx, client, args[1:])
	case "peer":
		return runPeer(ctx, client, args[1:])
	case "messages":
		return client.Get(ctx, "/messages")
	case "publish":
		return runPublish(ctx, client, args[1:])
	case "context":
		return runContext(ctx, client, args[1:])
	case "auth":
		return runAuth(ctx, client, args[1:])
	case "trust":
		return runTrust(ctx, client, args[1:])
	case "transfer":
		return runTransfer(ctx, client, args[1:])
	case "transfers":
		return client.Get(ctx, "/transfers")
	default:
		return types.APIResult{}, fmt.Errorf("unknown command: %s", args[0])
	}
}

func runPeers(ctx context.Context, client localAPIClient, args []string) (types.APIResult, error) {
	fs := flag.NewFlagSet("peers", flag.ContinueOnError)
	name := fs.String("name", "", "match displayName/localAlias substring")
	var labels stringList
	fs.Var(&labels, "label", "label key=value exact match; repeatable")
	var labelHas stringList
	fs.Var(&labelHas, "label-has", "label set member key=value match; repeatable")
	if err := fs.Parse(args); err != nil {
		return types.APIResult{}, err
	}

	var query strings.Builder
	for _, kv := range labels {
		if _, _, ok := strings.Cut(kv, "="); !ok {
			return types.APIResult{}, fmt.Errorf("invalid --label value: %s", kv)
		}
		query.WriteString("&label=")
		query.WriteString(url.QueryEscape(kv))
	}
	for _, kv := range labelHas {
		if _, _, ok := strings.Cut(kv, "="); !ok {
			return types.APIResult{}, fmt.Errorf("invalid --label-has value: %s", kv)
		}
		query.WriteString("&labelHas=")
		query.WriteString(url.QueryEscape(kv))
	}
	if trimmed := strings.TrimSpace(*name); trimmed != "" {
		query.WriteString("&name=")
		query.WriteString(url.QueryEscape(trimmed))
	}

	endpoint := "/peers"
	if query.Len() > 0 {
		endpoint += "?" + strings.TrimPrefix(query.String(), "&")
	}
	return client.Get(ctx, endpoint)
}

func runPeer(ctx context.Context, client localAPIClient, args []string) (types.APIResult, error) {
	if len(args) == 0 {
		return types.APIResult{}, fmt.Errorf("missing peer subcommand")
	}
	if args[0] == "-h" || args[0] == "--help" {
		printPeerHelp(os.Stderr)
		return types.APIResult{}, flag.ErrHelp
	}
	if len(args) < 2 {
		return types.APIResult{}, fmt.Errorf("peer %s requires a Bot id", args[0])
	}
	botID := args[1]
	rest := args[2:]
	attrsPath := "/peers/" + url.PathEscape(botID) + "/attrs"

	switch args[0] {
	case "alias":
		if len(rest) > 1 {
			return types.APIResult{}, fmt.Errorf("peer alias accepts at most one alias argument")
		}
		return client.Put(ctx, attrsPath, map[string]any{"alias": firstOrEmpty(rest)})
	case "describe":
		if len(rest) > 1 {
			return types.APIResult{}, fmt.Errorf("peer describe accepts at most one text argument")
		}
		return client.Put(ctx, attrsPath, map[string]any{"description": firstOrEmpty(rest)})
	case "label":
		if len(rest) < 2 {
			return types.APIResult{}, fmt.Errorf("peer label requires <key> <value> [value...]")
		}
		key := rest[0]
		values := rest[1:]
		var label types.LabelValue
		if len(values) == 1 {
			label = types.LabelString(values[0])
		} else {
			label = types.LabelSet(values)
		}
		return updatePeerLabels(ctx, client, attrsPath, func(labels types.Labels) {
			labels[key] = label
		})
	case "unlabel":
		if len(rest) != 1 {
			return types.APIResult{}, fmt.Errorf("peer unlabel requires exactly one <key>")
		}
		key := rest[0]
		return updatePeerLabels(ctx, client, attrsPath, func(labels types.Labels) {
			delete(labels, key)
		})
	default:
		return types.APIResult{}, fmt.Errorf("unknown peer subcommand: %s", args[0])
	}
}

func firstOrEmpty(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// updatePeerLabels reads current local attrs, mutates the labels via mutate,
// and writes back the full label map.
func updatePeerLabels(ctx context.Context, client localAPIClient, attrsPath string, mutate func(types.Labels)) (types.APIResult, error) {
	current, err := client.Get(ctx, attrsPath)
	if err != nil {
		return current, err
	}
	attrs, err := decodePeerAttrs(current.Data)
	if err != nil {
		return types.APIResult{}, err
	}
	labels := types.Labels{}
	for k, v := range attrs.Labels {
		labels[k] = v
	}
	mutate(labels)
	return client.Put(ctx, attrsPath, map[string]any{"labels": labels})
}

func decodePeerAttrs(data map[string]any) (types.PeerLocalAttrs, error) {
	raw, err := json.Marshal(data["attrs"])
	if err != nil {
		return types.PeerLocalAttrs{}, fmt.Errorf("decode peer attrs: %w", err)
	}
	var attrs types.PeerLocalAttrs
	if err := json.Unmarshal(raw, &attrs); err != nil {
		return types.PeerLocalAttrs{}, fmt.Errorf("decode peer attrs: %w", err)
	}
	return attrs, nil
}

func printPeerHelp(stderr *os.File) {
	fmt.Fprintln(stderr, "usage: clawsynapse --bot <local-id> peer <subcommand> <peer-bot-id> [args]")
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "Subcommands:")
	fmt.Fprintln(stderr, "  alias <bot-id> [alias]           set (or clear) a local alias")
	fmt.Fprintln(stderr, "  describe <bot-id> [text]         set (or clear) a local description")
	fmt.Fprintln(stderr, "  label <bot-id> <key> <value>...  set a local label (one value = scalar, many = set)")
	fmt.Fprintln(stderr, "  unlabel <bot-id> <key>           remove a local label")
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "Examples:")
	fmt.Fprintln(stderr, "  clawsynapse --bot <local-id> peer alias <peer-id> build-box")
	fmt.Fprintln(stderr, "  clawsynapse --bot <local-id> peer label <peer-id> caps shell fs.read")
	fmt.Fprintln(stderr, "  clawsynapse --bot <local-id> peer unlabel <peer-id> env")
}

func runPublish(ctx context.Context, client localAPIClient, args []string) (types.APIResult, error) {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	target := fs.String("target", "", "target Bot id")
	msgType := fs.String("type", "", "message type (e.g. chat.message, task.assign)")
	agentID := fs.String("agent", "", "target agent id override")
	message := fs.String("message", "", "message content")
	sessionKey := fs.String("session-key", "", "session key")
	requestID := fs.String("request-id", "", "original request ID (chat.progress only)")
	runID := fs.String("run-id", "", "active run ID (chat.progress only)")
	idempotencyKey := fs.String("idempotency-key", "", "stable publication retry key")
	var metadataFlags stringList
	fs.Var(&metadataFlags, "metadata", "metadata key=value; repeatable")
	if err := fs.Parse(args); err != nil {
		return types.APIResult{}, err
	}
	if strings.TrimSpace(*target) == "" {
		return types.APIResult{}, fmt.Errorf("missing --target")
	}
	if strings.TrimSpace(*message) == "" {
		return types.APIResult{}, fmt.Errorf("missing --message")
	}
	if *msgType == "chat.progress" && (*requestID == "" || *runID == "" || *sessionKey == "") {
		return types.APIResult{}, fmt.Errorf("chat.progress requires --request-id, --run-id and --session-key")
	}
	if *msgType != "chat.progress" && (*requestID != "" || *runID != "") {
		return types.APIResult{}, fmt.Errorf("--request-id and --run-id require --type chat.progress")
	}
	metadata, err := parseMetadata(metadataFlags)
	if err != nil {
		return types.APIResult{}, err
	}
	body := map[string]any{
		"targetBot":  *target,
		"message":    *message,
		"sessionKey": *sessionKey,
		"metadata":   metadata,
	}
	if *msgType != "" {
		body["type"] = *msgType
	}
	if strings.TrimSpace(*agentID) != "" {
		body["agentId"] = *agentID
	}
	if *requestID != "" {
		body["requestId"], body["runId"] = *requestID, *runID
	}
	if *idempotencyKey != "" {
		body["idempotencyKey"] = *idempotencyKey
	}
	return client.Post(ctx, "/publish", body)
}

func printPublishHelp(stderr *os.File) {
	fmt.Fprintln(stderr, "usage: clawsynapse publish [flags]")
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "Flags:")
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.String("target", "", "target Bot id")
	fs.String("type", "", "message type (e.g. chat.message, task.assign)")
	fs.String("agent", "", "target agent id override")
	fs.String("message", "", "message content")
	fs.String("session-key", "", "session key")
	fs.String("request-id", "", "original request ID (chat.progress only)")
	fs.String("run-id", "", "active run ID (chat.progress only)")
	fs.String("idempotency-key", "", "stable publication retry key")
	fs.Var(&stringList{}, "metadata", "metadata key=value; repeatable")
	fs.PrintDefaults()
}

func runAuth(ctx context.Context, client localAPIClient, args []string) (types.APIResult, error) {
	if len(args) == 0 {
		return types.APIResult{}, fmt.Errorf("missing auth subcommand")
	}
	if args[0] == "help" || args[0] == "-h" {
		flag.CommandLine.SetOutput(os.Stderr)
		flag.CommandLine.PrintDefaults()
		return types.APIResult{}, flag.ErrHelp
	}
	if args[0] != "challenge" {
		return types.APIResult{}, fmt.Errorf("unknown auth subcommand: %s", args[0])
	}

	fs := flag.NewFlagSet("auth challenge", flag.ContinueOnError)
	target := fs.String("target", "", "target Bot id")
	if err := fs.Parse(args[1:]); err != nil {
		return types.APIResult{}, err
	}
	if strings.TrimSpace(*target) == "" {
		return types.APIResult{}, fmt.Errorf("missing --target")
	}

	return client.Post(ctx, "/auth/challenge", map[string]any{
		"targetBot": *target,
	})
}

func runTrust(ctx context.Context, client localAPIClient, args []string) (types.APIResult, error) {
	if len(args) == 0 {
		return types.APIResult{}, fmt.Errorf("missing trust subcommand")
	}

	if args[0] == "-h" || args[0] == "--help" {
		printTrustHelp(os.Stderr)
		return types.APIResult{}, flag.ErrHelp
	}

	switch args[0] {
	case "pending":
		return client.Get(ctx, "/trust/pending")
	case "request":
		fs := flag.NewFlagSet("trust request", flag.ContinueOnError)
		target := fs.String("target", "", "target Bot id")
		reason := fs.String("reason", "", "request reason")
		var capabilities stringList
		fs.Var(&capabilities, "capability", "capability; repeatable")
		if err := fs.Parse(args[1:]); err != nil {
			return types.APIResult{}, err
		}
		if strings.TrimSpace(*target) == "" {
			return types.APIResult{}, fmt.Errorf("missing --target")
		}
		return client.Post(ctx, "/trust/request", map[string]any{
			"targetBot":    *target,
			"reason":       *reason,
			"capabilities": []string(capabilities),
		})
	case "approve", "reject":
		fs := flag.NewFlagSet("trust decision", flag.ContinueOnError)
		requestID := fs.String("request-id", "", "trust request id")
		reason := fs.String("reason", "", "decision reason")
		if err := fs.Parse(args[1:]); err != nil {
			return types.APIResult{}, err
		}
		if strings.TrimSpace(*requestID) == "" {
			return types.APIResult{}, fmt.Errorf("missing --request-id")
		}
		endpoint := "/trust/approve"
		if args[0] == "reject" {
			endpoint = "/trust/reject"
		}
		return client.Post(ctx, endpoint, map[string]any{
			"requestId": *requestID,
			"reason":    *reason,
		})
	case "revoke":
		fs := flag.NewFlagSet("trust revoke", flag.ContinueOnError)
		target := fs.String("target", "", "target Bot id")
		reason := fs.String("reason", "", "revoke reason")
		if err := fs.Parse(args[1:]); err != nil {
			return types.APIResult{}, err
		}
		if strings.TrimSpace(*target) == "" {
			return types.APIResult{}, fmt.Errorf("missing --target")
		}
		return client.Post(ctx, "/trust/revoke", map[string]any{
			"targetBot": *target,
			"reason":    *reason,
		})
	case "help":
		printTrustHelp(os.Stderr)
		return types.APIResult{}, flag.ErrHelp
	default:
		return types.APIResult{}, fmt.Errorf("unknown trust subcommand: %s", args[0])
	}
}

func printTrustHelp(stderr *os.File) {
	fmt.Fprintln(stderr, "usage: clawsynapse trust <subcommand>")
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "Subcommands:")
	fmt.Fprintln(stderr, "  pending              list pending trust requests")
	fmt.Fprintln(stderr, "  request               send a trust request")
	fmt.Fprintln(stderr, "  approve               approve a trust request")
	fmt.Fprintln(stderr, "  reject                reject a trust request")
	fmt.Fprintln(stderr, "  revoke                revoke trust for a Bot")
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "Examples:")
	fmt.Fprintln(stderr, "  clawsynapse --bot <local-id> trust pending")
	fmt.Fprintln(stderr, "  clawsynapse --bot <local-id> trust request --target <peer-id>")
	fmt.Fprintln(stderr, "  clawsynapse --bot <local-id> trust approve --request-id <id>")
}

func runTransfer(ctx context.Context, client localAPIClient, args []string) (types.APIResult, error) {
	if len(args) == 0 {
		return types.APIResult{}, fmt.Errorf("missing transfer subcommand")
	}

	if args[0] == "-h" || args[0] == "--help" {
		printTransferHelp(os.Stderr)
		return types.APIResult{}, flag.ErrHelp
	}

	switch args[0] {
	case "import":
		fs := flag.NewFlagSet("transfer import", flag.ContinueOnError)
		file := fs.String("file", "", "host file imported through local management without a scoped token")
		if err := fs.Parse(args[1:]); err != nil {
			return types.APIResult{}, err
		}
		if strings.TrimSpace(*file) == "" {
			return types.APIResult{}, fmt.Errorf("missing --file")
		}
		return client.Post(ctx, "/transfer/import", map[string]any{"filePath": *file})
	case "forward":
		fs := flag.NewFlagSet("transfer forward", flag.ContinueOnError)
		id := fs.String("id", "", "authorized transfer ID in selected Bot")
		target := fs.String("target", "", "target Bot ID")
		if err := fs.Parse(args[1:]); err != nil {
			return types.APIResult{}, err
		}
		if strings.TrimSpace(*id) == "" || strings.TrimSpace(*target) == "" {
			return types.APIResult{}, fmt.Errorf("missing --id or --target")
		}
		return client.Post(ctx, "/transfer/"+url.PathEscape(*id)+"/forward", map[string]any{"targetBot": *target})
	case "send":
		fs := flag.NewFlagSet("transfer send", flag.ContinueOnError)
		target := fs.String("target", "", "target Bot id")
		filePath := fs.String("file", "", "file path within the selected Bot workspace")
		mimeType := fs.String("mime-type", "", "mime type")
		var metadataFlags stringList
		fs.Var(&metadataFlags, "metadata", "metadata key=value; repeatable")
		if err := fs.Parse(args[1:]); err != nil {
			return types.APIResult{}, err
		}
		if strings.TrimSpace(*target) == "" {
			return types.APIResult{}, fmt.Errorf("missing --target")
		}
		if strings.TrimSpace(*filePath) == "" {
			return types.APIResult{}, fmt.Errorf("missing --file")
		}
		metadata, err := parseMetadata(metadataFlags)
		if err != nil {
			return types.APIResult{}, err
		}
		payload := map[string]any{
			"targetBot": *target,
			"filePath":  *filePath,
			"mimeType":  *mimeType,
		}
		if len(metadata) > 0 {
			payload["metadata"] = metadata
		}
		return client.Post(ctx, "/transfer/send", payload)
	case "get":
		fs := flag.NewFlagSet("transfer get", flag.ContinueOnError)
		transferID := fs.String("id", "", "transfer id")
		if err := fs.Parse(args[1:]); err != nil {
			return types.APIResult{}, err
		}
		if strings.TrimSpace(*transferID) == "" {
			return types.APIResult{}, fmt.Errorf("missing --id")
		}
		return client.Get(ctx, "/transfer/"+url.PathEscape(strings.TrimSpace(*transferID)))
	case "delete":
		fs := flag.NewFlagSet("transfer delete", flag.ContinueOnError)
		transferID := fs.String("id", "", "transfer id")
		if err := fs.Parse(args[1:]); err != nil {
			return types.APIResult{}, err
		}
		if strings.TrimSpace(*transferID) == "" {
			return types.APIResult{}, fmt.Errorf("missing --id")
		}
		return client.Delete(ctx, "/transfer/"+url.PathEscape(strings.TrimSpace(*transferID)))
	case "list":
		return client.Get(ctx, "/transfers")
	case "help":
		printTransferHelp(os.Stderr)
		return types.APIResult{}, flag.ErrHelp
	default:
		return types.APIResult{}, fmt.Errorf("unknown transfer subcommand: %s", args[0])
	}
}

func printTransferHelp(stderr *os.File) {
	fmt.Fprintln(stderr, "usage: clawsynapse transfer <subcommand>")
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "Subcommands:")
	fmt.Fprintln(stderr, "  send     send a selected-Bot workspace file to a target Bot")
	fmt.Fprintln(stderr, "  import   local-management import of an external host file (no scoped token)")
	fmt.Fprintln(stderr, "  forward  send an authorized transfer reference to a target Bot")
	fmt.Fprintln(stderr, "  get      get transfer details by id")
	fmt.Fprintln(stderr, "  delete   delete a transfer by id")
	fmt.Fprintln(stderr, "  list     list all transfers")
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "Examples:")
	fmt.Fprintln(stderr, "  clawsynapse --bot <local-id> transfer import --file <host-path>")
	fmt.Fprintln(stderr, "  clawsynapse --bot <local-id> transfer forward --id <transfer-id> --target <peer-id>")
	fmt.Fprintln(stderr, "  clawsynapse --bot <local-id> transfer send --target <peer-id> --file <workspace-path>")
	fmt.Fprintln(stderr, "  clawsynapse --bot <local-id> transfer get --id <transfer-id>")
	fmt.Fprintln(stderr, "  clawsynapse --bot <local-id> transfer list")
}

func parseMetadata(values []string) (map[string]any, error) {
	if len(values) == 0 {
		return nil, nil
	}

	metadata := make(map[string]any, len(values))
	for _, value := range values {
		key, raw, ok := strings.Cut(value, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("invalid --metadata value: %s", value)
		}
		metadata[strings.TrimSpace(key)] = strings.TrimSpace(raw)
	}
	return metadata, nil
}

func printResult(stdout, stderr *os.File, result types.APIResult, asJSON bool) {
	if result.Code == "" && result.Message == "" && len(result.Data) == 0 && result.TS == 0 && !result.OK {
		return
	}
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(result)
		return
	}

	stream := stdout
	if !result.OK {
		stream = stderr
	}

	if result.Code != "" {
		fmt.Fprintf(stream, "%s: %s\n", result.Code, result.Message)
	} else if result.Message != "" {
		fmt.Fprintln(stream, result.Message)
	}
	switch result.Code {
	case "health.ok":
		printHealthResult(stream, result.Data)
		return
	case "peers.ok":
		printPeersResult(stream, result.Data)
		return
	case "peer_attrs.ok":
		printPeerAttrsResult(stream, result.Data)
		return
	case "msg.published":
		printPublishResult(stream, result.Data)
		return
	case "trust.requested":
		printTrustRequestResult(stream, result.Data)
		return
	case "trust.responded":
		printTrustDecisionResult(stream, result.Data)
		return
	case "trust.revoked":
		printTrustRevokeResult(stream, result.Data)
		return
	case "auth.challenge_accepted":
		printAuthChallengeResult(stream, result.Data)
		return
	case "transfer.sent":
		printTransferSentResult(stream, result.Data)
		return
	case "transfer.detail":
		printTransferDetailResult(stream, result.Data)
		return
	case "transfer.list":
		printTransferListResult(stream, result.Data)
		return
	case "transfer.deleted":
		printTransferDeletedResult(stream, result.Data)
		return
	}
	if len(result.Data) > 0 {
		raw, err := json.MarshalIndent(result.Data, "", "  ")
		if err == nil {
			fmt.Fprintln(stream, string(raw))
		}
	}
}

func printHealthResult(stream *os.File, data map[string]any) {
	self, _ := data["self"].(map[string]any)
	nats, _ := data["nats"].(map[string]any)
	adapter, _ := data["adapter"].(map[string]any)

	if botID, _ := self["botId"].(string); botID != "" {
		fmt.Fprintf(stream, "botId: %s\n", botID)
	}
	if name, _ := self["displayName"].(string); name != "" {
		fmt.Fprintf(stream, "displayName: %s\n", name)
	}
	role, _ := self["role"].(string)
	if role != "" {
		fmt.Fprintf(stream, "role: %s\n", role)
	}
	if labels := renderRawLabels(self["labels"]); labels != "" {
		fmt.Fprintf(stream, "labels: %s\n", labels)
	}
	if version, _ := self["version"].(string); version != "" {
		fmt.Fprintf(stream, "version: %s\n", version)
	}
	if trustMode, _ := self["trustMode"].(string); trustMode != "" {
		fmt.Fprintf(stream, "trustMode: %s\n", trustMode)
	}
	if peersCount, ok := asInt64(data["peersCount"]); ok {
		fmt.Fprintf(stream, "peersCount: %d\n", peersCount)
	}
	if connected, ok := nats["connected"].(bool); ok {
		fmt.Fprintf(stream, "natsConnected: %t\n", connected)
	}
	if status, _ := nats["status"].(string); status != "" {
		fmt.Fprintf(stream, "natsStatus: %s\n", status)
	}
	if applicable, ok := adapter["applicable"].(bool); (ok && !applicable) || role == config.RoleConsole {
		fmt.Fprintln(stream, "harness: not applicable")
		return
	}
	if name, _ := adapter["name"].(string); name != "" {
		fmt.Fprintf(stream, "adapter: %s\n", name)
	}
	if healthy, ok := adapter["healthy"].(bool); ok {
		fmt.Fprintf(stream, "adapterHealthy: %t\n", healthy)
	}
}

func printPeersResult(stream *os.File, data map[string]any) {
	raw, err := json.Marshal(data["items"])
	if err != nil {
		return
	}
	var peers []types.Peer
	if err := json.Unmarshal(raw, &peers); err != nil {
		return
	}
	fmt.Fprint(stream, renderPeersTable(peers))
}

// renderPeersTable renders peers as an aligned table. Pure function for
// testability.
func renderPeersTable(peers []types.Peer) string {
	var buf strings.Builder
	w := tabwriter.NewWriter(&buf, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tBOT\tAUTH\tTRUST\tLABELS")
	for _, peer := range peers {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			peerDisplayName(peer),
			peerBotRef(peer),
			emptyDash(peer.AuthStatus),
			emptyDash(peer.TrustStatus),
			renderLabels(peer.Labels),
		)
	}
	_ = w.Flush()
	return buf.String()
}

func peerDisplayName(peer types.Peer) string {
	if name := strings.TrimSpace(peer.LocalAlias); name != "" {
		return name
	}
	if name := strings.TrimSpace(peer.DisplayName); name != "" {
		return name
	}
	return "-"
}

func peerBotRef(peer types.Peer) string {
	if did := strings.TrimSpace(peer.DID); did != "" {
		return identity.FingerprintFromDID(did)
	}
	const shortLen = 12
	if len(peer.BotID) > shortLen {
		return peer.BotID[:shortLen]
	}
	return peer.BotID
}

// renderLabels renders labels compactly: k=v for scalars, k=[a,b] for sets.
func renderLabels(labels types.Labels) string {
	if len(labels) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := labels[key]
		if value.IsSet() {
			members := value.Members()
			sort.Strings(members)
			parts = append(parts, key+"=["+strings.Join(members, ",")+"]")
		} else if scalar, ok := value.Scalar(); ok {
			parts = append(parts, key+"="+scalar)
		}
	}
	return strings.Join(parts, " ")
}

// renderRawLabels decodes a JSON-decoded labels map (string or []string
// values) and renders it like renderLabels. Returns "" when empty/invalid.
func renderRawLabels(raw any) string {
	if raw == nil {
		return ""
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return ""
	}
	var labels types.Labels
	if err := json.Unmarshal(b, &labels); err != nil {
		return ""
	}
	rendered := renderLabels(labels)
	if rendered == "-" {
		return ""
	}
	return rendered
}

func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func printPeerAttrsResult(stream *os.File, data map[string]any) {
	attrs, err := decodePeerAttrs(data)
	if err != nil {
		return
	}
	if botID, _ := data["botId"].(string); botID != "" {
		fmt.Fprintf(stream, "botId: %s\n", botID)
	}
	fmt.Fprintf(stream, "alias: %s\n", attrs.Alias)
	fmt.Fprintf(stream, "description: %s\n", attrs.Description)
	fmt.Fprintf(stream, "labels: %s\n", renderLabels(attrs.Labels))
}

func printPublishResult(stream *os.File, data map[string]any) {
	targetBot, _ := data["targetBot"].(string)
	messageID, _ := data["messageId"].(string)
	sessionKey, _ := data["sessionKey"].(string)
	if targetBot != "" {
		fmt.Fprintf(stream, "targetBot: %s\n", targetBot)
	}
	if messageID != "" {
		fmt.Fprintf(stream, "messageId: %s\n", messageID)
	}
	if sessionKey != "" {
		fmt.Fprintf(stream, "sessionKey: %s\n", sessionKey)
	}
}

func printTrustRequestResult(stream *os.File, data map[string]any) {
	targetBot, _ := data["targetBot"].(string)
	requestID, _ := data["requestId"].(string)
	if targetBot != "" {
		fmt.Fprintf(stream, "targetBot: %s\n", targetBot)
	}
	if requestID != "" {
		fmt.Fprintf(stream, "requestId: %s\n", requestID)
	}
}

func printTrustDecisionResult(stream *os.File, data map[string]any) {
	requestID, _ := data["requestId"].(string)
	decision, _ := data["decision"].(string)
	if requestID != "" {
		fmt.Fprintf(stream, "requestId: %s\n", requestID)
	}
	if decision != "" {
		fmt.Fprintf(stream, "decision: %s\n", decision)
	}
}

func printTrustRevokeResult(stream *os.File, data map[string]any) {
	targetBot, _ := data["targetBot"].(string)
	if targetBot != "" {
		fmt.Fprintf(stream, "targetBot: %s\n", targetBot)
	}
}

func printAuthChallengeResult(stream *os.File, data map[string]any) {
	targetBot, _ := data["targetBot"].(string)
	status, _ := data["status"].(string)
	if targetBot != "" {
		fmt.Fprintf(stream, "targetBot: %s\n", targetBot)
	}
	if status != "" {
		fmt.Fprintf(stream, "status: %s\n", status)
	}
}

func printTransferSentResult(stream *os.File, data map[string]any) {
	transferID, _ := data["transferId"].(string)
	bucket, _ := data["bucket"].(string)
	checksum, _ := data["checksum"].(string)
	if transferID != "" {
		fmt.Fprintf(stream, "transferId: %s\n", transferID)
	}
	if bucket != "" {
		fmt.Fprintf(stream, "bucket: %s\n", bucket)
	}
	if size, ok := asInt64(data["size"]); ok {
		fmt.Fprintf(stream, "size: %d\n", size)
	}
	if checksum != "" {
		fmt.Fprintf(stream, "checksum: %s\n", checksum)
	}
}

func printTransferDetailResult(stream *os.File, data map[string]any) {
	transfer, _ := data["transfer"].(map[string]any)
	if len(transfer) == 0 {
		return
	}
	raw, err := json.MarshalIndent(transfer, "", "  ")
	if err == nil {
		fmt.Fprintln(stream, string(raw))
	}
}

func printTransferListResult(stream *os.File, data map[string]any) {
	items, _ := data["items"].([]any)
	if len(items) == 0 {
		fmt.Fprintln(stream, "items: 0")
		return
	}
	fmt.Fprintf(stream, "items: %d\n", len(items))
	raw, err := json.MarshalIndent(items, "", "  ")
	if err == nil {
		fmt.Fprintln(stream, string(raw))
	}
}

func printTransferDeletedResult(stream *os.File, data map[string]any) {
	transferID, _ := data["transferId"].(string)
	if transferID != "" {
		fmt.Fprintf(stream, "transferId: %s\n", transferID)
	}
}

func asInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	case json.Number:
		value, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return value, true
	default:
		return 0, false
	}
}

func printUsage(stderr *os.File) {
	fmt.Fprintln(stderr, "usage: clawsynapse [--api-addr host:port] [--token-file FILE] [--bot ID] [--timeout 5s] [--json] <command> [args]")
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "Commands:")
	fmt.Fprintln(stderr, "  init                 generate configuration file")
	fmt.Fprintln(stderr, "  service              manage the daemon service (status|start|stop|restart)")
	fmt.Fprintln(stderr, "  dashboard            run the TUI dashboard")
	fmt.Fprintln(stderr, "  logs                 view daemon logs")
	fmt.Fprintln(stderr, "  upgrade              check for or apply upgrades")
	fmt.Fprintln(stderr, "  version              print version")
	fmt.Fprintln(stderr, "  bots                 list local Bot identities")
	fmt.Fprintln(stderr, "  bot                  create|get|config|start|stop|archive|restore|export|import|convert")
	fmt.Fprintln(stderr, "  migrate              offline schema upgrade, legacy migration or explicit --recover")
	fmt.Fprintln(stderr, "  daemon               check daemon health without a Bot selection")
	fmt.Fprintln(stderr, "  health               check selected Bot health")
	fmt.Fprintln(stderr, "  peers                list connected peers")
	fmt.Fprintln(stderr, "                       flags: --name <substr>  --label k=v (exact, repeatable)  --label-has k=v (set member, repeatable)")
	fmt.Fprintln(stderr, "  peer                 manage local peer remarks (alias|describe|label|unlabel)")
	fmt.Fprintln(stderr, "  messages             list recent messages")
	fmt.Fprintln(stderr, "  publish              publish a message to a peer")
	fmt.Fprintln(stderr, "  context read         page fixed public context for an active invocation")
	fmt.Fprintln(stderr, "  auth                 authentication commands")
	fmt.Fprintln(stderr, "  trust                trust management commands")
	fmt.Fprintln(stderr, "  transfer             file transfer commands")
	fmt.Fprintln(stderr, "  transfers            list all transfers")
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "Global Flags:")
	fmt.Fprintln(stderr, "  --api-addr string    local API address (default \"127.0.0.1:18080\")")
	fmt.Fprintln(stderr, "  --bot string         required Bot ID or unambiguous prefix for business commands")
	fmt.Fprintln(stderr, "  --token-file string  optional Bot-scoped credential; omitted for local management")
	fmt.Fprintln(stderr, "  --timeout duration   local API timeout (default 5s)")
	fmt.Fprintln(stderr, "  --json               print raw JSON response")
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "Only loopback daemon endpoints are supported. Any local process can manage without a token.")
	fmt.Fprintln(stderr, "With a Bot-scoped token, use a full Bot ID; catalog-based prefix resolution is unavailable.")
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "Examples:")
	fmt.Fprintln(stderr, "  clawsynapse init")
	fmt.Fprintln(stderr, "  clawsynapse service status")
	fmt.Fprintln(stderr, "  clawsynapse logs --lines 50")
	fmt.Fprintln(stderr, "  clawsynapse upgrade check")
	fmt.Fprintln(stderr, "  clawsynapse --bot <local-id> publish --target <peer-id> --message \"hello\"")
	fmt.Fprintln(stderr, "  clawsynapse --bot <local-id> peers --label role=builder --label-has caps=shell")
	fmt.Fprintln(stderr, "  clawsynapse --bot <local-id> peer alias <peer-id> build-box")
	fmt.Fprintln(stderr, "  clawsynapse --bot <local-id> trust request --target <peer-id>")
	fmt.Fprintln(stderr, "  clawsynapse --bot <local-id> transfer send --target <peer-id> --file <workspace-path>")
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "Run \"clawsynapse <command> -h\" for more details on a command.")
}
