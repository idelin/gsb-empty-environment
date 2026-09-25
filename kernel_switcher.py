"""Runtime FIR kernel switching with state migration and crossfade.

Design
------
The switcher runs a small set of *voices*.  Each voice owns one kernel
version and a time-varying weight.  All voices are fed by the *same*
input signal, so they share a single delay line (ring buffer) whose
length is bounded by the longest kernel ever installed.

Switching kernels never drops state:

* The outgoing voices keep convolving the shared history while their
  weights ramp linearly to zero, so the old kernel's tail response
  decays smoothly instead of being cut.
* The incoming voice ramps from zero to one over the same absolute
  sample interval and immediately sees the full input history, which
  migrates the state: for equal-DC-gain kernels a constant input stays
  constant (no step).
* If a new switch arrives while a fade is still running, every live
  voice is re-targeted to zero from its *current* weight and the new
  kernel ramps up.  Nothing is discarded.
* To keep memory and per-sample cost bounded, at most ``max_voices``
  voices are alive at once.  When a switch would exceed the limit, the
  two lowest-weight voices are *composed* into one equivalent voice.
  Because all voices share the same input, composition is exact:

      w_a * (h_a * x) + w_b * (h_b * x) == (w_c * h_c) * x
      with  w_c = w_a + w_b,  h_c = (w_a*h_a + w_b*h_b) / w_c

  The merged voice keeps the combined tail energy and fades out
  smoothly.  This is the "compose, never discard" rule for switches
  inside switches.

Every switch and merge is recorded in a transition graph (nodes =
kernel versions, edges = switch/merge events with absolute sample
indices and fade lengths).

Declared properties
-------------------
* Latency: 0 samples.  Output sample n depends only on inputs <= n
  (strictly causal direct-form FIR, no lookahead, no buffering).
* Numerical tolerance: float64 arithmetic throughout.  Tests use an
  absolute tolerance of 1e-9; observed deviation is <= ~1e-13.
* Memory: bounded and declared up front.  The shared delay line holds
  ``max_kernel_length - 1`` samples (constructor parameter, default
  1024); kernels longer than ``max_kernel_length`` are rejected, so a
  newly installed longer kernel never reads out of bounds and never
  sees discarded history.  Voices <= max_voices at all times.
* Per-sample cost: <= max_voices * (longest installed kernel)
  multiply-adds plus O(1) bookkeeping.  Weight ramps are evaluated in
  closed form per sample, so cost does not depend on fade length or
  switch rate.
* Safe points: every sample boundary is a safe point.  ``snapshot()``
  returns a JSON-serialisable dict; ``restore()`` resumes bit-exactly.
* Switch points are absolute sample indices, so block partitioning of
  the input never changes where a switch or fade happens.
"""

from __future__ import annotations

import copy

MAX_VOICES = 4
MAX_KERNEL_LENGTH = 1024
DEFAULT_SAMPLE_RATE = 48000.0
FORMAT_TAG = "kernel-switcher/1"


class Voice:
    """One live kernel version with a linear weight ramp.

    The weight is a closed-form function of the absolute sample index:
    it equals ``start_weight`` at ``fade_start``, ``target_weight`` at
    ``fade_end`` (both inclusive) and is linear in between.  A zero
    length fade (``fade_start == fade_end``) applies the target weight
    immediately.
    """

    __slots__ = (
        "vid",
        "version",
        "kernel",
        "start_weight",
        "target_weight",
        "fade_start",
        "fade_end",
    )

    def __init__(self, vid, version, kernel, start_weight, target_weight,
                 fade_start, fade_end):
        self.vid = vid
        self.version = version
        self.kernel = kernel
        self.start_weight = start_weight
        self.target_weight = target_weight
        self.fade_start = fade_start
        self.fade_end = fade_end

    def weight_at(self, n):
        if n >= self.fade_end:
            return self.target_weight
        if n <= self.fade_start:
            return self.start_weight
        span = self.fade_end - self.fade_start
        g = (n - self.fade_start) / span
        return self.start_weight + (self.target_weight - self.start_weight) * g

    def to_dict(self):
        return {
            "vid": self.vid,
            "version": self.version,
            "kernel": list(self.kernel),
            "start_weight": self.start_weight,
            "target_weight": self.target_weight,
            "fade_start": self.fade_start,
            "fade_end": self.fade_end,
        }

    @classmethod
    def from_dict(cls, data):
        return cls(
            data["vid"],
            data["version"],
            [float(k) for k in data["kernel"]],
            data["start_weight"],
            data["target_weight"],
            data["fade_start"],
            data["fade_end"],
        )


