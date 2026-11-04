// Package adminapi is the /admin/v1 HTTP transport over admin, with typed DTOs and
// the OpenAPI document. Like every transport, it imports only admin.
//
// The DTOs are admin's request and response types; the route table maps
// each to a method and path, and both Handler and OpenAPI are built from
// it. The committed document, docs/admin-api/openapi.json, is checked by a
// test, so CI fails when it is stale.
package adminapi
