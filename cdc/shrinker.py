"""自动缩减: 在保持事务结构可重放的前提下最小化失败序列。

所有变换都是结构保持的:
- 删除整个事务(Begin..Commit/Abort 整块), 并重编号 commit_seq;
- 删除崩溃点;
- 去除完全重复的事件;
- 把事务内乱序的 Change 恢复为按 seq 有序;
- 删除事务内某个 seq 的 Change 并重编号该事务剩余 seq。

每个候选都重新过完整判定(默认: 与原始失败同类), 无效候选
(例如破坏 commit_seq 连续性)会被 harness 归为 invalid_stream 而自动拒绝,
因此缩减结果始终是可校验、可重放的合法流。
"""
from __future__ import annotations

from typing import Callable, List, Optional

from .events import Abort, Begin, Change, Commit, Crash, Event
from .harness import run_case
from .processor import IncrementalProcessor

Predicate = Callable[[List[Event]], bool]


def _txn_ids(stream: List[Event]) -> List[int]:
    seen, ids = set(), []
    for ev in stream:
        txn_id = getattr(ev, "txn_id", None)
        if txn_id is not None and txn_id not in seen:
            seen.add(txn_id)
            ids.append(txn_id)
    return ids


def renumber_commit_seqs(stream: List[Event]) -> List[Event]:
    mapping = {}
    out = []
    for ev in stream:
        if isinstance(ev, Commit):
            if ev.commit_seq not in mapping:
                mapping[ev.commit_seq] = len(mapping)
            ev = Commit(ev.txn_id, mapping[ev.commit_seq])
        out.append(ev)
    return out


def _drop_txn(stream: List[Event], txn_id: int) -> List[Event]:
    kept = [ev for ev in stream if getattr(ev, "txn_id", None) != txn_id]
    return renumber_commit_seqs(kept)


def _dedup(stream: List[Event]) -> List[Event]:
    seen, out = set(), []
    for ev in stream:
        if ev not in seen:
            seen.add(ev)
            out.append(ev)
    return out


def _restore_order(stream: List[Event]) -> List[Event]:
    """把每个事务的 Change 按 seq 重新排回其原有位置序列中。"""
    queues = {}
    for ev in stream:
        if isinstance(ev, Change):
            queues.setdefault(ev.txn_id, []).append(ev)
    for txn_id in queues:
        queues[txn_id] = iter(sorted(queues[txn_id], key=lambda c: c.seq))
    return [
        next(queues[ev.txn_id]) if isinstance(ev, Change) else ev for ev in stream
    ]


def _drop_change_seq(stream: List[Event], txn_id: int, seq: int) -> List[Event]:
    remaining = sorted(
        {ev.seq for ev in stream if isinstance(ev, Change) and ev.txn_id == txn_id}
        - {seq}
    )
    mapping = {old: new for new, old in enumerate(remaining)}
    out = []
    for ev in stream:
        if isinstance(ev, Change) and ev.txn_id == txn_id:
            if ev.seq == seq:
                continue
            ev = Change(ev.txn_id, mapping[ev.seq], ev.table, ev.pk, ev.before, ev.after)
        out.append(ev)
    return out


def shrink(
    stream: List[Event],
    tables,
    processor_factory=IncrementalProcessor,
    predicate: Optional[Predicate] = None,
    max_rounds: int = 40,
) -> List[Event]:
    """贪心缩减失败流, 返回仍满足 predicate 的最小化序列。"""
    stream = list(stream)
    if predicate is None:
        failing = run_case(stream, tables, processor_factory).status
        if failing in ("ok", "invalid_stream"):
            raise ValueError(f"流未失败(状态 {failing}), 无法缩减")

        def predicate(candidate: List[Event]) -> bool:
            return run_case(candidate, tables, processor_factory).status == failing

    if not predicate(stream):
        raise ValueError("初始流不满足失败判定")

    def try_candidates(candidates) -> bool:
        nonlocal stream
        for candidate in candidates:
            if len(candidate) < len(stream) and predicate(candidate):
                stream = candidate
                return True
        return False

    for _ in range(max_rounds):
        ids = _txn_ids(stream)

        # 1) 删除事务: 先大块(连续一半)再逐个。
        if len(ids) > 1:
            half = max(1, len(ids) // 2)
            chunks = [ids[i : i + half] for i in range(0, len(ids), half)]
            accepted = False
            for chunk in chunks:
                chunk_set = set(chunk)
                candidate = renumber_commit_seqs(
                    [ev for ev in stream
                     if getattr(ev, "txn_id", None) not in chunk_set]
                )
                if try_candidates([candidate]):
                    accepted = True
                    break
            if accepted:
                continue
        if try_candidates([_drop_txn(stream, t) for t in _txn_ids(stream)]):
            continue

        # 2) 删除全部崩溃点。
        no_crash = [ev for ev in stream if not isinstance(ev, Crash)]
        if try_candidates([no_crash]):
            continue

        # 3) 去除重复事件。
        if try_candidates([_dedup(stream)]):
            continue

        # 4) 恢复事务内顺序(不缩短流, 但简化后续缩减; 仅在仍失败时接受)。
        ordered = _restore_order(stream)
        if ordered != stream and predicate(ordered):
            stream = ordered
            continue

        # 5) 删除事务内单个 Change。
        candidates = []
        for txn_id in _txn_ids(stream):
            seqs = sorted(
                {ev.seq for ev in stream
                 if isinstance(ev, Change) and ev.txn_id == txn_id}
            )
            for seq in seqs:
                candidates.append(_drop_change_seq(stream, txn_id, seq))
        if try_candidates(candidates):
            continue

        break
    return stream
