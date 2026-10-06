"""BuildFSProxy: explainable, race-conscious build-file access proxy.

All filesystem access goes through this object.  The root directory is
captured once at construction as a directory file descriptor; every
operation resolves its path component-by-component from that fd (see
``resolver.py``) and acts via ``dir_fd=`` syscalls.  Nothing here claims to
contain arbitrary processes that bypass the proxy -- it is a choke point
for cooperating build steps, not a sandbox.
"""

from __future__ import annotations

import os
import secrets

from .audit import AuditEvent, ObjectId
from .errors import PolicyDenied, ProxyError
from .hooks import Hooks
from .policy import Policy
from .resolver import Resolver

_CHUNK = 65536


def _join(parent_path, name):
    return name if parent_path == "" else parent_path + "/" + name


def _read_all(fd):
    chunks = []
    while True:
        block = os.read(fd, _CHUNK)
        if not block:
            return b"".join(chunks)
        chunks.append(block)


def _unlink_quiet(dir_fd, name):
    try:
        os.unlink(name, dir_fd=dir_fd)
    except OSError:
        pass


class BuildFSProxy:
    def __init__(self, root, policy: Policy, hooks: Hooks | None = None):
        self._root_fd = os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_CLOEXEC)
        self._hooks = hooks if hooks is not None else Hooks()
        self._resolver = Resolver(self._root_fd, self._hooks)
        self.policy = policy
        self.audit: list[AuditEvent] = []

    def close(self):
        if self._root_fd is not None:
            os.close(self._root_fd)
            self._root_fd = None

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.close()

    # -- public operations -------------------------------------------------

    def read(self, path) -> bytes:
        def op(ev):
            with self._resolver.resolve_file(path) as res:
                ev.resolved = res.path
                self._require(self.policy.can_read(res.path),
                              f"read not permitted for resolved path '{res.path}'")
                data = _read_all(res.fd)
                ev.objects = (self._obj(res.last), self._obj(res.entries[-2]))
                return data
        return self._run("read", path, {}, op)

    def create(self, path, data: bytes = b"", *, mode=0o644):
        """Create a new file; fails (FileExistsError) if the name exists."""
        def op(ev):
            parent, name = self._resolver.resolve_parent(path)
            with parent:
                ev.resolved = _join(parent.path, name)
                self._require(self.policy.can_write(ev.resolved),
                              f"create not permitted for resolved path '{ev.resolved}'")
                fd = os.open(name,
                             os.O_WRONLY | os.O_CREAT | os.O_EXCL
                             | os.O_NOFOLLOW | os.O_CLOEXEC,
                             mode, dir_fd=parent.fd)
                try:
                    self._write_all(fd, data)
                    ev.objects = (self._fstat_obj(fd), self._obj(parent.last))
                finally:
                    os.close(fd)
        self._run("create", path, {}, op)

    def replace(self, path, data: bytes, *, mode=0o644):
        """Atomically replace `path` with `data` via a same-directory temp file.

        A write failure (including an injected one) removes the temp file;
        the previous official output is never left half-written.
        """
        def op(ev):
            parent, name = self._resolver.resolve_parent(path)
            with parent:
                ev.resolved = _join(parent.path, name)
                self._require(self.policy.can_write(ev.resolved),
                              f"replace not permitted for resolved path '{ev.resolved}'")
                tmp = f".{name}.tmp-{os.getpid():x}-{secrets.token_hex(8)}"
                fd = os.open(tmp,
                             os.O_WRONLY | os.O_CREAT | os.O_EXCL
                             | os.O_NOFOLLOW | os.O_CLOEXEC,
                             mode, dir_fd=parent.fd)
                try:
                    try:
                        self._write_chunks(fd, data)
                        os.fsync(fd)
                        new_id = self._fstat_obj(fd)
                    finally:
                        os.close(fd)
                except BaseException:
                    _unlink_quiet(parent.fd, tmp)
                    raise
                self._hooks.before_replace_commit(
                    parent_fd=parent.fd, tmp_name=tmp, final_name=name)
                try:
                    os.rename(tmp, name,
                              src_dir_fd=parent.fd, dst_dir_fd=parent.fd)
                except BaseException:
                    _unlink_quiet(parent.fd, tmp)
                    raise
                ev.objects = (new_id, self._obj(parent.last))
        self._run("replace", path, {}, op)

    def rename(self, src, dst):
        """Rename within the root; needs delete rights on src, create on dst.

        Both policy checks happen before the single rename(2) call, so a
        denied rename changes neither path.
        """
        def op(ev):
            sparent, sname = self._resolver.resolve_parent(src)
            with sparent:
                dparent, dname = self._resolver.resolve_parent(dst)
                with dparent:
                    src_res = _join(sparent.path, sname)
                    dst_res = _join(dparent.path, dname)
                    ev.resolved = src_res
                    ev.extra["resolved_dst"] = dst_res
                    self._require(self.policy.can_write(src_res),
                                  f"rename source delete not permitted for '{src_res}'")
                    self._require(self.policy.can_write(dst_res),
                                  f"rename target create not permitted for '{dst_res}'")
                    self._hooks.before_rename_syscall(
                        src_parent_fd=sparent.fd, src_name=sname,
                        dst_parent_fd=dparent.fd, dst_name=dname)
                    os.rename(sname, dname,
                              src_dir_fd=sparent.fd, dst_dir_fd=dparent.fd)
                    ev.objects = (self._obj(sparent.last), self._obj(dparent.last))
        self._run("rename", src, {"dst": dst}, op)

    def unlink(self, path):
        def op(ev):
            parent, name = self._resolver.resolve_parent(path)
            with parent:
                ev.resolved = _join(parent.path, name)
                self._require(self.policy.can_write(ev.resolved),
                              f"unlink not permitted for resolved path '{ev.resolved}'")
                os.unlink(name, dir_fd=parent.fd)
                ev.objects = (self._obj(parent.last),)
        self._run("unlink", path, {}, op)

    def list(self, path=""):
        """Return the sorted entry names of a directory inside the root."""
        def op(ev):
            with self._resolver.resolve_dir(path) as res:
                ev.resolved = res.path
                self._require(self.policy.can_read(res.path),
                              f"list not permitted for resolved path '{res.path}'")
                names = sorted(os.listdir(res.fd))
                ev.objects = (self._obj(res.last),)
                return names
        return self._run("list", path, {}, op)

    # -- internals -----------------------------------------------------------

    @staticmethod
    def _require(ok, message):
        if not ok:
            raise PolicyDenied(message)

    @staticmethod
    def _obj(entry):
        return ObjectId(entry.dev, entry.ino)

    @staticmethod
    def _fstat_obj(fd):
        st = os.fstat(fd)
        return ObjectId(st.st_dev, st.st_ino)

    def _write_all(self, fd, data):
        view = memoryview(data)
        offset = 0
        while offset < len(view):
            offset += os.write(fd, view[offset:])

    def _write_chunks(self, fd, data):
        view = memoryview(data)
        offset = 0
        while offset < len(view):
            chunk = view[offset:offset + _CHUNK]
            self._hooks.on_write_chunk(fd=fd, offset=offset, chunk=bytes(chunk))
            written = os.write(fd, chunk)
            offset += written

    def _run(self, op_name, path, extra, fn):
        ev = AuditEvent(op=op_name, path=path, extra=dict(extra))
        try:
            result = fn(ev)
            ev.result = "ok"
            return result
        except ProxyError as exc:
            ev.result = "denied"
            ev.reason = f"{type(exc).__name__}: {exc}"
            raise
        except Exception as exc:
            ev.result = "error"
            ev.reason = f"{type(exc).__name__}: {exc}"
            raise
        finally:
            self.audit.append(ev)
