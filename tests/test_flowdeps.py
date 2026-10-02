import unittest

from flowdeps import (
    BranchDep,
    CycleError,
    Engine,
    FieldDep,
    NegativeDep,
    RuleDep,
    StepDep,
    reference_check,
    replay,
)
from sample import REGISTRY, build_engine


class ImpactQueryTests(unittest.TestCase):
    def setUp(self):
        self.eng = build_engine()
        self.eng.ensure("report")

    def test_direct_and_transitive_with_shortest_chain(self):
        imp = self.eng.impact("user:1", fields={"age"})
        self.assertEqual(imp.direct, ("eligibility",))
        self.assertEqual(imp.transitive, ("report",))
        self.assertEqual(imp.chains["eligibility"], ("user:1", "eligibility"))
        self.assertEqual(imp.chains["report"],
                         ("user:1", "eligibility", "report"))

    def test_impact_of_rule(self):
        imp = self.eng.impact("bonus")
        self.assertEqual(imp.direct, ("eligibility",))
        self.assertEqual(imp.transitive, ("report",))

    def test_impact_via_negative_dependency(self):
        imp = self.eng.impact("user:3")   # not present yet
        self.assertEqual(imp.direct, ("eligibility",))

    def test_dependency_kinds_are_distinguished(self):
        deps = self.eng.dependencies("eligibility")
        kinds = {type(d) for d in deps}
        self.assertIn(FieldDep, kinds)      # actual field reads
        self.assertIn(BranchDep, kinds)     # conditional branches
        self.assertIn(NegativeDep, kinds)   # missed lookup on user:3
        self.assertIn(RuleDep, kinds)       # min_age / bonus
        report_deps = self.eng.dependencies("report")
        self.assertIn(StepDep, {type(d) for d in report_deps})


class FieldPrecisionTests(unittest.TestCase):
    def setUp(self):
        self.eng = build_engine()
        self.eng.ensure("report")

    def test_unconsumed_field_change_affects_nobody(self):
        self.eng.upsert_fact("user:1",
                             {"region": "apac", "age": 34, "tier": "gold"})
        report = self.eng.commit()
        self.assertEqual(report.roots, ())
        self.assertEqual(report.affected, ())
        self.assertEqual(report.revalidated, ())

    def test_branch_gated_read_does_not_invalidate(self):
        # user:2 is a minor; its tier is only read on the adult branch.
        self.eng.upsert_fact("user:2",
                             {"region": "us", "age": 16, "tier": "pro"})
        report = self.eng.commit()
        self.assertEqual(report.affected, ())
        self.assertEqual(report.revalidated, ())

    def test_taken_branch_read_does_invalidate(self):
        # user:1 is an adult; tier IS consumed on that branch.
        self.eng.upsert_fact("user:1",
                             {"region": "eu", "age": 34, "tier": "platinum"})
        report = self.eng.commit()
        # eligibility's output embeds tier; report only prints bonus/factors,
        # so it is re-validated and its downstream is no longer affected.
        self.assertEqual(report.affected, ("eligibility",))
        self.assertEqual(report.revalidated, ("report",))

    def test_output_neutral_change_stops_at_revalidation(self):
        fp_before = self.eng.output_version("report")
        self.eng.upsert_fact("user:1",
                             {"region": "eu", "age": 40, "tier": "gold"})
        report = self.eng.commit()
        self.assertEqual(report.roots, ("eligibility",))
        self.assertEqual(report.revalidated, ("eligibility",))
        self.assertEqual(report.affected, ())
        self.assertEqual(self.eng.output_version("report"), fp_before)


class VersionSemanticsTests(unittest.TestCase):
    def setUp(self):
        self.eng = build_engine()
        self.eng.ensure("report")

    def test_old_version_consumers_do_not_track_new_version(self):
        self.eng.upsert_fact("user:1",
                             {"region": "apac", "age": 34, "tier": "gold"})
        self.eng.commit()
        deps = self.eng.dependencies("eligibility")
        age_dep = next(d for d in deps
                       if isinstance(d, FieldDep) and d.fact == "user:1"
                       and d.field == "age")
        self.assertEqual(age_dep.version, 1)   # still pinned to v1
        self.assertEqual(self.eng.fact_version("user:1"), 2)

    def test_skipped_version_change_still_invalidates(self):
        self.eng.upsert_fact("user:1",
                             {"region": "apac", "age": 34, "tier": "gold"})
        self.eng.commit()                       # v1->v2, age untouched
        self.eng.upsert_fact("user:1",
                             {"region": "apac", "age": 17, "tier": "gold"})
        report = self.eng.commit()              # v2->v3 crosses gate
        self.assertIn("eligibility", report.affected)

    def test_stable_ids_and_versions(self):
        self.assertEqual(self.eng.fact_version("user:1"), 1)
        self.assertEqual(self.eng.rule_version("min_age"), 1)
        self.assertEqual(self.eng.runs["report"].step_version, 1)
        self.eng.set_rule("min_age", 21)
        self.eng.commit()
        self.assertEqual(self.eng.rule_version("min_age"), 2)


class NegativeDependencyTests(unittest.TestCase):
    def setUp(self):
        self.eng = build_engine()
        self.eng.ensure("report")

    def test_adding_missing_input_fires_negative_edge(self):
        self.eng.upsert_fact("user:3",
                             {"region": "eu", "age": 50, "tier": "gold"})
        report = self.eng.commit()
        self.assertEqual(report.roots, ("eligibility",))
        self.assertEqual(report.affected, ("eligibility", "report"))
        self.assertTrue(self.eng.runs["eligibility"].value["waitlist:user:3"])

    def test_unrelated_addition_does_not_fire(self):
        self.eng.upsert_fact("user:9",
                             {"region": "eu", "age": 50, "tier": "gold"})
        report = self.eng.commit()
        self.assertEqual(report.affected, ())


