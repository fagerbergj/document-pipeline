package core

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// JanitorService periodically deletes leftover temp upload files. The streaming
// upload endpoints write to <vault>/tmp/<uuid>.bin while the body is being
// received; on success the file is renamed into place, on cancel/error it is
// deleted by the handler. This sweep handles the residue from crashes mid-upload.
type JanitorService struct {
	vaultPath string
	maxAge    time.Duration
	interval  time.Duration
}

func NewJanitorService(vaultPath string) *JanitorService {
	return &JanitorService{
		vaultPath: vaultPath,
		maxAge:    1 * time.Hour,
		interval:  15 * time.Minute,
	}
}

// Run blocks until ctx is cancelled, sweeping <vault>/tmp on a fixed interval.
func (s *JanitorService) Run(ctx context.Context) {
	s.sweep()
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweep()
		}
	}
}

func (s *JanitorService) sweep() {
	dir := filepath.Join(s.vaultPath, "tmp")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("janitor: read tmp dir", "err", err)
		}
		return
	}
	cutoff := time.Now().Add(-s.maxAge)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			path := filepath.Join(dir, e.Name())
			if err := os.Remove(path); err != nil {
				slog.Warn("janitor: remove tmp file", "path", path, "err", err)
			}
		}
	}
}
