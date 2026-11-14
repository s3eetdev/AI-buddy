"""Local synthetic Jira fixture, not a real Jira client or Go replacement."""

import hashlib
import json
import sqlite3
from pathlib import Path

from navigator.models import AnalyzeRequest, AnalyzeResponse
from navigator.service import validate_evidence


class DemoJira:
    def __init__(self, seed: Path, database: Path):
        data = json.loads(seed.read_text(encoding="utf-8"))
        self.issues = {t["task_id"]: t for t in data["issues"]}
        self.profiles = {p["id"]: p for p in data["profiles"]}
        database.parent.mkdir(parents=True, exist_ok=True)
        self.database = database
        with sqlite3.connect(database) as db:
            db.execute("""CREATE TABLE IF NOT EXISTS plans (
                id INTEGER PRIMARY KEY, task_id TEXT NOT NULL,
                profile TEXT NOT NULL, digest TEXT NOT NULL, result TEXT NOT NULL,
                created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
                UNIQUE(task_id, profile, digest))""")

    def task(self, task_id: str, profile: str) -> AnalyzeRequest:
        task = dict(self.issues[task_id])
        allowed = set(self.profiles[profile]["allowed_scopes"])
        task["allowed_scopes"] = [s for s in task["allowed_scopes"] if s in allowed]
        return AnalyzeRequest.model_validate(task)

    def save(self, result: AnalyzeResponse, profile: str, *, confirmed: bool) -> dict:
        if not confirmed:
            raise ValueError("Explicit confirmation is required")
        task = self.task(result.task_id, profile)
        if result.status != "ready":
            raise ValueError("Resolve clarification before saving a final plan")
        if any(s.scope not in task.allowed_scopes for s in result.sources):
            raise PermissionError("Plan contains inaccessible sources")
        if not result.sources:
            raise ValueError("A final plan needs sources")
        if not result.plan.findings or not result.plan.steps:
            raise ValueError("A final plan needs findings and steps")
        validate_evidence(result.plan, result.sources)
        payload = result.model_dump_json()
        digest = hashlib.sha256(payload.encode()).hexdigest()
        with sqlite3.connect(self.database) as db:
            db.execute(
                "INSERT OR IGNORE INTO plans(task_id,profile,digest,result) VALUES (?,?,?,?)",
                (result.task_id, profile, digest, payload),
            )
            row = db.execute(
                "SELECT id FROM plans WHERE task_id=? AND profile=? AND digest=?",
                (result.task_id, profile, digest),
            ).fetchone()
        return {
            "id": row[0],
            "task_id": result.task_id,
            "status": "saved",
            "uri": f"demo-jira://{result.task_id}/plans/{row[0]}",
            "database": str(self.database),
        }

    def read_plan(self, plan_id: int, profile: str) -> AnalyzeResponse:
        with sqlite3.connect(self.database) as db:
            row = db.execute(
                "SELECT result FROM plans WHERE id=? AND profile=?", (plan_id, profile)
            ).fetchone()
        if row is None:
            raise KeyError("Plan not found for this profile")
        result = AnalyzeResponse.model_validate_json(row[0])
        task = self.task(result.task_id, profile)
        if any(s.scope not in task.allowed_scopes for s in result.sources):
            raise PermissionError("Source access was revoked")
        return result
