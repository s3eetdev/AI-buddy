package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/s3eetdev/AI-buddy/backend/migrations"
)

func Open(ctx context.Context, url string) (*Store, error) {
	p, e := pgxpool.New(ctx, url)
	if e != nil {
		return nil, e
	}
	if e = p.Ping(ctx); e != nil {
		p.Close()
		return nil, e
	}
	return &Store{Pool: p}, nil
}
func (s *Store) Close() { s.Pool.Close() }

// IdempotentAnalysis returns an existing operation before validating state that may
// have changed since its first execution. Concurrent first calls are serialized
// again in the mutation transaction.
func (s *Store) IdempotentAnalysis(ctx context.Context, owner, operation, key, payloadHash, analysisID string) (*Analysis, error) {
	var id, stored string
	err := s.Pool.QueryRow(ctx, `SELECT analysis_id::text,payload_hash FROM idempotency_keys WHERE owner_profile=$1 AND operation=$2 AND key=$3`, owner, operation, key).Scan(&id, &stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if stored != payloadHash || (analysisID != "" && analysisID != id) {
		return nil, ErrConflict
	}
	return s.GetAnalysis(ctx, owner, id, nil)
}
func (s *Store) Migrate(ctx context.Context) error {
	_, e := s.Pool.Exec(ctx, migrations.Initial)
	return e
}
func (s *Store) Ready(ctx context.Context) error {
	var ok bool
	e := s.Pool.QueryRow(ctx, `SELECT NOT EXISTS (
		SELECT 1 FROM unnest(ARRAY['analyses','analysis_jobs','analysis_results',
		'analysis_answers','step_progress','idempotency_keys','publications','mock_jira_plans']) AS required(name)
		WHERE to_regclass(required.name) IS NULL
	)`).Scan(&ok)
	if e != nil {
		return e
	}
	if !ok {
		return errors.New("database schema missing")
	}
	return nil
}
func lockKey(ctx context.Context, tx pgx.Tx, owner, operation, key string) error {
	_, e := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, owner+"\x1f"+operation+"\x1f"+key)
	return e
}
func checkKey(ctx context.Context, tx pgx.Tx, owner, operation, key, hash string) (string, error) {
	var id, stored string
	e := tx.QueryRow(ctx, `SELECT analysis_id::text,payload_hash FROM idempotency_keys WHERE owner_profile=$1 AND operation=$2 AND key=$3`, owner, operation, key).Scan(&id, &stored)
	if errors.Is(e, pgx.ErrNoRows) {
		return "", nil
	}
	if e != nil {
		return "", e
	}
	if stored != hash {
		return "", ErrConflict
	}
	return id, nil
}
func (s *Store) CreateAnalysis(ctx context.Context, in CreateInput) (*Analysis, error) {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	if e = lockKey(ctx, tx, in.Owner, "create", in.IdempotencyKey); e != nil {
		return nil, e
	}
	id, e := checkKey(ctx, tx, in.Owner, "create", in.IdempotencyKey, in.PayloadHash)
	if e != nil {
		return nil, e
	}
	if id == "" {
		e = tx.QueryRow(ctx, `INSERT INTO analyses(id,owner_profile,jira_url,task_id,task_snapshot,status,execution_mode) VALUES(COALESCE(NULLIF($1,'')::uuid,gen_random_uuid()),$2,$3,$4,$5,'queued',$6) RETURNING id::text`, in.ID, in.Owner, in.JiraURL, in.TaskID, in.TaskSnapshot, in.ExecutionMode).Scan(&id)
		if e != nil {
			return nil, e
		}
		_, e = tx.Exec(ctx, `INSERT INTO analysis_jobs(id,analysis_id,input) VALUES(COALESCE(NULLIF($1,'')::uuid,gen_random_uuid()),$2,$3)`, in.JobID, id, in.AIRequest)
		if e != nil {
			return nil, e
		}
		_, e = tx.Exec(ctx, `INSERT INTO idempotency_keys(owner_profile,operation,key,payload_hash,analysis_id) VALUES($1,'create',$2,$3,$4)`, in.Owner, in.IdempotencyKey, in.PayloadHash, id)
		if e != nil {
			return nil, e
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return s.GetAnalysis(ctx, in.Owner, id, nil)
}
func (s *Store) GetAnalysis(ctx context.Context, owner, id string, revision *int) (*Analysis, error) {
	a := &Analysis{StepProgress: []Progress{}, Publication: Publication{Status: "not_published"}}
	var code, msg *string
	e := s.Pool.QueryRow(ctx, `SELECT id::text,owner_profile,jira_url,task_id,task_snapshot,status,execution_mode,current_result_revision,error_code,error_message,created_at,updated_at FROM analyses WHERE id::text=$1 AND owner_profile=$2`, id, owner).Scan(&a.ID, &a.Owner, &a.JiraURL, &a.TaskID, &a.TaskSnapshot, &a.Status, &a.ExecutionMode, &a.ResultRevision, &code, &msg, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	if code != nil {
		a.Error = &PublicError{Code: *code}
		if msg != nil {
			a.Error.Message = *msg
		}
	}
	rev := a.ResultRevision
	if revision != nil {
		rev = revision
	}
	if rev != nil {
		e = s.Pool.QueryRow(ctx, `SELECT result,ai_request,corpus_digest FROM analysis_results WHERE analysis_id=$1 AND revision=$2`, id, *rev).Scan(&a.Result, &a.AIRequest, &a.CorpusDigest)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		if e != nil {
			return nil, e
		}
		a.ResultRevision = rev
		rows, e := s.Pool.Query(ctx, `SELECT step_id,revision,completed,updated_at FROM step_progress WHERE analysis_id=$1 AND revision=$2 ORDER BY step_id`, id, *rev)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		for rows.Next() {
			var p Progress
			if e = rows.Scan(&p.StepID, &p.ResultRevision, &p.Completed, &p.UpdatedAt); e != nil {
				return nil, e
			}
			a.StepProgress = append(a.StepProgress, p)
		}
		if e = rows.Err(); e != nil {
			return nil, e
		}
		a.Publication, e = s.getPublication(ctx, owner, id, *rev)
		if e != nil {
			return nil, e
		}
	} else {
		// Before the first result, keep the attempt input available to internal callers.
		e = s.Pool.QueryRow(ctx, `SELECT input FROM analysis_jobs WHERE analysis_id=$1 ORDER BY created_at DESC LIMIT 1`, id).Scan(&a.AIRequest)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return nil, e
		}
	}
	return a, nil
}
func (s *Store) ListAnalyses(ctx context.Context, owner string, limit, offset int) ([]Analysis, error) {
	rows, e := s.Pool.Query(ctx, `SELECT id::text FROM analyses WHERE owner_profile=$1 ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3`, owner, limit, offset)
	if e != nil {
		return nil, e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	result := []Analysis{}
	for _, id := range ids {
		a, e := s.GetAnalysis(ctx, owner, id, nil)
		if e != nil {
			return nil, e
		}
		result = append(result, *a)
	}
	return result, nil
}
func (s *Store) SubmitAnswer(ctx context.Context, in AnswerInput) (*Analysis, error) {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	if e = lockKey(ctx, tx, in.Owner, "answer", in.IdempotencyKey); e != nil {
		return nil, e
	}
	id, e := checkKey(ctx, tx, in.Owner, "answer", in.IdempotencyKey, in.PayloadHash)
	if e != nil {
		return nil, e
	}
	if id != "" {
		if id != in.AnalysisID {
			return nil, ErrConflict
		}
		if e = tx.Commit(ctx); e != nil {
			return nil, e
		}
		return s.GetAnalysis(ctx, in.Owner, id, nil)
	}
	var status string
	var revision *int
	e = tx.QueryRow(ctx, `SELECT status,current_result_revision FROM analyses WHERE id::text=$1 AND owner_profile=$2 FOR UPDATE`, in.AnalysisID, in.Owner).Scan(&status, &revision)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	if status != "needs_clarification" || revision == nil || *revision != in.ExpectedRevision {
		return nil, ErrConflict
	}
	_, e = tx.Exec(ctx, `INSERT INTO analysis_answers(analysis_id,answer) VALUES($1,$2)`, in.AnalysisID, in.Answer)
	if e != nil {
		return nil, e
	}
	_, e = tx.Exec(ctx, `INSERT INTO analysis_jobs(id,analysis_id,input) VALUES(COALESCE(NULLIF($1,'')::uuid,gen_random_uuid()),$2,$3)`, in.JobID, in.AnalysisID, in.AIRequest)
	if e != nil {
		return nil, e
	}
	_, e = tx.Exec(ctx, `UPDATE analyses SET status='queued',error_code=NULL,error_message=NULL,updated_at=now() WHERE id=$1`, in.AnalysisID)
	if e != nil {
		return nil, e
	}
	_, e = tx.Exec(ctx, `INSERT INTO idempotency_keys(owner_profile,operation,key,payload_hash,analysis_id) VALUES($1,'answer',$2,$3,$4)`, in.Owner, in.IdempotencyKey, in.PayloadHash, in.AnalysisID)
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return s.GetAnalysis(ctx, in.Owner, in.AnalysisID, nil)
}
func (s *Store) SetProgress(ctx context.Context, owner, id string, revision int, stepID string, completed bool) (*Progress, error) {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	var rev *int
	var result []byte
	e = tx.QueryRow(ctx, `SELECT current_result_revision FROM analyses WHERE id::text=$1 AND owner_profile=$2 FOR UPDATE`, id, owner).Scan(&rev)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	if rev == nil || *rev != revision {
		return nil, ErrConflict
	}
	if e = tx.QueryRow(ctx, `SELECT result FROM analysis_results WHERE analysis_id=$1 AND revision=$2`, id, revision).Scan(&result); e != nil {
		return nil, e
	}
	var v struct {
		Plan struct {
			Steps []json.RawMessage `json:"steps"`
		} `json:"plan"`
	}
	if e = json.Unmarshal(result, &v); e != nil {
		return nil, e
	}
	found := false
	for i := range v.Plan.Steps {
		if fmt.Sprintf("v%d-s%d", revision, i+1) == stepID {
			found = true
			break
		}
	}
	if !found {
		return nil, ErrNotFound
	}
	p := &Progress{StepID: stepID, ResultRevision: revision, Completed: completed}
	e = tx.QueryRow(ctx, `INSERT INTO step_progress(analysis_id,revision,step_id,completed) VALUES($1,$2,$3,$4) ON CONFLICT(analysis_id,revision,step_id) DO UPDATE SET completed=excluded.completed,updated_at=now() RETURNING updated_at`, id, revision, stepID, completed).Scan(&p.UpdatedAt)
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return p, nil
}
func (s *Store) Answers(ctx context.Context, owner, id string) ([]string, error) {
	rows, e := s.Pool.Query(ctx, `SELECT aa.answer FROM analysis_answers aa JOIN analyses a ON a.id=aa.analysis_id WHERE a.id::text=$1 AND a.owner_profile=$2 ORDER BY aa.id`, id, owner)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	a := []string{}
	for rows.Next() {
		var x string
		if e = rows.Scan(&x); e != nil {
			return nil, e
		}
		a = append(a, x)
	}
	return a, rows.Err()
}
