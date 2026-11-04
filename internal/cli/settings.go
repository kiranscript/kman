package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kirank55/kman/internal/admin"
)

// setting is one setting as the CLI names, prints and parses it.
type setting struct {
	name  string
	get   func(admin.Settings) string
	parse func(string, *admin.SettingsUpdate) error
}

var settings = []setting{
	{
		name: "max_key_lifetime",
		get:  func(s admin.Settings) string { return formatSeconds(s.MaxKeyLifetimeSeconds) },
		parse: func(v string, u *admin.SettingsUpdate) error {
			n, err := parseSeconds(v)
			u.MaxKeyLifetimeSeconds = &n
			return err
		},
	},
	{
		name: "default_key_ttl",
		get:  func(s admin.Settings) string { return formatSeconds(s.DefaultKeyTTLSeconds) },
		parse: func(v string, u *admin.SettingsUpdate) error {
			n, err := parseSeconds(v)
			u.DefaultKeyTTLSeconds = &n
			return err
		},
	},
	{
		name: "spend_timezone",
		get:  func(s admin.Settings) string { return s.SpendTimezone },
		parse: func(v string, u *admin.SettingsUpdate) error {
			u.SpendTimezone = &v
			return nil
		},
	},
	{
		name: "soft_budget_defaults",
		get: func(s admin.Settings) string {
			if len(s.SoftBudgetPercents) == 0 {
				return "none"
			}
			var parts []string
			for _, p := range s.SoftBudgetPercents {
				parts = append(parts, strconv.Itoa(p))
			}
			return strings.Join(parts, ",")
		},
		parse: func(v string, u *admin.SettingsUpdate) error {
			pcts := []int{}
			if v != "none" {
				for _, p := range strings.Split(v, ",") {
					n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(p), "%"))
					if err != nil {
						return fmt.Errorf("%q is not a list of percentages such as 50,80,100 (or none)", v)
					}
					pcts = append(pcts, n)
				}
			}
			u.SoftBudgetPercents = &pcts
			return nil
		},
	},
}

func settingNamed(name string) (setting, error) {
	i := slices.IndexFunc(settings, func(s setting) bool { return s.name == name })
	if i < 0 {
		var names []string
		for _, s := range settings {
			names = append(names, s.name)
		}
		return setting{}, fmt.Errorf("no setting %q; the settings are %s", name, strings.Join(names, ", "))
	}
	return settings[i], nil
}

func cmdSettings(ctx context.Context, c *call) error {
	sub, err := c.sub("<get|set> ...", "get", "set")
	if err != nil {
		return err
	}
	if sub == "get" {
		return settingsGet(ctx, c)
	}
	fs := c.flags("settings set <name> <value> [<name> <value> ...] [--json]\n\n"+
		"Durations take a number of days (90d) or hours and minutes (36h, 1h30m).", true)
	pos, err := c.parse(fs, 2, -1)
	if err != nil {
		return err
	}
	if len(pos)%2 != 0 {
		fs.Usage()
		return errReported
	}
	var u admin.SettingsUpdate
	for i := 0; i < len(pos); i += 2 {
		s, err := settingNamed(pos[i])
		if err != nil {
			return err
		}
		if err := s.parse(pos[i+1], &u); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	got, err := cl.UpdateSettings(ctx, u)
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(got)
	}
	return printSettings(c, got, nil)
}

func settingsGet(ctx context.Context, c *call) error {
	fs := c.flags("settings get [<name> ...] [--json]", true)
	names, err := c.parse(fs, 0, -1)
	if err != nil {
		return err
	}
	var picked []setting
	for _, n := range names {
		s, err := settingNamed(n)
		if err != nil {
			return err
		}
		picked = append(picked, s)
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	got, err := cl.Settings(ctx)
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(got)
	}
	return printSettings(c, got, picked)
}

// printSettings prints one setting's value alone, or a table of settings.
func printSettings(c *call, s admin.Settings, picked []setting) error {
	if len(picked) == 1 {
		_, err := fmt.Fprintln(c.env.Stdout, picked[0].get(s))
		return err
	}
	if len(picked) == 0 {
		picked = settings
	}
	w := tabwriter.NewWriter(c.env.Stdout, 0, 0, 2, ' ', 0)
	for _, p := range picked {
		fmt.Fprintf(w, "%s\t%s\n", p.name, p.get(s))
	}
	return w.Flush()
}

const day = 24 * time.Hour

// parseSeconds parses 90d, or a Go duration such as 36h or 1h30m.
func parseSeconds(v string) (int64, error) {
	if n, ok := strings.CutSuffix(v, "d"); ok {
		days, err := strconv.ParseInt(n, 10, 64)
		if err == nil && days > 0 && days <= 100000 {
			return days * int64(day/time.Second), nil
		}
	} else if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return int64(d / time.Second), nil
	}
	return 0, errors.New("want a duration such as 90d, 36h or 1h30m (never is not allowed)")
}

// formatSeconds prints whole days as 90d and anything else as a Go
// duration, such as 36h.
func formatSeconds(n int64) string {
	d := time.Duration(n) * time.Second
	if d%day == 0 {
		return strconv.FormatInt(int64(d/day), 10) + "d"
	}
	s := d.String()
	s = strings.TrimSuffix(s, "0s")
	return strings.TrimSuffix(s, "0m")
}
