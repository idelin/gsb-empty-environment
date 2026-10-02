"""mpc_sum_sim 的 unittest 测试套件。

说明：本套件验证的是【模拟器状态机】与【有限域守恒性质】，
不构成任何密码学安全性或隐私性的形式化证明。
"""

from __future__ import annotations

import itertools
import random
import unittest

from mpc_sum_sim import (
    DEFAULT_PRIME,
    DuplicateShareError,
    IncompleteSharesError,
    Phase,
    PhaseError,
    ProtocolError,
    Round,
    RoundStatus,
    Share,
    UnauthorizedRecoveryError,
    UnknownPartyError,
    WrongRoundError,
    is_prime,
    reconstruct,
    share_secret,
)

PARTIES = ("A", "B", "C", "D", "E")


def run_round(inputs, dropped=(), threshold=2, prime=DEFAULT_PRIME,
              round_id=1, seed=0, skip_recovery=()):
    """跑一轮完整流程的辅助函数。skip_recovery 中的 (dealer, dropped) 不提交恢复材料。

    返回 (round_obj, result)。
    """
    parties = tuple(inputs.keys())
    rnd = Round(round_id=round_id, prime=prime, threshold=threshold,
                rng=random.Random(seed))
    rnd.commit(parties)
    for dealer, secret in inputs.items():
        rnd.deal(dealer, secret)
    rnd.close_sharing()
    for party in dropped:
        rnd.declare_dropout(party)
    rnd.close_dropout_confirmation()
    if rnd.phase is Phase.RECOVER:
        survivors = [p for p in parties if p not in dropped]
        for j in dropped:
            for i in survivors:
                if (i, j) in skip_recovery:
                    continue
                # 恢复材料 = 原始份额 s_{i->j} 的重传
                recovery_share = rnd._shares[(i, j)]
                rnd.submit_recovery(recovery_share)
    return rnd, rnd.finalize()


