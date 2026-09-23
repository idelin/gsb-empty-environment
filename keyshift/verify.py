"""独立的全量关系模型核验器：只读模型，不依赖引擎内部逻辑。

核验项：
- 主键索引与实体双向一致（index[pk]=eid 当且仅当 entity.pk=pk）。
- 稳定身份：entity_id 全局唯一、小于分配游标、退役身份绝不复用。
- 外键：每个外键值都能解析到目标实体，且反向引用索引与前向外键
  精确互为镜像（交换、环引用场景同样成立）。
- 键版本谱系 key_history 的提交序号单调不减。
可用于引擎任意可读快照。
"""
from __future__ import annotations


def verify_model(db):
    violations = []
    seen_ids = set()

    for table_name, table in db.tables.items():
        for pk, entity_id in table.index.items():
            entity = table.entities.get(entity_id)
            if entity is None:
                violations.append(
                    f"{table_name}: index {pk!r} -> missing entity {entity_id}")
            elif entity.pk != pk:
                violations.append(
                    f"{table_name}: index {pk!r} but entity {entity_id} "
                    f"has pk {entity.pk!r}")
            elif entity.table != table_name:
                violations.append(
                    f"{table_name}: entity {entity_id} tagged {entity.table!r}")
        if len(set(table.index.values())) != len(table.index):
            violations.append(f"{table_name}: index values not unique")

        for entity_id, entity in table.entities.items():
            if table.index.get(entity.pk) != entity_id:
                violations.append(
                    f"{table_name}: entity {entity_id} pk {entity.pk!r} "
                    f"not indexed")
            if entity_id in seen_ids:
                violations.append(f"entity id {entity_id} not unique")
            seen_ids.add(entity_id)
            if entity_id in db.retired_ids:
                violations.append(f"entity id {entity_id} reused after retire")
            for column, ref_table in table.fks.items():
                value = entity.data.get(column)
                if value is None:
                    continue
                target_table = db.tables.get(ref_table)
                target = (target_table.index.get(value)
                          if target_table else None)
                if target is None:
                    violations.append(
                        f"{table_name}.{column}: dangling fk {value!r} "
                        f"from entity {entity_id}")
                elif ((table_name, entity_id, column)
                      not in db.reverse_refs.get((ref_table, target), set())):
                    violations.append(
                        f"{table_name}.{column}: reverse ref missing for "
                        f"entity {entity_id} -> {ref_table}:{target}")

        for pk, history in table.key_history.items():
            seqs = [entry[0] for entry in history]
            if seqs != sorted(seqs):
                violations.append(
                    f"{table_name}: key_history for {pk!r} not monotonic")

    for (table_name, target_id), referrers in db.reverse_refs.items():
        target_table = db.tables.get(table_name)
        target = target_table.entities.get(target_id) if target_table else None
        for src_table, src_id, column in referrers:
            src = db.tables[src_table].entities.get(src_id)
            if src is None:
                violations.append(
                    f"reverse ref from missing {src_table}:{src_id}")
            elif target is None or src.data.get(column) != target.pk:
                violations.append(
                    f"reverse ref stale: {src_table}:{src_id}.{column} "
                    f"!= {table_name}:{target_id} pk")

    if seen_ids & db.retired_ids:
        violations.append("retired ids overlap live ids")
    for entity_id in seen_ids | db.retired_ids:
        if entity_id >= db._id_counter:
            violations.append(f"entity id {entity_id} beyond allocator")
    return violations


def verify_or_raise(db):
    violations = verify_model(db)
    if violations:
        raise AssertionError("model violations:\n" + "\n".join(violations))
