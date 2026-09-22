"""Local on-disk workspace. All task I/O goes through this object.

The kernel's own state (logs, staging) lives in a separate directory so that
directory enumeration of the workspace root is stable and deterministic.
"""
from __future__ import annotations

import os
import shutil
from typing import Iterable, Sequence

from . import identity as I


class WorkspaceError(Exception):
    pass


class NotAFile(WorkspaceError):
    pass


class NotADirectory(WorkspaceError):
    pass


class Workspace:
    def __init__(self, root: str, aliases: Iterable[tuple[str, str]] = ()):
        self.root = os.path.abspath(root)
        os.makedirs(self.root, exist_ok=True)
        self.aliases: dict[str, str] = I.make_alias_map(aliases)

    # -- path handling -----------------------------------------------------

    def _path(self, path: str) -> str:
        logical = I.resolve(path, self.aliases)
        if logical == ".":
            return self.root
        return os.path.join(self.root, *logical.split("/"))

    def identity(self, path: str) -> str:
        return I.resolve(path, self.aliases)

    # -- reads -------------------------------------------------------------

    def exists(self, path: str) -> bool:
        return os.path.exists(self._path(path))

    def is_dir(self, path: str) -> bool:
        return os.path.isdir(self._path(path))

    def read_file(self, path: str) -> bytes:
        p = self._path(path)
        if not os.path.isfile(p):
            if os.path.isdir(p):
                raise IsADirectoryError(path)
            raise FileNotFoundError(path)
        with open(p, "rb") as fh:
            return fh.read()

    def list_dir(self, path: str = ".", rule: str = "lex") -> list[tuple[str, str]]:
        p = self._path(path)
        if not os.path.isdir(p):
            if os.path.exists(p):
                raise NotADirectory(path)
            raise FileNotFoundError(path)
        entries: list[tuple[str, str]] = []
        for name in sorted(os.listdir(p)):
            kind = I.DIRECTORY if os.path.isdir(os.path.join(p, name)) else I.CONTENT
            entries.append((name, kind))
        return I.order_entries(entries, rule)

    # -- mutation (used by the test harness and by staged publication) ----

    def write_file(self, path: str, data: bytes | str) -> str:
        if isinstance(data, str):
            data = data.encode("utf-8")
        p = self._path(path)
        os.makedirs(os.path.dirname(p) or self.root, exist_ok=True)
        if os.path.isdir(p):
            raise IsADirectoryError(path)
        tmp = p + ".tmp"
        with open(tmp, "wb") as fh:
            fh.write(data)
        os.replace(tmp, p)
        return I.digest_bytes(data)

    def remove_file(self, path: str) -> None:
        p = self._path(path)
        if os.path.isdir(p):
            raise IsADirectoryError(path)
        try:
            os.remove(p)
        except FileNotFoundError:
            pass

    def replace_from(self, src: str, path: str) -> str:
        """Atomically publish a staged temp file at logical *path*."""
        p = self._path(path)
        os.makedirs(os.path.dirname(p) or self.root, exist_ok=True)
        if os.path.isdir(p):
            raise IsADirectoryError(path)
        with open(src, "rb") as fh:
            data = fh.read()
        digest = I.digest_bytes(data)
        os.replace(src, p)
        return digest

    def file_digest(self, path: str) -> str:
        return I.digest_bytes(self.read_file(path))

    # -- versioned tokens --------------------------------------------------

    def token(self, path: str, rule: str = "lex"):
        """Current version token for a logical identity.

        file      -> ("file", digest)
        directory -> ("dir", rule, fingerprint)
        missing   -> ("missing",)
        """
        logical = I.resolve(path, self.aliases)
        p = self._path(logical)
        if os.path.isfile(p):
            with open(p, "rb") as fh:
                return (I.CONTENT, I.digest_bytes(fh.read()))
        if os.path.isdir(p):
            entries = [
                (n, I.DIRECTORY if os.path.isdir(os.path.join(p, n)) else I.CONTENT)
                for n in os.listdir(p)
            ]
            return (I.DIRECTORY, rule, I.directory_fingerprint(entries, rule))
        return (I.MISSING,)

    def export_files(self) -> dict[str, bytes]:
        out: dict[str, bytes] = {}
        for dirpath, _dirs, files in os.walk(self.root):
            for name in files:
                full = os.path.join(dirpath, name)
                rel = os.path.relpath(full, self.root).replace(os.sep, "/")
                with open(full, "rb") as fh:
                    out[rel] = fh.read()
        return out


def seed_workspace(target: "Workspace", files: dict[str, bytes | str]) -> None:
    for rel, data in files.items():
        target.write_file(rel, data)


def copy_workspace(src: "Workspace", dst: "Workspace") -> None:
    for rel, data in src.export_files().items():
        dst.write_file(rel, data)
