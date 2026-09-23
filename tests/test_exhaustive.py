"""穷举“小树”的两条结构性质：

1. 往返：``parse(print(t)) == t``（结构相等，非数值等价）；
2. 括号最小性：删除输出中任意一组成对括号后，要么无法解析，
   要么解析成另一棵树。

穷举按“精确节点预算”枚举所有同构树（set 去重）。集合 1 覆盖全部五个
二元算符与一元负号（预算 6，9018 棵）；集合 2 覆盖条件表达式与函数
调用（预算 5，3830 棵）。
"""

import unittest

from exprlib import ExprError, parse, to_string
from exprlib.ast import BinOp, Call, Cond, Neg, Rat, Var

LEAVES = (Var("a"), Var("b"), Var("x"))
BIN_SET_1 = ("+", "-", "*", "/", "^")
BIN_SET_2 = ("+", "^")


def generate(budget, bin_ops, with_cond=False, with_call=False):
    cache = {0: set()}

    def rec(n):
        if n in cache:
            return cache[n]
        res = set()
        if n == 1:
            res.update(LEAVES)
            res.add(Rat(1))
            res.add(Rat(2, 3))
            cache[n] = res
            return res
        for sub in rec(n - 1):
            res.add(Neg(sub))
        for k in range(1, n - 1):
            lefts = rec(k)
            rights = rec(n - 1 - k)
            for left in lefts:
                for right in rights:
                    for op in bin_ops:
                        res.add(BinOp(op, left, right))
        if with_cond:
            for k in range(1, n - 1):
                rem = n - 1 - k
                conds = rec(k)
                for p in range(1, rem):
                    thens = rec(p)
                    elses = rec(rem - p)
                    for cond in conds:
                        for then in thens:
                            for other in elses:
                                res.add(Cond(cond, then, other))
        if with_call:
            for sub in rec(n - 1):
                res.add(Call("f", (sub,)))
        cache[n] = res
        return res

    out = set()
    for n in range(1, budget + 1):
        out |= rec(n)
    return out


def matching_pairs(text):
    pairs = []
    stack = []
    for i, ch in enumerate(text):
        if ch == "(":
            stack.append(i)
        elif ch == ")" and stack:
            pairs.append((stack.pop(), i))
    return pairs


def assert_tree_minimal(test_case, tree, stat):
    printed = to_string(tree)
    reparsed = parse(printed)
    test_case.assertEqual(reparsed, tree, f"往返失败：{tree!r} -> {printed!r}")
    stat["trees"] += 1
    for lo, hi in matching_pairs(printed):
        stripped = printed[:lo] + printed[lo + 1 : hi] + printed[hi + 1 :]
        stat["pairs"] += 1
        try:
            other = parse(stripped)
        except ExprError:
            stat["failed_to_parse"] += 1
            continue
        test_case.assertNotEqual(
            other, tree,
            f"括号多余：{printed!r} 删除 ({lo},{hi}) 后仍是同一棵树",
        )
        stat["changed_tree"] += 1


class ExhaustiveArithmeticTest(unittest.TestCase):
    def test_all_small_arithmetic_trees(self):
        trees = generate(6, BIN_SET_1)
        self.assertGreater(len(trees), 9000)
        stat = {"trees": 0, "pairs": 0, "failed_to_parse": 0, "changed_tree": 0}
        for tree in trees:
            assert_tree_minimal(self, tree, stat)
        self.assertEqual(stat["pairs"],
                         stat["failed_to_parse"] + stat["changed_tree"])


class ExhaustiveCondCallTest(unittest.TestCase):
    def test_all_small_conditional_call_trees(self):
        trees = generate(5, BIN_SET_2, with_cond=True, with_call=True)
        self.assertGreater(len(trees), 3800)
        stat = {"trees": 0, "pairs": 0, "failed_to_parse": 0, "changed_tree": 0}
        for tree in trees:
            assert_tree_minimal(self, tree, stat)
        self.assertEqual(stat["pairs"],
                         stat["failed_to_parse"] + stat["changed_tree"])


if __name__ == "__main__":
    unittest.main(verbosity=2)
