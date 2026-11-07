package adapter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ModelInfo uses the exact selector accepted by the native engine, never an alias.
type ModelInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

// ModelCatalog does not infer a default from ordering, saved sessions or aliases.
type ModelCatalog struct {
	Adapter      string      `json:"adapter"`
	Models       []ModelInfo `json:"models"`
	DefaultModel string      `json:"defaultModel"`
	DefaultKnown bool        `json:"defaultKnown"`
}

func (c ModelCatalog) Contains(id string) bool {
	for _, model := range c.Models {
		if model.ID == id {
			return true
		}
	}
	return false
}

// CatalogError contains only fixed diagnostics; native output is never exposed.
type CatalogError struct {
	Code    string
	Message string
}

func (e *CatalogError) Error() string { return e.Message }

func catalogFailure(code, message string) error {
	return &CatalogError{Code: code, Message: message}
}

func catalogFormatError() error {
	return catalogFailure("harness.models_invalid", "native Harness returned an unsupported model catalog format")
}

// QueryModelCatalog inherits the daemon environment and queries only model metadata.
// workspace must be a private, empty prospective binding directory, not a project.
func QueryModelCatalog(ctx context.Context, product, workspace string) (ModelCatalog, error) {
	return queryModelCatalog(ctx, product, func(ctx context.Context, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, product, args...)
		cmd.Dir = workspace
		return cmd
	})
}

func queryModelCatalog(ctx context.Context, product string, command commandFactory) (ModelCatalog, error) {
	catalog := ModelCatalog{Adapter: product, Models: []ModelInfo{}}
	var args []string
	switch product {
	case "openclaw", "default":
		return catalog, catalogFailure("harness.models_instance_managed", "OpenClaw models are managed by its dedicated engine instance; per-Bot model overrides are not supported")
	case "opencode":
		args = []string{"models", "--standalone"}
	case "omp":
		args = []string{"models", "--json", "--no-extensions"}
	case "pi":
		args = []string{"--list-models", "--no-extensions", "--no-mcp", "--no-skills", "--no-context-files", "--no-approve"}
	case "codex":
		models, configured, err := queryCodexModels(ctx, command)
		if err != nil {
			return catalog, err
		}
		catalog.Models = normalizeModels(models)
		if configured != "" && catalog.Contains(configured) {
			catalog.DefaultModel, catalog.DefaultKnown = configured, true
		}
		return catalog, nil
	default:
		return catalog, catalogFailure("harness.models_unsupported", "this Harness does not expose a supported model directory")
	}
	var output bytes.Buffer
	_, err := runProcess(ctx, command, args, "", func(line []byte) error {
		output.Write(line)
		output.WriteByte('\n')
		return nil
	}, 30*time.Second)
	if err != nil {
		return catalog, safeCatalogProcessError(err)
	}
	var models []ModelInfo
	switch product {
	case "opencode":
		models, err = parseOpenCodeModels(output.Bytes())
	case "omp":
		models, err = parseOMPModels(output.Bytes())
	case "pi":
		models, err = parsePiModels(output.Bytes())
	}
	if err != nil {
		return catalog, err
	}
	catalog.Models = normalizeModels(models)
	return catalog, nil
}

func safeCatalogProcessError(err error) error {
	var native *ExecutionError
	if errors.As(err, &native) {
		switch native.Code {
		case "timeout":
			return catalogFailure("harness.models_timeout", "native Harness model query timed out")
		case "canceled":
			return catalogFailure("harness.models_canceled", "native Harness model query was canceled")
		case "output_limit", "event_limit":
			return catalogFailure("harness.models_output_limit", "native Harness model catalog exceeded the output limit")
		case "start_failed":
			return catalogFailure("harness.models_unavailable", "native Harness executable could not be started in the daemon environment")
		}
	}
	return catalogFailure("harness.models_failed", "native Harness model query failed; inspect the engine installation and authentication in the daemon environment")
}

