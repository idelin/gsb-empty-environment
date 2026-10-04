// regalloc.cpp — 最小加权溢出成本的寄存器分配器 (C++17)
//
// 模型:
//   - P 个指令位置 (0..P-1), R 个物理寄存器 (题目中 R=3), 外加内存 MEM。
//   - 每个变量有活跃区间 [start, end] (闭区间)、权重 weight (每次使用若处于内存中的溢出代价)。
//   - use <var> <pos> [cnt]  : 变量在 pos 处被使用 cnt 次; 若该位置变量在 MEM, 代价 += weight*cnt。
//   - fix <var> <pos> <reg>  : 固定寄存器约束, 变量在 pos 处必须位于寄存器 reg。
//   - 允许在指令边界拆分区间: 相邻位置间变量可改变位置, 产生搬移成本:
//       reg->reg = moveCost, reg->MEM = storeCost (溢出写回), MEM->reg = loadCost (重新加载)。
//   - 变量诞生/消亡时不产生搬移成本; 内存中每个变量有独立溢出槽 (MEM 可共享, 寄存器不可)。
//
// 算法: 按位置 DP。状态 = 活跃变量到位置的映射 (寄存器注入式分配)。
//   k 个活跃变量、R 个寄存器时状态数 = sum_{i=0..R} P(k,i); k=12,R=3 时为 1465。
//
// 输入 (stdin, '#' 为注释):
//   positions <P>
//   regs <R>
//   costs <move> <load> <store>
//   var <name> <start> <end> <weight>      (每个变量一行)
//   use <name> <pos> [count]               (可多次)
//   fix <name> <pos> <reg>                 (可多次)
//
// 输出: 最小总成本及分解、逐位置分配、逐边界搬移, 并自检 (寄存器不共享/固定约束/成本复核)。

#include <cstdint>
#include <functional>
#include <iomanip>
#include <iostream>
#include <map>
#include <sstream>
#include <string>
#include <unordered_map>
#include <utility>
#include <vector>
using namespace std;

static const long long INF = (long long)4e18;

struct Var {
    string name;
    int start, end;     // 活跃区间, 闭区间
    long long weight;   // 每次使用时的溢出代价
};

int N, P, R;
long long moveCost, loadCost, storeCost;
vector<Var> vars;
unordered_map<string,int> varId;
vector<map<int,int>> usesAt;    // usesAt[v][pos] = 使用次数
vector<map<int,int>> fixedAt;   // fixedAt[v][pos] = 固定寄存器

vector<vector<int>> liveVars;             // liveVars[p] = 位置 p 处活跃的变量
vector<vector<int>> slotOf;               // slotOf[p][v] = 在 liveVars[p] 中的下标, 或 -1
vector<vector<vector<int8_t>>> states;    // states[p][s][slot] = 位置 (0..R-1 寄存器, R = MEM)
vector<vector<long long>> stateCost;      // 该状态在位置 p 的溢出成本

static string locName(int8_t loc) {
    if (loc == R) return "MEM";
    return "R" + to_string((int)loc);
}

// 相邻位置间位置变化的搬移成本
static long long changeCost(int8_t from, int8_t to) {
    if (from == to) return 0;
    if (from == R) return loadCost;   // MEM -> reg : reload
    if (to == R)   return storeCost;  // reg -> MEM : spill store
    return moveCost;                  // reg -> reg
}

// 枚举位置 p 的所有合法状态 (寄存器注入式分配, 满足固定约束)
static void genPosition(int p) {
    const auto& liv = liveVars[p];
    int k = (int)liv.size();
    vector<int8_t> cur(k);
    function<void(int,int)> dfs = [&](int i, int usedMask) {
        if (i == k) {
            long long cost = 0;
            for (int j = 0; j < k; j++) {
                int v = liv[j];
                auto it = usesAt[v].find(p);
                if (it != usesAt[v].end() && cur[j] == R)
                    cost += vars[v].weight * it->second;
            }
            states[p].push_back(cur);
            stateCost[p].push_back(cost);
            return;
        }
        int v = liv[i];
        auto fx = fixedAt[v].find(p);
        if (fx != fixedAt[v].end()) {           // 固定寄存器约束
            int r = fx->second;
            if (usedMask >> r & 1) return;      // 与其他固定约束冲突
            cur[i] = (int8_t)r;
            dfs(i + 1, usedMask | (1 << r));
            return;
        }
        cur[i] = (int8_t)R;                     // 放内存
        dfs(i + 1, usedMask);
        for (int r = 0; r < R; r++)             // 放某个空闲寄存器
            if (!(usedMask >> r & 1)) {
                cur[i] = (int8_t)r;
                dfs(i + 1, usedMask | (1 << r));
            }
    };
    dfs(0, 0);
}

