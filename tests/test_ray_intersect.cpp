#include <iostream>
#include <string>
#include <vector>

#include "raypoly/ray_intersect.hpp"

using namespace raypoly;

static int failures = 0;

#define CHECK(cond)                                                                            \
    do {                                                                                       \
        if (!(cond)) {                                                                         \
            std::cout << "FAIL " << __FILE__ << ":" << __LINE__ << ": " #cond "\n";            \
            ++failures;                                                                        \
        }                                                                                      \
    } while (0)

static Rational R(long long n) { return Rational(n); }
static Rational R(long long n, long long d) { return Rational(n, d); }
static Point P(long long x, long long y) { return Point{R(x), R(y)}; }

static Polygon square(long long lo, long long hi) {
    return Polygon{{P(lo, lo), P(hi, lo), P(hi, hi), P(lo, hi)}, {}};
}

static bool has_edge(const std::vector<EdgeRef>& refs, std::size_t ring, std::size_t edge) {
    for (const auto& r : refs)
        if (r.ring == ring && r.edge == edge) return true;
    return false;
}

static const BoundaryPiece* boundary_at(const RayIntersection& ri, const Rational& t) {
    for (const auto& bp : ri.boundary)
        if (bp.lo == t && bp.hi == t) return &bp;
    return nullptr;
}

static const BoundaryPiece* boundary_span(const RayIntersection& ri, const Rational& lo,
                                          const Rational& hi) {
    for (const auto& bp : ri.boundary)
        if (bp.lo == lo && bp.hi == hi) return &bp;
    return nullptr;
}

// Geometry-only fingerprint (ignores edge indices, which legitimately change
// when ring vertex order is reversed).
static std::string fingerprint(const RayIntersection& ri) {
    std::string out;
    for (const auto& iv : ri.interior) {
        out += "I(" + iv.lo.to_string() + (iv.lo_closed ? ",closed" : ",open");
        out += iv.hi_infinite ? ";inf)" : ";" + iv.hi.to_string() + (iv.hi_closed ? ",closed)" : ",open)");
    }
    for (const auto& bp : ri.boundary)
        out += "B[" + bp.lo.to_string() + "," + bp.hi.to_string() + "]";
    return out;
}

static void test_basic_crossing() {
    Polygon poly = square(0, 4);
    Ray ray{P(-1, 2), P(1, 0)};
    RayIntersection ri = intersect(ray, poly);
    CHECK(ri.interior.size() == 1);
    CHECK(ri.interior[0].lo == R(1) && ri.interior[0].hi == R(5));
    CHECK(!ri.interior[0].lo_closed && !ri.interior[0].hi_closed);
    CHECK(!ri.interior[0].hi_infinite);
    CHECK(ri.boundary.size() == 2);
    // Left edge is edge 3 ((0,4)->(0,0)), right edge is edge 1.
    CHECK(has_edge(ri.interior[0].lo_edges, 0, 3));
    CHECK(has_edge(ri.interior[0].hi_edges, 0, 1));
    CHECK(boundary_at(ri, R(1)) != nullptr);
    CHECK(boundary_at(ri, R(5)) != nullptr);
}

static void test_horizontal_edge_perpendicular() {
    Polygon poly = square(0, 4);
    Ray ray{P(2, -1), P(0, 1)}; // crosses both horizontal edges
    RayIntersection ri = intersect(ray, poly);
    CHECK(ri.interior.size() == 1);
    CHECK(ri.interior[0].lo == R(1) && ri.interior[0].hi == R(5));
    CHECK(has_edge(ri.interior[0].lo_edges, 0, 0)); // bottom edge (0,0)->(4,0)
    CHECK(has_edge(ri.interior[0].hi_edges, 0, 2)); // top edge (4,4)->(0,4)
}

static void test_collinear_overlap_outer() {
    Polygon poly = square(0, 4);
    Ray ray{P(-1, 0), P(1, 0)}; // travels along the bottom edge
    RayIntersection ri = intersect(ray, poly);
    CHECK(ri.interior.empty());
    CHECK(ri.boundary.size() == 1);
    const BoundaryPiece* bp = boundary_span(ri, R(1), R(5));
    CHECK(bp != nullptr);
    if (bp) {
        bool saw_overlap = false;
        for (const auto& e : bp->edges)
            if (e.overlap && e.ring == 0 && e.edge == 0) saw_overlap = true;
        CHECK(saw_overlap);
        // Vertices (0,0) and (4,0) are shared with edges 3 and 1; their hits
        // merge into the same piece instead of creating new parameters.
        CHECK(has_edge(bp->edges, 0, 3));
        CHECK(has_edge(bp->edges, 0, 1));
    }
}

