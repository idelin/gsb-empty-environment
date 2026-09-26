"""限额共享信用预留账本（单进程、可线性化）。

模型：
- 一个父账户拥有总额度 parent_limit；多个子账户各有自己的额度 limit。
- 父占用 = 父已提交金额 + 所有活跃预留金额；父可用 = parent_limit - 父占用。
- 子占用 = 子已提交金额 + 该子活跃预留金额；子可用同理。
- reserve / commit / cancel 均携带幂等键；同一键重放返回首次结果，不产生重复效果。
- commit 金额可以少于预留（差额自动释放），不得超过预留。
- set_parent_limit 下调不追溯撤销已提交/已预留金额，但新增预留必须满足当前可用额。
- 所有操作在单把可重入锁内完成“校验 + 生效”，失败请求不产生任何分录。
"""
from __future__ import annotations

import argparse
import json
import sys
import threading


class LedgerError(Exception):
    """业务校验错误。code 为机器可读错误码。"""

    def __init__(self, code: str, message: str):
        super().__init__(message)
        self.code = code


def _is_int(value) -> bool:
    return isinstance(value, int) and not isinstance(value, bool)


def _require_positive_amount(amount) -> None:
    if not _is_int(amount) or amount <= 0:
        raise LedgerError("invalid_amount", "amount must be a positive integer")


def _require_non_negative_amount(amount) -> None:
    if not _is_int(amount) or amount < 0:
        raise LedgerError("invalid_amount", "amount must be a non-negative integer")


class CreditLedger:
    def __init__(self, parent_limit: int, debug: bool = False):
        if not _is_int(parent_limit) or parent_limit < 0:
            raise ValueError("parent_limit must be a non-negative integer")
        self._lock = threading.RLock()
        self._debug = debug
        self._parent_limit = parent_limit
        self._parent_committed = 0
        # 允许父额度下调到当前占用之下：grace 记录占用允许达到的上界，
        # 占用高于额度期间只减不增，降回额度以下后恢复正常规则。
        self._parent_grace = parent_limit
        self._children: dict[str, dict] = {}
        self._reservations: dict[str, dict] = {}
        self._idempotency: dict[str, dict] = {}

    # ---- 内部派生量（调用方须持锁） ----

    def _reserved_total(self, child: str | None = None) -> int:
        return sum(
            r["amount"]
            for r in self._reservations.values()
            if child is None or r["child"] == child
        )

    def _parent_available(self) -> int:
        return self._parent_limit - self._parent_committed - self._reserved_total()

    def _child_available(self, child: str) -> int:
        c = self._children[child]
        return c["limit"] - c["committed"] - self._reserved_total(child)

    def _require_child(self, child: str) -> None:
        if child not in self._children:
            raise LedgerError("unknown_child", f"unknown child account: {child!r}")

    def _assert_invariants(self) -> None:
        assert self._parent_committed >= 0
        usage = self._parent_committed + self._reserved_total()
        assert usage <= self._parent_grace, (usage, self._parent_grace)
        for name, c in self._children.items():
            assert c["committed"] >= 0
            assert c["committed"] + self._reserved_total(name) <= c["limit"], name
        for r in self._reservations.values():
            assert r["child"] in self._children
            assert r["amount"] > 0

    def _replay_or(self, key: str):
        return self._idempotency.get(key)

    def _record(self, key: str, result: dict) -> dict:
        self._idempotency[key] = result
        if self._debug:
            self._assert_invariants()
        return dict(result)

    # ---- 管理操作 ----

    def add_child(self, name: str, limit: int) -> None:
        with self._lock:
            if not _is_int(limit) or limit < 0:
                raise LedgerError("invalid_limit", "child limit must be a non-negative integer")
            if name in self._children:
                raise LedgerError("duplicate_child", f"child already exists: {name!r}")
            self._children[name] = {"limit": limit, "committed": 0}

    def set_parent_limit(self, new_limit: int) -> dict:
        """下调不追溯撤销已提交金额；仅影响后续预留的可用额判断。"""
        with self._lock:
            if not _is_int(new_limit) or new_limit < 0:
                raise LedgerError("invalid_limit", "parent limit must be a non-negative integer")
            self._parent_limit = new_limit
            usage = self._parent_committed + self._reserved_total()
            self._parent_grace = max(new_limit, min(self._parent_grace, usage))
            if self._debug:
                self._assert_invariants()
            return {"ok": True, "parent_limit": new_limit}

    # ---- 幂等业务操作 ----

    def reserve(self, child: str, amount: int, key: str) -> dict:
        with self._lock:
            cached = self._replay_or(key)
            if cached is not None:
                return dict(cached)
            try:
                self._require_child(child)
                _require_positive_amount(amount)
                if amount > self._child_available(child):
                    raise LedgerError("child_limit_exceeded", "amount exceeds child available credit")
                if amount > self._parent_available():
                    raise LedgerError("parent_limit_exceeded", "amount exceeds parent available credit")
                rid = f"rsv_{key}"
                self._reservations[rid] = {"child": child, "amount": amount}
                result = {"ok": True, "reservation": rid, "child": child, "amount": amount}
            except LedgerError as exc:
                result = {"ok": False, "error": exc.code, "message": str(exc)}
            return self._record(key, result)

    def commit(self, reservation: str, amount: int, key: str) -> dict:
        """提交金额可少于预留（差额释放），不得超过预留；失败不产生任何分录。"""
        with self._lock:
            cached = self._replay_or(key)
            if cached is not None:
                return dict(cached)
            try:
                _require_non_negative_amount(amount)
                r = self._reservations.get(reservation)
                if r is None:
                    raise LedgerError("reservation_not_found", f"unknown reservation: {reservation!r}")
                if amount > r["amount"]:
                    raise LedgerError("commit_exceeds_reserved", "commit amount exceeds reserved amount")
                child = r["child"]
                del self._reservations[reservation]
                self._children[child]["committed"] += amount
                self._parent_committed += amount
                result = {
                    "ok": True,
                    "reservation": reservation,
                    "child": child,
                    "committed": amount,
                    "released": r["amount"] - amount,
                }
            except LedgerError as exc:
                result = {"ok": False, "error": exc.code, "message": str(exc)}
            return self._record(key, result)

    def cancel(self, reservation: str, key: str) -> dict:
        with self._lock:
            cached = self._replay_or(key)
            if cached is not None:
                return dict(cached)
            try:
                r = self._reservations.pop(reservation, None)
                if r is None:
                    raise LedgerError("reservation_not_found", f"unknown reservation: {reservation!r}")
                result = {
                    "ok": True,
                    "reservation": reservation,
                    "child": r["child"],
                    "released": r["amount"],
                }
            except LedgerError as exc:
                result = {"ok": False, "error": exc.code, "message": str(exc)}
            return self._record(key, result)

    # ---- 只读快照 ----

    def state(self) -> dict:
        with self._lock:
            return {
                "parent_limit": self._parent_limit,
                "parent_committed": self._parent_committed,
                "parent_reserved": self._reserved_total(),
                "parent_available": self._parent_available(),
                "children": {
                    name: {
                        "limit": c["limit"],
                        "committed": c["committed"],
                        "reserved": self._reserved_total(name),
                        "available": self._child_available(name),
                    }
                    for name, c in self._children.items()
                },
                "reservations": {
                    rid: {"child": r["child"], "amount": r["amount"]}
                    for rid, r in sorted(self._reservations.items())
                },
            }


