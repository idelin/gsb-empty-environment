#!/usr/bin/env python3.11
"""双时态计价回放账本。

同时按业务生效时间(valid time)与系统知悉时间(knowledge time)查询价格。
- 价格区间左闭右开 [valid_from, valid_to)
- 同商品同币种重叠规则: 按 (知悉时间, 修订号) 取最大者
- 撤销只影响知悉时间 >= 撤销知悉时间的查询
- 金额为整数分
- 同 (商品, 币种, 修订号) 内容不同则拒绝; 内容相同则幂等
- 查询可输出全部涉及规则作为证据
- 查询结果带缓存, 插入迟到修订/撤销时只失效受影响缓存项

命令行: 从 stdin 逐行读取 JSON 命令, 逐行向 stdout 输出 JSON 结果。
仅使用 Python 3.11 标准库。所有示例数据均为合成数据。
"""
from __future__ import annotations

import json
import sys
from dataclasses import asdict, dataclass


@dataclass(frozen=True)
class PriceRule:
    """一条价格规则(不可变)。时间为整数刻度, 金额为整数分。"""

    product: str
    currency: str
    valid_from: int  # 业务生效起点(含)
    valid_to: int    # 业务生效终点(不含)
    amount_cents: int
    revision: int
    known_from: int  # 系统知悉起点(含), 知悉时间无终点, 只能被撤销截断


@dataclass(frozen=True)
class Revocation:
    """一条撤销记录: 某修订自 known_from 起被系统知悉为已撤销。"""

    product: str
    currency: str
    revision: int
    known_from: int


def _require_int(name: str, value: object) -> int:
    if isinstance(value, bool) or not isinstance(value, int):
        raise ValueError(f"{name} 必须为整数, 得到 {value!r}")
    return value


def _require_str(name: str, value: object) -> str:
    if not isinstance(value, str) or not value:
        raise ValueError(f"{name} 必须为非空字符串, 得到 {value!r}")
    return value


class BitemporalPriceLedger:
    """双时态价格账本, 带按查询键缓存与精确失效。"""

    def __init__(self) -> None:
        self._rules: list[PriceRule] = []
        self._by_revision: dict[tuple[str, str, int], PriceRule] = {}
        self._revocations: list[Revocation] = []
        # 缓存键: (product, currency, business_time, known_time)
        # 缓存值: (胜出规则或 None, 证据列表)
        self._cache: dict[
            tuple[str, str, int, int],
            tuple[PriceRule | None, list[dict]],
        ] = {}

    # ---------------- 写入 ----------------

    def add_rule(self, rule: PriceRule) -> bool:
        """插入价格规则。返回 True 表示新插入, False 表示幂等重复。

        同 (product, currency, revision) 已存在且内容不同 -> ValueError。
        """
        self._validate_rule(rule)
        key = (rule.product, rule.currency, rule.revision)
        existing = self._by_revision.get(key)
        if existing is not None:
            if existing != rule:
                raise ValueError(
                    f"修订号冲突: {key} 已存在 {existing!r}, 拒绝不同内容 {rule!r}"
                )
            return False
        self._by_revision[key] = rule
        self._rules.append(rule)
        self._invalidate(
            rule.product, rule.currency,
            rule.valid_from, rule.valid_to, rule.known_from,
        )
        return True

    def revoke(self, product: str, currency: str, revision: int, known_from: int) -> Revocation:
        """登记撤销: 仅影响 known_time >= known_from 的查询。"""
        _require_str("product", product)
        _require_str("currency", currency)
        _require_int("revision", revision)
        _require_int("known_from", known_from)
        key = (product, currency, revision)
        rule = self._by_revision.get(key)
        if rule is None:
            raise KeyError(f"无法撤销不存在的修订: {key}")
        rev = Revocation(product, currency, revision, known_from)
        self._revocations.append(rev)
        self._invalidate(product, currency, rule.valid_from, rule.valid_to, known_from)
        return rev

    # ---------------- 查询 ----------------

    def query(
        self,
        product: str,
        currency: str,
        business_time: int,
        known_time: int,
    ) -> PriceRule | None:
        """返回胜出的价格规则; 无适用规则时返回 None。"""
        winner, _ = self._cached(product, currency, business_time, known_time)
        return winner

    def explain(
        self,
        product: str,
        currency: str,
        business_time: int,
        known_time: int,
    ) -> dict:
        """返回胜出规则及本次查询涉及的全部规则(证据)。"""
        winner, evidence = self._cached(product, currency, business_time, known_time)
        return {
            "product": product,
            "currency": currency,
            "business_time": business_time,
            "known_time": known_time,
            "amount_cents": winner.amount_cents if winner else None,
            "winner": asdict(winner) if winner else None,
            "evidence": evidence,
        }

    def cache_size(self) -> int:
        return len(self._cache)

    def cache_keys(self) -> list[tuple[str, str, int, int]]:
        return sorted(self._cache)

    # ---------------- 内部 ----------------

    @staticmethod
    def _validate_rule(rule: PriceRule) -> None:
        _require_str("product", rule.product)
        _require_str("currency", rule.currency)
        _require_int("valid_from", rule.valid_from)
        _require_int("valid_to", rule.valid_to)
        _require_int("amount_cents", rule.amount_cents)
        _require_int("revision", rule.revision)
        _require_int("known_from", rule.known_from)
        if rule.valid_from >= rule.valid_to:
            raise ValueError(
                f"价格区间必须满足 valid_from < valid_to, 得到 "
                f"[{rule.valid_from}, {rule.valid_to})"
            )
        if rule.amount_cents < 0:
            raise ValueError(f"金额(分)必须非负, 得到 {rule.amount_cents}")

    def _cached(
        self, product: str, currency: str, business_time: int, known_time: int
    ) -> tuple[PriceRule | None, list[dict]]:
        _require_str("product", product)
        _require_str("currency", currency)
        _require_int("business_time", business_time)
        _require_int("known_time", known_time)
        key = (product, currency, business_time, known_time)
        hit = self._cache.get(key)
        if hit is not None:
            return hit
        result = self._evaluate(product, currency, business_time, known_time)
        self._cache[key] = result
        return result

    def _evaluate(
        self, product: str, currency: str, business_time: int, known_time: int
    ) -> tuple[PriceRule | None, list[dict]]:
        candidates: list[tuple[PriceRule, bool]] = []
        for rule in self._rules:
            if (
                rule.product == product
                and rule.currency == currency
                and rule.valid_from <= business_time < rule.valid_to
                and rule.known_from <= known_time
            ):
                revoked = any(
                    rev.product == product
                    and rev.currency == currency
                    and rev.revision == rule.revision
                    and rev.known_from <= known_time
                    for rev in self._revocations
                )
                candidates.append((rule, revoked))
        active = [rule for rule, revoked in candidates if not revoked]
        winner = max(active, key=lambda r: (r.known_from, r.revision), default=None)
        evidence = [
            {
                "rule": asdict(rule),
                "revoked_at_known_time": revoked,
                "selected": rule is winner,
            }
            for rule, revoked in candidates
        ]
        return winner, evidence

    def _invalidate(
        self, product: str, currency: str, valid_from: int, valid_to: int, known_from: int
    ) -> None:
        """只失效可能被新记录影响的缓存项:

        同商品同币种, 且查询知悉时间 >= 记录知悉时间,
        且查询业务时间落在记录业务区间 [valid_from, valid_to) 内。
        """
        doomed = [
            key
            for key in self._cache
            if key[0] == product
            and key[1] == currency
            and key[3] >= known_from
            and valid_from <= key[2] < valid_to
        ]
        for key in doomed:
            del self._cache[key]


