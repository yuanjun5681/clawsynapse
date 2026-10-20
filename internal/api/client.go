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
	baseURL    string
	httpClient *http.Client
	tokenPath  string
	configErr  error
}

type ClientOption func(*Client)

// WithTokenFile selects a private bearer token file; it never enables anonymous requests.
func WithTokenFile(path string) ClientOption {
	return func(c *Client) { c.tokenPath = path }
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
	cfg, err := config.LoadFromOS(nil)
	if err != nil {
		c.configErr = fmt.Errorf("load local API client configuration: %w", err)
	} else {
		c.tokenPath = cfg.APITokenPath
		if strings.TrimSpace(baseURL) == "" {
			c.baseURL = normalizeBaseURL(cfg.LocalAPIAddr)
		}
		if cfg.APITLSCertFile != "" && !strings.Contains(baseURL, "://") {
			c.baseURL = "https://" + strings.TrimPrefix(c.baseURL, "http://")
		}
	}
	for _, option := range options {
		option(c)
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
	token, err := readToken(c.tokenPath)
	if err != nil {
		return types.APIResult{}, err
	}
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return types.APIResult{}, fmt.Errorf("invalid local API URL")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && (strings.EqualFold(u.Hostname(), "localhost") || (ip != nil && ip.IsLoopback()))) {
		return types.APIResult{}, fmt.Errorf("non-loopback API client requires HTTPS")
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
	req.Header.Set("Authorization", "Bearer "+token)

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
