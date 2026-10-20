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

	defaultAPIAddr := strings.TrimSpace(os.Getenv("LOCAL_API_ADDR"))

	apiAddr := fs.String("api-addr", defaultAPIAddr, "local API address")
	timeout := fs.Duration("timeout", 5*time.Second, "local API timeout")
	asJSON := fs.Bool("json", false, "print raw JSON response")

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
		if err := runDashboard(rest[1:], stdout, stderr); err != nil {
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
		if err := runUpgrade(rest[1:], os.Stdin, stdout, stderr, *apiAddr); err != nil {
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

	client := api.NewClient(*apiAddr, *timeout)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	result, err := dispatch(ctx, client, rest)
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
		return client.Get(ctx, "/v1/health")
	case "peers":
		return runPeers(ctx, client, args[1:])
	case "peer":
		return runPeer(ctx, client, args[1:])
	case "messages":
		return client.Get(ctx, "/v1/messages")
	case "publish":
		return runPublish(ctx, client, args[1:])
	case "auth":
		return runAuth(ctx, client, args[1:])
	case "trust":
		return runTrust(ctx, client, args[1:])
	case "transfer":
		return runTransfer(ctx, client, args[1:])
	case "transfers":
		return client.Get(ctx, "/v1/transfers")
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

	endpoint := "/v1/peers"
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
		return types.APIResult{}, fmt.Errorf("peer %s requires a node id", args[0])
	}
	nodeID := args[1]
	rest := args[2:]
	attrsPath := "/v1/peers/" + url.PathEscape(nodeID) + "/attrs"

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
	fmt.Fprintln(stderr, "usage: clawsynapse peer <subcommand> <node-id> [args]")
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "Subcommands:")
	fmt.Fprintln(stderr, "  alias <node-id> [alias]           set (or clear) a local alias")
	fmt.Fprintln(stderr, "  describe <node-id> [text]         set (or clear) a local description")
	fmt.Fprintln(stderr, "  label <node-id> <key> <value>...  set a local label (one value = scalar, many = set)")
	fmt.Fprintln(stderr, "  unlabel <node-id> <key>           remove a local label")
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "Examples:")
	fmt.Fprintln(stderr, "  clawsynapse peer alias <node-id> build-box")
	fmt.Fprintln(stderr, "  clawsynapse peer label <node-id> caps shell fs.read")
	fmt.Fprintln(stderr, "  clawsynapse peer unlabel <node-id> env")
}

