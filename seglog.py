"""seglog - 二进制追加日志分段校验组件.

帧格式 (所有多字节整数均为大端):

    +-----------+-------------------+-------------------+----------------+===========+-----------+
    | len  u32  | seq  u64          | prev_digest  u32  | payload (n B)  | crc  u32  |
    +-----------+-------------------+-------------------+----------------+===========+-----------+

- len:         帧体长度, 即 seq+prev_digest+payload+crc 的字节数 (不含 len 自身 4 字节).
- seq:         帧序号, 从 0 开始, 每帧严格 +1.
- prev_digest: 前一帧完整字节 (含其 len 前缀与 crc) 的 CRC32; 首帧为 0.
- payload:     任意字节数据, n = len - 16.
- crc:         对 "len 前缀 + seq + prev_digest + payload" 的 CRC32.

扫描结论分为三类:
- OK:       全部帧完整且校验通过.
- TRUNCATED: 末尾存在未完整写入的帧 (撕裂写), 允许截断到 good_offset 恢复.
- CORRUPT:  完整帧 CRC/链校验失败, 属中间损坏, 禁止跳过, 不允许截断恢复.
"""

from __future__ import annotations

import argparse
import os
import struct
import sys
import zlib
from dataclasses import dataclass, field

LEN_FMT = ">I"
SEQ_PREV_FMT = ">QI"
CRC_FMT = ">I"
LEN_SIZE = 4
SEQ_SIZE = 8
PREV_SIZE = 4
CRC_SIZE = 4
# len 之后、crc 之前的固定开销: seq + prev_digest
BODY_FIXED = SEQ_SIZE + PREV_SIZE
# 一帧除 payload 外的全部开销: len + seq + prev + crc
FRAME_OVERHEAD = LEN_SIZE + BODY_FIXED + CRC_SIZE

MAX_PAYLOAD = 64 * 1024 * 1024  # 64 MiB, 防御性上限

GENESIS_PREV = 0  # 首帧的 prev_digest


def crc32(data: bytes) -> int:
    return zlib.crc32(data) & 0xFFFFFFFF


def encode_frame(seq: int, prev_digest: int, payload: bytes) -> bytes:
    """编码一帧."""
    if len(payload) > MAX_PAYLOAD:
        raise ValueError("payload too large")
    head = struct.pack(SEQ_PREV_FMT, seq, prev_digest) + payload
    length = len(head) + CRC_SIZE
    prefix = struct.pack(LEN_FMT, length)
    return prefix + head + struct.pack(CRC_FMT, crc32(prefix + head))


@dataclass
class Frame:
    seq: int
    prev_digest: int
    payload: bytes
    offset: int          # 帧在日志中的起始偏移 (len 前缀处)
    raw: bytes           # 帧完整字节
    crc: int             # 帧内记录的 crc

    @property
    def digest(self) -> int:
        """本帧完整字节的 CRC32, 供下一帧链接."""
        return crc32(self.raw)


# ---------------------------------------------------------------- 流式解析器

class StreamParser:
    """增量式帧解析器: 可喂入任意大小的字节片段, 产出完整帧.

    用法:
        p = StreamParser()
        for chunk in chunks:
            for frame in p.feed(chunk):
                ...
        tail = p.pending  # 不足以构成完整帧的残余字节
    """

    def __init__(self) -> None:
        self._buf = bytearray()
        self._offset = 0  # 已消费字节在流中的绝对偏移

    @property
    def pending(self) -> bytes:
        return bytes(self._buf)

    @property
    def consumed(self) -> int:
        return self._offset

    def feed(self, data: bytes) -> list[Frame]:
        self._buf.extend(data)
        frames: list[Frame] = []
        while True:
            if len(self._buf) < LEN_SIZE:
                break
            (length,) = struct.unpack_from(LEN_FMT, self._buf, 0)
            if length < BODY_FIXED + CRC_SIZE or length > BODY_FIXED + CRC_SIZE + MAX_PAYLOAD:
                # 长度字段本身非法: 无法定位帧边界, 交给上层按损坏处理
                raise CorruptError(self._offset, f"invalid frame length field: {length}")
            if len(self._buf) < LEN_SIZE + length:
                break  # 帧未收齐, 等待更多数据
            raw = bytes(self._buf[: LEN_SIZE + length])
            frames.append(self._decode(raw, self._offset))
            del self._buf[: LEN_SIZE + length]
            self._offset += LEN_SIZE + length
        return frames

    @staticmethod
    def _decode(raw: bytes, offset: int) -> Frame:
        (length,) = struct.unpack_from(LEN_FMT, raw, 0)
        seq, prev = struct.unpack_from(SEQ_PREV_FMT, raw, LEN_SIZE)
        payload = raw[LEN_SIZE + BODY_FIXED : LEN_SIZE + length - CRC_SIZE]
        (crc,) = struct.unpack_from(CRC_FMT, raw, LEN_SIZE + length - CRC_SIZE)
        return Frame(seq=seq, prev_digest=prev, payload=payload,
                     offset=offset, raw=raw, crc=crc)


