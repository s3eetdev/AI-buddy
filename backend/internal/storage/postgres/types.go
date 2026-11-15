package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrConflict  = errors.New("conflict")
	ErrLeaseLost = errors.New("lease lost")
)

type Store struct{ Pool *pgxpool.Pool }

type Analysis struct {
	ID             string          `json:"analysis_id"`
	Owner          string          `json:"-"`
	JiraURL        string          `json:"jira_url"`
	TaskID         string          `json:"task_id"`
	TaskSnapshot   json.RawMessage `json:"-"`
	Status         string          `json:"status"`
	ExecutionMode  string          `json:"execution_mode"`
	ResultRevision *int            `json:"result_revision"`
	Result         json.RawMessage `json:"result"`
	AIRequest      json.RawMessage `json:"-"`
	CorpusDigest   string          `json:"-"`
	StepProgress   []Progress      `json:"step_progress"`
	Publication    Publication     `json:"publication"`
	Error          *PublicError    `json:"error"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}
type PublicError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type Progress struct {
	StepID         string    `json:"step_id"`
	ResultRevision int       `json:"result_revision"`
	Completed      bool      `json:"completed"`
	UpdatedAt      time.Time `json:"updated_at"`
}
type Publication struct {
	Status string       `json:"status"`
	PlanID string       `json:"plan_id,omitempty"`
	URL    string       `json:"url,omitempty"`
	Error  *PublicError `json:"error,omitempty"`
}
type CreateInput struct {
	ID, JobID, Owner, JiraURL, TaskID, IdempotencyKey, PayloadHash, ExecutionMode string
	TaskSnapshot, AIRequest                                                       json.RawMessage
}
type AnswerInput struct {
	AnalysisID, JobID, Owner, IdempotencyKey, PayloadHash, Answer string
	AIRequest                                                     json.RawMessage
	ExpectedRevision                                              int
}
type Job struct {
	ID, AnalysisID, Owner, ClaimToken string
	Attempt                           int
	Input, TaskSnapshot               json.RawMessage
	ExecutionMode                     string
}
type Completion struct {
	Result, AIRequest                   json.RawMessage
	CorpusDigest, Status, ExecutionMode string
}
type PublishInput struct {
	AnalysisID, Owner, IdempotencyKey, PayloadHash, Markdown string
	Revision                                                 int
	Snapshot                                                 json.RawMessage
}
type Plan struct {
	ID         string          `json:"plan_id"`
	Owner      string          `json:"-"`
	TaskID     string          `json:"task_id"`
	AnalysisID string          `json:"analysis_id"`
	Revision   int             `json:"result_revision"`
	Markdown   string          `json:"jira_markdown"`
	Snapshot   json.RawMessage `json:"result"`
	CreatedAt  time.Time       `json:"created_at"`
}

type WorkerStore interface {
	ClaimJob(context.Context, time.Duration, int) (*Job, error)
	Heartbeat(context.Context, Job, time.Duration) error
	FinishJob(context.Context, Job, Completion) error
	FailJob(context.Context, Job, PublicError, bool, time.Duration, int) error
}
