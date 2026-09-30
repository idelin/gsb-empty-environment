"""Driver helper shared by tests and the example.

Simulates a streaming source: feeds input in chunks, collects emitted blocks,
finishes channels when their input is exhausted and finally drains the tail.
"""
from __future__ import annotations


def drive(aligner, inputs, *, block_size, feed_chunk, commit_every=0,
          on_block=None):
    """Run ``aligner`` on ``inputs`` and return {name: (data, flags)}."""
    fed = {name: 0 for name in inputs}
    out = {name: [[], []] for name in inputs}

    def collect(block):
        for name in inputs:
            out[name][0].extend(block.data[name])
            out[name][1].extend(block.flags[name])
        if on_block is not None:
            on_block(aligner, block)

    while True:
        block = aligner.process(block_size)
        if block is None:
            progressed = False
            for name, samples in inputs.items():
                if fed[name] < len(samples):
                    take = min(feed_chunk, len(samples) - fed[name])
                    aligner.feed(name, samples[fed[name]:fed[name] + take])
                    fed[name] += take
                    progressed = True
            if not progressed:
                for name in inputs:
                    aligner.finish(name)
                break
            continue
        collect(block)
        if commit_every and aligner.cursor - aligner.committed >= commit_every:
            aligner.commit()
    for block in aligner.flush(block_size):
        collect(block)
    return {name: (values[0], values[1]) for name, values in out.items()}
