import asyncio
import copy
import json
from pathlib import Path
from unittest.mock import patch

import httpx
import pytest
from fastapi.testclient import TestClient

from navigator.api import create_app
from navigator.config import Settings
from navigator.llm import LLM, ProviderError
from navigator.models import AnalyzeRequest, Plan
from navigator.retrieval import Corpus
from navigator.service import Navigator, resolve_citation_lines, validate_evidence


def run(coro):
    return asyncio.run(coro)


@pytest.fixture
def corpus():
    return Corpus(Settings.corpus_path)


@pytest.fixture
def task():
    return AnalyzeRequest(
        task_id="T-1", title="seller cancellation", allowed_scopes=["demo"]
    )


@pytest.fixture
def plan():
    return dict(
        summary="Explore cancellation",
        findings=[
            dict(
                kind="fact",
                role="entry_point",
                explanation="Cancellation handler",
                citations=[
                    dict(
                        source_id="cancel-handler",
                        start_line=2,
                        end_line=2,
                        quote="// CancelOrder handles seller cancellation.",
                    )
                ],
            )
        ],
        steps=[
            dict(
                action="Read handler",
                source_ids=["cancel-handler"],
                expected_result="Understand event",
            )
        ],
        questions=[],
        limitations=[],
    )


class FakeLLM:
    settings = Settings(model="test")

    def __init__(self, *results):
        self.results = iter(results)
        self.payloads = []

    async def complete(self, system, payload, max_tokens=3500):
        self.payloads.append(copy.deepcopy(payload))
        return next(self.results), {"total_tokens": 10}


def test_acl_and_search(corpus):
    assert corpus.search("OrderCancelled", []) == []
    found = corpus.search("OrderCancelled", ["demo"])
    assert "cancel-contract" in {s.id for s in found}
    assert all(s.scope == "demo" for s in found)
    assert corpus.search("zzzzxxxx", ["demo"]) == []


def test_analysis(corpus, task, plan):
    llm = FakeLLM({"terms": "OrderCancelled"}, plan)
    result = run(Navigator(corpus, llm).analyze(task))
    assert result.status == "ready"
    assert result.usage["total_tokens"] == 20
    assert "cancel_order.go:2-2" in result.jira_markdown
    assert "CONFIDENTIAL_TEST_MARKER" not in json.dumps(llm.payloads)


def test_no_access_no_llm(corpus, task):
    task.allowed_scopes = []
    llm = FakeLLM()
    result = run(Navigator(corpus, llm).analyze(task))
    assert result.status == "insufficient_context"
    assert not llm.payloads


def test_ambiguous(corpus, task, plan):
    plan["questions"] = ["Which channel?"]
    result = run(
        Navigator(corpus, FakeLLM({"terms": "OrderCancelled"}, plan)).analyze(task)
    )
    assert result.status == "needs_clarification"


def test_repair(corpus, task, plan):
    llm = FakeLLM({"terms": "OrderCancelled"}, {}, plan)
    assert run(Navigator(corpus, llm).analyze(task)).status == "ready"
    assert "correction" in llm.payloads[-1]


def test_bad_response(corpus, task):
    with pytest.raises(ProviderError, match="ungrounded"):
        run(
            Navigator(corpus, FakeLLM({"terms": "OrderCancelled"}, {}, {})).analyze(
                task
            )
        )
    with pytest.raises(ProviderError, match="invalid"):
        run(Navigator(corpus, FakeLLM({"terms": []})).analyze(task))


@pytest.mark.parametrize(
    "changes", [{"source_id": "private"}, {"start_line": 999}, {"quote": "invented"}]
)
def test_bad_evidence(corpus, plan, changes):
    plan["findings"][0]["citations"][0].update(changes)
    with pytest.raises(ValueError):
        validate_evidence(Plan.model_validate(plan), corpus.sources)


def test_bad_step_and_missing_citation(corpus, plan):
    plan["steps"][0]["source_ids"] = ["fake"]
    with pytest.raises(ValueError):
        validate_evidence(Plan.model_validate(plan), corpus.sources)
    plan["findings"][0]["citations"] = []
    with pytest.raises(ValueError):
        Plan.model_validate(plan)


def test_api(corpus, task, plan):
    svc = Navigator(corpus, FakeLLM({"terms": "OrderCancelled"}, plan))
    client = TestClient(create_app(Settings(service_token="test"), svc))
    assert client.get("/health").status_code == 200
    assert client.post("/v1/analyze", json=task.model_dump()).status_code == 401
    headers = {"Authorization": "Bearer test"}
    assert client.post("/v1/analyze", headers=headers, json={}).status_code == 422
    assert (
        client.post("/v1/analyze", headers=headers, json=task.model_dump()).status_code
        == 200
    )
    assert (
        TestClient(create_app(Settings(), svc)).post("/v1/analyze", json={}).status_code
        == 503
    )


