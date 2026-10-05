#include <algorithm>
#include <cstdio>
#include <cstdlib>
#include <numeric>
#include <random>
#include <string>
#include <vector>

#include "chunked_analyzer.h"
#include "reference.h"

static int failures = 0;

#define CHECK(cond, msg)                                       \
    do {                                                       \
        if (!(cond)) {                                         \
            std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, msg); \
            ++failures;                                        \
        }                                                      \
    } while (0)

// Runs the chunked analyzer over the whole image with a random chunk arrival order
// and compares stats + materialized label map against the whole-image reference.
static void compareWithReference(const std::vector<uint8_t>& img, int W, int H, int cw, int ch,
                                 const std::string& name, std::mt19937& rng) {
    chunked::ChunkedAnalyzer an(W, H, cw, ch);
    std::vector<std::pair<int, int>> order;
    for (int cy = 0; cy < an.chunksY(); ++cy)
        for (int cx = 0; cx < an.chunksX(); ++cx) order.emplace_back(cx, cy);
    std::shuffle(order.begin(), order.end(), rng);

    std::vector<std::vector<uint8_t>> chunkData(order.size());
    for (auto [cx, cy] : order) {
        const int w = an.chunkWidth(cx), h = an.chunkHeight(cy);
        std::vector<uint8_t> data(w * h);
        for (int y = 0; y < h; ++y)
            for (int x = 0; x < w; ++x) data[y * w + x] = img[(cy * ch + y) * W + cx * cw + x];
        chunkData[cy * an.chunksX() + cx] = data;
        an.addChunk(cx, cy, data.data());
    }
    an.finalize();

    ref::Model rm = ref::run(img, W, H);

    const auto& got = an.components();
    CHECK(got.size() == rm.comps.size(), (name + ": component count").c_str());
    const size_t ncmp = std::min(got.size(), rm.comps.size());
    for (size_t i = 0; i < ncmp; ++i) {
        const auto& a = got[i];
        const auto& b = rm.comps[i];
        CHECK(a.id == static_cast<int>(i) + 1, (name + ": stable id").c_str());
        CHECK(a.minX == b.minX && a.minY == b.minY, (name + ": min coord").c_str());
        CHECK(a.area == b.area, (name + ": area").c_str());
        CHECK(a.perimeter == b.perimeter, (name + ": perimeter").c_str());
        CHECK(a.holes == b.holes, (name + ": holes").c_str());
    }

    // Full label map comparison.
    std::vector<int> gotLabels(W * H, 0);
    for (int cy = 0; cy < an.chunksY(); ++cy)
        for (int cx = 0; cx < an.chunksX(); ++cx) {
            const int w = an.chunkWidth(cx), h = an.chunkHeight(cy);
            std::vector<int> lab = an.labelChunk(cx, cy, chunkData[cy * an.chunksX() + cx].data());
            for (int y = 0; y < h; ++y)
                for (int x = 0; x < w; ++x) gotLabels[(cy * ch + y) * W + cx * cw + x] = lab[y * w + x];
        }
    CHECK(gotLabels == rm.labels, (name + ": label map").c_str());
}

static void setPx(std::vector<uint8_t>& img, int W, int x, int y) { img[y * W + x] = 1; }

// Diagonal contact across the shared corner of four chunks must merge (8-connectivity).
static void testCornerTouch(std::mt19937& rng) {
    const int W = 8, H = 8, C = 4;
    std::vector<uint8_t> img(W * H, 0);
    setPx(img, W, 3, 3); // bottom-right corner of chunk (0,0)
    setPx(img, W, 4, 4); // top-left corner of chunk (1,1)
    compareWithReference(img, W, H, C, C, "corner-touch", rng);

    chunked::ChunkedAnalyzer an(W, H, C, C);
    std::vector<uint8_t> data(C * C);
    for (int cy = 0; cy < 2; ++cy)
        for (int cx = 0; cx < 2; ++cx) {
            for (int y = 0; y < C; ++y)
                for (int x = 0; x < C; ++x) data[y * C + x] = img[(cy * C + y) * W + cx * C + x];
            an.addChunk(cx, cy, data.data());
        }
    an.finalize();
    CHECK(an.components().size() == 1, "corner-touch: merged into one component");
    CHECK(an.components()[0].area == 2, "corner-touch: area");
    CHECK(an.components()[0].perimeter == 8, "corner-touch: perimeter");
}

