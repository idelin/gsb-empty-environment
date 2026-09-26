#!/usr/bin/env python3
"""定额优惠组合求解器。

语义规则（详见 README.md）：
- 门槛按覆盖商品的"原价"合计判断，与已应用优惠无关（静态判断）。
- 优惠按选定次序逐个应用；每个优惠的实际减免 = min(折扣额, 覆盖商品剩余应付合计)。
- 分摊规则（确定性）：按商品在输入中的顺序，依次从覆盖商品的剩余应付中扣除。
- 每个优惠最多使用一次；同一互斥组最多选择一个优惠。
- 应用时减免为 0 的优惠不纳入序列，记入被排除列表。
- 目标：最小化实付；并列时取优惠 ID 序列字典序最小者。
"""

from __future__ import annotations

import argparse
import itertools
import json
import sys
from dataclasses import dataclass


# ---------------------------------------------------------------- 数据模型

@dataclass(frozen=True)
class Item:
    id: str
    price: int  # 原价（非负整数）


@dataclass(frozen=True)
class Offer:
    id: str
    discount: int            # 整数折扣额（非负）
    items: tuple[str, ...]   # 覆盖商品 ID（按输入商品顺序排列，去重）
    min_original: int = 0    # 最低原价门槛（按覆盖商品原价合计判断）
    group: str | None = None # 互斥组；None 表示不属于任何互斥组


def parse_instance(data: dict) -> tuple[list[Item], list[Offer]]:
    """解析并校验输入字典，返回 (items, offers)。非法输入抛 ValueError。"""
    if not isinstance(data, dict):
        raise ValueError("输入必须是 JSON 对象")
    raw_items = data.get("items")
    raw_offers = data.get("offers", [])
    if not isinstance(raw_items, list) or not raw_items:
        raise ValueError("'items' 必须是非空数组")
    if not isinstance(raw_offers, list):
        raise ValueError("'offers' 必须是数组")

    items: list[Item] = []
    seen_item_ids: set[str] = set()
    for raw in raw_items:
        try:
            item_id = raw["id"]
            price = raw["price"]
        except (TypeError, KeyError) as exc:
            raise ValueError(f"商品缺少 id/price 字段: {raw!r}") from exc
        if not isinstance(item_id, str) or not item_id:
            raise ValueError(f"商品 id 必须是非空字符串: {raw!r}")
        if item_id in seen_item_ids:
            raise ValueError(f"商品 id 重复: {item_id!r}")
        if not isinstance(price, int) or isinstance(price, bool) or price < 0:
            raise ValueError(f"商品价格必须是非负整数: {raw!r}")
        seen_item_ids.add(item_id)
        items.append(Item(id=item_id, price=price))

    offers: list[Offer] = []
    seen_offer_ids: set[str] = set()
    for raw in raw_offers:
        try:
            offer_id = raw["id"]
            discount = raw["discount"]
        except (TypeError, KeyError) as exc:
            raise ValueError(f"优惠缺少 id/discount 字段: {raw!r}") from exc
        if not isinstance(offer_id, str) or not offer_id:
            raise ValueError(f"优惠 id 必须是非空字符串: {raw!r}")
        if offer_id in seen_offer_ids:
            raise ValueError(f"优惠 id 重复: {offer_id!r}")
        if not isinstance(discount, int) or isinstance(discount, bool) or discount < 0:
            raise ValueError(f"优惠折扣必须是非负整数: {raw!r}")
        covered = raw.get("items")
        if covered is None:
            covered = [it.id for it in items]
        if not isinstance(covered, list) or not all(isinstance(c, str) for c in covered):
            raise ValueError(f"优惠 items 必须是商品 id 字符串数组: {raw!r}")
        unknown = [c for c in covered if c not in seen_item_ids]
        if unknown:
            raise ValueError(f"优惠 {offer_id!r} 覆盖了未知商品: {unknown!r}")
        min_original = raw.get("min_original", 0)
        if not isinstance(min_original, int) or isinstance(min_original, bool) or min_original < 0:
            raise ValueError(f"优惠 min_original 必须是非负整数: {raw!r}")
        group = raw.get("group")
        if group is not None and not isinstance(group, str):
            raise ValueError(f"优惠 group 必须是字符串或 null: {raw!r}")
        # 覆盖集合按输入商品顺序规范化并去重
        covered_set = set(covered)
        ordered = tuple(it.id for it in items if it.id in covered_set)
        seen_offer_ids.add(offer_id)
        offers.append(Offer(id=offer_id, discount=discount, items=ordered,
                            min_original=min_original, group=group))
    return items, offers


