"""DAG 符号求导器：加、乘、幂与限定初等函数（sin/cos/tan/exp/ln）。

设计要点
--------
- 哈希一致化（hash-consing）：结构相同的子表达式全局只保留一个节点，
  表达式天然构成 DAG，共享子表达式自动共享，无需手动处理。
- 求导按节点记忆化：共享子图只求导一次；幂规则产生 u^(n-1)，
  不会把 u^n 展开成 u*u*...*u，共享结构得以保留。
- 有理数用 fractions.Fraction 精确表示，不调用任何符号代数库。
- 每一步求导 / 化简都记录来源规则与适用域。
- 化简只做不改变定义域的恒等变换；唯一的全局约定是 a^0 = 1
  （含 0^0 = 1 的形式约定），使用时明确标注。
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction


class Expr:
    """DAG 节点。只能通过 _node() 构造，结构相同即同一对象。"""

    __slots__ = ("op", "args", "value")

    def __init__(self, op, args=(), value=None):
        self.op = op            # 'const'|'var'|'add'|'mul'|'pow'|'sin'|'cos'|'tan'|'exp'|'log'
        self.args = args        # tuple[Expr, ...]
        self.value = value      # const -> Fraction；var -> str

    def __repr__(self):
        return to_str(self)


_intern = {}


def _node(op, args=(), value=None):
    key = (op, args, value)
    node = _intern.get(key)
    if node is None:
        node = Expr(op, args, value)
        _intern[key] = node
    return node


def Const(v):
    return _node("const", (), Fraction(v))


def Var(name):
    return _node("var", (), name)


def Add(u, v): return _node("add", (u, v))
def Mul(u, v): return _node("mul", (u, v))
def Pow(u, v): return _node("pow", (u, v))
def Sin(u): return _node("sin", (u,))
def Cos(u): return _node("cos", (u,))
def Tan(u): return _node("tan", (u,))
def Exp(u): return _node("exp", (u,))
def Log(u): return _node("log", (u,))


ZERO = Const(0)
ONE = Const(1)
TWO = Const(2)
NEG_ONE = Const(-1)


def count_nodes(node):
    """统计 DAG 中不同节点的个数（共享子图只计一次）。"""
    seen = set()
    stack = [node]
    while stack:
        n = stack.pop()
        if id(n) in seen:
            continue
        seen.add(id(n))
        stack.extend(n.args)
    return len(seen)


def is_entire(node):
    """判断表达式是否在全定义域（R）上有定义。"""
    op = node.op
    if op in ("const", "var"):
        return True
    if op in ("add", "mul"):
        return all(is_entire(a) for a in node.args)
    if op in ("sin", "cos", "exp"):
        return is_entire(node.args[0])
    if op == "pow":
        u, v = node.args
        return (v.op == "const" and v.value.denominator == 1
                and v.value >= 0 and is_entire(u))
    return False  # tan / log / 一般幂


# ---------------------------------------------------------------- 求导

@dataclass
class RuleApp:
    rule: str     # 来源规则
    domain: str   # 适用域
    expr: Expr    # 被求导的子式
    result: Expr  # 求导结果（未化简）


class DiffEngine:
    """对固定自变量求导；按节点记忆化，共享子图只求导一次。"""

    def __init__(self, var="x"):
        self.var = var
        self.memo = {}
        self.trace = []
        self.compute_count = 0  # 实际执行求导的不同节点数

    def derive(self, node):
        cached = self.memo.get(node)
        if cached is not None:
            return cached
        self.compute_count += 1
        op = node.op
        if op == "const":
            result, rule, domain = ZERO, "常数规则 c' = 0", "恒成立"
        elif op == "var":
            if node.value == self.var:
                result, rule = ONE, "自变量规则 x' = 1"
            else:
                result, rule = ZERO, "非求导变量视为常数"
            domain = "恒成立"
        elif op == "add":
            du, dv = (self.derive(a) for a in node.args)
            result = Add(du, dv)
            rule = "和规则 (u+v)' = u'+v'"
            domain = "在 u、v 定义域的交集上成立"
        elif op == "mul":
            u, v = node.args
            du, dv = self.derive(u), self.derive(v)
            result = Add(Mul(du, v), Mul(u, dv))
            rule = "积规则 (u·v)' = u'·v+u·v'"
            domain = "在 u、v 定义域的交集上成立"
        elif op == "pow":
            result, rule, domain = self._derive_pow(node)
        elif op in ("sin", "cos", "tan", "exp", "log"):
            result, rule, domain = self._derive_func(node)
        else:
            raise ValueError(f"未知算子: {op}")
        self.memo[node] = result
        self.trace.append(RuleApp(rule, domain, node, result))
        return result

    def _derive_pow(self, node):
        u, v = node.args
        if v.op == "const":
            n = v.value
            if n == 0:
                return (ZERO,
                        "常数幂 0：约定 u^0 = 1（含 0^0 = 1），常数导数为 0",
                        "约定 a^0=1（含 0^0=1 的形式约定），在 u 的定义域上成立")
            du = self.derive(u)
            if n == 1:
                return du, "幂规则（指数 1）：(u^1)' = u'", "恒成立"
            result = Mul(Mul(Const(n), Pow(u, Const(n - 1))), du)
            rule = f"幂规则 (u^n)' = n·u^(n-1)·u'，n={_fmt_const(n)}"
            if n.denominator != 1:
                domain = "要求 u>0（非整数次幂）"
            elif n < 0:
                domain = "要求 u≠0（负整数次幂）"
            else:
                domain = "恒成立"
            return result, rule, domain
        du, dv = self.derive(u), self.derive(v)
        result = Mul(node, Add(Mul(dv, Log(u)),
                               Mul(v, Mul(du, Pow(u, NEG_ONE)))))
        return (result,
                "广义幂规则（对数求导法）(u^v)' = u^v·(v'·ln u + v·u'/u)",
                "要求 u>0")

    def _derive_func(self, node):
        u = node.args[0]
        du = self.derive(u)
        op = node.op
        if op == "sin":
            return Mul(Cos(u), du), "链式规则 (sin u)' = cos u·u'", "恒成立"
        if op == "cos":
            return Mul(Mul(NEG_ONE, Sin(u)), du), "链式规则 (cos u)' = -sin u·u'", "恒成立"
        if op == "tan":
            return (Mul(Add(ONE, Pow(node, TWO)), du),
                    "链式规则 (tan u)' = (1+tan²u)·u'",
                    "要求 cos u ≠ 0")
        if op == "exp":
            return Mul(node, du), "链式规则 (e^u)' = e^u·u'", "恒成立"
        # op == "log"
        return (Mul(du, Pow(u, NEG_ONE)),
                "链式规则 (ln u)' = u'/u",
                "要求 u>0（保留 ln 的定义域）")


# ---------------------------------------------------------------- 化简

@dataclass
class SimplifyStep:
    rule: str
    domain: str
    before: Expr
    after: Expr


_DOMAIN_OK = "恒等变换，定义域不变"


class Simplifier:
    """只做不改变定义域的恒等变换；共享子图只化简一次。"""

    def __init__(self):
        self.steps = []
        self.memo = {}

    def simplify(self, node):
        cached = self.memo.get(node)
        if cached is not None:
            return cached
        args = tuple(self.simplify(a) for a in node.args)
        cur = node if args == node.args else _node(node.op, args, node.value)
        cur = self._fixpoint(cur)
        self.memo[node] = cur
        return cur

    def _fixpoint(self, node):
        while True:
            nxt = self._rewrite_once(node)
            if nxt is node:
                return node
            node = nxt

    def _emit(self, before, after, rule, domain):
        self.steps.append(SimplifyStep(rule, domain, before, after))
        return after

    def _rewrite_once(self, node):
        op = node.op
        if op == "add":
            a, b = node.args
            if a.op == "const" and b.op == "const":
                return self._emit(node, Const(a.value + b.value),
                                  "有理数精确相加", _DOMAIN_OK)
            if a.op == "const" and a.value == 0:
                return self._emit(node, b, "加法单位元 0+u=u", _DOMAIN_OK)
            if b.op == "const" and b.value == 0:
                return self._emit(node, a, "加法单位元 u+0=u", _DOMAIN_OK)
            if a is b:
                return self._emit(node, Mul(TWO, a),
                                  "合并同类项 u+u=2u", _DOMAIN_OK)
        elif op == "mul":
            a, b = node.args
            if a.op == "const" and b.op == "const":
                return self._emit(node, Const(a.value * b.value),
                                  "有理数精确相乘", _DOMAIN_OK)
            if a.op == "const" and a.value == 0 and is_entire(b):
                return self._emit(node, ZERO, "零因子规则 0·u=0",
                                  "u 在全定义域有定义，定义域不变")
            if b.op == "const" and b.value == 0 and is_entire(a):
                return self._emit(node, ZERO, "零因子规则 u·0=0",
                                  "u 在全定义域有定义，定义域不变")
            if a.op == "const" and a.value == 1:
                return self._emit(node, b, "乘法单位元 1·u=u", _DOMAIN_OK)
            if b.op == "const" and b.value == 1:
                return self._emit(node, a, "乘法单位元 u·1=u", _DOMAIN_OK)
            if (a.op == "const" and b.op == "mul"
                    and b.args[0].op == "const"):
                coef = a.value * b.args[0].value
                return self._emit(node, Mul(Const(coef), b.args[1]),
                                  "系数合并（乘法结合律）", _DOMAIN_OK)
            if b.op == "const" and a.op != "const":
                return self._emit(node, Mul(b, a),
                                  "乘法交换律（系数前置）", _DOMAIN_OK)
            if a is b:
                return self._emit(node, Pow(a, TWO),
                                  "u·u=u²（保持共享，不展开）", _DOMAIN_OK)
        elif op == "pow":
            u, v = node.args
            if v.op == "const" and v.value == 0 and is_entire(u):
                return self._emit(
                    node, ONE, "零次幂 u^0=1",
                    "约定 a^0=1（含 0^0=1 的形式约定），u 全定义域有定义，定义域不变")
            if v.op == "const" and v.value == 1:
                return self._emit(node, u, "一次幂 u^1=u", _DOMAIN_OK)
            if (u.op == "const" and v.op == "const"
                    and v.value.denominator == 1
                    and not (u.value == 0 and v.value < 0)):
                return self._emit(node, Const(u.value ** v.value),
                                  "有理数精确求幂", _DOMAIN_OK)
        return node


# ---------------------------------------------------------------- 打印

_FUNC_NAME = {"sin": "sin", "cos": "cos", "tan": "tan",
              "exp": "exp", "log": "ln"}


def _fmt_const(v):
    return str(v.numerator) if v.denominator == 1 else f"{v.numerator}/{v.denominator}"


def _prec(node):
    if node.op == "add":
        return 1
    if node.op == "mul":
        return 2
    if node.op == "pow":
        return 3
    if node.op == "const" and node.value < 0:
        return 1
    return 4


def _par(node, parent_prec):
    s = to_str(node)
    return f"({s})" if _prec(node) < parent_prec else s


def to_str(node):
    op = node.op
    if op == "const":
        return _fmt_const(node.value)
    if op == "var":
        return node.value
    if op == "add":
        a, b = node.args
        return f"{_par(a, 1)} + {_par(b, 1)}"
    if op == "mul":
        a, b = node.args
        if a.op == "const" and a.value == -1:
            return f"-{_par(b, 2)}"
        return f"{_par(a, 2)} * {_par(b, 2)}"
    if op == "pow":
        a, b = node.args
        exp = to_str(b)
        if _prec(b) < 4 or (b.op == "const" and b.value.denominator != 1):
            exp = f"({exp})"
        return f"{_par(a, 3)}^{exp}"
    return f"{_FUNC_NAME[op]}({_par(node.args[0], 0)})"
