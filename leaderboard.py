"""撤回式排名游标（snapshot-pinned ranking cursor）。

排行榜按 (分数降序, ID 升序) 排序。每次写入/删除发布一个不可变版本；
游标打开时钉住当前版本，之后的修订与删除不影响该游标的分页结果；
新游标看到最新版本。版本由租约保护，过期游标显式报错，绝不悄悄切换版本。

仅依赖 Python 3.11 标准库。所有示例数据均为合成数据。
"""

from __future__ import annotations

import argparse
import itertools
import sys
from collections import deque
import threading
import time
from dataclasses import dataclass


class LeaderboardError(Exception):
    """组件错误基类。"""


class UnknownCursorError(LeaderboardError):
    """游标令牌不存在。"""


class CursorExpiredError(LeaderboardError):
    """游标租约已过期（或其钉住的版本已被回收）。明确报错，不切换版本。"""


class EntryNotFoundError(LeaderboardError):
    """要删除的条目不存在。"""


@dataclass(frozen=True)
class _Version:
    version_id: int
    items: tuple  # tuple[tuple[entry_id, score], ...]，已按 (-score, id) 排序


@dataclass
class _Cursor:
    token: str
    version_id: int
    offset: int
    expires_at: float


def _sort_items(entries: dict) -> tuple:
    return tuple(sorted(entries.items(), key=lambda kv: (-kv[1], kv[0])))


class Leaderboard:
    """线程安全的版本化排行榜。"""

    def __init__(self, lease_seconds: float = 60.0, clock=time.monotonic):
        if lease_seconds <= 0:
            raise ValueError("lease_seconds 必须为正数")
        self._lease_seconds = lease_seconds
        self._clock = clock
        self._lock = threading.RLock()
        self._entries: dict[str, float] = {}
        self._versions: dict[int, _Version] = {}
        self._current_version_id = 0
        self._cursors: dict[str, _Cursor] = {}
        # 已过期游标的墓碑（有界），保证过期游标报 CURSOR_EXPIRED 而非 UNKNOWN_CURSOR
        self._expired_tokens: set[str] = set()
        self._expired_order: deque[str] = deque()
        self._token_counter = itertools.count(1)
        self._publish()  # v1：空排行榜

    # ---- 写入路径：每次变更发布一个不可变新版本 ----

    def _publish(self) -> int:
        self._current_version_id += 1
        version = _Version(self._current_version_id, _sort_items(self._entries))
        self._versions[version.version_id] = version
        return version.version_id

    def set_score(self, entry_id: str, score: float) -> int:
        """写入/修订分数，返回新版本号。"""
        with self._lock:
            self._entries[entry_id] = score
            return self._publish()

    def delete(self, entry_id: str) -> int:
        """删除条目，返回新版本号；条目不存在则报错。"""
        with self._lock:
            if entry_id not in self._entries:
                raise EntryNotFoundError(f"条目不存在: {entry_id!r}")
            del self._entries[entry_id]
            return self._publish()

    # ---- 游标 ----

    def open_cursor(self) -> tuple[str, int]:
        """在当前版本上打开游标，返回 (token, version_id)。"""
        with self._lock:
            token = f"cur-{next(self._token_counter)}"
            self._cursors[token] = _Cursor(
                token=token,
                version_id=self._current_version_id,
                offset=0,
                expires_at=self._clock() + self._lease_seconds,
            )
            return token, self._current_version_id

    def _get_live_cursor(self, token: str) -> _Cursor:
        cursor = self._cursors.get(token)
        if cursor is None:
            if token in self._expired_tokens:
                raise CursorExpiredError(
                    f"游标 {token!r} 租约已过期且已被回收，请重新打开游标"
                )
            raise UnknownCursorError(f"未知游标: {token!r}")
        if self._clock() >= cursor.expires_at:
            raise CursorExpiredError(
                f"游标 {token!r} 租约已过期（钉住版本 v{cursor.version_id}），"
                "请重新打开游标"
            )
        return cursor

    def fetch_page(self, token: str, limit: int) -> tuple[list, str | None]:
        """取下一页，返回 (items, next_token)。到底时 next_token 为 None。

        items 为 [(entry_id, score), ...]。成功取页会续租。
        """
        if limit <= 0:
            raise ValueError("limit 必须为正整数")
        with self._lock:
            cursor = self._get_live_cursor(token)
            version = self._versions.get(cursor.version_id)
            if version is None:
                raise CursorExpiredError(
                    f"游标 {token!r} 钉住的版本 v{cursor.version_id} 已被回收"
                )
            items = list(version.items[cursor.offset : cursor.offset + limit])
            cursor.offset += len(items)
            cursor.expires_at = self._clock() + self._lease_seconds  # 活跃续租
            next_token = token if cursor.offset < len(version.items) else None
            return items, next_token

    # ---- 租约回收 ----

    def gc(self) -> list[int]:
        """清理过期游标，回收无活跃游标钉住的非当前版本。返回被回收版本号。"""
        with self._lock:
            now = self._clock()
            expired = [t for t, c in self._cursors.items() if now >= c.expires_at]
            for token in expired:
                del self._cursors[token]
                self._expired_tokens.add(token)
                self._expired_order.append(token)
            while len(self._expired_order) > 10000:  # 墓碑容量上限
                self._expired_tokens.discard(self._expired_order.popleft())
            pinned = {c.version_id for c in self._cursors.values()}
            pinned.add(self._current_version_id)  # 当前版本永不回收
            reclaimed = [v for v in self._versions if v not in pinned]
            for version_id in reclaimed:
                del self._versions[version_id]
            return sorted(reclaimed)

    # ---- 只读辅助 ----

    def current_version_id(self) -> int:
        with self._lock:
            return self._current_version_id

    def version_items(self, version_id: int) -> list:
        with self._lock:
            version = self._versions.get(version_id)
            if version is None:
                raise CursorExpiredError(f"版本 v{version_id} 不存在或已被回收")
            return list(version.items)


