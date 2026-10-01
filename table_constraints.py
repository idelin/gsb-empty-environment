"""压缩元组表约束：共享前缀存储 + 增量支持维护。

特性
----
- 元组表用 trie（前缀树）存储，共享前缀只存一次，实现压缩。
- 支持正表（positive table，允许的元组）与禁表（negative table，禁止的元组）。
- 重复元组只存储、只计数一次。
- 变量域有限；当某变量域删值后，所有依赖该 (变量, 值) 的元组支持同步失效，
  支持计数就地更新；trail 栈支持回溯撤销。
- 不调用 SAT/SMT 库：内置小型回溯求解器；声称有解时，用独立检查器
  对照*原始*元组列表（而非压缩结构）验证解。
- 正表为空 -> 不可行；禁表为空 -> 无限制。

仅使用 Python 3.11 标准库。
"""

from __future__ import annotations

POSITIVE = "positive"
NEGATIVE = "negative"


class Infeasible(Exception):
    """当前部分状态无法扩展为完整解时抛出。"""


# ---------------------------------------------------------------------------
# 共享前缀压缩表（trie）
# ---------------------------------------------------------------------------

class _TrieNode:
    __slots__ = ("children", "terminal")

    def __init__(self):
        self.children: dict = {}
        self.terminal = False


class SharedPrefixTable:
    """以 trie 存储元组表，公共前缀只存一份；重复元组只计一次。"""

    def __init__(self, arity: int):
        self.arity = arity
        self.root = _TrieNode()
        self.tuples: list[tuple] = []   # 去重后的元组，下标即 tuple id
        self._ids: dict[tuple, int] = {}
        self.duplicates = 0             # 被忽略的重复元组数

    def add(self, tup) -> int:
        tup = tuple(tup)
        if len(tup) != self.arity:
            raise ValueError(f"元组长度 {len(tup)} 与表元数 {self.arity} 不符")
        if tup in self._ids:
            self.duplicates += 1
            return self._ids[tup]
        node = self.root
        for value in tup:
            node = node.children.setdefault(value, _TrieNode())
        node.terminal = True
        tid = len(self.tuples)
        self._ids[tup] = tid
        self.tuples.append(tup)
        return tid

    def __len__(self) -> int:
        return len(self.tuples)

    def node_count(self) -> int:
        """trie 节点总数（含根），用于衡量压缩效果。"""
        def count(node: _TrieNode) -> int:
            return 1 + sum(count(child) for child in node.children.values())
        return count(self.root)

    def naive_slots(self) -> int:
        """不压缩时需要的存储槽数（去重后元组数 × 元数）。"""
        return len(self.tuples) * self.arity


# ---------------------------------------------------------------------------
# 表约束
# ---------------------------------------------------------------------------

class TableConstraint:
    """scope 上的元组表约束，polarity 为 POSITIVE（正表）或 NEGATIVE（禁表）。"""

    def __init__(self, scope, tuples, polarity: str = POSITIVE):
        if polarity not in (POSITIVE, NEGATIVE):
            raise ValueError(f"未知 polarity: {polarity!r}")
        self.scope = tuple(scope)
        if len(set(self.scope)) != len(self.scope):
            raise ValueError("scope 中的变量必须互不相同")
        self.polarity = polarity
        self.pos_of = {var: i for i, var in enumerate(self.scope)}
        self.scope_set = set(self.scope)
        # 原始输入原样保留，供独立检查器使用（不经过压缩结构）。
        self.raw_tuples = [tuple(t) for t in tuples]
        self._raw_set = set(self.raw_tuples)
        self.table = SharedPrefixTable(len(self.scope))
        for tup in self.raw_tuples:
            self.table.add(tup)
        # (位置, 值) -> 包含该取值的元组 id 集合（去重后）
        self.index: dict[tuple[int, object], set[int]] = {}
        for tid, tup in enumerate(self.table.tuples):
            for pos, val in enumerate(tup):
                self.index.setdefault((pos, val), set()).add(tid)
        # 运行时状态，由 Engine 初始化：
        #   bad[tid]     该元组中有多少个位置的值已不在当前域内（0 表示存活）
        #   smatch[tid]  禁表专用：有多少个位置的域已收缩为等于元组值的单例
        #   supports     正表专用：(变量, 值) -> 存活元组支持数
        self.bad = [0] * len(self.table.tuples)
        self.smatch = [0] * len(self.table.tuples)
        self.supports: dict[tuple, int] = {}

    def live_tuple_count(self) -> int:
        return sum(1 for b in self.bad if b == 0)


def PositiveTable(scope, tuples) -> TableConstraint:
    return TableConstraint(scope, tuples, POSITIVE)


def NegativeTable(scope, tuples) -> TableConstraint:
    return TableConstraint(scope, tuples, NEGATIVE)


# ---------------------------------------------------------------------------
# 传播引擎：维护域与各约束的支持结构
# ---------------------------------------------------------------------------

