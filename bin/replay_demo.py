#!/usr/bin/env python3
"""Replay the canonical mixed-clock scenario and print a summary."""
from __future__ import annotations

import json
import os
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from hlc.replayer import Replayer
from hlc.scenarios import MIXED_CLOCK_SCENARIO
from hlc.tag import Tag


def main() -> int:
    with tempfile.TemporaryDirectory(prefix="hlc-demo-") as datadir:
        artifact = Replayer(MIXED_CLOCK_SCENARIO, datadir).run()
    v = artifact.validation
    print(f"scenario   : {artifact.scenario}")
    print(f"validation : {'OK' if v['ok'] else 'FAIL'}")
    print(
        "events={events} published={published} edges={causal_edges} "
        "duplicates={duplicates} quarantined={quarantined} "
        "crashes={crashes} restarts={restarts}".format(**v)
    )
    for event in artifact.trace:
        tag = Tag.from_dict(event["tag"]) if event["tag"] else None
        label = tag.to_wire() if tag else "-"
        print(f"  {event['seq']:02d} {event['node']} {event['kind']:<10} {label}  {event['detail']}")
    if not v["ok"]:
        print("\n".join(v["errors"]))
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
