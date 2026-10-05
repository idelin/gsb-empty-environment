import itertools, random, subprocess, sys

def brute(T, R, SPILL, MOVE, vars_, cons):
    N = len(vars_)
    live = [[v for v in range(N) if vars_[v][0] <= i <= vars_[v][1]] for i in range(T)]
    consAt = [[] for _ in range(T)]
    for v, p, r in cons: consAt[p].append((v, r))
    # 状态：每个活跃变量的位置（0..R-1 寄存器, R 内存），寄存器单射
    states = []
    for i in range(T):
        lv = live[i]; st = []
        for assign in itertools.product(range(R + 1), repeat=len(lv)):
            ok = all(assign.count(r) <= 1 for r in range(R))
            if not ok: continue
            loc = {v: assign[k] for k, v in enumerate(lv)}
            if any(loc[v] != r for v, r in consAt[i]): continue
            st.append(loc)
        states.append(st)
        if not st: return None  # 不可行
    best = None
    def poscost(i, loc):
        return SPILL * sum(vars_[v][2] for v in live[i] if loc[v] == R)
    for path in itertools.product(*[range(len(states[i])) for i in range(T)]):
        cost = 0
        for i in range(T):
            cost += poscost(i, states[i][path[i]])
            if i:
                prev, cur = states[i-1][path[i-1]], states[i][path[i]]
                for v in set(prev) & set(cur):
                    if prev[v] != cur[v]: cost += MOVE * vars_[v][2]
        if best is None or cost < best: best = cost
    return best

random.seed(42)
fails = 0
for trial in range(300):
    T = random.randint(1, 4); R = 3
    SPILL = random.randint(1, 5); MOVE = random.randint(1, 3)
    N = random.randint(1, 5)
    vars_ = []
    for _ in range(N):
        s = random.randint(0, T - 1); e = random.randint(s, T - 1)
        vars_.append((s, e, random.randint(1, 6)))
    cons = []
    for _ in range(random.randint(0, 2)):
        v = random.randrange(N); s, e, _ = vars_[v]
        cons.append((v, random.randint(s, e), random.randrange(R)))
    inp = f"{T} {R} {SPILL} {MOVE}\n{N}\n"
    for k, (s, e, w) in enumerate(vars_): inp += f"v{k} {s} {e} {w}\n"
    inp += f"{len(cons)}\n"
    for v, p, r in cons: inp += f"v{v} {p} {r}\n"
    out = subprocess.run(["./regalloc"], input=inp, capture_output=True, text=True).stdout
    expect = brute(T, R, SPILL, MOVE, vars_, cons)
    if expect is None:
        ok = "无可行分配" in out or "没有任何合法状态" in out
    else:
        ok = f"最小总代价: {expect}\n" in out and "校验结果: 通过" in out
    if not ok:
        fails += 1
        print("MISMATCH trial", trial, "expect", expect)
        print(inp); print(out)
        if fails > 2: sys.exit(1)
print("全部通过" if fails == 0 else f"{fails} 个失败")
