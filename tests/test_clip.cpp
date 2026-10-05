// Tests for exact convex polygon clipping.
#include "exact/clip.hpp"
#include <algorithm>
#include <iostream>

using namespace exact;

static int failures = 0;
static int checks = 0;

#define CHECK(cond) do { ++checks; if (!(cond)) { \
    ++failures; std::cerr << "FAIL line " << __LINE__ << ": " #cond "\n"; } } while (0)

static Point P(long long x, long long y) { return Point(x, y); }
static Point PR(const char* x, const char* y) {
    return Point(Rational::fromString(x), Rational::fromString(y));
}

static bool hasVert(const ClipResult& r, const Point& p) {
    return std::find(r.verts.begin(), r.verts.end(), p) != r.verts.end();
}

// Independent verification of a clip result against both input polygons:
//  - every output vertex satisfies all half-planes of BOTH inputs;
//  - polygon outputs are normalized (CCW, no duplicate adjacent, no collinear).
static void verify(const ClipResult& r, std::vector<Point> A, std::vector<Point> B) {
    CHECK(satisfiesHalfPlanes(r.verts, A));
    CHECK(satisfiesHalfPlanes(r.verts, B));
    if (r.kind == ClipKind::Polygon) {
        CHECK(r.verts.size() >= 3);
        CHECK(area2(r.verts).sign() > 0); // CCW
        size_t n = r.verts.size();
        for (size_t i = 0; i < n; ++i) {
            CHECK(r.verts[i] != r.verts[(i + 1) % n]);
            CHECK(orient(r.verts[i], r.verts[(i + 1) % n], r.verts[(i + 2) % n]) != 0);
        }
    } else if (r.kind == ClipKind::Segment) {
        CHECK(r.verts.size() == 2);
        CHECK(r.verts[0] != r.verts[1]);
    } else if (r.kind == ClipKind::Point) {
        CHECK(r.verts.size() == 1);
    } else {
        CHECK(r.verts.empty());
    }
}

static void testOverlapSquares() {
    std::vector<Point> A = {P(0,0), P(4,0), P(4,4), P(0,4)};
    std::vector<Point> B = {P(2,2), P(6,2), P(6,6), P(2,6)};
    ClipResult r = clipConvex(A, B);
    CHECK(r.kind == ClipKind::Polygon);
    CHECK(r.verts.size() == 4);
    CHECK(hasVert(r, P(2,2)) && hasVert(r, P(4,2)) && hasVert(r, P(4,4)) && hasVert(r, P(2,4)));
    CHECK(r.area() == Rational(4));
    verify(r, A, B);
}

static void testSharedEdge() {
    // Full shared edge -> Segment.
    std::vector<Point> A = {P(0,0), P(2,0), P(2,2), P(0,2)};
    std::vector<Point> B = {P(2,0), P(4,0), P(4,2), P(2,2)};
    ClipResult r = clipConvex(A, B);
    CHECK(r.kind == ClipKind::Segment);
    CHECK(hasVert(r, P(2,0)) && hasVert(r, P(2,2)));
    verify(r, A, B);

    // Partially overlapping collinear edges -> shorter Segment.
    std::vector<Point> C = {P(0,0), P(4,0), P(4,2), P(0,2)};
    std::vector<Point> D = {P(2,2), P(6,2), P(6,4), P(2,4)};
    ClipResult r2 = clipConvex(C, D);
    CHECK(r2.kind == ClipKind::Segment);
    CHECK(hasVert(r2, P(2,2)) && hasVert(r2, P(4,2)));
    verify(r2, C, D);
}

static void testContainment() {
    std::vector<Point> A = {P(0,0), P(10,0), P(10,10), P(0,10)};
    std::vector<Point> B = {P(3,3), P(5,3), P(5,4), P(3,4)};
    ClipResult r = clipConvex(A, B);
    CHECK(r.kind == ClipKind::Polygon);
    CHECK(r.verts.size() == 4);
    CHECK(r.area() == Rational(2));
    for (auto& p : B) CHECK(hasVert(r, p));
    verify(r, A, B);

    // Reverse roles: same result.
    ClipResult r2 = clipConvex(B, A);
    CHECK(r2.kind == ClipKind::Polygon);
    CHECK(r2.verts == r.verts);
    verify(r2, B, A);
}

