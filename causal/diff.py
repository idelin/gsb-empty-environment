"""两时刻快照对比：根因差异、最小因果切片、重放验证。"""

from __future__ import annotations

from dataclasses import dataclass

from .model import Snapshot

# 根因差异类别
FACT_ADDED = "fact_added"
FACT_REMOVED = "fact_removed"
FACT_UPDATED = "fact_updated"
RULE_ADDED = "rule_added"
RULE_REMOVED = "rule_removed"
RULE_UPDATED = "rule_updated"

# 结论变化类别
ADDED = "added"                    # 新出现的结论
REMOVED = "removed"                # 失效的结论
SUPPORT_CHANGED = "support_changed"  # 结果相同但支持来源改变（单列）

# 原因类别
CAUSE_NEW_FACT = "新增事实"
CAUSE_RULE_CHANGE = "规则变更"
CAUSE_EVIDENCE_LOST = "证据失效"
CAUSE_EVIDENCE_UPDATED = "证据更新"

_KIND_LABELS = {
    FACT_ADDED: "新增事实",
    FACT_REMOVED: "撤回事实",
    FACT_UPDATED: "更新事实",
    RULE_ADDED: "新增规则",
    RULE_REMOVED: "删除规则",
    RULE_UPDATED: "规则内容变更",
}


@dataclass(frozen=True)
class RootDiff:
    """输入层（根）差异：目标 id + 旧/新版本。"""

    kind: str
    target_id: str
    old_version: object
    new_version: object

    def describe(self) -> str:
        label = _KIND_LABELS[self.kind]
        if self.old_version is None:
            return f"{label} {self.target_id}@v{self.new_version}"
        if self.new_version is None:
            return f"{label} {self.target_id}@v{self.old_version}"
        return f"{label} {self.target_id}: v{self.old_version} -> v{self.new_version}"


@dataclass
class ConclusionChange:
    """一条结论的变化及其最小因果切片（证据可重放）。"""

    conclusion_id: str
    change: str
    causes: tuple
    slice: tuple          # tuple[RootDiff]，1-最小
    replay_ok: bool
    steps_t1: tuple       # tuple[step_id]
    steps_t2: tuple


@dataclass
class Report:
    label_t1: str
    label_t2: str
    changes: tuple          # tuple[ConclusionChange]
    irrelevant_diffs: tuple  # 未进入任何解释的根差异


def root_diffs(s1: Snapshot, s2: Snapshot) -> tuple:
    """两个快照在输入层的全部差异（事实与规则，按版本识别）。"""
    diffs = []
    for fid in sorted(set(s1.facts) | set(s2.facts)):
        f1, f2 = s1.facts.get(fid), s2.facts.get(fid)
        if f1 is None:
            diffs.append(RootDiff(FACT_ADDED, fid, None, f2.version))
        elif f2 is None:
            diffs.append(RootDiff(FACT_REMOVED, fid, f1.version, None))
        elif f1 != f2:
            diffs.append(RootDiff(FACT_UPDATED, fid, f1.version, f2.version))
    for rid in sorted(set(s1.rules) | set(s2.rules)):
        r1, r2 = s1.rules.get(rid), s2.rules.get(rid)
        if r1 is None:
            diffs.append(RootDiff(RULE_ADDED, rid, None, r2.version))
        elif r2 is None:
            diffs.append(RootDiff(RULE_REMOVED, rid, r1.version, None))
        elif r1 != r2:
            diffs.append(RootDiff(RULE_UPDATED, rid, r1.version, r2.version))
    return tuple(diffs)


def apply_diffs(base: Snapshot, other: Snapshot, diffs) -> Snapshot:
    """把一组根差异应用到 base 快照上（新增/更新取自 other），用于重放。"""
    facts, rules = dict(base.facts), dict(base.rules)
    for d in diffs:
        if d.kind in (FACT_ADDED, FACT_UPDATED):
            facts[d.target_id] = other.facts[d.target_id]
        elif d.kind == FACT_REMOVED:
            facts.pop(d.target_id, None)
        elif d.kind in (RULE_ADDED, RULE_UPDATED):
            rules[d.target_id] = other.rules[d.target_id]
        elif d.kind == RULE_REMOVED:
            rules.pop(d.target_id, None)
    return Snapshot(f"{base.label}+slice", tuple(facts.values()), tuple(rules.values()))


def _minimize(base: Snapshot, other: Snapshot, candidates, prop) -> tuple:
    """贪心 1-最小化：逐个移除候选差异，只要性质仍成立就移除。

    结果保证：再移除任何一个元素，性质即不成立（无关输入被排除）。
    """
    current = list(candidates)
    changed = True
    while changed:
        changed = False
        for d in list(current):
            trial = [x for x in current if x is not d]
            if prop(apply_diffs(base, other, trial).derive()):
                current = trial
                changed = True
    return tuple(current)


def _causes_of(slice_, change: str) -> tuple:
    causes = set()
    for d in slice_:
        if d.kind == FACT_ADDED:
            causes.add(CAUSE_NEW_FACT)
        elif d.kind == FACT_REMOVED:
            causes.add(CAUSE_EVIDENCE_LOST)
        elif d.kind == FACT_UPDATED:
            if change == REMOVED:
                causes.add(CAUSE_EVIDENCE_LOST)
            elif change == SUPPORT_CHANGED:
                causes.add(CAUSE_EVIDENCE_UPDATED)
            else:
                causes.add(CAUSE_NEW_FACT)
        else:
            causes.add(CAUSE_RULE_CHANGE)
    return tuple(sorted(causes))


def compare(s1: Snapshot, s2: Snapshot) -> Report:
    """对比两个观察时刻，输出每条结论变化的最小因果切片。"""
    d1, d2 = s1.derive(), s2.derive()
    diffs = root_diffs(s1, s2)
    ids1, ids2 = d1.derived_ids(), d2.derived_ids()
    changes = []

    for cid in sorted(ids1 | ids2):
        if cid not in ids1:
            change = ADDED
            cone_ids = _cone_ids(d2, cid)
            prop = lambda der: cid in der.derived_ids()
        elif cid not in ids2:
            change = REMOVED
            cone_ids = _cone_ids(d1, cid)
            prop = lambda der: cid not in der.derived_ids()
        elif d1.step_ids_for(cid) != d2.step_ids_for(cid):
            change = SUPPORT_CHANGED
            cone_ids = _cone_ids(d1, cid) | _cone_ids(d2, cid)
            t1_steps = d1.step_ids_for(cid)
            prop = lambda der: cid in der.derived_ids() and der.step_ids_for(cid) != t1_steps
        else:
            continue  # 结论与支持均未变，不进入报告

        candidates = [d for d in diffs if d.target_id in cone_ids]
        slice_ = _minimize(s1, s2, candidates, prop)
        replay_ok = prop(apply_diffs(s1, s2, slice_).derive())
        changes.append(ConclusionChange(
            conclusion_id=cid,
            change=change,
            causes=_causes_of(slice_, change),
            slice=slice_,
            replay_ok=replay_ok,
            steps_t1=tuple(sorted(s.id for s in d1.steps_for(cid))),
            steps_t2=tuple(sorted(s.id for s in d2.steps_for(cid))),
        ))

    explained = {d.target_id for c in changes for d in c.slice}
    irrelevant = tuple(d for d in diffs if d.target_id not in explained)
    return Report(s1.label, s2.label, tuple(changes), irrelevant)


def _cone_ids(derivation, conclusion_id: str) -> set:
    roots, rules = derivation.cone(conclusion_id)
    return roots | {rid for rid, _ in rules}
