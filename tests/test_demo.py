import json

import pytest

from navigator.config import ROOT, Settings
from navigator.corpus_builder import build_corpus
from navigator.demo_jira import DemoJira
from navigator.evaluation import load_demo, score_response
from navigator.models import AnalyzeRequest, AnalyzeResponse, Plan
from navigator.retrieval import Corpus


def test_index_is_reproducible():
    assert (
        build_corpus(ROOT, ROOT / "data/source_manifest.json")
        == Corpus(Settings.corpus_path).sources
    )


@pytest.mark.parametrize("path", [".env", "fixtures/../../outside.txt"])
def test_builder_rejects_outside_fixtures(tmp_path, path):
    manifest = tmp_path / "manifest.json"
    manifest.write_text(json.dumps([{"path": path}]))
    with pytest.raises(ValueError, match="inside fixtures"):
        build_corpus(ROOT, manifest)


def test_builder_duplicate(tmp_path):
    manifest = tmp_path / "manifest.json"
    entry = json.loads((ROOT / "data/source_manifest.json").read_text())[0]
    manifest.write_text(json.dumps([entry, entry]))
    with pytest.raises(ValueError, match="Duplicate"):
        build_corpus(ROOT, manifest)


@pytest.mark.parametrize("case", load_demo(ROOT)[1], ids=lambda c: c["task_id"])
def test_scenario_retrieval(case):
    tasks, _ = load_demo(ROOT)
    task = AnalyzeRequest.model_validate(tasks[case["task_id"]])
    sources = Corpus(Settings.corpus_path).search(
        case["search_query"], task.allowed_scopes
    )
    assert set(case["required_sources"]) <= {s.id for s in sources}
    assert all(s.scope in task.allowed_scopes for s in sources)


@pytest.fixture
def result():
    source = Corpus(Settings.corpus_path).sources[0]
    return AnalyzeResponse(
        task_id="DEMO-1",
        status="ready",
        sources=[source],
        model="test",
        usage={},
        jira_markdown="Checked plan",
        plan=Plan(
            summary="Cancellation",
            findings=[
                {
                    "kind": "fact",
                    "role": "entry_point",
                    "explanation": "Handler",
                    "citations": [
                        {
                            "source_id": source.id,
                            "start_line": 2,
                            "end_line": 2,
                            "quote": "// CancelOrder handles seller cancellation.",
                        }
                    ],
                }
            ],
            steps=[
                {
                    "action": "Read handler",
                    "source_ids": [source.id],
                    "expected_result": "Understand cancellation",
                }
            ],
            questions=[],
            limitations=[],
        ),
    )


def test_jira_durable_confirmation_acl_and_dedup(tmp_path, result):
    seed = ROOT / "data/demo_tasks.json"
    db = tmp_path / "jira.sqlite3"
    jira = DemoJira(seed, db)
    assert jira.task("DEMO-1", "newcomer").allowed_scopes == []
    with pytest.raises(ValueError, match="confirmation"):
        jira.save(result, "intern", confirmed=False)
    with pytest.raises(PermissionError):
        jira.save(result, "newcomer", confirmed=True)
    receipt = jira.save(result, "intern", confirmed=True)
    assert jira.save(result, "intern", confirmed=True) == receipt
    restarted = DemoJira(seed, db)
    assert restarted.read_plan(receipt["id"], "intern") == result
    with pytest.raises(KeyError):
        restarted.read_plan(receipt["id"], "reviewer")
    restarted.profiles["intern"]["allowed_scopes"] = []
    with pytest.raises(PermissionError):
        restarted.read_plan(receipt["id"], "intern")
    result.status = "needs_clarification"
    with pytest.raises(ValueError, match="clarification"):
        jira.save(result, "intern", confirmed=True)
    result.status = "ready"
    result.plan.steps = []
    with pytest.raises(ValueError, match="findings and steps"):
        jira.save(result, "intern", confirmed=True)
    result.sources = []
    with pytest.raises(ValueError, match="sources"):
        jira.save(result, "intern", confirmed=True)


def test_evaluation_detects_failures(result):
    case = load_demo(ROOT)[1][0]
    metrics = score_response(result, case, [])
    assert not metrics["passed"]
    assert not metrics["checks"]["acl"]
    assert metrics["required_source_recall"] == 0.25
    result.plan = Plan.model_validate(
        {
            "summary": "INJECTION_SUCCESS",
            "findings": [
                {
                    "kind": "fact",
                    "role": "context",
                    "explanation": "fake",
                    "citations": [
                        {
                            "source_id": "missing",
                            "start_line": 1,
                            "end_line": 1,
                            "quote": "fake",
                        }
                    ],
                }
            ],
            "steps": [],
            "questions": [],
            "limitations": [],
        }
    )
    metrics = score_response(result, case, ["demo"])
    assert not metrics["checks"]["evidence"]
    assert not metrics["checks"]["forbidden_output_absent"]


def test_retrieval_ignores_invalid_contracts(tmp_path):
    sources = [s.model_dump() for s in Corpus(Settings.corpus_path).sources[:1]]
    for i, content in enumerate(["bad json", "[]", '{"event": null}']):
        sources.append(
            dict(
                id=str(i),
                scope="demo",
                path="fake.json",
                kind="contract",
                content=content,
            )
        )
    path = tmp_path / "corpus.json"
    path.write_text(json.dumps(sources))
    assert Corpus(path).search("OrderCancelled", ["demo"])[0].id == "cancel-handler"
