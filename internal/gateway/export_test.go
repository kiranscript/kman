package gateway

import "net/http"

// WriteError writes the error for a code, for the test of the error table:
// most codes have no stage that produces them yet.
func WriteError(w http.ResponseWriter, c string) { writeError(w, code(c), "") }
