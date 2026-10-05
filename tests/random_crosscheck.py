#!/usr/bin/env python3
"""Randomised cross-check: C++ solver vs independent Python brute force.
Also validates every solver output with the independent --check mode."""
import random
import subprocess
import sys

SOLVER = "./atlas_solver"
random.seed(int(sys.argv[1]) if len(sys.argv) > 1 else 12345)
trials = int(sys.argv[2]) if len(sys.argv) > 2 else 150

for t in range(trials):
    n = random.randint(1, 4)
    pad = random.randint(0, 2)
    mode = random.choice(["shared", "exclusive"])
    rects = [(random.randint(1, 4), random.randint(1, 4)) for _ in range(n)]
    inst = f"{n} {pad} {mode}\n" + "".join(f"{w} {h}\n" for w, h in rects)

    cpp = subprocess.run([SOLVER], input=inst, capture_output=True, text=True)
    assert cpp.returncode == 0, (inst, cpp.stderr)
    s_cpp = int(cpp.stdout.split()[0])

    bf = subprocess.run(["python3", "tests/brute.py"], input=inst,
                        capture_output=True, text=True)
    s_bf = int(bf.stdout.split()[0])

    assert s_cpp == s_bf, f"MISMATCH cpp={s_cpp} brute={s_bf}\n{inst}\n{cpp.stdout}"

    spec = f"{s_cpp}\n{n} {pad} {mode}\n" + \
        "".join(f"{w} {h}\n" for w, h in rects) + \
        "\n".join(cpp.stdout.splitlines()[1:]) + "\n"
    chk = subprocess.run([SOLVER, "--check"], input=spec,
                         capture_output=True, text=True)
    assert chk.returncode == 0 and chk.stdout.strip() == "OK", \
        f"checker rejected solver output\n{inst}\n{cpp.stdout}\n{chk.stdout}"

print(f"all {trials} random trials passed (minimal S matches brute force, checker OK)")
