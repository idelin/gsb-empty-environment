// rect_diff: boolean difference of rectangle sets on an integer grid (<= 12x12),
// producing a provably minimum non-overlapping rectangle decomposition
// (ties broken by lexicographic order of the sorted rectangle list).
//
// Region = union(keep rects) \ union(exclude rects), in cell coordinates:
// rect (x1 y1 x2 y2) covers cells [x1,x2) x [y1,y2).
//
// Minimality is established by exhaustive exact-partition search with
// lower-bound pruning (NOT by any scanline/greedy heuristic), and the
// search record is printed together with a reconstruction check.
//
// Input (stdin):
//   W H
//   K            then K lines: x1 y1 x2 y2   (keep rectangles)
//   E            then E lines: x1 y1 x2 y2   (exclude rectangles)
//
// g++ -std=c++17 -O2 rect_diff.cpp -o rect_diff

#include <algorithm>
#include <bitset>
#include <climits>
#include <cstdio>
#include <iostream>
#include <tuple>
#include <unordered_map>
#include <utility>
#include <vector>
using namespace std;

static const int MAXC = 144; // 12*12 cells
using Bits = bitset<MAXC>;

struct Rect {
    int x1, y1, x2, y2; // cell coords, half-open [x1,x2) x [y1,y2)
};
static bool operator<(const Rect& a, const Rect& b) {
    return tie(a.x1, a.y1, a.x2, a.y2) < tie(b.x1, b.y1, b.x2, b.y2);
}
static bool containsCell(const Rect& r, int x, int y) {
    return r.x1 <= x && x < r.x2 && r.y1 <= y && y < r.y2;
}

int W, H;
inline int cid(int x, int y) { return y * W + x; }

Bits regionBits;                 // target region
vector<Rect> rects;              // all cell-rectangles fully inside region, sorted lex
vector<Bits> rbits;              // cell bitmask of each rectangle
vector<vector<int>> cellRects;   // per cell: indices of rectangles containing it
Bits compat[MAXC];               // compat[c]: cells that can share one all-region rectangle with c
int maxRectArea = 1;
vector<int> cellOrder;           // cells ordered by ascending total rectangle count
double cellW[MAXC]; // fractional packing weights: every region rectangle has weight <= 1

long long nodesMin = 0;   // nodes spent proving minimality (k search)
long long nodesLex = 0;   // nodes spent in lex-minimization feasibility checks
long long* curNodes = &nodesMin;

// Max-weight all-region rectangle under current weights (oracle for the packing LP).
static double maxRectWeight(const vector<double>& w) {
    double best = 0;
    for (int y1 = 0; y1 < H; ++y1) {
        vector<double> col(W, 0.0);
        vector<char> ok(W, 1);
        for (int y2 = y1; y2 < H; ++y2) {
            for (int x = 0; x < W; ++x) {
                if (regionBits[cid(x, y2)]) col[x] += w[cid(x, y2)];
                else ok[x] = 0;
            }
            double run = 0;
            for (int x = 0; x < W; ++x) {
                if (!ok[x]) { run = 0; continue; }
                run += col[x];
                if (run < 0) run = 0;
                best = max(best, run);
            }
        }
    }
    return best;
}

// Memoization across the whole run (region and rectangle set are fixed):
//   memoInfeas[s] = highest budget for which s was proven NOT partitionable
//   memoFeas[s]   = smallest budget for which s was proven partitionable
unordered_map<Bits, int> memoInfeas, memoFeas;


