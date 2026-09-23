"""Bounded exact search for a minimum-operation factor-extraction DAG.

Candidates for a polynomial P (finite set, no claim of complete
factorization):

  1. content extraction:  P = c * x^m * Q  with (c, m) the rational content,
     then recurse on the primitive part Q;
  2. binary term grouping: P = A + B over every bipartition of the terms,
     then recurse on A and B (recursion covers k-way groupings).

The cost of a candidate is the number of add/mul nodes of the hash-consed
DAG, so shared subexpressions are charged once.  The search minimizes cost
globally over all candidates (branch and bound with admissible lower
bounds); it never assumes a locally greedy choice is globally optimal.

Admissible lower bounds used for pruning and for truncated runs:
  * structural: an expression whose expansion has t terms needs at least
    ceil(log2(t)) operations (each op at most doubles the term count);
  * content candidate: cost >= lower_bound(Q) (multiplying by a monomial
    cannot reduce the operation count of the subexpression);
  * split candidate: cost >= max(lower_bound(A), lower_bound(B)).

With a finite budget the search is interruptible: it returns the best
expression found so far (upper bound) together with the admissible lower
bound, so a truncated run reports a certified interval [lower, upper].
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from math import ceil, inf, log2

from .dag import Dag
from .poly import Poly
from .certificate import build_certificate


@dataclass
class Entry:
    cost: float          # best (minimum) cost found; inf if none yet
    lower: float         # admissible lower bound over all candidates
    node: int | None     # DAG root of the best expression, if any
    exact: bool          # True iff cost is proven minimal over all candidates
    choice: tuple | None # decomposition chosen for the best expression


@dataclass
class FactorResult:
    poly: Poly
    search: "FactorSearch"
    entry: Entry
    steps: list
    verified: bool

    @property
    def exact(self):
        return self.entry.exact

    @property
    def cost(self):
        return self.entry.cost

    @property
    def lower(self):
        return self.entry.lower

    @property
    def expression(self):
        if self.entry.node is None:
            return None
        return self.search.dag.to_string(self.entry.node)

    def render(self):
        lines = []
        lines.append(f"输入多项式: {self.poly}")
        lines.append(f"变量顺序: {', '.join(self.search.variables) or '(无)'}")
        if self.poly.is_zero():
            lines.append("零多项式: 特殊处理, 表示为常数 0, 不做内容提取。")
        if self.entry.node is not None:
            lines.append(f"最优表达式: {self.expression}")
        status = "精确(候选全集已枚举)" if self.exact else "预算中断(结果为上界)"
        lines.append(f"搜索状态: {status}; 评估步数: {self.search.steps}")
        upper = self.entry.cost if self.entry.node is not None else "∞"
        lines.append(f"运算次数(共享子表达式计一次): 下界={self.entry.lower}, 上界={upper}")
        lines.append("验证证书:")
        for step in self.steps:
            mark = "✓" if step.verified else "✗"
            lines.append(f"  步骤 {step.index} [{step.rule}] {mark}")
            lines.append(f"    变换: {step.statement}")
            lines.append(f"    适用域: {step.domain}")
            lines.append(f"    核验: {step.check}")
        lines.append(f"最终核验(乘回展开严格等于原式): {'通过' if self.verified else '失败'}")
        return "\n".join(lines)


class FactorSearch:
    def __init__(self, variables, budget=None):
        self.variables = tuple(variables)
        self.dag = Dag()
        self.budget = inf if budget is None else budget
        self.memo = {}
        self.steps = 0

    @staticmethod
    def _structural_lb(nterms):
        if nterms <= 1:
            return 0
        return max(1, ceil(log2(nterms)))

    def factor(self, poly):
        entry = self._factor(poly)
        steps, verified = build_certificate(self, poly, entry)
        return FactorResult(poly, self, entry, steps, verified)

    # -- core search -------------------------------------------------------
    def _factor(self, poly):
        key = poly.key()
        hit = self.memo.get(key)
        if hit is not None:
            return hit
        items = sorted(poly.terms.items())
        if not items:
            node = self.dag.const(Fraction(0))
            entry = Entry(0, 0, node, True, ("zero",))
            self.memo[key] = entry
            return entry
        if len(items) == 1:
            (mono, coeff), = items
            node = self.dag.term_node(coeff, mono, self.variables)
            cost = self.dag.cost(node)
            entry = Entry(cost, cost, node, True, ("term",))
            self.memo[key] = entry
            return entry
        if self.steps >= self.budget:
            return Entry(inf, self._structural_lb(len(items)), None, False, None)
        self.steps += 1

        best_node, best_cost, best_choice = None, inf, None
        cand_lowers = []
        all_exact = True

        def consider(node, cost, lower, exact, choice):
            nonlocal best_node, best_cost, best_choice, all_exact
            cand_lowers.append(lower)
            if not exact:
                all_exact = False
            if cost < best_cost:
                best_node, best_cost, best_choice = node, cost, choice

        struct_lb = self._structural_lb(len(items))

        # Candidate 1: extract the full rational content c * x^m.
        c, m = poly.content()
        if c != 1 or any(m):
            q = poly.divide_by_content(c, m)
            known = self.memo.get(q.key())
            quick_lb = known.lower if known else self._structural_lb(len(q.terms))
            quick_lb = max(quick_lb, struct_lb)
            if quick_lb >= best_cost:
                cand_lowers.append(quick_lb)
            else:
                child = self._factor(q)
                if child.node is not None:
                    cm_node = self.dag.term_node(c, m, self.variables)
                    node = self.dag.mul(cm_node, child.node)
                    cost = self.dag.cost(node)
                    consider(node, cost, cost, child.exact, ("content", c, m, q.key()))
                else:
                    consider(None, inf, max(child.lower, struct_lb), False, None)

        # Candidate 2: every binary partition of the term set.
        rest = items[1:]
        n = len(rest)
        for mask in range((1 << n) - 1):
            if self.steps >= self.budget:
                cand_lowers.append(struct_lb)
                all_exact = False
                break
            self.steps += 1
            a_terms = dict([items[0]] + [rest[i] for i in range(n) if mask >> i & 1])
            b_terms = dict([rest[i] for i in range(n) if not (mask >> i & 1)])
            pa = Poly(self.variables, a_terms)
            pb = Poly(self.variables, b_terms)
            known_a = self.memo.get(pa.key())
            known_b = self.memo.get(pb.key())
            quick_a = known_a.lower if known_a else self._structural_lb(len(a_terms))
            quick_b = known_b.lower if known_b else self._structural_lb(len(b_terms))
            quick = max(quick_a, quick_b, struct_lb)
            if quick >= best_cost:
                cand_lowers.append(quick)
                continue
            ea = self._factor(pa)
            eb = self._factor(pb)
            if ea.node is not None and eb.node is not None:
                node = self.dag.add(ea.node, eb.node)
                cost = self.dag.cost(node)
                consider(node, cost, cost, ea.exact and eb.exact,
                         ("split", pa.key(), pb.key()))
            else:
                consider(None, inf, max(ea.lower, eb.lower, struct_lb), False, None)

        lower = min(cand_lowers) if cand_lowers else struct_lb
        exact = all_exact and best_node is not None
        if exact:
            lower = best_cost
        entry = Entry(best_cost, lower, best_node, exact, best_choice)
        self.memo[key] = entry
        return entry


def factor_polynomial(poly, budget=None):
    """Factor-extraction search on a Poly (or a string to parse)."""
    from .poly import parse
    if isinstance(poly, str):
        poly = parse(poly)
    search = FactorSearch(poly.variables, budget=budget)
    return search.factor(poly)
