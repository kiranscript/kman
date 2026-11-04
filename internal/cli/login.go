package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
)

func cmdLogin(ctx context.Context, c *call) error {
	fs := c.flags("login [--admin-url url] [--config-dir dir]\n\nReads an admin token from the terminal, or from stdin when piped, checks it with the server, and saves both to the credentials file.", false)
	adminURL := fs.String("admin-url", DefaultAdminURL, "the admin API: http://host:port, https://host:port or unix:/path/to/socket")
	if _, err := c.parse(fs, 0, 0); err != nil {
		return err
	}
	path, err := c.credentialsPath()
	if err != nil {
		return err
	}
	tok, err := c.readToken()
	if err != nil {
		return err
	}
	cl, err := newClient(*adminURL, tok)
	if err != nil {
		return err
	}
	if plainRemote(*adminURL) {
		fmt.Fprintf(c.env.Stderr, "kman login: warning: %s is plain HTTP to another host, so the admin token crosses the network unencrypted\n", *adminURL)
	}
	who, err := cl.Whoami(ctx)
	if err != nil {
		return fmt.Errorf("%s did not accept the token: %w", *adminURL, err)
	}
	if err := SaveCredentials(path, *adminURL, tok); err != nil {
		return err
	}
	fmt.Fprintf(c.env.Stdout, "Logged in to %s as %s. Saved to %s.\n", *adminURL, who.Actor, path)
	if c.env.Getenv(TokenEnv) != "" {
		fmt.Fprintf(c.env.Stderr, "kman login: note: %s is set and still overrides the saved token\n", TokenEnv)
	}
	return nil
}

// readToken reads an admin token from the terminal without echo, or a line
// of stdin.
func (c *call) readToken() (string, error) {
	var raw []byte
	var err error
	if c.env.ReadSecret != nil {
		raw, err = c.env.ReadSecret("Admin token: ")
	} else {
		var line string
		line, err = bufio.NewReader(c.env.Stdin).ReadString('\n')
		if errors.Is(err, io.EOF) {
			err = nil
		}
		raw = []byte(line)
	}
	if err != nil {
		return "", fmt.Errorf("read the admin token: %w", err)
	}
	tok := strings.TrimSpace(string(raw))
	clear(raw)
	if tok == "" {
		return "", errors.New("no admin token given")
	}
	return tok, nil
}

// plainRemote reports whether u is plain HTTP to a host that is not
// loopback.
func plainRemote(u string) bool {
	p, err := url.Parse(u)
	if err != nil || p.Scheme != "http" {
		return false
	}
	host := p.Hostname()
	if host == "localhost" {
		return false
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
}

func cmdLogout(_ context.Context, c *call) error {
	fs := c.flags("logout [--config-dir dir]", false)
	if _, err := c.parse(fs, 0, 0); err != nil {
		return err
	}
	path, err := c.credentialsPath()
	if err != nil {
		return err
	}
	switch err := os.Remove(path); {
	case err == nil:
		fmt.Fprintf(c.env.Stdout, "Logged out: removed %s.\n", path)
	case errors.Is(err, os.ErrNotExist):
		fmt.Fprintf(c.env.Stdout, "Not logged in: %s does not exist.\n", path)
	default:
		return err
	}
	if c.env.Getenv(TokenEnv) != "" {
		fmt.Fprintf(c.env.Stderr, "kman logout: note: %s is still set\n", TokenEnv)
	}
	return nil
}
