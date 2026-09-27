"""Unit tests for the synthetic tax recalculation engine."""

import asyncio
import unittest
from datetime import date
from fractions import Fraction

from taxcalc import (
    Invoice,
    InvoiceLine,
    RateTable,
    RecalculationService,
    Revision,
    RevisionEngine,
    TaxRate,
    compute_invoice,
    round_half_even,
)

D = date(2026, 1, 15)


def make_table():
    return RateTable([
        TaxRate("v1", "SYNTH", Fraction(1, 2), date(2026, 1, 1), date(2026, 7, 1)),
        TaxRate("v2", "SYNTH", Fraction(1, 4), date(2026, 1, 1), date(2026, 7, 1)),
        TaxRate("v3", "SYNTH", Fraction(1, 10), date(2026, 7, 1), None),
        TaxRate("v1", "OTHER", Fraction(1, 5), date(2026, 1, 1), None),
    ])


def make_invoice(lines, rate_version="v1", invoice_id="INV-1"):
    return Invoice(
        invoice_id=invoice_id,
        jurisdiction="SYNTH",
        issued_on=D,
        lines=tuple(InvoiceLine(description=f"line{i}", net_cents=n) for i, n in enumerate(lines)),
        rate_version=rate_version,
    )


class TestHalfEvenRounding(unittest.TestCase):
    def test_acceptance_one_cent_half_rate_rounds_to_zero(self):
        self.assertEqual(round_half_even(Fraction(1) * Fraction(1, 2)), 0)

    def test_acceptance_three_cents_half_rate_rounds_to_two(self):
        self.assertEqual(round_half_even(Fraction(3) * Fraction(1, 2)), 2)

    def test_ties_to_even(self):
        self.assertEqual(round_half_even(Fraction(5, 2)), 2)   # 2.5 -> 2
        self.assertEqual(round_half_even(Fraction(7, 2)), 4)   # 3.5 -> 4
        self.assertEqual(round_half_even(Fraction(-5, 2)), -2) # -2.5 -> -2
        self.assertEqual(round_half_even(Fraction(-7, 2)), -4) # -3.5 -> -4

    def test_non_ties(self):
        self.assertEqual(round_half_even(Fraction(13, 5)), 3)   # 2.6 -> 3
        self.assertEqual(round_half_even(Fraction(12, 5)), 2)   # 2.4 -> 2
        self.assertEqual(round_half_even(Fraction(-13, 5)), -3) # -2.6 -> -3


class TestInvoiceComputation(unittest.TestCase):
    def test_acceptance_via_invoice(self):
        table = make_table()
        one = compute_invoice(make_invoice([1]), table)
        three = compute_invoice(make_invoice([3]), table)
        self.assertEqual(one.lines[0].tax_cents, 0)
        self.assertEqual(three.lines[0].tax_cents, 2)

    def test_total_is_sum_of_per_line_taxes(self):
        # rate 1/2, lines [1, 1]: per-line 0 + 0 = 0,
        # whereas rounding the aggregate (2 * 1/2 = 1) would give 1.
        result = compute_invoice(make_invoice([1, 1]), make_table())
        self.assertEqual([l.tax_cents for l in result.lines], [0, 0])
        self.assertEqual(result.total_tax_cents, 0)
        self.assertNotEqual(result.total_tax_cents,
                            round_half_even(Fraction(result.total_net_cents) * Fraction(1, 2)))

    def test_total_net(self):
        result = compute_invoice(make_invoice([100, 250]), make_table())
        self.assertEqual(result.total_net_cents, 350)
        self.assertEqual(result.total_tax_cents, 50 + 125)

    def test_unknown_rounding_policy_rejected(self):
        inv = Invoice("X", "SYNTH", D, (InvoiceLine("a", 10),), "v1", rounding="half_up")
        with self.assertRaises(ValueError):
            compute_invoice(inv, make_table())


