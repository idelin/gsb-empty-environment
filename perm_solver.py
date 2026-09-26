"""有限域排列约束求解器：allDifferent + 逆序数上下界。

仅使用 Python 标准库（不调用 SAT/SMT 库）。

模型：n 个变量 x_0..x_{n-1}，每个变量的域是 values（默认 1..n）的子集；
要求 allDifferent（即完备赋值必为 values 的一个排列），且排列的逆序数
落在闭区间 [low, high] 内。

传播：用 bitmask DP 精确计算当前域下所有合法完备赋值的逆序数区间
[lo, hi]（必然界/可能界），并对每个 变量=值 计算固定后的可达区间，
与 [low, high] 不相交则剪枝，直至不动点（对该约束合取达到弧一致）。

搜索：MRV 分支 + 回溯；进入分支前复制域，回溯即恢复域。
每次剪枝与失败都记录依据到 trace。

求解声称有解时，用独立检查器 check_solution（O(n^2) 直接数逆序对）
对照原始约束验证。
"""

from __future__ import annotations

import itertools


def inversion_count(perm):
    """O(n^2) 直接统计逆序对数。独立实现，供检查器与测试使用。"""
    inv = 0
    for i in range(len(perm)):
        for j in range(i + 1, len(perm)):
            if perm[i] > perm[j]:
                inv += 1
    return inv


def min_max_inversions(domains, values):
    """精确计算当前域下所有完备赋值（满足 allDifferent）的逆序数区间。

    domains: list[set]，每个位置的候选值集合。
    values:  已排序的全部取值列表，len(values) == len(domains)。

    返回 (lo, hi)；若不存在完备赋值（allDifferent 不可满足）返回 None。

    方法：按位置顺序做 bitmask DP。状态为已用值的位掩码 mask，
    填位置 pos = popcount(mask) 时选择值 v，新增逆序对数只依赖 mask：
    即 mask 中大于 v 的已用值个数。f[mask] 记录到达 mask 的最小/最大
    逆序数。数值递增遍历 mask 即按 popcount 递增的拓扑序。
    """
    n = len(domains)
    index = {v: k for k, v in enumerate(values)}
    dom_mask = []
    for d in domains:
        mask = 0
        for v in d:
            mask |= 1 << index[v]
        dom_mask.append(mask)

    size = 1 << n
    fmin = [None] * size
    fmax = [None] * size
    fmin[0] = 0
    fmax[0] = 0
    for mask in range(size):
        if fmin[mask] is None:
            continue
        pos = bin(mask).count("1")
        if pos >= n:
            continue
        avail = dom_mask[pos] & ~mask
        m = avail
        while m:
            bit = m & (-m)
            k = bit.bit_length() - 1  # values[k] 为所选值
            # 新增逆序对：已用值中大于 values[k] 的个数
            added = bin(mask & ~((1 << (k + 1)) - 1)).count("1")
            nmask = mask | bit
            lo = fmin[mask] + added
            hi = fmax[mask] + added
            if fmin[nmask] is None or lo < fmin[nmask]:
                fmin[nmask] = lo
            if fmax[nmask] is None or hi > fmax[nmask]:
                fmax[nmask] = hi
            m ^= bit
    full = size - 1
    if fmin[full] is None:
        return None
    return fmin[full], fmax[full]


def propagate(domains, values, low, high, log):
    """对 (allDifferent, low <= 逆序数 <= high) 做弧一致传播。

    返回剪枝后的新域列表；若约束不可满足返回 None。
    每次剪枝/失败通过 log(msg) 记录依据。
    """
    domains = [set(d) for d in domains]
    while True:
        bounds = min_max_inversions(domains, values)
        if bounds is None:
            log("失败：当前域下不存在满足 allDifferent 的完备赋值")
            return None
        lo, hi = bounds
        log(f"传播：当前可达逆序数区间 [{lo}, {hi}]，允许区间 [{low}, {high}]")
        if hi < low or lo > high:
            log(f"失败：可达区间 [{lo}, {hi}] 与允许区间 [{low}, {high}] 不相交")
            return None
        pruned = False
        for i in range(len(domains)):
            if len(domains[i]) <= 1:
                continue
            for v in sorted(domains[i]):
                sub = [set(d) for d in domains]
                sub[i] = {v}
                b = min_max_inversions(sub, values)
                if b is None:
                    log(f"剪枝：x{i} != {v}（固定 x{i}={v} 后 allDifferent 不可满足）")
                    domains[i].discard(v)
                    pruned = True
                else:
                    lo2, hi2 = b
                    if hi2 < low or lo2 > high:
                        log(f"剪枝：x{i} != {v}（固定后可达逆序数区间 [{lo2}, {hi2}] "
                            f"与允许区间 [{low}, {high}] 不相交）")
                        domains[i].discard(v)
                        pruned = True
        if not pruned:
            return domains


