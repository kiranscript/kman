package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/kirank55/kman/internal/policy/geoip"
)

// defaultGeoIPSource is where kman geoip update downloads DB-IP Lite from. It is
// the one address kman reaches by this command, and only when the operator runs
// it (ADR 0008).
const defaultGeoIPSource = "https://download.db-ip.com/free"

// geoipMaxSize is the most a database may unpack to. DB-IP Lite's are tens of
// megabytes; the cap stops a wrong URL from filling the disk.
const geoipMaxSize = 512 << 20

// geoipTimeout is how long one download may take.
const geoipTimeout = 10 * time.Minute

// geoipUsage is what `kman geoip` with no or an unknown subcommand prints.
const geoipUsage = "usage: kman geoip <update>"

// cmdGeoIP runs kman geoip.
func cmdGeoIP(ctx context.Context, args []string, s *sys) int {
	if len(args) == 0 || args[0] != "update" {
		if len(args) > 0 && (args[0] == "-h" || args[0] == "-help" || args[0] == "--help") {
			fmt.Fprintln(s.stderr, geoipUsage)
			return 0
		}
		fmt.Fprintln(s.stderr, geoipUsage)
		return 1
	}
	fs := flag.NewFlagSet("geoip update", flag.ContinueOnError)
	fs.SetOutput(s.stderr)
	fs.Usage = func() {
		fmt.Fprintln(s.stderr, "usage: kman geoip update [--config-dir dir] [--source url]")
		fs.PrintDefaults()
	}
	configDir := fs.String("config-dir", "", "the config directory holding kman.yaml (default ~/.config/kman, or %APPDATA%\\kman on Windows)")
	source := fs.String("source", defaultGeoIPSource, "the address to download DB-IP Lite from, which serves dbip-country-lite-YYYY-MM.mmdb.gz and dbip-asn-lite-YYYY-MM.mmdb.gz: a mirror, for a network that cannot reach DB-IP")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 1
	}
	if err := geoipUpdate(ctx, s, *configDir, *source); err != nil {
		fmt.Fprintln(s.stderr, "kman geoip update:", err)
		return 1
	}
	return 0
}

// geoipDownload is one database to fetch: the DB-IP file, and where it goes.
type geoipDownload struct {
	name string // as DB-IP names it, without the month
	dest string
	tmp  string
}

func geoipUpdate(ctx context.Context, s *sys, configDir, source string) error {
	if configDir == "" {
		dir, _, err := defaultDirs(s.goos, s.getenv)
		if err != nil {
			return err
		}
		configDir = dir
	}
	cfg, err := loadConfig(filepath.Join(configDir, configFile), s.getenv)
	if err != nil {
		return err
	}
	configured := cfg.GeoIP
	if configured.CountryDB == "" {
		configured.CountryDB = filepath.Join(cfg.DataDir, "geoip", "dbip-country-lite.mmdb")
	}
	if configured.ASNDB == "" {
		configured.ASNDB = filepath.Join(cfg.DataDir, "geoip", "dbip-asn-lite.mmdb")
	}
	downloads := []*geoipDownload{
		{name: "dbip-country-lite", dest: configured.CountryDB},
		{name: "dbip-asn-lite", dest: configured.ASNDB},
	}
	client := &http.Client{Timeout: geoipTimeout}
	now := s.now().UTC()
	defer func() {
		for _, d := range downloads {
			if d.tmp != "" {
				os.Remove(d.tmp)
			}
		}
	}()
	// Every download lands in a temporary file beside its destination first, so
	// that a failed one leaves the databases in use as they were.
	for _, d := range downloads {
		if err := d.fetch(ctx, client, source, now); err != nil {
			return err
		}
	}
	for _, d := range downloads {
		if err := os.Rename(d.tmp, d.dest); err != nil {
			return fmt.Errorf("replacing %s: %w (on Windows, stop kman serve first: it holds the file open)", d.dest, err)
		}
		d.tmp = ""
		fmt.Fprintln(s.stdout, "wrote", d.dest)
	}
	fmt.Fprintln(s.stdout, "IP geolocation data by DB-IP.com (https://db-ip.com), licensed CC BY 4.0.")
	if !cfg.GeoIP.enabled() {
		fmt.Fprintf(s.stdout, "kman does not read them until kman.yaml names them. Add:\n\ngeoip:\n  country_db: %s\n  asn_db: %s\n\n", configured.CountryDB, configured.ASNDB)
	}
	fmt.Fprintln(s.stdout, "kman reads GeoIP databases at startup: restart kman serve to use them.")
	return nil
}

// fetch downloads the database of this month, or of last month while DB-IP has
// not published this month's, and leaves it, checked, in d.tmp.
func (d *geoipDownload) fetch(ctx context.Context, client *http.Client, source string, now time.Time) error {
	var lastErr error
	for _, month := range []time.Time{now, now.AddDate(0, -1, 0)} {
		url := fmt.Sprintf("%s/%s-%s.mmdb.gz", trimSlash(source), d.name, month.Format("2006-01"))
		err := d.download(ctx, client, url)
		if err == nil {
			return nil
		}
		var missing *notPublishedError
		if !errors.As(err, &missing) {
			return err
		}
		lastErr = err
	}
	return lastErr
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// notPublishedError is a 404: that month's file is not there.
type notPublishedError struct{ url string }

func (e *notPublishedError) Error() string { return e.url + " is not published (404)" }

func (d *geoipDownload) download(ctx context.Context, client *http.Client, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return &notPublishedError{url}
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("downloading %s: %s", url, resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(d.dest), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(d.dest), filepath.Base(d.dest)+".tmp*")
	if err != nil {
		return err
	}
	d.tmp = f.Name()
	if err := unpack(f, resp.Body); err != nil {
		f.Close()
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	if err := f.Chmod(0o644); err != nil && !errors.Is(err, os.ErrPermission) {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// A page, or a file cut short, is not a database; find out here, not at the
	// next start.
	db, err := geoip.Open(d.tmp, "")
	if err != nil {
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	return db.Close()
}

// unpack copies a download to w, unpacking it when it is gzip, which is how DB-IP
// serves it, and refusing more than geoipMaxSize.
func unpack(w io.Writer, r io.Reader) error {
	br := bufio.NewReader(r)
	src := io.Reader(br)
	if magic, _ := br.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		zr, err := gzip.NewReader(br)
		if err != nil {
			return err
		}
		defer zr.Close()
		src = zr
	}
	n, err := io.Copy(w, io.LimitReader(src, geoipMaxSize+1))
	if err != nil {
		return err
	}
	if n > geoipMaxSize {
		return fmt.Errorf("the file is over %d MiB, which no GeoIP database is", geoipMaxSize>>20)
	}
	return nil
}
