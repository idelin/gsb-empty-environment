"""Command line interface for the bounded replay-deduplicating inbox.

Exit codes:
    0  success (committed / cached / duplicate / state query)
    2  message explicitly rejected (beyond the out-of-order window)
    3  invalid arguments or bad batch input
   99  process killed by an injected crash
"""

from __future__ import annotations

import argparse
import json
import sys
from typing import Optional, Sequence

from .inbox import BoundedInbox, _hard_exit


def _emit(obj: dict, stream=sys.stdout) -> None:
    stream.write(json.dumps(obj, ensure_ascii=False) + "\n")
    stream.flush()


def _build_hook(point: Optional[str]):
    if point is None:
        return None

    def hook(fired_point: str) -> None:
        if fired_point == point:
            _hard_exit(fired_point)

    return hook


def _cmd_init(args: argparse.Namespace) -> int:
    inbox = BoundedInbox(args.db, window=args.window)
    _emit({"op": "init", "db": args.db, "window": inbox.window})
    inbox.close()
    return 0


def _cmd_deliver(args: argparse.Namespace) -> int:
    inbox = BoundedInbox(
        args.db, window=args.window, crash_hook=_build_hook(args.crash)
    )
    try:
        result = inbox.deliver(args.sender, args.seq, args.value)
        _emit(result.to_dict())
        return 2 if result.status == "rejected" else 0
    finally:
        inbox.close()


def _cmd_state(args: argparse.Namespace) -> int:
    inbox = BoundedInbox(args.db, window=args.window)
    try:
        if args.senders:
            _emit({"states": [inbox.state(s) for s in args.senders]})
        else:
            rows = inbox.conn.execute("SELECT sender FROM senders ORDER BY sender")
            _emit({"states": [inbox.state(r[0]) for r in rows]})
    finally:
        inbox.close()
    return 0


def _cmd_batch(args: argparse.Namespace) -> int:
    inbox = BoundedInbox(args.db, window=args.window)
    exit_code = 0
    try:
        for lineno, raw in enumerate(sys.stdin, start=1):
            line = raw.strip()
            if not line or line.startswith("#"):
                continue
            try:
                req = json.loads(line)
                op = req["op"]
            except (ValueError, TypeError, KeyError) as exc:
                _emit(
                    {"error": f"line {lineno}: invalid request: {exc}"},
                    stream=sys.stderr,
                )
                return 3

            if op == "deliver":
                try:
                    hook = _build_hook(req.get("crash"))
                    if hook is not None:
                        inbox._crash_hook = hook
                    result = inbox.deliver(
                        req["sender"], int(req["seq"]), int(req["value"])
                    )
                    _emit(result.to_dict())
                    if result.status == "rejected":
                        exit_code = 2
                except (TypeError, ValueError) as exc:
                    _emit(
                        {"error": f"line {lineno}: {exc}"}, stream=sys.stderr
                    )
                    return 3
            elif op == "state":
                senders = req.get("senders")
                if senders:
                    _emit({"states": [inbox.state(s) for s in senders]})
                else:
                    rows = inbox.conn.execute(
                        "SELECT sender FROM senders ORDER BY sender"
                    )
                    _emit({"states": [inbox.state(r[0]) for r in rows]})
            else:
                _emit(
                    {"error": f"line {lineno}: unknown op {op!r}"},
                    stream=sys.stderr,
                )
                return 3
    finally:
        inbox.close()
    return exit_code


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="dedup_inbox",
        description="Bounded replay-deduplicating inbox (exactly-once).",
    )
    parser.add_argument("--db", required=True, help="path to the SQLite inbox file")
    parser.add_argument(
        "--window", type=int, default=3, help="out-of-order window W (default 3)"
    )

    sub = parser.add_subparsers(dest="op", required=True)

    p_init = sub.add_parser("init", help="create / open an inbox")
    p_init.set_defaults(func=_cmd_init)

    p_deliver = sub.add_parser("deliver", help="deliver one message")
    p_deliver.add_argument("--sender", required=True)
    p_deliver.add_argument("--seq", type=int, required=True)
    p_deliver.add_argument("--value", type=int, required=True)
    p_deliver.add_argument(
        "--crash",
        choices=["before_write", "after_write", "after_commit"],
        help="inject a crash around the transactional write",
    )
    p_deliver.set_defaults(func=_cmd_deliver)

    p_state = sub.add_parser("state", help="print committed and cached state")
    p_state.add_argument("senders", nargs="*")
    p_state.set_defaults(func=_cmd_state)

    p_batch = sub.add_parser(
        "batch", help="read JSONL requests from stdin "
        "({\"op\":\"deliver\",\"sender\":..,"
        "\"seq\":..,\"value\":..[,\"crash\":..]} or {\"op\":\"state\"})"
    )
    p_batch.set_defaults(func=_cmd_batch)
    return parser


def main(argv: Optional[Sequence[str]] = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    try:
        return args.func(args)
    except (ValueError, TypeError) as exc:
        _emit({"error": str(exc)}, stream=sys.stderr)
        return 3


if __name__ == "__main__":
    sys.exit(main())
