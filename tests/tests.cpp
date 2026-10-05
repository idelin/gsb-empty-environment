#include "ray_intersect.hpp"

#include <cstdio>

using namespace raypoly;

static int failures = 0;

#define CHECK(cond)                                              \
  do {                                                           \
    if (!(cond)) {                                               \
      ++failures;                                                \
      std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond); \
    }                                                            \
  } while (0)

static Polygon square(int lo, int hi) {
  Polygon p;
  p.outer = {{lo, lo}, {hi, lo}, {hi, hi}, {lo, hi}};
  return p;
}

// 1. Plain transverse crossing of a square.
static void test_basic_crossing() {
  auto res = intersectRayPolygon(Ray::throughIntegers(-1, 5, 1, 0), square(0, 10));
  CHECK(res.interior.size() == 1);
  CHECK(res.interior[0].lo == Rational(1));
  CHECK(res.interior[0].hi == Rational(11));
  CHECK(!res.interior[0].lo_closed);
  CHECK(!res.interior[0].hi_unbounded);
  CHECK(res.interior[0].lo_evidence.size() == 1);
  CHECK(res.interior[0].lo_evidence[0].ring == 0);
  CHECK(res.interior[0].lo_evidence[0].edge == 3);  // left edge (0,10)->(0,0)
  CHECK(res.interior[0].hi_evidence.size() == 1);
  CHECK(res.interior[0].hi_evidence[0].edge == 1);  // right edge (10,0)->(10,10)
  CHECK(res.boundary_points.size() == 2);
  CHECK(res.boundary_segments.empty());
}

// 2. Ray through two corners: each vertex hit toggles parity exactly once.
static void test_vertex_crossing_counted_once() {
  auto res = intersectRayPolygon(Ray::throughIntegers(-1, -1, 1, 1), square(0, 10));
  CHECK(res.interior.size() == 1);
  CHECK(res.interior[0].lo == Rational(1));   // corner (0,0)
  CHECK(res.interior[0].hi == Rational(11));  // corner (10,10)
  CHECK(res.boundary_points.size() == 2);
  CHECK(res.boundary_points[0].t == Rational(1));
  CHECK(res.boundary_points[0].edges.size() == 2);     // both incident edges
  CHECK(res.boundary_points[0].vertices.size() == 1);  // single vertex, once
  CHECK(res.boundary_points[1].t == Rational(11));
  CHECK(res.boundary_points[1].edges.size() == 2);
}

// 3. Ray collinear with a horizontal edge: pure boundary, no interior.
static void test_horizontal_edge_collinear() {
  auto res = intersectRayPolygon(Ray::throughIntegers(-1, 0, 1, 0), square(0, 10));
  CHECK(res.interior.empty());
  CHECK(res.boundary_segments.size() == 1);
  CHECK(res.boundary_segments[0].t0 == Rational(1));
  CHECK(res.boundary_segments[0].t1 == Rational(11));
  CHECK(res.boundary_segments[0].edges.size() == 1);
  CHECK(res.boundary_segments[0].edges[0].edge == 0);  // bottom edge
}

// 4. Ray origin lies on the collinear edge: overlap clipped to t >= 0.
static void test_collinear_overlap_origin_on_edge() {
  auto res = intersectRayPolygon(Ray::throughIntegers(5, 0, 1, 0), square(0, 10));
  CHECK(res.interior.empty());
  CHECK(res.boundary_segments.size() == 1);
  CHECK(res.boundary_segments[0].t0 == Rational(0));
  CHECK(res.boundary_segments[0].t1 == Rational(5));
}

// 5. Hole tangent along an edge: ray runs along the hole's bottom edge.
static Polygon donut_with_edge_on_y3() {
  Polygon p;
  p.outer = {{0, 0}, {20, 0}, {20, 10}, {0, 10}};
  p.holes = {{{5, 3}, {15, 3}, {15, 6}, {5, 6}}};
  return p;
}

static void test_hole_tangent_edge() {
  auto res =
      intersectRayPolygon(Ray::throughIntegers(-1, 3, 1, 0), donut_with_edge_on_y3());
  CHECK(res.interior.size() == 2);
  CHECK(res.interior[0].lo == Rational(1));
  CHECK(res.interior[0].hi == Rational(6));
  CHECK(res.interior[1].lo == Rational(16));
  CHECK(res.interior[1].hi == Rational(21));
  CHECK(res.boundary_segments.size() == 1);
  CHECK(res.boundary_segments[0].t0 == Rational(6));
  CHECK(res.boundary_segments[0].t1 == Rational(16));
  CHECK(res.boundary_segments[0].edges.size() == 1);
  CHECK(res.boundary_segments[0].edges[0].ring == 1);  // hole edge
  // Interior resumes after the tangent edge: evidence comes from the hole.
  CHECK(!res.interior[1].lo_evidence.empty());
  CHECK(res.interior[1].lo_evidence[0].ring == 1);
}