def dispatch(ledger: CreditLedger, cmd: dict) -> dict:
    op = cmd.get("op")
    if op == "add_child":
        ledger.add_child(cmd["child"], cmd["limit"])
        return {"ok": True, "child": cmd["child"]}
    if op == "reserve":
        return ledger.reserve(cmd["child"], cmd["amount"], cmd["key"])
    if op == "commit":
        return ledger.commit(cmd["reservation"], cmd["amount"], cmd["key"])
    if op == "cancel":
        return ledger.cancel(cmd["reservation"], cmd["key"])
    if op == "set_parent_limit":
        return ledger.set_parent_limit(cmd["limit"])
    if op == "state":
        return {"ok": True, "state": ledger.state()}
    raise LedgerError("unknown_op", f"unknown op: {op!r}")


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        description="限额共享信用预留账本：从 stdin 读取 JSON Lines 命令，向 stdout 输出 JSON Lines 结果。"
    )
    parser.add_argument("--parent-limit", type=int, required=True, help="父账户总额度（非负整数）")
    args = parser.parse_args(argv)
    ledger = CreditLedger(args.parent_limit)
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            out = dispatch(ledger, json.loads(line))
        except LedgerError as exc:
            out = {"ok": False, "error": exc.code, "message": str(exc)}
        except (json.JSONDecodeError, KeyError, TypeError, AttributeError) as exc:
            out = {"ok": False, "error": "bad_request", "message": str(exc)}
        print(json.dumps(out, ensure_ascii=False), flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
