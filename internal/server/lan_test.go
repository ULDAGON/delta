package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ferriskleier/delta/internal/service"
	"github.com/ferriskleier/delta/internal/storage"
)

func TestHostAllowedAcceptsLocalNetworkLiteralsOnlyWithLANAccess(t *testing.T) {
	pinOnLinkPrefixes(t, "192.168.1.24/24", "10.7.0.9/16", "fe80::1c2b/64")
	tests := []struct {
		name string
		host string
		off  bool
		on   bool
	}{
		{name: "localhost", host: "localhost", off: true, on: true},
		{name: "localhost with port", host: "localhost:7331", off: true, on: true},
		{name: "loopback", host: "127.0.0.1", off: true, on: true},
		{name: "loopback with port", host: "127.0.0.1:7331", off: true, on: true},
		{name: "ipv6 loopback", host: "[::1]", off: true, on: true},
		{name: "ipv6 loopback with port", host: "[::1]:7331", off: true, on: true},
		{name: "private", host: "192.168.1.24", on: true},
		{name: "private with port", host: "192.168.1.24:7331", on: true},
		{name: "private ten", host: "10.7.0.5:7331", on: true},
		{name: "ipv6 link-local with port", host: "[fe80::1]:7331", on: true},
		{name: "private carrier off link", host: "172.16.4.9:7331"},
		{name: "private subnet off link", host: "192.168.9.4:7331"},
		{name: "vpn routed private ten", host: "10.140.120.3:7331"},
		{name: "vpn routed private ten without port", host: "10.140.120.3"},
		{name: "public", host: "203.0.113.7"},
		{name: "public with port", host: "203.0.113.7:7331"},
		{name: "ipv6 public with port", host: "[2001:db8::1]:7331"},
		{name: "attacker hostname", host: "evil.example.com"},
		{name: "attacker hostname with port", host: "evil.example.com:7331"},
		{name: "attacker hostname shaped like a private address", host: "192-168-1-24.evil.example.com:7331"},
		{name: "private with invalid port", host: "192.168.1.24:99999"},
		{name: "empty", host: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hostAllowed(tt.host, false); got != tt.off {
				t.Fatalf("hostAllowed(%q, false) = %v, want %v", tt.host, got, tt.off)
			}
			if got := hostAllowed(tt.host, true); got != tt.on {
				t.Fatalf("hostAllowed(%q, true) = %v, want %v", tt.host, got, tt.on)
			}
		})
	}
}

func TestLANAccessGuardsPeerAddressAndHost(t *testing.T) {
	pinOnLinkPrefixes(t, "192.168.1.24/24", "10.7.0.9/16", "fe80::1c2b/64")
	svc := service.New(newLANStore(t))
	token := strings.Repeat("d4", 32)
	tests := []struct {
		name       string
		lanAccess  bool
		host       string
		remoteAddr string
		want       int
	}{
		{name: "loopback peer without lan access", host: "127.0.0.1:7331", remoteAddr: "127.0.0.1:52111", want: http.StatusOK},
		{name: "public peer without lan access", host: "127.0.0.1:7331", remoteAddr: "203.0.113.7:52111", want: http.StatusOK},
		{name: "private host without lan access", host: "192.168.1.24:7331", remoteAddr: "192.168.1.9:52111", want: http.StatusBadRequest},
		{name: "private peer with lan access", lanAccess: true, host: "192.168.1.24:7331", remoteAddr: "192.168.1.9:52111", want: http.StatusOK},
		{name: "private ten peer with lan access", lanAccess: true, host: "10.7.0.9:7331", remoteAddr: "10.7.0.5:52111", want: http.StatusOK},
		{name: "loopback peer with lan access", lanAccess: true, host: "127.0.0.1:7331", remoteAddr: "127.0.0.1:52111", want: http.StatusOK},
		{name: "link-local peer with lan access", lanAccess: true, host: "[fe80::1]:7331", remoteAddr: "[fe80::2%en0]:52111", want: http.StatusOK},
		{name: "public peer with lan access", lanAccess: true, host: "192.168.1.24:7331", remoteAddr: "203.0.113.7:52111", want: http.StatusForbidden},
		{name: "default public peer with lan access", lanAccess: true, host: "192.168.1.24:7331", want: http.StatusForbidden},
		// A corporate VPN routes all of 10/8: those peers are private but not
		// on link, so they must be refused as peer and as Host.
		{name: "vpn peer with lan access", lanAccess: true, host: "192.168.1.24:7331", remoteAddr: "10.140.120.3:52111", want: http.StatusForbidden},
		{name: "off-link private peer with lan access", lanAccess: true, host: "192.168.1.24:7331", remoteAddr: "172.16.4.9:52111", want: http.StatusForbidden},
		{name: "vpn host with lan access", lanAccess: true, host: "10.140.120.3:7331", remoteAddr: "192.168.1.9:52111", want: http.StatusBadRequest},
		{name: "attacker host with lan access", lanAccess: true, host: "evil.example.com", remoteAddr: "192.168.1.9:52111", want: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewHandler(svc, token, WithLANAccess(tt.lanAccess))
			request := httptest.NewRequest(http.MethodGet, "/api/health", nil)
			request.Header.Set("Authorization", "Bearer "+token)
			request.Host = tt.host
			if tt.remoteAddr != "" {
				request.RemoteAddr = tt.remoteAddr
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != tt.want {
				t.Fatalf("status = %d, want %d (body %q)", recorder.Code, tt.want, recorder.Body.String())
			}
			if tt.want == http.StatusForbidden {
				if got := strings.TrimSpace(recorder.Body.String()); got != "forbidden" {
					t.Fatalf("body = %q, want plain-text %q", got, "forbidden")
				}
			}
		})
	}
}