class CorruptError(Exception):
    """完整帧校验失败 (CRC / 链 / 序号)."""

    def __init__(self, offset: int, reason: str):
        super().__init__(f"corruption at offset {offset}: {reason}")
        self.offset = offset
        self.reason = reason


# ---------------------------------------------------------------- 扫描与诊断

@dataclass
class ScanResult:
    status: str                 # "ok" | "truncated" | "corrupt"
    frames: list[Frame] = field(default_factory=list)  # 校验通过的帧
    good_offset: int = 0        # 可安全保留的字节数 (最后一个好帧的末尾)
    reason: str = ""

    @property
    def ok(self) -> bool:
        return self.status == "ok"


def scan(data: bytes) -> ScanResult:
    """扫描整段日志字节, 校验每帧 CRC 与链式摘要.

    - 末尾不足一帧 -> truncated (撕裂写, 可截断恢复)
    - 完整帧校验失败 -> corrupt (中间损坏, 禁止跳过)
    """
    parser = StreamParser()
    frames: list[Frame] = []
    good_offset = 0
    try:
        frames = parser.feed(data)
    except CorruptError as exc:
        return ScanResult("corrupt", frames, exc.offset, exc.reason)

    # 链式校验: CRC -> prev_digest -> seq 连续性
    expected_seq = 0
    expected_prev = GENESIS_PREV
    good: list[Frame] = []
    for fr in frames:
        if crc32(fr.raw[:-CRC_SIZE]) != fr.crc:
            return ScanResult("corrupt", good, fr.offset,
                              f"frame seq={fr.seq} crc mismatch "
                              f"(stored=0x{fr.crc:08x}, computed=0x{crc32(fr.raw[:-CRC_SIZE]):08x})")
        if fr.prev_digest != expected_prev:
            return ScanResult("corrupt", good, fr.offset,
                              f"frame seq={fr.seq} chain break: prev_digest=0x{fr.prev_digest:08x}, "
                              f"expected=0x{expected_prev:08x}")
        if fr.seq != expected_seq:
            return ScanResult("corrupt", good, fr.offset,
                              f"frame seq gap: got {fr.seq}, expected {expected_seq}")
        expected_prev = fr.digest
        expected_seq += 1
        good_offset = fr.offset + len(fr.raw)
        good.append(fr)
    frames = good

    if parser.pending:
        need = ""
        if len(parser.pending) >= LEN_SIZE:
            (length,) = struct.unpack_from(LEN_FMT, parser.pending, 0)
            need = f" (frame declares {LEN_SIZE + length} bytes, only {len(parser.pending)} present)"
        return ScanResult("truncated", frames, good_offset,
                          f"incomplete tail of {len(parser.pending)} byte(s) at offset "
                          f"{good_offset}{need}")
    return ScanResult("ok", frames, good_offset)


