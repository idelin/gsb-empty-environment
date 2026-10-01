"""验证工具：直接计数、原赋值 -> 辅助赋值扩展、投影等价性、传播能力度量。

投影等价的含义：对任一原变量赋值 sigma，
    sigma 满足基数约束  <=>  存在辅助变量赋值使 CNF 成立。
两个方向分别验证：
  * =>  构造性方向：extend_assignment 按编码定义确定性地算出辅助赋值，
        再用独立检查器 check_assignment 验证 CNF 被满足；
  * <=  存在性方向：小变量数时穷举全部辅助赋值，否则用自实现的 DPLL 判定。
"""

from itertools import product

from cnf import (
    brute_force_extensions,
    check_assignment,
    dpll,
    lit_value,
    unit_propagate,
)
from encoders import AT_LEAST, AT_MOST


def direct_count(lits, assignment):
    """独立计数：赋值下为真的文字个数。"""
    return sum(1 for lit in lits if lit_value(lit, assignment) is True)


def direct_check(sense, lits, k, assignment):
    """独立检查器：直接按语义判断赋值是否满足基数约束。"""
    count = direct_count(lits, assignment)
    if sense == AT_MOST:
        return count <= k
    if sense == AT_LEAST:
        return count >= k
    return count == k  # exactly


def _eval_lit(lit, assignment):
    if lit is None:  # 常量 False
        return False
    return lit_value(lit, assignment) is True


def extend_assignment(enc, assignment):
    """由原赋值按编码定义确定性地构造完整（含辅助变量）赋值。"""
    full = dict(assignment)
    for lit in enc.lits:  # 未赋值的原变量按 False 补全，保证赋值完整
        full.setdefault(abs(lit), False)
    if enc.activation is not None:
        full[enc.activation] = True
    for defn in enc.definitions:
        var, op = defn[0], defn[1]
        if op == "or":
            full[var] = _eval_lit(defn[2], full) or _eval_lit(defn[3], full)
        elif op == "and":
            full[var] = _eval_lit(defn[2], full) and _eval_lit(defn[3], full)
        elif op == "count_ge":
            lits, threshold = defn[2], defn[3]
            full[var] = sum(1 for lit in lits if _eval_lit(lit, full)) >= threshold
        else:
            raise ValueError(f"未知定义类型: {op}")
    return full


def _base_assignment(enc, original_vars, values):
    base = dict(zip(original_vars, values))
    if enc.activation is not None:
        base[enc.activation] = True
    return base


def check_projection_equivalence(enc, original_vars, brute_force_limit=10):
    """对全部 2^n 个原赋值验证投影等价，返回报告字典。"""
    report = {"total": 0, "ok": 0, "mismatches": [], "brute_forced": 0, "dpll": 0}
    aux = list(enc.aux_vars)
    for values in product((False, True), repeat=len(original_vars)):
        base = _base_assignment(enc, original_vars, values)
        expect = direct_check(enc.sense, enc.lits, enc.k, base)
        if expect:
            # 构造性方向：扩展必须真的满足 CNF（独立检查器）
            full = extend_assignment(enc, base)
            if not check_assignment(enc.clauses, full):
                report["mismatches"].append(("extension_failed", base))
                report["total"] += 1
                continue
        # 存在性方向
        if len(aux) <= brute_force_limit:
            exists = brute_force_extensions(enc.clauses, base, aux) > 0
            report["brute_forced"] += 1
        else:
            exists = dpll(enc.clauses, base) is not None
            report["dpll"] += 1
        if exists != expect:
            report["mismatches"].append(("exists_mismatch", base, expect))
        else:
            report["ok"] += 1
        report["total"] += 1
    report["equivalent"] = not report["mismatches"]
    return report


def propagation_metrics(enc, original_vars):
    """在全部 3^n 个部分赋值上度量单元传播（UP）能力。

    返回：
      partial        部分赋值总数
      inconsistent   其中无法扩展为解的数量
      up_conflict    其中 UP 直接推出冲突的数量（越大传播越强）
      forced         存在某个原变量被所有扩展强制取值的总次数
      up_forced      其中 UP 能推出该强制值的次数
      up_derived     UP 在所有部分赋值上推导出的赋值总数（传播工作量）
    """
    stats = {"partial": 0, "inconsistent": 0, "up_conflict": 0,
             "forced": 0, "up_forced": 0, "up_derived": 0}
    assumptions = {}
    if enc.activation is not None:
        assumptions[enc.activation] = True
    for values in product((-1, 0, 1), repeat=len(original_vars)):
        partial = {v: (b == 1) for v, b in zip(original_vars, values) if b != 0}
        partial.update(assumptions)
        stats["partial"] += 1
        propagated, conflict = unit_propagate(enc.clauses, partial)
        stats["up_derived"] += len(propagated) - len(partial)
        if dpll(enc.clauses, partial) is None:
            stats["inconsistent"] += 1
            if conflict:
                stats["up_conflict"] += 1
            continue
        for var in original_vars:
            if var in partial:
                continue
            t = dpll(enc.clauses, {**partial, var: True})
            f = dpll(enc.clauses, {**partial, var: False})
            if (t is None) == (f is None):
                continue  # 两值都可扩展（未强制）；两个都 None 与一致性矛盾
            stats["forced"] += 1
            forced_value = t is not None
            if propagated.get(var) == forced_value:
                stats["up_forced"] += 1
    return stats


def solve_and_verify(enc, original_vars):
    """用自实现 DPLL 求解；声称有解时投影回原变量并用独立检查器验证。"""
    assumptions = {}
    if enc.activation is not None:
        assumptions[enc.activation] = True
    solution = dpll(enc.clauses, assumptions)
    if solution is None:
        return None, False
    projection = {v: solution.get(v, False) for v in original_vars}
    verified = direct_check(enc.sense, enc.lits, enc.k, projection)
    return projection, verified
