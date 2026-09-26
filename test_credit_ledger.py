import json
import random
import subprocess
import sys
import threading
import unittest

from credit_ledger import Ledger, LedgerError


def make_ledger(parent_limit=10, children=("a", "b"), child_limit=10):
    ledger = Ledger(parent_limit)
    for name in children:
        ledger.add_child(name, child_limit)
    return ledger


class AcceptanceTest(unittest.TestCase):
    def test_concurrent_reserve_at_most_one_wins_then_release_allows_other(self):
        for round_no in range(50):
            ledger = make_ledger(parent_limit=10)
            barrier = threading.Barrier(2)
            results = {}

            def try_reserve(child):
                barrier.wait()
                try:
                    results[child] = ledger.reserve(child, 6, key=f"r-{child}-{round_no}")
                except LedgerError as exc:
                    results[child] = exc

            threads = [threading.Thread(target=try_reserve, args=(c,)) for c in ("a", "b")]
            for t in threads:
                t.start()
            for t in threads:
                t.join()

            winners = [c for c in ("a", "b") if isinstance(results[c], dict)]
            losers = [c for c in ("a", "b") if isinstance(results[c], LedgerError)]
            self.assertEqual(len(winners), 1, f"round {round_no}: {results}")
            self.assertEqual(len(losers), 1)
            self.assertEqual(losers and results[losers[0]].code, "parent_limit_exceeded")

            winner, loser = winners[0], losers[0]
            commit = ledger.commit(f"r-{winner}-{round_no}", 4, key=f"c-{round_no}")
            self.assertEqual(commit["committed"], 4)
            self.assertEqual(commit["released"], 2)

            retry = ledger.reserve(loser, 6, key=f"r2-{loser}-{round_no}")
            self.assertTrue(retry["ok"])

            snap = ledger.snapshot()
            self.assertEqual(snap["parent"]["committed"], 4)
            self.assertEqual(snap["parent"]["reserved"], 6)
            self.assertEqual(snap["parent"]["available"], 0)


class IdempotencyTest(unittest.TestCase):
    def test_reserve_replay_returns_same_result_without_double_counting(self):
        ledger = make_ledger()
        first = ledger.reserve("a", 6, key="k1")
        for _ in range(5):
            self.assertEqual(ledger.reserve("a", 6, key="k1"), first)
        snap = ledger.snapshot()
        self.assertEqual(snap["parent"]["reserved"], 6)
        self.assertEqual(len(snap["reservations"]), 1)

    def test_commit_and_cancel_replay(self):
        ledger = make_ledger()
        ledger.reserve("a", 6, key="r1")
        commit = ledger.commit("r1", 4, key="c1")
        self.assertEqual(ledger.commit("r1", 4, key="c1"), commit)
        self.assertEqual(ledger.snapshot()["parent"]["committed"], 4)

        ledger.reserve("b", 5, key="r2")
        cancel = ledger.cancel("r2", key="x1")
        self.assertEqual(ledger.cancel("r2", key="x1"), cancel)
        self.assertEqual(ledger.snapshot()["parent"]["reserved"], 0)

    def test_idempotency_keys_are_namespaced_per_operation(self):
        ledger = make_ledger()
        ledger.reserve("a", 6, key="shared")
        commit = ledger.commit("shared", 6, key="shared")
        self.assertTrue(commit["ok"])
        self.assertEqual(ledger.snapshot()["parent"]["committed"], 6)


class AtomicityTest(unittest.TestCase):
    def test_failed_reserve_leaves_no_partial_entry_and_may_be_retried(self):
        ledger = make_ledger(parent_limit=10, child_limit=100)
        with self.assertRaises(LedgerError) as ctx:
            ledger.reserve("a", 11, key="r1")
        self.assertEqual(ctx.exception.code, "parent_limit_exceeded")
        snap = ledger.snapshot()
        self.assertEqual(snap["reservations"], {})
        self.assertEqual(snap["parent"]["reserved"], 0)
        self.assertEqual(snap["children"]["a"]["reserved"], 0)

        retry = ledger.reserve("a", 6, key="r1")
        self.assertTrue(retry["ok"])
        self.assertEqual(ledger.snapshot()["parent"]["reserved"], 6)

    def test_failed_commit_leaves_reservation_intact(self):
        ledger = make_ledger()
        ledger.reserve("a", 6, key="r1")
        with self.assertRaises(LedgerError) as ctx:
            ledger.commit("r1", 7, key="c1")
        self.assertEqual(ctx.exception.code, "exceeds_reserved")
        snap = ledger.snapshot()
        self.assertEqual(snap["reservations"]["r1"]["status"], "active")
        self.assertEqual(snap["parent"]["reserved"], 6)
        self.assertEqual(snap["parent"]["committed"], 0)

        ok = ledger.commit("r1", 6, key="c1")
        self.assertTrue(ok["ok"])

    def test_terminal_state_errors(self):
        ledger = make_ledger()
        ledger.reserve("a", 6, key="r1")
        ledger.commit("r1", 6, key="c1")
        with self.assertRaises(LedgerError):
            ledger.commit("r1", 1, key="c2")
        with self.assertRaises(LedgerError):
            ledger.cancel("r1", key="x1")
        with self.assertRaises(LedgerError):
            ledger.commit("missing", 1, key="c3")
        with self.assertRaises(LedgerError):
            ledger.cancel("missing", key="x2")
        with self.assertRaises(LedgerError):
            ledger.reserve("a", -1, key="neg")
        with self.assertRaises(LedgerError):
            ledger.reserve("nobody", 1, key="unk")


