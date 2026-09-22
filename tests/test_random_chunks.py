import random
import unittest

from correctable_resampler import CorrectableResampler, Mode
from correctable_resampler.reference import replay_grid


class RandomChunkReferenceTest(unittest.TestCase):
    def assert_matches_reference(self, mode, seed):
        rng = random.Random(seed)
        origin = rng.choice([-5, -1, 0, 2])
        period = rng.choice(["0.5", "1", "1.25", "2"])
        max_gap = rng.choice(["1", "2", "3.5", "8"])
        resampler = CorrectableResampler(origin, period, mode, max_gap)
        events = []
        ids = []
        indices = list(range(-8, 13))
        resampler.watch(indices)

        for number in range(18):
            sample_id = f"s{number}"
            time_value = rng.choice(
                [
                    "-6.0", "-5.5", "-4", "-2.5", "-1", "0",
                    "0.75", "1.5", "2", "3.25", "4", "6",
                ]
            )
            value = rng.randint(-10, 10)
            events.append(("sample", sample_id, time_value, value))
            ids.append(sample_id)
            if rng.random() < 0.18 and len(ids) > 2:
                target = ids[rng.randrange(len(ids) - 1)]
                if target not in [event[1] for event in events if event[0] == "retract"]:
                    events.append(("retract", target))
            if rng.random() < 0.16:
                events.append(("breakpoint", rng.choice(["-2", "0", "1.5", "3.25", "5"])))

        cursor = 0
        while cursor < len(events):
            step = rng.randint(1, 4)
            chunk = events[cursor : cursor + step]
            cursor += step
            for event in chunk:
                if event[0] == "sample":
                    resampler.ingest(event[1], event[2], event[3])
                elif event[0] == "retract":
                    try:
                        resampler.retract(event[1])
                    except KeyError:
                        pass
                else:
                    resampler.add_breakpoint(event[1])

            reference = replay_grid(
                events[:cursor], origin, period, mode, max_gap, indices
            )
            for index in indices:
                actual = resampler.point(index)
                expected = reference[index]
                self.assertEqual(actual.time, expected.time)
                self.assertEqual(actual.mode.value, expected.mode)
                self.assertEqual(actual.basis, expected.basis)
                self.assertEqual(actual.valid, expected.valid)
                self.assertEqual(actual.value, expected.value)
                self.assertEqual(actual.sample_ids, expected.sample_ids)
                self.assertEqual(
                    None if actual.reason is None else actual.reason.value,
                    expected.reason,
                )

    def test_linear_random_event_chunks(self):
        for seed in range(30):
            self.assert_matches_reference(Mode.LINEAR.value, seed)

    def test_step_random_event_chunks(self):
        for seed in range(30, 60):
            self.assert_matches_reference(Mode.STEP.value, seed)


if __name__ == "__main__":
    unittest.main()
