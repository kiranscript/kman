package alerts

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// slackBody renders an alert as a Slack message in mrkdwn. Slack reads &, < and
// > as markup, so they are escaped in everything an alert holds.
func slackBody(a Alert, link string) ([]byte, error) {
	esc := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace
	var b strings.Builder
	fmt.Fprintf(&b, "*[%s] %s*\n", strings.ToUpper(a.Severity.String()), esc(a.Title))
	if a.KeyID != "" {
		if a.KeyName != "" {
			fmt.Fprintf(&b, "Key: %s (%s)\n", esc(a.KeyName), esc(a.KeyID))
		} else {
			fmt.Fprintf(&b, "Key: %s\n", esc(a.KeyID))
		}
	}
	if len(a.Rules) > 0 {
		fmt.Fprintf(&b, "Rules: %s\n", esc(strings.Join(a.Rules, ", ")))
	}
	for _, f := range a.Evidence {
		fmt.Fprintf(&b, "%s: %s\n", esc(f.Label), esc(f.Value))
	}
	if link != "" {
		fmt.Fprintf(&b, "Incident: %s\n", esc(link))
	}
	if len(a.Commands) > 0 {
		b.WriteString("```\n" + esc(strings.Join(a.Commands, "\n")) + "\n```\n")
	}
	return json.Marshal(map[string]string{"text": strings.TrimRight(b.String(), "\n")})
}

// webhookPayload is what a webhook is sent: version 1 of the document that
// docs/operator-guide/alerts.md describes.
type webhookPayload struct {
	Version    int         `json:"version"`
	ID         string      `json:"id"`
	Kind       string      `json:"kind"`
	Severity   string      `json:"severity"`
	Time       time.Time   `json:"time"`
	Title      string      `json:"title"`
	Key        *webhookKey `json:"key,omitempty"`
	IncidentID int64       `json:"incident_id,omitempty"`
	Rules      []string    `json:"rules,omitempty"`
	Evidence   []Fact      `json:"evidence"`
	Commands   []string    `json:"commands"`
	Link       string      `json:"dashboard_url,omitempty"`
}

type webhookKey struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

func webhookBody(a Alert, deliveryID, link string) ([]byte, error) {
	p := webhookPayload{
		Version: 1, ID: deliveryID, Kind: a.Kind, Severity: a.Severity.String(), Time: a.Time.UTC(),
		Title: a.Title, IncidentID: a.IncidentID, Rules: a.Rules, Evidence: a.Evidence, Commands: a.Commands, Link: link,
	}
	if p.Evidence == nil {
		p.Evidence = []Fact{}
	}
	if p.Commands == nil {
		p.Commands = []string{}
	}
	if a.KeyID != "" {
		p.Key = &webhookKey{ID: a.KeyID, Name: a.KeyName}
	}
	return json.Marshal(p)
}

// sign is the signature a webhook receiver recomputes: HMAC-SHA256, under the
// channel's signing secret, of the timestamp, a dot and the body, in hex and
// prefixed "v1=". Signing the timestamp lets a receiver refuse a replay.
func sign(secret string, timestamp time.Time, body []byte) (ts, sig string) {
	ts = strconv.FormatInt(timestamp.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	return ts, "v1=" + hex.EncodeToString(mac.Sum(nil))
}
