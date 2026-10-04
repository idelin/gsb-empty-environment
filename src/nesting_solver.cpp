// 二维排料精确求解器 + 完整可行性验证器 (C++17)
//
// 模型假设（在注释中明确）：
//   - 板材为 W x H 的整数网格矩形，左下角为 (0,0)。
//   - 零件为矩形，可旋转 90 度，放置必须轴对齐且坐标为整数。
//   - 固定刀缝 k：任意两个已放零件之间、零件与任意缺陷矩形之间，
//     欧氏意义下的轴对齐间隙必须 >= k（即把每个矩形四边向外膨胀 k 后不得相交）。
//     板材边界不要求刀缝。
//   - 缺陷为板材上的矩形禁放区，逐缺陷精确判定（不做包围盒合并近似）。
//   - 零件数 <= 12，目标为放入零件总价值最大。
//
// 精确性说明：
//   对任意可行放置，反复将所有零件向左、向下压实（直到贴板边、缺陷边+k
//   或其他零件边+k）不破坏可行性也不改变放置集合。因此存在最优解，其每个
//   零件的 x 属于 {0} ∪ {r.x2+k} ∪ {r.x1-k-pw}，y 同理（r 取缺陷与已放零件）。
//   分支定界在该有限候选集上枚举，配合上界剪枝，得到精确最优值。
//
// 用法：
//   nesting_solver solve  <input.txt>            求解并自检，输出放置方案
//   nesting_solver verify <input.txt> <sol.txt>  独立验证一个方案
//
// 输入格式 (input.txt)：
//   W H
//   KERF k
//   DEFECTS d
//   x1 y1 x2 y2        (d 行，整数，要求 0<=x1<x2<=W, 0<=y1<y2<=H)
//   PARTS n            (n <= 12)
//   w h v              (n 行，宽 高 价值，正整数)
//
// 方案格式 (sol.txt，solve 模式输出同样格式)：
//   VALUE V
//   PLACEMENTS m
//   id x y rotated     (m 行；id 为 0 基零件编号，rotated∈{0,1})

#include <algorithm>
#include <cstdint>
#include <fstream>
#include <iostream>
#include <set>
#include <sstream>
#include <string>
#include <vector>

using namespace std;

struct Rect {
    long long x1, y1, x2, y2; // 半开区间 [x1,x2) x [y1,y2)
};

struct Part {
    long long w, h, v;
    int id;
};

struct Placement {
    int id;
    long long x, y;
    int rotated;
};

struct Problem {
    long long W, H, kerf;
    vector<Rect> defects;
    vector<Part> parts; // 原始顺序，id 即下标
};

// 两个矩形在保留间隙 k 的意义下是否冲突（膨胀 k 后相交即冲突）。
// k=0 时退化为严格重叠判定（边贴边允许）。逐矩形精确判定，非包围盒近似。
static bool conflict(const Rect& a, const Rect& b, long long k) {
    return a.x1 < b.x2 + k && b.x1 < a.x2 + k &&
           a.y1 < b.y2 + k && b.y1 < a.y2 + k;
}

