package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"

	"strings"
	"time"

	"clawsynapse/internal/config"

	"clawsynapse/pkg/types"
)

type Client struct {
	baseURL      string
	httpClient   *http.Client
	tokenPath    string
	configErr    error
	botID        string
	contextToken string
}

type ClientOption func(*Client)

// WithTokenFile selects an optional private Bot-scoped bearer token file.
func WithTokenFile(path string) ClientOption {
	return func(c *Client) { c.tokenPath = path }
}

// WithBot binds relative business requests to one explicit local Bot.
func WithBot(botID string) ClientOption {
	return func(c *Client) { c.botID = strings.TrimSpace(botID) }
}

// WithContextToken authenticates only the active invocation's context reader.
// It is not a management credential and is never sent to other endpoints.
func WithContextToken(token string) ClientOption {
	return func(c *Client) { c.contextToken = token }
}

func NewClient(baseURL string, timeout time.Duration, options ...ClientOption) *Client {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	c := &Client{
		baseURL: normalizeBaseURL(baseURL),
		httpClient: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return fmt.Errorf("local API redirects are not allowed")
			},
		},
	}
	for _, option := range options {
		option(c)
	}
	// Explicit endpoints do not depend on workstation daemon configuration.
	if strings.TrimSpace(baseURL) != "" && strings.Contains(baseURL, "://") {
		return c
	}
	cfg, err := config.LoadDaemon(nil)
	if err != nil {
		c.configErr = fmt.Errorf("load daemon client configuration: %w", err)
		return c
	}
	configuredURL := normalizeBaseURL(cfg.LocalAPIAddr)
	if cfg.APITLSCertFile != "" && !strings.Contains(cfg.LocalAPIAddr, "://") {
		configuredURL = "https://" + strings.TrimPrefix(configuredURL, "http://")
	}
	if strings.TrimSpace(baseURL) == "" || (!strings.Contains(baseURL, "://") && normalizeBaseURL(baseURL) == normalizeBaseURL(cfg.LocalAPIAddr)) {
		c.baseURL = configuredURL
	}
	return c
}

func (c *Client) Get(ctx context.Context, endpoint string) (types.APIResult, error) {
	return c.do(ctx, http.MethodGet, endpoint, nil)
}

func (c *Client) Post(ctx context.Context, endpoint string, payload any) (types.APIResult, error) {
	return c.do(ctx, http.MethodPost, endpoint, payload)
}

func (c *Client) Put(ctx context.Context, endpoint string, payload any) (types.APIResult, error) {
	return c.do(ctx, http.MethodPut, endpoint, payload)
}

func (c *Client) Delete(ctx context.Context, endpoint string) (types.APIResult, error) {
	return c.do(ctx, http.MethodDelete, endpoint, nil)
}

func (c *Client) do(ctx context.Context, method, endpoint string, payload any) (types.APIResult, error) {
	if c.configErr != nil {
		return types.APIResult{}, c.configErr
	}
	if !strings.HasPrefix(endpoint, "/") || strings.HasPrefix(endpoint, "/v1/") || strings.Contains(endpoint, "://") || strings.ContainsAny(endpoint, "\\#") {
		return types.APIResult{}, fmt.Errorf("invalid local API endpoint")
	}
	parsed, err := url.ParseRequestURI(endpoint)
	if err != nil || strings.Contains(parsed.Path, "..") || strings.Contains(parsed.Path, "//") {
		return types.APIResult{}, fmt.Errorf("invalid local API endpoint")
	}
	if !strings.HasPrefix(endpoint, "/v2/") {
		if c.botID == "" {
			return types.APIResult{}, fmt.Errorf("explicit Bot selection is required")
		}
		if strings.ContainsAny(c.botID, "/\\?#") {
			return types.APIResult{}, fmt.Errorf("invalid Bot ID")
		}
		endpoint = "/v2/bots/" + url.PathEscape(c.botID) + endpoint
	} else if c.botID != "" {
		prefix := "/v2/bots/" + url.PathEscape(c.botID)
		if parsed.Path != prefix && !strings.HasPrefix(parsed.Path, prefix+"/") {
			return types.APIResult{}, fmt.Errorf("request is outside selected Bot scope")
		}
	}
	token := ""
	if c.tokenPath != "" {
		var err error
		token, err = readToken(c.tokenPath)
		if err != nil {
			return types.APIResult{}, err
		}
	}
	u, err := url.Parse(c.baseURL)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return types.APIResult{}, fmt.Errorf("invalid local API URL")
	}
	ip := net.ParseIP(u.Hostname())
	if (u.Scheme != "http" && u.Scheme != "https") || !(strings.EqualFold(u.Hostname(), "localhost") || (ip != nil && ip.IsLoopback())) {
		return types.APIResult{}, fmt.Errorf("local API URL must use a loopback host")
	}
	var body *bytes.Reader
	if payload == nil {
		body = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(payload)
		if err != nil {
			return types.APIResult{}, fmt.Errorf("marshal request: %w", err)
		}
		body = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+endpoint, body)
	if err != nil {
		return types.APIResult{}, fmt.Errorf("build request: %w", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if c.contextToken != "" && method == http.MethodGet && contextReadEndpoint(parsed.Path) {
		req.Header.Set("X-ClawSynapse-Context-Token", c.contextToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return types.APIResult{}, fmt.Errorf("request local api: %w", err)
	}
	defer resp.Body.Close()

	var result types.APIResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return types.APIResult{}, fmt.Errorf("decode response: %w", err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		if strings.TrimSpace(result.Message) == "" {
			result.Message = resp.Status
		}
		return result, fmt.Errorf("local api error: %s", result.Message)
	}

	return result, nil
}

func normalizeBaseURL(baseURL string) string {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		trimmed = "127.0.0.1:18080"
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "http://" + trimmed
	}
	return strings.TrimRight(trimmed, "/")
}

func contextReadEndpoint(path string) bool {
	if rest, ok := strings.CutPrefix(path, "/v2/bots/"); ok {
		botID, operation, found := strings.Cut(rest, "/")
		if !found || botID == "" {
			return false
		}
		path = operation
	} else {
		path = strings.TrimPrefix(path, "/")
	}
	rest, ok := strings.CutPrefix(path, "requests/")
	if !ok {
		return false
	}
	requestID, operation, found := strings.Cut(rest, "/")
	return found && requestID != "" && operation == "context"
}
