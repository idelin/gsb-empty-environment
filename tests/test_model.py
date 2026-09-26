import unittest
from fractions import Fraction

from maintenance.model import Group, Instance, Job, Shift, to_rational, validate


def base_instance(**overrides):
    data = dict(
        shifts=(Shift("s1", 2),),
        groups=(Group("g1", 1),),
        jobs=(Job("j1", "g1", 1, 1, 0),),
    )
    data.update(overrides)
    return Instance(**data)


class TestValidation(unittest.TestCase):
    def test_valid_instance_passes(self):
        validate(base_instance())

    def test_empty_shifts_rejected(self):
        with self.assertRaises(ValueError):
            validate(base_instance(shifts=()))

    def test_zero_shift_length_rejected(self):
        with self.assertRaises(ValueError):
            validate(base_instance(shifts=(Shift("s1", 0),)))

    def test_zero_group_limit_rejected(self):
        with self.assertRaises(ValueError):
            validate(base_instance(groups=(Group("g1", 0),)))

    def test_unknown_group_rejected(self):
        with self.assertRaises(ValueError):
            validate(base_instance(jobs=(Job("j1", "ghost", 1, 1, 0),)))

    def test_duplicate_job_id_rejected(self):
        jobs = (Job("j1", "g1", 1, 1, 0), Job("j1", "g1", 1, 1, 0))
        with self.assertRaises(ValueError):
            validate(base_instance(jobs=jobs))

    def test_negative_score_rejected(self):
        with self.assertRaises(ValueError):
            validate(base_instance(jobs=(Job("j1", "g1", 1, -1, 0),)))

    def test_negative_startup_rejected(self):
        with self.assertRaises(ValueError):
            validate(base_instance(jobs=(Job("j1", "g1", 1, 1, -1),)))

    def test_zero_duration_rejected(self):
        with self.assertRaises(ValueError):
            validate(base_instance(jobs=(Job("j1", "g1", 0, 1, 0),)))

    def test_rational_coercion(self):
        job = Job("j1", "g1", 1, 2, 1)
        self.assertEqual(job.score, Fraction(2))
        self.assertIsInstance(job.startup, Fraction)
        self.assertEqual(to_rational(Fraction(1, 3)), Fraction(1, 3))
        with self.assertRaises(TypeError):
            to_rational(0.5)  # 浮点不在合成整数/有理数约定内


if __name__ == "__main__":
    unittest.main()