static bool readProblem(istream& in, Problem& P, string& err) {
    string tok;
    if (!(in >> P.W >> P.H)) { err = "缺少板材尺寸 W H"; return false; }
    if (P.W <= 0 || P.H <= 0) { err = "板材尺寸必须为正"; return false; }
    if (!(in >> tok) || tok != "KERF") { err = "缺少 KERF 行"; return false; }
    if (!(in >> P.kerf) || P.kerf < 0) { err = "刀缝必须为非负整数"; return false; }
    int d;
    if (!(in >> tok) || tok != "DEFECTS" || !(in >> d) || d < 0) { err = "DEFECTS 行格式错误"; return false; }
    for (int i = 0; i < d; i++) {
        Rect r;
        if (!(in >> r.x1 >> r.y1 >> r.x2 >> r.y2)) { err = "缺陷行不足"; return false; }
        if (!(r.x1 < r.x2 && r.y1 < r.y2)) { err = "缺陷矩形退化: 第 " + to_string(i) + " 个"; return false; }
        if (r.x1 < 0 || r.y1 < 0 || r.x2 > P.W || r.y2 > P.H) {
            err = "缺陷矩形超出板材: 第 " + to_string(i) + " 个"; return false;
        }
        P.defects.push_back(r);
    }
    int n;
    if (!(in >> tok) || tok != "PARTS" || !(in >> n)) { err = "PARTS 行格式错误"; return false; }
    if (n < 0 || n > 12) { err = "零件数必须在 [0,12]"; return false; }
    for (int i = 0; i < n; i++) {
        Part p; p.id = i;
        if (!(in >> p.w >> p.h >> p.v)) { err = "零件行不足"; return false; }
        if (p.w <= 0 || p.h <= 0 || p.v < 0) { err = "零件宽高必须为正、价值非负: id=" + to_string(i); return false; }
        P.parts.push_back(p);
    }
    return true;
}

// ---------------- 完整可行性验证器 ----------------
// 返回空 vector 表示可行；否则返回全部违规描述。
static vector<string> verify(const Problem& P, const vector<Placement>& sol, long long claimedValue) {
    vector<string> errs;
    vector<char> used(P.parts.size(), 0);
    vector<Rect> placed;
    long long total = 0;

    for (size_t i = 0; i < sol.size(); i++) {
        const Placement& pl = sol[i];
        string tag = "放置#" + to_string(i) + "(零件" + to_string(pl.id) + ")";
        if (pl.id < 0 || pl.id >= (int)P.parts.size()) {
            errs.push_back(tag + ": 零件编号越界");
            continue;
        }
        if (used[pl.id]) { errs.push_back(tag + ": 零件重复放置"); continue; }
        used[pl.id] = 1;
        if (pl.rotated != 0 && pl.rotated != 1) {
            errs.push_back(tag + ": rotated 必须为 0 或 1");
            continue;
        }
        const Part& p = P.parts[pl.id];
        long long pw = pl.rotated ? p.h : p.w;
        long long ph = pl.rotated ? p.w : p.h;
        Rect r{pl.x, pl.y, pl.x + pw, pl.y + ph};

        if (pl.x < 0 || pl.y < 0 || r.x2 > P.W || r.y2 > P.H) {
            errs.push_back(tag + ": 超出板材边界");
        }
        // 逐缺陷精确矩形相交（带刀缝膨胀），不合并包围盒。
        for (size_t j = 0; j < P.defects.size(); j++) {
            if (conflict(r, P.defects[j], P.kerf)) {
                errs.push_back(tag + ": 与缺陷#" + to_string(j) + " 间距不足/相交");
            }
        }
        for (size_t j = 0; j < placed.size(); j++) {
            if (conflict(r, placed[j], P.kerf)) {
                errs.push_back(tag + ": 与已放零件(序号" + to_string(j) + ") 间距不足/重叠");
            }
        }
        placed.push_back(r);
        total += p.v;
    }
    if (total != claimedValue) {
        errs.push_back("声明价值 " + to_string(claimedValue) + " 与实际合计 " + to_string(total) + " 不符");
    }
    return errs;
}

// ---------------- 精确分支定界求解器 ----------------
struct Solver {
    const Problem& P;
    vector<int> order;            // 按价值降序的零件下标
    vector<long long> suffixVal;  // 上界：剩余零件价值和
    vector<char> used;
    vector<Rect> placedRects;
    vector<Placement> placed;
    long long bestVal = 0;
    vector<Placement> best;

