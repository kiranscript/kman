package main

import (
	"path/filepath"
	"testing"
)

func TestDefaultDirs(t *testing.T) {
	for _, tc := range []struct {
		name, goos   string
		env          map[string]string
		config, data string
	}{
		{"linux", "linux", map[string]string{"HOME": "/home/ana"},
			"/home/ana/.config/kman", "/home/ana/.local/share/kman"},
		{"macOS", "darwin", map[string]string{"HOME": "/Users/ana"},
			"/Users/ana/.config/kman", "/Users/ana/.local/share/kman"},
		{"XDG dirs", "linux", map[string]string{"HOME": "/home/ana", "XDG_CONFIG_HOME": "/cfg", "XDG_DATA_HOME": "/data"},
			"/cfg/kman", "/data/kman"},
		{"relative XDG dirs are ignored", "linux", map[string]string{"HOME": "/home/ana", "XDG_CONFIG_HOME": "cfg"},
			"/home/ana/.config/kman", "/home/ana/.local/share/kman"},
		{"windows", "windows", map[string]string{"APPDATA": `C:\Users\ana\AppData\Roaming`, "LOCALAPPDATA": `C:\Users\ana\AppData\Local`},
			`C:\Users\ana\AppData\Roaming` + string(filepath.Separator) + "kman", `C:\Users\ana\AppData\Local` + string(filepath.Separator) + "kman"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, data, err := defaultDirs(tc.goos, func(k string) string { return tc.env[k] })
			if err != nil {
				t.Fatal(err)
			}
			if filepath.ToSlash(config) != filepath.ToSlash(tc.config) || filepath.ToSlash(data) != filepath.ToSlash(tc.data) {
				t.Errorf("defaultDirs = %q, %q; want %q, %q", config, data, tc.config, tc.data)
			}
		})
	}
}

func TestDefaultDirsWithoutAHomeAsksForFlags(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		if _, _, err := defaultDirs(goos, func(string) string { return "" }); err == nil {
			t.Errorf("defaultDirs(%s) with no environment succeeded", goos)
		}
	}
}
