import copy
import unittest
from fractions import Fraction

from demo import X, add, c, mul, pw, sample_proof
from proofcheck.expr import ExprError, normalize
from proofcheck.verifier import verify


class TestNormalize(unittest.TestCase):
    def test_expand_is_exact(self):
        poly = normalize(mul(add(X, c("1/3")), add(X, c("1/6"))), "x")
        self.assertEqual(poly, {2: Fraction(1), 1: Fraction(1, 2), 0: Fraction(1, 18)})
        self.assertIsInstance(poly[1], Fraction)

    def test_float_const_rejected(self):
        with self.assertRaises(ExprError):
            normalize({"const": 0.5}, "x")

    def test_unknown_variable_rejected(self):
        with self.assertRaises(ExprError):
            normalize({"var": "y"}, "x")

    def test_negative_power_rejected(self):
        with self.assertRaises(ExprError):
            normalize({"pow": [X, -1]}, "x")


class TestValidProof(unittest.TestCase):
    def test_valid_proof_passes(self):
        res = verify(sample_proof())
        self.assertTrue(res.ok, res.error)
        self.assertEqual([s.rule for s in res.steps],
                         ["expand", "combine", "differentiate", "substitute"])
        self.assertIsNone(res.failed_step)

    def test_every_step_reports_domain(self):
        res = verify(sample_proof())
        self.assertTrue(all(s.domain for s in res.steps))


class TestTampering(unittest.TestCase):
    def test_tampered_coefficient_locates_first_failure(self):
        proof = copy.deepcopy(sample_proof())
        proof["steps"][1]["output"]["add"][1]["mul"][0] = c(4)  # 3x -> 4x
        res = verify(proof)
        self.assertFalse(res.ok)
        self.assertEqual(res.failed_step, 1)
        self.assertEqual([s.index for s in res.steps], [0])

    def test_tampered_final_step(self):
        proof = copy.deepcopy(sample_proof())
        proof["steps"][3]["output"] = c(6)
        res = verify(proof)
        self.assertFalse(res.ok)
        self.assertEqual(res.failed_step, 3)

    def test_expand_wrong_value(self):
        proof = copy.deepcopy(sample_proof())
        proof["steps"][0]["output"] = add(pw(X, 2), mul(c(3), X), c(3))
        res = verify(proof)
        self.assertFalse(res.ok)
        self.assertEqual(res.failed_step, 0)

    def test_combine_not_fully_combined(self):
        proof = copy.deepcopy(sample_proof())
        proof["steps"][1]["output"] = add(pw(X, 2), mul(c(2), X), X, c(2))
        res = verify(proof)
        self.assertFalse(res.ok)
        self.assertEqual(res.failed_step, 1)
        self.assertIn("同类项", res.error)

    def test_differentiate_wrong_result(self):
        proof = copy.deepcopy(sample_proof())
        proof["steps"][2]["output"] = add(mul(c(2), X), c(4))
        res = verify(proof)
        self.assertFalse(res.ok)
        self.assertEqual(res.failed_step, 2)

    def test_label_is_not_trusted(self):
        # 标签写着 combine，内容却是一次求导：必须被拒绝
        proof = copy.deepcopy(sample_proof())
        proof["steps"][1]["output"] = add(mul(c(2), X), c(3))
        res = verify(proof)
        self.assertFalse(res.ok)
        self.assertEqual(res.failed_step, 1)


class TestChainAndResult(unittest.TestCase):
    def test_chain_break_detected(self):
        proof = copy.deepcopy(sample_proof())
        # 该步自身求导正确，但输入与上一已验证输出脱节
        proof["steps"][2]["input"] = add(pw(X, 2), mul(c(5), X), c(2))
        proof["steps"][2]["output"] = add(mul(c(2), X), c(5))
        res = verify(proof)
        self.assertFalse(res.ok)
        self.assertEqual(res.failed_step, 2)
        self.assertIn("证明链断裂", res.error)

    def test_result_must_equal_last_verified_expr(self):
        proof = copy.deepcopy(sample_proof())
        proof["result"] = c(6)
        res = verify(proof)
        self.assertFalse(res.ok)
        self.assertEqual(res.failed_step, 3)
        self.assertIn("最终结果不等于最后验证表达式", res.error)


class TestRulesAndDomains(unittest.TestCase):
    def test_unknown_rule_rejected(self):
        proof = copy.deepcopy(sample_proof())
        proof["steps"][2]["rule"] = "simplify"
        res = verify(proof)
        self.assertFalse(res.ok)
        self.assertEqual(res.failed_step, 2)
        self.assertIn("未知规则", res.error)

    def test_missing_domain_rejected(self):
        proof = copy.deepcopy(sample_proof())
        del proof["steps"][0]["domain"]
        res = verify(proof)
        self.assertFalse(res.ok)
        self.assertEqual(res.failed_step, 0)
        self.assertIn("适用域", res.error)

    def test_non_rational_substitute_value_rejected(self):
        proof = copy.deepcopy(sample_proof())
        proof["steps"][3]["value"] = "sqrt(2)"
        res = verify(proof)
        self.assertFalse(res.ok)
        self.assertEqual(res.failed_step, 3)

    def test_wrong_variable_rejected(self):
        proof = copy.deepcopy(sample_proof())
        proof["steps"][2]["var"] = "y"
        res = verify(proof)
        self.assertFalse(res.ok)
        self.assertEqual(res.failed_step, 2)


class TestExactRationals(unittest.TestCase):
    def test_fraction_substitution_is_exact(self):
        proof = {
            "variable": "x",
            "steps": [
                {
                    "rule": "substitute",
                    "domain": "x = 1/3 ∈ ℚ",
                    "var": "x",
                    "value": "1/3",
                    "input": add(mul(c(3), pw(X, 2)), mul(c("-1/2"), X), c("1/6")),
                    "output": c("1/3"),
                },
            ],
            "result": c("1/3"),
        }
        # 3*(1/3)^2 - (1/2)*(1/3) + 1/6 = 1/3，精确成立
        res = verify(proof)
        self.assertTrue(res.ok, res.error)

    def test_fraction_tamper_detected_exactly(self):
        proof = {
            "variable": "x",
            "steps": [
                {
                    "rule": "substitute",
                    "domain": "x = 1/3 ∈ ℚ",
                    "var": "x",
                    "value": "1/3",
                    "input": mul(c(3), X),
                    "output": c("0.999999999"),
                },
            ],
            "result": c("0.999999999"),
        }
        res = verify(proof)
        self.assertFalse(res.ok)
        self.assertEqual(res.failed_step, 0)


if __name__ == "__main__":
    unittest.main()
