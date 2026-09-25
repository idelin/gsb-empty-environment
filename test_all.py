"""unittest suite: lexer/parser/interp/optimizer + semantics contract."""
from __future__ import annotations

import unittest

from interp import LangError, run
from lang_parser import ParseError, parse
from lexer import LexError, tokenize
from nodes import Binary, Block, BoolLit, If, IntLit, Let, Print, Var
from optimize import expr_key, optimize_program, unparse


def optimize(src):
    return optimize_program(parse(src))


def run_src(src):
    return run(parse(src))


def rules(rationale):
    return [r["rule"] for r in rationale]


class TestLexerSpans(unittest.TestCase):
    def test_ascii_spans(self):
        toks = tokenize("let x = 12;")
        self.assertEqual(toks[0].span, (0, 3))    # let
        self.assertEqual(toks[1].span, (4, 5))    # x
        self.assertEqual(toks[3].span, (8, 10))   # 12

    def test_utf8_byte_spans(self):
        # '甲' 占 3 个 UTF-8 字节
        src = "let 甲 = 1;"
        toks = tokenize(src)
        name = toks[1]
        self.assertEqual(name.text, "甲")
        self.assertEqual(name.span, (4, 7))
        self.assertEqual(src.encode("utf-8")[4:7].decode("utf-8"), "甲")
        one = toks[3]
        self.assertEqual(one.span, (10, 11))

    def test_let_name_span_utf8(self):
        prog = parse("let 甲 = 1;\nprint 甲;")
        let_stmt = prog.body[0]
        self.assertEqual(let_stmt.name_span, (4, 7))
        var = prog.body[1].value
        self.assertEqual(var.span, (19, 22))

    def test_lex_error_span(self):
        with self.assertRaises(LexError) as cm:
            tokenize("let x = 1 ! 2;")
        self.assertEqual(cm.exception.span, (10, 11))


class TestParser(unittest.TestCase):
    def test_precedence(self):
        prog = parse("print 2 + 3 * 4;")
        expr = prog.body[0].value
        self.assertIsInstance(expr, Binary)
        self.assertEqual(expr.op, "+")
        self.assertEqual(expr.right.op, "*")

    def test_unary_and_not(self):
        prog = parse("print -2; print not true;")
        self.assertEqual(prog.body[0].value.op, "neg")
        self.assertEqual(prog.body[1].value.op, "not")

    def test_parse_error_span(self):
        with self.assertRaises(ParseError) as cm:
            parse("let = 1;")
        self.assertEqual(cm.exception.span, (4, 5))

    def test_op_span_recorded(self):
        prog = parse("print 1 / 0;")
        div = prog.body[0].value
        self.assertEqual(div.op_span, (8, 9))


class TestInterpreter(unittest.TestCase):
    def test_arithmetic(self):
        out, err = run_src("print 2 + 3 * 4; print 7 / 2; print 7 % 3;")
        self.assertIsNone(err)
        self.assertEqual(out, ["14", "3", "1"])

    def test_short_circuit_false_and(self):
        out, err = run_src("print false and (1 / 0);")
        self.assertIsNone(err)
        self.assertEqual(out, ["false"])

    def test_short_circuit_true_or(self):
        out, err = run_src("print true or (1 / 0);")
        self.assertIsNone(err)
        self.assertEqual(out, ["true"])

    def test_error_order_left_to_right(self):
        _, err = run_src("print (1 / 0) + (2 / 0);")
        self.assertIsNotNone(err)
        self.assertEqual(err.message, "division by zero")
        self.assertEqual(err.span, (9, 10))  # 左侧 '/'

    def test_undefined_variable_span(self):
        _, err = run_src("print x;")
        self.assertEqual(err.message, "undefined variable 'x'")
        self.assertEqual(err.span, (6, 7))

    def test_type_error(self):
        _, err = run_src("print 1 + true;")
        self.assertIn("type error", err.message)
        self.assertEqual(err.span, (8, 9))

    def test_scope_shadowing(self):
        out, err = run_src(
            "let y = 1; if true { let y = 2; print y; } print y;")
        self.assertIsNone(err)
        self.assertEqual(out, ["2", "1"])


