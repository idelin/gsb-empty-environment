#include "ray_intersect.hpp"

#include <algorithm>
#include <map>
#include <stdexcept>
#include <utility>

namespace raypoly {

namespace {

i128 abs128(i128 v) { return v < 0 ? -v : v; }

i128 gcd128(i128 a, i128 b) {
  a = abs128(a);
  b = abs128(b);
  while (b != 0) {
    i128 r = a % b;
    a = b;
    b = r;
  }
  return a;
}

}  // namespace

Rational::Rational() : num(0), den(1) {}
Rational::Rational(i64 value) : num(value), den(1) {}
Rational::Rational(i64 n, i64 d) { *this = make(n, d); }

Rational Rational::make(i128 n, i128 d) {
  if (d == 0) throw std::invalid_argument("Rational: zero denominator");
  Rational r;
  if (d < 0) {
    n = -n;
    d = -d;
  }
  i128 g = gcd128(n, d);
  if (g > 1) {
    n /= g;
    d /= g;
  }
  r.num = n;
  r.den = d;
  return r;
}

int Rational::sign() const { return (num > 0) - (num < 0); }

bool operator==(const Rational& a, const Rational& b) {
  return a.num == b.num && a.den == b.den;
}
bool operator!=(const Rational& a, const Rational& b) { return !(a == b); }
bool operator<(const Rational& a, const Rational& b) {
  return a.num * b.den < b.num * a.den;
}
bool operator<=(const Rational& a, const Rational& b) { return !(b < a); }
bool operator>(const Rational& a, const Rational& b) { return b < a; }
bool operator>=(const Rational& a, const Rational& b) { return !(a < b); }

std::string toString(i128 value) {
  if (value == 0) return "0";
  bool neg = value < 0;
  if (neg) value = -value;
  std::string s;
  while (value > 0) {
    s.push_back(static_cast<char>('0' + value % 10));
    value /= 10;
  }
  if (neg) s.push_back('-');
  std::reverse(s.begin(), s.end());
  return s;
}

std::string toString(const Rational& value) {
  if (value.den == 1) return toString(value.num);
  return toString(value.num) + "/" + toString(value.den);
}

Ray Ray::throughIntegers(i64 ox, i64 oy, i64 dx, i64 dy) {
  Ray r;
  r.ox = Rational(ox);
  r.oy = Rational(oy);
  r.dx = Rational(dx);
  r.dy = Rational(dy);
  return r;
}

namespace {

// Ray scaled to integer arithmetic:
//   origin    = (OX/OD, OY/OD)
//   direction = (DX/DD, DY/DD)
// All predicates and parameters below are exact.
struct ScaledRay {
  i128 OX, OY, OD;
  i128 DX, DY, DD;

  // Exact orientation predicate: sign of cross(direction, V - origin).
  int side(i64 x, i64 y) const {
    i128 v = DX * ((i128)y * OD - OY) - DY * ((i128)x * OD - OX);
    return (v > 0) - (v < 0);
  }

  // Parameter t of an on-line vertex: t = dot(V - O, D) / dot(D, D).
  Rational vertexT(i64 x, i64 y) const {
    i128 n = (((i128)x * OD - OX) * DX + ((i128)y * OD - OY) * DY) * DD;
    i128 d = OD * (DX * DX + DY * DY);
    return Rational::make(n, d);
  }