static void testReversedInput() {
    std::vector<Point> A = {P(0,0), P(4,0), P(4,4), P(0,4)};
    std::vector<Point> B = {P(2,2), P(6,2), P(6,6), P(2,6)};
    ClipResult ref = clipConvex(A, B);

    std::vector<Point> Ar(A.rbegin(), A.rend()); // clockwise
    std::vector<Point> Br(B.rbegin(), B.rend());
    ClipResult r1 = clipConvex(Ar, B);
    ClipResult r2 = clipConvex(A, Br);
    ClipResult r3 = clipConvex(Br, Ar);
    CHECK(r1.kind == ClipKind::Polygon && r1.verts == ref.verts);
    CHECK(r2.kind == ClipKind::Polygon && r2.verts == ref.verts);
    CHECK(r3.kind == ClipKind::Polygon && r3.verts == ref.verts);
    verify(r1, Ar, B);
    verify(r2, A, Br);
    verify(r3, Br, Ar);
}

static void testDuplicateAndCollinearInput() {
    // Same square as {0,0 4,0 4,4 0,4} but with duplicate adjacent vertices
    // and collinear points along edges.
    std::vector<Point> A = {P(0,0), P(0,0), P(2,0), P(4,0), P(4,0), P(4,2),
                            P(4,4), P(2,4), P(0,4), P(0,2), P(0,0), P(0,0)};
    std::vector<Point> B = {P(2,2), P(6,2), P(6,6), P(2,6)};
    ClipResult r = clipConvex(A, B);
    CHECK(r.kind == ClipKind::Polygon);
    CHECK(r.verts.size() == 4);
    CHECK(r.area() == Rational(4));
    CHECK(hasVert(r, P(2,2)) && hasVert(r, P(4,2)) && hasVert(r, P(4,4)) && hasVert(r, P(2,4)));
    verify(r, A, B);
}

static void testPointTouch() {
    std::vector<Point> A = {P(0,0), P(1,0), P(1,1), P(0,1)};
    std::vector<Point> B = {P(1,1), P(2,1), P(2,2), P(1,2)};
    ClipResult r = clipConvex(A, B);
    CHECK(r.kind == ClipKind::Point);
    CHECK(r.verts[0] == P(1,1));
    verify(r, A, B);
}

static void testDisjoint() {
    std::vector<Point> A = {P(0,0), P(1,0), P(1,1), P(0,1)};
    std::vector<Point> B = {P(3,3), P(4,3), P(4,4), P(3,4)};
    ClipResult r = clipConvex(A, B);
    CHECK(r.kind == ClipKind::Empty);
    verify(r, A, B);
}

static void testNearlyParallelLongEdges() {
    // Unit-height strip of length N vs. the same strip tilted by a tiny
    // rational slope. The clipped-off triangle has area 1/(2*10^6): far below
    // any reasonable floating-point epsilon relative to coordinates ~10^6.
    // Intersection: (0,0), (N,eps), (N,1), (0,1) -- (N,0) lies below B.
    const long long N = 1000000;
    Rational eps(BigInt(1), BigInt::fromString("1000000000000")); // 1e-12
    std::vector<Point> A = {P(0,0), P(N,0), P(N,1), P(0,1)};
    std::vector<Point> B = {Point(Rational(0), Rational(0)),
                            Point(Rational(N), eps),
                            Point(Rational(N), Rational(1) + eps),
                            Point(Rational(0), Rational(1))};
    ClipResult r = clipConvex(A, B);
    CHECK(r.kind == ClipKind::Polygon);
    CHECK(r.verts.size() == 4);
    CHECK(hasVert(r, P(0,0)) && hasVert(r, P(0,1)) && hasVert(r, P(N,1)));
    CHECK(hasVert(r, Point(Rational(N), eps)));
    Rational expected = Rational(N) - Rational(N) * eps / Rational(2);
    CHECK(r.area() == expected); // 999999.9999995 exactly
    verify(r, A, B);

    // Same with roles swapped and orientation reversed.
    std::vector<Point> Ar(A.rbegin(), A.rend());
    ClipResult r2 = clipConvex(B, Ar);
    CHECK(r2.kind == ClipKind::Polygon);
    CHECK(r2.area() == expected);
    verify(r2, B, Ar);
}

