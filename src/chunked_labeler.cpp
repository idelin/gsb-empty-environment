#include "chunked_labeler.hpp"

#include <algorithm>
#include <cassert>
#include <climits>
#include <numeric>
#include <unordered_map>

namespace {
constexpr int kDr4[4] = {-1, 1, 0, 0};
constexpr int kDc4[4] = {0, 0, -1, 1};
}  // namespace

ChunkedLabeler::ChunkedLabeler(int rows, int cols) : rows_(rows), cols_(cols) {
    assert(rows > 0 && cols > 0);
}

void ChunkedLabeler::addBlock(int row0, int col0, int h, int w,
                              const std::vector<std::uint8_t>& data) {
    assert(h > 0 && w > 0);
    assert(static_cast<long long>(h) * w == static_cast<long long>(data.size()));
    assert(row0 >= 0 && col0 >= 0 && row0 + h <= rows_ && col0 + w <= cols_);
    const int n = h * w;

    // Local labelling: foreground 8-connectivity, background 4-connectivity.
    std::vector<int> fl(n, -1), bl(n, -1);
    std::vector<int> stack;
    int nFg = 0;
    for (int i = 0; i < n; ++i) {
        if (!data[i] || fl[i] >= 0) continue;
        fl[i] = nFg;
        stack.push_back(i);
        while (!stack.empty()) {
            int cur = stack.back();
            stack.pop_back();
            int r = cur / w, c = cur % w;
            for (int dr = -1; dr <= 1; ++dr)
                for (int dc = -1; dc <= 1; ++dc) {
                    if (!dr && !dc) continue;
                    int nr = r + dr, nc = c + dc;
                    if (nr < 0 || nr >= h || nc < 0 || nc >= w) continue;
                    int ni = nr * w + nc;
                    if (data[ni] && fl[ni] < 0) {
                        fl[ni] = nFg;
                        stack.push_back(ni);
                    }
                }
        }
        ++nFg;
    }
    int nBg = 0;
    for (int i = 0; i < n; ++i) {
        if (data[i] || bl[i] >= 0) continue;
        bl[i] = nBg;
        stack.push_back(i);
        while (!stack.empty()) {
            int cur = stack.back();
            stack.pop_back();
            int r = cur / w, c = cur % w;
            for (int d = 0; d < 4; ++d) {
                int nr = r + kDr4[d], nc = c + kDc4[d];
                if (nr < 0 || nr >= h || nc < 0 || nc >= w) continue;
                int ni = nr * w + nc;
                if (!data[ni] && bl[ni] < 0) {
                    bl[ni] = nBg;
                    stack.push_back(ni);
                }
            }
        }
        ++nBg;
    }

    // Allocate global label ids for this block.
    const int fgBase = fgUf_.size();
    for (int k = 0; k < nFg; ++k) {
        fgUf_.add();
        fgArea_.push_back(0);
        fgPerim_.push_back(0);
        fgMinR_.push_back(rows_);
        fgMinC_.push_back(cols_);
    }
    const int bgBase = bgUf_.size();
    for (int k = 0; k < nBg; ++k) {
        bgUf_.add();
        bgExterior_.push_back(0);
    }

    // Per-label statistics. Pixels outside the block tentatively count as
    // background for the perimeter; cross-block foreground contacts are
    // corrected when the neighbour boundary is merged.
    for (int r = 0; r < h; ++r)
        for (int c = 0; c < w; ++c) {
            int i = r * w + c;
            int gr = row0 + r, gc = col0 + c;
            if (data[i]) {
                int g = fgBase + fl[i];
                fgArea_[g]++;
                if (gr < fgMinR_[g] || (gr == fgMinR_[g] && gc < fgMinC_[g])) {
                    fgMinR_[g] = gr;
                    fgMinC_[g] = gc;
                }
                for (int d = 0; d < 4; ++d) {
                    int nr = r + kDr4[d], nc = c + kDc4[d];
                    if (nr < 0 || nr >= h || nc < 0 || nc >= w || !data[nr * w + nc])
                        fgPerim_[g]++;
                }
            } else {
                int g = bgBase + bl[i];
                if (gr == 0 || gr == rows_ - 1 || gc == 0 || gc == cols_ - 1)
                    bgExterior_[g] = 1;
            }
        }

    // Local foreground/background 4-adjacency (for hole attribution).
    for (int r = 0; r < h; ++r)
        for (int c = 0; c < w; ++c) {
            int i = r * w + c;
            if (!data[i]) continue;
            for (int d = 0; d < 4; ++d) {
                int nr = r + kDr4[d], nc = c + kDc4[d];
                if (nr < 0 || nr >= h || nc < 0 || nc >= w) continue;
                int j = nr * w + nc;
                if (!data[j]) adj_.insert({bgBase + bl[j], fgBase + fl[i]});
            }
        }

    // Build the boundary summary, then drop the block pixel data.
    BlockSummary s;
    s.row0 = row0;
    s.col0 = col0;
    s.h = h;
    s.w = w;
    auto fillEdge = [&](Edge& e, int idx, int step, int len) {
        e.val.resize(len);
        e.fg.resize(len);
        e.bg.resize(len);
        for (int k = 0; k < len; ++k, idx += step) {
            e.val[k] = data[idx];
            e.fg[k] = data[idx] ? fgBase + fl[idx] : -1;
            e.bg[k] = data[idx] ? -1 : bgBase + bl[idx];
        }
    };
    fillEdge(s.top, 0, 1, w);
    fillEdge(s.bottom, (h - 1) * w, 1, w);
    fillEdge(s.left, 0, w, h);
    fillEdge(s.right, w - 1, w, h);

    // Merge against every previously consumed block.
    for (const BlockSummary& o : blocks_) {
        // Edge neighbours: shared horizontal/vertical edge segment.
        if (o.row0 + o.h == row0) mergeEdgePair(o.bottom, o.col0, s.top, col0);
        if (row0 + h == o.row0) mergeEdgePair(o.top, o.col0, s.bottom, col0);
        if (o.col0 + o.w == col0) mergeEdgePair(o.right, o.row0, s.left, row0);
        if (col0 + w == o.col0) mergeEdgePair(o.left, o.row0, s.right, row0);
        // Diagonal neighbours: single corner pixel contact (8-connectivity).
        if (o.row0 + o.h == row0 && o.col0 + o.w == col0)
            mergeCorner(o.bottom.fg.back(), s.top.fg.front());
        if (o.row0 + o.h == row0 && col0 + w == o.col0)
            mergeCorner(o.bottom.fg.front(), s.top.fg.back());
        if (row0 + h == o.row0 && o.col0 + o.w == col0)
            mergeCorner(o.top.fg.back(), s.bottom.fg.front());
        if (row0 + h == o.row0 && col0 + w == o.col0)
            mergeCorner(o.top.fg.front(), s.bottom.fg.back());
    }
    blocks_.push_back(std::move(s));
}

