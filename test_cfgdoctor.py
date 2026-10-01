"""python3 -m unittest test_cfgdoctor -v"""
import itertools
import unittest
from dataclasses import replace

from cfgdoctor import (DerivationError, Grammar, IncrementalAnalyzer, analyze,
                       certificate_to_derivation, compute_generating,
                       compute_nullable, compute_reachable, cross_check,
                       enumerate_terminal_strings, execute_certificate_witness,
                       find_epsilon_cycles, independent_reachable, run_derivation,
                       shortest_problem_chains, simplify, sub_grammar,
                       verify_cycle_witness, verify_generating_certificate)
from cfgdoctor.cycles import analyze_language_infiniteness


def dead_cycle_grammar():
    return Grammar({"a"}, {"S", "A", "B"}, "S", [
        ("S", ("a",)), ("S", ("A",)), ("A", ("B",)), ("B", ("A",))])


def nullable_cycle_grammar():
    return Grammar({"a"}, {"S", "A", "B"}, "S", [
        ("S", ("A",)), ("A", ("B",)), ("A", ("a",)), ("B", ("A",)), ("B", ())])


def unreachable_grammar():
    return Grammar({"a", "b"}, {"S", "X", "Y"}, "S", [
        ("S", ("a",)), ("X", ("Y",)), ("Y", ("X",)), ("Y", ("b",))])


def empty_language_grammar():
    return Grammar(set(), {"S", "A", "B"}, "S", [
        ("S", ("A",)), ("A", ("B",)), ("B", ("A",))])


def infinite_language_grammar():
    return Grammar({"a", "b"}, {"S"}, "S", [("S", ("a", "S")), ("S", ("b",))])


def demo_grammar():
    return Grammar({"a", "b", "c"}, {"S", "A", "B"}, "S", [
        ("S", ("A", "B")), ("S", ("c",)), ("A", ("a",)),
        ("A", ("B",)), ("B", ("b",)), ("B", ("A",))])


class ValidationTest(unittest.TestCase):
    def test_undeclared_symbol_rejected(self):
        with self.assertRaises(ValueError):
            Grammar({"a"}, {"S"}, "S", [("S", ("Z",))])

    def test_alphabet_overlap_rejected(self):
        with self.assertRaises(ValueError):
            Grammar({"a"}, {"a", "S"}, "S", [("S", ("a",))])

    def test_bad_start_rejected(self):
        with self.assertRaises(ValueError):
            Grammar({"a"}, {"S"}, "T", [("S", ("a",))])


class GeneratingCertificateTest(unittest.TestCase):
    def test_certificate_passes_independent_verification(self):
        for g in (dead_cycle_grammar(), nullable_cycle_grammar(),
                  unreachable_grammar(), empty_language_grammar(),
                  infinite_language_grammar(), demo_grammar()):
            cert = compute_generating(g)
            self.assertEqual(verify_generating_certificate(g, cert), [])

    def test_tampered_round_rejected(self):
        g = demo_grammar()
        cert = compute_generating(g)
        bad = replace(cert, rounds={**cert.rounds, "S": 7})
        self.assertTrue(verify_generating_certificate(g, bad))

    def test_tampered_partition_rejected(self):
        g = dead_cycle_grammar()
        cert = compute_generating(g)
        bad = replace(cert,
                      generating=frozenset({"S", "A"}),
                      non_generating=frozenset({"B"}))
        self.assertTrue(verify_generating_certificate(g, bad))

    def test_witness_executes_independently(self):
        g = demo_grammar()
        cert = compute_generating(g)
        for a in cert.generating:
            self.assertEqual(execute_certificate_witness(g, cert, a),
                             cert.witness_string[a])

    def test_certificate_derivation_steps_valid(self):
        g = demo_grammar()
        cert = compute_generating(g)
        steps = certificate_to_derivation(g, cert, "S")
        self.assertEqual(run_derivation(g, "S", steps), ("c",))


