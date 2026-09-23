import copy
import unittest
from fractions import Fraction

from proofcheck import (
    ProofError,
    REQUIRED_DOMAIN,
    ast_to_poly,
    poly_diff,
    verify_proof,
)

X = {"var": "x"}


def num(s):
    return {"num": s}


def valid_proof():
    return {
        "variable": "x",
        "steps": [
            {"rule": "init",
             "expr": {"mul": [{"add": [X, num("1")]}, {"add": [X, num("2")]}]},
             "domain": REQUIRED_DOMAIN["init"]},
            {"rule": "expand",
             "expr": {"add": [{"pow": [X, 2]},
                              {"mul": [num("2"), X]},
                              X,
                              num("2")]},
             "domain": REQUIRED_DOMAIN["expand"]},
            {"rule": "combine",
             "expr": {"add": [{"pow": [X, 2]},
                              {"mul": [num("3"), X]},
                              num("2")]},
             "domain": REQUIRED_DOMAIN["combine"]},
            {"rule": "diff",
             "expr": {"add": [{"mul": [num("2"), X]}, num("3")]},
             "domain": REQUIRED_DOMAIN["diff"]},
            {"rule": "substitute", "value": "1/2",
             "expr": num("4"),
             "domain": REQUIRED_DOMAIN["substitute"]},
        ],
    }


class TestValidProof(unittest.TestCase):
    def test_valid_proof_passes(self):
        final = verify_proof(valid_proof())
        self.assertEqual(final, {0: Fraction(4)})

    def test_final_equals_last_verified_expression(self):
        proof = valid_proof()
        final = verify_proof(proof)
        last = proof["steps"][-1]
        self.assertEqual(final, ast_to_poly(last["expr"], "x"))

    def test_rational_exact_arithmetic(self):
        # (x + 1/2)^2 在 x = 1/3 处代入 = (5/6)^2 = 25/36，精确有理数
        proof = {
            "variable": "x",
            "steps": [
                {"rule": "init",
                 "expr": {"pow": [{"add": [X, num("1/2")]}, 2]},
                 "domain": REQUIRED_DOMAIN["init"]},
                {"rule": "expand",
                 "expr": {"add": [{"pow": [X, 2]}, X, num("1/4")]},
                 "domain": REQUIRED_DOMAIN["expand"]},
                {"rule": "substitute", "value": "1/3",
                 "expr": num("25/36"),
                 "domain": REQUIRED_DOMAIN["substitute"]},
            ],
        }
        self.assertEqual(verify_proof(proof), {0: Fraction(25, 36)})


class TestTamperLocalization(unittest.TestCase):
    def _tamper_num(self, node, old, new):
        """深优先把第一个值为 old 的 num 改为 new，返回是否成功。"""
        if isinstance(node, dict):
            if node.get("num") == old:
                node["num"] = new
                return True
            for value in node.values():
                targets = value if isinstance(value, list) else [value]
                for item in targets:
                    if self._tamper_num(item, old, new):
                        return True
        return False

    def test_tamper_each_step_locates_first_failure(self):
        # (步骤下标, 被篡改的系数, 篡改后)
        cases = [(1, "2", "5"), (2, "3", "4"), (3, "3", "7"), (4, "4", "9")]
        for index, old, new in cases:
            with self.subTest(step=index):
                proof = copy.deepcopy(valid_proof())
                expr = proof["steps"][index]["expr"]
                self.assertTrue(self._tamper_num(expr, old, new))
                with self.assertRaises(ProofError) as ctx:
                    verify_proof(proof)
                self.assertEqual(ctx.exception.step_index, index)

    def test_tampered_coefficient_not_silently_accepted(self):
        proof = copy.deepcopy(valid_proof())
        proof["steps"][3]["expr"]["add"][0]["mul"][0]["num"] = "3"  # 2x -> 3x
        with self.assertRaises(ProofError) as ctx:
            verify_proof(proof)
        self.assertEqual(ctx.exception.step_index, 3)
        self.assertEqual(ctx.exception.rule, "diff")


