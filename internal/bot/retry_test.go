package bot

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/disgoorg/disgo/rest"
)

func restErrWithStatus(status int) *rest.Error {
	return &rest.Error{Response: &http.Response{StatusCode: status}}
}

func restErrWithCode(code rest.JSONErrorCode, status int) *rest.Error {
	return &rest.Error{Code: code, Response: &http.Response{StatusCode: status}}
}

func TestIsTransientRestError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"network error (no rest.Error)", errors.New("dial tcp: connection refused"), true},
		{"500 internal server error", restErrWithStatus(http.StatusInternalServerError), true},
		{"502 bad gateway", restErrWithStatus(http.StatusBadGateway), true},
		{"missing permissions (403)", restErrWithCode(rest.JSONErrorCodeLackPermissionsToPerformAction, http.StatusForbidden), false},
		{"unknown ban (404)", restErrWithCode(rest.JSONErrorCodeUnknownBan, http.StatusNotFound), false},
		{"rate limited (429, disgo already retried)", restErrWithStatus(http.StatusTooManyRequests), false},
		{"rest.Error without response", &rest.Error{Code: rest.JSONErrorCodeGeneral}, false},
	}
	for _, c := range cases {
		if got := isTransientRestError(c.err); got != c.want {
			t.Errorf("%s: isTransientRestError = %v, want %v", c.name, got, c.want)
		}
	}
}

func testBot() *Bot {
	return &Bot{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestRetryTransientSucceedsAfterTransientFailure(t *testing.T) {
	b := testBot()
	calls := 0
	err := b.retryTransient("op", 3, time.Millisecond, func() error {
		calls++
		if calls < 3 {
			return restErrWithStatus(http.StatusBadGateway)
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("err = %v, calls = %d; want nil, 3", err, calls)
	}
}

func TestRetryTransientGivesUpAfterAttempts(t *testing.T) {
	b := testBot()
	calls := 0
	fail := restErrWithStatus(http.StatusInternalServerError)
	err := b.retryTransient("op", 3, time.Millisecond, func() error {
		calls++
		return fail
	})
	if !errors.Is(err, fail) || calls != 3 {
		t.Fatalf("err = %v, calls = %d; want the final error after 3 calls", err, calls)
	}
}

func TestRetryTransientDoesNotRetryPermanentErrors(t *testing.T) {
	b := testBot()
	calls := 0
	fail := restErrWithCode(rest.JSONErrorCodeLackPermissionsToPerformAction, http.StatusForbidden)
	err := b.retryTransient("op", 3, time.Millisecond, func() error {
		calls++
		return fail
	})
	if !errors.Is(err, fail) || calls != 1 {
		t.Fatalf("err = %v, calls = %d; want the permanent error after 1 call", err, calls)
	}
}

func TestRetryTransientNoErrorNoRetry(t *testing.T) {
	b := testBot()
	calls := 0
	if err := b.retryTransient("op", 3, time.Millisecond, func() error {
		calls++
		return nil
	}); err != nil || calls != 1 {
		t.Fatalf("err = %v, calls = %d; want nil, 1", err, calls)
	}
}