void ChunkedLabeler::mergeEdgePair(const Edge& a, int startA, const Edge& b,
                                   int startB) {
    const int lenA = static_cast<int>(a.val.size());
    const int lenB = static_cast<int>(b.val.size());
    const int lo = std::max(startA, startB);
    const int hi = std::min(startA + lenA, startB + lenB);
    for (int p = lo; p < hi; ++p) {
        int ia = p - startA, ib = p - startB;
        int fa = a.fg[ia], fb = b.fg[ib];
        int ba = a.bg[ia], bb = b.bg[ib];
        if (fa >= 0 && fb >= 0) {
            fgUf_.unite(fa, fb);
            // Both sides tentatively counted this shared edge as perimeter.
            fgPerim_[fa]--;
            fgPerim_[fb]--;
        } else if (ba >= 0 && bb >= 0) {
            bgUf_.unite(ba, bb);
        } else if (fa >= 0 && bb >= 0) {
            adj_.insert({bb, fa});
        } else if (ba >= 0 && fb >= 0) {
            adj_.insert({ba, fb});
        }
    }
    // Foreground 8-connectivity also links pixels diagonally across the
    // shared edge (offset by one along the edge). Background stays
    // 4-connected, so this applies to foreground only.
    for (int p = lo - 1; p <= hi; ++p) {
        int ia = p - startA, ib = (p + 1) - startB;
        if (ia >= 0 && ia < lenA && ib >= 0 && ib < lenB && a.fg[ia] >= 0 &&
            b.fg[ib] >= 0)
            fgUf_.unite(a.fg[ia], b.fg[ib]);
        int ib2 = (p - 1) - startB;
        if (ia >= 0 && ia < lenA && ib2 >= 0 && ib2 < lenB && a.fg[ia] >= 0 &&
            b.fg[ib2] >= 0)
            fgUf_.unite(a.fg[ia], b.fg[ib2]);
    }
}

