import asyncio
import json
import shutil
import sys

import pytest

from navigator.config import ROOT, Settings
from navigator.demo_jira import DemoJira
from navigator.models import AnalyzeResponse, Plan
from navigator.retrieval import Corpus
from scripts import demo, evaluate, walkthrough


@pytest.fixture
def cli_root(tmp_path):
    (tmp_path / "data").mkdir()
    for name in ["demo_tasks.json", "evaluation_cases.json"]:
        shutil.copyfile(ROOT / "data" / name, tmp_path / "data" / name)
    return tmp_path


@pytest.fixture
def saved_result():
    source = Corpus(Settings.corpus_path).sources[0]
    return AnalyzeResponse(
        task_id="DEMO-1",
        status="ready",
        sources=[source],
        model="fake",
        usage={},
        jira_markdown="Synthetic plan",
        plan=Plan(
            summary="Read handler",
            findings=[
                {
                    "kind": "hypothesis",
                    "role": "context",
                    "explanation": "Review handler",
                    "citations": [],
                }
            ],
            steps=[
                {
                    "action": "Read",
                    "source_ids": [source.id],
                    "expected_result": "Understand",
                }
            ],
            questions=[],
            limitations=[],
        ),
    )


def test_evaluate_offline_and_unknown_case(cli_root, monkeypatch):
    monkeypatch.setattr(evaluate, "ROOT", cli_root)
    monkeypatch.setattr(Settings, "from_env", lambda: Settings())
    monkeypatch.setattr(sys, "argv", ["evaluate"])
    assert asyncio.run(evaluate.main()) == 0
    report = json.loads(
        (cli_root / "artifacts/evaluation/retrieval-only.json").read_text()
    )
    assert len(report["cases"]) == 6
    monkeypatch.setattr(sys, "argv", ["evaluate", "--case", "missing"])
    with pytest.raises(SystemExit):
        asyncio.run(evaluate.main())


def test_evaluate_live_no_access_and_failure(cli_root, monkeypatch):
    monkeypatch.setattr(evaluate, "ROOT", cli_root)
    monkeypatch.setattr(Settings, "from_env", lambda: Settings())
    monkeypatch.setattr(sys, "argv", ["evaluate", "--live", "--case", "DEMO-4"])
    assert asyncio.run(evaluate.main()) == 0
    monkeypatch.setattr(sys, "argv", ["evaluate", "--live", "--case", "DEMO-1"])
    assert asyncio.run(evaluate.main()) == 1
    report = json.loads(
        (cli_root / "artifacts/evaluation/live-DEMO-1.json").read_text()
    )
    assert report["cases"][0]["error"] == "llm_not_configured"


def test_walkthrough_replay_save_read_and_permissions(
    cli_root, saved_result, monkeypatch, capsys
):
    path = cli_root / "response.json"
    path.write_text(saved_result.model_dump_json(), encoding="utf-8")
    monkeypatch.setattr(walkthrough, "ROOT", cli_root)
    base = ["walkthrough", "--replay", str(path)]
    monkeypatch.setattr(sys, "argv", base)
    assert asyncio.run(walkthrough.main()) == 0
    assert "Plan not saved" in capsys.readouterr().out
    monkeypatch.setattr(sys, "argv", [*base, "--confirm-save"])
    assert asyncio.run(walkthrough.main()) == 0
    assert '"status": "saved"' in capsys.readouterr().out
    monkeypatch.setattr(sys, "argv", ["walkthrough", "--read-plan", "1"])
    assert asyncio.run(walkthrough.main()) == 0
    assert "Synthetic plan" in capsys.readouterr().out
    monkeypatch.setattr(sys, "argv", [*base, "--profile", "newcomer"])
    with pytest.raises(ValueError, match="permissions"):
        asyncio.run(walkthrough.main())


def test_walkthrough_live_path_without_network(cli_root, monkeypatch):
    monkeypatch.setattr(walkthrough, "ROOT", cli_root)
    monkeypatch.setattr(Settings, "from_env", lambda: Settings())
    monkeypatch.setattr(sys, "argv", ["walkthrough", "--case", "DEMO-4"])
    assert asyncio.run(walkthrough.main()) == 0


def test_demo_success_and_provider_failure(cli_root, saved_result, monkeypatch):
    monkeypatch.chdir(cli_root)
    (cli_root / "examples").mkdir()
    task = DemoJira(cli_root / "data/demo_tasks.json", cli_root / "jira.sqlite3").task(
        "DEMO-1", "intern"
    )
    (cli_root / "examples/task.json").write_text(
        task.model_dump_json(), encoding="utf-8"
    )
    monkeypatch.setattr(Settings, "from_env", lambda: Settings())
    assert asyncio.run(demo.main()) == 1

    async def analyze(self, task):
        return saved_result

    monkeypatch.setattr(demo.Navigator, "analyze", analyze)
    assert asyncio.run(demo.main()) == 0
    assert (cli_root / "artifacts/demo-result.json").exists()
