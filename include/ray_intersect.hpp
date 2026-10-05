#pragma once

// ray_intersect: exact intersection of a rational ray with the interior of an
// integer-vertex polygon (with holes), using the even-odd fill rule.
//
//  - All arithmetic is exact (128-bit integer rationals, integer orientation
//    predicates). No floating point is used anywhere.
//  - Boundary features (touch points and collinear overlaps) are classified
//    separately from interior intervals.
//  - Vertex hits are counted exactly once: maximal runs of on-line vertices
//    are collapsed into a single "chain" event.

#include <cstdint>
#include <string>
#include <vector>

namespace raypoly {

using i64 = std::int64_t;
using i128 = __int128_t;

// Exact rational number, normalized (den > 0, gcd(|num|, den) == 1).
struct Rational {
  i128 num;
  i128 den;

  Rational();
  Rational(i64 value);
  Rational(i64 n, i64 d);
  static Rational make(i128 n, i128 d);

  int sign() const;  // -1, 0, +1
};

bool operator==(const Rational& a, const Rational& b);
bool operator!=(const Rational& a, const Rational& b);
bool operator<(const Rational& a, const Rational& b);
bool operator<=(const Rational& a, const Rational& b);
bool operator>(const Rational& a, const Rational& b);
bool operator>=(const Rational& a, const Rational& b);

std::string toString(i128 value);
std::string toString(const Rational& value);

struct Vec2 {
  i64 x, y;
};

// Ray P(t) = origin + t * direction, t >= 0. Origin and direction are rational.
struct Ray {
  Rational ox, oy;  // origin
  Rational dx, dy;  // direction (must not both be zero)

  static Ray throughIntegers(i64 ox, i64 oy, i64 dx, i64 dy);
};

// rings: outer boundary plus zero or more hole rings. Vertex order (CW/CCW)
// does not matter: the even-odd fill rule is orientation independent.
struct Polygon {
  std::vector<Vec2> outer;
  std::vector<std::vector<Vec2>> holes;
};

// Edge i of ring r connects vertex i to vertex (i+1) % n. Ring 0 is the outer
// boundary, rings 1.. are the holes (in input order).
struct EdgeRef {
  std::size_t ring;
  std::size_t edge;
};

struct VertexRef {
  std::size_t ring;
  std::size_t vertex;
};

// An isolated parameter value where the ray touches/crosses the boundary.
struct BoundaryPoint {
  Rational t;
  std::vector<EdgeRef> edges;      // source edges (evidence)
  std::vector<VertexRef> vertices; // source vertices, if any
};

// A parameter interval [t0, t1] where the ray runs along boundary edge(s).
struct BoundarySegment {
  Rational t0, t1;  // inclusive
  std::vector<EdgeRef> edges;  // collinear source edges (evidence)
};

// Open parameter interval (lo, hi) where the ray is strictly inside the
// polygon. lo_closed is true only when lo == 0 and the ray origin itself is
// strictly inside. hi_unbounded means the ray never leaves the polygon.
struct InteriorInterval {
  Rational lo, hi;
  bool lo_closed = false;
  bool hi_unbounded = false;
  std::vector<EdgeRef> lo_evidence;  // boundary edges producing the lo end
  std::vector<EdgeRef> hi_evidence;  // boundary edges producing the hi end
};

struct Intersection {
  std::vector<InteriorInterval> interior;          // sorted by lo
  std::vector<BoundaryPoint> boundary_points;      // sorted by t
  std::vector<BoundarySegment> boundary_segments;  // sorted by t0
};

// Computes the full intersection of `ray` with `polygon` under even-odd fill.
// Throws std::invalid_argument if the ray direction is zero.
Intersection intersectRayPolygon(const Ray& ray, const Polygon& polygon);

}  // namespace raypoly
