from __future__ import annotations

import argparse
import json
import sys
from typing import Any

from .manifest import ManifestError, load_manifest_file
from .resolver import SelectionError, resolve


def result_payload(selection: Any) -> dict[str, Any]:
    return {
        "ok": True,
        "build": selection.build,
        "target": selection.target,
        "capability_to_implementation": selection.implementations,
        "selected_packages": selection.selected_packages,
        "tie_breaker": list(selection.tie_breaker),
    }


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="package-selector",
        description="Resolve local package capabilities without network access.",
    )
    parser.add_argument("manifest", help="Path to a local JSON manifest")
    parser.add_argument("--build", help="Build name for a multi-build manifest")
    parser.add_argument("--indent", type=int, default=2, help="JSON output indentation")
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        manifest = load_manifest_file(args.manifest)
        selection = resolve(manifest, args.build)
    except (OSError, json.JSONDecodeError, ManifestError, SelectionError) as exc:
        payload = {"ok": False, "error": str(exc)}
        print(json.dumps(payload, ensure_ascii=False, indent=2), file=sys.stderr)
        return 1
    print(json.dumps(result_payload(selection), ensure_ascii=False, indent=args.indent))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