class TestFieldBasics(unittest.TestCase):
    def test_default_prime_is_prime(self):
        self.assertTrue(is_prime(DEFAULT_PRIME))

    def test_non_prime_rejected(self):
        with self.assertRaises(ValueError):
            Round(round_id=1, prime=2**61)  # 偶数，非素数

    def test_share_reconstruct_mod_p_consistency(self):
        rng = random.Random(42)
        for secret in (0, 1, 2, DEFAULT_PRIME - 1, DEFAULT_PRIME // 2):
            shares = share_secret(secret, 1, "A", PARTIES, DEFAULT_PRIME, rng)
            self.assertEqual(reconstruct(shares, DEFAULT_PRIME), secret)

    def test_secret_out_of_field_rejected(self):
        rng = random.Random(0)
        with self.assertRaises(ValueError):
            share_secret(DEFAULT_PRIME, 1, "A", PARTIES, DEFAULT_PRIME, rng)

    def test_reconstruct_empty_incomplete(self):
        with self.assertRaises(IncompleteSharesError):
            reconstruct([], DEFAULT_PRIME)

    def test_cross_round_reconstruct_rejected(self):
        rng = random.Random(0)
        s1 = share_secret(5, 1, "A", PARTIES, DEFAULT_PRIME, rng)
        s2 = share_secret(7, 2, "A", PARTIES, DEFAULT_PRIME, rng)
        with self.assertRaises(WrongRoundError):
            reconstruct([s1[0], s2[0]], DEFAULT_PRIME)


class TestHappyPath(unittest.TestCase):
    def test_no_dropout_sum(self):
        inputs = {"A": 10, "B": 20, "C": 30}
        _, result = run_round(inputs, dropped=(), threshold=2)
        self.assertIs(result.status, RoundStatus.COMPLETE)
        self.assertEqual(result.total, 60 % DEFAULT_PRIME)
        self.assertEqual(result.effective_parties, ("A", "B", "C"))
        self.assertEqual(result.dropped, ())

    def test_sum_wraps_mod_p(self):
        inputs = {"A": DEFAULT_PRIME - 1, "B": DEFAULT_PRIME - 2, "C": 5}
        _, result = run_round(inputs, dropped=(), threshold=2)
        self.assertEqual(result.total, (DEFAULT_PRIME - 1 + DEFAULT_PRIME - 2 + 5) % DEFAULT_PRIME)

    def test_single_dropout_with_recovery(self):
        inputs = {"A": 1, "B": 2, "C": 3, "D": 4}
        _, result = run_round(inputs, dropped=("C",), threshold=2)
        self.assertIs(result.status, RoundStatus.COMPLETE)
        self.assertEqual(result.total, 1 + 2 + 4)  # 仅幸存方输入之和
        self.assertEqual(result.effective_parties, ("A", "B", "D"))
        self.assertEqual(result.dropped, ("C",))

    def test_multiple_dropouts_with_recovery(self):
        inputs = {"A": 10, "B": 20, "C": 30, "D": 40, "E": 50}
        _, result = run_round(inputs, dropped=("B", "D"), threshold=3)
        self.assertIs(result.status, RoundStatus.COMPLETE)
        self.assertEqual(result.total, 10 + 30 + 50)
        self.assertEqual(result.effective_parties, ("A", "C", "E"))


class TestThresholdAndIncomplete(unittest.TestCase):
    def test_threshold_not_met_aborts_without_output(self):
        inputs = {"A": 1, "B": 2, "C": 3, "D": 4}
        rnd, result = run_round(inputs, dropped=("B", "C", "D"), threshold=2)
        self.assertIs(rnd.phase, Phase.ABORTED)
        self.assertIs(result.status, RoundStatus.ABORTED)
        self.assertIsNone(result.total)  # 绝不输出部分和
        self.assertEqual(result.effective_parties, ())

    def test_threshold_exactly_met_completes(self):
        inputs = {"A": 1, "B": 2, "C": 3}
        _, result = run_round(inputs, dropped=("C",), threshold=2)
        self.assertIs(result.status, RoundStatus.COMPLETE)
        self.assertEqual(result.total, 3)

    def test_missing_recovery_returns_incomplete(self):
        inputs = {"A": 1, "B": 2, "C": 3}
        _, result = run_round(inputs, dropped=("C",), threshold=2,
                              skip_recovery={("A", "C")})
        self.assertIs(result.status, RoundStatus.INCOMPLETE)
        self.assertIsNone(result.total)
        self.assertEqual(result.missing_recovery, (("A", "C"),))

    def test_incomplete_sharing_cannot_close(self):
        rnd = Round(round_id=1, threshold=1, rng=random.Random(0))
        rnd.commit(("A", "B"))
        rnd.deal("A", 5)  # B 未提交份额
        with self.assertRaises(IncompleteSharesError):
            rnd.close_sharing()

    def test_threshold_above_party_count_rejected(self):
        rnd = Round(round_id=1, threshold=4)
        with self.assertRaises(ValueError):
            rnd.commit(("A", "B", "C"))


class TestRoundIsolationAndReplay(unittest.TestCase):
    def test_old_round_share_rejected(self):
        rng = random.Random(0)
        old = share_secret(5, 1, "A", PARTIES[:3], DEFAULT_PRIME, rng)
        rnd = Round(round_id=2, threshold=1, rng=random.Random(0))
        rnd.commit(PARTIES[:3])
        with self.assertRaises(WrongRoundError):
            rnd.submit_share(old[0])

    def test_duplicate_share_rejected(self):
        rnd = Round(round_id=1, threshold=1, rng=random.Random(0))
        rnd.commit(PARTIES[:2])
        shares = rnd.deal("A", 5)
        with self.assertRaises(DuplicateShareError):
            rnd.submit_share(shares[0])  # 重传同一份额

    def test_old_round_recovery_rejected(self):
        inputs = {"A": 1, "B": 2, "C": 3}
        rnd = Round(round_id=7, threshold=2, rng=random.Random(0))
        rnd.commit(tuple(inputs))
        for dealer, secret in inputs.items():
            rnd.deal(dealer, secret)
        rnd.close_sharing()
        rnd.declare_dropout("C")
        rnd.close_dropout_confirmation()
        stale = Share(round_id=6, dealer="A", recipient="C", value=0)
        with self.assertRaises(WrongRoundError):
            rnd.submit_recovery(stale)

    def test_duplicate_recovery_rejected(self):
        inputs = {"A": 1, "B": 2, "C": 3}
        rnd = Round(round_id=1, threshold=2, rng=random.Random(0))
        rnd.commit(tuple(inputs))
        for dealer, secret in inputs.items():
            rnd.deal(dealer, secret)
        rnd.close_sharing()
        rnd.declare_dropout("C")
        rnd.close_dropout_confirmation()
        share = rnd._shares[("A", "C")]
        rnd.submit_recovery(share)
        with self.assertRaises(DuplicateShareError):
            rnd.submit_recovery(share)


class TestRecoveryAuthorization(unittest.TestCase):
    def setUp(self):
        inputs = {"A": 1, "B": 2, "C": 3, "D": 4}
        self.rnd = Round(round_id=1, threshold=2, rng=random.Random(0))
        self.rnd.commit(tuple(inputs))
        for dealer, secret in inputs.items():
            self.rnd.deal(dealer, secret)
        self.rnd.close_sharing()
        self.rnd.declare_dropout("D")
        self.rnd.close_dropout_confirmation()

    def test_recovery_for_non_dropped_rejected(self):
        share = self.rnd._shares[("A", "B")]  # B 未掉线
        with self.assertRaises(UnauthorizedRecoveryError):
            self.rnd.submit_recovery(share)

    def test_recovery_from_dropped_dealer_rejected(self):
        self.rnd2 = Round(round_id=2, threshold=2, rng=random.Random(0))
        self.rnd2.commit(("A", "B", "C"))
        for dealer, secret in {"A": 1, "B": 2, "C": 3}.items():
            self.rnd2.deal(dealer, secret)
        self.rnd2.close_sharing()
        self.rnd2.declare_dropout("C")
        self.rnd2.close_dropout_confirmation()
        share = self.rnd2._shares[("C", "C")]  # 掉线方不能提交恢复材料
        with self.assertRaises(UnauthorizedRecoveryError):
            self.rnd2.submit_recovery(share)

    def test_recovery_value_mismatch_rejected(self):
        original = self.rnd._shares[("A", "D")]
        forged = Share(original.round_id, original.dealer, original.recipient,
                       (original.value + 1) % self.rnd.prime)
        with self.assertRaises(ProtocolError):
            self.rnd.submit_recovery(forged)


class TestPhaseAndMembership(unittest.TestCase):
    def test_share_before_commit_rejected(self):
        rnd = Round(round_id=1, threshold=1)
        with self.assertRaises(PhaseError):
            rnd.submit_share(Share(1, "A", "A", 0))

    def test_share_after_sharing_closed_rejected(self):
        rnd = Round(round_id=1, threshold=1, rng=random.Random(0))
        rnd.commit(("A",))
        rnd.deal("A", 1)
        rnd.close_sharing()
        with self.assertRaises(PhaseError):
            rnd.submit_share(Share(1, "A", "A", 0))

    def test_unknown_dealer_rejected(self):
        rnd = Round(round_id=1, threshold=1, rng=random.Random(0))
        rnd.commit(("A", "B"))
        with self.assertRaises(UnknownPartyError):
            rnd.deal("Z", 5)

    def test_unknown_dropout_declaration_rejected(self):
        rnd = Round(round_id=1, threshold=1, rng=random.Random(0))
        rnd.commit(("A",))
        rnd.deal("A", 1)
        rnd.close_sharing()
        with self.assertRaises(UnknownPartyError):
            rnd.declare_dropout("Z")

    def test_commit_is_one_shot(self):
        rnd = Round(round_id=1, threshold=1)
        rnd.commit(("A",))
        with self.assertRaises(PhaseError):
            rnd.commit(("A", "B"))


class TestStateExploration(unittest.TestCase):
    """有限域状态探索：在小素数域上枚举全部输入组合与全部掉线模式，
    核验每轮输出与实际幸存参与方输入之和守恒（mod p）。"""

    def test_exhaustive_conservation_small_field(self):
        prime = 7
        parties = ("A", "B", "C")
        threshold = 2
        dropout_patterns = [()]
        for size in (1, 2, 3):
            dropout_patterns.extend(itertools.combinations(parties, size))

        complete_runs = aborted_runs = 0
        for values in itertools.product(range(prime), repeat=3):
            inputs = dict(zip(parties, values))
            for dropped in dropout_patterns:
                seed = hash((values, dropped)) & 0xFFFF
                _, result = run_round(inputs, dropped=dropped,
                                      threshold=threshold, prime=prime,
                                      round_id=1, seed=seed)
                survivors = tuple(p for p in parties if p not in dropped)
                if len(survivors) < threshold:
                    self.assertIs(result.status, RoundStatus.ABORTED)
                    self.assertIsNone(result.total)
                    aborted_runs += 1
                else:
                    self.assertIs(result.status, RoundStatus.COMPLETE)
                    expected = sum(inputs[p] for p in survivors) % prime
                    self.assertEqual(result.total, expected,
                                     msg=f"inputs={inputs} dropped={dropped}")
                    self.assertEqual(result.effective_parties, survivors)
                    complete_runs += 1
        # 7^3 输入 × 8 种掉线模式 = 2744 个状态全部覆盖
        self.assertEqual(complete_runs + aborted_runs, 7**3 * 8)
        self.assertGreater(complete_runs, 0)
        self.assertGreater(aborted_runs, 0)

    def test_share_dealing_conserves_secret_per_dealer(self):
        # 探索：小域上每个秘密值的分享-还原恒等式
        prime = 11
        rng = random.Random(7)
        for secret in range(prime):
            for round_id in (1, 2, 3):
                shares = share_secret(secret, round_id, "A", PARTIES[:4], prime, rng)
                self.assertEqual(reconstruct(shares, prime), secret)
                self.assertTrue(all(0 <= s.value < prime for s in shares))


if __name__ == "__main__":
    unittest.main()