// A 2x2 foreground block straddling the junction of four chunks is one component.
static void testFourChunkJunction(std::mt19937& rng) {
    const int W = 8, H = 8, C = 4;
    std::vector<uint8_t> img(W * H, 0);
    setPx(img, W, 3, 3);
    setPx(img, W, 4, 3);
    setPx(img, W, 3, 4);
    setPx(img, W, 4, 4);
    compareWithReference(img, W, H, C, C, "four-chunk-junction", rng);

    // Diagonal-only contact at the junction point.
    std::vector<uint8_t> img2(W * H, 0);
    setPx(img2, W, 3, 3);
    setPx(img2, W, 4, 4);
    setPx(img2, W, 5, 5);
    compareWithReference(img2, W, H, C, C, "four-chunk-junction-diag", rng);
}

// All-background chunk borders must not create spurious merges.
static void testAllBlackBoundary(std::mt19937& rng) {
    const int W = 8, H = 8, C = 4;
    std::vector<uint8_t> img(W * H, 0);
    setPx(img, W, 1, 1);
    setPx(img, W, 6, 1);
    setPx(img, W, 1, 6);
    setPx(img, W, 6, 6);
    compareWithReference(img, W, H, C, C, "all-black-boundary", rng);

    chunked::ChunkedAnalyzer an(W, H, C, C);
    std::vector<uint8_t> data(C * C);
    for (int cy = 0; cy < 2; ++cy)
        for (int cx = 0; cx < 2; ++cx) {
            for (int y = 0; y < C; ++y)
                for (int x = 0; x < C; ++x) data[y * C + x] = img[(cy * C + y) * W + cx * C + x];
            an.addChunk(cx, cy, data.data());
        }
    an.finalize();
    CHECK(an.components().size() == 4, "all-black-boundary: four components");
    for (const auto& c : an.components())
        CHECK(c.area == 1 && c.perimeter == 4 && c.holes == 0, "all-black-boundary: stats");
}

// A foreground ring crossing chunk borders encloses exactly one background hole.
static void testHoleAcrossChunks(std::mt19937& rng) {
    const int W = 12, H = 12, C = 4;
    std::vector<uint8_t> img(W * H, 0);
    for (int i = 2; i <= 9; ++i) { // 8x8 outline from (2,2) to (9,9)
        setPx(img, W, i, 2);
        setPx(img, W, i, 9);
        setPx(img, W, 2, i);
        setPx(img, W, 9, i);
    }
    compareWithReference(img, W, H, C, C, "hole-across-chunks", rng);

    chunked::ChunkedAnalyzer an(W, H, C, C);
    std::vector<uint8_t> data(C * C);
    for (int cy = 0; cy < 3; ++cy)
        for (int cx = 0; cx < 3; ++cx) {
            for (int y = 0; y < C; ++y)
                for (int x = 0; x < C; ++x) data[y * C + x] = img[(cy * C + y) * W + cx * C + x];
            an.addChunk(cx, cy, data.data());
        }
    an.finalize();
    CHECK(an.components().size() == 1, "hole: one component");
    if (!an.components().empty()) {
        CHECK(an.components()[0].holes == 1, "hole: exactly one hole");
        CHECK(an.components()[0].area == 28, "hole: ring area");
        CHECK(an.components()[0].perimeter == 56, "hole: outer+inner perimeter");
    }
}

// Nested rings: outer ring owns one hole, the inner disk is separate.
static void testNestedHoles(std::mt19937& rng) {
    const int W = 16, H = 16, C = 5; // non-divisible chunk size on purpose
    std::vector<uint8_t> img(W * H, 0);
    for (int i = 2; i <= 13; ++i) {
        setPx(img, W, i, 2);
        setPx(img, W, i, 13);
        setPx(img, W, 2, i);
        setPx(img, W, 13, i);
    }
    for (int y = 6; y <= 9; ++y)
        for (int x = 6; x <= 9; ++x) setPx(img, W, x, y); // solid inner disk
    compareWithReference(img, W, H, C, C, "nested-holes", rng);

    // The background ring between the outer ring and the inner disk is 4-adjacent
    // to BOTH foreground components; the hole must be owned by the enclosing ring.
    chunked::ChunkedAnalyzer an(W, H, C, C);
    std::vector<uint8_t> data(an.chunkWidth(0) * an.chunkHeight(0));
    for (int cy = 0; cy < an.chunksY(); ++cy)
        for (int cx = 0; cx < an.chunksX(); ++cx) {
            const int w = an.chunkWidth(cx), h = an.chunkHeight(cy);
            data.resize(w * h);
            for (int y = 0; y < h; ++y)
                for (int x = 0; x < w; ++x)
                    data[y * w + x] = img[(cy * C + y) * W + cx * C + x];
            an.addChunk(cx, cy, data.data());
        }
    an.finalize();
    CHECK(an.components().size() == 2, "nested-holes: two components");
    if (an.components().size() == 2) {
        const auto& ring = an.components()[0]; // min pixel (2,2)
        const auto& disk = an.components()[1]; // min pixel (6,6)
        CHECK(ring.minX == 2 && ring.minY == 2, "nested-holes: ring is component 1");
        CHECK(ring.holes == 1, "nested-holes: ring owns the hole");
        CHECK(disk.holes == 0, "nested-holes: disk owns no hole");
    }
}

