package cli

import (
	"context"
	"net/url"
	"strconv"

	"github.com/kirank55/kman/internal/admin"
)

func (c *client) ListIncidents(ctx context.Context, req admin.IncidentQuery) (admin.IncidentList, error) {
	q := url.Values{}
	if req.Open {
		q.Set("open", "true")
	}
	if req.Key != "" {
		q.Set("key", req.Key)
	}
	if req.Before != 0 {
		q.Set("before", strconv.FormatInt(req.Before, 10))
	}
	if req.Limit != 0 {
		q.Set("limit", strconv.Itoa(req.Limit))
	}
	var out admin.IncidentList
	return out, c.do(ctx, "GET", "/incidents", q, nil, &out)
}

func incidentPath(id int64) string { return "/incidents/" + strconv.FormatInt(id, 10) }

func (c *client) GetIncident(ctx context.Context, req admin.IncidentRef) (admin.IncidentDetail, error) {
	var out admin.IncidentDetail
	return out, c.do(ctx, "GET", incidentPath(req.ID), nil, nil, &out)
}

func (c *client) AckIncident(ctx context.Context, req admin.IncidentRef) (admin.Incident, error) {
	var out admin.Incident
	return out, c.do(ctx, "POST", incidentPath(req.ID)+"/acknowledge", nil, nil, &out)
}

func (c *client) ResolveIncident(ctx context.Context, req admin.ResolveIncidentRequest) (admin.Incident, error) {
	var out admin.Incident
	return out, c.do(ctx, "POST", incidentPath(req.ID)+"/resolve", nil, req, &out)
}

func (c *client) ThisWasMe(ctx context.Context, req admin.ThisWasMeRequest) (admin.ThisWasMeResult, error) {
	var out admin.ThisWasMeResult
	return out, c.do(ctx, "POST", incidentPath(req.ID)+"/this-was-me", nil, req, &out)
}

func (c *client) ListKnownSources(ctx context.Context, req admin.KnownSourceQuery) (admin.KnownSourceList, error) {
	q := url.Values{}
	if req.Key != "" {
		q.Set("key", req.Key)
	}
	var out admin.KnownSourceList
	return out, c.do(ctx, "GET", "/known-sources", q, nil, &out)
}

func (c *client) AddKnownSource(ctx context.Context, req admin.AddKnownSourceRequest) (admin.KnownSource, error) {
	var out admin.KnownSource
	return out, c.do(ctx, "POST", "/known-sources", nil, req, &out)
}

func (c *client) RemoveKnownSource(ctx context.Context, req admin.KnownSourceRef) (admin.KnownSource, error) {
	var out admin.KnownSource
	return out, c.do(ctx, "DELETE", "/known-sources/"+strconv.FormatInt(req.ID, 10), nil, nil, &out)
}
