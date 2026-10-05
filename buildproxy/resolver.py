"""Component-by-component path resolution anchored at a root dirfd.

Design rules (this module never resolves a path by string and then re-opens
it by that string):

* Traversal starts from the root *directory file descriptor* and walks one
  component at a time with ``openat``-style calls (``dir_fd=``), each with
  ``O_NOFOLLOW`` so a symlink swapped in after the check cannot be followed
  by the kernel.
* Symlinks are resolved *by us*: ``readlink`` via the parent dirfd, target
  split into components and spliced back into the walk.  Absolute targets
  and ``..`` above the root are rejected (:class:`EscapeAttempt`); hops are
  bounded (:class:`SymlinkLoop`).
* ``..`` is handled by popping our own stack of already-open directory fds,
  never by asking the kernel to resolve ``..`` by name, so a concurrently
  renamed directory cannot make us wander.
* Component identity is verified: each directory is ``fstat``-ed after open
  and compared with the pre-open ``lstat``; a mismatch means the component
  was replaced between check and open and the walk is retried (bounded by
  the caller).

The logical path reported in :class:`Resolution` is the post-resolution
path (symlinks and ``..`` normalized away), which is what policy checks and
audit records are based on.
"""

from __future__ import annotations

import errno
import os
import stat
from collections import deque

from .errors import EscapeAttempt, SymlinkLoop

_O_PATH = getattr(os, "O_PATH", 0)
# O_PATH (Linux) opens anything, including directories without read
# permission, without following symlinks.  Elsewhere we fall back to
# O_RDONLY, which is sufficient for the directories we traverse.
_OPEN_FLAGS = (_O_PATH or os.O_RDONLY) | os.O_NOFOLLOW | os.O_CLOEXEC

DEFAULT_MAX_HOPS = 40


class RetryResolution(Exception):
    """A component changed identity between check and use; re-resolve."""


def _split(path: str) -> list[str]:
    if path.startswith("/"):
        raise EscapeAttempt(f"absolute path not allowed: {path!r}")
    return [c for c in path.split("/") if c not in ("", ".")]


class Resolution:
    """Owns ``parent_fd``: the dirfd of the directory holding the final
    component.  ``name`` is the final component, or ``None`` when the path
    resolved to a directory itself (e.g. ``""`` or ``a/b`` with b a dir and
    ``follow_final=True``).  Must be closed (or used as a context manager).
    """

    __slots__ = ("parent_fd", "name", "parts", "final_stat", "chain")

    def __init__(self, parent_fd, name, parts, final_stat, chain):
        self.parent_fd = parent_fd
        self.name = name
        self.parts = parts            # logical components incl. final
        self.final_stat = final_stat  # lstat/fstat of final component or None
        self.chain = chain            # (dev, ino) of each dir from root down

    @property
    def logical_path(self) -> str:
        return "/".join(self.parts)

    def close(self) -> None:
        if self.parent_fd is not None:
            os.close(self.parent_fd)
            self.parent_fd = None

    def __enter__(self) -> "Resolution":
        return self

    def __exit__(self, *exc) -> None:
        self.close()


def _close_stack(stack: list) -> None:
    for entry in stack:
        os.close(entry[1])
    stack.clear()


def _finish(stack: list, name, final_stat) -> Resolution:
    """Detach the top directory fd as the parent and close the rest."""
    parts = tuple(e[0] for e in stack[1:]) + ((name,) if name is not None else ())
    chain = tuple((e[2], e[3]) for e in stack)
    parent_fd = stack.pop()[1]
    _close_stack(stack)
    return Resolution(parent_fd, name, parts, final_stat, chain)


def resolve(
    root_fd: int,
    path: str,
    *,
    follow_final: bool,
    max_hops: int = DEFAULT_MAX_HOPS,
    trace=None,
) -> Resolution:
    """Resolve ``path`` below ``root_fd``.

    ``follow_final=True`` resolves a final symlink too (read/list);
    ``follow_final=False`` stops at the parent so the caller can act on the
    final name itself (create/replace/unlink/rename).
    """
    pending = deque(_split(path))
    # stack entries: [name, fd, dev, ino]; entry 0 is the root itself.
    root_st = os.fstat(root_fd)
    stack = [["", os.dup(root_fd), root_st.st_dev, root_st.st_ino]]
    hops = 0
    try:
        while pending:
            name = pending.popleft()
            is_final = not pending
            if name == "..":
                if len(stack) == 1:
                    raise EscapeAttempt(f"{path!r}: '..' would escape the root")
                os.close(stack.pop()[1])
                continue
            cur_fd = stack[-1][1]
            try:
                st = os.stat(name, dir_fd=cur_fd, follow_symlinks=False)
            except FileNotFoundError:
                if is_final:
                    return _finish(stack, name, None)
                raise
            if trace is not None:
                trace(
                    "component_checked",
                    component=name,
                    dev=st.st_dev,
                    ino=st.st_ino,
                    is_link=stat.S_ISLNK(st.st_mode),
                )
            if stat.S_ISLNK(st.st_mode):
                if is_final and not follow_final:
                    return _finish(stack, name, st)
                hops += 1
                if hops > max_hops:
                    raise SymlinkLoop(
                        f"more than {max_hops} symlink hops resolving {path!r}"
                    )
                target = os.readlink(name, dir_fd=cur_fd)
                if target.startswith("/"):
                    raise EscapeAttempt(
                        f"absolute symlink target {target!r} resolving {path!r}"
                    )
                pending.extendleft(reversed(_split(target)))
                continue
            if stat.S_ISDIR(st.st_mode):
                if is_final and not follow_final:
                    return _finish(stack, name, st)
                fd = os.open(name, _OPEN_FLAGS, dir_fd=cur_fd)
                fst = os.fstat(fd)
                if (fst.st_dev, fst.st_ino) != (st.st_dev, st.st_ino):
                    # Replaced between lstat and open: do not trust either.
                    os.close(fd)
                    raise RetryResolution(name)
                stack.append([name, fd, fst.st_dev, fst.st_ino])
                continue
            if not is_final:
                raise NotADirectoryError(
                    errno.ENOTDIR, os.strerror(errno.ENOTDIR), name
                )
            return _finish(stack, name, st)
        # Path resolved to a directory itself (root or a followed final dir).
        entry = stack.pop()
        parts = tuple(e[0] for e in stack[1:]) + ((entry[0],) if entry[0] else ())
        chain = tuple((e[2], e[3]) for e in stack) + ((entry[2], entry[3]),)
        _close_stack(stack)
        return Resolution(entry[1], None, parts, os.fstat(entry[1]), chain)
    except BaseException:
        _close_stack(stack)
        raise
