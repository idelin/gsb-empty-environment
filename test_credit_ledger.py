"""credit_ledger 的单元测试与竞争测试。所有数据均为合成数据。"""
import random
import threading
import unittest

from credit_ledger import CreditLedger, LedgerError


class BasicOperationTests(unittest.TestCase):
    def setUp(self):
        self.ledger = CreditLedger(10)
        self.ledger.add_child("a", 10)
        self.ledger.add_child("b", 10)

    def test_reserve_commit_partial_releases_difference(self):
        r = self.ledger.reserve("a", 6, "k1")
        self.assertTrue(r["ok"])
        c = self.ledger.commit(r["reservation"], 4, "k2")
        self.assertTrue(c["ok"])
        self.assertEqual(c["committed"], 4)
        self.assertEqual(c["released"], 2)
        st = self.ledger.state()
        self.assertEqual(st["parent_committed"], 4)
        self.assertEqual(st["parent_reserved"], 0)
        self.assertEqual(st["parent_available"], 6)

    def test_acceptance_scenario(self):
        # 父额10：a 预留6后 b 预留6失败；a 提交4释放2后，b 可预留6。
        ra = self.ledger.reserve("a", 6, "ra")
        self.assertTrue(ra["ok"])
        rb1 = self.ledger.reserve("b", 6, "rb1")
        self.assertFalse(rb1["ok"])
        self.assertEqual(rb1["error"], "parent_limit_exceeded")
        self.ledger.commit(ra["reservation"], 4, "ca")
        rb2 = self.ledger.reserve("b", 6, "rb2")
        self.assertTrue(rb2["ok"])
        st = self.ledger.state()
        self.assertEqual(st["parent_available"], 0)

    def test_commit_exceeding_reserved_fails_without_side_effects(self):
        r = self.ledger.reserve("a", 6, "k1")
        before = self.ledger.state()
        c = self.ledger.commit(r["reservation"], 7, "k2")
        self.assertFalse(c["ok"])
        self.assertEqual(c["error"], "commit_exceeds_reserved")
        self.assertEqual(self.ledger.state(), before)

    def test_commit_full_and_zero_amount(self):
        r1 = self.ledger.reserve("a", 5, "k1")
        c1 = self.ledger.commit(r1["reservation"], 5, "c1")
        self.assertTrue(c1["ok"])
        self.assertEqual(c1["released"], 0)
        r2 = self.ledger.reserve("a", 3, "k2")
        c2 = self.ledger.commit(r2["reservation"], 0, "c2")
        self.assertTrue(c2["ok"])
        self.assertEqual(c2["released"], 3)
        self.assertEqual(self.ledger.state()["parent_committed"], 5)

    def test_cancel_releases_reservation(self):
        r = self.ledger.reserve("a", 6, "k1")
        self.assertEqual(self.ledger.state()["parent_available"], 4)
        c = self.ledger.cancel(r["reservation"], "k2")
        self.assertTrue(c["ok"])
        self.assertEqual(c["released"], 6)
        self.assertEqual(self.ledger.state()["parent_available"], 10)

    def test_child_limit_enforced(self):
        small = CreditLedger(100)
        small.add_child("a", 5)
        self.assertTrue(small.reserve("a", 5, "k1")["ok"])
        r = small.reserve("a", 1, "k2")
        self.assertFalse(r["ok"])
        self.assertEqual(r["error"], "child_limit_exceeded")

    def test_invalid_amounts_rejected(self):
        for bad in (0, -1, 1.5, "6", True, None):
            r = self.ledger.reserve("a", bad, f"bad-{bad!r}")
            self.assertFalse(r["ok"], bad)
            self.assertEqual(r["error"], "invalid_amount")
        self.assertEqual(self.ledger.state()["reservations"], {})

    def test_unknown_child_and_reservation(self):
        self.assertFalse(self.ledger.reserve("ghost", 1, "k1")["ok"])
        self.assertEqual(self.ledger.commit("rsv_nope", 1, "k2")["error"], "reservation_not_found")
        self.assertEqual(self.ledger.cancel("rsv_nope", "k3")["error"], "reservation_not_found")

    def test_duplicate_child_rejected(self):
        with self.assertRaises(LedgerError):
            self.ledger.add_child("a", 5)


