"""压缩前缀树（radix / Patricia trie），节点维护带版本的 TopK 上界缓存。

设计要点：

* 边压缩：每条边携带字符串标签，单子链在删除后被合并，节点数与
  "不同前缀分叉数" 同阶，而不是与字符总数同阶。
* 每个节点缓存其子树的 ``(规范词, 频次)`` 精确 Top-``bound`` 列表
  （``cache``），按排名键 ``(-freq, word)`` 排序；``cache[0]`` 即子树
  最高频上界。缓存带有 ``cache_version``（计算时的全局版本）与
  ``stale`` 失效标记。
* 频次**上升 / 插入**沿根到终端的路径就地修补缓存（offer），保持精确；
  频次**下降 / 删除**只把路径标记为 stale，查询时按需重建——重建时
  自底向上合并子节点缓存，从而把此前被截断在 Top-``bound`` 之外的
  候选**补回**（backfill），无需扫描整个子树的全部词。
* 查询只刷新 stale 或容量不足的路径；未受影响的分支零开销。
"""

from __future__ import annotations

from typing import Callable, Iterator, Optional

__all__ = ["Node", "RadixTrie"]


def _rank_key(item: tuple[str, int]) -> tuple[int, str]:
    """排名键：频次降序，规范词字典序升序（稳定决胜）。"""
    word, freq = item
    return (-freq, word)


class Node:
    __slots__ = (
        "label",          # 从父节点指向本节点的边标签（根为 ""）
        "children",       # 首字符 -> Node
        "word",           # 若本节点是终端，为规范词，否则 None
        "total",          # 子树内终端（不同词）数量
        "bound",          # 缓存容量（TopK 上界宽度）
        "cache",          # list[(word, freq)]，按 _rank_key 排序，长度 <= bound
        "cache_version",  # 缓存计算时的全局版本
        "stale",          # 缓存是否失效（下降/删除/扩容后待重建）
    )

    def __init__(self, label: str = "") -> None:
        self.label = label
        self.children: dict[str, Node] = {}
        self.word: Optional[str] = None
        self.total = 0
        self.bound = 0
        self.cache: list[tuple[str, int]] = []
        self.cache_version = -1
        self.stale = True


