package adminapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
)

// bind builds a request of type t from r: path and query tags from the URL,
// json tags from the body, which must hold one JSON object with no unknown
// fields. It returns a pointer to the value.
func bind(r *http.Request, t reflect.Type) (any, error) {
	v := reflect.New(t)
	if hasBody(t) {
		body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxBody))
		if err != nil {
			return nil, fmt.Errorf("the request body is over %d bytes", maxBody)
		}
		if len(bytes.TrimSpace(body)) > 0 {
			dec := json.NewDecoder(bytes.NewReader(body))
			dec.DisallowUnknownFields()
			if err := dec.Decode(v.Interface()); err != nil {
				return nil, fmt.Errorf("the request body: %v", err)
			}
			if _, err := dec.Token(); !errors.Is(err, io.EOF) {
				return nil, errors.New("the request body holds more than one JSON value")
			}
		}
	}
	q := r.URL.Query()
	for i := range t.NumField() {
		f := t.Field(i)
		var raw string
		var where string
		if name := f.Tag.Get("path"); name != "" {
			raw, where = r.PathValue(name), "path parameter "+name
		} else if name := f.Tag.Get("query"); name != "" {
			if !q.Has(name) {
				continue
			}
			raw, where = q.Get(name), "query parameter "+name
		} else {
			continue
		}
		if err := setParam(v.Elem().Field(i), raw); err != nil {
			return nil, fmt.Errorf("%s: %v", where, err)
		}
	}
	return v.Interface(), nil
}

// hasBody reports whether t has any field the body sets.
func hasBody(t reflect.Type) bool {
	for _, f := range reflect.VisibleFields(t) {
		if jsonName(f) != "" {
			return true
		}
	}
	return false
}

// setParam parses a URL parameter into a string, integer or boolean field.
func setParam(f reflect.Value, raw string) error {
	switch f.Kind() {
	case reflect.String:
		f.SetString(raw)
	case reflect.Int, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("%q is not an integer", raw)
		}
		f.SetInt(n)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("%q is not true or false", raw)
		}
		f.SetBool(b)
	default:
		return fmt.Errorf("adminapi: cannot bind a %s", f.Kind())
	}
	return nil
}
