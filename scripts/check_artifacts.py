#!/usr/bin/env python3
"""Check a repository's .project/ artifact tree against the dot-project standard.

Usage: check_artifacts.py [repo-root]
Exit 0 when clean, 1 with one line per problem on stderr.
Stdlib only; Python 3.11+.
"""

from __future__ import annotations

import re
import subprocess
import sys
import tomllib
from pathlib import Path

STATUSES = {
    "specification": {"sketch", "draft", "published"},
    "plan": {"sketch", "draft", "published"},
    "research": {"sketch", "draft", "published"},
    "analysis": {"sketch", "draft", "published"},
    "review": {"sketch", "draft", "published"},
    "draft": {"sketch", "draft"},
    "sketch": {"sketch", "draft", "published"},
    "handoff": {"draft", "published"},
    "reference": {"draft", "published"},
    "backlog-item": {"open"},
}
REQUIRED = ("type", "id", "title", "description", "status", "revision", "created_at", "updated_at")
NUMBERED = re.compile(r"^(SPEC|PLAN|B)-(\d{4})$")
ID_MENTION = re.compile(r"\b(SPEC|PLAN|B)-(\d{4})\b")
PATH_MENTION = re.compile(r"\.project/[A-Za-z0-9_.\-/]*[A-Za-z0-9_\-/]")
POINTER = re.compile(r"^- (B-\d{4}) (bug|drift|debt|feature|chore) (P[123]) — .+ → (\.project/backlog/B-\d{4}-[a-z0-9-]+\.md)$")
KEY = re.compile(r"^  ([a-z_]+):(?:\s+(.*))?$")


def unquote(value: str) -> str:
    value = value.strip()
    if len(value) >= 2 and value[0] == value[-1] == '"':
        return value[1:-1]
    return value


def frontmatter(text: str) -> tuple[dict[str, str], int] | None:
    """Top-level `document:` keys and the number of changelog entries, or None."""
    lines = text.splitlines()
    if len(lines) < 3 or lines[0] != "---" or lines[1] != "document:":
        return None
    try:
        end = lines.index("---", 2)
    except ValueError:
        return None
    fields: dict[str, str] = {}
    entries = 0
    in_changelog = False
    for line in lines[2:end]:
        match = KEY.match(line)
        if match:
            key, value = match.group(1), match.group(2) or ""
            fields[key] = unquote(value)
            in_changelog = key == "changelog"
        elif in_changelog and line.startswith("    - "):
            entries += 1
    return fields, entries


def tracked_markdown(root: Path) -> list[Path]:
    out = subprocess.run(
        ["git", "-C", str(root), "ls-files", "--cached", "--others", "--exclude-standard", "*.md"],
        check=True, capture_output=True, text=True,
    ).stdout
    return [root / line for line in out.splitlines() if (root / line).is_file()]


def is_artifact(rel: Path) -> bool:
    parts = rel.parts
    return (
        parts[0] == ".project"
        and rel.name != "AGENTS.md"
        and "user-uploaded-assets" not in parts
        and "evidence" not in parts
    )


def check(root: Path) -> list[str]:
    problems: list[str] = []
    project = root / ".project"
    if not project.is_dir():
        return [f"{project}: missing"]

    counters: dict[str, int] = {}
    ids_file = project / "ids.toml"
    if ids_file.is_file():
        try:
            raw = tomllib.loads(ids_file.read_text())
        except tomllib.TOMLDecodeError as error:
            return [f".project/ids.toml: {error}"]
        for prefix, value in raw.items():
            if prefix not in {"SPEC", "PLAN", "B"} or not isinstance(value, int) or value < 1:
                problems.append(f".project/ids.toml: {prefix} must be SPEC, PLAN or B with a positive integer")
            else:
                counters[prefix] = value
    else:
        problems.append(".project/ids.toml: missing")

    markdown = tracked_markdown(root)
    ids: dict[str, Path] = {}
    backlog: dict[str, Path] = {}
    for path in markdown:
        rel = path.relative_to(root)
        if not is_artifact(rel):
            continue
        parsed = frontmatter(path.read_text())
        if parsed is None:
            problems.append(f"{rel}: missing `---` / `document:` frontmatter")
            continue
        fields, entries = parsed
        for key in REQUIRED:
            if not fields.get(key):
                problems.append(f"{rel}: frontmatter lacks `{key}`")
        kind, ident, status = fields.get("type", ""), fields.get("id", ""), fields.get("status", "")
        if kind not in STATUSES:
            problems.append(f"{rel}: unknown type `{kind}`")
        elif status not in STATUSES[kind]:
            problems.append(f"{rel}: status `{status}` not allowed for {kind} ({', '.join(sorted(STATUSES[kind]))})")
        if entries > 1:
            problems.append(f"{rel}: {entries} changelog entries; keep only the current revision's")
        if not ident:
            continue
        if ident in ids:
            problems.append(f"{rel}: id {ident} already used by {ids[ident].relative_to(root)}")
        ids[ident] = path
        numbered = NUMBERED.match(ident)
        if numbered and numbered.group(1) in counters and int(numbered.group(2)) >= counters[numbered.group(1)]:
            problems.append(f"{rel}: {ident} is not below ids.toml {numbered.group(1)} = {counters[numbered.group(1)]}")
        if kind == "backlog-item":
            backlog[ident] = path

    pointers: dict[str, list[Path]] = {}
    for path in markdown:
        rel = path.relative_to(root)
        if "user-uploaded-assets" in rel.parts or "evidence" in rel.parts or rel.name == "CHANGELOG.md":
            continue
        text = path.read_text()
        for number, line in enumerate(text.splitlines(), 1):
            for match in PATH_MENTION.finditer(line):
                target = match.group(0).rstrip("/.")
                following = line[match.end():match.end() + 1]
                if following in {"<", "{", "*"} or "NNNN" in target or "YYYY" in target:
                    continue
                if not (root / target).exists():
                    problems.append(f"{rel}:{number}: path {target} does not exist")
            for ident in sorted({match.group(0) for match in ID_MENTION.finditer(line)}):
                if ident not in ids:
                    problems.append(f"{rel}:{number}: {ident} is not a live artifact")
        if rel.name == "BACKLOG.md":
            for number, line in enumerate(text.splitlines(), 1):
                pointer = POINTER.match(line)
                if not pointer:
                    problems.append(f"{rel}:{number}: not a backlog pointer line")
                    continue
                ident, target = pointer.group(1), pointer.group(4)
                pointers.setdefault(ident, []).append(rel)
                if ident not in backlog or backlog[ident].relative_to(root).as_posix() != target:
                    problems.append(f"{rel}:{number}: {ident} does not point at its open item file")

    for ident, path in sorted(backlog.items()):
        found = pointers.get(ident, [])
        if len(found) != 1:
            where = ", ".join(p.as_posix() for p in found) or "no BACKLOG.md"
            problems.append(f"{path.relative_to(root)}: {ident} needs exactly one pointer, found {where}")
    return problems


def main() -> int:
    root = Path(sys.argv[1] if len(sys.argv) > 1 else ".").resolve()
    problems = check(root)
    for problem in problems:
        print(problem, file=sys.stderr)
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
