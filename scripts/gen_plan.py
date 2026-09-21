"""Regenerate data/build_plan.json from data/packages.json (local only)."""

import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from buildcache.manifest import expected_sha256

ROOT = Path(__file__).resolve().parent.parent
DATA = ROOT / "data"

NODE_SPECS = [
    ("core-lib", "core", ["src/core.c", "include/core.h"], ["lib/core.o", "lib/core.map"]),
    ("core-doc", "core", ["docs/core.md"], ["doc/core.html"]),
    ("plugin-ui", "plugin", ["src/ui.c", "assets/ui.png"], ["bin/ui.so", "bin/ui.sym"]),
]


def main() -> None:
    raw = json.loads((DATA / "packages.json").read_text(encoding="utf-8"))
    known = {p["name"] for p in raw["packages"]}
    nodes = []
    for node_id, package, inputs, outputs in NODE_SPECS:
        if package not in known:
            raise SystemExit(f"unknown package in spec: {package}")
        nodes.append(
            {
                "id": node_id,
                "package": package,
                "inputs": inputs,
                "outputs": [
                    {"path": path, "sha256": expected_sha256(node_id, tuple(inputs), path)}
                    for path in outputs
                ],
            }
        )
    target = DATA / "build_plan.json"
    target.write_text(json.dumps({"nodes": nodes}, indent=2) + "\n", encoding="utf-8")
    print(f"wrote {target} ({len(nodes)} nodes)")


if __name__ == "__main__":
    main()
