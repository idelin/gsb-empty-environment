"""proofcheck — 小型可验证证明步骤检查器。

对象：单变量有理系数多项式 Q[x]，用 fractions.Fraction 精确计算，
不调用任何符号代数库。

证明格式（JSON 友好的 dict）：
    {
      "variable": "x",
      "steps": [
        {"rule": "init",  "expr": <ast>, "domain": <str>},
        {"rule": "expand", "expr": <ast>, "domain": <str>},
        {"rule": "combine", "expr": <ast>, "domain": <str>},
        {"rule": "diff",   "expr": <ast>, "domain": <str>},
        {"rule": "substitute", "expr": <ast>, "value": "3/4", "domain": <str>},
      ]
    }

表达式 AST：
    {"num": "3/4"}                有理数（Fraction 可解析的字符串）
    {"var": "x"}                  变量
    {"add": [a, b, ...]}          加法
    {"mul": [a, b, ...]}          乘法
    {"neg": a}                    取负
    {"pow": [base, n]}            幂，n 为非负整数

验证原则：
  * 每步由验证器独立重算，绝不凭步骤标签信任结论；
  * 每步必须声明适用域（domain 字段），验证器同时独立检查前提；
  * 未知规则直接拒绝；
  * 任一系数被篡改时，报告首个失败步骤的序号；
  * 最终结果 = 最后一个通过验证的表达式的多项式。
"""

from __future__ import annotations

from fractions import Fraction

Poly = dict  # 映射：指数(int, >=0) -> 系数(Fraction)，零系数不存储


class ProofError(Exception):
    """验证失败；step_index 为首个失败步骤的下标（-1 表示结构性错误）。"""

    def __init__(self, step_index: int, rule: str, message: str):
        self.step_index = step_index
        self.rule = rule
        super().__init__(f"step {step_index} ({rule}): {message}")


# ---------------------------------------------------------------- 多项式运算

def _clean(poly: Poly) -> Poly:
    return {e: c for e, c in poly.items() if c != 0}


def poly_add(a: Poly, b: Poly) -> Poly:
    out = dict(a)
    for e, c in b.items():
        out[e] = out.get(e, Fraction(0)) + c
    return _clean(out)


def poly_neg(a: Poly) -> Poly:
    return {e: -c for e, c in a.items()}


def poly_mul(a: Poly, b: Poly) -> Poly:
    out: Poly = {}
    for ea, ca in a.items():
        for eb, cb in b.items():
            out[ea + eb] = out.get(ea + eb, Fraction(0)) + ca * cb
    return _clean(out)


def poly_pow(base: Poly, n: int) -> Poly:
    result: Poly = {0: Fraction(1)}
    for _ in range(n):
        result = poly_mul(result, base)
    return result


def poly_diff(poly: Poly) -> Poly:
    """幂法则逐项求导；适用域：Q[x]（非负整数指数）。"""
    return _clean({e - 1: c * e for e, c in poly.items() if e > 0})


def poly_eval(poly: Poly, value: Fraction) -> Poly:
    """在有理点处代入求值，结果为常数多项式。"""
    total = sum((c * value ** e for e, c in poly.items()), Fraction(0))
    return _clean({0: total})


# ---------------------------------------------------------------- AST -> 多项式

def ast_to_poly(node, variable: str, step_index: int = -1, rule: str = "parse") -> Poly:
    def fail(msg):
        raise ProofError(step_index, rule, msg)

    if not isinstance(node, dict) or len(node) != 1:
        fail(f"非法表达式节点: {node!r}")
    if "num" in node:
        try:
            coeff = Fraction(node["num"])
        except (ValueError, TypeError, ZeroDivisionError):
            fail(f"非法有理数: {node['num']!r}")
        return _clean({0: coeff})
    if "var" in node:
        if node["var"] != variable:
            fail(f"未知变量 {node['var']!r}，本证明变量为 {variable!r}")
        return {1: Fraction(1)}
    if "add" in node:
        parts = node["add"]
        if not isinstance(parts, list) or not parts:
            fail("add 需要非空列表")
        acc: Poly = {}
        for part in parts:
            acc = poly_add(acc, ast_to_poly(part, variable, step_index, rule))
        return acc
    if "mul" in node:
        parts = node["mul"]
        if not isinstance(parts, list) or not parts:
            fail("mul 需要非空列表")
        acc: Poly = {0: Fraction(1)}
        for part in parts:
            acc = poly_mul(acc, ast_to_poly(part, variable, step_index, rule))
        return acc
    if "neg" in node:
        return poly_neg(ast_to_poly(node["neg"], variable, step_index, rule))
    if "pow" in node:
        base_node, n = node["pow"]
        if not isinstance(n, int) or isinstance(n, bool) or n < 0:
            fail(f"pow 指数必须为非负整数（适用域 Q[x]），得到 {n!r}")
        return poly_pow(ast_to_poly(base_node, variable, step_index, rule), n)
    fail(f"未知表达式节点: {node!r}")


