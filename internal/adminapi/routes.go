package adminapi

import (
	"context"
	"net/http"
	"reflect"

	"github.com/kirank55/kman/internal/admin"
)

// Service is what the API serves: every admin command, and the token check
// that names each request's actor. *admin.Service implements it.
type Service interface {
	admin.API
	Authenticate(ctx context.Context, secret string) (string, error)
}

// Prefix is where the API is mounted.
const Prefix = "/admin/v1"

// route is one operation of the API. The handler and the OpenAPI document
// are both built from the routes, so they cannot drift apart.
type route struct {
	method, path string // path under Prefix, with {name} parameters
	id, summary  string // the OpenAPI operationId and summary
	status       int    // the status of a success
	req, resp    reflect.Type
	// call runs the operation with the bound request, a pointer to a req.
	call func(ctx context.Context, svc Service, req any) (any, error)
}

// op is a route whose command takes a request.
func op[Req, Resp any](method, path, id, summary string, status int, fn func(Service, context.Context, Req) (Resp, error)) route {
	return route{
		method: method, path: path, id: id, summary: summary, status: status,
		req: reflect.TypeFor[Req](), resp: reflect.TypeFor[Resp](),
		call: func(ctx context.Context, svc Service, req any) (any, error) { return fn(svc, ctx, *req.(*Req)) },
	}
}

// op0 is a route whose command takes no request.
func op0[Resp any](method, path, id, summary string, fn func(Service, context.Context) (Resp, error)) route {
	return route{
		method: method, path: path, id: id, summary: summary, status: http.StatusOK,
		resp: reflect.TypeFor[Resp](),
		call: func(ctx context.Context, svc Service, _ any) (any, error) { return fn(svc, ctx) },
	}
}

// routes is the whole API. A new admin command adds its route here, and
// then regenerates the committed OpenAPI document.
var routes = []route{
	op0("GET", "/whoami", "whoami", "Name the admin token this request is made with", Service.Whoami),

	op0("GET", "/admin-tokens", "listAdminTokens", "List every admin token, live or revoked", Service.ListAdminTokens),
	op("POST", "/admin-tokens", "createAdminToken", "Create a named admin token; its secret is returned only this once", http.StatusCreated, Service.CreateAdminToken),
	op("POST", "/admin-tokens/{name}/rotate", "rotateAdminToken", "Give an admin token a new secret; the old one stops working at once", http.StatusOK, Service.RotateAdminToken),
	op("POST", "/admin-tokens/{name}/revoke", "revokeAdminToken", "Revoke an admin token; the install's last live token cannot be revoked", http.StatusOK, Service.RevokeAdminToken),

	op0("GET", "/settings", "getSettings", "Read the install's settings", Service.Settings),
	op("PATCH", "/settings", "updateSettings", "Change some settings; each change is audited", http.StatusOK, Service.UpdateSettings),

	op("GET", "/audit", "listAudit", "Read a page of the audit log, oldest entry first", http.StatusOK, Service.Audit),

	op("GET", "/prices", "listPrices", "List the built-in price table and how old it is", http.StatusOK, Service.ListPrices),
	op0("GET", "/prices/status", "getPriceDataStatus", "Say how old the built-in price table is, and whether to upgrade", Service.PriceDataStatus),

	op0("GET", "/upstreams", "listUpstreams", "List every upstream with its credentials, never their secrets", Service.ListUpstreams),
	op("POST", "/upstreams", "addUpstream", "Add an upstream, copying a preset or built from the settings", http.StatusCreated, Service.AddUpstream),
	op("GET", "/upstreams/{ref}", "getUpstream", "Read one upstream, by name or id", http.StatusOK, Service.GetUpstream),
	op("PATCH", "/upstreams/{ref}", "updateUpstream", "Change some of an upstream's settings", http.StatusOK, Service.UpdateUpstream),
	op("DELETE", "/upstreams/{ref}", "removeUpstream", "Delete an upstream and its credentials; refused while a virtual key that is not revoked depends on one", http.StatusOK, Service.RemoveUpstream),
	op("GET", "/upstreams/{ref}/sync", "planUpstreamSync", "Show how an upstream differs from a newer version of its preset, changing nothing", http.StatusOK, Service.PlanUpstreamSync),
	op("POST", "/upstreams/{ref}/sync", "syncUpstream", "Apply a sync plan; refused if the upstream or the preset has moved since", http.StatusOK, Service.SyncUpstream),

	op("POST", "/credentials", "addCredential", "Seal a secret as a credential on an upstream; it is never shown again", http.StatusCreated, Service.AddCredential),
	op("PATCH", "/credentials/{ref}", "updateCredential", "Rename a credential or replace its secret", http.StatusOK, Service.UpdateCredential),
	op("DELETE", "/credentials/{ref}", "removeCredential", "Delete a credential and wipe its secret; refused while a virtual key that is not revoked depends on it", http.StatusOK, Service.RemoveCredential),
}

// docPath serves the OpenAPI document. It needs no token.
const docPath = "/openapi.json"
