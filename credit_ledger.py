"""Shared-limit credit reservation ledger.

Single-process, linearizable implementation: every mutating operation is
serialized through one reentrant lock, so each request is atomic and the
system behaves as if requests execute one at a time in some total order.

Model:
  - One parent account with a shared total limit.
  - Any number of child accounts, each with its own limit, drawing on the
    parent's shared limit.
  - reserve / commit / cancel carry idempotency keys. A successful result
    is memoized per (op, key); replaying the same key returns the stored
    response without re-applying effects. Failed requests record nothing,
    so they may be retried later (e.g. after capacity is released).
  - commit may be less than the reserved amount (the difference is
    released) but never more.
  - Lowering the parent limit never revokes committed amounts; it only
    causes new reservations beyond the current available amount to be
    rejected.

All amounts are non-negative integers (e.g. minor currency units).
"""

from __future__ import annotations

import json
import sys
import threading


class LedgerError(Exception):
    def __init__(self, code: str):
        super().__init__(code)
        self.code = code


def _check_amount(value) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or value < 0:
        raise LedgerError("invalid_amount")
    return value


class Ledger:
    def __init__(self, parent_limit: int = 0):
        self._lock = threading.RLock()
        self.parent_limit = _check_amount(parent_limit)
        self.parent_reserved = 0
        self.parent_committed = 0
        self.children: dict[str, dict] = {}
        self.reservations: dict[str, dict] = {}
        self._idempotency: dict[tuple[str, str], dict] = {}

    def _child(self, name: str) -> dict:
        child = self.children.get(name)
        if child is None:
            raise LedgerError("unknown_child")
        return child

    def _replay(self, op: str, key: str):
        return self._idempotency.get((op, key))

    def _memoize(self, op: str, key: str, result: dict) -> dict:
        self._idempotency[(op, key)] = dict(result)
        return result

    def add_child(self, name: str, limit: int) -> dict:
        with self._lock:
            if not isinstance(name, str) or not name:
                raise LedgerError("invalid_child")
            if name in self.children:
                raise LedgerError("child_exists")
            self.children[name] = {
                "limit": _check_amount(limit),
                "reserved": 0,
                "committed": 0,
            }
            return {"ok": True, "child": name}

    def set_parent_limit(self, limit: int) -> dict:
        with self._lock:
            self.parent_limit = _check_amount(limit)
            return {"ok": True, "parent_limit": self.parent_limit}

    def set_child_limit(self, name: str, limit: int) -> dict:
        with self._lock:
            self._child(name)["limit"] = _check_amount(limit)
            return {"ok": True, "child": name}

    def reserve(self, child_name: str, amount: int, key: str) -> dict:
        with self._lock:
            cached = self._replay("reserve", key)
            if cached is not None:
                return dict(cached)
            child = self._child(child_name)
            amount = _check_amount(amount)
            if amount > child["limit"] - child["reserved"] - child["committed"]:
                raise LedgerError("child_limit_exceeded")
            if amount > self.parent_limit - self.parent_reserved - self.parent_committed:
                raise LedgerError("parent_limit_exceeded")
            self.reservations[key] = {
                "child": child_name,
                "amount": amount,
                "status": "active",
                "committed": 0,
            }
            child["reserved"] += amount
            self.parent_reserved += amount
            return self._memoize("reserve", key, {
                "ok": True, "reservation": key, "reserved": amount,
            })

    def commit(self, reservation: str, amount: int, key: str) -> dict:
        with self._lock:
            cached = self._replay("commit", key)
            if cached is not None:
                return dict(cached)
            res = self.reservations.get(reservation)
            if res is None:
                raise LedgerError("unknown_reservation")
            if res["status"] != "active":
                raise LedgerError("reservation_not_active")
            amount = _check_amount(amount)
            if amount > res["amount"]:
                raise LedgerError("exceeds_reserved")
            child = self.children[res["child"]]
            released = res["amount"] - amount
            child["reserved"] -= res["amount"]
            self.parent_reserved -= res["amount"]
            child["committed"] += amount
            self.parent_committed += amount
            res["status"] = "committed"
            res["committed"] = amount
            return self._memoize("commit", key, {
                "ok": True, "reservation": reservation,
                "committed": amount, "released": released,
            })

    def cancel(self, reservation: str, key: str) -> dict:
        with self._lock:
            cached = self._replay("cancel", key)
            if cached is not None:
                return dict(cached)
            res = self.reservations.get(reservation)
            if res is None:
                raise LedgerError("unknown_reservation")
            if res["status"] != "active":
                raise LedgerError("reservation_not_active")
            child = self.children[res["child"]]
            child["reserved"] -= res["amount"]
            self.parent_reserved -= res["amount"]
            res["status"] = "cancelled"
            return self._memoize("cancel", key, {
                "ok": True, "reservation": reservation, "released": res["amount"],
            })

    def snapshot(self) -> dict:
        with self._lock:
            return {
                "parent": {
                    "limit": self.parent_limit,
                    "reserved": self.parent_reserved,
                    "committed": self.parent_committed,
                    "available": self.parent_limit
                        - self.parent_reserved - self.parent_committed,
                },
                "children": {
                    name: {
                        "limit": c["limit"],
                        "reserved": c["reserved"],
                        "committed": c["committed"],
                        "available": c["limit"] - c["reserved"] - c["committed"],
                    }
                    for name, c in self.children.items()
                },
                "reservations": {rid: dict(r) for rid, r in self.reservations.items()},
            }


def handle(ledger: Ledger, request: dict) -> dict:
    if not isinstance(request, dict):
        raise LedgerError("bad_request")
    op = request.get("op")
    try:
        if op == "init":
            if ledger.children or ledger.reservations:
                raise LedgerError("already_initialized")
            result = ledger.set_parent_limit(request["parent_limit"])
        elif op == "add_child":
            result = ledger.add_child(request["child"], request["limit"])
        elif op == "set_parent_limit":
            result = ledger.set_parent_limit(request["limit"])
        elif op == "set_child_limit":
            result = ledger.set_child_limit(request["child"], request["limit"])
        elif op == "reserve":
            result = ledger.reserve(request["child"], request["amount"], request["key"])
        elif op == "commit":
            result = ledger.commit(request["reservation"], request["amount"], request["key"])
        elif op == "cancel":
            result = ledger.cancel(request["reservation"], request["key"])
        elif op == "state":
            result = ledger.snapshot()
        else:
            raise LedgerError("unknown_op")
    except (KeyError, TypeError):
        raise LedgerError("bad_request")
    if op != "state":
        result = dict(result)
        result["op"] = op
    return result


def main(stream=None, out=None) -> int:
    stream = stream if stream is not None else sys.stdin
    out = out if out is not None else sys.stdout
    ledger = Ledger()
    for line in stream:
        line = line.strip()
        if not line:
            continue
        try:
            request = json.loads(line)
            response = handle(ledger, request)
        except LedgerError as exc:
            response = {"ok": False, "error": exc.code}
        except (json.JSONDecodeError, ValueError):
            response = {"ok": False, "error": "bad_request"}
        out.write(json.dumps(response) + "\n")
        out.flush()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
