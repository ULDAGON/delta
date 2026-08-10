package cli

import (
	"context"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ferriskleier/delta/internal/config"
	"github.com/ferriskleier/delta/internal/storage"
)

// LAN access hands the port to every device on the network, so a peer must not
// be able to hold a connection open without ever completing a request.
func TestServeDropsAConnectionThatStallsBeforeSendingItsHeaders(t *testing.T) {
	previous := serveReadHeaderTimeout
	serveReadHeaderTimeout = 250 * time.Millisecond
	t.Cleanup(func() { serveReadHeaderTimeout = previous })

	address, _ := serveLANDiary(t)
	conn := dialServe(t, address)
	// A request line with no terminating blank line: the headers never finish.
	if _, err := conn.Write([]byte("GET /api/health HTTP/1.1\r\n")); err != nil {
		t.Fatal(err)
	}
	assertServerClosed(t, conn, "read-header timeout")
}

func TestServeDropsAKeptAliveConnectionThatGoesIdle(t *testing.T) {
	previous := serveIdleTimeout
	serveIdleTimeout = 250 * time.Millisecond
	t.Cleanup(func() { serveIdleTimeout = previous })

	address, token := serveLANDiary(t)
	conn := dialServe(t, address)
	request := "GET /api/health HTTP/1.1\r\nHost: " + address + "\r\nAuthorization: Bearer " + token + "\r\n\r\n"
	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatal(err)
	}
	body := assertServerClosed(t, conn, "idle timeout")
	if !strings.Contains(body, "200 OK") {
		t.Fatalf("response = %q, want the health 200 before the idle close", body)
	}
}

func dialServe(t *testing.T, address string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// assertServerClosed reads until the server closes the connection and fails if
// the read deadline arrives first, which means the server never let go.
func assertServerClosed(t *testing.T, conn net.Conn, timeout string) string {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	read, err := io.ReadAll(conn)
	if deadline, ok := err.(net.Error); ok && deadline.Timeout() {
		t.Fatalf("the server still held the connection after its %s", timeout)
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(read)
}

// serveLANDiary runs `delta serve` with LAN access on a free loopback port and
// returns the host:port the listener answers on plus its bearer token.
func serveLANDiary(t *testing.T) (string, string) {
	t.Helper()
	t.Setenv(config.ConfigEnv, filepath.Join(t.TempDir(), "config.toml"))
	databasePath := filepath.Join(t.TempDir(), "diary.db")
	key := strings.Repeat("f2", storage.KeyBytes)
	store, err := storage.Open(context.Background(), databasePath, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	c, err := config.New(databasePath, key)
	if err != nil {
		t.Fatal(err)
	}
	c.Lan = true
	if err := config.Save(c); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	banner := make(chan string, 1)
	served := make(chan error, 1)
	go func() { served <- runServe(ctx, []string{"--listen", "127.0.0.1:0"}, bannerWriter{banner}) }()
	select {
	case <-banner:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("serve did not start")
	}
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-served:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("serve did not stop")
		}
	})
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimPrefix(loaded.APIAddress, "http://"), loaded.APIToken
}

type bannerWriter struct{ lines chan<- string }

func (w bannerWriter) Write(p []byte) (int, error) {
	select {
	case w.lines <- string(p):
	default:
	}
	return len(p), nil
}
