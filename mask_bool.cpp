// mask_bool: Boolean difference (KEEP minus EXCLUDE) of rectangle sets on an
// integer grid (<= 12x12), decomposed into a MINIMUM number of disjoint
// rectangles, ties broken lexicographically.
//
// Optimality is certified by exhaustive branch-and-bound search with
// transposition tables and lower bounds -- never by a scanline heuristic.
//
// Build:  c++ -std=c++17 -O2 -o mask_bool mask_bool.cpp
// Input (stdin or file argument):
//   H W
//   K
//   t l b r        (K keep rectangles, 0-based, inclusive)
//   E
//   t l b r        (E exclude rectangles, 0-based, inclusive)
// Lines may contain '#' comments.

#include <array>
#include <cstdint>
#include <cstdio>
#include <fstream>
#include <iostream>
#include <sstream>
#include <stdexcept>
#include <string>
#include <unordered_map>
#include <vector>
#include <algorithm>

using namespace std;

// ---------------------------------------------------------------- 144-bit mask
struct Mask {
    array<uint64_t, 3> w{};
    bool test(int i) const { return (w[i >> 6] >> (i & 63)) & 1ull; }
    void set(int i) { w[i >> 6] |= 1ull << (i & 63); }
    void reset(int i) { w[i >> 6] &= ~(1ull << (i & 63)); }
    bool any() const { return (w[0] | w[1] | w[2]) != 0; }
    int count() const {
        return __builtin_popcountll(w[0]) + __builtin_popcountll(w[1]) +
               __builtin_popcountll(w[2]);
    }
    int first() const {  // lowest set index == topmost-leftmost cell
        for (int k = 0; k < 3; ++k)
            if (w[k]) return (k << 6) + __builtin_ctzll(w[k]);
        return -1;
    }
    bool contains(const Mask& o) const {  // this superset of o
        return ((o.w[0] & ~w[0]) | (o.w[1] & ~w[1]) | (o.w[2] & ~w[2])) == 0;
    }
    void subtract(const Mask& o) {
        w[0] &= ~o.w[0]; w[1] &= ~o.w[1]; w[2] &= ~o.w[2];
    }
    void unite(const Mask& o) { w[0] |= o.w[0]; w[1] |= o.w[1]; w[2] |= o.w[2]; }
    bool intersects(const Mask& o) const {
        return ((w[0] & o.w[0]) | (w[1] & o.w[1]) | (w[2] & o.w[2])) != 0;
    }
    bool operator==(const Mask& o) const { return w == o.w; }
};

struct MaskHash {
    size_t operator()(const Mask& m) const {
        uint64_t h = m.w[0] * 0x9E3779B97F4A7C15ull;
        h ^= m.w[1] + 0x9E3779B97F4A7C15ull + (h << 6) + (h >> 2);
        h ^= m.w[2] + 0x9E3779B97F4A7C15ull + (h << 6) + (h >> 2);
        return (size_t)h;
    }
};

// ------------------------------------------------------------------ globals
struct Rect { int t, l, b, r, area; };

int H, W, N;                          // rows, cols, cells
vector<Rect> rects;                   // all axis-aligned rectangles on grid
vector<Mask> rectMask;                // bitmask per rectangle
vector<int> idsByAreaDesc;            // rectangle ids, largest area first
vector<Mask> compatCell;        // cells whose bounding box with v fits target
vector<int> anchoredArea[144];  // rects with top-left at cell, area desc
vector<int> anchoredLex[144];   // same, ordered by (b, r) ascending

struct NodeLimitExceeded {};

struct Search {
    unordered_map<Mask, int, MaskHash> tt;  // mask -> max budget proven infeasible
    long long nodes = 0, ttHits = 0;
    long long maxNodes;
    vector<int> sol;  // rectangle ids of the found solution (reversed)

