package adapter

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"clawsynapse/internal/protocol"
	"clawsynapse/internal/store"
)

const (
	maxNativeHistoryBytes   = 64 << 20
	maxNativeHistoryEntries = 100000
)

var errNativeHistoryCapacity = errors.New("native history exceeds verification capacity")

type topicNativeRecord struct {
	id, parent, role, text, kind, finish string
	completed                            bool
	activeLeaf                           *string
	digest                               [sha256.Size]byte
}

type topicNativeHistory struct {
	entries      []topicNativeRecord
	byID         map[string]int
	leaf         string
	headerDigest [sha256.Size]byte
}

func (h *topicNativeHistory) digest(count int) string {
	sum := sha256.New()
	_, _ = sum.Write(h.headerDigest[:])
	for i := range count {
		_, _ = sum.Write(h.entries[i].digest[:])
	}
	return "sha256:" + hex.EncodeToString(sum.Sum(nil))
}

func topicInputDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (h *topicNativeHistory) descends(id, ancestor string) bool {
	for steps := 0; id != "" && steps <= len(h.entries); steps++ {
		if id == ancestor {
			return true
		}
		i, found := h.byID[id]
		if !found {
			return false
		}
		id = h.entries[i].parent
	}
	return ancestor == "" && id == ""
}

func (h *topicNativeHistory) append(record topicNativeRecord, raw json.RawMessage) error {
	if record.id == "" {
		return errors.New("missing native entry identity")
	}
	if len(h.entries) >= maxNativeHistoryEntries {
		return errNativeHistoryCapacity
	}
	if _, duplicate := h.byID[record.id]; duplicate {
		return errors.New("duplicate native entry identity")
	}
	if record.parent != "" {
		if _, exists := h.byID[record.parent]; !exists {
			return errors.New("native entry has an unknown parent")
		}
	}
	// Native records were decoded by the caller. Hash their actual persisted
	// representation without retaining/copying private assistant/tool content.
	record.digest = sha256.Sum256(raw)
	h.byID[record.id] = len(h.entries)
	h.entries = append(h.entries, record)
	h.leaf = record.id
	if record.activeLeaf != nil {
		h.leaf = *record.activeLeaf
		if h.leaf != "" {
			if _, exists := h.byID[h.leaf]; !exists {
				return errors.New("native active leaf is missing")
			}
		}
	}
	return nil
}

type topicInvocation struct {
	previous store.SessionState
	before   *topicNativeHistory
	after    *topicNativeHistory
}

func topicStateError(started bool, cause error) error {
	return &ExecutionError{Code: "session_changed", Stage: "topic_context", Indeterminate: started,
		Err: errors.New("native topic basis is missing, legacy, or changed; create a new conversation explicitly: " + cause.Error())}
}

// A native provider failure can precede inference but follow persisted user
// input. Only pre-launch validation/start errors may remain certain for topics.
func indeterminateTopicError(err error) error {
	if err == nil {
		return nil
	}
	var execution *ExecutionError
	if errors.As(err, &execution) {
		failure := *execution
		failure.Indeterminate = true
		return &failure
	}
	return &ExecutionError{Code: "native_failed", Stage: "execution", Indeterminate: true, Err: err}
}

func (a *cliAdapter) confirmOpenCodeTopicTurn(ctx context.Context, stream *nativeStream, invocation *topicInvocation) error {
	history, err := a.readOpenCodeTopicHistory(ctx, stream.sessionID)
	if err != nil {
		return topicHistoryError(true, err)
	}
	for i := len(history.entries) - 1; i >= 0; i-- {
		record := history.entries[i]
		if record.role != "assistant" {
			continue
		}
		if record.id != stream.messageID || !record.completed || record.finish != "stop" {
			return incompleteOpenCodeTurn()
		}
		invocation.after = history
		stream.completed = true
		if stream.finalOnly {
			stream.candidateReady = true
		}
		return nil
	}
	return incompleteOpenCodeTurn()
}

func topicHistoryError(started bool, err error) error {
	var execution *ExecutionError
	if errors.Is(err, errNativeHistoryCapacity) || errors.Is(err, bufio.ErrTooLong) || errors.As(err, &execution) && execution.Code == "output_limit" {
		return &ExecutionError{Code: "context_capacity", Stage: "topic_context", Indeterminate: started, Err: errNativeHistoryCapacity}
	}
	return topicStateError(started, errors.New("cannot inspect persisted native history"))
}

