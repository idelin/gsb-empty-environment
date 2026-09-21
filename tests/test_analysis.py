import unittest

from minilang.analysis import analyze
from minilang.ast_nodes import Print
from minilang.cfg import build_cfg
from minilang.parser import parse


def analyze_src(src):
    return analyze(build_cfg(parse(src)))


def levels(src):
    return [d.level for d in analyze_src(src).diagnostics]


class ReachingDefinitionTests(unittest.TestCase):
    def test_straight_line_no_diagnostics(self):
        self.assertEqual(levels("let x = 1; print x;"), [])

    def test_reaching_definitions_accumulate(self):
        result = analyze_src("let x = 1; let y = 2; print x + y;")
        print_node = next(
            n for n in result.cfg.nodes if isinstance(n.stmt, Print)
        )
        reaching = result.reaching_in[print_node.id]
        self.assertEqual({d[0] for d in reaching}, {"x", "y"})

    def test_redefinition_kills_earlier_definition(self):
        result = analyze_src("let x = 1; x = 2; print x;")
        print_node = next(n for n in result.cfg.nodes if isinstance(n.stmt, Print))
        reaching = {d for d in result.reaching_in[print_node.id] if d[0] == "x"}
        self.assertEqual(len(reaching), 1)  # only the second def survives

    def test_loop_fixed_point_both_defs_reach(self):
        src = """
        let i = 0;
        let s = 0;
        while i < 3 {
            s = s + i;
            i = i + 1;
        }
        print s;
        """
        result = analyze_src(src)
        self.assertEqual(result.diagnostics, [])
        print_node = next(n for n in result.cfg.nodes if isinstance(n.stmt, Print))
        s_defs = {d for d in result.reaching_in[print_node.id] if d[0] == "s"}
        # fixed point: both 'let s' and the in-loop 's = s + i' reach the print
        self.assertEqual(len(s_defs), 2)
        self.assertIn("s", result.assigned_in[print_node.id])


class UseBeforeDefTests(unittest.TestCase):
    def test_undefined_variable_is_error(self):
        (diag,) = analyze_src("print u;").diagnostics
        self.assertEqual(diag.level, "error")
        self.assertIn("'u'", diag.message)

    def test_use_in_own_initializer(self):
        (diag,) = analyze_src("let x = x;").diagnostics
        self.assertEqual(diag.level, "error")

    def test_if_single_branch_not_guaranteed_defined(self):
        src = "let a = 1; if a { b = 2; } print b;"
        (diag,) = analyze_src(src).diagnostics
        self.assertEqual(diag.level, "warning")
        self.assertIn("'b'", diag.message)

    def test_if_both_branches_assign_is_defined(self):
        src = "let a = 1; if a { b = 1; } else { b = 2; } print b;"
        self.assertEqual(levels(src), [])

    def test_use_inside_other_branch(self):
        src = "let a = 1; if a { b = 1; } else { print b; }"
        (diag,) = analyze_src(src).diagnostics
        # no definition of b reaches the else branch at all: hard error
        self.assertEqual(diag.level, "error")

    def test_while_zero_iteration_path(self):
        src = "let i = 10; while i < 0 { c = 1; i = i - 1; } print c;"
        (diag,) = analyze_src(src).diagnostics
        self.assertEqual(diag.level, "warning")
        self.assertIn("'c'", diag.message)

    def test_defined_before_loop_used_after(self):
        src = "let i = 0; let s = 0; while i < 3 { s = s + i; i = i + 1; } print s;"
        self.assertEqual(levels(src), [])

    def test_nested_branches(self):
        src = """
        let a = 1;
        if a {
            if a { v = 1; } else { v = 2; }
            print v;
        }
        """
        self.assertEqual(levels(src), [])

    def test_diagnostic_span_points_at_use(self):
        src = "let a = 1;\nprint a + zz;"
        (diag,) = analyze_src(src).diagnostics
        self.assertEqual(src.encode()[diag.start : diag.end], b"zz")


class UnreachableTests(unittest.TestCase):
    def test_statements_after_halt_are_unreachable(self):
        result = analyze_src("print 1; halt; print 2; print 3;")
        infos = [d for d in result.diagnostics if d.level == "info"]
        self.assertEqual(len(infos), 2)
        self.assertEqual(len(result.unreachable_nodes), 2)
        self.assertTrue(all(d.message == "unreachable statement" for d in infos))

    def test_unreachable_code_skips_use_diagnostics(self):
        # 'zz' is undefined, but the statement is unreachable: only info
        result = analyze_src("halt; print zz;")
        self.assertEqual([d.level for d in result.diagnostics], ["info"])

    def test_constant_false_if_branch_unreachable(self):
        result = analyze_src("if 0 { print 1; } print 2;")
        self.assertEqual([d.level for d in result.diagnostics], ["info"])

    def test_constant_false_while_body_unreachable(self):
        result = analyze_src("while 0 { print 1; } print 2;")
        self.assertEqual([d.level for d in result.diagnostics], ["info"])

    def test_constant_true_while_has_no_fallthrough(self):
        result = analyze_src("while 1 { halt; } print 1;")
        self.assertEqual([d.level for d in result.diagnostics], ["info"])

    def test_reachable_program_has_no_info(self):
        self.assertEqual(levels("let x = 1; print x;"), [])
