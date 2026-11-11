package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInside(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	if err := os.Mkdir(data, dirMode); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		want bool
	}{
		{data, true},
		{filepath.Join(data, "master.key"), true},
		{filepath.Join(data, "sub", "not-yet", "master.key"), true},
		{filepath.Join(data, "..", "data", "master.key"), true},
		{filepath.Join(root, "data2", "master.key"), false},
		{filepath.Join(root, "master.key"), false},
		{root, false},
	} {
		got, err := inside(data, tc.path)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("inside(data, %s) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
