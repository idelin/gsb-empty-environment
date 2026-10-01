import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from petrinet import (PetriNet, Place, Transition, InvariantCertificate,
                      CertificateStore, SearchLimits, search, cross_check,
                      is_enabled, fire, enabled, verify_certificate,
                      invariant_value, verify_symmetry, group_closure,
                      canonical, replay, ReplayError,
                      SAFE, VIOLATED, PENDING)


def line_net():
    places = [Place("raw", 3), Place("buf", 2), Place("done", 3)]
    transitions = [
        Transition("t_load", inputs={"raw": 1}, outputs={"buf": 1}),
        Transition("t_unload", inputs={"buf": 1}, outputs={"done": 1}),
        Transition("t_inspect", read={"done": 2}),
        Transition("t_recycle", inputs={"done": 3}, outputs={"raw": 3},
                   inhibitor={"buf": 1}),
    ]
    return PetriNet(places, transitions)


class TestModel(unittest.TestCase):
    def test_capacity_must_be_finite_positive(self):
        with self.assertRaises(ValueError):
            Place("p", None)
        with self.assertRaises(ValueError):
            Place("p", 0)
        with self.assertRaises(ValueError):
            Place("p", -1)

    def test_unknown_arc_endpoint_rejected(self):
        with self.assertRaises(ValueError):
            PetriNet([Place("p", 1)],
                     [Transition("t", inputs={"nope": 1})])

    def test_arc_weight_must_be_positive(self):
        with self.assertRaises(ValueError):
            PetriNet([Place("p", 1)], [Transition("t", inputs={"p": 0})])

    def test_encode_rejects_over_capacity_marking(self):
        net = PetriNet([Place("p", 1)], [])
        with self.assertRaises(ValueError):
            net.encode({"p": 2})

    def test_fingerprint_changes_on_edit(self):
        net = line_net()
        fp = net.fingerprint()
        net.replace_transition(Transition("t_load", inputs={"raw": 1},
                                          outputs={"buf": 2}))
        self.assertNotEqual(fp, net.fingerprint())


class TestEnabling(unittest.TestCase):
    def test_input_multiplicity_needs_enough_tokens(self):
        net = PetriNet([Place("p", 3), Place("q", 1)],
                       [Transition("t", inputs={"p": 2},
                                   outputs={"q": 1})])
        t = net.transitions["t"]
        self.assertFalse(is_enabled(net, net.encode({"p": 1}), t))
        self.assertTrue(is_enabled(net, net.encode({"p": 2}), t))
        self.assertEqual(net.decode(fire(net, net.encode({"p": 3}), t)),
                         {"p": 1, "q": 1})

    def test_self_loop_judged_on_net_effect(self):
        # consume 1 and produce 1 on a full capacity-1 place: enabled,
        # because the final count (1) respects capacity.
        net = PetriNet([Place("p", 1)],
                       [Transition("t", inputs={"p": 1},
                                   outputs={"p": 1})])
        m = net.encode({"p": 1})
        self.assertTrue(is_enabled(net, m, net.transitions["t"]))
        self.assertEqual(fire(net, m, net.transitions["t"]), m)

    def test_over_capacity_transition_disabled(self):
        net = PetriNet([Place("p", 1)],
                       [Transition("t", outputs={"p": 1})])
        self.assertTrue(is_enabled(net, net.encode({"p": 0}),
                                   net.transitions["t"]))
        self.assertFalse(is_enabled(net, net.encode({"p": 1}),
                                    net.transitions["t"]))

    def test_self_loop_over_capacity_disabled(self):
        # consume 1, produce 2 on capacity 2 with 1 token: final would be 2
        # (ok); with 2 tokens the input arc fails first.  Use cap 2 / in 1
        # out 2 with 2 tokens -> final 3 > 2 -> disabled.
        net = PetriNet([Place("p", 2)],
                       [Transition("t", inputs={"p": 1},
                                   outputs={"p": 2})])
        self.assertTrue(is_enabled(net, net.encode({"p": 1}),
                                   net.transitions["t"]))
        self.assertFalse(is_enabled(net, net.encode({"p": 2}),
                                    net.transitions["t"]))

    def test_read_arc_tests_without_consuming(self):
        net = PetriNet([Place("p", 2), Place("q", 1)],
                       [Transition("t", read={"p": 2},
                                   outputs={"q": 1})])
        t = net.transitions["t"]
        self.assertFalse(is_enabled(net, net.encode({"p": 1}), t))
        m = net.encode({"p": 2})
        self.assertTrue(is_enabled(net, m, t))
        self.assertEqual(net.decode(fire(net, m, t)), {"p": 2, "q": 1})

    def test_inhibitor_arc(self):
        net = PetriNet([Place("p", 2), Place("q", 1)],
                       [Transition("t", inhibitor={"p": 2},
                                   outputs={"q": 1})])
        t = net.transitions["t"]
        self.assertTrue(is_enabled(net, net.encode({"p": 1}), t))
        self.assertFalse(is_enabled(net, net.encode({"p": 2}), t))


