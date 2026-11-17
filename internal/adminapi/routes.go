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
	// download, instead of call, is set for a route that answers with a file
	// rather than JSON: a backup.
	download func(ctx context.Context, svc Service) (admin.Download, error)
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

// opDownload is a route that takes no request and answers with a file.
func opDownload(method, path, id, summary string, fn func(Service, context.Context) (admin.Download, error)) route {
	return route{
		method: method, path: path, id: id, summary: summary, status: http.StatusOK,
		download: func(ctx context.Context, svc Service) (admin.Download, error) { return fn(svc, ctx) },
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

	opDownload("POST", "/backup", "createBackup", "Download a consistent copy of the database, made while kman runs; it holds ciphertext only, so it opens only with the master key", Service.Backup),
	op("POST", "/prune", "pruneData", "Delete events, hourly usage and delivered alerts older than a date, and with audit_log the audit log's entries too; the prune is audited", http.StatusOK, Service.Prune),
	op0("GET", "/update", "getUpdateStatus", "Say whether a newer kman release is known; only an install with update_check on ever asks", Service.UpdateStatus),
	op("GET", "/audit", "listAudit", "Read a page of the audit log, oldest entry first", http.StatusOK, Service.Audit),

	op("GET", "/prices", "listPrices", "List the built-in price table, how old it is, and the price overrides", http.StatusOK, Service.ListPrices),
	op0("GET", "/prices/status", "getPriceDataStatus", "Say how old the built-in price table is, and whether to upgrade", Service.PriceDataStatus),
	op("PUT", "/price-overrides", "setPriceOverride", "Set a price override, install-wide or on one upstream; it wins over the built-in entries until removed", http.StatusOK, Service.SetPriceOverride),
	op("DELETE", "/price-overrides", "removePriceOverride", "Remove a price override", http.StatusOK, Service.RemovePriceOverride),
	op("POST", "/price-overrides/import", "importPriceOverrides", "Set many price overrides, all or none", http.StatusOK, Service.ImportPriceOverrides),

	op0("GET", "/upstreams", "listUpstreams", "List every upstream with its credentials, never their secrets", Service.ListUpstreams),
	op("POST", "/upstreams", "addUpstream", "Add an upstream, copying a preset or built from the settings", http.StatusCreated, Service.AddUpstream),
	op("GET", "/upstreams/{ref}", "getUpstream", "Read one upstream, by name or id", http.StatusOK, Service.GetUpstream),
	op("PATCH", "/upstreams/{ref}", "updateUpstream", "Change some of an upstream's settings", http.StatusOK, Service.UpdateUpstream),
	op("DELETE", "/upstreams/{ref}", "removeUpstream", "Delete an upstream, its credentials and its price overrides; refused while a virtual key that is not revoked depends on one", http.StatusOK, Service.RemoveUpstream),
	op("GET", "/upstreams/{ref}/sync", "planUpstreamSync", "Show how an upstream differs from a newer version of its preset, changing nothing", http.StatusOK, Service.PlanUpstreamSync),
	op("POST", "/upstreams/{ref}/sync", "syncUpstream", "Apply a sync plan; refused if the upstream or the preset has moved since", http.StatusOK, Service.SyncUpstream),

	op("POST", "/credentials", "addCredential", "Seal a secret as a credential on an upstream; it is never shown again", http.StatusCreated, Service.AddCredential),
	op("PATCH", "/credentials/{ref}", "updateCredential", "Rename a credential or replace its secret", http.StatusOK, Service.UpdateCredential),
	op("DELETE", "/credentials/{ref}", "removeCredential", "Delete a credential and wipe its secret; refused while a virtual key that is not revoked depends on it", http.StatusOK, Service.RemoveCredential),
	op("POST", "/credentials/{ref}/compromise", "compromiseCredential", "Replace a leaked credential's secret and rotate every virtual key on it, with no grace, in one step; the new key secrets are returned only this once", http.StatusOK, Service.CompromiseCredential),

	op0("GET", "/channels", "listChannels", "List the alert channels, with what each one's outbox holds, never their addresses", Service.ListChannels),
	op("POST", "/channels", "addChannel", "Add a Slack or signed-webhook alert channel; a webhook's signing secret is returned only this once", http.StatusCreated, Service.AddChannel),
	op("POST", "/channels/{ref}/test", "testChannel", "Send a test alert to a channel now and say whether it arrived", http.StatusOK, Service.TestChannel),
	op("DELETE", "/channels/{ref}", "removeChannel", "Remove an alert channel and give up the alerts still waiting for it", http.StatusOK, Service.RemoveChannel),

	op("GET", "/keys", "listKeys", "List every virtual key, or the one with a name, never their secrets", http.StatusOK, Service.ListKeys),
	op("POST", "/keys", "createKey", "Create a virtual key on an upstream; its secret is returned only this once", http.StatusCreated, Service.CreateKey),
	op("GET", "/keys/{id}", "getKey", "Read one virtual key, by key id", http.StatusOK, Service.GetKey),
	op("PATCH", "/keys/{id}", "updateKey", "Change a virtual key's name, note, tags, expiry or policy", http.StatusOK, Service.UpdateKey),
	op("POST", "/keys/{id}/disable", "disableKey", "Stop a virtual key from the next request until it is enabled again", http.StatusOK, Service.DisableKey),
	op("POST", "/keys/{id}/enable", "enableKey", "Let a disabled virtual key work again; a revoked key cannot be enabled", http.StatusOK, Service.EnableKey),
	op("POST", "/keys/{id}/revoke", "revokeKey", "End a virtual key for good, from the next request; it cannot be undone", http.StatusOK, Service.RevokeKey),
	op("POST", "/keys/{id}/rotate", "rotateKey", "Replace a virtual key's secret and keep the key; the new secret is returned only this once", http.StatusOK, Service.RotateKey),

	op0("GET", "/exposure", "getExposure", "List the keys that work now and would be easy to abuse: open to every address, never expiring, or unused for 30 days, each with the command that fixes it", Service.Exposure),
}

// docPath serves the OpenAPI document. It needs no token.
const docPath = "/openapi.json"
