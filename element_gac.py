"""value = array[index] 的广义弧一致（GAC）传播器。

特性
----
- index、value 与数组元素均可为有限域变量或常量；
- 别名感知：index 与数组元素可指向同一个 Variable，枚举支持时按
  “去重后的变量”赋值，保留同一变量在不同位置取值一致的相关性；
- 越界下标剔除并记录原因，每个被删值都有无支持证据；
- 基于 trail 的 mark/undo 撤回，支持回溯搜索；
- 独立检查器按语义直接验证原约束，不复用传播代码；
- 空数组不可满足。

仅使用标准库，Python 3.11。
"""

from itertools import product


class Variable:
    """有限域变量。按身份（identity）区分，允许同一变量出现在约束的多个位置。"""

    __slots__ = ("name", "domain")

    def __init__(self, name, domain):
        self.name = name
        self.domain = set(domain)

    def __repr__(self):
        return f"Variable({self.name!r}, {sorted(self.domain)})"


class Removal:
    """一次值删除的证据：谁被删、为什么、是否已被撤回。"""

    __slots__ = ("var_name", "value", "reason", "undone")

    def __init__(self, var_name, value, reason):
        self.var_name = var_name
        self.value = value
        self.reason = reason
        self.undone = False

    def __str__(self):
        tag = "（已撤回）" if self.undone else ""
        return f"删除 {self.var_name}={self.value}{tag}：{self.reason}"


class ElementConstraint:
    """约束 value == array[index]。

    index 为 Variable；array 为 Variable/常量 列表；value 为 Variable/常量。
    """

    def __init__(self, index, array, value):
        if not isinstance(index, Variable):
            raise TypeError("index 必须是 Variable")
        self.index = index
        self.array = list(array)
        self.value = value
        # 作用域：按出现顺序去重的变量。别名变量只出现一次，
        # 枚举支持时同一变量在所有位置取同一个值，相关性得以保留。
        self.scope = []
        for item in (index, *self.array, value):
            if isinstance(item, Variable) and item not in self.scope:
                self.scope.append(item)

    def _holds(self, assign):
        """assign: {Variable: 取值}，要求覆盖 scope。"""
        pos = assign[self.index]
        if not isinstance(pos, int) or isinstance(pos, bool):
            return False
        if pos < 0 or pos >= len(self.array):
            return False
        target = self.array[pos]
        elem = assign[target] if isinstance(target, Variable) else target
        wanted = assign[self.value] if isinstance(self.value, Variable) else self.value
        return elem == wanted

    def revise(self):
        """对 scope 做广义弧一致：返回应删除的 (var, value, reason) 列表。

        小域枚举全部变量组合，收集每个值的支持；无支持者给出原因。
        """
        if any(not v.domain for v in self.scope):
            return []
        supported = {v: set() for v in self.scope}
        domains = [sorted(v.domain) for v in self.scope]
        for combo in product(*domains):
            assign = dict(zip(self.scope, combo))
            if self._holds(assign):
                for var, val in assign.items():
                    supported[var].add(val)
        removals = []
        for var in self.scope:
            for val in sorted(var.domain):
                if val not in supported[var]:
                    removals.append((var, val, self._reason(var, val)))
        return removals

    def _reason(self, var, val):
        n = len(self.array)
        if var is self.index:
            if not isinstance(val, int) or isinstance(val, bool) or val < 0 or val >= n:
                if n == 0:
                    return "数组为空，任何下标都越界，该值不可能有支持"
                return f"越界：index={val} 不在合法范围 0..{n - 1}（数组长度为 {n}）"
            return (f"无支持：index={val} 时 array[{val}] 与 value 的当前域"
                    f"没有共同取值（枚举全部组合均不满足）")
        if var is self.value:
            return (f"无支持：value={val} 时，index 当前域内没有任何合法下标 i "
                    f"能使 array[i] 取到 {val}")
        positions = [p for p, item in enumerate(self.array) if item is var]
        return (f"无支持：该变量（数组位置 {positions}）取 {val} 时，"
                f"index 与 value 的当前域无法同时满足 value=array[index]")


