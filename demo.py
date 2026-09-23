"""Run every local fixture through the processor and print the verdicts.

Usage: python3.11 demo.py [fixtures_dir]
"""

import json
import sys
from pathlib import Path

from cdc_verify import Event, Processor


def run_fixture(path: Path) -> Processor:
    data = json.loads(path.read_text(encoding="utf-8"))
    print(f"== {path.name}: {data.get('name', '')}")
    proc = Processor(seed=data.get("seed"))
    for step in data.get("steps", []):
        if "resolve" in step:
            req = step["resolve"]
            status = proc.resolve(req["table"], req["pk"], req["strategy"])
            print(f"  resolve {req['table']}/{req['pk']} [{req['strategy']}]"
                  f" -> {status}")
            continue
        events = [Event(txn_id=step["txn_id"], **ev) for ev in step["events"]]
        status = proc.process_transaction(events)
        print(f"  txn {step['txn_id']:<4} -> {status}")

    report = proc.report
    if report.duplicates:
        print(f"  duplicates skipped: {report.duplicates}")
    for conflict in report.conflicts:
        print(f"  conflict: {json.dumps(conflict.as_dict(), ensure_ascii=False)}")
    for blocked in report.blocked:
        print(f"  blocked:  {json.dumps(blocked, ensure_ascii=False)}")
    for resolution in report.resolutions:
        print(f"  resolved: {json.dumps(resolution, ensure_ascii=False)}")
    store = {f"{table}/{pk}": row for (table, pk), row in sorted(proc.store.items())}
    print(f"  final store: {json.dumps(store, ensure_ascii=False)}")
    print()
    return proc


def main() -> None:
    fixtures_dir = Path(sys.argv[1] if len(sys.argv) > 1 else "fixtures")
    for path in sorted(fixtures_dir.glob("*.json")):
        run_fixture(path)


if __name__ == "__main__":
    main()
