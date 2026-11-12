package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"

	"github.com/kirank55/kman/internal/admin"
)

func (c *client) Prune(ctx context.Context, req admin.PruneRequest) (admin.PruneResult, error) {
	var out admin.PruneResult
	return out, c.do(ctx, "POST", "/prune", nil, req, &out)
}

// Backup asks for a backup and returns it as a stream, which the caller must
// close. Unlike every other call it has no overall timeout, since a large
// database takes as long as it takes; ctx still ends it.
func (c *client) Backup(ctx context.Context) (admin.Download, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", c.base+prefix+"/backup", nil)
	if err != nil {
		return admin.Download{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	hc := *c.hc
	hc.Timeout = 0
	resp, err := hc.Do(req)
	if err != nil {
		return admin.Download{}, fmt.Errorf("cannot reach kman at %s (is kman serve running?): %w", c.shown, unwrapURLError(err))
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		var e struct {
			Error admin.Error `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
			return admin.Download{}, &e.Error
		}
		return admin.Download{}, fmt.Errorf("%s answered %s: is it a kman admin listener?", c.shown, resp.Status)
	}
	name := ""
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
		name = params["filename"]
	}
	return admin.Download{Name: name, Size: resp.ContentLength, Body: resp.Body}, nil
}

func (c *client) UpdateStatus(ctx context.Context) (admin.UpdateStatus, error) {
	var out admin.UpdateStatus
	return out, c.do(ctx, "GET", "/update", nil, nil, &out)
}
