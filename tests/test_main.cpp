#include <algorithm>
#include <cstdio>
#include <numeric>
#include <random>
#include <string>
#include <vector>

#include "chunked_labeler.hpp"
#include "reference_model.hpp"

using Stats = ChunkedLabeler::ComponentStats;

static int g_failures = 0;
static int g_checks = 0;

static void fail(const std::string& name, const std::string& what) {
    g_failures++;
    std::printf("FAIL [%s] %s\n", name.c_str(), what.c_str());
}

static void checkEqual(const std::vector<Stats>& got,
                       const std::vector<Stats>& want,
                       const std::string& name) {
    g_checks++;
    if (got.size() != want.size()) {
        fail(name, "component count: got " + std::to_string(got.size()) +
                       " want " + std::to_string(want.size()));
        return;
    }
    for (size_t i = 0; i < got.size(); ++i) {
        const Stats& a = got[i];
        const Stats& b = want[i];
        if (a.label != b.label || a.area != b.area ||
            a.perimeter != b.perimeter || a.holes != b.holes ||
            a.minRow != b.minRow || a.minCol != b.minCol) {
            fail(name,
                 "comp " + std::to_string(i) + ": got{lab=" +
                     std::to_string(a.label) + " area=" + std::to_string(a.area) +
                     " per=" + std::to_string(a.perimeter) +
                     " holes=" + std::to_string(a.holes) + " min=(" +
                     std::to_string(a.minRow) + "," + std::to_string(a.minCol) +
                     ")} want{lab=" + std::to_string(b.label) +
                     " area=" + std::to_string(b.area) +
                     " per=" + std::to_string(b.perimeter) +
                     " holes=" + std::to_string(b.holes) + " min=(" +
                     std::to_string(b.minRow) + "," + std::to_string(b.minCol) +
                     ")}");
        }
    }
}

struct Rect {
    int r0, c0, h, w;
};

// Runs the chunked labeler over `img` partitioned by `rects`, feeding
// blocks in `order`, and compares against the whole-image model.
static void runChunkedVsReference(const std::vector<std::uint8_t>& img, int R,
                                  int C, const std::vector<Rect>& rects,
                                  const std::vector<int>& order,
                                  const std::string& name) {
    ChunkedLabeler lab(R, C);
    for (int idx : order) {
        const Rect& rc = rects[idx];
        std::vector<std::uint8_t> block(rc.h * rc.w);
        for (int r = 0; r < rc.h; ++r)
            for (int c = 0; c < rc.w; ++c)
                block[r * rc.w + c] = img[(rc.r0 + r) * C + (rc.c0 + c)];
        lab.addBlock(rc.r0, rc.c0, rc.h, rc.w, block);
    }
    checkEqual(lab.finalize(), referenceModel(img, R, C), name);
}

// Regular grid tiling from cut positions.
static std::vector<Rect> gridRects(int R, int C, const std::vector<int>& rowCuts,
                                   const std::vector<int>& colCuts) {
    std::vector<int> rows{0};
    rows.insert(rows.end(), rowCuts.begin(), rowCuts.end());
    rows.push_back(R);
    std::vector<int> cols{0};
    cols.insert(cols.end(), colCuts.begin(), colCuts.end());
    cols.push_back(C);
    std::vector<Rect> out;
    for (size_t i = 0; i + 1 < rows.size(); ++i)
        for (size_t j = 0; j + 1 < cols.size(); ++j)
            out.push_back({rows[i], cols[j], rows[i + 1] - rows[i],
                           cols[j + 1] - cols[j]});
    return out;
}

// Irregular (guillotine) tiling: recursively split rectangles.
static void guillotine(int r0, int c0, int h, int w, int depth,
                       std::mt19937& rng, std::vector<Rect>& out) {
    if (depth == 0 || (h == 1 && w == 1)) {
        out.push_back({r0, c0, h, w});
        return;
    }
    bool canH = h > 1, canV = w > 1;
    bool horizontal = canH && (!canV || (rng() & 1));
    if (horizontal) {
        std::uniform_int_distribution<int> cut(1, h - 1);
        int k = cut(rng);
        guillotine(r0, c0, k, w, depth - 1, rng, out);
        guillotine(r0 + k, c0, h - k, w, depth - 1, rng, out);
    } else {
        std::uniform_int_distribution<int> cut(1, w - 1);
        int k = cut(rng);
        guillotine(r0, c0, h, k, depth - 1, rng, out);
        guillotine(r0, c0 + k, h, w - k, depth - 1, rng, out);
    }
}

