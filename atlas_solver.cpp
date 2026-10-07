// atlas_solver.cpp — 纹理图集精确求解器 (C++17)
//
// 功能:
//   将最多 12 个矩形纹理装入最小整数边长的正方形图集。
//   - 允许每块纹理旋转 90 度;
//   - 每块纹理四周须保留 pad 像素隔离区(含图集边缘);
//   - 隔离区是否可共用由输入模式决定:
//       mode 0 (shared):    相邻纹理的隔离区可重叠 -> 核心间距 >= pad
//       mode 1 (exclusive): 隔离区不可共用         -> 核心间距 >= 2*pad
//   - 求解为精确算法: 对边长 S 二分, 可行性用完备回溯判定
//     (候选坐标取"规范模式": 边界 pad 或已有矩形边+gap),
//     因此返回的是可证明的最小边长, 而非启发式结果;
//   - 输出坐标后由独立碰撞检查器复检(不复用求解器逻辑);
//   - 支持 --check 模式: 独立验证外部给定的一组坐标。
//
// 输入 (stdin):
//   <mode> <pad>
//   <n>
//   <w1> <h1>
//   ...            (n 行, 1 <= n <= 12)
// 求解模式输出:
//   S <边长>
//   <i> <x> <y> <rotated>   (每块纹理核心左上角坐标, rotated∈{0,1})
//   CHECK OK
// 复检模式 (--check): 在矩形列表之后追加:
//   <S>
//   <x> <y> <rotated>       (n 行)
//   输出 CHECK OK / CHECK FAIL 及原因。

#include <algorithm>
#include <array>
#include <cmath>
#include <cstdio>
#include <numeric>
#include <string>
#include <vector>

struct Rect { int w, h; };

static int  n;            // 矩形数量
static int  pad;          // 隔离像素宽度
static bool sharedPad;    // 隔离区是否可共用
static int  gap;          // 核心间距要求: shared ? pad : 2*pad
static int  S;            // 当前试算的正方形边长
static std::vector<Rect> rects;
static std::vector<int>  ord;                 // 放置顺序(面积降序)
static std::vector<int>  px, py, pw, ph, prot;// 当前部分解
static std::vector<char> placed;

// ---------- 求解器 ----------

static bool sepOk(int x, int y, int w, int h) {
    if (x < pad || y < pad || x + w > S - pad || y + h > S - pad) return false;
    for (int j = 0; j < n; ++j) if (placed[j]) {
        bool xsep = (x + w + gap <= px[j]) || (px[j] + pw[j] + gap <= x);
        bool ysep = (y + h + gap <= py[j]) || (py[j] + ph[j] + gap <= y);
        if (!xsep && !ysep) return false;   // 两个方向间距都不足 -> 冲突
    }
    return true;
}

static bool dfs(int k) {
    if (k == n) return true;
    int i = ord[k];
    // 规范候选坐标: 任一最优解可平移为每块贴边界或贴其他矩形(含 gap)
    std::vector<int> xs{pad}, ys{pad};
    for (int j = 0; j < n; ++j) if (placed[j]) {
        xs.push_back(px[j] + pw[j] + gap);
        ys.push_back(py[j] + ph[j] + gap);
    }
    auto uniq = [](std::vector<int>& v){
        std::sort(v.begin(), v.end());
        v.erase(std::unique(v.begin(), v.end()), v.end());
    };
    uniq(xs); uniq(ys);

    for (int ori = 0; ori < 2; ++ori) {
        int w = ori ? rects[i].h : rects[i].w;
        int h = ori ? rects[i].w : rects[i].h;
        if (ori == 1 && rects[i].w == rects[i].h) break;      // 正方形无需重复
        if (k == 0 && w < h) continue;                        // 整体旋转对称: 首块横放
        if (w + 2 * pad > S || h + 2 * pad > S) continue;     // 单独都放不下
        for (int y : ys) for (int x : xs) {
            if (k == 0 && (2 * x + w > S || 2 * y + h > S)) continue; // 镜像对称
            if (!sepOk(x, y, w, h)) continue;
            placed[i] = 1; px[i] = x; py[i] = y; pw[i] = w; ph[i] = h; prot[i] = ori;
            if (dfs(k + 1)) return true;
            placed[i] = 0;
        }
    }
    return false;
}

static bool feasible(int side) {
    S = side;
    std::fill(placed.begin(), placed.end(), 0);
    return dfs(0);
}

// ---------- 独立碰撞检查器(不复用求解器任何逻辑) ----------

