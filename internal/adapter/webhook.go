package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"clawsynapse/internal/protocol"
)

type WebhookConfig struct {
	NodeID    string
	URL       string
	AuthToken string
	Logger    *slog.Logger
}

type WebhookAdapter struct {
	nodeID string
	url    string
	token  string
	log    *slog.Logger
	client *http.Client
}

func NewWebhookAdapter(cfg WebhookConfig) (*WebhookAdapter, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("webhook url is required")
	}
	return &WebhookAdapter{
		nodeID: cfg.NodeID,
		url:    cfg.URL,
		token:  strings.TrimSpace(cfg.AuthToken),
		log:    cfg.Logger,
		client: &http.Client{},
	}, nil
}

type webhookPayload struct {
	NodeID     string                  `json:"nodeId"`
	MessageID  string                  `json:"messageId"`
	RequestID  string                  `json:"requestId,omitempty"`
	RunID      string                  `json:"runId,omitempty"`
	Seq        uint64                  `json:"seq,omitempty"`
	Type       string                  `json:"type"`
	AgentID    string                  `json:"agentId,omitempty"`
	From       string                  `json:"from"`
	SessionKey string                  `json:"sessionKey,omitempty"`
	Message    string                  `json:"message"`
	Payload    protocol.MessagePayload `json:"payload"`
	Metadata   map[string]any          `json:"metadata,omitempty"`
}

type webhookResponse struct {
	Reply     string `json:"reply"`
	Message   string `json:"message"`
	RunID     string `json:"runId"`
	SessionID string `json:"sessionId"`
	Error     string `json:"error"`
	Accepted  *bool  `json:"accepted"`
	Success   *bool  `json:"success"`
	Completed *bool  `json:"completed"`
}

func (a *WebhookAdapter) DeliverMessage(ctx context.Context, req DeliverMessageRequest) (*DeliverMessageResult, error) {
	payload := webhookPayload{
		NodeID:     a.nodeID,
		MessageID:  req.MessageID,
		RequestID:  req.RequestID,
		RunID:      req.RunID,
		Seq:        req.Seq,
		Type:       req.Type,
		AgentID:    req.AgentID,
		From:       req.From,
		SessionKey: req.SessionKey,
		Message:    req.Message,
		Metadata:   req.Metadata,
		Payload:    req.Payload,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal webhook payload: %w", err)
	}

	if a.log != nil {
		a.log.Info("sending webhook",
			slog.String("url", a.url),
			slog.String("from", req.From),
			slog.String("type", req.Type),
		)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create webhook request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	a.authorize(httpReq)

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("webhook request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil {
		return nil, &ExecutionError{Code: "response_read", Stage: "webhook", Indeterminate: true}
	}
	if len(respBody) > 4096 {
		return nil, &ExecutionError{Code: "response_limit", Stage: "webhook", Indeterminate: true}
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		result := parseWebhookSuccess(respBody)
		if !result.Completed {
			result.Reply = ""
		}
		if result.Success && result.Completed && result.Reply != "" {
			emitter := &answerEmitter{emit: req.Emit}
			if err := emitter.add(result.Reply); err != nil {
				return nil, err
			}
			if err := emitter.flush(); err != nil {
				return nil, err
			}
			if req.Emit != nil {
				result.Reply = ""
			}
		}
		return result, nil
	}

	return &DeliverMessageResult{
		Success: false,
		Error:   fmt.Sprintf("webhook returned status %d", resp.StatusCode),
	}, nil
}

func parseWebhookSuccess(body []byte) *DeliverMessageResult {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return &DeliverMessageResult{
			Success:  true,
			Accepted: true,
		}
	}

	var reply string
	if err := json.Unmarshal(body, &reply); err == nil {
		return &DeliverMessageResult{
			Success:   true,
			Accepted:  true,
			Completed: true,
			Reply:     reply,
		}
	}

	var resp webhookResponse
	if err := json.Unmarshal(body, &resp); err == nil && hasStructuredWebhookReply(resp) {
		result := &DeliverMessageResult{
			Success:   true,
			Accepted:  true,
			RunID:     strings.TrimSpace(resp.RunID),
			SessionID: strings.TrimSpace(resp.SessionID),
			Reply:     firstStructuredWebhookText(resp.Reply, resp.Message),
			Error:     strings.TrimSpace(resp.Error),
		}
		if resp.Success != nil {
			result.Success = *resp.Success
		}
		if resp.Accepted != nil {
			result.Accepted = *resp.Accepted
		}
		result.Completed = result.Success && result.Reply != ""
		if resp.Completed != nil {
			result.Completed = *resp.Completed && result.Success
		}
		if result.Error != "" {
			result.Success = false
			result.Completed = false
		}
		return result
	}

	return &DeliverMessageResult{
		Success:   true,
		Accepted:  true,
		Completed: true,
		Reply:     text,
	}
}

func hasStructuredWebhookReply(resp webhookResponse) bool {
	return strings.TrimSpace(resp.Reply) != "" ||
		strings.TrimSpace(resp.Message) != "" ||
		strings.TrimSpace(resp.RunID) != "" ||
		strings.TrimSpace(resp.SessionID) != "" ||
		strings.TrimSpace(resp.Error) != "" ||
		resp.Accepted != nil ||
		resp.Success != nil || resp.Completed != nil
}

func firstStructuredWebhookText(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func (a *WebhookAdapter) GetStatus(ctx context.Context) (*AgentStatus, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.url, nil)
	if err != nil {
		return &AgentStatus{Healthy: false}, err
	}
	a.authorize(req)
	resp, err := a.client.Do(req)
	if err != nil {
		return &AgentStatus{Healthy: false}, err
	}
	resp.Body.Close()
	healthy := resp.StatusCode < 500 && resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden
	return &AgentStatus{Healthy: healthy}, nil
}

func (a *WebhookAdapter) authorize(req *http.Request) {
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
}
