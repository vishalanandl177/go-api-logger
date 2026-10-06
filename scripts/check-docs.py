#!/usr/bin/env python3
"""Check local documentation links and optionally build complete Go examples.

Python 3.10+ and Git are required. The README quick start must match its runnable
source. --compile additionally requires Go and may
download the dependencies pinned by scripts/_releasecheck/go.mod. Code blocks
are compiled, never executed. Partial snippets without a package declaration
are skipped.
"""

from __future__ import annotations

import argparse
from dataclasses import dataclass
import html
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
from urllib.parse import unquote, urlsplit


ROOT = Path(__file__).resolve().parents[1]
FENCE = re.compile(r"^ {0,3}(`{3,}|~{3,})(.*)$")
LINK = re.compile(r"!?\[[^\]\n]*\]\(\s*(?:<([^>]+)>|([^\s)]+))(?:\s+[\"'][^\n]*?[\"'])?\s*\)")
REFERENCE = re.compile(r"^ {0,3}\[[^\]]+\]:\s*(?:<([^>]+)>|(\S+))", re.MULTILINE)


@dataclass
class Document:
    path: Path
    text: str
    prose: str
    snippets: list[tuple[int, str]]


def read_document(path: Path) -> Document:
    text = path.read_text(encoding="utf-8-sig")
    prose: list[str] = []
    snippets: list[tuple[int, str]] = []
    fence = ""
    language = ""
    code: list[str] = []
    start = 0
    for line_number, line in enumerate(text.splitlines(keepends=True), 1):
        match = FENCE.match(line)
        if not fence:
            if match:
                fence = match[1]
                language = match[2].strip().lower()
                code = []
                start = line_number + 1
                prose.append("\n")
            else:
                prose.append(line)
        elif match and match[1][0] == fence[0] and len(match[1]) >= len(fence) and not match[2].strip():
            source = "".join(code)
            if language in ("go", "golang") and re.search(r"(?m)^package [A-Za-z_][A-Za-z_0-9]*\s*$", source):
                snippets.append((start, source))
            fence = ""
            prose.append("\n")
        else:
            code.append(line)
            prose.append("\n")
    return Document(path, text, "".join(prose), snippets)


def heading_ids(document: Document) -> set[str]:
    ids: set[str] = set()
    counts: dict[str, int] = {}
    lines = document.prose.splitlines()
    for index, line in enumerate(lines):
        heading = re.match(r"^ {0,3}#{1,6}\s+(.+?)(?:\s+#+\s*)?$", line)
        title = heading[1] if heading else ""
        if not title and index + 1 < len(lines) and re.match(r"^ {0,3}(?:=+|-+)\s*$", lines[index + 1]):
            title = line.strip()
        if not title:
            continue
        title = re.sub(r"!?\[([^\]]+)\]\([^)]*\)", r"\1", title)
        title = html.unescape(re.sub(r"<[^>]+>", "", title)).lower()
        slug = re.sub(r"[^\w\- ]", "", title).replace(" ", "-")
        number = counts.get(slug, 0)
        counts[slug] = number + 1
        ids.add(f"{slug}-{number}" if number else slug)
    ids.update(re.findall(r"\b(?:id|name)=[\"']([^\"']+)[\"']", document.prose))
    return ids


def check_links(documents: list[Document]) -> list[str]:
    errors: list[str] = []
    cache = {document.path: document for document in documents}
    for document in documents:
        label = document.path.relative_to(ROOT).as_posix()
        for line_number, line in enumerate(document.text.splitlines(), 1):
            if "\u2014" in line:
                errors.append(f"{label}:{line_number}: use a regular hyphen instead of an em dash")
        for pattern in (LINK, REFERENCE):
            for match in pattern.finditer(document.prose):
                destination = html.unescape(match[1] or match[2])
                url = urlsplit(destination)
                if url.scheme or url.netloc:
                    continue
                line_number = document.prose.count("\n", 0, match.start()) + 1
                target = unquote(url.path)
                path = (ROOT / target.lstrip("/") if target.startswith("/") else document.path.parent / target).resolve() if target else document.path
                if not path.exists():
                    errors.append(f"{label}:{line_number}: missing local target {destination}")
                    continue
                if not url.fragment:
                    continue
                if path.is_dir():
                    path = path / "README.md"
                if path.suffix.lower() not in (".md", ".txt") or not path.is_file():
                    continue
                if path not in cache:
                    cache[path] = read_document(path)
                if unquote(url.fragment) not in heading_ids(cache[path]):
                    errors.append(f"{label}:{line_number}: missing heading anchor {destination}")
    return errors


