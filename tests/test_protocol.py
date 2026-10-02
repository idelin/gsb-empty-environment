import unittest

from secure_sum_sim.protocol import (
    Party,
    RejectionError,
    Round,
    RoundStatus,
    run_round,
)
from secure_sum_sim.sharing import DEFAULT_PRIME, split_secret
from secure_sum_sim.verification import check_conservation

INPUTS = {"A": 10, "B": 20, "C": 30, "D": 40, "E": 50}


def make_round(round_id="r1", threshold=3):
    rnd = Round(round_id, threshold, DEFAULT_PRIME)
    for pid, v in INPUTS.items():
        rnd.commit(Party(pid, v))
    rnd.close_commitment()
    return rnd


def make_recovery_round(dropped=("D",)):
    rnd = make_round()
    for pid in rnd.committed:
        for share in rnd.create_shares(pid):
            rnd.deliver_share(pid, share)
    rnd.close_sharing()
    rnd.confirm_dropouts(dropped)
    return rnd


class TestRoundFlow(unittest.TestCase):
    def test_complete_no_dropout(self):
        result, _ = run_round("r1", INPUTS, 3, DEFAULT_PRIME)
        self.assertIs(result.status, RoundStatus.COMPLETE)
        self.assertEqual(result.value, sum(INPUTS.values()) % DEFAULT_PRIME)
        self.assertEqual(set(result.covered_parties), set(INPUTS))

    def test_complete_with_dropout_covers_active_only(self):
        result, _ = run_round("r1", INPUTS, 3, DEFAULT_PRIME, dropped={"D", "E"})
        self.assertIs(result.status, RoundStatus.COMPLETE)
        self.assertEqual(set(result.covered_parties), {"A", "B", "C"})
        self.assertEqual(result.value, 60 % DEFAULT_PRIME)
        self.assertTrue(check_conservation(result, INPUTS, DEFAULT_PRIME))

    def test_threshold_insufficient_no_partial_sum(self):
        result, _ = run_round("r1", INPUTS, 4, DEFAULT_PRIME, dropped={"D", "E"})
        self.assertIs(result.status, RoundStatus.INCOMPLETE)
        self.assertIsNone(result.value)
        self.assertIsNone(result.covered_parties)

    def test_all_drop_is_incomplete(self):
        result, _ = run_round("r1", INPUTS, 2, DEFAULT_PRIME, dropped=set(INPUTS))
        self.assertIs(result.status, RoundStatus.INCOMPLETE)
        self.assertIsNone(result.value)

    def test_mod_p_wraparound(self):
        inputs = {"A": DEFAULT_PRIME - 1, "B": 2, "C": 0}
        result, _ = run_round("r1", inputs, 2, DEFAULT_PRIME)
        self.assertEqual(result.value, 1)


class TestRejections(unittest.TestCase):
    def test_old_round_share_rejected(self):
        rnd = make_round("r2")
        stale = split_secret(5, 5, DEFAULT_PRIME, "r1", "A", list("ABCDE"))[0]
        with self.assertRaises(RejectionError):
            rnd.deliver_share("A", stale)

    def test_duplicate_retransmission_rejected(self):
        rnd = make_round()
        share = rnd.create_shares("A")[0]
        rnd.deliver_share("A", share)
        with self.assertRaises(RejectionError):
            rnd.deliver_share("A", share)

    def test_share_from_uncommitted_rejected(self):
        rnd = make_round()
        share = split_secret(1, 5, DEFAULT_PRIME, "r1", "Z", list("ABCDE"))[0]
        with self.assertRaises(RejectionError):
            rnd.deliver_share("Z", share)

    def test_late_share_rejected_after_share_phase(self):
        rnd = make_recovery_round()
        late = split_secret(1, 5, DEFAULT_PRIME, "r1", "A", list("ABCDE"))[0]
        with self.assertRaises(RejectionError):
            rnd.deliver_share("A", late)

    def test_commit_after_close_rejected(self):
        rnd = make_round()
        with self.assertRaises(RejectionError):
            rnd.commit(Party("Z", 1))

    def test_duplicate_commit_rejected(self):
        rnd = Round("r", 2, DEFAULT_PRIME)
        rnd.commit(Party("A", 1))
        with self.assertRaises(RejectionError):
            rnd.commit(Party("A", 1))

    def test_close_sharing_requires_all_shares(self):
        rnd = make_round()
        for share in rnd.create_shares("A"):
            rnd.deliver_share("A", share)
        with self.assertRaises(RejectionError):
            rnd.close_sharing()

    def test_dropout_of_uncommitted_rejected(self):
        rnd = make_round()
        for pid in rnd.committed:
            for share in rnd.create_shares(pid):
                rnd.deliver_share(pid, share)
        rnd.close_sharing()
        with self.assertRaises(RejectionError):
            rnd.confirm_dropouts({"Z"})


class TestRecovery(unittest.TestCase):
    def test_recovery_material_only_for_dropped(self):
        rnd = make_recovery_round(dropped={"D"})
        with self.assertRaises(RejectionError):
            rnd.release_recovery_material("A", "B")  # B 未声明掉线

    def test_dropped_party_cannot_release(self):
        rnd = make_recovery_round(dropped={"D"})
        with self.assertRaises(RejectionError):
            rnd.release_recovery_material("D", "D")

    def test_duplicate_recovery_material_rejected(self):
        rnd = make_recovery_round(dropped={"D"})
        rnd.release_recovery_material("A", "D")
        with self.assertRaises(RejectionError):
            rnd.release_recovery_material("A", "D")

    def test_missing_recovery_material_incomplete(self):
        rnd = make_recovery_round(dropped={"D"})
        rnd.release_recovery_material("A", "D")  # 其余活跃拥有方未释放
        rnd.finalize_recovery()
        for h in rnd.active:
            rnd.submit_partial(h)
        result = rnd.aggregate()
        self.assertIs(result.status, RoundStatus.INCOMPLETE)
        self.assertIsNone(result.value)

    def test_dropped_party_cannot_submit_partial(self):
        rnd = make_recovery_round(dropped={"D"})
        for o in rnd.active:
            rnd.release_recovery_material(o, "D")
        rnd.finalize_recovery()
        with self.assertRaises(RejectionError):
            rnd.submit_partial("D")

    def test_missing_partial_incomplete(self):
        rnd = make_recovery_round(dropped={"D"})
        for o in rnd.active:
            rnd.release_recovery_material(o, "D")
        rnd.finalize_recovery()
        rnd.submit_partial("A")  # 其余活跃方未提交部分和
        result = rnd.aggregate()
        self.assertIs(result.status, RoundStatus.INCOMPLETE)
        self.assertIsNone(result.value)


if __name__ == "__main__":
    unittest.main()
