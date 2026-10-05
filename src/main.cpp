// Texture atlas solver (exact, C++17).
//
// Packs up to 12 axis-aligned rectangles (rotation by 90 degrees allowed)
// into a square of minimum integer side length. Every texture must be
// surrounded by `pad` isolation pixels, including along the atlas border.
// Two isolation modes are supported:
//   shared    - the isolation strip between two neighbouring textures may
//               be reused by both, so any pair must be separated by >= pad;
//   exclusive - each texture owns its isolation zone, so any pair must be
//               separated by >= 2*pad.
//
// Method (exact, not a heuristic):
//   Requiring a pairwise separation of `gap` is equivalent to packing the
//   boxes (w_i+gap) x (h_i+gap) without overlap. A packing of those boxes
//   in a square of side L corresponds to an atlas of side
//   S = L + 2*pad - gap. Hence minimising S is minimising L.
//   For each candidate L (ascending from a lower bound) feasibility is
//   decided by a complete backtracking search. Any packing of
//   integer-sided boxes can be compacted (push every box left/down until
//   it rests against the border or another box) so that every coordinate
//   is a subset sum of box sides; the search enumerates exactly those
//   candidate coordinates, which makes the decision procedure complete.
//   The first feasible L is therefore provably minimal.
//   The resulting placement is validated by an independent checker before
//   being printed.
//
// Usage:
//   atlas_solver            solve; stdin:  n pad mode / w h (n lines)
//   atlas_solver --check    verify; stdin: S / n pad mode / w h (n) / x y rot (n)

#include <algorithm>
#include <cmath>
#include <iostream>
#include <iterator>
#include <set>
#include <sstream>
#include <string>
#include <vector>

#include "checker.h"

namespace {

struct BoxOpt { int w, h; bool rot; };

int g_n;
std::vector<std::vector<BoxOpt>> g_opts; // per texture, 1-2 orientations
std::vector<int> g_order;                // search order (largest first)
std::vector<int> g_candX, g_candY;       // subset-sum candidate coordinates
std::vector<BoxOpt> g_placed;            // box chosen for g_order[k]
std::vector<int> g_px, g_py;             // its position
int g_L;

bool dfs(int k) {
    if (k == g_n) return true;
    const int r = g_order[k];
    for (const BoxOpt& o : g_opts[r]) {
        const int maxX = g_L - o.w, maxY = g_L - o.h;
        if (maxX < 0 || maxY < 0) continue;
        for (int x : g_candX) {
            if (x > maxX) break;
            if (k == 0 && 2 * x > maxX) break; // square symmetry: lower-left half
            for (int y : g_candY) {
                if (y > maxY) break;
                if (k == 0 && 2 * y > maxY) break;
                bool free_ = true;
                for (int j = 0; j < k; ++j) {
                    const BoxOpt& b = g_placed[j];
                    if (x < g_px[j] + b.w && g_px[j] < x + o.w &&
                        y < g_py[j] + b.h && g_py[j] < y + o.h) {
                        free_ = false;
                        break;
                    }
                }
                if (!free_) continue;
                g_placed[k] = o;
                g_px[k] = x;
                g_py[k] = y;
                if (dfs(k + 1)) return true;
            }
        }
    }
    return false;
}

// All values in [0, L] expressible as a sum picking one side-option per box.
std::vector<int> subsetSums(const std::vector<std::vector<int>>& sides, int L) {
    std::set<int> cur{0};
    for (const auto& opts : sides) {
        std::set<int> nxt = cur; // a box may also contribute nothing
        for (int base : cur)
            for (int s : opts)
                if (base + s <= L) nxt.insert(base + s);
        cur.swap(nxt);
    }
    return std::vector<int>(cur.begin(), cur.end());
}

int ceilSqrt(long long v) {
    long long s = static_cast<long long>(std::sqrt(static_cast<long double>(v)));
    while (s * s < v) ++s;
    while (s > 0 && (s - 1) * (s - 1) >= v) --s;
    return static_cast<int>(s);
}

} // namespace

