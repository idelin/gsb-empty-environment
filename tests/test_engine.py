import json
import os
import tempfile
import unittest

from constkit import CancelToken, Cancelled, Engine


TOGGLE_SRC = """
module M;
const A = B + 1;
when !fast { const B = 2; }
when fast  { const B = A; }
"""


class ConditionCycleTests(unittest.TestCase):
    def test_toggle_creates_then_removes_cycle(self):
        e = Engine()
        r = e.apply(sources={"M": TOGGLE_SRC})
        self.assertTrue(r.ok)
        self.assertEqual(e.artifact("M", "A"), 3)

        r = e.apply(flags={"fast": True})
        self.assertEqual(len([d for d in r.diagnostics if d.code == "cycle"]), 2)
        # the previous usable artifact is not polluted by the failure
        self.assertEqual(e.artifact("M", "A"), 3)
        self.assertEqual(e.artifact("M", "B"), 2)

        r = e.apply(flags={"fast": False})
        self.assertEqual(r.diagnostics, [])
        self.assertEqual(e.artifact("M", "A"), 3)
        self.assertEqual(e.artifact("M", "B"), 2)

    def test_condition_switch_deletes_old_dependency(self):
        e = Engine()
        e.apply(sources={"M": """
            module M;
            when slow { const X = Y + 1; }
            const Y = 10;
        """})
        self.assertNotIn("M.X", e.artifacts())
        self.assertEqual(e.artifact("M", "Y"), 10)
        r = e.apply(flags={"slow": True})
        self.assertTrue(r.ok)
        self.assertEqual(e.artifact("M", "X"), 11)
        r = e.apply(flags={"slow": False})
        self.assertTrue(r.ok)
        self.assertNotIn("M.X", e.artifacts())
        self.assertIn("M.X", e.snapshot.retired)
        self.assertEqual(e.snapshot.retired["M.X"].value, 11)


class ModuleIsolationTests(unittest.TestCase):
    def test_same_named_constants_do_not_misbind(self):
        e = Engine()
        e.apply(sources={
            "A": "module A;\nconst v = 1;\nconst r = v + B.v;\n",
            "B": "module B;\nconst v = 2;\n",
        })
        self.assertEqual(e.artifact("A", "v"), 1)
        self.assertEqual(e.artifact("B", "v"), 2)
        self.assertEqual(e.artifact("A", "r"), 3)

    def test_local_constants_are_not_exported(self):
        e = Engine()
        r = e.apply(sources={
            "M": "module M;\nlocal secret = 100;\nconst pub = secret + 1;\n",
            "N": "module N;\nconst x = 2;\nconst ok = M.pub + x;\n"
                  "const bad = M.secret;\n",
        })
        self.assertEqual(e.artifact("M", "pub"), 101)
        self.assertEqual(e.artifact("N", "ok"), 103)
        bad = next(d for d in r.diagnostics if d.code == "name-error")
        self.assertIn("not exported", bad.message)
        with self.assertRaises(KeyError):
            e.artifact("N", "bad")


class IncrementalTests(unittest.TestCase):
    SRC_M = "module M;\nconst a = 1;\nconst b = a + 1;\n"
    SRC_N = "module N;\nconst x = 10;\nconst y = x * 2;\n"
    SRC_P = "module P;\nconst q = M.b + 1;\n"

    def _engine(self):
        e = Engine()
        e.apply(sources={"M": self.SRC_M, "N": self.SRC_N, "P": self.SRC_P})
        return e

    def test_initial_evaluation_counts_all_constants(self):
        e = self._engine()
        self.assertEqual(e.eval_count, 5)
        self.assertEqual(sorted(e.last_evaluated), ["M.a", "M.b", "N.x", "N.y", "P.q"])

    def test_unrelated_module_is_not_evaluated(self):
        e = self._engine()
        e.eval_count = 0
        e.apply(sources={"M": "module M;\nconst a = 2;\nconst b = a + 1;\n"})
        self.assertEqual(e.eval_count, 3)
        self.assertEqual(e.last_evaluated, ["M.a", "M.b", "P.q"])
        self.assertEqual(sorted(e.last_reused), ["N.x", "N.y"])
        self.assertEqual(e.artifact("M", "b"), 3)
        self.assertEqual(e.artifact("P", "q"), 4)

    def test_no_change_means_no_evaluation(self):
        e = self._engine()
        e.eval_count = 0
        e.apply(sources={"M": self.SRC_M})
        self.assertEqual(e.eval_count, 0)
        self.assertEqual(sorted(e.last_reused), ["M.a", "M.b", "N.x", "N.y", "P.q"])

    def test_downstream_only_constant_does_not_retrigger_leaf(self):
        e = self._engine()
        e.eval_count = 0
        e.apply(sources={"P": "module P;\nconst q = M.b + 100;\n"})
        self.assertEqual(e.last_evaluated, ["P.q"])
        self.assertNotIn("M.a", e.last_evaluated)
        self.assertNotIn("N.x", e.last_evaluated)