class DeadCycleTest(unittest.TestCase):
    def test_only_cycle_members_non_generating(self):
        g = dead_cycle_grammar()
        cert = compute_generating(g)
        self.assertEqual(cert.generating, frozenset({"S"}))
        self.assertEqual(cert.non_generating, frozenset({"A", "B"}))

    def test_cycle_classified_dead(self):
        g = dead_cycle_grammar()
        cert = compute_generating(g)
        nullable = compute_nullable(g)
        reach = compute_reachable(g)
        cycles = find_epsilon_cycles(g, cert, nullable, reach)
        self.assertEqual(len(cycles), 1)
        self.assertEqual(cycles[0].kind, "dead")
        self.assertEqual(cycles[0].members, frozenset({"A", "B"}))

    def test_dead_cycle_witness_still_executes(self):
        g = dead_cycle_grammar()
        cert = compute_generating(g)
        nullable = compute_nullable(g)
        reach = compute_reachable(g)
        (cycle,) = find_epsilon_cycles(g, cert, nullable, reach)
        start = cycle.path[0][0]
        self.assertTrue(
            verify_cycle_witness(g, start, cycle.regeneration_steps))

    def test_shortest_chain_points_into_cycle(self):
        g = empty_language_grammar()
        cert = compute_generating(g)
        reach = compute_reachable(g)
        chains = shortest_problem_chains(g, cert, reach)
        self.assertEqual(chains["S"].chain, (("S", 0),))
        self.assertIn("死循环", chains["S"].core)
        self.assertEqual(chains["A"].chain, ())


class NullableCycleTest(unittest.TestCase):
    def setUp(self):
        self.g = nullable_cycle_grammar()
        self.cert = compute_generating(self.g)
        self.nullable = compute_nullable(self.g)
        self.reach = compute_reachable(self.g)
        (self.cycle,) = find_epsilon_cycles(
            self.g, self.cert, self.nullable, self.reach)

    def test_all_symbols_generating(self):
        self.assertEqual(self.cert.non_generating, frozenset())

    def test_cycle_is_nullable_with_exit(self):
        self.assertEqual(self.cycle.kind, "nullable-exit")
        self.assertTrue(self.cycle.nullable)
        self.assertIn(self.cycle.ambiguous_string, {("a",), ()})

    def test_cycle_witness_regenerates(self):
        start = self.cycle.path[0][0]
        self.assertTrue(
            verify_cycle_witness(self.g, start, self.cycle.regeneration_steps))

    def test_simplification_keeps_whole_cycle(self):
        result = simplify(self.g, self.cert, self.reach)
        self.assertFalse(result.empty_language)
        self.assertTrue(all(e.action == "keep" for e in result.entries))
        self.assertEqual(len(result.grammar.productions), 5)

    def test_finite_language_despite_infinite_ambiguity(self):
        inf = analyze_language_infiniteness(
            self.g, self.cert, self.nullable, self.reach)
        self.assertFalse(inf.language_infinite)
        self.assertTrue(self.cycle.generating)
        self.assertEqual(
            enumerate_terminal_strings(self.g, "S", 8),
            {("a",), ()})


class UnreachableTest(unittest.TestCase):
    def test_unreachable_set(self):
        g = unreachable_grammar()
        self.assertEqual(compute_reachable(g), frozenset({"S"}))
        self.assertEqual(independent_reachable(g), frozenset({"S"}))

    def test_cleanup_drops_unreachable_rules(self):
        g = unreachable_grammar()
        result = simplify(g)
        dropped = {e.index for e in result.entries if e.action == "drop"}
        self.assertEqual(dropped, {1, 2, 3})
        self.assertEqual(len(result.grammar.productions), 1)

    def test_unreachable_chain_explains_referencers(self):
        g = unreachable_grammar()
        chains = shortest_problem_chains(
            g, compute_generating(g), compute_reachable(g))
        self.assertEqual(chains["X"].kind, "unreachable")
        self.assertIn("Y", chains["X"].core)


class EmptyLanguageTest(unittest.TestCase):
    def test_start_useless_returns_empty_language(self):
        g = empty_language_grammar()
        result = simplify(g)
        self.assertTrue(result.empty_language)
        self.assertIsNone(result.grammar)
        self.assertIn("∅", result.explanation)
        self.assertTrue(all(v is None for v in result.mapping.values()))


class InfiniteVsAmbiguousTest(unittest.TestCase):
    def test_infinite_language_without_epsilon_cycle(self):
        g = infinite_language_grammar()
        cert = compute_generating(g)
        nullable = compute_nullable(g)
        reach = compute_reachable(g)
        cycles = find_epsilon_cycles(g, cert, nullable, reach)
        self.assertEqual(cycles, [])
        inf = analyze_language_infiniteness(g, cert, nullable, reach)
        self.assertTrue(inf.language_infinite)
        self.assertEqual(inf.pumping_symbol, "S")

    def test_infinite_ambiguity_without_infinite_language(self):
        g = nullable_cycle_grammar()
        rep = analyze(g)
        self.assertFalse(rep.infiniteness.language_infinite)
        self.assertTrue(any(c.generating for c in rep.cycles))


