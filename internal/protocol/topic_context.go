package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"unicode/utf8"
)

const (
	TopicTurnSchema      = "clawsynapse.topic-turn/1"
	TopicSyncSchema      = "clawsynapse.topic-sync/1"
	TopicReceiptSchema   = "clawsynapse.topic-receipt/1"
	TopicPageSchema      = "clawsynapse.topic-page/1"
	TopicPolicyVersion   = "clawsynapse.topic-policy/1"
	TopicReaderMaxEvents = 100
	TopicReaderMaxBytes  = 128 << 10
)

// TopicEvent is forwarded public conversation data, never privileged policy.
// Field order and encoding/json escaping are part of the digest contract.
type TopicEvent struct {
	Revision  uint64 `json:"revision"`
	Kind      string `json:"kind"`
	MessageID string `json:"messageId"`
	Author    string `json:"author,omitempty"`
	BotID     string `json:"botId,omitempty"`
	RequestID string `json:"requestId,omitempty"`
	RunID     string `json:"runId,omitempty"`
	Text      string `json:"text"`
}

type TopicContextBatch struct {
	BaseRevision    uint64       `json:"baseRevision"`
	ThroughRevision uint64       `json:"throughRevision"`
	Digest          string       `json:"digest"`
	Events          []TopicEvent `json:"events"`
}

type TopicTurn struct {
	Schema             string            `json:"schema"`
	TurnID             string            `json:"turnId"`
	TurnRevision       uint64            `json:"turnRevision"`
	NativeSessionEpoch string            `json:"nativeSessionEpoch"`
	PreviousRequestID  string            `json:"previousRequestId,omitempty"`
	Context            TopicContextBatch `json:"context"`
}

type TopicSync struct {
	Schema          string       `json:"schema"`
	TurnID          string       `json:"turnId"`
	BaseRevision    uint64       `json:"baseRevision"`
	ThroughRevision uint64       `json:"throughRevision"`
	Digest          string       `json:"digest"`
	Events          []TopicEvent `json:"events"`
}

type TopicReceipt struct {
	Schema                string `json:"schema"`
	SyncMessageID         string `json:"syncMessageId"`
	StoredThroughRevision uint64 `json:"storedThroughRevision"`
	NativeSessionEpoch    string `json:"nativeSessionEpoch"`
	PolicyVersion         string `json:"policyVersion"`
	Error                 string `json:"error,omitempty"`
}

type TopicExecution struct {
	Turn                    TopicTurn
	Events                  []TopicEvent
	MaterializedThrough     uint64
	RequireResume           bool
	NativePreviousRequestID string
	ReaderAvailable         bool
	ReaderToken             string `json:"-"`
}

type TopicContextPage struct {
	Schema          string       `json:"schema"`
	ThroughRevision uint64       `json:"throughRevision"`
	Events          []TopicEvent `json:"events"`
	NextRevision    uint64       `json:"nextRevision"`
	Complete        bool         `json:"complete"`
}

func TopicEventsDigest(events []TopicEvent) string {
	if events == nil {
		events = []TopicEvent{}
	}
	data, _ := json.Marshal(events)
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

// TopicInputSchema detects business input without taking ownership of unrelated
// payload.input values. Unknown topic versions must not become ordinary chat.
func TopicInputSchema(input json.RawMessage) string {
	var object map[string]json.RawMessage
	if json.Unmarshal(input, &object) != nil {
		return ""
	}
	var schema string
	_ = json.Unmarshal(object["schema"], &schema)
	return schema
}

func IsTopicSchema(schema string) bool {
	return strings.HasPrefix(schema, "clawsynapse.topic-")
}

func ParseTopicTurn(input json.RawMessage) (TopicTurn, bool, error) {
	var turn TopicTurn
	schema := TopicInputSchema(input)
	if !IsTopicSchema(schema) {
		return turn, false, nil
	}
	if schema != TopicTurnSchema {
		return turn, true, errors.New("topic.unsupported_schema")
	}
	if err := DecodeStrict(input, &turn); err != nil {
		return turn, true, err
	}
	if !topicIdentifier(turn.TurnID) || !topicIdentifier(turn.NativeSessionEpoch) || (turn.PreviousRequestID != "" && !topicIdentifier(turn.PreviousRequestID)) {
		return turn, true, errors.New("topic.invalid_turn_identity")
	}
	if turn.Context.ThroughRevision == math.MaxUint64 || turn.TurnRevision != turn.Context.ThroughRevision+1 {
		return turn, true, errors.New("topic.invalid_turn_revision")
	}
	if err := ValidateTopicBatch(turn.Context); err != nil {
		return turn, true, err
	}
	return turn, true, nil
}

func ParseTopicSync(input json.RawMessage) (TopicSync, error) {
	var sync TopicSync
	if err := DecodeStrict(input, &sync); err != nil {
		return sync, err
	}
	return sync, ValidateTopicSync(sync)
}

func ValidateTopicSync(sync TopicSync) error {
	if sync.Schema != TopicSyncSchema {
		return errors.New("topic.unsupported_schema")
	}
	if !topicIdentifier(sync.TurnID) {
		return errors.New("topic.invalid_turn_identity")
	}
	return ValidateTopicBatch(TopicContextBatch{BaseRevision: sync.BaseRevision, ThroughRevision: sync.ThroughRevision, Digest: sync.Digest, Events: sync.Events})
}

func ValidateTopicBatch(batch TopicContextBatch) error {
	if batch.Events == nil || batch.ThroughRevision < batch.BaseRevision || batch.ThroughRevision-batch.BaseRevision != uint64(len(batch.Events)) {
		return errors.New("topic.invalid_range")
	}
	seen := make(map[[5]string]bool, len(batch.Events))
	for i, event := range batch.Events {
		if event.Revision != batch.BaseRevision+uint64(i)+1 || !topicIdentifier(event.MessageID) || !utf8.ValidString(event.Text) {
			return errors.New("topic.invalid_event")
		}
		key := [5]string{event.Kind, event.MessageID, event.BotID, event.RequestID, event.RunID}
		if event.Kind == "bot.reply.completed" {
			key[1] = ""
		}
		if seen[key] {
			return errors.New("topic.event_identity_conflict")
		}
		seen[key] = true
		switch event.Kind {
		case "user.message":
			if !topicIdentifier(event.Author) || event.BotID != "" || event.RequestID != "" || event.RunID != "" {
				return errors.New("topic.invalid_user_origin")
			}
		case "bot.reply.completed":
			if event.Author != "" || !topicIdentifier(event.BotID) || !topicIdentifier(event.RequestID) || !topicIdentifier(event.RunID) {
				return errors.New("topic.invalid_reply_origin")
			}
		default:
			return errors.New("topic.unsupported_event")
		}
		encoded, err := json.Marshal(event)
		if err != nil || len(encoded) > TopicReaderMaxBytes-1024 {
			return errors.New("topic.event_exceeds_reader_limit")
		}
	}
	if batch.Digest != TopicEventsDigest(batch.Events) {
		return errors.New("topic.digest_mismatch")
	}
	return nil
}

func topicIdentifier(value string) bool {
	if value == "" || len(value) > 256 || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r <= ' ' || r == 127 {
			return false
		}
	}
	return true
}
