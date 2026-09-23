"""展开验证证书：每一步提取独立核验，最终乘回必须严格等于原式。

每步变换都说明适用域：提公因式是多项式环 Q[vars] 内的恒等变换，
不做任何除以变量的操作，因此既不引入也不消除定义域限制。
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .dag import expand
from .poly import Polynomial, term_str

DOMAIN_NOTE = (
    "适用域：变量取任意有理数。本步为多项式环内的恒等变换（无除法），"
    "不引入也不消除定义域限制。"
)


@dataclass
class CertStep:
    index: int
    factor_coeff: Fraction
    factor_mono: tuple
    group: Polynomial     # 左端：被提取的子式
    quotient: Polynomial  # 商式
    verified: bool


@dataclass
class Certificate:
    original: Polynomial
    dag: tuple
    complete: bool
    zero_case: bool
    steps: list
    final_ok: bool

    def render(self) -> str:
        vars = self.original.vars
        lines = []
        if self.zero_case:
            lines.append("零多项式：特殊情形，按约定直接表示为 0，无需提取。")
            mark = "OK" if self.final_ok else "FAIL"
            lines.append(f"最终核验：展开 0 == 原式（严格相等）[{mark}]")
            return "\n".join(lines)
        for st in self.steps:
            factor_s = term_str(vars, st.factor_coeff, st.factor_mono)
            mark = "OK" if st.verified else "FAIL"
            lines.append(f"步骤 {st.index}：提取公因子 {factor_s}")
            lines.append(f"  {st.group} = ({factor_s}) * ({st.quotient})")
            ring = "Q[" + ", ".join(vars) + "]"
            lines.append(f"  依据：乘法对加法的分配律（提公因式），在 {ring} 上恒等")
            lines.append(f"  {DOMAIN_NOTE}")
            lines.append(f"  独立核验：展开 ({factor_s})*({st.quotient}) == 左端 [{mark}]")
        mark = "OK" if self.final_ok else "FAIL"
        lines.append(f"最终核验：DAG 独立展开 == 原式（严格相等）[{'OK' if self.final_ok else 'FAIL'}]")
        if not self.complete:
            lines.append("注意：搜索因预算中断，本证书只保证等价性，不保证因子 DAG 的全局最优性。")
        return "\n".join(lines)


def _term_poly(vars, coeff, mono) -> Polynomial:
    return Polynomial.build(vars, {tuple(mono): Fraction(coeff)})


def build_certificate(original: Polynomial, dag, complete: bool) -> Certificate:
    vars = original.vars
    if original.is_zero:
        return Certificate(original, dag, complete, True, [], expand(dag, vars) == original)
    steps = []
    seen = set()

    def visit(node):
        if node in seen:
            return
        seen.add(node)
        if node[0] in ("add", "mul"):
            for c in node[1]:
                visit(c)
        if node[0] == "mul":
            atoms = [c for c in node[1] if c[0] == "atom"]
            others = [c for c in node[1] if c[0] != "atom"]
            if atoms and others:
                coeff = Fraction(1)
                mono = [0] * len(vars)
                for a in atoms:
                    coeff *= a[1]
                    mono = [x + y for x, y in zip(mono, a[2])]
                mono = tuple(mono)
                group = expand(node, vars)
                quotient = Polynomial.one(vars)
                for o in others:
                    quotient = quotient.mul(expand(o, vars))
                ok = _term_poly(vars, coeff, mono).mul(quotient) == group
                steps.append(CertStep(len(steps) + 1, coeff, mono, group, quotient, ok))

    visit(dag)
    return Certificate(original, dag, complete, False, steps, expand(dag, vars) == original)
