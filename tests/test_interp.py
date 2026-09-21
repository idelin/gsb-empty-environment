import unittest

from minihm.interp import evaluate, show_value

from .helpers import parse


def eval_of(code: str):
    return evaluate(parse(code), {})


class InterpTest(unittest.TestCase):
    def test_arithmetic(self):
        self.assertEqual(eval_of("1 + 2 * 3"), 7)
        self.assertEqual(eval_of("10 - 4 - 3"), 3)

    def test_comparison_and_equality(self):
        self.assertEqual(eval_of("1 < 2"), True)
        self.assertEqual(eval_of("2 == 3"), False)

    def test_if(self):
        self.assertEqual(eval_of("if 1 < 2 then 10 else 20"), 10)

    def test_closures_capture_environment(self):
        self.assertEqual(eval_of("let add = fun x -> fun y -> x + y in add 2 3"), 5)

    def test_identity_reused_at_two_types(self):
        code = "let id = fun x -> x in if id true then id 1 else id 2"
        self.assertEqual(eval_of(code), 1)

    def test_compose(self):
        code = ("let compose = fun f -> fun g -> fun x -> f (g x) in "
                "let inc = fun n -> n + 1 in "
                "let double = fun n -> n * 2 in "
                "compose inc double 3")
        self.assertEqual(eval_of(code), 7)

    def test_shadowing(self):
        self.assertEqual(eval_of("let x = 1 in let x = x + 1 in x"), 2)

    def test_show_value(self):
        self.assertEqual(show_value(True), "true")
        self.assertEqual(show_value(3), "3")
        self.assertEqual(show_value(eval_of("fun x -> x")), "<function>")


if __name__ == "__main__":
    unittest.main()