def check_solution(assignment, orig_domains, values, low, high):
    """独立检查器：对照原始约束验证一个完备赋值。

    不依赖求解器内部的 DP 结构：直接检查排列性、原域成员性、
    并用 O(n^2) 直接数逆序对验证上下界。
    """
    n = len(assignment)
    if len(orig_domains) != n or len(values) != n:
        return False
    if sorted(assignment) != sorted(values):  # allDifferent + 完备
        return False
    for i, v in enumerate(assignment):
        if v not in orig_domains[i]:
            return False
    inv = inversion_count(assignment)
    return low <= inv <= high


def brute_force_solutions(domains, values, low, high):
    """小规模对照用：枚举全部排列，返回满足原约束的解列表。"""
    n = len(values)
    sols = []
    for perm in itertools.permutations(values):
        if all(perm[i] in domains[i] for i in range(n)):
            if low <= inversion_count(perm) <= high:
                sols.append(list(perm))
    return sols


class PermSolver:
    """回溯求解器：MRV 分支，进入分支前复制域，回溯即恢复域。"""

    def __init__(self, domains, low, high, values=None, verbose=False):
        n = len(domains)
        self.values = sorted(values) if values is not None else list(range(1, n + 1))
        if len(self.values) != n:
            raise ValueError("values 的数量必须等于变量个数")
        self.orig_domains = [set(d) for d in domains]
        self.low = low
        self.high = high
        self.verbose = verbose
        self.trace = []

    def _log(self, msg):
        self.trace.append(msg)
        if self.verbose:
            print(msg)

    def solve(self):
        """返回第一个解（list）或 None。声称有解前用独立检查器验证。"""
        if self.low > self.high:
            self._log(f"立即失败：下界 {self.low} > 上界 {self.high}，上下界矛盾")
            return None
        domains = propagate(self.orig_domains, self.values, self.low, self.high, self._log)
        if domains is None:
            return None
        return self._search(domains)

    def count_solutions(self):
        """统计全部解个数（充分 exercise 回溯与域恢复）。"""
        if self.low > self.high:
            return 0
        domains = propagate(self.orig_domains, self.values, self.low, self.high, self._log)
        if domains is None:
            return 0
        return self._count(domains)

    def _pick_var(self, domains):
        n = len(domains)
        return min((k for k in range(n) if len(domains[k]) > 1),
                   key=lambda k: len(domains[k]), default=None)

    def _search(self, domains):
        if all(len(d) == 1 for d in domains):
            assign = [next(iter(d)) for d in domains]
            if check_solution(assign, self.orig_domains, self.values,
                              self.low, self.high):
                self._log(f"找到解 {assign}，独立检查器验证通过")
                return assign
            self._log(f"内部错误：{assign} 未通过独立检查器")
            return None
        i = self._pick_var(domains)
        for v in sorted(domains[i]):
            self._log(f"分支：x{i} = {v}（域大小 {len(domains[i])}）")
            sub = [set(d) for d in domains]  # 复制域
            sub[i] = {v}
            sub = propagate(sub, self.values, self.low, self.high, self._log)
            if sub is not None:
                result = self._search(sub)
                if result is not None:
                    return result
            self._log(f"回溯：撤销 x{i} = {v}，恢复各变量域")
        return None

    def _count(self, domains):
        if all(len(d) == 1 for d in domains):
            assign = [next(iter(d)) for d in domains]
            ok = check_solution(assign, self.orig_domains, self.values,
                                self.low, self.high)
            assert ok, f"完备赋值 {assign} 未通过独立检查器"
            return 1
        i = self._pick_var(domains)
        total = 0
        for v in sorted(domains[i]):
            sub = [set(d) for d in domains]
            sub[i] = {v}
            sub = propagate(sub, self.values, self.low, self.high, self._log)
            if sub is not None:
                total += self._count(sub)
        return total


if __name__ == "__main__":
    # 运行样例
    n = 5
    domains = [set(range(1, n + 1)) for _ in range(n)]
    domains[0] = {3, 4, 5}
    domains[4] = {1, 2}
    low, high = 4, 6
    print(f"变量数 n={n}，值域 1..{n}，逆序数区间 [{low}, {high}]")
    print(f"初始域：{[sorted(d) for d in domains]}")
    print("--- 求解轨迹 ---")
    solver = PermSolver(domains, low, high, verbose=True)
    sol = solver.solve()
    print("--- 结果 ---")
    if sol is None:
        print("无解")
    else:
        inv = inversion_count(sol)
        print(f"解：{sol}，逆序数 = {inv}（独立检查器已通过）")
    total = PermSolver(domains, low, high).count_solutions()
    print(f"该实例解的总数：{total}")
