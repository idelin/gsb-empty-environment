"""Reduced ordered binary decision diagrams (ROBDDs) with negation normal form.

Boolean conditions over a finite set of named feature variables are represented
canonically, so structurally identical conditions share one node and pairwise
combination is memoized.  The variable ordering follows feature declaration
order; it never changes, which keeps representations shared across incremental
updates.

Only Python 3.11 standard library is used.
"""

from __future__ import annotations

from typing import Dict, Iterable, List, Optional, Sequence, Tuple

TRUE = 1
FALSE = 0


class ROBDD:
    def __init__(self, variables: Optional[Sequence[str]] = None):
        self.var_order: List[str] = list(variables or [])
        self.var_index: Dict[str, int] = {v: i for i, v in enumerate(self.var_order)}
        # node id -> (var index, low node, high node); 0/1 are terminals.
        self.nodes: List[Tuple[int, int, int]] = [(-1, 0, 0), (-1, 1, 1)]
        self.unique: Dict[Tuple[int, int, int], int] = {}
        self._ite_memo: Dict[Tuple[int, int, int], int] = {}
        self._not_memo: Dict[int, int] = {TRUE: FALSE, FALSE: TRUE}
        self._and_memo: Dict[Tuple[int, int], int] = {}
        self._or_memo: Dict[Tuple[int, int], int] = {}

    # -- variables ---------------------------------------------------------
    def declare(self, name: str) -> None:
        if name in self.var_index:
            return
        self.var_index[name] = len(self.var_order)
        self.var_order.append(name)

    def ensure_variables(self, names: Iterable[str]) -> None:
        for n in names:
            self.declare(n)

    # -- construction ------------------------------------------------------
    def mk(self, var_index: int, low: int, high: int) -> int:
        if low == high:
            return low
        key = (var_index, low, high)
        nid = self.unique.get(key)
        if nid is not None:
            return nid
        nid = len(self.nodes)
        self.nodes.append((var_index, low, high))
        self.unique[key] = nid
        return nid

    def lit(self, name: str) -> int:
        if name not in self.var_index:
            raise KeyError(f"undeclared variable: {name}")
        vi = self.var_index[name]
        return self.mk(vi, FALSE, TRUE)

    # -- operations --------------------------------------------------------
    def not_(self, f: int) -> int:
        cached = self._not_memo.get(f)
        if cached is not None:
            return cached
        _, low, high = self.nodes[f]
        r = self.mk(self.nodes[f][0], self.not_(low), self.not_(high))
        self._not_memo[f] = r
        self._not_memo[r] = f
        return r

    def and_(self, f: int, g: int) -> int:
        if f == FALSE or g == FALSE:
            return FALSE
        if f == TRUE:
            return g
        if g == TRUE:
            return f
        key = (f, g) if f <= g else (g, f)
        cached = self._and_memo.get(key)
        if cached is not None:
            return cached
        r = self.ite(f, g, FALSE)
        self._and_memo[key] = r
        return r

    def or_(self, f: int, g: int) -> int:
        if f == TRUE or g == TRUE:
            return TRUE
        if f == FALSE:
            return g
        if g == FALSE:
            return f
        key = (f, g) if f <= g else (g, f)
        cached = self._or_memo.get(key)
        if cached is not None:
            return cached
        r = self.ite(f, TRUE, g)
        self._or_memo[key] = r
        return r

    def ite(self, a: int, b: int, c: int) -> int:
        if a == TRUE:
            return b
        if a == FALSE:
            return c
        if b == TRUE and c == FALSE:
            return a
        if b == c:
            return b
        key = (a, b, c)
        cached = self._ite_memo.get(key)
        if cached is not None:
            return cached
        va = self.nodes[a][0] if a > 1 else 1 << 30
        vb = self.nodes[b][0] if b > 1 else 1 << 30
        vc = self.nodes[c][0] if c > 1 else 1 << 30
        top = min(va, vb, vc)

        def cof(node: int, val: int) -> int:
            if node <= 1:
                return node
            v, low, high = self.nodes[node]
            if v > top:
                return node
            return high if val else low

        hi = self.ite(cof(a, 1), cof(b, 1), cof(c, 1))
        lo = self.ite(cof(a, 0), cof(b, 0), cof(c, 0))
        r = self.mk(top, lo, hi)
        self._ite_memo[key] = r
        return r

    def restrict(self, f: int, var: str, value: bool) -> int:
        """Boolean quantification: f with one variable fixed (BDD substitution)."""
        vi = self.var_index[var]
        memo: Dict[int, int] = {TRUE: TRUE, FALSE: FALSE}

        def rec(n: int) -> int:
            if n in memo:
                return memo[n]
            v, lo, hi = self.nodes[n]
            if v < vi:
                r = self.mk(v, rec(lo), rec(hi))
            elif v == vi:
                r = rec(hi if value else lo)
            else:
                r = n
            memo[n] = r
            return r

        return rec(f)

    def satisfy(self, f: int) -> Optional[Dict[str, bool]]:
        """Return one satisfying assignment (only decided variables included),
        or None when f is unsatisfiable."""
        if f == FALSE:
            return None
        model: Dict[str, bool] = {}
        node = f
        while node > 1:
            vi, lo, hi = self.nodes[node]
            if lo != FALSE:
                model[self.var_order[vi]] = False
                node = lo
            else:
                model[self.var_order[vi]] = True
                node = hi
        return model

    def holds(self, f: int, assignment: Dict[str, bool]) -> bool:
        node = f
        while node > 1:
            vi, lo, hi = self.nodes[node]
            name = self.var_order[vi]
            if name not in assignment:
                # undecided variables default to disabled
                node = lo
            else:
                node = hi if assignment[name] else lo
        return node == TRUE

    def models(self, f: int):
        """Enumerate every complete satisfying assignment (feature -> bool).

        Intended for small graphs (tests / oracle cross-checks).
        """
        if f == FALSE:
            return

        def rec(n: int, next_vi: int, model: Dict[str, bool]):
            if n == FALSE:
                return
            if n == TRUE:
                for vi in range(next_vi, len(self.var_order)):
                    model[self.var_order[vi]] = False
                yield dict(model)
                for vi in range(next_vi, len(self.var_order)):
                    del model[self.var_order[vi]]
                return
            v, lo, hi = self.nodes[n]
            for vi in range(next_vi, v):
                model[self.var_order[vi]] = False
            model[self.var_order[v]] = False
            yield from rec(lo, v + 1, model)
            model[self.var_order[v]] = True
            yield from rec(hi, v + 1, model)
            del model[self.var_order[v]]
            for vi in range(next_vi, v):
                del model[self.var_order[vi]]

        yield from rec(f, 0, {})

    def size(self) -> int:
        """Number of nonterminal BDD nodes ever interned in this manager."""
        return len(self.nodes) - 2



