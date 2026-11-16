"""Build-time report and coverage gate; not part of the application runtime."""

import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
REPORTS = ROOT / "artifacts" / "coverage"


def main():
    python = json.loads((REPORTS / "python.json").read_text(encoding="utf-8"))
    frontend = json.loads(
        (REPORTS / "frontend" / "coverage-summary.json").read_text(encoding="utf-8")
    )
    # Go's native metric is statements. Also derive line coverage from the exact
    # instrumented ranges shown by go tool cover. Repeated package records are
    # merged; a line covered in any block is covered (same rule as Istanbul).
    blocks = {}
    for record in (REPORTS / "go" / "coverage.out").read_text().splitlines()[1:]:
        span, statements, count = record.split()
        blocks[(span, int(statements))] = blocks.get((span, int(statements)), 0) + int(
            count
        )
    lines = {}
    for (span, statements), count in blocks.items():
        if not statements:
            continue
        match = re.fullmatch(r"(.+):(\d+)\.(\d+),(\d+)\.(\d+)", span)
        if not match:
            raise ValueError(f"Invalid Go coverage span: {span}")
        name, start, _, end, end_column = match.groups()
        # End column 1 means that line itself is outside the instrumented range.
        end = int(end) - (int(end_column) == 1)
        for line in range(int(start), end + 1):
            key = (name, line)
            lines[key] = lines.get(key, False) or count > 0
    statements = sum(size for _, size in blocks)
    hit_statements = sum(size for (_, size), count in blocks.items() if count)
    go_files = {Path(name).name for name, _ in lines}
    if "main.go" not in go_files:
        raise ValueError("Go entrypoint is absent from coverage")
    covered_go_paths = {name.split("/backend/", 1)[1] for name, _ in lines}
    runtime_go_paths = {
        p.relative_to(ROOT / "backend").as_posix()
        for p in (ROOT / "backend").rglob("*.go")
        if not p.name.endswith("_test.go")
        and "migrations" not in p.parts
        and re.search(r"\bfunc\b", p.read_text(encoding="utf-8"))
    }
    if missing := runtime_go_paths - covered_go_paths:
        raise ValueError(f"Unmeasured Go application files: {sorted(missing)}")
    measured_frontend = {Path(p).name for p in frontend if p != "total"}
    runtime_frontend = {
        p.name
        for p in (ROOT / "frontend" / "src").rglob("*")
        if p.suffix in {".ts", ".tsx"} and "types" not in p.parts
    }
    if missing := runtime_frontend - measured_frontend:
        raise ValueError(f"Unmeasured frontend files: {sorted(missing)}")
    measured_python = {p.replace("\\", "/") for p in python["files"]}
    runtime_python = {"main.py"} | {
        p.relative_to(ROOT).as_posix()
        for directory in ("navigator", "scripts")
        for p in (ROOT / directory).rglob("*.py")
    }
    if missing := runtime_python - measured_python:
        raise ValueError(f"Unmeasured Python files: {sorted(missing)}")
    events = [
        json.loads(row)
        for row in (REPORTS / "go" / "tests.json").read_text().splitlines()
    ]
    skipped = [e for e in events if e.get("Action") == "skip" and e.get("Test")]
    if skipped:
        raise ValueError("Go tests were skipped; PostgreSQL coverage is incomplete")
    py = python["totals"]
    rows = {
        "Python": {"covered": py["covered_lines"], "total": py["num_statements"]},
        "Frontend": {
            "covered": frontend["total"]["lines"]["covered"],
            "total": frontend["total"]["lines"]["total"],
        },
        "Go instrumented lines": {"covered": sum(lines.values()), "total": len(lines)},
    }
    for value in rows.values():
        value["percent"] = round(100 * value["covered"] / value["total"], 2)
    total = sum(v["total"] for v in rows.values())
    covered = sum(v["covered"] for v in rows.values())
    result = {
        "components": rows,
        "combined_lines": {
            "covered": covered,
            "total": total,
            "percent": round(100 * covered / total, 2),
        },
        "go_statements": {
            "covered": hit_statements,
            "total": statements,
            "percent": round(100 * hit_statements / statements, 2),
        },
        "go_tests_passed": sum(
            e.get("Action") == "pass" and bool(e.get("Test")) for e in events
        ),
        "go_tests_skipped": len(skipped),
        "threshold_percent": 81,
    }
    (REPORTS / "summary.json").write_text(
        json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
    print(json.dumps(result, ensure_ascii=False, indent=2))
    if (
        any(v["percent"] < 81 for v in rows.values())
        or result["go_statements"]["percent"] < 81
    ):
        raise SystemExit("Coverage gate failed: every component must reach 81%")


if __name__ == "__main__":
    main()
