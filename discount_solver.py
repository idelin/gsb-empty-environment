"""定额优惠组合求解器.

语义规则
--------
1. 每个优惠指定: id、整数折扣金额 amount、覆盖商品集合 item_ids、
   最低原价门槛 min_subtotal、互斥组 mutex_group(可选)。
2. 门槛永远按商品原价(未打折)计算: 覆盖商品原价小计 >= min_subtotal 才可用。
   该判定与执行次序无关, 因为原价不随优惠改变。
3. 优惠生效时, 实际折扣 = min(amount, 覆盖商品剩余应付合计),
   即折扣不超过覆盖商品剩余应付, 实付不会变负。
4. 每个优惠最多使用一次; 同一互斥组内最多选择一个优惠。
5. 执行次序也是决策变量: 由于规则 3 的截断, 不同次序可能产生不同总折扣。
6. 只有实际折扣 > 0 的优惠才会被应用(应用零效果优惠无意义,
   且避免字典序比较出现退化序列)。
7. 目标: 先最小化最终实付, 再取优惠 ID 序列(按应用顺序)字典序最小者。
   ID 按字符串字典序比较。

分摊规则: 每个优惠的实际折扣按商品在输入中的顺序, 依次填满覆盖商品的
剩余应付(确定性规则, 仅影响分摊展示, 不影响总实付)。
"""

from __future__ import annotations

import argparse
import itertools
import json
import sys
from dataclasses import dataclass


# ---------------------------------------------------------------------------
# 数据模型
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class Item:
    id: str
    price: int
    quantity: int = 1

    @property
    def subtotal(self) -> int:
        return self.price * self.quantity


@dataclass(frozen=True)
class Offer:
    id: str
    amount: int
    item_ids: tuple[str, ...]
    min_subtotal: int = 0
    mutex_group: str | None = None


@dataclass(frozen=True)
class _PreparedOffer:
    """解析后的优惠: 覆盖商品下标按商品输入顺序排列."""
    offer: Offer
    covered: tuple[int, ...]       # 商品下标(按输入顺序)
    original_subtotal: int         # 覆盖商品原价小计


class InputError(ValueError):
    """输入数据非法."""


# ---------------------------------------------------------------------------
# 输入解析与校验
# ---------------------------------------------------------------------------

def _require_int(value, name: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int):
        raise InputError(f"{name} 必须是整数, 得到: {value!r}")
    return value


def parse_cart(data: dict) -> tuple[list[Item], list[Offer]]:
    if not isinstance(data, dict):
        raise InputError("输入必须是 JSON 对象")
    raw_items = data.get("items")
    raw_offers = data.get("offers", [])
    if not isinstance(raw_items, list) or not raw_items:
        raise InputError("items 必须是非空数组")
    if not isinstance(raw_offers, list):
        raise InputError("offers 必须是数组")

    items: list[Item] = []
    seen_item_ids: set[str] = set()
    for i, raw in enumerate(raw_items):
        if not isinstance(raw, dict):
            raise InputError(f"items[{i}] 必须是对象")
        item_id = raw.get("id")
        if not isinstance(item_id, str) or not item_id:
            raise InputError(f"items[{i}].id 必须是非空字符串")
        if item_id in seen_item_ids:
            raise InputError(f"商品 id 重复: {item_id!r}")
        seen_item_ids.add(item_id)
        price = _require_int(raw.get("price"), f"items[{i}].price")
        quantity = _require_int(raw.get("quantity", 1), f"items[{i}].quantity")
        if price < 0:
            raise InputError(f"items[{i}].price 不能为负: {price}")
        if quantity <= 0:
            raise InputError(f"items[{i}].quantity 必须为正: {quantity}")
        items.append(Item(id=item_id, price=price, quantity=quantity))

    offers: list[Offer] = []
    seen_offer_ids: set[str] = set()
    for i, raw in enumerate(raw_offers):
        if not isinstance(raw, dict):
            raise InputError(f"offers[{i}] 必须是对象")
        offer_id = raw.get("id")
        if not isinstance(offer_id, str) or not offer_id:
            raise InputError(f"offers[{i}].id 必须是非空字符串")
        if offer_id in seen_offer_ids:
            raise InputError(f"优惠 id 重复: {offer_id!r}")
        seen_offer_ids.add(offer_id)
        amount = _require_int(raw.get("amount"), f"offers[{i}].amount")
        if amount <= 0:
            raise InputError(f"offers[{i}].amount 必须为正整数: {amount}")
        raw_ids = raw.get("item_ids")
        if raw_ids is None:
            item_ids = tuple(it.id for it in items)  # 缺省覆盖全部商品
        else:
            if not isinstance(raw_ids, list) or not raw_ids:
                raise InputError(f"offers[{i}].item_ids 必须是非空数组")
            item_ids = tuple(raw_ids)
        for iid in item_ids:
            if iid not in seen_item_ids:
                raise InputError(f"offers[{i}] 引用了不存在的商品: {iid!r}")
        if len(set(item_ids)) != len(item_ids):
            raise InputError(f"offers[{i}].item_ids 存在重复商品")
        min_subtotal = _require_int(raw.get("min_subtotal", 0),
                                    f"offers[{i}].min_subtotal")
        if min_subtotal < 0:
            raise InputError(f"offers[{i}].min_subtotal 不能为负")
        mutex_group = raw.get("mutex_group")
        if mutex_group is not None and not isinstance(mutex_group, str):
            raise InputError(f"offers[{i}].mutex_group 必须是字符串或 null")
        offers.append(Offer(id=offer_id, amount=amount, item_ids=item_ids,
                            min_subtotal=min_subtotal, mutex_group=mutex_group))
    return items, offers


