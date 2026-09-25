"""比较两个观察时刻的推导结论，输出最小因果切片。

变化分类（三大类）：
- 新增事实   NEW_FACT
- 规则变更   RULE_ADDED / RULE_CHANGED（同 ID 不同版本按版本识别）
- 证据失效   FACT_REMOVED / RULE_REMOVED / FACT_UPDATED（旧版本证据失效）

结果相同但支持来源改变的结论单列（SupportChange）。
切片通过"重放 + 贪心缩减"保证最小性，且每个切片都可重放验证。
与任何变化结论无关的输入不进入解释，仅列入 irrelevant_inputs。
"""
from __future__ import annotations

from dataclasses import dataclass

from engine import Fact, Rule, run_engine

# 细粒度原因类别
NEW_FACT = "new_fact"
FACT_UPDATED = "fact_updated"
RULE_ADDED = "rule_added"
RULE_CHANGED = "rule_changed"
FACT_REMOVED = "fact_removed"
RULE_REMOVED = "rule_removed"

# 三大类
GROUP_NEW_FACT = "新增事实"
GROUP_RULE_CHANGE = "规则变更"
GROUP_INVALIDATED = "证据失效"

GROUP_OF = {
    NEW_FACT: GROUP_NEW_FACT,
    RULE_ADDED: GROUP_RULE_CHANGE,
    RULE_CHANGED: GROUP_RULE_CHANGE,
    FACT_REMOVED: GROUP_INVALIDATED,
    RULE_REMOVED: GROUP_INVALIDATED,
    FACT_UPDATED: GROUP_INVALIDATED,
}


def format_atom(atom: tuple) -> str:
    pred, args = atom
    return f"{pred}({', '.join(args)})"


@dataclass(frozen=True)
class Snapshot:
    """一个观察时刻的全部输入：版本化事实 + 版本化规则。"""
    name: str
    facts: tuple
    rules: tuple

    def elements(self) -> dict:
        return {f.key: f for f in self.facts} | {r.key: r for r in self.rules}

    def fact_index(self) -> dict:
        return {f.fid: f for f in self.facts}

    def rule_index(self) -> dict:
        return {r.rid: r for r in self.rules}


@dataclass(frozen=True)
class SliceElement:
    kind: str    # NEW_FACT / RULE_CHANGED / ...
    key: tuple   # ("fact", fid, ver) / ("rule", rid, ver)
    obj: object  # Fact 或 Rule

    @property
    def group(self) -> str:
        return GROUP_OF[self.kind]


@dataclass
class ConclusionChange:
    atom: tuple
    causal_slice: tuple   # SliceElement... 最小因果切片
    replay_ok: bool       # 重放验证结果
    replay_steps: tuple   # 重放得到的推导步骤 sid

    @property
    def groups(self) -> list:
        seen = []
        for e in self.causal_slice:
            if e.group not in seen:
                seen.append(e.group)
        return seen


@dataclass
class SupportChange:
    """结论不变但支持来源改变，单列。"""
    atom: tuple
    old_support: tuple
    new_support: tuple
    rule_version_changes: tuple  # ((rid, old_ver, new_ver), ...)
    added_support: tuple
    removed_support: tuple


@dataclass
class DiffReport:
    gained: list            # ConclusionChange
    lost: list              # ConclusionChange
    support_changed: list   # SupportChange
    irrelevant_inputs: list # 被排除在解释之外的无关输入（字符串描述）


def replay_derives(base: Snapshot, slice_objs, atom: tuple) -> bool:
    """重放：在 base 输入上应用切片元素，验证能否重推出 atom。"""
    facts = base.facts + tuple(o for o in slice_objs if isinstance(o, Fact))
    rules = base.rules + tuple(o for o in slice_objs if isinstance(o, Rule))
    return atom in run_engine(facts, rules).supports


def _minimize(base: Snapshot, slice_objs: list, atom: tuple) -> list:
    """贪心缩减至 1-最小：去掉任一元素后重放不再成立。"""
    current = list(slice_objs)
    changed = True
    while changed:
        changed = False
        for i in range(len(current)):
            trial = current[:i] + current[i + 1:]
            if replay_derives(base, trial, atom):
                current = trial
                changed = True
                break
    return current



def _proof_steps(deriv, atom: tuple) -> tuple:
    """收集 atom 完整证明 DAG 涉及的全部步骤 sid（确定性排序）。"""
    seen_atoms = set()
    sids = set()
    queue = [atom]
    while queue:
        cur = queue.pop()
        if cur in seen_atoms:
            continue
        seen_atoms.add(cur)
        for sid in deriv.steps_by_conclusion.get(cur, []):
            sids.add(sid)
            for tok in deriv.steps[sid].input_tokens:
                if tok[0] == "derived":
                    queue.append((tok[1], tok[2]))
    return tuple(sorted(sids))


def _classify_new(key: tuple, snap: Snapshot) -> str:
    tag, eid, _ver = key
    if tag == "fact":
        return FACT_UPDATED if eid in snap.fact_index() else NEW_FACT
    return RULE_CHANGED if eid in snap.rule_index() else RULE_ADDED


def _classify_old(key: tuple, snap: Snapshot) -> str:
    tag, eid, _ver = key
    if tag == "fact":
        return FACT_UPDATED if eid in snap.fact_index() else FACT_REMOVED
    return RULE_CHANGED if eid in snap.rule_index() else RULE_REMOVED


