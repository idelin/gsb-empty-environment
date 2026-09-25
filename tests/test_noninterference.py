"""Finite-domain pairwise noninterference: naive leaks, fixed does not."""
import json
import unittest

from secexplain.engine import Engine
from secexplain.model import PUBLIC
from secexplain.noninterference import (low_equivalent, pairwise_check,
                                        public_output)
from secexplain.projection import project, project_trace
from secexplain.sampledata import sample_domain


class DomainTest(unittest.TestCase):
    def setUp(self):
        self.domain = sample_domain()

    def test_domain_worlds_are_pairwise_low_equivalent(self):
        for i, a in enumerate(self.domain):
            for b in self.domain[i + 1:]:
                self.assertTrue(low_equivalent(a, b, PUBLIC),
                                f"{a.name} vs {b.name}")


class NaiveLeakTest(unittest.TestCase):
    """Counterexample: naive field deletion still leaks."""

    def setUp(self):
        self.domain = sample_domain()

    def test_naive_projection_violates_noninterference(self):
        violations = pairwise_check(self.domain, "approved", PUBLIC,
                                    mode="naive")
        self.assertTrue(violations, "naive projection should leak")

    def test_naive_leak_witness_is_a_count_or_depth_difference(self):
        violations = pairwise_check(self.domain, "approved", PUBLIC,
                                    mode="naive")
        witness = violations[0]
        out_a = json.loads(witness.output_a[len("OK:"):])
        out_b = json.loads(witness.output_b[len("OK:"):])
        self.assertNotEqual(out_a["summary"], out_b["summary"])
        leaked_keys = [k for k in out_a["summary"]
                       if out_a["summary"][k] != out_b["summary"][k]]
        self.assertTrue(leaked_keys)
        self.assertTrue(any("hidden" in k or "total" in k
                            for k in leaked_keys))


class FixedProjectionTest(unittest.TestCase):
    def setUp(self):
        self.domain = sample_domain()

    def test_fixed_projection_is_noninterfering(self):
        violations = pairwise_check(self.domain, "approved", PUBLIC,
                                    mode="fixed")
        self.assertEqual(violations, [])

    def test_public_errors_are_identical_across_worlds(self):
        # "note_0" exists (hidden) in some worlds and not at all in
        # others; "no_such_key" exists nowhere.  Both must yield the
        # same public error everywhere.
        for query in ("note_0", "no_such_key"):
            violations = pairwise_check(self.domain, query, PUBLIC,
                                        mode="fixed")
            self.assertEqual(violations, [])
            output = public_output(self.domain[0], query, PUBLIC)
            self.assertTrue(output.startswith("ERROR:"))

    def test_structural_counts_are_identical_across_worlds(self):
        summaries = set()
        for world in self.domain:
            derivation = Engine(world.rules).derive(world.facts)
            expl = project(derivation, "approved", PUBLIC)
            summaries.add(json.dumps(expl.summary, sort_keys=True))
        self.assertEqual(len(summaries), 1)

    def test_projected_debug_traces_are_identical_across_worlds(self):
        traces = set()
        for world in self.domain:
            derivation = Engine(world.rules).derive(world.facts)
            traces.add(json.dumps(project_trace(derivation, PUBLIC),
                                  sort_keys=True))
        self.assertEqual(len(traces), 1)


if __name__ == "__main__":
    unittest.main()