void ChunkedLabeler::mergeCorner(int fgA, int fgB) {
    if (fgA >= 0 && fgB >= 0) fgUf_.unite(fgA, fgB);
}

std::vector<ChunkedLabeler::ComponentStats> ChunkedLabeler::finalize() {
    // Aggregate foreground statistics per union-find root.
    std::unordered_map<int, int> rootToIdx;
    std::vector<ComponentStats> comps;
    std::vector<int> roots;
    for (int g = 0; g < fgUf_.size(); ++g) {
        int r = fgUf_.find(g);
        auto [it, inserted] =
            rootToIdx.emplace(r, static_cast<int>(comps.size()));
        if (inserted) {
            comps.push_back(ComponentStats{});
            comps.back().minRow = rows_;
            comps.back().minCol = cols_;
            roots.push_back(r);
        }
        ComponentStats& cs = comps[it->second];
        cs.area += fgArea_[g];
        cs.perimeter += fgPerim_[g];
        if (fgMinR_[g] < cs.minRow ||
            (fgMinR_[g] == cs.minRow && fgMinC_[g] < cs.minCol)) {
            cs.minRow = fgMinR_[g];
            cs.minCol = fgMinC_[g];
        }
    }

    // Stable numbering: labels 1..N in (minRow, minCol) order.
    std::vector<int> order(comps.size());
    std::iota(order.begin(), order.end(), 0);
    std::sort(order.begin(), order.end(), [&](int x, int y) {
        return comps[x].minRow != comps[y].minRow
                   ? comps[x].minRow < comps[y].minRow
                   : comps[x].minCol < comps[y].minCol;
    });
    std::vector<ComponentStats> sorted(comps.size());
    std::unordered_map<int, int> rootLabel;
    for (int i = 0; i < static_cast<int>(order.size()); ++i) {
        sorted[i] = comps[order[i]];
        sorted[i].label = i + 1;
        rootLabel[roots[order[i]]] = i + 1;
    }

    // Holes: background roots that never touch the image border.
    std::unordered_map<int, bool> bgExt;
    for (int g = 0; g < bgUf_.size(); ++g) {
        int r = bgUf_.find(g);
        bgExt[r] = bgExt[r] || bgExterior_[g];
    }
    std::unordered_map<int, std::set<int>> bgToFg;
    for (const auto& [b, f] : adj_)
        bgToFg[bgUf_.find(b)].insert(fgUf_.find(f));
    for (const auto& [r, ext] : bgExt) {
        if (ext) continue;
        int best = INT_MAX;
        auto it = bgToFg.find(r);
        if (it == bgToFg.end()) continue;
        for (int fr : it->second) best = std::min(best, rootLabel[fr]);
        if (best != INT_MAX) sorted[best - 1].holes++;
    }
    return sorted;
}
