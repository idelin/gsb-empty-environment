import json
import tempfile
import unittest
from pathlib import Path

from entity_kg import (
    ConflictError,
    EntityRepository,
    FactKind,
    make_fact_id,
    make_step_id,
)
from entity_kg.rules import all_rules, rule


def fixed_clock():
    ts = {"n": 0}

    def next_ts() -> str:
        ts["n"] += 1
        return f"2026-01-01T00:00:0{ts['n']}+00:00"

    return next_ts


class EntityResolutionTests(unittest.TestCase):
    def setUp(self):
        self.repo = EntityRepository(clock=fixed_clock())

    def test_rules_are_versioned(self):
        ids = {r.rid: r for r in all_rules()}
        self.assertIn("R-EQ-TRANSITIVE", ids)
        self.assertIn("R-NEQ-CYCLE", ids)
        self.assertEqual(ids["R-REP-MIN"].version, 1)

    def test_explicit_eq_merges_with_min_representative(self):
        self.repo.assert_eq("n3", "n1", reason="r1")
        self.repo.assert_eq("n1", "n2", reason="r2")
        state = self.repo.state
        self.assertEqual(state.representatives["n3"], "n1")
        self.assertEqual(state.representatives["n2"], "n1")
        members = {e.representative: e.members
                   for e in state.entities}["n1"]
        self.assertEqual(members, ["n1", "n2", "n3"])

    def test_representative_order_independent(self):
        a = EntityRepository(clock=fixed_clock())
        b = EntityRepository(clock=fixed_clock())
        a.assert_eq("alpha", "beta", reason="x")
        a.assert_eq("beta", "gamma", reason="y")
        b.assert_eq("gamma", "beta", reason="y")
        b.assert_eq("beta", "alpha", reason="x")
        self.assertEqual(a.state.representatives, b.state.representatives)
        self.assertEqual(
            [s.sid for s in a.state.steps],
            [s.sid for s in b.state.steps])

    def test_transitive_step_has_explicit_chain_and_stable_id(self):
        f1 = make_fact_id(FactKind.EQ, "a", "b", "r1")
        f2 = make_fact_id(FactKind.EQ, "b", "c", "r2")
        self.repo.assert_eq("a", "b", reason="r1")
        self.repo.assert_eq("b", "c", reason="r2")
        step, = self.repo.state.steps
        expected = make_step_id("R-EQ-TRANSITIVE", [f1, f2], "a", "c")
        self.assertEqual(step.sid, expected)
        self.assertEqual(step.rule_id, "R-EQ-TRANSITIVE")
        self.assertEqual(step.version, 1)
        self.assertEqual([p.fact_id for p in step.chain], [f1, f2])
        self.assertTrue(all(p.step_id is None for p in step.chain))

    def test_conflict_cycle_for_a_eq_b_eq_c_but_a_neq_c(self):
        self.repo.assert_eq("a", "b", reason="r1")
        self.repo.assert_eq("b", "c", reason="r2")
        result = self.repo.assert_neq("a", "c", reason="r3")
        self.assertFalse(self.repo.is_consistent())
        conflict, = self.repo.state.conflicts
        self.assertEqual(result.conflict_ids, [conflict.cid])
        self.assertEqual(conflict.kind, "inequality_cycle")
        self.assertEqual(conflict.rule_id, "R-NEQ-CYCLE")
        self.assertEqual(conflict.version, 1)
        kinds = [(e.a, e.b, e.kind) for e in conflict.cycle]
        self.assertEqual(len(kinds), 3)
        facts_in_cycle = {e.fact_id for e in conflict.cycle}
        f1 = make_fact_id(FactKind.EQ, "a", "b", "r1")
        f2 = make_fact_id(FactKind.EQ, "b", "c", "r2")
        f3 = make_fact_id(FactKind.NEQ, "a", "c", "r3")
        self.assertEqual(facts_in_cycle, {f1, f2, f3})
        eq_steps = {e.step_id for e in conflict.cycle
                    if e.kind is FactKind.EQ}
        self.assertEqual(eq_steps, {self.repo.state.steps[0].sid})

    def test_retracting_eq_edge_splits_entity(self):
        self.repo.assert_eq("a", "b", reason="r1")
        self.repo.assert_eq("b", "c", reason="r2")
        self.repo.assert_neq("a", "c", reason="r3")
        self.assertEqual(len(self.repo.state.conflicts), 1)
        self.repo.retract("b", "c", FactKind.EQ, reason="wrong evidence")
        state = self.repo.state
        self.assertEqual(state.representatives["a"], "a")
        self.assertEqual(state.representatives["b"], "a")
        self.assertEqual(state.representatives["c"], "c")
        self.assertEqual(state.conflicts, [])
        self.assertEqual(state.steps, [])
        fact = self.repo.fact("b", "c", FactKind.EQ)
        self.assertIsNone(fact)
        stored = [f for f in self.repo.facts.values()
                  if f.a == "b" and f.b == "c"][0]
        self.assertFalse(stored.active)
        self.assertEqual(stored.version, 2)
        self.assertIsNotNone(stored.retracted_seq)

    def test_retract_missing_edge_raises(self):
        self.repo.assert_eq("a", "b", reason="r1")
        with self.assertRaises(KeyError):
            self.repo.retract("a", "b", FactKind.NEQ, reason="x")

    def test_retract_by_fact_id(self):
        r = self.repo.assert_eq("a", "b", reason="r1")
        self.repo.retract_fact(r.fact_id, reason="bad")
        self.assertEqual(self.repo.state.entities[0].members, ["a"])

    def test_same_name_never_merges(self):
        self.repo.add_name("x", "王芳")
        self.repo.add_name("y", "王芳")
        self.assertTrue(self.repo.is_consistent())
        self.assertNotEqual(
            self.repo.representative("x"),
            self.repo.representative("y"))
        ents = sorted(e.representative for e in self.repo.state.entities)
        self.assertEqual(ents, ["x", "y"])

    def test_neq_across_components_is_consistent(self):
        self.repo.assert_neq("a", "b", reason="different people")
        self.assertTrue(self.repo.is_consistent())
        self.assertEqual(self.repo.state.conflicts, [])

    def test_direct_eq_neq_contradiction(self):
        self.repo.assert_eq("a", "b", reason="r1")
        self.repo.assert_neq("a", "b", reason="r2")
        conflict, = self.repo.state.conflicts
        self.assertEqual(conflict.kind, "direct_contradiction")
        self.assertEqual(conflict.rule_id, "R-EQ-NEQ-DIRECT")

    def test_reflexive_neq_conflict(self):
        self.repo.assert_neq("a", "a", reason="bad")
        conflict, = self.repo.state.conflicts
        self.assertEqual(conflict.kind, "reflexivity")
        self.assertEqual(conflict.rule_id, "R-NEQ-REFLEXIVE")

    def test_fact_identity_is_content_addressed_and_symmetric(self):
        id1 = make_fact_id(FactKind.EQ, "a", "b", "why")
        id2 = make_fact_id(FactKind.EQ, "b", "a", "why")
        self.assertEqual(id1, id2)
        id3 = make_fact_id(FactKind.EQ, "a", "b", "different")
        self.assertNotEqual(id1, id3)

    def test_idempotent_assert_does_not_duplicate(self):
        first = self.repo.assert_eq("a", "b", reason="r")
        second = self.repo.assert_eq("b", "a", reason="r")
        self.assertEqual(first.fact_id, second.fact_id)
        eq_facts = [f for f in self.repo.facts.values()
                    if f.kind is FactKind.EQ]
        self.assertEqual(len(eq_facts), 1)
        self.assertEqual(len(self.repo.events), 1)

    def test_re_assert_after_retract_is_new_version_chain(self):
        self.repo.assert_eq("a", "b", reason="r1")
        self.repo.retract("a", "b", FactKind.EQ, reason="bad")
        self.repo.assert_eq("a", "b", reason="r2")
        facts = sorted(self.repo.facts.values(), key=lambda f: f.added_seq)
        self.assertEqual(len(facts), 2)
        self.assertFalse(facts[0].active)
        self.assertTrue(facts[1].active)
        self.assertEqual(facts[0].version, 2)
        self.assertEqual(facts[1].version, 1)

    def test_strict_mode_raises_conflict_error(self):
        strict = EntityRepository(strict=True, clock=fixed_clock())
        strict.assert_eq("a", "b", reason="r1")
        strict.assert_eq("b", "c", reason="r2")
        with self.assertRaises(ConflictError) as ctx:
            strict.assert_neq("a", "c", reason="r3")
        self.assertEqual(ctx.exception.conflicts[0]["kind"],
                         "inequality_cycle")

    def test_four_node_chain_retract_middle(self):
        self.repo.assert_eq("a", "b", reason="r1")
        self.repo.assert_eq("b", "c", reason="r2")
        self.repo.assert_eq("c", "d", reason="r3")
        # only non-direct pairs derive steps: (a,c),(a,d),(b,d)
        self.assertEqual(len(self.repo.state.steps), 3)
        self.repo.retract("b", "c", FactKind.EQ, reason="x")
        comps = sorted(sorted(e.members) for e in self.repo.state.entities)
        self.assertEqual(comps, [["a", "b"], ["c", "d"]])
        self.assertEqual(len(self.repo.state.steps), 0)

    def test_invalid_node_ids_rejected(self):
        with self.assertRaises(ValueError):
            self.repo.assert_eq("", "b", reason="r")
        with self.assertRaises(ValueError):
            self.repo.assert_eq(" a", "b", reason="r")
        with self.assertRaises(ValueError):
            self.repo.assert_eq("a", "b", reason="")


