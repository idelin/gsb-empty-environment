"""事务引擎：原子地撤销旧键索引并建立新索引，同步修订反向引用。

语义：
- 一笔事务可含多条主键变更；校验全部通过后才落库，任一冲突则整笔失败。
- 同一事务内允许两实体交换键（或更多实体成环轮换），因为目标键会在
  同事务内被让出；落库分“先摘下全部旧键、再挂全部新键”两阶段执行。
- 主键变更与跨表外键跟随更新属于同一笔事务。
- 重复事件 (tx_id, commit_seq) 与提交序号落后于当前版本的迟到事件
  被版本隔离，不会再次移动实体。
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .model import KeyShiftError


@dataclass
class TxResult:
    tx_id: str
    status: str                 # committed | failed | skipped
    moves: dict = field(default_factory=dict)   # (table, old_key) -> new_key
    cascaded: int = 0           # 同步修订的外键引用数
    reason: str = ""


def apply_stream(db, events):
    """按到达顺序把事件流分组为事务并逐笔原子应用。"""
    groups = {}
    order = []
    for event in events:
        if event.tx_id not in groups:
            groups[event.tx_id] = []
            order.append(event.tx_id)
        groups[event.tx_id].append(event)
    results = []
    for tx_id in order:
        group = sorted(groups[tx_id], key=lambda e: e.commit_seq)
        results.append(apply_transaction(db, tx_id, group))
    return results


def _partition_fresh(db, events):
    """分离出新鲜事件；重复与迟到事件按版本隔离。"""
    fresh, isolated = [], []
    for event in events:
        if (event.tx_id, event.commit_seq) in db.applied_events:
            isolated.append((event, "duplicate"))
        elif event.commit_seq <= db.commit_seq:
            isolated.append((event, "stale-version"))
        else:
            fresh.append(event)
    return fresh, isolated


def apply_transaction(db, tx_id, events):
    fresh, isolated = _partition_fresh(db, events)
    for event, reason in isolated:
        db.isolated_events.append((event, reason))
    if isolated:
        # 事务必须整体新鲜：部分过期则整笔跳过，绝不部分落库。
        return TxResult(tx_id, "skipped",
                        reason=f"{len(isolated)} event(s) isolated")
    if not fresh:
        return TxResult(tx_id, "skipped", reason="empty transaction")

    # 1) 汇总移动计划；(表, 旧键) -> (新键, commit_seq)
    moves = {}
    for event in fresh:
        if event.old_key == event.new_key:
            continue
        mkey = (event.table, event.old_key)
        if mkey in moves and moves[mkey][0] != event.new_key:
            return TxResult(tx_id, "failed",
                            reason=f"conflicting moves for {mkey}")
        moves[mkey] = (event.new_key, event.commit_seq)

    # 2) 校验：旧键必须存在；新键若被占用，占用者必须在本事务内让出。
    for table_name, old_key in moves:
        table = db.tables.get(table_name)
        if table is None:
            return TxResult(tx_id, "failed",
                            reason=f"unknown table {table_name!r}")
        if old_key not in table.index:
            return TxResult(tx_id, "failed",
                            reason=f"old key {old_key!r} not found "
                                   f"in {table_name!r}")
    vacating = set(moves)
    targets = {}
    for (table_name, old_key), (new_key, _seq) in moves.items():
        tkey = (table_name, new_key)
        if tkey in targets:
            return TxResult(tx_id, "failed",
                            reason=f"two moves target {tkey}")
        targets[tkey] = old_key
        occupant = db.tables[table_name].index.get(new_key)
        if occupant is not None and tkey not in vacating:
            return TxResult(tx_id, "failed",
                            reason=f"new key {new_key!r} occupied "
                                   f"in {table_name!r}")

    # 3) 两阶段落库：先摘下全部旧键，再挂全部新键并级联外键。
    undo = []
    cascaded = 0
    try:
        detached = {}
        for table_name, old_key in moves:
            table = db.tables[table_name]
            entity_id = table.index.pop(old_key)
            detached[(table_name, old_key)] = entity_id
            undo.append(("index_restore", table_name, old_key, entity_id))
        for (table_name, old_key), (new_key, seq) in moves.items():
            table = db.tables[table_name]
            entity_id = detached[(table_name, old_key)]
            entity = table.entities[entity_id]
            undo.append(("entity_restore", table_name, entity_id,
                         entity.pk, entity.version))
            entity.pk = new_key
            entity.version += 1
            table.index[new_key] = entity_id
            undo.append(("index_drop", table_name, new_key))
            table.key_history.setdefault(old_key, []).append(
                (seq, entity_id, entity.version))
            undo.append(("history_trim", table_name, old_key))
            referrers = sorted(
                db.reverse_refs.get((table_name, entity_id), ()))
            for src_table, src_id, column in referrers:
                src = db.tables[src_table].entities[src_id]
                undo.append(("fk_restore", src_table, src_id, column,
                             src.data[column]))
                src.data[column] = new_key
                cascaded += 1
    except Exception as exc:  # 意外失败：按撤销日志回滚，保证原子性
        _rollback(db, undo)
        return TxResult(tx_id, "failed", reason=f"rolled back: {exc}")

    for event in fresh:
        db.applied_events.add((event.tx_id, event.commit_seq))
    db.commit_seq = max(db.commit_seq, max(e.commit_seq for e in fresh))
    return TxResult(tx_id, "committed",
                    moves={k: v[0] for k, v in moves.items()},
                    cascaded=cascaded)


def _rollback(db, undo):
    for action in reversed(undo):
        kind = action[0]
        if kind == "index_restore":
            db.tables[action[1]].index[action[2]] = action[3]
        elif kind == "index_drop":
            db.tables[action[1]].index.pop(action[2], None)
        elif kind == "entity_restore":
            entity = db.tables[action[1]].entities[action[2]]
            entity.pk, entity.version = action[3], action[4]
        elif kind == "history_trim":
            db.tables[action[1]].key_history[action[2]].pop()
        elif kind == "fk_restore":
            db.tables[action[1]].entities[action[2]].data[action[3]] = action[4]
        else:  # pragma: no cover
            raise KeyShiftError(f"unknown undo action {kind!r}")