class LimitDecreaseTest(unittest.TestCase):
    def test_parent_limit_decrease_is_not_retroactive(self):
        ledger = make_ledger(parent_limit=10)
        ledger.reserve("a", 6, key="r1")
        ledger.commit("r1", 6, key="c1")

        ledger.set_parent_limit(5)
        snap = ledger.snapshot()
        self.assertEqual(snap["parent"]["committed"], 6)
        self.assertEqual(snap["parent"]["available"], -1)

        with self.assertRaises(LedgerError) as ctx:
            ledger.reserve("b", 1, key="r2")
        self.assertEqual(ctx.exception.code, "parent_limit_exceeded")

        ledger.set_parent_limit(10)
        self.assertTrue(ledger.reserve("b", 4, key="r2")["ok"])
        with self.assertRaises(LedgerError):
            ledger.reserve("b", 1, key="r3")

    def test_child_limit_enforced_independently(self):
        ledger = Ledger(100)
        ledger.add_child("a", 5)
        ledger.add_child("b", 50)
        with self.assertRaises(LedgerError) as ctx:
            ledger.reserve("a", 6, key="r1")
        self.assertEqual(ctx.exception.code, "child_limit_exceeded")
        self.assertTrue(ledger.reserve("b", 6, key="r2")["ok"])


class CancelTest(unittest.TestCase):
    def test_cancel_releases_full_amount(self):
        ledger = make_ledger()
        ledger.reserve("a", 6, key="r1")
        result = ledger.cancel("r1", key="x1")
        self.assertEqual(result["released"], 6)
        snap = ledger.snapshot()
        self.assertEqual(snap["parent"]["reserved"], 0)
        self.assertEqual(snap["parent"]["available"], 10)
        self.assertTrue(ledger.reserve("b", 10, key="r2")["ok"])

    def test_commit_zero_releases_everything(self):
        ledger = make_ledger()
        ledger.reserve("a", 6, key="r1")
        result = ledger.commit("r1", 0, key="c1")
        self.assertEqual(result["released"], 6)
        self.assertEqual(ledger.snapshot()["parent"]["available"], 10)


class StressTest(unittest.TestCase):
    def test_random_concurrent_ops_preserve_invariants(self):
        ledger = Ledger(100)
        children = ["c0", "c1", "c2", "c3"]
        for name in children:
            ledger.add_child(name, 100)
        barrier = threading.Barrier(8)
        errors = []

        def worker(seed):
            rng = random.Random(seed)
            barrier.wait()
            for i in range(300):
                child = rng.choice(children)
                action = rng.random()
                try:
                    if action < 0.5:
                        ledger.reserve(child, rng.randint(0, 30), key=f"r-{seed}-{i}")
                    elif action < 0.75:
                        rid = f"r-{seed}-{rng.randint(0, i)}"
                        amount = ledger.reservations.get(rid, {}).get("amount", 10)
                        ledger.commit(rid, rng.randint(0, amount + 5), key=f"c-{seed}-{i}")
                    else:
                        ledger.cancel(f"r-{seed}-{rng.randint(0, i)}", key=f"x-{seed}-{i}")
                except LedgerError:
                    pass
                except Exception as exc:  # noqa: BLE001
                    errors.append(exc)

        threads = [threading.Thread(target=worker, args=(s,)) for s in range(8)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        self.assertEqual(errors, [])

        snap = ledger.snapshot()
        parent = snap["parent"]
        self.assertGreaterEqual(parent["reserved"], 0)
        self.assertGreaterEqual(parent["committed"], 0)
        self.assertLessEqual(parent["reserved"] + parent["committed"], parent["limit"])
        self.assertEqual(
            parent["reserved"],
            sum(c["reserved"] for c in snap["children"].values()),
        )
        self.assertEqual(
            parent["committed"],
            sum(c["committed"] for c in snap["children"].values()),
        )
        active = [r for r in snap["reservations"].values() if r["status"] == "active"]
        self.assertEqual(parent["reserved"], sum(r["amount"] for r in active))
        for name, child in snap["children"].items():
            own = [r for r in active if r["child"] == name]
            self.assertEqual(child["reserved"], sum(r["amount"] for r in own))
            self.assertLessEqual(child["reserved"] + child["committed"], child["limit"])


class CliTest(unittest.TestCase):
    def test_cli_roundtrip(self):
        script = "\n".join([
            json.dumps({"op": "init", "parent_limit": 10}),
            json.dumps({"op": "add_child", "child": "a", "limit": 100}),
            json.dumps({"op": "add_child", "child": "b", "limit": 100}),
            json.dumps({"op": "reserve", "child": "a", "amount": 6, "key": "r1"}),
            json.dumps({"op": "reserve", "child": "b", "amount": 6, "key": "r2"}),
            json.dumps({"op": "commit", "reservation": "r1", "amount": 4, "key": "c1"}),
            json.dumps({"op": "reserve", "child": "b", "amount": 6, "key": "r2"}),
            json.dumps({"op": "reserve", "child": "b", "amount": 6, "key": "r3"}),
            json.dumps({"op": "state"}),
            "not json",
        ]) + "\n"
        proc = subprocess.run(
            [sys.executable, "credit_ledger.py"],
            input=script, capture_output=True, text=True, timeout=30,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        lines = [json.loads(line) for line in proc.stdout.strip().splitlines()]
        self.assertEqual(len(lines), 10)
        self.assertTrue(lines[3]["ok"])
        self.assertEqual(lines[4], {"ok": False, "error": "parent_limit_exceeded"})
        self.assertEqual(lines[5]["released"], 2)
        self.assertTrue(lines[6]["ok"])
        self.assertEqual(lines[7], {"ok": False, "error": "parent_limit_exceeded"})
        self.assertEqual(lines[8]["parent"]["available"], 0)
        self.assertEqual(lines[9], {"ok": False, "error": "bad_request"})


if __name__ == "__main__":
    unittest.main()
