package proton_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/rclone/go-proton-api"
	"github.com/stretchr/testify/require"
)

func TestUploadBlockRetriesWithSeekableReader(t *testing.T) {
	var calls int
	var payloads [][]byte

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		payloads = append(payloads, mustReadBlockPayload(t, r))

		w.Header().Set("Content-Type", "application/json")

		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"Code":9000,"Error":"boom"}`))
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"Code":1000}`))
	}))
	defer ts.Close()

	m := proton.New(
		proton.WithHostURL(ts.URL),
		proton.WithRetryCount(3),
	)
	defer m.Close()

	c := m.NewClient("", "", "")
	defer c.Close()

	err := c.UploadBlock(context.Background(), ts.URL, "token", bytes.NewReader([]byte("payload")))
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.Len(t, payloads, 2)
	require.Equal(t, []byte("payload"), payloads[0])
	require.Equal(t, []byte("payload"), payloads[1])
}

func TestUploadBlockRetriesOnTooManyRequests(t *testing.T) {
	var calls int

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")

		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"Code":9000,"Error":"boom"}`))
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"Code":1000}`))
	}))
	defer ts.Close()

	m := proton.New(
		proton.WithHostURL(ts.URL),
		proton.WithRetryCount(3),
	)
	defer m.Close()

	c := m.NewClient("", "", "")
	defer c.Close()

	err := c.UploadBlock(context.Background(), ts.URL, "token", bytes.NewReader([]byte("payload")))
	require.NoError(t, err)
	require.Equal(t, 2, calls)
}

func TestUploadBlockHonorsRetryAfterHeader(t *testing.T) {
	callTimes := make([]time.Time, 0, 2)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callTimes = append(callTimes, time.Now())
		w.Header().Set("Content-Type", "application/json")

		if len(callTimes) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"Code":9000,"Error":"boom"}`))
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"Code":1000}`))
	}))
	defer ts.Close()

	m := proton.New(
		proton.WithHostURL(ts.URL),
		proton.WithRetryCount(1),
	)
	defer m.Close()

	c := m.NewClient("", "", "")
	defer c.Close()

	err := c.UploadBlock(context.Background(), ts.URL, "token", bytes.NewReader([]byte("payload")))
	require.NoError(t, err)
	require.Len(t, callTimes, 2)
	require.GreaterOrEqual(t, callTimes[1].Sub(callTimes[0]), 800*time.Millisecond)
}

func TestUploadBlockHonorsRetryAfterHTTPDate(t *testing.T) {
	callTimes := make([]time.Time, 0, 2)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestTime := time.Now()
		callTimes = append(callTimes, requestTime)
		w.Header().Set("Content-Type", "application/json")

		if len(callTimes) == 1 {
			w.Header().Set("Retry-After", requestTime.UTC().Add(2*time.Second).Format(http.TimeFormat))
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"Code":9000,"Error":"boom"}`))
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"Code":1000}`))
	}))
	defer ts.Close()

	m := proton.New(
		proton.WithHostURL(ts.URL),
		proton.WithRetryCount(1),
	)
	defer m.Close()

	c := m.NewClient("", "", "")
	defer c.Close()

	err := c.UploadBlock(context.Background(), ts.URL, "token", bytes.NewReader([]byte("payload")))
	require.NoError(t, err)
	require.Len(t, callTimes, 2)
	require.GreaterOrEqual(t, callTimes[1].Sub(callTimes[0]), 900*time.Millisecond)
}

func TestUploadBlockHonorsConfiguredRetryCount(t *testing.T) {
	retryCount := 2
	var calls int

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"Code":9000,"Error":"boom"}`))
	}))
	defer ts.Close()

	m := proton.New(
		proton.WithHostURL(ts.URL),
		proton.WithRetryCount(retryCount),
	)
	defer m.Close()

	c := m.NewClient("", "", "")
	defer c.Close()

	err := c.UploadBlock(context.Background(), ts.URL, "token", bytes.NewReader([]byte("payload")))
	require.Error(t, err)
	require.Equal(t, retryCount+1, calls)
}

func TestUploadBlockDoesNotRetryOnNonTransientAPIError(t *testing.T) {
	var calls int

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"Code":9000,"Error":"boom"}`))
	}))
	defer ts.Close()

	m := proton.New(
		proton.WithHostURL(ts.URL),
		proton.WithRetryCount(3),
	)
	defer m.Close()

	c := m.NewClient("", "", "")
	defer c.Close()

	err := c.UploadBlock(context.Background(), ts.URL, "token", bytes.NewReader([]byte("payload")))
	require.Error(t, err)
	require.Equal(t, 1, calls)
}