class NotEqualConstraint:
    """辅助约束 a != b（用于构造需要回溯的搜索场景）。仅当一端单值时传播。"""

    def __init__(self, a, b):
        self.a = a
        self.b = b
        self.scope = [a, b]

    def revise(self):
        removals = []
        if len(self.a.domain) == 1:
            (only,) = self.a.domain
            if only in self.b.domain:
                removals.append((self.b, only,
                                 f"{self.a.name} 已确定为 {only}，"
                                 f"{self.b.name} != {self.a.name} 要求删除"))
        if len(self.b.domain) == 1:
            (only,) = self.b.domain
            if only in self.a.domain:
                removals.append((self.a, only,
                                 f"{self.b.name} 已确定为 {only}，"
                                 f"{self.a.name} != {self.b.name} 要求删除"))
        return removals


class CSP:
    """变量 + 约束 + trail 撤回 + 删除证据。"""

    def __init__(self, verbose=False):
        self.variables = []
        self.constraints = []
        self.verbose = verbose
        self.trail = []         # [(var, value, Removal)]，用于撤回
        self.explanations = []  # [Removal]，含已撤回项，作为证据保留

    def add_variable(self, var):
        self.variables.append(var)
        return var

    def add_constraint(self, con):
        self.constraints.append(con)
        return con

    def _remove_value(self, var, value, reason):
        if value not in var.domain:
            return False
        var.domain.discard(value)
        removal = Removal(var.name, value, reason)
        self.trail.append((var, value, removal))
        self.explanations.append(removal)
        if self.verbose:
            print(f"  {removal}")
        return True

    def assign(self, var, value):
        """搜索决策：把 var 定为 value（删除其余值）。"""
        for other in sorted(var.domain):
            if other != value:
                self._remove_value(var, other, f"搜索决策：试 {var.name}={value}")

    def mark(self):
        return len(self.trail)

    def undo(self, mark):
        """撤回到 mark 处：恢复被删的值，并把对应证据标记为已撤回。"""
        while len(self.trail) > mark:
            var, value, removal = self.trail.pop()
            var.domain.add(value)
            removal.undone = True
            if self.verbose:
                print(f"  撤回 {var.name}={value}")

    def propagate(self):
        """AC-3 风格不动点传播。任一变量域变空返回 False。"""
        queue = list(self.constraints)
        while queue:
            con = queue.pop(0)
            for var, value, reason in con.revise():
                if self._remove_value(var, value, reason):
                    for other in self.constraints:
                        if (other is not con and var in other.scope
                                and other not in queue):
                            queue.append(other)
            if any(not v.domain for v in self.variables):
                return False
        return True

    def explain(self, var_name=None):
        """每个被删值的无支持证据（含已撤回项，带标记）。"""
        return [r for r in self.explanations
                if var_name is None or r.var_name == var_name]


def solve(csp):
    """DFS + 回溯。返回 {变量名: 取值} 或 None。

    声称有解前，先用独立检查器验证原约束。
    """
    if not csp.propagate():
        return None
    result = _dfs(csp)
    if result is not None and not check_solution(csp.constraints, result):
        raise AssertionError(f"内部错误：解未通过独立检查器 {result}")
    return result


def _dfs(csp):
    var = next((v for v in csp.variables if len(v.domain) > 1), None)
    if var is None:
        return {v.name: next(iter(v.domain)) for v in csp.variables}
    for value in sorted(var.domain):
        m = csp.mark()
        csp.assign(var, value)
        result = _dfs(csp) if csp.propagate() else None
        if result is not None:
            return result
        csp.undo(m)
    return None


# ----------------------------------------------------------------------
# 独立检查器：不复用传播代码，直接按语义验证原约束
# ----------------------------------------------------------------------

def check_element(index_value, array_values, value_value):
    """直接按定义验证 value == array[index]。"""
    if not isinstance(index_value, int) or isinstance(index_value, bool):
        return False
    if index_value < 0 or index_value >= len(array_values):
        return False
    return array_values[index_value] == value_value