int main(int argc, char** argv) {
    if (argc > 1 && std::string(argv[1]) == "--check") {
        std::string spec((std::istreambuf_iterator<char>(std::cin)),
                         std::istreambuf_iterator<char>());
        std::string err = atlascheck::verify(spec);
        if (err.empty()) {
            std::cout << "OK\n";
            return 0;
        }
        std::cout << "FAIL: " << err;
        return 1;
    }

    int n;
    long long pad;
    std::string modeTok;
    if (!(std::cin >> n >> pad >> modeTok)) {
        std::cerr << "usage: n pad mode on first line, then n lines of w h\n";
        return 2;
    }
    bool shared;
    if (modeTok == "shared" || modeTok == "0") shared = true;
    else if (modeTok == "exclusive" || modeTok == "1") shared = false;
    else {
        std::cerr << "mode must be shared/0 or exclusive/1\n";
        return 2;
    }
    if (n < 1 || n > 12 || pad < 0) {
        std::cerr << "require 1 <= n <= 12 and pad >= 0\n";
        return 2;
    }

    std::vector<int> w(n), h(n);
    for (int i = 0; i < n; ++i) {
        if (!(std::cin >> w[i] >> h[i]) || w[i] < 1 || h[i] < 1) {
            std::cerr << "bad texture size on line " << i + 2 << "\n";
            return 2;
        }
    }

    const long long gap = shared ? pad : 2 * pad;
    g_n = n;
    g_opts.assign(n, {});
    long long areaSum = 0;
    int dimLB = 0;
    for (int i = 0; i < n; ++i) {
        const int bw = w[i] + static_cast<int>(gap);
        const int bh = h[i] + static_cast<int>(gap);
        g_opts[i].push_back({bw, bh, false});
        if (bw != bh) g_opts[i].push_back({bh, bw, true});
        areaSum += 1LL * bw * bh;
        dimLB = std::max(dimLB, std::max(bw, bh));
    }

    g_order.resize(n);
    for (int i = 0; i < n; ++i) g_order[i] = i;
    std::sort(g_order.begin(), g_order.end(), [&](int a, int b) {
        int ma = std::max(g_opts[a][0].w, g_opts[a][0].h);
        int mb = std::max(g_opts[b][0].w, g_opts[b][0].h);
        if (ma != mb) return ma > mb;
        return g_opts[a][0].w * g_opts[a][0].h > g_opts[b][0].w * g_opts[b][0].h;
    });

    g_placed.resize(n);
    g_px.resize(n);
    g_py.resize(n);

    const int lb = std::max(ceilSqrt(areaSum), dimLB);
    int L = lb;
    for (;; ++L) {
        std::vector<std::vector<int>> xs(n), ys(n);
        for (int i = 0; i < n; ++i) {
            for (const auto& o : g_opts[i]) {
                xs[i].push_back(o.w);
                ys[i].push_back(o.h);
            }
        }
        g_L = L;
        g_candX = subsetSums(xs, L);
        g_candY = subsetSums(ys, L);
        if (dfs(0)) break;
    }

    const long long S = static_cast<long long>(L) + 2 * pad - gap;

    // Emit in original input order; shift boxes back by pad.
    std::vector<BoxOpt> boxOf(n);
    std::vector<int> bx(n), by(n);
    for (int k = 0; k < n; ++k) {
        const int r = g_order[k];
        boxOf[r] = g_placed[k];
        bx[r] = g_px[k];
        by[r] = g_py[k];
    }

    std::ostringstream out;
    out << S << "\n";
    for (int i = 0; i < n; ++i)
        out << (bx[i] + pad) << " " << (by[i] + pad) << " "
            << (boxOf[i].rot ? 1 : 0) << "\n";

    // Independent verification before reporting anything.
    std::ostringstream spec;
    spec << S << "\n" << n << " " << pad << " " << (shared ? "shared" : "exclusive") << "\n";
    for (int i = 0; i < n; ++i) spec << w[i] << " " << h[i] << "\n";
    for (int i = 0; i < n; ++i)
        spec << (bx[i] + pad) << " " << (by[i] + pad) << " "
             << (boxOf[i].rot ? 1 : 0) << "\n";
    const std::string err = atlascheck::verify(spec.str());
    if (!err.empty()) {
        std::cerr << "internal error: solver produced invalid placement: " << err;
        return 1;
    }

    std::cout << out.str();
    return 0;
}
