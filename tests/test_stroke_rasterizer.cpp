// Tests for the stroke rasterizer. Run: exit code 0 means all checks passed.

#include "stroke_rasterizer/stroke_rasterizer.hpp"

#include <cmath>
#include <cstdio>
#include <queue>
#include <utility>
#include <vector>

using stroke::Bitmap;
using stroke::CapStyle;
using stroke::JoinStyle;
using stroke::Point;
using stroke::StrokeOptions;

static int g_checks = 0;
static int g_failures = 0;

#define CHECK(cond)                                                     \
    do {                                                                \
        ++g_checks;                                                     \
        if (!(cond)) {                                                  \
            ++g_failures;                                               \
            std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond); \
        }                                                               \
    } while (0)

static StrokeOptions opts(double width, JoinStyle join,
                          CapStyle cap = CapStyle::kButt, double miterLimit = 4.0) {
    StrokeOptions o;
    o.width = width;
    o.join = join;
    o.cap = cap;
    o.miterLimit = miterLimit;
    return o;
}

[[maybe_unused]] static void dump(const Bitmap& bm) {
    for (int y = 0; y < bm.height(); ++y) {
        for (int x = 0; x < bm.width(); ++x) std::putchar(bm.get(x, y) ? '#' : '.');
        std::putchar('\n');
    }
}

// 8-connectivity check: every covered pixel must be reachable from the first
// one. A broken join would show up as an isolated island.
static bool isConnected8(const Bitmap& bm) {
    const auto px = bm.pixels();
    if (px.empty()) return true;
    std::vector<char> seen(static_cast<std::size_t>(bm.width()) * bm.height(), 0);
    auto idx = [&](int x, int y) { return y * bm.width() + x; };
    std::queue<std::pair<int, int>> q;
    q.push(px.front());
    seen[idx(px.front().first, px.front().second)] = 1;
    std::size_t reached = 0;
    while (!q.empty()) {
        const auto [x, y] = q.front();
        q.pop();
        ++reached;
        for (int dy = -1; dy <= 1; ++dy)
            for (int dx = -1; dx <= 1; ++dx) {
                if (dx == 0 && dy == 0) continue;
                const int nx = x + dx, ny = y + dy;
                if (bm.get(nx, ny) && !seen[idx(nx, ny)]) {
                    seen[idx(nx, ny)] = 1;
                    q.push({nx, ny});
                }
            }
    }
    return reached == px.size();
}

// Shared integrity checks for any stroked path:
//  1) no holes along the centerline (sampled points land on covered pixels),
//  2) the stroke is a single 8-connected component,
//  3) the pixel list contains no duplicates (no double counting).
static void checkStrokeIntegrity(const std::vector<Point>& path, const StrokeOptions& o,
                                 int w, int h) {
    Bitmap bm(w, h);
    stroke::rasterize(bm, path, o);
    for (std::size_t i = 0; i + 1 < path.size(); ++i) {
        const Point a = path[i], b = path[i + 1];
        const double len = stroke::length(b - a);
        if (len < 1.5) continue;
        for (double s = 0.75; s <= len - 0.75; s += 0.25) {
            const Point p = a + (b - a) * (s / len);
            CHECK(bm.get(static_cast<int>(std::floor(p.x)), static_cast<int>(std::floor(p.y))));
        }
    }
    CHECK(isConnected8(bm));
    const auto px = bm.pixels();
    CHECK(px.size() == bm.count());
    for (std::size_t i = 1; i < px.size(); ++i) CHECK(px[i] != px[i - 1]);
}

// ---------------------------------------------------------------- segments

static void testHorizontalButt() {
    Bitmap bm(16, 12);
    stroke::rasterize(bm, {{2, 5}, {12, 5}}, opts(3.0, JoinStyle::kMiter));
    CHECK(bm.count() == 40); // x in [2..11], y in {3,4,5,6}
    CHECK(bm.get(2, 3) && bm.get(11, 6));
    CHECK(!bm.get(1, 5) && !bm.get(12, 5));
    CHECK(!bm.get(5, 2) && !bm.get(5, 7));
}

static void testSquareCaps() {
    Bitmap bm(16, 12);
    stroke::rasterize(bm, {{2, 5}, {12, 5}}, opts(3.0, JoinStyle::kMiter, CapStyle::kSquare));
    CHECK(bm.count() == 56); // caps extend the line by half width on both ends
    CHECK(bm.get(0, 5) && bm.get(13, 5));
    CHECK(!bm.get(14, 5));
}

static void testRoundCaps() {
    Bitmap bm(16, 12);
    stroke::rasterize(bm, {{2, 5}, {12, 5}}, opts(3.0, JoinStyle::kMiter, CapStyle::kRound));
    CHECK(bm.count() == 44); // 40 + 2 extra pixels per round cap
    CHECK(bm.get(1, 4) && bm.get(1, 5));
    CHECK(!bm.get(1, 3) && !bm.get(0, 5));
}

// ------------------------------------------------------------------- joins