// 6. Hole tangent at a single vertex: interior passes through uninterrupted.
static void test_hole_tangent_vertex() {
  Polygon p;
  p.outer = {{0, 0}, {20, 0}, {20, 10}, {0, 10}};
  p.holes = {{{6, 8}, {10, 4}, {14, 8}}};  // triangle touching y=4 at (10,4)
  auto res = intersectRayPolygon(Ray::throughIntegers(-1, 4, 1, 0), p);
  CHECK(res.interior.size() == 1);
  CHECK(res.interior[0].lo == Rational(1));
  CHECK(res.interior[0].hi == Rational(21));
  CHECK(res.boundary_points.size() == 3);  // two outer crossings + touch point
  CHECK(res.boundary_points[1].t == Rational(11));
  CHECK(res.boundary_points[1].edges.size() == 2);
  CHECK(res.boundary_points[1].vertices.size() == 1);
  CHECK(res.boundary_points[1].vertices[0].ring == 1);
}

// 7. Reversed vertex order must give identical results (even-odd fill).
static void test_reversed_vertex_order() {
  Polygon p = donut_with_edge_on_y3();
  auto fwd = intersectRayPolygon(Ray::throughIntegers(-1, 3, 1, 0), p);
  auto rev = [&](std::vector<Vec2>& ring) {
    std::reverse(ring.begin(), ring.end());
  };
  rev(p.outer);
  for (auto& h : p.holes) rev(h);
  auto bwd = intersectRayPolygon(Ray::throughIntegers(-1, 3, 1, 0), p);
  CHECK(fwd.interior.size() == bwd.interior.size());
  for (std::size_t i = 0; i < fwd.interior.size(); ++i) {
    CHECK(fwd.interior[i].lo == bwd.interior[i].lo);
    CHECK(fwd.interior[i].hi == bwd.interior[i].hi);
  }
  CHECK(fwd.boundary_segments.size() == bwd.boundary_segments.size());
  for (std::size_t i = 0; i < fwd.boundary_segments.size(); ++i) {
    CHECK(fwd.boundary_segments[i].t0 == bwd.boundary_segments[i].t0);
    CHECK(fwd.boundary_segments[i].t1 == bwd.boundary_segments[i].t1);
  }
  CHECK(fwd.boundary_points.size() == bwd.boundary_points.size());
}

// 8. Ray crossing a hole fully: two interior intervals.
static void test_hole_full_crossing() {
  Polygon p;
  p.outer = {{0, 0}, {20, 0}, {20, 10}, {0, 10}};
  p.holes = {{{5, 5}, {15, 5}, {15, 9}, {5, 9}}};
  auto res = intersectRayPolygon(Ray::throughIntegers(-1, 7, 1, 0), p);
  CHECK(res.interior.size() == 2);
  CHECK(res.interior[0].lo == Rational(1));
  CHECK(res.interior[0].hi == Rational(6));
  CHECK(res.interior[1].lo == Rational(16));
  CHECK(res.interior[1].hi == Rational(21));
  CHECK(res.boundary_points.size() == 4);
  CHECK(res.interior[1].lo_evidence.size() == 1);
  CHECK(res.interior[1].lo_evidence[0].ring == 1);
  CHECK(res.interior[1].lo_evidence[0].edge == 1);  // hole right edge
}

// 9. Rational origin and direction: exact rational interval endpoints.
static void test_rational_ray() {
  Ray r;
  r.ox = Rational(1, 2);
  r.oy = Rational(1, 2);
  r.dx = Rational(1, 3);
  r.dy = Rational(2, 5);
  auto res = intersectRayPolygon(r, square(0, 10));
  CHECK(res.interior.size() == 1);
  CHECK(res.interior[0].lo == Rational(0));
  CHECK(res.interior[0].lo_closed);  // origin strictly inside
  CHECK(res.interior[0].hi == Rational(95, 4));  // exits through y = 10
  CHECK(res.interior[0].hi_evidence.size() == 1);
  CHECK(res.interior[0].hi_evidence[0].edge == 2);  // top edge
}

