"""seglog 单元测试 (Python 3.11 标准库, 全部使用合成数据)."""

import os
import random
import struct
import subprocess
import sys
import tempfile
import unittest

import seglog
from seglog import (FRAME_OVERHEAD, LEN_FMT, LEN_SIZE, CorruptError, StreamParser,
                    append_frame, encode_frame, read_index, rebuild_index, scan,
                    scan_file, truncate_tail)


def build_log(frames_payloads):
    """用合成 payload 构造日志字节串."""
    out = bytearray()
    prev = seglog.GENESIS_PREV
    for seq, payload in enumerate(frames_payloads):
        raw = encode_frame(seq, prev, payload)
        out += raw
        prev = seglog.crc32(raw)
    return bytes(out)


def frame_boundaries(payloads):
    bounds, pos = [0], 0
    for p in payloads:
        pos += FRAME_OVERHEAD + len(p)
        bounds.append(pos)
    return bounds


class TestFrameCodec(unittest.TestCase):
    def test_roundtrip(self):
        raw = encode_frame(7, 0xDEADBEEF, b"hello")
        frames = StreamParser().feed(raw)
        self.assertEqual(len(frames), 1)
        fr = frames[0]
        self.assertEqual(fr.seq, 7)
        self.assertEqual(fr.prev_digest, 0xDEADBEEF)
        self.assertEqual(fr.payload, b"hello")
        self.assertEqual(fr.raw, raw)

    def test_empty_payload(self):
        data = build_log([b"", b"x", b""])
        result = scan(data)
        self.assertTrue(result.ok)
        self.assertEqual([f.payload for f in result.frames], [b"", b"x", b""])

    def test_binary_payload(self):
        payload = bytes(range(256)) * 4
        data = build_log([payload])
        result = scan(data)
        self.assertTrue(result.ok)
        self.assertEqual(result.frames[0].payload, payload)


class TestStreamingParser(unittest.TestCase):
    PAYLOADS = [b"alpha", b"", b"gamma-gamma-gamma", bytes(range(64)), b"z"]

    def test_arbitrary_chunk_sizes(self):
        data = build_log(self.PAYLOADS)
        expected = scan(data)
        self.assertTrue(expected.ok)
        for chunk_size in list(range(1, 12)) + [len(data)]:
            parser = StreamParser()
            got = []
            for i in range(0, len(data), chunk_size):
                got.extend(parser.feed(data[i:i + chunk_size]))
            self.assertEqual(parser.pending, b"")
            self.assertEqual([f.raw for f in got],
                             [f.raw for f in expected.frames],
                             msg=f"chunk_size={chunk_size}")

    def test_random_chunks(self):
        data = build_log(self.PAYLOADS)
        rng = random.Random(42)
        for _ in range(20):
            parser = StreamParser()
            got, pos = [], 0
            while pos < len(data):
                n = rng.randint(1, 17)
                got.extend(parser.feed(data[pos:pos + n]))
                pos += n
            self.assertEqual([f.payload for f in got], self.PAYLOADS)

    def test_partial_frame_pending(self):
        data = build_log([b"abcdef"])
        parser = StreamParser()
        self.assertEqual(parser.feed(data[:3]), [])       # 不足长度前缀
        self.assertEqual(parser.feed(data[3:-2]), [])      # 帧体未收齐
        frames = parser.feed(data[-2:])
        self.assertEqual(len(frames), 1)
        self.assertEqual(parser.pending, b"")


class TestPerByteTruncation(unittest.TestCase):
    """对日志的每一个字节截断点逐一验证: 只能是 ok 或 truncated, 绝不 corrupt."""

    PAYLOADS = [b"one", b"twotwo", b"", b"three" * 10]

    def test_every_byte_cut(self):
        data = build_log(self.PAYLOADS)
        bounds = frame_boundaries(self.PAYLOADS)
        for cut in range(len(data) + 1):
            result = scan(data[:cut])
            expected_good = max(b for b in bounds if b <= cut)
            if cut in bounds:
                self.assertEqual(result.status, "ok", f"cut={cut}")
                self.assertEqual(result.good_offset, cut)
            else:
                self.assertEqual(result.status, "truncated", f"cut={cut}")
                self.assertEqual(result.good_offset, expected_good, f"cut={cut}")
                # 截断后必须恢复到前帧且校验通过
                recovered = scan(data[:result.good_offset])
                self.assertTrue(recovered.ok, f"cut={cut}")
                self.assertEqual(len(recovered.frames),
                                 bounds.index(expected_good), f"cut={cut}")

    def test_every_byte_cut_file_level(self):
        """文件级: 每个截断点用 truncate_tail 恢复后 verify 必须通过."""
        data = build_log(self.PAYLOADS)
        with tempfile.TemporaryDirectory() as td:
            path = os.path.join(td, "log.bin")
            for cut in range(len(data) + 1):
                with open(path, "wb") as fh:
                    fh.write(data[:cut])
                result = truncate_tail(path)
                self.assertNotEqual(result.status, "corrupt", f"cut={cut}")
                self.assertTrue(scan_file(path).ok, f"cut={cut}")


