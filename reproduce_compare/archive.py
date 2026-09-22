"""Bounded, in-memory ZIP inspection.

Members are never extracted to disk. Reading enforces a shared
:class:`Budget` covering nesting depth, total decompressed bytes and total
entry count. Duplicate member names, path traversal and damaged structures
raise explicit exceptions instead of one member being silently preferred.
"""

from __future__ import annotations

import stat as stat_module
import struct
import zipfile
from dataclasses import dataclass, replace

from .errors import CorruptArchiveError, QuotaExceededError

ZIP_LOCAL_MAGIC = b"PK\x03\x04"
ZIP_EMPTY_MAGIC = b"PK\x05\x06"  # end-of-central-directory (e.g. empty archive)

_UNIX_SYSTEM = 3
_DOS_UNRESOLVED = (1980, 1, 1, 0, 0, 0)
_SUPPORTED_COMPRESSION = {zipfile.ZIP_STORED, zipfile.ZIP_DEFLATED, zipfile.ZIP_BZIP2, zipfile.ZIP_LZMA}


@dataclass(frozen=True)
class Timestamp:
    year: int
    month: int
    day: int
    hour: int
    minute: int
    second: int  # DOS two-second resolution already applied

    def as_tuple(self) -> tuple[int, int, int, int, int, int]:
        return (self.year, self.month, self.day, self.hour, self.minute, self.second)

    @classmethod
    def from_dos(cls, dos_date: int, dos_time: int) -> "Timestamp":
        year = ((dos_date >> 9) & 0x7F) + 1980
        month = (dos_date >> 5) & 0x0F
        day = dos_date & 0x1F
        hour = (dos_time >> 11) & 0x1F
        minute = (dos_time >> 5) & 0x3F
        second = (dos_time & 0x1F) * 2
        return cls(year, month, day, hour, minute, second)

    @classmethod
    def from_zipinfo(cls, info: zipfile.ZipInfo) -> "Timestamp":
        return cls(info.date_time[0], info.date_time[1], info.date_time[2],
                   info.date_time[3], info.date_time[4], info.date_time[5])


@dataclass(frozen=True)
class ZipEntry:
    name: str
    kind: str                 # "file" | "dir" | "symlink"
    mode: int
    data: bytes
    symlink_target: str | None
    timestamp: Timestamp
    compressed_size: int
    is_dir_flag: bool


@dataclass
class Budget:
    max_depth: int = 8
    max_uncompressed_bytes: int = 256 * 1024 * 1024
    max_entries: int = 100_000
    depth: int = 0
    uncompressed_bytes: int = 0
    entries: int = 0

    def enter(self) -> "Budget":
        if self.depth + 1 > self.max_depth:
            raise QuotaExceededError(
                f"nested archive depth {self.depth + 1} exceeds limit {self.max_depth}"
            )
        return replace(self, depth=self.depth + 1)

    def charge_entries(self, count: int) -> None:
        if self.entries + count > self.max_entries:
            raise QuotaExceededError(
                f"entry count {self.entries + count} exceeds limit {self.max_entries}",
                self.entries + count,
            )
        self.entries += count

    def charge_bytes(self, count: int) -> None:
        if self.uncompressed_bytes + count > self.max_uncompressed_bytes:
            raise QuotaExceededError(
                "uncompressed byte budget "
                f"{self.uncompressed_bytes + count} exceeds limit {self.max_uncompressed_bytes}",
                self.uncompressed_bytes + count,
            )
        self.uncompressed_bytes += count


def looks_like_zip(data: bytes) -> bool:
    return data.startswith(ZIP_LOCAL_MAGIC) or data.startswith(ZIP_EMPTY_MAGIC)


def validate_member_name(name: str) -> None:
    if name == "" or "\x00" in name:
        raise CorruptArchiveError(f"invalid empty or NUL member name: {name!r}")
    if name.startswith("/") or name.startswith("\\"):
        raise CorruptArchiveError(f"absolute member path (traversal risk): {name!r}")
    if "\\" in name:
        raise CorruptArchiveError(f"backslash member path (traversal risk): {name!r}")
    if len(name) >= 2 and name[1] == ":":
        raise CorruptArchiveError(f"drive-letter member path (traversal risk): {name!r}")
    parts = [part for part in name.split("/") if part]
    if not parts:
        raise CorruptArchiveError(f"member has no usable path components: {name!r}")
    if any(part == ".." for part in parts):
        raise CorruptArchiveError(f"parent-directory member path (traversal risk): {name!r}")
    # ``.`` components can alter resolution on some consumers; reject them too.
    if any(part == "." for part in parts):
        raise CorruptArchiveError(f"current-directory member path: {name!r}")