def _prepare(items: list[Item], offers: list[Offer]):
    """拆分: 满足门槛(按原价)的可用优惠 / 因门槛被排除的优惠."""
    index_of = {it.id: i for i, it in enumerate(items)}
    eligible: list[_PreparedOffer] = []
    rejected: list[tuple[Offer, str]] = []
    for offer in offers:
        covered = tuple(sorted((index_of[i] for i in offer.item_ids)))
        original_subtotal = sum(items[i].subtotal for i in covered)
        if original_subtotal < offer.min_subtotal:
            rejected.append((offer, "threshold_not_met"))
        else:
            eligible.append(_PreparedOffer(offer, covered, original_subtotal))
    return eligible, rejected


# ---------------------------------------------------------------------------
# 分摊
# ---------------------------------------------------------------------------

def _apply(remaining: tuple[int, ...], covered: tuple[int, ...],
           amount: int) -> tuple[tuple[int, ...], dict[int, int], int]:
    """按输入顺序贪心分摊, 返回 (新剩余, 分摊明细, 实际折扣)."""
    effective = min(amount, sum(remaining[i] for i in covered))
    rem = list(remaining)
    allocation: dict[int, int] = {}
    left = effective
    for idx in covered:
        if left == 0:
            break
        take = min(rem[idx], left)
        if take:
            rem[idx] -= take
            allocation[idx] = take
            left -= take
    return tuple(rem), allocation, effective


# ---------------------------------------------------------------------------
# 主求解器: 记忆化 DFS, 状态 = (各商品剩余应付, 已用优惠集合)
# ---------------------------------------------------------------------------

def solve(items: list[Item], offers: list[Offer]) -> dict:
    eligible, rejected = _prepare(items, offers)
    original_total = sum(it.subtotal for it in items)
    initial_remaining = tuple(it.subtotal for it in items)
    n = len(eligible)

    group_of = [p.offer.mutex_group for p in eligible]
    memo: dict[tuple, tuple[int, tuple[str, ...]]] = {}

    def dfs(remaining: tuple[int, ...], used: frozenset[int],
            used_groups: frozenset[str]) -> tuple[int, tuple[str, ...]]:
        """返回从该状态出发的 (最大额外折扣, 字典序最小优惠ID后缀序列)."""
        key = (remaining, used)
        if key in memo:
            return memo[key]
        best_discount = 0
        best_seq: tuple[str, ...] = ()
        for i, prep in enumerate(eligible):
            if i in used:
                continue
            group = group_of[i]
            if group is not None and group in used_groups:
                continue
            new_remaining, _, effective = _apply(remaining, prep.covered,
                                                 prep.offer.amount)
            if effective <= 0:
                continue  # 零效果优惠不应用
            sub_discount, sub_seq = dfs(
                new_remaining, used | {i},
                used_groups | ({group} if group is not None else frozenset()))
            cand_discount = effective + sub_discount
            cand_seq = (prep.offer.id,) + sub_seq
            if (cand_discount > best_discount
                    or (cand_discount == best_discount
                        and (not best_seq or cand_seq < best_seq))):
                best_discount = cand_discount
                best_seq = cand_seq
        memo[key] = (best_discount, best_seq)
        return memo[key]

    total_discount, sequence = dfs(initial_remaining, frozenset(), frozenset())
    final_payment = original_total - total_discount

    # 重放最优序列, 生成逐步分摊
    steps = []
    remaining = initial_remaining
    id_to_prep = {p.offer.id: p for p in eligible}
    for offer_id in sequence:
        prep = id_to_prep[offer_id]
        remaining, allocation, effective = _apply(remaining, prep.covered,
                                                  prep.offer.amount)
        steps.append({
            "offer_id": offer_id,
            "effective_discount": effective,
            "allocation": {items[i].id: v for i, v in sorted(allocation.items())},
            "remaining_after": {items[i].id: remaining[i]
                                for i in range(len(items))},
        })

    # 被排除优惠及原因
    applied_ids = set(sequence)
    group_of_id = {p.offer.id: p.offer.mutex_group for p in eligible}
    applied_groups = {group_of_id[o] for o in applied_ids
                      if group_of_id[o] is not None}
    excluded = []
    for offer, reason in rejected:
        excluded.append({
            "offer_id": offer.id,
            "reason": reason,
            "detail": (f"覆盖商品原价小计低于门槛 {offer.min_subtotal}"
                       f"(门槛按原价判定, 与执行次序无关)"),
        })
    for prep in eligible:
        offer = prep.offer
        if offer.id in applied_ids:
            continue
        if offer.mutex_group is not None and offer.mutex_group in applied_groups:
            winner = next(o for o in sequence
                          if group_of_id[o] == offer.mutex_group)
            excluded.append({
                "offer_id": offer.id,
                "reason": "mutex_conflict",
                "detail": f"互斥组 {offer.mutex_group!r} 中已选用优惠 {winner!r}",
            })
        else:
            covered_remaining = sum(remaining[i] for i in prep.covered)
            if covered_remaining == 0:
                reason, detail = ("no_remaining_payable",
                                  "最优序列执行后, 其覆盖商品剩余应付已为 0")
            else:
                # 理论上不可达: 否则追加该优惠可进一步降低实付
                reason, detail = ("not_selected",
                                  "加入后无法进一步降低实付或字典序不占优")
            excluded.append({"offer_id": offer.id, "reason": reason,
                             "detail": detail})
    excluded.sort(key=lambda e: e["offer_id"])

    return {
        "original_total": original_total,
        "total_discount": total_discount,
        "final_payment": final_payment,
        "sequence": list(sequence),
        "steps": steps,
        "excluded": excluded,
    }


