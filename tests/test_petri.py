import unittest

from petri import (
    COMPLETE,
    INVALID,
    PENDING,
    VALID,
    CertificateCache,
    PetriNet,
    ReplayError,
    SymmetryReduction,
    Transition,
    check_invariant,
    cross_check,
    reachability,
    replay,
    replay_dict,
)


def producer_consumer():
    """Bounded buffer: produce self-loops into buffer (cap 2), consume drains."""
    return PetriNet(
        capacities={"p_ready": 1, "c_ready": 1, "buffer": 2},
        transitions=[
            Transition.make("produce", inputs={"p_ready": 1},
                            outputs={"p_ready": 1, "buffer": 1}),
            Transition.make("consume", inputs={"buffer": 1, "c_ready": 1},
                            outputs={"c_ready": 1}),
        ],
        initial={"p_ready": 1, "c_ready": 1},
    )


def worker_net(n=3):
    """n identical workers, each flips idle_i <-> busy_i (symmetric net)."""
    capacities = {}
    transitions = []
    initial = {}
    # A shared scheduler token read (not consumed) by every wake transition
    # keeps wake/sleep structurally distinct, so the only symmetries are the
    # worker permutations -- not idle/busy flips.
    capacities["sched"] = 1
    initial["sched"] = 1
    for i in range(n):
        capacities[f"idle_{i}"] = 1
        capacities[f"busy_{i}"] = 1
        initial[f"idle_{i}"] = 1
        transitions.append(Transition.make(f"wake_{i}", inputs={f"idle_{i}": 1},
                                           outputs={f"busy_{i}": 1},
                                           reads={"sched": 1}))
        transitions.append(Transition.make(f"sleep_{i}", inputs={f"busy_{i}": 1},
                                           outputs={f"idle_{i}": 1}))
    return PetriNet(capacities, transitions, initial)


def chain_net():
    """Asymmetric a -> b -> c chain: no nontrivial place symmetry."""
    return PetriNet(
        capacities={"a": 2, "b": 2, "c": 2},
        transitions=[
            Transition.make("t1", inputs={"a": 1}, outputs={"b": 1}),
            Transition.make("t2", inputs={"b": 1}, outputs={"c": 1}),
        ],
        initial={"a": 1},
    )


def ring_net():
    """Conservative token ring a -> b -> c -> a (total tokens invariant)."""
    return PetriNet(
        capacities={"a": 2, "b": 2, "c": 2},
        transitions=[
            Transition.make("t1", inputs={"a": 1}, outputs={"b": 1}),
            Transition.make("t2", inputs={"b": 1}, outputs={"c": 1}),
            Transition.make("t3", inputs={"c": 1}, outputs={"a": 1}),
        ],
        initial={"a": 1},
    )


class TestFiringSemantics(unittest.TestCase):
    def test_input_multiplicity_requires_enough_tokens(self):
        net = PetriNet(
            {"p": 3, "q": 3},
            [Transition.make("t", inputs={"p": 2}, outputs={"q": 1})],
            initial={"p": 1},
        )
        t = net.transitions["t"]
        self.assertFalse(net.is_enabled(t, net.initial))  # 1 token < weight 2
        m2 = net.marking_dict  # noqa: F841
        state = (2, 0)  # p=2, q=0
        self.assertTrue(net.is_enabled(t, state))
        self.assertEqual(net.fire(t, state), (0, 1))

    def test_capacity_overflow_disables_transition(self):
        net = PetriNet(
            {"p": 1},
            [Transition.make("gen", outputs={"p": 1})],
            initial={"p": 1},
        )
        t = net.transitions["gen"]
        self.assertFalse(net.is_enabled(t, net.initial))  # would exceed cap 1
        self.assertTrue(net.is_enabled(t, (0,)))

    def test_self_loop_consumed_and_produced_together(self):
        # in=1, out=1 on the same place: net effect 0, stays enabled at cap.
        net = PetriNet(
            {"p": 2},
            [Transition.make("loop", inputs={"p": 1}, outputs={"p": 1})],
            initial={"p": 2},
        )
        t = net.transitions["loop"]
        self.assertTrue(net.is_enabled(t, net.initial))
        self.assertEqual(net.fire(t, net.initial), net.initial)
        # in=1, out=2 with cap 3: 2 - 1 + 2 = 3 fits; with cap 2 it would not.
        net2 = PetriNet(
            {"p": 3},
            [Transition.make("grow", inputs={"p": 1}, outputs={"p": 2})],
            initial={"p": 2},
        )
        self.assertTrue(net2.is_enabled(net2.transitions["grow"], net2.initial))
        net3 = PetriNet(
            {"p": 2},
            [Transition.make("grow", inputs={"p": 1}, outputs={"p": 2})],
            initial={"p": 2},
        )
        self.assertFalse(net3.is_enabled(net3.transitions["grow"], net3.initial))
        # self-loop still needs its input token.
        self.assertFalse(net.is_enabled(t, (0,)))

    def test_read_arc_requires_but_does_not_consume(self):
        net = PetriNet(
            {"lock": 1, "count": 2},
            [Transition.make("tick", reads={"lock": 1}, outputs={"count": 1})],
            initial={"lock": 1},
        )
        t = net.transitions["tick"]
        self.assertTrue(net.is_enabled(t, net.initial))
        after = net.fire(t, net.initial)
        self.assertEqual(net.marking_dict(after), {"lock": 1, "count": 1})
        # without the lock token the read arc blocks the transition
        self.assertFalse(net.is_enabled(t, (0, 0)))

    def test_inhibitor_arc_blocks_above_threshold(self):
        net = PetriNet(
            {"p": 2, "q": 1},
            [Transition.make("t", inhibitors={"p": 1}, outputs={"q": 1})],
            initial={"p": 0},
        )
        t = net.transitions["t"]
        self.assertTrue(net.is_enabled(t, net.initial))   # p == 0 < 1
        self.assertFalse(net.is_enabled(t, (1, 0)))       # p == 1 inhibits
        self.assertFalse(net.is_enabled(t, (2, 0)))

    def test_capacity_must_be_finite(self):
        with self.assertRaises(ValueError):
            PetriNet({"p": 0}, [], initial={})
        with self.assertRaises(ValueError):
            PetriNet({"p": None}, [], initial={})
        with self.assertRaises(ValueError):
            PetriNet({"p": 1.5}, [], initial={})


