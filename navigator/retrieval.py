import json
import math
import re
from collections import Counter
from pathlib import Path

from navigator.models import Source


def tokens(text: str) -> list[str]:
    text = re.sub(r"([a-z])([A-Z])", r"\1 \2", text)
    return re.findall(r"[\w]+", text.lower())


class Corpus:
    """Small local corpus; ACL filtering happens before scoring and LLM calls."""

    def __init__(self, path: Path):
        self.sources = [
            Source.model_validate(s)
            for s in json.loads(path.read_text(encoding="utf-8"))
        ]
        if len({s.id for s in self.sources}) != len(self.sources):
            raise ValueError("Duplicate source IDs")

    def search(self, query: str, scopes: list[str], limit: int = 8) -> list[Source]:
        visible = [s for s in self.sources if s.scope in scopes]
        terms = set(tokens(query))
        documents = [Counter(tokens(s.path + " " + s.content)) for s in visible]
        scored = []
        for s, counts in zip(visible, documents):
            score = sum(
                math.log(1 + len(visible) / (1 + sum(t in d for d in documents)))
                * counts[t]
                / (counts[t] + 1.2)
                for t in terms
                if counts[t]
            )
            if score:
                scored.append((score, s))
        ranked = [s for _, s in sorted(scored, key=lambda pair: (-pair[0], pair[1].id))]
        # Keep the event contract and both code endpoints together. Lexical
        # ranking alone can crowd the contract out with explanatory documents.
        # This only selects evidence; it does not assert that the link is true.
        peers = []
        for contract in visible:
            if contract.kind != "contract":
                continue
            try:
                event = json.loads(contract.content).get("event")
            except (ValueError, AttributeError):
                continue
            if not isinstance(event, str) or not event:
                continue
            if event in query or any(event in s.content for s in ranked[:2]):
                peers.append(contract)
                peers.extend(
                    s for s in visible if s.kind == "code" and event in s.content
                )
        ordered = list({s.id: s for s in [*ranked[:2], *peers, *ranked[2:]]}.values())
        selected, size = [], 0
        for source in ordered[:limit]:
            if size + len(source.content) <= 45000:
                selected.append(source)
                size += len(source.content)
        return selected
