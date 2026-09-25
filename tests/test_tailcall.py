"""tailcall 解释器的 unittest 测试套件。

运行：python3.11 -m unittest discover -v
"""

import sys
import unittest

from tailcall import LangError, parse, run

SUM_TO = (
    "def sum_to(n, acc) = if n == 0 then acc else sum_to(n - 1, acc + n)\n"
    "sum_to(100000, 0)\n"
)


def run_src(src, trace=False):
    return run(parse(src), trace=trace)


class TailCallTests(unittest.TestCase):
    def test_tail_recursion_100k_constant_physical_stack(self):
        # 尾递归十万次：物理帧不增长，Python 调用栈也不消耗
        program = parse(SUM_TO)
        old_limit = sys.getrecursionlimit()
        sys.setrecursionlimit(200)
        try:
            m = run(program)
        finally:
            sys.setrecursionlimit(old_limit)
        self.assertIsNone(m.error)
        self.assertEqual(m.result, 5000050000)
        self.assertEqual(m.max_frames, 1)
        self.assertEqual(m.tail_reuse_count, 100001)

    def test_pending_computation_blocks_tail_call(self):
        # 1 + f()：加法待完成，f 的调用不是尾调用，帧随深度增长
        src = (
            "def f(n) = if n == 0 then 0 else 1 + f(n - 1)\n"
            "f(100)\n"
        )
        m = run_src(src)
        self.assertEqual(m.result, 100)
        self.assertEqual(m.max_frames, 101)
        # 仅主程序对 f(100) 的那一次尾调用复用了帧
        self.assertEqual(m.tail_reuse_count, 1)

    def test_accumulator_version_is_tail(self):
        # 同一逻辑的累加器写法是尾调用，帧数保持常数
        src = (
            "def f(n, acc) = if n == 0 then acc else f(n - 1, acc + 1)\n"
            "f(100, 0)\n"
        )
        m = run_src(src)
        self.assertEqual(m.result, 100)
        self.assertEqual(m.max_frames, 1)
        self.assertEqual(m.tail_reuse_count, 101)

    def test_if_let_and_or_are_tail_positions(self):
        m = run_src(
            "def f(n) = if n == 0 then 0 else let m = n - 1 in f(m)\n"
            "f(20000)\n"
        )
        self.assertEqual(m.result, 0)
        self.assertEqual(m.max_frames, 1)
        m = run_src(
            "def g(n) = n == 0 or g(n - 1)\n"
            "g(20000)\n"
        )
        self.assertIs(m.result, True)
        self.assertEqual(m.max_frames, 1)

    def test_finally_cleanup_order_normal(self):
        m = run_src(
            'def leaf() = emit("leaf")\n'
            'def guarded() = try leaf() finally emit("cleanup")\n'
            'try guarded() finally emit("outer")\n'
        )
        self.assertIsNone(m.error)
        self.assertEqual(m.output, ["leaf", "cleanup", "outer"])
        # try 体内的调用不是尾调用：leaf 与 guarded 都压了新帧
        self.assertEqual(m.max_frames, 3)

    def test_finally_cleanup_order_on_error(self):
        m = run_src(
            'def boom() = 1 / 0\n'
            'def inner() = try boom() finally emit("inner 清理")\n'
            'try inner() finally emit("outer 清理")\n'
        )
        self.assertIsNotNone(m.error)
        self.assertIn("除数为零", m.error.message)
        self.assertEqual(m.output, ["inner 清理", "outer 清理"])

    def test_finally_value_is_discarded(self):
        m = run_src("try 1 finally 2")
        self.assertEqual(m.result, 1)

    def test_logical_trace_is_readable(self):
        src = (
            "def fact(n, acc) = if n == 0 then acc else fact(n - 1, acc * n)\n"
            "0 + fact(3, 1)\n"
        )
        m = run_src(src, trace=True)
        self.assertEqual(m.result, 6)
        text = "\n".join(m.trace_log)
        self.assertIn("fact(3, 1)（新帧）", text)
        self.assertIn("复用帧", text)
        self.assertIn("返回 6", text)

    def test_error_keeps_logical_trace_tail(self):
        # 尾递归出错：物理帧已复用，逻辑轨迹仍保留完整调用链
        src = (
            "def boom(n) = if n == 0 then 1 / 0 else boom(n - 1)\n"
            "boom(10)\n"
        )
        m = run_src(src)
        text = str(m.error)
        self.assertIn("逻辑调用轨迹", text)
        self.assertIn("boom(10)", text)
        self.assertIn("boom(0)", text)
        self.assertIn("尾复用", text)

    def test_error_keeps_logical_trace_non_tail(self):
        src = (
            "def down(n) = if n == 0 then 1 / 0 else 0 + down(n - 1)\n"
            "down(3)\n"
        )
        m = run_src(src)
        text = str(m.error)
        self.assertIn("down(0)", text)
        self.assertIn("down(1)", text)
        self.assertIn("down(2)", text)
        self.assertIn("down(3)", text)


