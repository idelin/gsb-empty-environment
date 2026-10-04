import unittest

from hmm import HMM, MISSING, NEG_INF, VersionedInference, forward_backward


def make_model():
    return HMM(
        n_states=3,
        initial=(0.5, 0.3, 0.2),
        transition=(
            (0.6, 0.3, 0.1),
            (0.2, 0.5, 0.3),
            (0.3, 0.3, 0.4),
        ),
        emission=(
            {"x": 0.7, "y": 0.2, "z": 0.1},
            {"x": 0.2, "y": 0.6, "z": 0.2},
            {"x": 0.1, "y": 0.3, "z": 0.6},
        ),
    )


OBS = ("x", "y", "z", "x", "y", "z")


class TestVersionedInference(unittest.TestCase):
    def setUp(self):
        self.m = make_model()

    def assertMatchesFresh(self, vi, obs, version=None):
        fresh = forward_backward(self.m, obs)
        self.assertAlmostEqual(
            vi.log_evidence(version), fresh.log_evidence, places=9
        )
        got = vi.posteriors(version)
        if fresh.posteriors is None:
            self.assertIsNone(got)
        else:
            for g_row, f_row in zip(got, fresh.posteriors):
                for g, f in zip(g_row, f_row):
                    self.assertAlmostEqual(g, f, places=9)

    def test_initial_version_matches_full_inference(self):
        vi = VersionedInference(self.m, OBS)
        self.assertEqual(vi.version, 0)
        self.assertMatchesFresh(vi, OBS)

    def test_revision_matches_fresh_recompute(self):
        for t in (0, 2, 5):  # boundaries and interior
            vi2 = VersionedInference(self.m, OBS)
            new_obs = list(OBS)
            new_obs[t] = "z" if OBS[t] != "z" else "x"
            vid = vi2.revise(t, new_obs[t])
            self.assertEqual(vid, 1)
            self.assertMatchesFresh(vi2, tuple(new_obs))

    def test_old_versions_are_retained(self):
        vi = VersionedInference(self.m, OBS)
        before = vi.posteriors(0)
        before_ev = vi.log_evidence(0)
        vi.revise(2, "x")
        vi.revise(4, "z")
        self.assertEqual(vi.versions(), [0, 1, 2])
        # Old queries still return the original answers.
        self.assertEqual(vi.posteriors(0)[0], before[0])
        self.assertEqual(vi.log_evidence(0), before_ev)
        self.assertEqual(vi.observations(0), OBS)
        self.assertEqual(vi.observations(1)[2], "x")
        self.assertEqual(vi.observations(2)[4], "z")

    def test_sequential_revisions_match_fresh(self):
        vi = VersionedInference(self.m, OBS)
        obs = list(OBS)
        for t, o in ((1, "z"), (3, MISSING), (4, "x")):
            obs[t] = o
            vi.revise(t, o)
        self.assertMatchesFresh(vi, tuple(obs))

    def test_revision_to_impossible_observation(self):
        vi = VersionedInference(self.m, OBS)
        vi.revise(1, "not-a-symbol")
        self.assertEqual(vi.log_evidence(), NEG_INF)
        self.assertIsNone(vi.posteriors())
        # The previous version is unaffected.
        self.assertNotEqual(vi.log_evidence(0), NEG_INF)
        self.assertIsNotNone(vi.posteriors(0))

    def test_revision_with_missing_observation(self):
        vi = VersionedInference(self.m, OBS)
        vi.revise(0, MISSING)
        obs = (MISSING,) + OBS[1:]
        self.assertMatchesFresh(vi, obs)

    def test_out_of_range_revision_rejected(self):
        vi = VersionedInference(self.m, OBS)
        with self.assertRaises(IndexError):
            vi.revise(len(OBS), "x")

    def test_unknown_version_rejected(self):
        vi = VersionedInference(self.m, OBS)
        with self.assertRaises(KeyError):
            vi.log_evidence(99)


if __name__ == "__main__":
    unittest.main()
