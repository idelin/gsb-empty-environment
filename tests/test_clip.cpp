// Tests for exact convex polygon clipping.
#include <cstdio>
#include <vector>
#include "exactclip/clip.hpp"

using namespace exactclip;

static int g_failures = 0;
static int g_checks = 0;

#define CHECK(cond)                                                        \
    do {                                                                   \
        ++g_checks;                                                        \
        if (!(cond)) {                                                     \
            ++g_failures;                                                  \
            std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);   \
        }                                                                  \
    } while (0)

static Point pt(long long x, long long y) { return Point{Rational(x), Rational(y)}; }
static Point ptq(long long xn, long long xd, long long yn, long long yd) {
    return Point{Rational(xn, xd), Rational(yn, yd)};
}

static std::vector<Point> reversed(std::vector<Point> p) {
    std::reverse(p.begin(), p.end());
    return p;
}

// Independent verification: the result must satisfy the half-planes of both
// input polygons, and Empty results must come with a separating axis.
static void verifyResult(const ClipResult& res, const std::vector<Point>& rawA,
                         const std::vector<Point>& rawB) {
    std::vector<Point> a = normalizePolygon(rawA);
    std::vector<Point> b = normalizePolygon(rawB);
    switch (res.kind) {
    case Kind::Polygon: {
        CHECK(res.vertices.size() >= 3);
        CHECK(strictlyConvexCCW(res.vertices));
        CHECK(polygonArea2(res.vertices).sign() > 0);
        for (const Point& p : res.vertices) {
            CHECK(onOrInside(a, p));
            CHECK(onOrInside(b, p));
        }
        break;
    }
    case Kind::Segment:
        CHECK(res.vertices.size() == 2);
        CHECK(res.vertices[0] != res.vertices[1]);
        for (const Point& p : res.vertices) {
            CHECK(onOrInside(a, p));
            CHECK(onOrInside(b, p));
        }
        break;
    case Kind::Point:
        CHECK(res.vertices.size() == 1);
        CHECK(onOrInside(a, res.vertices[0]));
        CHECK(onOrInside(b, res.vertices[0]));
        break;
    case Kind::Empty:
        CHECK(res.vertices.empty());
        if (a.size() >= 3 && b.size() >= 3) CHECK(separated(a, b));
        break;
    }
}

static void test_shared_edge() {
    std::vector<Point> a = {pt(0,0), pt(1,0), pt(1,1), pt(0,1)};
    std::vector<Point> b = {pt(1,0), pt(2,0), pt(2,1), pt(1,1)};
    ClipResult r = clipConvex(a, b);
    CHECK(r.kind == Kind::Segment);
    CHECK(r.vertices.size() == 2);
    bool found = (r.vertices[0] == pt(1,0) && r.vertices[1] == pt(1,1)) ||
                 (r.vertices[0] == pt(1,1) && r.vertices[1] == pt(1,0));
    CHECK(found);
    verifyResult(r, a, b);
}

static void test_containment() {
    std::vector<Point> a = {pt(0,0), pt(10,0), pt(10,10), pt(0,10)};
    std::vector<Point> b = {pt(2,2), pt(3,2), pt(3,3), pt(2,3)};
    ClipResult r = clipConvex(a, b);
    CHECK(r.kind == Kind::Polygon);
    CHECK(r.vertices.size() == 4);
    CHECK(r.vertices == b); // normalized CCW, lexicomin first
    CHECK(polygonArea2(r.vertices) == Rational(2));
    verifyResult(r, a, b);
}

static void test_reversed_input() {
    std::vector<Point> a = {pt(0,0), pt(10,0), pt(10,10), pt(0,10)};
    std::vector<Point> b = {pt(2,2), pt(4,2), pt(4,4), pt(2,4)};
    ClipResult forward = clipConvex(a, b);
    ClipResult bothCW = clipConvex(reversed(a), reversed(b));
    ClipResult mixed = clipConvex(reversed(a), b);
    CHECK(forward.kind == Kind::Polygon);
    CHECK(bothCW.kind == Kind::Polygon);
    CHECK(mixed.kind == Kind::Polygon);
    CHECK(forward.vertices == bothCW.vertices);
    CHECK(forward.vertices == mixed.vertices);
    verifyResult(bothCW, reversed(a), reversed(b));
}

static void test_point_touch() {
    std::vector<Point> a = {pt(0,0), pt(1,0), pt(1,1), pt(0,1)};
    std::vector<Point> b = {pt(1,1), pt(2,1), pt(2,2), pt(1,2)};
    ClipResult r = clipConvex(a, b);
    CHECK(r.kind == Kind::Point);
    CHECK(r.vertices.size() == 1);
    CHECK(r.vertices[0] == pt(1,1));
    verifyResult(r, a, b);
}

static void test_disjoint() {
    std::vector<Point> a = {pt(0,0), pt(1,0), pt(1,1), pt(0,1)};
    std::vector<Point> b = {pt(3,0), pt(4,0), pt(4,1), pt(3,1)};
    ClipResult r = clipConvex(a, b);
    CHECK(r.kind == Kind::Empty);
    verifyResult(r, a, b);
}

