"""轨迹记录格式（可 JSON 序列化、可持久保存）。

每条记录是一个 dict，公共字段：
  seq:  int        全局递增序号（跨任务与环境变更统一编号）
  task: str | None 任务标识；环境变更操作为 None
  op:   str        操作名

任务侧只读操作：
  open   成功: path, resolution=[step...], handle, obj, version
         失败: path, resolution=[step...], error
  read   handle, obj, version, content        （按句柄读，绑定对象而非路径）
  lookup 同 open（无 handle/version），用于纯查询（含失败查询）
  enum   path, resolution, obj, mode="full"|"prefix", members=[名字...]
         mode="prefix" 表示只消费了前缀，保守依赖整个目录成员集合

resolution step（解析链，记录实际解析过的对象，而非最终字符串）：
  {"dir": oid, "name": str, "obj": oid}      成功解析一个分量
  {"symlink": oid, "target": str}            符号链接跳转（解析本身构成依赖）
  {"dir": oid, "name": str, "error": kind}   失败分量（必为最后一步）
  error kind: "not_found" | "perm_denied" | "not_a_dir" | "is_a_dir" | "symlink_loop"
  （读权限错误与不存在严格分开表示）

环境变更操作（驱动失效判定）：
  write      path, obj, old_version, new_version   内容版本递增
  create     path, dir, name, obj, version         新建文件
  mkdir      path, dir, name, obj
  rename     src, dst, src_dir, src_name, dst_dir, dst_name, obj
  link       src, dst, dir, name, obj              硬链接（共享对象）
  symlink    path, dir, name, obj, target
  set_target path, obj, old, new                   修改符号链接目标
  unlink     path, dir, name, obj
  chmod      path, obj, old, new                   读权限变化
  external   path, note                            未建模外部变更（证据不足的唯一来源）
"""
from __future__ import annotations

import json

TASK_OPS = ("open", "read", "lookup", "enum")
MUTATION_OPS = (
    "write", "create", "mkdir", "rename", "link", "symlink",
    "set_target", "unlink", "chmod", "external",
)

ERR_NOT_FOUND = "not_found"
ERR_PERM = "perm_denied"
ERR_NOT_DIR = "not_a_dir"
ERR_IS_DIR = "is_a_dir"
ERR_LOOP = "symlink_loop"


class TraceLog:
    """带序号的操作轨迹，可序列化为 JSON Lines。"""

    def __init__(self) -> None:
        self.records: list[dict] = []

    def log(self, task, **fields) -> dict:
        rec = {"seq": len(self.records), "task": task}
        rec.update(fields)
        self.records.append(rec)
        return rec

    def to_jsonl(self) -> str:
        return "\n".join(json.dumps(r, sort_keys=True) for r in self.records) + "\n"

    @staticmethod
    def from_jsonl(text: str) -> "TraceLog":
        log = TraceLog()
        for line in text.splitlines():
            line = line.strip()
            if line:
                log.records.append(json.loads(line))
        for i, rec in enumerate(log.records):
            assert rec["seq"] == i, "轨迹序号必须连续"
        return log
