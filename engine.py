"""带溯源的前向链规则引擎。

事实、规则、推导步骤均带稳定 ID 与版本：
- Fact.key  = ("fact", fid, version)
- Rule.key  = ("rule", rid, version)
- Step.sid  = 对 (规则 key, 输入 token, 结论) 的 sha1，跨进程稳定可重放

每个推导出的结论记录其全部支持集（support），支持集的元素是
事实/规则的版本化 key，用于后续的因果切片计算。
"""
from __future__ import annotations

import hashlib
from collections import defaultdict
from dataclasses import dataclass

MAX_SUPPORTS_PER_ATOM = 128


@dataclass(frozen=True)
class Fact:
    fid: str
    version: int
    pred: str
    args: tuple[str, ...]

    @property
    def atom(self) -> tuple:
        return (self.pred, self.args)

    @property
    def key(self) -> tuple:
        return ("fact", self.fid, self.version)

    def __str__(self) -> str:
        return f"fact:{self.fid}@v{self.version} {self.pred}({', '.join(self.args)})"


@dataclass(frozen=True)
class Rule:
    rid: str
    version: int
    conditions: tuple  # ((pred, (arg, ...)), ...)  变量以 "?" 开头
    conclusion: tuple  # (pred, (arg, ...))

    @property
    def key(self) -> tuple:
        return ("rule", self.rid, self.version)

    def __str__(self) -> str:
        conds = " & ".join(f"{p}({', '.join(a)})" for p, a in self.conditions)
        cpred, cargs = self.conclusion
        return f"rule:{self.rid}@v{self.version} {conds} => {cpred}({', '.join(cargs)})"


def _subst_atom(template: tuple, subst: dict) -> tuple:
    pred, args = template
    return (pred, tuple(subst.get(a, a) for a in args))


def _unify(cond: tuple, atom: tuple, subst: dict):
    cpred, cargs = cond
    apred, aargs = atom
    if cpred != apred or len(cargs) != len(aargs):
        return None
    out = dict(subst)
    for c, a in zip(cargs, aargs):
        if c.startswith("?"):
            if c in out:
                if out[c] != a:
                    return None
            else:
                out[c] = a
        elif c != a:
            return None
    return out


def match_rule(rule: Rule, atoms: list) -> list:
    """返回 [(subst, used_atoms)]，used_atoms 记录溯源所需的匹配原子。"""
    states = [({}, ())]
    for cond in rule.conditions:
        nxt = []
        for subst, used in states:
            for atom in atoms:
                s2 = _unify(cond, atom, subst)
                if s2 is not None:
                    nxt.append((s2, used + (atom,)))
        states = nxt
        if not states:
            break
    return states


def step_id(rule_key: tuple, input_tokens: tuple, conclusion_atom: tuple) -> str:
    raw = repr((rule_key, input_tokens, conclusion_atom))
    return "step-" + hashlib.sha1(raw.encode()).hexdigest()[:12]


@dataclass(frozen=True)
class Step:
    sid: str
    rule_key: tuple
    input_tokens: tuple
    conclusion: tuple


class Derivation:
    def __init__(self) -> None:
        self.supports: dict = defaultdict(set)        # atom -> {frozenset(element key)}
        self.steps: dict = {}                          # sid -> Step
        self.steps_by_conclusion: dict = defaultdict(list)

    def atoms(self) -> set:
        return set(self.supports)


def run_engine(facts: tuple, rules: tuple) -> Derivation:
    """前向链推导至不动点，记录每个结论的全部最小支持集。全程确定性可重放。"""
    deriv = Derivation()
    fact_by_atom = {}
    for f in facts:
        fact_by_atom[f.atom] = f
        deriv.supports[f.atom].add(frozenset({f.key}))

    def token_of(atom: tuple) -> tuple:
        f = fact_by_atom.get(atom)
        if f is not None:
            return ("fact", f.fid, f.version)
        return ("derived", atom[0], atom[1])

    changed = True
    while changed:
        changed = False
        known = sorted(deriv.atoms(), key=repr)
        for rule in rules:
            for subst, used in match_rule(rule, known):
                concl = _subst_atom(rule.conclusion, subst)
                input_tokens = tuple(sorted(token_of(a) for a in used))
                sid = step_id(rule.key, input_tokens, concl)
                if sid not in deriv.steps:
                    deriv.steps[sid] = Step(sid, rule.key, input_tokens, concl)
                    deriv.steps_by_conclusion[concl].append(sid)
                combos = {frozenset({rule.key})}
                for atom in used:
                    slist = sorted(deriv.supports[atom], key=repr)
                    combos = {c | s for c in combos for s in slist}
                    if len(combos) > MAX_SUPPORTS_PER_ATOM:
                        combos = set(sorted(combos, key=repr)[:MAX_SUPPORTS_PER_ATOM])
                before = len(deriv.supports[concl])
                deriv.supports[concl].update(combos)
                if len(deriv.supports[concl]) != before:
                    changed = True
    return deriv
