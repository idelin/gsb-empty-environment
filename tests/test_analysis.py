import unittest

from minilang import CFGBuilder, analyze, parse_source


def run(src):
    prog = parse_source(src)
    cfg = CFGBuilder().build(prog)
    return analyze(prog, cfg)


def kinds(result):
    return [d.kind for d in result.diagnostics]


class TestReachingDefinitions(unittest.TestCase):
    def test_loop_fixpoint_both_defs_reach(self):
        # x = 1; while (x < 10) { x = x + 1; } y = x;
        result = run("x = 1; while (x < 10) { x = x + 1; } y = x;")
        cfg = result.cfg
        cond = next(b for b in cfg.blocks if b.kind == "cond")
        inner = next(b for b in cfg.blocks
                     if b.kind == "assign" and b.stmt.name == "x" and b.id != 1)
        first = cfg.entry.succs[0]
        # 不动点：循环条件处两条定义都能到达
        self.assertEqual(result.reach_in[cond.id],
                         {("x", first.id), ("x", inner.id)})
        # 循环之后两条定义同样都能到达
        after = next(b for b in cfg.blocks if b.kind == "assign" and b.stmt.name == "y")
        self.assertEqual(result.reach_in[after.id],
                         {("x", first.id), ("x", inner.id)})

    def test_kill_removes_overwritten_defs(self):
        result = run("x = 1; x = 2;")
        cfg = result.cfg
        second = cfg.entry.succs[0].succs[0]
        self.assertEqual(result.reach_out[second.id], {("x", second.id)})


class TestDefinitelyDefined(unittest.TestCase):
    def test_assigned_in_both_branches_is_defined(self):
        result = run("a = 1; if (a > 0) { b = 2; } else { b = 3; } c = b;")
        self.assertNotIn("undefined-use", kinds(result))

    def test_assigned_in_one_branch_is_not_defined(self):
        result = run("a = 1; if (a > 0) { b = 2; } c = b;")
        undef = [d for d in result.diagnostics if d.kind == "undefined-use"]
        self.assertEqual(len(undef), 1)
        self.assertIn("'b'", undef[0].message)

    def test_loop_zero_trip_path(self):
        # 循环可能一次都不执行，循环体内的赋值不能保证循环后已定义
        result = run("a = 1; while (a > 10) { d = 1; } e = d;")
        undef = [d for d in result.diagnostics if d.kind == "undefined-use"]
        self.assertEqual(len(undef), 1)
        self.assertIn("'d'", undef[0].message)

    def test_var_defined_before_loop_ok_inside(self):
        result = run("x = 1; while (x < 10) { x = x + 1; } print x;")
        self.assertNotIn("undefined-use", kinds(result))

    def test_never_assigned_var(self):
        result = run("print q;")
        self.assertEqual(kinds(result), ["undefined-use"])

    def test_self_reference_before_def(self):
        result = run("x = x + 1;")
        self.assertEqual(kinds(result), ["undefined-use"])


class TestUnreachable(unittest.TestCase):
    def test_dead_if_branch_marked(self):
        result = run("x = 1; if (0) { u = 1; } print x;")
        unreach = [d for d in result.diagnostics if d.kind == "unreachable"]
        self.assertEqual(len(unreach), 1)
        self.assertEqual(kinds(result), ["unreachable"])

    def test_dead_while_body_marked(self):
        result = run("while (0) { a = 1; } b = 2;")
        unreach = [d for d in result.diagnostics if d.kind == "unreachable"]
        self.assertEqual(len(unreach), 1)

    def test_after_infinite_loop_unreachable(self):
        result = run("while (1) { a = 1; } b = 2;")
        unreach = [d for d in result.diagnostics if d.kind == "unreachable"]
        self.assertEqual(len(unreach), 1)

    def test_undefined_use_span_is_byte_range(self):
        src = "# 注释\nprint q;\n"
        result = run(src)
        d = result.diagnostics[0]
        self.assertEqual(src.encode("utf-8")[d.span[0]:d.span[1]], b"q")


if __name__ == "__main__":
    unittest.main()
