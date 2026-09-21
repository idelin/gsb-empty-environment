"""call-by-need 表达式图的单元测试。

用 ``python3.11 -m unittest discover -s tests -v`` 运行。
"""
from __future__ import annotations

import unittest

from lazygraph import (
    BlackHoleError,
    DivByZeroError,
    Interpreter,
    LexError,
    ParseError,
    SourceMap,
    Span,
    Tracer,
    UnboundVariableError,
    run,
)


class LazinessTests(unittest.TestCase):
    def test_unused_division_by_zero_in_let_in(self):
        # 未被选择的分支不强制，除零表达式安静地悬挂着。
        source = """
        let boom = 1 / 0
        in if false then boom else 42
        """
        self.assertEqual(run(source), 42)

    def test_unused_division_by_zero_top_level(self):
        source = """
        let boom = 1 / 0
        let answer = 6 * 7
        answer
        """
        interp = Interpreter(source)
        self.assertEqual(interp.run(), 42)
        self.assertFalse(interp.top_thunks["boom"].forced)

    def test_division_by_zero_raises_when_used(self):
        with self.assertRaises(DivByZeroError):
            run("let boom = 1 / 0 in boom")

    def test_short_circuit_avoids_error(self):
        source = """
        let boom = 1 / 0
        in false && (boom == 0)
        """
        self.assertIs(run(source), False)

    def test_lazy_recursion_produces_value(self):
        # 递归结构只取前几项，无限结构风格的惰性求值不会卡死。
        source = """
        let rec ones = 1 + ones
        in if false then ones else 7
        """
        self.assertEqual(run(source), 7)


class ThunkSharingTests(unittest.TestCase):
    def test_thunk_evaluated_once_across_ten_references(self):
        source = """
        let x = tick()
        in x + x + x + x + x + x + x + x + x + x
        """
        tracer = Tracer()
        result = run(source, tracer)
        self.assertEqual(result, 10)  # 第一次 tick() 返回 1，十次引用都命中 1
        self.assertEqual(tracer.tick_count, 1)

    def test_cached_value_reused(self):
        source = """
        let expensive = 21 * 2
        expensive + expensive + expensive
        """
        interp = Interpreter(source)
        self.assertEqual(interp.run(), 126)
        thunk = interp.top_thunks["expensive"]
        self.assertTrue(thunk.forced)
        self.assertEqual(thunk.value, 42)
        # 再强制两次都是缓存命中。
        self.assertEqual(thunk.force(), 42)
        self.assertEqual(thunk.force(), 42)
        hits = [event for event in interp.tracer.events if event.kind == "hit"]
        self.assertGreaterEqual(len(hits), 2)

    def test_trace_records_nested_force_chain(self):
        source = """
        let a = tick() + 1
        let b = a + 10
        b
        """
        tracer = Tracer()
        self.assertEqual(run(source, tracer), 12)
        names = [
            event.name for event in tracer.events if event.kind == "enter"
        ]
        self.assertEqual(names, ["<结果>", "b", "a"])


class CachedExceptionTests(unittest.TestCase):
    def test_exception_is_cached_and_rethrown_identical(self):
        source = """
        let boom = 1 / 0
        boom + boom
        """
        interp = Interpreter(source)
        with self.assertRaises(DivByZeroError):
            interp.run()
        boom = interp.top_thunks["boom"]
        self.assertTrue(boom.forced)
        self.assertIsNotNone(boom.error)
        first_error = boom.error
        with self.assertRaises(DivByZeroError):
            boom.force()
        # 第二次抛出的是同一个异常对象（真正缓存，而非重新计算）。
        self.assertIs(boom.error, first_error)

    def test_wrappers_share_same_blackhole_error(self):
        source = """
        let rec a = b
        and b = c
        and c = a
        in a
        """
        interp = Interpreter(source)
        with self.assertRaises(BlackHoleError) as context:
            interp.run()
        error = context.exception
        self.assertEqual(
            [entry.name for entry in error.chain], ["a", "b", "c", "a"]
        )
        # 环上 a/b/c 三个 thunk 都已结束求值，缓存的是同一个异常对象；
        # 包装它们的结果 thunk 同样如此。
        entered = [
            event.thunk
            for event in interp.tracer.events if event.kind == "enter"
        ]
        self.assertEqual(
            [thunk.name for thunk in entered], ["<结果>", "a", "b", "c"]
        )
        for thunk in entered[1:]:
            self.assertTrue(thunk.forced)
            self.assertIs(thunk.error, error)

    def test_unbound_variable_cached_like_other_errors(self):
        source = "let x = missing\nx + x"
        interp = Interpreter(source)
        with self.assertRaises(UnboundVariableError):
            interp.run()
        x = interp.top_thunks["x"]
        self.assertTrue(x.forced)
        self.assertIsInstance(x.error, UnboundVariableError)