class TestReachability(unittest.TestCase):
    def test_exact_bounded_state_space_not_coverability(self):
        # Without capacities 'acc' would grow unboundedly (t1 self-produces);
        # the bounded net has an exact, finite reachable set instead.
        net = PetriNet(
            {"acc": 2, "sink": 2},
            [
                Transition.make("t1", outputs={"acc": 1}),
                Transition.make("t2", inputs={"acc": 1}, outputs={"sink": 1}),
                Transition.make("t3", inputs={"sink": 1}, outputs={"acc": 1}),
            ],
            initial={},
        )
        res = reachability(net)
        self.assertEqual(res.status, COMPLETE)
        self.assertEqual(res.semantics, "exact-bounded-reachability")
        reached = [net.marking_dict(res.representatives[k]) for k in res.states]
        # exactly the 3 x 3 bounded grid -- finite, where the uncapacitated
        # net would have infinitely many reachable markings
        self.assertEqual(len(res.states), 9)
        self.assertIn({"acc": 2, "sink": 2}, reached)
        self.assertIn({"acc": 2, "sink": 1}, reached)
        self.assertTrue(all(
            m["acc"] <= 2 and m["sink"] <= 2 for m in reached
        ))

    def test_marking_path_to_state(self):
        net = producer_consumer()
        res = reachability(net)
        target = None
        for k in res.states:
            if net.marking_dict(res.representatives[k])["buffer"] == 2:
                target = k
                break
        self.assertIsNotNone(target)
        trace = res.trace_to(target)
        self.assertEqual(trace, ["produce", "produce"])
        self.assertEqual(replay_dict(net, trace)["buffer"], 2)

    def test_pending_when_search_truncated(self):
        net = producer_consumer()
        res = reachability(net, max_states=2)
        self.assertEqual(res.status, PENDING)
        inv = check_invariant(net, lambda m: True, max_states=2)
        self.assertEqual(inv.status, PENDING)
        self.assertIsNone(inv.holds)  # no verdict from a partial search

    def test_invariant_holds_and_violation_counterexample(self):
        net = producer_consumer()
        ok = check_invariant(net, lambda m: m["buffer"] <= 2,
                             description="buffer within capacity")
        self.assertEqual(ok.status, COMPLETE)
        self.assertTrue(ok.holds)

        bad = check_invariant(net, lambda m: m["buffer"] <= 1,
                              description="buffer never exceeds 1")
        self.assertFalse(bad.holds)
        cex = bad.counterexample
        self.assertIsNotNone(cex)
        # counterexample replays to the exact violating marking
        self.assertEqual(cex.replay(net), cex.marking)
        self.assertEqual(cex.marking["buffer"], 2)
        self.assertEqual(set(cex.marking), {"p_ready", "c_ready", "buffer"})

    def test_replay_rejects_broken_trace(self):
        net = producer_consumer()
        with self.assertRaises(ReplayError):
            replay(net, ["consume"])  # buffer empty at start
        with self.assertRaises(ReplayError):
            replay(net, ["nope"])


