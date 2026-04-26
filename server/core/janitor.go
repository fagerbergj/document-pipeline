package core

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// JanitorService periodically deletes leftover temp upload files and prunes
// stage-output artifacts that have been superseded by a newer run.
type JanitorService struct {
	vaultPath string
	jobs      port.JobRepo
	artifacts port.ArtifactRepo
	store     port.DocumentArtifactStore

	tmpMaxAge      time.Duration // how old a <vault>/tmp file must be before deletion
	tmpInterval    time.Duration // tmp sweep cadence
	orphanMaxAge   time.Duration // grace period before a superseded artifact is purged
	orphanInterval time.Duration // orphan-artifact sweep cadence
}

// NewJanitorService constructs a janitor with sensible defaults.
// jobs/artifacts/store may be nil — in that case orphan-artifact sweeping is
// skipped (only the tmp sweep runs). Tests that don't care about cleanup
// often pass nil.
func NewJanitorService(vaultPath string, jobs port.JobRepo, artifacts port.ArtifactRepo, store port.DocumentArtifactStore) *JanitorService {
	return &JanitorService{
		vaultPath:      vaultPath,
		jobs:           jobs,
		artifacts:      artifacts,
		store:          store,
		tmpMaxAge:      1 * time.Hour,
		tmpInterval:    15 * time.Minute,
		orphanMaxAge:   7 * 24 * time.Hour, // soft delete: keep history a week
		orphanInterval: 24 * time.Hour,
	}
}

// Run blocks until ctx is cancelled. Both sweeps tick on independent intervals.
func (s *JanitorService) Run(ctx context.Context) {
	s.sweepTmp()
	if s.canSweepOrphans() {
		s.sweepOrphanArtifacts(ctx)
	}
	tmpTicker := time.NewTicker(s.tmpInterval)
	defer tmpTicker.Stop()
	orphanTicker := time.NewTicker(s.orphanInterval)
	defer orphanTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tmpTicker.C:
			s.sweepTmp()
		case <-orphanTicker.C:
			if s.canSweepOrphans() {
				s.sweepOrphanArtifacts(ctx)
			}
		}
	}
}

func (s *JanitorService) canSweepOrphans() bool {
	return s.jobs != nil && s.artifacts != nil && s.store != nil
}

func (s *JanitorService) sweepTmp() {
	dir := filepath.Join(s.vaultPath, "tmp")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("janitor: read tmp dir", "err", err)
		}
		return
	}
	cutoff := time.Now().Add(-s.tmpMaxAge)
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

// pageSize controls how many rows the janitor pulls at once from the repos.
// Small enough to bound memory; large enough to avoid round-trip overhead.
const janitorPageSize = 200

// sweepOrphanArtifacts deletes stage-output artifacts (and their backing files)
// that no longer appear in the latest run of any job, after a grace period.
//
// Both jobs and artifacts are walked via cursor pagination so memory stays flat
// regardless of how big the tables grow. The "live" set is the union of every
// Field.ArtifactID in the latest run of every job; anything created by a job
// (CreatedJobID != nil) older than orphanMaxAge and not in the live set is
// superseded and gets pruned.
func (s *JanitorService) sweepOrphanArtifacts(ctx context.Context) {
	live, err := s.collectLiveArtifactIDs(ctx)
	if err != nil {
		slog.Warn("janitor: collect live set", "err", err)
		return
	}

	cutoff := time.Now().Add(-s.orphanMaxAge)
	deleted, examined := 0, 0
	var token *model.PageToken
	for {
		page, err := s.artifacts.ListPaginated(ctx, port.ArtifactFilter{}, model.PageRequest{PageSize: janitorPageSize, PageToken: token})
		if err != nil {
			slog.Warn("janitor: list artifacts page", "err", err)
			return
		}
		for _, a := range page.Data {
			examined++
			if a.CreatedJobID == nil { // source uploads are never swept
				continue
			}
			if a.CreatedAt.After(cutoff) { // still within grace period
				continue
			}
			if _, isLive := live[a.ID]; isLive {
				continue
			}
			if err := s.removeArtifactFile(a); err != nil {
				slog.Warn("janitor: remove artifact file", "artifact_id", a.ID[:8], "err", err)
			}
			if err := s.artifacts.Delete(ctx, a.ID); err != nil {
				slog.Warn("janitor: delete artifact row", "artifact_id", a.ID[:8], "err", err)
				continue
			}
			deleted++
		}
		if page.NextPageToken == nil {
			break
		}
		t, err := DecodePageToken(*page.NextPageToken)
		if err != nil {
			slog.Warn("janitor: decode artifact page token", "err", err)
			return
		}
		token = &t
	}
	if deleted > 0 {
		slog.Info("janitor: pruned superseded artifacts", "deleted", deleted, "live", len(live), "examined", examined)
	}
}

// collectLiveArtifactIDs walks every job (paginated) and returns the union of
// ArtifactIDs referenced by the latest run's Inputs and Outputs.
func (s *JanitorService) collectLiveArtifactIDs(ctx context.Context) (map[string]struct{}, error) {
	live := make(map[string]struct{})
	var token *model.PageToken
	for {
		page, err := s.jobs.ListPaginated(ctx, port.JobFilter{}, model.PageRequest{PageSize: janitorPageSize, PageToken: token})
		if err != nil {
			return nil, err
		}
		for _, j := range page.Data {
			if len(j.Runs) == 0 {
				continue
			}
			latest := j.Runs[len(j.Runs)-1]
			for _, f := range latest.Inputs {
				if f.ArtifactID != "" {
					live[f.ArtifactID] = struct{}{}
				}
			}
			for _, f := range latest.Outputs {
				if f.ArtifactID != "" {
					live[f.ArtifactID] = struct{}{}
				}
			}
		}
		if page.NextPageToken == nil {
			return live, nil
		}
		t, err := DecodePageToken(*page.NextPageToken)
		if err != nil {
			return nil, err
		}
		token = &t
	}
}

func (s *JanitorService) removeArtifactFile(a model.Artifact) error {
	if a.Path != nil && *a.Path != "" {
		return os.Remove(filepath.Join(s.vaultPath, *a.Path))
	}
	// Legacy layout: <vault>/artifacts/<id>/<filename>.
	dir := filepath.Join(s.vaultPath, "artifacts", a.ID)
	return os.RemoveAll(dir)
}