    explicit Solver(const Problem& prob) : P(prob) {
        int n = (int)P.parts.size();
        order.resize(n);
        for (int i = 0; i < n; i++) order[i] = i;
        stable_sort(order.begin(), order.end(), [&](int a, int b) {
            return P.parts[a].v > P.parts[b].v;
        });
        suffixVal.assign(n + 1, 0);
        for (int i = n - 1; i >= 0; i--) suffixVal[i] = suffixVal[i + 1] + P.parts[order[i]].v;
        used.assign(n, 0);
    }

    // 零件 (pw,ph) 放在 (x,y) 是否可行：板内、避开缺陷与其他零件（均带刀缝）。
    bool feasible(long long x, long long y, long long pw, long long ph) const {
        if (x < 0 || y < 0 || x + pw > P.W || y + ph > P.H) return false;
        Rect r{x, y, x + pw, y + ph};
        for (const Rect& d : P.defects) if (conflict(r, d, P.kerf)) return false;
        for (const Rect& q : placedRects) if (conflict(r, q, P.kerf)) return false;
        return true;
    }

    // 候选坐标：由压实论证，x ∈ {0} ∪ {r.x2+k} ∪ {r.x1-k-pw}，y 同理。
    void candidates(long long pw, long long ph, vector<pair<long long,long long>>& out) const {
        set<long long> xs, ys;
        xs.insert(0); ys.insert(0);
        auto addRects = [&](const Rect& r) {
            xs.insert(r.x2 + P.kerf);
            xs.insert(r.x1 - P.kerf - pw);
            ys.insert(r.y2 + P.kerf);
            ys.insert(r.y1 - P.kerf - ph);
        };
        for (const Rect& d : P.defects) addRects(d);
        for (const Rect& q : placedRects) addRects(q);
        for (long long x : xs)
            for (long long y : ys)
                if (feasible(x, y, pw, ph)) out.push_back({x, y});
    }

    void dfs(int depth, long long curVal) {
        if (curVal + suffixVal[depth] <= bestVal) return; // 上界剪枝
        if (depth == (int)order.size()) {
            if (curVal > bestVal) { bestVal = curVal; best = placed; }
            return;
        }
        int idx = order[depth];
        const Part& p = P.parts[idx];
        // 分支1：放置该零件（两种朝向；正方形只试一次）
        for (int rot = 0; rot < 2; rot++) {
            if (rot == 1 && p.w == p.h) break;
            long long pw = rot ? p.h : p.w;
            long long ph = rot ? p.w : p.h;
            vector<pair<long long,long long>> cands;
            candidates(pw, ph, cands);
            for (auto [x, y] : cands) {
                used[idx] = 1;
                placed.push_back({p.id, x, y, rot});
                placedRects.push_back({x, y, x + pw, y + ph});
                dfs(depth + 1, curVal + p.v);
                placedRects.pop_back();
                placed.pop_back();
                used[idx] = 0;
            }
        }
        // 分支2：跳过该零件
        dfs(depth + 1, curVal);
    }

    void greedySeed() {
        for (int idx : order) {
            const Part& p = P.parts[idx];
            long long bestScore = -1; Placement bp{-1, 0, 0, 0}; Rect br{0,0,0,0};
            for (int rot = 0; rot < 2; rot++) {
                if (rot == 1 && p.w == p.h) break;
                long long pw = rot ? p.h : p.w, ph = rot ? p.w : p.h;
                vector<pair<long long,long long>> cands;
                candidates(pw, ph, cands);
                for (auto [x, y] : cands) { // 偏好靠左下
                    long long score = -(x + y);
                    if (score > bestScore) { bestScore = score; bp = {p.id, x, y, rot}; br = {x, y, x + pw, y + ph}; }
                }
            }
            if (bp.id >= 0) {
                used[idx] = 1; placed.push_back(bp); placedRects.push_back(br);
                bestVal += p.v;
            }
        }
        best = placed;
        // 重置状态供精确搜索使用
        fill(used.begin(), used.end(), 0);
        placed.clear(); placedRects.clear();
    }

