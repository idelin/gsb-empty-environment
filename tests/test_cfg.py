import unittest

from minilang import CFGBuilder, parse_source


def build(src):
    prog = parse_source(src)
    return CFGBuilder().build(prog)


def ids(blocks):
    return sorted(b.id for b in blocks)


class TestCFG(unittest.TestCase):
    def test_linear_chain(self):
        cfg = build("x = 1; y = 2;")
        entry, exit_ = cfg.entry, cfg.exit
        self.assertEqual(len(cfg.blocks), 4)
        b1, b2 = entry.succs[0], entry.succs[0].succs[0]
        self.assertEqual(b1.kind, "assign")
        self.assertEqual(b2.kind, "assign")
        self.assertEqual(b2.succs, [exit_])

    def test_if_without_else_cond_has_two_succs(self):
        cfg = build("if (a > 0) { b = 1; } c = 2;")
        cond = cfg.entry.succs[0]
        self.assertEqual(cond.kind, "cond")
        self.assertEqual(len(cond.succs), 2)  # then 分支与汇合点

    def test_while_back_edge_and_zero_trip_exit(self):
        cfg = build("while (a > 0) { a = a - 1; } b = 1;")
        cond = cfg.entry.succs[0]
        body = cond.succs[0]
        self.assertEqual(body.kind, "assign")
        self.assertIn(cond, body.succs)          # 回边
        self.assertEqual(len(cond.succs), 2)     # 循环体 + 零次循环出口

    def test_constant_if_prunes_dead_branch(self):
        cfg = build("if (0) { a = 1; } else { a = 2; }")
        cond = cfg.entry.succs[0]
        self.assertEqual(len(cond.succs), 1)
        self.assertEqual(cond.succs[0].stmt.name, "a")
        self.assertEqual(cond.succs[0].stmt.value.value, 2)

    def test_constant_while_zero_skips_body(self):
        cfg = build("while (0) { a = 1; } b = 2;")
        cond = cfg.entry.succs[0]
        self.assertEqual(len(cond.succs), 1)
        self.assertEqual(cond.succs[0].stmt.name, "b")


if __name__ == "__main__":
    unittest.main()