func (a *cliAdapter) prepareTopicInput(ctx context.Context, req DeliverMessageRequest, sessionID string) (*topicInvocation, error) {
	if req.TopicContext == nil {
		return nil, nil
	}
	if a.TopicContextIdentity() == "" {
		return nil, &ExecutionError{Code: "context_unsupported", Stage: "validation"}
	}
	if a.sessionStore == nil || req.SessionKey == "" || req.RequestID == "" || req.RunID == "" || req.TopicContext.Turn.NativeSessionEpoch == "" {
		return nil, topicStateError(false, errors.New("durable topic scope is required"))
	}
	previous, found, err := a.sessionStore.LoadSessionState(a.product, a.scopedSessionKey(req.SessionKey))
	if err != nil {
		return nil, topicStateError(false, errors.New("cannot load native mapping"))
	}
	execution := req.TopicContext
	if !found {
		if execution.RequireResume || execution.MaterializedThrough != 0 || sessionID != "" || execution.NativePreviousRequestID != "" {
			return nil, topicStateError(false, errors.New("verified native mapping is missing"))
		}
		return &topicInvocation{before: &topicNativeHistory{byID: make(map[string]int)}}, nil
	}
	basis := previous.Topic
	if basis == nil || basis.Epoch != execution.Turn.NativeSessionEpoch || basis.PolicyVersion != protocol.TopicPolicyVersion || basis.ThroughRevision != execution.MaterializedThrough || previous.SessionID != sessionID || basis.RequestID != execution.NativePreviousRequestID {
		return nil, topicStateError(false, errors.New("topic receipt continuity does not match"))
	}
	before, err := a.readTopicNativeHistory(ctx, sessionID)
	if err != nil {
		return nil, topicHistoryError(false, err)
	}
	if basis.EntryCount < 1 || basis.EntryCount > len(before.entries) || before.digest(basis.EntryCount) != basis.HistoryDigest || before.leaf != basis.LastEntryID {
		return nil, topicStateError(false, errors.New("native history or active leaf changed"))
	}
	// OMP may append off-branch token accounting after completion. It is not
	// model context and explicitly restores the same active leaf on replay.
	for _, record := range before.entries[basis.EntryCount:] {
		if a.product != "omp" || record.kind != "model_usage" || record.activeLeaf == nil || *record.activeLeaf != basis.LastEntryID {
			return nil, topicStateError(false, errors.New("unexpected native history appended"))
		}
	}
	index, exists := before.byID[basis.UserEntryID]
	if !exists || before.entries[index].role != "user" || before.entries[index].parent != basis.ParentEntryID || topicInputDigest(before.entries[index].text) != basis.InputDigest || !before.descends(before.leaf, basis.UserEntryID) {
		return nil, topicStateError(false, errors.New("previous native input is no longer on the active branch"))
	}
	return &topicInvocation{previous: previous, before: before}, nil
}

