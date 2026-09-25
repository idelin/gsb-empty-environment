#!/usr/bin/env python3
"""Replay all sample scenarios and print their results. Exit 1 on failure."""
import sys

from audiosched.fixtures import (BLOCK_SIZE, DECLARED_LATENCY_SECONDS,
                                 SAMPLE_RATE, TOLERANCE)
from audiosched.replay import run_all


def main():
    print(f"sample rate {SAMPLE_RATE} Hz, block {BLOCK_SIZE} samples, "
          f"declared latency {DECLARED_LATENCY_SECONDS * 1e3:.3f} ms, "
          f"tolerance {TOLERANCE:g}")
    results = run_all()
    all_ok = True
    for name, summary in results.items():
        ok = all(summary["checks"].values())
        all_ok = all_ok and ok
        print(f"\n[{'PASS' if ok else 'FAIL'}] {name}")
        for line in summary["log"]:
            print(f"  log: {line}")
        for check, passed in summary["checks"].items():
            print(f"  check {check}: {'ok' if passed else 'FAILED'}")
        for entry in summary["audit"]:
            print(f"  audit: {entry}")
    print(f"\n{'ALL SCENARIOS PASS' if all_ok else 'SOME SCENARIOS FAILED'}")
    return 0 if all_ok else 1


if __name__ == "__main__":
    sys.exit(main())
