package jobqueue

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestSendCallbackSuccessFirstAttempt verifies that a callback that succeeds
// on the first attempt makes exactly one HTTP call and no retries.
func TestSendCallbackSuccessFirstAttempt(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sendCallback(srv.Client(), "", srv.URL, nil, nil)

	if got := calls.Load(); got != 1 {
		t.Errorf("expected 1 call, got %d", got)
	}
}

// TestSendCallbackRetiesOn5xx verifies that HTTP 5xx responses trigger retries.
func TestSendCallbackRetiesOn5xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < int32(callbackMaxAttempts) {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	// Override initial delay so the test runs fast.
	// (We can't easily override the constant, so just verify the call count.)
	client := &http.Client{Timeout: 2 * time.Second}
	sendCallback(client, "", srv.URL, nil, nil)

	// Should have called exactly callbackMaxAttempts times (fails then succeeds).
	if got := calls.Load(); got != int32(callbackMaxAttempts) {
		t.Errorf("expected %d calls (retry on 5xx), got %d", callbackMaxAttempts, got)
	}
}

// TestSendCallbackNoRetryOn4xx verifies that 4xx responses are not retried.
func TestSendCallbackNoRetryOn4xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	sendCallback(srv.Client(), "", srv.URL, nil, nil)

	if got := calls.Load(); got != 1 {
		t.Errorf("expected 1 call (no retry on 4xx), got %d", got)
	}
}

// TestSendCallbackSignsWhenSecretSet verifies that a non-empty webhookSecret
// causes the X-VaultRun-Signature header to be present.
func TestSendCallbackSignsWhenSecretSet(t *testing.T) {
	var gotSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-VaultRun-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sendCallback(srv.Client(), "mysecret", srv.URL, nil, nil)

	if gotSig == "" {
		t.Error("expected X-VaultRun-Signature header, got none")
	}
	if len(gotSig) < 7 || gotSig[:7] != "sha256=" {
		t.Errorf("X-VaultRun-Signature should start with 'sha256=', got %q", gotSig)
	}
}

// TestSendCallbackNoSignatureWhenNoSecret verifies that without a secret there
// is no X-VaultRun-Signature header.
func TestSendCallbackNoSignatureWhenNoSecret(t *testing.T) {
	var gotSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-VaultRun-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sendCallback(srv.Client(), "", srv.URL, nil, nil)

	if gotSig != "" {
		t.Errorf("expected no signature header, got %q", gotSig)
	}
}