def _pick_support(supports: set, other_elements: dict) -> frozenset:
    """选择含'对端不存在元素'最少、规模最小的支持集。"""
    return min(
        supports,
        key=lambda s: (
            len([k for k in s if k not in other_elements]),
            len(s),
            repr(sorted(s)),
        ),
    )


def _explain_gained(atom, a, b, db) -> ConclusionChange:
    elems_a = a.elements()
    best = _pick_support(db.supports[atom], elems_a)
    delta_objs = [b.elements()[k] for k in sorted(best) if k not in elems_a]
    minimal = _minimize(a, delta_objs, atom)
    minimal.sort(key=lambda o: repr(o.key))
    causal_slice = tuple(
        SliceElement(_classify_new(o.key, a), o.key, o) for o in minimal
    )
    deriv = run_engine(
        a.facts + tuple(o for o in minimal if isinstance(o, Fact)),
        a.rules + tuple(o for o in minimal if isinstance(o, Rule)),
    )
    return ConclusionChange(
        atom=atom,
        causal_slice=causal_slice,
        replay_ok=atom in deriv.supports,
        replay_steps=_proof_steps(deriv, atom),
    )


def _explain_lost(atom, a, b, da) -> ConclusionChange:
    elems_b = b.elements()
    best = _pick_support(da.supports[atom], elems_b)
    delta_objs = [a.elements()[k] for k in sorted(best) if k not in elems_b]
    minimal = _minimize(b, delta_objs, atom)
    minimal.sort(key=lambda o: repr(o.key))
    causal_slice = tuple(
        SliceElement(_classify_old(o.key, b), o.key, o) for o in minimal
    )
    deriv = run_engine(
        b.facts + tuple(o for o in minimal if isinstance(o, Fact)),
        b.rules + tuple(o for o in minimal if isinstance(o, Rule)),
    )
    return ConclusionChange(
        atom=atom,
        causal_slice=causal_slice,
        replay_ok=atom in deriv.supports,
        replay_steps=_proof_steps(deriv, atom),
    )


def _support_change(atom, da, db) -> SupportChange:
    old = min(da.supports[atom], key=lambda s: (len(s), repr(sorted(s))))
    new = min(db.supports[atom], key=lambda s: (len(s), repr(sorted(s))))
    removed = tuple(sorted(old - new, key=repr))
    added = tuple(sorted(new - old, key=repr))
    removed_rules = {k[1]: k[2] for k in removed if k[0] == "rule"}
    added_rules = {k[1]: k[2] for k in added if k[0] == "rule"}
    version_changes = tuple(
        (rid, removed_rules[rid], added_rules[rid])
        for rid in sorted(removed_rules)
        if rid in added_rules
    )
    return SupportChange(
        atom=atom,
        old_support=tuple(sorted(old, key=repr)),
        new_support=tuple(sorted(new, key=repr)),
        rule_version_changes=version_changes,
        added_support=added,
        removed_support=removed,
    )


def _describe_irrelevant(key: tuple, a: Snapshot, b: Snapshot) -> str:
    elems_a, elems_b = a.elements(), b.elements()
    if key in elems_a:
        return f"{elems_a[key]}（在 {b.name} 中已删除/被替换，与任何结论变化无关）"
    return f"{elems_b[key]}（在 {b.name} 中新增/变更，与任何结论变化无关）"


def diff_snapshots(a: Snapshot, b: Snapshot) -> DiffReport:
    da = run_engine(a.facts, a.rules)
    db = run_engine(b.facts, b.rules)

    # 只统计有推导步骤的原子为"推导结论"；纯断言事实属于输入变化，会进入切片
    derived_a = {x for x in da.atoms() if da.steps_by_conclusion.get(x)}
    derived_b = {x for x in db.atoms() if db.steps_by_conclusion.get(x)}

    gained_atoms = sorted(derived_b - da.atoms(), key=repr)
    lost_atoms = sorted(derived_a - db.atoms(), key=repr)
    common_derived = sorted(
        (derived_a | derived_b) & da.atoms() & db.atoms(), key=repr
    )

    gained = [_explain_gained(atom, a, b, db) for atom in gained_atoms]
    lost = [_explain_lost(atom, a, b, da) for atom in lost_atoms]
    support_changed = [
        _support_change(atom, da, db)
        for atom in common_derived
        if da.supports[atom] != db.supports[atom]
    ]

    # 无关输入：未出现在任何变化结论任一支持集中的增量输入，不进入解释
    used_keys = set()
    for atom in gained_atoms:
        used_keys |= set().union(*db.supports[atom])
    for atom in lost_atoms:
        used_keys |= set().union(*da.supports[atom])
    for atom in common_derived:
        if da.supports[atom] != db.supports[atom]:
            used_keys |= set().union(*da.supports[atom])
            used_keys |= set().union(*db.supports[atom])

    delta_keys = set(a.elements()) ^ set(b.elements())
    irrelevant = [
        _describe_irrelevant(k, a, b)
        for k in sorted(delta_keys - used_keys, key=repr)
    ]

    return DiffReport(
        gained=gained,
        lost=lost,
        support_changed=support_changed,
        irrelevant_inputs=irrelevant,
    )


def format_key(key: tuple) -> str:
    tag, eid, ver = key
    return f"{tag}:{eid}@v{ver}"
