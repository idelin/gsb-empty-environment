"""有限泛化层级（每字段一棵，允许 DAG）。

- 节点用字符串名字唯一标识；同名节点视为同一节点（天然去重）。
- parents 定义 child -> parents 的有向边，可构成 DAG（一个节点可有多个父节点）。
- 节点层级：叶子为 0，其余节点 level = 1 + 子节点 level 的最大值。
- 泛化语义：generalize(value, L) 返回 value 在层级 L 的唯一祖先。
  不同泛化路径收敛到同一节点时按集合去重；若某层仍存在多个不同祖先
  （歧义），构造时即抛出 HierarchyError。
"""

from __future__ import annotations


class HierarchyError(ValueError):
    """层级定义非法（环、歧义祖先、层级不可达等）。"""


class Hierarchy:
    def __init__(self, field, leaves, parents):
        self.field = field
        self.parents = {}
        for node, ps in parents.items():
            self.parents[node] = tuple(sorted(set(ps)))
        self.leaves = tuple(dict.fromkeys(leaves))
        for leaf in self.leaves:
            self.parents.setdefault(leaf, ())
        nodes = set(self.parents)
        for ps in self.parents.values():
            nodes.update(ps)
        for node in nodes:
            self.parents.setdefault(node, ())
        self.nodes = nodes

        self.level = self._compute_levels()

        self._ancestors = {}
        reachable = set()
        for leaf in self.leaves:
            closure = self._closure(leaf)
            self._ancestors[leaf] = closure
            reachable |= closure
        if not reachable:
            raise HierarchyError(f"字段 {self.field}: 层级为空")
        self.max_level = max(self.level[n] for n in reachable)

        # 预计算每个叶子在每一层的唯一祖先（DAG 收敛去重 + 歧义校验）
        self._paths = {}
        for leaf in self.leaves:
            self._paths[leaf] = self._compute_path(leaf)

        # 每个节点覆盖的叶子数（损失函数用）
        self.leaf_count = {n: 0 for n in nodes}
        for leaf in self.leaves:
            for node in self._ancestors[leaf]:
                self.leaf_count[node] += 1
        self.total_leaves = len(self.leaves)

    def _compute_levels(self):
        children = {node: [] for node in self.parents}
        for node, ps in self.parents.items():
            for parent in ps:
                children[parent].append(node)
        levels = {}
        visiting = set()

        def lvl(node):
            if node in levels:
                return levels[node]
            if node in visiting:
                raise HierarchyError(f"字段 {self.field}: 层级存在环，经过节点 {node!r}")
            visiting.add(node)
            cs = children.get(node, ())
            levels[node] = 0 if not cs else 1 + max(lvl(c) for c in cs)
            visiting.discard(node)
            return levels[node]

        for node in self.parents:
            lvl(node)
        return levels

    def _closure(self, leaf):
        seen = set()
        stack = [leaf]
        while stack:
            node = stack.pop()
            if node in seen:
                continue
            seen.add(node)
            stack.extend(self.parents.get(node, ()))
        return seen

    def _ancestors_at(self, leaf, target):
        """leaf 在层级 target 的祖先集合（沿所有路径上溯，收敛节点去重）。"""
        frontier = {leaf}
        result = set()
        while frontier:
            nxt = set()
            for node in frontier:
                node_level = self.level[node]
                if node_level == target:
                    result.add(node)
                elif node_level < target:
                    nxt.update(self.parents.get(node, ()))
            frontier = nxt
        return result

    def _compute_path(self, leaf):
        path = []
        for target in range(self.max_level + 1):
            found = self._ancestors_at(leaf, target)
            if not found:
                raise HierarchyError(
                    f"字段 {self.field}: 值 {leaf!r} 无法泛化到层级 {target}"
                )
            if len(found) > 1:
                raise HierarchyError(
                    f"字段 {self.field}: 值 {leaf!r} 在层级 {target} "
                    f"存在歧义祖先 {sorted(found)}"
                )
            path.append(next(iter(found)))
        return tuple(path)

    def generalize(self, value, level):
        """把叶子取值 value 泛化到层级 level，返回该层唯一祖先节点名。"""
        try:
            path = self._paths[value]
        except KeyError:
            raise KeyError(f"字段 {self.field} 的未知取值: {value!r}") from None
        if not 0 <= level < len(path):
            raise ValueError(f"字段 {self.field} 的非法层级: {level}")
        return path[level]