class IdempotencyTests(unittest.TestCase):
    def setUp(self):
        self.ledger = CreditLedger(10)
        self.ledger.add_child("a", 10)

    def test_reserve_replay_returns_same_result_without_double_effect(self):
        r1 = self.ledger.reserve("a", 6, "k1")
        r2 = self.ledger.reserve("a", 6, "k1")
        self.assertEqual(r1, r2)
        st = self.ledger.state()
        self.assertEqual(len(st["reservations"]), 1)
        self.assertEqual(st["parent_reserved"], 6)

    def test_commit_replay_commits_once(self):
        r = self.ledger.reserve("a", 6, "k1")
        c1 = self.ledger.commit(r["reservation"], 4, "k2")
        c2 = self.ledger.commit(r["reservation"], 4, "k2")
        self.assertEqual(c1, c2)
        self.assertEqual(self.ledger.state()["parent_committed"], 4)

    def test_cancel_replay_releases_once(self):
        r = self.ledger.reserve("a", 6, "k1")
        x1 = self.ledger.cancel(r["reservation"], "k2")
        x2 = self.ledger.cancel(r["reservation"], "k2")
        self.assertEqual(x1, x2)
        self.assertEqual(self.ledger.state()["parent_available"], 10)

    def test_failed_result_is_also_memoized(self):
        f1 = self.ledger.reserve("a", 99, "k1")
        f2 = self.ledger.reserve("a", 99, "k1")
        self.assertFalse(f1["ok"])
        self.assertEqual(f1, f2)


class ParentLimitDecreaseTests(unittest.TestCase):
    def test_decrease_is_not_retroactive_but_blocks_new_reservations(self):
        ledger = CreditLedger(10)
        ledger.add_child("a", 10)
        ledger.add_child("b", 10)
        r = ledger.reserve("a", 6, "k1")
        ledger.commit(r["reservation"], 6, "k2")
        out = ledger.set_parent_limit(4)
        self.assertTrue(out["ok"])
        st = ledger.state()
        # 已提交 6 不被撤销，可用额为负。
        self.assertEqual(st["parent_committed"], 6)
        self.assertEqual(st["parent_available"], -2)
        # 新增预留被拒绝，且状态不变。
        before = ledger.state()
        self.assertFalse(ledger.reserve("b", 1, "k3")["ok"])
        self.assertEqual(ledger.state(), before)
        # 上调后可恢复预留。
        ledger.set_parent_limit(10)
        self.assertTrue(ledger.reserve("b", 4, "k4")["ok"])

    def test_decrease_does_not_revoke_existing_reservations(self):
        ledger = CreditLedger(10)
        ledger.add_child("a", 10)
        r = ledger.reserve("a", 8, "k1")
        ledger.set_parent_limit(5)
        st = ledger.state()
        self.assertEqual(st["parent_reserved"], 8)
        # 既有预留仍可提交。
        c = ledger.commit(r["reservation"], 8, "k2")
        self.assertTrue(c["ok"])
        self.assertEqual(ledger.state()["parent_committed"], 8)


class FailureAtomicityTests(unittest.TestCase):
    def test_failed_requests_leave_no_partial_entries(self):
        ledger = CreditLedger(10)
        ledger.add_child("a", 4)
        ledger.add_child("b", 10)
        r = ledger.reserve("b", 6, "good")
        before = ledger.state()
        # 各类失败：超子额度、超父额度、未知预留、超预留提交、未知子账户。
        self.assertFalse(ledger.reserve("a", 5, "f1")["ok"])   # 子额度仅4
        self.assertFalse(ledger.reserve("b", 5, "f2")["ok"])   # 父仅剩4
        self.assertFalse(ledger.commit("rsv_ghost", 1, "f3")["ok"])
        self.assertFalse(ledger.commit(r["reservation"], 7, "f4")["ok"])
        self.assertFalse(ledger.cancel("rsv_ghost", "f5")["ok"])
        self.assertFalse(ledger.reserve("ghost", 1, "f6")["ok"])
        self.assertEqual(ledger.state(), before)


