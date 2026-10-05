#!/usr/bin/env python3
"""Independent exact brute-force atlas solver, used to cross-check the C++
solver. Uses the raw problem semantics directly (no box-expansion transform):
for S ascending, backtrack over every integer position/orientation, requiring
border padding and pairwise separation (gap = pad shared, 2*pad exclusive)."""
import sys
from itertools import product

def solve(n, pad, shared, rects):
    gap = pad if shared else 2 * pad
    def ok_place(placed, x, y, w, h):
        if x < pad or y < pad or x + w > 0:  # border checked by caller bounds
            pass
        for (px, py, pw, ph) in placed:
            if not (x + w + gap <= px or px + pw + gap <= x or
                    y + h + gap <= py or py + ph + gap <= y):
                return False
        return True
    S = max(max(r) for r in rects) + 2 * pad
    while True:
        placed = []
        # positions per rect: (x, y, w, h) within border padding
        opts = []
        for (w, h) in rects:
            o = []
            for (ww, hh) in {(w, h), (h, w)}:
                for x in range(pad, S - pad - ww + 1):
                    for y in range(pad, S - pad - hh + 1):
                        o.append((x, y, ww, hh))
            if not o:
                break
            opts.append(o)
        else:
            def bt(i):
                if i == n:
                    return True
                for cand in opts[i]:
                    if ok_place(placed, *cand):
                        placed.append(cand)
                        if bt(i + 1):
                            return True
                        placed.pop()
                return False
            if bt(0):
                return S
        S += 1

def main():
    data = sys.stdin.read().split()
    n, pad, mode = int(data[0]), int(data[1]), data[2]
    shared = mode in ("shared", "0")
    rects = [(int(data[3 + 2 * i]), int(data[4 + 2 * i])) for i in range(n)]
    print(solve(n, pad, shared, rects))

if __name__ == "__main__":
    main()
