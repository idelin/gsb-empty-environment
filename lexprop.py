"""字典序约束传播器（严格 / 非严格，支持不同长度向量与共享变量）。

语义规则（长度规则对所有情形一致，包括空向量）：
  比较前 min(n, m) 个位置；若某位置首先不同，则该位大小决定结果；
  若重合部分全部相等，则短者为小——非严格 (<=) 要求 n <= m，
  严格 (<) 要求左向量为右向量的真前缀，即 n < m。

推论：
  * 相同向量只满足非严格（X < X 恒不可满足）；
  * 前位已经严格更小，则后位不受任何额外约束；
  * 空向量：[] <= 任意向量成立；[] < Y 当且仅当 Y 非空。

实现要点：
  * 变量域有限，纯标准库，不调用 SAT/SMT 库；
  * 基于"首差位置 / 全等前缀"情形分析做多项式时间可行性判定，
    对每个 (变量, 值) 做支持检查，实现真正的域过滤（GAC），
    而非只比较当前上下界；
  * 每个被删值附带由原约束导出的证明（所有情形为何不可行）；
  * push/pop 保存并恢复域与前缀支持缓存；
  * 传播带预算，预算耗尽时明确报告尚未达到固定点。
"""
from __future__ import annotations

from dataclasses import dataclass


class Contradiction(Exception):
    """约束在当前域下不可满足（携带由原约束导出的证明）。"""

    def __init__(self, explanation: "Explanation"):
        self.explanation = explanation
        super().__init__(explanation.render())


class Variable:
    """有限域变量。共享变量即在多个位置引用同一 Variable 对象。"""

    __slots__ = ("name",)

    def __init__(self, name: str):
        self.name = name

    def __repr__(self) -> str:  # pragma: no cover - 便于调试输出
        return self.name


@dataclass(frozen=True)
class LexConstraint:
    """left (strict ? < : <=) right，按字典序。"""

    left: tuple
    right: tuple
    strict: bool = False

    def __init__(self, left, right, strict: bool = False):
        object.__setattr__(self, "left", tuple(left))
        object.__setattr__(self, "right", tuple(right))
        object.__setattr__(self, "strict", bool(strict))

    @property
    def variables(self) -> tuple:
        seen = []
        for var in self.left + self.right:
            if var not in seen:
                seen.append(var)
        return tuple(seen)

    def describe(self) -> str:
        rel = "<lex" if self.strict else "<=lex"
        lhs = "(" + ", ".join(v.name for v in self.left) + ")"
        rhs = "(" + ", ".join(v.name for v in self.right) + ")"
        return f"{lhs} {rel} {rhs}"


@dataclass(frozen=True)
class CaseFailure:
    """单个可行性情形失败的原因（证明的一部分）。"""

    case: str
    reason: str


@dataclass(frozen=True)
class Explanation:
    """由原约束导出的证明：主体 + 每个候选情形各自失败的原因。"""

    subject: str
    failures: tuple

    def render(self) -> str:
        lines = [self.subject + "："]
        for failure in self.failures:
            lines.append(f"  - 情形[{failure.case}]：{failure.reason}")
        return "\n".join(lines)


@dataclass(frozen=True)
class Deletion:
    """一次值删除及其证明。"""

    var: Variable
    value: object
    explanation: Explanation


@dataclass
class _Feasibility:
    feasible: bool
    witness: str = ""
    failures: tuple = ()


@dataclass
class PropResult:
    fixpoint: bool
    status: str  # "fixpoint" | "budget_exhausted"
    message: str
    deletions: list  # 本次 propagate 调用产生的 Deletion 列表