func runPublish(ctx context.Context, client localAPIClient, args []string) (types.APIResult, error) {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	target := fs.String("target", "", "target node id")
	msgType := fs.String("type", "", "message type (e.g. chat.message, task.assign)")
	agentID := fs.String("agent", "", "target agent id override")
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
	body := map[string]any{
		"targetNode": *target,
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
	return client.Post(ctx, "/v1/publish", body)
}

func printPublishHelp(stderr *os.File) {
	fmt.Fprintln(stderr, "usage: clawsynapse publish [flags]")
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "Flags:")
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.String("target", "", "target node id")
	fs.String("type", "", "message type (e.g. chat.message, task.assign)")
	fs.String("agent", "", "target agent id override")
	fs.String("message", "", "message content")
	fs.String("session-key", "", "session key")
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
	fmt.Fprintln(stderr, "  revoke                revoke trust for a node")
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "Examples:")
	fmt.Fprintln(stderr, "  clawsynapse trust pending")
	fmt.Fprintln(stderr, "  clawsynapse trust request --target <node-id>")
	fmt.Fprintln(stderr, "  clawsynapse trust approve --request-id <id>")
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
	case "send":
		fs := flag.NewFlagSet("transfer send", flag.ContinueOnError)
		target := fs.String("target", "", "target node id")
		filePath := fs.String("file", "", "local file path")
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
			"targetNode": *target,
			"filePath":   *filePath,
			"mimeType":   *mimeType,
		}
		if len(metadata) > 0 {
			payload["metadata"] = metadata
		}
		return client.Post(ctx, "/v1/transfer/send", payload)
	case "get":
		fs := flag.NewFlagSet("transfer get", flag.ContinueOnError)
		transferID := fs.String("id", "", "transfer id")
		if err := fs.Parse(args[1:]); err != nil {
			return types.APIResult{}, err
		}
		if strings.TrimSpace(*transferID) == "" {
			return types.APIResult{}, fmt.Errorf("missing --id")
		}
		return client.Get(ctx, "/v1/transfer/"+strings.TrimSpace(*transferID))
	case "delete":
		fs := flag.NewFlagSet("transfer delete", flag.ContinueOnError)
		transferID := fs.String("id", "", "transfer id")
		if err := fs.Parse(args[1:]); err != nil {
			return types.APIResult{}, err
		}
		if strings.TrimSpace(*transferID) == "" {
			return types.APIResult{}, fmt.Errorf("missing --id")
		}
		return client.Delete(ctx, "/v1/transfer/"+strings.TrimSpace(*transferID))
	case "list":
		return client.Get(ctx, "/v1/transfers")
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
	fmt.Fprintln(stderr, "  send     send a file to a target node")
	fmt.Fprintln(stderr, "  get      get transfer details by id")
	fmt.Fprintln(stderr, "  delete   delete a transfer by id")
	fmt.Fprintln(stderr, "  list     list all transfers")
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "Examples:")
	fmt.Fprintln(stderr, "  clawsynapse transfer send --target <node-id> --file <path>")
	fmt.Fprintln(stderr, "  clawsynapse transfer get --id <transfer-id>")
	fmt.Fprintln(stderr, "  clawsynapse transfer list")
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

	if nodeID, _ := self["nodeId"].(string); nodeID != "" {
		fmt.Fprintf(stream, "nodeId: %s\n", nodeID)
	}
	if name, _ := self["displayName"].(string); name != "" {
		fmt.Fprintf(stream, "displayName: %s\n", name)
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
	fmt.Fprintln(w, "NAME\tNODE\tAUTH\tTRUST\tLABELS")
	for _, peer := range peers {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			peerDisplayName(peer),
			peerNodeRef(peer),
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

func peerNodeRef(peer types.Peer) string {
	if did := strings.TrimSpace(peer.DID); did != "" {
		return identity.FingerprintFromDID(did)
	}
	const shortLen = 12
	if len(peer.NodeID) > shortLen {
		return peer.NodeID[:shortLen]
	}
	return peer.NodeID
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
	if nodeID, _ := data["nodeId"].(string); nodeID != "" {
		fmt.Fprintf(stream, "nodeId: %s\n", nodeID)
	}
	fmt.Fprintf(stream, "alias: %s\n", attrs.Alias)
	fmt.Fprintf(stream, "description: %s\n", attrs.Description)
	fmt.Fprintf(stream, "labels: %s\n", renderLabels(attrs.Labels))
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
	fmt.Fprintln(stderr, "usage: clawsynapse [--api-addr host:port] [--timeout 5s] [--json] <command> [args]")
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "Commands:")
	fmt.Fprintln(stderr, "  init                 generate configuration file")
	fmt.Fprintln(stderr, "  service              manage the daemon service (status|start|stop|restart)")
	fmt.Fprintln(stderr, "  dashboard            run the TUI dashboard")
	fmt.Fprintln(stderr, "  logs                 view daemon logs")
	fmt.Fprintln(stderr, "  upgrade              check for or apply upgrades")
	fmt.Fprintln(stderr, "  version              print version")
	fmt.Fprintln(stderr, "  health               check daemon health")
	fmt.Fprintln(stderr, "  peers                list connected peers")
	fmt.Fprintln(stderr, "                       flags: --name <substr>  --label k=v (exact, repeatable)  --label-has k=v (set member, repeatable)")
	fmt.Fprintln(stderr, "  peer                 manage local peer remarks (alias|describe|label|unlabel)")
	fmt.Fprintln(stderr, "  messages             list recent messages")
	fmt.Fprintln(stderr, "  publish              publish a message to a peer")
	fmt.Fprintln(stderr, "  auth                 authentication commands")
	fmt.Fprintln(stderr, "  trust                trust management commands")
	fmt.Fprintln(stderr, "  transfer             file transfer commands")
	fmt.Fprintln(stderr, "  transfers            list all transfers")
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "Global Flags:")
	fmt.Fprintln(stderr, "  --api-addr string    local API address (default \"127.0.0.1:18080\")")
	fmt.Fprintln(stderr, "  --timeout duration   local API timeout (default 5s)")
	fmt.Fprintln(stderr, "  --json               print raw JSON response")
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "Examples:")
	fmt.Fprintln(stderr, "  clawsynapse init")
	fmt.Fprintln(stderr, "  clawsynapse service status")
	fmt.Fprintln(stderr, "  clawsynapse logs --lines 50")
	fmt.Fprintln(stderr, "  clawsynapse upgrade check")
	fmt.Fprintln(stderr, "  clawsynapse publish --target <node-id> --message \"hello\"")
	fmt.Fprintln(stderr, "  clawsynapse peers --label role=builder --label-has caps=shell")
	fmt.Fprintln(stderr, "  clawsynapse peer alias <node-id> build-box")
	fmt.Fprintln(stderr, "  clawsynapse trust request --target <node-id>")
	fmt.Fprintln(stderr, "  clawsynapse transfer send --target <node-id> --file <path>")
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "Run \"clawsynapse <command> -h\" for more details on a command.")
}
