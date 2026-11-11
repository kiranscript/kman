# Address locks and GeoIP

A key can be locked to the networks or countries it may be used from, so a stolen key fails from anywhere else. A request from an address the lock does not allow is refused with `403 address_not_allowed`, before kman reads its body.

## Lock a key to networks

No database is needed:

```sh
kman key update my-app --policy '{"allowed_cidrs":["203.0.113.0/24","198.51.100.7"]}'
```

A bare address is a network of one. kman refuses a network with bits set past its prefix (`10.0.0.5/8`; it names `10.0.0.0/8`), the whole internet (`0.0.0.0/0`: leave the lock out instead) and an IPv4-mapped IPv6 network (write it as IPv4).

Behind a reverse proxy, set `trusted_proxies` in `kman.yaml` first ([TLS and reverse proxies](tls.md)), or every request seems to come from the proxy and a lock either refuses everyone or lets everyone behind it in. kman believes `X-Forwarded-For` only from a trusted proxy, so a thief cannot claim an allowed address.

## Lock a key to countries

A country lock needs a GeoIP database. kman reads [MaxMind DB](https://maxmind.github.io/MaxMind-DB/) (`.mmdb`) files, from the paths in `kman.yaml`:

```yaml
geoip:
  country_db: /var/lib/kman/geoip/dbip-country-lite.mmdb   # country locks, and the country on events
  asn_db: /var/lib/kman/geoip/dbip-asn-lite.mmdb           # optional: the ASN on events
```

Both paths are absolute, and both are optional. A database that holds both (a MaxMind GeoLite2 City, say) may be named for both. You get the files one of two ways:

- **`kman geoip update`** downloads DB-IP Lite's country and ASN databases (about 10 MB) from `download.db-ip.com`, unpacks them, checks they open, and writes them where `kman.yaml` says, or to `geoip/` in the data directory, printing the lines to add to `kman.yaml` if it names none. It is the **only** thing in kman that contacts DB-IP, and only when you run it ([ADR 0008](../adr/0008-no-network-call-unless-configured.md)). A failed download leaves the databases in use as they were. On a network that cannot reach DB-IP, `--source <url>` takes a mirror that serves the same file names (`dbip-country-lite-YYYY-MM.mmdb.gz`).
- **Your own `.mmdb` files**, from any provider: put them where `kman.yaml` points. kman never downloads anything for you.

kman reads the databases once, at startup: restart `kman serve` after an update. DB-IP publishes a new month's files at the start of each month; run the command from cron if you want them fresh. IP geolocation data by [DB-IP.com](https://db-ip.com), licensed [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/).

Then:

```sh
kman key update my-app --policy '{"allowed_countries":["US","DE"]}'
```

Codes are ISO 3166-1 alpha-2, in either case.

## What is refused, and never allowed

- **A country lock with no country database is refused where you set it**, with a message saying to run `kman geoip update` and set `geoip.country_db`. It is never stored to fail later, and never read as "no lock".
- **kman will not start** if a stored key is locked to countries and `kman.yaml` has no `country_db` (say you took `geoip` out of the file), or if a path named in `geoip` is missing or is not a database. It names the keys.
- **An address no database knows** fails a country lock: a lock never lets through what it can't check.
- **A key with both locks** must satisfy both: inside a network *and* in a country.
- A geolocation database is wrong for some addresses, VPNs and cloud ranges. A country lock narrows who can use a stolen key; it is not a boundary on its own. Prefer a network lock where your app has a fixed address.

With a database, each request's country and ASN is also what its event carries.
