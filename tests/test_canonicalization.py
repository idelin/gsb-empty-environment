import unittest

from dp_synth import (
    Condition,
    CountQuery,
    HistogramQuery,
    Predicate,
    SumQuery,
    canonical_query_id,
)
from dp_synth.synthetic_data import canonical_fingerprint, make_synthetic_employee_data


class CanonicalizationTests(unittest.TestCase):
    def test_predicate_condition_order_irrelevant(self):
        a = Predicate((Condition("city", "==", "Beijing"), Condition("active", "==", True)))
        b = Predicate((Condition("active", "==", True), Condition("city", "==", "Beijing")))
        self.assertEqual(a.canonical(), b.canonical())
        self.assertEqual(canonical_query_id(CountQuery(a, 1.0)), canonical_query_id(CountQuery(b, 1.0)))

    def test_in_set_order_irrelevant(self):
        a = Predicate((Condition("city", "in", ["Beijing", "Shanghai"]),))
        b = Predicate((Condition("city", "in", ["Shanghai", "Beijing", "Beijing"]),))
        self.assertEqual(canonical_query_id(CountQuery(a, 1.0)), canonical_query_id(CountQuery(b, 1.0)))

    def test_record_key_order_irrelevant(self):
        fp1 = canonical_fingerprint([{"a": 1, "b": 2}])
        fp2 = canonical_fingerprint([{"b": 2, "a": 1}])
        self.assertEqual(fp1, fp2)

    def test_threshold_change_changes_id(self):
        base = CountQuery(epsilon=1.0)
        with_t = CountQuery(epsilon=1.0, threshold=5.0)
        with_t2 = CountQuery(epsilon=1.0, threshold=6.0)
        self.assertNotEqual(canonical_query_id(base), canonical_query_id(with_t))
        self.assertNotEqual(canonical_query_id(with_t), canonical_query_id(with_t2))

    def test_epsilon_change_changes_id(self):
        self.assertNotEqual(
            canonical_query_id(CountQuery(epsilon=1.0)),
            canonical_query_id(CountQuery(epsilon=0.5)),
        )

    def test_kind_field_bounds_change_id(self):
        p = Predicate((Condition("city", "==", "Beijing"),))
        self.assertNotEqual(
            canonical_query_id(CountQuery(p, 1.0)),
            canonical_query_id(HistogramQuery("city", Predicate(), 1.0)),
        )
        self.assertNotEqual(
            canonical_query_id(SumQuery("salary", 0, 60000, p, 1.0)),
            canonical_query_id(SumQuery("salary", 0, 50000, p, 1.0)),
        )

    def test_data_versions_differ_when_data_changes(self):
        d1 = make_synthetic_employee_data(n=100, seed=1)
        d2 = make_synthetic_employee_data(n=101, seed=1)
        d3 = make_synthetic_employee_data(n=100, seed=2)
        self.assertNotEqual(d1.version, d2.version)
        self.assertNotEqual(d1.version, d3.version)
        self.assertEqual(d1.version, make_synthetic_employee_data(n=100, seed=1).version)


if __name__ == "__main__":
    unittest.main()