class CrossCheckTest(unittest.TestCase):
    def test_cross_check_ok_on_all_samples(self):
        for g in (dead_cycle_grammar(), nullable_cycle_grammar(),
                  unreachable_grammar(), empty_language_grammar(),
                  infinite_language_grammar(), demo_grammar()):
            cert = compute_generating(g)
            report = cross_check(g, cert, compute_reachable(g))
            self.assertTrue(report.ok, report)

    def test_enum_absence_is_not_proof(self):
        nts = {"S"} | {f"A{k}" for k in range(1, 9)}
        prods = ([("S", ("A1",))]
                 + [(f"A{k}", (f"A{k+1}",)) for k in range(1, 8)]
                 + [("A8", ("a",))])
        g = Grammar({"a"}, nts, "S", prods)
        self.assertEqual(enumerate_terminal_strings(g, "S", 3), set())
        cert = compute_generating(g)
        self.assertIn("S", cert.generating)
        report = cross_check(g, cert, compute_reachable(g))
        self.assertTrue(report.witness_confirmed["S"])
        self.assertTrue(any("不构成证明" in n for n in report.notes))


class IndependentExecutionTest(unittest.TestCase):
    def test_run_derivation_rejects_bad_position(self):
        g = demo_grammar()
        with self.assertRaises(DerivationError):
            run_derivation(g, "S", [(0, 3)])

    def test_run_derivation_rejects_lhs_mismatch(self):
        g = demo_grammar()
        with self.assertRaises(DerivationError):
            run_derivation(g, "S", [(2, 0)])

    def test_cycle_witness_rejects_empty(self):
        g = nullable_cycle_grammar()
        self.assertFalse(verify_cycle_witness(g, "A", ()))


class SimplificationEquivalenceTest(unittest.TestCase):
    def test_bounded_language_preserved(self):
        for g in (dead_cycle_grammar(), nullable_cycle_grammar(),
                  unreachable_grammar(), infinite_language_grammar(),
                  demo_grammar()):
            result = simplify(g)
            self.assertFalse(result.empty_language)
            before = enumerate_terminal_strings(g, g.start, 6)
            after = enumerate_terminal_strings(
                result.grammar, result.grammar.start, 6)
            self.assertEqual(before, after)

    def test_mapping_consistent_with_entries(self):
        g = demo_grammar()
        result = simplify(g)
        for e in result.entries:
            if e.action == "keep":
                self.assertIsNotNone(result.mapping[e.index])
            else:
                self.assertIsNone(result.mapping[e.index])


class IncrementalTest(unittest.TestCase):
    def test_scripted_cascade(self):
        g = demo_grammar()
        inc = IncrementalAnalyzer(g)
        cs = inc.retract(2)
        self.assertEqual(cs.lost_generating, frozenset())
        cs = inc.retract(4)
        self.assertEqual(cs.lost_generating, frozenset({"A", "B"}))
        self.assertFalse(cs.language_emptied)
        cs = inc.retract(1)
        self.assertEqual(cs.lost_generating, frozenset({"S"}))
        self.assertTrue(cs.language_emptied)

    def test_reachability_cascade(self):
        g = Grammar({"b"}, {"S", "A", "B"}, "S", [
            ("S", ("A",)), ("A", ("B",)), ("B", ("b",))])
        inc = IncrementalAnalyzer(g)
        cs = inc.retract(0)
        self.assertEqual(cs.lost_reachable, frozenset({"A", "B"}))
        self.assertEqual(inc.reachable, {"S"})

    def test_double_retract_rejected(self):
        inc = IncrementalAnalyzer(demo_grammar())
        inc.retract(0)
        with self.assertRaises(ValueError):
            inc.retract(0)

    def test_matches_batch_recompute_for_all_retraction_orders(self):
        g = demo_grammar()
        n = len(g.productions)
        for perm in itertools.permutations(range(n)):
            inc = IncrementalAnalyzer(g)
            for idx in perm:
                inc.retract(idx)
                alive = [i for i in range(n) if inc.alive[i]]
                batch = sub_grammar(g, alive)
                self.assertEqual(inc.generating,
                                 set(compute_generating(batch).generating))
                self.assertEqual(inc.reachable,
                                 set(compute_reachable(batch)))

    def test_circular_support_not_fooled(self):
        g = Grammar({"a"}, {"S", "A", "B"}, "S", [
            ("S", ("A",)), ("A", ("B",)), ("B", ("A",)), ("A", ("a",))])
        inc = IncrementalAnalyzer(g)
        cs = inc.retract(3)
        self.assertEqual(cs.lost_generating, frozenset({"S", "A", "B"}))


if __name__ == "__main__":
    unittest.main()
