"""Decision-tree compiler (Maranget-style matrix compilation).

Produces a decision tree plus diagnostics:
  * unreachable cases (a case whose row never survives to a leaf),
  * non-exhaustive matches with a counterexample witness pattern,
  * validation errors (unknown constructor, arity mismatch, duplicate
    bindings, unbound variables, unknown functions).

Guards never take part in the exhaustiveness proof: a guarded row falls
through to the remaining rows, and if those rows do not cover the value
space the match is reported non-exhaustive.
"""
from __future__ import annotations

from dataclasses import dataclass

from .nodes import (Diag, EBin, ECall, ECtor, ELit, ETuple, EUn, EVar,
                    PCtor, PLit, PTuple, PVar, PWild, lit_key)

BUILTINS = {"len": 1}


# ---------------------------------------------------------------------------
# decision tree nodes
# ---------------------------------------------------------------------------

@dataclass
class Leaf:
    case_idx: int
    bindings: list          # [(name, path), ...]


@dataclass
class Guard:
    case_idx: int
    guard: object           # guard expression
    bindings: list          # [(name, path), ...]
    otherwise: object       # subtree taken when the guard fails


@dataclass
class Switch:
    occ: tuple              # occurrence: path into the subject value
    arms: list              # [(key, subtree), ...]
    default: object         # subtree or None (None => signature complete)
    default_example: object # example key missing from arms (for witness)


@dataclass
class Fail:
    pass


@dataclass
class _Row:
    pats: list
    guard: object
    case_idx: int
    bindings: list


class _Ctx:
    def __init__(self, unions):
        self.unions = unions
        self.union_tags = {u: [t for t, _ in cs] for u, cs in unions.items()}
        self.taginfo = {}   # tag -> (union name, arity)
        for u, cs in unions.items():
            for tag, arity in cs:
                self.taginfo[tag] = (u, arity)


# ---------------------------------------------------------------------------
# validation
# ---------------------------------------------------------------------------

def _validate_pattern(p, ctx, diags, names):
    if isinstance(p, (PWild, PLit)):
        return
    if isinstance(p, PVar):
        if p.name in names:
            diags.append(Diag("error",
                              f"duplicate binding {p.name!r} in pattern",
                              p.span))
        else:
            names[p.name] = p.span
        return
    if isinstance(p, PCtor):
        info = ctx.taginfo.get(p.tag)
        if info is None:
            diags.append(Diag("error", f"unknown constructor {p.tag!r}",
                              p.span))
            return
        want = info[1]
        if len(p.args) != want:
            diags.append(Diag(
                "error",
                f"constructor {p.tag!r} takes {want} field(s), "
                f"got {len(p.args)}", p.span))
            return
        for a in p.args:
            _validate_pattern(a, ctx, diags, names)
        return
    if isinstance(p, PTuple):
        for it in p.items:
            _validate_pattern(it, ctx, diags, names)
        return
    raise AssertionError(f"unknown pattern node {p!r}")


def _validate_expr(e, ctx, diags, bound):
    if isinstance(e, ELit):
        return
    if isinstance(e, EVar):
        if e.name not in bound:
            diags.append(Diag("error", f"unbound variable {e.name!r}", e.span))
        return
    if isinstance(e, ECtor):
        info = ctx.taginfo.get(e.tag)
        if info is None:
            diags.append(Diag("error", f"unknown constructor {e.tag!r}",
                              e.span))
            return
        if len(e.args) != info[1]:
            diags.append(Diag(
                "error",
                f"constructor {e.tag!r} takes {info[1]} field(s), "
                f"got {len(e.args)}", e.span))
            return
        for a in e.args:
            _validate_expr(a, ctx, diags, bound)
        return
    if isinstance(e, ECall):
        if e.name not in BUILTINS:
            diags.append(Diag("error", f"unknown function {e.name!r}", e.span))
            return
        if len(e.args) != BUILTINS[e.name]:
            diags.append(Diag(
                "error",
                f"function {e.name!r} takes {BUILTINS[e.name]} argument(s), "
                f"got {len(e.args)}", e.span))
            return
        for a in e.args:
            _validate_expr(a, ctx, diags, bound)
        return
    if isinstance(e, ETuple):
        for it in e.items:
            _validate_expr(it, ctx, diags, bound)
        return
    if isinstance(e, EBin):
        _validate_expr(e.left, ctx, diags, bound)
        _validate_expr(e.right, ctx, diags, bound)
        return
    if isinstance(e, EUn):
        _validate_expr(e.operand, ctx, diags, bound)
        return
    raise AssertionError(f"unknown expr node {e!r}")