class Expr:
    """Parser/builder for tiny boolean expressions:
    feature names, integers 0/1, "!", "&", "|", "->", parentheses.
    """

    def __init__(self, bdd: ROBDD):
        self.bdd = bdd
        self._cache: Dict[str, int] = {"1": TRUE, "0": FALSE, "true": TRUE, "false": FALSE}

    def parse(self, text: str) -> int:
        text = (text or "1").strip()
        if text in self._cache:
            return self._cache[text]
        tokens = self._tokenize(text)
        parser = _Parser(tokens, self)
        node = parser.parse_expr()
        if parser.pos != len(tokens):
            raise ValueError(f"trailing tokens in condition: {text!r}")
        if len(tokens) <= 8:
            self._cache[text] = node
        return node

    @staticmethod
    def _tokenize(text: str) -> List[str]:
        out: List[str] = []
        i = 0
        while i < len(text):
            ch = text[i]
            if ch.isspace():
                i += 1
            elif ch in "!()&|":
                out.append(ch)
                i += 1
            elif ch == "-" and i + 1 < len(text) and text[i + 1] == ">":
                out.append("->")
                i += 2
            else:
                j = i
                while j < len(text) and (text[j].isalnum() or text[j] in "_.-"):
                    j += 1
                if j == i:
                    raise ValueError(f"unexpected character {ch!r} in {text!r}")
                out.append(text[i:j])
                i = j
        return out

    def var(self, name: str) -> int:
        if name not in self.bdd.var_index:
            raise KeyError(f"undeclared feature: {name}")
        return self.bdd.lit(name)


class _Parser:
    def __init__(self, tokens: List[str], expr: Expr):
        self.tokens = tokens
        self.pos = 0
        self.expr = expr
        self.bdd = expr.bdd

    def peek(self) -> Optional[str]:
        return self.tokens[self.pos] if self.pos < len(self.tokens) else None

    def next(self) -> str:
        t = self.tokens[self.pos]
        self.pos += 1
        return t

    def parse_expr(self) -> int:
        return self.parse_or()

    def parse_or(self) -> int:
        node = self.parse_and()
        while self.peek() == "|":
            self.next()
            node = self.bdd.or_(node, self.parse_and())
        return node

    def parse_and(self) -> int:
        node = self.parse_impl()
        while self.peek() == "&":
            self.next()
            node = self.bdd.and_(node, self.parse_impl())
        return node

    def parse_impl(self) -> int:
        left = self.parse_unary()
        if self.peek() == "->":
            self.next()
            right = self.parse_impl()
            return self.bdd.or_(self.bdd.not_(left), right)
        return left

    def parse_unary(self) -> int:
        if self.peek() == "!":
            self.next()
            return self.bdd.not_(self.parse_unary())
        return self.parse_atom()

    def parse_atom(self) -> int:
        t = self.peek()
        if t is None:
            raise ValueError("unexpected end of condition")
        if t == "(":
            self.next()
            node = self.parse_expr()
            if self.next() != ")":
                raise ValueError("missing closing parenthesis")
            return node
        self.next()
        if t in ("1", "true", "True"):
            return TRUE
        if t in ("0", "false", "False"):
            return FALSE
        return self.expr.var(t)