# ---------------------------------------------------------------- 结构检查

def _contains_expandable(node) -> bool:
    """存在可展开结构：乘法含加法因子，或加法为底的幂。"""
    if not isinstance(node, dict):
        return False
    if "mul" in node:
        parts = node["mul"]
        if any(isinstance(p, dict) and "add" in p for p in parts):
            return True
        return any(_contains_expandable(p) for p in parts)
    if "pow" in node:
        base, _ = node["pow"]
        if isinstance(base, dict) and "add" in base:
            return True
        return _contains_expandable(base)
    if "add" in node:
        return any(_contains_expandable(p) for p in node["add"])
    if "neg" in node:
        return _contains_expandable(node["neg"])
    return False


def _monomial_exponent(node):
    """若 node 是规范单项式（c、x、c*x、c*x^k、x^k），返回其指数，否则 None。"""
    if not isinstance(node, dict) or len(node) != 1:
        return None
    if "num" in node:
        return 0
    if "var" in node:
        return 1
    if "pow" in node:
        base, n = node["pow"]
        if isinstance(base, dict) and "var" in base and isinstance(n, int) and n >= 1:
            return n
        return None
    if "mul" in node:
        parts = node["mul"]
        if (len(parts) == 2 and isinstance(parts[0], dict) and "num" in parts[0]
                and Fraction(parts[0]["num"]) != 0):
            inner = _monomial_exponent(parts[1])
            if inner is not None and inner >= 1:
                return inner
    return None


def _is_canonical(node) -> bool:
    """合并同类项后的规范形：每项为单项式，指数互异且严格降序，无零系数。"""
    terms = node["add"] if isinstance(node, dict) and "add" in node else [node]
    exponents = []
    for term in terms:
        exp = _monomial_exponent(term)
        if exp is None:
            return False
        if isinstance(term, dict) and "num" in term and Fraction(term["num"]) == 0:
            return False
        exponents.append(exp)
    return exponents == sorted(exponents, reverse=True) and len(set(exponents)) == len(exponents)


# ---------------------------------------------------------------- 规则验证

# 每条规则要求步骤声明的适用域；验证器同时独立检查实际前提。
REQUIRED_DOMAIN = {
    "init": "Q[x]",
    "expand": "Q[x]",
    "combine": "Q[x]",
    "diff": "Q[x]（多项式，非负整数指数，逐项幂法则）",
    "substitute": "x ∈ Q（有理点代入）",
}


def _check_domain(step: dict, index: int, rule: str) -> None:
    declared = step.get("domain")
    if declared is None:
        raise ProofError(index, rule, "缺少 domain 字段：每步必须声明适用域")
    if declared != REQUIRED_DOMAIN[rule]:
        raise ProofError(
            index, rule,
            f"适用域声明不符：要求 {REQUIRED_DOMAIN[rule]!r}，得到 {declared!r}")


def _verify_expand(prev_ast, prev_poly: Poly, step: dict, index: int, variable: str) -> tuple:
    if not _contains_expandable(prev_ast):
        raise ProofError(index, "expand", "上一步表达式不含可展开结构，规则不适用")
    claim = ast_to_poly(step.get("expr"), variable, index, "expand")
    if claim != prev_poly:
        raise ProofError(index, "expand", "展开结果与上一步表达式不相等")
    return step["expr"], claim


def _verify_combine(prev_ast, prev_poly: Poly, step: dict, index: int, variable: str) -> tuple:
    claim = ast_to_poly(step.get("expr"), variable, index, "combine")
    if claim != prev_poly:
        raise ProofError(index, "combine", "合并结果与上一步表达式不相等")
    if not _is_canonical(step["expr"]):
        raise ProofError(index, "combine", "结果不是规范形（存在未合并同类项或零系数项）")
    return step["expr"], claim