# ---------------------------------------------------------------------------
# keys and signatures
# ---------------------------------------------------------------------------

def _pat_key(p):
    if isinstance(p, PCtor):
        return ("ctor", p.tag)
    if isinstance(p, PTuple):
        return ("tuple", len(p.items))
    if isinstance(p, PLit):
        return lit_key(p.value)
    raise AssertionError(f"no key for {p!r}")


def _key_arity(key, ctx):
    if key[0] == "ctor":
        return ctx.taginfo[key[1]][1]
    if key[0] == "tuple":
        return key[1]
    return 0


def _pat_args(p):
    if isinstance(p, PCtor):
        return p.args
    if isinstance(p, PTuple):
        return p.items
    return []


def _completeness(keys, ctx):
    """(is_complete, example_missing_key) for the keys of one column."""
    # single-constructor signatures
    for k in keys:
        if k[0] == "tuple":
            return True, None
        if k[0] == "lit" and k[1] == "none":
            return True, None
    # union signatures
    ctor_tags = [k[1] for k in keys if k[0] == "ctor"]
    for tag in ctor_tags:
        uname = ctx.taginfo[tag][0]
        if all(("ctor", t) in keys for t in ctx.union_tags[uname]):
            return True, None
    # boolean signature
    lits = [k for k in keys if k[0] == "lit"]
    bools = {k[2] for k in lits if k[1] == "bool"}
    if bools == {True, False}:
        return True, None
    # incomplete: pick a concrete missing example
    if ctor_tags:
        uname = ctx.taginfo[ctor_tags[0]][0]
        for t in ctx.union_tags[uname]:
            if ("ctor", t) not in keys:
                return False, ("ctor", t)
    if any(k[1] == "bool" for k in lits):
        missing = True if ("lit", "bool", True) not in lits else False
        return False, ("lit", "bool", missing)
    if any(k[1] == "int" for k in lits):
        used = {k[2] for k in lits if k[1] == "int"}
        v = 0
        while v in used:
            v += 1
        return False, ("lit", "int", v)
    if any(k[1] == "str" for k in lits):
        used = {k[2] for k in lits if k[1] == "str"}
        v = ""
        while v in used:
            v += "?"
        return False, ("lit", "str", v)
    return False, None


# ---------------------------------------------------------------------------
# matrix compilation
# ---------------------------------------------------------------------------

def _is_wild_or_var(p):
    return isinstance(p, (PWild, PVar))


def _compile_rows(rows, occs, ctx):
    if not rows:
        return Fail()
    first = rows[0]
    if all(_is_wild_or_var(p) for p in first.pats):
        bindings = first.bindings + [
            (p.name, occs[i])
            for i, p in enumerate(first.pats) if isinstance(p, PVar)
        ]
        if first.guard is None:
            return Leaf(first.case_idx, bindings)
        return Guard(first.case_idx, first.guard, bindings,
                     _compile_rows(rows[1:], occs, ctx))

    # leftmost column that contains at least one non-variable pattern
    col = next(i for i in range(len(occs))
               if any(not _is_wild_or_var(r.pats[i]) for r in rows))

    def rot(xs):
        return [xs[col]] + xs[:col] + xs[col + 1:]

    rows = [_Row(rot(r.pats), r.guard, r.case_idx, r.bindings) for r in rows]
    occs = rot(occs)
    occ = occs[0]

    keys = []
    for r in rows:
        p = r.pats[0]
        if _is_wild_or_var(p):
            continue
        k = _pat_key(p)
        if k not in keys:
            keys.append(k)

    arms = []
    for k in keys:
        arity = _key_arity(k, ctx)
        suboccs = [occ + (i,) for i in range(arity)] + occs[1:]
        subrows = []
        for r in rows:
            p = r.pats[0]
            if isinstance(p, PWild):
                subrows.append(_Row([PWild(p.span)] * arity + r.pats[1:],
                                    r.guard, r.case_idx, r.bindings))
            elif isinstance(p, PVar):
                subrows.append(_Row([PWild(p.span)] * arity + r.pats[1:],
                                    r.guard, r.case_idx,
                                    r.bindings + [(p.name, occ)]))
            elif _pat_key(p) == k:
                subrows.append(_Row(_pat_args(p) + r.pats[1:],
                                    r.guard, r.case_idx, r.bindings))
            # other constructors: row dropped for this arm
        arms.append((k, _compile_rows(subrows, suboccs, ctx)))

    complete, example = _completeness(keys, ctx)
    if complete:
        default = None
        example = None
    else:
        defrows = []
        for r in rows:
            p = r.pats[0]
            if isinstance(p, PWild):
                defrows.append(_Row(r.pats[1:], r.guard, r.case_idx,
                                    r.bindings))
            elif isinstance(p, PVar):
                defrows.append(_Row(r.pats[1:], r.guard, r.case_idx,
                                    r.bindings + [(p.name, occ)]))
        default = _compile_rows(defrows, occs[1:], ctx)
    return Switch(occ, arms, default, example)


