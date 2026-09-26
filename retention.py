#!/usr/bin/env python3
"""分层保留策略删除计划（Hierarchical retention deletion planner）。

约束模型：
  - 文件可删除时间 = created_at + max(目录继承下界, 文件自有下界)
  - 目录继承下界 = 沿目录链向上最近一个设置了 retention_days 的目录的值（未设置则为 0）
  - 法律保留（legal hold）：文件自身或任一祖先目录带标记 => 可删除时间为无限（永不）
  - 目录移动后，子树内所有文件的继承约束被重新计算并重建索引
  - 计划基于 (file_id, version) 快照；执行时版本变化 => 跳过并给出理由
  - 最早可删除索引：最小堆 (deletable_at, epoch, file_id)，时间推进只弹出到期项，不全量扫描

仅使用 Python 3.11 标准库。
"""

from __future__ import annotations

import argparse
import heapq
import json
import sys
from dataclasses import dataclass


class RetentionError(Exception):
    """结构或约束错误（如目录移动成环、引用不存在的对象）。"""


@dataclass
class Directory:
    id: str
    parent: str | None
    retention_days: int | None = None
    legal_hold: bool = False


@dataclass
class FileRecord:
    id: str
    dir_id: str
    created_at: int
    retention_days: int = 0
    legal_hold: bool = False
    version: int = 1


