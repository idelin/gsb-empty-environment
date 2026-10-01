"""数据模型、可行性检查与差距报告。

所有数值均为合成整数或有理数（int / Fraction / 有理数字符串），
为保证精确性拒绝 float。不调用任何外部求解器。
"""
from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from fractions import Fraction


class Status(Enum):
    """求解状态：可行 / 最优 / 预算未决 严格分开。"""
    OPTIMAL = "optimal"        # 已证明最优
    FEASIBLE = "feasible"      # 有可行解，但最优性未证明
    INFEASIBLE = "infeasible"  # 已证明无可行解
    UNDECIDED = "undecided"    # 搜索预算耗尽且暂无可行解，最优性未决


def to_rational(value, name="value") -> Fraction:
    if isinstance(value, bool):
        raise TypeError(f"{name}: 不接受 bool")
    if isinstance(value, Fraction):
        return value
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, str):
        try:
            return Fraction(value)
        except ValueError as exc:
            raise ValueError(f"{name}: 无法解析为有理数 {value!r}") from exc
    raise TypeError(
        f"{name}: 仅接受 int / Fraction / 有理数字符串，得到 {type(value).__name__}；"
        "为保证精确性拒绝 float"
    )


@dataclass(frozen=True)
class SetSpec:
    sid: str
    cost: Fraction
    elements: frozenset


