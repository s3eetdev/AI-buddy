"""Run a real synthetic analysis; saves the result without printing secrets."""

import asyncio
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from navigator.config import Settings
from navigator.llm import LLM, ProviderError
from navigator.models import AnalyzeRequest
from navigator.retrieval import Corpus
from navigator.service import Navigator


async def main():
    settings = Settings.from_env()
    task = AnalyzeRequest.model_validate_json(
        Path("examples/task.json").read_text(encoding="utf-8")
    )
    try:
        result = await Navigator(Corpus(settings.corpus_path), LLM(settings)).analyze(
            task
        )
    except ProviderError as exc:
        print(json.dumps({"error": exc.code, "status": exc.status}))
        return 1
    Path("artifacts").mkdir(exist_ok=True)
    Path("artifacts/demo-result.json").write_text(
        result.model_dump_json(indent=2), encoding="utf-8"
    )
    print(
        json.dumps(
            {
                "status": result.status,
                "sources": len(result.sources),
                "findings": len(result.plan.findings),
                "usage": result.usage,
            }
        )
    )
    return 0


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