static void test_vertex_counted_once() {
    Polygon poly = square(0, 4);
    Ray ray{P(-1, -1), P(1, 1)}; // through corners (0,0) and (4,4)
    RayIntersection ri = intersect(ray, poly);
    CHECK(ri.interior.size() == 1);
    CHECK(ri.interior[0].lo == R(1) && ri.interior[0].hi == R(5));
    CHECK(ri.boundary.size() == 2); // exactly one piece per corner
    const BoundaryPiece* enter = boundary_at(ri, R(1));
    const BoundaryPiece* exit = boundary_at(ri, R(5));
    CHECK(enter != nullptr && exit != nullptr);
    if (enter) { // both adjacent edges recorded as evidence, single t
        CHECK(has_edge(enter->edges, 0, 0));
        CHECK(has_edge(enter->edges, 0, 3));
    }
    if (exit) {
        CHECK(has_edge(exit->edges, 0, 1));
        CHECK(has_edge(exit->edges, 0, 2));
    }
}

static Polygon square_with_diamond_hole() {
    Polygon poly = square(0, 10);
    poly.holes.push_back({P(5, 3), P(7, 5), P(5, 7), P(3, 5)});
    return poly;
}

static void test_hole_vertex_crossing() {
    Polygon poly = square_with_diamond_hole();
    Ray ray{P(-1, 5), P(1, 0)}; // through hole vertices (3,5) and (7,5)
    RayIntersection ri = intersect(ray, poly);
    CHECK(ri.interior.size() == 2);
    CHECK(ri.interior[0].lo == R(1) && ri.interior[0].hi == R(4));
    CHECK(ri.interior[1].lo == R(8) && ri.interior[1].hi == R(11));
    CHECK(ri.boundary.size() == 4);
    const BoundaryPiece* v = boundary_at(ri, R(4)); // vertex (3,5): one piece
    CHECK(v != nullptr);
    if (v) {
        CHECK(has_edge(v->edges, 1, 2)); // hole edge (5,7)->(3,5)
        CHECK(has_edge(v->edges, 1, 3)); // hole edge (3,5)->(5,3)
    }
}

static void test_hole_tangency() {
    Polygon poly = square_with_diamond_hole();
    Ray ray{P(-1, 7), P(1, 0)}; // tangent to hole vertex (5,7)
    RayIntersection ri = intersect(ray, poly);
    // Tangency does not toggle parity: interior on both sides, split only by
    // the boundary point itself.
    CHECK(ri.interior.size() == 2);
    CHECK(ri.interior[0].lo == R(1) && ri.interior[0].hi == R(6));
    CHECK(ri.interior[1].lo == R(6) && ri.interior[1].hi == R(11));
    const BoundaryPiece* v = boundary_at(ri, R(6));
    CHECK(v != nullptr);
    if (v) {
        CHECK(has_edge(v->edges, 1, 1)); // hole edge (7,5)->(5,7)
        CHECK(has_edge(v->edges, 1, 2)); // hole edge (5,7)->(3,5)
    }
}

static void test_hole_collinear_overlap() {
    Polygon poly = square(0, 10);
    poly.holes.push_back({P(4, 4), P(6, 4), P(6, 6), P(4, 6)});
    Ray ray{P(-1, 6), P(1, 0)}; // runs along the hole's top edge
    RayIntersection ri = intersect(ray, poly);
    CHECK(ri.interior.size() == 2);
    CHECK(ri.interior[0].lo == R(1) && ri.interior[0].hi == R(5));
    CHECK(ri.interior[1].lo == R(7) && ri.interior[1].hi == R(11));
    const BoundaryPiece* bp = boundary_span(ri, R(5), R(7));
    CHECK(bp != nullptr);
    if (bp) {
        bool saw_overlap = false;
        for (const auto& e : bp->edges)
            if (e.overlap && e.ring == 1) saw_overlap = true;
        CHECK(saw_overlap);
    }
    CHECK(boundary_at(ri, R(1)) != nullptr);
    CHECK(boundary_at(ri, R(11)) != nullptr);
}