# ---------------------------------------------------------------- 内部工具

class _BudgetExceeded(Exception):
    """精确搜索状态数超出预算时抛出，用于回退到启发式。"""


def _prepare(items: list[Item], offers: list[Offer]):
    """预计算：覆盖下标、门槛静态判定、互斥组位掩码。"""
    index_of = {it.id: pos for pos, it in enumerate(items)}
    covered_idx = [tuple(index_of[c] for c in of.items) for of in offers]
    threshold_ok = [
        sum(items[i].price for i in idxs) >= of.min_original
        for of, idxs in zip(offers, covered_idx)
    ]
    group_block = [0] * len(offers)
    group_members: dict[str, list[int]] = {}
    for pos, of in enumerate(offers):
        if of.group is not None:
            group_members.setdefault(of.group, []).append(pos)
    for members in group_members.values():
        mask = 0
        for pos in members:
            mask |= 1 << pos
        for pos in members:
            group_block[pos] = mask
    return covered_idx, threshold_ok, group_block


def _apply(remaining: tuple[int, ...], covered: tuple[int, ...], discount: int):
    """按确定性规则分摊一个优惠，返回 (实际减免, 新剩余元组, 分摊{下标: 额})。"""
    rem = list(remaining)
    left = discount
    allocation: dict[int, int] = {}
    for i in covered:
        if left <= 0:
            break
        take = rem[i] if rem[i] < left else left
        if take > 0:
            rem[i] -= take
            allocation[i] = take
            left -= take
    return discount - left, tuple(rem), allocation


# ---------------------------------------------------------------- 求解器

def _exact_search(items, offers, covered_idx, threshold_ok, group_block, max_states):
    """记忆化精确搜索。状态 = (已用优惠位掩码, 各商品剩余应付元组)。

    返回 (从该状态还能获得的最大减免, 对应的字典序最小优惠 ID 后缀序列)。
    """
    memo: dict[tuple[int, tuple[int, ...]], tuple[int, tuple[str, ...]]] = {}

    def search(used_mask: int, remaining: tuple[int, ...]):
        key = (used_mask, remaining)
        hit = memo.get(key)
        if hit is not None:
            return hit
        if len(memo) >= max_states:
            raise _BudgetExceeded
        best_deduction = 0
        best_seq: tuple[str, ...] = ()
        for pos, offer in enumerate(offers):
            bit = 1 << pos
            if used_mask & bit:
                continue
            if not threshold_ok[pos]:
                continue
            if used_mask & group_block[pos]:
                continue
            deducted, new_remaining, _ = _apply(remaining, covered_idx[pos], offer.discount)
            if deducted <= 0:
                continue  # 减免为 0 的优惠不纳入序列
            sub_deduction, sub_seq = search(used_mask | bit, new_remaining)
            cand_deduction = deducted + sub_deduction
            cand_seq = (offer.id,) + sub_seq
            if (cand_deduction > best_deduction
                    or (cand_deduction == best_deduction
                        and (not best_seq or cand_seq < best_seq))):
                best_deduction = cand_deduction
                best_seq = cand_seq
        memo[key] = (best_deduction, best_seq)
        return memo[key]

    initial = tuple(it.price for it in items)
    return search(0, initial)