// API misuse must be reported.
static void testApiMisuse() {
    chunked::ChunkedAnalyzer an(4, 4, 4, 4);
    std::vector<uint8_t> data(16, 0);
    an.addChunk(0, 0, data.data());
    bool threw = false;
    try {
        an.addChunk(0, 0, data.data());
    } catch (const std::logic_error&) {
        threw = true;
    }
    CHECK(threw, "api: duplicate chunk rejected");
    threw = false;
    try {
        chunked::ChunkedAnalyzer an2(8, 8, 4, 4);
        an2.finalize();
    } catch (const std::logic_error&) {
        threw = true;
    }
    CHECK(threw, "api: finalize with missing chunks rejected");
    threw = false;
    try {
        chunked::ChunkedAnalyzer an3(4, 4, 4, 4);
        an3.addChunk(1, 0, data.data());
    } catch (const std::out_of_range&) {
        threw = true;
    }
    CHECK(threw, "api: out-of-range chunk rejected");
}

static void testRandom(std::mt19937&) {
    for (int iter = 0; iter < 1000; ++iter) {
        std::mt19937 rng(777000 + iter); // per-iteration seed for reproducibility
        std::uniform_int_distribution<int> dimD(1, 48);
        std::uniform_int_distribution<int> chunkD(1, 13);
        std::uniform_real_distribution<double> probD(0.05, 0.95);
        const int W = dimD(rng), H = dimD(rng);
        const int cw = chunkD(rng), ch = chunkD(rng);
        const double p = probD(rng);
        std::vector<uint8_t> img(W * H);
        std::bernoulli_distribution bd(p);
        for (auto& v : img) v = bd(rng) ? 1 : 0;
        compareWithReference(img, W, H, cw, ch,
                             "random-" + std::to_string(iter) + " W=" + std::to_string(W) +
                                 " H=" + std::to_string(H) + " cw=" + std::to_string(cw) +
                                 " ch=" + std::to_string(ch),
                             rng);
        if (failures > 10) return; // stop early on systematic breakage
    }
}

// Structured patterns: stripes, checkerboards, single-pixel-wide snakes across chunks.
static void testStructured(std::mt19937& rng) {
    for (int W = 1; W <= 33; W += 4)
        for (int H = 1; H <= 33; H += 5)
            for (int cw = 1; cw <= 9; cw += 3)
                for (int ch = 1; ch <= 9; ch += 4) {
                    std::vector<uint8_t> img(W * H);
                    for (int y = 0; y < H; ++y)
                        for (int x = 0; x < W; ++x) img[y * W + x] = (x + y) % 2; // checkerboard
                    compareWithReference(img, W, H, cw, ch, "checkerboard", rng);
                    for (int y = 0; y < H; ++y)
                        for (int x = 0; x < W; ++x) img[y * W + x] = (x % 3 == 0) ? 1 : 0; // stripes
                    compareWithReference(img, W, H, cw, ch, "stripes", rng);
                    std::fill(img.begin(), img.end(), 1); // all foreground
                    compareWithReference(img, W, H, cw, ch, "all-foreground", rng);
                    std::fill(img.begin(), img.end(), 0); // all background
                    compareWithReference(img, W, H, cw, ch, "all-background", rng);
                    if (failures > 10) return;
                }
}

int main() {
    std::mt19937 rng(20261005);
    testCornerTouch(rng);
    testFourChunkJunction(rng);
    testAllBlackBoundary(rng);
    testHoleAcrossChunks(rng);
    testNestedHoles(rng);
    testApiMisuse();
    testStructured(rng);
    testRandom(rng);
    if (failures == 0) {
        std::printf("ALL TESTS PASSED\n");
        return 0;
    }
    std::printf("%d FAILURES\n", failures);
    return 1;
}
