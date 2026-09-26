"""Command-line interface for the cross-source evidence engine.

Input is a single JSON document (a file path, or stdin with ``-``):

{
  "budget": 1000000,
  "rules": [{"premises": ["A"], "conclusion": "B"}, ...],
  "base": [{"source": "s", "fact": "A"}, ...],
  "commands": [
    {"op": "add", "source": "t", "fact": "B"},
    {"op": "revoke", "source": "s"},
    {"op": "supports", "fact": "B"},
    {"op": "state"}
  ]
}

``base`` entries are applied first (in order), then each command. Supports
are emitted as sorted lists of source ids, ordered by (size, lexicographic).
Every result carries the engine's ``interrupted`` budget flag.
"""

from __future__ import annotations

import argparse
import json
import sys
from typing import Any

from .engine import Engine, Rule


def _load_rules(spec: Any) -> list[Rule]:
    if spec is None:
        return []
    if not isinstance(spec, list):
        raise ValueError("rules must be a list")
    rules: list[Rule] = []
    for item in spec:
        if not isinstance(item, dict):
            raise ValueError("each rule must be an object")
        premises = item.get("premises", [])
        if not isinstance(premises, list):
            raise ValueError("rule premises must be a list")
        rules.append(Rule(premises, item["conclusion"]))
    return rules


def _run(document: dict[str, Any]) -> list[dict[str, Any]]:
    engine = Engine(_load_rules(document.get("rules")),
                    budget=int(document.get("budget", 1_000_000)))
    results: list[dict[str, Any]] = []

    def snapshot(fact: str | None = None) -> dict[str, Any]:
        entry = {
            "interrupted": engine.interrupted,
            "derivations": engine.derivations,
        }
        if fact is None:
            entry["facts"] = engine.all_facts()
            entry["state"] = {
                name: [sorted(s) for s in engine.supports(name)]
                for name in engine.all_facts()
            }
        else:
            entry["fact"] = fact
            entry["supported"] = engine.supported(fact)
            entry["supports"] = [
                sorted(s) for s in engine.supports(fact)
            ]
        return entry

    for assertion in document.get("base", []):
        engine.add_source(assertion["source"], assertion["fact"])

    commands = document.get("commands", [])
    if not isinstance(commands, list):
        raise ValueError("commands must be a list")
    for command in commands:
        op = command.get("op")
        if op == "add":
            engine.add_source(command["source"], command["fact"])
            results.append({
                "op": "add", "source": command["source"],
                "fact": command["fact"], "interrupted": engine.interrupted,
                "derivations": engine.derivations,
            })
        elif op == "revoke":
            engine.revoke_source(command["source"])
            results.append({
                "op": "revoke", "source": command["source"],
                "interrupted": engine.interrupted,
                "derivations": engine.derivations,
            })
        elif op == "supports":
            results.append(snapshot(command["fact"]))
        elif op == "state":
            results.append(snapshot())
        else:
            raise ValueError(f"unknown op: {op!r}")
    return results


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Cross-source confident-evidence rule engine")
    parser.add_argument("input", help="JSON input file, or '-' for stdin")
    args = parser.parse_args(argv)

    if args.input == "-":
        document = json.load(sys.stdin)
    else:
        with open(args.input, encoding="utf-8") as handle:
            document = json.load(handle)
    try:
        results = _run(document)
    except (ValueError, KeyError) as exc:
        print(f"input error: {exc}", file=sys.stderr)
        return 2
    json.dump({"results": results}, sys.stdout, ensure_ascii=False, indent=2)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
