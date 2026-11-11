package profiles_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/profiles"
)

// The e2e transcripts say what usage each response holds, so the SDK tests replay
// the very responses these are read from.
func TestEveryTranscriptReportsTheUsageItSays(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "e2e", "testdata", "transcripts", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metered int
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var tr struct {
			Profile   string `json:"profile"`
			Exchanges []struct {
				Response struct {
					Headers http.Header `json:"headers"`
					Chunks  []string    `json:"chunks"`
				} `json:"response"`
				Usage json.RawMessage `json:"usage"`
			} `json:"exchanges"`
		}
		if err := json.Unmarshal(raw, &tr); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if tr.Profile == "" {
			continue
		}
		for _, ex := range tr.Exchanges {
			metered++
			t.Run(strings.TrimSuffix(filepath.Base(file), ".json"), func(t *testing.T) {
				var want profiles.Usage
				dec := json.NewDecoder(bytes.NewReader(ex.Usage))
				dec.DisallowUnknownFields()
				if err := dec.Decode(&want); err != nil {
					t.Fatalf("the transcript's usage: %v", err)
				}
				// As they arrive, and then split up as a network might.
				r := profiles.For(tr.Profile).UsageReader(ex.Response.Headers)
				singly := profiles.For(tr.Profile).UsageReader(ex.Response.Headers)
				for _, chunk := range ex.Response.Chunks {
					r.Feed([]byte(chunk))
					for i := range len(chunk) {
						singly.Feed([]byte{chunk[i]})
					}
				}
				if got := r.Usage(); !reflect.DeepEqual(got, want) {
					t.Errorf("usage = %+v\n want   %+v", got, want)
				}
				if got := singly.Usage(); !reflect.DeepEqual(got, want) {
					t.Errorf("a byte at a time: usage = %+v\n want   %+v", got, want)
				}
			})
		}
	}
	if metered == 0 {
		t.Error("no transcript has a profile")
	}
}
