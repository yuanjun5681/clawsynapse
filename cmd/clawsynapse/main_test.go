package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"clawsynapse/pkg/types"
)

func TestRunVersionPrintsVersion(t *testing.T) {
	stdout := tempFile(t)
	stderr := tempFile(t)

	oldVersion := version
	version = "v9.9.9"
	t.Cleanup(func() {
		version = oldVersion
	})

	code := run([]string{"version"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	got := readFile(t, stdout)
	if got != "v9.9.9\n" {
		t.Fatalf("unexpected stdout %q", got)
	}
}

func TestPrintResultHealthIncludesVersion(t *testing.T) {
	stdout := tempFile(t)
	stderr := tempFile(t)

	printResult(stdout, stderr, types.APIResult{
		OK:      true,
		Code:    "health.ok",
		Message: "service healthy",
		Data: map[string]any{
			"self": map[string]any{
				"nodeId":    "n1-localnodeid0000000000000000000000",
				"version":   "v0.3.2",
				"trustMode": "tofu",
			},
			"peersCount": float64(2),
			"nats": map[string]any{
				"connected": true,
				"status":    "connected",
			},
			"adapter": map[string]any{
				"name":    "openclaw",
				"healthy": true,
			},
		},
	}, false)

	got := readFile(t, stdout)
	if !strings.Contains(got, "health.ok: service healthy\n") {
		t.Fatalf("expected health status line, got %q", got)
	}
	if !strings.Contains(got, "version: v0.3.2\n") {
		t.Fatalf("expected version line, got %q", got)
	}
	if !strings.Contains(got, "adapterHealthy: true\n") {
		t.Fatalf("expected adapterHealthy line, got %q", got)
	}
	if readFile(t, stderr) != "" {
		t.Fatalf("expected empty stderr, got %q", readFile(t, stderr))
	}
}

func TestRunTransferSendPostsExpectedPayload(t *testing.T) {
	client := &stubAPIClient{result: types.APIResult{OK: true, Code: "transfer.sent"}}
	_, err := runTransfer(context.Background(), client, []string{
		"send",
		"--target", "node-beta",
		"--file", "/tmp/demo.txt",
		"--mime-type", "text/plain",
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.method != "POST" {
		t.Fatalf("expected POST, got %s", client.method)
	}
	if client.endpoint != "/v1/transfer/send" {
		t.Fatalf("unexpected endpoint %s", client.endpoint)
	}
	body, _ := client.payload.(map[string]any)
	if body["targetNode"] != "node-beta" {
		t.Fatalf("unexpected targetNode %#v", body["targetNode"])
	}
	if body["filePath"] != "/tmp/demo.txt" {
		t.Fatalf("unexpected filePath %#v", body["filePath"])
	}
	if body["mimeType"] != "text/plain" {
		t.Fatalf("unexpected mimeType %#v", body["mimeType"])
	}
}

func TestRunTransferSendIncludesMetadata(t *testing.T) {
	client := &stubAPIClient{result: types.APIResult{OK: true, Code: "transfer.sent"}}
	_, err := runTransfer(context.Background(), client, []string{
		"send",
		"--target", "node-beta",
		"--file", "/tmp/demo.txt",
		"--metadata", "taskId=task-001",
		"--metadata", "todoId=todo-042",
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := client.payload.(map[string]any)
	md, ok := body["metadata"].(map[string]any)
	if !ok {
		t.Fatal("expected metadata in payload")
	}
	if md["taskId"] != "task-001" {
		t.Fatalf("metadata taskId = %v, want task-001", md["taskId"])
	}
	if md["todoId"] != "todo-042" {
		t.Fatalf("metadata todoId = %v, want todo-042", md["todoId"])
	}
}

func TestRunPublishPostsExpectedPayloadIncludingAgentID(t *testing.T) {
	client := &stubAPIClient{result: types.APIResult{OK: true, Code: "msg.published"}}
	_, err := runPublish(context.Background(), client, []string{
		"--target", "node-beta",
		"--type", "chat.message",
		"--agent", "agent-42",
		"--message", "hello",
		"--session-key", "sess-1",
		"--metadata", "priority=high",
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.method != "POST" {
		t.Fatalf("expected POST, got %s", client.method)
	}
	if client.endpoint != "/v1/publish" {
		t.Fatalf("unexpected endpoint %s", client.endpoint)
	}
	body, _ := client.payload.(map[string]any)
	if body["targetNode"] != "node-beta" {
		t.Fatalf("unexpected targetNode %#v", body["targetNode"])
	}
	if body["type"] != "chat.message" {
		t.Fatalf("unexpected type %#v", body["type"])
	}
	if body["agentId"] != "agent-42" {
		t.Fatalf("unexpected agentId %#v", body["agentId"])
	}
	if body["sessionKey"] != "sess-1" {
		t.Fatalf("unexpected sessionKey %#v", body["sessionKey"])
	}
	md, _ := body["metadata"].(map[string]any)
	if md["priority"] != "high" {
		t.Fatalf("unexpected metadata %#v", body["metadata"])
	}
}

func TestRunTransferSendOmitsEmptyMetadata(t *testing.T) {
	client := &stubAPIClient{result: types.APIResult{OK: true, Code: "transfer.sent"}}
	_, err := runTransfer(context.Background(), client, []string{
		"send",
		"--target", "node-beta",
		"--file", "/tmp/demo.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := client.payload.(map[string]any)
	if _, exists := body["metadata"]; exists {
		t.Fatal("expected no metadata key when --metadata not provided")
	}
}

func TestRunTransferGetFetchesTransferByID(t *testing.T) {
	client := &stubAPIClient{result: types.APIResult{OK: true, Code: "transfer.detail"}}
	_, err := runTransfer(context.Background(), client, []string{"get", "--id", "tr-123"})
	if err != nil {
		t.Fatal(err)
	}
	if client.method != "GET" {
		t.Fatalf("expected GET, got %s", client.method)
	}
	if client.endpoint != "/v1/transfer/tr-123" {
		t.Fatalf("unexpected endpoint %s", client.endpoint)
	}
}

func TestRunTransferDeleteCallsDeleteEndpoint(t *testing.T) {
	client := &stubAPIClient{result: types.APIResult{OK: true, Code: "transfer.deleted"}}
	_, err := runTransfer(context.Background(), client, []string{"delete", "--id", "tr-456"})
	if err != nil {
		t.Fatal(err)
	}
	if client.method != "DELETE" {
		t.Fatalf("expected DELETE, got %s", client.method)
	}
	if client.endpoint != "/v1/transfer/tr-456" {
		t.Fatalf("unexpected endpoint %s", client.endpoint)
	}
}

func TestRunTransferListUsesTransfersEndpoint(t *testing.T) {
	client := &stubAPIClient{result: types.APIResult{OK: true, Code: "transfer.list"}}
	_, err := runTransfer(context.Background(), client, []string{"list"})
	if err != nil {
		t.Fatal(err)
	}
	if client.method != "GET" {
		t.Fatalf("expected GET, got %s", client.method)
	}
	if client.endpoint != "/v1/transfers" {
		t.Fatalf("unexpected endpoint %s", client.endpoint)
	}
}

func TestRenderPeersTable(t *testing.T) {
	peers := []types.Peer{
		{
			NodeID:      "n1-peerwithalias0000000000000000",
			DID:         "did:claw:aliaspeer",
			DisplayName: "Builder Node",
			LocalAlias:  "build-box",
			AuthStatus:  "authenticated",
			TrustStatus: "trusted",
			Labels:      types.Labels{"role": types.LabelString("builder")},
		},
		{
			NodeID:     "n1-peernodisplayname000000000000",
			AuthStatus: "discovered",
			Labels:     types.Labels{"caps": types.LabelSet([]string{"shell", "fs.read"})},
		},
		{
			NodeID:     "n1-peernolabels00000000000000000",
			AuthStatus: "discovered",
		},
	}

	out := renderPeersTable(peers)

	if !strings.Contains(out, "NAME") || !strings.Contains(out, "NODE") ||
		!strings.Contains(out, "AUTH") || !strings.Contains(out, "TRUST") ||
		!strings.Contains(out, "LABELS") {
		t.Fatalf("expected header line with all columns, got %q", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != len(peers)+1 {
		t.Fatalf("expected header + %d rows, got %d lines: %q", len(peers), len(lines), out)
	}

	// Local alias takes precedence over displayName.
	if !strings.Contains(lines[1], "build-box") || strings.Contains(lines[1], "Builder Node") {
		t.Fatalf("expected alias to take precedence over displayName, got %q", lines[1])
	}
	if !strings.Contains(lines[1], "role=builder") {
		t.Fatalf("expected scalar label rendered as k=v, got %q", lines[1])
	}

	// DisplayName used when no alias; set label rendered as k=[a,b].
	if !strings.Contains(lines[2], "caps=[fs.read,shell]") {
		t.Fatalf("expected set label rendered as k=[a,b], got %q", lines[2])
	}

	// No name, no labels: dashes.
	fields := strings.Fields(lines[3])
	if fields[0] != "-" {
		t.Fatalf("expected '-' name for peer without name, got %q", lines[3])
	}
	if !strings.Contains(lines[3], " -") || strings.Contains(lines[3], "=") {
		t.Fatalf("expected '-' labels for peer without labels, got %q", lines[3])
	}
}

func TestRunPeersBuildsFilterQuery(t *testing.T) {
	client := &stubAPIClient{result: types.APIResult{OK: true, Code: "peers.ok"}}
	_, err := runPeers(context.Background(), client, []string{
		"--label", "role=builder",
		"--label", "env = prod",
		"--label-has", "caps=shell",
		"--name", "build box",
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.method != "GET" {
		t.Fatalf("expected GET, got %s", client.method)
	}
	want := "/v1/peers?label=role%3Dbuilder&label=env+%3D+prod&labelHas=caps%3Dshell&name=build+box"
	if client.endpoint != want {
		t.Fatalf("unexpected endpoint %q, want %q", client.endpoint, want)
	}
}

func TestRunPeersPlainListsAll(t *testing.T) {
	client := &stubAPIClient{result: types.APIResult{OK: true, Code: "peers.ok"}}
	if _, err := runPeers(context.Background(), client, nil); err != nil {
		t.Fatal(err)
	}
	if client.endpoint != "/v1/peers" {
		t.Fatalf("unexpected endpoint %q", client.endpoint)
	}
}

func TestRunPeerAliasPutsAlias(t *testing.T) {
	client := &stubAPIClient{result: types.APIResult{OK: true, Code: "peer_attrs.ok"}}
	_, err := runPeer(context.Background(), client, []string{"alias", "node-1", "build-box"})
	if err != nil {
		t.Fatal(err)
	}
	if client.method != "PUT" {
		t.Fatalf("expected PUT, got %s", client.method)
	}
	if client.endpoint != "/v1/peers/node-1/attrs" {
		t.Fatalf("unexpected endpoint %q", client.endpoint)
	}
	body, _ := client.payload.(map[string]any)
	if body["alias"] != "build-box" {
		t.Fatalf("unexpected payload %#v", client.payload)
	}
}

func TestRunPeerAliasWithoutValueClearsAlias(t *testing.T) {
	client := &stubAPIClient{result: types.APIResult{OK: true, Code: "peer_attrs.ok"}}
	if _, err := runPeer(context.Background(), client, []string{"alias", "node-1"}); err != nil {
		t.Fatal(err)
	}
	body, _ := client.payload.(map[string]any)
	if alias, ok := body["alias"]; !ok || alias != "" {
		t.Fatalf("expected empty alias to clear, got %#v", client.payload)
	}
}

func TestRunPeerLabelMergesAndPutsFullLabels(t *testing.T) {
	client := &stubAPIClient{result: types.APIResult{OK: true, Code: "peer_attrs.ok", Data: map[string]any{
		"nodeId": "node-1",
		"attrs": map[string]any{
			"labels": map[string]any{"role": "builder"},
		},
	}}}
	_, err := runPeer(context.Background(), client, []string{"label", "node-1", "caps", "shell", "fs.read"})
	if err != nil {
		t.Fatal(err)
	}
	if client.method != "PUT" {
		t.Fatalf("expected PUT after GET, got %s", client.method)
	}
	body, _ := client.payload.(map[string]any)
	labels, _ := body["labels"].(types.Labels)
	if len(labels) != 2 {
		t.Fatalf("expected merged labels to keep existing keys, got %#v", body)
	}
	if scalar, ok := labels["role"].Scalar(); !ok || scalar != "builder" {
		t.Fatalf("expected existing scalar label preserved, got %#v", labels)
	}
	members := labels["caps"].Members()
	if !labels["caps"].IsSet() || len(members) != 2 {
		t.Fatalf("expected multi-value label stored as set, got %#v", labels["caps"])
	}
}

func TestRunPeerUnlabelRemovesKey(t *testing.T) {
	client := &stubAPIClient{result: types.APIResult{OK: true, Code: "peer_attrs.ok", Data: map[string]any{
		"nodeId": "node-1",
		"attrs": map[string]any{
			"labels": map[string]any{"role": "builder", "env": "prod"},
		},
	}}}
	_, err := runPeer(context.Background(), client, []string{"unlabel", "node-1", "env"})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := client.payload.(map[string]any)
	labels, _ := body["labels"].(types.Labels)
	if _, exists := labels["env"]; exists {
		t.Fatalf("expected env label removed, got %#v", labels)
	}
	if _, exists := labels["role"]; !exists {
		t.Fatalf("expected role label kept, got %#v", labels)
	}
}

type stubAPIClient struct {
	method   string
	endpoint string
	payload  any
	result   types.APIResult
	err      error
}

func (s *stubAPIClient) Get(_ context.Context, endpoint string) (types.APIResult, error) {
	s.method = "GET"
	s.endpoint = endpoint
	s.payload = nil
	return s.result, s.err
}

func (s *stubAPIClient) Post(_ context.Context, endpoint string, payload any) (types.APIResult, error) {
	s.method = "POST"
	s.endpoint = endpoint
	s.payload = payload
	return s.result, s.err
}

func (s *stubAPIClient) Delete(_ context.Context, endpoint string) (types.APIResult, error) {
	s.method = "DELETE"
	s.endpoint = endpoint
	s.payload = nil
	return s.result, s.err
}

func (s *stubAPIClient) Put(_ context.Context, endpoint string, payload any) (types.APIResult, error) {
	s.method = "PUT"
	s.endpoint = endpoint
	s.payload = payload
	return s.result, s.err
}

func tempFile(t *testing.T) *os.File {
	t.Helper()

	f, err := os.CreateTemp(t.TempDir(), "clawsynapse-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = f.Close()
	})
	return f
}

func readFile(t *testing.T, f *os.File) string {
	t.Helper()

	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}
