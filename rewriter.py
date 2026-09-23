"""基于模式的符号表达式重写系统。

特性:
- 模式匹配重写, 规则带优先级与条件(条件不满足则规则不可用)
- 有理数精确计算(fractions.Fraction), 不依赖符号代数库
- 检测重复规范状态(循环, 如 a->b, b->a)与步数耗尽
- 输出完整变换链, 每步记录重写位置与适用域说明
- 局部重写位置确定(前序最左最外), 变换链可回放
"""
from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum
from fractions import Fraction
from typing import Callable, Optional, Union


# ---------------------------------------------------------------------------
# 表达式
# ---------------------------------------------------------------------------

class Expr:
    """表达式基类。"""


@dataclass(frozen=True)
class Symbol(Expr):
    name: str


@dataclass(frozen=True)
class Number(Expr):
    value: Fraction

    def __init__(self, value):
        object.__setattr__(self, "value", Fraction(value))


@dataclass(frozen=True)
class Apply(Expr):
    op: str
    args: tuple


def app(op: str, *args: Expr) -> Apply:
    return Apply(op, tuple(args))


# ---------------------------------------------------------------------------
# 模式
# ---------------------------------------------------------------------------

class Pat:
    """模式基类。"""


@dataclass(frozen=True)
class PVar(Pat):
    """匹配任意表达式, 绑定表达式本身。"""
    name: str


@dataclass(frozen=True)
class PNum(Pat):
    """仅匹配 Number, 绑定其 Fraction 值。"""
    name: str


@dataclass(frozen=True)
class PLit(Pat):
    """精确匹配一个字面表达式。"""
    expr: Expr


@dataclass(frozen=True)
class PApply(Pat):
    op: str
    args: tuple


def papp(op: str, *args: Pat) -> PApply:
    return PApply(op, tuple(args))


def _bind(name: str, value, bind: dict) -> Optional[dict]:
    if name in bind:
        return bind if bind[name] == value else None
    bind[name] = value
    return bind


def match(pat: Pat, expr: Expr, bind: Optional[dict] = None) -> Optional[dict]:
    """模式匹配, 成功返回绑定字典, 失败返回 None。同名变量须一致。"""
    bind = {} if bind is None else dict(bind)
    if isinstance(pat, PVar):
        return _bind(pat.name, expr, bind)
    if isinstance(pat, PNum):
        if isinstance(expr, Number):
            return _bind(pat.name, expr.value, bind)
        return None
    if isinstance(pat, PLit):
        return bind if pat.expr == expr else None
    if isinstance(pat, PApply):
        if (isinstance(expr, Apply) and expr.op == pat.op
                and len(expr.args) == len(pat.args)):
            for sub_pat, sub_expr in zip(pat.args, expr.args):
                bind = match(sub_pat, sub_expr, bind)
                if bind is None:
                    return None
            return bind
        return None
    raise TypeError(f"未知模式: {pat!r}")


def instantiate(template: Pat, bind: dict) -> Expr:
    """用绑定实例化右侧模板。"""
    if isinstance(template, PVar):
        return bind[template.name]
    if isinstance(template, PNum):
        return Number(bind[template.name])
    if isinstance(template, PLit):
        return template.expr
    if isinstance(template, PApply):
        return Apply(template.op,
                     tuple(instantiate(a, bind) for a in template.args))
    raise TypeError(f"未知模板: {template!r}")


# ---------------------------------------------------------------------------
# 规则
# ---------------------------------------------------------------------------

Rhs = Union[Pat, Callable[[dict], Expr]]
Cond = Optional[Callable[[dict], bool]]


@dataclass(frozen=True)
class Rule:
    """重写规则。

    priority: 数值大者优先; 同优先级按名称字典序, 保证确定性。
    condition: 作用于绑定字典; 不满足时该规则对本次匹配不可用。
    domain: 适用域的文字说明, 记录进变换链。
    """
    name: str
    lhs: Pat
    rhs: Rhs
    condition: Cond = None
    priority: int = 0
    domain: str = ""

    def apply(self, bind: dict) -> Expr:
        if callable(self.rhs):
            return self.rhs(bind)
        return instantiate(self.rhs, bind)


# ---------------------------------------------------------------------------
# 位置与子项替换
# ---------------------------------------------------------------------------

def preorder_positions(expr: Expr, path: tuple = ()):
    """前序(根优先, 自左向右)枚举所有位置, 保证确定性。"""
    yield path
    if isinstance(expr, Apply):
        for i, sub in enumerate(expr.args):
            yield from preorder_positions(sub, path + (i,))


def subexpr_at(expr: Expr, pos: tuple) -> Expr:
    for i in pos:
        expr = expr.args[i]
    return expr


def replace_at(expr: Expr, pos: tuple, new: Expr) -> Expr:
    if not pos:
        return new
    i, rest = pos[0], pos[1:]
    args = list(expr.args)
    args[i] = replace_at(args[i], rest, new)
    return Apply(expr.op, tuple(args))