class TestReachability(unittest.TestCase):
    def test_exact_state_count(self):
        # raw 0..3, buf 0..2 with raw+buf+done=3, done 0..3
        net = line_net()
        res = search(net, {"raw": 3})
        self.assertEqual(res.verdict, SAFE)
        # count markings satisfying raw+buf+done=3, buf<=2
        expect = sum(1 for r in range(4) for b in range(3)
                     if 0 <= 3 - r - b <= 3)
        self.assertEqual(len(res.markings), expect)

    def test_terminal_markings_and_token_total(self):
        # batch net: raw -> done, ends in a deadlock with all tokens in done
        net = PetriNet([Place("raw", 2), Place("done", 2)],
                       [Transition("t", inputs={"raw": 1},
                                   outputs={"done": 1})])
        cert = InvariantCertificate("c", {"raw": 1, "done": 1})
        res = search(net, {"raw": 2}, certificates=[cert])
        self.assertEqual(len(res.terminal_markings), 1)
        for m in res.terminal_markings:
            d = net.decode(m)
            self.assertEqual(d, {"done": 2, "raw": 0})
            self.assertEqual(sum(d.values()), 2)  # net invariant
            self.assertEqual(enabled(net, m), [])
        self.assertEqual(res.terminal_certificates_ok, {"c": True})

    def test_bad_property_counterexample_replayable(self):
        net = line_net()
        res = search(net, {"raw": 3}, bad=lambda m: m["done"] >= 1)
        self.assertEqual(res.verdict, VIOLATED)
        ce = res.counterexample
        self.assertEqual(ce.transitions, ["t_load", "t_unload"])
        self.assertTrue(ce.verify(net))
        self.assertEqual(replay(net, ce.initial, ce.transitions), ce.final)
        # counterexample uses real place ids
        self.assertEqual(set(ce.final), {"buf", "done", "raw"})

    def test_counterexample_json_roundtrip(self):
        net = line_net()
        res = search(net, {"raw": 3}, bad=lambda m: m["done"] >= 2)
        ce = res.counterexample
        clone = type(ce).from_json(ce.to_json())
        self.assertTrue(clone.verify(net))
        self.assertEqual(clone.final, ce.final)

    def test_safe_property(self):
        net = line_net()
        res = search(net, {"raw": 3},
                     bad=lambda m: m["buf"] == 2 and m["done"] == 2)
        self.assertEqual(res.verdict, SAFE)
        self.assertIsNone(res.counterexample)

    def test_truncated_search_is_pending(self):
        net = line_net()
        res = search(net, {"raw": 3}, limits=SearchLimits(max_states=2))
        self.assertEqual(res.verdict, PENDING)
        res = search(net, {"raw": 3}, limits=SearchLimits(max_depth=0))
        self.assertEqual(res.verdict, PENDING)

    def test_violation_beats_pending(self):
        net = line_net()
        res = search(net, {"raw": 3}, bad=lambda m: m["done"] >= 1,
                     limits=SearchLimits(max_states=100))
        self.assertEqual(res.verdict, VIOLATED)

    def test_path_to_uses_real_place_ids(self):
        net = line_net()
        res = search(net, {"raw": 3})
        path = res.path_to({"raw": 1, "buf": 0, "done": 2})
        self.assertEqual(path, ["t_load", "t_load", "t_unload", "t_unload"])
        self.assertIsNone(res.path_to({"raw": 0, "buf": 0, "done": 0}))

    def test_replay_error_on_disabled_step(self):
        net = line_net()
        with self.assertRaises(ReplayError):
            replay(net, {"raw": 3}, ["t_unload"])


