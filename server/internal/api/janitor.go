package api

import (
	"context"
	"time"

	"flowdrop/internal/auth"
	"flowdrop/internal/storage"
	"flowdrop/internal/store"
)

// Janitor performs periodic housekeeping: expired sessions, unreferenced blob
// rows/files, and stale upload temp files. It never deletes files that are
// part of an in-flight upload (storage tracks those as active).
type Janitor struct {
	Sessions *auth.Store
	Store    *store.Store
	Storage  *storage.Store
	Logger   interface {
		Info(string, ...any)
		Warn(string, ...any)
		Error(string, ...any)
	}
	// GracePeriod protects a blob that was just moved into place but whose
	// metadata transaction has not committed yet.
	GracePeriod time.Duration
	TempMaxAge  time.Duration
}

func (j *Janitor) grace() time.Duration {
	if j.GracePeriod <= 0 {
		return 10 * time.Minute
	}
	return j.GracePeriod
}

func (j *Janitor) tempMaxAge() time.Duration {
	if j.TempMaxAge <= 0 {
		return 6 * time.Hour
	}
	return j.TempMaxAge
}

// RunOnce performs one cleanup pass. Individual failures are logged and do not
// abort the rest of the pass.
func (j *Janitor) RunOnce(ctx context.Context) {
	if n, err := j.Sessions.CleanupExpired(); err != nil {
		j.Logger.Error("janitor: sessions", "err", err)
	} else if n > 0 {
		j.Logger.Info("janitor: removed expired sessions", "count", n)
	}

	// 1. Unreferenced blob rows (crash between file commit and DB commit, or a
	//    failed compensating delete).
	cutoff := time.Now().Add(-j.grace())
	orphans, err := j.Store.OrphanBlobs(cutoff)
	if err != nil {
		j.Logger.Error("janitor: query orphan blobs", "err", err)
	} else {
		for _, b := range orphans {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if err := j.Store.DeleteBlobRow(b.ID); err != nil {
				j.Logger.Warn("janitor: delete blob row", "err", err)
				continue
			}
			if err := j.Storage.RemoveBlob(b.StorageKey); err != nil {
				j.Logger.Warn("janitor: delete blob file", "err", err)
				continue
			}
			j.Logger.Info("janitor: removed orphan blob", "size", b.SizeBytes)
		}
	}

	// 2. Files on disk with no DB row (e.g. row deleted but unlink failed).
	if keys, err := j.Store.KnownBlobKeys(); err != nil {
		j.Logger.Error("janitor: list known keys", "err", err)
	} else if n, err := j.Storage.RemoveUnknownBlobs(keys, cutoff); err != nil {
		j.Logger.Warn("janitor: remove unknown blobs", "err", err)
	} else if n > 0 {
		j.Logger.Info("janitor: removed unreferenced files", "count", n)
	}

	// 3. Stale upload temp files.
	if n, err := j.Storage.CleanupTemp(j.tempMaxAge()); err != nil {
		j.Logger.Warn("janitor: cleanup temp", "err", err)
	} else if n > 0 {
		j.Logger.Info("janitor: removed stale temp files", "count", n)
	}
}

// Run loops until ctx is cancelled.
func (j *Janitor) Run(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 15 * time.Minute
	}
	j.RunOnce(ctx)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			j.RunOnce(ctx)
		}
	}
}
