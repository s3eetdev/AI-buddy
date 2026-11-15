package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Store) ClaimJob(ctx context.Context, lease time.Duration, maxAttempts int) (*Job, error) {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	// Exhausted leases must not be reclaimed indefinitely after process restarts.
	_, e = tx.Exec(ctx, `WITH expired AS (UPDATE analysis_jobs SET state='failed',error_code='job_attempts_exhausted',error_message='Analysis could not be completed' WHERE state='running' AND lease_until<=now() AND attempts >= $1 RETURNING analysis_id) UPDATE analyses SET status='failed',error_code='job_attempts_exhausted',error_message='Analysis could not be completed',updated_at=now() WHERE id IN (SELECT analysis_id FROM expired)`, maxAttempts)
	if e != nil {
		return nil, e
	}
	j := &Job{}
	e = tx.QueryRow(ctx, `WITH candidate AS (SELECT id FROM analysis_jobs WHERE attempts<$1 AND ((state='queued' AND next_run_at<=now()) OR (state='running' AND lease_until<=now())) ORDER BY next_run_at,created_at FOR UPDATE SKIP LOCKED LIMIT 1), claimed AS (UPDATE analysis_jobs j SET state='running',attempts=j.attempts+1,claim_token=gen_random_uuid(),lease_until=now()+$2 * interval '1 second' FROM candidate c WHERE j.id=c.id RETURNING j.*) SELECT j.id::text,j.analysis_id::text,a.owner_profile,j.claim_token::text,j.attempts,j.input,a.task_snapshot,a.execution_mode FROM claimed j JOIN analyses a ON a.id=j.analysis_id`, maxAttempts, lease.Seconds()).Scan(&j.ID, &j.AnalysisID, &j.Owner, &j.ClaimToken, &j.Attempt, &j.Input, &j.TaskSnapshot, &j.ExecutionMode)
	if errors.Is(e, pgx.ErrNoRows) {
		if e = tx.Commit(ctx); e != nil {
			return nil, e
		}
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	_, e = tx.Exec(ctx, `UPDATE analyses SET status='collecting_context',error_code=NULL,error_message=NULL,updated_at=now() WHERE id=$1`, j.AnalysisID)
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return j, nil
}
func (s *Store) Heartbeat(ctx context.Context, j Job, lease time.Duration) error {
	tag, e := s.Pool.Exec(ctx, `UPDATE analysis_jobs SET lease_until=now()+$4 * interval '1 second' WHERE id=$1 AND claim_token=$2 AND attempts=$3 AND state='running' AND lease_until>now()`, j.ID, j.ClaimToken, j.Attempt, lease.Seconds())
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}
func (s *Store) FinishJob(ctx context.Context, j Job, c Completion) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var id string
	e = tx.QueryRow(ctx, `SELECT analysis_id::text FROM analysis_jobs WHERE id=$1 AND claim_token=$2 AND attempts=$3 AND state='running' AND lease_until>now() FOR UPDATE`, j.ID, j.ClaimToken, j.Attempt).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if e != nil {
		return e
	}
	if id != j.AnalysisID {
		return ErrLeaseLost
	}
	var rev int
	e = tx.QueryRow(ctx, `SELECT COALESCE(current_result_revision,0)+1 FROM analyses WHERE id=$1 FOR UPDATE`, id).Scan(&rev)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO analysis_results(analysis_id,revision,result,ai_request,corpus_digest,execution_mode) VALUES($1,$2,$3,$4,$5,$6)`, id, rev, c.Result, c.AIRequest, c.CorpusDigest, c.ExecutionMode)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `UPDATE analysis_jobs SET state='done',lease_until=NULL WHERE id=$1`, j.ID)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `UPDATE analyses SET status=$2,current_result_revision=$3,error_code=NULL,error_message=NULL,updated_at=now() WHERE id=$1`, id, c.Status, rev)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) FailJob(ctx context.Context, j Job, fail PublicError, retry bool, backoff time.Duration, maxAttempts int) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var id string
	e = tx.QueryRow(ctx, `SELECT analysis_id::text FROM analysis_jobs WHERE id=$1 AND claim_token=$2 AND attempts=$3 AND state='running' AND lease_until>now() FOR UPDATE`, j.ID, j.ClaimToken, j.Attempt).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if e != nil {
		return e
	}
	if id != j.AnalysisID {
		return ErrLeaseLost
	}
	if retry && j.Attempt < maxAttempts {
		_, e = tx.Exec(ctx, `UPDATE analysis_jobs SET state='queued',next_run_at=now()+$2 * interval '1 second',lease_until=NULL,error_code=$3,error_message=$4 WHERE id=$1`, j.ID, backoff.Seconds(), fail.Code, fail.Message)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `UPDATE analyses SET status='queued',updated_at=now() WHERE id=$1`, id)
	} else {
		_, e = tx.Exec(ctx, `UPDATE analysis_jobs SET state='failed',lease_until=NULL,error_code=$2,error_message=$3 WHERE id=$1`, j.ID, fail.Code, fail.Message)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `UPDATE analyses SET status='failed',error_code=$2,error_message=$3,updated_at=now() WHERE id=$1`, id, fail.Code, fail.Message)
	}
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