func (a *cliAdapter) confirmTopicInput(ctx context.Context, req DeliverMessageRequest, sessionID, prompt string, invocation *topicInvocation, assistantID string) (*TopicInputReceipt, error) {
	if !safeSessionID(sessionID) || invocation.previous.SessionID != "" && sessionID != invocation.previous.SessionID {
		return nil, topicStateError(true, errors.New("native session identity changed"))
	}
	after := invocation.after
	if after == nil {
		var err error
		after, err = a.readTopicNativeHistory(ctx, sessionID)
		if err != nil {
			return nil, topicHistoryError(true, err)
		}
	}
	before := invocation.before
	if len(after.entries) <= len(before.entries) || len(before.entries) != 0 && after.digest(len(before.entries)) != before.digest(len(before.entries)) {
		return nil, topicStateError(true, errors.New("native history did not extend the expected basis"))
	}
	var user *topicNativeRecord
	for i := len(before.entries); i < len(after.entries); i++ {
		record := &after.entries[i]
		if record.kind == "context_edit" || record.kind == "reset_boundary" {
			return nil, topicStateError(true, errors.New("native context was rewritten during execution"))
		}
		if record.role != "user" {
			continue
		}
		if user != nil || record.text != prompt || !after.descends(record.parent, before.leaf) {
			return nil, topicStateError(true, errors.New("persisted user input is ambiguous or off branch"))
		}
		user = record
	}
	if user == nil || !after.descends(after.leaf, user.id) {
		return nil, topicStateError(true, errors.New("persisted user input is missing from the active branch"))
	}
	// Completion and input persistence are separate evidence. Require the
	// authoritative persisted assistant after this exact user, not stdout alone.
	completed := false
	for i := len(after.entries) - 1; i >= 0; i-- {
		record := after.entries[i]
		if record.role == "assistant" && after.descends(after.leaf, record.id) {
			completed = record.completed && record.finish == "stop" && after.descends(record.id, user.id) && (a.product != "opencode" || record.id == assistantID)
			break
		}
	}
	if !completed {
		return nil, topicStateError(true, errors.New("persisted assistant does not confirm this input's completed turn"))
	}
	current, found, err := a.sessionStore.LoadSessionState(a.product, a.scopedSessionKey(req.SessionKey))
	if err != nil || found != (invocation.previous.SessionID != "") || found && !sameTopicMapping(current, invocation.previous) {
		return nil, topicStateError(true, errors.New("native mapping changed during execution"))
	}
	receipt := &TopicInputReceipt{NativeSessionID: sessionID, UserEntryID: user.id, ParentEntryID: user.parent,
		InputDigest: topicInputDigest(prompt), LastEntryID: after.leaf, ThroughRevision: req.TopicContext.Turn.TurnRevision}
	var input topicInput
	if json.Unmarshal([]byte(prompt), &input) != nil {
		return nil, topicStateError(true, errors.New("persisted topic manifest cannot be verified"))
	}
	if input.RequiredContext != nil {
		receipt.RequiredReadThrough = input.RequiredContext.ThroughRevision
	}
	now := time.Now().UnixMilli()
	created := invocation.previous.CreatedAtMs
	if created == 0 {
		created = now
	}
	state := store.SessionState{Adapter: a.product, SessionKey: a.scopedSessionKey(req.SessionKey), SessionID: sessionID, CreatedAtMs: created, UpdatedAtMs: now,
		Topic: &store.TopicSessionState{Epoch: req.TopicContext.Turn.NativeSessionEpoch, PolicyVersion: protocol.TopicPolicyVersion,
			RequestID: req.RequestID, RunID: req.RunID, UserEntryID: receipt.UserEntryID, ParentEntryID: receipt.ParentEntryID,
			InputDigest: receipt.InputDigest, LastEntryID: receipt.LastEntryID, ThroughRevision: receipt.ThroughRevision,
			RequiredReadThrough: receipt.RequiredReadThrough,
			EntryCount:          len(after.entries), HistoryDigest: after.digest(len(after.entries))}}
	if err := a.sessionStore.SaveSessionState(state); err != nil {
		return nil, &ExecutionError{Code: "session_store", Stage: "session_save", Indeterminate: true, Err: errors.New("cannot persist verified native topic receipt")}
	}
	return receipt, nil
}

func sameTopicMapping(left, right store.SessionState) bool {
	return left.SessionID == right.SessionID && left.Topic != nil && right.Topic != nil && *left.Topic == *right.Topic
}

func (a *cliAdapter) readTopicNativeHistory(ctx context.Context, id string) (*topicNativeHistory, error) {
	if a.isPiFamily() {
		return a.readPiTopicHistory(id)
	}
	if a.product == "opencode" {
		return a.readOpenCodeTopicHistory(ctx, id)
	}
	return nil, errors.New("native topic persistence is unsupported")
}

