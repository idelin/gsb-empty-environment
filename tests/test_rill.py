import pathlib
import unittest

from rill import CompileError, compile_source, run_source
from rill.lexer import Lexer
from rill.parser import Parser
from rill.types import INT, STR, Type, is_subtype

ROOT = pathlib.Path(__file__).resolve().parent.parent
EXAMPLES = ROOT / "examples"


def checked(src):
    program = compile_source(src)
    return program


def output_of(src):
    return run_source(src)


def expect_compile_error(src):
    try:
        run_source(src)
    except CompileError:
        return
    raise AssertionError("expected CompileError")


class RecordTypesTests(unittest.TestCase):
    def test_field_order_independent(self):
        a = Type.record([("a", INT), ("b", STR)])
        b = Type.record([("b", STR), ("a", INT)])
        self.assertEqual(a, b)

    def test_read_position_is_width_covariant(self):
        wide = Type.record([("name", STR), ("age", INT)])
        narrow = Type.record([("name", STR)])
        self.assertTrue(is_subtype(wide, narrow, readonly=True))
        self.assertFalse(is_subtype(narrow, wide, readonly=True))

    def test_write_position_is_invariant(self):
        wide = Type.record([("name", STR), ("age", INT)])
        narrow = Type.record([("name", STR)])
        self.assertFalse(is_subtype(wide, narrow, readonly=False))
        self.assertFalse(is_subtype(narrow, wide, readonly=False))

    def test_nested_read_covariance(self):
        outer_narrow = Type.record([("inner", Type.record([("x", INT)]))])
        outer_wide = Type.record(
            [("inner", Type.record([("x", INT), ("y", INT)]))])
        self.assertTrue(is_subtype(outer_wide, outer_narrow, readonly=True))


class ExecutionTests(unittest.TestCase):
    def test_record_access_update_and_alias(self):
        src = """
        fn bump(writable r: {n: Int}) {
            r.n = r.n + 1
        }
        var q = {n: 1}
        bump(q)
        print(q.n)
        """
        self.assertEqual(output_of(src), "2\n")

    def test_extra_fields_ok_for_readonly_fewer_field_param(self):
        src = """
        fn name_of(readonly r: {name: Str}) {
            print(r.name)
        }
        var p = {name: "Ada", age: 36}
        name_of(p)
        name_of({name: "Lin", extra: true})
        """
        self.assertEqual(output_of(src), "Ada\nLin\n")

    def test_record_literal_extra_fields_readonly(self):
        src = """
        fn f(readonly r: {a: Int}) { print(r.a) }
        f({a: 1, b: 2})
        """
        self.assertEqual(output_of(src), "1\n")

    def test_explicit_extend_adds_field(self):
        src = """
        var p = {name: "Ada"}
        extend p.age = 36
        print(p.name)
        print(p.age)
        """
        self.assertEqual(output_of(src), "Ada\n36\n")

    def test_extend_in_branch_does_not_leak(self):
        # The widened field is only visible inside the branch.
        src = """
        var p = {name: "Ada"}
        if true {
            extend p.age = 36
            print(p.age)
        }
        print(p.name)
        """
        self.assertEqual(output_of(src), "36\nAda\n")
        leak = """
        var p = {name: "Ada"}
        if true { extend p.age = 36 }
        print(p.age)
        """
        expect_compile_error(leak)

    def test_while_and_arithmetic(self):
        src = """
        var total = 0
        var i = 1
        while i <= 5 {
            total = total + i
            i = i + 1
        }
        print(total)
        """
        self.assertEqual(output_of(src), "15\n")

    def test_string_concatenation_and_record_render(self):
        src = 'var p = {name: "Ada", age: 36}\nprint("hi " + p.name)\nprint(p)'
        lines = output_of(src).splitlines()
        self.assertEqual(lines[0], "hi Ada")
        self.assertEqual(lines[1], "{age: 36, name: Ada}")

    def test_nested_readonly_covariance_runtime(self):
        src = """
        fn x_of(readonly r: {inner: {x: Int}}) { print(r.inner.x) }
        x_of({inner: {x: 7, y: 9}})
        """
        self.assertEqual(output_of(src), "7\n")

    def test_function_return_value(self):
        src = """
        fn add(readonly a: Int, readonly b: Int): Int { return a + b }
        print(add(2, 3))
        """
        self.assertEqual(output_of(src), "5\n")


