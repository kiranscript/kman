package detect

import (
	"bytes"
	"io"

	"github.com/kirank55/kman/internal/token"
)

// Severity is how much a finding matters.
type Severity string

const (
	// High is a virtual key: it works until it expires or is revoked.
	High Severity = "high"
	// Low is a child key, which is short-lived by design (ADR 0001).
	Low Severity = "low"
)

// Finding is one kman key found in text, confirmed by its checksum. It never
// holds the secret: Redacted is the prefix and the key id, which is not
// secret, and which names the key to revoke.
type Finding struct {
	Source   string   `json:"source" doc:"What was scanned: a file path, or - for standard input."`
	Line     int      `json:"line" doc:"The line, counting from 1."`
	Column   int      `json:"column" doc:"The byte on the line where the key starts, counting from 1."`
	Kind     string   `json:"kind" doc:"virtual key or child key."`
	KeyID    string   `json:"key_id" doc:"The key id inside the key: kman key show <key_id> names the key."`
	Severity Severity `json:"severity" doc:"high for a virtual key, low for a child key."`
	Label    string   `json:"label" doc:"What to call the finding: child key (short-lived) for a child key."`
	Redacted string   `json:"redacted" doc:"The key's prefix and id, then an ellipsis. The secret is never reported."`
}

// blockSize is how much Scan reads at a time. A key is at most maxKey bytes, so
// blocks overlap by overlap bytes: the longest key and the byte before it,
// which the \b in the scanner pattern needs.
const (
	blockSize = 64 << 10
	maxKey    = len("ek_kmn_") + 8 + 1 + 49
	overlap   = maxKey + 1
)

// Scan finds the kman keys in what r holds, in the order they appear. The
// published pattern (token.Scanner) finds candidates and token.Parse confirms
// each one by its checksum, so a string that only looks like a key is not
// reported. It reads r in blocks, so a large file or log costs bounded memory,
// and a key is found once wherever it falls. source names r in each finding.
// It works offline: it asks no server whether a key is live.
func Scan(source string, r io.Reader) ([]Finding, error) {
	var out []Finding
	window := make([]byte, 0, blockSize+overlap)
	// base is the absolute offset of window[0]; line and lineStart are the
	// line number and the offset of the first byte of the line at base.
	var base int64
	line, lineStart := 1, int64(0)
	// decided is the absolute offset up to which matches have been settled.
	var decided int64 = -1
	for {
		old := len(window)
		window = window[:old+blockSize]
		n, err := io.ReadFull(r, window[old:])
		window = window[:old+n]
		final := err == io.EOF || err == io.ErrUnexpectedEOF
		if err != nil && !final {
			return out, err
		}
		for _, m := range token.Scanner.FindAllIndex(window, -1) {
			start, end := m[0], m[1]
			// A match that runs to the end of a block may be a longer word cut
			// short: the next block settles it.
			if !final && end >= len(window) || base+int64(end) <= decided {
				continue
			}
			s := string(window[start:end])
			t, perr := token.Parse(s)
			if perr != nil {
				continue
			}
			ln, ls := line, lineStart
			if i := bytes.LastIndexByte(window[:start], '\n'); i >= 0 {
				ln += bytes.Count(window[:start], []byte{'\n'})
				ls = base + int64(i) + 1
			}
			out = append(out, finding(source, ln, int(base+int64(start)-ls)+1, t))
		}
		if final {
			return out, nil
		}
		decided = base + int64(len(window)) - 1
		// Move on by all but the overlap, tracking the line.
		keep := len(window) - overlap
		if i := bytes.LastIndexByte(window[:keep], '\n'); i >= 0 {
			line += bytes.Count(window[:keep], []byte{'\n'})
			lineStart = base + int64(i) + 1
		}
		base += int64(keep)
		window = append(window[:0], window[keep:]...)
	}
}

func finding(source string, line, col int, t token.Token) Finding {
	f := Finding{Source: source, Line: line, Column: col, Kind: t.Kind.String(), KeyID: string(t.ID),
		Severity: High, Label: t.Kind.String()}
	prefix := "kmn_"
	if t.Kind == token.ChildKey {
		f.Severity, f.Label, prefix = Low, "child key (short-lived)", "ek_kmn_"
	}
	f.Redacted = prefix + f.KeyID + "_…"
	return f
}
