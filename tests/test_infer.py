import unittest

from minihm.errors import InfiniteType, TypeMismatch, UnboundName
from minihm.infer import infer_program
from minihm.report import render_error
from minihm.source import Source
from minihm.types import show

from .helpers import parse


def type_of(code: str) -> str:
    return show(infer_program(parse(code)))


def error_of(code: str) -> str:
    try:
        infer_program(parse(code))
    except Exception as exc:  # noqa: BLE001 - re-raised by assertions below
        return render_error(exc, Source(code))
    raise AssertionError("expected a type error")


class InferTest(unittest.TestCase):
    def test_int_and_bool_literals(self):
        self.assertEqual(type_of("42"), "Int")
        self.assertEqual(type_of("true"), "Bool")

    def test_arithmetic_and_comparison(self):
        self.assertEqual(type_of("1 + 2 * 3"), "Int")
        self.assertEqual(type_of("1 < 2"), "Bool")
        self.assertEqual(type_of("1 == 2"), "Bool")
        self.assertEqual(type_of("true == false"), "Bool")

    def test_identity_function_is_polymorphic(self):
        self.assertEqual(type_of("fun x -> x"), "a -> a")

    def test_identity_reused_at_two_types(self):
        # let 泛化：id 同时用于 Bool -> Bool 与 Int -> Int
        code = "let id = fun x -> x in if id true then id 1 else id 2"
        self.assertEqual(type_of(code), "Int")

    def test_let_generalization(self):
        code = "let f = fun x -> x in let a = f 1 in f true"
        self.assertEqual(type_of(code), "Bool")

    def test_lambda_bound_variable_is_not_generalized(self):
        with self.assertRaises(TypeMismatch):
            infer_program(parse("fun f -> if f true then f 1 else 0"))

    def test_higher_order_compose(self):
        code = ("let compose = fun f -> fun g -> fun x -> f (g x) in "
                "let inc = fun n -> n + 1 in compose inc inc 0")
        self.assertEqual(type_of(code), "Int")

    def test_self_application_rejected_by_occurs_check(self):
        with self.assertRaises(InfiniteType):
            infer_program(parse("fun x -> x x"))

    def test_occurs_check_through_let(self):
        with self.assertRaises(InfiniteType):
            infer_program(parse("let f = fun x -> x x in f"))

    def test_infinite_type_message(self):
        msg = error_of("fun x -> x x")
        self.assertIn("infinite type", msg)
        self.assertIn("occurs check", msg)
        self.assertIn("bytes 4..5", msg)   # parameter `x`
        self.assertIn("bytes 9..12", msg)  # the application `x x`

    def test_bool_plus_int_locates_both_constraint_sources(self):
        msg = error_of("true + 1")
        self.assertIn("cannot unify `Bool` with `Int`", msg)
        self.assertIn("bytes 0..4", msg)   # `true`  -> Bool 的来源
        self.assertIn("bytes 5..6", msg)   # `+`     -> Int  的来源
        self.assertIn("boolean literal `true`", msg)
        self.assertIn("`+` requires `Int` operands", msg)
        self.assertIn("constraint chain", msg)

    def test_chain_through_let_and_application(self):
        code = "let f = fun x -> x + 1 in f true"
        msg = error_of(code)
        self.assertIn("bytes 28..32", msg)  # `true`
        self.assertIn("bytes 19..20", msg)  # `+` inside the let body
        self.assertIn("function application", msg)

    def test_if_condition_must_be_bool(self):
        msg = error_of("if 1 then 2 else 3")
        self.assertIn("bytes 3..4", msg)   # the `1`
        self.assertIn("condition of `if`", msg)

    def test_if_branches_must_agree(self):
        msg = error_of("if true then 1 else false")
        self.assertIn("both branches of `if`", msg)

    def test_equality_mismatch(self):
        msg = error_of("1 == true")
        self.assertIn("`==`", msg)
        self.assertIn("bytes 0..1", msg)   # `1`
        self.assertIn("bytes 5..9", msg)   # `true`

    def test_unbound_variable(self):
        with self.assertRaises(UnboundName):
            infer_program(parse("x + 1"))

    def test_unicode_positions_in_errors(self):
        # λ 占 2 个 UTF-8 字节，后续位置仍按字节区间报告
        msg = error_of("let λ = true in λ + 1")
        self.assertIn("bytes 9..13", msg)  # `true`
        self.assertIn("bytes 20..21", msg) # `+`


if __name__ == "__main__":
    unittest.main()
