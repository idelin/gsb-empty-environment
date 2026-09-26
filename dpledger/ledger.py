"""差分隐私预算账本：预留 / 提交（发布）/ 取消。

持久事务设计：
- 预留、噪声结果生成、结果发布与预算扣减在同一个 SQLite 事务内完成，
  崩溃不会产生半提交状态。
- 提交边界故障枚举（fault_hooks 注入点）：
    1. "commit:before_txn"        —— 事务开始前崩溃：无任何持久化。
    2. "commit:before_db_commit"  —— 写入后、落库提交前崩溃：整体回滚。
    3. "commit:after_db_commit"   —— 落库提交后、返回调用方前崩溃：
       重试必须幂等地复用已持久化的噪声值与预算记录。
- 公开日志（events 表）只含公开字段，绝不写入种子 / RNG 状态等秘密。

组合规则：明示 epsilon 求和 —— 已发布查询的 epsilon 之和即为总消耗上界。
"""

from __future__ import annotations

import hashlib
import json
import sqlite3
import threading
import time

from .errors import (
    BudgetExhaustedError,
    ConflictError,
    InvalidStateError,
    UnknownQueryError,
)
from .noise import default_rng, laplace_sample

SCHEMA = """
CREATE TABLE IF NOT EXISTS meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS queries (
    query_id TEXT PRIMARY KEY,
    content_hash TEXT NOT NULL,
    content_json TEXT NOT NULL,
    epsilon REAL NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('RESERVED', 'PUBLISHED', 'CANCELLED')),
    created_at REAL NOT NULL,
    updated_at REAL NOT NULL
);
CREATE TABLE IF NOT EXISTS results (
    query_id TEXT PRIMARY KEY REFERENCES queries (query_id),
    true_value REAL NOT NULL,
    noisy_value REAL NOT NULL,
    published_at REAL NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    type TEXT NOT NULL CHECK (type IN ('RESERVE', 'PUBLISH', 'CANCEL')),
    query_id TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    at REAL NOT NULL
);
"""

STATE_RESERVED = "RESERVED"
STATE_PUBLISHED = "PUBLISHED"
STATE_CANCELLED = "CANCELLED"


def canonical_content(content: dict) -> str:
    """查询内容的规范化序列化，用于内容身份（哈希）比较。"""
    return json.dumps(content, sort_keys=True, separators=(",", ":"))


def content_hash(content: dict) -> str:
    return hashlib.sha256(canonical_content(content).encode("utf-8")).hexdigest()


