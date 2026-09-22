import random
import unittest

from disordered_counter import CounterAnalyzer, Sample, SourceSpec
from disordered_counter.intervals import classify_pair


def build_scenario(rng: random.Random):
    config = {
        "a": SourceSpec(width=4, max_gap=10),
        "b": SourceSpec(width=8, max_gap=15),
    }
    samples: list[Sample] = []

    for source, spec in config.items():
        modulus = spec.modulus
        max_increment_per_time = (modulus - 1) // spec.max_gap
        event_time = rng.randint(0, 4)
        count = rng.randrange(modulus)
        seq = 0
        reset = True
        for ordinal in range(22):
            samples.append(
                Sample(
                    sample_id=f"{source}-{ordinal}",
                    source=source,
                    event_time=event_time,
                    count=count,
                    seq=seq,
                    reset=reset,
                )
            )

            gap = rng.randint(1, 18)
            event_time += gap
            seq += 1
            reset = rng.random() < 0.08
            increment = 0 if reset else rng.randrange(max_increment_per_time * gap + 1)
            count = rng.randrange(modulus) if reset else (count + increment) % modulus

    rng.shuffle(samples)
    return config, samples


class RandomConsistencyTests(unittest.TestCase):
    def test_random_insertions_retractions_and_reference(self) -> None:
        rng = random.Random(20260922)
        for scenario in range(40):
            config, samples = build_scenario(rng)
            engine = CounterAnalyzer(config, f"random-{scenario}")
            samples_by_id = {sample.sample_id: sample for sample in samples}
            pending = set(samples_by_id)
            inserted: set[str] = set()

            for step in range(len(samples) * 3):
                if not pending and not inserted:
                    break

                if pending and (not inserted or rng.random() < 0.72):
                    sample_id = rng.choice(tuple(pending))
                    sample = samples_by_id[sample_id]
                    old_delta, part_deltas = self._insert_conservation(engine, sample)
                    engine.add_sample(sample)
                    inserted.add(sample_id)
                    pending.remove(sample_id)

                    if old_delta is not None and all(
                        value is not None for value in part_deltas
                    ):
                        self.assertEqual(
                            sum(part_deltas),
                            old_delta,
                            msg=(scenario, step, sample_id),
                        )
                else:
                    removable = tuple(
                        sample_id
                        for sample_id in inserted
                        if not samples_by_id[sample_id].reset
                    )
                    if removable:
                        sample_id = rng.choice(removable)
                        sample = samples_by_id[sample_id]
                        removed, replacement = self._retract_conservation(
                            engine,
                            sample,
                        )
                        engine.retract_sample(sample_id)
                        inserted.remove(sample_id)
                        pending.add(sample_id)

                        if replacement is not None and all(
                            value is not None for value in removed
                        ):
                            self.assertEqual(
                                replacement,
                                sum(removed),
                                msg=(scenario, step, sample_id),
                            )

                self.assertTrue(engine.matches_reference(), msg=(scenario, step))
                self.assertEqual(
                    engine.summary().known_delta,
                    sum(
                        interval.delta
                        for interval in engine.recompute_reference()
                        if interval.status == "definite"
                    ),
                    msg=(scenario, step),
                )

    @staticmethod
    def _ordered_source_samples(engine: CounterAnalyzer, source: str):
        return [sample for sample in engine.active_samples() if sample.source == source]

    @staticmethod
    def _future_index(source_samples: list[Sample], sample: Sample) -> int:
        candidate = (sample.event_time, sample.seq, sample.sample_id)
        for index, item in enumerate(source_samples):
            if candidate < (item.event_time, item.seq, item.sample_id):
                return index
        return len(source_samples)

    def _insert_conservation(self, engine: CounterAnalyzer, sample: Sample):
        source_samples = self._ordered_source_samples(engine, sample.source)
        index = self._future_index(source_samples, sample)
        old_delta = None
        if 0 < index < len(source_samples):
            old = classify_pair(
                engine.config,
                source_samples[index - 1],
                source_samples[index],
            )
            old_delta = old.delta

        parts: list[int | None] = []
        if index > 0:
            parts.append(
                classify_pair(
                    engine.config,
                    source_samples[index - 1],
                    sample,
                ).delta
            )
        if index < len(source_samples):
            parts.append(
                classify_pair(
                    engine.config,
                    sample,
                    source_samples[index],
                ).delta
            )
        return old_delta, tuple(parts)

    @staticmethod
    def _interval_delta(engine: CounterAnalyzer, left_id: str, right_id: str):
        return next(
            interval.delta
            for interval in engine.active_intervals
            if interval.left_sample_id == left_id
            and interval.right_sample_id == right_id
        )

    def _retract_conservation(self, engine: CounterAnalyzer, sample: Sample):
        source_samples = self._ordered_source_samples(engine, sample.source)
        index = next(
            index
            for index, item in enumerate(source_samples)
            if item.sample_id == sample.sample_id
        )
        removed: list[int | None] = []
        if index > 0:
            removed.append(
                self._interval_delta(
                    engine,
                    source_samples[index - 1].sample_id,
                    sample.sample_id,
                )
            )
        if index + 1 < len(source_samples):
            removed.append(
                self._interval_delta(
                    engine,
                    sample.sample_id,
                    source_samples[index + 1].sample_id,
                )
            )

        replacement = None
        if 0 < index < len(source_samples) - 1:
            replacement = classify_pair(
                engine.config,
                source_samples[index - 1],
                source_samples[index + 1],
            ).delta
        return tuple(removed), replacement


if __name__ == "__main__":
    unittest.main()
