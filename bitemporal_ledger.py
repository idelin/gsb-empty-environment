"""双时态计价回放账本（bitemporal price ledger）。

同时按两个时间轴查询价格：
  - 业务生效时间 (business / valid time)：价格区间 [valid_from, valid_to)，左闭右开；
  - 系统知悉时间 (system / knowledge time)：规则被账本知悉的时刻 known_at。

冲突消解：同商品同币种、同一业务时刻上重叠的规则，
取 (known_at, revision) 最大者。撤销只在撤销被知悉之后（含该时刻）生效。

金额一律为整数分。同 (product, currency, revision) 但内容不同的插入会被拒绝；
内容完全相同的重复插入是幂等空操作。

查询结果带缓存；插入迟到修订或撤销时，仅失效可能受影响的缓存项。
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from dataclasses import dataclass, field
from typing import Optional


class LedgerError(Exception):
    """账本操作的基础异常。"""


class ConflictError(LedgerError):
    """同修订号但内容不同的插入冲突。"""


class UnknownRevisionError(LedgerError):
    """撤销一个不存在的修订。"""


@dataclass(frozen=True)
class PriceRule:
    product: str
    currency: str
    amount: int          # 整数分
    valid_from: int      # 业务生效起点（含）
    valid_to: int        # 业务生效终点（不含）
    revision: int        # 修订号
    known_at: int        # 系统知悉时间

    def content_key(self) -> tuple:
        return (self.amount, self.valid_from, self.valid_to)

    def to_dict(self) -> dict:
        return {
            "product": self.product,
            "currency": self.currency,
            "amount": self.amount,
            "valid_from": self.valid_from,
            "valid_to": self.valid_to,
            "revision": self.revision,
            "known_at": self.known_at,
        }

    @staticmethod
    def from_dict(d: dict) -> "PriceRule":
        return PriceRule(
            product=d["product"],
            currency=d["currency"],
            amount=d["amount"],
            valid_from=d["valid_from"],
            valid_to=d["valid_to"],
            revision=d["revision"],
            known_at=d["known_at"],
        )


@dataclass
class EvidenceEntry:
    rule: PriceRule
    revoked: bool          # 在本次查询的知悉时间下是否已撤销
    in_business_range: bool

    def to_dict(self) -> dict:
        d = self.rule.to_dict()
        d["revoked"] = self.revoked
        d["in_business_range"] = self.in_business_range
        return d


@dataclass
class QueryResult:
    product: str
    currency: str
    business_time: int
    knowledge_time: int
    amount: Optional[int]              # 命中规则的金额（分），无命中为 None
    winner: Optional[PriceRule]
    evidence: list                     # list[EvidenceEntry]，本次查询涉及的全部规则

    def to_dict(self, with_evidence: bool = True) -> dict:
        d = {
            "product": self.product,
            "currency": self.currency,
            "business_time": self.business_time,
            "knowledge_time": self.knowledge_time,
            "amount": self.amount,
            "winner": self.winner.to_dict() if self.winner else None,
        }
        if with_evidence:
            d["evidence"] = [e.to_dict() for e in self.evidence]
        return d


def _check_int(name: str, value) -> int:
    if isinstance(value, bool) or not isinstance(value, int):
        raise LedgerError(f"{name} 必须为整数，得到 {value!r}")
    return value


class Ledger:
    """双时态价格账本。缓存不持久化，仅在进程内有效。"""

    def __init__(self) -> None:
        self._rules: list[PriceRule] = []
        # (product, currency, revision) -> 撤销被知悉的时间
        self._revocations: dict[tuple, int] = {}
        self._clock = 0
        # (product, currency, business_time, knowledge_time) -> QueryResult
        self._cache: dict[tuple, QueryResult] = {}

    # ------------------------------------------------------------------ 写入

    def _tick(self, known_at: Optional[int]) -> int:
        if known_at is None:
            self._clock += 1
            return self._clock
        _check_int("known_at", known_at)
        self._clock = max(self._clock, known_at)
        return known_at

    def insert(self, product: str, currency: str, amount: int,
               valid_from: int, valid_to: int, revision: int,
               known_at: Optional[int] = None) -> int:
        """插入一条价格规则，返回其知悉时间。

        同 (product, currency, revision) 内容不同 -> ConflictError；
        内容相同 -> 幂等，返回原 known_at。
        """
        _check_int("amount", amount)
        if amount < 0:
            raise LedgerError("amount 必须为非负整数分")
        _check_int("valid_from", valid_from)
        _check_int("valid_to", valid_to)
        _check_int("revision", revision)
        if valid_from >= valid_to:
            raise LedgerError("价格区间必须满足 valid_from < valid_to（左闭右开）")

        key = (product, currency, revision)
        for r in self._rules:
            if (r.product, r.currency, r.revision) == key:
                if r.content_key() != (amount, valid_from, valid_to):
                    raise ConflictError(
                        f"修订号冲突: {key} 已存在内容 "
                        f"amount={r.amount} range=[{r.valid_from},{r.valid_to})，"
                        f"拒绝写入 amount={amount} range=[{valid_from},{valid_to})"
                    )
                return r.known_at  # 幂等

        known_at = self._tick(known_at)
        rule = PriceRule(product, currency, amount, valid_from, valid_to,
                         revision, known_at)
        self._rules.append(rule)
        self._invalidate(product, currency, valid_from, valid_to, known_at)
        return known_at

    def revoke(self, product: str, currency: str, revision: int,
               known_at: Optional[int] = None) -> int:
        """撤销某修订，返回撤销被知悉的时间。只影响该时刻之后（含）的查询。"""
        key = (product, currency, revision)
        rule = next((r for r in self._rules
                     if (r.product, r.currency, r.revision) == key), None)
        if rule is None:
            raise UnknownRevisionError(f"不存在的修订: {key}")
        if key in self._revocations:
            return self._revocations[key]  # 幂等
        known_at = self._tick(known_at)
        self._revocations[key] = known_at
        self._invalidate(product, currency, rule.valid_from, rule.valid_to,
                         known_at)
        return known_at

    # ------------------------------------------------------------------ 缓存

    def _invalidate(self, product: str, currency: str,
                    valid_from: int, valid_to: int, known_at: int) -> int:
        """只失效可能受影响的缓存项：同商品同币种、业务时间落在规则区间内、
        且查询知悉时间不早于本次变更的知悉时间。"""
        doomed = [
            k for k in self._cache
            if k[0] == product and k[1] == currency
            and valid_from <= k[2] < valid_to
            and k[3] >= known_at
        ]
        for k in doomed:
            del self._cache[k]
        return len(doomed)

    @property
    def cache_size(self) -> int:
        return len(self._cache)

    # ------------------------------------------------------------------ 查询

    def _revoked_known_at(self, rule: PriceRule) -> Optional[int]:
        return self._revocations.get((rule.product, rule.currency, rule.revision))

    def query(self, product: str, currency: str,
              business_time: int, knowledge_time: int) -> QueryResult:
        """按业务时间与知悉时间查询价格（金额：整数分）。"""
        _check_int("business_time", business_time)
        _check_int("knowledge_time", knowledge_time)
        key = (product, currency, business_time, knowledge_time)
        if key in self._cache:
            return self._cache[key]

        evidence: list[EvidenceEntry] = []
        active: list[PriceRule] = []
        for r in self._rules:
            if r.product != product or r.currency != currency:
                continue
            if r.known_at > knowledge_time:
                continue  # 查询时尚未知悉，不参与也不构成证据
            in_range = r.valid_from <= business_time < r.valid_to
            revoked_at = self._revoked_known_at(r)
            revoked = revoked_at is not None and revoked_at <= knowledge_time
            evidence.append(EvidenceEntry(r, revoked, in_range))
            if in_range and not revoked:
                active.append(r)

        winner = max(active, key=lambda r: (r.known_at, r.revision), default=None)
        result = QueryResult(
            product=product,
            currency=currency,
            business_time=business_time,
            knowledge_time=knowledge_time,
            amount=winner.amount if winner else None,
            winner=winner,
            evidence=evidence,
        )
        self._cache[key] = result
        return result

    # -------------------------------------------------------------- 持久化

    def dump(self) -> dict:
        return {
            "clock": self._clock,
            "rules": [r.to_dict() for r in self._rules],
            "revocations": [
                {"product": p, "currency": c, "revision": rev, "known_at": k}
                for (p, c, rev), k in sorted(self._revocations.items())
            ],
        }

    @classmethod
    def load(cls, data: dict) -> "Ledger":
        ledger = cls()
        ledger._clock = data.get("clock", 0)
        ledger._rules = [PriceRule.from_dict(d) for d in data.get("rules", [])]
        for d in data.get("revocations", []):
            ledger._revocations[(d["product"], d["currency"], d["revision"])] = \
                d["known_at"]
        return ledger


# ---------------------------------------------------------------------- CLI

def _load_db(path: str) -> Ledger:
    if os.path.exists(path):
        with open(path, "r", encoding="utf-8") as fh:
            return Ledger.load(json.load(fh))
    return Ledger()


def _save_db(path: str, ledger: Ledger) -> None:
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(ledger.dump(), fh, ensure_ascii=False, indent=2)


def _emit(obj: dict) -> None:
    json.dump(obj, sys.stdout, ensure_ascii=False)
    sys.stdout.write("\n")


def main(argv: Optional[list] = None) -> int:
    parser = argparse.ArgumentParser(
        prog="bitemporal-ledger",
        description="双时态计价回放账本 CLI（金额单位：整数分；区间左闭右开）",
    )
    parser.add_argument("--db", default="ledger.json", help="账本状态文件路径")
    sub = parser.add_subparsers(dest="command", required=True)

    p_ins = sub.add_parser("insert", help="插入价格规则")
    p_ins.add_argument("--product", required=True)
    p_ins.add_argument("--currency", required=True)
    p_ins.add_argument("--amount", required=True, type=int, help="整数分")
    p_ins.add_argument("--valid-from", required=True, type=int)
    p_ins.add_argument("--valid-to", required=True, type=int, help="不含")
    p_ins.add_argument("--revision", required=True, type=int)
    p_ins.add_argument("--known-at", type=int, default=None,
                       help="缺省使用内部逻辑时钟")

    p_rev = sub.add_parser("revoke", help="撤销某修订")
    p_rev.add_argument("--product", required=True)
    p_rev.add_argument("--currency", required=True)
    p_rev.add_argument("--revision", required=True, type=int)
    p_rev.add_argument("--known-at", type=int, default=None)

    p_q = sub.add_parser("query", help="按业务时间+知悉时间查询")
    p_q.add_argument("--product", required=True)
    p_q.add_argument("--currency", required=True)
    p_q.add_argument("--business-time", required=True, type=int)
    p_q.add_argument("--knowledge-time", required=True, type=int)
    p_q.add_argument("--no-evidence", action="store_true",
                     help="不输出证据列表")

    args = parser.parse_args(argv)
    ledger = _load_db(args.db)

    try:
        if args.command == "insert":
            known_at = ledger.insert(
                args.product, args.currency, args.amount,
                args.valid_from, args.valid_to, args.revision, args.known_at,
            )
            _save_db(args.db, ledger)
            _emit({"status": "ok", "known_at": known_at})
        elif args.command == "revoke":
            known_at = ledger.revoke(
                args.product, args.currency, args.revision, args.known_at,
            )
            _save_db(args.db, ledger)
            _emit({"status": "ok", "revoked_known_at": known_at})
        else:  # query
            result = ledger.query(
                args.product, args.currency,
                args.business_time, args.knowledge_time,
            )
            _emit(result.to_dict(with_evidence=not args.no_evidence))
    except LedgerError as exc:
        json.dump({"status": "error", "error": str(exc)}, sys.stderr,
                  ensure_ascii=False)
        sys.stderr.write("\n")
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
