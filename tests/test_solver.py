import io
import json
import unittest
from contextlib import redirect_stdout
from fractions import Fraction

from storage_scheduler import (
    Instance,
    STATUS_FEASIBLE,
    STATUS_INFEASIBLE,
    STATUS_OPTIMAL,
    STATUS_UNDECIDED,
    objective_of,
    solve,
    validate_schedule,
)
from storage_scheduler.cli import main as cli_main


def make_instance(**overrides):
    data = {
        "capacity": 10,
        "charge_efficiency": 1,
        "discharge_efficiency": 1,
        "max_charge_power": 10,
        "max_discharge_power": 10,
        "initial_energy": 0,
        "final_energy": 0,
        "prices": [1, 5],
        "demands": [0, 0],
    }
    data.update(overrides)
    return Instance.from_dict(data)


class TestOptimalArbitrage(unittest.TestCase):
    def test_buy_low_sell_high(self):
        inst = make_instance()
        result = solve(inst)
        self.assertEqual(result.status, STATUS_OPTIMAL)
        self.assertEqual(result.objective, Fraction(-40))  # 10*1 - 10*5
        s = result.schedule
        self.assertEqual(s[0].charge_grid, Fraction(10))
        self.assertEqual(s[0].soc_after, Fraction(10))
        self.assertEqual(s[1].discharge_delivered, Fraction(10))
        self.assertEqual(s[1].soc_after, Fraction(0))
        validate_schedule(inst, s)

    def test_capacity_binding(self):
        inst = make_instance(capacity=4)
        result = solve(inst)
        self.assertEqual(result.status, STATUS_OPTIMAL)
        self.assertEqual(result.objective, Fraction(-16))  # 4*1 - 4*5
        for ps in result.schedule:
            self.assertLessEqual(ps.soc_after, Fraction(4))

    def test_power_limits(self):
        inst = make_instance(capacity=100, max_charge_power=3,
                             max_discharge_power=2)
        result = solve(inst)
        self.assertEqual(result.status, STATUS_OPTIMAL)
        # charge 2 @1, discharge 2 @5 -> 2 - 10 = -8 (over-charging is
        # wasted because the final-energy target is already met)
        self.assertEqual(result.objective, Fraction(-8))
        self.assertEqual(result.schedule[0].charge_grid, Fraction(2))
        self.assertEqual(result.schedule[1].discharge_delivered, Fraction(2))


class TestEfficiencyPerDirection(unittest.TestCase):
    def test_losses_counted_per_direction(self):
        # eta_c = eta_d = 1/2: to deliver 1 in period 2 we must store 2,
        # which requires buying 4 grid energy in period 1.
        inst = make_instance(
            charge_efficiency="1/2", discharge_efficiency="1/2",
            prices=[1, 3], demands=[0, 1])
        result = solve(inst)
        self.assertEqual(result.status, STATUS_OPTIMAL)
        self.assertEqual(result.schedule[0].charge_grid, Fraction(4))
        self.assertEqual(result.schedule[1].discharge_delivered, Fraction(1))
        self.assertEqual(result.objective, Fraction(1))  # 4*1 - 1*3
        validate_schedule(inst, result.schedule)

    def test_rational_exactness(self):
        inst = make_instance(
            charge_efficiency="2/3", discharge_efficiency="3/5",
            prices=["1/3", "2/7"], demands=[0, "1/5"])
        result = solve(inst)
        self.assertEqual(result.status, STATUS_OPTIMAL)
        # need (1/5)/(3/5) = 1/3 stored; grid = (1/3)/(2/3) = 1/2;
        # cost = 1/2 * 1/3 = 1/6; revenue = 1/5 * 2/7 = 2/35.
        self.assertEqual(result.objective, Fraction(23, 210))
        self.assertIsInstance(result.objective, Fraction)
        for ps in result.schedule:
            self.assertIsInstance(ps.charge_grid, Fraction)
            self.assertIsInstance(ps.soc_after, Fraction)


