package admin

import (
	"context"
	"time"

	"github.com/kiranscript/kman/internal/store"
)

// maxClients is how many addresses KeyClients lists. The key's total is always
// given, so a longer list is said to be longer.
const maxClients = 50

// KeyClients is the client addresses a key was used from, grouped, in the events
// kman still keeps (30 days unless retention says otherwise).
type KeyClients struct {
	KeyID   string      `json:"key_id"`
	Clients []KeyClient `json:"clients" doc:"The addresses the key was used from, the most recently seen first, at most 50. A request that came in on a Unix socket has no address and is not counted."`
	Total   int         `json:"total" doc:"How many distinct addresses the key was used from. More than the length of clients when the list was cut."`
}

// KeyClient is what one client address did with a key.
type KeyClient struct {
	Address           string    `json:"address" doc:"The client address: the connection's, or X-Forwarded-For only through trusted_proxies."`
	Requests          int64     `json:"requests" doc:"Forwarded requests and realtime sessions."`
	Blocked           int64     `json:"blocked" doc:"Requests kman refused: a locked address, a refused key, a policy."`
	FirstSeen         time.Time `json:"first_seen"`
	LastSeen          time.Time `json:"last_seen"`
	Country           string    `json:"country,omitempty" doc:"ISO 3166-1 code, from the country database, when there is one that knows the address."`
	ASN               int64     `json:"asn,omitempty" doc:"The autonomous system number, from the ASN database, when there is one that knows the address."`
	CostMicro         int64     `json:"cost_micro" doc:"The known cost of its requests, in microdollars."`
	UnmeteredRequests int64     `json:"unmetered_requests,omitempty" doc:"Forwarded requests whose cost is not known, which cost_micro does not include."`
}

// KeyClients says which addresses a key was used from. A client address is
// personal data, so it stays out of the admin API unless the install turned on
// expose_client_addresses; with it off this is refused, and says how to turn it
// on. It reads and changes nothing.
func (s *Service) KeyClients(ctx context.Context, req KeyRef) (KeyClients, error) {
	out := KeyClients{KeyID: req.ID, Clients: []KeyClient{}}
	err := s.read(ctx, func(tx *store.Tx) error {
		settings, err := loadSettings(tx)
		if err != nil {
			return err
		}
		if !settings.ExposeClientAddresses {
			return errorf(CodeConflict, "the admin API does not give out client addresses until you turn that on: kman settings set expose_client_addresses true")
		}
		if _, err := tx.Key(req.ID); err != nil {
			return fromStore(err, "key "+req.ID)
		}
		rows, total, err := tx.KeyClients(req.ID, maxClients)
		if err != nil {
			return err
		}
		out.Total = total
		for _, r := range rows {
			out.Clients = append(out.Clients, KeyClient{Address: r.IP, Requests: r.Requests, Blocked: r.Blocked,
				FirstSeen: r.FirstSeen, LastSeen: r.LastSeen, Country: r.Country, ASN: r.ASN,
				CostMicro: r.CostMicro, UnmeteredRequests: r.Unmetered})
		}
		return nil
	})
	return out, err
}
