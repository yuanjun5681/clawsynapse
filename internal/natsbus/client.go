package natsbus

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

type Client struct {
	mu   sync.RWMutex
	nc   *nats.Conn
	url  string
	name string

	connectedAt      int64
	lastDisconnectAt int64
	lastReconnectAt  int64
	disconnects      int64
	reconnects       int64
	lastError        string
	lastConnectedURL string
	closed           bool
}

type Status struct {
	Name             string `json:"name"`
	ServerURL        string `json:"serverUrl"`
	Connected        bool   `json:"connected"`
	Status           string `json:"status"`
	ConnectedAt      int64  `json:"connectedAt,omitempty"`
	LastDisconnectAt int64  `json:"lastDisconnectAt,omitempty"`
	LastReconnectAt  int64  `json:"lastReconnectAt,omitempty"`
	Disconnects      int64  `json:"disconnects"`
	Reconnects       int64  `json:"reconnects"`
	LastError        string `json:"lastError,omitempty"`
	InMsgs           uint64 `json:"inMsgs"`
	OutMsgs          uint64 `json:"outMsgs"`
	InBytes          uint64 `json:"inBytes"`
	OutBytes         uint64 `json:"outBytes"`
}

func Connect(ctx context.Context, servers []string, name string) (*Client, error) {
	if len(servers) == 0 {
		return nil, fmt.Errorf("empty nats servers")
	}

	url := strings.Join(servers, ",")
	c := &Client{url: url, name: name}

	nc, err := nats.Connect(url,
		nats.Name(name),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.disconnects++
			c.lastDisconnectAt = time.Now().UnixMilli()
			if err != nil {
				c.lastError = err.Error()
			}
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.reconnects++
			c.lastReconnectAt = time.Now().UnixMilli()
			c.lastConnectedURL = nc.ConnectedUrl()
		}),
		nats.ClosedHandler(func(_ *nats.Conn) {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.closed = true
		}),
	)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.nc = nc
	c.connectedAt = time.Now().UnixMilli()
	c.lastConnectedURL = nc.ConnectedUrl()
	c.mu.Unlock()

	go func() {
		<-ctx.Done()
		c.Close()
	}()

	return c, nil
}

func (c *Client) Status() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()

	st := Status{
		Name:             c.name,
		ServerURL:        c.url,
		ConnectedAt:      c.connectedAt,
		LastDisconnectAt: c.lastDisconnectAt,
		LastReconnectAt:  c.lastReconnectAt,
		Disconnects:      c.disconnects,
		Reconnects:       c.reconnects,
		LastError:        c.lastError,
	}

	if c.nc == nil || c.closed {
		st.Connected = false
		st.Status = "closed"
		return st
	}

	stats := c.nc.Stats()
	st.InMsgs = stats.InMsgs
	st.OutMsgs = stats.OutMsgs
	st.InBytes = stats.InBytes
	st.OutBytes = stats.OutBytes

	st.ServerURL = c.lastConnectedURL
	status := c.nc.Status().String()
	st.Status = status
	st.Connected = c.nc.IsConnected()
	return st
}

func (c *Client) PublishJSON(subject string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.nc == nil {
		return fmt.Errorf("nats client is closed")
	}
	return c.nc.Publish(subject, b)
}

func (c *Client) Subscribe(subject string, handler func(subject string, data []byte)) (*nats.Subscription, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.nc == nil {
		return nil, fmt.Errorf("nats client is closed")
	}

	return c.nc.Subscribe(subject, func(msg *nats.Msg) {
		handler(msg.Subject, msg.Data)
	})
}

func (c *Client) FlushTimeout(timeout time.Duration) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.nc == nil {
		return fmt.Errorf("nats client is closed")
	}
	return c.nc.FlushTimeout(timeout)
}

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.nc != nil {
		c.nc.Close()
		c.nc = nil
	}
	c.closed = true
}