def test_api_provider_error(corpus, task):
    client = TestClient(
        create_app(Settings(service_token="test"), Navigator(corpus, LLM(Settings())))
    )
    response = client.post(
        "/v1/analyze", headers={"Authorization": "Bearer test"}, json=task.model_dump()
    )
    assert response.status_code == 503
    assert response.json()["error"]["code"] == "llm_not_configured"


@pytest.mark.parametrize(
    "status,body,code",
    [
        (429, {}, "llm_rate_limited"),
        (401, {}, "llm_request_failed"),
        (200, {}, "llm_invalid_response"),
        (200, {"choices": [{"finish_reason": "length"}]}, "llm_invalid_response"),
        (200, {"choices": [{"message": {"content": "[]"}}]}, "llm_invalid_response"),
    ],
)
def test_provider_errors(status, body, code):
    async def post(*args, **kwargs):
        return httpx.Response(status, json=body)

    with (
        patch.object(httpx.AsyncClient, "post", post),
        pytest.raises(ProviderError, match=code),
    ):
        run(LLM(Settings(api_key="secret")).complete("system", {}))


@pytest.mark.parametrize(
    "error,code",
    [
        (httpx.ReadTimeout("x"), "llm_timeout"),
        (httpx.ConnectError("x"), "llm_unavailable"),
    ],
)
def test_network_errors(error, code):
    async def post(*args, **kwargs):
        raise error

    with (
        patch.object(httpx.AsyncClient, "post", post),
        pytest.raises(ProviderError, match=code),
    ):
        run(LLM(Settings(api_key="secret")).complete("system", {}))


def test_provider_success():
    async def post(*args, **kwargs):
        assert kwargs["headers"]["Authorization"] == "Bearer secret"
        return httpx.Response(
            200,
            json={
                "choices": [{"message": {"content": '{"terms":"order"}'}}],
                "usage": {"total_tokens": 12, "bad": "ignored"},
            },
        )

    with patch.object(httpx.AsyncClient, "post", post):
        assert run(LLM(Settings(api_key="secret")).complete("system", {})) == (
            {"terms": "order"},
            {"total_tokens": 12},
        )


def test_duplicate_source(tmp_path, corpus):
    path = tmp_path / "sources.json"
    path.write_text(json.dumps([corpus.sources[0].model_dump()] * 2))
    with pytest.raises(ValueError, match="Duplicate"):
        Corpus(path)


def test_corpus_snapshots_match_files(corpus):
    for source in corpus.sources:
        assert Path(source.path).read_text(encoding="utf-8") == source.content


def test_resolve_lines_only_for_unique_verbatim_quotes(corpus, plan):
    parsed = Plan.model_validate(plan)
    cite = parsed.findings[0].citations[0]
    cite.start_line = cite.end_line = 999
    resolve_citation_lines(parsed, corpus.sources)
    assert (cite.start_line, cite.end_line) == (2, 2)
    validate_evidence(parsed, corpus.sources)
    cite.quote = "invented text"
    resolve_citation_lines(parsed, corpus.sources)
    with pytest.raises(ValueError, match="quote mismatch"):
        validate_evidence(parsed, corpus.sources)
    cite.source_id = "unknown"
    resolve_citation_lines(parsed, corpus.sources)
    with pytest.raises(ValueError, match="unknown source"):
        validate_evidence(parsed, corpus.sources)


def test_ambiguous_quote_location_is_not_guessed(corpus, plan):
    parsed = Plan.model_validate(plan)
    cite = parsed.findings[0].citations[0]
    cite.quote = "Order"
    cite.start_line = cite.end_line = 999
    resolve_citation_lines(parsed, corpus.sources)
    assert cite.start_line == 999


def test_repair_includes_specific_invalid_source(corpus, task, plan):
    invalid = copy.deepcopy(plan)
    invalid["steps"][0]["source_ids"] = ["acceptance_criteria"]
    llm = FakeLLM({"terms": "OrderCancelled"}, invalid, plan)
    assert run(Navigator(corpus, llm).analyze(task)).status == "ready"
    assert "acceptance_criteria" in llm.payloads[-1]["validation_error"]
    assert "numbered_lines" in llm.payloads[-1]["sources"][0]


def test_task_requirements_can_be_cited(corpus, task, plan):
    task.acceptance_criteria = ["Send an email"]
    plan["findings"][0]["citations"] = [
        {
            "source_id": "task:T-1",
            "start_line": 5,
            "end_line": 5,
            "quote": "Send an email",
        }
    ]
    result = run(
        Navigator(corpus, FakeLLM({"terms": "OrderCancelled"}, plan)).analyze(task)
    )
    assert result.sources[-1].id == "task:T-1"
    assert "Send an email" in result.jira_markdown


def test_task_context_is_bounded():
    with pytest.raises(ValueError, match="28000"):
        AnalyzeRequest(
            task_id="T",
            title="task",
            allowed_scopes=[],
            acceptance_criteria=["x" * 28001],
        )
