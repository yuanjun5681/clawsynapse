package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"clawsynapse/internal/api"
	"clawsynapse/pkg/types"
)

type stringList []string

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

	defaultAPIAddr := strings.TrimSpace(os.Getenv("LOCAL_API_ADDR"))
	if defaultAPIAddr == "" {
		defaultAPIAddr = "127.0.0.1:18080"
	}

	apiAddr := fs.String("api-addr", defaultAPIAddr, "local API address")
	timeout := fs.Duration("timeout", 5*time.Second, "local API timeout")
	asJSON := fs.Bool("json", false, "print raw JSON response")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	rest := fs.Args()
	if len(rest) == 0 {
		printUsage(stderr)
		return 2
	}

	client := api.NewClient(*apiAddr, *timeout)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	result, err := dispatch(ctx, client, rest)
	if err != nil {
		printResult(stdout, stderr, result, *asJSON)
		if strings.TrimSpace(result.Message) == "" {
			fmt.Fprintf(stderr, "error: %v\n", err)
		}
		return 1
	}

	printResult(stdout, stderr, result, *asJSON)
	return 0
}

func dispatch(ctx context.Context, client *api.Client, args []string) (types.APIResult, error) {
	switch args[0] {
	case "health":
		return client.Get(ctx, "/v1/health")
	case "peers":
		return client.Get(ctx, "/v1/peers")
	case "messages":
		return client.Get(ctx, "/v1/messages")
	case "publish":
		return runPublish(ctx, client, args[1:])
	case "auth":
		return runAuth(ctx, client, args[1:])
	case "trust":
		return runTrust(ctx, client, args[1:])
	default:
		return types.APIResult{}, fmt.Errorf("unknown command: %s", args[0])
	}
}

func runPublish(ctx context.Context, client *api.Client, args []string) (types.APIResult, error) {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	target := fs.String("target", "", "target node id")
	message := fs.String("message", "", "message content")
	sessionKey := fs.String("session-key", "", "session key")
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
	metadata, err := parseMetadata(metadataFlags)
	if err != nil {
		return types.APIResult{}, err
	}
	return client.Post(ctx, "/v1/publish", map[string]any{
		"targetNode": *target,
		"message":    *message,
		"sessionKey": *sessionKey,
		"metadata":   metadata,
	})
}

func runAuth(ctx context.Context, client *api.Client, args []string) (types.APIResult, error) {
	if len(args) == 0 {
		return types.APIResult{}, fmt.Errorf("missing auth subcommand")
	}
	if args[0] != "challenge" {
		return types.APIResult{}, fmt.Errorf("unknown auth subcommand: %s", args[0])
	}

	fs := flag.NewFlagSet("auth challenge", flag.ContinueOnError)
	target := fs.String("target", "", "target node id")
	if err := fs.Parse(args[1:]); err != nil {
		return types.APIResult{}, err
	}
	if strings.TrimSpace(*target) == "" {
		return types.APIResult{}, fmt.Errorf("missing --target")
	}

	return client.Post(ctx, "/v1/auth/challenge", map[string]any{
		"targetNode": *target,
	})
}

func runTrust(ctx context.Context, client *api.Client, args []string) (types.APIResult, error) {
	if len(args) == 0 {
		return types.APIResult{}, fmt.Errorf("missing trust subcommand")
	}

	switch args[0] {
	case "pending":
		return client.Get(ctx, "/v1/trust/pending")
	case "request":
		fs := flag.NewFlagSet("trust request", flag.ContinueOnError)
		target := fs.String("target", "", "target node id")
		reason := fs.String("reason", "", "request reason")
		var capabilities stringList
		fs.Var(&capabilities, "capability", "capability; repeatable")
		if err := fs.Parse(args[1:]); err != nil {
			return types.APIResult{}, err
		}
		if strings.TrimSpace(*target) == "" {
			return types.APIResult{}, fmt.Errorf("missing --target")
		}
		return client.Post(ctx, "/v1/trust/request", map[string]any{
			"targetNode":   *target,
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
		endpoint := "/v1/trust/approve"
		if args[0] == "reject" {
			endpoint = "/v1/trust/reject"
		}
		return client.Post(ctx, endpoint, map[string]any{
			"requestId": *requestID,
			"reason":    *reason,
		})
	case "revoke":
		fs := flag.NewFlagSet("trust revoke", flag.ContinueOnError)
		target := fs.String("target", "", "target node id")
		reason := fs.String("reason", "", "revoke reason")
		if err := fs.Parse(args[1:]); err != nil {
			return types.APIResult{}, err
		}
		if strings.TrimSpace(*target) == "" {
			return types.APIResult{}, fmt.Errorf("missing --target")
		}
		return client.Post(ctx, "/v1/trust/revoke", map[string]any{
			"targetNode": *target,
			"reason":     *reason,
		})
	default:
		return types.APIResult{}, fmt.Errorf("unknown trust subcommand: %s", args[0])
	}
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
	}
	if len(result.Data) > 0 {
		raw, err := json.MarshalIndent(result.Data, "", "  ")
		if err == nil {
			fmt.Fprintln(stream, string(raw))
		}
	}
}

func printPublishResult(stream *os.File, data map[string]any) {
	targetNode, _ := data["targetNode"].(string)
	messageID, _ := data["messageId"].(string)
	sessionKey, _ := data["sessionKey"].(string)
	if targetNode != "" {
		fmt.Fprintf(stream, "targetNode: %s\n", targetNode)
	}
	if messageID != "" {
		fmt.Fprintf(stream, "messageId: %s\n", messageID)
	}
	if sessionKey != "" {
		fmt.Fprintf(stream, "sessionKey: %s\n", sessionKey)
	}
}

func printTrustRequestResult(stream *os.File, data map[string]any) {
	targetNode, _ := data["targetNode"].(string)
	requestID, _ := data["requestId"].(string)
	if targetNode != "" {
		fmt.Fprintf(stream, "targetNode: %s\n", targetNode)
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
	targetNode, _ := data["targetNode"].(string)
	if targetNode != "" {
		fmt.Fprintf(stream, "targetNode: %s\n", targetNode)
	}
}

func printAuthChallengeResult(stream *os.File, data map[string]any) {
	targetNode, _ := data["targetNode"].(string)
	status, _ := data["status"].(string)
	if targetNode != "" {
		fmt.Fprintf(stream, "targetNode: %s\n", targetNode)
	}
	if status != "" {
		fmt.Fprintf(stream, "status: %s\n", status)
	}
}

func printUsage(stderr *os.File) {
	fmt.Fprintln(stderr, "usage: clawsynapse [--api-addr host:port] [--timeout 5s] [--json] <command>")
	fmt.Fprintln(stderr, "commands: health, peers, messages, publish, auth challenge, trust request|pending|approve|reject|revoke")
}
