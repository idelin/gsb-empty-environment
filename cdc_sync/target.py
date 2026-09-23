"""目标库：staging 区构建 + 单事务原子换名发布。

所有写入先落在 stg__* 暂存表；发布时在一个事务里把 stg__* 换名为
live__*，对外只会看到旧一代或新一代，绝不出现混合状态。快照或回放
任何一步失败，只需丢弃暂存表，live__* 保持原样。
"""

from __future__ import annotations

import json
import sqlite3
from typing import Optional

from .models import ChangeEvent, OP_DELETE
from .source import check_ident

STG_PREFIX = "stg__"
LIVE_PREFIX = "live__"
TOMBSTONES = "tombstones"


class TargetDatabase:
    def __init__(self, path: str):
        self.path = path
        self._conn = sqlite3.connect(path, isolation_level=None)
        self._conn.execute("PRAGMA journal_mode=WAL")
        self._conn.execute("PRAGMA busy_timeout=5000")

    def close(self) -> None:
        self._conn.close()

    def _tables_with_prefix(self, prefix: str) -> set[str]:
        rows = self._conn.execute(
            "SELECT name FROM sqlite_master WHERE type = 'table' AND name LIKE ?",
            (prefix + "%",),
        ).fetchall()
        return {r[0] for r in rows}

    def reset_staging(self) -> None:
        for name in self._tables_with_prefix(STG_PREFIX):
            self._conn.execute(f'DROP TABLE IF EXISTS "{name}"')

    def abort_staging(self) -> None:
        """快照/回放失败时调用：丢弃暂存区，已发布状态不受影响。"""
        self._conn.rollback()
        self.reset_staging()

    def ensure_staging_table(self, tbl: str) -> None:
        check_ident(tbl)
        self._conn.execute(
            f'CREATE TABLE IF NOT EXISTS "{STG_PREFIX}{tbl}"('
            "pk TEXT PRIMARY KEY, data TEXT NOT NULL, seq INTEGER NOT NULL)"
        )
        self._conn.execute(
            f'CREATE TABLE IF NOT EXISTS "{STG_PREFIX}{TOMBSTONES}"('
            "tbl TEXT NOT NULL, pk TEXT NOT NULL, seq INTEGER NOT NULL, "
            "PRIMARY KEY (tbl, pk))"
        )

    def stage_row(self, tbl: str, pk: str, data: dict, seq: int) -> None:
        """写入快照行，行上携带水位序号，供回放时做幂等比较。"""
        check_ident(tbl)
        self._conn.execute(
            f'INSERT INTO "{STG_PREFIX}{tbl}"(pk, data, seq) VALUES (?, ?, ?)',
            (pk, json.dumps(data, ensure_ascii=False, sort_keys=True), seq),
        )

    def apply_event(self, event: ChangeEvent) -> bool:
        """应用一条缓存事件；已包含或已过期的变更直接跳过（防双计、防复活）。

        每行/每个墓碑记录最近应用的 commit_seq，仅当事件的 commit_seq
        严格更大时才应用。删除留下墓碑，避免乱序到达的旧更新把行复活。
        返回 True 表示实际应用，False 表示被幂等跳过。
        """
        if event.commit_seq is None:
            raise ValueError("回放的事件必须携带 commit_seq")
        tbl = check_ident(event.table)
        self.ensure_staging_table(tbl)
        cur = self._conn.cursor()
        row = cur.execute(
            f'SELECT seq FROM "{STG_PREFIX}{tbl}" WHERE pk = ?', (event.pk,)
        ).fetchone()
        tomb = cur.execute(
            f'SELECT seq FROM "{STG_PREFIX}{TOMBSTONES}" WHERE tbl = ? AND pk = ?',
            (tbl, event.pk),
        ).fetchone()
        known: Optional[int] = None
        for candidate in (row, tomb):
            if candidate is not None:
                known = candidate[0] if known is None else max(known, candidate[0])
        if known is not None and event.commit_seq <= known:
            return False
        if event.op == OP_DELETE:
            cur.execute(f'DELETE FROM "{STG_PREFIX}{tbl}" WHERE pk = ?', (event.pk,))
            cur.execute(
                f'INSERT INTO "{STG_PREFIX}{TOMBSTONES}"(tbl, pk, seq) VALUES (?, ?, ?) '
                "ON CONFLICT(tbl, pk) DO UPDATE SET seq = excluded.seq",
                (tbl, event.pk, event.commit_seq),
            )
        else:
            cur.execute(
                f'INSERT INTO "{STG_PREFIX}{tbl}"(pk, data, seq) VALUES (?, ?, ?) '
                "ON CONFLICT(pk) DO UPDATE SET data = excluded.data, seq = excluded.seq",
                (
                    event.pk,
                    json.dumps(event.after, ensure_ascii=False, sort_keys=True),
                    event.commit_seq,
                ),
            )
            cur.execute(
                f'DELETE FROM "{STG_PREFIX}{TOMBSTONES}" WHERE tbl = ? AND pk = ?',
                (tbl, event.pk),
            )
        return True

    def publish(self) -> None:
        """单事务内把整代 stg__* 换成 live__*；失败回滚则维持旧一代。"""
        staging = self._tables_with_prefix(STG_PREFIX)
        if not staging:
            return
        cur = self._conn.cursor()
        cur.execute("BEGIN IMMEDIATE")
        try:
            new_live = {LIVE_PREFIX + name[len(STG_PREFIX):] for name in staging}
            for old in self._tables_with_prefix(LIVE_PREFIX) - new_live:
                cur.execute(f'DROP TABLE IF EXISTS "{old}"')
            for stg in sorted(staging):
                live = LIVE_PREFIX + stg[len(STG_PREFIX):]
                cur.execute(f'DROP TABLE IF EXISTS "{live}"')
                cur.execute(f'ALTER TABLE "{stg}" RENAME TO "{live}"')
            cur.execute("COMMIT")
        except BaseException:
            cur.execute("ROLLBACK")
            raise

    def live_tables(self) -> list[str]:
        return sorted(
            name[len(LIVE_PREFIX):]
            for name in self._tables_with_prefix(LIVE_PREFIX)
            if name != LIVE_PREFIX + TOMBSTONES
        )

    def live_rows(self, tbl: str) -> dict[str, dict]:
        check_ident(tbl)
        name = LIVE_PREFIX + tbl
        if name not in self._tables_with_prefix(LIVE_PREFIX):
            return {}
        return {
            pk: json.loads(data)
            for pk, data in self._conn.execute(f'SELECT pk, data FROM "{name}"')
        }

    def live_tombstones(self) -> dict[tuple[str, str], int]:
        name = LIVE_PREFIX + TOMBSTONES
        if name not in self._tables_with_prefix(LIVE_PREFIX):
            return {}
        return {
            (tbl, pk): seq
            for tbl, pk, seq in self._conn.execute(f'SELECT tbl, pk, seq FROM "{name}"')
        }