class BudgetLedger:
    """以 SQLite 为持久存储的预算账本，线程安全。""" 

    def __init__(self, db_path: str, total_epsilon: float, rng=None, fault_hooks=None):
        if total_epsilon <= 0:
            raise ValueError("total_epsilon must be positive")
        self._conn = sqlite3.connect(db_path, check_same_thread=False)
        self._conn.isolation_level = None  # 手动管理事务
        self._conn.execute("PRAGMA journal_mode=WAL")
        self._conn.executescript(SCHEMA)
        self._lock = threading.RLock()
        self._rng = rng if rng is not None else default_rng()
        self._fault_hooks = dict(fault_hooks or {})
        with self._lock:
            self._conn.execute("BEGIN IMMEDIATE")
            try:
                row = self._conn.execute(
                    "SELECT value FROM meta WHERE key='total_epsilon'"
                ).fetchone()
                if row is None:
                    self._conn.execute(
                        "INSERT INTO meta (key, value) VALUES ('total_epsilon', ?)",
                        (repr(float(total_epsilon)),),
                    )
                elif float(row[0]) != float(total_epsilon):
                    raise ValueError(
                        f"ledger already initialised with total_epsilon={row[0]}"
                    )
                self._conn.execute("COMMIT")
            except BaseException:
                self._conn.execute("ROLLBACK")
                raise

    # ------------------------------------------------------------------ 工具

    def _run_hooks(self, point: str) -> None:
        hook = self._fault_hooks.get(point)
        if hook is not None:
            hook()

    def _log_event(self, event_type: str, query_id: str, payload: dict) -> None:
        # 公开日志：payload 只允许公开字段（见 checker.PUBLIC_PAYLOAD_KEYS）。
        self._conn.execute(
            "INSERT INTO events (type, query_id, payload_json, at) VALUES (?,?,?,?)",
            (event_type, query_id, json.dumps(payload, sort_keys=True), time.time()),
        )

    def _get_query(self, query_id: str):
        return self._conn.execute(
            "SELECT query_id, content_hash, content_json, epsilon, state"
            " FROM queries WHERE query_id = ?",
            (query_id,),
        ).fetchone()

    def _committed_epsilon(self) -> float:
        row = self._conn.execute(
            "SELECT COALESCE(SUM(epsilon), 0.0) FROM queries"
            " WHERE state IN ('RESERVED', 'PUBLISHED')"
        ).fetchone()
        return float(row[0])

    @property
    def total_epsilon(self) -> float:
        row = self._conn.execute(
            "SELECT value FROM meta WHERE key='total_epsilon'"
        ).fetchone()
        return float(row[0])

    # ------------------------------------------------------------------ 预留

    def reserve(self, query_id: str, content: dict, epsilon: float) -> dict:
        """预留预算。重复同内容预留幂等；同 ID 不同内容报冲突。"""
        if epsilon <= 0:
            raise ValueError("epsilon must be positive")
        if "sensitivity" not in content:
            raise ValueError("content must declare 'sensitivity'")
        chash = content_hash(content)
        cjson = canonical_content(content)
        with self._lock:
            self._conn.execute("BEGIN IMMEDIATE")
            try:
                row = self._get_query(query_id)
                if row is not None:
                    if row[1] != chash:
                        raise ConflictError(
                            f"query_id {query_id!r} already registered with"
                            " different content"
                        )
                    if row[4] == STATE_RESERVED:
                        self._conn.execute("COMMIT")  # 幂等：返回既有预留
                        return self.status(query_id)
                    raise InvalidStateError(
                        f"query_id {query_id!r} already {row[4]}, cannot re-reserve"
                    )
                if self._committed_epsilon() + epsilon > self.total_epsilon + 1e-12:
                    raise BudgetExhaustedError(
                        f"reserving {epsilon} would exceed total budget"
                        f" {self.total_epsilon}"
                    )
                now = time.time()
                self._conn.execute(
                    "INSERT INTO queries"
                    " (query_id, content_hash, content_json, epsilon, state,"
                    "  created_at, updated_at) VALUES (?,?,?,?,?,?,?)",
                    (query_id, chash, cjson, float(epsilon), STATE_RESERVED, now, now),
                )
                self._log_event(
                    "RESERVE", query_id,
                    {"query_id": query_id, "content_hash": chash, "epsilon": epsilon},
                )
                self._conn.execute("COMMIT")
            except BaseException:
                self._conn.execute("ROLLBACK")
                raise
        return self.status(query_id)

    # -------------------------------------------------------------- 提交发布

    def commit(self, query_id: str, content: dict, true_value: float) -> dict:
        """生成噪声、发布结果并扣减预算（单一持久事务）。

        重复提交同内容幂等：已发布则复用原噪声值与预算记录直接返回。
        """
        chash = content_hash(content)
        self._run_hooks("commit:before_txn")
        with self._lock:
            self._conn.execute("BEGIN IMMEDIATE")
            try:
                row = self._get_query(query_id)
                if row is None:
                    raise UnknownQueryError(f"query_id {query_id!r} not reserved")
                if row[1] != chash:
                    raise ConflictError(
                        f"query_id {query_id!r} committed with different content"
                    )
                epsilon = float(row[3])
                state = row[4]
                if state == STATE_CANCELLED:
                    raise InvalidStateError(
                        f"query_id {query_id!r} was cancelled, cannot publish"
                    )
                if state == STATE_PUBLISHED:
                    # 幂等重试：复用已持久化的噪声与预算记录。
                    result = self._conn.execute(
                        "SELECT noisy_value, published_at FROM results"
                        " WHERE query_id = ?",
                        (query_id,),
                    ).fetchone()
                    self._conn.execute("COMMIT")
                    return {
                        "query_id": query_id,
                        "state": STATE_PUBLISHED,
                        "epsilon": epsilon,
                        "noisy_value": float(result[0]),
                        "published_at": float(result[1]),
                        "reused": True,
                    }
                # 状态为 RESERVED：若噪声记录已存在（此前部分流程遗留），复用之。
                existing = self._conn.execute(
                    "SELECT noisy_value, published_at FROM results WHERE query_id = ?",
                    (query_id,),
                ).fetchone()
                if existing is not None:
                    noisy_value = float(existing[0])
                    published_at = float(existing[1])
                else:
                    sensitivity = float(json.loads(row[2])["sensitivity"])
                    noisy_value = float(true_value) + laplace_sample(
                        self._rng, sensitivity / epsilon
                    )
                    published_at = time.time()
                    self._conn.execute(
                        "INSERT INTO results"
                        " (query_id, true_value, noisy_value, published_at)"
                        " VALUES (?,?,?,?)",
                        (query_id, float(true_value), noisy_value, published_at),
                    )
                self._conn.execute(
                    "UPDATE queries SET state = ?, updated_at = ? WHERE query_id = ?",
                    (STATE_PUBLISHED, published_at, query_id),
                )
                self._log_event(
                    "PUBLISH", query_id,
                    {
                        "query_id": query_id,
                        "content_hash": chash,
                        "epsilon": epsilon,
                        "noisy_value": noisy_value,
                    },
                )
                self._run_hooks("commit:before_db_commit")
                self._conn.execute("COMMIT")
            except BaseException:
                self._conn.execute("ROLLBACK")
                raise
        self._run_hooks("commit:after_db_commit")
        return {
            "query_id": query_id,
            "state": STATE_PUBLISHED,
            "epsilon": epsilon,
            "noisy_value": noisy_value,
            "published_at": published_at,
            "reused": False,
        }

    # ------------------------------------------------------------------ 取消

    def cancel(self, query_id: str, content: dict) -> dict:
        """取消预留并返还预算。已发布查询不可取消、不退还预算。"""
        chash = content_hash(content)
        with self._lock:
            self._conn.execute("BEGIN IMMEDIATE")
            try:
                row = self._get_query(query_id)
                if row is None:
                    raise UnknownQueryError(f"query_id {query_id!r} not reserved")
                if row[1] != chash:
                    raise ConflictError(
                        f"query_id {query_id!r} cancelled with different content"
                    )
                state = row[4]
                if state == STATE_PUBLISHED:
                    raise InvalidStateError(
                        f"query_id {query_id!r} already published;"
                        " cancellation cannot refund budget"
                    )
                if state == STATE_CANCELLED:
                    self._conn.execute("COMMIT")  # 幂等
                    return self.status(query_id)
                now = time.time()
                self._conn.execute(
                    "UPDATE queries SET state = ?, updated_at = ? WHERE query_id = ?",
                    (STATE_CANCELLED, now, query_id),
                )
                self._log_event(
                    "CANCEL", query_id,
                    {
                        "query_id": query_id,
                        "content_hash": chash,
                        "epsilon": float(row[3]),
                    },
                )
                self._conn.execute("COMMIT")
            except BaseException:
                self._conn.execute("ROLLBACK")
                raise
        return self.status(query_id)

    # ------------------------------------------------------------------ 查询

    def status(self, query_id: str) -> dict:
        with self._lock:
            row = self._get_query(query_id)
            if row is None:
                raise UnknownQueryError(f"query_id {query_id!r} not found")
            return {
                "query_id": row[0],
                "content_hash": row[1],
                "epsilon": float(row[3]),
                "state": row[4],
            }

    def budget(self) -> dict:
        """预算快照：total = reserved(活跃预留) + spent(已发布) + available。"""
        with self._lock:
            reserved = float(self._conn.execute(
                "SELECT COALESCE(SUM(epsilon), 0.0) FROM queries"
                " WHERE state = 'RESERVED'"
            ).fetchone()[0])
            spent = float(self._conn.execute(
                "SELECT COALESCE(SUM(epsilon), 0.0) FROM queries"
                " WHERE state = 'PUBLISHED'"
            ).fetchone()[0])
            total = self.total_epsilon
            return {
                "total": total,
                "reserved": reserved,
                "spent": spent,
                "available": total - reserved - spent,
            }

    def events(self) -> list:
        """公开事件日志（不含任何秘密随机状态）。"""
        with self._lock:
            rows = self._conn.execute(
                "SELECT seq, type, query_id, payload_json, at FROM events"
                " ORDER BY seq"
            ).fetchall()
        return [
            {
                "seq": r[0],
                "type": r[1],
                "query_id": r[2],
                "payload": json.loads(r[3]),
                "at": r[4],
            }
            for r in rows
        ]

    def close(self) -> None:
        with self._lock:
            self._conn.close()
