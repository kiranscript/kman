package adminapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/kirank55/kman/internal/admin"
)

// errorCodes are every admin.Code, and "internal".
var errorCodes = []admin.Code{admin.CodeInvalid, admin.CodeNotFound, admin.CodeConflict, admin.CodeUnauthorized, "internal"}

// OpenAPI generates the API's OpenAPI 3.1 document from the routes and the
// Go types of their requests and responses. The same bytes are served at
// Prefix+"/openapi.json" and committed as docs/admin-api/openapi.json.
func OpenAPI() ([]byte, error) {
	g := &schemas{defs: map[string]any{}}
	paths := map[string]map[string]any{}
	add := func(path, method string, op map[string]any) {
		if paths[path] == nil {
			paths[path] = map[string]any{}
		}
		paths[path][strings.ToLower(method)] = op
	}
	for _, rt := range routes {
		op := map[string]any{
			"operationId": rt.id,
			"summary":     rt.summary,
			"responses": map[string]any{
				fmt.Sprint(rt.status): jsonResponse(http.StatusText(rt.status), g.of(rt.resp)),
				"default":             jsonResponse("An error", g.of(reflect.TypeFor[ErrorResponse]())),
			},
		}
		if rt.req != nil {
			if params := parameters(g, rt.req); len(params) > 0 {
				op["parameters"] = params
			}
			if hasBody(rt.req) {
				op["requestBody"] = map[string]any{
					"required": true,
					"content":  map[string]any{"application/json": map[string]any{"schema": g.of(rt.req)}},
				}
			}
		}
		add(Prefix+rt.path, rt.method, op)
	}
	add(Prefix+docPath, "GET", map[string]any{
		"operationId": "openapi",
		"summary":     "This document. It needs no admin token.",
		"security":    []any{},
		"responses":   map[string]any{"200": jsonResponse("OK", map[string]any{"type": "object"})},
	})
	doc := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":   "kman admin API",
			"version": "v1",
			"description": "The one way to change a running kman (ADR 0004). Every change is one store transaction, " +
				"audited with its actor: the name of the admin token the request was made with. " +
				"Generated from the Go types in internal/admin; do not edit by hand.",
		},
		"security": []any{map[string]any{"adminToken": []any{}}},
		"paths":    paths,
		"components": map[string]any{
			"schemas": g.defs,
			"securitySchemes": map[string]any{
				"adminToken": map[string]any{
					"type": "http", "scheme": "bearer",
					"description": "An admin token (kma_...). A dashboard session cookie is never accepted here.",
				},
			},
		},
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func jsonResponse(desc string, schema any) map[string]any {
	return map[string]any{
		"description": desc,
		"content":     map[string]any{"application/json": map[string]any{"schema": schema}},
	}
}

// parameters lists the path and query parameters of a request type.
func parameters(g *schemas, t reflect.Type) []any {
	var out []any
	for i := range t.NumField() {
		f := t.Field(i)
		in, name := "path", f.Tag.Get("path")
		if name == "" {
			in, name = "query", f.Tag.Get("query")
		}
		if name == "" {
			continue
		}
		p := map[string]any{"name": name, "in": in, "required": in == "path", "schema": g.of(f.Type)}
		if d := f.Tag.Get("doc"); d != "" {
			p["description"] = d
		}
		out = append(out, p)
	}
	return out
}

// schemas builds JSON Schemas for Go types, collecting named structs as
// components.
type schemas struct{ defs map[string]any }

var (
	timeType = reflect.TypeFor[time.Time]()
	rawType  = reflect.TypeFor[json.RawMessage]()
	codeType = reflect.TypeFor[admin.Code]()
)

func (g *schemas) of(t reflect.Type) map[string]any {
	switch t {
	case timeType:
		return map[string]any{"type": "string", "format": "date-time"}
	case rawType:
		return map[string]any{"description": "Any JSON value."}
	case codeType:
		return map[string]any{"type": "string", "enum": errorCodes}
	}
	switch t.Kind() {
	case reflect.Pointer:
		return g.of(t.Elem())
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int32:
		return map[string]any{"type": "integer"}
	case reflect.Int64:
		return map[string]any{"type": "integer", "format": "int64"}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": g.of(t.Elem())}
	case reflect.Struct:
		name := t.Name()
		if _, done := g.defs[name]; !done {
			g.defs[name] = nil // a placeholder, in case the type refers to itself
			g.defs[name] = g.object(t)
		}
		return map[string]any{"$ref": "#/components/schemas/" + name}
	}
	panic("adminapi: no schema for " + t.String())
}

// object is the schema of a struct's JSON fields.
func (g *schemas) object(t reflect.Type) map[string]any {
	props := map[string]any{}
	required := []string{}
	for i := range t.NumField() {
		f := t.Field(i)
		name := jsonName(f)
		if name == "" {
			continue
		}
		s := g.of(f.Type)
		if d := f.Tag.Get("doc"); d != "" {
			if ref, ok := s["$ref"]; ok {
				s = map[string]any{"description": d, "allOf": []any{map[string]any{"$ref": ref}}}
			} else {
				s = withDescription(s, d)
			}
		}
		props[name] = s
		if f.Type.Kind() != reflect.Pointer && !strings.Contains(f.Tag.Get("json"), ",omitempty") {
			required = append(required, name)
		}
	}
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

func withDescription(s map[string]any, d string) map[string]any {
	out := map[string]any{}
	for k, v := range s {
		out[k] = v
	}
	out["description"] = d
	return out
}

// jsonName is the name of a field in JSON, or "" if it has none there.
func jsonName(f reflect.StructField) string {
	tag, ok := f.Tag.Lookup("json")
	if !ok || !f.IsExported() {
		return ""
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "-" {
		return ""
	}
	if name == "" {
		return f.Name
	}
	return name
}
