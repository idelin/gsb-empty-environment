import unittest

from recordlang.api import typecheck_program
from recordlang.ast_nodes import show_type
from recordlang.errors import LangError


def types_of(src):
    return [show_type(t) for t in typecheck_program(src)]


class WidthSubtypingTest(unittest.TestCase):
    def test_extra_fields_accepted_at_readonly_parameter(self):
        src = """
        let get_x = fn (p: { x: Int } @ro) -> Int { p.x };
        let point3 = { x = 1, y = 2, z = 3 };
        get_x(point3);
        """
        self.assertEqual(types_of(src), ["Int"])

    def test_extra_fields_rejected_at_writable_parameter(self):
        src = """
        let set_x = fn (p: { x: Int } @rw) -> { x: Int } { p with x = 0 };
        let point3 = { x = 1, y = 2, z = 3 };
        set_x(point3);
        """
        with self.assertRaises(LangError) as ctx:
            typecheck_program(src)
        self.assertIn("invariant", str(ctx.exception))

    def test_exact_type_accepted_at_writable_parameter(self):
        src = """
        let set_x = fn (p: { x: Int } @rw) -> { x: Int } { p with x = 0 };
        set_x({ x = 9 });
        """
        self.assertEqual(types_of(src), ["{x: Int}"])

    def test_nested_width_subtyping_at_readonly(self):
        src = """
        let f = fn (r: { p: { x: Int } } @ro) -> Int { r.p.x };
        f({ p = { x = 1, y = 2 } });
        """
        self.assertEqual(types_of(src), ["Int"])

    def test_nested_extra_fields_rejected_at_writable(self):
        src = """
        let f = fn (r: { p: { x: Int } } @rw) -> Int { r.p.x };
        f({ p = { x = 1, y = 2 } });
        """
        with self.assertRaises(LangError):
            typecheck_program(src)


class FieldAccessTest(unittest.TestCase):
    def test_missing_field_rejected_at_compile_time(self):
        with self.assertRaises(LangError) as ctx:
            typecheck_program("let r = { x = 1 };\nr.y;\n")
        self.assertIn("no field 'y'", str(ctx.exception))

    def test_access_beyond_readonly_view_rejected(self):
        src = "let f = fn (p: { x: Int } @ro) -> Int { p.y };"
        with self.assertRaises(LangError) as ctx:
            typecheck_program(src)
        self.assertIn("no field 'y'", str(ctx.exception))

    def test_error_span_is_utf8_byte_range(self):
        src = "let café = { x = 1 };\ncafé.y;\n"
        with self.assertRaises(LangError) as ctx:
            typecheck_program(src)
        start, end = ctx.exception.span
        self.assertEqual(src.encode("utf-8")[start:end].decode("utf-8"), "y")


class UpdateExtendTest(unittest.TestCase):
    def test_update_existing_field(self):
        self.assertEqual(types_of("{ x = 1 } with x = 2;"), ["{x: Int}"])

    def test_update_missing_field_requires_extend(self):
        with self.assertRaises(LangError) as ctx:
            typecheck_program("{ x = 1 } with y = 2;")
        self.assertIn("extend", str(ctx.exception))

    def test_extend_adds_field(self):
        self.assertEqual(
            types_of("extend { x = 1 } with y = 2;"), ["{x: Int, y: Int}"]
        )

    def test_extend_existing_field_rejected(self):
        with self.assertRaises(LangError) as ctx:
            typecheck_program("extend { x = 1 } with x = 2;")
        self.assertIn("already exists", str(ctx.exception))

    def test_extended_field_is_accessible(self):
        self.assertEqual(types_of("(extend { x = 1 } with y = 2).y;"), ["Int"])


class FunctionTest(unittest.TestCase):
    def test_return_type_is_covariant(self):
        src = "let f = fn (x: Int @ro) -> { x: Int } { { x = x, y = 2 } };"
        types_of(src)  # must not raise

    def test_argument_type_mismatch(self):
        src = """
        let f = fn (x: Int @ro) -> Int { x };
        f(true);
        """
        with self.assertRaises(LangError):
            typecheck_program(src)

    def test_unbound_variable(self):
        with self.assertRaises(LangError):
            typecheck_program("x;")


if __name__ == "__main__":
    unittest.main()
