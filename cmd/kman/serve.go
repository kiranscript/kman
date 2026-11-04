package main

import (
	"context"
	"fmt"
)

func cmdServe(ctx context.Context, args []string, s *sys) int {
	fmt.Fprintln(s.stderr, "kman serve: not yet")
	return 1
}