class TestRateMatching(unittest.TestCase):
    def test_jurisdiction_and_interval(self):
        table = make_table()
        self.assertEqual(table.match("SYNTH", date(2026, 3, 1)).version, "v1")
        self.assertEqual(table.match("SYNTH", date(2026, 8, 1)).version, "v3")
        self.assertEqual(table.match("OTHER", date(2026, 3, 1)).rate, Fraction(1, 5))

    def test_interval_end_is_exclusive(self):
        table = make_table()
        self.assertEqual(table.match("SYNTH", date(2026, 7, 1)).version, "v3")

    def test_no_match_raises(self):
        with self.assertRaises(LookupError):
            make_table().match("NOWHERE", D)
        with self.assertRaises(LookupError):
            make_table().match("SYNTH", date(2025, 1, 1))

    def test_version_pinning(self):
        table = make_table()
        self.assertEqual(table.match("SYNTH", D, "v2").rate, Fraction(1, 4))
        with self.assertRaises(LookupError):
            table.match("SYNTH", date(2026, 8, 1), "v1")


class TestRevisionEngine(unittest.TestCase):
    def test_reversal_and_reissue(self):
        table = make_table()
        engine = RevisionEngine(table)
        original = make_invoice([3, 5])
        reversal, reissue, created = engine.apply(original, Revision("REV-1", "v2"))
        self.assertTrue(created)

        self.assertEqual(reversal.kind, "reversal")
        self.assertEqual(reversal.references, "INV-1")
        self.assertEqual([l.net_cents for l in reversal.lines], [-3, -5])
        self.assertEqual(reversal.rate_version, "v1")

        self.assertEqual(reissue.kind, "reissue")
        self.assertEqual(reissue.references, "INV-1")
        self.assertEqual(reissue.rate_version, "v2")
        self.assertEqual([l.net_cents for l in reissue.lines], [3, 5])

        # Recomputed with new rate 1/4: 3/4 -> 1 (half-even of 0.75), 5/4 -> 1.
        rc = compute_invoice(reissue, table)
        self.assertEqual([l.tax_cents for l in rc.lines], [1, 1])

    def test_original_is_not_modified(self):
        engine = RevisionEngine(make_table())
        original = make_invoice([3, 5])
        snapshot = original
        engine.apply(original, Revision("REV-1", "v2"))
        self.assertIs(original, snapshot)
        self.assertEqual(original.rate_version, "v1")
        self.assertEqual([l.net_cents for l in original.lines], [3, 5])

    def test_replay_same_revision_is_idempotent(self):
        engine = RevisionEngine(make_table())
        original = make_invoice([3])
        rev = Revision("REV-1", "v2")
        r1, i1, c1 = engine.apply(original, rev)
        r2, i2, c2 = engine.apply(original, rev)
        self.assertTrue(c1)
        self.assertFalse(c2)
        self.assertIs(r1, r2)
        self.assertIs(i1, i2)

    def test_distinct_revisions_each_apply(self):
        engine = RevisionEngine(make_table())
        original = make_invoice([3])
        _, _, c1 = engine.apply(original, Revision("REV-1", "v2"))
        _, _, c2 = engine.apply(original, Revision("REV-2", "v2"))
        self.assertTrue(c1)
        self.assertTrue(c2)


class TestAsyncRecalculationService(unittest.TestCase):
    def test_async_recalc_and_replay(self):
        table = make_table()
        engine = RevisionEngine(table)
        service = RecalculationService(engine)
        original = make_invoice([1, 3])

        async def scenario():
            await service.submit(original, Revision("REV-1", "v2"))
            await service.submit(original, Revision("REV-1", "v2"))  # replay
            return await service.run()

        results = asyncio.run(scenario())
        self.assertEqual(len(results), 2)
        self.assertTrue(results[0]["created"])
        self.assertFalse(results[1]["created"])
        self.assertEqual(results[0]["reversal"]["total_tax_cents"], -2)  # -(0 + 2)
        self.assertEqual(results[0]["reissue"]["rate_version"], "v2")
        self.assertEqual(results[0]["reissue"]["total_tax_cents"], 1)  # 0 + 1


if __name__ == "__main__":
    unittest.main()
