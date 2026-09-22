"""依赖证据：把任务轨迹段压缩成可持久保存的依赖集合。

压缩规则：
- 内容读按对象合并为 {obj: [(version, seq), ...]}；同一对象观察到多个版本
  即“不一致快照”，证据标记 inconsistent，永远不能批准缓存。
- 路径解析按完整 step 链去重；符号链接跳转、目录读权限、缺失名分别入表。
- 目录枚举区分 full（记录成员名集合）与 prefix（保守依赖整个成员集合）。
"""
from __future__ import annotations

import json
from dataclasses import dataclass, field

from .trace import ERR_PERM


@dataclass
class Evidence:
    task: str
    # 成功解析：{"path", "seq", "steps":[...]}
    path_deps: list[dict] = field(default_factory=list)
    # 失败查询：{"path", "seq", "steps":[...含失败步], "error", "obj"?}
    negatives: list[dict] = field(default_factory=list)
    # 内容读：obj -> [[version, seq], ...]
    content: dict[int, list[list[int]]] = field(default_factory=dict)
    # 目录枚举：{"dir", "mode", "members"?, "seq"}
    enums: list[dict] = field(default_factory=list)
    # 符号链接目标：obj -> [target, seq]
    symlinks: dict[int, list] = field(default_factory=dict)
    # 读权限观察：obj -> [readable, seq]
    perms: dict[int, list] = field(default_factory=dict)
    inconsistent: bool = False

    # ---- 序列化（证据恢复） ---------------------------------------------

    def to_dict(self) -> dict:
        return {
            "task": self.task,
            "path_deps": self.path_deps,
            "negatives": self.negatives,
            "content": {str(k): v for k, v in self.content.items()},
            "enums": self.enums,
            "symlinks": {str(k): v for k, v in self.symlinks.items()},
            "perms": {str(k): v for k, v in self.perms.items()},
            "inconsistent": self.inconsistent,
        }

    @staticmethod
    def from_dict(d: dict) -> "Evidence":
        return Evidence(
            task=d["task"],
            path_deps=d["path_deps"],
            negatives=d["negatives"],
            content={int(k): v for k, v in d["content"].items()},
            enums=d["enums"],
            symlinks={int(k): v for k, v in d["symlinks"].items()},
            perms={int(k): v for k, v in d["perms"].items()},
            inconsistent=d["inconsistent"],
        )


def _note_perm(ev: Evidence, oid: int, readable: bool, seq: int) -> None:
    prev = ev.perms.get(oid)
    if prev is None or prev[1] <= seq:
        ev.perms[oid] = [readable, seq]


def _note_resolution(ev: Evidence, rec: dict) -> None:
    """把一次解析（成功或失败）的 step 链拆成各类依赖。"""
    seq = rec["seq"]
    steps = rec.get("resolution", [])
    failing = steps[-1] if steps and "error" in steps[-1] else None
    for st in steps:
        if "dir" in st:
            if st is failing and st.get("error") == ERR_PERM:
                continue  # 该目录当时不可读，由下方负依赖记录 False
            _note_perm(ev, st["dir"], True, seq)  # 能搜到该目录 => 当时可读
        if "symlink" in st and "error" not in st:
            ev.symlinks[st["symlink"]] = [st["target"], seq]
    if rec.get("error"):
        ev.negatives.append({
            "path": rec["path"], "seq": seq, "steps": steps,
            "error": rec["error"], "obj": rec.get("obj"),
        })
        if rec["error"] == ERR_PERM:
            if failing and "dir" in failing:
                _note_perm(ev, failing["dir"], False, seq)
            if rec.get("obj") is not None:
                _note_perm(ev, rec["obj"], False, seq)
    else:
        ev.path_deps.append({"path": rec["path"], "seq": seq, "steps": steps})


def build_evidence(task: str, records: list[dict]) -> Evidence:
    """从该任务的轨迹记录（已按 seq 排序）压缩出依赖证据。"""
    ev = Evidence(task=task)
    seen_paths: set[tuple] = set()
    for rec in records:
        op = rec["op"]
        if op in ("open", "lookup", "enum"):
            key = (op, rec["path"], rec.get("error"),
                   json.dumps(rec.get("resolution", []), sort_keys=True))
            if key not in seen_paths:
                seen_paths.add(key)
                _note_resolution(ev, rec)
            if rec.get("error"):
                continue
            if op == "open":
                ev.content.setdefault(rec["obj"], [])
                pair = [rec["version"], rec["seq"]]
                if pair not in ev.content[rec["obj"]]:
                    ev.content[rec["obj"]].append(pair)
                _note_perm(ev, rec["obj"], True, rec["seq"])
            elif op == "enum":
                dep = {"dir": rec["obj"], "mode": rec["mode"], "seq": rec["seq"]}
                if rec["mode"] == "full":
                    dep["members"] = list(rec["members"])
                ev.enums.append(dep)
                _note_perm(ev, rec["obj"], True, rec["seq"])
        elif op == "read":
            ev.content.setdefault(rec["obj"], [])
            pair = [rec["version"], rec["seq"]]
            if pair not in ev.content[rec["obj"]]:
                ev.content[rec["obj"]].append(pair)
    for versions in ev.content.values():
        if len({v for v, _ in versions}) > 1:
            ev.inconsistent = True
    return ev


def save_evidence(path: str, evidence: dict[str, Evidence]) -> None:
    payload = {task: ev.to_dict() for task, ev in evidence.items()}
    with open(path, "w", encoding="utf-8") as f:
        json.dump(payload, f, ensure_ascii=False, indent=1, sort_keys=True)


def load_evidence(path: str) -> dict[str, Evidence]:
    with open(path, encoding="utf-8") as f:
        payload = json.load(f)
    return {task: Evidence.from_dict(d) for task, d in payload.items()}
