"""持久日志：幂等键与归因变更在同一记录中原子生效，恢复后保持一致。"""
from __future__ import annotations

import json
import os
import zlib
from typing import List, Optional

from .engine import Engine


class Journal:
    """追加式日志。每条记录为 {crc, payload} 信封，写坏/截断的尾部记录在恢复时丢弃。"""

    def __init__(self, path: str):
        self.path = path

    def append(self, record: dict) -> None:
        payload = json.dumps(record, sort_keys=True, ensure_ascii=False)
        envelope = {"crc": zlib.crc32(payload.encode("utf-8")), "payload": payload}
        line = json.dumps(envelope, ensure_ascii=False)
        with open(self.path, "a", encoding="utf-8") as fh:
            fh.write(line + "\n")
            fh.flush()
            os.fsync(fh.fileno())

    def _scan(self):
        """逐行校验，返回 (有效记录, 有效内容的字节长度)。"""
        records = []
        valid_bytes = 0
        if not os.path.exists(self.path):
            return records, valid_bytes
        with open(self.path, "rb") as fh:
            for raw in fh:
                try:
                    envelope = json.loads(raw.decode("utf-8"))
                    payload = envelope["payload"]
                    if zlib.crc32(payload.encode("utf-8")) != envelope["crc"]:
                        break  # 校验失败：视为崩溃截断，停止扫描
                    records.append(json.loads(payload))
                    valid_bytes += len(raw)
                except (json.JSONDecodeError, KeyError, TypeError, UnicodeDecodeError):
                    break  # 截断的尾部记录
        return records, valid_bytes

    def replay(self) -> List[dict]:
        return self._scan()[0]

    def truncate_tail(self) -> None:
        """截掉崩溃留下的无效尾部，保证后续追加落在干净位置。"""
        if not os.path.exists(self.path):
            return
        _, valid_bytes = self._scan()
        size = os.path.getsize(self.path)
        if valid_bytes < size:
            with open(self.path, "r+b") as fh:
                fh.truncate(valid_bytes)


class PersistentEngine:
    """带持久日志与幂等键的归因引擎。

    每条日志记录同时包含幂等键、操作与产生的归因变更，单条记录
    一次 fsync 落盘，因此幂等键与归因变更同事务生效；恢复时按
    日志回放，二者一起恢复，重复键被跳过。
    """

    def __init__(self, window: int, journal_path: Optional[str] = None):
        self.engine = Engine(window)
        self.journal = Journal(journal_path) if journal_path else None
        self.seen_keys = set()
        if self.journal:
            self.recover()

    def recover(self) -> None:
        self.journal.truncate_tail()
        for record in self.journal.replay():
            key = record["key"]
            if key in self.seen_keys:
                continue
            self.seen_keys.add(key)
            self.engine.apply(record["op"])

    def apply(self, key: str, op: dict) -> List[dict]:
        if key in self.seen_keys:
            return []  # 幂等：重复键不再生效
        changes = self.engine.apply(op)
        if self.journal:
            self.journal.append({"key": key, "op": op, "changes": changes})
        self.seen_keys.add(key)
        return changes

    @property
    def attributions(self):
        return self.engine.attributions

    @property
    def seq(self):
        return self.engine.seq

    def query_at(self, seq: int):
        return self.engine.query_at(seq)
