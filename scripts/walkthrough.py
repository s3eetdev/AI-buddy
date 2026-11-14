"""Task -> real AI -> explicit confirmation -> persisted local Jira imitation."""

import argparse
import asyncio
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

from navigator.config import Settings
from navigator.demo_jira import DemoJira
from navigator.llm import LLM, ProviderError
from navigator.models import AnalyzeResponse
from navigator.retrieval import Corpus
from navigator.service import Navigator


async def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--case", default="DEMO-1")
    parser.add_argument("--profile", default="intern")
    parser.add_argument(
        "--confirm-save",
        action="store_true",
        help="Explicitly approve saving the plan to the local simulation",
    )
    parser.add_argument(
        "--replay",
        type=Path,
        help="Replay a saved real AI response offline; clearly labelled as replay",
    )
    parser.add_argument(
        "--read-plan",
        type=int,
        help="Read back an already persisted plan without calling the LLM",
    )
    args = parser.parse_args()
    jira = DemoJira(ROOT / "data/demo_tasks.json", ROOT / "artifacts/demo-jira.sqlite3")
    if args.read_plan is not None:
        result = jira.read_plan(args.read_plan, args.profile)
        print(result.jira_markdown)
        return 0
    task = jira.task(args.case, args.profile)
    print(
        json.dumps(
            {
                "stage": "collecting_context",
                "task_id": task.task_id,
                "mode": "replay" if args.replay else "live",
            }
        ),
        flush=True,
    )
    if args.replay:
        result = AnalyzeResponse.model_validate_json(
            args.replay.read_text(encoding="utf-8")
        )
        if result.task_id != task.task_id or any(
            s.scope not in task.allowed_scopes for s in result.sources
        ):
            raise ValueError("Replay task or permissions do not match")
    else:
        settings = Settings.from_env()
        result = await Navigator(Corpus(settings.corpus_path), LLM(settings)).analyze(
            task
        )
    print(result.jira_markdown)
    print(json.dumps({"stage": result.status, "task_id": task.task_id}), flush=True)
    if args.confirm_save:
        receipt = jira.save(result, args.profile, confirmed=True)
        # Verify durable readback, not merely a successful function return.
        assert jira.read_plan(receipt["id"], args.profile) == result
        print(json.dumps(receipt, ensure_ascii=True))
    else:
        print("Plan not saved. Review it before using --confirm-save.")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(asyncio.run(main()))
    except ProviderError as exc:
        print(json.dumps({"error": exc.code}))
        sys.exit(1)
    except (ValueError, KeyError, PermissionError) as exc:
        print(str(exc))
        sys.exit(1)