static void testTinyAreaNotSwallowed() {
    // Intersection is a triangle of area 1/(2*10^18): an implementation using
    // floating-point thresholds would report Empty here.
    Rational tiny(BigInt(1), BigInt::fromString("1000000000000000000")); // 1e-18
    std::vector<Point> A = {P(0,0), P(2,0), P(0,2)};
    std::vector<Point> B = {P(1,0), P(2,0), Point(Rational(1), tiny)};
    ClipResult r = clipConvex(A, B);
    CHECK(r.kind == ClipKind::Polygon);
    CHECK(r.verts.size() == 3);
    CHECK(r.area() == tiny / Rational(2));
    verify(r, A, B);
}

static void testRationalVertices() {
    // Intersection points land on non-integer rationals.
    std::vector<Point> A = {PR("0","0"), PR("1","0"), PR("1","1"), PR("0","1")};
    std::vector<Point> B = {PR("1/3","1/7"), PR("5/6","1/7"), PR("5/6","11/13"), PR("1/3","11/13")};
    ClipResult r = clipConvex(A, B);
    CHECK(r.kind == ClipKind::Polygon);
    CHECK(r.verts == B); // B is contained in A and already normalized CCW
    verify(r, A, B);

    // Crossing edges produce exact rational intersections.
    std::vector<Point> T1 = {P(0,0), P(3,0), P(0,3)};
    std::vector<Point> T2 = {P(1,0), P(4,0), P(1,3)};
    ClipResult r2 = clipConvex(T1, T2);
    CHECK(r2.kind == ClipKind::Polygon);
    // T1: x+y<=3; T2: x>=1, y>=0, x+3y... hypotenuse from (1,0) to ... use checks:
    CHECK(hasVert(r2, P(1,0)));
    CHECK(hasVert(r2, P(3,0)));
    CHECK(hasVert(r2, PR("1","2")));        // x=1 meets x+y=3
    verify(r2, T1, T2);
    // Area: triangle (1,0),(3,0),(1,2) = 2.
    CHECK(r2.area() == Rational(2));
}

static void testDegenerateInputs() {
    std::vector<Point> A = {P(0,0), P(4,0), P(4,4), P(0,4)};

    // Point input inside / outside.
    ClipResult r1 = clipConvex({P(2,2)}, A);
    CHECK(r1.kind == ClipKind::Point && r1.verts[0] == P(2,2));
    ClipResult r2 = clipConvex({P(9,9)}, A);
    CHECK(r2.kind == ClipKind::Empty);

    // Segment input crossing the square.
    ClipResult r3 = clipConvex({P(-1,2), P(5,2)}, A);
    CHECK(r3.kind == ClipKind::Segment);
    CHECK(hasVert(r3, P(0,2)) && hasVert(r3, P(4,2)));
    verify(r3, {P(-1,2), P(5,2)}, A);

    // Segment vs. segment crossing at a rational point.
    ClipResult r4 = clipConvex({P(0,0), P(2,2)}, {P(0,2), P(2,0)});
    CHECK(r4.kind == ClipKind::Point && r4.verts[0] == P(1,1));

    // Collinear overlapping segments.
    ClipResult r5 = clipConvex({P(0,0), P(4,0)}, {P(2,0), P(6,0)});
    CHECK(r5.kind == ClipKind::Segment);
    CHECK(hasVert(r5, P(2,0)) && hasVert(r5, P(4,0)));
}

int main() {
    testOverlapSquares();
    testSharedEdge();
    testContainment();
    testReversedInput();
    testDuplicateAndCollinearInput();
    testPointTouch();
    testDisjoint();
    testNearlyParallelLongEdges();
    testTinyAreaNotSwallowed();
    testRationalVertices();
    testDegenerateInputs();

    std::cout << (failures == 0 ? "ALL PASS" : "FAILURES") << ": "
              << (checks - failures) << "/" << checks << " checks passed\n";
    return failures == 0 ? 0 : 1;
}
