"""合成虚拟文件系统：执行操作并发射带序号的轨迹记录。

核心语义：路径不是文件身份。
- 成功 open 绑定稳定对象 ID 与内容版本；之后改名/删除路径不影响已打开句柄。
- 硬链接共享同一对象；符号链接解析本身构成依赖。
- 失败查询记录实际解析过的父目录链与缺失名（含错误类别），而非最终字符串。
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .trace import (
    ERR_IS_DIR,
    ERR_LOOP,
    ERR_NOT_DIR,
    ERR_NOT_FOUND,
    ERR_PERM,
    TraceLog,
)

KIND_FILE = "file"
KIND_DIR = "dir"
KIND_SYMLINK = "symlink"

_MAX_SYMLINK_HOPS = 16


class FSError(Exception):
    def __init__(self, kind: str, steps: list[dict] | None = None):
        super().__init__(kind)
        self.kind = kind
        self.steps = steps or []


@dataclass
class Obj:
    oid: int
    kind: str
    readable: bool = True
    content: str = ""          # file
    version: int = 1           # file / symlink 内容版本
    target: str = ""           # symlink
    entries: dict = field(default_factory=dict)  # dir: name -> oid
    nlink: int = 1


class VFS:
    def __init__(self, trace: TraceLog | None = None) -> None:
        self.trace = trace if trace is not None else TraceLog()
        self.current_task: str | None = None
        self.objects: dict[int, Obj] = {}
        self.root = self._new_obj(KIND_DIR)
        self.handles: dict[int, int] = {}   # handle -> oid
        self._next_handle = 0

    # ---- 内部工具 -------------------------------------------------------

    def _new_obj(self, kind: str, **kw) -> Obj:
        oid = max(self.objects, default=-1) + 1
        obj = Obj(oid=oid, kind=kind, **kw)
        self.objects[oid] = obj
        return obj

    @staticmethod
    def _split(path: str) -> list[str]:
        if not path.startswith("/"):
            raise ValueError(f"仅支持绝对路径: {path!r}")
        return [c for c in path.split("/") if c]

    def _resolve(self, path: str, follow_final: bool = True):
        """解析路径，返回 (obj, steps)；失败抛 FSError(kind, steps)。"""
        steps: list[dict] = []
        comps = self._split(path)
        obj = self.root
        i = 0
        hops = 0
        while True:
            while i < len(comps):
                name = comps[i]
                if obj.kind != KIND_DIR:
                    raise FSError(ERR_NOT_DIR, steps + [
                        {"dir": obj.oid, "name": name, "error": ERR_NOT_DIR}])
                if not obj.readable:
                    raise FSError(ERR_PERM, steps + [
                        {"dir": obj.oid, "name": name, "error": ERR_PERM}])
                child_oid = obj.entries.get(name)
                if child_oid is None:
                    raise FSError(ERR_NOT_FOUND, steps + [
                        {"dir": obj.oid, "name": name, "error": ERR_NOT_FOUND}])
                child = self.objects[child_oid]
                steps.append({"dir": obj.oid, "name": name, "obj": child.oid})
                i += 1
                if child.kind == KIND_SYMLINK and i < len(comps):
                    hops += 1
                    if hops > _MAX_SYMLINK_HOPS:
                        raise FSError(ERR_LOOP, steps + [
                            {"symlink": child.oid, "target": child.target,
                             "error": ERR_LOOP}])
                    steps.append({"symlink": child.oid, "target": child.target})
                    comps = self._split(child.target) + comps[i:]
                    obj, i = self.root, 0
                else:
                    obj = child
            if follow_final and obj.kind == KIND_SYMLINK:
                hops += 1
                if hops > _MAX_SYMLINK_HOPS:
                    raise FSError(ERR_LOOP, steps + [
                        {"symlink": obj.oid, "target": obj.target,
                         "error": ERR_LOOP}])
                steps.append({"symlink": obj.oid, "target": obj.target})
                comps = self._split(obj.target)
                obj, i = self.root, 0
                continue
            return obj, steps

    def _resolve_parent(self, path: str):
        comps = self._split(path)
        if not comps:
            raise FSError(ERR_NOT_DIR, [])
        parent_path = "/" + "/".join(comps[:-1])
        parent, _ = self._resolve(parent_path if comps[:-1] else "/")
        if parent.kind != KIND_DIR:
            raise FSError(ERR_NOT_DIR, [])
        if not parent.readable:
            raise FSError(ERR_PERM, [])
        return parent, comps[-1]

    # ---- 任务侧只读操作 -------------------------------------------------

    def open(self, path: str) -> int:
        rec = {"op": "open", "path": path}
        try:
            obj, steps = self._resolve(path)
        except FSError as e:
            rec.update(resolution=e.steps, error=e.kind)
            self.trace.log(self.current_task, **rec)
            raise
        rec["resolution"] = steps
        if obj.kind == KIND_DIR:
            rec["error"] = ERR_IS_DIR
            self.trace.log(self.current_task, **rec)
            raise FSError(ERR_IS_DIR, steps)
        if not obj.readable:
            rec.update(obj=obj.oid, error=ERR_PERM)
            self.trace.log(self.current_task, **rec)
            raise FSError(ERR_PERM, steps)
        handle = self._next_handle
        self._next_handle += 1
        self.handles[handle] = obj.oid
        rec.update(handle=handle, obj=obj.oid, version=obj.version)
        self.trace.log(self.current_task, **rec)
        return handle

    def read(self, handle: int) -> str:
        obj = self.objects[self.handles[handle]]
        self.trace.log(self.current_task, op="read", handle=handle,
                       obj=obj.oid, version=obj.version, content=obj.content)
        return obj.content

    def lookup(self, path: str) -> int:
        rec = {"op": "lookup", "path": path}
        try:
            obj, steps = self._resolve(path)
        except FSError as e:
            rec.update(resolution=e.steps, error=e.kind)
            self.trace.log(self.current_task, **rec)
            raise
        rec.update(resolution=steps, obj=obj.oid)
        self.trace.log(self.current_task, **rec)
        return obj.oid

    def enum(self, path: str, limit: int | None = None) -> list[str]:
        rec = {"op": "enum", "path": path}
        try:
            obj, steps = self._resolve(path)
        except FSError as e:
            rec.update(resolution=e.steps, error=e.kind)
            self.trace.log(self.current_task, **rec)
            raise
        rec["resolution"] = steps
        if obj.kind != KIND_DIR:
            rec["error"] = ERR_NOT_DIR
            self.trace.log(self.current_task, **rec)
            raise FSError(ERR_NOT_DIR, steps)
        if not obj.readable:
            rec.update(obj=obj.oid, error=ERR_PERM)
            self.trace.log(self.current_task, **rec)
            raise FSError(ERR_PERM, steps)
        names = sorted(obj.entries)
        if limit is not None and limit < len(names):
            mode, names = "prefix", names[:limit]
        else:
            mode = "full"
        rec.update(obj=obj.oid, mode=mode, members=list(names))
        self.trace.log(self.current_task, **rec)
        return list(names)

    # ---- 环境变更操作 ---------------------------------------------------

    def write(self, path: str, content: str) -> None:
        obj, _ = self._resolve(path)
        if obj.kind != KIND_FILE:
            raise FSError(ERR_IS_DIR)
        old = obj.version
        obj.content = content
        obj.version += 1
        self.trace.log(self.current_task, op="write", path=path, obj=obj.oid,
                       old_version=old, new_version=obj.version)

    def create(self, path: str, content: str = "") -> int:
        parent, name = self._resolve_parent(path)
        if name in parent.entries:
            raise FSError("exists")
        obj = self._new_obj(KIND_FILE, content=content)
        parent.entries[name] = obj.oid
        self.trace.log(self.current_task, op="create", path=path,
                       dir=parent.oid, name=name, obj=obj.oid, version=obj.version)
        return obj.oid

    def mkdir(self, path: str) -> int:
        parent, name = self._resolve_parent(path)
        if name in parent.entries:
            raise FSError("exists")
        obj = self._new_obj(KIND_DIR)
        parent.entries[name] = obj.oid
        self.trace.log(self.current_task, op="mkdir", path=path,
                       dir=parent.oid, name=name, obj=obj.oid)
        return obj.oid

    def rename(self, src: str, dst: str) -> None:
        src_parent, src_name = self._resolve_parent(src)
        dst_parent, dst_name = self._resolve_parent(dst)
        oid = src_parent.entries.get(src_name)
        if oid is None:
            raise FSError(ERR_NOT_FOUND)
        if dst_name in dst_parent.entries:
            raise FSError("exists")
        del src_parent.entries[src_name]
        dst_parent.entries[dst_name] = oid
        self.trace.log(self.current_task, op="rename", src=src, dst=dst,
                       src_dir=src_parent.oid, src_name=src_name,
                       dst_dir=dst_parent.oid, dst_name=dst_name, obj=oid)

    def link(self, src: str, dst: str) -> None:
        obj, _ = self._resolve(src)
        if obj.kind == KIND_DIR:
            raise FSError(ERR_IS_DIR)
        dst_parent, dst_name = self._resolve_parent(dst)
        if dst_name in dst_parent.entries:
            raise FSError("exists")
        dst_parent.entries[dst_name] = obj.oid
        obj.nlink += 1
        self.trace.log(self.current_task, op="link", src=src, dst=dst,
                       dir=dst_parent.oid, name=dst_name, obj=obj.oid)

    def symlink(self, path: str, target: str) -> int:
        parent, name = self._resolve_parent(path)
        if name in parent.entries:
            raise FSError("exists")
        obj = self._new_obj(KIND_SYMLINK, target=target)
        parent.entries[name] = obj.oid
        self.trace.log(self.current_task, op="symlink", path=path,
                       dir=parent.oid, name=name, obj=obj.oid, target=target)
        return obj.oid

    def set_target(self, path: str, target: str) -> None:
        obj, _ = self._resolve(path, follow_final=False)
        if obj.kind != KIND_SYMLINK:
            raise FSError(ERR_NOT_DIR)
        old = obj.target
        obj.target = target
        obj.version += 1
        self.trace.log(self.current_task, op="set_target", path=path,
                       obj=obj.oid, old=old, new=target)

    def unlink(self, path: str) -> None:
        parent, name = self._resolve_parent(path)
        oid = parent.entries.get(name)
        if oid is None:
            raise FSError(ERR_NOT_FOUND)
        obj = self.objects[oid]
        if obj.kind == KIND_DIR and obj.entries:
            raise FSError("not_empty")
        del parent.entries[name]
        obj.nlink -= 1
        self.trace.log(self.current_task, op="unlink", path=path,
                       dir=parent.oid, name=name, obj=oid)

    def chmod(self, path: str, readable: bool) -> None:
        obj, _ = self._resolve(path, follow_final=False)
        old = obj.readable
        obj.readable = readable
        self.trace.log(self.current_task, op="chmod", path=path,
                       obj=obj.oid, old=old, new=readable)

    # ---- 测试/演示辅助 ---------------------------------------------------

    def populate(self, spec: dict, base: str = "") -> None:
        """用嵌套 dict 快速建初始结构：str=文件内容, dict=目录, ("->",t)=符号链接。"""
        for name, val in spec.items():
            path = f"{base}/{name}" if base else f"/{name}"
            if isinstance(val, dict):
                self.mkdir(path)
                self.populate(val, path)
            elif isinstance(val, tuple) and val[0] == "->":
                self.symlink(path, val[1])
            else:
                self.create(path, val)
