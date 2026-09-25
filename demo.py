"""Runnable demo: runtime kernel switching with crossfade and snapshots.

Run with:  python3.11 demo.py
"""

import json

from fixtures import SAMPLE_RATE, constant, impulse, moving_average, sine
from kernel_switch import KernelSwitchFilter, TransitionGraph

LATENCY_SAMPLES = 0          # strictly causal, no lookahead
ABS_TOLERANCE = 1e-9         # float64 reference-comparison tolerance


def build_graph():
    graph = TransitionGraph()
    graph.add_version("ma4", moving_average(4))
    graph.add_version("ma16", moving_average(16))
    graph.add_version("ma48", moving_average(48))
    graph.add_transition("ma4", "ma16", fade_samples=64, bidirectional=True)
    graph.add_transition("ma16", "ma48", fade_samples=64, bidirectional=True)
    graph.add_transition("ma4", "ma48", fade_samples=64, bidirectional=True)
    return graph


def main():
    print("sample rate: %.0f Hz | declared latency: %d sample(s) | "
          "abs tolerance: %.0e" % (SAMPLE_RATE, LATENCY_SAMPLES,
                                   ABS_TOLERANCE))

    # 1) Constant signal, equal-gain kernels: no audible/visible step.
    graph = build_graph()
    filt = KernelSwitchFilter(graph, "ma4", SAMPLE_RATE)
    signal = constant(256, 1.0)
    filt.process_block(signal[:100])
    filt.request_switch("ma16")            # fade over samples 100..163
    out, metas = filt.process_block(signal[100:])
    steady = out[80:]                      # well past the fade
    max_dev = max(abs(y - 1.0) for y in steady)
    print("constant-signal switch ma4->ma16: max |y-1| after fade = %.3e"
          % max_dev)
    mid = metas[32]                        # a sample inside the fade
    print("  meta @n=%d t=%.6fs weights=%s"
          % (mid.sample_index, mid.time_seconds,
             {k: round(v, 4) for k, v in mid.weights.items()}))

    # 2) Impulse through a switch point: old tail fades, never cut.
    filt = KernelSwitchFilter(build_graph(), "ma4", SAMPLE_RATE)
    signal = impulse(200, at=58)           # tail straddles the switch
    pre, _ = filt.process_block(signal[:60])
    filt.request_switch("ma48")
    post, _ = filt.process_block(signal[60:])
    out = pre + post
    print("impulse @n=58, switch ma4->ma48 @n=60: y[58:70] = %s"
          % [round(v, 6) for v in out[58:70]])

    # 3) Switch during a fade (cancel): ma4 -> ma48, then back to ma4.
    filt = KernelSwitchFilter(build_graph(), "ma4", SAMPLE_RATE)
    signal = sine(400, freq=997.0)
    filt.process_block(signal[:120])
    filt.request_switch("ma48")            # fade 120..183
    filt.process_block(signal[120:140])
    filt.request_switch("ma4")             # cancel mid-fade at n=140
    out, metas = filt.process_block(signal[140:])
    final_weights = metas[-1].weights
    print("cancel mid-fade @n=140: final weights = %s"
          % {k: round(v, 6) for k, v in final_weights.items()})

    # 4) Snapshot at an arbitrary safe point, restore, continue.
    filt = KernelSwitchFilter(build_graph(), "ma4", SAMPLE_RATE)
    signal = sine(300, freq=440.0)
    filt.process_block(signal[:150])
    filt.request_switch("ma16")
    filt.process_block(signal[150:170])
    snap = filt.snapshot()                 # safe point: mid-fade, n=170
    tail_a, _ = filt.process_block(signal[170:])
    restored = KernelSwitchFilter.restore(build_graph(),
                                          json.loads(json.dumps(snap)))
    tail_b, _ = restored.process_block(signal[170:])
    identical = tail_a == tail_b
    print("snapshot/restore at n=170 (mid-fade): bit-exact continuation "
          "= %s" % identical)

    # 5) Bounds: memory and per-sample cost stay bounded.
    print("bounds: active instances <= %d, history <= %d samples "
          "(max kernel length)" % (filt.MAX_ACTIVE_INSTANCES,
                                   filt.max_history_length))


if __name__ == "__main__":
    main()
