"""源库：sqlite 实现。

数据表之外维护一张 cdc_log（本地事务日志，模拟 CDC 位点来源），
每次提交把该事务的变更连同全局单调递增的 commit_seq 一并写入。
快照读通过 WAL 模式下独立的只读事务获得一致性视图。
"""

from __future__ import annotations

import json
import re
import sqlite3
from typing import Iterator, Optional, Sequence

from .models import ChangeEvent, OP_DELETE

_IDENT = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")


def check_ident(name: str) -> str:
    if not _IDENT.match(name):
        raise ValueError(f"非法标识符: {name!r}")
    return name


def _connect(path: str) -> sqlite3.Connection:
    conn = sqlite3.connect(path, isolation_level=None)
    conn.execute("PRAGMA journal_mode=WAL")
    conn.execute("PRAGMA busy_timeout=5000")
    return conn


class SourceDatabase:
    """变更的来源。数据表结构统一为 (pk TEXT PRIMARY KEY, data TEXT JSON)。"""

    def __init__(self, path: str):
        self.path = path
        self._conn = _connect(path)
        self._conn.executescript(
            """
            CREATE TABLE IF NOT EXISTS __tables(name TEXT PRIMARY KEY);
            CREATE TABLE IF NOT EXISTS __seq(
                id INTEGER PRIMARY KEY CHECK (id = 1),
                value INTEGER NOT NULL
            );
            INSERT OR IGNORE INTO __seq(id, value) VALUES (1, 0);
            CREATE TABLE IF NOT EXISTS cdc_log(
                commit_seq INTEGER NOT NULL,
                tx_id      TEXT NOT NULL,
                tbl        TEXT NOT NULL,
                pk         TEXT NOT NULL,
                op         TEXT NOT NULL,
                before     TEXT,
                after      TEXT
            );
            CREATE INDEX IF NOT EXISTS idx_cdc_log_seq ON cdc_log(commit_seq);
            """
        )

    def close(self) -> None:
        self._conn.close()

    def _ensure_table(self, tbl: str) -> None:
        check_ident(tbl)
        self._conn.execute(
            f'CREATE TABLE IF NOT EXISTS "{tbl}"('
            "pk TEXT PRIMARY KEY, data TEXT NOT NULL)"
        )
        self._conn.execute("INSERT OR IGNORE INTO __tables(name) VALUES (?)", (tbl,))

    def apply_transaction(self, events: Sequence[ChangeEvent]) -> list[ChangeEvent]:
        """把同一事务的变更作为一个原子提交落库，返回分配了 commit_seq 的事件。"""
        if not events:
            return []
        cur = self._conn.cursor()
        cur.execute("BEGIN IMMEDIATE")
        try:
            cur.execute("UPDATE __seq SET value = value + 1 WHERE id = 1")
            seq = cur.execute("SELECT value FROM __seq WHERE id = 1").fetchone()[0]
            assigned = []
            for ev in events:
                if ev.commit_seq is not None:
                    raise ValueError("事件不应自带 commit_seq，由源库提交时分配")
                self._ensure_table(ev.table)
                if ev.op == OP_DELETE:
                    cur.execute(f'DELETE FROM "{ev.table}" WHERE pk = ?', (ev.pk,))
                else:
                    cur.execute(
                        f'INSERT INTO "{ev.table}"(pk, data) VALUES (?, ?) '
                        "ON CONFLICT(pk) DO UPDATE SET data = excluded.data",
                        (ev.pk, json.dumps(ev.after, ensure_ascii=False, sort_keys=True)),
                    )
                cur.execute(
                    "INSERT INTO cdc_log VALUES (?, ?, ?, ?, ?, ?, ?)",
                    (
                        seq,
                        ev.tx_id,
                        ev.table,
                        ev.pk,
                        ev.op,
                        json.dumps(ev.before, ensure_ascii=False, sort_keys=True)
                        if ev.before is not None
                        else None,
                        json.dumps(ev.after, ensure_ascii=False, sort_keys=True)
                        if ev.after is not None
                        else None,
                    ),
                )
                assigned.append(ev.with_seq(seq))
            cur.execute("COMMIT")
            return assigned
        except BaseException:
            cur.execute("ROLLBACK")
            raise

    def max_commit_seq(self) -> int:
        row = self._conn.execute("SELECT COALESCE(MAX(commit_seq), 0) FROM cdc_log").fetchone()
        return int(row[0])

    def fetch_changes_since(self, commit_seq: int) -> list[ChangeEvent]:
        """读取 commit_seq 严格大于给定值的全部变更（本地日志，不接消息平台）。"""
        rows = self._conn.execute(
            "SELECT tx_id, tbl, pk, op, before, after, commit_seq "
            "FROM cdc_log WHERE commit_seq > ? ORDER BY commit_seq, rowid",
            (commit_seq,),
        ).fetchall()
        return [
            ChangeEvent(
                tx_id=tx_id,
                table=tbl,
                pk=pk,
                op=op,
                before=json.loads(before) if before is not None else None,
                after=json.loads(after) if after is not None else None,
                commit_seq=seq,
            )
            for tx_id, tbl, pk, op, before, after, seq in rows
        ]

    def open_snapshot(self) -> "SnapshotReader":
        return SnapshotReader(self.path)

    def table_rows(self, tbl: str) -> dict[str, dict]:
        """当前最新状态（校验用，非一致性视图）。"""
        check_ident(tbl)
        return {
            pk: json.loads(data)
            for pk, data in self._conn.execute(f'SELECT pk, data FROM "{tbl}"')
        }

    def tables(self) -> list[str]:
        return [r[0] for r in self._conn.execute("SELECT name FROM __tables ORDER BY name")]


class SnapshotReader:
    """一致性快照：独立连接的只读事务，水位与数据取自同一视图。"""

    def __init__(self, path: str):
        self._conn = _connect(path)
        self._conn.execute("BEGIN")
        # 水位与后续所有数据读取处于同一快照视图，二者天然一致。
        self.watermark: int = int(
            self._conn.execute(
                "SELECT COALESCE(MAX(commit_seq), 0) FROM cdc_log"
            ).fetchone()[0]
        )

    def tables(self) -> list[str]:
        return [r[0] for r in self._conn.execute("SELECT name FROM __tables ORDER BY name")]

    def iter_rows(self, tbl: str) -> Iterator[tuple[str, dict]]:
        check_ident(tbl)
        for pk, data in self._conn.execute(f'SELECT pk, data FROM "{tbl}" ORDER BY pk'):
            yield pk, json.loads(data)

    def close(self) -> None:
        try:
            self._conn.execute("ROLLBACK")
        finally:
            self._conn.close()
