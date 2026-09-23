"""核心关系模型：稳定实体 ID、表、主键索引与反向引用索引。

设计要点：
- 每个实体拥有单调分配、永不复用的稳定 entity_id，与可变主键解耦。
- 主键索引 index: pk_value -> entity_id；反向引用 reverse_refs 以
  (目标表, 目标 entity_id) 为键，因此主键变更不会扰动反向索引结构，
  只需同步修订引用行的外键值。
- key_history 记录每个键的版本谱系 (commit_seq, entity_id, version)，
  用于按版本隔离迟到的旧键事件。
"""
from __future__ import annotations

import copy


class KeyShiftError(Exception):
    """引擎基础错误。"""


class ConflictError(KeyShiftError):
    """新键已被占用，或删除仍被引用的实体。"""


class MissingKeyError(KeyShiftError):
    """旧键或外键目标不存在。"""


class Entity:
    __slots__ = ("entity_id", "table", "pk", "data", "version")

    def __init__(self, entity_id, table, pk, data, version=0):
        self.entity_id = entity_id
        self.table = table
        self.pk = pk
        self.data = dict(data)
        self.version = version

    def __repr__(self):
        return (
            f"Entity(id={self.entity_id}, table={self.table!r}, "
            f"pk={self.pk!r}, v={self.version})"
        )


class Table:
    def __init__(self, name, pk_column="id", fks=None):
        self.name = name
        self.pk_column = pk_column
        self.fks = dict(fks or {})  # 外键列 -> 目标表
        self.index = {}             # 主键值 -> entity_id
        self.entities = {}          # entity_id -> Entity
        self.key_history = {}       # 主键值 -> [(commit_seq, entity_id, version)]


class Database:
    def __init__(self):
        self.tables = {}
        self.commit_seq = 0          # 已应用的最大提交序号
        self.applied_events = set()  # (tx_id, commit_seq) 幂等去重
        self.isolated_events = []    # 被版本隔离的 (event, reason)
        self.reverse_refs = {}       # (表, entity_id) -> {(源表, 源 entity_id, 外键列)}
        self.retired_ids = set()     # 已退役、永不复用的 entity_id
        self._id_counter = 0

    # -- 模式与基础读写 -------------------------------------------------

    def create_table(self, name, pk_column="id", fks=None):
        if name in self.tables:
            raise ConflictError(f"table {name!r} already exists")
        self.tables[name] = Table(name, pk_column, fks)

    def _allocate_id(self):
        entity_id = self._id_counter
        self._id_counter += 1
        return entity_id

    def insert(self, table_name, pk, data=None):
        table = self.tables[table_name]
        if pk in table.index:
            raise ConflictError(f"key {pk!r} already exists in {table_name!r}")
        data = dict(data or {})
        # 先校验全部外键，再变更，保证插入本身也是原子的。
        resolved = {}
        for col, ref_table in table.fks.items():
            value = data.get(col)
            if value is None:
                continue
            target = self.tables[ref_table].index.get(value)
            if target is None:
                raise MissingKeyError(
                    f"dangling fk {col}={value!r} -> {ref_table!r}")
            resolved[col] = (ref_table, target)
        entity_id = self._allocate_id()
        entity = Entity(entity_id, table_name, pk, data)
        table.entities[entity_id] = entity
        table.index[pk] = entity_id
        table.key_history.setdefault(pk, []).append(
            (self.commit_seq, entity_id, 0))
        for col, (ref_table, target) in resolved.items():
            self.reverse_refs.setdefault((ref_table, target), set()).add(
                (table_name, entity_id, col))
        return entity_id

    def delete(self, table_name, pk):
        table = self.tables[table_name]
        entity_id = table.index.get(pk)
        if entity_id is None:
            raise MissingKeyError(f"key {pk!r} not found in {table_name!r}")
        if self.reverse_refs.get((table_name, entity_id)):
            raise ConflictError(
                f"entity {entity_id} in {table_name!r} is still referenced")
        entity = table.entities.pop(entity_id)
        del table.index[pk]
        for col, ref_table in table.fks.items():
            value = entity.data.get(col)
            if value is None:
                continue
            target = self.tables[ref_table].index.get(value)
            if target is not None:
                refs = self.reverse_refs.get((ref_table, target))
                if refs:
                    refs.discard((table_name, entity_id, col))
        self.retired_ids.add(entity_id)  # 稳定身份不可复用
        return entity_id

    def update_fk(self, table_name, pk, column, value):
        table = self.tables[table_name]
        entity_id = table.index.get(pk)
        if entity_id is None:
            raise MissingKeyError(f"key {pk!r} not found in {table_name!r}")
        ref_table = table.fks[column]
        entity = table.entities[entity_id]
        old = entity.data.get(column)
        if old is not None:
            old_target = self.tables[ref_table].index.get(old)
            if old_target is not None:
                refs = self.reverse_refs.get((ref_table, old_target))
                if refs:
                    refs.discard((table_name, entity_id, column))
        if value is not None:
            new_target = self.tables[ref_table].index.get(value)
            if new_target is None:
                raise MissingKeyError(
                    f"dangling fk {column}={value!r} -> {ref_table!r}")
            self.reverse_refs.setdefault((ref_table, new_target), set()).add(
                (table_name, entity_id, column))
        entity.data[column] = value

    def get(self, table_name, pk):
        """按当前主键查询；已迁走的旧键返回 None。"""
        table = self.tables[table_name]
        entity_id = table.index.get(pk)
        return table.entities.get(entity_id) if entity_id is not None else None

    def snapshot(self):
        """返回深拷贝的只读快照；任何已提交快照上索引与引用一致。"""
        return copy.deepcopy(self)
