package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// JobRepo implements port.JobRepo against SQLite.
type JobRepo struct{ db *sql.DB }

var _ port.JobRepo = (*JobRepo)(nil)

func (r *JobRepo) Upsert(ctx context.Context, job model.Job) error {
	optsJSON, err := json.Marshal(job.Options)
	if err != nil {
		return fmt.Errorf("marshal options: %w", err)
	}
	runsJSON, err := json.Marshal(job.Runs)
	if err != nil {
		return fmt.Errorf("marshal runs: %w", err)
	}
	_, err = r.db.ExecContext(ctx, q["jobs.Upsert"],
		job.ID, job.DocumentID, job.Stage, string(job.Status),
		string(optsJSON), string(runsJSON),
		job.CreatedAt.UTC().Format(time.RFC3339Nano),
		job.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (r *JobRepo) GetByID(ctx context.Context, id string) (model.Job, error) {
	row := r.db.QueryRowContext(ctx, q["jobs.GetByID"], id)
	job, err := scanJob(row)
	if err == sql.ErrNoRows {
		return model.Job{}, fmt.Errorf("job not found: %s", id)
	}
	return job, err
}

func (r *JobRepo) GetByDocumentAndStage(ctx context.Context, documentID, stage string) (model.Job, bool, error) {
	row := r.db.QueryRowContext(ctx, q["jobs.GetByDocumentAndStage"], documentID, stage)
	job, err := scanJob(row)
	if err == sql.ErrNoRows {
		return model.Job{}, false, nil
	}
	if err != nil {
		return model.Job{}, false, err
	}
	return job, true, nil
}

func (r *JobRepo) UpdateStatus(ctx context.Context, id, status string, updatedAt time.Time) error {
	_, err := r.db.ExecContext(ctx, q["jobs.UpdateStatus"],
		status, updatedAt.UTC().Format(time.RFC3339Nano), id)
	return err
}

func (r *JobRepo) UpdateOptions(ctx context.Context, id string, opts model.JobOptions, updatedAt time.Time) error {
	b, err := json.Marshal(opts)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, q["jobs.UpdateOptions"],
		string(b), updatedAt.UTC().Format(time.RFC3339Nano), id)
	return err
}

func (r *JobRepo) UpdateRuns(ctx context.Context, id string, runs []model.Run, updatedAt time.Time) error {
	b, err := json.Marshal(runs)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, q["jobs.UpdateRuns"],
		string(b), updatedAt.UTC().Format(time.RFC3339Nano), id)
	return err
}

func (r *JobRepo) ListForDocument(ctx context.Context, documentID string) ([]model.Job, error) {
	rows, err := r.db.QueryContext(ctx, q["jobs.ListForDocument"], documentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

func (r *JobRepo) ListPending(ctx context.Context, stage string) ([]model.Job, error) {
	rows, err := r.db.QueryContext(ctx, q["jobs.ListPending"], stage)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

func (r *JobRepo) ListPaginated(ctx context.Context, filter port.JobFilter, page model.PageRequest) (model.PageResult[model.Job], error) {
	sc, ok := jobSortMap[filter.Sort]
	if !ok {
		sc = jobSortMap["pipeline"]
	}

	conditions := []string{}
	params := []any{}

	if len(filter.IDs) > 0 {
		conditions = append(conditions, "id IN "+inClause(len(filter.IDs)))
		for _, v := range filter.IDs {
			params = append(params, v)
		}
	}
	if len(filter.DocumentIDs) > 0 {
		conditions = append(conditions, "document_id IN "+inClause(len(filter.DocumentIDs)))
		for _, v := range filter.DocumentIDs {
			params = append(params, v)
		}
	}
	if len(filter.Stages) > 0 {
		conditions = append(conditions, "stage IN "+inClause(len(filter.Stages)))
		for _, v := range filter.Stages {
			params = append(params, v)
		}
	}
	if len(filter.Statuses) > 0 {
		conditions = append(conditions, "status IN "+inClause(len(filter.Statuses)))
		for _, v := range filter.Statuses {
			params = append(params, v)
		}
	}
	if page.PageToken != nil {
		conditions = append(conditions, sc.cursorWhere)
		params = append(params, page.PageToken.SortKey, page.PageToken.LastID)
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	limit := page.PageSize
	if limit <= 0 {
		limit = 50
	}
	params = append(params, limit+1)

	stmt := fmt.Sprintf("SELECT * FROM jobs %s ORDER BY %s LIMIT ?", where, sc.order)
	rows, err := r.db.QueryContext(ctx, stmt, params...)
	if err != nil {
		return model.PageResult[model.Job]{}, err
	}
	defer rows.Close()

	jobs, err := scanJobs(rows)
	if err != nil {
		return model.PageResult[model.Job]{}, err
	}

	hasMore := len(jobs) > limit
	if hasMore {
		jobs = jobs[:limit]
	}

	var nextToken *string
	if hasMore && len(jobs) > 0 {
		last := jobs[len(jobs)-1]
		nextToken = encodeToken(last.CreatedAt.UTC().Format(time.RFC3339Nano), last.ID)
	}

	return model.PageResult[model.Job]{Data: jobs, NextPageToken: nextToken}, nil
}

func (r *JobRepo) ResetRunning(ctx context.Context) (int, error) {
	res, err := r.db.ExecContext(ctx, q["jobs.ResetRunning"])
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (r *JobRepo) CascadeReplay(ctx context.Context, documentID, fromStage string, stageOrder []string, updatedAt time.Time) error {
	idx := -1
	for i, name := range stageOrder {
		if name == fromStage {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil
	}
	downstream := stageOrder[idx+1:]
	if len(downstream) == 0 {
		return nil
	}
	params := []any{updatedAt.UTC().Format(time.RFC3339Nano), documentID}
	for _, s := range downstream {
		params = append(params, s)
	}
	_, err := r.db.ExecContext(ctx,
		"UPDATE jobs SET status='pending', updated_at=? WHERE document_id=? AND stage IN "+inClause(len(downstream)),
		params...)
	return err
}

// ── scan helpers ──────────────────────────────────────────────────────────────

func scanJob(row rowScanner) (model.Job, error) {
	var (
		j         model.Job
		status    string
		optsJSON  string
		runsJSON  string
		createdAt string
		updatedAt string
	)
	err := row.Scan(
		&j.ID, &j.DocumentID, &j.Stage, &status,
		&optsJSON, &runsJSON, &createdAt, &updatedAt,
	)
	if err != nil {
		return model.Job{}, err
	}
	j.Status = model.JobStatus(status)
	if optsJSON != "" {
		json.Unmarshal([]byte(optsJSON), &j.Options)
	}
	if runsJSON != "" {
		json.Unmarshal([]byte(runsJSON), &j.Runs)
	}
	if j.Runs == nil {
		j.Runs = []model.Run{}
	}
	j.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	j.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return j, nil
}

func scanJobs(rows *sql.Rows) ([]model.Job, error) {
	var jobs []model.Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}