# ---- 命令行界面 ----
#
# 输入：stdin 每行一条命令（空行与 # 开头的行被忽略）：
#   set <id> <score>     写入/修订分数
#   delete <id>          删除条目
#   open                 在当前版本打开游标
#   page <token> <n>     取下一页（n 条）
#   gc                   回收过期租约与无引用旧版本
#   snapshot             打印当前版本内容
#   quit                 退出
#
# 输出：stdout 每行一条结果：
#   OK v<version>                          set/delete 成功
#   CURSOR <token> v<version>              open 成功
#   PAGE <id:score> ... NEXT <token|END>   page 结果
#   GC v<id> ... | GC NONE                 回收的版本
#   SNAP v<version> <id:score> ...         当前快照
#   ERROR <CODE> <message>                 错误（CURSOR_EXPIRED / UNKNOWN_CURSOR /
#                                          NOT_FOUND / BAD_INPUT / UNKNOWN_COMMAND）


def _fmt_score(score: float) -> str:
    return f"{score:g}"


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        prog="leaderboard",
        description="撤回式排名游标：版本化排行榜 + 钉版本分页游标（行协议 stdin/stdout）",
    )
    parser.add_argument("--lease-seconds", type=float, default=60.0,
                        help="游标租约时长（秒），默认 60")
    args = parser.parse_args(argv)
    board = Leaderboard(lease_seconds=args.lease_seconds)
    out = sys.stdout

    for line in sys.stdin:
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        parts = line.split()
        command = parts[0].lower()
        try:
            if command == "set":
                version_id = board.set_score(parts[1], float(parts[2]))
                print(f"OK v{version_id}", file=out)
            elif command == "delete":
                version_id = board.delete(parts[1])
                print(f"OK v{version_id}", file=out)
            elif command == "open":
                token, version_id = board.open_cursor()
                print(f"CURSOR {token} v{version_id}", file=out)
            elif command == "page":
                items, next_token = board.fetch_page(parts[1], int(parts[2]))
                body = " ".join(f"{i}:{_fmt_score(s)}" for i, s in items)
                tail = f" {body}" if body else ""
                print(f"PAGE{tail} NEXT {next_token or 'END'}", file=out)
            elif command == "gc":
                reclaimed = board.gc()
                tail = " ".join(f"v{v}" for v in reclaimed) or "NONE"
                print(f"GC {tail}", file=out)
            elif command == "snapshot":
                version_id = board.current_version_id()
                items = board.version_items(version_id)
                body = " ".join(f"{i}:{_fmt_score(s)}" for i, s in items)
                tail = f" {body}" if body else ""
                print(f"SNAP v{version_id}{tail}", file=out)
            elif command in ("quit", "exit"):
                break
            else:
                print(f"ERROR UNKNOWN_COMMAND {command}", file=out)
        except CursorExpiredError as exc:
            print(f"ERROR CURSOR_EXPIRED {exc}", file=out)
        except UnknownCursorError as exc:
            print(f"ERROR UNKNOWN_CURSOR {exc}", file=out)
        except EntryNotFoundError as exc:
            print(f"ERROR NOT_FOUND {exc}", file=out)
        except (ValueError, IndexError) as exc:
            print(f"ERROR BAD_INPUT {exc}", file=out)
        out.flush()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
