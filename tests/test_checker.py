import unittest

from rational_id.checker import IDENTICAL, NOT_IDENTICAL, UNKNOWN, check


class IdenticalTest(unittest.TestCase):
    def test_expansion(self):
        self.assertEqual(check("(x+1)^2", "x^2+2*x+1").verdict, IDENTICAL)

    def test_multivariate(self):
        self.assertEqual(check("1/x + 1/y", "(x+y)/(x*y)").verdict, IDENTICAL)

    def test_zero_difference(self):
        self.assertEqual(
            check("(a+b)^2 - a^2 - 2*a*b - b^2", "0").verdict, IDENTICAL)

    def test_rational_constants(self):
        self.assertEqual(check("1/2", "0.5").verdict, IDENTICAL)

    def test_compound_fractions_domain_differs(self):
        # (1/x)/(1/y) 在 y=0 处无定义，而 y/x 有定义：定义域不同
        r = check("(1/x)/(1/y)", "y/x")
        self.assertEqual(r.verdict, NOT_IDENTICAL)
        self.assertEqual(r.counterexample["point"]["y"], "0")

    def test_both_nowhere_defined_is_vacuously_identical(self):
        r = check("1/(x-x)", "2/(y-y)")
        self.assertEqual(r.verdict, IDENTICAL)

    def test_log_states_applicable_domain(self):
        r = check("(x+1)^2", "x^2+2*x+1")
        self.assertTrue(any("适用域" in entry for entry in r.log))


class NotIdenticalTest(unittest.TestCase):
    def test_value_difference(self):
        r = check("x+1", "x+2")
        self.assertEqual(r.verdict, NOT_IDENTICAL)
        self.assertTrue(r.counterexample["verified"])
        self.assertNotEqual(r.counterexample["left"], r.counterexample["right"])

    def test_random_point_agreement_is_not_identity(self):
        # 两式在 x = 0,1,2,3 都相等，但并不恒等；必须由精确比较给出结论
        r = check("x*(x-1)*(x-2)*(x-3)", "0")
        self.assertEqual(r.verdict, NOT_IDENTICAL)
        witness = int(r.counterexample["point"]["x"])
        self.assertNotIn(witness, (0, 1, 2, 3))

    def test_reduction_alone_is_not_enough_x_over_x(self):
        r = check("x/x", "1")
        self.assertEqual(r.verdict, NOT_IDENTICAL)
        self.assertEqual(r.counterexample["point"], {"x": "0"})
        self.assertIn("无定义", r.counterexample["left"])
        self.assertEqual(r.counterexample["right"], "1")

    def test_reduction_alone_is_not_enough_hole(self):
        r = check("(x^2-1)/(x-1)", "x+1")
        self.assertEqual(r.verdict, NOT_IDENTICAL)
        self.assertEqual(r.counterexample["point"], {"x": "1"})
        self.assertIn("无定义", r.counterexample["left"])

    def test_domain_difference_multivariate(self):
        r = check("(x^2-y^2)/(x-y)", "x+y")
        self.assertEqual(r.verdict, NOT_IDENTICAL)
        point = r.counterexample["point"]
        self.assertEqual(point["x"], point["y"])
        self.assertIn("无定义", r.counterexample["left"])

    def test_nested_division_domain(self):
        r = check("1/(1/x)", "x")
        self.assertEqual(r.verdict, NOT_IDENTICAL)
        self.assertEqual(r.counterexample["point"], {"x": "0"})

    def test_one_side_nowhere_defined(self):
        r = check("1/(x-x)", "1")
        self.assertEqual(r.verdict, NOT_IDENTICAL)
        self.assertTrue(r.counterexample["verified"])

    def test_denominator_change_inside_sum(self):
        r = check("x + 1/(x-x)", "x")
        self.assertEqual(r.verdict, NOT_IDENTICAL)


class UnknownTest(unittest.TestCase):
    def test_unsupported_function_call(self):
        r = check("sin(x)", "sin(x)")
        self.assertEqual(r.verdict, UNKNOWN)
        self.assertIn("支持域", r.reason)

    def test_unsupported_negative_exponent(self):
        r = check("x^-1", "1/x")
        self.assertEqual(r.verdict, UNKNOWN)

    def test_term_budget_exceeded(self):
        r = check("(a+b+c+d+e)^4", "(a+b+c+d+e)^4", term_budget=10)
        self.assertEqual(r.verdict, UNKNOWN)
        self.assertIn("项数预算", r.reason)

    def test_identical_within_budget(self):
        r = check("(a+b)^3", "a^3+3*a^2*b+3*a*b^2+b^3")
        self.assertEqual(r.verdict, IDENTICAL)


if __name__ == "__main__":
    unittest.main()