static void test_ray_starts_inside() {
    Polygon poly = square(0, 4);
    Ray ray{P(2, 2), P(1, 0)};
    RayIntersection ri = intersect(ray, poly);
    CHECK(ri.interior.size() == 1);
    CHECK(ri.interior[0].lo == R(0) && ri.interior[0].lo_closed);
    CHECK(ri.interior[0].hi == R(2) && !ri.interior[0].hi_closed);
    CHECK(ri.interior[0].lo_edges.empty());
    CHECK(ri.boundary.size() == 1);
}

static void test_rational_direction_exact() {
    Polygon poly = square(0, 4);
    Ray ray{P(-3, -1), P(2, 1)}; // slope 1/2
    RayIntersection ri = intersect(ray, poly);
    CHECK(ri.interior.size() == 1);
    CHECK(ri.interior[0].lo == R(3, 2)); // enters at (0, 1/2)
    CHECK(ri.interior[0].hi == R(7, 2)); // exits at (4, 5/2)
    // Evidence edge parameters are exact rationals too.
    CHECK(ri.interior[0].lo_edges.size() == 1);
    if (ri.interior[0].lo_edges.size() == 1)
        CHECK(ri.interior[0].lo_edges[0].s0 == R(7, 8));
}

static void test_miss_and_pointing_away() {
    Polygon poly = square(0, 4);
    Ray miss{P(-1, 5), P(1, 0)};
    RayIntersection a = intersect(miss, poly);
    CHECK(a.interior.empty() && a.boundary.empty());
    Ray away{P(10, 2), P(1, 0)};
    RayIntersection b = intersect(away, poly);
    CHECK(b.interior.empty() && b.boundary.empty());
}

static Polygon reversed_polygon(const Polygon& poly) {
    Polygon out;
    out.outer.assign(poly.outer.rbegin(), poly.outer.rend());
    for (const auto& h : poly.holes) out.holes.emplace_back(h.rbegin(), h.rend());
    return out;
}

static void test_reversed_vertex_order() {
    std::vector<Polygon> polys;
    polys.push_back(square(0, 4));
    polys.push_back(square_with_diamond_hole());
    Polygon overlap_poly = square(0, 10);
    overlap_poly.holes.push_back({P(4, 4), P(6, 4), P(6, 6), P(4, 6)});
    polys.push_back(overlap_poly);

    std::vector<Ray> rays = {
        {P(-1, 2), P(1, 0)},   {P(-1, -1), P(1, 1)}, {P(-1, 5), P(1, 0)},
        {P(-1, 7), P(1, 0)},   {P(-1, 6), P(1, 0)},  {P(2, 2), P(1, 0)},
        {P(-3, -1), P(2, 1)},  {P(2, -1), P(0, 1)},
    };
    for (const auto& poly : polys) {
        Polygon rev = reversed_polygon(poly);
        for (const auto& ray : rays) {
            std::string fwd = fingerprint(intersect(ray, poly));
            std::string bwd = fingerprint(intersect(ray, rev));
            CHECK(fwd == bwd);
            if (fwd != bwd)
                std::cout << "  fwd=" << fwd << "\n  bwd=" << bwd << "\n";
        }
    }
}

static void test_locate() {
    Polygon poly = square_with_diamond_hole();
    CHECK(locate(P(1, 1), poly) == PointLocation::Inside);
    CHECK(locate(P(5, 5), poly) == PointLocation::Outside); // inside the hole
    CHECK(locate(P(0, 5), poly) == PointLocation::Boundary);
    CHECK(locate(P(5, 7), poly) == PointLocation::Boundary); // hole vertex
    CHECK(locate(P(20, 5), poly) == PointLocation::Outside);
}

int main() {
    test_basic_crossing();
    test_horizontal_edge_perpendicular();
    test_collinear_overlap_outer();
    test_vertex_counted_once();
    test_hole_vertex_crossing();
    test_hole_tangency();
    test_hole_collinear_overlap();
    test_ray_starts_inside();
    test_rational_direction_exact();
    test_miss_and_pointing_away();
    test_reversed_vertex_order();
    test_locate();
    if (failures == 0) {
        std::cout << "All tests passed.\n";
        return 0;
    }
    std::cout << failures << " check(s) failed.\n";
    return 1;
}
