"""带撤销的最小解释集（最小事实见证集）。

布尔结果由 AND/OR 无环电路计算，叶子是可撤销事实。
对每个当前为真的根，输出其最小事实见证集：
  - 见证集是当前为真的叶子的一个子集，使得“仅这些叶子为真”时根仍为真；
  - 重复叶子只计一次（见证集是集合，天然去重）；
  - 比较规则：先按集合大小，再按 ID 排序后的字典序，取最小者。

重要：最小见证是全局最优，不能用“逐子树局部最优再拼接”代替。
反例（验收边界）：root = (a OR b) AND (b OR c)。
  逐子树贪心：左子树取 {a}、右子树取 {b}，拼出 {a, b}（2 个事实）；
  全局最优：{b}（1 个事实）。

算法：对根锥体内当前为真的叶子，按 (数量, 字典序) 顺序枚举子集，
第一个使根为真的子集即全局最优见证。该问题是单调电路的 min-ones，
一般情形 NP-hard，故最坏情况指数级；但枚举顺序保证结果可证最优。
本实现只使用 Python 标准库（兼容 3.11）。
"""

from __future__ import annotations

import itertools
import sys


class Node:
    __slots__ = ("id", "kind", "children", "parents", "value")

    def __init__(self, nid: str, kind: str, children: list[str] | None = None):
        self.id = nid
        self.kind = kind  # "leaf" | "and" | "or"
        self.children = children or []
        self.parents: list[Node] = []
        self.value = False


class Circuit:
    """AND/OR 无环电路，叶子为可撤销事实，支持增量更新。"""

    def __init__(self) -> None:
        self.nodes: dict[str, Node] = {}
        self.roots: list[str] = []
        self._witness_cache: dict[str, frozenset[str] | None] = {}

    # ---- 构建 ----

    def _new_node(self, nid: str, kind: str, children: list[str] | None = None) -> Node:
        if not nid:
            raise ValueError("节点 ID 不能为空")
        if nid in self.nodes:
            raise ValueError(f"节点 ID 重复: {nid}")
        node = Node(nid, kind, children)
        for cid in node.children:
            if cid not in self.nodes:
                raise ValueError(f"未知子节点: {cid}")
            self.nodes[cid].parents.append(node)
        # 子节点必须先于父节点定义，因此不可能成环（无环性由构造保证）。
        self.nodes[nid] = node
        return node

    def add_leaf(self, nid: str, asserted: bool = True) -> None:
        node = self._new_node(nid, "leaf")
        node.value = asserted

    def add_and(self, nid: str, children: list[str]) -> None:
        node = self._new_node(nid, "and", children)
        node.value = all(self.nodes[c].value for c in children)

    def add_or(self, nid: str, children: list[str]) -> None:
        node = self._new_node(nid, "or", children)
        node.value = any(self.nodes[c].value for c in children)

    def add_root(self, nid: str) -> None:
        if nid not in self.nodes:
            raise ValueError(f"未知节点: {nid}")
        if nid not in self.roots:
            self.roots.append(nid)

    # ---- 撤销 / 恢复（增量更新） ----

    def revoke(self, nid: str) -> None:
        self._set_leaf(nid, False)

    def assert_fact(self, nid: str) -> None:
        self._set_leaf(nid, True)

    def _set_leaf(self, nid: str, value: bool) -> None:
        node = self.nodes.get(nid)
        if node is None:
            raise ValueError(f"未知叶子: {nid}")
        if node.kind != "leaf":
            raise ValueError(f"只能撤销/断言叶子事实: {nid}")
        if node.value == value:
            return
        node.value = value
        # 1) 增量传播值：只重算值发生变化的祖先。
        queue = list(node.parents)
        while queue:
            parent = queue.pop()
            if parent.kind == "and":
                new_value = all(self.nodes[c].value for c in parent.children)
            else:  # "or"
                new_value = any(self.nodes[c].value for c in parent.children)
            if new_value != parent.value:
                parent.value = new_value
                queue.extend(parent.parents)
        # 2) 见证失效：即使根的值没变，见证也可能变
        #    （例如 OR(a,b) 撤销 a 后值仍为真，但见证从 {a} 变为 {b}），
        #    因此使该叶子的所有祖先的见证缓存失效。
        seen = {nid}
        stack = [node]
        while stack:
            current = stack.pop()
            self._witness_cache.pop(current.id, None)
            for parent in current.parents:
                if parent.id not in seen:
                    seen.add(parent.id)
                    stack.append(parent)

    # ---- 查询 ----

    def value(self, nid: str) -> bool:
        return self.nodes[nid].value

    def min_witness(self, nid: str) -> frozenset[str] | None:
        """根为真时返回全局最小见证集，否则返回 None。"""
        if nid in self._witness_cache:
            return self._witness_cache[nid]
        node = self.nodes[nid]
        result: frozenset[str] | None = None
        if node.value:
            leaves = sorted(self._true_leaves(nid))
            # 按 (数量, 字典序) 枚举，首个可行解即全局最优（可证最优）。
            for size in range(len(leaves) + 1):
                for combo in itertools.combinations(leaves, size):
                    if self._eval_with(node, frozenset(combo)):
                        result = frozenset(combo)
                        break
                if result is not None:
                    break
        self._witness_cache[nid] = result
        return result

    def _true_leaves(self, nid: str) -> list[str]:
        leaves = []
        seen = set()
        stack = [self.nodes[nid]]
        while stack:
            node = stack.pop()
            if node.id in seen:
                continue
            seen.add(node.id)
            if node.kind == "leaf":
                if node.value:
                    leaves.append(node.id)
            else:
                stack.extend(self.nodes[c] for c in node.children)
        return leaves

    def _eval_with(self, node: Node, true_leaves: frozenset[str]) -> bool:
        """假设仅 true_leaves 中的叶子为真，求 node 的值。"""
        memo: dict[str, bool] = {}

        def go(n: Node) -> bool:
            if n.id in memo:
                return memo[n.id]
            if n.kind == "leaf":
                value = n.id in true_leaves
            elif n.kind == "and":
                value = all(go(self.nodes[c]) for c in n.children)
            else:
                value = any(go(self.nodes[c]) for c in n.children)
            memo[n.id] = value
            return value

        return go(node)


