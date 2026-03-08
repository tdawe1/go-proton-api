package proton

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"

	"github.com/go-resty/resty/v2"
)

func TestCatchDialErrorRetriesOnNilResponseNetworkError(t *testing.T) {
	err := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("boom")}

	if !catchDialError(nil, err) {
		t.Fatal("expected catchDialError to retry on nil response network error")
	}
}

func TestCatchDropErrorRetriesOnNilResponseNetworkError(t *testing.T) {
	err := &net.OpError{Op: "read", Net: "tcp", Err: errors.New("boom")}

	if !catchDropError(nil, err) {
		t.Fatal("expected catchDropError to retry on nil response network error")
	}
}

func TestCatchTooManyRequestsRetriesOnNilResponseNetworkError(t *testing.T) {
	err := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("boom")}

	if !catchTooManyRequests(nil, err) {
		t.Fatal("expected catchTooManyRequests to retry on nil response network error")
	}
}

func TestCatchTooManyRequestsRetriesOnAPIErrorStatusWithoutResponse(t *testing.T) {
	err := &APIError{Status: http.StatusTooManyRequests, Code: 9000, Message: "retry"}

	if !catchTooManyRequests(nil, err) {
		t.Fatal("expected catchTooManyRequests to retry on APIError status without response")
	}
}

func TestCatchDialErrorSkipsRetryOnNilResponseWithoutNetworkError(t *testing.T) {
	err := errors.New("boom")

	if catchDialError(nil, err) {
		t.Fatal("expected catchDialError to skip retry on non-network error")
	}
}

func TestCatchDropErrorSkipsRetryOnCanceledNetworkError(t *testing.T) {
	err := &net.OpError{Op: "read", Net: "tcp", Err: context.Canceled}

	if catchDropError(nil, err) {
		t.Fatal("expected catchDropError to skip retry when request is canceled")
	}
}

func TestCatchDropErrorRetriesOnDeadlineExceededNetworkError(t *testing.T) {
	err := &net.OpError{Op: "read", Net: "tcp", Err: context.DeadlineExceeded}

	if !catchDropError(nil, err) {
		t.Fatal("expected catchDropError to retry on deadline exceeded network error")
	}
}

func TestIsTransientTransportErrorSkipsPermanentURLError(t *testing.T) {
	err := &url.Error{Op: "Get", URL: "https://example.invalid", Err: errors.New("x509: certificate signed by unknown authority")}

	if isTransientTransportError(err) {
		t.Fatal("expected permanent URL error to be non-transient")
	}
}

func TestCatchDialErrorSkipsCanceledWhenRawResponseMissing(t *testing.T) {
	res := &resty.Response{}

	if catchDialError(res, context.Canceled) {
		t.Fatal("expected catchDialError to skip canceled request even when raw response is missing")
	}
}

func TestCatchDropErrorSkipsCanceledWhenRawResponseMissing(t *testing.T) {
	res := &resty.Response{}

	if catchDropError(res, context.Canceled) {
		t.Fatal("expected catchDropError to skip canceled request even when raw response is missing")
	}
}

func TestIsTransientTransportErrorSkipsNonTimeoutNetError(t *testing.T) {
	err := nonTimeoutNetError{msg: "permanent network failure"}

	if isTransientTransportError(err) {
		t.Fatal("expected non-timeout net error to be non-transient")
	}
}

func TestCatchTooManyRequestsRetriesOnHTTP429(t *testing.T) {
	res := newRetryTestResponse(context.Background(), http.StatusTooManyRequests)

	if !catchTooManyRequests(res, nil) {
		t.Fatal("expected catchTooManyRequests to retry on HTTP 429")
	}
}

func TestIsTransientTransportErrorSkipsNonTimeoutNetOpError(t *testing.T) {
	err := &net.OpError{Op: "dial", Net: "tcp", Err: nonTimeoutNetError{msg: "permanent"}}

	if isTransientTransportError(err) {
		t.Fatal("expected non-timeout net.OpError to be non-transient")
	}
}

func TestIsTransientTransportErrorSkipsPermanentURLErrorWrappedNetOpError(t *testing.T) {
	err := &url.Error{
		Op:  "Get",
		URL: "https://example.invalid",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: nonTimeoutNetError{msg: "permanent"}},
	}

	if isTransientTransportError(err) {
		t.Fatal("expected wrapped non-timeout net.OpError to be non-transient")
	}
}

func TestCatchersSkipRetryWhenDisabled(t *testing.T) {
	disabledCtx := withRetryDisabled(context.Background())
	res429 := newRetryTestResponse(disabledCtx, http.StatusTooManyRequests)

	if catchTooManyRequests(res429, nil) {
		t.Fatal("expected catchTooManyRequests to skip retry when disabled")
	}

	resMissingRaw := &resty.Response{Request: resty.New().R().SetContext(disabledCtx)}
	if catchDialError(resMissingRaw, &net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}) {
		t.Fatal("expected catchDialError to skip retry when disabled")
	}
	if catchDropError(resMissingRaw, &net.OpError{Op: "read", Net: "tcp", Err: context.DeadlineExceeded}) {
		t.Fatal("expected catchDropError to skip retry when disabled")
	}
}

func newRetryTestResponse(ctx context.Context, status int) *resty.Response {
	return &resty.Response{
		Request:     resty.New().R().SetContext(ctx),
		RawResponse: &http.Response{StatusCode: status},
	}
}

type nonTimeoutNetError struct {
	msg string
}

func (e nonTimeoutNetError) Error() string {
	return e.msg
}

func (nonTimeoutNetError) Timeout() bool {
	return false
}

func (nonTimeoutNetError) Temporary() bool {
	return false
}