    // Lower bound on rectangles needed to partition mask m:
    //  * corner cells: a cell of m with no m-cell above and no m-cell left.
    //    Any rectangle inside m containing such a cell must have its top-left
    //    corner exactly there, so each needs its own rectangle.
    //  * ceil(cells / largest rectangle area that fits inside m).
    int lowerBound(const Mask& m) const {
        int pop = m.count();
        if (!pop) return 0;
        int corners = 0;
        Mask t = m;
        while (t.any()) {
            int v = t.first();
            t.reset(v);
            int r = v / W, c = v % W;
            bool up = r > 0 && m.test(v - W);
            bool lf = c > 0 && m.test(v - 1);
            if (!up && !lf) ++corners;
        }
        int maxArea = 1;
        for (int id : idsByAreaDesc)
            if (m.contains(rectMask[id])) { maxArea = rects[id].area; break; }
        int areaBound = (pop + maxArea - 1) / maxArea;
        // Incompatible-set bound: greedily collect cells of m that are
        // pairwise incompatible (no single rectangle inside the target can
        // cover both). Each needs its own rectangle. Compatibility is
        // precomputed against the full target, so it stays valid for any
        // remaining subset m.
        int clique = 0;
        Mask avail = m;
        while (avail.any()) {
            int v = avail.first();
            ++clique;
            avail.subtract(compatCell[v]);  // drops v and all compatible cells
        }
        return max(max(corners, clique), areaBound);
    }

    // Completeness: in any partition of m, the rectangle covering the
    // topmost-leftmost cell of m must be anchored exactly at that cell,
    // so branching over all anchored rectangles contained in m suffices.
    bool dfs(const Mask& m, int budget, const vector<int> anchored[144]) {
        if (!m.any()) return true;
        if (++nodes > maxNodes) throw NodeLimitExceeded{};
        int lb = lowerBound(m);
        if (lb > budget) {
            auto& e = tt[m];
            if (budget > e) e = budget;
            return false;
        }
        auto it = tt.find(m);
        if (it != tt.end() && it->second >= budget) { ++ttHits; return false; }
        int cell = m.first();
        for (int id : anchored[cell]) {
            if (!m.contains(rectMask[id])) continue;
            Mask nxt = m;
            nxt.subtract(rectMask[id]);
            if (dfs(nxt, budget - 1, anchored)) {
                sol.push_back(id);
                return true;
            }
        }
        auto& e = tt[m];
        if (budget > e) e = budget;
        return false;
    }
};

// ------------------------------------------------------------------ input
struct Tokenizer {
    vector<string> toks;
    size_t p = 0;
    explicit Tokenizer(istream& in) {
        string line;
        while (getline(in, line)) {
            auto h = line.find('#');
            if (h != string::npos) line = line.substr(0, h);
            istringstream ss(line);
            string t;
            while (ss >> t) toks.push_back(t);
        }
    }
    int nextInt() {
        if (p >= toks.size()) throw runtime_error("unexpected end of input");
        return stoi(toks[p++]);
    }
};

static Rect readRect(Tokenizer& tz, const char* kind, int i) {
    Rect rc{tz.nextInt(), tz.nextInt(), tz.nextInt(), tz.nextInt(), 0};
    if (rc.t < 0 || rc.l < 0 || rc.b >= H || rc.r >= W || rc.t > rc.b || rc.l > rc.r) {
        fprintf(stderr, "error: %s rectangle %d (%d %d %d %d) outside %dx%d grid\n",
                kind, i, rc.t, rc.l, rc.b, rc.r, H, W);
        exit(1);
    }
    return rc;
}

