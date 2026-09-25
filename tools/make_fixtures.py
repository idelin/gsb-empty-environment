"""Generate local JSON fixtures deterministically (no audio hardware)."""

import json
import math
from pathlib import Path

FS = 100.0
OUT = Path(__file__).resolve().parent.parent / "fixtures"


def base_signal(n):
    out = []
    for i in range(n):
        t = i / FS
        v = (math.sin(2 * math.pi * 1.5 * t)
             + 0.3 * math.sin(2 * math.pi * 7.0 * t)
             + 0.02 * math.sin(2 * math.pi * 13.7 * t))
        out.append(round(v, 10))
    return out


def write(name, samples):
    path = OUT / name
    payload = {"name": name, "sample_rate": FS, "samples": samples}
    path.write_text(json.dumps(payload, indent=1), encoding="utf-8")
    print(f"wrote {path}")


def main():
    OUT.mkdir(exist_ok=True)

    short = base_signal(120)
    short[10] = 0.0
    for i in range(30, 33):
        short[i] = None
    for i in range(64, 70):
        short[i] = None
    write("signal_short_gaps.json", short)

    long = base_signal(120)
    for i in range(40, 70):
        long[i] = None
    write("signal_long_gap.json", long)

    start = base_signal(100)
    for i in range(0, 4):
        start[i] = None
    write("signal_start_gap.json", start)


if __name__ == "__main__":
    main()