class TestInvariants(unittest.TestCase):
    def test_valid_certificate_accepted(self):
        net = line_net()
        cert = InvariantCertificate("c", {"raw": 1, "buf": 1, "done": 1})
        self.assertTrue(verify_certificate(net, cert))

    def test_invalid_certificate_rejected(self):
        net = line_net()
        bad = InvariantCertificate("bad", {"raw": 1, "buf": 2, "done": 1})
        self.assertFalse(verify_certificate(net, bad))

    def test_invariant_value(self):
        net = line_net()
        cert = InvariantCertificate("c", {"raw": 1, "buf": 1, "done": 1})
        self.assertEqual(invariant_value(net, cert, net.encode({"raw": 3})), 3)

    def test_certificate_store_incremental_revalidation(self):
        net = line_net()
        cert = InvariantCertificate("c", {"raw": 1, "buf": 1, "done": 1})
        store = CertificateStore(net)
        self.assertTrue(store.register(cert))
        # widen t_load output: conservation broken
        net.replace_transition(Transition("t_load", inputs={"raw": 1},
                                          outputs={"buf": 2}))
        self.assertEqual(store.revalidate(), {"c": False})
        # restore: invalid cert gets a full re-check and becomes valid
        net.replace_transition(Transition("t_load", inputs={"raw": 1},
                                          outputs={"buf": 1}))
        self.assertEqual(store.revalidate(), {"c": True})

    def test_unaffected_certificate_survives_edit(self):
        net = line_net()
        store = CertificateStore(net)
        cert = InvariantCertificate("total", {"raw": 1, "buf": 1, "done": 1})
        self.assertTrue(store.register(cert))
        # edit t_inspect (read arc only, zero token effect): cert stays valid
        net.replace_transition(Transition("t_inspect", read={"done": 3}))
        self.assertEqual(store.revalidate(), {"total": True})

    def test_removed_transition_skipped_in_revalidation(self):
        net = line_net()
        store = CertificateStore(net)
        cert = InvariantCertificate("total", {"raw": 1, "buf": 1, "done": 1})
        self.assertTrue(store.register(cert))
        net.remove_transition("t_recycle")
        self.assertEqual(store.revalidate(), {"total": True})


