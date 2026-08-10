package cli_test

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ferriskleier/delta/internal/cli"
	"github.com/ferriskleier/delta/internal/config"
	"github.com/ferriskleier/delta/internal/server"
	"github.com/ferriskleier/delta/internal/storage"
)

func TestServeWithLanBindsEveryInterfaceAndKeepsALoopbackAPIAddress(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv(config.ConfigEnv, configPath)
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
	defer cancel()
	lines := make(chan string, 1)
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- cli.Run(ctx, []string{"serve", "--listen", "127.0.0.1:0"}, strings.NewReader(""), lineWriter{lines}, ioDiscard{})
	}()
	var line string
	select {
	case line = <-lines:
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not start")
	}
	if !strings.HasPrefix(line, "delta "+cli.Version+" serving "+databasePath+" on http://127.0.0.1:") {
		t.Fatalf("serve banner = %q", line)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(loaded.APIAddress, "http://127.0.0.1:") {
		t.Fatalf("persisted API address = %q, want a loopback URL", loaded.APIAddress)
	}
	port := loaded.APIAddress[strings.LastIndex(loaded.APIAddress, ":")+1:]
	lanURLs := server.LANURLs(port)
	for _, url := range lanURLs {
		if !strings.Contains(line, url) {
			t.Fatalf("serve banner = %q, missing LAN URL %q", line, url)
		}
	}
	// A "tcp" listener on 0.0.0.0 is dual-stack and would also answer on every
	// IPv6 address of this machine, including globally routable ones.
	if conn, err := net.DialTimeout("tcp6", net.JoinHostPort("::1", port), 2*time.Second); err == nil {
		conn.Close()
		t.Fatalf("LAN listener answered on [::1]:%s, want an IPv4-only socket", port)
	}

	// Only the loopback address is exercised here: a sandboxed test process
	// cannot reliably connect to this machine's own LAN address.
	request, err := http.NewRequest(http.MethodGet, loaded.APIAddress+"/api/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+c.APIToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status on %s = %d, want 200", loaded.APIAddress, response.StatusCode)
	}

	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop")
	}
}
