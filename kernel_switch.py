"""Runtime FIR kernel switching with state migration and crossfade.

Design notes
------------
* All active kernel instances are FIR filters driven by one shared input
  delay line (history).  Because every instance sees the same input, a
  weighted mix of instances is itself an LTI system whose impulse response
  is the weighted sum of the individual kernels.  This makes "composing"
  old kernels exact: no tail response is ever truncated, only faded.
* Switching to a new kernel never resets state: the new kernel reads the
  shared history, so its output is identical to a filter that had been
  running since stream start (state migration by shared history).
* At most two instances are active at any time: if a switch is requested
  while a fade is in progress, the current mix is first composed into a
  single equivalent kernel, then a fresh fade to the target starts.
  Working memory and per-sample cost are therefore bounded by
  O(max_kernel_length) and O(2 * max_kernel_length) respectively.
* Fade schedules are expressed in absolute sample indices, so block
  partitioning never changes the switch sample points.
* Latency: zero additional samples.  Output sample n depends only on
  input samples <= n (strictly causal, no lookahead).  Group delay of the
  kernels themselves is not compensated.
"""

from __future__ import annotations

import math
from collections import deque
from dataclasses import dataclass

SNAPSHOT_FORMAT = "kernel-switch-snapshot/1"


class KernelVersion:
    """A named FIR kernel (impulse response)."""

    def __init__(self, name, coeffs):
        coeffs = tuple(float(c) for c in coeffs)
        if not coeffs:
            raise ValueError("kernel must have at least one coefficient")
        if not name:
            raise ValueError("kernel must have a name")
        self.name = str(name)
        self.coeffs = coeffs

    @property
    def length(self):
        return len(self.coeffs)

    @property
    def gain(self):
        """DC gain (sum of coefficients)."""
        return math.fsum(self.coeffs)

    def __repr__(self):
        return "KernelVersion(%r, length=%d)" % (self.name, self.length)


class TransitionGraph:
    """Directed graph of allowed kernel transitions with fade durations."""

    def __init__(self):
        self._versions = {}
        self._edges = {}

    def add_version(self, name, coeffs):
        version = KernelVersion(name, coeffs)
        self._versions[version.name] = version
        return version

    def add_transition(self, src, dst, fade_samples, bidirectional=False):
        self.require_version(src)
        self.require_version(dst)
        fade_samples = int(fade_samples)
        if fade_samples < 1:
            raise ValueError("fade_samples must be >= 1")
        self._edges[(src, dst)] = fade_samples
        if bidirectional:
            self._edges[(dst, src)] = fade_samples

    def require_version(self, name):
        if name not in self._versions:
            raise KeyError("unknown kernel version: %r" % (name,))

    def has_transition(self, src, dst):
        return (src, dst) in self._edges

    def fade_samples(self, src, dst):
        return self._edges[(src, dst)]

    def version(self, name):
        self.require_version(name)
        return self._versions[name]

    @property
    def version_names(self):
        return tuple(self._versions)

    @property
    def max_kernel_length(self):
        if not self._versions:
            raise ValueError("graph has no kernel versions")
        return max(v.length for v in self._versions.values())


class _Instance:
    """One active kernel: coefficients plus provenance and current weight."""

    __slots__ = ("coeffs", "provenance", "weight")

    def __init__(self, coeffs, provenance, weight):
        self.coeffs = coeffs
        self.provenance = dict(provenance)  # version name -> fraction in [0, 1]
        self.weight = float(weight)


def _compose(instances):
    """Merge weighted instances into one equivalent instance.

    For FIR filters sharing one input, w1*h1 + w2*h2 (zero-padded to the
    longer length) applied to the same history reproduces the mixed output
    exactly, so no tail energy is discarded.
    """
    length = max(len(inst.coeffs) for inst in instances)
    coeffs = [0.0] * length
    provenance = {}
    for inst in instances:
        weight = inst.weight
        for k, coeff in enumerate(inst.coeffs):
            coeffs[k] += weight * coeff
        for name, frac in inst.provenance.items():
            provenance[name] = provenance.get(name, 0.0) + weight * frac
    return _Instance(tuple(coeffs), provenance, 1.0)


@dataclass
class SampleMeta:
    """Per-sample metadata: source kernel weights and exact sample time."""

    sample_index: int
    time_seconds: float
    weights: dict  # source kernel version name -> weight at this sample