// 10. Ray tangent to an outer vertex: touch point, no interior.
static void test_tangent_outer_vertex() {
  Polygon p;
  p.outer = {{2, 0}, {4, 2}, {2, 4}, {0, 2}};  // diamond
  auto res = intersectRayPolygon(Ray::throughIntegers(-1, 0, 1, 0), p);
  CHECK(res.interior.empty());
  CHECK(res.boundary_points.size() == 1);
  CHECK(res.boundary_points[0].t == Rational(3));
  CHECK(res.boundary_points[0].edges.size() == 2);
}

// 11. Ray starting strictly inside: interval begins closed at t = 0.
static void test_origin_inside() {
  auto res = intersectRayPolygon(Ray::throughIntegers(5, 5, 1, 0), square(0, 10));
  CHECK(res.interior.size() == 1);
  CHECK(res.interior[0].lo == Rational(0));
  CHECK(res.interior[0].lo_closed);
  CHECK(res.interior[0].hi == Rational(5));
  CHECK(res.boundary_points.size() == 1);
}

// 12. Ray missing the polygon entirely.
static void test_miss() {
  auto res = intersectRayPolygon(Ray::throughIntegers(-1, -5, 1, 0), square(0, 10));
  CHECK(res.interior.empty());
  CHECK(res.boundary_points.empty());
  CHECK(res.boundary_segments.empty());
}

// 13. Even-odd fill: a "hole" ring nested inside a hole becomes an island.
static void test_even_odd_nested_rings() {
  Polygon p;
  p.outer = {{0, 0}, {20, 0}, {20, 20}, {0, 20}};
  p.holes = {{{2, 2}, {18, 2}, {18, 18}, {2, 18}},
             {{4, 4}, {16, 4}, {16, 16}, {4, 16}}};
  auto res = intersectRayPolygon(Ray::throughIntegers(-1, 10, 1, 0), p);
  CHECK(res.interior.size() == 3);
  CHECK(res.interior[0].lo == Rational(1));
  CHECK(res.interior[0].hi == Rational(3));
  CHECK(res.interior[1].lo == Rational(5));
  CHECK(res.interior[1].hi == Rational(17));
  CHECK(res.interior[2].lo == Rational(19));
  CHECK(res.interior[2].hi == Rational(21));
}

// 14. Collinear chain that toggles: ray enters the polygon through a
// horizontal edge (V-shaped boundary crossing the ray along an edge).
static void test_collinear_chain_with_toggle() {
  Polygon p;
  // Pentagon: the ray y = 0 overlaps edge (0,0)-(4,0); the boundary continues
  // below the ray on one side and above on the other, so parity toggles.
  p.outer = {{0, 0}, {4, 0}, {4, -3}, {8, 2}, {0, 2}};
  auto res = intersectRayPolygon(Ray::throughIntegers(-1, 0, 1, 0), p);
  CHECK(res.boundary_segments.size() == 1);
  CHECK(res.boundary_segments[0].t0 == Rational(1));
  CHECK(res.boundary_segments[0].t1 == Rational(5));
  // After the overlap the ray is inside until it exits through edge (8,2)-(0,2)
  // ... actually exit is through edge (4,-3)-(8,2) or (8,2)-(0,2); just check
  // there is exactly one interior interval starting right after the segment.
  CHECK(res.interior.size() == 1);
  CHECK(res.interior[0].lo == Rational(5));
  CHECK(res.interior[0].hi == Rational(37, 5));  // exits edge (4,-3)-(8,2)
  CHECK(!res.interior[0].lo_evidence.empty());   // chain edges as evidence
  CHECK(res.interior[0].hi_evidence.size() == 1);
  CHECK(res.interior[0].hi_evidence[0].edge == 2);
}

int main() {
  test_basic_crossing();
  test_vertex_crossing_counted_once();
  test_horizontal_edge_collinear();
  test_collinear_overlap_origin_on_edge();
  test_hole_tangent_edge();
  test_hole_tangent_vertex();
  test_reversed_vertex_order();
  test_hole_full_crossing();
  test_rational_ray();
  test_tangent_outer_vertex();
  test_origin_inside();
  test_miss();
  test_even_odd_nested_rings();
  test_collinear_chain_with_toggle();

  if (failures == 0) {
    std::printf("all tests passed\n");
    return 0;
  }
  std::printf("%d check(s) failed\n", failures);
  return 1;
}