// placedRects[i] = {x, y, w, h} 为核心矩形; 返回空串表示通过, 否则返回错误描述
static std::string independentCheck(int side,
                                    const std::vector<std::array<long long,4>>& core) {
    if ((int)core.size() != n) return "rect count mismatch";
    // 1) 尺寸必须与某个输入矩形(允许旋转)一致
    std::vector<char> matched(n, 0);
    for (int i = 0; i < n; ++i) {
        long long w = core[i][2], h = core[i][3];
        bool ok = false;
        for (int j = 0; j < n && !ok; ++j) if (!matched[j]) {
            if ((rects[j].w == w && rects[j].h == h) ||
                (rects[j].w == h && rects[j].h == w)) { matched[j] = 1; ok = true; }
        }
        if (!ok) return "rect " + std::to_string(i) + " size matches no input rect";
    }
    // 2) 边界: 核心距图集边缘至少 pad
    for (int i = 0; i < n; ++i) {
        long long x = core[i][0], y = core[i][1], w = core[i][2], h = core[i][3];
        if (x < pad || y < pad || x + w > (long long)side - pad || y + h > (long long)side - pad)
            return "rect " + std::to_string(i) + " violates border padding";
    }
    // 3) 两两间距: x 或 y 方向净间距 >= gap
    for (int a = 0; a < n; ++a) for (int b = a + 1; b < n; ++b) {
        long long ax1 = core[a][0], ay1 = core[a][1], ax2 = ax1 + core[a][2], ay2 = ay1 + core[a][3];
        long long bx1 = core[b][0], by1 = core[b][1], bx2 = bx1 + core[b][2], by2 = by1 + core[b][3];
        long long gapX = std::max(bx1 - ax2, ax1 - bx2); // >0 表示 x 方向分离
        long long gapY = std::max(by1 - ay2, ay1 - by2);
        if (gapX < gap && gapY < gap)
            return "rects " + std::to_string(a) + " and " + std::to_string(b) +
                   " violate isolation gap " + std::to_string(gap);
    }
    return "";
}

// ---------- 主流程 ----------

int main(int argc, char** argv) {
    int mode;
    if (std::scanf("%d %d", &mode, &pad) != 2) { std::fprintf(stderr, "bad input\n"); return 2; }
    if (std::scanf("%d", &n) != 1 || n < 1 || n > 12) { std::fprintf(stderr, "bad n\n"); return 2; }
    rects.resize(n);
    for (auto& r : rects) if (std::scanf("%d %d", &r.w, &r.h) != 2) return 2;
    sharedPad = (mode == 0);
    gap = sharedPad ? pad : 2 * pad;

    bool checkOnly = (argc > 1 && std::string(argv[1]) == "--check");

    if (checkOnly) {
        int side; if (std::scanf("%d", &side) != 1) return 2;
        std::vector<std::array<long long,4>> core(n);
        for (int i = 0; i < n; ++i) {
            long long x, y; int rot;
            if (std::scanf("%lld %lld %d", &x, &y, &rot) != 3) return 2;
            core[i] = {x, y, rot ? rects[i].h : rects[i].w, rot ? rects[i].w : rects[i].h};
        }
        std::string err = independentCheck(side, core);
        if (err.empty()) { std::printf("CHECK OK\n"); return 0; }
        std::printf("CHECK FAIL: %s\n", err.c_str()); return 1;
    }

    // 下界: 单块可放 + 总面积
    long long area = 0; int lb = 0;
    for (auto& r : rects) {
        area += 1LL * r.w * r.h;
        lb = std::max(lb, std::max(r.w, r.h) + 2 * pad);
    }
    lb = std::max(lb, (int)std::ceil(std::sqrt((double)area)));
    // 上界: 全部横排成一行(必然可行)
    long long row = 2LL * pad, hmax = 0;
    for (auto& r : rects) {
        int w = std::max(r.w, r.h), h = std::min(r.w, r.h);
        row += w; hmax = std::max(hmax, (long long)h);
    }
    row += 1LL * gap * (n - 1);
    int ub = (int)std::max(row, (long long)hmax + 2LL * pad);

    ord.resize(n); std::iota(ord.begin(), ord.end(), 0);
    std::sort(ord.begin(), ord.end(), [&](int a, int b){
        return 1LL * rects[a].w * rects[a].h > 1LL * rects[b].w * rects[b].h;
    });
    px.assign(n,0); py.assign(n,0); pw.assign(n,0); ph.assign(n,0);
    prot.assign(n,0); placed.assign(n,0);

    // 可行性关于 S 单调, 二分最小可行边长
    int lo = lb - 1, hi = ub;   // lo 不可行, hi 可行
    while (hi - lo > 1) {
        int mid = lo + (hi - lo) / 2;
        if (feasible(mid)) hi = mid; else lo = mid;
    }
    feasible(hi);               // 重新求得 hi 处的具体放置

    // 独立复检求解器输出
    std::vector<std::array<long long,4>> core(n);
    for (int i = 0; i < n; ++i) core[i] = {px[i], py[i], pw[i], ph[i]};
    std::string err = independentCheck(hi, core);
    if (!err.empty()) { std::printf("CHECK FAIL: %s\n", err.c_str()); return 1; }

    std::printf("S %d\n", hi);
    for (int i = 0; i < n; ++i)
        std::printf("%d %d %d %d\n", i, px[i], py[i], prot[i]);
    std::printf("CHECK OK\n");
    return 0;
}
