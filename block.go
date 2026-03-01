package proton

import (
	"context"
	"io"

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
	var uploadErr error

	for attempt := 1; attempt <= 3; attempt++ {
		uploadErr = c.do(ctx, func(r *resty.Request) (*resty.Response, error) {
			return r.
				SetHeader("pm-storage-token", token).
				SetMultipartField("Block", "blob", "application/octet-stream", block).
				Post(bareURL)
		})
		if uploadErr == nil {
			return nil
		}

		if attempt < 3 {
			seeker, ok := block.(io.Seeker)
			if !ok {
				break
			}

			if _, seekErr := seeker.Seek(0, io.SeekStart); seekErr != nil {
				uploadErr = seekErr
				break
			}
		}
	}

	return uploadErr
}