def check_quickstart(documents: list[Document]) -> list[str]:
    readme = next(document for document in documents if document.path == ROOT / "README.md")
    examples = [(line, source) for line, source in readme.snippets if re.search(r"(?m)^package main\s*$", source)]
    if len(examples) != 1:
        return ["README.md: expected exactly one complete package main quick-start example"]
    source_path = ROOT / "examples" / "quickstart" / "main.go"
    source = source_path.read_text(encoding="utf-8-sig")
    package = re.search(r"(?m)^package main\s*$", source)
    if package is None:
        return ["examples/quickstart/main.go: expected package main"]
    # Ignore the command's leading documentation comment and trailing whitespace,
    # while keeping the same visible, gofmt-formatted code in both locations.
    normalize = lambda text: "\n".join(line.rstrip() for line in text.strip().splitlines())
    line, documented = examples[0]
    if normalize(documented) != normalize(source[package.start():]):
        return [f"README.md:{line}: quick-start code differs from examples/quickstart/main.go; update both copies"]
    return []


def compile_snippets(documents: list[Document], mode: str) -> int:
    snippets = [(document, line, code) for document in documents for line, code in document.snippets]
    if not snippets:
        raise RuntimeError("No complete Go examples with package declarations were found")
    with tempfile.TemporaryDirectory(prefix="apilog-doccheck-") as directory:
        temporary = Path(directory)
        consumer = ROOT / "scripts" / "_releasecheck"
        module = re.sub(r"(?m)^module .*", "module example.com/apilog-doccheck", (consumer / "go.mod").read_text(encoding="utf-8"))
        if mode == "checkout":
            for relative in (".", "storage", "integrations"):
                checkout = (ROOT / relative).resolve()
                name = re.search(r"(?m)^module (\S+)", (checkout / "go.mod").read_text(encoding="utf-8"))
                assert name is not None
                module += f'\nreplace {name[1]} => "{checkout.as_posix()}"\n'
        (temporary / "go.mod").write_text(module, encoding="utf-8")
        (temporary / "go.sum").write_bytes((consumer / "go.sum").read_bytes())
        for index, (document, line, source) in enumerate(snippets, 1):
            package = temporary / f"snippet{index:03d}"
            package.mkdir()
            (package / "main.go").write_text(source, encoding="utf-8")
            print(f"  snippet{index:03d}: {document.path.relative_to(ROOT).as_posix()}:{line}", flush=True)
        environment = dict(os.environ, GOWORK="off")
        result = subprocess.run(["go", "build", "-mod=mod", "./..."], cwd=temporary, env=environment, text=True, timeout=600, check=False)
        if result.returncode:
            raise RuntimeError("Go documentation examples did not compile; map snippet numbers to the locations above")
    return len(snippets)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--compile", choices=("checkout", "released"), help="build complete Go fences against the checkout or the pinned published consumer modules")
    args = parser.parse_args()
    try:
        listing = subprocess.run(["git", "ls-files", "-z", "--cached", "--others", "--exclude-standard"], cwd=ROOT, capture_output=True, text=True, check=True)
        paths = sorted({ROOT / name for name in listing.stdout.split("\0") if name and (name.endswith(".md") or Path(name).name in ("llms.txt", "llms-full.txt"))})
        documents = [read_document(path) for path in paths if path.is_file()]
        errors = check_links(documents) + check_quickstart(documents)
        if errors:
            print("\n".join(errors), file=sys.stderr)
            return 1
        print(f"Checked local links, heading anchors and punctuation in {len(documents)} documentation files; README quick start matches its runnable source.", flush=True)
        if args.compile:
            count = compile_snippets(documents, args.compile)
            print(f"Compiled {count} complete Go examples against {args.compile} modules.")
        return 0
    except (OSError, subprocess.SubprocessError, RuntimeError) as error:
        print(f"Documentation check failed: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