class PersistenceTests(unittest.TestCase):
    def test_replay_produces_identical_state_and_versions(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "events.jsonl"
            repo = EntityRepository(path, clock=fixed_clock())
            repo.add_name("a", "Alice")
            repo.assert_eq("a", "b", reason="r1")
            repo.assert_eq("b", "c", reason="r2")
            repo.assert_neq("a", "c", reason="r3")
            repo.retract("b", "c", FactKind.EQ, reason="r4")
            expected_state = json.dumps(
                repo.state.to_dict(), sort_keys=True)
            expected_facts = {k: v.to_dict()
                              for k, v in repo.facts.items()}

            replayed = EntityRepository.replay(path, clock=fixed_clock())
            self.assertEqual(
                json.dumps(replayed.state.to_dict(), sort_keys=True),
                expected_state)
            self.assertEqual(
                {k: v.to_dict() for k, v in replayed.facts.items()},
                expected_facts)
            self.assertEqual(
                {k: v.version for k, v in replayed.facts.items()},
                {k: v.version for k, v in repo.facts.items()})

    def test_replay_byte_stable_step_and_conflict_ids(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "log.jsonl"
            repo = EntityRepository(path, clock=fixed_clock())
            repo.assert_eq("a", "b", reason="r1")
            repo.assert_eq("b", "c", reason="r2")
            repo.assert_neq("a", "c", reason="r3")
            ids_before = ([s.sid for s in repo.state.steps]
                          + [c.cid for c in repo.state.conflicts])
            replayed = EntityRepository.replay(path)
            ids_after = ([s.sid for s in replayed.state.steps]
                         + [c.cid for c in replayed.state.conflicts])
            self.assertEqual(ids_before, ids_after)

    def test_log_is_append_only_jsonl(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "log.jsonl"
            repo = EntityRepository(path, clock=fixed_clock())
            repo.assert_eq("a", "b", reason="r1")
            repo.retract("a", "b", FactKind.EQ, reason="r2")
            lines = path.read_text(encoding="utf-8").strip().splitlines()
            self.assertEqual(len(lines), 2)
            kinds = [json.loads(line)["kind"] for line in lines]
            self.assertEqual(kinds, ["ASSERT", "RETRACT"])

    def test_state_version_tracks_event_count(self):
        self_ = EntityRepository(clock=fixed_clock())
        self.assertEqual(self_.state.version, 0)
        self_.assert_eq("a", "b", reason="r1")
        self.assertEqual(self_.state.version, 1)
        self_.assert_neq("c", "d", reason="r2")
        self.assertEqual(self_.state.version, 2)

    def test_snapshot_round_trip_contains_all_registries(self):
        repo = EntityRepository(clock=fixed_clock())
        repo.assert_eq("a", "b", reason="r1")
        snap = repo.snapshot()
        self.assertEqual(snap["schema_version"], 1)
        self.assertTrue(snap["rules"])
        self.assertIn("derived", snap)
        self.assertIn("facts", snap)
        for rule_entry in snap["rules"]:
            self.assertIn("version", rule_entry)


if __name__ == "__main__":
    unittest.main(verbosity=2)
