#!/usr/bin/env python3
"""Independent cross-check of mask_bool optimality on small grids.

Uses subset DP (dp[m] = min over rectangles r subset of m of dp[m-r] + 1),
a completely different method from the tool's branch-and-bound, then compares
the certified minimum rectangle count.
"""
import random
import subprocess
import sys
import re

def brute_min(H, W, target):
    n = H * W
    rects = []
    for t in range(H):
        for l in range(W):
            for b in range(t, H):
                for r in range(l, W):
                    m = 0
                    for i in range(t, b + 1):
                        for j in range(l, r + 1):
                            m |= 1 << (i * W + j)
                    if m & target == m:
                        rects.append(m)
    INF = float('inf')
    dp = {0: 0}
    # iterate masks in popcount order
    sub = target
    masks = []
    s = sub
    while True:
        masks.append(s)
        if s == 0:
            break
        s = (s - 1) & target
    masks.sort(key=bin(0).__class__ and (lambda x: bin(x).count('1')))
    for m in masks:
        if m == 0:
            continue
        best = INF
        for r in rects:
            if r & m == r:
                v = dp[m ^ r] + 1
                if v < best:
                    best = v
        dp[m] = best
    return dp[target]

def run_tool(H, W, keeps, excls):
    lines = [f"{H} {W}", str(len(keeps))]
    lines += ["%d %d %d %d" % r for r in keeps]
    lines.append(str(len(excls)))
    lines += ["%d %d %d %d" % r for r in excls]
    out = subprocess.run(["./mask_bool"], input="\n".join(lines) + "\n",
                         capture_output=True, text=True)
    if out.returncode != 0:
        print(out.stdout, out.stderr)
        sys.exit("tool failed")
    m = re.search(r"^OPTIMAL (\d+)", out.stdout, re.M)
    k = int(m.group(1))
    if "VERIFY disjoint=ok inside=ok union=ok" not in out.stdout and k > 0:
        sys.exit("verification failed:\n" + out.stdout)
    # parse solution and independently verify reconstruction
    sol = re.search(r"^SOLUTION \d+\n((?:\d+ \d+ \d+ \d+\n)*)", out.stdout, re.M)
    union = 0
    for line in sol.group(1).strip().splitlines():
        t, l, b, r = map(int, line.split())
        rm = 0
        for i in range(t, b + 1):
            for j in range(l, r + 1):
                rm |= 1 << (i * W + j)
        assert union & rm == 0, "overlap in tool output"
        union |= rm
    assert union == target_mask(H, W, keeps, excls), "union mismatch"
    return k

def target_mask(H, W, keeps, excls):
    m = 0
    for t, l, b, r in keeps:
        for i in range(t, b + 1):
            for j in range(l, r + 1):
                m |= 1 << (i * W + j)
    for t, l, b, r in excls:
        for i in range(t, b + 1):
            for j in range(l, r + 1):
                m &= ~(1 << (i * W + j))
    return m

def rand_rect(rng, H, W):
    t = rng.randrange(H); b = rng.randrange(t, H)
    l = rng.randrange(W); r = rng.randrange(l, W)
    return (t, l, b, r)

def main():
    rng = random.Random(20261005)
    trials = int(sys.argv[1]) if len(sys.argv) > 1 else 60
    for it in range(trials):
        H = rng.randint(1, 4)
        W = rng.randint(1, 4)
        if H * W > 14:
            W = 3
        keeps = [rand_rect(rng, H, W) for _ in range(rng.randint(1, 3))]
        excls = [rand_rect(rng, H, W) for _ in range(rng.randint(0, 3))]
        tm = target_mask(H, W, keeps, excls)
        expect = brute_min(H, W, tm)
        got = run_tool(H, W, keeps, excls)
        if got != expect:
            print(f"MISMATCH H={H} W={W} keeps={keeps} excls={excls}: "
                  f"tool={got} brute={expect}")
            sys.exit(1)
    print(f"all {trials} random trials match brute force")

if __name__ == "__main__":
    main()
