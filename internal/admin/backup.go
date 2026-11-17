package admin

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/kirank55/kman/internal/store"
)

// Download is a file the server hands to the caller: a backup. It is a stream,
// not a JSON value, so the admin API serves it as application/octet-stream.
type Download struct {
	// Name is a file name for it, such as kman-backup-20261104T120000Z.db.
	Name string
	// Size is how many bytes Body holds.
	Size int64
	// Body is the file. The caller must Close it, which removes the server's
	// staging copy.
	Body io.ReadCloser
}

// Backup makes a consistent copy of the database while kman runs (a VACUUM INTO,
// SPEC §3) and hands it over as a stream. The copy holds ciphertext only: it
// opens only with the master key, which is backed up separately. It is staged in
// the data directory, which only kman can read, and removed when the Download is
// closed. The file is the caller's to write, not the server's, so a remote admin
// cannot make the server write to a path it chooses. The backup is recorded in the
// audit log.
func (s *Service) Backup(ctx context.Context) (Download, error) {
	if _, err := caller(ctx); err != nil {
		return Download{}, err
	}
	now := s.now()
	stage, err := os.MkdirTemp(s.store.Dir(), "backup-")
	if err != nil {
		return Download{}, err
	}
	path := filepath.Join(stage, "kman.backup")
	fail := func(err error) (Download, error) {
		os.RemoveAll(stage)
		return Download{}, err
	}
	if err := s.store.Backup(ctx, path); err != nil {
		return fail(err)
	}
	err = s.command(ctx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		return []audit{{action: "backup.create", kind: "store", id: "install"}}, nil
	})
	if err != nil {
		return fail(err)
	}
	f, err := os.Open(path)
	if err != nil {
		return fail(err)
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return fail(err)
	}
	return Download{
		Name: "kman-backup-" + now.UTC().Format("20060102T150405Z") + ".db",
		Size: fi.Size(),
		Body: &stagedFile{File: f, dir: stage},
	}, nil
}

// stagedFile is a file whose directory goes with it when it is closed.
type stagedFile struct {
	*os.File
	dir string
}

func (f *stagedFile) Close() error {
	err := f.File.Close()
	if rerr := os.RemoveAll(f.dir); err == nil {
		err = rerr
	}
	return err
}