def scan_file(path: str, chunk_size: int = 1 << 16) -> ScanResult:
    """流式扫描文件 (任意大小分块读取)."""
    parser = StreamParser()
    frames: list[Frame] = []
    good_offset = 0
    try:
        with open(path, "rb") as fh:
            while True:
                chunk = fh.read(chunk_size)
                if not chunk:
                    break
                frames.extend(parser.feed(chunk))
    except CorruptError as exc:
        return ScanResult("corrupt", frames, exc.offset, exc.reason)

    expected_seq = 0
    expected_prev = GENESIS_PREV
    good = []
    for fr in frames:
        if crc32(fr.raw[:-CRC_SIZE]) != fr.crc:
            return ScanResult("corrupt", good, fr.offset,
                              f"frame seq={fr.seq} crc mismatch")
        if fr.prev_digest != expected_prev:
            return ScanResult("corrupt", good, fr.offset,
                              f"frame seq={fr.seq} chain break")
        if fr.seq != expected_seq:
            return ScanResult("corrupt", good, fr.offset,
                              f"frame seq gap: got {fr.seq}, expected {expected_seq}")
        expected_prev = fr.digest
        expected_seq += 1
        good_offset = fr.offset + len(fr.raw)
        good.append(fr)
    frames = good

    if parser.pending:
        return ScanResult("truncated", frames, good_offset,
                          f"incomplete tail of {len(parser.pending)} byte(s) at offset {good_offset}")
    return ScanResult("ok", frames, good_offset)


def truncate_tail(path: str) -> ScanResult:
    """仅当末尾为不完整帧时截断; 中间损坏则拒绝."""
    result = scan_file(path)
    if result.status == "corrupt":
        raise CorruptError(result.good_offset,
                           f"refuse to truncate: {result.reason}")
    if result.status == "truncated":
        with open(path, "r+b") as fh:
            fh.truncate(result.good_offset)
    return result


# ---------------------------------------------------------------- 追加写入

def append_frame(path: str, payload: bytes) -> Frame:
    """向日志追加一帧 (先扫描取得末帧序号与摘要)."""
    result = scan_file(path) if os.path.exists(path) and os.path.getsize(path) > 0 \
        else ScanResult("ok", [], 0)
    if result.status == "corrupt":
        raise CorruptError(result.good_offset, f"refuse to append: {result.reason}")
    if result.status == "truncated":
        raise CorruptError(result.good_offset,
                           "refuse to append: incomplete tail present, run truncate first")
    seq = result.frames[-1].seq + 1 if result.frames else 0
    prev = result.frames[-1].digest if result.frames else GENESIS_PREV
    raw = encode_frame(seq, prev, payload)
    with open(path, "ab") as fh:
        fh.write(raw)
    return StreamParser._decode(raw, result.good_offset)


# ---------------------------------------------------------------- 索引重建

INDEX_ENTRY = struct.Struct(">QQI")  # seq, offset, frame_total_len


def rebuild_index(log_path: str, index_path: str | None = None) -> list[tuple[int, int, int]]:
    """扫描日志, 重建 (seq, offset, length) 索引; 可选写入二进制索引文件."""
    result = scan_file(log_path)
    entries = [(fr.seq, fr.offset, len(fr.raw)) for fr in result.frames]
    if index_path:
        with open(index_path, "wb") as fh:
            fh.write(struct.pack(">I", len(entries)))
            for seq, offset, length in entries:
                fh.write(INDEX_ENTRY.pack(seq, offset, length))
    return entries


def read_index(index_path: str) -> list[tuple[int, int, int]]:
    with open(index_path, "rb") as fh:
        data = fh.read()
    (count,) = struct.unpack_from(">I", data, 0)
    entries = []
    pos = 4
    for _ in range(count):
        entries.append(INDEX_ENTRY.unpack_from(data, pos))
        pos += INDEX_ENTRY.size
    return entries


# ---------------------------------------------------------------- 故障注入

def inject_flip_byte(path: str, offset: int) -> None:
    with open(path, "r+b") as fh:
        fh.seek(offset)
        b = fh.read(1)
        if len(b) != 1:
            raise ValueError(f"offset {offset} out of range")
        fh.seek(offset)
        fh.write(bytes([b[0] ^ 0xFF]))