// Exact decision: can `uncovered` be partitioned into <= budget region-rectangles?
bool canPartition(const Bits& uncovered, int budget) {
    ++(*curNodes);
    if (uncovered.none()) return true;
    if (budget <= 0) return false;
    if ((int)uncovered.count() > budget * maxRectArea) return false;
    auto fi = memoFeas.find(uncovered);
    if (fi != memoFeas.end() && fi->second <= budget) return true;
    auto ii = memoInfeas.find(uncovered);
    if (ii != memoInfeas.end() && ii->second >= budget) return false;

    // Lower bound A: fractional packing (valid for any sub-region).
    {
        double s = 0;
        for (int v = 0; v < MAXC; ++v) if (uncovered[v]) s += cellW[v];
        if ((int)ceil(s - 1e-9) > budget) return false;
    }
    // Lower bound B: greedy maximal set of pairwise-incompatible cells.
    // Two cells are incompatible iff no all-region rectangle contains both,
    // so each needs its own rectangle in any partition.
    {
        Bits avail = uncovered;
        int indep = 0;
        while (avail.any()) {
            int best = -1, bestDeg = INT_MAX;
            for (int v = 0; v < MAXC; ++v) if (avail[v]) {
                int d = (int)(compat[v] & avail).count();
                if (d < bestDeg) { bestDeg = d; best = v; }
            }
            ++indep;
            avail &= ~compat[best];
        }
        if (indep > budget) return false;
    }

    if (budget == 1) {
        // a single rectangle works iff `uncovered` itself is one of the rectangles
        static const unordered_map<Bits, char>* table = nullptr;
        if (!table) {
            auto* t = new unordered_map<Bits, char>();
            for (size_t i = 0; i < rbits.size(); ++i) (*t)[rbits[i]] = 1;
            table = t;
        }
        bool ok = table->count(uncovered) > 0;
        if (ok) { memoFeas[uncovered] = 1; return true; }
        memoInfeas[uncovered] = 1;
        return false;
    }
    // Branch on an uncovered cell with fewest feasible rectangles.
    // Cells are scanned in ascending static rectangle-count order, so a
    // highly constrained cell is found almost immediately; any choice is
    // correct, this is purely a search-efficiency heuristic.
    // Pass 1: find a minimally constrained cell (count only, no allocation).
    int bc = -1;
    size_t bcnt = SIZE_MAX;
    for (int v : cellOrder) {
        if (!uncovered[v]) continue;
        size_t cnt = 0;
        for (int r : cellRects[v])
            if ((rbits[r] & ~uncovered).none()) ++cnt;
        // cnt is never 0: the unit rectangle of a region cell always qualifies.
        if (cnt < bcnt) { bcnt = cnt; bc = v; if (cnt == 1) break; }
    }
    // Pass 2: materialize the chosen cell's rectangle list.
    vector<int> bl;
    bl.reserve(bcnt);
    for (int r : cellRects[bc])
        if ((rbits[r] & ~uncovered).none()) bl.push_back(r);
    // Larger rectangles first: finds feasible solutions / exhausts budgets faster.
    sort(bl.begin(), bl.end(), [&](int a, int b) {
        return rbits[a].count() > rbits[b].count();
    });
    for (int r : bl)
        if (canPartition(uncovered & ~rbits[r], budget - 1)) {
            auto& s = memoFeas[uncovered];
            if (s == 0 || s > budget) s = budget;
            return true;
        }
    auto& s = memoInfeas[uncovered];
    if (s < budget) s = budget;
    return false;
}