# ---------------------------------------------------------------------------
# 小规模穷举对照器: 枚举所有子集的全部排列, 用于交叉验证
# ---------------------------------------------------------------------------

def brute_force(items: list[Item], offers: list[Offer],
                max_offers: int = 8) -> dict | None:
    """与 solve 语义完全一致的朴素穷举. 优惠数超过 max_offers 时返回 None."""
    eligible, _ = _prepare(items, offers)
    if len(eligible) > max_offers:
        return None
    original_total = sum(it.subtotal for it in items)
    initial_remaining = tuple(it.subtotal for it in items)

    best_payment = original_total
    best_seq: tuple[str, ...] = ()
    for r in range(1, len(eligible) + 1):
        for perm in itertools.permutations(range(len(eligible)), r):
            groups = [eligible[i].offer.mutex_group for i in perm]
            named = [g for g in groups if g is not None]
            if len(named) != len(set(named)):
                continue  # 互斥组冲突
            remaining = initial_remaining
            total = 0
            valid = True
            for i in perm:
                prep = eligible[i]
                remaining, _, effective = _apply(remaining, prep.covered,
                                                 prep.offer.amount)
                if effective <= 0:
                    valid = False  # 与主求解器一致: 不应用零效果优惠
                    break
                total += effective
            if not valid:
                continue
            payment = original_total - total
            seq = tuple(eligible[i].offer.id for i in perm)
            if payment < best_payment or (payment == best_payment
                                          and seq < best_seq):
                best_payment = payment
                best_seq = seq
    return {
        "original_total": original_total,
        "total_discount": original_total - best_payment,
        "final_payment": best_payment,
        "sequence": list(best_seq),
    }


def compare_with_brute_force(items: list[Item], offers: list[Offer],
                             max_offers: int = 8) -> dict:
    """主求解器与穷举对照, 返回对照报告."""
    result = solve(items, offers)
    ref = brute_force(items, offers, max_offers=max_offers)
    if ref is None:
        return {"match": None,
                "note": f"优惠数超过 {max_offers}, 穷举对照已跳过"}
    match = (result["final_payment"] == ref["final_payment"]
             and result["sequence"] == ref["sequence"])
    return {"match": match, "solver_sequence": result["sequence"],
            "brute_force_sequence": ref["sequence"],
            "solver_final_payment": result["final_payment"],
            "brute_force_final_payment": ref["final_payment"]}


# ---------------------------------------------------------------------------
# 命令行
# ---------------------------------------------------------------------------

def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="discount_solver",
        description="定额优惠组合求解: 最小实付 + 优惠ID序列字典序最小")
    parser.add_argument("input", nargs="?", default="-",
                        help="输入 JSON 文件路径, 缺省或 '-' 表示标准输入")
    parser.add_argument("--verify", action="store_true",
                        help="同时运行小规模穷举对照器并输出比对结果")
    parser.add_argument("--max-brute-force", type=int, default=8,
                        help="穷举对照器允许的最多可用优惠数(默认 8)")
    args = parser.parse_args(argv)

    try:
        text = (sys.stdin.read() if args.input == "-"
                else open(args.input, encoding="utf-8").read())
        data = json.loads(text)
        items, offers = parse_cart(data)
    except (OSError, json.JSONDecodeError, InputError) as exc:
        print(json.dumps({"error": str(exc)}, ensure_ascii=False),
              file=sys.stderr)
        return 2

    output = solve(items, offers)
    if args.verify:
        output["brute_force_check"] = compare_with_brute_force(
            items, offers, max_offers=args.max_brute_force)
    print(json.dumps(output, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
