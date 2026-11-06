package gateway

import (
	"encoding/json"
	"net/http"
)

// code is an error code of SPEC §6. An app sees it in the error's code field
// and in the X-Kman-Error header.
type code string

const (
	codeInvalidKey          code = "invalid_key"
	codeKeyExpired          code = "key_expired"
	codeKeyRevoked          code = "key_revoked"
	codeKeyDisabled         code = "key_disabled"
	codePathNotAllowed      code = "path_not_allowed"
	codeModelNotAllowed     code = "model_not_allowed"
	codeAddressNotAllowed   code = "address_not_allowed"
	codeUnmeteredRequest    code = "unmetered_request"
	codeUnknownModelPrice   code = "unknown_model_price"
	codeSpendCapReached     code = "spend_cap_reached"
	codeRequestTooLarge     code = "request_too_large"
	codeRateLimited         code = "rate_limited"
	codeUpstreamUnavailable code = "upstream_unavailable"
	codeStoreUnavailable    code = "store_unavailable"
)

// meaning is what a code tells an app. A stage with more to say passes its own
// message.
type meaning struct {
	status  int
	kind    string // the error's type field, which SDKs branch on
	message string
}

const (
	kindAuthentication = "authentication_error"
	kindPermission     = "permission_error"
	kindInvalidRequest = "invalid_request_error"
	kindRateLimit      = "rate_limit_error"
	kindServer         = "api_error"
)

// meanings is the error table of SPEC §6. A message never repeats anything from
// the request.
var meanings = map[code]meaning{
	codeInvalidKey:          {http.StatusUnauthorized, kindAuthentication, "This is not a valid kman virtual key."},
	codeKeyExpired:          {http.StatusUnauthorized, kindAuthentication, "This virtual key has expired."},
	codeKeyRevoked:          {http.StatusUnauthorized, kindAuthentication, "This virtual key has been revoked."},
	codeKeyDisabled:         {http.StatusForbidden, kindPermission, "This virtual key is disabled."},
	codePathNotAllowed:      {http.StatusForbidden, kindPermission, "This virtual key may not use this method and path."},
	codeModelNotAllowed:     {http.StatusForbidden, kindPermission, "This virtual key may not use this model."},
	codeAddressNotAllowed:   {http.StatusForbidden, kindPermission, "This virtual key may not be used from this address."},
	codeUnmeteredRequest:    {http.StatusForbidden, kindPermission, "This virtual key may send only requests kman can meter."},
	codeUnknownModelPrice:   {http.StatusForbidden, kindPermission, "kman has no price for this model."},
	codeSpendCapReached:     {http.StatusForbidden, kindPermission, "This virtual key has reached its spend cap."},
	codeRequestTooLarge:     {http.StatusRequestEntityTooLarge, kindInvalidRequest, "The request body is over kman's limit."},
	codeRateLimited:         {http.StatusTooManyRequests, kindRateLimit, "This virtual key is over its rate limit."},
	codeUpstreamUnavailable: {http.StatusBadGateway, kindServer, "kman could not get an answer from the upstream."},
	codeStoreUnavailable:    {http.StatusServiceUnavailable, kindServer, "kman cannot record spend for this key right now."},
}

// wireError is the OpenAI error shape, which every SDK kman fronts can read.
type wireError struct {
	Error wireErrorBody `json:"error"`
}

type wireErrorBody struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    code    `json:"code"`
}

// writeError answers with c's error. An empty message means c's usual one.
func writeError(w http.ResponseWriter, c code, message string) {
	m := meanings[c]
	if message == "" {
		message = m.message
	}
	body, err := json.Marshal(wireError{wireErrorBody{Message: message, Type: m.kind, Code: c}})
	if err != nil {
		panic(err) // a struct of strings always marshals
	}
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("X-Kman-Error", string(c))
	w.WriteHeader(m.status)
	w.Write(append(body, '\n'))
}