class TestConstantFold(unittest.TestCase):
    def test_pure_expr_folded(self):
        prog, rat = optimize("print 2 + 3 * 4;")
        self.assertIn("constant-fold", rules(rat))
        value = prog.body[0].value
        self.assertIsInstance(value, IntLit)
        self.assertEqual(value.value, 14)

    def test_fold_keeps_span(self):
        prog, rat = optimize("print 2 + 3;")
        entry = rat[0]
        self.assertEqual(entry["span"], [6, 11])
        self.assertEqual(prog.body[0].value.span, (6, 11))

    def test_erroring_const_not_folded(self):
        prog, rat = optimize("print 1 / 0;")
        self.assertIn("fold-skip", rules(rat))
        self.assertNotIn("constant-fold", rules(rat))
        value = prog.body[0].value
        self.assertIsInstance(value, Binary)  # 保留到运行期
        _, err = run(prog)
        self.assertEqual(err.message, "division by zero")
        self.assertEqual(err.span, (8, 9))

    def test_type_erroring_const_not_folded(self):
        prog, rat = optimize("print 1 + true;")
        self.assertIn("fold-skip", rules(rat))
        _, err = run(prog)
        self.assertIn("type error", err.message)
        self.assertEqual(err.span, (8, 9))


class TestShortCircuitFold(unittest.TestCase):
    def test_false_and_drops_erroring_rhs(self):
        prog, rat = optimize("print false and (1 / 0); print 7;")
        self.assertIn("short-circuit-fold", rules(rat))
        out, err = run(prog)
        self.assertIsNone(err)
        self.assertEqual(out, ["false", "7"])
        self.assertIsInstance(prog.body[0].value, BoolLit)

    def test_true_or_drops_erroring_rhs(self):
        prog, rat = optimize("print true or (1 / 0);")
        self.assertIn("short-circuit-fold", rules(rat))
        out, err = run(prog)
        self.assertIsNone(err)
        self.assertEqual(out, ["true"])

    def test_true_and_becomes_rhs(self):
        prog, rat = optimize("let b = true; print true and b;")
        self.assertIn("short-circuit-fold", rules(rat))
        out, err = run(prog)
        self.assertEqual(out, ["true"])

    def test_false_or_becomes_rhs(self):
        prog, rat = optimize("let b = false; print false or b;")
        self.assertIn("short-circuit-fold", rules(rat))
        out, err = run(prog)
        self.assertEqual(out, ["false"])


class TestBranchSimplify(unittest.TestCase):
    def test_if_true_keeps_then(self):
        prog, rat = optimize("if true { print 1; } else { print 1 / 0; }")
        self.assertIn("branch-simplify", rules(rat))
        self.assertFalse(any(isinstance(s, If) for s in prog.body))
        out, err = run(prog)
        self.assertIsNone(err)
        self.assertEqual(out, ["1"])

    def test_if_false_keeps_else(self):
        prog, rat = optimize("if false { print 1 / 0; } else { print 2; }")
        self.assertIn("branch-simplify", rules(rat))
        out, err = run(prog)
        self.assertEqual(out, ["2"])

    def test_splice_preserves_scope(self):
        # 常量分支拼接后 let 作用域必须不变：块内 let 不外泄
        src = "let y = 1; if true { let y = 2; print y; } print y;"
        out0, err0 = run_src(src)
        prog, _ = optimize(src)
        out1, err1 = run(prog)
        self.assertEqual(out0, ["2", "1"])
        self.assertEqual(out1, out0)
        self.assertTrue(any(isinstance(s, Block) for s in prog.body))

    def test_empty_branches_keep_cond_eval(self):
        # 空分支 if 退化为条件表达式语句：条件中的错误仍须发生
        src = "if (1 / 0) == 1 { } else { }"
        prog, rat = optimize(src)
        self.assertIn("branch-simplify", rules(rat))
        _, err = run(prog)
        self.assertIsNotNone(err)
        self.assertEqual(err.message, "division by zero")

    def test_fold_enables_branch_simplify(self):
        prog, rat = optimize("if 1 + 1 == 2 { print 5; } else { print 6; }")
        self.assertIn("constant-fold", rules(rat))
        self.assertIn("branch-simplify", rules(rat))
        out, _ = run(prog)
        self.assertEqual(out, ["5"])