class SemanticTests(unittest.TestCase):
    def test_arithmetic_and_precedence(self):
        self.assertEqual(run_src("1 + 2 * 3").result, 7)
        self.assertEqual(run_src("(1 + 2) * 3").result, 9)
        self.assertEqual(run_src("10 - 4 - 3").result, 3)
        self.assertEqual(run_src("2 * 3 % 4").result, 2)
        self.assertEqual(run_src("-3 + 1").result, -2)
        self.assertIs(run_src("1 + 1 == 2").result, True)
        self.assertIs(run_src("not true").result, False)
        self.assertIs(run_src("if 3 > 2 then 10 else 20").result, 10)

    def test_and_or_short_circuit(self):
        self.assertIs(run_src("true or 1 / 0").result, True)
        self.assertIs(run_src("false and 1 / 0").result, False)
        self.assertIs(run_src("false or true").result, True)
        m = run_src("1 / 0 or true")
        self.assertIn("除数为零", m.error.message)

    def test_let_and_emit(self):
        m = run_src("let x = emit(3) in x + 1")
        self.assertEqual(m.result, 4)
        self.assertEqual(m.output, [3])

    def test_string_values(self):
        m = run_src('emit("你好")')
        self.assertEqual(m.output, ["你好"])

    def test_arity_error(self):
        m = run_src("def f(a, b) = a + b\nf(1)")
        self.assertIn("需要 2 个参数，实际传入 1 个", m.error.message)

    def test_undefined_name(self):
        m = run_src("x + 1")
        self.assertIn("未定义的名字：x", m.error.message)

    def test_call_non_function(self):
        m = run_src("1(2)")
        self.assertIn("不可调用", m.error.message)

    def test_syntax_error_has_span(self):
        with self.assertRaises(LangError) as ctx:
            parse("1 +")
        self.assertIsNotNone(ctx.exception.span)


class ByteSpanTests(unittest.TestCase):
    def test_spans_are_utf8_byte_ranges(self):
        src = "# 注释：中文字符\nlet 甲 = 41 in 甲 + 1"
        program = parse(src)
        data = src.encode("utf-8")
        main = program.main
        self.assertEqual(data[main.value.span[0]:main.value.span[1]], b"41")
        self.assertEqual(data[main.body.left.span[0]:main.body.left.span[1]],
                         "甲".encode("utf-8"))
        self.assertEqual(run(program).result, 42)

    def test_error_span_slices_source_bytes(self):
        src = "# 中文注释\n1 / 0"
        m = run_src(src)
        span = m.error.span
        self.assertEqual(src.encode("utf-8")[span[0]:span[1]], b"1 / 0")

    def test_string_token_span_with_multibyte(self):
        src = 'emit("你好")'
        program = parse(src)
        call = program.main
        data = src.encode("utf-8")
        arg = call.args[0]
        self.assertEqual(data[arg.span[0]:arg.span[1]],
                         '"你好"'.encode("utf-8"))


if __name__ == "__main__":
    unittest.main()
