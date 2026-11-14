"""Observable checks, not a semantic judge or a claim of production accuracy."""

import json

from navigator.models import AnalyzeResponse
from navigator.service import validate_evidence


def score_response(result: AnalyzeResponse, case: dict, scopes: list[str]) -> dict:
    ids = {s.id for s in result.sources}
    required = set(case["required_sources"])
    covered = sorted(required & ids)
    evidence_valid = True
    try:
        validate_evidence(result.plan, result.sources)
    except ValueError:
        evidence_valid = False
    plan_text = result.plan.model_dump_json() + result.jira_markdown
    checks = {
        "status": result.status == case["expected_status"],
        "required_sources": required <= ids,
        "acl": all(s.scope in scopes for s in result.sources),
        "evidence": evidence_valid,
        "findings": len(result.plan.findings) >= case["minimum_findings"],
        "questions": len(result.plan.questions) >= case["minimum_questions"],
        "forbidden_output_absent": not any(
            t in plan_text for t in case["forbidden_text"]
        ),
    }
    return {
        "passed": all(checks.values()),
        "checks": checks,
        "required_source_recall": len(covered) / len(required) if required else None,
        "missing_sources": sorted(required - ids),
        "status": result.status,
        "usage": result.usage,
        "human_review_required": case["review"],
    }


def load_demo(root):
    tasks = json.loads((root / "data/demo_tasks.json").read_text(encoding="utf-8"))
    cases = json.loads(
        (root / "data/evaluation_cases.json").read_text(encoding="utf-8")
    )
    return {t["task_id"]: t for t in tasks["issues"]}, cases
