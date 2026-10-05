// regalloc.cpp —— 基于动态规划的最优寄存器分配器（C++17）
//
// 模型
//   - T 条指令位置（0..T-1），R 个物理寄存器（题目 R=3），外加内存；
//     每个变量拥有独立的内存槽，因此内存槽之间永不冲突。
//   - 变量 v：活跃区间 [s,e]（闭区间，按指令位置计），权重 w（使用次数）。
//     假设变量在区间内的每个位置都被使用。
//   - 固定寄存器约束 (v, pos, r)：变量 v 在指令 pos 必须位于寄存器 r。
//   - 允许在指令边界拆分活跃区间（相邻位置的存放位置可以不同）。
//
// 代价
//   - 溢出代价：变量在某位置被使用且位于内存      -> w * SPILL
//   - 搬移代价：变量在相邻位置间改变存放位置
//     （寄存器<->寄存器 或 寄存器<->内存）        -> w * MOVE
//     区间起点免费放置，区间终点免费消失。
//
// 输入（stdin，或 argv[1] 指定的文件）
//   T R SPILL MOVE
//   N
//   name s e w        （共 N 行）
//   C
//   name pos reg      （共 C 行，reg 为 0..R-1）
//
// 输出
//   最小总代价及分解、逐指令位置分配表、逐边界搬移列表、正确性校验结果。
//
// 复杂度：每位置有效状态数 <= 1 + P(k,1) + P(k,2) + P(k,3)（k 为该位置活跃
//   变量数，k=12 时为 1465），转移用预计算的差异掩码 O(1) 求值。

#include <cstdio>
#include <cstdint>
#include <fstream>
#include <iostream>
#include <string>
#include <unordered_map>
#include <vector>
using namespace std;

using ll = long long;
static const ll INF = (ll)4e18;

