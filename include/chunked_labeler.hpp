#pragma once

// Chunked connected-component analysis of a binary image.
//
// The image is delivered as rectangular blocks that may arrive in any
// order. Only the block currently being processed and an O(perimeter)
// boundary summary per consumed block are retained; full pixel data of
// earlier blocks is discarded.
//
// Semantics (identical to a whole-image two-pass analysis):
//   - foreground components use 8-connectivity;
//   - background components (holes) use 4-connectivity;
//   - perimeter counts 4-neighbour pixel edges between foreground and
//     background, where the image exterior counts as background;
//   - a hole is a background component not touching the image border;
//     it is attributed to the adjacent foreground component with the
//     smallest stable label;
//   - final labels 1..N are assigned in (minRow, minCol) order of each
//     component's topmost-leftmost pixel.

#include <cstdint>
#include <set>
#include <utility>
#include <vector>

class ChunkedLabeler {
public:
    struct ComponentStats {
        int label = 0;         // 1..N in (minRow, minCol) order
        long long area = 0;    // foreground pixel count
        long long perimeter = 0;
        long long holes = 0;   // enclosed background components
        int minRow = 0;
        int minCol = 0;
    };

    ChunkedLabeler(int rows, int cols);

    // Consumes one block. data is row-major with h*w entries;
    // 0 = background, non-zero = foreground. Blocks must tile the
    // image exactly once, but may arrive in any order and may have
    // arbitrary (non-uniform) rectangles.
    void addBlock(int row0, int col0, int h, int w,
                  const std::vector<std::uint8_t>& data);

    // Stable per-component statistics. Call after all blocks arrived.
    std::vector<ComponentStats> finalize();

    int rows() const { return rows_; }
    int cols() const { return cols_; }

private:
    struct UnionFind {
        std::vector<int> parent;
        int add() {
            parent.push_back(static_cast<int>(parent.size()));
            return static_cast<int>(parent.size()) - 1;
        }
        int find(int x) {
            while (parent[x] != x) {
                parent[x] = parent[parent[x]];
                x = parent[x];
            }
            return x;
        }
        void unite(int a, int b) {
            a = find(a);
            b = find(b);
            if (a != b) parent[b] = a;
        }
        int size() const { return static_cast<int>(parent.size()); }
    };

    // One-pixel-wide frame of a consumed block. fg/bg hold global label
    // ids (-1 when the pixel belongs to the other class).
    struct Edge {
        std::vector<std::uint8_t> val;
        std::vector<int> fg;
        std::vector<int> bg;
    };
    struct BlockSummary {
        int row0, col0, h, w;
        Edge top, bottom, left, right;
    };

    void mergeEdgePair(const Edge& a, int startA, const Edge& b, int startB);
    void mergeCorner(int fgA, int fgB);

    int rows_, cols_;
    UnionFind fgUf_, bgUf_;
    // Per-global-label accumulators; aggregated by root in finalize().
    std::vector<long long> fgArea_, fgPerim_;
    std::vector<int> fgMinR_, fgMinC_;
    std::vector<char> bgExterior_;
    // 4-adjacent (bg label, fg label) pairs, used for hole attribution.
    std::set<std::pair<int, int>> adj_;
    std::vector<BlockSummary> blocks_;
};
