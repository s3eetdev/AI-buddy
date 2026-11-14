"""Offline retrieval by default; --live calls the actual model and costs money."""

import argparse
import asyncio
import json
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

from navigator.config import Settings
from navigator.evaluation import load_demo, score_response
from navigator.llm import LLM, ProviderError
from navigator.models import AnalyzeRequest
from navigator.retrieval import Corpus
from navigator.service import Navigator


async def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--live", action="store_true")
    parser.add_argument("--case", help="Run a single task, e.g. DEMO-1")
    args = parser.parse_args()
    tasks, cases = load_demo(ROOT)
    if args.case:
        cases = [c for c in cases if c["task_id"] == args.case]
        if not cases:
            parser.error("Unknown task ID")
    settings = Settings.from_env()
    corpus = Corpus(settings.corpus_path)
    service = Navigator(corpus, LLM(settings))
    output = ROOT / "artifacts" / "evaluation"
    output.mkdir(parents=True, exist_ok=True)
    report = {
        "mode": "live" if args.live else "retrieval-only",
        "timestamp_utc": datetime.now(timezone.utc).isoformat(),
        "model": settings.model if args.live else None,
        "cases": [],
    }
    for case in cases:
        task = AnalyzeRequest.model_validate(tasks[case["task_id"]])
        start = time.monotonic()
        row = {"task_id": task.task_id}
        if args.live:
            try:
                result = await service.analyze(task)
                row.update(score_response(result, case, task.allowed_scopes))
                (output / f"{task.task_id}.json").write_text(
                    result.model_dump_json(indent=2), encoding="utf-8"
                )
            except ProviderError as exc:
                row.update(passed=False, error=exc.code)
        else:
            sources = corpus.search(case["search_query"], task.allowed_scopes)
            missing = sorted(set(case["required_sources"]) - {s.id for s in sources})
            row.update(
                passed=not missing
                and all(s.scope in task.allowed_scopes for s in sources),
                missing_sources=missing,
                retrieved=[s.id for s in sources],
            )
        row["elapsed_seconds"] = round(time.monotonic() - start, 2)
        report["cases"].append(row)
        print(json.dumps(row, ensure_ascii=True), flush=True)
    report["passed"] = all(r["passed"] for r in report["cases"])
    suffix = f"-{args.case}" if args.case else ""
    (output / f"{report['mode']}{suffix}.json").write_text(
        json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8"
    )
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
