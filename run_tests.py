"""Run the full test suite and write the results to tests/results/.

Usage:  python3 run_tests.py
"""
import io
import os
import subprocess
import sys
import time

ROOT = os.path.dirname(os.path.abspath(__file__))
RESULTS = os.path.join(ROOT, "tests", "results")


def main() -> int:
    os.makedirs(RESULTS, exist_ok=True)
    started = time.strftime("%Y-%m-%d %H:%M:%S")
    proc = subprocess.run(
        [sys.executable, "-m", "unittest", "discover", "-s", "tests", "-v"],
        cwd=ROOT,
        capture_output=True,
        text=True,
    )
    report = proc.stderr + proc.stdout
    summary = [
        f"wmcoord test run: {started}",
        f"python: {sys.version.split()[0]}",
        "",
        report,
    ]
    out_path = os.path.join(RESULTS, "latest.txt")
    with open(out_path, "w") as handle:
        handle.write("\n".join(summary))
    tail = report.strip().splitlines()[-3:]
    print("\n".join(tail))
    print(f"\nfull report: {out_path}")
    return proc.returncode


if __name__ == "__main__":
    raise SystemExit(main())
