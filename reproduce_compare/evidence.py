"""Evidence bundle serialization.

A bundle binds the conclusion to the two raw input trees (via manifest
digests), the exact rules used, and every ignored difference / substantive
difference / undeterminable reason.
"""

from __future__ import annotations

import json
from decimal import Decimal

from .digest import digest_bytes
from .rules import RuleSet, rules_to_bytes

SCHEMA_VERSION = 1


def build_bundle(result: ComparisonResult, ruleset: RuleSet) -> dict[str, object]:
    rules_blob = rules_to_bytes(ruleset)
    return {
        "schema_version": SCHEMA_VERSION,
        "tool": "reproduce_compare",
        "inputs": {
            "old_root": result.old_root,
            "new_root": result.new_root,
            "old_manifest_digest": result.old_manifest_digest,
            "new_manifest_digest": result.new_manifest_digest,
        },
        "rules": {
            "sha256": digest_bytes(rules_blob),
            "document": json.loads(rules_blob.decode("utf-8")),
        },
        "conclusion": result.to_evidence(),
    }


def _default(obj):
    if isinstance(obj, Decimal):
        return {"__decimal__": format(obj, "f")}
    raise TypeError(f"cannot serialize {type(obj)!r}")


def write_bundle(path: str, bundle: dict[str, object]) -> None:
    with open(path, "w", encoding="utf-8") as handle:
        json.dump(bundle, handle, ensure_ascii=False, indent=2, sort_keys=True,
                  default=_default)
        handle.write("\n")


def read_bundle(path: str) -> dict[str, object]:
    with open(path, encoding="utf-8") as handle:
        return json.load(handle)
