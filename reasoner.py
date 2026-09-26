#!/usr/bin/env python3
"""跨源置信证据决策：可追溯规则推理引擎。

语义
----
- 规则: 有限前提事实集合 -> 单一结论事实；不允许否定（单调推理）。
- 事实可由多个相互独立的来源直接断言（基事实）。
- 推理采用从基事实出发的最小不动点前向链:
    * 只有能追溯到真实来源的事实才成立；
    * 循环规则在没有外部来源支撑时不会凭空成立。
- 每个事实附带全部“按集合包含极小”的来源集合（来源组合的反链）。
- 撤销来源 = 删除其基事实断言后重新求最小不动点；
  失去全部支持的派生事实随之消失。
- 来源集合可能指数增长；推理过程受预算保护，超预算时立即中断，
  结果中以 truncated=true 标记（此时结果为尽力而为的部分结果）。

仅使用 Python 3.11+ 标准库。
"""
from __future__ import annotations

import argparse
import itertools
import json
import sys
from typing import Optional


class BudgetExceeded(Exception):
    """计算工作量超过预算，触发中断。"""


def insert_minimal(sets: list[frozenset[str]], cand: frozenset[str]) -> bool:
    """把 cand 加入包含极小反链 sets。

    返回 True 表示反链发生变化（cand 是新的极小集合）。
    若已有集合 s 满足 s ⊆ cand，则 cand 冗余；
    同时移除所有严格包含 cand 的旧集合。
    """
    for existing in sets:
        if existing <= cand:
            return False
    kept = [existing for existing in sets if not cand < existing]
    kept.append(cand)
    sets[:] = kept
    return True


class Engine:
    """规则引擎：管理来源断言、规则，并计算可追溯支持。"""

    def __init__(self, budget: Optional[int] = None) -> None:
        self.base_facts: dict[str, set[str]] = {}
        self.rules: list[tuple[frozenset[str], str]] = []
        self.budget = budget
        self.steps = 0
        self.truncated = False

    def assert_source(self, source: str, facts) -> None:
        """来源 source 直接断言若干事实。"""
        for fact in facts:
            self.base_facts.setdefault(fact, set()).add(source)

    def revoke_source(self, source: str) -> None:
        """撤销来源 source：只删除其自身断言，派生事实在重算时自行处理。"""
        for fact, srcs in list(self.base_facts.items()):
            srcs.discard(source)
            if not srcs:
                del self.base_facts[fact]

    def add_rule(self, premises, conclusion: str) -> None:
        """添加规则 premises(有限事实集合) -> conclusion。"""
        self.rules.append((frozenset(premises), conclusion))

    def _tick(self, n: int = 1) -> None:
        self.steps += n
        if self.budget is not None and self.steps > self.budget:
            self.truncated = True
            raise BudgetExceeded()

    def compute_supports(self) -> dict[str, list[frozenset[str]]]:
        """求最小不动点，返回 事实 -> 极小来源集合列表。

        被截断时返回当前已得到的部分结果（仍为包含极小反链，但可能不完整），
        并把 self.truncated 置为 True。
        """
        self.steps = 0
        self.truncated = False
        supports: dict[str, list[frozenset[str]]] = {}

        try:
            # 基事实：每个断言来源构成一个单元素来源集合。
            for fact in sorted(self.base_facts):
                entry = supports.setdefault(fact, [])
                for source in sorted(self.base_facts[fact]):
                    self._tick()
                    insert_minimal(entry, frozenset({source}))

            # 单调最小不动点：无变化时终止。循环规则若无外部来源支撑，
            # 各轮都无法产生新支持，因此不会凭空成立。
            changed = True
            while changed:
                changed = False
                for premises, conclusion in self.rules:
                    self._tick()
                    premise_supports: list[list[frozenset[str]]] = []
                    ready = True
                    for premise in premises:
                        sp = supports.get(premise)
                        if not sp:
                            ready = False
                            break
                        premise_supports.append(sp)
                    if not ready:
                        continue

                    # 空前提规则：结论由空来源集合 ∅ 支持。
                    if not premises:
                        candidates = [frozenset()]
                    else:
                        candidates = (
                            frozenset().union(*combo)
                            for combo in itertools.product(*premise_supports)
                        )

                    entry = supports.setdefault(conclusion, [])
                    for cand in candidates:
                        self._tick()
                        if insert_minimal(entry, cand):
                            changed = True
        except BudgetExceeded:
            pass

        return supports


def format_result(supports: dict[str, list[frozenset[str]]],
                  truncated: bool, steps: int,
                  budget: Optional[int]) -> dict:
    """把内部结果转成 JSON 友好结构；来源集合先按数量再按字典序排序。"""
    facts = {}
    for fact in sorted(supports):
        sets = supports[fact]
        if not sets:
            continue
        ordered = sorted((sorted(src_set) for src_set in sets),
                         key=lambda lst: (len(lst), lst))
        facts[fact] = ordered
    return {
        "facts": facts,
        "truncated": truncated,
        "steps": steps,
        "budget": budget,
    }


def build_engine(data: dict, budget_override: Optional[int] = None) -> Engine:
    budget = budget_override if budget_override is not None else data.get("budget")
    engine = Engine(budget=budget)

    for source, facts in data.get("sources", {}).items():
        engine.assert_source(source, facts)

    for rule in data.get("rules", []):
        engine.add_rule(rule.get("premises", []), rule["conclusion"])

    for op in data.get("operations", []):
        kind = op["op"]
        if kind == "assert":
            engine.assert_source(op["source"], op["facts"])
        elif kind == "revoke":
            engine.revoke_source(op["source"])
        else:
            raise ValueError(f"未知操作: {kind!r}")

    return engine


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        description="可追溯规则推理引擎：计算每个事实的极小来源集合。")
    parser.add_argument("input", nargs="?",
                        help="JSON 输入文件路径；省略时从标准输入读取")
    parser.add_argument("--budget", type=int, default=None,
                        help="工作量预算（覆盖输入中的 budget 字段）")
    parser.add_argument("--compact", action="store_true",
                        help="输出紧凑 JSON")
    args = parser.parse_args(argv)

    if args.input:
        with open(args.input, "r", encoding="utf-8") as fh:
            data = json.load(fh)
    else:
        data = json.load(sys.stdin)

    engine = build_engine(data, args.budget)
    supports = engine.compute_supports()
    result = format_result(supports, engine.truncated,
                           engine.steps, engine.budget)
    json.dump(result, sys.stdout,
              indent=None if args.compact else 2, ensure_ascii=False)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
