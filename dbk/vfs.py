from __future__ import annotations

import hashlib
import os
import shutil
from dataclasses import dataclass
from pathlib import Path


MISSING_PREFIX = "dbk:missing:"
STATE_DIR_NAME = ".buildkernel"


class FileNotFound(Exception):
    pass


@dataclass(frozen=True)
class DirEntry:
    name: str
    type: str


class VFS:
    """A small deterministic view over a local on-disk workspace."""

    def __init__(self, root: str | os.PathLike[str]):
        self.root = Path(root).resolve()
        self.root.mkdir(parents=True, exist_ok=True)

    def normalize(self, path: str | os.PathLike[str]) -> str:
        raw = os.fspath(path)
        if os.path.isabs(raw):
            candidate = Path(raw)
            try:
                candidate.relative_to(self.root)
            except ValueError as exc:
                raise ValueError(f"path {raw!r} is outside the workspace") from exc
            p = candidate
        else:
            p = Path(raw)
        parts: list[str] = []
        for part in p.parts:
            if part == "" or part == ".":
                continue
            if part == "..":
                if not parts:
                    raise ValueError(f"path {raw!r} escapes the workspace")
                parts.pop()
                continue
            parts.append(part)
        return "/".join(parts)

    def absolute(self, path: str | os.PathLike[str]) -> Path:
        return self.root / self.normalize(path)

    def write(self, path: str | os.PathLike[str], data: bytes | str) -> None:
        if isinstance(data, str):
            data = data.encode("utf-8")
        target = self.absolute(path)
        target.parent.mkdir(parents=True, exist_ok=True)
        temporary = target.with_name(f".{target.name}.tmp.{os.getpid()}")
        temporary.write_bytes(data)
        os.replace(temporary, target)

    def remove(self, path: str | os.PathLike[str]) -> None:
        target = self.absolute(path)
        if target.is_symlink() or target.is_file():
            target.unlink()
        elif target.exists():
            shutil.rmtree(target)

    def mkdir(self, path: str | os.PathLike[str]) -> None:
        self.absolute(path).mkdir(parents=True, exist_ok=True)

    def exists(self, path: str | os.PathLike[str]) -> bool:
        return self.absolute(path).exists()

    def is_dir(self, path: str | os.PathLike[str]) -> bool:
        return self.absolute(path).is_dir()

    def read(self, path: str | os.PathLike[str]) -> bytes:
        target = self.absolute(path)
        try:
            return target.read_bytes()
        except FileNotFoundError as exc:
            raise FileNotFound(str(path)) from exc
        except IsADirectoryError as exc:
            raise IsADirectoryError(str(path)) from exc

    def entry_type(self, path: str | os.PathLike[str]) -> str:
        target = self.absolute(path)
        if target.is_symlink():
            return "symlink"
        if target.is_dir():
            return "dir"
        if target.is_file():
            return "file"
        return "other"

    def digest(self, path: str | os.PathLike[str]) -> str:
        target = self.absolute(path)
        with target.open("rb") as stream:
            return hashlib.file_digest(stream, "sha256").hexdigest()

    def scan_dir(self, path: str | os.PathLike[str]) -> list[DirEntry]:
        target = self.absolute(path)
        try:
            with os.scandir(target) as iterator:
                raw = list(iterator)
        except FileNotFoundError as exc:
            raise FileNotFound(str(path)) from exc
        entries: list[DirEntry] = []
        if target == self.root and any(entry.name == STATE_DIR_NAME for entry in raw):
            raw = [entry for entry in raw if entry.name != STATE_DIR_NAME]
        for entry in raw:
            if entry.is_symlink():
                entry_type = "symlink"
            elif entry.is_dir(follow_symlinks=False):
                entry_type = "dir"
            elif entry.is_file(follow_symlinks=False):
                entry_type = "file"
            else:
                entry_type = "other"
            entries.append(DirEntry(entry.name, entry_type))
        return entries

    def copy_tree(self, destination: str | os.PathLike[str]) -> "VFS":
        destination = Path(destination)
        if destination.exists():
            shutil.rmtree(destination)

        def ignore(_dir: str, names: list[str]) -> set[str]:
            if Path(_dir).resolve() == self.root:
                return {name for name in names if name == STATE_DIR_NAME}
            return set()

        shutil.copytree(self.root, destination, symlinks=True, ignore=ignore)
        return VFS(destination)
