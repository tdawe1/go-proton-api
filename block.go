package proton

import (
	"context"
	"errors"
	"io"
	"net/http"

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

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		uploadErr = c.do(uploadCtx, func(r *resty.Request) (*resty.Response, error) {
			return r.
				SetHeader("pm-storage-token", token).
				SetMultipartField("Block", "blob", "application/octet-stream", block).
				Post(bareURL)
		})
		if uploadErr == nil {
			return nil
		}

		if attempt == maxAttempts || !shouldRetryUploadBlock(uploadErr) {
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
	}

	return uploadErr
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
