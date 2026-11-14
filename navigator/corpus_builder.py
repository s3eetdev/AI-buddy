"""Build a reproducible corpus from explicitly allowlisted local fixtures."""

import json
from pathlib import Path

from navigator.models import Source


def build_corpus(root: Path, manifest: Path) -> list[Source]:
    root = root.resolve()
    entries = json.loads(manifest.read_text(encoding="utf-8"))
    sources = []
    for entry in entries:
        path = (root / entry["path"]).resolve()
        if not path.is_relative_to(root / "fixtures"):
            raise ValueError("Source must be inside fixtures")
        sources.append(
            Source.model_validate(
                {**entry, "content": path.read_text(encoding="utf-8")}
            )
        )
    if len({s.id for s in sources}) != len(sources):
        raise ValueError("Duplicate source IDs")
    return sources
