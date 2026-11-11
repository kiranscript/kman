package cli

import (
	"context"
	"net/url"

	"github.com/kirank55/kman/internal/admin"
)

func (c *client) RotateKey(ctx context.Context, req admin.RotateKeyRequest) (admin.RotatedKey, error) {
	var out admin.RotatedKey
	return out, c.do(ctx, "POST", "/keys/"+url.PathEscape(req.ID)+"/rotate", nil, req, &out)
}

func (c *client) CompromiseCredential(ctx context.Context, req admin.CompromiseCredentialRequest) (admin.CompromisedCredential, error) {
	var out admin.CompromisedCredential
	return out, c.do(ctx, "POST", "/credentials/"+url.PathEscape(req.Ref)+"/compromise", nil, req, &out)
}
