// 二维排料求解器（C++17）
// - 零件：整数顶点、边轴对齐的简单多边形（允许凹多边形），可旋转 0/90/180/270
// - 板材：W x H 整数网格，含若干缺陷矩形
// - 约束：轴对齐放置；零件完整在板内；零件-零件、零件-缺陷间距 >= kerf（刀缝）
// - 目标：最多 12 件，使放入零件总价值最大
// - 碰撞检测：多边形精确分解为矩形集合后做矩形级（含 kerf 膨胀）相交测试，
//   不使用包围盒近似（凹口内的缺陷不会被误判，也不会漏判）
#include <algorithm>
#include <cstdio>
#include <fstream>
#include <iostream>
#include <set>
#include <sstream>
#include <string>
#include <vector>
using namespace std;

typedef long long ll;

struct Pt { ll x, y; };
struct Rect { ll x1, y1, x2, y2; };

static bool overlapStrict(const Rect& a, const Rect& b) {
    return a.x1 < b.x2 && b.x1 < a.x2 && a.y1 < b.y2 && b.y1 < a.y2;
}
static Rect inflate(const Rect& r, ll k) { return {r.x1 - k, r.y1 - k, r.x2 + k, r.y2 + k}; }

// 将边轴对齐的简单多边形精确分解为不重叠矩形（扫描线法，整数坐标精确）
static vector<Rect> decompose(const vector<Pt>& poly) {
    vector<ll> ys;
    for (auto& p : poly) ys.push_back(p.y);
    sort(ys.begin(), ys.end());
    ys.erase(unique(ys.begin(), ys.end()), ys.end());
    vector<Rect> out;
    for (size_t i = 0; i + 1 < ys.size(); i++) {
        ll y0 = ys[i], y1 = ys[i + 1];
        vector<ll> xs;
        for (size_t j = 0; j < poly.size(); j++) {
            Pt a = poly[j], b = poly[(j + 1) % poly.size()];
            if (a.x != b.x) continue; // 只看竖边
            ll lo = min(a.y, b.y), hi = max(a.y, b.y);
            if (lo <= y0 && hi >= y1) xs.push_back(a.x); // 竖边跨越整个水平条带
        }
        sort(xs.begin(), xs.end());
        for (size_t k = 0; k + 1 < xs.size(); k += 2)
            if (xs[k] < xs[k + 1]) out.push_back({xs[k], y0, xs[k + 1], y1});
    }
    return out;
}

struct Variant { // 一种旋转形态（已归一化到 min=0）
    vector<Pt> poly;
    vector<Rect> rects;
    ll w = 0, h = 0;
};

static Variant normalize(vector<Pt> p) {
    ll mx = p[0].x, my = p[0].y;
    for (auto& q : p) { mx = min(mx, q.x); my = min(my, q.y); }
    for (auto& q : p) { q.x -= mx; q.y -= my; }
    Variant v; v.poly = p; v.rects = decompose(p);
    for (auto& r : v.rects) { v.w = max(v.w, r.x2); v.h = max(v.h, r.y2); }
    return v;
}
static vector<Pt> rot90(const vector<Pt>& p) { // 顺时针 90°: (x,y)->(y,-x)
    vector<Pt> r; for (auto& q : p) r.push_back({q.y, -q.x}); return r;
}
static string rectKey(const vector<Rect>& rs) {
    vector<Rect> s = rs; sort(s.begin(), s.end(), [](const Rect&a, const Rect&b){
        if (a.x1!=b.x1) return a.x1<b.x1; if (a.y1!=b.y1) return a.y1<b.y1;
        if (a.x2!=b.x2) return a.x2<b.x2; return a.y2<b.y2; });
    string k; char buf[64];
    for (auto& r : s) { snprintf(buf, sizeof buf, "%lld,%lld,%lld,%lld;", r.x1,r.y1,r.x2,r.y2); k += buf; }
    return k;
}

struct Part { int id; ll value; vector<Variant> vars; };

struct Input {
    ll W, H, kerf;
    vector<Rect> defects;
    vector<Part> parts;
};

