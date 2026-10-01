"""CNF 基础设施：变量管理、子句规范化、单元传播、朴素 DPLL、穷举与独立检查。

只使用标准库，不调用任何 SAT/SMT 求解器。文字用 DIMACS 风格的有符号整数
表示：正整数 v 表示变量 v 为真，-v 表示变量 v 为假。空子句 () 表示不可满足。
"""

from itertools import product


class VarManager:
    """单调递增地分配变量编号，保证不同编码的辅助变量永不冲突。"""

    def __init__(self, start=1):
        if start < 1:
            raise ValueError("DIMACS 变量编号从 1 开始")
        self._next = start
        self.names = {}

    def new_var(self, name=None):
        var = self._next
        self._next += 1
        self.names[var] = name or f"v{var}"
        return var

    def ensure(self, var):
        """声明外部已占用的最大编号，后续辅助变量从其之后开始分配。"""
        if var >= self._next:
            self._next = var + 1

    @property
    def num_vars(self):
        return self._next - 1

    def name(self, var):
        return self.names.get(var, f"v{var}")


def normalize_clause(clause):
    """去重并排序；含互补文字的子句是重言式，返回 None 表示应丢弃。"""
    seen = set()
    for lit in clause:
        if -lit in seen:
            return None
        seen.add(lit)
    return tuple(sorted(seen, key=lambda l: (abs(l), l < 0)))


class CNF:
    """简单的子句集合容器。"""

    def __init__(self):
        self.clauses = []

    def add(self, clause):
        norm = normalize_clause(clause)
        if norm is not None:
            self.clauses.append(norm)

    def extend(self, clauses):
        for clause in clauses:
            self.add(clause)

    @property
    def num_clauses(self):
        return len(self.clauses)

    def vars(self):
        return {abs(lit) for clause in self.clauses for lit in clause}


def lit_value(lit, assignment):
    """赋值下文字的真值：True / False / None（未赋值）。"""
    val = assignment.get(abs(lit))
    if val is None:
        return None
    return val == (lit > 0)


def clause_status(clause, assignment):
    """返回 ('sat'|'conflict'|'unit'|'open', unit_lit_or_None)。"""
    unassigned = []
    for lit in clause:
        val = lit_value(lit, assignment)
        if val is True:
            return "sat", None
        if val is None:
            unassigned.append(lit)
    if not unassigned:
        return "conflict", None
    if len(unassigned) == 1:
        return "unit", unassigned[0]
    return "open", None


def unit_propagate(clauses, assignment):
    """单元传播至不动点。返回 (assignment, conflict?)。不修改入参。"""
    assignment = dict(assignment)
    changed = True
    while changed:
        changed = False
        for clause in clauses:
            status, lit = clause_status(clause, assignment)
            if status == "conflict":
                return assignment, True
            if status == "unit":
                assignment[abs(lit)] = lit > 0
                changed = True
    return assignment, False


def check_assignment(clauses, assignment):
    """独立检查器：给定（足够完整的）赋值，逐子句验证是否被满足。"""
    return all(
        any(lit_value(lit, assignment) is True for lit in clause)
        for clause in clauses
    )


def dpll(clauses, assignment=None):
    """朴素 DPLL（自实现，非 SAT 库）。返回满足赋值字典或 None。"""
    assignment, conflict = unit_propagate(clauses, assignment or {})
    if conflict:
        return None
    var = None
    for clause in clauses:
        status, _ = clause_status(clause, assignment)
        if status != "sat":
            for lit in clause:
                if abs(lit) not in assignment:
                    var = abs(lit)
                    break
        if var is not None:
            break
    if var is None:
        return assignment
    for value in (True, False):
        result = dpll(clauses, {**assignment, var: value})
        if result is not None:
            return result
    return None


def brute_force_extensions(clauses, fixed, aux_vars):
    """穷举全部辅助赋值，返回满足 CNF 的扩展数量（仅适合小变量数）。"""
    count = 0
    for bits in product((False, True), repeat=len(aux_vars)):
        assignment = dict(fixed)
        assignment.update(zip(aux_vars, bits))
        if check_assignment(clauses, assignment):
            count += 1
    return count