def _verify_diff(prev_ast, prev_poly: Poly, step: dict, index: int, variable: str) -> tuple:
    claim = ast_to_poly(step.get("expr"), variable, index, "diff")
    expected = poly_diff(prev_poly)
    if claim != expected:
        raise ProofError(index, "diff", "求导结果与独立重算不一致")
    return step["expr"], claim


def _verify_substitute(prev_ast, prev_poly: Poly, step: dict, index: int, variable: str) -> tuple:
    if "value" not in step:
        raise ProofError(index, "substitute", "缺少代入值 value")
    try:
        value = Fraction(step["value"])
    except (ValueError, TypeError, ZeroDivisionError):
        raise ProofError(index, "substitute",
                         f"代入值必须为有理数（适用域 x ∈ Q），得到 {step['value']!r}")
    claim = ast_to_poly(step.get("expr"), variable, index, "substitute")
    expected = poly_eval(prev_poly, value)
    if claim != expected:
        raise ProofError(index, "substitute", "代入结果与独立重算不一致")
    return step["expr"], claim


RULES = {
    "expand": _verify_expand,
    "combine": _verify_combine,
    "diff": _verify_diff,
    "substitute": _verify_substitute,
}


# ---------------------------------------------------------------- 证明验证

def verify_proof(proof: dict) -> Poly:
    """逐步独立验证整个证明，返回最后一个通过验证的表达式的多项式。

    任何一步失败都抛出 ProofError，其 step_index 即首个失败步骤。
    """
    variable = proof.get("variable", "x")
    steps = proof.get("steps")
    if not isinstance(steps, list) or not steps:
        raise ProofError(-1, "proof", "steps 必须是非空列表")

    prev_ast, prev_poly = None, None
    for index, step in enumerate(steps):
        rule = step.get("rule")
        if rule not in ("init",) and rule not in RULES:
            raise ProofError(index, str(rule), f"未知规则 {rule!r}，拒绝")
        _check_domain(step, index, rule)
        if rule == "init":
            if index != 0:
                raise ProofError(index, "init", "init 只能是第一步")
            prev_poly = ast_to_poly(step.get("expr"), variable, index, "init")
            prev_ast = step["expr"]
        else:
            if prev_ast is None:
                raise ProofError(index, rule, "缺少 init 步骤")
            prev_ast, prev_poly = RULES[rule](prev_ast, prev_poly, step, index, variable)
    return prev_poly  # 最终结果 == 最后验证表达式的多项式


# ---------------------------------------------------------------- 演示

def _demo() -> None:
    proof = {
        "variable": "x",
        "steps": [
            {"rule": "init",
             "expr": {"mul": [{"add": [{"var": "x"}, {"num": "1"}]},
                              {"add": [{"var": "x"}, {"num": "2"}]}]},
             "domain": REQUIRED_DOMAIN["init"]},
            {"rule": "expand",
             "expr": {"add": [{"pow": [{"var": "x"}, 2]},
                              {"mul": [{"num": "2"}, {"var": "x"}]},
                              {"var": "x"},
                              {"num": "2"}]},
             "domain": REQUIRED_DOMAIN["expand"]},
            {"rule": "combine",
             "expr": {"add": [{"pow": [{"var": "x"}, 2]},
                              {"mul": [{"num": "3"}, {"var": "x"}]},
                              {"num": "2"}]},
             "domain": REQUIRED_DOMAIN["combine"]},
            {"rule": "diff",
             "expr": {"add": [{"mul": [{"num": "2"}, {"var": "x"}]},
                              {"num": "3"}]},
             "domain": REQUIRED_DOMAIN["diff"]},
            {"rule": "substitute", "value": "1/2",
             "expr": {"num": "4"},
             "domain": REQUIRED_DOMAIN["substitute"]},
        ],
    }

    final = verify_proof(proof)
    print("合法证明验证通过，最终结果 =", dict(final))

    import copy
    tampered = copy.deepcopy(proof)
    tampered["steps"][2]["expr"]["add"][1]["mul"][0]["num"] = "4"  # 3x 改成 4x
    try:
        verify_proof(tampered)
    except ProofError as err:
        print(f"篡改检测：首个失败步骤 = step {err.step_index} -> {err}")


if __name__ == "__main__":
    _demo()