static Input readInput(istream& in) {
    Input in_;
    Input& I = in_;
    in >> I.W >> I.H >> I.kerf;
    int D; in >> D;
    for (int i = 0; i < D; i++) {
        Rect r; in >> r.x1 >> r.y1 >> r.x2 >> r.y2; I.defects.push_back(r);
    }
    int N; in >> N;
    for (int i = 0; i < N; i++) {
        Part p; int nv; in >> p.id >> p.value >> nv;
        vector<Pt> poly(nv);
        for (auto& q : poly) in >> q.x >> q.y;
        set<string> seen;
        vector<Pt> cur = poly;
        for (int r = 0; r < 4; r++) {
            Variant v = normalize(cur);
            if (seen.insert(rectKey(v.rects)).second) p.vars.push_back(v);
            cur = rot90(cur);
        }
        I.parts.push_back(p);
    }
    return I;
}

struct Placed {
    int partId; int rot; ll x, y; ll value;
    vector<Rect> rects; // 平移后的精确矩形分解
};

// ---------- 可行性检查（精确，非包围盒） ----------
struct Checker {
    ll W, H, kerf;
    const vector<Rect>& defects;
    Checker(ll W_, ll H_, ll k, const vector<Rect>& d) : W(W_), H(H_), kerf(k), defects(d) {}

    bool insideSheet(const vector<Rect>& rs) const {
        for (auto& r : rs)
            if (r.x1 < 0 || r.y1 < 0 || r.x2 > W || r.y2 > H) return false;
        return true;
    }
    // 零件 vs 缺陷：缺陷膨胀 kerf 后与零件分解矩形严格相交 => 距离 < kerf
    bool hitsDefect(const vector<Rect>& rs) const {
        for (auto& d : defects) {
            Rect dd = inflate(d, kerf);
            for (auto& r : rs) if (overlapStrict(r, dd)) return true;
        }
        return false;
    }
    // 两零件间距 >= kerf：一方膨胀 kerf 后无严格相交
    static bool conflict(const vector<Rect>& a, const vector<Rect>& b, ll kerf) {
        for (auto& r : a) {
            Rect rr = inflate(r, kerf);
            for (auto& s : b) if (overlapStrict(rr, s)) return true;
        }
        return false;
    }
};

static vector<Rect> translate(const vector<Rect>& rs, ll dx, ll dy) {
    vector<Rect> out; out.reserve(rs.size());
    for (auto& r : rs) out.push_back({r.x1+dx, r.y1+dy, r.x2+dx, r.y2+dy});
    return out;
}

// ---------- 分支限界求解器 ----------
struct Solver {
    Input I;
    Checker chk;
    vector<int> order;            // 按价值降序的零件下标
    vector<ll> suffix;            // 价值后缀和（限界用）
    ll bestVal = 0;
    vector<Placed> best;
    ll nodes = 0;

    Solver(Input in) : I(std::move(in)), chk(I.W, I.H, I.kerf, I.defects) {
        int n = (int)I.parts.size();
        order.resize(n);
        for (int i = 0; i < n; i++) order[i] = i;
        sort(order.begin(), order.end(), [&](int a, int b){
            if (I.parts[a].value != I.parts[b].value) return I.parts[a].value > I.parts[b].value;
            return I.parts[a].id < I.parts[b].id;
        });
        suffix.assign(n + 1, 0);
        for (int i = n - 1; i >= 0; i--) suffix[i] = suffix[i+1] + I.parts[order[i]].value;
    }

    void dfs(int idx, ll curVal, vector<Placed>& placed) {
        nodes++;
        if (curVal + suffix[idx] <= bestVal) return; // 限界
        if (idx == (int)order.size()) {
            if (curVal > bestVal) { bestVal = curVal; best = placed; }
            return;
        }
        const Part& p = I.parts[order[idx]];
        // 候选坐标：法线模式（0、已放零件/缺陷的右、上边缘 + kerf）
        set<ll> xs{0}, ys{0};
        for (auto& pl : placed)
            for (auto& r : pl.rects) { xs.insert(r.x2 + I.kerf); ys.insert(r.y2 + I.kerf); }
        for (auto& d : I.defects) { xs.insert(d.x2 + I.kerf); ys.insert(d.y2 + I.kerf); }

        for (int vi = 0; vi < (int)p.vars.size(); vi++) {
            const Variant& v = p.vars[vi];
            for (ll y : ys) {
                if (y + v.h > I.H) continue;
                for (ll x : xs) {
                    if (x + v.w > I.W) continue;
                    vector<Rect> rs = translate(v.rects, x, y);
                    if (chk.hitsDefect(rs)) continue;
                    bool bad = false;
                    for (auto& pl : placed)
                        if (Checker::conflict(rs, pl.rects, I.kerf)) { bad = true; break; }
                    if (bad) continue;
                    placed.push_back({p.id, vi, x, y, p.value, rs});
                    dfs(idx + 1, curVal + p.value, placed);
                    placed.pop_back();
                }
            }
        }
        dfs(idx + 1, curVal, placed); // 跳过该零件
        if (idx == (int)order.size() - 1 && curVal > bestVal) { bestVal = curVal; best = placed; }
    }