class TestSymmetry(unittest.TestCase):
    def sym_net(self):
        places = [Place("idleA", 1), Place("busyA", 1),
                  Place("idleB", 1), Place("busyB", 1)]
        transitions = [
            Transition("startA", inputs={"idleA": 1}, outputs={"busyA": 1}),
            Transition("stopA", inputs={"busyA": 1}, outputs={"idleA": 1}),
            Transition("startB", inputs={"idleB": 1}, outputs={"busyB": 1}),
            Transition("stopB", inputs={"busyB": 1}, outputs={"idleB": 1}),
        ]
        swap = {"idleA": "idleB", "idleB": "idleA",
                "busyA": "busyB", "busyB": "busyA"}
        return PetriNet(places, transitions), swap

    def test_verify_symmetry_accepts_automorphism(self):
        net, swap = self.sym_net()
        self.assertTrue(verify_symmetry(net, swap))

    def test_verify_symmetry_rejects_capacity_mismatch(self):
        _, swap = self.sym_net()
        net = PetriNet([Place("idleA", 1), Place("busyA", 2),
                        Place("idleB", 1), Place("busyB", 1)],
                       [Transition("startA", inputs={"idleA": 1},
                                   outputs={"busyA": 1}),
                        Transition("stopA", inputs={"busyA": 1},
                                   outputs={"idleA": 1}),
                        Transition("startB", inputs={"idleB": 1},
                                   outputs={"busyB": 1}),
                        Transition("stopB", inputs={"busyB": 1},
                                   outputs={"idleB": 1})])
        self.assertFalse(verify_symmetry(net, swap))

    def test_verify_symmetry_rejects_non_automorphism(self):
        net, _ = self.sym_net()
        # map that is not a permutation of all places
        self.assertFalse(verify_symmetry(net, {"idleA": "idleB"}))

    def test_group_closure_size(self):
        net, swap = self.sym_net()
        perms = group_closure(net, [swap])
        self.assertEqual(len(perms), 2)

    def test_canonical_representative(self):
        net, swap = self.sym_net()
        perms = group_closure(net, [swap])
        m1 = net.encode({"busyA": 1, "idleB": 1})
        m2 = net.encode({"idleA": 1, "busyB": 1})
        self.assertEqual(canonical(m1, perms), canonical(m2, perms))

    def test_cross_check_small_net(self):
        net, swap = self.sym_net()
        cc = cross_check(net, {"idleA": 1, "idleB": 1}, [swap])
        self.assertTrue(cc["match"])
        self.assertEqual(cc["full_states"], 4)
        self.assertEqual(cc["reduced_states"], 3)
        self.assertEqual(cc["orbits"], 3)

    def test_reduced_counterexample_replays_on_real_places(self):
        # symmetric net with a bad property reachable in one step
        net, swap = self.sym_net()
        perms = group_closure(net, [swap])
        res = search(net, {"idleA": 1, "idleB": 1},
                     bad=lambda m: m.get("busyB", 0) >= 1, perms=perms)
        self.assertEqual(res.verdict, VIOLATED)
        ce = res.counterexample
        self.assertTrue(ce.verify(net))
        self.assertEqual(set(ce.final),
                         {"busyA", "busyB", "idleA", "idleB"})

    def test_symmetry_preserves_enabling(self):
        # for every reachable marking, enabled sets of symmetric markings
        # are symmetric images of each other
        net, swap = self.sym_net()
        res = search(net, {"idleA": 1, "idleB": 1})
        rename = {"startA": "startB", "startB": "startA",
                  "stopA": "stopB", "stopB": "stopA"}
        perms = group_closure(net, [swap])
        from petrinet.symmetry import apply_perm
        for m in res.markings:
            sm = apply_perm(m, perms[1])
            self.assertEqual(sorted(rename[t] for t in enabled(net, m)),
                             enabled(net, sm))


class TestFiniteCapacityNotCoverability(unittest.TestCase):
    def test_capacity_blocks_unbounded_accumulation(self):
        # Unbounded semantics: move, move -> p has 2 -> escape fires.
        # Capacity 1: second 'move' is disabled, 'escape' never enabled.
        net = PetriNet([Place("p", 1), Place("q", 1), Place("leak", 1)],
                       [Transition("move", inputs={"q": 1},
                                   outputs={"p": 1, "q": 1}),
                        Transition("escape", inputs={"p": 2},
                                   outputs={"leak": 1})])
        res = search(net, {"q": 1}, bad=lambda m: m["leak"] >= 1)
        self.assertEqual(res.verdict, SAFE)
        self.assertEqual(len(res.markings), 2)

    def test_unbounded_place_rejected_by_model(self):
        with self.assertRaises(ValueError):
            PetriNet([Place("p", None)], [])


if __name__ == "__main__":
    unittest.main(verbosity=2)