class RetentionStore:
    """目录树 + 文件 + 最早可删除索引。"""

    def __init__(self) -> None:
        self.directories: dict[str, Directory] = {}
        self.files: dict[str, FileRecord] = {}
        self._children: dict[str, set[str]] = {}
        self._dir_files: dict[str, set[str]] = {}
        # 最早可删除索引：最小堆，惰性删除（epoch 过期即跳过）
        self._heap: list[tuple[int, int, str]] = []
        self._epoch: dict[str, int] = {}
        self.stats = {"heap_pops": 0, "stale_skips": 0}

    # ---------- 结构操作 ----------

    def add_directory(self, dir_id: str, parent: str | None,
                      retention_days: int | None = None,
                      legal_hold: bool = False) -> None:
        if dir_id in self.directories:
            raise RetentionError(f"duplicate directory: {dir_id}")
        self.directories[dir_id] = Directory(dir_id, parent, retention_days, legal_hold)
        if parent is not None:
            self._children.setdefault(parent, set()).add(dir_id)

    def add_file(self, file_id: str, dir_id: str, created_at: int,
                 retention_days: int = 0, legal_hold: bool = False,
                 version: int = 1) -> None:
        if file_id in self.files:
            raise RetentionError(f"duplicate file: {file_id}")
        if dir_id not in self.directories:
            raise RetentionError(f"unknown directory: {dir_id}")
        self.files[file_id] = FileRecord(file_id, dir_id, created_at,
                                         retention_days, legal_hold, version)
        self._dir_files.setdefault(dir_id, set()).add(file_id)
        self._reindex_file(file_id)

    def move_file(self, file_id: str, new_dir: str) -> None:
        f = self._file(file_id)
        if new_dir not in self.directories:
            raise RetentionError(f"unknown directory: {new_dir}")
        self._dir_files[f.dir_id].discard(file_id)
        f.dir_id = new_dir
        self._dir_files.setdefault(new_dir, set()).add(file_id)
        self._reindex_file(file_id)

    def move_dir(self, dir_id: str, new_parent: str | None) -> None:
        d = self._dir(dir_id)
        if new_parent is not None:
            self._dir(new_parent)
            if self._is_ancestor(dir_id, new_parent) or new_parent == dir_id:
                raise RetentionError(
                    f"move would create a cycle: {dir_id} -> {new_parent}")
        if d.parent is not None:
            self._children[d.parent].discard(dir_id)
        d.parent = new_parent
        if new_parent is not None:
            self._children.setdefault(new_parent, set()).add(dir_id)
        # 继承约束变化：重算整个子树
        self._reindex_subtree(dir_id)

    def set_legal_hold(self, file_id: str, flag: bool) -> None:
        self._file(file_id).legal_hold = flag
        self._reindex_file(file_id)

    def set_dir_legal_hold(self, dir_id: str, flag: bool) -> None:
        self._dir(dir_id).legal_hold = flag
        self._reindex_subtree(dir_id)

    def modify_file(self, file_id: str) -> None:
        """模拟文件被修改：版本号 +1（不改变可删除时间）。"""
        self._file(file_id).version += 1

    # ---------- 约束计算 ----------

    def _inherited_bound(self, dir_id: str) -> int:
        """沿目录链向上，取最近一个设置了 retention_days 的目录；均无则为 0。"""
        seen: set[str] = set()
        cur: str | None = dir_id
        while cur is not None:
            if cur in seen:
                raise RetentionError(f"directory cycle detected at: {cur}")
            seen.add(cur)
            d = self._dir(cur)
            if d.retention_days is not None:
                return d.retention_days
            cur = d.parent
        return 0

    def _has_legal_hold(self, f: FileRecord) -> bool:
        if f.legal_hold:
            return True
        seen: set[str] = set()
        cur: str | None = f.dir_id
        while cur is not None:
            if cur in seen:
                raise RetentionError(f"directory cycle detected at: {cur}")
            seen.add(cur)
            d = self._dir(cur)
            if d.legal_hold:
                return True
            cur = d.parent
        return False

    def effective_bound_days(self, file_id: str) -> int:
        f = self._file(file_id)
        return max(self._inherited_bound(f.dir_id), f.retention_days)

    def deletable_at(self, file_id: str) -> int | None:
        """返回最早可删除的绝对时间；法律保留 => None（无限）。"""
        f = self._file(file_id)
        if self._has_legal_hold(f):
            return None
        return f.created_at + self.effective_bound_days(file_id)

    # ---------- 最早可删除索引 ----------

    def _reindex_file(self, file_id: str) -> None:
        epoch = self._epoch.get(file_id, 0) + 1
        self._epoch[file_id] = epoch
        t = self.deletable_at(file_id)
        if t is not None:
            heapq.heappush(self._heap, (t, epoch, file_id))

    def _reindex_subtree(self, dir_id: str) -> None:
        stack = [dir_id]
        while stack:
            d = stack.pop()
            for fid in self._dir_files.get(d, ()):
                self._reindex_file(fid)
            stack.extend(self._children.get(d, ()))

    def due_files(self, now: int) -> list[FileRecord]:
        """返回 now 时刻全部到期文件。只弹出堆顶到期项，不做全量扫描。"""
        due: list[tuple[int, int, str]] = []
        while self._heap and self._heap[0][0] <= now:
            entry = heapq.heappop(self._heap)
            self.stats["heap_pops"] += 1
            if entry[1] != self._epoch.get(entry[2]):
                self.stats["stale_skips"] += 1
                continue
            due.append(entry)
        for entry in due:  # 查询非破坏性：弹出的到期项放回堆中
            heapq.heappush(self._heap, entry)
        return [self.files[fid] for _, _, fid in due]

    # ---------- 计划与执行 ----------

    def build_plan(self, now: int) -> dict:
        """基于当前版本快照生成删除计划。"""
        entries = []
        for f in sorted(self.due_files(now), key=lambda r: r.id):
            entries.append({
                "file_id": f.id,
                "version": f.version,
                "deletable_at": self.deletable_at(f.id),
            })
        return {"generated_at": now, "entries": entries}

    def execute_plan(self, plan: dict, now: int) -> dict:
        """执行计划；版本与快照不一致或约束不再满足时跳过并给出理由。"""
        deleted: list[dict] = []
        skipped: list[dict] = []
        for e in plan.get("entries", []):
            fid = e["file_id"]
            f = self.files.get(fid)
            if f is None:
                skipped.append({"file_id": fid, "reason": "file no longer exists"})
                continue
            if f.version != e["version"]:
                skipped.append({
                    "file_id": fid,
                    "planned_version": e["version"],
                    "current_version": f.version,
                    "reason": "version changed since plan snapshot",
                })
                continue
            if self._has_legal_hold(f):
                skipped.append({"file_id": fid, "reason": "legal hold active"})
                continue
            t = self.deletable_at(fid)
            if t is None or t > now:
                skipped.append({
                    "file_id": fid,
                    "reason": f"retention constraint not satisfied at time {now}",
                })
                continue
            deleted.append({"file_id": fid, "version": f.version})
        return {"executed_at": now, "deleted": deleted, "skipped": skipped}

    # ---------- 辅助 ----------

    def _file(self, file_id: str) -> FileRecord:
        try:
            return self.files[file_id]
        except KeyError:
            raise RetentionError(f"unknown file: {file_id}") from None

    def _dir(self, dir_id: str) -> Directory:
        try:
            return self.directories[dir_id]
        except KeyError:
            raise RetentionError(f"unknown directory: {dir_id}") from None

    def _is_ancestor(self, maybe_ancestor: str, dir_id: str) -> bool:
        cur = self.directories[dir_id].parent
        while cur is not None:
            if cur == maybe_ancestor:
                return True
            cur = self.directories[cur].parent
        return False


