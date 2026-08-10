package server

import (
	"testing"
	"time"
)

func TestSessionsExpireWhenIdleAndRefreshOnUse(t *testing.T) {
	sessions := newSessionState()
	start := time.Now()
	id, err := sessions.create(start)
	if err != nil {
		t.Fatal(err)
	}
	almost := start.Add(sessionIdleTTL - time.Minute)
	if !sessions.valid(id, almost) {
		t.Fatal("session expired before its idle TTL")
	}
	// The use at "almost" refreshed the clock, so the same absolute offset
	// past the original start must still be valid.
	if !sessions.valid(id, start.Add(sessionIdleTTL+time.Minute)) {
		t.Fatal("session use did not refresh the idle clock")
	}
	if sessions.valid(id, start.Add(3*sessionIdleTTL)) {
		t.Fatal("idle session never expired")
	}
	if sessions.valid(id, start.Add(3*sessionIdleTTL)) {
		t.Fatal("expired session validated on a second look")
	}
}

func TestSessionStoreEvictsTheStalestAtItsCap(t *testing.T) {
	sessions := newSessionState()
	start := time.Now()
	first, err := sessions.create(start)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < sessionLimit+3; i++ {
		if _, err := sessions.create(start.Add(time.Duration(i) * time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if len(sessions.lastSeen) > sessionLimit {
		t.Fatalf("session store holds %d entries, cap is %d", len(sessions.lastSeen), sessionLimit)
	}
	if sessions.valid(first, start.Add(time.Minute)) {
		t.Fatal("the stalest session survived cap eviction")
	}
}

func TestLoginThrottleBlocksAfterTheAllowanceAndResets(t *testing.T) {
	login := &loginState{}
	now := time.Now()
	for i := 0; i < loginFailureAllowance-1; i++ {
		login.fail(now)
		if ok, _ := login.allowed(now); !ok {
			t.Fatalf("blocked after %d failures, allowance is %d", i+1, loginFailureAllowance)
		}
	}
	login.fail(now)
	if ok, wait := login.allowed(now); ok || wait <= 0 {
		t.Fatalf("allowed = %v wait = %v after the full allowance, want a block", ok, wait)
	}
	if ok, _ := login.allowed(now.Add(loginFailureDelay + time.Second)); !ok {
		t.Fatal("block never lifted after its delay")
	}
	login.reset()
	if ok, _ := login.allowed(now); !ok {
		t.Fatal("reset did not clear the block")
	}
}
