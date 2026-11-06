package profiles

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
)

// openAI is the OpenAI profile, and that of every service speaking its protocol.
type openAI struct{ generic }

func (openAI) Prepare(h http.Header, path string, body []byte, injectUsage bool) []byte {
	askForIdentity(h)
	if injectUsage && streamsOnlyReportUsageIfAsked(path) {
		return askForUsage(body)
	}
	return body
}

func (openAI) UsageReader(h http.Header) *UsageReader { return newUsageReader(h, (*reported).openAI) }

// anthropic is the Anthropic profile. Its streams report usage unasked.
type anthropic struct{ generic }

func (anthropic) Prepare(h http.Header, _ string, body []byte, _ bool) []byte {
	askForIdentity(h)
	return body
}

func (anthropic) UsageReader(h http.Header) *UsageReader {
	return newUsageReader(h, (*reported).anthropic)
}

// askForIdentity asks for an uncompressed response, because the reader can't see
// usage in a compressed one, and an app could ask for one for that reason alone.
// The app gets a response it can read whatever it asked for: having no encoding
// is always acceptable, and it costs only the bandwidth between kman and the app.
// No header would mean any encoding is fine (RFC 9110), so it is stated.
func askForIdentity(h http.Header) { h.Set("Accept-Encoding", "identity") }

// streamsOnlyReportUsageIfAsked reports whether a route takes
// stream_options.include_usage: chat and legacy completions. The Responses API
// always reports usage, and refuses the option as unknown.
func streamsOnlyReportUsageIfAsked(path string) bool {
	return strings.HasSuffix(strings.TrimSuffix(path, "/"), "/completions")
}

// usageOption is what a stream is asked for.
const usageOption = `{"include_usage":true}`

// askForUsage returns a request body that has stream_options.include_usage set,
// when it asks for a stream. An app's own false is overridden: a stream with no
// usage couldn't be metered, so that would be a way around a cap. A body that
// isn't a JSON object stays as it is, for the upstream to refuse.
//
// Usually the option is simply added, which leaves the app's bytes alone.
// Otherwise the body is rewritten, with its values unchanged, so that it doesn't
// hold the key twice.
func askForUsage(body []byte) []byte {
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return body
	}
	// Anything but false or null: an upstream may be lax about what it takes for
	// true, and is sure to refuse the option if it took this for false.
	stream, ok := fields["stream"]
	if !ok || string(stream) == "false" || string(stream) == "null" {
		return body
	}
	own, ok := fields["stream_options"]
	if !ok {
		end := bytes.LastIndexByte(body, '}')
		out := make([]byte, 0, len(body)+len(`,"stream_options":`)+len(usageOption))
		out = append(out, body[:end]...)
		out = append(out, `,"stream_options":`...)
		out = append(out, usageOption...)
		return append(out, '}')
	}
	options := map[string]json.RawMessage{}
	if string(own) != "null" && json.Unmarshal(own, &options) != nil {
		return body
	}
	if string(options["include_usage"]) == "true" {
		return body
	}
	options["include_usage"] = json.RawMessage("true")
	merged, err := marshalKeepingText(options)
	if err != nil {
		return body
	}
	fields["stream_options"] = merged
	out, err := marshalKeepingText(fields)
	if err != nil {
		return body
	}
	return out
}

// marshalKeepingText marshals fields without the HTML-escaping json.Marshal adds
// to strings. Valid JSON from the app stays as it was written, whitespace aside.
func marshalKeepingText(fields map[string]json.RawMessage) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(fields); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
