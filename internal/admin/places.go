package admin

import (
	"context"
	"time"

	"github.com/kirank55/kman/internal/store"
)

// PlaceQuery names a key, and optionally the kind of place to list.
type PlaceQuery struct {
	ID   string `json:"-" path:"id" doc:"The key id."`
	Kind string `json:"-" query:"kind" doc:"Only places of this kind: country, asn or network."`
}

// KeyPlaces is every place a key has been used from.
type KeyPlaces struct {
	KeyID  string  `json:"key_id"`
	Places []Place `json:"places" doc:"Most recently seen first."`
}

// Place is one place a key has been used from: a country, an autonomous system,
// or a network. It counts requests kman forwarded and requests it refused, so a
// key that is disabled, or locked to other addresses, still shows where it
// turned up.
type Place struct {
	Kind      string    `json:"kind" doc:"country, asn or network."`
	Value     string    `json:"value" doc:"An ISO 3166-1 alpha-2 country code, an AS number, or an IPv4 /24 or IPv6 /64 network. Empty for the other row."`
	Other     bool      `json:"other,omitempty" doc:"This row counts every place of its kind past the first 1,000 the key was seen from."`
	Label     string    `json:"label,omitempty" doc:"A name for the value, when kman has one: an AS's organisation, from the ASN database."`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Requests  int64     `json:"requests" doc:"Requests kman forwarded from here, and realtime sessions."`
	Refused   int64     `json:"refused" doc:"Requests kman refused from here."`
}

// Places returns where a key has been used from, most recently seen first. It
// reads places, which outlive the events: a place's first sight stays true
// after its events are pruned.
func (s *Service) Places(ctx context.Context, req PlaceQuery) (KeyPlaces, error) {
	switch req.Kind {
	case "", store.PlaceCountry, store.PlaceASN, store.PlaceNetwork:
	default:
		return KeyPlaces{}, errorf(CodeInvalid, "kind %q is not one of country, asn or network", req.Kind)
	}
	out := KeyPlaces{KeyID: req.ID, Places: []Place{}}
	err := s.read(ctx, func(tx *store.Tx) error {
		if _, err := tx.Key(req.ID); err != nil {
			return fromStore(err, "key "+req.ID)
		}
		rows, err := tx.Places(req.ID, req.Kind)
		if err != nil {
			return err
		}
		for _, p := range rows {
			out.Places = append(out.Places, Place{
				Kind: p.Kind, Value: p.Value, Other: p.Value == store.PlaceOther, Label: p.Label,
				FirstSeen: p.FirstSeen, LastSeen: p.LastSeen, Requests: p.Count, Refused: p.Blocked,
			})
		}
		return nil
	})
	return out, err
}
