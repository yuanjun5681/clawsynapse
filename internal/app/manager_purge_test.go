package app

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"clawsynapse/pkg/types"
)

func TestManagerDeletingRejectsHTTPAdmission(t *testing.T) {
	a := testDaemon(t)
	id := newTestBot(t, a, "deleting")
	token := botToken(t, a, id)
	cfg, err := a.manager.Config(id)
	if err != nil {
		t.Fatal(err)
	}
	changed := cfg
	changed.DisplayName = "must not be saved"
	b, err := a.manager.entry(id)
	if err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	before := b.info
	b.info.State = "deleting"
	deleting := b.info
	b.mu.Unlock()

	for _, tc := range []struct {
		name   string
		method string
		path   string
		token  string
		body   any
	}{
		{name: "info", method: "GET", path: "/v2/bots/" + id},
		{name: "scoped info", method: "GET", path: "/v2/bots/" + id, token: token},
		{name: "config", method: "GET", path: "/v2/bots/" + id + "/config"},
		{name: "update", method: "PUT", path: "/v2/bots/" + id + "/config", body: changed},
		{name: "archive", method: "POST", path: "/v2/bots/" + id + "/archive"},
		{name: "restore", method: "POST", path: "/v2/bots/" + id + "/restore"},
		{name: "stop", method: "POST", path: "/v2/bots/" + id + "/stop"},
		{name: "start", method: "POST", path: "/v2/bots/" + id + "/start"},
		{name: "offline reader", method: "GET", path: "/v2/bots/" + id + "/messages", token: token},
		{name: "writer", method: "POST", path: "/v2/bots/" + id + "/publish", token: token, body: map[string]string{"targetBot": "n1-peer", "message": "must not publish"}},
		{name: "model context", method: "GET", path: "/v2/harnesses/default/models?botId=" + id},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := request(t, a, tc.method, tc.path, tc.token, tc.body)
			requireStatus(t, response, http.StatusConflict)
			var result types.APIResult
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.OK || result.Code != "bot.busy" {
				t.Fatalf("deleting admission was not refused: %s", response.Body.String())
			}
		})
	}
	if _, err := a.manager.Transition(id, "unknown"); !purgeFailureCode(err, "bot.busy") {
		t.Fatalf("unknown lifecycle operation bypassed deleting guard: %v", err)
	}
	items := a.manager.List()
	if len(items) != 1 || items[0] != deleting {
		t.Fatalf("listing lost the deletion state: %+v", items)
	}

	b.mu.Lock()
	after := b.info
	b.info = before
	b.mu.Unlock()
	if after != deleting {
		t.Fatalf("rejected operations changed deletion state: %+v", after)
	}
	stored, err := a.manager.Config(id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored, cfg) {
		t.Fatalf("rejected update changed configuration: %+v", stored)
	}
}

func TestManagerDeletingRejectsRetainedRuntimeAcquisition(t *testing.T) {
	a := testDaemon(t)
	id := newTestBot(t, a, "retained runtime")
	b, err := a.manager.entry(id)
	if err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.info.State = "deleting"
	// Purge retains the runtime while draining already admitted references.
	b.runtime = &Runtime{handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("deleting runtime served a newly admitted request")
	})}
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.runtime = nil
		b.info.State = "stopped"
		b.mu.Unlock()
	}()

	for _, write := range []bool{false, true} {
		handler, release, err := a.manager.Acquire(id, write)
		if release != nil {
			release()
		}
		if !purgeFailureCode(err, "bot.busy") || handler != nil || release != nil {
			t.Fatalf("Acquire(write=%t) admitted a deleting runtime: handler=%v release=%t err=%v", write, handler, release != nil, err)
		}
	}
	if _, err := a.manager.Info(id); !purgeFailureCode(err, "bot.busy") {
		t.Fatalf("Info admitted a deleting runtime: %v", err)
	}
}

func TestManagerResumeExcludesDeletingAndClosing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		state   string
		closing bool
	}{
		{name: "deleting", state: "deleting"},
		{name: "starting", state: "starting"},
		{name: "stopping", state: "stopping"},
		{name: "running", state: "running"},
		{name: "closing", state: "stopped", closing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testDaemon(t)
			id := newTestBot(t, a, "resume exclusion")
			b, err := a.manager.entry(id)
			if err != nil {
				t.Fatal(err)
			}
			b.mu.Lock()
			b.info.DesiredState = "running"
			b.info.State = tc.state
			b.mu.Unlock()
			a.manager.mu.Lock()
			a.manager.closing = tc.closing
			a.manager.mu.Unlock()

			a.manager.Resume()

			b.mu.Lock()
			state := b.info.State
			b.mu.Unlock()
			if state != tc.state {
				t.Fatalf("Resume changed excluded state %q to %q", tc.state, state)
			}
		})
	}
}