class BlockResult:
    """Output of :meth:`KernelSwitcher.process_block`.

    Carries, alongside the samples, the exact sample time base and the
    per-sample source-kernel weights (version -> weight, weights of
    voices sharing a version label are summed).
    """

    __slots__ = ("samples", "weights", "start_sample", "sample_rate")

    def __init__(self, samples, weights, start_sample, sample_rate):
        self.samples = samples
        self.weights = weights
        self.start_sample = start_sample
        self.sample_rate = sample_rate

    @property
    def end_sample(self):
        """Absolute index one past the last sample."""
        return self.start_sample + len(self.samples)

    @property
    def times(self):
        """Exact sample times: t[i] = (start_sample + i) / sample_rate."""
        base = self.start_sample
        sr = self.sample_rate
        return [(base + i) / sr for i in range(len(self.samples))]


class KernelSwitcher:
    """FIR filter whose kernel can be replaced at runtime with crossfade."""

    def __init__(self, kernel, version="v0", sample_rate=DEFAULT_SAMPLE_RATE,
                 max_voices=MAX_VOICES, max_kernel_length=MAX_KERNEL_LENGTH):
        kernel = [float(k) for k in kernel]
        if not kernel:
            raise ValueError("kernel must not be empty")
        if max_voices < 2:
            raise ValueError("max_voices must be >= 2")
        if len(kernel) > max_kernel_length:
            raise ValueError(
                f"kernel of length {len(kernel)} exceeds declared "
                f"max_kernel_length={max_kernel_length}"
            )
        self.sample_rate = float(sample_rate)
        self.max_voices = int(max_voices)
        self.max_kernel_length = int(max_kernel_length)
        self.n = 0  # absolute index of the next sample to produce
        self.voices = [Voice(0, version, kernel, 1.0, 1.0, 0, 0)]
        self._vid_counter = 1
        # Fixed-size shared delay line: deep history is retained up to
        # the declared bound, so installing a longer kernel later never
        # reads out of bounds or sees already-discarded samples.
        self._cap = max(1, self.max_kernel_length - 1)
        self._max_kernel_len = len(kernel)
        self._ring = [0.0] * self._cap
        self._wpos = 0
        self._pending = []  # (at_sample, seq, version, kernel, fade_samples)
        self._seq = 0
        self.graph = {
            "nodes": {
                version: {"kernel_length": len(kernel), "first_seen_sample": 0}
            },
            "edges": [],
        }

    # ------------------------------------------------------------------ API

    @property
    def active_voice_count(self):
        return len(self.voices)

    @property
    def history_length(self):
        """Length of the shared delay line (max_kernel_length - 1)."""
        return self._cap

    @property
    def worst_case_macs(self):
        """Upper bound on multiply-adds per output sample."""
        return self.max_voices * self._max_kernel_len

    def schedule_switch(self, version, kernel, fade_samples, at_sample=None):
        """Schedule a kernel switch.

        ``at_sample`` is the absolute sample index where the fade starts
        (default: the next processed sample).  The fade reaches weight 1
        for the new kernel at ``at_sample + fade_samples``.  Scheduling
        in the past raises ``ValueError``.  Returns the start index.
        """
        kernel = [float(k) for k in kernel]
        if not kernel:
            raise ValueError("kernel must not be empty")
        if len(kernel) > self.max_kernel_length:
            raise ValueError(
                f"kernel of length {len(kernel)} exceeds declared "
                f"max_kernel_length={self.max_kernel_length}"
            )
        if fade_samples < 0:
            raise ValueError("fade_samples must be >= 0")
        at = self.n if at_sample is None else int(at_sample)
        if at < self.n:
            raise ValueError(
                "cannot schedule a switch in the past: "
                f"at_sample={at} < current sample {self.n}"
            )
        self._pending.append((at, self._seq, version, kernel, int(fade_samples)))
        self._seq += 1
        self._pending.sort(key=lambda item: (item[0], item[1]))
        return at

    def process_sample(self, x):
        """Process one input sample, return the output sample."""
        self._apply_due_switches()
        x = float(x)
        ring = self._ring
        cap = self._cap
        wpos = self._wpos
        n = self.n
        y = 0.0
        for voice in self.voices:
            w = voice.weight_at(n)
            if w == 0.0:
                continue
            kernel = voice.kernel
            acc = kernel[0] * x
            for j in range(1, len(kernel)):
                acc += kernel[j] * ring[(wpos - j) % cap]
            y += w * acc
        ring[wpos] = x
        self._wpos = (wpos + 1) % cap
        if len(self.voices) > 1:
            self.voices = [
                v for v in self.voices
                if not (v.target_weight == 0.0 and n >= v.fade_end)
            ]
        self.n = n + 1
        return y

    def process_block(self, samples):
        """Process a block; returns a :class:`BlockResult`.

        Block boundaries are irrelevant to behaviour: switches fire at
        absolute sample indices, so any partitioning of the same input
        yields bit-identical output.
        """
        start = self.n
        out = []
        weights = []
        for x in samples:
            out.append(self.process_sample(x))
            weights.append(self.weights_at(self.n - 1))
        return BlockResult(out, weights, start, self.sample_rate)

    def weights_at(self, n):
        """Source-kernel weights at absolute sample ``n`` (version -> w)."""
        result = {}
        for voice in self.voices:
            w = voice.weight_at(n)
            if w != 0.0:
                result[voice.version] = result.get(voice.version, 0.0) + w
        return result

    # ------------------------------------------------------- snapshot/restore

    def snapshot(self):
        """JSON-serialisable snapshot; valid at any sample boundary."""
        return {
            "format": FORMAT_TAG,
            "sample_rate": self.sample_rate,
            "max_voices": self.max_voices,
            "max_kernel_length": self.max_kernel_length,
            "n": self.n,
            "vid_counter": self._vid_counter,
            "cap": self._cap,
            "max_installed_kernel_len": self._max_kernel_len,
            "wpos": self._wpos,
            "ring": list(self._ring),
            "voices": [v.to_dict() for v in self.voices],
            "pending": [list(p[:2]) + [p[2], list(p[3]), p[4]]
                        for p in self._pending],
            "seq": self._seq,
            "graph": copy.deepcopy(self.graph),
        }

    @classmethod
    def restore(cls, snap):
        """Rebuild a switcher from :meth:`snapshot`; resumes bit-exactly."""
        if snap.get("format") != FORMAT_TAG:
            raise ValueError(f"unsupported snapshot format: {snap.get('format')!r}")
        self = cls.__new__(cls)
        self.sample_rate = snap["sample_rate"]
        self.max_voices = snap["max_voices"]
        self.max_kernel_length = snap["max_kernel_length"]
        self.n = snap["n"]
        self._vid_counter = snap["vid_counter"]
        self._cap = snap["cap"]
        self._max_kernel_len = snap["max_installed_kernel_len"]
        self._wpos = snap["wpos"]
        self._ring = list(snap["ring"])
        self.voices = [Voice.from_dict(v) for v in snap["voices"]]
        self._pending = [
            (p[0], p[1], p[2], [float(k) for k in p[3]], p[4])
            for p in snap["pending"]
        ]
        self._seq = snap["seq"]
        self.graph = copy.deepcopy(snap["graph"])
        return self

    # -------------------------------------------------------------- internals

    def _apply_due_switches(self):
        while self._pending and self._pending[0][0] <= self.n:
            _, _, version, kernel, fade = self._pending.pop(0)
            self._start_switch(version, kernel, fade, self.n)

    def _start_switch(self, version, kernel, fade, n):
        while len(self.voices) >= self.max_voices:
            self._merge_smallest(n)
        for voice in self.voices:
            voice.start_weight = voice.weight_at(n)
            voice.target_weight = 0.0
            voice.fade_start = n
            voice.fade_end = n + fade
        self.voices.append(
            Voice(self._vid_counter, version, kernel, 0.0, 1.0, n, n + fade)
        )
        self._vid_counter += 1
        if len(kernel) > self._max_kernel_len:
            self._max_kernel_len = len(kernel)
        self._register_node(version, len(kernel), n)
        self.graph["edges"].append({
            "type": "switch",
            "from": [v.version for v in self.voices[:-1]],
            "to": version,
            "sample": n,
            "fade_samples": fade,
        })

    def _merge_smallest(self, n):
        """Compose the two lowest-weight voices into one equivalent voice.

        Exact because all voices share the same input delay line:
        w_a*(h_a*x) + w_b*(h_b*x) == w_c*(h_c*x) with w_c = w_a + w_b
        and h_c = (w_a*h_a + w_b*h_b)/w_c.  The combined tail is kept,
        not dropped.  Voices still ramping up (target_weight > 0) are
        never merged while a fading voice exists.
        """
        ranked = sorted(
            self.voices,
            key=lambda v: (v.target_weight > 0.0, v.weight_at(n)),
        )
        a, b = ranked[0], ranked[1]
        wa = a.weight_at(n)
        wb = b.weight_at(n)
        wc = wa + wb
        label = f"({a.version}+{b.version})"
        self.graph["edges"].append({
            "type": "merge",
            "from": [a.version, b.version],
            "to": label,
            "sample": n,
            "weights": [wa, wb],
        })
        self.voices = [v for v in self.voices if v is not a and v is not b]
        if wc == 0.0:
            return  # both voices carry exactly zero energy: safe to drop
        la, lb = len(a.kernel), len(b.kernel)
        length = max(la, lb)
        merged = [
            (wa * (a.kernel[i] if i < la else 0.0)
             + wb * (b.kernel[i] if i < lb else 0.0)) / wc
            for i in range(length)
        ]
        self._register_node(label, length, n)
        self.voices.append(
            Voice(self._vid_counter, label, merged, wc, wc, n, n)
        )
        self._vid_counter += 1
        if length > self._max_kernel_len:
            self._max_kernel_len = length

    def _register_node(self, version, kernel_length, n):
        node = self.graph["nodes"].get(version)
        if node is None:
            self.graph["nodes"][version] = {
                "kernel_length": kernel_length,
                "first_seen_sample": n,
            }
        else:
            node["kernel_length"] = max(node["kernel_length"], kernel_length)