def _greedy_search(items, offers, covered_idx, threshold_ok, group_block):
    """兜底启发式：每步选择即时减免最大的优惠（并列取 ID 最小）。不保证最优。"""
    remaining = tuple(it.price for it in items)
    used_mask = 0
    sequence: list[str] = []
    total_deduction = 0
    while True:
        best = None  # (deducted, offer_id, pos, new_remaining)
        for pos, offer in enumerate(offers):
            bit = 1 << pos
            if used_mask & bit or not threshold_ok[pos] or used_mask & group_block[pos]:
                continue
            deducted, new_remaining, _ = _apply(remaining, covered_idx[pos], offer.discount)
            if deducted <= 0:
                continue
            if (best is None or deducted > best[0]
                    or (deducted == best[0] and offer.id < best[1])):
                best = (deducted, offer.id, pos, new_remaining)
        if best is None:
            break
        deducted, _, pos, remaining = best
        used_mask |= 1 << pos
        total_deduction += deducted
        sequence.append(offers[pos].id)
    return total_deduction, tuple(sequence)


def _replay(items, offers, covered_idx, sequence):
    """按序列重放，生成逐步分摊明细。"""
    pos_of = {of.id: pos for pos, of in enumerate(offers)}
    remaining = tuple(it.price for it in items)
    steps = []
    for offer_id in sequence:
        pos = pos_of[offer_id]
        offer = offers[pos]
        deducted, remaining, allocation = _apply(remaining, covered_idx[pos], offer.discount)
        steps.append({
            "offer": offer_id,
            "discount": offer.discount,
            "deducted": deducted,
            "allocation": {items[i].id: amount for i, amount in allocation.items()},
            "remaining_after": {items[i].id: remaining[i] for i in range(len(items))},
        })
    return steps, remaining


def _exclusion_reasons(items, offers, covered_idx, threshold_ok, sequence, final_remaining):
    """为每个未入选优惠给出被排除原因。"""
    chosen = set(sequence)
    chosen_by_group: dict[str, str] = {}
    for of in offers:
        if of.id in chosen and of.group is not None:
            chosen_by_group[of.group] = of.id
    excluded = []
    for pos, offer in enumerate(offers):
        if offer.id in chosen:
            continue
        original_sum = sum(items[i].price for i in covered_idx[pos])
        if not threshold_ok[pos]:
            reason = (f"门槛未满足：覆盖商品原价合计 {original_sum} "
                      f"低于最低原价门槛 {offer.min_original}（门槛按原价判断）")
        elif offer.group is not None and offer.group in chosen_by_group:
            reason = (f"互斥：与已选优惠 '{chosen_by_group[offer.group]}' "
                      f"同属互斥组 '{offer.group}'")
        elif all(final_remaining[i] == 0 for i in covered_idx[pos]):
            reason = "无减免效果：覆盖商品剩余应付已为 0"
        else:
            reason = "未被选中：纳入该优惠无法使实付更低或序列字典序更优"
        excluded.append({"offer": offer.id, "reason": reason})
    return excluded


def solve(items: list[Item], offers: list[Offer], max_states: int = 200_000) -> dict:
    """主求解入口。返回结果字典；proven_optimal 表示结果是否经精确搜索证实最优。"""
    covered_idx, threshold_ok, group_block = _prepare(items, offers)
    proven = True
    try:
        total_deduction, sequence = _exact_search(
            items, offers, covered_idx, threshold_ok, group_block, max_states)
    except _BudgetExceeded:
        proven = False
        total_deduction, sequence = _greedy_search(
            items, offers, covered_idx, threshold_ok, group_block)
    steps, final_remaining = _replay(items, offers, covered_idx, sequence)
    total_original = sum(it.price for it in items)
    return {
        "total_original": total_original,
        "total_discount": total_original - sum(final_remaining),
        "total_payable": sum(final_remaining),
        "proven_optimal": proven,
        "sequence": list(sequence),
        "steps": steps,
        "excluded": _exclusion_reasons(items, offers, covered_idx, threshold_ok,
                                       set(sequence), final_remaining),
    }


