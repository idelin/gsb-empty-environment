#!/usr/bin/env python3.11
"""Replay a named scenario and print its deterministic transcript.

Usage: python3.11 replay.py [scenario ...]   (default: all)
"""

from __future__ import annotations

import sys

from audiosched.fixtures import (BLOCK_SIZE, DECLARED_LATENCY_SAMPLES,
                                 NUMERIC_TOLERANCE, SAMPLE_RATE)
from audiosched.scenarios import SCENARIOS


def main(argv: list[str]) -> int:
    names = argv or list(SCENARIOS)
    print(f"sample_rate={SAMPLE_RATE} block_size={BLOCK_SIZE} "
          f"declared_latency_samples={DECLARED_LATENCY_SAMPLES} "
          f"tolerance={NUMERIC_TOLERANCE}")
    for name in names:
        if name not in SCENARIOS:
            print(f"unknown scenario: {name}", file=sys.stderr)
            return 2
        result = SCENARIOS[name]()
        print(f"\n== {name} ==")
        for entry in result["audit"]:
            print(f"  audit: {entry}")
        for block in result["outputs"]:
            head = ", ".join(f"{s:.6f}" for s in block.samples[:4])
            print(f"  output id={block.output_id} n={len(block.samples)} "
                  f"samples=[{head}, ...]")
        print(f"  digest: {result['digest']}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