# ---------- 场景加载与 CLI ----------

def load_scenario(path: str) -> tuple[RetentionStore, dict]:
    with open(path, "r", encoding="utf-8") as fh:
        data = json.load(fh)
    store = RetentionStore()
    for d in data.get("directories", []):
        store.add_directory(d["id"], d.get("parent"),
                            d.get("retention_days"), d.get("legal_hold", False))
    for f in data.get("files", []):
        store.add_file(f["id"], f["dir"], f["created_at"],
                       f.get("retention_days", 0), f.get("legal_hold", False),
                       f.get("version", 1))
    for op in data.get("operations", []):
        apply_operation(store, op)
    return store, data


def apply_operation(store: RetentionStore, op: dict) -> None:
    kind = op["op"]
    if kind == "move_dir":
        store.move_dir(op["dir"], op.get("new_parent"))
    elif kind == "move_file":
        store.move_file(op["file"], op["dir"])
    elif kind == "set_legal_hold":
        store.set_legal_hold(op["file"], op["flag"])
    elif kind == "set_dir_legal_hold":
        store.set_dir_legal_hold(op["dir"], op["flag"])
    elif kind == "modify_file":
        store.modify_file(op["file"])
    else:
        raise RetentionError(f"unknown operation: {kind}")


def _dump(obj: dict) -> None:
    json.dump(obj, sys.stdout, ensure_ascii=False, indent=2)
    sys.stdout.write("\n")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="retention",
        description="分层保留策略删除计划：plan 生成计划，execute 执行计划")
    sub = parser.add_subparsers(dest="command", required=True)

    p_plan = sub.add_parser("plan", help="根据场景文件生成删除计划（JSON）")
    p_plan.add_argument("scenario", help="场景 JSON 文件路径")
    p_plan.add_argument("--now", type=int, default=None,
                        help="当前时间（默认取场景文件中的 now）")

    p_exec = sub.add_parser("execute", help="执行删除计划（JSON）")
    p_exec.add_argument("scenario", help="场景 JSON 文件路径")
    p_exec.add_argument("plan", help="plan 命令输出的计划 JSON 文件路径")
    p_exec.add_argument("--now", type=int, default=None,
                        help="执行时间（默认取场景文件中的 now）")

    args = parser.parse_args(argv)
    try:
        store, data = load_scenario(args.scenario)
        if args.command == "plan":
            now = args.now if args.now is not None else data.get("now", 0)
            _dump(store.build_plan(now))
        else:
            with open(args.plan, "r", encoding="utf-8") as fh:
                plan = json.load(fh)
            now = args.now if args.now is not None else data.get("now", 0)
            _dump(store.execute_plan(plan, now))
    except (RetentionError, OSError, json.JSONDecodeError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