func validModelID(id string, qualified bool) bool {
	if id == "" || len(id) > 1024 || strings.HasPrefix(id, "-") {
		return false
	}
	for _, r := range id {
		if r <= ' ' || r == '\x7f' {
			return false
		}
	}
	if qualified {
		provider, model, ok := strings.Cut(id, "/")
		return ok && provider != "" && model != ""
	}
	return true
}

func normalizeModels(models []ModelInfo) []ModelInfo {
	sort.Slice(models, func(i, j int) bool {
		if models[i].ID == models[j].ID {
			return models[i].DisplayName < models[j].DisplayName
		}
		return models[i].ID < models[j].ID
	})
	out := models[:0]
	for _, model := range models {
		if len(out) > 0 && out[len(out)-1].ID == model.ID {
			continue
		}
		if model.DisplayName == "" {
			model.DisplayName = model.ID
		}
		out = append(out, model)
	}
	return out
}

func parseOpenCodeModels(data []byte) ([]ModelInfo, error) {
	models := []ModelInfo{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		id := strings.TrimSpace(line)
		if id == "" {
			continue
		}
		if !validModelID(id, true) {
			return nil, catalogFormatError()
		}
		models = append(models, ModelInfo{ID: id, DisplayName: id})
	}
	return models, nil
}

func parseOMPModels(data []byte) ([]ModelInfo, error) {
	var envelope struct {
		Models *[]struct {
			Provider string `json:"provider"`
			ID       string `json:"id"`
			Selector string `json:"selector"`
			Name     string `json:"name"`
		} `json:"models"`
	}
	if json.Unmarshal(data, &envelope) != nil || envelope.Models == nil {
		return nil, catalogFormatError()
	}
	models := make([]ModelInfo, 0, len(*envelope.Models))
	for _, model := range *envelope.Models {
		id := model.Provider + "/" + model.ID
		if !validModelID(id, true) || model.Provider == "" || model.ID == "" || (model.Selector != "" && model.Selector != id) {
			return nil, catalogFormatError()
		}
		models = append(models, ModelInfo{ID: id, DisplayName: model.Name})
	}
	return models, nil
}

func parsePiModels(data []byte) ([]ModelInfo, error) {
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 {
		return nil, catalogFormatError()
	}
	header := strings.Fields(lines[0])
	if len(header) < 2 || header[0] != "provider" || header[1] != "model" {
		return nil, catalogFormatError()
	}
	models := []ModelInfo{}
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != len(header) || !validModelID(fields[0]+"/"+fields[1], true) {
			return nil, catalogFormatError()
		}
		id := fields[0] + "/" + fields[1]
		models = append(models, ModelInfo{ID: id, DisplayName: id})
	}
	return models, nil
}

type catalogRPC struct {
	input   io.Writer
	scanner *bufio.Scanner
	nextID  int
}

func (c *catalogRPC) call(method string, params any, result any) error {
	c.nextID++
	request, err := json.Marshal(struct {
		ID     int    `json:"id"`
		Method string `json:"method"`
		Params any    `json:"params"`
	}{c.nextID, method, params})
	if err != nil {
		return catalogFormatError()
	}
	if _, err := fmt.Fprintf(c.input, "%s\n", request); err != nil {
		return catalogFailure("harness.models_failed", "native Harness model protocol closed unexpectedly")
	}
	for c.scanner.Scan() {
		var response struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(c.scanner.Bytes(), &response) != nil {
			return catalogFormatError()
		}
		if len(response.ID) == 0 {
			continue // Notifications contain no model result.
		}
		if string(response.ID) != strconv.Itoa(c.nextID) {
			return catalogFormatError()
		}
		if len(response.Error) != 0 && string(response.Error) != "null" {
			return catalogFailure("harness.models_failed", "native Harness rejected the model directory request")
		}
		if len(response.Result) == 0 || string(response.Result) == "null" || json.Unmarshal(response.Result, result) != nil {
			return catalogFormatError()
		}
		return nil
	}
	return catalogFailure("harness.models_failed", "native Harness model protocol ended or exceeded the output limit")
}

