"""通用隐式键值 Treap（按 key 排序的随机平衡树）。

树本身只负责"有序 + 平衡 + 子树聚合"，具体聚合内容由调用方通过
``singleton(key, value)`` 与 ``combine(left, node, right)`` 注入。

复杂度：插入/更新/删除均为期望 O(log n)，且更新路径上每个节点只做一次
O(1) 的 ``pull``（重算子树聚合值），因此一次区间增量更新不会重扫全部区间。
"""

from __future__ import annotations

import contextlib
import random
from typing import Any, Callable, Iterator, Optional, Tuple


class TreapNode:
    __slots__ = ("key", "value", "priority", "left", "right", "agg")

    def __init__(self, key: Any, value: Any, priority: int) -> None:
        self.key = key
        self.value = value
        self.priority = priority
        self.left: Optional["TreapNode"] = None
        self.right: Optional["TreapNode"] = None
        self.agg: Any = None


class Treap:
    """按 ``key`` 有序的 Treap，聚合值由外部提供。"""

    def __init__(
        self,
        singleton: Callable[[Any, Any], Any],
        combine: Callable[[Any, Any, Any, Any], Any],
        seed: int = 0x5EED,
    ) -> None:
        self._singleton = singleton
        self._combine = combine
        self._rng = random.Random(seed)
        self.root: Optional[TreapNode] = None

    # ---- 基本维护 -----------------------------------------------------

    def _pull(self, node: TreapNode) -> TreapNode:
        """依据左右子树重算本节点聚合值。单次 O(1)。"""
        left_agg = node.left.agg if node.left is not None else None
        right_agg = node.right.agg if node.right is not None else None
        node.agg = self._combine(left_agg, node.value, right_agg)
        return node

    def _merge(
        self, a: Optional[TreapNode], b: Optional[TreapNode]
    ) -> Optional[TreapNode]:
        if a is None:
            return b
        if b is None:
            return a
        if a.priority > b.priority:
            a.right = self._merge(a.right, b)
            return self._pull(a)
        b.left = self._merge(a, b.left)
        return self._pull(b)

    def _split(
        self, node: Optional[TreapNode], key: Any
    ) -> Tuple[Optional[TreapNode], Optional[TreapNode]]:
        """拆成 key < ``key`` 与 key >= ``key`` 两棵树。"""
        if node is None:
            return None, None
        if node.key < key:
            a, b = self._split(node.right, key)
            node.right = a
            return self._pull(node), b
        a, b = self._split(node.left, key)
        node.left = b
        return a, self._pull(node)

    # ---- 写操作 -------------------------------------------------------

    def upsert(
        self,
        key: Any,
        make_value: Callable[[], Any],
        mutate: Callable[[Any], Any],
    ) -> Any:
        """插入或原地更新某个 key。

        - key 不存在时调用 ``make_value()`` 创建载荷；
        - 已存在时把旧载荷交给 ``mutate()``，返回值作为新载荷；
          返回 ``REMOVE`` 哨兵则删除该 key。
        仅更新路径上的 O(log n) 个节点需要重新聚合。
        """

        def insert(node: Optional[TreapNode]) -> Tuple[Optional[TreapNode], Any]:
            if node is None:
                value = make_value()
                new_node = TreapNode(key, value, self._rng.getrandbits(64))
                return self._pull(new_node), value
            if key == node.key:
                new_value = mutate(node.value)
                if new_value is REMOVE:
                    return self._merge(node.left, node.right), REMOVE
                node.value = new_value
                return self._pull(node), new_value
            if key < node.key:
                new_left, result = insert(node.left)
                node.left = new_left
                return self._pull(node), result
            new_right, result = insert(node.right)
            node.right = new_right
            return self._pull(node), result

        self.root, result = insert(self.root)
        return result

    def get(self, key: Any) -> Optional[Any]:
        node = self.root
        while node is not None:
            if key == node.key:
                return node.value
            node = node.left if key < node.key else node.right
        return None

    # ---- 读操作 -------------------------------------------------------

    def in_order(self) -> Iterator[Tuple[Any, Any]]:
        stack = []
        node = self.root
        while node is not None or stack:
            while node is not None:
                stack.append(node)
                node = node.left
            node = stack.pop()
            yield node.key, node.value
            node = node.right

    @staticmethod
    def walk_subtree(node: Optional[TreapNode]) -> Iterator[TreapNode]:
        if node is None:
            return
        stack = []
        current = node
        while current is not None or stack:
            while current is not None:
                stack.append(current)
                current = current.left
            current = stack.pop()
            yield current
            current = current.right

    @contextlib.contextmanager
    def split_range(self, lo: Optional[Any], hi: Optional[Any]):
        """临时把树拆成 ``[lo, hi)`` 子树供只读遍历。

        进入时按 key 切三刀，退出时原样合并回去；区间查询因此只接触
        O(log n) 的切分路径 + 范围内节点，不扫描范围外的区间。
        """
        if hi is None:
            mid, right = self.root, None
        else:
            mid, right = self._split(self.root, hi)
        if lo is None:
            left, mid = None, mid
        else:
            left, mid = self._split(mid, lo)
        try:
            yield mid
        finally:
            self.root = self._merge(self._merge(left, mid), right)


REMOVE = object()