int main() {
    ios::sync_with_stdio(false);
    cin.tie(nullptr);

    // ---------- 解析输入 ----------
    vector<vector<string>> cmds;
    string line;
    while (getline(cin, line)) {
        auto hash = line.find('#');
        if (hash != string::npos) line = line.substr(0, hash);
        istringstream iss(line);
        vector<string> tok; string w;
        while (iss >> w) tok.push_back(w);
        if (!tok.empty()) cmds.push_back(std::move(tok));
    }
    bool haveP = false, haveR = false, haveCosts = false;
    for (auto& t : cmds) {
        if (t[0] == "positions") { P = stoi(t[1]); haveP = true; }
        else if (t[0] == "regs") { R = stoi(t[1]); haveR = true; }
        else if (t[0] == "costs") {
            moveCost = stoll(t[1]); loadCost = stoll(t[2]); storeCost = stoll(t[3]);
            haveCosts = true;
        } else if (t[0] == "var") {
            if (t.size() < 5) { cerr << "bad var line\n"; return 1; }
            Var v; v.name = t[1]; v.start = stoi(t[2]); v.end = stoi(t[3]); v.weight = stoll(t[4]);
            if (varId.count(v.name)) { cerr << "duplicate var " << v.name << "\n"; return 1; }
            varId[v.name] = (int)vars.size();
            vars.push_back(v);
        }
    }
    if (!haveP || !haveR || !haveCosts || vars.empty()) {
        cerr << "need: positions, regs, costs, and at least one var\n";
        return 1;
    }
    N = (int)vars.size();
    if (N > 12) { cerr << "too many variables (limit 12)\n"; return 1; }
    usesAt.assign(N, {});
    fixedAt.assign(N, {});
    for (auto& t : cmds) {
        if (t[0] == "use") {
            int v = varId.count(t[1]) ? varId[t[1]] : -1;
            if (v < 0) { cerr << "unknown var " << t[1] << "\n"; return 1; }
            int p = stoi(t[2]);
            int c = t.size() > 3 ? stoi(t[3]) : 1;
            usesAt[v][p] += c;
        } else if (t[0] == "fix") {
            int v = varId.count(t[1]) ? varId[t[1]] : -1;
            if (v < 0) { cerr << "unknown var " << t[1] << "\n"; return 1; }
            int p = stoi(t[2]), r = stoi(t[3]);
            if (r < 0 || r >= R) { cerr << "bad reg in fix\n"; return 1; }
            fixedAt[v][p] = r;
        }
    }
    for (int v = 0; v < N; v++) {
        if (vars[v].start < 0 || vars[v].end >= P || vars[v].start > vars[v].end) {
            cerr << "bad interval for var " << vars[v].name << "\n";
            return 1;
        }
        for (auto& [p, c] : usesAt[v])
            if (p < vars[v].start || p > vars[v].end) {
                cerr << "use of " << vars[v].name << " at " << p << " outside live interval\n";
                return 1;
            }
        for (auto& [p, r] : fixedAt[v])
            if (p < vars[v].start || p > vars[v].end) {
                cerr << "fix of " << vars[v].name << " at " << p << " outside live interval\n";
                return 1;
            }
    }

    // ---------- 每位置活跃集合 ----------
    liveVars.assign(P, {});
    slotOf.assign(P, vector<int>(N, -1));
    for (int p = 0; p < P; p++)
        for (int v = 0; v < N; v++)
            if (vars[v].start <= p && p <= vars[v].end) {
                slotOf[p][v] = (int)liveVars[p].size();
                liveVars[p].push_back(v);
            }

    // ---------- 状态枚举 ----------
    states.assign(P, {});
    stateCost.assign(P, {});
    for (int p = 0; p < P; p++) {
        genPosition(p);
        if (states[p].empty()) {
            cerr << "INFEASIBLE: no legal assignment at position " << p
                 << " (conflicting fixed constraints or too few registers)\n";
            return 1;
        }
    }

    // ---------- DP ----------
    vector<vector<long long>> dp(P);
    vector<vector<int>> par(P);
    dp[0] = stateCost[0];
    par[0].assign(states[0].size(), -1);
    for (int p = 1; p < P; p++) {
        int S1 = (int)states[p - 1].size(), S2 = (int)states[p].size();
        dp[p].assign(S2, INF);
        par[p].assign(S2, -1);
        // 公共活跃变量的 (slotPrev, slotCur) 对
        vector<pair<int,int>> common;
        for (int v : liveVars[p - 1])
            if (slotOf[p][v] >= 0) common.push_back({slotOf[p - 1][v], slotOf[p][v]});
        for (int j = 0; j < S2; j++) {
            long long best = INF; int bestI = -1;
            for (int i = 0; i < S1; i++) {
                if (dp[p - 1][i] >= INF) continue;
                long long mv = 0;
                for (auto& [sp, sc] : common)
                    mv += changeCost(states[p - 1][i][sp], states[p][j][sc]);
                long long cand = dp[p - 1][i] + mv;
                if (cand < best) { best = cand; bestI = i; }
            }
            if (bestI >= 0) {
                dp[p][j] = best + stateCost[p][j];
                par[p][j] = bestI;
            }
        }
    }

    // ---------- 回溯最优解 ----------
    int last = P - 1;
    long long bestCost = INF; int bestS = -1;
    for (int j = 0; j < (int)states[last].size(); j++)
        if (dp[last][j] < bestCost) { bestCost = dp[last][j]; bestS = j; }
    if (bestS < 0) { cerr << "INFEASIBLE\n"; return 1; }

    vector<int> path(P);
    path[last] = bestS;
    for (int p = last; p > 0; p--) path[p - 1] = par[p][path[p]];

    // ---------- 自检: 寄存器不共享 + 固定约束 + 成本复核 ----------
    long long checkSpill = 0, checkMove = 0;
    bool ok = true;
    for (int p = 0; p < P; p++) {
        auto& st = states[p][path[p]];
        vector<int> regOwner(R, -1);
        for (int s = 0; s < (int)liveVars[p].size(); s++) {
            int v = liveVars[p][s];
            int8_t loc = st[s];
            if (loc < R) {
                if (regOwner[loc] != -1) {
                    cerr << "CHECK FAIL: pos " << p << " reg R" << (int)loc << " shared by "
                         << vars[regOwner[loc]].name << " and " << vars[v].name << "\n";
                    ok = false;
                }
                regOwner[loc] = v;
            }
            auto fx = fixedAt[v].find(p);
            if (fx != fixedAt[v].end() && loc != fx->second) {
                cerr << "CHECK FAIL: pos " << p << " " << vars[v].name
                     << " violates fixed constraint\n";
                ok = false;
            }
            auto it = usesAt[v].find(p);
            if (it != usesAt[v].end() && loc == R)
                checkSpill += vars[v].weight * it->second;
        }
        if (p > 0) {
            auto& prev = states[p - 1][path[p - 1]];
            for (int v : liveVars[p - 1])
                if (slotOf[p][v] >= 0)
                    checkMove += changeCost(prev[slotOf[p - 1][v]], st[slotOf[p][v]]);
        }
    }
    if (checkSpill + checkMove != bestCost) {
        cerr << "CHECK FAIL: recomputed cost " << checkSpill + checkMove
             << " != DP cost " << bestCost << "\n";
        ok = false;
    }

    // ---------- 输出 ----------
    cout << "=== 最优寄存器分配 ===\n";
    cout << "最小总成本 = " << bestCost
         << "  (溢出 " << checkSpill << " + 搬移 " << checkMove << ")\n\n";
    cout << "逐位置分配:\n";
    for (int p = 0; p < P; p++) {
        cout << "  pos " << setw(2) << p << ": ";
        auto& st = states[p][path[p]];
        bool first = true;
        for (int s = 0; s < (int)liveVars[p].size(); s++) {
            if (!first) cout << "  ";
            first = false;
            cout << vars[liveVars[p][s]].name << "=" << locName(st[s]);
        }
        if (first) cout << "(无活跃变量)";
        cout << "\n";
    }
    cout << "\n搬移序列:\n";
    bool anyMove = false;
    for (int p = 1; p < P; p++) {
        auto& prev = states[p - 1][path[p - 1]];
        auto& cur = states[p][path[p]];
        vector<string> ms;
        for (int v : liveVars[p - 1]) {
            if (slotOf[p][v] < 0) continue;
            int8_t a = prev[slotOf[p - 1][v]], b = cur[slotOf[p][v]];
            if (a == b) continue;
            const char* kind = (a == R) ? "reload" : (b == R) ? "spill " : "move  ";
            ostringstream os;
            os << vars[v].name << ": " << locName(a) << " -> " << locName(b)
               << "  (" << kind << ", +" << changeCost(a, b) << ")";
            ms.push_back(os.str());
        }
        if (!ms.empty()) {
            anyMove = true;
            cout << "  " << (p - 1) << " -> " << p << ": ";
            for (size_t i = 0; i < ms.size(); i++)
                cout << (i ? "; " : "") << ms[i];
            cout << "\n";
        }
    }
    if (!anyMove) cout << "  (无搬移)\n";

    cout << "\n自检: " << (ok ? "通过 — 任意位置无两个活跃变量共用寄存器, 固定约束满足, 成本复核一致"
                              : "失败") << "\n";
    return ok ? 0 : 2;
}
