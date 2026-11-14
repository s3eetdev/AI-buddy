"""Regenerate the local fixture index without any network calls."""

import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

from navigator.corpus_builder import build_corpus

if __name__ == "__main__":
    sources = build_corpus(ROOT, ROOT / "data/source_manifest.json")
    (ROOT / "data/demo_sources.json").write_text(
        json.dumps([s.model_dump() for s in sources], ensure_ascii=False, indent=2)
        + "\n",
        encoding="utf-8",
    )
    print(f"Indexed {len(sources)} synthetic sources")