  // Parameter t where edge (a, b) crosses the ray line. Only called when the
  // endpoints lie strictly on opposite sides, so the crossing is proper and
  // lies strictly inside the edge.
  Rational crossingT(const Vec2& a, const Vec2& b) const {
    i128 ex = (i128)b.x - a.x;
    i128 ey = (i128)b.y - a.y;
    i128 n = (((i128)a.x * OD - OX) * ey - ((i128)a.y * OD - OY) * ex) * DD;
    i128 d = OD * (DX * ey - DY * ex);
    return Rational::make(n, d);
  }
};

struct Event {
  int toggles = 0;  // number of parity flips at this parameter (mod 2 matters)
  bool point = false;
  std::vector<EdgeRef> edges;
  std::vector<VertexRef> vertices;
};

struct RationalLess {
  bool operator()(const Rational& a, const Rational& b) const { return a < b; }
};

struct Seg {
  Rational lo, hi;
  std::vector<EdgeRef> edges;
};

void dedupeEdges(std::vector<EdgeRef>& edges) {
  std::sort(edges.begin(), edges.end(), [](const EdgeRef& a, const EdgeRef& b) {
    return a.ring != b.ring ? a.ring < b.ring : a.edge < b.edge;
  });
  edges.erase(std::unique(edges.begin(), edges.end(),
                          [](const EdgeRef& a, const EdgeRef& b) {
                            return a.ring == b.ring && a.edge == b.edge;
                          }),
              edges.end());
}

}  // namespace

Intersection intersectRayPolygon(const Ray& ray, const Polygon& polygon) {
  if (ray.dx.num == 0 && ray.dy.num == 0)
    throw std::invalid_argument("intersectRayPolygon: zero ray direction");

  ScaledRay sr;
  sr.OD = ray.ox.den * ray.oy.den;
  sr.OX = ray.ox.num * ray.oy.den;
  sr.OY = ray.oy.num * ray.ox.den;
  sr.DD = ray.dx.den * ray.dy.den;
  sr.DX = ray.dx.num * ray.dy.den;
  sr.DY = ray.dy.num * ray.dx.den;

  std::vector<const std::vector<Vec2>*> rings;
  rings.push_back(&polygon.outer);
  for (const auto& h : polygon.holes) rings.push_back(&h);

  std::map<Rational, Event, RationalLess> events;
  std::vector<Seg> segs;

  for (std::size_t ri = 0; ri < rings.size(); ++ri) {
    const auto& ring = *rings[ri];
    const std::size_t n = ring.size();
    if (n < 2) continue;

    std::vector<int> sides(n);
    std::size_t zeroCount = 0;
    for (std::size_t i = 0; i < n; ++i) {
      sides[i] = sr.side(ring[i].x, ring[i].y);
      if (sides[i] == 0) ++zeroCount;
    }

    // Transverse crossings: both endpoints strictly off the ray line and on
    // opposite sides. On-line vertices never take part here, so a vertex hit
    // can never be double counted by this loop.
    for (std::size_t i = 0; i < n; ++i) {
      std::size_t j = (i + 1) % n;
      if (sides[i] != 0 && sides[j] != 0 && sides[i] != sides[j]) {
        Rational t = sr.crossingT(ring[i], ring[j]);
        Event& ev = events[t];
        ev.toggles += 1;
        ev.point = true;
        ev.edges.push_back({ri, i});
      }
    }

    if (zeroCount == n) {
      // Degenerate ring lying entirely on the ray line: pure boundary.
      for (std::size_t i = 0; i < n; ++i) {
        std::size_t j = (i + 1) % n;
        Rational ta = sr.vertexT(ring[i].x, ring[i].y);
        Rational tb = sr.vertexT(ring[j].x, ring[j].y);
        if (tb < ta) std::swap(ta, tb);
        if (ta < tb) segs.push_back({ta, tb, {{ri, i}}});
      }
      continue;
    }

    // Maximal cyclic runs of on-line vertices ("chains"). Each chain produces
    // at most one parity toggle, decided by the sides of the two off-line
    // vertices bracketing the run: opposite sides -> the boundary crosses the
    // ray (toggle); same side -> the boundary merely touches it (no toggle).
    std::size_t start = 0;
    while (sides[start] == 0) ++start;
    std::size_t k = 1;
    while (k <= n) {
      std::size_t idx = (start + k) % n;
      if (sides[idx] != 0) {
        ++k;
        continue;
      }
      std::vector<std::size_t> run;
      std::size_t len = 0;
      while (len < n && sides[(start + k + len) % n] == 0) {
        run.push_back((start + k + len) % n);
        ++len;
      }
      k += len;

      std::size_t prevIdx = (run.front() + n - 1) % n;
      std::size_t nextIdx = (run.back() + 1) % n;
      bool toggle = sides[prevIdx] != sides[nextIdx];

      Rational tlo = sr.vertexT(ring[run[0]].x, ring[run[0]].y);
      Rational thi = tlo;
      for (std::size_t v : run) {
        Rational t = sr.vertexT(ring[v].x, ring[v].y);
        if (t < tlo) tlo = t;
        if (thi < t) thi = t;
      }

      std::vector<EdgeRef> collinear;
      for (std::size_t a = 0; a + 1 < run.size(); ++a)
        collinear.push_back({ri, run[a]});
      EdgeRef inEdge{ri, prevIdx};
      EdgeRef outEdge{ri, run.back()};

      if (run.size() == 1 || tlo == thi) {
        // Isolated touch/cross vertex.
        Event& ev = events[tlo];
        if (toggle) ev.toggles += 1;
        ev.point = true;
        ev.edges.push_back(inEdge);
        ev.edges.push_back(outEdge);
        for (std::size_t v : run) ev.vertices.push_back({ri, v});
      } else {
        // Collinear overlap: boundary segment plus a single toggle at tlo.
        std::vector<EdgeRef> evidence = collinear;
        evidence.push_back(inEdge);
        evidence.push_back(outEdge);
        segs.push_back({tlo, thi, collinear});
        Event& eLo = events[tlo];
        if (toggle) eLo.toggles += 1;
        eLo.edges.insert(eLo.edges.end(), evidence.begin(), evidence.end());
        Event& eHi = events[thi];
        eHi.edges.insert(eHi.edges.end(), evidence.begin(), evidence.end());
      }
    }
  }

  // Sweep the parameter axis from -inf to +inf, tracking even-odd parity.
  // Features with t < 0 only contribute parity (they sit behind the origin);
  // features with t >= 0 are reported.
  Intersection result;
  bool inside = false;
  bool havePrev = false;
  Rational prev;
  std::vector<EdgeRef> prevEvidence;

  auto covered = [&](const Rational& a, const Rational& b) {
    for (const auto& s : segs)
      if (!(a < s.lo) && !(s.hi < b)) return true;
    return false;
  };

  for (const auto& entry : events) {
    const Rational& t = entry.first;
    const Event& ev = entry.second;
    if (havePrev && inside && !covered(prev, t) && t.sign() > 0) {
      InteriorInterval iv;
      iv.hi = t;
      iv.hi_evidence = ev.edges;
      if (prev.sign() < 0) {
        iv.lo = Rational(0);
        iv.lo_closed = true;  // origin is strictly inside
      } else {
        iv.lo = prev;
        iv.lo_evidence = prevEvidence;
      }
      if (iv.lo < iv.hi) result.interior.push_back(std::move(iv));
    }
    if (t.sign() >= 0 && ev.point)
      result.boundary_points.push_back({t, ev.edges, ev.vertices});
    if (ev.toggles & 1) inside = !inside;
    prev = t;
    prevEvidence = ev.edges;
    havePrev = true;
  }

  if (inside) {  // trailing interval (prev, +inf)
    InteriorInterval iv;
    iv.hi_unbounded = true;
    iv.hi = Rational(0);  // unused
    if (!havePrev || prev.sign() < 0) {
      iv.lo = Rational(0);
      iv.lo_closed = true;
    } else {
      iv.lo = prev;
      iv.lo_evidence = prevEvidence;
    }
    result.interior.push_back(std::move(iv));
  }

  // Emit collinear overlaps, clipped to t >= 0.
  for (auto& s : segs) {
    Rational lo = s.lo.sign() < 0 ? Rational(0) : s.lo;
    if (lo < s.hi) {
      result.boundary_segments.push_back({lo, s.hi, s.edges});
    } else if (lo == s.hi) {
      result.boundary_points.push_back({lo, s.edges, {}});
    }
  }

  // Merge interior intervals that meet at a non-toggling boundary point
  // (e.g. a tangent vertex): the touch point is reported separately, it must
  // not fragment the interior. Intervals separated by a boundary segment
  // never share an endpoint, so this is safe.
  {
    std::vector<InteriorInterval> mergedInterior;
    for (auto& iv : result.interior) {
      if (!mergedInterior.empty() && !mergedInterior.back().hi_unbounded &&
          mergedInterior.back().hi == iv.lo) {
        mergedInterior.back().hi = iv.hi;
        mergedInterior.back().hi_unbounded = iv.hi_unbounded;
        mergedInterior.back().hi_evidence = std::move(iv.hi_evidence);
      } else {
        mergedInterior.push_back(std::move(iv));
      }
    }
    result.interior = std::move(mergedInterior);
  }

  // Sort and merge boundary points sharing the same parameter.
  auto& bp = result.boundary_points;
  std::sort(bp.begin(), bp.end(),
            [](const BoundaryPoint& a, const BoundaryPoint& b) { return a.t < b.t; });
  std::vector<BoundaryPoint> merged;
  for (auto& p : bp) {
    if (!merged.empty() && merged.back().t == p.t) {
      auto& m = merged.back();
      m.edges.insert(m.edges.end(), p.edges.begin(), p.edges.end());
      m.vertices.insert(m.vertices.end(), p.vertices.begin(), p.vertices.end());
    } else {
      merged.push_back(std::move(p));
    }
  }
  for (auto& p : merged) dedupeEdges(p.edges);
  result.boundary_points = std::move(merged);

  std::sort(result.boundary_segments.begin(), result.boundary_segments.end(),
            [](const BoundarySegment& a, const BoundarySegment& b) {
              return a.t0 < b.t0;
            });
  for (auto& s : result.boundary_segments) dedupeEdges(s.edges);
  for (auto& iv : result.interior) {
    dedupeEdges(iv.lo_evidence);
    dedupeEdges(iv.hi_evidence);
  }

  return result;
}

}  // namespace raypoly