class RejectionTests(unittest.TestCase):
    def test_missing_field_access_rejected_at_compile_time(self):
        expect_compile_error('var p = {name: "Ada"}\nprint(p.age)')

    def test_updating_missing_field_requires_extend(self):
        expect_compile_error('var p = {name: "Ada"}\np.age = 36')

    def test_writable_rejects_extra_fields(self):
        # Same widening that is allowed for readonly must be rejected for a
        # writable (mutation) position.
        src = """
        fn set_n(writable r: {n: Int}) { r.n = 5 }
        var wide = {n: 1, tag: "wide"}
        set_n(wide)
        """
        expect_compile_error(src)

    def test_writable_rejects_narrower_record(self):
        # A narrower object could make the function touch a field that does
        # not exist; writable positions therefore require an exact match.
        src = """
        fn touch(writable r: {a: Int, b: Int}) { r.b = r.b + 1 }
        var x = {a: 1}
        touch(x)
        """
        expect_compile_error(src)

    def test_writable_needs_lvalue(self):
        src = """
        fn f(writable r: {a: Int}) { r.a = 1 }
        f({a: 1})
        """
        expect_compile_error(src)

    def test_readonly_parameter_write_rejected(self):
        src = """
        fn f(readonly r: {n: Int}) { r.n = 3 }
        var x = {n: 1}
        f(x)
        """
        expect_compile_error(src)

    def test_let_binding_is_frozen(self):
        expect_compile_error('let p = {n: 1}\np.n = 2')
        expect_compile_error('let x = 1\nx = 2')

    def test_extend_existing_field_rejected(self):
        expect_compile_error("var p = {n: 1}\nextend p.n = 2")

    def test_return_type_mismatch(self):
        expect_compile_error("fn f(): Int { return true }")

    def test_primitive_field_access_rejected(self):
        expect_compile_error("var x = 1\nprint(x.foo)")

    def test_demo_error_files_are_rejected(self):
        for name in [
            "errors_missing_field.rec",
            "errors_unsafe_covariance.rec",
            "errors_update_missing.rec",
            "errors_readonly_write.rec",
        ]:
            with self.subTest(name=name):
                source = (EXAMPLES / name).read_text(encoding="utf-8")
                with self.assertRaises(CompileError):
                    compile_source(source)


class SpanTests(unittest.TestCase):
    def test_spans_are_utf8_byte_intervals(self):
        # '名' is 3 UTF-8 bytes.  String token spans include both quote bytes.
        source = 'var 名 = {"名": 1}\n'
        tokens = Lexer(source).tokenize()
        by_kind_value = {(t.kind, t.value): (t.start, t.end) for t in tokens}
        self.assertEqual(by_kind_value[("NAME", "名")], (4, 7))
        self.assertEqual(by_kind_value[("STR", "名")], (11, 16))
        string_token = next(t for t in tokens if t.kind == "STR" and t.value == "名")
        raw = source.encode("utf-8")[string_token.start + 1:string_token.end - 1]
        self.assertEqual(raw.decode("utf-8"), "名")
        self.assertEqual(len(source.encode("utf-8")), 21)

    def test_missing_field_error_carries_field_span(self):
        source = 'var p = {name: "Ada"}\nprint(p.age)'
        try:
            compile_source(source)
        except CompileError as err:
            segment = source.encode("utf-8")[err.start:err.end].decode("utf-8")
            self.assertEqual(segment, "age")
        else:
            self.fail("expected CompileError")

    def test_chinese_identifier_and_render(self):
        src = 'var 人 = {名: "Ada"}\nprint(人.名)'
        self.assertEqual(output_of(src), "Ada\n")


class DemoFileTests(unittest.TestCase):
    def test_demo_program_runs(self):
        source = (EXAMPLES / "demo.rec").read_text(encoding="utf-8")
        out = run_source(source)
        self.assertEqual(
            out.splitlines(),
            ["name: Ada", "{age: 36, name: Ada}", "2", "36", "6", "7"],
        )


if __name__ == "__main__":
    unittest.main()
