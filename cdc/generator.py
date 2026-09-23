"""确定性测试流生成器。

两步:
1. 由种子生成"规范历史": 一串完整事务(Begin/Change*/Commit|Abort),
   before/after 值由生成器内部维护的模型状态推出, commit_seq 连续。
2. 投递扰动: 事务内 Change 乱序、事件重复(至少一次语义)、
   在事务中途插入崩溃点并在重启后整体重放该事务。

同一 seed 生成的流完全相同, 可确定性复现。
"""
from __future__ import annotations

import random
from dataclasses import dataclass, field
from typing import Dict, List, Tuple

from .events import Abort, Begin, Change, Commit, Crash, Event
from .fixtures import TABLES, random_row


@dataclass
class GeneratedStream:
    seed: int
    history: List[Event]          # 规范历史(未扰动)
    stream: List[Event]           # 扰动后的投递流
    stats: Dict[str, int] = field(default_factory=dict)


def _generate_history(
    rng: random.Random, num_txns: int, max_changes: int, tables
) -> List[Event]:
    state: Dict[str, Dict[str, object]] = {t: {} for t in tables}
    events: List[Event] = []
    commit_seq = 0
    table_names = sorted(tables)

    for txn_id in range(num_txns):
        events.append(Begin(txn_id))
        overlay: Dict[Tuple[str, str], object] = {}
        for seq in range(rng.randint(1, max_changes)):
            table = rng.choice(table_names)
            pk = rng.choice(tables[table])
            key = (table, pk)
            current = overlay.get(key, state[table].get(pk))
            if current is None:
                after = random_row(rng, table)              # insert
            elif rng.random() < 0.25:
                after = None                                # delete
            else:
                after = random_row(rng, table)              # update
            events.append(Change(txn_id, seq, table, pk, current, after))
            overlay[key] = after
        if rng.random() < 0.85:
            events.append(Commit(txn_id, commit_seq))
            commit_seq += 1
            for (table, pk), value in overlay.items():
                if value is None:
                    state[table].pop(pk, None)
                else:
                    state[table][pk] = value
        else:
            events.append(Abort(txn_id))
    return events


def _split_txns(history: List[Event]) -> List[List[Event]]:
    blocks: List[List[Event]] = []
    current: List[Event] = []
    for ev in history:
        current.append(ev)
        if isinstance(ev, (Commit, Abort)):
            blocks.append(current)
            current = []
    return blocks


def _inject_duplicates(
    rng: random.Random, block: List[Event], dup_rate: float
) -> Tuple[List[Event], int]:
    """在块内注入重复事件。约束: Change/Begin 的副本不得越过终结事件,
    终结事件的副本只能紧跟其后 —— 保证流仍然有效且语义不变。"""
    block = list(block)
    term = block[-1]
    injected = 0
    for ev in list(block):
        if rng.random() >= dup_rate:
            continue
        if ev is term or ev == term:
            block.append(ev)                      # 终结副本放最后
        else:
            block.insert(rng.randint(1, len(block) - 1), ev)
        injected += 1
    return block, injected


def _perturb(
    rng: random.Random,
    history: List[Event],
    dup_rate: float,
    reorder_rate: float,
    crash_rate: float,
) -> Tuple[List[Event], Dict[str, int]]:
    stream: List[Event] = []
    stats = {"duplicates": 0, "reordered_txns": 0, "crashes": 0}

    for block in _split_txns(history):
        begin, changes, term = block[0], block[1:-1], block[-1]
        delivered = list(changes)
        if len(delivered) > 1 and rng.random() < reorder_rate:
            rng.shuffle(delivered)
            stats["reordered_txns"] += 1
        delivery = [begin] + delivered + [term]
        delivery, injected = _inject_duplicates(rng, delivery, dup_rate)
        stats["duplicates"] += injected

        if rng.random() < crash_rate:
            # 在终结事件之前选崩溃点; 崩溃后按规范顺序整体重放该事务。
            term_index = delivery.index(term)
            point = rng.randint(1, max(1, term_index))
            stream.extend(delivery[:point])
            stream.append(Crash())
            stream.extend([begin] + list(changes) + [term])
            stats["crashes"] += 1
        else:
            stream.extend(delivery)
        if rng.random() < crash_rate / 3:
            stream.append(Crash())                # 事务间空崩溃
            stats["crashes"] += 1
    return stream, stats


def generate(
    seed: int = 0,
    num_txns: int = 10,
    max_changes: int = 4,
    dup_rate: float = 0.15,
    reorder_rate: float = 0.4,
    crash_rate: float = 0.15,
    tables=TABLES,
) -> GeneratedStream:
    rng = random.Random(seed)
    history = _generate_history(rng, num_txns, max_changes, tables)
    stream, stats = _perturb(rng, history, dup_rate, reorder_rate, crash_rate)
    stats["txns"] = num_txns
    stats["events"] = len(stream)
    return GeneratedStream(seed=seed, history=history, stream=stream, stats=stats)