def _is_unix(info: zipfile.ZipInfo) -> bool:
    return info.create_system == _UNIX_SYSTEM


def _unix_mode(info: zipfile.ZipInfo) -> int:
    return (info.external_attr >> 16) & 0xFFFF


def _classify(info: zipfile.ZipInfo) -> tuple[str, int, str | None, bytes]:
    is_dir_flag = info.is_dir()
    unix_mode = _unix_mode(info) if _is_unix(info) else 0
    stat_mode = unix_mode & 0o7777

    if is_dir_flag:
        kind = "dir"
        mode = stat_mode or 0o755
        target = None
        data = b""
    elif stat_file_is_symlink(unix_mode):
        kind = "symlink"
        mode = stat_mode or 0o777
        target = None  # resolved by caller after payload read
        data = b""
    else:
        kind = "file"
        mode = stat_mode or (0o644 if _is_unix(info) else 0o666)
        target = None
        data = b""
    return kind, mode, target, data


def stat_file_is_symlink(unix_mode: int) -> bool:
    return stat_module.S_IFMT(unix_mode) == stat_module.S_IFLNK


def _read_local_header_timestamp(fp, header_offset: int, name: str) -> Timestamp:
    fp.seek(header_offset)
    header = fp.read(30)
    if len(header) < 30 or not header.startswith(ZIP_LOCAL_MAGIC):
        raise CorruptArchiveError(f"missing local header for member {name!r}")
    dos_time, dos_date = struct.unpack_from("<HH", header, 10)
    return Timestamp.from_dos(dos_date, dos_time)


def read_zip(data: bytes, budget: Budget) -> tuple[list[ZipEntry], Budget]:
    """Parse all members, enforcing the budget while decompressing."""
    if not looks_like_zip(data):
        raise CorruptArchiveError("data does not begin with a ZIP signature")

    try:
        zf = zipfile.ZipFile(__import__("io").BytesIO(data))
        infos = zf.infolist()
    except zipfile.BadZipFile as exc:
        raise CorruptArchiveError(f"damaged ZIP structure: {exc}") from None

    seen_names: set[str] = set()
    for info in infos:
        if info.filename in seen_names:
            raise CorruptArchiveError(f"duplicate ZIP member name: {info.filename!r}")
        seen_names.add(info.filename)
        validate_member_name(info.filename)

    budget.charge_entries(len(infos))

    entries: list[ZipEntry] = []
    for info in infos:
        if info.flag_bits & 0x1:
            raise CorruptArchiveError(f"encrypted ZIP members are not supported: {info.filename!r}")
        if info.compress_type not in _SUPPORTED_COMPRESSION:
            raise CorruptArchiveError(
                f"unsupported compression method {info.compress_type} for {info.filename!r}"
            )

        kind, mode, _target, _empty = _classify(info)
        central_ts = Timestamp.from_zipinfo(info)

        # Resolve timestamp bytes via the parser-located local header.
        try:
            local_ts = _read_local_header_timestamp(zf.fp, info.header_offset, info.filename)
        except AttributeError:  # pragma: no cover - stdlib always exposes fp
            raise CorruptArchiveError("cannot locate local header stream") from None
        if local_ts.as_tuple() != central_ts.as_tuple():
            raise CorruptArchiveError(
                f"local/central timestamp metadata disagree for {info.filename!r}"
            )

        payload = b""
        symlink_target: str | None = None
        if not info.is_dir():
            budget.charge_bytes(info.file_size)
            try:
                payload = zf.read(info)
            except (zipfile.BadZipFile, EOFError, RuntimeError, zlib_error()) as exc:
                raise CorruptArchiveError(
                    f"failed to decompress {info.filename!r}: {exc}"
                ) from None
            if len(payload) != info.file_size:
                raise CorruptArchiveError(
                    f"decompressed length mismatch for {info.filename!r}"
                )
            if kind == "symlink":
                try:
                    symlink_target = payload.decode("utf-8")
                except UnicodeDecodeError:
                    symlink_target = payload.decode("utf-8", "surrogateescape")

        entries.append(
            ZipEntry(
                name=info.filename,
                kind=kind,
                mode=mode,
                data=payload if kind == "file" else b"",
                symlink_target=symlink_target,
                timestamp=central_ts,
                compressed_size=info.compress_size,
                is_dir_flag=info.is_dir(),
            )
        )

    zf.close()
    return entries, budget


def zlib_error():
    import zlib

    return zlib.error