def inject_truncate(path: str, size: int) -> None:
    with open(path, "r+b") as fh:
        fh.truncate(size)


# ---------------------------------------------------------------- CLI

def _cmd_append(args: argparse.Namespace) -> int:
    payload = args.text.encode("utf-8") if args.text is not None else sys.stdin.buffer.read()
    fr = append_frame(args.log, payload)
    print(f"appended seq={fr.seq} offset={fr.offset} len={len(fr.raw)}")
    return 0


def _print_result(result: ScanResult) -> None:
    print(f"status={result.status} frames={len(result.frames)} good_offset={result.good_offset}")
    if result.reason:
        print(f"detail: {result.reason}")


def _cmd_verify(args: argparse.Namespace) -> int:
    result = scan_file(args.log)
    _print_result(result)
    return {"ok": 0, "truncated": 2, "corrupt": 1}[result.status]


def _cmd_truncate(args: argparse.Namespace) -> int:
    try:
        result = truncate_tail(args.log)
    except CorruptError as exc:
        print(f"refused: {exc}", file=sys.stderr)
        return 1
    _print_result(result)
    if result.status == "truncated":
        print(f"truncated file to {result.good_offset} bytes")
    else:
        print("nothing to truncate")
    return 0


def _cmd_index(args: argparse.Namespace) -> int:
    entries = rebuild_index(args.log, args.index)
    for seq, offset, length in entries:
        print(f"seq={seq} offset={offset} len={length}")
    if args.index:
        print(f"index written to {args.index} ({len(entries)} entries)")
    return 0


def _cmd_dump(args: argparse.Namespace) -> int:
    result = scan_file(args.log)
    for fr in result.frames:
        preview = fr.payload[:32].hex()
        print(f"seq={fr.seq} offset={fr.offset} payload={len(fr.payload)}B "
              f"prev=0x{fr.prev_digest:08x} crc=0x{fr.crc:08x} data={preview}")
    _print_result(result)
    return {"ok": 0, "truncated": 2, "corrupt": 1}[result.status]


def _cmd_inject(args: argparse.Namespace) -> int:
    if args.kind == "flip-byte":
        inject_flip_byte(args.log, args.value)
        print(f"flipped byte at offset {args.value}")
    elif args.kind == "truncate":
        inject_truncate(args.log, args.value)
        print(f"truncated file to {args.value} bytes")
    return 0


def build_argparser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="seglog",
        description="二进制追加日志: 长度前缀+序号+前段摘要+CRC 分段校验")
    sub = p.add_subparsers(dest="cmd", required=True)

    pa = sub.add_parser("append", help="追加一帧 (payload 来自 --text 或 stdin)")
    pa.add_argument("log")
    pa.add_argument("--text", help="文本 payload; 缺省从 stdin 读取原始字节")
    pa.set_defaults(fn=_cmd_append)

    pv = sub.add_parser("verify", help="校验日志 (exit: 0=ok 1=corrupt 2=truncated)")
    pv.add_argument("log")
    pv.set_defaults(fn=_cmd_verify)

    pt = sub.add_parser("truncate", help="截去不完整尾部 (损坏时拒绝)")
    pt.add_argument("log")
    pt.set_defaults(fn=_cmd_truncate)

    pi = sub.add_parser("index", help="重建索引")
    pi.add_argument("log")
    pi.add_argument("index", nargs="?", help="可选: 二进制索引输出路径")
    pi.set_defaults(fn=_cmd_index)

    pd = sub.add_parser("dump", help="列出全部帧")
    pd.add_argument("log")
    pd.set_defaults(fn=_cmd_dump)

    pj = sub.add_parser("inject", help="故障注入")
    pj.add_argument("kind", choices=["flip-byte", "truncate"])
    pj.add_argument("log")
    pj.add_argument("value", type=int,
                    help="flip-byte: 字节偏移; truncate: 保留字节数")
    pj.set_defaults(fn=_cmd_inject)
    return p


def main(argv: list[str] | None = None) -> int:
    args = build_argparser().parse_args(argv)
    return args.fn(args)


if __name__ == "__main__":
    raise SystemExit(main())
