"""Replayable example: a deterministic op script over the noise-floor bank.

The same script is executable in four modes that must yield identical
query results:

* ``sequential`` -- ops replayed as recorded;
* ``chunked``    -- adds fed in fixed-size chunks;
* ``shuffled``   -- adds within each phase arrive out of order;
* ``persist``    -- bank saved to a plain dict and restored after each phase.

Run directly (``python3.11 -m noise_floor.replay``) to print the replay log
and a cross-mode consistency check.
"""

from __future__ import annotations

import random

from .fixtures import BANDS, SCENES, band_values
from .limits import LIMITS
from .model import MARK_APPLIED, NoiseFloorBank, TRANSIENT, VALID

WINDOW_FRAMES = 64
PHASE_FRAMES = 32
N_FRAMES = 96
QUANTILE = 0.5
MARGIN = 6.0


def build_script(n_frames: int = N_FRAMES) -> list[tuple]:
    """Deterministic op log built purely from local fixtures."""
    ops: list[tuple] = []
    series = {band: band_values(band, n_frames) for band in BANDS}
    sid = 0
    for scene_i, scene in enumerate(SCENES):
        ops.append(("scene", scene))
        offset = scene_i * 1000
        for start in range(0, n_frames, PHASE_FRAMES):
            ops.append(("phase",))
            stop = min(start + PHASE_FRAMES, n_frames)
            for band in BANDS:
                for frame in range(start, stop):
                    ops.append(("add", band, offset + sid, frame,
                                series[band][frame], VALID))
                    sid += 1
            # A few gap frames arrive as missing instead of valid.
            for band in BANDS:
                gap_frame = start + 5
                ops.append(("add", band, offset + sid, gap_frame, None,
                            "missing"))
                sid += 1
            for band in BANDS:
                ops.append(("estimate", band))
                ops.append(("threshold", band))
        # Detections are recorded while the extreme peaks still contribute.
        ops.append(("phase",))
        for band in BANDS:
            ops.append(("detect_rel", band, n_frames, -0.01))
        # Late marks: extreme peaks were included earlier; retract them now.
        from .fixtures import PEAK_FRAMES
        for band in BANDS:
            for peak in PEAK_FRAMES[band]:
                ops.append(("mark_peak", band, peak))
        for band in BANDS:
            ops.append(("estimate", band))
        # Corrections re-decide the stale detections, then reclaim history.
        ops.append(("phase",))
        for band in BANDS:
            ops.append(("correct", band))
            ops.append(("watermark", band, n_frames - WINDOW_FRAMES))
            ops.append(("estimate", band))
    return ops


def run(ops: list[tuple], mode: str = "sequential", chunk_size: int = 16,
        seed: int = 3) -> list[tuple]:
    """Replay an op script in one of the four modes; return the result log."""
    if mode not in ("sequential", "chunked", "shuffled", "persist"):
        raise ValueError(f"unknown mode {mode!r}")
    bank = NoiseFloorBank(window_frames=WINDOW_FRAMES,
                          quantile=QUANTILE, margin=MARGIN)
    log: list[tuple] = []
    rng = random.Random(seed)
    pending: list[tuple] = []  # buffered adds of the current phase

    def flush():
        adds = pending[:]
        pending.clear()
        if mode == "shuffled":
            rng.shuffle(adds)
        if mode == "chunked":
            for i in range(0, len(adds), chunk_size):
                for op in adds[i:i + chunk_size]:
                    _apply(bank, op, log)
        else:
            for op in adds:
                _apply(bank, op, log)
        if mode == "persist":
            snapshot = bank.to_dict()
            restored = NoiseFloorBank.from_dict(snapshot)
            bank.__dict__.update(restored.__dict__)

    for op in ops:
        if op[0] == "add":
            pending.append(op)
            continue
        if op[0] == "phase":
            flush()
            continue
        flush()
        _apply(bank, op, log)
    flush()
    return log


def _apply(bank: NoiseFloorBank, op: tuple, log: list[tuple]) -> None:
    kind = op[0]
    if kind == "scene":
        bank.set_scene(op[1])
    elif kind == "add":
        _, band, sid, frame, value, state = op
        bank.model(band).add(sid, frame, value, state)
    elif kind == "mark_peak":
        _, band, frame = op
        model = bank.model(band)
        target = next(sid for sid in model.sample_ids_at(frame)
                      if model._samples[sid].state == VALID)
        status = model.mark(target, TRANSIENT)
        log.append(("mark", band, frame, status))
    elif kind == "detect_rel":
        _, band, frame, delta = op
        model = bank.model(band)
        th = model.threshold()
        detected = None if th.value is None else model.detect(
            frame, th.value + delta)
        log.append(("detect", band, frame, detected))
    elif kind == "estimate":
        est = bank.model(op[1]).estimate()
        log.append(("estimate", op[1], est.value, est.n_valid, est.version))
    elif kind == "threshold":
        th = bank.model(op[1]).threshold()
        log.append(("threshold", op[1], th.value, th.version))
    elif kind == "detect":
        _, band, frame, value = op
        detected = bank.model(band).detect(frame, value)
        log.append(("detect", band, frame, detected))
    elif kind == "correct":
        corrections = bank.model(op[1]).correct_detections()
        log.append(("correct", op[1], len(corrections)))
    elif kind == "watermark":
        _, band, frame = op
        reclaimed = bank.model(band).set_watermark(frame)
        log.append(("watermark", band, frame, reclaimed))
    else:  # pragma: no cover - defensive
        raise ValueError(f"unknown op {kind!r}")


def main() -> None:
    ops = build_script()
    logs = {mode: run(ops, mode=mode)
            for mode in ("sequential", "chunked", "shuffled", "persist")}
    base = logs["sequential"]
    for mode, log in logs.items():
        assert _close(log, base), f"mode {mode} diverged"
    print(f"limits: {LIMITS}")
    print(f"replayed {len(ops)} ops x 4 modes; all modes identical")
    for entry in base[:6]:
        print(" ", entry)
    print("  ...")
    for entry in base[-6:]:
        print(" ", entry)


def _close(a: list[tuple], b: list[tuple]) -> bool:
    if len(a) != len(b):
        return False
    tol = LIMITS.quantile_abs_tol
    for ea, eb in zip(a, b):
        if len(ea) != len(eb):
            return False
        for xa, xb in zip(ea, eb):
            if isinstance(xa, float) or isinstance(xb, float):
                if xa is None or xb is None:
                    if xa is not xb:
                        return False
                elif abs(xa - xb) > tol:
                    return False
            elif xa != xb:
                return False
    return True


if __name__ == "__main__":
    main()
