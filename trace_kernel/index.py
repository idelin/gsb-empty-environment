"""反向索引：依赖键 -> 任务集合。

键类型：
  ("member", dir_oid, name)  目录成员映射（路径解析步、缺失名）
  ("content", obj_oid)       内容版本读
  ("enum", dir_oid)          目录枚举
  ("symlink", obj_oid)       符号链接目标
  ("perm", obj_oid)          读权限

索引支持按任务整体增删（重算/删除任务不得残留旧依赖），并记录访问日志，
使得无关子树的变更只触碰受影响的索引项、只重算受影响任务。
"""
from __future__ import annotations

from .evidence import Evidence

Key = tuple


def evidence_keys(ev: Evidence) -> set[Key]:
    keys: set[Key] = set()
    for dep in ev.path_deps + ev.negatives:
        for st in dep["steps"]:
            if "dir" in st and "name" in st:
                keys.add(("member", st["dir"], st["name"]))
            if "symlink" in st:
                keys.add(("symlink", st["symlink"]))
    for oid in ev.content:
        keys.add(("content", oid))
    for e in ev.enums:
        keys.add(("enum", e["dir"]))
    for oid in ev.symlinks:
        keys.add(("symlink", oid))
    for oid in ev.perms:
        keys.add(("perm", oid))
    return keys


class ReverseIndex:
    def __init__(self, record_access: bool = False) -> None:
        self._map: dict[Key, set[str]] = {}
        self._task_keys: dict[str, set[Key]] = {}
        self.record_access = record_access
        self.access_log: list[Key] = []

    def add(self, task: str, keys: set[Key]) -> None:
        self.remove(task)
        self._task_keys[task] = set(keys)
        for k in keys:
            self._map.setdefault(k, set()).add(task)

    def remove(self, task: str) -> None:
        for k in self._task_keys.pop(task, ()):
            holders = self._map.get(k)
            if holders is not None:
                holders.discard(task)
                if not holders:
                    del self._map[k]

    def lookup(self, key: Key) -> set[str]:
        if self.record_access:
            self.access_log.append(key)
        return set(self._map.get(key, ()))

    def tasks_for(self, keys: set[Key]) -> set[str]:
        out: set[str] = set()
        for k in keys:
            out |= self.lookup(k)
        return out

    def tasks(self) -> set[str]:
        return set(self._task_keys)

    def keys_of(self, task: str) -> set[Key]:
        return set(self._task_keys.get(task, ()))