int main(int argc, char** argv) {
    const char* path = nullptr;
    long long maxNodes = 50000000;
    for (int i = 1; i < argc; ++i) {
        string a = argv[i];
        if (a == "--max-nodes" && i + 1 < argc) maxNodes = stoll(argv[++i]);
        else if (a == "--help") {
            puts("usage: mask_bool [input-file] [--max-nodes N]\n"
                 "input: H W / K / K keep rects / E / E exclude rects\n"
                 "rects: t l b r  (0-based, inclusive); '#' starts a comment");
            return 0;
        } else path = argv[i];
    }
    ifstream file;
    istream* in = &cin;
    if (path) { file.open(path); if (!file) { perror(path); return 1; } in = &file; }
    Tokenizer tz(*in);

    H = tz.nextInt(); W = tz.nextInt();
    if (H < 1 || W < 1 || H > 12 || W > 12) {
        fprintf(stderr, "error: grid must be within 1..12 x 1..12\n");
        return 1;
    }
    N = H * W;

    int K = tz.nextInt();
    vector<Rect> keep(K);
    for (auto& rc : keep) rc = readRect(tz, "keep", (int)(&rc - keep.data()));
    int E = tz.nextInt();
    vector<Rect> excl(E);
    for (auto& rc : excl) rc = readRect(tz, "exclude", (int)(&rc - excl.data()));

    // Target mask = union(keep) minus union(exclude).
    Mask keepM, exclM;
    auto paint = [](Mask& m, const Rect& rc) {
        for (int r = rc.t; r <= rc.b; ++r)
            for (int c = rc.l; c <= rc.r; ++c) m.set(r * W + c);
    };
    for (auto& rc : keep) paint(keepM, rc);
    for (auto& rc : excl) paint(exclM, rc);
    Mask target = keepM;
    target.subtract(exclM);

    // Enumerate every rectangle on the grid.
    for (int t = 0; t < H; ++t)
        for (int l = 0; l < W; ++l)
            for (int b = t; b < H; ++b)
                for (int r = l; r < W; ++r) {
                    Rect rc{t, l, b, r, (b - t + 1) * (r - l + 1)};
                    Mask m;
                    paint(m, rc);
                    int id = (int)rects.size();
                    rects.push_back(rc);
                    rectMask.push_back(m);
                    anchoredArea[t * W + l].push_back(id);
                    anchoredLex[t * W + l].push_back(id);
                }
    // Pairwise cell compatibility w.r.t. the target: u is compatible with v
    // iff the bounding box of {v, u} lies entirely inside the target.
    compatCell.assign(N, Mask{});
    for (int v = 0; v < N; ++v) {
        if (!target.test(v)) continue;
        int vr = v / W, vc = v % W;
        for (int u = 0; u < N; ++u) {
            if (!target.test(u)) continue;
            int ur = u / W, uc = u % W;
            bool ok = true;
            for (int r = min(vr, ur); r <= max(vr, ur) && ok; ++r)
                for (int c = min(vc, uc); c <= max(vc, uc); ++c)
                    if (!target.test(r * W + c)) { ok = false; break; }
            if (ok) compatCell[v].set(u);
        }
    }

    idsByAreaDesc.resize(rects.size());
    for (int i = 0; i < (int)rects.size(); ++i) idsByAreaDesc[i] = i;
    sort(idsByAreaDesc.begin(), idsByAreaDesc.end(),
         [](int a, int b) { return rects[a].area > rects[b].area; });
    for (int c = 0; c < N; ++c) {
        auto& va = anchoredArea[c];
        sort(va.begin(), va.end(), [](int a, int b) {
            if (rects[a].area != rects[b].area) return rects[a].area > rects[b].area;
            return tie(rects[a].b, rects[a].r) < tie(rects[b].b, rects[b].r);
        });
        auto& vl = anchoredLex[c];
        sort(vl.begin(), vl.end(), [](int a, int b) {
            return tie(rects[a].b, rects[a].r) < tie(rects[b].b, rects[b].r);
        });
    }

    printf("GRID %d %d\n", H, W);
    printf("KEEP %d\n", K);
    for (auto& rc : keep) printf("%d %d %d %d\n", rc.t, rc.l, rc.b, rc.r);
    printf("EXCLUDE %d\n", E);
    for (auto& rc : excl) printf("%d %d %d %d\n", rc.t, rc.l, rc.b, rc.r);
    printf("TARGET_CELLS %d\n", target.count());
    puts("MAP");
    for (int r = 0; r < H; ++r) {
        for (int c = 0; c < W; ++c) putchar(target.test(r * W + c) ? '#' : '.');
        putchar('\n');
    }

    // Scanline (row-run) decomposition: a valid upper bound, NOT a certificate.
    int scanline = 0;
    for (int r = 0; r < H; ++r)
        for (int c = 0; c < W; ++c)
            if (target.test(r * W + c) && (c == 0 || !target.test(r * W + c - 1)))
                ++scanline;
    printf("SCANLINE %d  # row-run heuristic, valid decomposition but NOT "
           "claimed minimal\n", scanline);

    if (!target.any()) {
        puts("OPTIMAL 0");
        puts("SOLUTION 0");
        puts("VERIFY disjoint=ok union=ok cells=0");
        puts("LEX trivial (empty solution)");
        puts("SEARCH_STATS nodes=0 tt_hits=0");
        return 0;
    }

    Search search;
    search.maxNodes = maxNodes;
    int lbRoot = search.lowerBound(target);
    printf("LOWER_BOUND %d  # max(corner cells, incompatible-cell set, ceil(cells/max rect area))\n",
           lbRoot);
    puts("SEARCH");

    // Phase 1: iterative deepening on the rectangle budget; the first feasible
    // budget K is the certified minimum (all smaller budgets exhausted).
    int Kopt = -1;
    try {
        for (int k = lbRoot;; ++k) {
            search.sol.clear();
            long long before = search.nodes;
            if (search.dfs(target, k, anchoredArea)) {
                Kopt = k;
                printf("K=%d nodes=%lld feasible\n", k, search.nodes - before);
                break;
            }
            printf("K=%d nodes=%lld infeasible (exhausted)\n", k,
                   search.nodes - before);
        }
        // Phase 2: re-run with budget Kopt trying candidates in lexicographic
        // (b, r) order; the first solution found is the lexicographically
        // smallest among all minimum-count solutions. Rectangles are generated
        // in increasing (t, l), which is exactly the sorted-solution order.
        search.sol.clear();
        long long before = search.nodes;
        bool ok = search.dfs(target, Kopt, anchoredLex);
        printf("LEX_PASS K=%d nodes=%lld %s\n", Kopt, search.nodes - before,
               ok ? "done" : "FAILED");
        if (!ok) { fprintf(stderr, "internal error: lex pass failed\n"); return 2; }
    } catch (const NodeLimitExceeded&) {
        printf("LIMIT max_nodes=%lld exceeded; optimality not certified\n",
               maxNodes);
        return 2;
    }

    reverse(search.sol.begin(), search.sol.end());
    printf("OPTIMAL %d  # certified: every partition with fewer rectangles "
           "was exhaustively ruled out\n", Kopt);

    // Verification: rectangles valid, inside target, pairwise disjoint, and
    // their union reconstructs the target mask exactly.
    Mask uni;
    bool disjoint = true, inside = true;
    for (int id : search.sol) {
        if (uni.intersects(rectMask[id])) disjoint = false;
        if (!target.contains(rectMask[id])) inside = false;
        uni.unite(rectMask[id]);
    }
    bool exact = (uni == target);
    printf("VERIFY disjoint=%s inside=%s union=%s cells=%d\n",
           disjoint ? "ok" : "FAIL", inside ? "ok" : "FAIL",
           exact ? "ok" : "FAIL", uni.count());
    if (!disjoint || !inside || !exact) {
        fprintf(stderr, "verification failed\n");
        return 2;
    }

    printf("SOLUTION %d\n", Kopt);
    for (int id : search.sol) {
        const Rect& rc = rects[id];
        printf("%d %d %d %d\n", rc.t, rc.l, rc.b, rc.r);
    }
    puts("LEX minimal  # rectangles (t l b r), solution sorted; lexicographically"
         " smallest among all minimum-count partitions");
    puts("LAYOUT");
    for (int r = 0; r < H; ++r) {
        for (int c = 0; c < W; ++c) {
            char ch = '.';
            for (size_t i = 0; i < search.sol.size(); ++i) {
                const Rect& rc = rects[search.sol[i]];
                if (r >= rc.t && r <= rc.b && c >= rc.l && c <= rc.r) {
                    ch = i < 26 ? char('a' + i) : '*';
                    break;
                }
            }
            putchar(ch);
        }
        putchar('\n');
    }
    printf("SEARCH_STATS nodes=%lld tt_hits=%lld tt_size=%zu\n", search.nodes,
           search.ttHits, search.tt.size());
    return 0;
}
