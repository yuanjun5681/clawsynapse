package natsbus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

const messagesStream = "CLAWSYNAPSE_MESSAGES"
const messagesSubject = "clawsynapse.msg.*.inbox"

// EnsureMessagingStream never rewrites an existing stream's subjects or purges
// another Bot's messages. Messaging requires durable broker acceptance.
func (c *Client) EnsureMessagingStream() error {
	js := c.JetStream()
	if js == nil {
		return errors.New("JetStream is required for messaging")
	}
	info, err := js.StreamInfo(messagesStream)
	if errors.Is(err, nats.ErrStreamNotFound) {
		info, err = js.AddStream(&nats.StreamConfig{Name: messagesStream, Subjects: []string{messagesSubject}, Retention: nats.LimitsPolicy, Storage: nats.FileStorage, MaxAge: 7 * 24 * time.Hour, Duplicates: 24 * time.Hour})
		if err != nil {
			// Concurrent Bots may race to create the same shared stream.
			info, err = js.StreamInfo(messagesStream)
		}
	}
	if err != nil {
		return fmt.Errorf("initialize durable messaging stream: %w", err)
	}
	for _, subject := range info.Config.Subjects {
		if subject == messagesSubject && info.Config.Storage == nats.FileStorage && info.Config.Retention == nats.LimitsPolicy {
			return nil
		}
	}
	return errors.New("messaging stream must preserve wildcard inboxes with file-backed limits retention")
}

func (c *Client) PublishMessage(ctx context.Context, subject string, data []byte, messageID string) error {
	js := c.JetStream()
	if js == nil {
		return errors.New("JetStream is required for messaging")
	}
	msg := &nats.Msg{Subject: subject, Data: data, Header: nats.Header{}}
	msg.Header.Set(nats.MsgIdHdr, messageID)
	_, err := js.PublishMsg(msg, nats.Context(ctx), nats.ExpectStream(messagesStream))
	return err
}

func (c *Client) MessagingInbox(botID string) (*nats.Subscription, error) {
	if err := c.EnsureMessagingStream(); err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(botID))
	durable := "node_" + hex.EncodeToString(digest[:16])
	return c.JetStream().PullSubscribe("clawsynapse.msg."+botID+".inbox", durable,
		nats.BindStream(messagesStream), nats.ManualAck(), nats.AckExplicit(),
		nats.AckWait(30*time.Second), nats.MaxAckPending(128), nats.DeliverAll())
}
