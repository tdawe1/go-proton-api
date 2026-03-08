package proton

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"github.com/go-resty/resty/v2"
)

func (c *Client) GetBlock(ctx context.Context, bareURL, token string) (io.ReadCloser, error) {
	res, err := c.doRes(ctx, func(r *resty.Request) (*resty.Response, error) {
		return r.SetHeader("pm-storage-token", token).SetDoNotParseResponse(true).Get(bareURL)
	})
	if err != nil {
		return nil, err
	}

	return res.RawBody(), nil
}

func (c *Client) RequestBlockUpload(ctx context.Context, req BlockUploadReq) ([]BlockUploadLink, error) {
	var res struct {
		UploadLinks []BlockUploadLink
	}

	if err := c.do(ctx, func(r *resty.Request) (*resty.Response, error) {
		return r.SetResult(&res).SetBody(req).Post("/drive/blocks")
	}); err != nil {
		return nil, err
	}

	return res.UploadLinks, nil
}

func (c *Client) UploadBlock(ctx context.Context, bareURL, token string, block io.Reader) error {
	uploadCtx := withRetryDisabled(ctx)

	maxAttempts := c.m.rc.RetryCount + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	var uploadErr error
	didAuthRefresh := false

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		res, err := c.doRes(uploadCtx, func(r *resty.Request) (*resty.Response, error) {
			return r.
				SetHeader("pm-storage-token", token).
				SetMultipartField("Block", "blob", "application/octet-stream", block).
				Post(bareURL)
		})
		uploadErr = err
		if uploadErr == nil {
			return nil
		}

		wantsAuthRefresh := false
		shouldRetry := false

		if apiErr := (*APIError)(nil); errors.As(uploadErr, &apiErr) && apiErr.Status == http.StatusUnauthorized && !didAuthRefresh {
			wantsAuthRefresh = true
			shouldRetry = true
		} else if attempt < maxAttempts && shouldRetryUploadBlock(uploadErr) {
			shouldRetry = true
		}

		if !shouldRetry {
			break
		}

		seeker, ok := block.(io.Seeker)
		if !ok {
			break
		}

		if _, seekErr := seeker.Seek(0, io.SeekStart); seekErr != nil {
			uploadErr = seekErr
			break
		}

		if wantsAuthRefresh {
			if err := c.authRefresh(ctx); err != nil {
				return fmt.Errorf("failed to refresh auth: %w", err)
			}
			didAuthRefresh = true

			if attempt == maxAttempts {
				maxAttempts++
			}

			continue
		}

		retryDelay := uploadRetryDelay(res, attempt)
		if retryDelay > 0 {
			retryTimer := time.NewTimer(retryDelay)
			select {
			case <-ctx.Done():
				retryTimer.Stop()
				return ctx.Err()
			case <-retryTimer.C:
			}
		}
	}

	return uploadErr
}

func uploadRetryDelay(res *resty.Response, attempt int) time.Duration {
	if res != nil {
		retryAfterHeader := res.Header().Get("Retry-After")
		if retryAfter, err := strconv.Atoi(retryAfterHeader); err == nil && retryAfter > 0 {
			return time.Duration(retryAfter) * time.Second
		}

		if retryAfterDate, err := http.ParseTime(retryAfterHeader); err == nil {
			delay := time.Until(retryAfterDate)
			if delay > 0 {
				return delay
			}
		}
	}

	if attempt < 1 {
		attempt = 1
	}

	delay := time.Duration(1<<uint(attempt-1)) * 200 * time.Millisecond
	if delay > 2*time.Second {
		delay = 2 * time.Second
	}

	// Add jitter to reduce synchronized retries under contention.
	delay += time.Duration(rand.Intn(100)) * time.Millisecond

	return delay
}

func shouldRetryUploadBlock(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	if apiErr := (*APIError)(nil); errors.As(err, &apiErr) {
		return apiErr.Status == http.StatusTooManyRequests || apiErr.Status == http.StatusServiceUnavailable
	}

	return isTransientTransportError(err)
}