class TestFinalEnergyTarget(unittest.TestCase):
    def test_final_energy_must_be_met_even_if_expensive(self):
        inst = make_instance(prices=[10], demands=[0], capacity=5,
                             max_charge_power=5, final_energy=3)
        result = solve(inst)
        self.assertEqual(result.status, STATUS_OPTIMAL)
        self.assertEqual(result.schedule[-1].soc_after, Fraction(3))
        self.assertEqual(result.objective, Fraction(30))

    def test_final_energy_unreachable_is_infeasible(self):
        inst = make_instance(prices=[1, 1], demands=[0, 0], capacity=5,
                             max_charge_power=1, initial_energy=1,
                             final_energy=5)
        result = solve(inst)
        self.assertEqual(result.status, STATUS_INFEASIBLE)
        self.assertIsNone(result.schedule)


class TestInfeasible(unittest.TestCase):
    def test_demand_exceeds_discharge_power(self):
        inst = make_instance(prices=[1], demands=[6], max_discharge_power=5)
        self.assertEqual(solve(inst).status, STATUS_INFEASIBLE)

    def test_not_enough_energy_for_demand(self):
        # demand 3 in period 1, only 1 stored, and charging is forbidden in a
        # demand period (no simultaneous charge/discharge).
        inst = make_instance(prices=[1, 1], demands=[3, 3], capacity=2,
                             max_charge_power=2, initial_energy=1)
        self.assertEqual(solve(inst).status, STATUS_INFEASIBLE)


class TestNegativePrices(unittest.TestCase):
    def test_no_same_period_cycle_and_bounded_gain(self):
        # If same-period charge+discharge were allowed, one could buy 4 grid
        # energy at a negative price and resell the losses in the same period
        # for a profit of 6 (period 1) and 9 (period 2) = -15 total, with the
        # battery never moving. That meaningless cycle is forbidden, so the
        # best is to charge 2 grid energy in period 2 only: objective -6.
        inst = make_instance(
            prices=[-2, -3], demands=[0, 0], capacity=1,
            charge_efficiency="1/2", discharge_efficiency="1/2",
            max_charge_power=4, max_discharge_power=4)
        result = solve(inst)
        self.assertEqual(result.status, STATUS_OPTIMAL)
        self.assertEqual(result.objective, Fraction(-6))
        for ps in result.schedule:
            self.assertFalse(ps.charge_grid > 0 and ps.discharge_delivered > 0,
                             "simultaneous charge/discharge detected")
        validate_schedule(inst, result.schedule)

    def test_negative_price_mode_enumeration(self):
        # Two negative-price zero-demand periods -> 4 mode configurations.
        inst = make_instance(
            prices=[-1, -2, 5], demands=[0, 0, 1], capacity=2,
            max_charge_power=2, max_discharge_power=2)
        result = solve(inst)
        self.assertEqual(result.status, STATUS_OPTIMAL)
        self.assertEqual(result.configs_total, 4)
        # charge 2 @-2 in period 2, deliver 2 @5 in period 3 (demand 1 plus
        # one surplus unit sold): -4 - 10 = -14
        self.assertEqual(result.objective, Fraction(-14))
        validate_schedule(inst, result.schedule)


class TestBudgetDecision(unittest.TestCase):
    def test_budget_yes_and_no(self):
        inst = make_instance()  # optimal objective is -40
        self.assertEqual(solve(inst, budget=Fraction(-30)).budget_decision, "yes")
        self.assertEqual(solve(inst, budget=Fraction(-50)).budget_decision, "no")

    def test_budget_undecided_when_iteration_budget_exhausted(self):
        inst = make_instance()
        result = solve(inst, budget=Fraction(-1), iteration_budget=0)
        self.assertEqual(result.status, STATUS_FEASIBLE)  # feasible, not proven
        self.assertEqual(result.objective, Fraction(0))   # do-nothing incumbent
        self.assertEqual(result.budget_decision, "undecided")
        # A budget the incumbent already meets is decided immediately.
        self.assertEqual(solve(inst, budget=Fraction(0),
                               iteration_budget=0).budget_decision, "yes")

    def test_undecided_when_config_budget_exhausted(self):
        inst = make_instance(
            prices=[-1, -2, 5], demands=[0, 0, 1], capacity=2,
            max_charge_power=2, max_discharge_power=2)
        partial = solve(inst, config_budget=2)
        self.assertEqual(partial.status, STATUS_FEASIBLE)
        # best of the first two configs: charge 2 @-1, deliver 2 @5 -> -12
        self.assertEqual(partial.objective, Fraction(-12))
        self.assertEqual(partial.configs_evaluated, 2)
        self.assertEqual(partial.configs_total, 4)
        full = solve(inst, config_budget=4)
        self.assertEqual(full.status, STATUS_OPTIMAL)
        self.assertEqual(full.objective, Fraction(-14))


