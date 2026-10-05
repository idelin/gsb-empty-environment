"""The build file access proxy.

All file operations go through this class; the root directory is pinned at
construction time as a directory file descriptor and every operation is
performed relative to open dirfds, component by component (see
:mod:`buildproxy.resolver`).  There is intentionally no API that accepts an
absolute path or re-opens a resolved path by string.
"""

from __future__ import annotations

import errno
import itertools
import os
import stat

from . import resolver
from ._sysrename import rename_noreplace
from .audit import AuditEvent
from .errors import (
    EscapeAttempt,
    PolicyDenied,
    ProxyError,
    ResolutionFailed,
    SymlinkLoop,
)

_WRITE_CHUNK = 65536


class FileProxy:
    """Policy-enforcing, race-hardened file access proxy for one root tree.

    Parameters
    ----------
    root:
        Path to the root directory.  Opened once as a dirfd; all later
        operations are relative to that fd.
    policy:
        :class:`buildproxy.policy.Policy` with readable/writable subtrees.
    audit_sink:
        Callable receiving one :class:`AuditEvent` per operation.
    write_hook:
        Optional ``hook(offset, chunk)`` called before each write chunk of
        ``create``/``replace``; raising injects a write failure (used to
        prove that half-written output is never exposed).
    trace_hook:
        Optional ``hook(event, **fields)`` invoked at precise points
        (``component_checked``, ``resolved``, ``final_open``,
        ``replace_commit``, ``rename_commit``) so tests can schedule
        concurrent interleavings with barriers.
    """

    def __init__(
        self,
        root,
        policy,
        *,
        audit_sink=None,
        max_symlink_hops=resolver.DEFAULT_MAX_HOPS,
        max_attempts=16,
        write_hook=None,
        trace_hook=None,
    ):
        self._root_fd = os.open(
            os.fspath(root), os.O_RDONLY | os.O_DIRECTORY | os.O_CLOEXEC
        )
        root_st = os.fstat(self._root_fd)
        self._root_identity = (root_st.st_dev, root_st.st_ino)
        self._policy = policy
        self._audit_sink = audit_sink
        self._max_hops = max_symlink_hops
        self._max_attempts = max_attempts
        self._write_hook = write_hook
        self._trace = trace_hook
        self._tmp_counter = itertools.count()
        self._closed = False

    # -- lifecycle ---------------------------------------------------------

    def close(self) -> None:
        if not self._closed:
            self._closed = True
            os.close(self._root_fd)

    def __enter__(self) -> "FileProxy":
        return self

    def __exit__(self, *exc) -> None:
        self.close()

    # -- internals ----------------------------------------------------------

    def _resolve(self, path, *, follow_final, info, key="resolved"):
        root_st = os.fstat(self._root_fd)
        if (root_st.st_dev, root_st.st_ino) != self._root_identity:
            raise ResolutionFailed("root directory identity changed")
        res = resolver.resolve(
            self._root_fd,
            path,
            follow_final=follow_final,
            max_hops=self._max_hops,
            trace=self._trace,
        )
        info[key] = res.logical_path
        info.setdefault("chain", res.chain)
        if self._trace is not None:
            self._trace("resolved", path=res.logical_path)
        return res

    def _with_retries(self, body):
        attempts = 0
        while True:
            try:
                return body()
            except resolver.RetryResolution:
                attempts += 1
                if attempts >= self._max_attempts:
                    raise ResolutionFailed(
                        "path kept changing concurrently; giving up"
                    )

    def _emit(self, op, path, path2, info, result, reason):
        if self._audit_sink is None:
            return
        self._audit_sink(
            AuditEvent(
                op=op,
                path=path,
                path2=path2,
                resolved=info.get("resolved"),
                resolved2=info.get("resolved2"),
                dev=info.get("dev"),
                ino=info.get("ino"),
                chain=tuple(info.get("chain", ())),
                result=result,
                reason=reason,
            )
        )

    def _audited(self, op, path, path2, body):
        info: dict = {}
        try:
            out = self._with_retries(lambda: body(info))
        except (PolicyDenied, EscapeAttempt, SymlinkLoop) as exc:
            self._emit(op, path, path2, info, "denied", str(exc))
            raise
        except Exception as exc:
            self._emit(
                op, path, path2, info, "error", f"{type(exc).__name__}: {exc}"
            )
            raise
        self._emit(op, path, path2, info, "ok", None)
        return out

    @staticmethod
    def _require_name(res, op):
        if res.name is None:
            raise ValueError(f"{op}: path must name a final component")

    def _open_leaf(self, res, flags, info):
        """Open the resolved final component without following symlinks and
        verify it is still the object we checked."""
        try:
            fd = os.open(res.name, flags | os.O_NOFOLLOW | os.O_CLOEXEC,
                         dir_fd=res.parent_fd)
        except OSError as exc:
            if exc.errno == errno.ELOOP:
                # A symlink was swapped in after resolution; re-resolve.
                raise resolver.RetryResolution(res.name)
            raise
        st = os.fstat(fd)
        if (st.st_dev, st.st_ino) != (
            res.final_stat.st_dev,
            res.final_stat.st_ino,
        ):
            os.close(fd)
            raise resolver.RetryResolution(res.name)
        info["dev"], info["ino"] = st.st_dev, st.st_ino
        return fd, st

    def _write_all(self, fd, data: bytes) -> None:
        view = memoryview(data)
        offset = 0
        while offset < len(view):
            chunk = view[offset : offset + _WRITE_CHUNK]
            if self._write_hook is not None:
                self._write_hook(offset, bytes(chunk))
            offset += os.write(fd, chunk)

    # -- public operations ---------------------------------------------------

    def read(self, path: str) -> bytes:
        """Read a regular file inside a readable subtree (final symlink is
        followed only if it resolves inside the root)."""

        def body(info):
            with self._resolve(path, follow_final=True, info=info) as res:
                if res.name is None:
                    raise IsADirectoryError(errno.EISDIR, "is a directory", path)
                self._policy.require_read(res.parts)
                if self._trace is not None:
                    self._trace("final_open", path=res.logical_path)
                # O_NONBLOCK so a fifo/device swapped in cannot block us;
                # ignored for regular files.
                fd, st = self._open_leaf(res, os.O_RDONLY | os.O_NONBLOCK, info)
                try:
                    if not stat.S_ISREG(st.st_mode):
                        raise IsADirectoryError(
                            errno.EISDIR, "not a regular file", path
                        )
                    chunks = []
                    while True:
                        block = os.read(fd, _WRITE_CHUNK)
                        if not block:
                            break
                        chunks.append(block)
                    return b"".join(chunks)
                finally:
                    os.close(fd)

        return self._audited("read", path, None, body)

    def list(self, path: str = "") -> list[str]:
        """List a directory inside a readable subtree."""

        def body(info):
            with self._resolve(path, follow_final=True, info=info) as res:
                self._policy.require_read(res.parts)
                target = res.name if res.name is not None else "."
                try:
                    fd = os.open(
                        target,
                        os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC,
                        dir_fd=res.parent_fd,
                    )
                except OSError as exc:
                    if exc.errno == errno.ELOOP:
                        raise resolver.RetryResolution(res.name)
                    raise
                try:
                    st = os.fstat(fd)
                    if (st.st_dev, st.st_ino) != (
                        res.final_stat.st_dev,
                        res.final_stat.st_ino,
                    ):
                        raise resolver.RetryResolution(res.name)
                    if not stat.S_ISDIR(st.st_mode):
                        raise NotADirectoryError(
                            errno.ENOTDIR, "not a directory", path
                        )
                    info["dev"], info["ino"] = st.st_dev, st.st_ino
                    return sorted(os.listdir(fd))
                finally:
                    os.close(fd)

        return self._audited("list", path, None, body)

    def create(self, path: str, data: bytes) -> None:
        """Create a new file (fails if the name already exists, including as
        a symlink) inside a writable subtree."""

        def body(info):
            with self._resolve(path, follow_final=False, info=info) as res:
                self._require_name(res, "create")
                self._policy.require_write(res.parts)
                fd = os.open(
                    res.name,
                    os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC,
                    0o644,
                    dir_fd=res.parent_fd,
                )
                created = None
                ok = False
                try:
                    created = os.fstat(fd)
                    self._write_all(fd, data)
                    os.fsync(fd)
                    info["dev"], info["ino"] = created.st_dev, created.st_ino
                    ok = True
                finally:
                    os.close(fd)
                    if not ok and created is not None:
                        # Remove our own partial file, but only if the name
                        # still refers to the exact object we created (it may
                        # have been swapped concurrently).
                        try:
                            cur = os.stat(
                                res.name, dir_fd=res.parent_fd,
                                follow_symlinks=False,
                            )
                            if (cur.st_dev, cur.st_ino) == (
                                created.st_dev,
                                created.st_ino,
                            ):
                                os.unlink(res.name, dir_fd=res.parent_fd)
                        except OSError:
                            pass

        self._audited("create", path, None, body)

    def replace(self, path: str, data: bytes) -> None:
        """Atomically replace ``path`` with ``data``.

        The content is written to a unique temporary file in the *same*
        directory (same pinned dirfd), fsynced, then renamed over the target
        with ``renameat``.  A failure (including an injected one via
        ``write_hook``) removes the temporary file; a half-written file is
        never visible under the official name.  The rename replaces the
        final name itself, so a symlink at ``path`` is replaced, never
        followed.
        """

        def body(info):
            with self._resolve(path, follow_final=False, info=info) as res:
                self._require_name(res, "replace")
                self._policy.require_write(res.parts)
                tmp = ".%s.proxy-tmp-%d-%d" % (
                    res.name,
                    os.getpid(),
                    next(self._tmp_counter),
                )
                fd = os.open(
                    tmp,
                    os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC,
                    0o644,
                    dir_fd=res.parent_fd,
                )
                try:
                    self._write_all(fd, data)
                    os.fsync(fd)
                    st = os.fstat(fd)
                except BaseException:
                    os.close(fd)
                    try:
                        os.unlink(tmp, dir_fd=res.parent_fd)
                    except OSError:
                        pass
                    raise
                os.close(fd)
                if self._trace is not None:
                    self._trace("replace_commit", path=res.logical_path)
                os.replace(
                    tmp,
                    res.name,
                    src_dir_fd=res.parent_fd,
                    dst_dir_fd=res.parent_fd,
                )
                os.fsync(res.parent_fd)
                info["dev"], info["ino"] = st.st_dev, st.st_ino

        self._audited("replace", path, None, body)

    def rename(self, src: str, dst: str) -> None:
        """Atomically move ``src`` to ``dst`` (no clobber).

        Requires *delete* permission on the source (writable subtree) and
        *create* permission on the target (writable subtree).  Uses
        ``renameat2(RENAME_NOREPLACE)`` so a target that appears
        concurrently causes ``FileExistsError`` instead of being silently
        overwritten; a rejected rename changes neither path.
        """

        def body(info):
            with self._resolve(src, follow_final=False, info=info) as rsrc:
                self._require_name(rsrc, "rename")
                with self._resolve(
                    dst, follow_final=False, info=info, key="resolved2"
                ) as rdst:
                    self._require_name(rdst, "rename")
                    self._policy.require_write(rsrc.parts)
                    self._policy.require_write(rdst.parts)
                    if rsrc.final_stat is None:
                        raise FileNotFoundError(
                            errno.ENOENT, os.strerror(errno.ENOENT), src
                        )
                    if self._trace is not None:
                        self._trace(
                            "rename_commit",
                            src=rsrc.logical_path,
                            dst=rdst.logical_path,
                        )
                    rename_noreplace(
                        rsrc.name, rsrc.parent_fd, rdst.name, rdst.parent_fd
                    )

        self._audited("rename", src, dst, body)

    def unlink(self, path: str) -> None:
        """Remove a name inside a writable subtree.  Removes a symlink
        itself; never follows it."""

        def body(info):
            with self._resolve(path, follow_final=False, info=info) as res:
                self._require_name(res, "unlink")
                self._policy.require_write(res.parts)
                os.unlink(res.name, dir_fd=res.parent_fd)

        self._audited("unlink", path, None, body)