    void solve() {
        greedySeed();
        dfs(0, 0);
        // 按零件 id 排序输出，稳定可读
        sort(best.begin(), best.end(), [](const Placement& a, const Placement& b) { return a.id < b.id; });
    }
};

static void printSolution(const Problem& P, long long val, const vector<Placement>& sol, ostream& out) {
    out << "VALUE " << val << "\n";
    out << "PLACEMENTS " << sol.size() << "\n";
    for (const Placement& pl : sol) {
        const Part& p = P.parts[pl.id];
        long long pw = pl.rotated ? p.h : p.w;
        long long ph = pl.rotated ? p.w : p.h;
        out << pl.id << " " << pl.x << " " << pl.y << " " << pl.rotated
            << "   # " << pw << "x" << ph << " 价值" << p.v << "\n";
    }
}

static bool readSolution(istream& in, vector<Placement>& sol, long long& val, string& err) {
    string tok;
    int m;
    if (!(in >> tok) || tok != "VALUE" || !(in >> val)) { err = "缺少 VALUE 行"; return false; }
    if (!(in >> tok) || tok != "PLACEMENTS" || !(in >> m) || m < 0) { err = "PLACEMENTS 行格式错误"; return false; }
    for (int i = 0; i < m; i++) {
        string line;
        // 逐行读取以忽略行尾注释
        do { if (!getline(in, line)) { err = "放置行不足"; return false; } } while (line.find_first_not_of(" \t\r") == string::npos);
        auto hash = line.find('#');
        if (hash != string::npos) line = line.substr(0, hash);
        istringstream ls(line);
        Placement pl;
        if (!(ls >> pl.id >> pl.x >> pl.y >> pl.rotated)) { err = "放置行格式错误: 第 " + to_string(i) + " 行"; return false; }
        sol.push_back(pl);
    }
    return true;
}

int main(int argc, char** argv) {
    string mode = argc >= 2 ? argv[1] : "solve";
    auto usage = []() {
        cerr << "用法:\n"
             << "  nesting_solver solve  <input.txt>\n"
             << "  nesting_solver verify <input.txt> <solution.txt>\n";
        return 2;
    };

    if (mode == "solve") {
        if (argc != 3) return usage();
        ifstream fin(argv[2]);
        if (!fin) { cerr << "无法打开输入文件\n"; return 1; }
        Problem P; string err;
        if (!readProblem(fin, P, err)) { cerr << "输入错误: " << err << "\n"; return 1; }
        Solver s(P);
        s.solve();
        // 求解器自检：用独立验证器验证自身输出
        auto errs = verify(P, s.best, s.bestVal);
        if (!errs.empty()) {
            cerr << "内部错误：求解器产出不可行方案:\n";
            for (auto& e : errs) cerr << "  " << e << "\n";
            return 1;
        }
        printSolution(P, s.bestVal, s.best, cout);
        cerr << "最优值 " << s.bestVal << "，放置 " << s.best.size()
             << "/" << P.parts.size() << " 件，自检通过。\n";
        return 0;
    }

    if (mode == "verify") {
        if (argc != 4) return usage();
        ifstream fin(argv[2]), fsol(argv[3]);
        if (!fin || !fsol) { cerr << "无法打开文件\n"; return 1; }
        Problem P; string err;
        if (!readProblem(fin, P, err)) { cerr << "输入错误: " << err << "\n"; return 1; }
        vector<Placement> sol; long long val;
        if (!readSolution(fsol, sol, val, err)) { cerr << "方案格式错误: " << err << "\n"; return 1; }
        auto errs = verify(P, sol, val);
        if (errs.empty()) {
            cout << "FEASIBLE: 方案可行，总价值 " << val << "，放置 " << sol.size() << " 件。\n";
            return 0;
        }
        cout << "INFEASIBLE: 发现 " << errs.size() << " 处违规:\n";
        for (auto& e : errs) cout << "  - " << e << "\n";
        return 1;
    }

    return usage();
}
