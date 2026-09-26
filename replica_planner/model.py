"""数据模型：节点、对象副本需求与放置约束校验。

约束数据均为合成整数或有理数（Fraction），不依赖任何外部求解器。
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction


def to_fraction(value) -> Fraction:
    """把 int / Fraction / "a/b" 字符串统一转换为 Fraction，保证精确有理数运算。"""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("布尔值不是有效的成本数据")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, str):
        return Fraction(value)
    raise TypeError(f"不支持的成本类型: {type(value)!r}")


@dataclass(frozen=True)
class Node:
    """一个存储节点。fault domain 由 rack（机架）标识。"""

    node_id: str
    rack: str
    capacity: int            # 可用于该对象的副本槽位数，0 表示已满/下线
    transfer_cost: Fraction  # 向该节点新迁入一个副本的传输成本（保留已有副本成本为 0）

    def __init__(self, node_id, rack, capacity, transfer_cost):
        object.__setattr__(self, "node_id", str(node_id))
        object.__setattr__(self, "rack", str(rack))
        capacity = int(capacity)
        if capacity < 0:
            raise ValueError(f"节点 {node_id} 容量不能为负: {capacity}")
        object.__setattr__(self, "capacity", capacity)
        cost = to_fraction(transfer_cost)
        if cost < 0:
            raise ValueError(f"节点 {node_id} 传输成本不能为负: {cost}")
        object.__setattr__(self, "transfer_cost", cost)


@dataclass(frozen=True)
class ObjectSpec:
    """单个对象的副本需求。

    replicas:     目标副本数 R
    min_replicas: 迁移过程中任何时刻都不得跌破的最低副本数 m (m <= R)
    current:      当前持有副本的节点 id 元组
    """

    object_id: str
    replicas: int
    min_replicas: int
    current: tuple[str, ...] = ()

    def __init__(self, object_id, replicas, min_replicas, current=()):
        object.__setattr__(self, "object_id", str(object_id))
        replicas = int(replicas)
        min_replicas = int(min_replicas)
        if replicas < 1:
            raise ValueError(f"目标副本数必须 >= 1: {replicas}")
        if not 0 <= min_replicas <= replicas:
            raise ValueError(f"最低副本数须满足 0 <= min <= replicas: {min_replicas} !<= {replicas}")
        object.__setattr__(self, "replicas", replicas)
        object.__setattr__(self, "min_replicas", min_replicas)
        object.__setattr__(self, "current", tuple(str(c) for c in current))


def validate_placement(nodes_by_id: dict[str, Node], placement) -> list[str]:
    """校验一个放置方案，返回错误列表（空列表表示合法）。

    规则：
    - 同一对象的副本不能重复计在同一节点；
    - 任意两个副本不能处于同一机架（同机架不同节点不满足机架隔离）。
    """
    errors: list[str] = []
    seen_nodes: set[str] = set()
    rack_holder: dict[str, str] = {}
    for nid in placement:
        node = nodes_by_id.get(nid)
        if node is None:
            errors.append(f"未知节点: {nid}")
            continue
        if nid in seen_nodes:
            errors.append(f"副本重复计在同一节点: {nid}")
        seen_nodes.add(nid)
        holder = rack_holder.get(node.rack)
        if holder is not None:
            errors.append(
                f"机架隔离冲突: 节点 {nid} 与 {holder} 同处机架 {node.rack}"
            )
        else:
            rack_holder[node.rack] = nid
    return errors