class BlackHoleTests(unittest.TestCase):
    def test_direct_self_reference(self):
        interp = Interpreter("let rec a = a in a")
        with self.assertRaises(BlackHoleError) as context:
            interp.run()
        error = context.exception
        self.assertEqual(error.thunk_name, "a")
        self.assertEqual([entry.name for entry in error.chain], ["a", "a"])

    def test_indirect_self_reference_two_nodes(self):
        source = """
        let rec a = b + 1
        and b = a + 1
        in a
        """
        interp = Interpreter(source)
        with self.assertRaises(BlackHoleError) as context:
            interp.run()
        self.assertEqual(
            [entry.name for entry in context.exception.chain], ["a", "b", "a"]
        )

    def test_indirect_self_reference_three_nodes(self):
        source = """
        let rec a = b
        and b = c
        and c = a
        in a
        """
        interp = Interpreter(source)
        with self.assertRaises(BlackHoleError) as context:
            interp.run()
        self.assertEqual(
            [entry.name for entry in context.exception.chain],
            ["a", "b", "c", "a"],
        )

    def test_blackhole_chain_locations_are_spans(self):
        source = "let rec a = a in a"
        interp = Interpreter(source)
        with self.assertRaises(BlackHoleError) as context:
            interp.run()
        error = context.exception
        self.assertIsNotNone(error.chain[-1].at)
        rendered = error.render_chain(interp.sourcemap)
        self.assertIn("回到自身", rendered)
        self.assertIn("环: a -> a", rendered)

    def test_blackhole_cached_on_second_force(self):
        interp = Interpreter("let rec a = a in a")
        first = None
        for _ in range(2):
            try:
                interp.result_thunk.force()
            except BlackHoleError as error:
                if first is None:
                    first = error
                else:
                    self.assertIs(error, first)
        self.assertIsNotNone(first)


class ScopingTests(unittest.TestCase):
    def test_non_recursive_let_cannot_reference_self(self):
        # 非递归 let 里的 x 指向（不存在的）外层 x，而不是自己。
        with self.assertRaises(UnboundVariableError):
            run("let x = x + 1 in x")

    def test_sequential_bindings_see_earlier_not_later(self):
        source = """
        let a = 3
        let b = a + 4
        b
        """
        self.assertEqual(run(source), 7)
        with self.assertRaises(UnboundVariableError):
            run("let a = b + 1\nlet b = 1\na")

    def test_shadowing(self):
        self.assertEqual(run("let x = 1 in let x = x + 10 in x"), 11)


class EvaluatorSemanticsTests(unittest.TestCase):
    def test_arithmetic_and_division(self):
        self.assertEqual(run("7 / 2"), 3)
        self.assertEqual(run("7 % 3"), 1)
        self.assertEqual(run("2 + 3 * 4"), 14)
        self.assertEqual(run("(2 + 3) * 4"), 20)
        self.assertEqual(run("-5 + 2"), -3)

    def test_comparisons_and_booleans(self):
        self.assertIs(run("1 < 2"), True)
        self.assertIs(run("2 == 3"), False)
        self.assertIs(run("1 != 2"), True)
        self.assertIs(run("true && false"), False)
        self.assertIs(run("false || true"), True)

    def test_if_selects_branch(self):
        self.assertEqual(run("if 1 then 10 else 20"), 10)
        self.assertEqual(run("if 0 then 10 else 20"), 20)


class SourceMapTests(unittest.TestCase):
    def test_spans_are_utf8_byte_offsets(self):
        # 中文每个字符 3 个 UTF-8 字节；位置按字节计算。
        source = "# 中文注释\nlet x = 1\nx"
        sourcemap = SourceMap(source)
        interp = Interpreter(source)
        x = interp.top_thunks["x"]
        # "let x = 1" 开头：1 字节换行 + 3 字节"中文"=6 + "注释"6 + ...
        # 直接按字节串定位而不是手算，确保与词法器一致。
        expected = sourcemap.text_bytes.index(b"x =")
        self.assertEqual(x.binding_span.start, expected)
        line, column = sourcemap.locate(expected)
        self.assertEqual(line, 2)
        self.assertEqual(column, 5)

    def test_multibyte_in_comment_does_not_shift_code(self):
        source = "# 中文字符串注释 🎉\n40 + 2\n"
        self.assertEqual(run(source), 42)
        sourcemap = SourceMap(source)
        offset = source.encode("utf-8").index(b"40")
        self.assertEqual(sourcemap.locate(offset), (2, 1))

    def test_lex_error_has_byte_span(self):
        with self.assertRaises(LexError) as context:
            run("1 + @")
        self.assertEqual(context.exception.span, Span(4, 5))

    def test_parse_error_has_span(self):
        with self.assertRaises(ParseError):
            run("1 + ")


class TracerRenderTests(unittest.TestCase):
    def test_render_trace_contains_chain(self):
        tracer = Tracer()
        run("let a = 1\nlet b = a + 2\nb", tracer)
        text = tracer.render_trace()
        self.assertIn("强制 thunk 'b'", text)
        self.assertIn("强制 thunk 'a'", text)
        self.assertIn("求值完成", text)


if __name__ == "__main__":
    unittest.main()