    void solve() {
        vector<Placed> placed;
        dfs(0, 0, placed);
    }
};

// ---------- 独立验证器 ----------
// 重新读取输入与解文件，逐项检查并打印结果；全部通过返回 0。
static int verify(const Input& I, istream& solIn) {
    ll claimed; int cnt;
    if (!(solIn >> claimed >> cnt)) { cout << "FAIL: 解文件格式错误\n"; return 1; }
    bool ok = true;
    auto report = [&](bool pass, const string& msg) {
        cout << (pass ? "PASS" : "FAIL") << ": " << msg << "\n";
        if (!pass) ok = false;
    };
    report(cnt >= 0 && cnt <= 12, "零件数量 <= 12 (实际 " + to_string(cnt) + ")");
    report(cnt <= (int)I.parts.size(), "零件数量不超过输入零件总数");

    Checker chk(I.W, I.H, I.kerf, I.defects);
    set<int> used;
    vector<Placed> placed;
    ll total = 0;
    for (int i = 0; i < cnt; i++) {
        int id, rot; ll x, y;
        solIn >> id >> rot >> x >> y;
        auto it = find_if(I.parts.begin(), I.parts.end(), [&](const Part& p){ return p.id == id; });
        if (it == I.parts.end()) { report(false, "零件 id=" + to_string(id) + " 不存在"); continue; }
        if (!used.insert(id).second) { report(false, "零件 id=" + to_string(id) + " 重复使用"); continue; }
        if (rot < 0 || rot >= (int)it->vars.size()) { report(false, "零件 id=" + to_string(id) + " 旋转编号非法"); continue; }
        const Variant& v = it->vars[rot];
        vector<Rect> rs = translate(v.rects, x, y);
        report(chk.insideSheet(rs), "零件 id=" + to_string(id) + " 完整位于板材内");
        report(!chk.hitsDefect(rs), "零件 id=" + to_string(id) + " 与缺陷矩形间距 >= kerf（精确多边形检测）");
        for (auto& pl : placed)
            report(!Checker::conflict(rs, pl.rects, I.kerf),
                   "零件 id=" + to_string(id) + " 与 id=" + to_string(pl.partId) + " 间距 >= kerf");
        placed.push_back({id, rot, x, y, it->value, rs});
        total += it->value;
    }
    report(total == claimed, "总价值校验 (声明 " + to_string(claimed) + ", 实算 " + to_string(total) + ")");
    cout << (ok ? "VERIFY OK, total value = " : "VERIFY FAILED, total value = ") << total << "\n";
    return ok ? 0 : 1;
}

int main(int argc, char** argv) {
    if (argc < 3) {
        fprintf(stderr, "用法:\n  %s solve <input.txt> [solution.txt]\n  %s verify <input.txt> <solution.txt>\n", argv[0], argv[0]);
        return 2;
    }
    string mode = argv[1];
    ifstream fin(argv[2]);
    if (!fin) { fprintf(stderr, "无法打开输入文件 %s\n", argv[2]); return 2; }
    Input I = readInput(fin);

    if (mode == "solve") {
        if (I.parts.size() > 12) {
            fprintf(stderr, "输入零件数 %zu 超过 12 件上限\n", I.parts.size());
            return 2;
        }
        Solver s(std::move(I));
        s.solve();
        ostringstream out;
        out << s.bestVal << " " << s.best.size() << "\n";
        for (auto& pl : s.best) out << pl.partId << " " << pl.rot << " " << pl.x << " " << pl.y << "\n";
        string sol = out.str();
        cout << sol;
        fprintf(stderr, "搜索节点数: %lld\n", s.nodes);
        if (argc >= 4) { ofstream f(argv[3]); f << sol; }
        return 0;
    }
    if (mode == "verify") {
        if (argc < 4) { fprintf(stderr, "verify 需要解文件\n"); return 2; }
        ifstream sf(argv[3]);
        if (!sf) { fprintf(stderr, "无法打开解文件 %s\n", argv[3]); return 2; }
        return verify(I, sf);
    }
    fprintf(stderr, "未知模式 %s\n", mode.c_str());
    return 2;
}