class TestAcceptanceBoundaries(unittest.TestCase):
    def test_length_only_last_frame_recovers(self):
        """验收边界 1: 最后一帧只写了长度字段 -> 可恢复到前一帧."""
        data = build_log([b"first", b"second"])
        # 模拟撕裂写: 只写入第三帧的 4 字节长度前缀
        length = FRAME_OVERHEAD + 5
        torn = data + struct.pack(LEN_FMT, length)
        result = scan(torn)
        self.assertEqual(result.status, "truncated")
        self.assertEqual(result.good_offset, len(data))
        self.assertEqual([f.payload for f in result.frames], [b"first", b"second"])
        with tempfile.TemporaryDirectory() as td:
            path = os.path.join(td, "log.bin")
            with open(path, "wb") as fh:
                fh.write(torn)
            truncate_tail(path)
            self.assertEqual(os.path.getsize(path), len(data))
            self.assertTrue(scan_file(path).ok)

    def test_full_frame_bad_crc_is_corrupt_not_tail(self):
        """验收边界 2: 完整帧 CRC 错误必须报损坏, 不能当作可忽略尾部."""
        data = bytearray(build_log([b"aaa", b"bbb", b"ccc"]))
        # 翻转最后一帧 payload 中的一个字节 (帧长度不变, 仍是"完整"帧)
        last_frame_payload = len(data) - seglog.CRC_SIZE - 1
        data[last_frame_payload] ^= 0xFF
        result = scan(bytes(data))
        self.assertEqual(result.status, "corrupt")
        self.assertIn("crc mismatch", result.reason)
        # 不允许截断恢复
        with tempfile.TemporaryDirectory() as td:
            path = os.path.join(td, "log.bin")
            with open(path, "wb") as fh:
                fh.write(bytes(data))
            with self.assertRaises(CorruptError):
                truncate_tail(path)
            # 文件未被改动
            self.assertEqual(os.path.getsize(path), len(data))


class TestCorruption(unittest.TestCase):
    PAYLOADS = [b"f0", b"f1", b"f2", b"f3"]

    def test_middle_frame_corruption(self):
        """中间帧损坏: 报 corrupt, 且不得跳过损坏帧继续提交后续帧."""
        data = bytearray(build_log(self.PAYLOADS))
        bounds = frame_boundaries(self.PAYLOADS)
        data[bounds[1] + LEN_SIZE + 2] ^= 0x01  # 第 2 帧 (seq=1) 的 payload
        result = scan(bytes(data))
        self.assertEqual(result.status, "corrupt")
        self.assertEqual(result.good_offset, bounds[1])
        # 好帧只到损坏点之前, 后续帧不提交
        self.assertEqual([f.payload for f in result.frames], [b"f0"])

    def test_every_byte_flip_detected(self):
        """对日志每个字节做位翻转: 绝不误判 ok.

        非长度前缀字节 -> 必须报 corrupt; 长度前缀字节翻转后与撕裂写
        不可区分 (声明长度超出文件末尾), 允许报 truncated, 但同样不得 ok.
        """
        data = build_log(self.PAYLOADS)
        bounds = frame_boundaries(self.PAYLOADS)
        len_prefix_positions = set()
        for b in bounds[:-1]:
            len_prefix_positions.update(range(b, b + LEN_SIZE))
        for pos in range(len(data)):
            mutated = bytearray(data)
            mutated[pos] ^= 0xFF
            result = scan(bytes(mutated))
            self.assertNotEqual(result.status, "ok",
                                f"flip at {pos} missed")
            if pos not in len_prefix_positions:
                self.assertEqual(result.status, "corrupt",
                                 f"flip at {pos}: {result.status}")

    def test_chain_break_detected(self):
        """prev_digest 被篡改 (并重算本帧 CRC) -> 链校验失败."""
        frames = [encode_frame(0, seglog.GENESIS_PREV, b"a")]
        frames.append(encode_frame(1, 0x12345678, b"b"))  # 错误的 prev
        result = scan(b"".join(frames))
        self.assertEqual(result.status, "corrupt")
        self.assertIn("chain break", result.reason)

    def test_seq_gap_detected(self):
        frames = [encode_frame(0, seglog.GENESIS_PREV, b"a")]
        frames.append(encode_frame(5, seglog.crc32(frames[0]), b"b"))  # 跳号
        result = scan(b"".join(frames))
        self.assertEqual(result.status, "corrupt")
        self.assertIn("seq gap", result.reason)

    def test_append_refused_on_corrupt_and_truncated(self):
        with tempfile.TemporaryDirectory() as td:
            path = os.path.join(td, "log.bin")
            append_frame(path, b"x")
            with open(path, "ab") as fh:
                fh.write(struct.pack(LEN_FMT, 100))  # 撕裂尾部
            with self.assertRaises(CorruptError):
                append_frame(path, b"y")
            truncate_tail(path)
            append_frame(path, b"y")  # 截断后可继续追加
            result = scan_file(path)
            self.assertTrue(result.ok)
            self.assertEqual([f.payload for f in result.frames], [b"x", b"y"])
            # 损坏后拒绝追加
            seglog.inject_flip_byte(path, 0)
            with self.assertRaises(CorruptError):
                append_frame(path, b"z")


