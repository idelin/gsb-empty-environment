import unittest
from fractions import Fraction

from wmmf import (
    Demand,
    Network,
    allocate,
    build_certificate,
    check_feasible,
    verify_certificate,
)


def demo_network():
    return Network(
        capacities={"L1": 10, "L2": 4},
        demands={
            "A": Demand("A", 1, 100, {"L1"}),
            "B": Demand("B", 1, 100, {"L1", "L2"}),
            "C": Demand("C", 1, 100, {"L2"}),
        },
    )


class TestCertificate(unittest.TestCase):
    def test_certificate_builds_and_verifies(self):
        net = demo_network()
        alloc = allocate(net)
        cert = build_certificate(net, alloc.rates)
        self.assertTrue(verify_certificate(net, alloc.rates, cert))
        # Every demand here is unsatisfied (caps are huge), so each needs one.
        self.assertEqual(set(cert.witnesses), {"A", "B", "C"})
        self.assertEqual(cert.witnesses["A"].link, "L1")
        self.assertEqual(cert.witnesses["B"].link, "L2")
        self.assertEqual(cert.witnesses["C"].link, "L2")

    def test_capped_demands_need_no_witness(self):
        net = Network(
            {"L1": 10},
            {"A": Demand("A", 1, 3, {"L1"}), "B": Demand("B", 1, 100, {"L1"})},
        )
        alloc = allocate(net)
        cert = build_certificate(net, alloc.rates)
        self.assertTrue(verify_certificate(net, alloc.rates, cert))
        self.assertNotIn("A", cert.witnesses)  # A is satisfied at its cap
        self.assertIn("B", cert.witnesses)

    def test_infeasible_rates_fail_verification(self):
        net = demo_network()
        alloc = allocate(net)
        cert = build_certificate(net, alloc.rates)
        tampered = dict(alloc.rates)
        tampered["A"] = Fraction(11)  # exceeds L1 capacity 10
        self.assertFalse(verify_certificate(net, tampered, cert))
        self.assertTrue(check_feasible(net, tampered))

    def test_suboptimal_rates_fail_verification(self):
        net = demo_network()
        alloc = allocate(net)
        cert = build_certificate(net, alloc.rates)
        # Feasible but not max-min fair: B starved below its fair share.
        unfair = {"A": Fraction(9), "B": Fraction(1), "C": Fraction(2)}
        self.assertFalse(check_feasible(net, unfair))
        self.assertFalse(verify_certificate(net, unfair, cert))
        # And no certificate can be built for it at all.
        with self.assertRaises(ValueError):
            build_certificate(net, unfair)

    def test_certificate_property_by_grid_brute_force(self):
        """Direct check of the certificate's promise on a rational grid:
        any feasible grid point that raises an unsatisfied demand must lower
        some demand whose normalized rate was no higher."""
        net = demo_network()
        alloc = allocate(net)
        rates = alloc.rates
        norms = net.normalized_rates(rates)
        step = Fraction(1, 2)
        grids = {}
        for demand_id, demand in net.demands.items():
            values = []
            v = Fraction(0)
            while v <= min(demand.cap, 12):
                values.append(v)
                v += step
            grids[demand_id] = values
        checked = 0
        for a in grids["A"]:
            for b in grids["B"]:
                for c in grids["C"]:
                    point = {"A": a, "B": b, "C": c}
                    if check_feasible(net, point):
                        continue
                    for d in net.demands:
                        if rates[d] >= net.demands[d].cap:
                            continue  # satisfied demands are exempt
                        if point[d] > rates[d]:
                            # must exist a victim with normalized rate <= norm(d)
                            victims = [
                                o
                                for o in net.demands
                                if net.normalized_rates(point)[o] < norms[o]
                                and norms[o] <= norms[d]
                            ]
                            self.assertTrue(
                                victims,
                                f"raising {d} to {point[d]} at {point} hurt nobody "
                                f"with normalized rate <= {norms[d]}",
                            )
                            checked += 1
        self.assertGreater(checked, 0)


if __name__ == "__main__":
    unittest.main()