class BatchPublishTests(unittest.TestCase):
    def test_validate_before_publish_keeps_good_state(self):
        e = Engine()
        e.apply(sources={"M": "module M;\nconst A = 1;\nconst B = A + 1;\n"})
        before = dict(e.artifacts())
        before_flags = dict(e.flags)

        bad = "module M;\nconst A = 1;\nconst B = 1 / (A - A);\n"
        r = e.apply(sources={"M": bad}, require_clean=True)
        self.assertFalse(r.committed)
        self.assertTrue(any(d.code == "div-by-zero" for d in r.diagnostics))
        self.assertEqual(e.artifacts(), before)  # publish rejected: state intact
        self.assertEqual(e.diagnostics, [])
        self.assertEqual(e.sources["M"], "module M;\nconst A = 1;\nconst B = A + 1;\n")
        self.assertEqual(e.flags, before_flags)

        fixed = "module M;\nconst A = 41;\nconst B = A + 1;\n"
        r = e.apply(sources={"M": fixed}, require_clean=True)
        self.assertTrue(r.committed)
        self.assertEqual(e.artifact("M", "B"), 42)

    def test_batch_with_flags_validates_atomically(self):
        e = Engine()
        e.apply(sources={"M": TOGGLE_SRC})
        before = dict(e.artifacts())
        r = e.apply(sources={"M": TOGGLE_SRC}, flags={"fast": True}, require_clean=True)
        self.assertFalse(r.committed)
        self.assertNotIn("fast", e.flags)
        self.assertEqual(e.artifacts(), before)


class _AutoCancel(CancelToken):
    """Cancels itself after `after` cancellation checks."""

    def __init__(self, after):
        super().__init__()
        self.after = after
        self.checks = 0

    def throw_if_cancelled(self):
        self.checks += 1
        if self.checks > self.after:
            self.cancel()
        super().throw_if_cancelled()


class CancellationTests(unittest.TestCase):
    def test_cancelled_staging_leaves_state_untouched(self):
        e = Engine()
        e.apply(sources={"M": "module M;\nconst A = 1;\n", "N": "module N;\nconst X = 7;\n"})
        before = dict(e.artifacts())
        before_diags = [d.format() for d in e.diagnostics]
        before_count = e.eval_count

        token = _AutoCancel(after=1)
        with self.assertRaises(Cancelled):
            e.apply(sources={
                "M": "module M;\nconst A = 100;\nconst B = A + 1;\n",
                "N": "module N;\nconst X = 70;\nconst Y = X + 1;\n",
            }, token=token)

        self.assertEqual(e.artifacts(), before)
        self.assertEqual([d.format() for d in e.diagnostics], before_diags)
        self.assertEqual(e.eval_count, before_count)
        self.assertEqual(e.sources["M"], "module M;\nconst A = 1;\n")

    def test_retry_after_cancel_converges(self):
        e = Engine()
        e.apply(sources={"M": "module M;\nconst A = 1;\n"})
        new = "module M;\nconst A = 100;\nconst B = A + 1;\n"
        token = _AutoCancel(after=1)
        with self.assertRaises(Cancelled):
            e.apply(sources={"M": new}, token=token)
        r = e.apply(sources={"M": new})
        self.assertTrue(r.ok)
        self.assertEqual(e.artifact("M", "B"), 101)


class CheckpointTests(unittest.TestCase):
    def test_restore_is_consistent_and_remains_incremental(self):
        e = Engine()
        e.apply(sources={
            "M": "module M;\nconst a = 1;\nconst b = a + 1;\n",
            "N": "module N;\nconst x = 10;\nconst y = x * 2;\n",
            "P": "module P;\nconst q = M.b + 1;\n",
        })
        e.apply(flags={"trace": True})  # no-op flag change must not invalidate
        self.assertEqual(e.last_evaluated, [])

        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "ckpt.json")
            e.save_checkpoint(path)
            with open(path, encoding="utf-8") as fh:
                raw = json.loads(fh.read())
            self.assertEqual(raw["version"], 1)
            restored = Engine.load_checkpoint(path)

        self.assertEqual(restored.artifacts(), e.artifacts())
        self.assertEqual([d.format() for d in restored.diagnostics],
                         [d.format() for d in e.diagnostics])
        self.assertEqual(restored.eval_count, e.eval_count)

        change = "module M;\nconst a = 5;\nconst b = a + 1;\n"
        r1 = e.apply(sources={"M": change})
        r2 = restored.apply(sources={"M": change})
        self.assertEqual(r1.evaluated, r2.evaluated)
        self.assertEqual(r1.reused, r2.reused)
        self.assertEqual(restored.artifacts(), e.artifacts())
        self.assertEqual(restored.artifact("P", "q"), 7)
        self.assertNotIn("N.x", r2.evaluated)

    def test_checkpoint_preserves_error_state_and_cancel_consistency(self):
        e = Engine()
        e.apply(sources={"M": TOGGLE_SRC})
        e.apply(flags={"fast": True})
        self.assertTrue(any(d.code == "cycle" for d in e.diagnostics))
        self.assertEqual(e.artifact("M", "A"), 3)

        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "ckpt.json")
            e.save_checkpoint(path)
            restored = Engine.load_checkpoint(path)

        self.assertEqual([d.format() for d in restored.diagnostics],
                         [d.format() for d in e.diagnostics])
        self.assertEqual(restored.artifacts(), e.artifacts())

        # cancelling an attempt must match the checkpointed, last-good state
        token = _AutoCancel(after=1)
        with self.assertRaises(Cancelled):
            restored.apply(flags={"fast": False}, token=token)
        self.assertEqual(restored.artifacts(), e.artifacts())
        self.assertEqual([d.format() for d in restored.diagnostics],
                         [d.format() for d in e.diagnostics])

    def test_enum_and_bigint_round_trip(self):
        e = Engine()
        big = str(1 << 130)
        e.apply(sources={"M": f"module M;\nenum E {{ A, B = 9 }}\nconst x = E.B;\nconst y = {big};\n"})
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "ckpt.json")
            e.save_checkpoint(path)
            restored = Engine.load_checkpoint(path)
        self.assertEqual(restored.artifact("M", "x"), e.artifact("M", "x"))
        self.assertEqual(restored.artifact("M", "y"), 1 << 130)


if __name__ == "__main__":
    unittest.main()