class StepCorrectionTests(unittest.TestCase):
    def setUp(self):
        self.eng = build_engine()
        self.eng.ensure("report")

    def test_correction_retracts_old_edges(self):
        from sample import pricing_v2
        self.eng.define_step("pricing", 2, pricing_v2)
        report = self.eng.commit()
        self.assertEqual(report.roots, ("pricing",))
        self.assertEqual(self.eng.runs["pricing"].step_version, 2)
        deps = self.eng.dependencies("pricing")
        self.assertFalse(any(isinstance(d, FieldDep) and d.fact == "rate:us"
                             for d in deps))
        # the retracted edge no longer invalidates
        self.eng.upsert_fact("rate:us", {"factor": 9.9})
        report = self.eng.commit()
        self.assertEqual(report.affected, ())

    def test_correction_with_same_output_is_revalidated(self):
        from sample import pricing_v1
        eng = self.eng
        eng.define_step("pricing", 2, pricing_v1)   # same logic, new version
        report = eng.commit()
        self.assertEqual(report.revalidated, ("pricing",))
        self.assertEqual(report.affected, ())


class CycleTests(unittest.TestCase):
    def test_cycle_is_diagnosed(self):
        eng = build_engine()
        eng.define_step("a", 1, lambda r: r.step("b"))
        eng.define_step("b", 1, lambda r: r.step("a"))
        with self.assertRaises(CycleError) as ctx:
            eng.ensure("a")
        chain = ctx.exception.chain
        self.assertEqual(chain[0], chain[-1])
        self.assertEqual(set(chain), {"a", "b"})
        self.assertIn("cyclic processing chain", str(ctx.exception))

    def test_invalidation_is_never_self_supporting(self):
        eng = build_engine()
        eng.ensure("report")
        # A change that leaves every output unchanged must not cascade.
        eng.upsert_fact("user:1", {"region": "eu", "age": 41, "tier": "gold"})
        report = eng.commit()
        self.assertEqual(report.affected, ())
        # Every explanation chain is rooted in a real change label.
        eng.upsert_fact("user:2", {"region": "us", "age": 21, "tier": "free"})
        report = eng.commit()
        for chain in report.explanations.values():
            self.assertTrue(chain[0].startswith(
                ("update ", "add ", "remove ", "rule ", "step ")))


class BatchTests(unittest.TestCase):
    def setUp(self):
        self.eng = build_engine()
        self.eng.ensure("report")

    def test_batch_minimal_explanations_and_revalidated(self):
        self.eng.upsert_fact("rate:eu", {"factor": 0.8})
        self.eng.set_rule("bonus", 150)
        report = self.eng.commit()
        self.assertEqual(set(report.roots), {"eligibility", "pricing"})
        self.assertEqual(report.affected, ("eligibility", "pricing", "report"))
        self.assertEqual(report.direct, ("eligibility", "pricing"))
        self.assertEqual(report.transitive, ("report",))
        chain = report.explanations["report"]
        self.assertEqual(len(chain), 3)          # shortest: change->step->report
        self.assertEqual(chain[-1], "report")
        self.assertIn(chain[1], {"eligibility", "pricing"})

    def test_batch_with_revalidated_node_reported(self):
        self.eng.upsert_fact("user:1",
                             {"region": "eu", "age": 40, "tier": "gold"})
        self.eng.upsert_fact("rate:eu", {"factor": 0.8})
        report = self.eng.commit()
        self.assertEqual(report.revalidated, ("eligibility",))
        self.assertEqual(report.affected, ("pricing", "report"))
        # report's explanation goes through pricing, not the revalidated node
        self.assertEqual(report.explanations["report"][1], "pricing")

    def test_remove_fact_invalidates(self):
        self.eng.remove_fact("rate:us")
        report = self.eng.commit()
        self.assertIn("pricing", report.affected)


class ReferenceAndEvidenceTests(unittest.TestCase):
    def _exercised_engine(self):
        from sample import pricing_v2
        eng = build_engine()
        eng.ensure("report")
        eng.upsert_fact("user:2", {"region": "us", "age": 21, "tier": "pro"})
        eng.upsert_fact("user:3", {"region": "eu", "age": 50, "tier": "gold"})
        eng.set_rule("bonus", 120)
        eng.commit()
        eng.define_step("pricing", 2, pricing_v2)
        eng.commit()
        eng.ensure("report")
        return eng

    def test_reference_check_finds_no_missed_invalidation(self):
        eng = self._exercised_engine()
        ref = reference_check(eng)
        self.assertTrue(ref.ok)
        self.assertEqual(ref.missed_invalidations, ())
        self.assertEqual(ref.irrelevant_branch_accesses, 0)
        self.assertEqual(ref.missing_dependencies, 0)

    def test_reference_check_catches_stale_edges(self):
        eng = self._exercised_engine()
        # simulate a bug: an edge that clean re-execution proves irrelevant
        dirty = set(eng.dependencies("pricing"))
        dirty.add(FieldDep("ghost", 1, "x"))
        eng.graph.install("pricing", dirty)
        ref = reference_check(eng)
        self.assertFalse(ref.ok)
        self.assertEqual(ref.irrelevant_branch_accesses, 1)

    def test_evidence_is_replayable(self):
        eng = self._exercised_engine()
        rep = replay(eng.log, REGISTRY)
        self.assertTrue(rep.ok)
        self.assertEqual(rep.first_divergence, None)
        self.assertGreater(rep.total_entries, 0)


if __name__ == "__main__":
    unittest.main()