class TestCSE(unittest.TestCase):
    def test_common_subexpr_reused(self):
        src = ("let x = 5; let y = x * 2; let a = x * 2 + 1; "
               "print a; print y;")
        prog, rat = optimize(src)
        self.assertIn("cse", rules(rat))
        a_value = prog.body[2].value
        self.assertEqual(unparse(a_value), "(y + 1)")
        out, err = run(prog)
        self.assertEqual(out, ["11", "10"])

    def test_cse_respects_rebinding(self):
        src = ("let x = 1; let a = x + 1; let x = 2; let b = x + 1; "
               "print b;")
        prog, rat = optimize(src)
        self.assertNotIn("cse", rules(rat))
        b_value = prog.body[3].value
        self.assertIsInstance(b_value, Binary)  # 未被替换成 a
        out, _ = run(prog)
        self.assertEqual(out, ["3"])

    def test_cse_in_branch_uses_outer_binding(self):
        src = "let x = 3; let y = x * x; if x > 1 { print x * x; }"
        prog, rat = optimize(src)
        self.assertIn("cse", rules(rat))
        out, _ = run(prog)
        self.assertEqual(out, ["9"])

    def test_cse_does_not_leak_out_of_branch(self):
        src = ("let x = 3; if x > 1 { let w = x * x; print w; } "
               "print x * x;")
        prog, rat = optimize(src)
        # 分支内的 let w 在分支外不可见，最后的 x * x 不应被替换为 w
        cse_entries = [r for r in rat if r["rule"] == "cse"
                       and r["after"] == "w"]
        self.assertEqual(cse_entries, [])
        out, _ = run(prog)
        self.assertEqual(out, ["9", "9"])

    def test_no_swap_of_throwing_operands(self):
        # 可能抛错的两项不能交换：错误必须仍发生在左侧 '/'
        src = "print (1 / 0) + (2 / 0);"
        prog, _ = optimize(src)
        _, err = run(prog)
        self.assertEqual(err.span, (9, 10))

    def test_expr_key_pure_canonicalization(self):
        # 纯（可证不抛错）操作数允许规范化顺序
        key1 = expr_key(parse("print 2 + 3;").body[0].value)
        key2 = expr_key(parse("print 3 + 2;").body[0].value)
        self.assertEqual(key1, key2)
        # 含变量的操作数不可证纯，保持书写顺序
        key3 = expr_key(parse("print a + b;").body[0].value)
        key4 = expr_key(parse("print b + a;").body[0].value)
        self.assertNotEqual(key3, key4)


class TestContract(unittest.TestCase):
    """优化前后：输出序列、错误消息、错误字节区间完全一致。"""

    PROGRAMS = [
        "print 2 + 3 * 4;",
        "print false and (1 / 0); print true or (1 / 0);",
        "print 1 / 0;",
        "print (1 / 0) + (2 / 0);",
        "print (2 / 0) + (1 / 0);",
        "print 1 + true;",
        "print not 1;",
        "print x;",
        "let x = 5; let y = x * 2; let a = x * 2 + 1; print a; print y;",
        "let x = 1; let a = x + 1; let x = 2; let b = x + 1; print b;",
        "if true { print 1; } else { print 1 / 0; }",
        "if false { print 1 / 0; } else { print 2; }",
        "if (1 / 0) == 1 { } else { }",
        "let y = 1; if true { let y = 2; print y; } print y;",
        "let x = 3; if x > 1 { print x * x; } print x * x;",
        "print 10 % 0;",
        "let a = 7; print a / 0; print 999;",
        "print (1 < 2) == true;",
        "let 甲 = 4; print 甲 * 甲;",
        "print -(-5); print not (1 > 2);",
    ]

    def test_all_programs_contract(self):
        for src in self.PROGRAMS:
            with self.subTest(src=src):
                out0, err0 = run(parse(src))
                prog, _ = optimize(src)
                out1, err1 = run(prog)
                self.assertEqual(out0, out1)
                self.assertEqual(err0 is None, err1 is None)
                if err0 is not None:
                    self.assertEqual(err0.message, err1.message)
                    self.assertEqual(err0.span, err1.span)

    def test_rationale_schema(self):
        _, rat = optimize("let x = 2 + 3; print x * x; print x * x;")
        self.assertTrue(rat)
        for entry in rat:
            self.assertIn("rule", entry)
            self.assertIn("span", entry)
            self.assertIn("before", entry)
            self.assertIn("after", entry)
            self.assertIn("reason", entry)
            s, e = entry["span"]
            self.assertLessEqual(0, s)
            self.assertLess(s, e)


if __name__ == "__main__":
    unittest.main()