class TestCertificates(unittest.TestCase):
    def test_register_and_check_reachable(self):
        net = ring_net()
        cache = CertificateCache(net)
        cert = cache.register("total", {"a": 1, "b": 1, "c": 1})
        self.assertEqual(cert.status, VALID)
        self.assertEqual(cert.constant, 1)
        res = reachability(net)
        self.assertTrue(cache.check_reachable("total", res))

    def test_register_rejects_non_invariant(self):
        net = producer_consumer()
        cache = CertificateCache(net)
        with self.assertRaises(ValueError):
            cache.register("bogus", {"buffer": 1})  # buffer not conserved

    def test_incremental_revalidation_stays_valid_for_read_arc_edit(self):
        net = ring_net()
        cache = CertificateCache(net)
        cache.register("total", {"a": 1, "b": 1, "c": 1})
        # adding a read arc moves no tokens: certificate must survive,
        # and only the changed transition column is rechecked.
        net.modify_transition("t1", reads={"c": 1})
        report = cache.revalidate()
        self.assertEqual(report["total"], VALID)
        cert = cache.get("total")
        self.assertEqual(cert.checked_transitions, frozenset({"t1"}))
        self.assertEqual(cert.revision, net.revision)

    def test_incremental_revalidation_detects_broken_flow(self):
        net = ring_net()
        cache = CertificateCache(net)
        cache.register("total", {"a": 1, "b": 1, "c": 1})
        # t2 now creates an extra token: conservation is broken.
        net.modify_transition("t2", outputs={"c": 2})
        report = cache.revalidate()
        self.assertEqual(report["total"], INVALID)
        self.assertEqual(cache.get("total").violations, {"t2"})
        # repairing the transition revalidates the same cache entry
        net.modify_transition("t2", outputs={"c": 1})
        report = cache.revalidate()
        self.assertEqual(report["total"], VALID)

    def test_terminal_token_totals_match_invariant(self):
        # net with deadlocks: token migrates s -> d and stops.
        net = PetriNet(
            {"s": 2, "d": 2},
            [Transition.make("move", inputs={"s": 1}, outputs={"d": 1})],
            initial={"s": 1},
        )
        cache = CertificateCache(net)
        cache.register("total", {"s": 1, "d": 1})  # all-ones: token total
        res = reachability(net)
        deadlocks = res.deadlock_keys()
        self.assertTrue(deadlocks)
        report = cache.verify_terminal_token_totals("total", res)
        self.assertTrue(all(report.values()))
        for k in deadlocks:
            marking = net.marking_dict(res.representatives[k])
            self.assertEqual(sum(marking.values()), 1)  # invariant total


class TestSymmetryReduction(unittest.TestCase):
    def test_canonical_form_and_enabling_preserved(self):
        net = worker_net(3)
        red = SymmetryReduction(net)
        self.assertGreater(len(red.permutations), 1)  # real symmetries found
        res = reachability(net)
        for k in res.states:
            m = res.representatives[k]
            self.assertTrue(red.preserves_enabling(m))
            self.assertEqual(red.canonical(red.canonical(m)), red.canonical(m))

    def test_reduced_search_smaller_and_cross_checked(self):
        net = worker_net(3)
        report = cross_check(net)
        self.assertEqual(report.status, COMPLETE)
        self.assertTrue(report.ok, report.details)
        self.assertEqual(report.plain_states, 8)    # 2^3 markings
        self.assertEqual(report.reduced_states, 4)  # orbits: 0..3 busy

    def test_cross_check_with_rotational_symmetry(self):
        # the token ring is symmetric under place rotation: its three
        # reachable markings collapse into a single orbit
        net = ring_net()
        red = SymmetryReduction(net)
        self.assertEqual(len(red.permutations), 3)
        report = cross_check(net)
        self.assertTrue(report.ok)
        self.assertEqual(report.plain_states, 3)
        self.assertEqual(report.reduced_states, 1)

    def test_reduced_counterexample_uses_real_place_ids_and_replays(self):
        net = worker_net(3)
        red = SymmetryReduction(net)
        inv = check_invariant(
            net,
            lambda m: any(m[f"idle_{i}"] == 1 for i in range(3)),
            reduction=red,
            description="at least one worker idle",
        )
        self.assertFalse(inv.holds)
        cex = inv.counterexample
        # real place IDs, not canonical placeholders
        self.assertEqual(
            set(cex.marking),
            {"sched"}
            | {f"idle_{i}" for i in range(3)}
            | {f"busy_{i}" for i in range(3)},
        )
        self.assertTrue(all(cex.marking[f"busy_{i}"] == 1 for i in range(3)))
        # replayable on the real net from the real initial marking
        self.assertEqual(cex.replay(net), cex.marking)

    def test_identity_fallback_keeps_search_exact(self):
        net = chain_net()  # asymmetric: identity is the only automorphism
        red = SymmetryReduction(net)
        self.assertEqual(len(red.permutations), 1)  # identity only
        plain = reachability(net)
        reduced = reachability(net, reduction=red)
        self.assertEqual(len(plain.states), len(reduced.states))
        report = cross_check(net)
        self.assertTrue(report.ok)
        self.assertEqual(report.plain_states, report.reduced_states)


if __name__ == "__main__":
    unittest.main()
