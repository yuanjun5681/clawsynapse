package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"clawsynapse/pkg/types"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	return &Client{
		baseURL: normalizeBaseURL(baseURL),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *Client) Get(ctx context.Context, endpoint string) (types.APIResult, error) {
	return c.do(ctx, http.MethodGet, endpoint, nil)
}

func (c *Client) Post(ctx context.Context, endpoint string, payload any) (types.APIResult, error) {
	return c.do(ctx, http.MethodPost, endpoint, payload)
}

func (c *Client) do(ctx context.Context, method, endpoint string, payload any) (types.APIResult, error) {
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
