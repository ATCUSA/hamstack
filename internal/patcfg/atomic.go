package patcfg

import (
	"os"
	"path/filepath"
)

// writeAtomic replaces the file at path with data so that it is either the old file or the new one, never a part of
// either: a temporary file beside it, written and flushed to disk, then renamed over it. Pat's config holds the
// Winlink password, and a Pi loses power without warning; a write cut short left a file Pat could not read. A symlink
// is followed, so the file it points at is replaced and the link stays.
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // gone after the rename; left only when something failed
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil { // the rename itself, on disk
		_ = d.Sync()
		d.Close()
	}
	return nil
}