func TestLANURLsAdvertiseOnlyPrivateIPv4AddressesOnLink(t *testing.T) {
	pinOnLinkPrefixes(t, "192.168.1.24/24", "10.7.0.9/16", "203.0.113.7/24", "fe80::1c2b/64")
	want := []string{"http://192.168.1.24:7331", "http://10.7.0.9:7331"}
	got := LANURLs("7331")
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("LANURLs(\"7331\") = %#v, want %#v", got, want)
	}
	if urls := LANURLs(""); urls == nil || len(urls) != 0 {
		t.Fatalf("LANURLs(\"\") = %#v, want an empty slice", urls)
	}
}

func TestLANURLsOnThisMachineStayPrivateIPv4(t *testing.T) {
	for _, value := range LANURLs("7331") {
		host := strings.TrimPrefix(value, "http://")
		if host == value {
			t.Fatalf("LAN URL %q is not an http URL", value)
		}
		name, port, err := net.SplitHostPort(host)
		if err != nil {
			t.Fatal(err)
		}
		if port != "7331" {
			t.Fatalf("LAN URL %q port = %q, want 7331", value, port)
		}
		ip := net.ParseIP(name)
		if ip == nil || ip.To4() == nil || !ip.IsPrivate() {
			t.Fatalf("LAN URL %q is not a private IPv4 address", value)
		}
	}
}

// The interface filter cannot be faked out, so it is checked against this
// machine: no address of a down, loopback, or tunnel interface may appear.
func TestReadOnLinkPrefixesSkipsDownLoopbackAndTunnelInterfaces(t *testing.T) {
	devices, err := net.Interfaces()
	if err != nil {
		t.Skipf("interfaces unavailable: %v", err)
	}
	excluded := map[string]string{}
	for _, device := range devices {
		if device.Flags&net.FlagUp != 0 && device.Flags&net.FlagLoopback == 0 && device.Flags&net.FlagPointToPoint == 0 {
			continue
		}
		addresses, err := device.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			excluded[address.String()] = device.Name
		}
	}
	for _, prefix := range readOnLinkPrefixes() {
		if name, ok := excluded[prefix.String()]; ok {
			t.Fatalf("on-link prefix %s belongs to excluded interface %s", prefix, name)
		}
	}
}

func TestOnLinkPrefixesCacheRefreshesAfterTheTTL(t *testing.T) {
	onLinkCache.mu.Lock()
	onLinkCache.prefixes, onLinkCache.read = nil, time.Time{}
	onLinkCache.mu.Unlock()

	cachedOnLinkPrefixes()
	first := onLinkCacheRead()
	if first.IsZero() {
		t.Fatal("cachedOnLinkPrefixes did not record a read time")
	}
	cachedOnLinkPrefixes()
	if again := onLinkCacheRead(); !again.Equal(first) {
		t.Fatalf("read time after a second call within the TTL = %v, want %v", again, first)
	}

	onLinkCache.mu.Lock()
	onLinkCache.read = time.Now().Add(-onLinkTTL - time.Second)
	onLinkCache.mu.Unlock()
	cachedOnLinkPrefixes()
	if refreshed := onLinkCacheRead(); !refreshed.After(first) {
		t.Fatalf("read time after the TTL = %v, want a refresh later than %v", refreshed, first)
	}
}

func onLinkCacheRead() time.Time {
	onLinkCache.mu.Lock()
	defer onLinkCache.mu.Unlock()
	return onLinkCache.read
}

// pinOnLinkPrefixes replaces the machine's own networks with a fixed set so the
// LAN matrices do not depend on where the tests run.
func pinOnLinkPrefixes(t *testing.T, cidrs ...string) {
	t.Helper()
	prefixes := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		ip, network, err := net.ParseCIDR(cidr)
		if err != nil {
			t.Fatal(err)
		}
		// net.Interface.Addrs reports the interface's own address with the
		// prefix mask, not the masked network address.
		network.IP = ip
		prefixes = append(prefixes, network)
	}
	previous := onLinkPrefixes
	onLinkPrefixes = func() []*net.IPNet { return prefixes }
	t.Cleanup(func() { onLinkPrefixes = previous })
}

func newLANStore(t *testing.T) *storage.Store {
	t.Helper()
	store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "diary.db"), strings.Repeat("a1", storage.KeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
