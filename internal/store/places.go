package store

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"slices"
	"strconv"
	"time"
)

// The kinds of place a key is used from (#84).
const (
	PlaceCountry = "country" // an ISO 3166-1 alpha-2 code
	PlaceASN     = "asn"     // an autonomous system number, in decimal
	PlaceNetwork = "network" // an IPv4 /24 or an IPv6 /64
	PlaceClient  = "client"  // a User-Agent reduced to a product and major version, or none (#86)
)

// MaxPlacesPerKind is how many places of one kind a key keeps. A key seen from
// more, as a leaked key used from everywhere would be, counts the rest in one
// row of value PlaceOther, so the table stays bounded however it is used.
const MaxPlacesPerKind = 1000

// PlaceOther is the value of the row that counts a key's places of one kind
// past MaxPlacesPerKind.
const PlaceOther = ""

// PlaceRow is one place one key has been used from, from the baseline_places
// table. Places are kept forever, so FirstSeen stays true after the events that
// showed it are pruned.
type PlaceRow struct {
	KeyID, Kind, Value string
	// Label names the value, when kman knows a name: an ASN's organisation.
	Label               string
	FirstSeen, LastSeen time.Time
	Count               int64 // requests kman forwarded, and realtime sessions
	Blocked             int64 // requests kman refused
}

// NewPlace is a place that a batch of events gave a key for the first time (a
// row added to baseline_places, not one only updated), found by AddEventsFresh
// in the transaction that stored the batch. The location rules read these
// (#89): a place is new exactly once, and the transaction that recorded it can
// raise what it means.
type NewPlace struct {
	KeyID, Kind, Value string
	// Label names the value, when the event had a name for it: an ASN's
	// organisation.
	Label string
	// At is when the first event of the batch that showed the place happened, and
	// Event its index in the rows given to AddEventsFresh.
	At    time.Time
	Event int
	// Prior is how many places of this kind the key had before the batch, not
	// counting the overflow row. With none, this is the key's first place of the
	// kind and there is nothing to compare it with.
	Prior int
}

// placeKey is one key's place.
type placeKey struct{ key, kind, value string }

// placeTally adds up places over a run of events, in the order each was first
// seen, so that past the limit the places kept are the earliest.
type placeTally struct {
	order []placeKey
	m     map[placeKey]*PlaceRow
	first map[placeKey]int // the index of the event that first showed a place
}

func newPlaceTally() *placeTally {
	return &placeTally{m: map[placeKey]*PlaceRow{}, first: map[placeKey]int{}}
}

// add counts the places of one event. An event with no key belongs to no key,
// an event with no address (a Unix socket) has no network, and an event with no
// client (stored before clients were recorded) has none.
func (t *placeTally) add(i int, r EventRow) {
	if r.KeyID == "" {
		return
	}
	if r.Country != "" {
		t.count(i, r, PlaceCountry, r.Country, "")
	}
	if r.ASN != 0 {
		t.count(i, r, PlaceASN, strconv.FormatInt(r.ASN, 10), r.ASOrg)
	}
	if n := networkOf(r.ClientIP); n != "" {
		t.count(i, r, PlaceNetwork, n, "")
	}
	if r.Client != "" {
		t.count(i, r, PlaceClient, r.Client, "")
	}
}

func (t *placeTally) count(i int, r EventRow, kind, value, label string) {
	k := placeKey{r.KeyID, kind, value}
	p := t.m[k]
	if p == nil {
		p = &PlaceRow{KeyID: r.KeyID, Kind: kind, Value: value, FirstSeen: r.Time, LastSeen: r.Time}
		t.m[k] = p
		t.order = append(t.order, k)
		t.first[k] = i
	}
	if label != "" {
		p.Label = label
	}
	p.FirstSeen, p.LastSeen = minTime(p.FirstSeen, r.Time), maxTime(p.LastSeen, r.Time)
	if r.Kind == "blocked" {
		p.Blocked++
	} else {
		p.Count++
	}
}

