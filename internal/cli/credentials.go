package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CredentialsFile is the file in the config directory that holds the admin
// URL and token the CLI uses: written by kman init and kman login, removed
// by kman logout.
const CredentialsFile = "credentials.json"

// The environment variables that override the credentials file.
const (
	TokenEnv = "KMAN_ADMIN_TOKEN"
	URLEnv   = "KMAN_ADMIN_URL"
)

// DefaultAdminURL is where kman init puts the admin listener.
const DefaultAdminURL = "http://127.0.0.1:8081"

// credentials is the credentials file.
type credentials struct {
	AdminURL string `json:"admin_url"`
	Token    string `json:"token"`
}

// SaveCredentials writes the credentials file at path, 0600, replacing any
// earlier one in one step.
func SaveCredentials(path, adminURL, token string) error {
	raw, err := json.MarshalIndent(credentials{AdminURL: adminURL, Token: token}, "", "  ")
	if err != nil {
		return err
	}
	// CreateTemp makes the file 0600.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".credentials-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // fails harmlessly once renamed
	_, err = tmp.Write(append(raw, '\n'))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// login is where the CLI's admin URL and token come from.
type login struct {
	url, token string
	path       string // the credentials file
	tokenFrom  string // the credentials file's path, or TokenEnv
}

func (c *call) credentialsPath() (string, error) {
	if c.configDir == "" {
		return "", errors.New("no config directory: pass --config-dir")
	}
	return filepath.Join(c.configDir, CredentialsFile), nil
}

// login reads the credentials file, with KMAN_ADMIN_URL and
// KMAN_ADMIN_TOKEN overriding what it holds.
func (c *call) login() (login, error) {
	path, err := c.credentialsPath()
	if err != nil {
		return login{}, err
	}
	l := login{path: path}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		var cr credentials
		if err := json.Unmarshal(raw, &cr); err != nil {
			return login{}, fmt.Errorf("%s: %v; run kman login again", path, err)
		}
		l.url, l.token, l.tokenFrom = cr.AdminURL, cr.Token, path
	case !errors.Is(err, os.ErrNotExist):
		return login{}, err
	}
	if u := c.env.Getenv(URLEnv); u != "" {
		l.url = u
	}
	if l.url == "" {
		l.url = DefaultAdminURL
	}
	if t := strings.TrimSpace(c.env.Getenv(TokenEnv)); t != "" {
		l.token, l.tokenFrom = t, TokenEnv
	}
	if l.token == "" {
		return login{}, fmt.Errorf("no admin token: run kman login --admin-url <url>, or set %s", TokenEnv)
	}
	return l, nil
}
