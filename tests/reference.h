// Independent whole-image reference model used to validate the chunked analyzer.
#pragma once

#include <algorithm>
#include <cstdint>
#include <queue>
#include <vector>

#include "chunked_analyzer.h"

namespace ref {

struct Model {
    std::vector<chunked::ComponentStats> comps;
    std::vector<int> labels; // W*H, 0 = background, else final component id
};

inline Model run(const std::vector<uint8_t>& img, int W, int H) {
    const int n = W * H;
    std::vector<int> flab(n, -1);
    int nf = 0;
    // Foreground 8-connectivity flood fill.
    for (int s = 0; s < n; ++s) {
        if (!img[s] || flab[s] >= 0) continue;
        std::queue<int> q;
        flab[s] = nf;
        q.push(s);
        while (!q.empty()) {
            const int cur = q.front();
            q.pop();
            const int x = cur % W, y = cur / W;
            for (int dy = -1; dy <= 1; ++dy)
                for (int dx = -1; dx <= 1; ++dx) {
                    if (!dx && !dy) continue;
                    const int px = x + dx, py = y + dy;
                    if (px < 0 || py < 0 || px >= W || py >= H) continue;
                    const int pi = py * W + px;
                    if (img[pi] && flab[pi] < 0) {
                        flab[pi] = nf;
                        q.push(pi);
                    }
                }
        }
        nf++;
    }

    std::vector<long> area(nf, 0), perim(nf, 0);
    std::vector<int> minx(nf, W), miny(nf, H);
    for (int y = 0; y < H; ++y)
        for (int x = 0; x < W; ++x) {
            const int lb = flab[y * W + x];
            if (lb < 0) continue;
            area[lb]++;
            if (y < miny[lb] || (y == miny[lb] && x < minx[lb])) { // row-major min pixel
                miny[lb] = y;
                minx[lb] = x;
            }
            static const int dx[4] = {1, -1, 0, 0};
            static const int dy[4] = {0, 0, 1, -1};
            for (int d = 0; d < 4; ++d) {
                const int px = x + dx[d], py = y + dy[d];
                if (px < 0 || py < 0 || px >= W || py >= H || !img[py * W + px]) perim[lb]++;
            }
        }

    // Background 4-connectivity flood fill.
    std::vector<int> blab(n, -1);
    int nb = 0;
    for (int s = 0; s < n; ++s) {
        if (img[s] || blab[s] >= 0) continue;
        std::queue<int> q;
        blab[s] = nb;
        q.push(s);
        while (!q.empty()) {
            const int cur = q.front();
            q.pop();
            const int x = cur % W, y = cur / W;
            static const int dx[4] = {1, -1, 0, 0};
            static const int dy[4] = {0, 0, 1, -1};
            for (int d = 0; d < 4; ++d) {
                const int px = x + dx[d], py = y + dy[d];
                if (px < 0 || py < 0 || px >= W || py >= H) continue;
                const int pi = py * W + px;
                if (!img[pi] && blab[pi] < 0) {
                    blab[pi] = nb;
                    q.push(pi);
                }
            }
        }
        nb++;
    }

    // Region adjacency (4-neighborhood) between fg and bg components, plus
    // border flags and row-major min pixels for canonical ordering.
    std::vector<char> bgBorder(nb, 0), fgBorder(nf, 0);
    std::vector<int> bminx(nb, W), bminy(nb, H);
    std::vector<std::pair<int, int>> adjPairs; // (bg comp, fg comp)
    for (int y = 0; y < H; ++y)
        for (int x = 0; x < W; ++x) {
            const bool onBorder = (x == 0 || y == 0 || x == W - 1 || y == H - 1);
            const int fb0 = flab[y * W + x];
            if (fb0 >= 0 && onBorder) fgBorder[fb0] = 1;
            const int bb = blab[y * W + x];
            if (bb < 0) continue;
            if (onBorder) bgBorder[bb] = 1;
            if (y < bminy[bb] || (y == bminy[bb] && x < bminx[bb])) {
                bminy[bb] = y;
                bminx[bb] = x;
            }
            static const int dx[4] = {1, -1, 0, 0};
            static const int dy[4] = {0, 0, 1, -1};
            for (int d = 0; d < 4; ++d) {
                const int px = x + dx[d], py = y + dy[d];
                if (px < 0 || py < 0 || px >= W || py >= H) continue;
                const int fb = flab[py * W + px];
                if (fb >= 0) adjPairs.emplace_back(bb, fb);
            }
        }

    std::vector<int> order(nf);
    for (int i = 0; i < nf; ++i) order[i] = i;
    std::sort(order.begin(), order.end(), [&](int a, int b) {
        if (miny[a] != miny[b]) return miny[a] < miny[b];
        if (minx[a] != minx[b]) return minx[a] < minx[b];
        return a < b;
    });
    std::vector<int> idOf(nf, 0);
    for (int i = 0; i < nf; ++i) idOf[order[i]] = i + 1;

    // Hole ownership via nesting-tree BFS from the virtual exterior, identical
    // semantics to the chunked analyzer: canonical order by component min pixel.
    std::vector<int> bgOrder(nb);
    for (int i = 0; i < nb; ++i) bgOrder[i] = i;
    std::sort(bgOrder.begin(), bgOrder.end(), [&](int a, int b) {
        if (bminy[a] != bminy[b]) return bminy[a] < bminy[b];
        if (bminx[a] != bminx[b]) return bminx[a] < bminx[b];
        return a < b;
    });
    std::vector<int> bgNode(nb);
    for (int i = 0; i < nb; ++i) bgNode[bgOrder[i]] = nf + i;
    std::vector<std::vector<int>> tree(nf + nb);
    for (const auto& pr : adjPairs) {
        const int u = idOf[pr.second] - 1;
        const int v = bgNode[pr.first];
        tree[u].push_back(v);
        tree[v].push_back(u);
    }
    auto keyOf = [&](int node) {
        if (node < nf) {
            const int c = order[node];
            return std::make_pair(miny[c], minx[c]);
        }
        const int b = bgOrder[node - nf];
        return std::make_pair(bminy[b], bminx[b]);
    };
    for (auto& lst : tree) {
        std::sort(lst.begin(), lst.end(), [&](int a, int b) { return keyOf(a) < keyOf(b); });
        lst.erase(std::unique(lst.begin(), lst.end()), lst.end());
    }
    std::vector<int> parent(nf + nb, -2);
    std::vector<int> queue;
    for (int i = 0; i < nf; ++i)
        if (fgBorder[order[i]]) {
            parent[i] = -1;
            queue.push_back(i);
        }
    for (int i = 0; i < nb; ++i)
        if (bgBorder[bgOrder[i]]) {
            parent[nf + i] = -1;
            queue.push_back(nf + i);
        }
    std::sort(queue.begin(), queue.end(), [&](int a, int b) { return keyOf(a) < keyOf(b); });
    for (size_t head = 0; head < queue.size(); ++head) {
        const int u = queue[head];
        for (const int v : tree[u])
            if (parent[v] == -2) {
                parent[v] = u;
                queue.push_back(v);
            }
        }
    std::vector<long> holes(nf, 0);
    for (int i = 0; i < nb; ++i) {
        if (bgBorder[bgOrder[i]]) continue;
        const int par = parent[nf + i];
        if (par >= 0) holes[par]++;
    }

    Model m;
    m.labels.assign(n, 0);
    for (int i = 0; i < nf; ++i) {
        const int c = order[i];
        chunked::ComponentStats s;
        s.id = i + 1;
        s.minX = minx[c];
        s.minY = miny[c];
        s.area = area[c];
        s.perimeter = perim[c];
        s.holes = holes[i];
        m.comps.push_back(s);
    }
    for (int i = 0; i < n; ++i)
        if (flab[i] >= 0) m.labels[i] = idOf[flab[i]];
    return m;
}

} // namespace ref
