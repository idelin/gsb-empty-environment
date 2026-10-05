#pragma once

// Whole-image reference model: labels the full image in one pass and
// computes the same statistics as ChunkedLabeler. Used as ground truth
// to validate the chunked implementation.

#include <algorithm>
#include <numeric>
#include <set>
#include <utility>
#include <vector>

#include "chunked_labeler.hpp"

inline std::vector<ChunkedLabeler::ComponentStats> referenceModel(
    const std::vector<std::uint8_t>& img, int R, int C) {
    using Stats = ChunkedLabeler::ComponentStats;
    const int n = R * C;
    const int dr[4] = {-1, 1, 0, 0};
    const int dc[4] = {0, 0, -1, 1};

    std::vector<int> fl(n, -1), bl(n, -1);
    std::vector<int> stack;
    int nFg = 0, nBg = 0;
    for (int i = 0; i < n; ++i) {
        if (!img[i] || fl[i] >= 0) continue;
        fl[i] = nFg;
        stack.push_back(i);
        while (!stack.empty()) {
            int cur = stack.back();
            stack.pop_back();
            int r = cur / C, c = cur % C;
            for (int drr = -1; drr <= 1; ++drr)
                for (int dcc = -1; dcc <= 1; ++dcc) {
                    if (!drr && !dcc) continue;
                    int nr = r + drr, nc = c + dcc;
                    if (nr < 0 || nr >= R || nc < 0 || nc >= C) continue;
                    int ni = nr * C + nc;
                    if (img[ni] && fl[ni] < 0) {
                        fl[ni] = nFg;
                        stack.push_back(ni);
                    }
                }
        }
        ++nFg;
    }
    for (int i = 0; i < n; ++i) {
        if (img[i] || bl[i] >= 0) continue;
        bl[i] = nBg;
        stack.push_back(i);
        while (!stack.empty()) {
            int cur = stack.back();
            stack.pop_back();
            int r = cur / C, c = cur % C;
            for (int d = 0; d < 4; ++d) {
                int nr = r + dr[d], nc = c + dc[d];
                if (nr < 0 || nr >= R || nc < 0 || nc >= C) continue;
                int ni = nr * C + nc;
                if (!img[ni] && bl[ni] < 0) {
                    bl[ni] = nBg;
                    stack.push_back(ni);
                }
            }
        }
        ++nBg;
    }

    std::vector<long long> area(nFg, 0), perim(nFg, 0);
    std::vector<int> minR(nFg, R), minC(nFg, C);
    std::vector<char> ext(nBg, 0);
    std::set<std::pair<int, int>> adj;
    for (int r = 0; r < R; ++r)
        for (int c = 0; c < C; ++c) {
            int i = r * C + c;
            if (img[i]) {
                int k = fl[i];
                area[k]++;
                if (r < minR[k] || (r == minR[k] && c < minC[k])) {
                    minR[k] = r;
                    minC[k] = c;
                }
                for (int d = 0; d < 4; ++d) {
                    int nr = r + dr[d], nc = c + dc[d];
                    if (nr < 0 || nr >= R || nc < 0 || nc >= C || !img[nr * C + nc])
                        perim[k]++;
                }
                for (int d = 0; d < 4; ++d) {
                    int nr = r + dr[d], nc = c + dc[d];
                    if (nr < 0 || nr >= R || nc < 0 || nc >= C) continue;
                    int j = nr * C + nc;
                    if (!img[j]) adj.insert({bl[j], k});
                }
            } else {
                if (r == 0 || r == R - 1 || c == 0 || c == C - 1) ext[bl[i]] = 1;
            }
        }

    std::vector<int> ord(nFg);
    std::iota(ord.begin(), ord.end(), 0);
    std::sort(ord.begin(), ord.end(), [&](int x, int y) {
        return minR[x] != minR[y] ? minR[x] < minR[y] : minC[x] < minC[y];
    });
    std::vector<Stats> out(nFg);
    std::vector<int> stable(nFg);
    for (int i = 0; i < nFg; ++i) {
        int k = ord[i];
        out[i].label = i + 1;
        out[i].area = area[k];
        out[i].perimeter = perim[k];
        out[i].minRow = minR[k];
        out[i].minCol = minC[k];
        stable[k] = i + 1;
    }
    for (int b = 0; b < nBg; ++b) {
        if (ext[b]) continue;
        int best = nFg + 1;
        for (const auto& [bb, ff] : adj)
            if (bb == b) best = std::min(best, stable[ff]);
        if (best <= nFg) out[best - 1].holes++;
    }
    return out;
}
