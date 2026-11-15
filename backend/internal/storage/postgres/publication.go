package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ErrPublishFailed deliberately contains no database or provider details.
var ErrPublishFailed = errors.New("publication failed")

// A failure to write the mock plan should not roll back the publication receipt.
// The savepoint is inside the same transaction as the receipt, so successful
// publication still commits the plan and receipt together.
func failPublication(ctx context.Context, tx pgx.Tx, in PublishInput) error {
	if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT publication_write`); err != nil {
		return ErrPublishFailed
	}
	if _, err := tx.Exec(ctx, `UPDATE publications SET status='publish_failed',plan_id=NULL,error='publish_failed' WHERE owner_profile=$1 AND operation_key=$2`, in.Owner, in.IdempotencyKey); err != nil {
		return ErrPublishFailed
	}
	if err := tx.Commit(ctx); err != nil {
		return ErrPublishFailed
	}
	return ErrPublishFailed
}

func (s *Store) Publish(ctx context.Context, in PublishInput) (*Plan, error) {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	if e = lockKey(ctx, tx, in.Owner, "publish", in.IdempotencyKey); e != nil {
		return nil, e
	}
	var existing *string
	var hash, analysis, publicationStatus string
	var revision int
	e = tx.QueryRow(ctx, `SELECT plan_id::text,payload_hash,analysis_id::text,revision,status FROM publications WHERE owner_profile=$1 AND operation_key=$2`, in.Owner, in.IdempotencyKey).Scan(&existing, &hash, &analysis, &revision, &publicationStatus)
	if e == nil {
		if hash != in.PayloadHash || analysis != in.AnalysisID || revision != in.Revision {
			return nil, ErrConflict
		}
		if publicationStatus == "published" && existing != nil {
			if e = tx.Commit(ctx); e != nil {
				return nil, e
			}
			return s.GetPlan(ctx, in.Owner, *existing)
		}
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return nil, e
	}
	var status, task string
	var rev *int
	e = tx.QueryRow(ctx, `SELECT status,current_result_revision,task_id FROM analyses WHERE id::text=$1 AND owner_profile=$2 FOR UPDATE`, in.AnalysisID, in.Owner).Scan(&status, &rev, &task)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	if status != "ready" || rev == nil || *rev != in.Revision {
		return nil, ErrConflict
	}
	var result []byte
	e = tx.QueryRow(ctx, `SELECT result FROM analysis_results WHERE analysis_id=$1 AND revision=$2`, in.AnalysisID, in.Revision).Scan(&result)
	if e != nil {
		return nil, e
	}
	var v struct {
		Status   string            `json:"status"`
		Markdown string            `json:"jira_markdown"`
		Sources  []json.RawMessage `json:"sources"`
		Plan     struct {
			Findings  []json.RawMessage `json:"findings"`
			Steps     []json.RawMessage `json:"steps"`
			Questions []string          `json:"questions"`
		} `json:"plan"`
	}
	if e = json.Unmarshal(result, &v); e != nil {
		return nil, e
	}
	if v.Status != "ready" || len(v.Plan.Findings) == 0 || len(v.Plan.Steps) == 0 || len(v.Plan.Questions) > 0 || len(v.Sources) == 0 {
		return nil, ErrConflict
	}
	_, e = tx.Exec(ctx, `INSERT INTO publications(owner_profile,operation_key,payload_hash,analysis_id,revision,status) VALUES($1,$2,$3,$4,$5,'publishing') ON CONFLICT(owner_profile,operation_key) DO UPDATE SET status='publishing',error=NULL`, in.Owner, in.IdempotencyKey, in.PayloadHash, in.AnalysisID, in.Revision)
	if e != nil {
		return nil, ErrPublishFailed
	}
	if _, e = tx.Exec(ctx, `SAVEPOINT publication_write`); e != nil {
		return nil, ErrPublishFailed
	}
	var planID string
	e = tx.QueryRow(ctx, `INSERT INTO mock_jira_plans(owner_profile,task_id,analysis_id,revision,jira_markdown,snapshot,operation_key) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(owner_profile,analysis_id,revision) DO UPDATE SET analysis_id=excluded.analysis_id RETURNING id::text`, in.Owner, task, in.AnalysisID, in.Revision, v.Markdown, result, in.IdempotencyKey).Scan(&planID)
	if e != nil {
		return nil, failPublication(ctx, tx, in)
	}
	_, e = tx.Exec(ctx, `UPDATE publications SET status='published',plan_id=$3,error=NULL WHERE owner_profile=$1 AND operation_key=$2`, in.Owner, in.IdempotencyKey, planID)
	if e != nil {
		return nil, failPublication(ctx, tx, in)
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, ErrPublishFailed
	}
	return s.GetPlan(ctx, in.Owner, planID)
}

// Plans take precedence over failures from other operation keys for this version.
func (s *Store) getPublication(ctx context.Context, owner, id string, revision int) (Publication, error) {
	p := Publication{Status: "not_published"}
	var plan, status *string
	err := s.Pool.QueryRow(ctx, `SELECT
		(SELECT id::text FROM mock_jira_plans WHERE owner_profile=$1 AND analysis_id=$2 AND revision=$3),
		(SELECT status FROM publications WHERE owner_profile=$1 AND analysis_id=$2 AND revision=$3 ORDER BY created_at DESC, operation_key DESC LIMIT 1)`, owner, id, revision).Scan(&plan, &status)
	if err != nil {
		return p, err
	}
	if plan != nil {
		return Publication{Status: "published", PlanID: *plan, URL: "/api/v1/mock-jira/plans/" + *plan}, nil
	}
	if status == nil {
		return p, nil
	}
	p.Status = *status
	if *status == "publish_failed" {
		p.Error = &PublicError{Code: "publish_failed", Message: "Не удалось сохранить план. Повторите публикацию."}
	}
	return p, nil
}
func (s *Store) GetPlan(ctx context.Context, owner, id string) (*Plan, error) {
	p := &Plan{}
	e := s.Pool.QueryRow(ctx, `SELECT id::text,owner_profile,task_id,analysis_id::text,revision,jira_markdown,snapshot,created_at FROM mock_jira_plans WHERE id::text=$1 AND owner_profile=$2`, id, owner).Scan(&p.ID, &p.Owner, &p.TaskID, &p.AnalysisID, &p.Revision, &p.Markdown, &p.Snapshot, &p.CreatedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	return p, nil
}