# ---------------------------------------------------------------------------
# 重写引擎
# ---------------------------------------------------------------------------

class Status(Enum):
    NORMAL_FORM = "normal_form"            # 已化简到规范形
    CYCLE = "cycle_detected"               # 检测到重复状态(循环)
    EXHAUSTED = "max_steps_exhausted"      # 步数耗尽


@dataclass
class Step:
    index: int                 # 步号(从 1 开始)
    rule: str                  # 规则名
    position: tuple            # 重写位置(参数下标路径)
    before: Expr
    after: Expr
    domain: str                # 适用域说明
    bindings: dict = field(default_factory=dict)


@dataclass
class Result:
    initial: Expr
    final: Expr
    status: Status
    steps: list
    cycle_prefix: Optional[int] = None  # 循环时, 该状态首次出现的步索引(0 表示初始)


class Engine:
    def __init__(self, rules):
        # 优先级降序, 名称升序: 完全确定的选择顺序
        self.rules = sorted(rules, key=lambda r: (-r.priority, r.name))

    def find_redex(self, expr: Expr):
        """按确定顺序找第一个可应用规则, 返回 (position, rule, bindings) 或 None。"""
        for pos in preorder_positions(expr):
            sub = subexpr_at(expr, pos)
            for rule in self.rules:
                bind = match(rule.lhs, sub)
                if bind is None:
                    continue
                if rule.condition is not None and not rule.condition(bind):
                    continue  # 条件不满足, 规则不可用
                return pos, rule, bind
        return None

    def rewrite(self, expr: Expr, max_steps: int = 100) -> Result:
        seen = {expr: 0}
        steps: list = []
        current = expr
        while len(steps) < max_steps:
            found = self.find_redex(current)
            if found is None:
                return Result(expr, current, Status.NORMAL_FORM, steps)
            pos, rule, bind = found
            nxt = replace_at(current, pos, rule.apply(bind))
            steps.append(Step(len(steps) + 1, rule.name, pos,
                              current, nxt, rule.domain, bind))
            if nxt in seen:
                return Result(expr, nxt, Status.CYCLE, steps,
                              cycle_prefix=seen[nxt])
            seen[nxt] = len(steps)
            current = nxt
        return Result(expr, current, Status.EXHAUSTED, steps)

    def replay(self, initial: Expr, steps) -> tuple:
        """按 (规则名, 位置) 回放变换链, 返回 (最终表达式, 是否每步一致)。"""
        by_name = {r.name: r for r in self.rules}
        current = initial
        ok = True
        for step in steps:
            rule = by_name[step.rule]
            sub = subexpr_at(current, step.position)
            bind = match(rule.lhs, sub)
            if bind is None:
                return current, False
            if rule.condition is not None and not rule.condition(bind):
                return current, False
            current = replace_at(current, step.position, rule.apply(bind))
            if current != step.after:
                ok = False
        return current, ok


# ---------------------------------------------------------------------------
# 解析与打印
# ---------------------------------------------------------------------------

def parse(text: str) -> Expr:
    """解析 prefix 形式: add(1/2, x)、mul(2, add(x, 1)) 等。"""
    tokens = _tokenize(text)
    expr, pos = _parse_expr(tokens, 0)
    if pos != len(tokens):
        raise ValueError(f"多余输入: {text!r}")
    return expr


def _tokenize(text: str):
    tokens, i = [], 0
    while i < len(text):
        ch = text[i]
        if ch.isspace():
            i += 1
        elif ch in "(),":
            tokens.append(ch)
            i += 1
        elif ch.isdigit() or (ch == "-" and i + 1 < len(text)
                              and text[i + 1].isdigit()):
            j = i + 1
            while j < len(text) and (text[j].isdigit() or text[j] == "/"):
                j += 1
            tokens.append(text[i:j])
            i = j
        elif ch.isalpha() or ch == "_":
            j = i + 1
            while j < len(text) and (text[j].isalnum() or text[j] == "_"):
                j += 1
            tokens.append(text[i:j])
            i = j
        else:
            raise ValueError(f"非法字符: {ch!r}")
    return tokens


def _parse_expr(tokens, pos):
    tok = tokens[pos]
    if tok.lstrip("-")[0].isdigit():
        return Number(Fraction(tok)), pos + 1
    if pos + 1 < len(tokens) and tokens[pos + 1] == "(":
        args, pos = [], pos + 2
        if tokens[pos] != ")":
            while True:
                arg, pos = _parse_expr(tokens, pos)
                args.append(arg)
                if tokens[pos] == ",":
                    pos += 1
                else:
                    break
        if tokens[pos] != ")":
            raise ValueError("缺少 )")
        return Apply(tok, tuple(args)), pos + 1
    return Symbol(tok), pos + 1


_INFIX = {"add": "+", "mul": "*", "div": "/"}


