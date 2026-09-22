"""Run: python3.11 src/demo.py"""

from __future__ import annotations

from automata import Pipeline, compose
from samples import pipeline_stages, stopper


def main() -> None:
    stages = pipeline_stages()
    pipeline = Pipeline(stages, buffer_size=1)

    whole = ["abx"]
    split = ["a", "bx", "", ""]
    whole_result = pipeline.run_with_stats(whole)
    split_result = pipeline.run_with_stats(split)
    reference = compose(stages, whole)

    print("whole chunks:      ", whole)
    print("arbitrary chunks:  ", split)
    print("outputs:           ", whole_result.outputs)
    print("reference outputs: ", reference)
    print("peak buffers:      ", whole_result.peak_buffers)
    print("chunk outputs:     ", split_result.outputs)

    stop_pipeline = Pipeline((stopper(),), buffer_size=1)

    def source() -> object:
        for index in range(10_000):
            yield "b" if index == 50 else "a"

    stop_result = stop_pipeline.run_with_stats(source())
    print("stop outputs:      ", stop_result.outputs)
    print("source consumed:   ", stop_result.consumed)
    print("stopped:           ", stop_result.stopped)


if __name__ == "__main__":
    main()