static std::vector<int> shuffledOrder(int n, std::mt19937& rng) {
    std::vector<int> ord(n);
    std::iota(ord.begin(), ord.end(), 0);
    std::shuffle(ord.begin(), ord.end(), rng);
    return ord;
}

// 1) Corner contact: diagonal foreground pixels meeting exactly at the
//    four-block junction must merge (8-connectivity).
static void testCornerTouch() {
    const int R = 6, C = 6;
    auto rects = gridRects(R, C, {3}, {3});
    std::mt19937 rng(42);

    // Four pixels around the junction: each touches the others through
    // 8-connectivity, so all four merge into one component of area 4.
    std::vector<std::uint8_t> img(R * C, 0);
    img[2 * C + 2] = 1;  // NW block, bottom-right corner
    img[3 * C + 3] = 1;  // SE block, top-left corner
    img[2 * C + 3] = 1;  // NE block, bottom-left corner
    img[3 * C + 2] = 1;  // SW block, top-right corner
    runChunkedVsReference(img, R, C, rects, shuffledOrder(4, rng),
                          "corner_touch_four_pixels");
    auto comps = referenceModel(img, R, C);
    g_checks++;
    if (comps.size() != 1 || comps[0].area != 4)
        fail("corner_touch_four_pixels", "expected 1 component of area 4");

    // A diagonal pair across the junction plus a disjoint pair elsewhere
    // -> two components of area 2, perimeter 8 each.
    std::vector<std::uint8_t> img2(R * C, 0);
    img2[2 * C + 2] = 1;
    img2[3 * C + 3] = 1;
    img2[0 * C + 5] = 1;
    img2[1 * C + 4] = 1;
    runChunkedVsReference(img2, R, C, rects, shuffledOrder(4, rng),
                          "corner_touch_two_pairs");
    auto comps2 = referenceModel(img2, R, C);
    g_checks++;
    if (comps2.size() != 2 || comps2[0].area != 2 || comps2[1].area != 2 ||
        comps2[0].perimeter != 8 || comps2[1].perimeter != 8)
        fail("corner_touch_two_pairs",
             "expected 2 comps, area 2, perim 8 each");
}

// 2) Four-block junction: shapes centred exactly on the junction of four
//    blocks, including a ring whose hole spans all four blocks.
static void testFourBlockJunction() {
    const int R = 8, C = 8;
    auto rects = gridRects(R, C, {4}, {4});
    std::mt19937 rng(7);

    // Plus shape centred on the junction.
    std::vector<std::uint8_t> plus(R * C, 0);
    for (int r = 2; r <= 5; ++r) plus[r * C + 4] = 1;
    for (int c = 2; c <= 5; ++c) plus[4 * C + c] = 1;
    runChunkedVsReference(plus, R, C, rects, shuffledOrder(4, rng),
                          "junction_plus");

    // Ring rows/cols 2..5: hole is the 2x2 block straddling the junction.
    std::vector<std::uint8_t> ring(R * C, 0);
    for (int k = 2; k <= 5; ++k) {
        ring[2 * C + k] = 1;
        ring[5 * C + k] = 1;
        ring[k * C + 2] = 1;
        ring[k * C + 5] = 1;
    }
    runChunkedVsReference(ring, R, C, rects, shuffledOrder(4, rng),
                          "junction_ring_hole");
    auto comps = referenceModel(ring, R, C);
    g_checks++;
    if (comps.size() != 1 || comps[0].holes != 1 || comps[0].area != 12)
        fail("junction_ring_hole", "expected 1 comp, 1 hole, area 12");

    // Diagonal chain threading through the junction only via corners.
    std::vector<std::uint8_t> diag(R * C, 0);
    for (int k = 0; k < 8; ++k) diag[k * C + k] = 1;
    runChunkedVsReference(diag, R, C, rects, shuffledOrder(4, rng),
                          "junction_diagonal_chain");
    auto comps3 = referenceModel(diag, R, C);
    g_checks++;
    if (comps3.size() != 1 || comps3[0].area != 8)
        fail("junction_diagonal_chain", "expected 1 comp of area 8");
}