class KernelSwitchFilter:
    """Streaming FIR filter whose kernel can be swapped at runtime."""

    MAX_ACTIVE_INSTANCES = 2

    def __init__(self, graph, initial, sample_rate):
        if not isinstance(graph, TransitionGraph):
            raise TypeError("graph must be a TransitionGraph")
        sample_rate = float(sample_rate)
        if not sample_rate > 0.0:
            raise ValueError("sample_rate must be positive")
        self._graph = graph
        self._sample_rate = sample_rate
        version = graph.version(initial)
        self._instances = [_Instance(version.coeffs, {version.name: 1.0}, 1.0)]
        self._logical_version = version.name
        # Shared input delay line, newest sample first.  Bounded by the
        # longest kernel in the graph, so a longer new kernel can never
        # read out of bounds.
        self._history = deque(maxlen=graph.max_kernel_length)
        self._sample_index = 0
        self._fade = None  # {"start", "length", "target"} in absolute samples

    # ------------------------------------------------------------------
    # properties (bounds introspection, used by tests and callers)
    # ------------------------------------------------------------------
    @property
    def sample_rate(self):
        return self._sample_rate

    @property
    def sample_index(self):
        """Index of the next sample to be processed (absolute)."""
        return self._sample_index

    @property
    def logical_version(self):
        """Destination version of the latest accepted switch request."""
        return self._logical_version

    @property
    def active_instance_count(self):
        return len(self._instances)

    @property
    def history_length(self):
        return len(self._history)

    @property
    def max_history_length(self):
        return self._graph.max_kernel_length

    @property
    def fade_in_progress(self):
        return self._fade is not None

    # ------------------------------------------------------------------
    # switching
    # ------------------------------------------------------------------
    def request_switch(self, target, fade_samples=None):
        """Request a kernel switch starting at the next processed sample.

        Rules:
        * The request is validated against the transition graph, from the
          logical (latest destination) version to ``target``.
        * Requesting the current logical version is a no-op.
        * If a fade is in progress, the current mix (old kernel tails
          included) is first composed into one equivalent kernel; the new
          fade then runs from that composite to ``target``.
        """
        self._graph.require_version(target)
        source = self._logical_version
        if target == source:
            return False
        if not self._graph.has_transition(source, target):
            raise KeyError(
                "no transition %r -> %r in graph" % (source, target))
        if fade_samples is None:
            fade_samples = self._graph.fade_samples(source, target)
        fade_samples = int(fade_samples)
        if fade_samples < 1:
            raise ValueError("fade_samples must be >= 1")

        if len(self._instances) > 1:
            # Switch requested mid-fade: compose the running mix instead
            # of dropping the old kernels' tail responses.
            self._instances = [_compose(self._instances)]
            self._fade = None

        new_version = self._graph.version(target)
        self._instances.append(
            _Instance(new_version.coeffs, {target: 1.0}, 0.0))
        self._fade = {
            "start": self._sample_index,
            "length": fade_samples,
            "target": target,
        }
        self._logical_version = target
        return True

    def _apply_fade(self, n):
        fade = self._fade
        progress = (n - fade["start"] + 1) / fade["length"]
        w_new = min(1.0, max(0.0, progress))
        old, new = self._instances[0], self._instances[1]
        old.weight = 1.0 - w_new
        new.weight = w_new
        if w_new >= 1.0:
            new.weight = 1.0
            self._instances = [new]
            self._fade = None

    # ------------------------------------------------------------------
    # processing
    # ------------------------------------------------------------------
    def process_sample(self, x):
        """Process one sample, returning (output, SampleMeta)."""
        self._history.appendleft(float(x))
        n = self._sample_index
        if self._fade is not None:
            self._apply_fade(n)
        history = self._history
        history_len = len(history)
        y = 0.0
        for inst in self._instances:
            if inst.weight == 0.0:
                continue
            acc = 0.0
            for k, coeff in enumerate(inst.coeffs):
                if k >= history_len:
                    break  # zero initial state before stream start
                acc += coeff * history[k]
            y += inst.weight * acc
        meta = SampleMeta(n, n / self._sample_rate, self._source_weights())
        self._sample_index += 1
        return y, meta

    def process_block(self, samples):
        """Process a block; switch points are block-size independent."""
        outputs = []
        metas = []
        for x in samples:
            y, meta = self.process_sample(x)
            outputs.append(y)
            metas.append(meta)
        return outputs, metas

    def _source_weights(self):
        weights = {}
        for inst in self._instances:
            for name, frac in inst.provenance.items():
                weights[name] = weights.get(name, 0.0) + inst.weight * frac
        return weights

    # ------------------------------------------------------------------
    # snapshot / restore (any sample boundary is a safe point)
    # ------------------------------------------------------------------
    def snapshot(self):
        """Capture the full state as a JSON-serializable dict."""
        return {
            "format": SNAPSHOT_FORMAT,
            "sample_rate": self._sample_rate,
            "sample_index": self._sample_index,
            "logical_version": self._logical_version,
            "history": list(self._history),
            "instances": [
                {
                    "coeffs": list(inst.coeffs),
                    "provenance": dict(inst.provenance),
                    "weight": inst.weight,
                }
                for inst in self._instances
            ],
            "fade": dict(self._fade) if self._fade is not None else None,
        }

    @classmethod
    def restore(cls, graph, snapshot):
        """Restore from a snapshot dict; processing continues identically."""
        if snapshot.get("format") != SNAPSHOT_FORMAT:
            raise ValueError("unsupported snapshot format")
        obj = cls.__new__(cls)
        obj._graph = graph
        obj._sample_rate = float(snapshot["sample_rate"])
        obj._sample_index = int(snapshot["sample_index"])
        obj._logical_version = snapshot["logical_version"]
        obj._history = deque(snapshot["history"],
                             maxlen=graph.max_kernel_length)
        obj._instances = [
            _Instance(tuple(entry["coeffs"]), entry["provenance"],
                      entry["weight"])
            for entry in snapshot["instances"]
        ]
        fade = snapshot["fade"]
        obj._fade = dict(fade) if fade is not None else None
        return obj
