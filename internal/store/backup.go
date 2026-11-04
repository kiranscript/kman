package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// keepCopies is how many pre-migration copies are kept.
const keepCopies = 2

// copyPrefix starts the name of each pre-migration copy:
// kman.db.pre-<version>, where version is the schema the database is about
// to be migrated to, as four digits (kman.db.pre-0002 is the database as it
// was before migration 0002).
const copyPrefix = FileName + ".pre-"

// copyAside writes a consistent copy of the database, as it is before being
// migrated to schema version to, then removes all but the newest keepCopies
// copies.
func (s *Store) copyAside(ctx context.Context, to int) error {
	dst := filepath.Join(filepath.Dir(s.path), fmt.Sprintf("%s%04d", copyPrefix, to))
	// A copy left by an earlier failed attempt at the same upgrade is
	// replaced: the database has not changed since it was taken.
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("store: copy before migrating: %w", err)
	}
	// VACUUM INTO accepts an empty file, so the copy is created 0600 first.
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return fmt.Errorf("store: copy before migrating: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("store: copy before migrating: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		os.Remove(dst)
		return fmt.Errorf("store: copy before migrating: %w", err)
	}
	s.log.Info("copied the database before migrating", "copy", dst)
	return s.pruneCopies()
}

// pruneCopies removes all but the newest keepCopies pre-migration copies.
func (s *Store) pruneCopies() error {
	dir := filepath.Dir(s.path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("store: list copies: %w", err)
	}
	var versions []int
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), copyPrefix)
		if !ok || len(rest) != 4 {
			continue
		}
		if v, err := strconv.Atoi(rest); err == nil {
			versions = append(versions, v)
		}
	}
	slices.Sort(versions)
	for len(versions) > keepCopies {
		old := filepath.Join(dir, fmt.Sprintf("%s%04d", copyPrefix, versions[0]))
		if err := os.Remove(old); err != nil {
			return fmt.Errorf("store: remove old copy: %w", err)
		}
		versions = versions[1:]
	}
	return nil
}
