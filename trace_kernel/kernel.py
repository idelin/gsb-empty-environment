"""分析内核：轨迹 -> 证据 -> 反向索引 -> 变更事件 -> 三态结论。

结论：
  valid         缓存仍有效（无任何依赖被后续事件触碰）
  invalid       必须失效（给出最短解释链：依赖 -> 事件 -> 结论）
  insufficient  证据不足（必须给出具体原因；不允许用“全部失效”代替）

时间语义：依赖记录观察发生的 seq；只有 seq 更大的事件才可能使其失效，
任务观察之前发生的事件已包含在观察结果中。
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .evidence import Evidence, build_evidence, load_evidence, save_evidence
from .index import ReverseIndex, evidence_keys
from .trace import MUTATION_OPS, TASK_OPS

VALID = "valid"
INVALID = "invalid"
INSUFFICIENT = "insufficient"


@dataclass
class Verdict:
    task: str
    status: str
    chain: list[str] = field(default_factory=list)
    reason: str = ""          # insufficient 时必填的具体原因


def _event_desc(e: dict) -> str:
    op = e["op"]
    if op == "write":
        return f"事件#{e['seq']} write {e['path']}: 对象o{e['obj']} 版本v{e['old_version']}->v{e['new_version']}"
    if op == "chmod":
        return f"事件#{e['seq']} chmod {e['path']}: 对象o{e['obj']} 可读{e['old']}->{e['new']}"
    if op == "rename":
        return f"事件#{e['seq']} rename {e['src']} -> {e['dst']} (对象o{e['obj']})"
    if op in ("create", "mkdir", "symlink", "link"):
        return f"事件#{e['seq']} {op} {e.get('path') or e.get('dst')}: 目录d{e['dir']} 新增成员 {e['name']!r} -> o{e['obj']}"
    if op == "unlink":
        return f"事件#{e['seq']} unlink {e['path']}: 目录d{e['dir']} 移除成员 {e['name']!r} (原o{e['obj']})"
    if op == "set_target":
        return f"事件#{e['seq']} set_target {e['path']}: 符号链接o{e['obj']} 目标 {e['old']!r}->{e['new']!r}"
    if op == "external":
        return f"事件#{e['seq']} 外部变更 {e['path']}: {e.get('note', '')}"
    return f"事件#{e['seq']} {op}"


def _member_actions(e: dict) -> list[tuple[int, str, int, str]]:
    """把事件展开为 (目录oid, 名字, 对象oid, add|remove) 列表。"""
    op = e["op"]
    if op in ("create", "mkdir", "symlink", "link"):
        return [(e["dir"], e["name"], e["obj"], "add")]
    if op == "unlink":
        return [(e["dir"], e["name"], e["obj"], "remove")]
    if op == "rename":
        return [(e["src_dir"], e["src_name"], e["obj"], "remove"),
                (e["dst_dir"], e["dst_name"], e["obj"], "add")]
    return []


def event_keys(e: dict) -> set[tuple]:
    keys: set[tuple] = set()
    if e["op"] == "write":
        keys.add(("content", e["obj"]))
    elif e["op"] == "chmod":
        keys.add(("perm", e["obj"]))
    elif e["op"] == "set_target":
        keys.add(("symlink", e["obj"]))
    else:
        for d, n, _o, _a in _member_actions(e):
            keys.add(("member", d, n))
            keys.add(("enum", d))   # 成员变化影响该目录的枚举依赖
    return keys


class AnalysisKernel:
    def __init__(self, record_access: bool = False) -> None:
        self.evidence: dict[str, Evidence] = {}
        self.index = ReverseIndex(record_access=record_access)
        self.events: list[dict] = []
        self.verdicts: dict[str, Verdict] = {}
        self.evaluated: set[str] = set()   # 本轮实际检查过依赖的任务

    # ---- 轨迹摄入 -------------------------------------------------------

    def ingest(self, records: list[dict]) -> None:
        by_task: dict[str, list[dict]] = {}
        for rec in records:
            if rec["op"] in TASK_OPS and rec.get("task") is not None:
                by_task.setdefault(rec["task"], []).append(rec)
            elif rec["op"] in MUTATION_OPS:
                self.events.append(rec)
        for task, recs in by_task.items():
            self._install(task, build_evidence(task, recs))

    def _install(self, task: str, ev: Evidence) -> None:
        self.evidence[task] = ev
        self.index.add(task, evidence_keys(ev))

    def replace_task(self, task: str, records: list[dict]) -> None:
        """任务重算：先整体移除旧依赖，再装新证据，索引不得残留。"""
        self.index.remove(task)
        self.evidence.pop(task, None)
        self.verdicts.pop(task, None)
        recs = [r for r in records if r.get("task") == task and r["op"] in TASK_OPS]
        self._install(task, build_evidence(task, recs))

    def remove_task(self, task: str) -> None:
        self.index.remove(task)
        self.evidence.pop(task, None)
        self.verdicts.pop(task, None)

    def add_external_event(self, path: str, note: str = "") -> dict:
        seq = max((e["seq"] for e in self.events), default=-1) + 1
        if self.events:
            seq = max(seq, max(e["seq"] for e in self.events) + 1)
        e = {"seq": seq, "task": None, "op": "external", "path": path, "note": note}
        self.events.append(e)
        return e

    # ---- 证据恢复 -------------------------------------------------------

    def save(self, path: str) -> None:
        save_evidence(path, self.evidence)

    @classmethod
    def restore(cls, path: str, events: list[dict] | None = None) -> "AnalysisKernel":
        k = cls()
        for task, ev in load_evidence(path).items():
            k._install(task, ev)
        k.events = list(events or [])
        return k

    # ---- 判定 -----------------------------------------------------------

    def evaluate(self, events: list[dict] | None = None) -> dict[str, Verdict]:
        if events is not None:
            self.events = list(events)
        self.verdicts = {}
        self.evaluated = set()
        self.index.access_log.clear()

        candidates: set[str] = set()
        for e in self.events:
            candidates |= self.index.tasks_for(event_keys(e))
        external = [e for e in self.events if e["op"] == "external"]

        for task, ev in self.evidence.items():
            self.verdicts[task] = self._judge(task, ev, candidates, external)
        return self.verdicts

    def _judge(self, task: str, ev: Evidence,
               candidates: set[str], external: list[dict]) -> Verdict:
        if ev.inconsistent:
            bad = {o: vs for o, vs in ev.content.items()
                   if len({v for v, _ in vs}) > 1}
            o, vs = next(iter(sorted(bad.items())))
            vers = sorted({v for v, _ in vs})
            return Verdict(task, INVALID, [
                f"任务{task}: 对对象o{o}的多次读取观察到不同版本{vers}，"
                f"读取跨越内容变更，快照不一致",
                "结论: 不能批准缓存（缓存结果混合了多个版本）",
            ])
        if task not in candidates and not external:
            return Verdict(task, VALID, [
                f"任务{task}: 全部依赖键均未出现在事件"
                f"{[e['seq'] for e in self.events]}影响的反向索引项中"])
        if task in candidates:
            self.evaluated.add(task)
        for e in sorted(self.events, key=lambda x: x["seq"]):
            if e["op"] == "external":
                continue
            hit = self._match(ev, e)
            if hit is not None:
                dep_desc, why = hit
                return Verdict(task, INVALID, [
                    f"任务{task} 依赖: {dep_desc}",
                    _event_desc(e),
                    f"结论: {why}，缓存必须失效",
                ])
        for e in external:
            overlap = self._path_overlap(ev, e["path"])
            if overlap is not None:
                return Verdict(task, INSUFFICIENT, [
                    f"任务{task} 依赖: 解析过路径 {overlap}",
                    _event_desc(e),
                    "结论: 证据不足，保守失效",
                ], reason=(
                    f"事件#{e['seq']} 是未建模的外部变更({e['path']})，"
                    f"缺少对象身份与变更类别，无法判定是否影响已解析路径 {overlap}"))
        return Verdict(task, VALID, [
            f"任务{task}: 候选事件逐条比对后无依赖冲突"])

    def _path_overlap(self, ev: Evidence, ext_path: str) -> str | None:
        def under(a: str, b: str) -> bool:
            return a == b or a.startswith(b.rstrip("/") + "/") or \
                b.startswith(a.rstrip("/") + "/")
        for dep in ev.path_deps + ev.negatives:
            if under(dep["path"], ext_path):
                return dep["path"]
        return None

    def _match(self, ev: Evidence, e: dict) -> tuple[str, str] | None:
        """事件 e 是否击中任务证据 ev 的某条依赖；返回 (依赖描述, 失效原因)。"""
        op = e["op"]
        if op == "write":
            for ver, seq in ev.content.get(e["obj"], []):
                if e["seq"] > seq:
                    return (f"seq={seq} 读取对象o{e['obj']}@v{ver}",
                            f"对象内容版本已变为v{e['new_version']}")
            return None
        if op == "chmod":
            prev = ev.perms.get(e["obj"])
            if prev is not None and e["seq"] > prev[1] and e["new"] != prev[0]:
                return (f"seq={prev[1]} 观察到对象o{e['obj']} 可读={prev[0]}",
                        f"读权限变为{e['new']}，重新解析/读取结果会不同")
            return None
        if op == "set_target":
            prev = ev.symlinks.get(e["obj"])
            if prev is not None and e["seq"] > prev[1]:
                return (f"seq={prev[1]} 解析经过符号链接o{e['obj']}，当时目标 {prev[0]!r}",
                        "符号链接解析本身构成依赖，目标已改变"
                        "（即使最终内容相同也保守失效）")
            return None
        for d, n, o, action in _member_actions(e):
            hit = self._match_member(ev, e, d, n, o, action)
            if hit is not None:
                return hit
        return None

    def _match_member(self, ev: Evidence, e: dict,
                      d: int, n: str, o: int, action: str):
        for dep in ev.path_deps + ev.negatives:
            if e["seq"] <= dep["seq"]:
                continue
            for st in dep["steps"]:
                if st.get("dir") != d or st.get("name") != n:
                    continue
                if "obj" in st and action == "remove":
                    return (f"解析 {dep['path']} 时 d{d} 中 {n!r} 绑定到o{st['obj']}",
                            "该目录成员映射已被移除/改名")
                if "obj" in st and action == "add" and st["obj"] != o:
                    return (f"解析 {dep['path']} 时 d{d} 中 {n!r} 绑定到o{st['obj']}",
                            f"该名字现绑定到另一对象o{o}")
                if st.get("error") == "not_found" and action == "add":
                    return (f"查询 {dep['path']} 时 d{d} 中不存在 {n!r}",
                            "缺失名已被创建，失败查询的结果不再成立")
        for en in ev.enums:
            if en["dir"] != d or e["seq"] <= en["seq"]:
                continue
            if en["mode"] == "prefix":
                return (f"seq={en['seq']} 仅消费了目录d{d}的枚举前缀",
                        "前缀消费保守依赖整个成员集合，成员集合已变化")
            members = en.get("members", [])
            if action == "add" and n not in members:
                return (f"seq={en['seq']} 完整枚举目录d{d}得到{members}",
                        f"新增成员 {n!r}，完整枚举结果会不同")
            if action == "remove" and n in members:
                return (f"seq={en['seq']} 完整枚举目录d{d}得到{members}",
                        f"成员 {n!r} 被移除，完整枚举结果会不同")
        return None

    # ---- 查询库 ---------------------------------------------------------

    def verdict(self, task: str) -> Verdict:
        if task in self.verdicts:
            return self.verdicts[task]
        if task not in self.evidence:
            return Verdict(task, INSUFFICIENT, [],
                           reason=f"没有任务{task}的依赖证据（未摄入或已删除）")
        self.evaluate()
        return self.verdicts[task]

    def explain(self, task: str) -> str:
        v = self.verdict(task)
        lines = [f"[{v.status.upper()}] 任务 {task}"]
        lines += [f"  {i + 1}. {c}" for i, c in enumerate(v.chain)]
        if v.reason:
            lines.append(f"  证据不足原因: {v.reason}")
        return "\n".join(lines)