int main() {
    if (!(cin >> W >> H)) { fprintf(stderr, "bad input\n"); return 1; }
    if (W < 1 || H < 1 || W > 12 || H > 12) {
        fprintf(stderr, "grid must be within 1..12 in each dimension\n");
        return 1;
    }
    auto readRects = [&](vector<Rect>& out) {
        int n; cin >> n;
        for (int i = 0; i < n; ++i) {
            Rect r; cin >> r.x1 >> r.y1 >> r.x2 >> r.y2;
            if (r.x1 > r.x2) swap(r.x1, r.x2);
            if (r.y1 > r.y2) swap(r.y1, r.y2);
            r.x1 = max(r.x1, 0); r.y1 = max(r.y1, 0);
            r.x2 = min(r.x2, W); r.y2 = min(r.y2, H);
            if (r.x1 < r.x2 && r.y1 < r.y2) out.push_back(r);
        }
    };
    vector<Rect> keeps, excludes;
    readRects(keeps);
    readRects(excludes);

    Bits keepBits, exclBits;
    auto paint = [](Bits& b, const Rect& r) {
        for (int y = r.y1; y < r.y2; ++y)
            for (int x = r.x1; x < r.x2; ++x) b.set(cid(x, y));
    };
    for (auto& r : keeps) paint(keepBits, r);
    for (auto& r : excludes) paint(exclBits, r);
    regionBits = keepBits & ~exclBits;

    printf("Grid %dx%d | keep rects %zu | exclude rects %zu | region cells %zu\n\n",
           W, H, keeps.size(), excludes.size(), regionBits.count());
    printf("Region (# = kept):\n");
    for (int y = 0; y < H; ++y) {
        for (int x = 0; x < W; ++x) putchar(regionBits[cid(x, y)] ? '#' : '.');
        putchar('\n');
    }
    putchar('\n');

    // Enumerate every cell-rectangle fully contained in the region.
    cellRects.assign(W * H, {});
    for (int x1 = 0; x1 < W; ++x1)
        for (int y1 = 0; y1 < H; ++y1)
            for (int x2 = x1 + 1; x2 <= W; ++x2)
                for (int y2 = y1 + 1; y2 <= H; ++y2) {
                    Bits b;
                    bool ok = true;
                    for (int y = y1; y < y2 && ok; ++y)
                        for (int x = x1; x < x2 && ok; ++x)
                            if (regionBits[cid(x, y)]) b.set(cid(x, y));
                            else ok = false;
                    if (!ok) continue;
                    rects.push_back({x1, y1, x2, y2});
                    rbits.push_back(b);
                }
    sort(rects.begin(), rects.end());
    // rebuild rbits in sorted order
    {
        vector<Bits> nb(rects.size());
        for (size_t i = 0; i < rects.size(); ++i) {
            Bits b;
            for (int y = rects[i].y1; y < rects[i].y2; ++y)
                for (int x = rects[i].x1; x < rects[i].x2; ++x) b.set(cid(x, y));
            nb[i] = b;
            maxRectArea = max(maxRectArea, (int)b.count());
        }
        rbits = std::move(nb);
    }
    for (size_t i = 0; i < rects.size(); ++i)
        for (int c = 0; c < MAXC; ++c)
            if (rbits[i][c]) cellRects[c].push_back((int)i);

    // Compatibility: cells a,b compatible iff some region rectangle covers both.
    for (size_t i = 0; i < rects.size(); ++i)
        for (int c = 0; c < MAXC; ++c)
            if (rbits[i][c]) compat[c] |= rbits[i];

    cellOrder.resize(MAXC);
    for (int c = 0; c < MAXC; ++c) cellOrder[c] = c;
    sort(cellOrder.begin(), cellOrder.end(), [&](int a, int b) {
        return cellRects[a].size() < cellRects[b].size();
    });

    // Fractional packing weights via multiplicative weights on the dual of the
    // set-cover LP: maximize sum(w) subject to every rectangle weighing <= 1.
    // Any partition needs >= sum(w over remaining cells) rectangles, so this is
    // a valid (and typically much stronger) lower bound at every search node.
    {
        vector<double> w(MAXC, 0.0);
        for (int c = 0; c < MAXC; ++c) if (regionBits[c]) w[c] = 1.0;
        double bestObj = 0;
        vector<double> bestW = w;
        const double eps = 0.02;
        for (int t = 0; t < 30000; ++t) {
            double m = maxRectWeight(w);
            if (m <= 1e-12) break;
            double obj = 0;
            for (int c = 0; c < MAXC; ++c) obj += w[c];
            obj /= m;
            if (obj > bestObj) {
                bestObj = obj;
                for (int c = 0; c < MAXC; ++c) bestW[c] = w[c] / m;
            }
            // penalize cells of a max-weight rectangle
            // (recompute to find which rect attains m)
            double best = 0; Rect br{0,0,0,0};
            for (int y1 = 0; y1 < H; ++y1) {
                vector<double> col(W, 0.0);
                vector<char> ok(W, 1);
                for (int y2 = y1; y2 < H; ++y2) {
                    for (int x = 0; x < W; ++x) {
                        if (regionBits[cid(x, y2)]) col[x] += w[cid(x, y2)];
                        else ok[x] = 0;
                    }
                    double run = 0; int rx1 = 0;
                    for (int x = 0; x < W; ++x) {
                        if (!ok[x]) { run = 0; rx1 = x + 1; continue; }
                        run += col[x];
                        if (run <= 0) { run = 0; rx1 = x + 1; continue; }
                        if (run > best) { best = run; br = {rx1, y1, x + 1, y2 + 1}; }
                    }
                }
            }
            for (int y = br.y1; y < br.y2; ++y)
                for (int x = br.x1; x < br.x2; ++x)
                    w[cid(x, y)] *= (1.0 - eps);
        }
        for (int c = 0; c < MAXC; ++c) cellW[c] = bestW[c];
        printf("  fractional packing bound (root) = %.3f\n", bestObj);
    }

    // --- Phase 1: minimum rectangle count via iterative deepening on k ---
    printf("Search record (exact partition search, lower-bound pruned):\n");
    int lb = 0;
    {
        Bits avail = regionBits;
        while (avail.any()) {
            int best = -1, bestDeg = INT_MAX;
            for (int v = 0; v < MAXC; ++v) if (avail[v]) {
                int d = (int)(compat[v] & avail).count();
                if (d < bestDeg) { bestDeg = d; best = v; }
            }
            ++lb;
            avail &= ~compat[best];
        }
    }
    printf("  lower bound (greedy pairwise-incompatible cell set) = %d\n", lb);

    int K = -1;
    for (int k = lb; ; ++k) {
        long long before = nodesMin;
        bool ok = canPartition(regionBits, k);
        printf("  k = %d : %s (search nodes %lld)\n",
               k, ok ? "FEASIBLE" : "INFEASIBLE (exhaustive)", nodesMin - before);
        if (ok) { K = k; break; }
    }
    printf("  => minimum rectangle count = %d (all counts below exhaustively refuted)\n\n", K);

    // --- Phase 2: lexicographically smallest sorted list among minimum partitions ---
    // Key fact: in any partition of the remaining region, the lexicographically
    // smallest rectangle must cover the (x,y)-lexicographically smallest remaining
    // cell. So at each position we only try rectangles covering that cell, in
    // increasing lex order, keeping the first one whose remainder is still
    // partitionable with the remaining budget.
    curNodes = &nodesLex;
    vector<Rect> answer;
    Bits remaining = regionBits;
    Rect prev{-1, -1, -1, -1};
    bool hasPrev = false;
    for (int i = 0; i < K; ++i) {
        int mx = -1, my = -1;
        for (int x = 0; x < W && mx < 0; ++x)
            for (int y = 0; y < H; ++y)
                if (remaining[cid(x, y)]) { mx = x; my = y; break; }
        int chosen = -1;
        for (size_t r = 0; r < rects.size(); ++r) {
            if (hasPrev && !(prev < rects[r])) continue;
            if (!containsCell(rects[r], mx, my)) continue;
            if ((rbits[r] & ~remaining).any()) continue;
            if (canPartition(remaining & ~rbits[r], K - i - 1)) { chosen = (int)r; break; }
        }
        if (chosen < 0) { fprintf(stderr, "internal error: no lex candidate\n"); return 1; }
        answer.push_back(rects[chosen]);
        remaining &= ~rbits[chosen];
        prev = rects[chosen];
        hasPrev = true;
    }

    printf("Lex-minimization feasibility-check nodes: %lld\n\n", nodesLex);

    printf("Minimum decomposition (%d rectangles, lexicographically smallest):\n", K);
    for (int i = 0; i < K; ++i)
        printf("  R%-2d (%2d,%2d) - (%2d,%2d)   area %d\n", i + 1,
               answer[i].x1, answer[i].y1, answer[i].x2, answer[i].y2,
               (answer[i].x2 - answer[i].x1) * (answer[i].y2 - answer[i].y1));

    // --- Phase 3: reconstruction verification ---
    bool disjoint = true;
    Bits acc;
    for (int i = 0; i < K; ++i) {
        Bits b;
        paint(b, answer[i]);
        if ((acc & b).any()) disjoint = false;
        acc |= b;
    }
    bool unionOk = (acc == regionBits);
    printf("\nVerification:\n");
    printf("  pairwise disjoint            : %s\n", disjoint ? "OK" : "FAIL");
    printf("  union == keep \\ exclude      : %s (%zu/%zu cells)\n",
           unionOk ? "OK" : "FAIL", acc.count(), regionBits.count());
    printf("  optimality                   : exact exhaustive search, not scanline heuristic\n");
    return (disjoint && unionOk) ? 0 : 2;
}