// write adds the tally to baseline_places. A place the key already has is
// updated; a new one is added while the key has fewer than MaxPlacesPerKind of
// its kind, and is counted as PlaceOther after that. It returns the places it
// added, in the order they were first seen: not the overflow row, which is not
// a place.
func (t *placeTally) write(ctx context.Context, tx *sql.Tx) ([]NewPlace, error) {
	if len(t.order) == 0 {
		return nil, nil
	}
	update, err := tx.PrepareContext(ctx, `UPDATE baseline_places SET
			label = COALESCE(NULLIF(?4, ''), label),
			first_seen = MIN(first_seen, ?5), last_seen = MAX(last_seen, ?6),
			count = count + ?7, blocked = blocked + ?8
		WHERE key_id = ?1 AND kind = ?2 AND value = ?3`)
	if err != nil {
		return nil, typed(err, "add places")
	}
	defer update.Close()
	insert, err := tx.PrepareContext(ctx, `INSERT INTO baseline_places (key_id, kind, value, label, first_seen, last_seen, count, blocked)
		VALUES (?1, ?2, ?3, NULLIF(?4, ''), ?5, ?6, ?7, ?8)
		ON CONFLICT (key_id, kind, value) DO UPDATE SET
			first_seen = MIN(first_seen, excluded.first_seen), last_seen = MAX(last_seen, excluded.last_seen),
			count = count + excluded.count, blocked = blocked + excluded.blocked`)
	if err != nil {
		return nil, typed(err, "add places")
	}
	defer insert.Close()
	type keyKind struct{ key, kind string }
	kept := map[keyKind]int{}  // how many places of a kind a key has, once looked up
	prior := map[keyKind]int{} // ... and how many it had before this batch added any
	var fresh []NewPlace
	for _, k := range t.order {
		p := t.m[k]
		res, err := update.ExecContext(ctx, p.KeyID, p.Kind, p.Value, p.Label, toMilli(p.FirstSeen), toMilli(p.LastSeen), p.Count, p.Blocked)
		if err != nil {
			return nil, typed(err, "add places of key "+p.KeyID)
		}
		if n, err := res.RowsAffected(); err != nil {
			return nil, typed(err, "add places of key "+p.KeyID)
		} else if n > 0 {
			continue
		}
		kk := keyKind{p.KeyID, p.Kind}
		n, ok := kept[kk]
		if !ok {
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM baseline_places WHERE key_id = ? AND kind = ? AND value <> ''`,
				p.KeyID, p.Kind).Scan(&n); err != nil {
				return nil, typed(err, "add places of key "+p.KeyID)
			}
			prior[kk] = n
		}
		value, label := p.Value, p.Label
		if n >= MaxPlacesPerKind {
			value, label = PlaceOther, ""
		} else {
			n++
			fresh = append(fresh, NewPlace{KeyID: p.KeyID, Kind: p.Kind, Value: p.Value, Label: p.Label, At: p.FirstSeen, Event: t.first[k], Prior: prior[kk]})
		}
		kept[kk] = n
		if _, err := insert.ExecContext(ctx, p.KeyID, p.Kind, value, label, toMilli(p.FirstSeen), toMilli(p.LastSeen), p.Count, p.Blocked); err != nil {
			return nil, typed(err, "add places of key "+p.KeyID)
		}
	}
	return fresh, nil
}

// networkOf is the network an address is counted in: its /24 for IPv4, its /64
// for IPv6 (the same /64 the limiter counts as one address), and nothing for
// no address or one that does not parse.
func networkOf(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	a = a.Unmap()
	bits := 64
	if a.Is4() {
		bits = 24
	}
	p, err := a.WithZone("").Prefix(bits)
	if err != nil {
		return ""
	}
	return p.String()
}

// NetworkOf is the network an address is counted in: its /24 for IPv4, its /64
// for IPv6, and "" for no address or one that does not parse.
func NetworkOf(ip string) string { return networkOf(ip) }

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// Places returns the places a key has been used from, most recently seen
// first, then by kind and value. An empty kind is every kind.
func (tx *Tx) Places(keyID, kind string) ([]PlaceRow, error) {
	rows, err := tx.tx.QueryContext(tx.ctx, `SELECT key_id, kind, value, COALESCE(label, ''), first_seen, last_seen, count, blocked
		FROM baseline_places WHERE key_id = ?1 AND (?2 = '' OR kind = ?2)
		ORDER BY last_seen DESC, kind, value`, keyID, kind)
	if err != nil {
		return nil, typed(err, "read places")
	}
	defer rows.Close()
	out := []PlaceRow{}
	for rows.Next() {
		var p PlaceRow
		var first, last int64
		if err := rows.Scan(&p.KeyID, &p.Kind, &p.Value, &p.Label, &first, &last, &p.Count, &p.Blocked); err != nil {
			return nil, typed(err, "read places")
		}
		p.FirstSeen, p.LastSeen = fromMilli(first), fromMilli(last)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, typed(err, "read places")
	}
	return out, nil
}

// UsualPlaces returns the places of one kind that a key is used from most: the
// ones it has had forwarded requests from, busiest first, then most recently
// seen, without the overflow row and without the values in except (the places a
// batch has just added, which are what is being compared). It returns at most
// limit.
func (tx *Tx) UsualPlaces(keyID, kind string, except []string, limit int) ([]PlaceRow, error) {
	rows, err := tx.tx.QueryContext(tx.ctx, `SELECT key_id, kind, value, COALESCE(label, ''), first_seen, last_seen, count, blocked
		FROM baseline_places WHERE key_id = ?1 AND kind = ?2 AND value <> '' AND count > 0
		ORDER BY count DESC, last_seen DESC, value LIMIT ?3`, keyID, kind, limit+len(except))
	if err != nil {
		return nil, typed(err, "read places")
	}
	defer rows.Close()
	out := []PlaceRow{}
	for rows.Next() && len(out) < limit {
		var p PlaceRow
		var first, last int64
		if err := rows.Scan(&p.KeyID, &p.Kind, &p.Value, &p.Label, &first, &last, &p.Count, &p.Blocked); err != nil {
			return nil, typed(err, "read places")
		}
		if slices.Contains(except, p.Value) {
			continue
		}
		p.FirstSeen, p.LastSeen = fromMilli(first), fromMilli(last)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, typed(err, "read places")
	}
	return out, nil
}

// LearningStart returns when the key's learning period began, which is its first
// forwarded request or realtime session, and false for a key that has had none
// (one that was only refused has not started to be learned).
func (tx *Tx) LearningStart(keyID string) (time.Time, bool, error) {
	var ms int64
	err := tx.tx.QueryRowContext(tx.ctx, `SELECT started_at FROM key_learning WHERE key_id = ?`, keyID).Scan(&ms)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return time.Time{}, false, nil
	case err != nil:
		return time.Time{}, false, typed(err, "read the learning period of key "+keyID)
	}
	return fromMilli(ms), true, nil
}
