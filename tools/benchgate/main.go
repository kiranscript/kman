// Command benchgate is the benchmark gate of SPEC §17. It compares go test
// -bench output from the base branch with the head of a PR and fails when
// allocations per op rise or benchstat's statistics show a slowdown over
// the limit (10% by default).
//
//	benchgate -base base.txt -head head.txt
//
// Benchmarks join the gate by existing: CI runs every benchmark in the main
// module with -benchmem.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	basePath := flag.String("base", "", "go test -bench output from the base branch")
	headPath := flag.String("head", "", "go test -bench output from the head")
	maxSlowdown := flag.Float64("max-slowdown", 0.10, "the largest significant slowdown allowed, as a fraction")
	flag.Parse()
	if *basePath == "" || *headPath == "" || flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}
	os.Exit(run(*basePath, *headPath, *maxSlowdown))
}

func run(basePath, headPath string, maxSlowdown float64) int {
	base, err := os.Open(basePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	defer base.Close()
	head, err := os.Open(headPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	defer head.Close()
	r, err := Compare(base, head, maxSlowdown)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	for _, n := range r.Notes {
		fmt.Println("note:", n)
	}
	for _, f := range r.Failures {
		fmt.Println("FAIL:", f)
	}
	if len(r.Failures) > 0 {
		return 1
	}
	fmt.Println("benchmark gate passed")
	return 0
}
