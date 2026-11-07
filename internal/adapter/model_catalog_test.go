package adapter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNativeCatalogFormats(t *testing.T) {
	for _, tc := range []struct {
		name  string
		parse func([]byte) ([]ModelInfo, error)
		input string
		want  []ModelInfo
	}{
		{"opencode", parseOpenCodeModels, "z/model\na/model\nz/model\n", []ModelInfo{{ID: "a/model", DisplayName: "a/model"}, {ID: "z/model", DisplayName: "z/model"}}},
		{"omp", parseOMPModels, `{"models":[{"provider":"p","id":"a/b","selector":"p/a/b","name":"Native model"},{"provider":"p","id":"a/b","name":"Native model"}]}`, []ModelInfo{{ID: "p/a/b", DisplayName: "Native model"}}},
		{"pi", parsePiModels, "provider model context max-out thinking images\np m 200K 64K yes no\n", []ModelInfo{{ID: "p/m", DisplayName: "p/m"}}},
		{"empty opencode", parseOpenCodeModels, "", []ModelInfo{}},
		{"empty omp", parseOMPModels, `{"models":[]}`, []ModelInfo{}},
		{"empty pi", parsePiModels, "provider model context max-out thinking images\n", []ModelInfo{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.parse([]byte(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			got = normalizeModels(got)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("models = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestNativeCatalogRejectsUnexpectedFormats(t *testing.T) {
	for _, tc := range []struct {
		parse func([]byte) ([]ModelInfo, error)
		input string
	}{
		{parseOpenCodeModels, "Loading provider secrets...\np/m\n"},
		{parseOpenCodeModels, "unqualified-model"},
		{parseOMPModels, `{"models":[{"provider":"p","id":"m","selector":"different/m"}]}`},
		{parseOMPModels, `{"models":null}`},
		{parseOMPModels, `[]`},
		{parsePiModels, "provider model\np\n"},
		{parsePiModels, "provider model\np model with spaces\n"},
		{parsePiModels, "Unexpected startup warning\nprovider model\np m\n"},
	} {
		if _, err := tc.parse([]byte(tc.input)); err == nil {
			t.Fatalf("accepted unsupported catalog %q", tc.input)
		}
	}
}

func TestCodexCatalogPaginationAndExplicitDefault(t *testing.T) {
	transcript := strings.Join([]string{
		`{"id":1,"result":{"userAgent":"fixture"}}`,
		`{"method":"notification","params":{"ignored":true}}`,
		`{"id":2,"result":{"data":[{"id":"different-alias","model":"native-b","displayName":"B","isDefault":true}],"nextCursor":"page-two"}}`,
		`{"id":3,"result":{"data":[{"model":"native-a","displayName":"A"},{"model":"native-b","displayName":"B"}],"nextCursor":null}}`,
		`{"id":4,"result":{"config":{"model":"native-a"}}}`,
	}, "\n")
	var requests bytes.Buffer
	rpc := &catalogRPC{input: &requests, scanner: bufio.NewScanner(strings.NewReader(transcript))}
	models, defaultModel, err := collectCodexModels(rpc)
	if err != nil {
		t.Fatal(err)
	}
	catalog := ModelCatalog{Models: normalizeModels(models)}
	if defaultModel != "native-a" || !catalog.Contains("native-b") || catalog.Contains("different-alias") || len(catalog.Models) != 2 {
		t.Fatalf("wrong native IDs/default: %#v, %q", catalog, defaultModel)
	}
	var calls []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(requests.String()), "\n") {
		var call map[string]any
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, call)
	}
	wantMethods := []string{"initialize", "initialized", "model/list", "model/list", "config/read"}
	for i, method := range wantMethods {
		if calls[i]["method"] != method {
			t.Fatalf("call %d = %#v", i, calls[i])
		}
	}
	if calls[3]["params"].(map[string]any)["cursor"] != "page-two" {
		t.Fatal("pagination cursor was not returned to native engine")
	}
}

func TestCodexCatalogUnknownDefaultAndProtocolFailures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		responses string
		wantErr   bool
	}{
		{"unknown default", `{"id":2,"result":{"data":[{"model":"native","isDefault":true}]}}
{"id":3,"result":{"config":{"model":null}}}`, false},
		{"default interface unavailable", `{"id":2,"result":{"data":[]}}
{"id":3,"error":{"message":"private diagnostic"}}`, false},
		{"repeated cursor", `{"id":2,"result":{"data":[],"nextCursor":"same"}}
{"id":3,"result":{"data":[],"nextCursor":"same"}}`, true},
		{"native error", `{"id":2,"error":{"message":"private diagnostic"}}`, true},
		{"missing data", `{"id":2,"result":{}}`, true},
		{"wrong response ID", `{"id":99,"result":{"data":[]}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transcript := "{\"id\":1,\"result\":{}}\n" + tc.responses
			rpc := &catalogRPC{input: &bytes.Buffer{}, scanner: bufio.NewScanner(strings.NewReader(transcript))}
			_, defaultModel, err := collectCodexModels(rpc)
			if (err != nil) != tc.wantErr || defaultModel != "" {
				t.Fatalf("default = %q, error = %v", defaultModel, err)
			}
			if err != nil && strings.Contains(err.Error(), "private diagnostic") {
				t.Fatal("raw native error leaked")
			}
		})
	}
}

func TestModelCatalogHelperProcess(t *testing.T) {
	if os.Getenv("CLAWSYNAPSE_MODEL_HELPER") != "1" {
		return
	}
	if os.Getenv("CLAWSYNAPSE_MODEL_MODE") == "failure" {
		fmt.Fprintln(os.Stderr, "private native diagnostics")
		os.Exit(17)
	}
	fmt.Print("p/m\np/m\n")
	os.Exit(0)
}

func TestCatalogCommandsAreMetadataOnlyAndErrorsAreSafe(t *testing.T) {
	for _, tc := range []struct {
		product string
		args    []string
	}{
		{"opencode", []string{"models", "--standalone"}},
		{"omp", []string{"models", "--json", "--no-extensions"}},
		{"pi", []string{"--list-models", "--no-extensions", "--no-mcp", "--no-skills", "--no-context-files", "--no-approve"}},
	} {
		t.Run(tc.product, func(t *testing.T) {
			factory := func(ctx context.Context, args ...string) *exec.Cmd {
				if !reflect.DeepEqual(args, tc.args) {
					t.Fatalf("unsafe query arguments: %#v", args)
				}
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestModelCatalogHelperProcess$")
				cmd.Env = append(os.Environ(), "CLAWSYNAPSE_MODEL_HELPER=1", "CLAWSYNAPSE_MODEL_MODE=failure")
				return cmd
			}
			_, err := queryModelCatalog(context.Background(), tc.product, factory)
			var native *CatalogError
			if !errors.As(err, &native) || native.Code != "harness.models_failed" || strings.Contains(err.Error(), "private") {
				t.Fatalf("unsafe error = %v", err)
			}
		})
	}
	factory := func(ctx context.Context, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestModelCatalogHelperProcess$")
		cmd.Env = append(os.Environ(), "CLAWSYNAPSE_MODEL_HELPER=1")
		return cmd
	}
	catalog, err := queryModelCatalog(context.Background(), "opencode", factory)
	if err != nil || len(catalog.Models) != 1 || catalog.DefaultKnown || catalog.DefaultModel != "" {
		t.Fatalf("catalog = %#v, error = %v", catalog, err)
	}
	for _, product := range []string{"openclaw", "default", "webhook", "unknown"} {
		called := false
		_, err := queryModelCatalog(context.Background(), product, func(ctx context.Context, args ...string) *exec.Cmd {
			called = true
			return nil
		})
		if err == nil || called {
			t.Fatalf("unsupported Harness %q executed a process", product)
		}
	}
}

func TestCatalogCancellationTimeoutAndUnavailableAreSafe(t *testing.T) {
	for _, product := range []string{"opencode", "omp", "pi", "codex"} {
		t.Run(product, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			deadline, stop := context.WithDeadline(context.Background(), time.Unix(0, 0))
			defer stop()
			for _, tc := range []struct {
				ctx  context.Context
				code string
			}{{ctx, "harness.models_canceled"}, {deadline, "harness.models_timeout"}} {
				_, err := queryModelCatalog(tc.ctx, product, func(context.Context, ...string) *exec.Cmd {
					t.Fatal("canceled metadata query started a process")
					return nil
				})
				var native *CatalogError
				if !errors.As(err, &native) || native.Code != tc.code {
					t.Fatalf("error = %v, want %s", err, tc.code)
				}
			}
			_, err := queryModelCatalog(context.Background(), product, func(ctx context.Context, args ...string) *exec.Cmd {
				return exec.CommandContext(ctx, "/nonexistent/clawsynapse-native-catalog-fixture")
			})
			var native *CatalogError
			if !errors.As(err, &native) || native.Code != "harness.models_unavailable" || strings.Contains(err.Error(), "/nonexistent") {
				t.Fatalf("unsafe unavailable error = %v", err)
			}
		})
	}
}
