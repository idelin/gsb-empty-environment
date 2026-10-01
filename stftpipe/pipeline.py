"""Incremental STFT pipeline.

- Variable-hop frame schedule; ISTFT normalizes each sample by the *actual*
  summed squared window weights, never by a fixed COLA coefficient.
- Dropped frames: samples whose weight falls to zero are reported through an
  unrecoverable mask instead of being zero-filled and called lossless.
- Local spectral edits must preserve the real-signal conjugate symmetry
  X[k] = conj(X[N-k]); violations are rejected with SpectralEditError.
- Edits / drops / restores rebuild only the affected output interval.
- Channels share one frame schedule and stay time-aligned.
- Checkpoints keep the unclosed overlap tail so streaming can resume.
- Batches are transactional: a failed correction never changes the
  published output version.
"""
from __future__ import annotations

import bisect
import copy

from .core import NUMERICAL_TOLERANCE, WEIGHT_EPS, STFTKernel, build_starts


class SpectralEditError(ValueError):
    """Spectral edit violates the real-signal conjugate constraint or bounds."""


class _ChannelState:
    def __init__(self, samples):
        self.samples = samples
        self.spectra = []
        self.frame_time = []
        self.output = []
        self.wsum = []
        self.mask = []


class STFTPipeline:
    def __init__(self, size, hops, window="hann", sample_rate=16000):
        if isinstance(hops, int):
            hops = [hops]
        hops = [int(h) for h in hops]
        if not hops or any(h <= 0 for h in hops):
            raise ValueError("hops must be positive integers")
        self.kernel = STFTKernel(size, window)
        self.size = self.kernel.size
        self.hops = hops
        self.sample_rate = int(sample_rate)
        self.starts = []
        self.out_len = 0
        self.channels = []
        self.dropped = set()
        self.published_version = 0

    @property
    def window(self):
        return self.kernel.window

    @property
    def algorithmic_latency_samples(self):
        return self.kernel.latency_samples

    @property
    def n_frames(self):
        return len(self.starts)

    def add_channel(self, samples, sample_rate=None):
        if sample_rate is not None and int(sample_rate) != self.sample_rate:
            raise ValueError("sample rate mismatch: channels cannot be time-aligned")
        self.channels.append(_ChannelState([float(v) for v in samples]))
        self._reschedule()
        return len(self.channels) - 1

    def _reschedule(self):
        self.out_len = max((len(c.samples) for c in self.channels), default=0)
        self.starts = build_starts(self.size, self.hops, self.out_len)

    def analyze(self):
        for ch in self.channels:
            ch.spectra = [self.kernel.analyze_frame(ch.samples, s) for s in self.starts]
            ch.frame_time = [self.kernel.synthesize_frame(sp) for sp in ch.spectra]
            ch.output = [0.0] * self.out_len
            ch.wsum = [0.0] * self.out_len
            ch.mask = [False] * self.out_len
        self.dropped = set()
        if self.starts:
            self._rebuild_interval(0, len(self.starts) - 1)
        self.published_version = 1

    # ------------------------------------------------------------------ output
    def output(self, channel=0):
        return list(self.channels[channel].output)

    def weights(self, channel=0):
        return list(self.channels[channel].wsum)

    def mask(self, channel=0):
        return list(self.channels[channel].mask)

    def is_lossless(self, channel=0):
        return not any(self.channels[channel].mask)

    def unrecoverable_regions(self, channel=0):
        mask = self.channels[channel].mask
        regions = []
        n = 0
        while n < len(mask):
            if mask[n]:
                m = n
                while m < len(mask) and mask[m]:
                    m += 1
                regions.append((n, m))
                n = m
            else:
                n += 1
        return regions

    # ----------------------------------------------------------------- rebuild
    def _rebuild_interval(self, lo_frame, hi_frame):
        """Rebuild only outputs covered by frames [lo_frame, hi_frame].

        Contributions from frames just outside that range (their windows
        overlap into the interval) are included from cache, so the result is
        exact while untouched regions keep their published values.
        """
        if not self.starts:
            return
        lo_frame = max(0, lo_frame)
        hi_frame = min(hi_frame, len(self.starts) - 1)
        if lo_frame > hi_frame:
            return
        a = max(0, self.starts[lo_frame])
        b = min(self.starts[hi_frame] + self.size, self.out_len)
        starts = self.starts
        size = self.size
        win = self.kernel.window
        dropped = self.dropped
        i_lo = bisect.bisect_right(starts, a - size)
        i_hi = bisect.bisect_left(starts, b)
        candidates = range(i_lo, i_hi)
        for ch in self.channels:
            out = ch.output
            wsum = ch.wsum
            mask = ch.mask
            frames = ch.frame_time
            for n in range(a, b):
                acc = 0.0
                w = 0.0
                for i in candidates:
                    j = n - starts[i]
                    if 0 <= j < size and i not in dropped:
                        wv = win[j]
                        acc += wv * frames[i][j]
                        w += wv * wv
                wsum[n] = w
                if w <= WEIGHT_EPS:
                    mask[n] = True
                    out[n] = 0.0
                else:
                    mask[n] = False
                    out[n] = acc / w

    # ------------------------------------------------------------------- ops
    def drop_frame(self, frame):
        return self.apply_batch([("drop", frame)])

    def restore_frame(self, frame):
        return self.apply_batch([("restore", frame)])

    def edit_spectrum(self, channel, frame, edits):
        return self.apply_batch([("edit", channel, frame, edits)])

    def apply_batch(self, ops):
        """Apply ops transactionally. On any failure the previous published
        state is restored and published_version is left unchanged."""
        snapshot = self._snapshot()
        try:
            touched = [self._dispatch(op) for op in ops]
            if touched:
                self._rebuild_interval(min(touched), max(touched))
        except Exception:
            self._restore(snapshot)
            raise
        self.published_version += 1
        return self.published_version

    def _check_frame(self, frame):
        if not 0 <= frame < len(self.starts):
            raise IndexError(f"frame {frame} out of range [0, {len(self.starts)})")

    def _dispatch(self, op):
        kind = op[0]
        if kind == "drop":
            frame = op[1]
            self._check_frame(frame)
            self.dropped.add(frame)
            return frame
        if kind == "restore":
            frame = op[1]
            self._check_frame(frame)
            self.dropped.discard(frame)
            return frame
        if kind == "edit":
            _, channel, frame, edits = op
            self._check_frame(frame)
            self._apply_edit(channel, frame, edits)
            return frame
        raise ValueError(f"unknown op: {kind!r}")

    def _apply_edit(self, channel, frame, edits):
        if not edits:
            return
        size = self.size
        spec = self.channels[channel].spectra[frame]
        for k, v in edits.items():
            if not 0 <= k < size:
                raise SpectralEditError(f"bin {k} out of range [0, {size})")
            mirror = (-k) % size
            if mirror == k:
                if abs(complex(v).imag) > NUMERICAL_TOLERANCE:
                    raise SpectralEditError(
                        f"bin {k} is DC/Nyquist and must stay real")
            elif mirror in edits:
                if abs(complex(edits[mirror]) - complex(v).conjugate()) > NUMERICAL_TOLERANCE:
                    raise SpectralEditError(
                        f"bin {k} and mirror bin {mirror} violate conjugate symmetry")
        for k, v in edits.items():
            spec[k] = complex(v)
        for k, v in edits.items():
            mirror = (-k) % size
            if mirror != k and mirror not in edits:
                spec[mirror] = complex(v).conjugate()
        self.channels[channel].frame_time[frame] = self.kernel.synthesize_frame(spec)

    # ------------------------------------------------------------- snapshot
    def _snapshot(self):
        return copy.deepcopy({
            "dropped": self.dropped,
            "spectra": [c.spectra for c in self.channels],
            "frame_time": [c.frame_time for c in self.channels],
            "output": [c.output for c in self.channels],
            "wsum": [c.wsum for c in self.channels],
            "mask": [c.mask for c in self.channels],
        })

    def _restore(self, snap):
        self.dropped = set(snap["dropped"])
        for ch, sp, ft, out, ws, mk in zip(
            self.channels, snap["spectra"], snap["frame_time"],
            snap["output"], snap["wsum"], snap["mask"],
        ):
            ch.spectra = sp
            ch.frame_time = ft
            ch.output = out
            ch.wsum = ws
            ch.mask = mk

    # ----------------------------------------------------------- checkpoint
    def _tail_frame(self):
        """First frame whose window is not fully inside the input: the
        unclosed overlap tail starts here. Earlier frames (and the output
        they cover) are closed -- no future frame can still add weight and
        no contributing frame was zero-padded at the tail."""
        idx = bisect.bisect_right(self.starts, self.out_len - self.size)
        return min(idx, len(self.starts) - 1)

    def checkpoint(self):
        """Serialize state. Samples before the last frame start are closed;
        the overlap tail from tail_start on is unclosed (future frames may
        still add weight there) and is preserved explicitly."""
        tail_start = self.starts[self._tail_frame()] if self.starts else 0
        return {
            "size": self.size,
            "hops": list(self.hops),
            "window": self.kernel.window_name,
            "sample_rate": self.sample_rate,
            "starts": list(self.starts),
            "out_len": self.out_len,
            "dropped": sorted(self.dropped),
            "published_version": self.published_version,
            "tail_start": tail_start,
            "open_tail": {
                "output": [list(c.output[tail_start:]) for c in self.channels],
                "wsum": [list(c.wsum[tail_start:]) for c in self.channels],
            },
            "channels": [
                {
                    "samples": list(c.samples),
                    "spectra": [[complex(v) for v in sp] for sp in c.spectra],
                    "output": list(c.output),
                    "wsum": list(c.wsum),
                    "mask": list(c.mask),
                }
                for c in self.channels
            ],
        }

    @classmethod
    def from_checkpoint(cls, state):
        pipe = cls(state["size"], state["hops"], state["window"], state["sample_rate"])
        pipe.starts = list(state["starts"])
        pipe.out_len = state["out_len"]
        pipe.dropped = set(state["dropped"])
        pipe.published_version = state["published_version"]
        for cd in state["channels"]:
            ch = _ChannelState([float(v) for v in cd["samples"]])
            ch.spectra = [[complex(v) for v in sp] for sp in cd["spectra"]]
            ch.frame_time = [pipe.kernel.synthesize_frame(sp) for sp in ch.spectra]
            ch.output = list(cd["output"])
            ch.wsum = list(cd["wsum"])
            ch.mask = list(cd["mask"])
            pipe.channels.append(ch)
        return pipe

    def extend(self, samples_map, extra_hops):
        """Append samples and frames (streaming). Rebuild starts at the
        unclosed tail saved by the last checkpoint/analyze, so previously
        closed output is untouched. Tail frames that were zero-padded at
        the old input end are re-analyzed with the new data."""
        if not self.starts:
            raise RuntimeError("cannot extend an empty pipeline")
        if isinstance(extra_hops, int):
            extra_hops = [extra_hops]
        extra_hops = [int(h) for h in extra_hops]
        if not extra_hops or any(h <= 0 for h in extra_hops):
            raise ValueError("extra hops must be positive integers")
        old_out_len = self.out_len
        old_frames = len(self.starts)
        first_dirty = self._tail_frame()
        for idx, add in samples_map.items():
            self.channels[idx].samples.extend(float(v) for v in add)
        n = max(len(c.samples) for c in self.channels)
        i = 0
        while True:
            nxt = self.starts[-1] + extra_hops[i % len(extra_hops)]
            if nxt >= n:
                break
            self.starts.append(nxt)
            i += 1
        grew = n - old_out_len
        for ch in self.channels:
            if grew > 0:
                ch.output.extend([0.0] * grew)
                ch.wsum.extend([0.0] * grew)
                ch.mask.extend([False] * grew)
            for f in range(first_dirty, old_frames):
                sp = self.kernel.analyze_frame(ch.samples, self.starts[f])
                ch.spectra[f] = sp
                ch.frame_time[f] = self.kernel.synthesize_frame(sp)
            for s in self.starts[old_frames:]:
                sp = self.kernel.analyze_frame(ch.samples, s)
                ch.spectra.append(sp)
                ch.frame_time.append(self.kernel.synthesize_frame(sp))
        self.out_len = n
        self._rebuild_interval(first_dirty, len(self.starts) - 1)
        self.published_version += 1
