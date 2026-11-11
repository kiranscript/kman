package cli

import (
	"context"
	"net/url"

	"github.com/kirank55/kman/internal/admin"
)

func (c *client) AddChannel(ctx context.Context, req admin.AddChannelRequest) (admin.CreatedChannel, error) {
	var out admin.CreatedChannel
	return out, c.do(ctx, "POST", "/channels", nil, req, &out)
}

func (c *client) ListChannels(ctx context.Context) (admin.ChannelList, error) {
	var out admin.ChannelList
	return out, c.do(ctx, "GET", "/channels", nil, nil, &out)
}

func (c *client) TestChannel(ctx context.Context, req admin.ChannelRef) (admin.ChannelTestResult, error) {
	var out admin.ChannelTestResult
	return out, c.do(ctx, "POST", "/channels/"+url.PathEscape(req.Ref)+"/test", nil, nil, &out)
}

func (c *client) RemoveChannel(ctx context.Context, req admin.ChannelRef) (admin.Channel, error) {
	var out admin.Channel
	return out, c.do(ctx, "DELETE", "/channels/"+url.PathEscape(req.Ref), nil, nil, &out)
}
