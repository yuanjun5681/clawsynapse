package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"clawsynapse/internal/store"
)

func privateTestDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOpenCodeBotAndBindingScopeSessionsWorkspaceAndModel(t *testing.T) {
	shared := store.NewFSStore(privateTestDir(t))
	root := privateTestDir(t)
	configs := []OpenCodeConfig{
		{BotID: "bot-a", BindingRevision: "first", Workspace: filepath.Join(root, "a-first"), Model: "opencode/free-model", SessionStore: shared},
		{BotID: "bot-b", BindingRevision: "first", Workspace: filepath.Join(root, "b-first"), Model: "provider/other-model", SessionStore: shared},
		{BotID: "bot-a", BindingRevision: "second", Workspace: filepath.Join(root, "a-second"), Model: "opencode/free-model", SessionStore: shared},
	}
	var first *OpenCodeAdapter
	keys := map[string]bool{}
	for i, cfg := range configs {
		a, err := NewOpenCodeAdapter(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = a
		}
		key := a.scopedSessionKey("same-logical-conversation")
		if keys[key] {
			t.Fatal("Bot/binding reused a session namespace")
		}
		keys[key] = true
		record := filepath.Join(root, cfg.BotID+cfg.BindingRevision+".json")
		a.command = helperCommand("opencode", "success", record, "")
		if _, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{Message: "task", SessionKey: "same-logical-conversation"}); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(record)
		if err != nil {
			t.Fatal(err)
		}
		var captured struct {
			Args []string `json:"args"`
			CWD  string   `json:"cwd"`
		}
		if err := json.Unmarshal(data, &captured); err != nil {
			t.Fatal(err)
		}
		args := strings.Join(captured.Args, " ")
		if strings.Contains(args, "--session") {
			t.Fatalf("new Bot/binding resumed another binding: %s", args)
		}
		if !strings.Contains(args, "--model "+cfg.Model) {
			t.Fatalf("configured model not passed: %s", args)
		}
		if captured.CWD != cfg.Workspace {
			t.Fatalf("native cwd = %s, want %s", captured.CWD, cfg.Workspace)
		}
	}
	// Creating another binding must not overwrite or recover the old mapping.
	id, err := first.loadSession("same-logical-conversation")
	if err != nil || id != "ses_fixture" {
		t.Fatalf("original binding mapping = %s / %v", id, err)
	}
}

func TestBotNativeBindingRequiresExplicitWorkspaceAndRevision(t *testing.T) {
	for _, cfg := range []OpenCodeConfig{
		{BotID: "bot"}, {BotID: "bot", BindingRevision: "revision"},
		{BotID: "bot", Workspace: privateTestDir(t)},
		{BotID: "bot", BindingRevision: "revision", Workspace: "relative"},
	} {
		if _, err := NewOpenCodeAdapter(cfg); err == nil {
			t.Fatalf("unsafe native binding accepted: %+v", cfg)
		}
	}
}

func TestOpenClawSessionIdentifiersAreScopedAndSafe(t *testing.T) {
	root := privateTestDir(t)
	var previous string
	for _, bot := range []string{"a", "b"} {
		a, err := NewOpenClawAdapter(OpenClawConfig{BotID: bot, BindingRevision: "binding", Workspace: filepath.Join(root, bot)})
		if err != nil {
			t.Fatal(err)
		}
		record := filepath.Join(root, bot+".json")
		a.command = helperCommand("openclaw", "success", record, "")
		if _, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{Message: "task", SessionKey: "conversation;id"}); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(record)
		if err != nil {
			t.Fatal(err)
		}
		var captured struct {
			Args []string `json:"args"`
		}
		if err := json.Unmarshal(data, &captured); err != nil {
			t.Fatal(err)
		}
		var id string
		for i, arg := range captured.Args {
			if arg == "--session-id" && i+1 < len(captured.Args) {
				id = captured.Args[i+1]
			}
		}
		if !safeSessionID(id) || id == previous {
			t.Fatalf("unsafe or shared native session %q", id)
		}
		previous = id
	}
}

func TestWebhookCarriesExplicitReceiverIdentity(t *testing.T) {
	var payload webhookPayload
	var bot, binding, instance string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bot = r.Header.Get("X-ClawSynapse-Bot-ID")
		binding = r.Header.Get("X-ClawSynapse-Binding-Revision")
		instance = r.Header.Get("X-ClawSynapse-Instance")
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(`{"success":true,"completed":true}`))
	}))
	defer srv.Close()
	a, err := NewWebhookAdapter(WebhookConfig{BotID: "bot", BindingRevision: "binding", Instance: "receiver", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{Message: "task"}); err != nil {
		t.Fatal(err)
	}
	if bot != "bot" || binding != "binding" || instance != "receiver" || payload.BotID != bot || payload.BindingRevision != binding || payload.Instance != instance {
		t.Fatalf("receiver identity missing: %+v / %s / %s / %s", payload, bot, binding, instance)
	}
	if _, err := NewWebhookAdapter(WebhookConfig{BotID: "bot", URL: srv.URL}); err == nil {
		t.Fatal("ambiguous receiver binding accepted")
	}
}

func TestOpenCodePrivateBotConsumersSurviveAnotherBotCancellation(t *testing.T) {
	root := privateTestDir(t)
	type outcome struct {
		result *DeliverMessageResult
		err    error
	}
	doneA, doneB := make(chan outcome, 1), make(chan outcome, 1)
	enteredA, enteredB := make(chan string, 1), make(chan string, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctxA, cancelA := context.WithCancel(ctx)
	defer cancelA()
	start := func(bot string, ctx context.Context, entered chan string, done chan outcome) {
		a, err := NewOpenCodeAdapter(OpenCodeConfig{BotID: bot, BindingRevision: "binding", Workspace: filepath.Join(root, bot)})
		if err != nil {
			t.Fatal(err)
		}
		a.command = helperCommand("opencode", "private-engine", "", filepath.Join(root, bot+".release"))
		go func() {
			result, err := a.DeliverMessage(ctx, DeliverMessageRequest{Message: "task", SessionKey: "shared-logical-key", Emit: func(event OutputEvent) error { entered <- event.Text; return nil }})
			done <- outcome{result, err}
		}()
	}
	start("a", ctxA, enteredA, doneA)
	start("b", ctx, enteredB, doneB)
	for _, consumer := range []struct {
		bot     string
		entered chan string
	}{{"a", enteredA}, {"b", enteredB}} {
		select {
		case output := <-consumer.entered:
			if output != consumer.bot {
				t.Fatalf("consumer %s received another engine's output: %q", consumer.bot, output)
			}
		case <-ctx.Done():
			t.Fatal("private engine did not produce output")
		}
	}
	cancelA()
	select {
	case a := <-doneA:
		assertExecutionCode(t, a.err, "canceled")
	case <-ctx.Done():
		t.Fatal("canceled Bot child did not exit")
	}
	if err := os.WriteFile(filepath.Join(root, "b.release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case b := <-doneB:
		if b.err != nil || b.result == nil || !b.result.Completed || b.result.SessionID != "ses_b" {
			t.Fatalf("unrelated Bot consumer interrupted: %+v / %v", b.result, b.err)
		}
	case <-ctx.Done():
		t.Fatal("unrelated Bot did not complete")
	}
}
