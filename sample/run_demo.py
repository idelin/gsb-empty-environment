#!/usr/bin/env python3
"""End-to-end demonstration with real subprocess crashes and recovery.

Modes:
  reset                       remove the demo state directory
  ingest  --crash-after SEQ   commit fixture batches up to SEQ, then crash
  send    --fail-id ID        send one batch; --fail-id rejects that delivery
          --lose-ack-id ID    accept payload, lose the acknowledgement
  status                      print durable state as JSON
  demo                        run the full scenario (default)

The full demo executes each phase in a separate Python subprocess, so a
simulated crash is a real process death; SQLite and the sink journal carry
all state across restarts.
"""

from __future__ import annotations

import argparse
import json
import shutil
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

from outbox.model import load_changes, output_id  # noqa: E402
from outbox.processor import Processor  # noqa: E402
from outbox.sink import LocalSink  # noqa: E402
from outbox.store import Store  # noqa: E402

DEMO_DIR = Path(__file__).resolve().parent / ".demo"
DB_PATH = DEMO_DIR / "pipeline.sqlite3"
SINK_PATH = DEMO_DIR / "sink_journal.json"
FIXTURE_PATH = Path(__file__).resolve().parent / "fixtures" / "accounts.json"


def _open() -> tuple[Store, LocalSink, Processor]:
    store = Store.open(str(DB_PATH))
    sink = LocalSink(SINK_PATH)
    return store, sink, Processor(store, sink)


def cmd_reset() -> int:
    if DEMO_DIR.exists():
        shutil.rmtree(DEMO_DIR)
    print("state reset")
    return 0


def cmd_ingest(crash_after: int | None) -> int:
    changes = load_changes(FIXTURE_PATH)
    store, sink, processor = _open()
    try:
        report = processor.ingest(changes, crash_after_seq=crash_after)
        print(
            f"ingest ok: committed={report.committed} "
            f"outputs_enqueued={report.outputs_enqueued} "
            f"high_water={report.high_water_seq}"
        )
        return 0
    except Exception as exc:  # noqa: BLE001 - demo prints any failure mode
        from outbox.errors import CrashInjected

        if isinstance(exc, CrashInjected):
            print(f"CRASH (exit 99): {exc}")
            return 99
        print(f"ingest failed: {type(exc).__name__}: {exc}")
        return 1
    finally:
        store.close()


def cmd_send(fail_id: str | None, lose_ack_id: str | None) -> int:
    store, sink, processor = _open()
    try:
        if fail_id:
            sink.fail_once(fail_id)
        if lose_ack_id:
            sink.lose_ack_once(lose_ack_id)
        report = processor.send_pending()
        status = "ok" if report.ok else "retryable-failure"
        print(
            f"send {status}: sent_seq={report.sent} "
            f"acked={len(report.acked_ids)} "
            f"duplicates={report.duplicates} failures={report.failures}"
        )
        return 0
    finally:
        store.close()


def cmd_status() -> int:
    store, sink, _ = _open()
    try:
        state = {
            **store.recovery_info(),
            "projection": {
                f"{table}/{key}": value
                for (table, key), value in sorted(store.projection_snapshot().items())
            },
            "sink": sink.stats(),
        }
    finally:
        store.close()
    print(json.dumps(state, indent=2, ensure_ascii=False, sort_keys=True))
    return 0


def _run_subprocess(args: list[str]) -> int:
    print(f"$ python3.11 sample/run_demo.py {' '.join(args)}")
    proc = subprocess.run(
        [sys.executable, str(Path(__file__).resolve()), *args],
        capture_output=True,
        text=True,
    )
    if proc.stdout:
        print(proc.stdout, end="")
    if proc.stderr:
        print(proc.stderr, end="", file=sys.stderr)
    return proc.returncode


def _status_inproc() -> dict:
    store, sink, _ = _open()
    try:
        return {
            **store.recovery_info(),
            "projection": {
                f"{table}/{key}": value
                for (table, key), value in sorted(
                    store.projection_snapshot().items()
                )
            },
            "sink": sink.stats(),
        }
    finally:
        store.close()


def cmd_demo() -> int:
    changes = load_changes(FIXTURE_PATH)
    idem = {change.commit_seq: output_id(change) for change in changes}

    print("== phase 0: fresh start ==")
    cmd_reset()

    print("\n== phase 1: crash right after the logical commits of seq 1,2 ==")
    print("    (offset + business state + outbox are already durable;")
    print("     no output has been sent yet)")
    code = _run_subprocess(["ingest", "--crash-after", "2"])
    print(f"(exit code {code})")
    print(json.dumps(_status_inproc(), indent=2, ensure_ascii=False, sort_keys=True))

    print("\n== phase 2: restart, replay fixture (already-seen batches skipped) ==")
    code = _run_subprocess(["ingest"])
    print(f"(exit code {code})")

    print("\n== phase 3: restart, send batch; seq 3 delivery is rejected")
    print("    (seq 1,2 succeed and are acked; seq 3,4 stay pending)")
    code = _run_subprocess(["send", "--fail-id", idem[3]])
    print(f"(exit code {code})")
    print(json.dumps(_status_inproc(), indent=2, ensure_ascii=False, sort_keys=True))

    print("\n== phase 4: restart, retry the batch; seq 3 payload is accepted")
    print("    but its acknowledgement is lost (seq 4 never reached)")
    code = _run_subprocess(["send", "--lose-ack-id", idem[3]])
    print(f"(exit code {code})")
    print(json.dumps(_status_inproc(), indent=2, ensure_ascii=False, sort_keys=True))

    print("\n== phase 5: restart, resend seq 3; sink dedupes by idempotency id")
    print("    seq 4 is then delivered; outbox drains to zero pending")
    code = _run_subprocess(["send"])
    print(f"(exit code {code})")

    print("\n== final state ==")
    state = _status_inproc()
    print(json.dumps(state, indent=2, ensure_ascii=False, sort_keys=True))

    invariants = [
        state["high_water_seq"] == 4,
        state["committed_sequences"] == [1, 2, 3, 4],
        state["pending_outputs"] == 0,
        state["sink"]["unique_delivered"] == 4,
        state["sink"]["duplicate_deliveries"] >= 1,
        set(state["projection"]) == {"accounts/acct-2"},
        state["projection"]["accounts/acct-2"]["balance"] == 30,
    ]
    if all(invariants):
        print("\nRESULT: PASS - no acknowledged input lost, no business effect duplicated")
        return 0
    print("\nRESULT: FAIL - invariant violation")
    return 1


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command")

    sub.add_parser("reset")

    ingest = sub.add_parser("ingest")
    ingest.add_argument("--crash-after", type=int, default=None)

    send = sub.add_parser("send")
    send.add_argument("--fail-id", default=None)
    send.add_argument("--lose-ack-id", default=None)

    sub.add_parser("status")
    sub.add_parser("demo")
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    command = args.command or "demo"
    if command == "reset":
        return cmd_reset()
    if command == "ingest":
        return cmd_ingest(args.crash_after)
    if command == "send":
        return cmd_send(args.fail_id, args.lose_ack_id)
    if command == "status":
        return cmd_status()
    if command == "demo":
        return cmd_demo()
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
