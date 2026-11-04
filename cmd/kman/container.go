package main

import "os"

// inContainer reports whether kman runs in a container: Docker leaves
// /.dockerenv, Podman /run/.containerenv, systemd-nspawn and Podman set
// $container, and Kubernetes sets $KUBERNETES_SERVICE_HOST.
func inContainer(getenv func(string) string) bool {
	if getenv("container") != "" || getenv("KUBERNETES_SERVICE_HOST") != "" {
		return true
	}
	for _, f := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(f); err == nil {
			return true
		}
	}
	return false
}

// dockerSteps is what init prints when it refuses to run in a container
// that has no key source.
const dockerSteps = `In a container the data directory is the volume, so the master key has to come from outside it:

  1. docker run --rm ghcr.io/kirank55/kman keygen > master.key
  2. Mount master.key as a secret and set KMAN_MASTER_KEY_FILE to where it is mounted
     (the Compose example does both).
  3. docker compose run --rm kman init     (the admin token goes to your terminal, not the logs)
  4. docker compose up -d
`
