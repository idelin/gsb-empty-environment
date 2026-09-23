"""缓冲同事务的多条变更，按提交序号原子应用。

语义：
- 同一 ``txid`` 的所有 Change 先缓冲；收到 Commit 且收齐清单后事务才可提交。
- 提交严格按 Commit 序号顺序；序号缺口使流水线暂停，补齐后自动续上。
- Abort（回滚）直接丢弃缓冲中的事务；已提交事务的重复事件不二次生效。
- 提交为全有或全无：先完整校验每条变更的前值，再一次性替换并持久化。
- 未提交（半事务、等待清单、等待序号缺口）的变更对外始终不可见，
  进程重启后也不会恢复，只从持久化的已提交快照继续。
"""

from __future__ import annotations

import copy
import json
import os
import tempfile
from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional

Row = Dict[str, Any]


class ConflictError(RuntimeError):
    """变更前值与当前已提交状态不一致，事务无法原子应用。"""


@dataclass(frozen=True)
class Change:
    txid: str
    seqno: int          # 事务内序号，从 1 开始、连续
    table: str
    key: Any
    before: Optional[Row]
    after: Optional[Row]


@dataclass(frozen=True)
class Commit:
    txid: str
    commit_seq: int     # 全局提交序号
    change_count: int


@dataclass(frozen=True)
class Abort:
    txid: str


@dataclass
class _Txn:
    txid: str
    changes: Dict[int, Change] = field(default_factory=dict)
    max_seqno: int = 0
    commit_seq: Optional[int] = None
    change_count: Optional[int] = None

    def is_complete(self) -> bool:
        return (
            self.commit_seq is not None
            and self.change_count is not None
            and self.change_count > 0
            and self.max_seqno == self.change_count
            and len(self.changes) == self.change_count
        )


