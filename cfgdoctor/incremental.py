"""规则撤回后的增量修复(DRed 风格:过度删除 + 局部重导出)。

只支持删除(撤回),变化单调。朴素的计数法在循环依赖下会出错
(例如 A->B, B->A 互相"支撑"),因此采用:
  1. 过度删除: 沿"见证依赖"闭包找出所有可能受影响的符号并暂时移除;
  2. 局部重导出: 在受影响区域内重跑最小不动点/可达性传播,
     未受影响的符号状态保持不变(其见证依然良基)。
"""
from collections import defaultdict, deque
from dataclasses import dataclass


@dataclass(frozen=True)
class ChangeSet:
    retracted: int
    lost_generating: frozenset
    lost_reachable: frozenset
    language_emptied: bool


class IncrementalAnalyzer:
    def __init__(self, grammar):
        self.grammar = grammar
        nts = grammar.nonterminals
        self.alive = [True] * len(grammar.productions)
        self.appears = defaultdict(set)
        for i, p in enumerate(grammar.productions):
            for s in set(p.rhs):
                if s in nts:
                    self.appears[s].add(i)
        self.generating = set()
        self.gen_witness = {}
        self.gen_dependents = defaultdict(set)
        remaining = []
        for i, p in enumerate(grammar.productions):
            remaining.append(sum(1 for s in p.rhs if s in nts))
        queue = deque()
        for i, p in enumerate(grammar.productions):
            if remaining[i] == 0 and p.lhs not in self.generating:
                self.generating.add(p.lhs)
                self._set_gen_witness(p.lhs, i)
                queue.append(p.lhs)
        while queue:
            x = queue.popleft()
            for j in self.appears.get(x, ()):
                remaining[j] -= grammar.productions[j].rhs.count(x)
                if remaining[j] == 0:
                    a = grammar.productions[j].lhs
                    if a not in self.generating:
                        self.generating.add(a)
                        self._set_gen_witness(a, j)
                        queue.append(a)
        self.reachable = {grammar.start}
        self.reach_witness = {}
        self.witness_users = defaultdict(set)
        dq = deque([grammar.start])
        while dq:
            a = dq.popleft()
            for j in grammar.production_indices_of(a):
                for t in set(grammar.productions[j].rhs):
                    if t in nts and t not in self.reachable:
                        self.reachable.add(t)
                        self._set_reach_witness(t, j)
                        dq.append(t)

    def _set_gen_witness(self, a, j):
        old = self.gen_witness.get(a)
        if old is not None:
            for s in self.grammar.productions[old].rhs:
                if s in self.grammar.nonterminals:
                    self.gen_dependents[s].discard(a)
        self.gen_witness[a] = j
        for s in self.grammar.productions[j].rhs:
            if s in self.grammar.nonterminals:
                self.gen_dependents[s].add(a)

    def _set_reach_witness(self, a, j):
        old = self.reach_witness.get(a)
        if old is not None:
            self.witness_users[old].discard(a)
        self.reach_witness[a] = j
        self.witness_users[j].add(a)

    def _retract_generating(self, index):
        grammar = self.grammar
        nts = grammar.nonterminals
        p = grammar.productions[index]
        suspects = set()
        if self.gen_witness.get(p.lhs) == index:
            stack = [p.lhs]
            while stack:
                x = stack.pop()
                if x in suspects:
                    continue
                suspects.add(x)
                stack.extend(self.gen_dependents.get(x, ()))
        if not suspects:
            return frozenset()
        for x in suspects:
            self.generating.discard(x)
            w = self.gen_witness.pop(x, None)
            if w is not None:
                for s in grammar.productions[w].rhs:
                    if s in nts:
                        self.gen_dependents[s].discard(x)
        cnt = {}
        queue = deque()
        for a in suspects:
            for j in grammar.production_indices_of(a):
                if not self.alive[j]:
                    continue
                c = sum(1 for s in grammar.productions[j].rhs
                        if s in nts and s not in self.generating)
                cnt[j] = c
                if c == 0 and a not in self.generating:
                    self.generating.add(a)
                    self._set_gen_witness(a, j)
                    queue.append(a)
        while queue:
            x = queue.popleft()
            for j in self.appears.get(x, ()):
                if not self.alive[j] or j not in cnt:
                    continue
                a = grammar.productions[j].lhs
                if a in self.generating:
                    continue
                cnt[j] -= grammar.productions[j].rhs.count(x)
                if cnt[j] == 0:
                    self.generating.add(a)
                    self._set_gen_witness(a, j)
                    queue.append(a)
        return frozenset(suspects - self.generating)

    def _retract_reachable(self, index):
        grammar = self.grammar
        nts = grammar.nonterminals
        p = grammar.productions[index]
        suspects = set()
        if p.lhs in self.reachable:
            stack = [x for x in self.witness_users.get(index, ())]
            while stack:
                x = stack.pop()
                if x in suspects or x not in self.reachable:
                    continue
                suspects.add(x)
                for j in grammar.production_indices_of(x):
                    stack.extend(self.witness_users.get(j, ()))
        if not suspects:
            return frozenset()
        for x in suspects:
            self.reachable.discard(x)
            w = self.reach_witness.pop(x, None)
            if w is not None:
                self.witness_users[w].discard(x)
        referrers = set()
        for x in suspects:
            for j in self.appears.get(x, ()):
                if self.alive[j]:
                    referrers.add(grammar.productions[j].lhs)
        queue = deque(r for r in referrers if r in self.reachable)
        while queue:
            a = queue.popleft()
            for j in grammar.production_indices_of(a):
                if not self.alive[j]:
                    continue
                for t in set(grammar.productions[j].rhs):
                    if (t in nts and t in suspects
                            and t not in self.reachable):
                        self.reachable.add(t)
                        self._set_reach_witness(t, j)
                        queue.append(t)
        return frozenset(suspects - self.reachable)

    def retract(self, index):
        """撤回一条产生式,增量修复可生成/可达状态,返回变化集。"""
        if not 0 <= index < len(self.grammar.productions):
            raise ValueError(f"产生式编号 {index} 越界")
        if not self.alive[index]:
            raise ValueError(f"产生式 #{index} 已被撤回")
        self.alive[index] = False
        lost_gen = self._retract_generating(index)
        lost_reach = self._retract_reachable(index)
        return ChangeSet(
            retracted=index,
            lost_generating=lost_gen,
            lost_reachable=lost_reach,
            language_emptied=self.grammar.start not in self.generating,
        )

    def is_useful_production(self, index):
        if not self.alive[index]:
            return False
        p = self.grammar.productions[index]
        if p.lhs not in self.generating or p.lhs not in self.reachable:
            return False
        return all(s not in self.grammar.nonterminals or s in self.generating
                   for s in p.rhs)
