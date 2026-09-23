"""穷举小树验证：

1. 结构往返：对每棵树 t，parse(to_string(t)) == t（结构相等）。
2. 最小性：删去输出中的任意一对括号，重新解析要么失败，要么得到
   结构不同的树。

对默认运算符表（含负号、分数、右结合幂、条件表达式、函数调用）
和一张自定义表（含 none 结合性运算符）分别穷举。
"""
import unittest
from fractions import Fraction

from minparen import (Binary, Call, Cond, Const, LEFT, NONE, RIGHT,
                      OperatorTable, ParseError, Parser, Unary, Var, render)


def generate_trees(max_size, leaves, unary_ops, binary_ops, calls):
    """按节点数穷举所有语法树。size = 节点个数。"""
    by_size = [[] for _ in range(max_size + 1)]
    by_size[1] = list(leaves)
    for size in range(2, max_size + 1):
        out = []
        child = size - 1
        if child >= 1:
            for sub in by_size[child]:
                for op in unary_ops:
                    out.append(Unary(op, sub))
                for name, argc in calls:
                    if argc == 1:
                        out.append(Call(name, (sub,)))
        for ls in range(1, size - 1):
            rs = size - 1 - ls
            if rs < 1:
                continue
            for left in by_size[ls]:
                for right in by_size[rs]:
                    for op in binary_ops:
                        out.append(Binary(op, left, right))
                    for name, argc in calls:
                        if argc == 2:
                            out.append(Call(name, (left, right)))
        # 条件表达式：1 + a + b + c = size
        for a in range(1, size - 1):
            for b in range(1, size - 1):
                c = size - 1 - a - b
                if c < 1:
                    continue
                for x in by_size[a]:
                    for y in by_size[b]:
                        for z in by_size[c]:
                            out.append(Cond(x, y, z))
        by_size[size] = out
    return [tree for level in by_size for tree in level]


def remove_pair(text, pair):
    i, j = pair
    return text[:i] + text[i + 1:j] + text[j + 1:]


def check_all(trees, table):
    parser = Parser(table)
    checked = removed = 0
    for tree in trees:
        r = render(tree, table)
        back = parser.parse(r.text)
        assert back == tree, f"往返失败: {tree!r} -> {r.text!r} -> {back!r}"
        checked += 1
        for pair in r.parens:
            broken = remove_pair(r.text, pair)
            removed += 1
            try:
                alt = parser.parse(broken)
            except ParseError:
                continue  # 删括号后解析失败：符合要求
            assert alt != tree, (
                f"括号冗余: {tree!r} -> {r.text!r}，删去 {pair} 后 "
                f"{broken!r} 仍解析回原树"
            )
    return checked, removed


class RoundTripTests(unittest.TestCase):
    def test_default_table_exhaustive(self):
        from minparen import DEFAULT_TABLE
        leaves = [Const(0), Const(1), Const(Fraction(1, 2)), Var("a"), Var("b")]
        trees = generate_trees(
            max_size=5,
            leaves=leaves,
            unary_ops=["-"],
            binary_ops=["+", "-", "*", "/", "^"],
            calls=[("f", 1), ("g", 2)],
        )
        checked, removed = check_all(trees, DEFAULT_TABLE)
        print(f"\n默认表：{checked} 棵树往返通过，{removed} 对括号删除验证通过")

    def test_custom_table_exhaustive(self):
        table = OperatorTable(
            binary={
                "+": (1, LEFT),
                "=": (2, NONE),
                "*": (3, LEFT),
                "^": (5, RIGHT),
            },
            unary={"-": 4},
        )
        leaves = [Const(0), Const(1), Var("a")]
        trees = generate_trees(
            max_size=4,
            leaves=leaves,
            unary_ops=["-"],
            binary_ops=["+", "=", "*", "^"],
            calls=[("f", 1)],
        )
        checked, removed = check_all(trees, table)
        print(f"\n自定义表：{checked} 棵树往返通过，{removed} 对括号删除验证通过")


if __name__ == "__main__":
    unittest.main()