func (a *cliAdapter) readPiTopicHistory(id string) (*topicNativeHistory, error) {
	path, err := a.findPiSession(id)
	if err != nil {
		return nil, err
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() {
		return nil, errors.New("native history is unavailable")
	}
	if before.Size() > maxNativeHistoryBytes {
		return nil, errNativeHistoryCapacity
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, errors.New("native history changed while opening")
	}
	history := &topicNativeHistory{byID: make(map[string]int)}
	scanner := bufio.NewScanner(io.LimitReader(file, maxNativeHistoryBytes+1))
	scanner.Buffer(make([]byte, 4096), maxEventBytes)
	for scanner.Scan() {
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}
		var entry struct {
			Type         string          `json:"type"`
			ID           string          `json:"id"`
			ParentID     json.RawMessage `json:"parentId"`
			ActiveLeafID json.RawMessage `json:"activeLeafId"`
			Message      struct {
				Role       string          `json:"role"`
				Content    json.RawMessage `json:"content"`
				StopReason string          `json:"stopReason"`
			} `json:"message"`
		}
		if json.Unmarshal(raw, &entry) != nil {
			return nil, errors.New("invalid native session record")
		}
		if entry.Type == "session" {
			history.headerDigest = sha256.Sum256(raw)
			continue
		}
		if entry.Type == "title" {
			continue
		}
		record := topicNativeRecord{id: entry.ID, kind: entry.Type}
		if len(entry.ParentID) == 0 || json.Unmarshal(entry.ParentID, &record.parent) != nil {
			return nil, errors.New("native record has no verifiable parent")
		}
		if len(entry.ActiveLeafID) != 0 {
			var leaf string
			if json.Unmarshal(entry.ActiveLeafID, &leaf) != nil {
				return nil, errors.New("native record has an invalid active leaf")
			}
			record.activeLeaf = &leaf
		}
		if entry.Type == "message" {
			record.role, record.finish = entry.Message.Role, entry.Message.StopReason
			record.completed = record.finish == "stop"
			if record.role == "user" {
				if json.Unmarshal(entry.Message.Content, &record.text) != nil {
					var parts []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					}
					if json.Unmarshal(entry.Message.Content, &parts) != nil || len(parts) != 1 || parts[0].Type != "text" {
						return nil, errors.New("unsupported native user input content")
					}
					record.text = parts[0].Text
				}
			}
		}
		if err := history.append(record, raw); err != nil {
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(opened, after) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) {
		return nil, errors.New("native session changed during inspection")
	}
	if err := store.CheckPath(path); err != nil {
		return nil, err
	}
	return history, nil
}

func (a *cliAdapter) readOpenCodeTopicHistory(ctx context.Context, id string) (*topicNativeHistory, error) {
	// Unsanitized local export is necessary for an exact input digest. Never
	// publish or log it. The separate history budget permits long conversations
	// without relaxing execution output limits or silently truncating history.
	output, err := runProcessWithLimits(ctx, a.runCommand, []string{"session", "export", "--standalone", id}, "", nil, statusTimeout, maxNativeHistoryBytes+maxStderrBytes, maxNativeHistoryBytes)
	if err != nil {
		return nil, err
	}
	var state struct {
		Info struct {
			ID      string `json:"id"`
			Outcome string `json:"outcome"`
		} `json:"info"`
		Messages []json.RawMessage `json:"messages"`
	}
	if json.Unmarshal(output, &state) != nil || state.Info.ID != id || len(state.Messages) == 0 {
		return nil, errors.New("native export does not match the expected session")
	}
	history := &topicNativeHistory{byID: make(map[string]int)}
	for _, raw := range state.Messages {
		var message struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			Text    string `json:"text"`
			Finish  string `json:"finish"`
			Outcome string `json:"outcome"`
			Time    struct {
				Completed int64 `json:"completed"`
			} `json:"time"`
		}
		if json.Unmarshal(raw, &message) != nil {
			return nil, errors.New("invalid native exported message")
		}
		record := topicNativeRecord{id: message.ID, parent: history.leaf, kind: message.Type, text: message.Text,
			finish: message.Finish, completed: message.Time.Completed > 0}
		if message.Type == "user" || message.Type == "assistant" {
			record.role = message.Type
		}
		if err := history.append(record, raw); err != nil {
			return nil, err
		}
	}
	var final struct {
		Type    string `json:"type"`
		Outcome string `json:"outcome"`
	}
	if json.Unmarshal(state.Messages[len(state.Messages)-1], &final) != nil || final.Type != "idle" || final.Outcome != "succeeded" || state.Info.Outcome != "succeeded" {
		return nil, errors.New("native exported session is not settled successfully")
	}
	return history, nil
}
