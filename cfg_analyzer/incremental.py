"""规则撤回后的增量修复。

可生成性集合采用"删除-重导出"（DRed 风格）增量维护：
1. 过度删除：从被撤回规则出发，沿 support 依赖级联删除失去证明的符号；
2. 重导出：在剩余规则上重新运行不动点，恢复存在替代证明的符号。
其余诊断（可达性、环、清理映射等）在当前文法快照上重算（O(文法)），
并返回与撤回前的差异。测试中将增量结果与全量重算逐一比对。
"""
from __future__ import annotations

from collections import defaultdict, deque
from dataclasses import dataclass

from .core import generating_certificate
from .grammar import Grammar
from .report import Diagnosis, analyze


@dataclass
class RetractionDiff:
    retracted_index: int
    retracted_rule: str
    lost_generating: list[str]        # 撤回后不再可生成的符号
    recovered: list[str]              # 级联删除后被替代证明恢复的符号
    newly_unreachable: list[str]
    newly_useless_rules: list[int]    # 原文法下标
    language_became_empty: bool
    still_empty: bool

    def summary(self) -> str:
        lines = [f"撤回 #{self.retracted_index}: {self.retracted_rule}"]
        lines.append(f"  新不可生成: {self.lost_generating or '无'}")
        if self.recovered:
            lines.append(f"  级联删除后经替代证明恢复: {self.recovered}")
        lines.append(f"  新不可达: {self.newly_unreachable or '无'}")
        lines.append(f"  新无用规则: {self.newly_useless_rules or '无'}")
        if self.language_became_empty:
            lines.append("  ⚠ 语言变为空（开始符失去可生成性）")
        return "\n".join(lines)


class IncrementalAnalyzer:
    def __init__(self, grammar: Grammar, enum_depth: int = 8):
        self.grammar = grammar
        self._alive = [True] * len(grammar.productions)
        self._enum_depth = enum_depth
        # 增量维护的可生成性状态
        self._gen: set[str] = set()
        self._support: dict[str, int] = {}
        self._init_generating()
        self.diagnosis: Diagnosis = self._full_diagnosis()

    # -------------------------------------------------- 内部
    def _init_generating(self):
        cert = generating_certificate(self.grammar)
        self._gen = set(cert.result)
        self._support = dict(cert.support)

    def _current_grammar(self) -> Grammar:
        prods = tuple(p for p, ok in zip(self.grammar.productions, self._alive) if ok)
        return Grammar(self.grammar.terminals, self.grammar.nonterminals,
                       self.grammar.start, prods)

    def _full_diagnosis(self) -> Diagnosis:
        return analyze(self._current_grammar(), enum_depth=self._enum_depth)

    def _rhs_users(self) -> dict[str, list[int]]:
        users: dict[str, list[int]] = defaultdict(list)
        for idx, prod in enumerate(self.grammar.productions):
            if not self._alive[idx]:
                continue
            for s in prod.rhs:
                if s in self.grammar.nonterminals:
                    users[s].append(idx)
        return users

    # -------------------------------------------------- 对外
    def retract(self, index: int) -> RetractionDiff:
        """撤回一条规则（原文法下标），增量修复可生成性并返回差异。"""
        if not (0 <= index < len(self.grammar.productions)):
            raise IndexError(f"产生式下标 {index} 越界")
        if not self._alive[index]:
            raise ValueError(f"产生式 #{index} 已被撤回")
        before = self.diagnosis
        before_useless = self._useless_orig_indices(before)
        prod = self.grammar.productions[index]
        self._alive[index] = False

        # ---- 1. 过度删除（沿 support 级联）----
        users = self._rhs_users()
        queue: deque[str] = deque()
        if self._support.get(prod.lhs) == index:
            queue.append(prod.lhs)
        over_deleted: set[str] = set()
        while queue:
            sym = queue.popleft()
            if sym not in self._gen:
                continue
            self._gen.discard(sym)
            self._support.pop(sym, None)
            over_deleted.add(sym)
            for pi in users.get(sym, ()):
                q = self.grammar.productions[pi]
                if self._support.get(q.lhs) == pi:
                    queue.append(q.lhs)

        # ---- 2. 重导出（剩余规则上的不动点）----
        while True:
            changed = False
            for pi, q in enumerate(self.grammar.productions):
                if not self._alive[pi] or q.lhs in self._gen:
                    continue
                if all(s in self.grammar.terminals or s in self._gen for s in q.rhs):
                    self._gen.add(q.lhs)
                    self._support[q.lhs] = pi
                    changed = True
            if not changed:
                break
        recovered = sorted(over_deleted & self._gen)

        # ---- 3. 其余诊断在快照上重算，汇总差异 ----
        self.diagnosis = self._full_diagnosis()
        after = self.diagnosis
        # 一致性自检：增量维护的集合必须等于全量重算
        assert frozenset(self._gen) == after.generating, "增量维护与全量重算不一致"

        after_useless = self._useless_orig_indices(after)
        newly_useless = sorted(after_useless - before_useless - {index})
        return RetractionDiff(
            retracted_index=index,
            retracted_rule=str(prod),
            lost_generating=sorted(before.generating - after.generating),
            recovered=recovered,
            newly_unreachable=sorted(
                (before.reachable - after.reachable) & after.grammar.nonterminals
            ),
            newly_useless_rules=newly_useless,
            language_became_empty=(not before.language_empty) and after.language_empty,
            still_empty=after.language_empty,
        )

    def _useless_orig_indices(self, diag: Diagnosis) -> set[int]:
        """把诊断（基于当前快照）中的无用规则快照下标映射回原文法下标。"""
        alive_orig = [i for i, ok in enumerate(self._alive) if ok]
        return {alive_orig[f.index] for f in diag.cleanup.fates if not f.kept}