func TestUploadBlockStopsRetryForNonSeekableReader(t *testing.T) {
	var calls int

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"Code":9000,"Error":"boom"}`))
	}))
	defer ts.Close()

	m := proton.New(
		proton.WithHostURL(ts.URL),
		proton.WithRetryCount(3),
	)
	defer m.Close()

	c := m.NewClient("", "", "")
	defer c.Close()

	err := c.UploadBlock(context.Background(), ts.URL, "token", bytes.NewBufferString("payload"))
	require.Error(t, err)
	require.Equal(t, 1, calls)
}

func TestUploadBlockReturnsSeekError(t *testing.T) {
	var calls int
	seekErr := errors.New("seek failed")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"Code":9000,"Error":"boom"}`))
	}))
	defer ts.Close()

	m := proton.New(
		proton.WithHostURL(ts.URL),
		proton.WithRetryCount(3),
	)
	defer m.Close()

	c := m.NewClient("", "", "")
	defer c.Close()

	err := c.UploadBlock(context.Background(), ts.URL, "token", &failingSeekReader{Reader: bytes.NewReader([]byte("payload")), err: seekErr})
	require.ErrorIs(t, err, seekErr)
	require.Equal(t, 1, calls)
}

func TestUploadBlockRetriesOnTransportError(t *testing.T) {
	retryCount := 3
	calls := 0
	transportErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("network down")}

	m := proton.New(
		proton.WithHostURL("https://example.invalid"),
		proton.WithRetryCount(retryCount),
		proton.WithTransport(roundTripperFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, transportErr
		})),
	)
	defer m.Close()

	c := m.NewClient("", "", "")
	defer c.Close()

	err := c.UploadBlock(context.Background(), "https://storage.proton.invalid/storage/blocks", "token", bytes.NewReader([]byte("payload")))
	require.Error(t, err)
	require.ErrorIs(t, err, transportErr)
	require.Equal(t, retryCount+1, calls)
}

func TestUploadBlockSkipsRetryOnPermanentWrappedURLError(t *testing.T) {
	retryCount := 3
	calls := 0
	transportErr := &url.Error{
		Op:  "Post",
		URL: "https://storage.proton.invalid/storage/blocks",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: nonTimeoutNetError{msg: "permanent"}},
	}

	m := proton.New(
		proton.WithHostURL("https://example.invalid"),
		proton.WithRetryCount(retryCount),
		proton.WithTransport(roundTripperFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, transportErr
		})),
	)
	defer m.Close()

	c := m.NewClient("", "", "")
	defer c.Close()

	err := c.UploadBlock(context.Background(), "https://storage.proton.invalid/storage/blocks", "token", bytes.NewReader([]byte("payload")))
	require.Error(t, err)
	require.ErrorIs(t, err, transportErr)
	require.Equal(t, 1, calls)
}

func TestUploadBlockRefreshesAuthOnUnauthorized(t *testing.T) {
	uploadCalls := 0
	refreshCalls := 0

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/upload":
			uploadCalls++
			w.Header().Set("Content-Type", "application/json")

			if uploadCalls == 1 {
				require.Equal(t, "Bearer expired-access-token", r.Header.Get("Authorization"))
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"Code":10013,"Error":"token expired"}`))
				return
			}

			require.Equal(t, "Bearer refreshed-access-token", r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"Code":1000}`))
		case "/auth/v4/refresh":
			refreshCalls++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"UID":"uid","AccessToken":"refreshed-access-token","RefreshToken":"refreshed-refresh-token"}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer ts.Close()

	m := proton.New(
		proton.WithHostURL(ts.URL),
		proton.WithRetryCount(0),
	)
	defer m.Close()

	c := m.NewClient("uid", "expired-access-token", "refresh-token")
	defer c.Close()

	err := c.UploadBlock(context.Background(), ts.URL+"/upload", "storage-token", bytes.NewReader([]byte("payload")))
	require.NoError(t, err)
	require.Equal(t, 2, uploadCalls)
	require.Equal(t, 1, refreshCalls)
}

func mustReadBlockPayload(t *testing.T, r *http.Request) []byte {
	t.Helper()

	file, _, err := r.FormFile("Block")
	require.NoError(t, err)
	defer file.Close()

	payload, err := io.ReadAll(file)
	require.NoError(t, err)

	return payload
}

type failingSeekReader struct {
	*bytes.Reader
	err error
}

func (r *failingSeekReader) Seek(int64, int) (int64, error) {
	return 0, r.err
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
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
