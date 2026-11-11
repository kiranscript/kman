// Package admin is the one mutation path (ADR 0004). Each command runs one store
// transaction, publishes the change to the registry, and records an audit
// entry with its actor.
//
// The request and response types of the commands are the admin API's DTOs:
// adminapi serves them as /admin/v1 and generates the OpenAPI document from
// them, and cli sends them as the API's typed client. Both transports import
// only admin, so the types live here. Their json tags are the wire format;
// path and query tags name the URL parameters adminapi binds, and doc tags
// describe fields in the OpenAPI document.
//
// Every command reads its caller from the context (WithCaller). adminapi
// sets it from the request's admin token; the dashboard (M3) will set it from
// its session.
package admin