func queryCodexModels(ctx context.Context, command commandFactory) ([]ModelInfo, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", safeCatalogProcessError(cancellationError(err, false))
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := command(ctx, "app-server", "--listen", "stdio://")
	cmd.WaitDelay = 2 * time.Second
	attach, cleanup, err := configureProcess(cmd)
	if err != nil {
		return nil, "", safeCatalogProcessError(err)
	}
	defer cleanup()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, "", safeCatalogProcessError(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, "", safeCatalogProcessError(err)
	}
	budget := &outputBudget{cancel: cancel}
	cmd.Stderr = &boundedOutput{budget: budget, stage: "stderr", limit: maxStderrBytes}
	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return nil, "", safeCatalogProcessError(cancellationError(ctx.Err(), false))
		}
		return nil, "", catalogFailure("harness.models_unavailable", "native Harness executable could not be started in the daemon environment")
	}
	defer func() {
		_ = stdin.Close()
		cleanup()
		_ = cmd.Wait()
	}()
	if err := attach(); err != nil {
		return nil, "", safeCatalogProcessError(err)
	}
	limited := &io.LimitedReader{R: stdout, N: maxOutputBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), maxEventBytes)
	rpc := &catalogRPC{input: stdin, scanner: scanner}
	models, configured, err := collectCodexModels(rpc)
	budget.mu.Lock()
	outputErr := budget.err
	budget.mu.Unlock()
	if outputErr != nil {
		return nil, "", safeCatalogProcessError(outputErr)
	}
	if ctx.Err() != nil {
		return nil, "", safeCatalogProcessError(cancellationError(ctx.Err(), false))
	}
	if limited.N == 0 || scanner.Err() != nil {
		return nil, "", catalogFailure("harness.models_output_limit", "native Harness model catalog exceeded the output limit")
	}
	return models, configured, err
}

func collectCodexModels(rpc *catalogRPC) ([]ModelInfo, string, error) {
	var initialized json.RawMessage
	if err := rpc.call("initialize", map[string]any{"clientInfo": map[string]string{"name": "clawsynapse-model-catalog", "version": "1"}}, &initialized); err != nil {
		return nil, "", err
	}
	if _, err := io.WriteString(rpc.input, "{\"method\":\"initialized\"}\n"); err != nil {
		return nil, "", catalogFailure("harness.models_failed", "native Harness initialization failed")
	}
	models := []ModelInfo{}
	cursor := ""
	seen := map[string]bool{}
	for range 100 {
		var result struct {
			Data *[]struct {
				Model       string `json:"model"`
				DisplayName string `json:"displayName"`
			} `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		params := map[string]any{"limit": 100}
		if cursor != "" {
			params["cursor"] = cursor
		}
		if err := rpc.call("model/list", params, &result); err != nil {
			return nil, "", err
		}
		if result.Data == nil {
			return nil, "", catalogFormatError()
		}
		for _, model := range *result.Data {
			if !validModelID(model.Model, false) {
				return nil, "", catalogFormatError()
			}
			models = append(models, ModelInfo{ID: model.Model, DisplayName: model.DisplayName})
		}
		if result.NextCursor == nil || *result.NextCursor == "" {
			// config/read resolves active profile and global configuration. A missing
			// explicit model remains unknown rather than trusting model/list ordering.
			var config struct {
				Config struct {
					Model string `json:"model"`
				} `json:"config"`
			}
			if rpc.call("config/read", map[string]bool{"includeLayers": false}, &config) != nil {
				return models, "", nil
			}
			return models, config.Config.Model, nil
		}
		cursor = *result.NextCursor
		if seen[cursor] {
			return nil, "", catalogFormatError()
		}
		seen[cursor] = true
	}
	return nil, "", catalogFailure("harness.models_output_limit", "native Harness model catalog exceeded the page limit")
}