class Problem:
    """带预算的最大覆盖问题。

    约束（硬约束）：
    - 预算：所选集合成本之和 <= budget
    - 依赖：选 i 必须同时选 deps[i] 的全部（可传递，允许成环——环内同选同弃）
    - 互斥：每个互斥组中至多选一个集合
    - 最低覆盖：元素 e 至少被覆盖 min_coverage[e] 次

    收益：元素被覆盖 >= 1 次即计入其权重，重复覆盖只算一次。
    version 在每次动态修改（如 delete_set）后递增，用于见证失效检测。
    """

    def __init__(self, weights, sets, budget, deps, mutex_groups, min_coverage):
        self.weights = weights                # list[Fraction]
        self.sets = sets                      # list[SetSpec]
        self.budget = budget                  # Fraction
        self.deps = deps                      # dict[int, frozenset[int]]
        self.mutex_groups = mutex_groups      # list[frozenset[int]]
        self.min_coverage = min_coverage      # dict[int, int]
        self.version = 0
        self._rebuild_index()

    # ---------- 构造 ----------

    @classmethod
    def create(cls, element_weights, set_costs, set_elements, budget, *,
               set_ids=None, dependencies=None, mutex_groups=None,
               min_coverage=None) -> "Problem":
        m = len(element_weights)
        weights = []
        for k, w in enumerate(element_weights):
            fw = to_rational(w, f"element_weights[{k}]")
            if fw < 0:
                raise ValueError(f"element_weights[{k}] 不能为负: {fw}")
            weights.append(fw)

        n = len(set_costs)
        if len(set_elements) != n:
            raise ValueError("set_costs 与 set_elements 长度不一致")
        if set_ids is None:
            set_ids = [f"s{i}" for i in range(n)]
        if len(set_ids) != n or len(set(set_ids)) != n:
            raise ValueError("set_ids 长度需与集合数一致且无重复")
        index = {sid: i for i, sid in enumerate(set_ids)}

        def idx_of(sid, ctx):
            try:
                return index[sid]
            except KeyError:
                raise ValueError(f"{ctx}: 未知集合 id {sid!r}") from None

        sets = []
        for i in range(n):
            cost = to_rational(set_costs[i], f"set_costs[{i}]")
            if cost < 0:
                raise ValueError(f"集合 {set_ids[i]} 成本不能为负: {cost}")
            elems = frozenset(set_elements[i])
            for e in elems:
                if not isinstance(e, int) or isinstance(e, bool) or not 0 <= e < m:
                    raise ValueError(f"集合 {set_ids[i]} 含非法元素下标 {e!r}")
            sets.append(SetSpec(set_ids[i], cost, elems))

        deps = {}
        for sid, reqs in (dependencies or {}).items():
            i = idx_of(sid, "dependencies")
            req_idx = frozenset(idx_of(r, "dependencies") for r in reqs)
            if i in req_idx:
                raise ValueError(f"集合 {sid} 不能依赖自身")
            if req_idx:
                deps[i] = req_idx

        mutex = []
        for group in (mutex_groups or []):
            g = frozenset(idx_of(s, "mutex_groups") for s in group)
            if len(g) >= 2:
                mutex.append(g)

        min_cov = {}
        for e, r in (min_coverage or {}).items():
            if not isinstance(e, int) or isinstance(e, bool) or not 0 <= e < m:
                raise ValueError(f"min_coverage 含非法元素下标 {e!r}")
            if not isinstance(r, int) or isinstance(r, bool) or r < 0:
                raise ValueError(f"min_coverage[{e}] 需为非负 int，得到 {r!r}")
            if r > 0:
                min_cov[e] = r

        fbudget = to_rational(budget, "budget")
        if fbudget < 0:
            raise ValueError(f"预算不能为负: {fbudget}")
        return cls(weights, sets, fbudget, deps, mutex, min_cov)

    # ---------- 基本查询 ----------

    def __len__(self):
        return len(self.sets)

    def _rebuild_index(self):
        self._index = {s.sid: i for i, s in enumerate(self.sets)}

    @property
    def ids(self):
        return tuple(s.sid for s in self.sets)

    def index_of(self, sid) -> int:
        return self._index[sid]

    def selection_by_ids(self, ids):
        return [self._index[sid] for sid in ids]

    def coverage_counts(self, selection):
        counts = [0] * len(self.weights)
        for i in selection:
            for e in self.sets[i].elements:
                counts[e] += 1
        return counts

    def profit(self, selection) -> Fraction:
        """覆盖 >= 1 次即计入权重，重复覆盖只算一次。"""
        counts = self.coverage_counts(selection)
        return sum((w for w, c in zip(self.weights, counts) if c >= 1), Fraction(0))

    def total_cost(self, selection) -> Fraction:
        return sum((self.sets[i].cost for i in selection), Fraction(0))

    def violations(self, selection):
        """返回违反约束的描述列表；空列表表示可行。"""
        sel = set(selection)
        msgs = []
        cost = self.total_cost(sel)
        if cost > self.budget:
            msgs.append(f"成本 {cost} 超过预算 {self.budget}")
        for i in sel:
            for j in self.deps.get(i, ()):
                if j not in sel:
                    msgs.append(f"集合 {self.sets[i].sid} 依赖未选中的 {self.sets[j].sid}")
        for g in self.mutex_groups:
            hit = sel & g
            if len(hit) > 1:
                msgs.append(f"互斥组冲突: {sorted(self.sets[i].sid for i in hit)}")
        counts = self.coverage_counts(sel)
        for e, req in self.min_coverage.items():
            if counts[e] < req:
                msgs.append(f"元素 {e} 覆盖 {counts[e]} 次，低于最低要求 {req} 次")
        return msgs

    def is_feasible(self, selection) -> bool:
        return not self.violations(selection)

    def dep_closure(self, indices):
        """indices 连同其全部传递依赖。"""
        closure = set(indices)
        stack = list(closure)
        while stack:
            i = stack.pop()
            for j in self.deps.get(i, ()):
                if j not in closure:
                    closure.add(j)
                    stack.append(j)
        return closure

    # ---------- 动态修改 ----------

    def delete_set(self, sid):
        """删除集合并重排下标；version 递增，旧见证/旧上界随之失效。"""
        i = self._index[sid]
        del self.sets[i]

        def remap(x):
            return x - 1 if x > i else x

        new_deps = {}
        for a, reqs in self.deps.items():
            if a == i:
                continue
            new_reqs = frozenset(remap(r) for r in reqs if r != i)
            if new_reqs:
                new_deps[remap(a)] = new_reqs
        self.deps = new_deps

        new_groups = []
        for g in self.mutex_groups:
            ng = frozenset(remap(x) for x in g if x != i)
            if len(ng) >= 2:
                new_groups.append(ng)
        self.mutex_groups = new_groups

        self._rebuild_index()
        self.version += 1


def optimality_gap(lower: Fraction, upper: Fraction):
    """返回 (绝对差距, 相对差距)。

    仅为本次运行的实测差距；本库不为启发式编造任何近似保证。
    """
    if upper < lower:
        raise ValueError("上界小于下界，界限不可靠")
    absolute = upper - lower
    relative = absolute / upper if upper > 0 else Fraction(0)
    return absolute, relative
