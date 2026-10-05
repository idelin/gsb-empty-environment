#include "chunked_analyzer.h"

#include <algorithm>
#include <climits>
#include <numeric>
#include <stdexcept>
#include <unordered_map>

namespace chunked {

namespace {

// Labels pixels where m[i] != 0 with the given connectivity.
// Returns the component count; labels receive dense ids 0..n-1, or -1 for other pixels.
int labelLocal(const std::vector<uint8_t>& m, int w, int h, bool eightConn, std::vector<int>& labels) {
    const int n = w * h;
    labels.assign(n, -1);
    std::vector<int> parent(n);
    std::iota(parent.begin(), parent.end(), 0);
    auto find = [&](int x) {
        int r = x;
        while (parent[r] != r) r = parent[r];
        while (parent[x] != r) {
            int p = parent[x];
            parent[x] = r;
            x = p;
        }
        return r;
    };
    auto uni = [&](int a, int b) {
        a = find(a);
        b = find(b);
        if (a != b) parent[b] = a;
    };
    for (int y = 0; y < h; ++y) {
        for (int x = 0; x < w; ++x) {
            if (!m[y * w + x]) continue;
            if (x > 0 && m[y * w + x - 1]) uni(y * w + x, y * w + x - 1);
            if (y > 0 && m[(y - 1) * w + x]) uni(y * w + x, (y - 1) * w + x);
            if (eightConn) {
                if (x > 0 && y > 0 && m[(y - 1) * w + x - 1]) uni(y * w + x, (y - 1) * w + x - 1);
                if (x + 1 < w && y > 0 && m[(y - 1) * w + x + 1]) uni(y * w + x, (y - 1) * w + x + 1);
            }
        }
    }
    std::vector<int> remap(n, -1);
    int count = 0;
    for (int i = 0; i < n; ++i) {
        if (!m[i]) continue;
        int r = find(i);
        if (remap[r] < 0) remap[r] = count++;
        labels[i] = remap[r];
    }
    return count;
}

} // namespace

ChunkedAnalyzer::ChunkedAnalyzer(int width, int height, int chunkWidth, int chunkHeight)
    : w_(width), h_(height), cw_(chunkWidth), ch_(chunkHeight) {
    if (w_ <= 0 || h_ <= 0 || cw_ <= 0 || ch_ <= 0) throw std::invalid_argument("bad dimensions");
    nx_ = (w_ + cw_ - 1) / cw_;
    ny_ = (h_ + ch_ - 1) / ch_;
    chunks_.resize(static_cast<size_t>(nx_) * ny_);
}

int ChunkedAnalyzer::chunkWidth(int cx) const { return std::min(cw_, w_ - cx * cw_); }
int ChunkedAnalyzer::chunkHeight(int cy) const { return std::min(ch_, h_ - cy * ch_); }

int ChunkedAnalyzer::fgFind(int x) const {
    while (fg_[x].parent != x) x = fg_[x].parent;
    return x;
}

void ChunkedAnalyzer::fgUnion(int a, int b) {
    a = fgFind(a);
    b = fgFind(b);
    if (a == b) return;
    if (fg_[a].rank < fg_[b].rank) std::swap(a, b);
    fg_[b].parent = a;
    if (fg_[a].rank == fg_[b].rank) fg_[a].rank++;
    fg_[a].area += fg_[b].area;
    fg_[a].adj += fg_[b].adj;
    if (fg_[b].minY < fg_[a].minY ||
        (fg_[b].minY == fg_[a].minY && fg_[b].minX < fg_[a].minX)) { // row-major min pixel
        fg_[a].minY = fg_[b].minY;
        fg_[a].minX = fg_[b].minX;
    }
    fg_[a].border = fg_[a].border || fg_[b].border;
}

int ChunkedAnalyzer::bgFind(int x) const {
    while (bg_[x].parent != x) x = bg_[x].parent;
    return x;
}

void ChunkedAnalyzer::bgUnion(int a, int b) {
    a = bgFind(a);
    b = bgFind(b);
    if (a == b) return;
    if (bg_[a].rank < bg_[b].rank) std::swap(a, b);
    bg_[b].parent = a;
    if (bg_[a].rank == bg_[b].rank) bg_[a].rank++;
    bg_[a].border = bg_[a].border || bg_[b].border;
    if (bg_[b].minY < bg_[a].minY ||
        (bg_[b].minY == bg_[a].minY && bg_[b].minX < bg_[a].minX)) {
        bg_[a].minY = bg_[b].minY;
        bg_[a].minX = bg_[b].minX;
    }
}

void ChunkedAnalyzer::addChunk(int cx, int cy, const uint8_t* data) {
    if (cx < 0 || cy < 0 || cx >= nx_ || cy >= ny_) throw std::out_of_range("chunk index");
    ChunkSummary& cs = chunks_[static_cast<size_t>(cy) * nx_ + cx];
    if (cs.present) throw std::logic_error("chunk added twice");
    const int w = chunkWidth(cx);
    const int h = chunkHeight(cy);

    std::vector<uint8_t> fgm(w * h), bgm(w * h);
    for (int i = 0; i < w * h; ++i) {
        fgm[i] = data[i] ? 1 : 0;
        bgm[i] = data[i] ? 0 : 1;
    }
    std::vector<int> fl, bl;
    const int nf = labelLocal(fgm, w, h, /*eightConn=*/true, fl);
    const int nb = labelLocal(bgm, w, h, /*eightConn=*/false, bl);
    cs.baseFg = static_cast<int>(fg_.size());
    cs.baseBg = static_cast<int>(bg_.size());

    std::vector<long> area(nf, 0), adj(nf, 0);
    std::vector<int> minx(nf, INT_MAX), miny(nf, INT_MAX);
    std::vector<char> fgBorder(nf, 0);
    for (int y = 0; y < h; ++y) {
        for (int x = 0; x < w; ++x) {
            const int lb = fl[y * w + x];
            if (lb < 0) continue;
            area[lb]++;
            const int gx = cx * cw_ + x, gy = cy * ch_ + y;
            if (gy < miny[lb] || (gy == miny[lb] && gx < minx[lb])) { // row-major min pixel
                miny[lb] = gy;
                minx[lb] = gx;
            }
            if (gx == 0 || gy == 0 || gx == w_ - 1 || gy == h_ - 1) fgBorder[lb] = 1;
            if (x + 1 < w && fl[y * w + x + 1] >= 0) adj[lb]++; // right pair
            if (y + 1 < h && fl[(y + 1) * w + x] >= 0) adj[lb]++; // down pair
        }
    }
    for (int i = 0; i < nf; ++i)
        fg_.push_back({cs.baseFg + i, 0, area[i], adj[i], minx[i], miny[i], fgBorder[i] != 0});

    std::vector<char> border(nb, 0);
    std::vector<int> bminx(nb, INT_MAX), bminy(nb, INT_MAX);
    for (int y = 0; y < h; ++y) {
        for (int x = 0; x < w; ++x) {
            const int lb = bl[y * w + x];
            if (lb < 0) continue;
            const int gx = cx * cw_ + x, gy = cy * ch_ + y;
            if (gx == 0 || gy == 0 || gx == w_ - 1 || gy == h_ - 1) border[lb] = 1;
            if (gy < bminy[lb] || (gy == bminy[lb] && gx < bminx[lb])) {
                bminy[lb] = gy;
                bminx[lb] = gx;
            }
        }
    }
    for (int i = 0; i < nb; ++i)
        bg_.push_back({cs.baseBg + i, 0, bminx[i], bminy[i], border[i] != 0});

    // Intra-chunk 4-adjacency between background and foreground (for hole ownership).
    for (int y = 0; y < h; ++y) {
        for (int x = 0; x < w; ++x) {
            const int bb = bl[y * w + x];
            if (bb < 0) continue;
            static const int dx[4] = {1, -1, 0, 0};
            static const int dy[4] = {0, 0, 1, -1};
            for (int d = 0; d < 4; ++d) {
                const int px = x + dx[d], py = y + dy[d];
                if (px < 0 || py < 0 || px >= w || py >= h) continue;
                const int fb = fl[py * w + px];
                if (fb >= 0) adjPairs_.emplace_back(cs.baseBg + bb, cs.baseFg + fb);
            }
        }
    }

    // Boundary summary: the only per-chunk pixel data retained.
    cs.top.resize(w);
    cs.bottom.resize(w);
    cs.left.resize(h);
    cs.right.resize(h);
    for (int x = 0; x < w; ++x) {
        cs.top[x] = {fl[x], bl[x]};
        cs.bottom[x] = {fl[(h - 1) * w + x], bl[(h - 1) * w + x]};
    }
    for (int y = 0; y < h; ++y) {
        cs.left[y] = {fl[y * w], bl[y * w]};
        cs.right[y] = {fl[y * w + w - 1], bl[y * w + w - 1]};
    }
    cs.present = true;
    stitchWithNeighbors(cx, cy);
}

void ChunkedAnalyzer::crossPair(ChunkSummary& a, const EdgePix& ea, ChunkSummary& b,
                                const EdgePix& eb, bool direct) {
    if (ea.fg >= 0 && eb.fg >= 0) {
        fgUnion(a.baseFg + ea.fg, b.baseFg + eb.fg);
        if (direct) fg_[fgFind(a.baseFg + ea.fg)].adj++; // cross-chunk 4-adjacent pair
    } else if (direct && ea.bg >= 0 && eb.bg >= 0) {
        bgUnion(a.baseBg + ea.bg, b.baseBg + eb.bg);
    } else if (direct) {
        if (ea.fg >= 0)
            adjPairs_.emplace_back(b.baseBg + eb.bg, a.baseFg + ea.fg);
        else
            adjPairs_.emplace_back(a.baseBg + ea.bg, b.baseFg + eb.fg);
    }
}

void ChunkedAnalyzer::stitchVertical(int idxLeft, int idxRight) {
    ChunkSummary& L = chunks_[idxLeft];
    ChunkSummary& R = chunks_[idxRight];
    const int h = static_cast<int>(L.right.size());
    for (int r = 0; r < h; ++r) {
        crossPair(L, L.right[r], R, R.left[r], /*direct=*/true);
        if (r > 0) crossPair(L, L.right[r], R, R.left[r - 1], /*direct=*/false);
        if (r + 1 < h) crossPair(L, L.right[r], R, R.left[r + 1], /*direct=*/false);
    }
}

void ChunkedAnalyzer::stitchHorizontal(int idxTop, int idxBottom) {
    ChunkSummary& T = chunks_[idxTop];
    ChunkSummary& B = chunks_[idxBottom];
    const int w = static_cast<int>(T.bottom.size());
    for (int x = 0; x < w; ++x) {
        crossPair(T, T.bottom[x], B, B.top[x], /*direct=*/true);
        if (x > 0) crossPair(T, T.bottom[x], B, B.top[x - 1], /*direct=*/false);
        if (x + 1 < w) crossPair(T, T.bottom[x], B, B.top[x + 1], /*direct=*/false);
    }
}

void ChunkedAnalyzer::stitchWithNeighbors(int cx, int cy) {
    const int me = cy * nx_ + cx;
    auto at = [&](int x, int y) -> ChunkSummary& { return chunks_[static_cast<size_t>(y) * nx_ + x]; };
    ChunkSummary& self = chunks_[me];
    const int w = chunkWidth(cx);

    if (cx > 0 && at(cx - 1, cy).present) stitchVertical(me - 1, me);
    if (cx + 1 < nx_ && at(cx + 1, cy).present) stitchVertical(me, me + 1);
    if (cy > 0 && at(cx, cy - 1).present) stitchHorizontal(me - nx_, me);
    if (cy + 1 < ny_ && at(cx, cy + 1).present) stitchHorizontal(me, me + nx_);

    // Diagonal neighbors touch only at a single corner pixel (8-connectivity).
    if (cx + 1 < nx_ && cy + 1 < ny_ && at(cx + 1, cy + 1).present)
        crossPair(self, self.bottom[w - 1], at(cx + 1, cy + 1), at(cx + 1, cy + 1).top[0], false);
    if (cx > 0 && cy > 0 && at(cx - 1, cy - 1).present)
        crossPair(self, self.top[0], at(cx - 1, cy - 1), at(cx - 1, cy - 1).bottom.back(), false);
    if (cx + 1 < nx_ && cy > 0 && at(cx + 1, cy - 1).present)
        crossPair(self, self.top[w - 1], at(cx + 1, cy - 1), at(cx + 1, cy - 1).bottom[0], false);
    if (cx > 0 && cy + 1 < ny_ && at(cx - 1, cy + 1).present)
        crossPair(self, self.bottom[0], at(cx - 1, cy + 1), at(cx - 1, cy + 1).top.back(), false);
}

void ChunkedAnalyzer::finalize() {
    for (const auto& c : chunks_)
        if (!c.present) throw std::logic_error("finalize called with missing chunks");

    idOf_.assign(fg_.size(), 0);
    std::vector<int> roots;
    for (int i = 0; i < static_cast<int>(fg_.size()); ++i)
        if (fgFind(i) == i) roots.push_back(i);
    std::sort(roots.begin(), roots.end(), [&](int a, int b) {
        if (fg_[a].minY != fg_[b].minY) return fg_[a].minY < fg_[b].minY;
        if (fg_[a].minX != fg_[b].minX) return fg_[a].minX < fg_[b].minX;
        return a < b;
    });

    comps_.clear();
    comps_.reserve(roots.size());
    for (int i = 0; i < static_cast<int>(roots.size()); ++i) {
        const int r = roots[i];
        idOf_[r] = i + 1;
        ComponentStats s;
        s.id = i + 1;
        s.minX = fg_[r].minX;
        s.minY = fg_[r].minY;
        s.area = fg_[r].area;
        s.perimeter = 4 * fg_[r].area - 2 * fg_[r].adj;
        s.holes = 0;
        comps_.push_back(s);
    }

    // Hole ownership via the nesting tree: foreground/background components are
    // nodes of an adjacency graph (4-adjacency between regions); rooting it at the
    // virtual exterior makes each hole's parent the foreground component that
    // encloses it. Traversal order is canonical (component min-pixel keys), so the
    // result is independent of chunk arrival order.
    std::vector<int> bgRoots;
    for (int i = 0; i < static_cast<int>(bg_.size()); ++i)
        if (bgFind(i) == i) bgRoots.push_back(i);
    const int nF = static_cast<int>(roots.size());
    const int nB = static_cast<int>(bgRoots.size());
    std::unordered_map<int, int> bgNodeOf;
    for (int i = 0; i < nB; ++i) bgNodeOf[bgRoots[i]] = nF + i;
    std::vector<std::vector<int>> tree(nF + nB);
    for (const auto& p : adjPairs_) {
        const int u = idOf_[fgFind(p.second)] - 1; // fg node index
        const int v = bgNodeOf[bgFind(p.first)];   // bg node index
        tree[u].push_back(v);
        tree[v].push_back(u);
    }
    auto keyOf = [&](int node) {
        if (node < nF) {
            const FgNode& fn = fg_[roots[node]];
            return std::make_pair(fn.minY, fn.minX);
        }
        const BgNode& bn = bg_[bgRoots[node - nF]];
        return std::make_pair(bn.minY, bn.minX);
    };
    for (auto& lst : tree) {
        std::sort(lst.begin(), lst.end(), [&](int a, int b) { return keyOf(a) < keyOf(b); });
        lst.erase(std::unique(lst.begin(), lst.end()), lst.end());
    }
    std::vector<int> parent(nF + nB, -2); // -2 = unvisited, -1 = virtual exterior
    std::vector<int> queue;
    for (int i = 0; i < nF; ++i)
        if (fg_[roots[i]].border) {
            parent[i] = -1;
            queue.push_back(i);
        }
    for (int i = 0; i < nB; ++i)
        if (bg_[bgRoots[i]].border) {
            parent[nF + i] = -1;
            queue.push_back(nF + i);
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
    for (int i = 0; i < nB; ++i) {
        if (bg_[bgRoots[i]].border) continue;
        const int par = parent[nF + i];
        if (par >= 0) comps_[par].holes++; // par is an fg node index == id - 1
    }

    finalized_ = true;
}

std::vector<int> ChunkedAnalyzer::labelChunk(int cx, int cy, const uint8_t* data) const {
    if (!finalized_) throw std::logic_error("not finalized");
    if (cx < 0 || cy < 0 || cx >= nx_ || cy >= ny_) throw std::out_of_range("chunk index");
    const ChunkSummary& cs = chunks_[static_cast<size_t>(cy) * nx_ + cx];
    const int w = chunkWidth(cx);
    const int h = chunkHeight(cy);
    std::vector<uint8_t> fgm(w * h);
    for (int i = 0; i < w * h; ++i) fgm[i] = data[i] ? 1 : 0;
    std::vector<int> fl;
    labelLocal(fgm, w, h, /*eightConn=*/true, fl);
    std::vector<int> out(w * h, 0);
    for (int i = 0; i < w * h; ++i)
        if (fl[i] >= 0) out[i] = idOf_[fgFind(cs.baseFg + fl[i])];
    return out;
}

} // namespace chunked
