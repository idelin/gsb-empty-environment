"""Read-only materialization of a directory tree.

The tree is read once per root; file bytes and symlink targets are captured
for hashing. Nothing here writes anywhere.
"""

from __future__ import annotations

import os
import stat as stat_module
from dataclasses import dataclass


@dataclass(frozen=True)
class TreeEntry:
    relpath: str          # POSIX-style, relative to root
    kind: str             # "file" | "dir" | "symlink"
    mode: int
    data: bytes
    symlink_target: str | None


class TreeReadError(OSError):
    pass


def _walk(root: str) -> list[TreeEntry]:
    entries: list[TreeEntry] = []
    root_fd = os.open(root, os.O_RDONLY | getattr(os, "O_DIRECTORY", 0))
    pending_fds: list[int] = [root_fd]
    try:
        stack: list[tuple[str, int]] = [(".", root_fd)]
        while stack:
            prefix, dir_fd = stack.pop()
            with os.scandir(dir_fd) as scan:
                children = sorted(scan, key=lambda e: e.name)
            for child in children:
                rel = child.name if prefix == "." else prefix + "/" + child.name
                st = os.lstat(child.name, dir_fd=dir_fd)
                mode = stat_module.S_IMODE(st.st_mode)
                if stat_module.S_ISLNK(st.st_mode):
                    raw_target = os.readlink(child.name, dir_fd=dir_fd)
                    target = os.fsdecode(raw_target) if isinstance(raw_target, bytes) else raw_target
                    entries.append(TreeEntry(rel, "symlink", mode, b"", target))
                elif stat_module.S_ISDIR(st.st_mode):
                    entries.append(TreeEntry(rel, "dir", mode, b"", None))
                    sub_fd = os.open(
                        child.name,
                        os.O_RDONLY | getattr(os, "O_DIRECTORY", 0),
                        dir_fd=dir_fd,
                    )
                    pending_fds.append(sub_fd)
                    stack.append((rel, sub_fd))
                elif stat_module.S_ISREG(st.st_mode):
                    fd = os.open(child.name, os.O_RDONLY, dir_fd=dir_fd)
                    try:
                        chunks: list[bytes] = []
                        while True:
                            chunk = os.read(fd, 1024 * 1024)
                            if not chunk:
                                break
                            chunks.append(chunk)
                    finally:
                        os.close(fd)
                    entries.append(TreeEntry(rel, "file", mode, b"".join(chunks), None))
                else:
                    raise TreeReadError(f"unsupported special file in tree: {rel}")
    finally:
        for fd in pending_fds:
            os.close(fd)
    return entries


def scan_tree(root: str) -> dict[str, TreeEntry]:
    if not os.path.isdir(root) or os.path.islink(root):
        raise TreeReadError(f"tree root must be a real directory: {root}")
    return {entry.relpath: entry for entry in _walk(root)}