class RadixTrie:
    def __init__(self, freq_of: Callable[[str], Optional[int]], base_bound: int = 16) -> None:
        self.root = Node()
        self.root.bound = base_bound
        self.base_bound = base_bound
        self.version = 0            # 全局版本，每次结构/频次变更 +1
        self.refresh_count = 0      # 统计：缓存重建的节点数（用于测试非全子树扫描）
        self._freq_of = freq_of

    # ------------------------------------------------------------------
    # 结构操作
    # ------------------------------------------------------------------
    def _insert(self, word: str) -> tuple[Node, bool, list[Node]]:
        """插入词，返回 (终端节点, 是否新建, 根到终端的路径)。"""
        path = [self.root]
        node = self.root
        key = word
        while True:
            if not key:
                created = node.word is None
                if created:
                    node.word = word
                return node, created, path
            child = node.children.get(key[0])
            if child is None:
                leaf = Node(key)
                leaf.word = word
                node.children[key[0]] = leaf
                path.append(leaf)
                return leaf, True, path
            label = child.label
            j = 0
            limit = min(len(key), len(label))
            while j < limit and key[j] == label[j]:
                j += 1
            if j == len(label):
                path.append(child)
                node = child
                key = key[j:]
                continue
            # 在 j 处分裂 child 边
            mid = Node(label[:j])
            mid.total = child.total  # 继承被分裂子树的终端计数
            child.label = label[j:]
            mid.children[child.label[0]] = child
            node.children[key[0]] = mid
            path.append(mid)
            if j == len(key):
                mid.word = word
                return mid, True, path
            leaf = Node(key[j:])
            leaf.word = word
            mid.children[key[j]] = leaf
            path.append(leaf)
            return leaf, True, path

    def _path(self, word: str) -> Optional[list[Node]]:
        """根到该词终端节点的路径；词不存在返回 None。"""
        path = [self.root]
        node = self.root
        key = word
        while key:
            child = node.children.get(key[0])
            if child is None:
                return None
            label = child.label
            if not key.startswith(label):
                return None
            path.append(child)
            node = child
            key = key[len(label):]
        if node.word is None:
            return None
        return path

    def locate(self, prefix: str) -> Optional[tuple[Node, str]]:
        """定位前缀子树：返回 (子树根节点, 根到该节点的完整字符串)。

        前缀可以落在某条边的标签中间，此时该边下游整个子树都匹配。
        """
        node = self.root
        base = ""
        key = prefix
        while key:
            child = node.children.get(key[0])
            if child is None:
                return None
            label = child.label
            if len(key) < len(label):
                if label.startswith(key):
                    return child, base + label
                return None
            if not key.startswith(label):
                return None
            base += label
            key = key[len(label):]
            node = child
        return node, base

    # ------------------------------------------------------------------
    # 频次变更传播（由 Suggester 调用）
    # ------------------------------------------------------------------
    def upsert(self, word: str, freq: int) -> None:
        """插入新词或上调频次：沿路径就地修补缓存（保持精确 TopK 上界）。"""
        node, created, path = self._insert(word)
        if created:
            for n in path:
                n.total += 1
        for n in path:
            self._cache_offer(n, word, freq)
        self.version += 1

    def decrease(self, word: str) -> None:
        """下调频次：路径标记 stale，候选将在查询重建时补回。"""
        path = self._path(word)
        if path is None:
            return
        for n in path:
            n.stale = True
        self.version += 1

    def delete(self, word: str) -> None:
        """删除词：路径标记 stale 并做边压缩。"""
        path = self._path(word)
        if path is None:
            return
        for n in path:
            n.stale = True
            n.total -= 1
        node = path[-1]
        node.word = None
        # 自底向上压缩：无终端且子节点 <= 1 的节点被合并/摘除
        i = len(path) - 1
        while i > 0:
            n = path[i]
            if n.word is not None or len(n.children) > 1:
                break
            parent = path[i - 1]
            if len(n.children) == 1:
                child = next(iter(n.children.values()))
                child.label = n.label + child.label
                parent.children[child.label[0]] = child
            else:
                del parent.children[n.label[0]]
            i -= 1
        self.version += 1

    # ------------------------------------------------------------------
    # TopK 上界缓存
    # ------------------------------------------------------------------
    def _cache_offer(self, node: Node, word: str, freq: int) -> None:
        """频次上升/插入时向新鲜缓存就地并入候选；stale 缓存留待重建。"""
        if node.stale:
            return
        cache = node.cache
        for idx, (w, _) in enumerate(cache):
            if w == word:
                cache[idx] = (word, freq)
                break
        else:
            cache.append((word, freq))
        cache.sort(key=_rank_key)
        del cache[node.bound:]
        node.cache_version = self.version

    def ensure(self, node: Node, min_bound: int) -> None:
        """保证节点缓存新鲜且容量 >= min_bound（查询入口，按需重建）。"""
        min_bound = max(min_bound, self.base_bound)  # 容量下限，避免反复扩容重建
        if node.bound < min_bound:
            node.bound = min_bound
            node.stale = True
        if node.stale:
            self._refresh(node)

    def _refresh(self, node: Node) -> None:
        """合并子节点缓存重建本节点 TopK 上界；只下探 stale/容量不足的分支。"""
        for child in node.children.values():
            if child.stale or child.bound < node.bound:
                self.ensure(child, node.bound)
        merged: list[tuple[str, int]] = []
        if node.word is not None:
            merged.append((node.word, self._freq_of(node.word) or 0))
        for child in node.children.values():
            merged.extend(child.cache)
        merged.sort(key=_rank_key)
        del merged[node.bound:]
        node.cache = merged
        node.stale = False
        node.cache_version = self.version
        self.refresh_count += 1

    # ------------------------------------------------------------------
    # 字典序遍历（带 start_after 剪枝的分页）
    # ------------------------------------------------------------------
    def iter_lex(
        self,
        prefix: str,
        start_after: Optional[str],
        limit: int,
    ) -> list[str]:
        """按字典序产出前缀子树中 > start_after 的前 limit 个词。"""
        located = self.locate(prefix)
        if located is None or limit <= 0:
            return []
        node, base = located
        out: list[str] = []

        def collect(n: Node, path_str: str, filtering: bool) -> None:
            if len(out) >= limit:
                return
            if n.word is not None and (not filtering or n.word > start_after):
                out.append(n.word)
                if len(out) >= limit:
                    return
            for key in sorted(n.children):
                child = n.children[key]
                child_str = path_str + child.label
                f = filtering
                if filtering:
                    # 子树所有词共享前缀 child_str，据此整体剪枝：
                    if start_after < child_str:
                        f = False          # 子树全部 > start_after，无需逐词过滤
                    elif not start_after.startswith(child_str):
                        continue           # 子树全部 <= start_after，整棵跳过
                collect(child, child_str, f)
                if len(out) >= limit:
                    return

        filtering = start_after is not None
        if filtering and start_after < base:
            filtering = False
        collect(node, base, filtering)
        return out

    # ------------------------------------------------------------------
    # 统计
    # ------------------------------------------------------------------
    def node_count(self) -> int:
        count = 0
        stack = [self.root]
        while stack:
            n = stack.pop()
            count += 1
            stack.extend(n.children.values())
        return count
