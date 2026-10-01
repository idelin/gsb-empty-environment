"""可恢复的外存洗牌：预写日志 + 检查点，崩溃恢复与连续运行逐项一致。

磁盘布局（workdir 下）：
  data.bin        定长记录文件（8 字节无符号整数），原地执行 Fisher-Yates
  journal.json    当前步的预写日志：绝对写集合 + 抽随机后的随机源状态
  checkpoint.json 已提交状态：交换前沿、随机源状态、已发布位置、seed/样本量

每步的持久化顺序：
  1. 抽 j = randbelow(frontier+1)（随机抽样只依赖逻辑步，与 I/O 块大小、
     读写重试完全无关；重试都是幂等的绝对写，不消耗随机性）
  2. 写日志（绝对写：data[i]:=rj, data[j]:=ri）并落盘
  3. 应用数据写并落盘
  4. 原子替换写检查点（frontier-1、抽后随机状态、已发布位置）
  5. 删除日志

恢复：若日志存在且与检查点前沿一致，则重放日志中的绝对写（幂等），
推进检查点后继续。绝不重新抽取已提交的随机数，因此不会重复交换，
也不会造成元素重复或缺失。
"""
from __future__ import annotations

import json
import os
import struct
from pathlib import Path

from .prng import randbelow
from .shuffle import restore_source, state_from_json, state_to_json

RECORD = struct.Struct("<Q")


class InjectedCrash(Exception):
    """测试用：模拟进程在持久化操作边界崩溃。"""


class ExternalShuffle:
    def __init__(self, workdir, fail_after=None, sync: bool = True):
        self.workdir = Path(workdir)
        self.data_path = self.workdir / "data.bin"
        self.journal_path = self.workdir / "journal.json"
        self.checkpoint_path = self.workdir / "checkpoint.json"
        self.fail_after = fail_after  # 第 N 个持久化操作前注入崩溃（仅测试）
        self.sync = sync
        self.op_count = 0
        self.on_step = None  # 可选钩子 on_step(frontier, j)

    # ---- 持久化原语 ----

    def _op(self, fn):
        if self.fail_after is not None and self.op_count >= self.fail_after:
            raise InjectedCrash(f"injected crash before op #{self.op_count}")
        self.op_count += 1
        return fn()

    def _fsync_dir(self):
        if not self.sync:
            return
        fd = os.open(self.workdir, os.O_RDONLY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)

    def _write_json_atomic(self, path: Path, obj: dict):
        tmp = path.with_name(path.name + ".tmp")
        with open(tmp, "w") as f:
            json.dump(obj, f)
            f.flush()
            if self.sync:
                os.fsync(f.fileno())
        os.replace(tmp, path)
        self._fsync_dir()

    def _read_json(self, path: Path):
        try:
            with open(path) as f:
                return json.load(f)
        except (FileNotFoundError, json.JSONDecodeError, OSError):
            return None  # 缺失或撕裂写都视为不存在

    def _remove_journal(self):
        self.journal_path.unlink(missing_ok=True)
        self._fsync_dir()

    def _read_record(self, pos: int) -> bytes:
        with open(self.data_path, "rb") as f:
            f.seek(pos * RECORD.size)
            data = f.read(RECORD.size)
        if len(data) != RECORD.size:
            raise OSError(f"short read at record {pos}")
        return data

    def _write_record(self, pos: int, data: bytes):
        with open(self.data_path, "r+b") as f:
            f.seek(pos * RECORD.size)
            f.write(data)
            f.flush()
            if self.sync:
                os.fsync(f.fileno())

    # ---- 初始化与检查点 ----

    def initialize(self, values, source) -> None:
        """写入初始数据并建立起始检查点（前沿 = n-1，无已发布位置）。"""
        self.workdir.mkdir(parents=True, exist_ok=True)

        def write_data():
            with open(self.data_path, "wb") as f:
                for v in values:
                    f.write(RECORD.pack(v))
                f.flush()
                if self.sync:
                    os.fsync(f.fileno())

        self._op(write_data)
        ckpt = {
            "n": len(values),
            "frontier": len(values) - 1,
            "rng_state": state_to_json(source.getstate()),
            "seed": getattr(source, "seed", None),
            "draws": getattr(source, "draws", 0),
            "bits": getattr(source, "bits", 0),
            "published": [],
            "done": len(values) <= 1,
        }
        self._op(lambda: self._write_json_atomic(self.checkpoint_path, ckpt))
        self._remove_journal()

    def _read_checkpoint(self) -> dict:
        ckpt = self._read_json(self.checkpoint_path)
        if ckpt is None:
            raise RuntimeError("no valid checkpoint; initialize() first")
        return ckpt

    @staticmethod
    def _next_checkpoint(ckpt: dict, journal: dict) -> dict:
        i = journal["frontier"]
        n = ckpt["n"]
        published_start = i if i > 1 else 0
        return {
            "n": n,
            "frontier": i - 1,
            "rng_state": journal["rng_state_after"],
            "seed": ckpt["seed"],
            "draws": journal["draws"],
            "bits": journal["bits"],
            "published": list(range(published_start, n)),
            "done": i - 1 < 1,
        }

    # ---- 单步执行 ----

    def step(self, source) -> bool:
        ckpt = self._read_checkpoint()
        if ckpt["done"] or ckpt["frontier"] < 1:
            return False
        i = ckpt["frontier"]
        restore_source(source, ckpt)
        j = randbelow(source, i + 1)  # 抽样只依赖逻辑步 i
        ri = self._read_record(i)     # 读可任意重试，不影响随机序列
        rj = self._read_record(j)
        journal = {
            "frontier": i,
            "j": j,
            "writes": [[i, rj.hex()], [j, ri.hex()]],
            "rng_state_after": state_to_json(source.getstate()),
            "draws": getattr(source, "draws", None),
            "bits": getattr(source, "bits", None),
        }
        self._op(lambda: self._write_json_atomic(self.journal_path, journal))
        self._op(lambda: self._write_record(i, rj))
        self._op(lambda: self._write_record(j, ri))
        if self.on_step is not None:
            self.on_step(i, j)
        next_ckpt = self._next_checkpoint(ckpt, journal)
        self._op(lambda: self._write_json_atomic(self.checkpoint_path, next_ckpt))
        self._op(self._remove_journal)
        return True

    # ---- 崩溃恢复 ----

    def recover(self, source) -> None:
        """重放未提交日志（幂等绝对写），恢复随机源到已提交状态。"""
        ckpt = self._read_checkpoint()
        journal = self._read_json(self.journal_path)
        if journal is not None and journal.get("frontier") == ckpt["frontier"]:
            for pos, hexval in journal["writes"]:
                self._write_record(pos, bytes.fromhex(hexval))
            ckpt = self._next_checkpoint(ckpt, journal)
            self._write_json_atomic(self.checkpoint_path, ckpt)
            self._remove_journal()
        elif journal is not None:
            self._remove_journal()  # 陈旧/撕裂日志：对应步从未提交，安全丢弃
        restore_source(source, ckpt)

    # ---- 驱动与读取 ----

    def run(self, source) -> list:
        while self.step(source):
            pass
        return self.read_all()

    def read_all(self) -> list:
        out = []
        with open(self.data_path, "rb") as f:
            while True:
                chunk = f.read(RECORD.size)
                if not chunk:
                    break
                out.append(RECORD.unpack(chunk)[0])
        return out