# ---------------------------------------------------------------------------
# tree analyses
# ---------------------------------------------------------------------------

def _collect_used(node, acc):
    if isinstance(node, Leaf):
        acc.add(node.case_idx)
    elif isinstance(node, Guard):
        acc.add(node.case_idx)
        _collect_used(node.otherwise, acc)
    elif isinstance(node, Switch):
        for _, sub in node.arms:
            _collect_used(sub, acc)
        if node.default is not None:
            _collect_used(node.default, acc)


def _has_fail(node):
    if isinstance(node, Fail):
        return True
    if isinstance(node, Guard):
        return _has_fail(node.otherwise)
    if isinstance(node, Switch):
        if any(_has_fail(sub) for _, sub in node.arms):
            return True
        return node.default is not None and _has_fail(node.default)
    return False


def _witness(node):
    """Map occurrence path -> example key for one unmatched value shape."""
    if isinstance(node, Fail):
        return {}
    if isinstance(node, Leaf):
        return None
    if isinstance(node, Guard):
        return _witness(node.otherwise)
    if isinstance(node, Switch):
        for k, sub in node.arms:
            w = _witness(sub)
            if w is not None:
                return {node.occ: k, **w}
        if node.default is not None:
            w = _witness(node.default)
            if w is not None:
                return {node.occ: node.default_example, **w}
        return None
    raise AssertionError(f"unknown node {node!r}")


def _render_key(key, wm, path, ctx):
    if key is None:
        return "_"
    if key[0] == "ctor":
        tag = key[1]
        arity = ctx.taginfo[tag][1]
        if arity == 0:
            return tag
        inner = ", ".join(_render_at(wm, path + (i,), ctx)
                          for i in range(arity))
        return f"{tag}({inner})"
    if key[0] == "tuple":
        n = key[1]
        inner = ", ".join(_render_at(wm, path + (i,), ctx)
                          for i in range(n))
        if n == 1:
            inner += ","
        return f"({inner})"
    kind, value = key[1], key[2]
    if kind == "bool":
        return "true" if value else "false"
    if kind == "none":
        return "none"
    if kind == "int":
        return str(value)
    return '"' + value + '"'


def _render_at(wm, path, ctx):
    return _render_key(wm.get(path), wm, path, ctx)


def render_witness(wm, ctx):
    return _render_at(wm, (), ctx)


# ---------------------------------------------------------------------------
# entry point
# ---------------------------------------------------------------------------

def compile_program(prog):
    """Compile a parsed Program. Returns (tree_or_None, [Diag])."""
    diags = []
    ctx = _Ctx(prog.unions)

    _validate_expr(prog.subject, ctx, diags, set())
    for case in prog.cases:
        names = {}
        _validate_pattern(case.pattern, ctx, diags, names)
        bound = set(names)
        if case.guard is not None:
            _validate_expr(case.guard, ctx, diags, bound)
        _validate_expr(case.body, ctx, diags, bound)
    if any(d.severity == "error" for d in diags):
        return None, diags

    rows = [_Row([c.pattern], c.guard, i, [])
            for i, c in enumerate(prog.cases)]
    tree = _compile_rows(rows, [()], ctx)

    used = set()
    _collect_used(tree, used)
    for i, case in enumerate(prog.cases):
        if i not in used:
            diags.append(Diag("warning", f"case {i + 1} is unreachable",
                              case.span))

    if _has_fail(tree):
        example = render_witness(_witness(tree), ctx)
        diags.append(Diag(
            "warning",
            f"non-exhaustive match; missing case e.g. `{example}` "
            f"(guards are not assumed to hold)",
            prog.subject_span))
    return tree, diags
