package client

import (
	"net/http"
	"testing"

	"github.com/ferriskleier/delta/internal/config"
)

func TestNormalizeBaseURLUsesDefaultWhenEmpty(t *testing.T) {
	if got := NormalizeBaseURL(""); got != config.DefaultAPIAddress {
		t.Fatalf("NormalizeBaseURL(\"\") = %q, want %q", got, config.DefaultAPIAddress)
	}
	if got := New("", "token", nil).http; got != http.DefaultClient {
		t.Fatal("New should use the default HTTP transport when none is provided")
	}
}
