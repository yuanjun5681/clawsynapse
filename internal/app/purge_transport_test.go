package app

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"clawsynapse/internal/config"
	"clawsynapse/internal/natsbus"

	"github.com/nats-io/nkeys"
)

type purgeNATSConnect struct {
	JWT       string `json:"jwt"`
	Signature string `json:"sig"`
}

// This protocol peer exercises the real nats.go connection, nonce credentials,
// request/reply consumer deletion and flush boundary without an external server.
func purgeNATSPeer(t *testing.T, closeOnDeparture bool) (string, <-chan purgeNATSConnect, <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	connected := make(chan purgeNATSConnect, 1)
	published := make(chan string, 8)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.WriteString(conn, "INFO {\"server_id\":\"purge-test\",\"version\":\"2.10.0\",\"proto\":1,\"max_payload\":1048576,\"nonce\":\"purge-nonce\"}\r\n")
		reader := bufio.NewReader(conn)
		sid := ""
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			switch fields[0] {
			case "CONNECT":
				var auth purgeNATSConnect
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "CONNECT ")), &auth); err != nil {
					t.Error(err)
					return
				}
				connected <- auth
			case "PING":
				_, _ = io.WriteString(conn, "PONG\r\n")
			case "SUB":
				sid = fields[len(fields)-1]
			case "PUB":
				size, err := strconv.Atoi(fields[len(fields)-1])
				if err != nil {
					t.Error(err)
					return
				}
				if _, err := io.CopyN(io.Discard, reader, int64(size+2)); err != nil {
					return
				}
				published <- fields[1]
				if closeOnDeparture && fields[1] == "clawsynapse.discovery.global.depart" {
					return
				}
				if fields[1] == "purge.control" {
					_, _ = fmt.Fprintf(conn, "MSG purge.control %s 2\r\n{}\r\n", sid)
				}
				if len(fields) == 4 {
					response := `{"type":"io.nats.jetstream.api.v1.consumer_delete_response","success":true}`
					_, _ = fmt.Fprintf(conn, "MSG %s %s %d\r\n%s\r\n", fields[2], sid, len(response), response)
				}
			}
		}
	}()
	return "nats://" + listener.Addr().String(), connected, published
}

func TestDeleteConsumerAuthenticatesWithBotCredentials(t *testing.T) {
	server, connected, published := purgeNATSPeer(t, false)
	key, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	seed, err := key.Seed()
	if err != nil {
		t.Fatal(err)
	}
	credentials := filepath.Join(t.TempDir(), "bot.creds")
	data := fmt.Sprintf("-----BEGIN NATS USER JWT-----\nbot-specific-jwt\n------END NATS USER JWT------\n\n-----BEGIN USER NKEY SEED-----\n%s\n------END USER NKEY SEED------\n", seed)
	if err := os.WriteFile(credentials, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	d := config.DefaultDaemonConfig()
	d.NATSServers = []string{server}
	d.NATSCredentialsFile = filepath.Join(t.TempDir(), "unusable-daemon.creds")
	b := config.DefaultBotConfig()
	b.NATSCredentialsFile = credentials
	if err := deleteMessagingConsumer(d, b, "n1-test"); err != nil {
		t.Fatalf("Bot-authenticated durable deletion failed: %v", err)
	}
	auth := <-connected
	if auth.JWT != "bot-specific-jwt" {
		t.Fatalf("consumer cleanup used wrong JWT: %q", auth.JWT)
	}
	signature, err := base64.RawURLEncoding.DecodeString(auth.Signature)
	if err != nil || key.Verify([]byte("purge-nonce"), signature) != nil {
		t.Fatalf("consumer cleanup did not sign nonce with Bot credentials: %v", err)
	}
	if subject := <-published; !strings.HasPrefix(subject, "$JS.API.CONSUMER.DELETE.CLAWSYNAPSE_MESSAGES.node_") {
		t.Fatalf("cleanup touched wrong broker resource: %s", subject)
	}
}

func TestDeleteConsumerUsesBotTLSOverrides(t *testing.T) {
	d := config.DefaultDaemonConfig()
	d.NATSServers = []string{"nats://127.0.0.1:1"}
	d.NATSTLSCAFile = filepath.Join(t.TempDir(), "daemon-ca.pem")
	b := config.DefaultBotConfig()
	b.NATSTLSCAFile = filepath.Join(t.TempDir(), "bot-ca.pem")
	if err := deleteMessagingConsumer(d, b, "n1-test"); err == nil || !strings.Contains(err.Error(), b.NATSTLSCAFile) {
		t.Fatalf("consumer cleanup did not use Bot TLS context: %v", err)
	}
}

func TestLivePurgeReportsUnconfirmedFlush(t *testing.T) {
	a := testDaemon(t)
	a.manager.cfg.NATSServers = nil
	id := newTestBot(t, a, "flush-failure")
	_, r := purgeTestRuntime(t, a, id)
	server, _, published := purgeNATSPeer(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus, err := natsbus.Connect(ctx, []string{server}, "purge-flush-test")
	if err != nil {
		t.Fatal(err)
	}
	r.bus = bus
	defer bus.Close()
	result, err := a.manager.Purge(id)
	if err != nil {
		t.Fatal(err)
	}
	if subject := <-published; subject != "clawsynapse.discovery.global.depart" {
		t.Fatalf("deletion departure not published: %s", subject)
	}
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "NATS flush did not confirm") {
			return
		}
	}
	t.Fatalf("live purge lost flush failure warning: %v", result.Warnings)
}