# ---------------------------------------------------------------- 穷举对照器

def brute_force(items: list[Item], offers: list[Offer]) -> tuple[int, tuple[str, ...]]:
    """独立实现的朴素穷举：枚举所有合法子集的全部排列。

    仅适用于小规模（优惠数约 <= 8）。返回 (最大总减免, 字典序最小序列)。
    与主求解器语义一致：模拟时跳过减免为 0 的优惠（不纳入序列）。
    """
    covered_idx, threshold_ok, group_block = _prepare(items, offers)
    initial = tuple(it.price for it in items)
    n = len(offers)
    best_deduction = 0
    best_seq: tuple[str, ...] = ()
    for size in range(n + 1):
        for combo in itertools.combinations(range(n), size):
            combo_mask = 0
            for pos in combo:
                combo_mask |= 1 << pos
            if any(not threshold_ok[pos] for pos in combo):
                continue
            if any(combo_mask & (group_block[pos] & ~(1 << pos)) for pos in combo):
                continue  # 同组多于一个
            for perm in itertools.permutations(combo):
                remaining = initial
                deduction = 0
                seq: list[str] = []
                for pos in perm:
                    offer = offers[pos]
                    deducted, remaining, _ = _apply(remaining, covered_idx[pos], offer.discount)
                    if deducted <= 0:
                        continue  # 与主求解器一致：零减免优惠不纳入序列
                    deduction += deducted
                    seq.append(offer.id)
                cand_seq = tuple(seq)
                if (deduction > best_deduction
                        or (deduction == best_deduction and deduction > 0
                            and (not best_seq or cand_seq < best_seq))):
                    best_deduction = deduction
                    best_seq = cand_seq
    return best_deduction, best_seq


def verify_against_brute_force(items: list[Item], offers: list[Offer],
                               result: dict) -> dict:
    """用穷举对照器核验 solve 的结果（仅限小规模）。"""
    bf_deduction, bf_seq = brute_force(items, offers)
    total_original = sum(it.price for it in items)
    bf_payable = total_original - bf_deduction
    return {
        "checked": True,
        "brute_force_payable": bf_payable,
        "brute_force_sequence": list(bf_seq),
        "payable_match": result["total_payable"] == bf_payable,
        "sequence_match": list(result["sequence"]) == list(bf_seq),
        "match": (result["total_payable"] == bf_payable
                  and list(result["sequence"]) == list(bf_seq)),
    }


# ---------------------------------------------------------------- 命令行

def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="quota_offer_solver",
        description="定额优惠组合求解：最小实付，其次优惠 ID 序列字典序最小。")
    parser.add_argument("input", nargs="?", default="-",
                        help="输入 JSON 文件路径，缺省或 '-' 表示标准输入")
    parser.add_argument("--verify", action="store_true",
                        help="运行小规模穷举对照器核验结果（优惠数较多时很慢）")
    parser.add_argument("--max-states", type=int, default=200_000,
                        help="精确搜索的状态数预算，超出则回退启发式（默认 200000）")
    parser.add_argument("--pretty", action="store_true", help="缩进美化输出 JSON")
    args = parser.parse_args(argv)

    try:
        if args.input == "-":
            data = json.load(sys.stdin)
        else:
            with open(args.input, "r", encoding="utf-8") as fh:
                data = json.load(fh)
    except (OSError, json.JSONDecodeError) as exc:
        print(f"读取输入失败: {exc}", file=sys.stderr)
        return 2
    try:
        items, offers = parse_instance(data)
    except ValueError as exc:
        print(f"输入非法: {exc}", file=sys.stderr)
        return 2

    result = solve(items, offers, max_states=args.max_states)
    if args.verify:
        result["verification"] = verify_against_brute_force(items, offers, result)
    json.dump(result, sys.stdout, ensure_ascii=False,
              indent=2 if args.pretty else None)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
