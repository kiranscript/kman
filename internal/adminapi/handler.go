package adminapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/kirank55/kman/internal/admin"
)

// maxBody is the largest request body the API reads.
const maxBody = 1 << 20

// Handler serves the API under Prefix. Every route but the OpenAPI document
// needs an admin token as a bearer token; nothing else, such as a
// dashboard cookie, is ever accepted. Internal errors are logged to log and
// answered without detail.
func Handler(svc Service, log *slog.Logger) http.Handler {
	doc, docErr := OpenAPI()
	mux := http.NewServeMux()
	for _, rt := range routes {
		mux.Handle(rt.method+" "+Prefix+rt.path, authenticated(svc, log, rt))
	}
	mux.HandleFunc("GET "+Prefix+docPath, func(w http.ResponseWriter, r *http.Request) {
		if docErr != nil {
			internalError(w, log, docErr)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(doc)
	})
	mux.HandleFunc(Prefix+"/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, &admin.Error{Code: admin.CodeNotFound, Message: "no such admin API route: " + r.Method + " " + r.URL.Path})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Responses carry secrets and the install's state: never cache them.
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

// authenticated serves rt for a request with a live admin token, as its
// actor.
func authenticated(svc Service, log *slog.Logger, rt route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			unauthorized(w, &admin.Error{Code: admin.CodeUnauthorized, Message: "send an admin token as Authorization: Bearer kma_..."})
			return
		}
		actor, err := svc.Authenticate(r.Context(), strings.TrimSpace(secret))
		if err != nil {
			var ae *admin.Error
			if errors.As(err, &ae) {
				unauthorized(w, ae)
			} else {
				internalError(w, log, err)
			}
			return
		}
		ctx := admin.WithCaller(r.Context(), admin.Caller{Actor: actor, ClientIP: clientIP(r)})
		var req any
		if rt.req != nil {
			if req, err = bind(r, rt.req); err != nil {
				writeError(w, http.StatusBadRequest, &admin.Error{Code: admin.CodeInvalid, Message: err.Error()})
				return
			}
		}
		resp, err := rt.call(ctx, svc, req)
		if err != nil {
			var ae *admin.Error
			if !errors.As(err, &ae) {
				internalError(w, log, err)
				return
			}
			writeError(w, statusOf(ae.Code), ae)
			return
		}
		writeJSON(w, rt.status, resp)
	})
}

// statusOf is the HTTP status of an admin error code.
func statusOf(c admin.Code) int {
	switch c {
	case admin.CodeInvalid:
		return http.StatusBadRequest
	case admin.CodeNotFound:
		return http.StatusNotFound
	case admin.CodeConflict:
		return http.StatusConflict
	case admin.CodeUnauthorized:
		return http.StatusUnauthorized
	}
	return http.StatusInternalServerError
}

// clientIP is the address a request came from, "" over a Unix socket.
// trusted_proxies (#42) is not consulted: the admin listener is meant to be
// reached directly.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	return host
}

// ErrorResponse is the body of every error.
type ErrorResponse struct {
	Error admin.Error `json:"error"`
}

func writeError(w http.ResponseWriter, status int, e *admin.Error) {
	writeJSON(w, status, ErrorResponse{Error: *e})
}

func unauthorized(w http.ResponseWriter, e *admin.Error) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="kman admin"`)
	writeError(w, http.StatusUnauthorized, e)
}

func internalError(w http.ResponseWriter, log *slog.Logger, err error) {
	log.Error("admin API", "err", err)
	writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: admin.Error{Code: "internal", Message: "internal error: see the kman serve log"}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(append(b, '\n'))
}