def check_solution(constraints, solution):
    """独立验证完整解 {变量名: 取值} 是否满足所有原始约束。"""
    for con in constraints:
        if isinstance(con, ElementConstraint):
            idx = solution[con.index.name]
            arr = [solution[x.name] if isinstance(x, Variable) else x
                   for x in con.array]
            wanted = (solution[con.value.name]
                      if isinstance(con.value, Variable) else con.value)
            if not check_element(idx, arr, wanted):
                return False
        elif isinstance(con, NotEqualConstraint):
            if solution[con.a.name] == solution[con.b.name]:
                return False
        else:
            raise TypeError(f"未知约束类型：{type(con).__name__}")
    return True


def brute_force_solutions(constraints, domains):
    """小域枚举全部赋值，用独立检查器筛选。domains: {变量名: 可迭代}。"""
    names = list(domains)
    out = []
    for combo in product(*(sorted(domains[n]) for n in names)):
        sol = dict(zip(names, combo))
        if check_solution(constraints, sol):
            out.append(sol)
    return out


# ----------------------------------------------------------------------
# 运行样例
# ----------------------------------------------------------------------

def _show_domains(csp, title):
    print(title)
    for v in csp.variables:
        print(f"  {v.name} ∈ {sorted(v.domain)}")


def demo():
    print("=" * 64)
    print("样例 1：基本传播 —— 越界剔除、无支持剔除与解释")
    print("=" * 64)
    csp = CSP(verbose=True)
    I = csp.add_variable(Variable("I", {0, 1, 2, 3}))
    A = csp.add_variable(Variable("A", {1, 2, 3}))
    B = csp.add_variable(Variable("B", {2, 3, 4}))
    V = csp.add_variable(Variable("V", {1, 2, 3, 4, 5}))
    csp.add_constraint(ElementConstraint(I, [A, B, 7], V))
    _show_domains(csp, "传播前：")
    print("传播过程：")
    ok = csp.propagate()
    _show_domains(csp, f"传播后（可满足={ok}）：")
    print("删除值的证据：")
    for r in csp.explain():
        print(f"  {r}")
    sol = solve(csp)
    print(f"求解结果：{sol}")
    print(f"独立检查器验证：{check_solution(csp.constraints, sol)}")
    print()

    print("=" * 64)
    print("样例 2：index 自引用（别名）—— array[0] 与 index 是同一变量")
    print("=" * 64)
    csp2 = CSP(verbose=True)
    J = csp2.add_variable(Variable("J", {0, 1, 2}))
    W = csp2.add_variable(Variable("W", {0, 5, 9}))
    csp2.add_constraint(ElementConstraint(J, [J, 9], W))
    _show_domains(csp2, "传播前：")
    print("传播过程：")
    csp2.propagate()
    _show_domains(csp2, "传播后：")
    sol2 = solve(csp2)
    print(f"求解结果：{sol2}")
    print(f"独立检查器验证：{check_solution(csp2.constraints, sol2)}")
    print("全部解（独立枚举核对）：")
    for s in brute_force_solutions(csp2.constraints,
                                   {"J": {0, 1, 2}, "W": {0, 5, 9}}):
        print(f"  {s}")
    print()

    print("=" * 64)
    print("样例 3：回溯与撤回 —— 第一个搜索分支失败，撤回后另寻他枝")
    print("=" * 64)
    csp3 = CSP(verbose=True)
    I3 = csp3.add_variable(Variable("I", {0, 1}))
    A3 = csp3.add_variable(Variable("A", {1, 2}))
    B3 = csp3.add_variable(Variable("B", {1, 2}))
    V3 = csp3.add_variable(Variable("V", {1, 2}))
    csp3.add_constraint(ElementConstraint(I3, [A3, B3], V3))
    csp3.add_constraint(NotEqualConstraint(V3, A3))
    print("搜索过程：")
    sol3 = solve(csp3)
    print(f"求解结果：{sol3}")
    print(f"独立检查器验证：{check_solution(csp3.constraints, sol3)}")
    print("全部删除证据（含已撤回）：")
    for r in csp3.explain():
        print(f"  {r}")


if __name__ == "__main__":
    demo()