class TestInitialEnergy(unittest.TestCase):
    def test_initial_energy_used_for_demand(self):
        inst = make_instance(prices=[3], demands=[2], initial_energy=2,
                             capacity=5, max_discharge_power=5)
        result = solve(inst)
        self.assertEqual(result.status, STATUS_OPTIMAL)
        self.assertEqual(result.objective, Fraction(-6))  # sell 2 @3
        self.assertEqual(result.schedule[0].charge_grid, Fraction(0))


class TestBruteForceCrossCheck(unittest.TestCase):
    def test_matches_integer_brute_force(self):
        # Lossless instance with integer data: the LP has an integral optimal
        # solution, so an exhaustive integer search must agree with the solver.
        inst = make_instance(prices=[2, 5, 1], demands=[0, 1, 0], capacity=3,
                             max_charge_power=3, max_discharge_power=3)
        result = solve(inst)
        self.assertEqual(result.status, STATUS_OPTIMAL)

        best = None
        for c1 in range(4):
            for d2 in range(4):
                for c3 in range(4):
                    for d3 in range(4):
                        if c3 > 0 and d3 > 0:
                            continue
                        if d2 < 1:  # demand in period 2
                            continue
                        s1 = c1
                        s2 = s1 - d2
                        s3 = s2 + c3 - d3
                        if not (0 <= s1 <= 3 and 0 <= s2 <= 3 and 0 <= s3 <= 3):
                            continue
                        obj = 2 * c1 - 5 * d2 + 1 * (c3 - d3)
                        best = obj if best is None else min(best, obj)
        self.assertEqual(result.objective, Fraction(best))


class TestValidation(unittest.TestCase):
    def test_invalid_instances_rejected(self):
        with self.assertRaises(ValueError):
            make_instance(capacity=0)
        with self.assertRaises(ValueError):
            make_instance(charge_efficiency=0)
        with self.assertRaises(ValueError):
            make_instance(discharge_efficiency="3/2")
        with self.assertRaises(ValueError):
            make_instance(final_energy=11)
        with self.assertRaises(ValueError):
            make_instance(prices=[1], demands=[0, 0])
        with self.assertRaises(ValueError):
            make_instance(demands=[-1, 0])
        with self.assertRaises(ValueError):
            Instance.from_dict({"capacity": 1})


class TestCli(unittest.TestCase):
    def test_cli_text_and_json(self):
        buf = io.StringIO()
        with redirect_stdout(buf):
            code = cli_main(["examples/arbitrage.json"])
        self.assertEqual(code, 0)
        out = buf.getvalue()
        self.assertIn("Status: optimal", out)
        self.assertIn("-112/5", out)

        buf = io.StringIO()
        with redirect_stdout(buf):
            code = cli_main(["examples/arbitrage.json", "--budget", "-20",
                             "--json"])
        self.assertEqual(code, 0)
        payload = json.loads(buf.getvalue())
        self.assertEqual(payload["status"], "optimal")
        self.assertEqual(payload["objective"], "-112/5")
        self.assertEqual(payload["budget_decision"], "yes")
        self.assertEqual(len(payload["schedule"]), 4)

    def test_cli_infeasible_example(self):
        buf = io.StringIO()
        with redirect_stdout(buf):
            code = cli_main(["examples/infeasible.json"])
        self.assertEqual(code, 0)
        self.assertIn("Status: infeasible", buf.getvalue())


if __name__ == "__main__":
    unittest.main()