int main(int argc, char** argv) {
    istream* in = &cin;
    ifstream fin;
    if (argc > 1) {
        fin.open(argv[1]);
        if (!fin) { fprintf(stderr, "无法打开输入文件 %s\n", argv[1]); return 1; }
        in = &fin;
    }
    istream& is = *in;

    // ---------- 输入 ----------
    int T, R; ll SPILL, MOVE;
    if (!(is >> T >> R >> SPILL >> MOVE)) { fprintf(stderr, "输入格式错误\n"); return 1; }
    int N; is >> N;
    if (N < 0 || N > 15) { fprintf(stderr, "变量数需满足 0 <= N <= 15\n"); return 1; }
    vector<string> name(N);
    vector<int> S(N), E(N);
    vector<ll> W(N);
    unordered_map<string,int> idx;
    for (int v = 0; v < N; v++) {
        if (!(is >> name[v] >> S[v] >> E[v] >> W[v])) { fprintf(stderr, "变量定义不完整\n"); return 1; }
        if (idx.count(name[v])) { fprintf(stderr, "变量名重复: %s\n", name[v].c_str()); return 1; }
        idx[name[v]] = v;
        if (S[v] < 0 || E[v] >= T || S[v] > E[v]) { fprintf(stderr, "变量 %s 的区间非法\n", name[v].c_str()); return 1; }
    }
    int C; is >> C;
    struct Cons { int v, pos, r; };
    vector<Cons> cons;
    vector<vector<Cons>> consAt(T);
    for (int i = 0; i < C; i++) {
        string nm; int pos, r;
        if (!(is >> nm >> pos >> r)) { fprintf(stderr, "约束定义不完整\n"); return 1; }
        auto it = idx.find(nm);
        if (it == idx.end()) { fprintf(stderr, "约束中的变量不存在: %s\n", nm.c_str()); return 1; }
        if (pos < 0 || pos >= T || r < 0 || r >= R) { fprintf(stderr, "约束 (%s,%d,%d) 非法\n", nm.c_str(), pos, r); return 1; }
        int v = it->second;
        if (pos < S[v] || pos > E[v]) { fprintf(stderr, "警告：约束 (%s,%d,R%d) 位于活跃区间之外，忽略\n", nm.c_str(), pos, r); continue; }
        cons.push_back({v, pos, r});
        consAt[pos].push_back({v, pos, r});
    }

    // ---------- 活跃掩码与权重和 ----------
    vector<uint32_t> liveMask(T, 0);
    for (int v = 0; v < N; v++)
        for (int i = S[v]; i <= E[v]; i++) liveMask[i] |= (1u << v);
    vector<ll> wsum(1u << N, 0);
    for (uint32_t m = 1; m < (1u << N); m++) {
        int b = __builtin_ctz(m);
        wsum[m] = wsum[m ^ (1u << b)] + W[b];
    }

    // ---------- 状态编码：寄存器 -> 变量 的单射 ----------
    // code 的 R 个 (N+1) 进制位：第 r 位 = 寄存器 r 中的变量编号 + 1（0 表示空）。
    const int B = N + 1;
    int numCodes = 1;
    for (int i = 0; i < R; i++) {
        if (numCodes > 2000000 / B) { fprintf(stderr, "状态空间过大\n"); return 1; }
        numCodes *= B;
    }
    vector<char> valid(numCodes, 0);
    vector<vector<int8_t>> regVar(numCodes);      // regVar[code][r] = 变量或 -1
    vector<vector<int8_t>> locOf(numCodes);       // locOf[code][v] = 寄存器号或 -1(内存)
    vector<uint32_t> memMask(numCodes, 0);        // 处于内存的变量集合（含非活跃）
    for (int code = 0; code < numCodes; code++) {
        int x = code;
        vector<int8_t> rv(R);
        for (int r = 0; r < R; r++) { rv[r] = (int8_t)(x % B) - 1; x /= B; }
        regVar[code] = rv;
        bool ok = true;
        vector<char> seen(N, 0);
        for (int r = 0; r < R && ok; r++)
            if (rv[r] >= 0) { if (seen[rv[r]]) ok = false; else seen[rv[r]] = 1; }
        if (!ok) continue;
        valid[code] = 1;
        vector<int8_t> loc(N, -1);
        for (int r = 0; r < R; r++) if (rv[r] >= 0) loc[rv[r]] = (int8_t)r;
        locOf[code] = loc;
        uint32_t mm = 0;
        for (int v = 0; v < N; v++) if (loc[v] < 0) mm |= (1u << v);
        memMask[code] = mm;
    }

    // 每个位置的合法状态：寄存器只放活跃变量，且满足该位置的固定约束
    vector<vector<int>> validAt(T);
    for (int i = 0; i < T; i++) {
        for (int code = 0; code < numCodes; code++) {
            if (!valid[code]) continue;
            bool ok = true;
            for (int r = 0; r < R && ok; r++) {
                int v = regVar[code][r];
                if (v >= 0 && !((liveMask[i] >> v) & 1)) ok = false;
            }
            if (!ok) continue;
            for (auto& cn : consAt[i])
                if (locOf[code][cn.v] != cn.r) { ok = false; break; }
            if (ok) validAt[i].push_back(code);
        }
        if (validAt[i].empty()) {
            printf("位置 %d 没有任何合法状态：固定寄存器约束互相冲突，无可行分配。\n", i);
            return 0;
        }
    }

    // 状态对差异掩码（位置变化与活跃集合无关，可全局预计算）
    vector<uint16_t> diffTab;
    bool useTab = (ll)numCodes * numCodes <= 30000000;
    if (useTab) {
        diffTab.assign((size_t)numCodes * numCodes, 0);
        for (int p = 0; p < numCodes; p++) if (valid[p])
            for (int c = 0; c < numCodes; c++) if (valid[c]) {
                uint16_t m = 0;
                for (int v = 0; v < N; v++) if (locOf[p][v] != locOf[c][v]) m |= (uint16_t)(1u << v);
                diffTab[(size_t)p * numCodes + c] = m;
            }
    }
    auto getDiff = [&](int p, int c) -> uint32_t {
        if (useTab) return diffTab[(size_t)p * numCodes + c];
        uint32_t m = 0;
        for (int v = 0; v < N; v++) if (locOf[p][v] != locOf[c][v]) m |= (1u << v);
        return m;
    };

    // ---------- 动态规划 ----------
    auto posCost = [&](int i, int code) -> ll {
        return SPILL * wsum[memMask[code] & liveMask[i]];
    };
    vector<ll> dpPrev(numCodes, INF), dpCur(numCodes, INF);
    vector<vector<int>> parent(T, vector<int>(numCodes, -1));
    for (int c : validAt[0]) dpPrev[c] = posCost(0, c);
    for (int i = 1; i < T; i++) {
        uint32_t both = liveMask[i - 1] & liveMask[i];
        for (int c : validAt[i]) {
            ll best = INF; int bp = -1;
            for (int p : validAt[i - 1]) {
                if (dpPrev[p] == INF) continue;
                ll cost = dpPrev[p] + MOVE * wsum[getDiff(p, c) & both];
                if (cost < best) { best = cost; bp = p; }
            }
            if (bp >= 0) { dpCur[c] = best + posCost(i, c); parent[i][c] = bp; }
        }
        swap(dpPrev, dpCur);
        fill(dpCur.begin(), dpCur.end(), INF);
    }
    ll bestFinal = INF; int bestCode = -1;
    for (int c : validAt[T - 1]) if (dpPrev[c] < bestFinal) { bestFinal = dpPrev[c]; bestCode = c; }
    if (bestCode < 0) { printf("无可行分配。\n"); return 0; }

    // ---------- 回溯 ----------
    vector<int> codes(T);
    codes[T - 1] = bestCode;
    for (int i = T - 1; i >= 1; i--) codes[i - 1] = parent[i][codes[i]];

    // ---------- 输出 ----------
    auto locName = [&](int loc) -> string {
        if (loc < 0) return "MEM";
        return "R" + to_string(loc);
    };
    printf("最小总代价: %lld\n", bestFinal);

    ll spillTotal = 0, moveTotal = 0;
    for (int i = 0; i < T; i++) spillTotal += posCost(i, codes[i]);
    struct Mv { int pos, v, from, to; ll cost; };
    vector<Mv> moves;
    for (int i = 1; i < T; i++) {
        uint32_t both = liveMask[i - 1] & liveMask[i];
        for (int v = 0; v < N; v++) if ((both >> v) & 1) {
            int lf = locOf[codes[i - 1]][v], lt = locOf[codes[i]][v];
            if (lf != lt) { moves.push_back({i, v, lf, lt, MOVE * W[v]}); moveTotal += MOVE * W[v]; }
        }
    }
    printf("  其中溢出代价 %lld，搬移代价 %lld\n\n", spillTotal, moveTotal);

    printf("逐指令位置分配（MEM = 内存槽，每变量独立）：\n");
    printf("%4s", "位置");
    for (int r = 0; r < R; r++) printf("  %-4s", ("R" + to_string(r)).c_str());
    printf("  %-s\n", "MEM");
    for (int i = 0; i < T; i++) {
        printf("%4d", i);
        for (int r = 0; r < R; r++) {
            int v = regVar[codes[i]][r];
            printf("  %-4s", v >= 0 ? name[v].c_str() : "-");
        }
        string mems;
        for (int v = 0; v < N; v++)
            if (((liveMask[i] >> v) & 1) && locOf[codes[i]][v] < 0)
                mems += name[v], mems += ' ';
        printf("  %-s\n", mems.empty() ? "-" : mems.c_str());
    }

    printf("\n搬移列表（在指令边界处）：\n");
    if (moves.empty()) printf("  （无）\n");
    for (auto& m : moves)
        printf("  边界 %d->%d: %s: %s -> %s  (代价 %lld)\n",
               m.pos - 1, m.pos, name[m.v].c_str(), locName(m.from).c_str(), locName(m.to).c_str(), m.cost);

    // ---------- 校验 ----------
    bool ok = true;
    auto fail = [&](const string& msg) { printf("%s", msg.c_str()); ok = false; };
    for (int i = 0; i < T; i++) {
        int code = codes[i];
        vector<char> inReg(N, 0);
        for (int r = 0; r < R; r++) {
            int v = regVar[code][r];
            if (v < 0) continue;
            if (!((liveMask[i] >> v) & 1)) fail("校验失败：位置 " + to_string(i) + " 寄存器 R" + to_string(r) + " 存放了不活跃的 " + name[v] + "\n");
            if (inReg[v]) fail("校验失败：位置 " + to_string(i) + " 变量 " + name[v] + " 同时占用多个寄存器\n");
            inReg[v] = 1;
        }
        // 同时活跃变量不得共用位置：任意两个活跃变量占用的寄存器必须不同
        for (int r1 = 0; r1 < R; r1++)
            for (int r2 = r1 + 1; r2 < R; r2++) {
                int v1 = regVar[code][r1], v2 = regVar[code][r2];
                if (v1 >= 0 && v1 == v2) fail("校验失败：位置 " + to_string(i) + " 寄存器 R" + to_string(r1) + " 与 R" + to_string(r2) + " 被 " + name[v1] + " 共用\n");
            }
        for (auto& cn : consAt[i])
            if (locOf[code][cn.v] != cn.r)
                fail("校验失败：位置 " + to_string(i) + " 变量 " + name[cn.v] + " 应固定在 R" + to_string(cn.r) + "\n");
    }
    // 搬移与分配一致性：相邻位置间每个持续活跃变量的位置变化必须一一对应
    for (int i = 1; i < T; i++) {
        uint32_t both = liveMask[i - 1] & liveMask[i];
        uint32_t changed = 0;
        for (int v = 0; v < N; v++) if ((both >> v) & 1)
            if (locOf[codes[i - 1]][v] != locOf[codes[i]][v]) changed |= (1u << v);
        uint32_t recorded = 0;
        for (auto& m : moves) if (m.pos == i) recorded |= (1u << m.v);
        if (changed != recorded) fail("校验失败：边界 " + to_string(i - 1) + "->" + to_string(i) + " 的搬移与分配不一致\n");
    }
    // 代价复核
    if (spillTotal + moveTotal != bestFinal) fail("校验失败：代价分解与总代价不符\n");

    printf("\n校验结果: %s（同时活跃变量未共用位置，固定约束满足，搬移一致）\n",
           ok ? "通过" : "失败");
    return ok ? 0 : 2;
}
