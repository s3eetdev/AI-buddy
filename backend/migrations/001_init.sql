CREATE TABLE IF NOT EXISTS analyses (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), owner_profile text NOT NULL, jira_url text NOT NULL, task_id text NOT NULL,
 task_snapshot jsonb NOT NULL, status text NOT NULL, execution_mode text NOT NULL, current_result_revision integer,
 error_code text, error_message text, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS analysis_jobs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), analysis_id uuid NOT NULL REFERENCES analyses(id), input jsonb NOT NULL,
 state text NOT NULL DEFAULT 'queued', attempts integer NOT NULL DEFAULT 0, next_run_at timestamptz NOT NULL DEFAULT now(),
 lease_until timestamptz, claim_token uuid, error_code text, error_message text, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS one_active_job ON analysis_jobs(analysis_id) WHERE state IN ('queued','running');
CREATE INDEX IF NOT EXISTS pending_jobs ON analysis_jobs(state,next_run_at,lease_until);
CREATE TABLE IF NOT EXISTS analysis_results (
 analysis_id uuid NOT NULL REFERENCES analyses(id), revision integer NOT NULL, result jsonb NOT NULL, ai_request jsonb NOT NULL,
 corpus_digest text NOT NULL, execution_mode text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(analysis_id,revision)
);
CREATE TABLE IF NOT EXISTS analysis_answers (
 id bigserial PRIMARY KEY, analysis_id uuid NOT NULL REFERENCES analyses(id), answer text NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS step_progress (
 analysis_id uuid NOT NULL, revision integer NOT NULL, step_id text NOT NULL, completed boolean NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(analysis_id,revision,step_id),
 FOREIGN KEY(analysis_id,revision) REFERENCES analysis_results(analysis_id,revision)
);
CREATE TABLE IF NOT EXISTS idempotency_keys (
 owner_profile text NOT NULL, operation text NOT NULL, key text NOT NULL, payload_hash text NOT NULL,
 analysis_id uuid NOT NULL REFERENCES analyses(id), PRIMARY KEY(owner_profile,operation,key)
);
CREATE TABLE IF NOT EXISTS mock_jira_plans (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), owner_profile text NOT NULL, task_id text NOT NULL,
 analysis_id uuid NOT NULL, revision integer NOT NULL, jira_markdown text NOT NULL, snapshot jsonb NOT NULL,
 operation_key text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(analysis_id,revision) REFERENCES analysis_results(analysis_id,revision), UNIQUE(owner_profile,analysis_id,revision)
);
CREATE TABLE IF NOT EXISTS publications (
 owner_profile text NOT NULL, operation_key text NOT NULL, payload_hash text NOT NULL, analysis_id uuid NOT NULL,
 revision integer NOT NULL, status text NOT NULL, plan_id uuid REFERENCES mock_jira_plans(id), error text,
 created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(owner_profile,operation_key),
 FOREIGN KEY(analysis_id,revision) REFERENCES analysis_results(analysis_id,revision)
);
