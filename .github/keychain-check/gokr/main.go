// Reads an item through zalando/go-keyring, built with CGO_ENABLED=0
// (so it shells out to /usr/bin/security). Never prints the secret.
package main

import (
	"fmt"
	"os"

	"github.com/zalando/go-keyring"
)

func main() {
	v, err := keyring.Get(os.Args[1], "kman")
	if err != nil {
		fmt.Println("ERR", err)
		os.Exit(1)
	}
	want, _ := os.ReadFile("/tmp/kman-expected")
	if v == string(want) {
		fmt.Println("SILENT_MATCH")
	} else {
		fmt.Println("MISMATCH")
	}
}