class Propagator:
    """对单个字典序约束做 GAC 域过滤。"""

    def __init__(self, constraint: LexConstraint, domains: dict):
        self.constraint = constraint
        missing = [v.name for v in constraint.variables if v not in domains]
        if missing:
            raise ValueError(f"缺少变量域：{missing}")
        self.domains = {v: set(domains[v]) for v in constraint.variables}
        for var, dom in self.domains.items():
            if not dom:
                raise ValueError(f"变量 {var.name} 的域为空")
        # 前缀支持缓存：前缀长度 p -> 该前缀等式诱导的变量等价类（与域无关的结构）
        self._prefix_cache: dict = {}
        self.deletions: list = []
        self._stack: list = []

    # ---------- push / pop ----------

    def push(self) -> None:
        """保存当前域、前缀支持缓存与删除日志。"""
        snapshot = (
            {v: set(d) for v, d in self.domains.items()},
            dict(self._prefix_cache),
            list(self.deletions),
        )
        self._stack.append(snapshot)

    def pop(self) -> None:
        """恢复到最近一次 push 的状态（含前缀支持缓存）。"""
        if not self._stack:
            raise IndexError("pop 时没有可恢复的状态")
        domains, cache, deletions = self._stack.pop()
        self.domains = domains
        self._prefix_cache = cache
        self.deletions = deletions

    # ---------- 前缀等式结构（缓存） ----------

    def _prefix_classes(self, p: int):
        """前 p 个位置的等式 left[j]=right[j] 诱导的变量等价类。"""
        cached = self._prefix_cache.get(p)
        if cached is not None:
            return cached
        con = self.constraint
        parent = {v: v for v in con.variables}

        def find(v):
            while parent[v] is not v:
                parent[v] = parent[parent[v]]
                v = parent[v]
            return v

        for j in range(p):
            ra, rb = find(con.left[j]), find(con.right[j])
            if ra is not rb:
                parent[rb] = ra
        groups: dict = {}
        for var in con.variables:
            groups.setdefault(find(var), set()).add(var)
        classes = frozenset(frozenset(g) for g in groups.values())
        self._prefix_cache[p] = classes
        return classes

    # ---------- 可行性判定（情形分析） ----------

    def _feasibility(self, domains: dict) -> _Feasibility:
        """判断约束在给定域下是否可满足；不可行时给出全部情形的失败原因。"""
        con = self.constraint
        n, m = len(con.left), len(con.right)
        k = min(n, m)
        failures = []

        for i in range(k):
            classes = self._prefix_classes(i)
            inter = {}
            bad = None
            for cls in classes:
                common = set.intersection(*(set(domains[v]) for v in cls))
                if not common:
                    bad = cls
                    break
                inter[cls] = common
            case = f"首差位置 i={i}"
            if bad is not None:
                names = "、".join(sorted(v.name for v in bad))
                failures.append(CaseFailure(case, f"前缀等式等价类 {{{names}}} 的域交集为空"))
                continue
            cls_l = next(c for c in classes if con.left[i] in c)
            cls_r = next(c for c in classes if con.right[i] in c)
            if cls_l == cls_r:
                failures.append(
                    CaseFailure(case, f"位置 {i} 两侧被前缀等式并入同一等价类，无法严格小于")
                )
                continue
            lo, hi = min(inter[cls_l]), max(inter[cls_r])
            if lo < hi:
                return _Feasibility(True, f"首差位置 i={i}：取左侧 {lo} < 右侧 {hi}")
            failures.append(
                CaseFailure(case, f"需要左侧值小于右侧值，但左侧最小可取 {lo}，右侧最大可取 {hi}")
            )

        # 重合部分全部相等的情形，由长度规则裁决
        case = "全等前缀情形"
        length_ok = (n < m) if con.strict else (n <= m)
        if not length_ok:
            if con.strict:
                rule = f"严格字典序要求左向量为右向量的真前缀（需要 {n} < {m}，不成立）"
            else:
                rule = f"左向量全等时需为右向量前缀（需要长度 {n} <= {m}，不成立）"
            failures.append(CaseFailure(case, rule))
        else:
            classes = self._prefix_classes(k)
            bad = None
            for cls in classes:
                if not set.intersection(*(set(domains[v]) for v in cls)):
                    bad = cls
                    break
            if bad is None:
                rel = "<" if con.strict else "<="
                return _Feasibility(True, f"前 {k} 位全部相等，长度规则 {n} {rel} {m} 成立")
            names = "、".join(sorted(v.name for v in bad))
            failures.append(CaseFailure(case, f"等式等价类 {{{names}}} 的域交集为空"))

        return _Feasibility(False, "", tuple(failures))

    # ---------- 传播 ----------

    def propagate(self, budget: int | None = None) -> PropResult:
        """GAC 域过滤直到固定点；budget 为支持检查次数上限。

        预算耗尽时返回 fixpoint=False，并明确说明尚未达到传播固定点。
        约束不可满足时抛出 Contradiction（携带证明）。
        """
        con = self.constraint
        start = len(self.deletions)
        checks = 0
        while True:
            feas = self._feasibility(self.domains)
            if not feas.feasible:
                raise Contradiction(
                    Explanation(f"约束 {con.describe()} 在当前域下不可满足", feas.failures)
                )
            changed = False
            exhausted = False
            for var in con.variables:
                for value in sorted(self.domains[var]):
                    if budget is not None and checks >= budget:
                        exhausted = True
                        break
                    checks += 1
                    trial = dict(self.domains)
                    trial[var] = {value}
                    trial_feas = self._feasibility(trial)
                    if not trial_feas.feasible:
                        self.domains[var].discard(value)
                        explanation = Explanation(
                            f"变量 {var.name} 的值 {value} 被删除：固定 {var.name}={value} 后"
                            f"原约束 {con.describe()} 不可满足",
                            trial_feas.failures,
                        )
                        self.deletions.append(Deletion(var, value, explanation))
                        changed = True
                if exhausted:
                    break
            if exhausted:
                return PropResult(
                    False,
                    "budget_exhausted",
                    "预算不足，尚未达到传播固定点；已删值均附带证明，但剩余域中可能仍有可删值",
                    self.deletions[start:],
                )
            if not changed:
                return PropResult(
                    True,
                    "fixpoint",
                    "达到传播固定点：剩余域中每个值都有支持",
                    self.deletions[start:],
                )

    def is_satisfiable(self) -> bool:
        """当前域下约束是否仍可满足。"""
        return self._feasibility(self.domains).feasible