# ---------------- 命令行 ----------------
#
# 输入: stdin 每行一个 JSON 对象, 字段 op 指定操作:
#   {"op":"add","product":"P1","currency":"CNY","valid_from":0,"valid_to":20,
#    "amount_cents":1000,"revision":1,"known_from":2}
#   {"op":"revoke","product":"P1","currency":"CNY","revision":1,"known_from":12}
#   {"op":"query","product":"P1","currency":"CNY","business_time":7,"known_time":7}
#   {"op":"explain","product":"P1","currency":"CNY","business_time":7,"known_time":8}
#   {"op":"cache_stats"}
# 输出: stdout 每行一个 JSON 对象, 与输入一一对应:
#   add    -> {"ok":true,"inserted":true|false}
#   revoke -> {"ok":true}
#   query  -> {"ok":true,"amount_cents":<int|null>,"rule":<object|null>}
#   explain-> {"ok":true,"amount_cents":...,"winner":...,"evidence":[...]}
#   cache_stats -> {"ok":true,"size":<int>,"keys":[[p,c,bt,kt],...]}
#   任何错误 -> {"ok":false,"error":"<消息>"} (该行失败, 后续行继续处理)


def _handle(ledger: BitemporalPriceLedger, cmd: dict) -> dict:
    op = cmd.get("op")
    if op == "add":
        rule = PriceRule(
            product=cmd["product"],
            currency=cmd["currency"],
            valid_from=cmd["valid_from"],
            valid_to=cmd["valid_to"],
            amount_cents=cmd["amount_cents"],
            revision=cmd["revision"],
            known_from=cmd["known_from"],
        )
        return {"ok": True, "inserted": ledger.add_rule(rule)}
    if op == "revoke":
        ledger.revoke(cmd["product"], cmd["currency"], cmd["revision"], cmd["known_from"])
        return {"ok": True}
    if op == "query":
        rule = ledger.query(
            cmd["product"], cmd["currency"], cmd["business_time"], cmd["known_time"]
        )
        return {
            "ok": True,
            "amount_cents": rule.amount_cents if rule else None,
            "rule": asdict(rule) if rule else None,
        }
    if op == "explain":
        result = ledger.explain(
            cmd["product"], cmd["currency"], cmd["business_time"], cmd["known_time"]
        )
        result["ok"] = True
        return result
    if op == "cache_stats":
        return {
            "ok": True,
            "size": ledger.cache_size(),
            "keys": [list(key) for key in ledger.cache_keys()],
        }
    raise ValueError(f"未知操作: {op!r}")


def main(argv: list[str] | None = None) -> int:
    ledger = BitemporalPriceLedger()
    out = sys.stdout
    for lineno, line in enumerate(sys.stdin, start=1):
        line = line.strip()
        if not line:
            continue
        try:
            cmd = json.loads(line)
            if not isinstance(cmd, dict):
                raise ValueError("每行必须是一个 JSON 对象")
            result = _handle(ledger, cmd)
        except Exception as exc:  # noqa: BLE001 - CLI 需逐行容错
            result = {"ok": False, "error": f"第{lineno}行: {exc}"}
        out.write(json.dumps(result, ensure_ascii=False) + "\n")
    out.flush()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