// 90-degree corner at (10.3, 10.3), half width 2. The miter reaches (12.3,
// 12.3); pixel (11,11) with center (11.5, 11.5) separates the join styles.
static void testMiterJoin90() {
    Bitmap bm(15, 15);
    stroke::rasterize(bm, {{2.3, 10.3}, {10.3, 10.3}, {10.3, 2.3}},
                      opts(4.0, JoinStyle::kMiter));
    CHECK(bm.get(11, 11)); // inside the miter quad
    CHECK(bm.get(10, 11));
    CHECK(bm.get(9, 9)); // inner side, covered by the segment rectangles
}

static void testBevelJoin90() {
    Bitmap bm(15, 15);
    stroke::rasterize(bm, {{2.3, 10.3}, {10.3, 10.3}, {10.3, 2.3}},
                      opts(4.0, JoinStyle::kBevel));
    CHECK(!bm.get(11, 11)); // cut off by the bevel edge x + y = 22.6
    CHECK(bm.get(10, 11));  // inside the bevel triangle
    CHECK(bm.get(9, 9));
}

static void testRoundJoin90() {
    Bitmap bm(15, 15);
    stroke::rasterize(bm, {{2.3, 10.3}, {10.3, 10.3}, {10.3, 2.3}},
                      opts(4.0, JoinStyle::kRound));
    CHECK(bm.get(11, 11)); // (11.5,11.5) is ~1.70 from the corner, radius 2
    CHECK(bm.get(9, 9));
}

static void testMiterLimitFallback90() {
    Bitmap bm(15, 15);
    // miter length is 2*sqrt(2) ~ 2.83 > 0.5 * 4, so it must fall back to bevel.
    stroke::rasterize(bm, {{2.3, 10.3}, {10.3, 10.3}, {10.3, 2.3}},
                      opts(4.0, JoinStyle::kMiter, CapStyle::kButt, 0.5));
    CHECK(!bm.get(11, 11));
    CHECK(bm.get(10, 11));
}

// Sharp V: (1,5) -> (11,5) -> (1,7). The miter spike reaches about (26.2, 3.5)
// with length ~15.2; pixel (20,4) is covered only when the miter is kept.
static void testSharpCornerMiterLimit() {
    const std::vector<Point> v{{1, 5}, {11, 5}, {1, 7}};
    {
        Bitmap bm(40, 12);
        stroke::rasterize(bm, v, opts(3.0, JoinStyle::kMiter, CapStyle::kButt, 10.0));
        CHECK(bm.get(20, 4));   // inside the miter spike
        CHECK(!bm.get(27, 3));  // beyond the miter point
    }
    {
        Bitmap bm(40, 12);
        stroke::rasterize(bm, v, opts(3.0, JoinStyle::kMiter, CapStyle::kButt, 1.0));
        CHECK(!bm.get(20, 4)); // limit 1 * 3 = 3 < 15.2: falls back to bevel
    }
    {
        Bitmap bm(40, 12);
        stroke::rasterize(bm, v, opts(3.0, JoinStyle::kBevel));
        CHECK(!bm.get(20, 4));
    }
    {
        Bitmap bm(40, 12);
        stroke::rasterize(bm, v, opts(3.0, JoinStyle::kRound));
        CHECK(!bm.get(20, 4)); // the round join only reaches radius 1.5
    }
}

// -------------------------------------------------------------- foldbacks

// (2,5) -> (12,5) -> (2,5): the stroke folds back on itself at (12,5).
static void testReversalRound() {
    Bitmap bm(16, 12);
    stroke::rasterize(bm, {{2, 5}, {12, 5}, {2, 5}}, opts(3.0, JoinStyle::kRound));
    CHECK(bm.count() == 42); // 40 rectangle + 2 semicircle pixels at x = 12
    CHECK(bm.get(12, 4) && bm.get(12, 5));
    CHECK(!bm.get(12, 3) && !bm.get(13, 5));
}

static void testReversalBevel() {
    Bitmap bm(16, 12);
    stroke::rasterize(bm, {{2, 5}, {12, 5}, {2, 5}}, opts(3.0, JoinStyle::kBevel));
    CHECK(bm.count() == 42); // bevel triangle covers the same two pixels here
    CHECK(bm.get(12, 4) && bm.get(12, 5));
    CHECK(!bm.get(13, 5));
}

static void testReversalMiterFallsBackToBevel() {
    Bitmap bm(16, 12);
    stroke::rasterize(bm, {{2, 5}, {12, 5}, {2, 5}}, opts(3.0, JoinStyle::kMiter));
    CHECK(bm.get(12, 4) && bm.get(12, 5));
    CHECK(!bm.get(13, 5));
}

// ------------------------------------------------------- zero-length input

static void testZeroLengthSegments() {
    // Duplicated points must not break joins or crash direction computation.
    Bitmap bm(16, 12);
    stroke::rasterize(bm, {{2, 5}, {2, 5}, {12, 5}, {12, 5}, {12, 9}},
                      opts(2.0, JoinStyle::kMiter));
    CHECK(bm.get(2, 4));   // horizontal part
    CHECK(bm.get(12, 8));  // vertical part
    CHECK(bm.get(12, 4));  // miter at the (12,5) corner
    CHECK(bm.count() > 0);
}

