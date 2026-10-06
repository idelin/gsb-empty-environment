"""Component-wise, dirfd-based path resolution.

The resolver never builds a filesystem string and hands it to a syscall.
Instead it walks the path one component at a time, opening each component
relative to the *file descriptor of its already-open parent* (openat
semantics via ``dir_fd=``).  Confinement therefore does not depend on a
realpath() check racing against a later open():

* every directory in the chain is pinned by an open fd (device+inode);
* components are opened with ``O_NOFOLLOW`` so a symlink swapped in after
  the check can never be followed implicitly -- the open fails with
  ``ELOOP`` and the link is resolved explicitly instead;
* relative symlinks are spliced into the remaining component stream, with
  a hop limit; absolute symlinks and ``..`` above the root are rejected;
* after opening, the fd's fstat() identity is compared with the identity
  seen at check time; a mismatch re-resolves the component (bounded);
* ``..`` is resolved against the logical ancestor stack (the fd we came
  from), never by asking the filesystem for ".." -- so it cannot escape
  even if a directory was renamed while we hold it.
"""

from __future__ import annotations

import errno
import os
import stat as statmod
from collections import deque
from dataclasses import dataclass

from .errors import EscapeError, ResolutionRaceError, SymlinkLimitExceeded
from .policy import split_components

MAX_SYMLINK_HOPS = 40
MAX_IDENTITY_RETRIES = 16


@dataclass
class ChainEntry:
    name: str
    fd: int
    dev: int
    ino: int


class Resolved:
    """An open chain of directory fds from the root to the resolved object."""

    def __init__(self, entries):
        self.entries = entries

    @property
    def path(self):
        """Resolved root-relative logical path ("" == the root itself)."""
        return "/".join(e.name for e in self.entries[1:])

    @property
    def fd(self):
        return self.entries[-1].fd

    @property
    def last(self):
        return self.entries[-1]

    def close(self):
        entries, self.entries = self.entries, []
        for entry in entries:
            try:
                os.close(entry.fd)
            except OSError:
                pass

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.close()


class Resolver:
    def __init__(self, root_fd, hooks):
        self._root_fd = root_fd
        self._hooks = hooks
        st = os.fstat(root_fd)
        self._root_dev = st.st_dev
        self._root_ino = st.st_ino

    # -- public resolution modes -------------------------------------------

    def resolve_dir(self, path):
        """Resolve the whole path; the final component must be a directory."""
        return self._walk(split_components(path), final="dir")

    def resolve_file(self, path):
        """Resolve the whole path, following symlinks; final may be any non-dir."""
        resolved = self._walk(split_components(path), final="file")
        if statmod.S_ISDIR(os.fstat(resolved.fd).st_mode):
            resolved.close()
            raise IsADirectoryError(errno.EISDIR, "is a directory", path)
        return resolved

    def resolve_parent(self, path):
        """Resolve all but the final component; return (Resolved, final_name).

        The final component is deliberately *not* opened or followed, so
        unlink/create/replace/rename operate on the name itself (e.g. remove
        a symlink rather than its target).
        """
        comps = split_components(path)
        if not comps or comps[-1] == "..":
            raise ValueError(f"path must name a final component: {path!r}")
        final_name = comps.pop()
        return self._walk(comps, final="dir"), final_name

    # -- the walk -----------------------------------------------------------

    def _walk(self, comps, *, final):
        root_dup = os.dup(self._root_fd)
        stack = [ChainEntry("", root_dup, self._root_dev, self._root_ino)]
        pending = deque(comps)
        hops = 0
        try:
            while pending:
                comp = pending.popleft()
                if comp == "..":
                    if len(stack) == 1:
                        raise EscapeError("'..' would escape the root")
                    os.close(stack.pop().fd)
                    continue
                retries = 0
                while True:
                    parent = stack[-1]
                    # CHECK: what is this component right now?
                    st = os.stat(comp, dir_fd=parent.fd, follow_symlinks=False)
                    prefix = "/".join(e.name for e in stack[1:])
                    self._hooks.after_component_check(
                        name=comp, parent_fd=parent.fd,
                        stat_result=st, path_so_far=prefix)
                    if statmod.S_ISLNK(st.st_mode):
                        target = os.readlink(comp, dir_fd=parent.fd)
                        if target.startswith("/"):
                            raise EscapeError(
                                f"absolute symlink rejected: {comp!r} -> {target!r}")
                        hops += 1
                        if hops > MAX_SYMLINK_HOPS:
                            raise SymlinkLimitExceeded(
                                f"more than {MAX_SYMLINK_HOPS} symlink hops")
                        pending.extendleft(reversed(split_components(target)))
                        break
                    # OPEN: O_NOFOLLOW makes a check->open symlink swap fail
                    # loudly instead of silently following the link.  Linux
                    # reports ELOOP, or ENOTDIR when O_DIRECTORY is also set
                    # (a symlink itself is not a directory); both mean
                    # "re-check and resolve explicitly".
                    is_last = not pending
                    need_dir = not is_last or final == "dir"
                    if need_dir and not statmod.S_ISDIR(st.st_mode):
                        raise NotADirectoryError(
                            errno.ENOTDIR, "not a directory", comp)
                    flags = os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC
                    if need_dir:
                        flags |= os.O_DIRECTORY
                    try:
                        fd = os.open(comp, flags, dir_fd=parent.fd)
                    except OSError as exc:
                        if exc.errno in (errno.ELOOP, errno.ENOTDIR):
                            # Swapped between check and open: re-check and
                            # resolve whatever is there now.
                            retries += 1
                            if retries > MAX_IDENTITY_RETRIES:
                                raise ResolutionRaceError(
                                    f"component {comp!r} keeps changing under us")
                            continue
                        raise
                    fst = os.fstat(fd)
                    if (fst.st_dev, fst.st_ino) != (st.st_dev, st.st_ino):
                        # Identity changed between check and open: do not
                        # trust either snapshot; re-resolve the component.
                        os.close(fd)
                        retries += 1
                        if retries > MAX_IDENTITY_RETRIES:
                            raise ResolutionRaceError(
                                f"component {comp!r} keeps changing identity")
                        continue
                    stack.append(ChainEntry(comp, fd, fst.st_dev, fst.st_ino))
                    break
        except BaseException:
            for entry in stack:
                try:
                    os.close(entry.fd)
                except OSError:
                    pass
            raise
        return Resolved(stack)