class Applier:
    def __init__(self, state_path: Optional[str] = None, first_commit_seq: int = 1):
        self._state_path = state_path
        self._tables: Dict[str, Dict[Any, Row]] = {}
        self._applied_txids: Dict[str, int] = {}
        self._buffered: Dict[str, _Txn] = {}
        self._ready: Dict[int, _Txn] = {}
        self._last_commit_seq: Optional[int] = first_commit_seq - 1
        if state_path:
            self._load()

    # ---------- 公开 API ----------

    def handle_change(self, change: Change) -> str:
        if change.txid in self._applied_txids:
            return "duplicate"
        txn = self._buffered.get(change.txid)
        if txn is None:
            txn = _Txn(txid=change.txid)
            self._buffered[change.txid] = txn
        existing = txn.changes.get(change.seqno)
        if existing is not None:
            if existing != change:
                raise ValueError(
                    f"事务 {change.txid} 序号 {change.seqno} 的变更与之前收到的不一致"
                )
            return "duplicate"
        txn.changes[change.seqno] = change
        txn.max_seqno = max(txn.max_seqno, change.seqno)
        if txn.commit_seq is not None:
            return self._promote_if_complete(txn)  # 补齐早到提交标记等待的清单
        return "buffered"

    def handle_commit(self, commit: Commit) -> str:
        status = self._applied_txids.get(commit.txid)
        if status is not None:
            if status != commit.commit_seq:
                raise ValueError(
                    f"事务 {commit.txid} 已以提交序号 {status} 提交，"
                    f"重复提交携带了不同序号 {commit.commit_seq}"
                )
            return "duplicate"
        txn = self._buffered.get(commit.txid)
        if txn is None:
            txn = _Txn(txid=commit.txid)
            self._buffered[commit.txid] = txn
        if txn.commit_seq is not None:
            if (
                txn.commit_seq != commit.commit_seq
                or txn.change_count != commit.change_count
            ):
                raise ValueError(f"事务 {commit.txid} 收到互相冲突的提交标记")
            return "duplicate"
        txn.commit_seq = commit.commit_seq
        txn.change_count = commit.change_count
        return self._promote_if_complete(txn)

    def handle_abort(self, abort: Abort) -> str:
        if abort.txid in self._applied_txids:
            return "duplicate"
        txn = self._buffered.pop(abort.txid, None)
        if txn is None:
            txn = next(
                (t for t in self._ready.values() if t.txid == abort.txid), None
            )
        if txn is None:
            return "unknown"
        for cs in [c for c, t in self._ready.items() if t is txn]:
            del self._ready[cs]
        return "aborted"

    def get(self, table: str, key: Any) -> Optional[Row]:
        return copy.deepcopy(self._tables.get(table, {}).get(key))

    def table_rows(self, table: str) -> Dict[Any, Row]:
        return copy.deepcopy(self._tables.get(table, {}))

    @property
    def last_commit_seq(self) -> Optional[int]:
        return self._last_commit_seq

    @property
    def paused(self) -> bool:
        if not self._ready:
            return False
        return min(self._ready) != self._last_commit_seq + 1

    def pump(self) -> List[str]:
        return self._pump()

    # ---------- 内部逻辑 ----------

    def _promote_if_complete(self, txn: _Txn) -> str:
        if not txn.is_complete():
            return "waiting"
        assert txn.commit_seq is not None
        other = self._ready.get(txn.commit_seq)
        if other is not None:
            if other is not txn:
                raise ValueError(f"提交序号 {txn.commit_seq} 已被另一个事务占用")
            return "duplicate"
        del self._buffered[txn.txid]
        self._ready[txn.commit_seq] = txn
        if self._pump():
            return "committed"
        return "paused"

    def _pump(self) -> List[str]:
        committed: List[str] = []
        while self._ready:
            nxt = self._last_commit_seq + 1
            txn = self._ready.get(nxt)
            if txn is None:
                break  # 序号缺口：暂停，等待缺失的提交
            del self._ready[nxt]
            self._apply(txn, nxt)
            committed.append(txn.txid)
        return committed

    def _apply(self, txn: _Txn, commit_seq: int) -> None:
        ordered = [txn.changes[i] for i in range(1, txn.change_count + 1)]

        # 在影子副本上按序“校验前值 + 应用变更”：同事务内先删后插同键时，
        # 后续变更看到的是前面变更后的状态。任一条失败则丢弃影子副本，
        # 已提交状态与磁盘都不变，实现全有或全无。
        proposed: Dict[str, Dict[Any, Row]] = copy.deepcopy(self._tables)
        for change in ordered:
            tbl = proposed.setdefault(change.table, {})
            if tbl.get(change.key) != change.before:
                raise ConflictError(
                    f"事务 {txn.txid} 在表 {change.table} 键 {change.key!r} 上"
                    f"前值不匹配：期望 {change.before!r}，"
                    f"实际 {tbl.get(change.key)!r}"
                )
            if change.after is None:
                tbl.pop(change.key, None)
            else:
                tbl[change.key] = copy.deepcopy(change.after)
        for tbl_name in list(proposed):
            if not proposed[tbl_name]:
                del proposed[tbl_name]

        # 第三阶段：原子落盘成功后再切换内存状态。
        self._persist(proposed, txn.txid, commit_seq)
        self._tables = proposed
        self._applied_txids[txn.txid] = commit_seq
        self._last_commit_seq = commit_seq

    # ---------- 持久化 ----------

    def _persist(
        self,
        tables: Dict[str, Dict[Any, Row]],
        txid: str,
        commit_seq: int,
    ) -> None:
        if self._state_path is None:
            return
        serialised_tables = {
            name: [[key, row] for key, row in table.items()]
            for name, table in tables.items()
        }
        payload = {
            "last_commit_seq": commit_seq,
            "tables": serialised_tables,
            "applied_txids": dict(self._applied_txids, **{txid: commit_seq}),
        }
        directory = os.path.dirname(os.path.abspath(self._state_path))
        os.makedirs(directory, exist_ok=True)
        fd, tmp_path = tempfile.mkstemp(
            prefix=".txstate-", suffix=".tmp", dir=directory
        )
        try:
            with os.fdopen(fd, "w", encoding="utf-8") as fh:
                json.dump(payload, fh, ensure_ascii=False, sort_keys=True)
                fh.flush()
                os.fsync(fh.fileno())
            os.replace(tmp_path, self._state_path)
            try:
                dir_fd = os.open(directory, os.O_RDONLY)
                try:
                    os.fsync(dir_fd)
                finally:
                    os.close(dir_fd)
            except (OSError, AttributeError):
                pass  # 部分平台不支持目录 fsync，尽力而为
        except BaseException:
            try:
                os.unlink(tmp_path)
            except OSError:
                pass
            raise

    def _load(self) -> None:
        assert self._state_path is not None
        if not os.path.exists(self._state_path):
            return
        with open(self._state_path, "r", encoding="utf-8") as fh:
            payload = json.load(fh)
        self._tables = {
            name: {entry[0]: entry[1] for entry in entries}
            for name, entries in payload.get("tables", {}).items()
        }
        self._applied_txids = dict(payload.get("applied_txids", {}))
        self._last_commit_seq = payload.get("last_commit_seq")
