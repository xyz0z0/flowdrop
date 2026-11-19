package main

import (
	"archive/tar"
	"compress/gzip"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// runBackup writes a gzip-compressed tar of a consistent snapshot: the SQLite
// database (via VACUUM INTO, which is safe while the hub is running) plus the
// blobs and tmp directories.
func runBackup(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: flowdrop backup OUT.tar.gz")
	}
	out := args[0]
	cfg, err := loadConfig(nil)
	if err != nil {
		return err
	}
	// Open the DB just to take a consistent snapshot; do not keep it locked.
	conn, err := sql.Open("sqlite", "file:"+filepath.Join(cfg.DataDir, "flowdrop.db")+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer conn.Close()
	if err := conn.Ping(); err != nil {
		return fmt.Errorf("database not readable (is the hub initialized?): %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return err
	}
	tmpDB, err := os.CreateTemp("", "flowdrop-snapshot-*.db")
	if err != nil {
		return err
	}
	tmpDB.Close()
	defer os.Remove(tmpDB.Name())

	if _, err := conn.Exec(`VACUUM INTO ?`, tmpDB.Name()); err != nil {
		return fmt.Errorf("snapshot database: %w", err)
	}

	f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	if err := addFileToTar(tw, tmpDB.Name(), "flowdrop.db"); err != nil {
		return err
	}
	for _, dir := range []string{"blobs", "tmp"} {
		root := filepath.Join(cfg.DataDir, dir)
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(cfg.DataDir, p)
			if err != nil {
				return err
			}
			return addFileToTar(tw, p, filepath.ToSlash(rel))
		})
		if err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	fmt.Printf("Backup written: %s (%s)\n", out, humanBytes(fi.Size()))
	fmt.Println("Restore with: stop the service, extract over the data dir, then start it.")
	return nil
}

func addFileToTar(tw *tar.Writer, path, name string) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	hdr := &tar.Header{
		Name:    strings.TrimPrefix(name, "/"),
		Mode:    0o600,
		Size:    info.Size(),
		ModTime: time.Now(),
		Format:  tar.FormatPAX,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}