def render(expr) -> str:
    if isinstance(expr, Number):
        return str(expr.value)
    if isinstance(expr, Symbol):
        return expr.name
    if isinstance(expr, Apply):
        if expr.op in _INFIX and len(expr.args) == 2:
            return f"({render(expr.args[0])} {_INFIX[expr.op]} {render(expr.args[1])})"
        if expr.op == "neg" and len(expr.args) == 1:
            return f"(-{render(expr.args[0])})"
        inner = ", ".join(render(a) for a in expr.args)
        return f"{expr.op}({inner})"
    if isinstance(expr, Fraction):
        return str(expr)
    raise TypeError(f"无法打印: {expr!r}")


def render_prefix(expr: Expr) -> str:
    """前缀(可解析)形式打印, 与 parse 互逆。"""
    if isinstance(expr, (Number, Symbol)):
        return render(expr)
    inner = ", ".join(render_prefix(a) for a in expr.args)
    return f"{expr.op}({inner})"


def render_step(step: Step) -> str:
    pos = "(" + ", ".join(map(str, step.position)) + ")" if step.position else "()"
    bind = ", ".join(f"{k}={render(v)}" for k, v in sorted(step.bindings.items()))
    lines = [
        f"步骤 {step.index} @ 位置 {pos}  规则 [{step.rule}]",
        f"  绑定: {bind}" if bind else "  绑定: (无)",
        f"  适用域: {step.domain or '(无条件)'}",
        f"  {render(step.before)}",
        f"  => {render(step.after)}",
    ]
    return "\n".join(lines)


_STATUS_TEXT = {
    Status.NORMAL_FORM: "达到规范形(无规则可用)",
    Status.CYCLE: "检测到循环(重复状态)",
    Status.EXHAUSTED: "步数耗尽",
}


def render_result(result: Result) -> str:
    lines = [f"初始: {render(result.initial)}"]
    for step in result.steps:
        lines.append(render_step(step))
    lines.append(f"终止状态: {_STATUS_TEXT[result.status]}")
    if result.status is Status.CYCLE:
        lines.append(f"  重复状态首次出现于步索引 {result.cycle_prefix}")
    lines.append(f"最终: {render(result.final)}  (共 {len(result.steps)} 步)")
    return "\n".join(lines)


# ---------------------------------------------------------------------------
# 标准算术规则(有理数精确计算)
# ---------------------------------------------------------------------------

def standard_rules():
    """常用化简规则: 常量折叠 + 单位元/零元, 全部精确有理数。"""
    return [
        Rule("add-num", papp("add", PNum("a"), PNum("b")),
             lambda b: Number(b["a"] + b["b"]),
             priority=100, domain="a, b ∈ ℚ"),
        Rule("mul-num", papp("mul", PNum("a"), PNum("b")),
             lambda b: Number(b["a"] * b["b"]),
             priority=100, domain="a, b ∈ ℚ"),
        Rule("div-num", papp("div", PNum("a"), PNum("b")),
             lambda b: Number(b["a"] / b["b"]),
             condition=lambda b: b["b"] != 0,
             priority=100, domain="a, b ∈ ℚ 且 b ≠ 0"),
        Rule("neg-num", papp("neg", PNum("a")),
             lambda b: Number(-b["a"]),
             priority=100, domain="a ∈ ℚ"),
        Rule("add-zero", papp("add", PLit(Number(0)), PVar("x")),
             PVar("x"), priority=50, domain="任意表达式 x"),
        Rule("add-zero-r", papp("add", PVar("x"), PLit(Number(0))),
             PVar("x"), priority=50, domain="任意表达式 x"),
        Rule("mul-one", papp("mul", PLit(Number(1)), PVar("x")),
             PVar("x"), priority=50, domain="任意表达式 x"),
        Rule("mul-one-r", papp("mul", PVar("x"), PLit(Number(1))),
             PVar("x"), priority=50, domain="任意表达式 x"),
        Rule("mul-zero", papp("mul", PLit(Number(0)), PVar("x")),
             PLit(Number(0)), priority=50, domain="任意表达式 x"),
        Rule("mul-zero-r", papp("mul", PVar("x"), PLit(Number(0))),
             PLit(Number(0)), priority=50, domain="任意表达式 x"),
        Rule("abs-nonneg", papp("abs", PNum("a")),
             lambda b: Number(b["a"]),
             condition=lambda b: b["a"] >= 0,
             priority=50, domain="a ∈ ℚ 且 a ≥ 0"),
        Rule("abs-neg", papp("abs", PNum("a")),
             lambda b: Number(-b["a"]),
             condition=lambda b: b["a"] < 0,
             priority=50, domain="a ∈ ℚ 且 a < 0"),
        Rule("div-cancel", papp("div", papp("mul", PVar("x"), PVar("y")),
                                PVar("y")),
             PVar("x"),
             condition=lambda b: isinstance(b["y"], Number)
             and b["y"].value != 0,
             priority=50, domain="y 为非零有理数"),
    ]