class ConcurrencyTests(unittest.TestCase):
    def test_concurrent_reserve_at_most_one_winner(self):
        # 验收边界：父额10，两个子账户并发各预留6，每轮至多一个成功。
        for round_no in range(200):
            ledger = CreditLedger(10, debug=True)
            ledger.add_child("a", 10)
            ledger.add_child("b", 10)
            barrier = threading.Barrier(2)
            results = []

            def work(child, key):
                barrier.wait()
                results.append(ledger.reserve(child, 6, key))

            threads = [
                threading.Thread(target=work, args=("a", f"a-{round_no}")),
                threading.Thread(target=work, args=("b", f"b-{round_no}")),
            ]
            for t in threads:
                t.start()
            for t in threads:
                t.join()
            winners = [r for r in results if r["ok"]]
            self.assertEqual(len(winners), 1, f"round {round_no}: {results}")
            st = ledger.state()
            self.assertEqual(st["parent_reserved"], 6)
            self.assertEqual(st["parent_available"], 4)

    def test_concurrent_reserve_then_commit_then_other_can_reserve(self):
        # 验收边界续：胜者提交4释放2后，另一子账户可预留6。
        for round_no in range(100):
            ledger = CreditLedger(10, debug=True)
            ledger.add_child("a", 10)
            ledger.add_child("b", 10)
            barrier = threading.Barrier(2)
            results = []

            def work(child, key):
                barrier.wait()
                results.append((child, ledger.reserve(child, 6, key)))

            threads = [
                threading.Thread(target=work, args=("a", f"a-{round_no}")),
                threading.Thread(target=work, args=("b", f"b-{round_no}")),
            ]
            for t in threads:
                t.start()
            for t in threads:
                t.join()
            winner_child, winner = next((c, r) for c, r in results if r["ok"])
            loser_child = "b" if winner_child == "a" else "a"
            c = ledger.commit(winner["reservation"], 4, f"c-{round_no}")
            self.assertTrue(c["ok"])
            self.assertEqual(c["released"], 2)
            r2 = ledger.reserve(loser_child, 6, f"r2-{round_no}")
            self.assertTrue(r2["ok"], f"round {round_no}")
            st = ledger.state()
            self.assertEqual(st["parent_committed"], 4)
            self.assertEqual(st["parent_reserved"], 6)
            self.assertEqual(st["parent_available"], 0)

    def test_mixed_operations_stress_invariants_hold(self):
        # 多线程混合 reserve/commit/cancel：无异常、最终状态满足全部额度约束。
        ledger = CreditLedger(50, debug=True)
        children = [f"c{i}" for i in range(5)]
        for name in children:
            ledger.add_child(name, 30)
        errors = []

        def worker(seed):
            rng = random.Random(seed)
            for i in range(300):
                key = f"{seed}-{i}"
                child = rng.choice(children)
                try:
                    roll = rng.random()
                    if roll < 0.5:
                        ledger.reserve(child, rng.randint(1, 10), key)
                    else:
                        st = ledger.state()
                        rids = [rid for rid, r in st["reservations"].items()
                                if r["child"] == child]
                        if not rids:
                            continue
                        rid = rng.choice(rids)
                        if roll < 0.8:
                            ledger.commit(rid, rng.randint(0, st["reservations"][rid]["amount"]), key)
                        else:
                            ledger.cancel(rid, key)
                except Exception as exc:  # 业务结果走返回值，异常即缺陷
                    errors.append(exc)

        threads = [threading.Thread(target=worker, args=(s,)) for s in range(8)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        self.assertEqual(errors, [])
        st = ledger.state()
        self.assertGreaterEqual(st["parent_available"], 0)
        self.assertEqual(
            st["parent_reserved"],
            sum(r["amount"] for r in st["reservations"].values()),
        )
        for name in children:
            self.assertGreaterEqual(st["children"][name]["available"], 0)

    def test_concurrent_commit_same_reservation_only_one_wins(self):
        ledger = CreditLedger(10, debug=True)
        ledger.add_child("a", 10)
        r = ledger.reserve("a", 6, "k1")
        barrier = threading.Barrier(4)
        results = []

        def work(i):
            barrier.wait()
            results.append(ledger.commit(r["reservation"], 6, f"c-{i}"))

        threads = [threading.Thread(target=work, args=(i,)) for i in range(4)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        self.assertEqual(sum(1 for x in results if x["ok"]), 1)
        self.assertEqual(ledger.state()["parent_committed"], 6)


if __name__ == "__main__":
    unittest.main()