static void test_duplicates_and_collinear_input() {
    std::vector<Point> clean = {pt(0,0), pt(4,0), pt(4,4), pt(0,4)};
    std::vector<Point> messy = {
        pt(0,0), pt(0,0), pt(2,0), pt(4,0), pt(4,0),
        pt(4,2), pt(4,4), pt(2,4), pt(0,4), pt(0,2), pt(0,0),
    };
    std::vector<Point> inner = {pt(1,1), pt(3,1), pt(3,3), pt(1,3)};
    ClipResult rClean = clipConvex(clean, inner);
    ClipResult rMessy = clipConvex(messy, inner);
    CHECK(rClean.kind == Kind::Polygon);
    CHECK(rMessy.kind == Kind::Polygon);
    CHECK(rClean.vertices == rMessy.vertices);
    // Normalization of the messy polygon itself.
    CHECK(normalizePolygon(messy) == clean);
    verifyResult(rMessy, messy, inner);
}

static void test_nearly_parallel_long_edges() {
    // Unit-height strip of length L clipped by a razor-thin triangle whose
    // lower edge has slope -2/L^2: almost parallel to the strip's top edge,
    // crossing it at x = L/2 (a genuine rational intersection).
    const long long L = 999999;
    std::vector<Point> strip = {pt(0,0), pt(L,0), pt(L,1), pt(0,1)};
    std::vector<Point> tri = {ptq(0,1, L + 1, L), ptq(L,1, L - 1, L), ptq(L,1, 3,1)};
    ClipResult r = clipConvex(strip, tri);
    CHECK(r.kind == Kind::Polygon);
    CHECK(r.vertices.size() == 3);
    // Sliver vertices: (L/2, 1), (L, 1 - 1/L), (L, 1).
    CHECK(r.vertices[0] == ptq(L,2, 1,1));
    CHECK(r.vertices[1] == ptq(L,1, L - 1, L));
    CHECK(r.vertices[2] == pt(L,1));
    // Exact area: base 1/L, height L/2 -> area2 == 1/2. A float epsilon
    // would swallow this sliver; exact arithmetic keeps it.
    CHECK(polygonArea2(r.vertices) == Rational(1,2));
    verifyResult(r, strip, tri);
    // Reversed roles and orientations give the identical sliver.
    ClipResult r2 = clipConvex(reversed(tri), reversed(strip));
    CHECK(r2.kind == Kind::Polygon);
    CHECK(r2.vertices == r.vertices);
}

static void test_rational_intersection() {
    // Square [0,2]^2 clipped by a 45-degree rotated square of "radius" 3/2
    // centered at (1,1): intersection is an octagon with half-integer
    // vertices and exact area2 == 7.
    std::vector<Point> sq = {pt(0,0), pt(2,0), pt(2,2), pt(0,2)};
    std::vector<Point> dia = {ptq(1,1, -1,2), ptq(5,2, 1,1), ptq(1,1, 5,2), ptq(-1,2, 1,1)};
    ClipResult r = clipConvex(sq, dia);
    CHECK(r.kind == Kind::Polygon);
    CHECK(r.vertices.size() == 8);
    CHECK(polygonArea2(r.vertices) == Rational(7));
    CHECK(r.vertices[0] == ptq(0,1, 1,2)); // lexicomin vertex
    verifyResult(r, sq, dia);
}

static void test_identical_polygons() {
    std::vector<Point> a = {pt(0,0), pt(5,0), pt(5,5), pt(0,5)};
    ClipResult r = clipConvex(a, a);
    CHECK(r.kind == Kind::Polygon);
    CHECK(r.vertices == a);
    verifyResult(r, a, a);
}

static void test_shared_partial_edge() {
    // Overlap along part of one edge only.
    std::vector<Point> a = {pt(0,0), pt(4,0), pt(4,4), pt(0,4)};
    std::vector<Point> b = {pt(4,1), pt(6,1), pt(6,3), pt(4,3)};
    ClipResult r = clipConvex(a, b);
    CHECK(r.kind == Kind::Segment);
    CHECK(r.vertices.size() == 2);
    bool found = (r.vertices[0] == pt(4,1) && r.vertices[1] == pt(4,3)) ||
                 (r.vertices[0] == pt(4,3) && r.vertices[1] == pt(4,1));
    CHECK(found);
    verifyResult(r, a, b);
}

static void test_big_coordinates() {
    // Large magnitudes that would overflow 64-bit intermediates.
    const long long B = 4000000000LL; // > 2^32
    std::vector<Point> a = {pt(0,0), pt(B,0), pt(B,B), pt(0,B)};
    std::vector<Point> b = {pt(B/2, B/2), pt(2*B, B/2), pt(2*B, 2*B), pt(B/2, 2*B)};
    ClipResult r = clipConvex(a, b);
    CHECK(r.kind == Kind::Polygon);
    CHECK(r.vertices.size() == 4);
    CHECK(polygonArea2(r.vertices) == Rational(B/2) * Rational(B/2) * Rational(2));
    verifyResult(r, a, b);
}

int main() {
    test_shared_edge();
    test_containment();
    test_reversed_input();
    test_point_touch();
    test_disjoint();
    test_duplicates_and_collinear_input();
    test_nearly_parallel_long_edges();
    test_rational_intersection();
    test_identical_polygons();
    test_shared_partial_edge();
    test_big_coordinates();
    std::printf("%d checks, %d failures\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}
