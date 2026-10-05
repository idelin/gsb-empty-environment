#pragma once

#include <cstdint>
#include <utility>
#include <vector>

namespace chunked {

// Statistics of one 8-connected foreground component.
struct ComponentStats {
    int id = 0;         // 1-based, assigned in row-major order of the component's min pixel
    int minX = 0;       // smallest pixel x of the component (global coords)
    int minY = 0;       // smallest pixel y of the component (global coords)
    long area = 0;      // number of foreground pixels
    long perimeter = 0; // 4-neighborhood boundary length (outer + hole boundaries + image border)
    long holes = 0;     // enclosed 4-connected background regions owned by this component
};

// Chunks of a binary image may arrive in any order; only the current chunk and
// per-chunk boundary summaries are kept in memory. Foreground uses 8-connectivity,
// background uses 4-connectivity (consistent Jordan pairing).
class ChunkedAnalyzer {
public:
    ChunkedAnalyzer(int width, int height, int chunkWidth, int chunkHeight);

    int width() const { return w_; }
    int height() const { return h_; }
    int chunksX() const { return nx_; }
    int chunksY() const { return ny_; }
    int chunkWidth(int cx) const;
    int chunkHeight(int cy) const;

    // Feeds one chunk. Each chunk must be added exactly once.
    // data: row-major chunkWidth(cx) * chunkHeight(cy) bytes; 0 = background, else foreground.
    void addChunk(int cx, int cy, const uint8_t* data);

    // Computes final statistics. All chunks must have been added.
    void finalize();

    // Components sorted by (minY, minX); id == index + 1.
    const std::vector<ComponentStats>& components() const { return comps_; }

    // Materializes final label ids for one chunk (0 = background).
    // `data` must be identical to what was passed to addChunk.
    std::vector<int> labelChunk(int cx, int cy, const uint8_t* data) const;

private:
    struct EdgePix {
        int fg = -1; // local foreground label of the edge pixel, -1 if background
        int bg = -1; // local background label of the edge pixel, -1 if foreground
    };
    struct ChunkSummary {
        bool present = false;
        int baseFg = 0; // offset of this chunk's local fg labels in fg_
        int baseBg = 0; // offset of this chunk's local bg labels in bg_
        std::vector<EdgePix> top, bottom, left, right;
    };
    struct FgNode {
        int parent;
        int rank;
        long area;
        long adj; // number of 4-adjacent fg-fg pixel pairs
        int minX, minY;  // row-major smallest pixel of the component
        bool border;     // touches the outer image boundary
    };
    struct BgNode {
        int parent;
        int rank;
        int minX, minY;  // row-major smallest pixel of the component
        bool border;     // touches the outer image boundary
    };

    int fgFind(int x) const;
    void fgUnion(int a, int b);
    int bgFind(int x) const;
    void bgUnion(int a, int b);

    void stitchWithNeighbors(int cx, int cy);
    void stitchVertical(int idxLeft, int idxRight);
    void stitchHorizontal(int idxTop, int idxBottom);
    void crossPair(ChunkSummary& a, const EdgePix& ea, ChunkSummary& b, const EdgePix& eb, bool direct);

    int w_, h_, cw_, ch_, nx_, ny_;
    std::vector<ChunkSummary> chunks_;
    std::vector<FgNode> fg_;
    std::vector<BgNode> bg_;
    std::vector<std::pair<int, int>> adjPairs_; // (bg node, fg node) 4-adjacency records
    std::vector<int> idOf_;                     // fg root node -> final component id
    std::vector<ComponentStats> comps_;
    bool finalized_ = false;
};

} // namespace chunked
