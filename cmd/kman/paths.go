package main

import (
	"errors"
	"path"
	"path/filepath"
)

// The files kman keeps in the config directory.
const (
	configFile    = "kman.yaml"
	masterKeyFile = "master.key"
	sealedKeyFile = "master.key.sealed"
)

// The CLI's credentials file, cli.CredentialsFile, sits beside them.

// defaultDirs returns the config directory and the data directory for an
// OS, reading the environment through getenv: ~/.config/kman and
// ~/.local/share/kman on Linux and macOS (or under $XDG_CONFIG_HOME and
// $XDG_DATA_HOME when they are set), and %APPDATA%\kman and
// %LOCALAPPDATA%\kman on Windows.
func defaultDirs(goos string, getenv func(string) string) (config, data string, err error) {
	if goos == "windows" {
		app, local := getenv("APPDATA"), getenv("LOCALAPPDATA")
		if app == "" || local == "" {
			return "", "", errors.New("%APPDATA% or %LOCALAPPDATA% is not set: pass --config-dir and --data-dir")
		}
		return filepath.Join(app, "kman"), filepath.Join(local, "kman"), nil
	}
	home := getenv("HOME")
	xdgConfig, xdgData := getenv("XDG_CONFIG_HOME"), getenv("XDG_DATA_HOME")
	// The XDG spec says to ignore relative paths. These are Unix paths, so
	// path, not filepath, judges them whatever OS runs this.
	if !path.IsAbs(xdgConfig) {
		xdgConfig = ""
	}
	if !path.IsAbs(xdgData) {
		xdgData = ""
	}
	if home == "" && (xdgConfig == "" || xdgData == "") {
		return "", "", errors.New("$HOME is not set: pass --config-dir and --data-dir")
	}
	if xdgConfig == "" {
		xdgConfig = filepath.Join(home, ".config")
	}
	if xdgData == "" {
		xdgData = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(xdgConfig, "kman"), filepath.Join(xdgData, "kman"), nil
}