class TestLabelNotTrusted(unittest.TestCase):
    def test_wrong_claim_under_correct_label_rejected(self):
        # 标签写 diff，但声称的结果其实是原式而非导数
        proof = copy.deepcopy(valid_proof())
        proof["steps"][3]["expr"] = proof["steps"][2]["expr"]
        with self.assertRaises(ProofError) as ctx:
            verify_proof(proof)
        self.assertEqual(ctx.exception.step_index, 3)

    def test_correct_value_under_wrong_rule_rejected(self):
        # 数值上等于导数，却标注为 combine（combine 要求与原式相等）
        proof = copy.deepcopy(valid_proof())
        step = proof["steps"][3]
        step["rule"] = "combine"
        step["domain"] = REQUIRED_DOMAIN["combine"]
        with self.assertRaises(ProofError) as ctx:
            verify_proof(proof)
        self.assertEqual(ctx.exception.step_index, 3)


class TestRuleAndDomainRejection(unittest.TestCase):
    def test_unknown_rule_rejected(self):
        proof = copy.deepcopy(valid_proof())
        proof["steps"][1]["rule"] = "simplify"
        with self.assertRaises(ProofError) as ctx:
            verify_proof(proof)
        self.assertEqual(ctx.exception.step_index, 1)
        self.assertIn("未知规则", str(ctx.exception))

    def test_missing_domain_rejected(self):
        proof = copy.deepcopy(valid_proof())
        del proof["steps"][2]["domain"]
        with self.assertRaises(ProofError) as ctx:
            verify_proof(proof)
        self.assertEqual(ctx.exception.step_index, 2)

    def test_wrong_domain_rejected(self):
        proof = copy.deepcopy(valid_proof())
        proof["steps"][3]["domain"] = "R[x]"
        with self.assertRaises(ProofError) as ctx:
            verify_proof(proof)
        self.assertEqual(ctx.exception.step_index, 3)

    def test_combine_requires_canonical_form(self):
        # 数值相等但含未合并同类项 x + x
        proof = copy.deepcopy(valid_proof())
        proof["steps"][2]["expr"] = {"add": [{"pow": [X, 2]},
                                             {"mul": [num("2"), X]},
                                             X,
                                             num("2")]}
        with self.assertRaises(ProofError) as ctx:
            verify_proof(proof)
        self.assertEqual(ctx.exception.step_index, 2)

    def test_expand_requires_expandable_input(self):
        proof = copy.deepcopy(valid_proof())
        # 对已经展开的 combine 结果再 expand，无规则可适用
        proof["steps"].insert(3, {"rule": "expand",
                                  "expr": proof["steps"][2]["expr"],
                                  "domain": REQUIRED_DOMAIN["expand"]})
        with self.assertRaises(ProofError) as ctx:
            verify_proof(proof)
        self.assertEqual(ctx.exception.step_index, 3)

    def test_substitute_requires_rational_value(self):
        proof = copy.deepcopy(valid_proof())
        proof["steps"][4]["value"] = "sqrt(2)"
        with self.assertRaises(ProofError) as ctx:
            verify_proof(proof)
        self.assertEqual(ctx.exception.step_index, 4)

    def test_negative_exponent_rejected(self):
        proof = copy.deepcopy(valid_proof())
        proof["steps"][0]["expr"] = {"pow": [X, -1]}
        with self.assertRaises(ProofError) as ctx:
            verify_proof(proof)
        self.assertEqual(ctx.exception.step_index, 0)


class TestPrimitives(unittest.TestCase):
    def test_derivative(self):
        poly = ast_to_poly({"add": [{"mul": [num("3/4"), {"pow": [X, 3]}]},
                                    {"neg": {"mul": [num("2"), X]}},
                                    num("5")]}, "x")
        self.assertEqual(poly_diff(poly), {2: Fraction(9, 4), 0: Fraction(-2)})

    def test_unknown_variable_rejected(self):
        with self.assertRaises(ProofError):
            ast_to_poly({"var": "y"}, "x")


if __name__ == "__main__":
    unittest.main()
