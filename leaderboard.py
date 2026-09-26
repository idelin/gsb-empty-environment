#!/usr/bin/env python3
"""撤回式排名游标：基于不可变版本快照的排行榜分页游标。

排序规则：分数降序，分数相同按 ID 升序。

核心语义：
- 每次变更（set/delete）产生一个新的不可变版本快照。
- open_cursor 将游标固定（pin）在打开时的最新版本上；
  之后的分数修订与删除不影响已打开游标，不会重复或漏项。
- 新打开的游标看到新版本。
- 游标持有租约（lease），每次成功翻页滑动续租。
  租约过期后翻页明确抛出 CursorExpiredError，绝不悄悄切换版本。
- collect() 回收没有任何存活租约引用的旧版本（最新版本始终保留）。
"""
from __future__ import annotations

import argparse
import json
import shlex
import sys
import threading
import time
from dataclasses import dataclass


class CursorExpiredError(Exception):
    """游标租约已过期（或游标不存在/已被回收）。"""


@dataclass
class _Cursor:
    version: int
    offset: int
    expires_at: float


class Leaderboard:
    """线程安全的排行榜，支持版本化快照与撤回式分页游标。"""

    def __init__(self, lease_ttl: float = 60.0, clock=time.monotonic):
        self._lock = threading.RLock()
        self._clock = clock
        self._lease_ttl = float(lease_ttl)
        self._scores: dict[str, float] = {}
        self._versions: dict[int, tuple[tuple[str, float], ...]] = {1: ()}
        self._latest = 1
        self._cursors: dict[str, _Cursor] = {}
        self._cursor_seq = 0

    # ---- 写操作（每次真实变更产生一个新版本） ----

    def _commit(self) -> int:
        self._latest += 1
        snapshot = tuple(sorted(self._scores.items(), key=lambda kv: (-kv[1], kv[0])))
        self._versions[self._latest] = snapshot
        return self._latest

    def set(self, entry_id: str, score: float) -> int:
        """插入或修订分数，返回新版本号。"""
        with self._lock:
            self._scores[str(entry_id)] = float(score)
            return self._commit()

    def delete(self, entry_id: str) -> int:
        """删除条目，返回新版本号；条目不存在时不产生新版本。"""
        with self._lock:
            if entry_id not in self._scores:
                return self._latest
            del self._scores[entry_id]
            return self._commit()

    # ---- 游标操作 ----

    def open_cursor(self) -> str:
        """打开游标并固定在当前最新版本上，返回游标 ID。"""
        with self._lock:
            self._cursor_seq += 1
            cursor_id = f"c{self._cursor_seq}"
            self._cursors[cursor_id] = _Cursor(
                version=self._latest,
                offset=0,
                expires_at=self._clock() + self._lease_ttl,
            )
            return cursor_id

    def _live_cursor(self, cursor_id: str) -> _Cursor:
        cursor = self._cursors.get(cursor_id)
        if cursor is None:
            raise CursorExpiredError(f"cursor {cursor_id!r} 不存在或已被回收")
        if self._clock() >= cursor.expires_at:
            raise CursorExpiredError(f"cursor {cursor_id!r} 租约已过期")
        return cursor

    def page(self, cursor_id: str, size: int) -> tuple[list[tuple[str, float]], bool]:
        """取下一页，返回 (entries, done)。entries 为 [(id, score), ...]。

        游标过期时抛出 CursorExpiredError，绝不悄悄切换到其他版本。
        每次成功翻页滑动续租。
        """
        if size < 0:
            raise ValueError("size 必须 >= 0")
        with self._lock:
            cursor = self._live_cursor(cursor_id)
            snapshot = self._versions[cursor.version]
            entries = list(snapshot[cursor.offset : cursor.offset + size])
            cursor.offset += len(entries)
            cursor.expires_at = self._clock() + self._lease_ttl
            return entries, cursor.offset >= len(snapshot)

    def close(self, cursor_id: str) -> bool:
        """显式撤回游标，释放其租约；返回是否确实存在该游标。"""
        with self._lock:
            return self._cursors.pop(cursor_id, None) is not None

    # ---- 版本回收与查询 ----

    def collect(self) -> list[int]:
        """回收过期游标及无存活租约引用的旧版本，返回被回收的版本号。"""
        with self._lock:
            now = self._clock()
            expired = [cid for cid, c in self._cursors.items() if now >= c.expires_at]
            for cid in expired:
                del self._cursors[cid]
            pinned = {c.version for c in self._cursors.values()}
            collected = [
                v for v in self._versions if v != self._latest and v not in pinned
            ]
            for v in collected:
                del self._versions[v]
            return sorted(collected)

    @property
    def current_version(self) -> int:
        with self._lock:
            return self._latest

    def cursor_version(self, cursor_id: str) -> int:
        with self._lock:
            return self._live_cursor(cursor_id).version

    def snapshot(self, version: int) -> tuple[tuple[str, float], ...]:
        """读取指定版本的不可变快照；版本不存在（已回收）时抛 KeyError。"""
        with self._lock:
            return self._versions[version]


HELP = (
    "set <id> <score>  插入/修订分数",
    "del <id>          删除条目",
    "open              打开游标（固定在当前最新版本）",
    "page <cursor> <n> 取下一页（最多 n 条）",
    "close <cursor>    显式撤回游标",
    "gc                回收过期游标与无引用旧版本",
    "version           显示当前最新版本号",
    "help              显示帮助",
    "quit              退出",
)


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description="撤回式排名游标排行榜 CLI（JSON Lines 输出）")
    parser.add_argument("--ttl", type=float, default=60.0, help="游标租约秒数，默认 60")
    args = parser.parse_args(argv)
    board = Leaderboard(lease_ttl=args.ttl)

    def emit(obj):
        print(json.dumps(obj, ensure_ascii=False), flush=True)

    for line in sys.stdin:
        parts = shlex.split(line)
        if not parts:
            continue
        cmd, *rest = parts
        try:
            if cmd == "set":
                emit({"ok": True, "version": board.set(rest[0], float(rest[1]))})
            elif cmd == "del":
                emit({"ok": True, "version": board.delete(rest[0])})
            elif cmd == "open":
                cid = board.open_cursor()
                emit({"ok": True, "cursor": cid, "version": board.cursor_version(cid)})
            elif cmd == "page":
                entries, done = board.page(rest[0], int(rest[1]))
                emit({
                    "ok": True,
                    "entries": [{"id": i, "score": s} for i, s in entries],
                    "done": done,
                })
            elif cmd == "close":
                emit({"ok": True, "closed": board.close(rest[0])})
            elif cmd == "gc":
                emit({"ok": True, "collected": board.collect()})
            elif cmd == "version":
                emit({"ok": True, "version": board.current_version})
            elif cmd == "help":
                emit({"ok": True, "commands": list(HELP)})
            elif cmd in ("quit", "exit"):
                break
            else:
                emit({"ok": False, "error": f"未知命令: {cmd}"})
        except CursorExpiredError as exc:
            emit({"ok": False, "error": f"cursor_expired: {exc}"})
        except (ValueError, IndexError, KeyError) as exc:
            emit({"ok": False, "error": f"bad_request: {exc}"})
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