// 3) All-black boundaries: blocks separated by empty edges must not merge;
//    degenerate all-background and all-foreground images.
static void testAllBlackBoundary() {
    const int R = 9, C = 9;
    auto rects = gridRects(R, C, {3, 6}, {3, 6});
    std::mt19937 rng(9);

    // One foreground pixel at each block centre; every block edge is black.
    std::vector<std::uint8_t> img(R * C, 0);
    for (int br = 0; br < 3; ++br)
        for (int bc = 0; bc < 3; ++bc) img[(br * 3 + 1) * C + (bc * 3 + 1)] = 1;
    runChunkedVsReference(img, R, C, rects, shuffledOrder(9, rng),
                          "black_boundary_isolated_pixels");
    auto comps = referenceModel(img, R, C);
    g_checks++;
    if (comps.size() != 9)
        fail("black_boundary_isolated_pixels", "expected 9 components");
    for (const auto& cs : comps)
        if (cs.area != 1 || cs.perimeter != 4)
            fail("black_boundary_isolated_pixels",
                 "expected area 1, perimeter 4");

    // Fully black image: no components, no holes.
    std::vector<std::uint8_t> black(R * C, 0);
    runChunkedVsReference(black, R, C, rects, shuffledOrder(9, rng),
                          "all_black");
    g_checks++;
    if (!referenceModel(black, R, C).empty()) fail("all_black", "expected 0 comps");

    // Fully white image: one component, perimeter = image outline, no holes.
    const int RW = 5, CW = 7;
    std::vector<std::uint8_t> white(RW * CW, 1);
    auto wrects = gridRects(RW, CW, {2, 4}, {3, 5});
    runChunkedVsReference(white, RW, CW, wrects, shuffledOrder(9, rng),
                          "all_white");
    auto wcomps = referenceModel(white, RW, CW);
    g_checks++;
    if (wcomps.size() != 1 || wcomps[0].area != RW * CW ||
        wcomps[0].perimeter != 2 * (RW + CW) || wcomps[0].holes != 0)
        fail("all_white", "expected 1 comp, perim 2(R+C), 0 holes");
}

// 4) Randomised consistency: random images, grid and guillotine tilings,
//    shuffled block arrival; chunked result must equal the whole-image model.
static void testRandomConsistency() {
    std::mt19937 rng(20261006);
    const double densities[] = {0.05, 0.2, 0.4, 0.6, 0.8, 0.95};
    for (int iter = 0; iter < 400; ++iter) {
        std::uniform_int_distribution<int> dimDist(1, 30);
        int R = dimDist(rng), C = dimDist(rng);
        double p = densities[rng() % 6];
        std::bernoulli_distribution fg(p);
        std::vector<std::uint8_t> img(R * C);
        for (auto& v : img) v = fg(rng) ? 1 : 0;

        std::vector<Rect> rects;
        if (rng() & 1) {
            // Grid tiling with 0..3 random cuts per axis.
            std::vector<int> rowCuts, colCuts;
            for (int k = 0; k < 3; ++k) {
                if (R > 1) {
                    std::uniform_int_distribution<int> d(1, R - 1);
                    rowCuts.push_back(d(rng));
                }
                if (C > 1) {
                    std::uniform_int_distribution<int> d(1, C - 1);
                    colCuts.push_back(d(rng));
                }
            }
            std::sort(rowCuts.begin(), rowCuts.end());
            rowCuts.erase(std::unique(rowCuts.begin(), rowCuts.end()),
                          rowCuts.end());
            std::sort(colCuts.begin(), colCuts.end());
            colCuts.erase(std::unique(colCuts.begin(), colCuts.end()),
                          colCuts.end());
            rects = gridRects(R, C, rowCuts, colCuts);
        } else {
            std::uniform_int_distribution<int> depthDist(1, 4);
            guillotine(0, 0, R, C, depthDist(rng), rng, rects);
        }
        runChunkedVsReference(img, R, C, rects,
                              shuffledOrder((int)rects.size(), rng),
                              "random_" + std::to_string(iter));
    }
}

int main() {
    testCornerTouch();
    testFourBlockJunction();
    testAllBlackBoundary();
    testRandomConsistency();
    std::printf("%d checks, %d failures\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}