def format_witness(witness: frozenset[str] | None) -> str:
    if witness is None:
        return "none"
    return "{" + ", ".join(sorted(witness)) + "}"


# ---- 命令行 ----
#
# 输入格式（每行一条命令，# 开头为注释，空行忽略）：
#   leaf <id>                 新建叶子事实，初始为真
#   and <id> <child>...       新建 AND 节点（子节点须已定义）
#   or  <id> <child>...       新建 OR  节点（子节点须已定义）
#   root <id>                 将节点标记为根
#   revoke <id>               撤销叶子事实（置假，增量更新）
#   assert <id>               恢复叶子事实（置真，增量更新）
#   value <id>                输出 "<id> = true|false"
#   witness <id>              输出 "<id> = {b}" 或 "<id> = none"
#
# 用法：python3 minwitness.py [输入文件]   （缺省读标准输入）

def run_commands(lines, out) -> None:
    circuit = Circuit()
    for lineno, raw in enumerate(lines, 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        parts = line.split()
        cmd, args = parts[0], parts[1:]
        try:
            if cmd == "leaf":
                circuit.add_leaf(args[0])
            elif cmd == "and":
                circuit.add_and(args[0], args[1:])
            elif cmd == "or":
                circuit.add_or(args[0], args[1:])
            elif cmd == "root":
                circuit.add_root(args[0])
            elif cmd == "revoke":
                circuit.revoke(args[0])
            elif cmd == "assert":
                circuit.assert_fact(args[0])
            elif cmd == "value":
                out.write(f"{args[0]} = {'true' if circuit.value(args[0]) else 'false'}\n")
            elif cmd == "witness":
                out.write(f"{args[0]} = {format_witness(circuit.min_witness(args[0]))}\n")
            else:
                raise ValueError(f"未知命令: {cmd}")
        except (ValueError, IndexError) as exc:
            raise SystemExit(f"第 {lineno} 行错误: {line!r}: {exc}")


def main(argv: list[str]) -> None:
    if len(argv) > 1:
        with open(argv[1], encoding="utf-8") as fh:
            run_commands(fh, sys.stdout)
    else:
        run_commands(sys.stdin, sys.stdout)


if __name__ == "__main__":
    main(sys.argv)
