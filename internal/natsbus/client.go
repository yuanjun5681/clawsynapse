package natsbus

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

type Client struct {
	mu   sync.RWMutex
	nc   *nats.Conn
	js   nats.JetStreamContext
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
	callbackMu       sync.Mutex
	callbacks        sync.WaitGroup
	quiescing        bool
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

type Options struct {
	CredentialsFile string
	TLSCAFile       string
	TLSCertFile     string
	TLSKeyFile      string
	TLSServerName   string
}

func Connect(ctx context.Context, servers []string, name string, options ...Options) (*Client, error) {
	if len(servers) == 0 {
		return nil, fmt.Errorf("empty nats servers")
	}

	serverURLs := strings.Join(servers, ",")
	c := &Client{url: redactServerURLs(serverURLs), name: name}
	if len(options) > 1 {
		return nil, fmt.Errorf("only one NATS options value is supported")
	}
	var security Options
	if len(options) == 1 {
		security = options[0]
	}
	securityOptions, err := connectionOptions(security)
	if err != nil {
		return nil, err
	}

	opts := []nats.Option{
		nats.Name(name),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
		// Core messages have no durable acceptance; never queue stale commands during reconnect.
		nats.ReconnectBufSize(0),
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
			c.lastConnectedURL = redactServerURLs(nc.ConnectedUrl())
		}),
		nats.ClosedHandler(func(_ *nats.Conn) {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.closed = true
		}),
	}
	opts = append(opts, securityOptions...)
	nc, err := nats.Connect(serverURLs, opts...)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.nc = nc
	c.connectedAt = time.Now().UnixMilli()
	c.lastConnectedURL = redactServerURLs(nc.ConnectedUrl())
	c.mu.Unlock()

	js, err := nc.JetStream()
	if err != nil {
		slog.Warn("jetstream not available, transfer disabled", slog.String("error", err.Error()))
	} else {
		c.mu.Lock()
		c.js = js
		c.mu.Unlock()
	}

	go func() {
		<-ctx.Done()
		c.Close()
	}()

	return c, nil
}

func connectionOptions(o Options) ([]nats.Option, error) {
	var opts []nats.Option
	if o.CredentialsFile != "" {
		opts = append(opts, nats.UserCredentials(o.CredentialsFile))
	}
	if (o.TLSCertFile == "") != (o.TLSKeyFile == "") {
		return nil, fmt.Errorf("NATS TLS client certificate and key must both be configured")
	}
	if o.TLSCAFile == "" && o.TLSCertFile == "" && o.TLSServerName == "" {
		return opts, nil
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: o.TLSServerName}
	if o.TLSCAFile != "" {
		data, err := os.ReadFile(o.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("read NATS CA file: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("NATS CA file contains no certificates")
		}
		tlsConfig.RootCAs = roots
	}
	if o.TLSCertFile != "" {
		cert, err := tls.LoadX509KeyPair(o.TLSCertFile, o.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load NATS TLS client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}
	opts = append(opts, nats.Secure(tlsConfig))
	return opts, nil
}

func redactServerURLs(raw string) string {
	parts := strings.Split(raw, ",")
	for i, part := range parts {
		u, err := url.Parse(part)
		if err != nil {
			parts[i] = "<invalid NATS URL>"
			continue
		}
		u.User = nil
		parts[i] = u.String()
	}
	return strings.Join(parts, ",")
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
	return c.Publish(subject, b)
}

func (c *Client) Publish(subject string, data []byte) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.nc == nil {
		return fmt.Errorf("nats client is closed")
	}
	if !c.nc.IsConnected() {
		return nats.ErrDisconnected
	}
	return c.nc.Publish(subject, data)
}

func (c *Client) Subscribe(subject string, handler func(subject string, data []byte)) (*nats.Subscription, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.nc == nil {
		return nil, fmt.Errorf("nats client is closed")
	}

	return c.nc.Subscribe(subject, func(msg *nats.Msg) {
		c.callbackMu.Lock()
		if c.quiescing {
			c.callbackMu.Unlock()
			return
		}
		c.callbacks.Add(1)
		c.callbackMu.Unlock()
		defer c.callbacks.Done()
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

func (c *Client) JetStream() nats.JetStreamContext {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.js
}

func (c *Client) HasJetStream() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.js != nil && c.nc != nil && c.nc.IsConnected()
}

func (c *Client) Conn() *nats.Conn {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.nc
}

// Quiesce rejects new control callbacks and joins all admitted callbacks without
// deleting subscriptions or durable consumers. The Bot keeps NATS open to depart.
func (c *Client) Quiesce() {
	c.callbackMu.Lock()
	c.quiescing = true
	c.callbackMu.Unlock()
	c.callbacks.Wait()
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
