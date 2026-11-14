from typing import Literal

from pydantic import BaseModel, ConfigDict, Field, model_validator


class StrictModel(BaseModel):
    model_config = ConfigDict(extra="forbid", str_strip_whitespace=True)


class AnalyzeRequest(StrictModel):
    task_id: str = Field(min_length=1, max_length=100)
    title: str = Field(min_length=1, max_length=500)
    description: str = Field(default="", max_length=16000)
    acceptance_criteria: list[str] = Field(default_factory=list, max_length=30)
    allowed_scopes: list[str] = Field(max_length=50)
    question: str = Field(default="", max_length=2000)

    @model_validator(mode="after")
    def bound_task_context(self):
        if (
            sum(
                map(
                    len,
                    [
                        self.title,
                        self.description,
                        self.question,
                        *self.acceptance_criteria,
                    ],
                )
            )
            > 28000
        ):
            raise ValueError("Combined task context must not exceed 28000 characters")
        return self


class Citation(StrictModel):
    source_id: str
    start_line: int = Field(ge=1)
    end_line: int = Field(ge=1)
    quote: str = Field(min_length=1, max_length=3000)


class Finding(StrictModel):
    kind: Literal["fact", "hypothesis"]
    role: Literal["entry_point", "change_candidate", "context", "relationship"]
    explanation: str = Field(min_length=1, max_length=3000)
    citations: list[Citation] = Field(default_factory=list, max_length=8)

    @model_validator(mode="after")
    def fact_needs_evidence(self):
        if self.kind == "fact" and not self.citations:
            raise ValueError("Facts require evidence")
        return self


class Step(StrictModel):
    action: str = Field(min_length=1, max_length=2000)
    source_ids: list[str] = Field(default_factory=list, max_length=10)
    expected_result: str = Field(min_length=1, max_length=2000)


class Plan(StrictModel):
    summary: str = Field(min_length=1, max_length=4000)
    findings: list[Finding] = Field(max_length=20)
    steps: list[Step] = Field(max_length=12)
    questions: list[str] = Field(max_length=12)
    limitations: list[str] = Field(max_length=12)


class Source(StrictModel):
    # Preserve whitespace so citation line numbers match the files exactly.
    model_config = ConfigDict(extra="forbid", str_strip_whitespace=False)

    id: str
    scope: str
    path: str
    kind: Literal["code", "documentation", "contract", "test", "catalog"]
    content: str = Field(min_length=1, max_length=30000)


class AnalyzeResponse(StrictModel):
    task_id: str
    status: Literal["ready", "needs_clarification", "insufficient_context"]
    plan: Plan
    sources: list[Source]
    model: str | None
    usage: dict[str, int]
    jira_markdown: str