class TestIndex(unittest.TestCase):
    def test_rebuild_index(self):
        payloads = [b"aa", b"bbbb", b"c", b"dddddd"]
        data = build_log(payloads)
        with tempfile.TemporaryDirectory() as td:
            log = os.path.join(td, "log.bin")
            idx = os.path.join(td, "log.idx")
            with open(log, "wb") as fh:
                fh.write(data)
            entries = rebuild_index(log, idx)
            self.assertEqual([e[0] for e in entries], [0, 1, 2, 3])
            self.assertEqual([e[1] for e in entries], frame_boundaries(payloads)[:-1])
            for seq, offset, length in entries:
                self.assertEqual(data[offset:offset + 1], data[offset:offset + 1])
                self.assertEqual(length, FRAME_OVERHEAD + len(payloads[seq]))
            # 二进制索引文件可回读且一致
            self.assertEqual(read_index(idx), entries)
            # 用索引随机访问: offset 处解析出的帧 seq 一致
            for seq, offset, _ in entries:
                fr = StreamParser().feed(data[offset:])[0]
                self.assertEqual(fr.seq, seq)

    def test_index_ignores_incomplete_tail(self):
        data = build_log([b"a", b"b"])
        torn = data + b"\x00\x00"  # 半个长度前缀
        with tempfile.TemporaryDirectory() as td:
            log = os.path.join(td, "log.bin")
            with open(log, "wb") as fh:
                fh.write(torn)
            entries = rebuild_index(log)
            self.assertEqual(len(entries), 2)


class TestFaultInjection(unittest.TestCase):
    def test_flip_byte_and_truncate(self):
        data = build_log([b"hello", b"world"])
        with tempfile.TemporaryDirectory() as td:
            path = os.path.join(td, "log.bin")
            with open(path, "wb") as fh:
                fh.write(data)
            seglog.inject_flip_byte(path, 5)
            self.assertEqual(scan_file(path).status, "corrupt")
            with open(path, "wb") as fh:
                fh.write(data)
            seglog.inject_truncate(path, len(data) - 3)
            self.assertEqual(scan_file(path).status, "truncated")


class TestCli(unittest.TestCase):
    def run_cli(self, *args, input_bytes=None):
        return subprocess.run(
            [sys.executable, "seglog.py", *args],
            capture_output=True, input=input_bytes, cwd=os.path.dirname(
                os.path.abspath(__file__)))

    def test_cli_workflow(self):
        with tempfile.TemporaryDirectory() as td:
            log = os.path.join(td, "log.bin")
            idx = os.path.join(td, "log.idx")
            r = self.run_cli("append", log, "--text", "hello")
            self.assertEqual(r.returncode, 0, r.stderr)
            r = self.run_cli("append", log, input_bytes=b"\x00\x01\x02")
            self.assertEqual(r.returncode, 0, r.stderr)
            r = self.run_cli("verify", log)
            self.assertEqual(r.returncode, 0, r.stdout)
            self.assertIn(b"status=ok", r.stdout)
            r = self.run_cli("index", log, idx)
            self.assertEqual(r.returncode, 0)
            self.assertTrue(os.path.exists(idx))
            r = self.run_cli("dump", log)
            self.assertEqual(r.returncode, 0)
            self.assertIn(b"seq=0", r.stdout)
            # 注入截断 -> verify 退出码 2 -> truncate 修复 -> verify 退出码 0
            size = os.path.getsize(log)
            r = self.run_cli("inject", "truncate", log, str(size - 1))
            self.assertEqual(r.returncode, 0, r.stderr)
            r = self.run_cli("verify", log)
            self.assertEqual(r.returncode, 2, r.stdout)
            r = self.run_cli("truncate", log)
            self.assertEqual(r.returncode, 0, r.stderr)
            r = self.run_cli("verify", log)
            self.assertEqual(r.returncode, 0, r.stdout)
            # 注入字节翻转 (首帧 payload 区) -> verify 退出码 1 -> truncate 拒绝
            r = self.run_cli("inject", "flip-byte", log, "16")
            self.assertEqual(r.returncode, 0, r.stderr)
            r = self.run_cli("verify", log)
            self.assertEqual(r.returncode, 1, r.stdout)
            r = self.run_cli("truncate", log)
            self.assertEqual(r.returncode, 1)


if __name__ == "__main__":
    unittest.main(verbosity=2)