class Engine:
    """维护变量域，并在域删值时同步失效相关元组支持。

    所有变更都记录到 trail，可用 mark()/undo() 在搜索中回溯。
    """

    def __init__(self, domains: dict, constraints):
        self.domains = {var: set(d) for var, d in domains.items()}
        self.constraints = list(constraints)
        self.trail: list[tuple] = []
        for c in self.constraints:
            self._setup(c)
        for c in self.constraints:
            self._initial_enforce(c)

    # -- 初始化 -----------------------------------------------------------

    def _setup(self, c: TableConstraint):
        n = len(c.table.tuples)
        c.bad = [0] * n
        c.smatch = [0] * n
        c.supports = {}
        for tid, tup in enumerate(c.table.tuples):
            for pos, val in enumerate(tup):
                dom = self.domains[c.scope[pos]]
                if val not in dom:
                    c.bad[tid] += 1
                elif c.polarity == NEGATIVE and len(dom) == 1:
                    c.smatch[tid] += 1
        if c.polarity == POSITIVE:
            for tid, tup in enumerate(c.table.tuples):
                if c.bad[tid] == 0:
                    for pos, val in enumerate(tup):
                        key = (c.scope[pos], val)
                        c.supports[key] = c.supports.get(key, 0) + 1

    def _initial_enforce(self, c: TableConstraint):
        if c.polarity == POSITIVE:
            if not c.table.tuples:
                raise Infeasible("正表为空，约束不可行")
            for var in c.scope:
                for val in sorted(self.domains[var], key=repr):
                    if c.supports.get((var, val), 0) == 0:
                        self.remove_value(var, val)
        else:
            # 禁表为空 -> 无限制，循环自然为空
            for tid in range(len(c.table.tuples)):
                if c.bad[tid] == 0:
                    self._enforce_negative_tuple(c, tid)

    # -- 域删值（核心：同步失效相关支持） ----------------------------------

    def remove_value(self, var, val):
        dom = self.domains[var]
        if val not in dom:
            return
        if len(dom) == 1:
            raise Infeasible(f"变量 {var!r} 的域被删空")
        dom.discard(val)
        self.trail.append(("value", var, val))
        # 1) 所有含 (var, val) 的元组同步失效
        for c in self.constraints:
            pos = c.pos_of.get(var)
            if pos is None:
                continue
            for tid in c.index.get((pos, val), ()):
                was_alive = c.bad[tid] == 0
                c.bad[tid] += 1
                self.trail.append(("bad", c, tid))
                if was_alive and c.polarity == POSITIVE:
                    # 先增加 bad 再撤销支持，避免级联删值时同一元组被重复 kill
                    self._kill_positive_tuple(c, tid)
        # 2) 域收缩为单例时，禁表中匹配该单例的元组更接近“被强制”
        if len(dom) == 1:
            singleton = next(iter(dom))
            for c in self.constraints:
                if c.polarity != NEGATIVE:
                    continue
                pos = c.pos_of.get(var)
                if pos is None:
                    continue
                for tid in c.index.get((pos, singleton), ()):
                    c.smatch[tid] += 1
                    self.trail.append(("smatch", c, tid))
                    if c.bad[tid] == 0:
                        self._enforce_negative_tuple(c, tid)

    def _kill_positive_tuple(self, c: TableConstraint, tid: int):
        """正表元组死亡：它对所有 (变量, 值) 的支持同步撤销。"""
        for pos, val in enumerate(c.table.tuples[tid]):
            var = c.scope[pos]
            key = (var, val)
            c.supports[key] -= 1
            self.trail.append(("support", c, key))
            if c.supports[key] == 0 and val in self.domains[var]:
                # 该值失去全部支持 -> 级联剪枝
                self.remove_value(var, val)

    def _enforce_negative_tuple(self, c: TableConstraint, tid: int):
        arity = len(c.scope)
        if c.smatch[tid] >= arity:
            raise Infeasible("禁表元组被单例域强制命中，冲突")
        if c.smatch[tid] == arity - 1:
            # 仅剩一个自由位置：该位置不能取元组中的值，否则命中禁表
            for pos, val in enumerate(c.table.tuples[tid]):
                dom = self.domains[c.scope[pos]]
                if not (len(dom) == 1 and val in dom):
                    self.remove_value(c.scope[pos], val)
                    break

    # -- 回溯 ---------------------------------------------------------------

    def mark(self) -> int:
        return len(self.trail)

    def undo(self, mark: int):
        while len(self.trail) > mark:
            entry = self.trail.pop()
            kind = entry[0]
            if kind == "value":
                self.domains[entry[1]].add(entry[2])
            elif kind == "bad":
                entry[1].bad[entry[2]] -= 1
            elif kind == "support":
                entry[1].supports[entry[2]] += 1
            elif kind == "smatch":
                entry[1].smatch[entry[2]] -= 1


# ---------------------------------------------------------------------------
# 独立检查器：只对照原始元组列表，不接触压缩结构
# ---------------------------------------------------------------------------

def check_solution(solution: dict, constraints) -> bool:
    """独立验证完整赋值是否满足所有原始约束。"""
    for c in constraints:
        tup = tuple(solution[var] for var in c.scope)
        present = tup in c._raw_set
        if c.polarity == POSITIVE and not present:
            return False
        if c.polarity == NEGATIVE and present:
            return False
    return True


# ---------------------------------------------------------------------------
# 回溯求解器（纯标准库，不调用 SAT/SMT）
# ---------------------------------------------------------------------------

def _search(engine: Engine):
    var, best = None, None
    for v, d in engine.domains.items():
        if len(d) > 1 and (best is None or len(d) < best):
            var, best = v, len(d)
    if var is None:
        return {v: next(iter(d)) for v, d in engine.domains.items()}
    for val in sorted(engine.domains[var], key=repr):
        mark = engine.mark()
        try:
            for other in sorted(engine.domains[var], key=repr):
                if other != val:
                    engine.remove_value(var, other)
            result = _search(engine)
            if result is not None:
                return result
        except Infeasible:
            pass
        finally:
            engine.undo(mark)
    return None


def solve(domains: dict, constraints):
    """返回经独立检查器验证的解（dict），不可行时返回 None。"""
    try:
        engine = Engine(domains, constraints)
        solution = _search(engine)
    except Infeasible:
        return None
    if solution is not None:
        assert check_solution(solution, constraints), "内部错误：解未通过独立检查"
    return solution