static void testDegeneratePointPath() {
    {
        Bitmap bm(12, 12);
        stroke::rasterize(bm, {{5, 5}, {5, 5}, {5, 5}},
                          opts(2.0, JoinStyle::kMiter, CapStyle::kRound));
        CHECK(bm.count() == 4); // disc of radius 1 -> pixels {4,5} x {4,5}
        CHECK(bm.get(5, 5));
        CHECK(!bm.get(6, 5));
    }
    {
        Bitmap bm(12, 12);
        stroke::rasterize(bm, {{5, 5}, {5, 5}}, opts(2.0, JoinStyle::kMiter, CapStyle::kSquare));
        CHECK(bm.count() == 4);
    }
    {
        Bitmap bm(12, 12);
        stroke::rasterize(bm, {{5, 5}, {5, 5}}, opts(2.0, JoinStyle::kMiter, CapStyle::kButt));
        CHECK(bm.count() == 0); // butt cap on a degenerate path draws nothing
    }
}

// ------------------------------------------------------- squared distances

static void testSquaredDistanceBoundary() {
    // Disc of radius 2.5 centered at (5, 4.5): pixel (3,2) has center
    // (3.5, 2.5), dx*dx + dy*dy = 2.25 + 4 = 6.25 = r*r exactly (inclusive).
    Bitmap bm(12, 12);
    stroke::rasterize(bm, {{5, 4.5}, {5, 4.5}}, opts(5.0, JoinStyle::kMiter, CapStyle::kRound));
    CHECK(bm.get(3, 2));
    CHECK(bm.get(6, 6));  // (6.5,6.5): 1.5^2 + 2^2 = 6.25, also on the boundary
    CHECK(!bm.get(3, 1)); // 2.25 + 9 = 11.25 > 6.25
    CHECK(!bm.get(7, 6)); // 6.25 + 4 = 10.25 > 6.25
}

// ------------------------------------------------- no holes, no duplicates

static void testNoDoubleCountOnOverlap() {
    // The folded path rasterizes the same rectangle twice; the bitmap must
    // still report each pixel exactly once.
    Bitmap bm(16, 12);
    stroke::rasterize(bm, {{2, 5}, {12, 5}, {2, 5}}, opts(3.0, JoinStyle::kRound));
    const auto px = bm.pixels();
    CHECK(px.size() == bm.count());
    for (std::size_t i = 1; i < px.size(); ++i) CHECK(px[i] != px[i - 1]);
}

static void testIntegrityZigzag() {
    const std::vector<Point> path{{2, 2}, {18, 3}, {3, 9}, {17, 12}};
    for (JoinStyle join : {JoinStyle::kMiter, JoinStyle::kRound, JoinStyle::kBevel})
        checkStrokeIntegrity(path, opts(3.0, join), 24, 16);
}

static void testIntegrityReversal() {
    const std::vector<Point> path{{2, 8}, {18, 8}, {2, 8}};
    for (JoinStyle join : {JoinStyle::kMiter, JoinStyle::kRound, JoinStyle::kBevel})
        checkStrokeIntegrity(path, opts(3.0, join), 24, 16);
}

static void testIntegritySharpCorner() {
    // Sharp V whose miter (~17.6) exceeds the limit, exercising the bevel
    // fallback on a real corner.
    const std::vector<Point> path{{1, 5}, {20, 5}, {2, 8}};
    for (JoinStyle join : {JoinStyle::kMiter, JoinStyle::kRound, JoinStyle::kBevel})
        checkStrokeIntegrity(path, opts(3.0, join), 24, 16);
}

static void testIntegrityZeroLengthAndCaps() {
    const std::vector<Point> path{{2, 2}, {2, 2}, {18, 3}, {18, 3}, {4, 12}};
    for (JoinStyle join : {JoinStyle::kMiter, JoinStyle::kRound, JoinStyle::kBevel})
        for (CapStyle cap : {CapStyle::kButt, CapStyle::kSquare, CapStyle::kRound})
            checkStrokeIntegrity(path, opts(3.0, join, cap), 24, 16);
}

int main() {
    testHorizontalButt();
    testSquareCaps();
    testRoundCaps();
    testMiterJoin90();
    testBevelJoin90();
    testRoundJoin90();
    testMiterLimitFallback90();
    testSharpCornerMiterLimit();
    testReversalRound();
    testReversalBevel();
    testReversalMiterFallsBackToBevel();
    testZeroLengthSegments();
    testDegeneratePointPath();
    testSquaredDistanceBoundary();
    testNoDoubleCountOnOverlap();
    testIntegrityZigzag();
    testIntegrityReversal();
    testIntegritySharpCorner();
    testIntegrityZeroLengthAndCaps();

    std::printf("%d checks, %d failures\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}
