#pragma once

#include <algorithm>
#include <map>
#include <stdexcept>
#include <vector>

#include "rational.hpp"

namespace raypoly {

struct Point {
    Rational x, y;
};

// Ray: p(t) = origin + t * dir, t >= 0. dir must be non-zero.
struct Ray {
    Point origin;
    Point dir;
};

enum class PointLocation { Inside, Outside, Boundary };

// Even-odd filled polygon: one outer ring plus any number of hole rings.
// Rings are implicitly closed; winding order is irrelevant (even-odd rule).
struct Polygon {
    std::vector<Point> outer;
    std::vector<std::vector<Point>> holes;

    std::vector<const std::vector<Point>*> rings() const {
        std::vector<const std::vector<Point>*> out;
        out.push_back(&outer);
        for (const auto& h : holes) out.push_back(&h);
        return out;
    }
};

// Exact orientation predicate: sign of cross(b - a, c - a).
inline int orientation(const Point& a, const Point& b, const Point& c) {
    Rational cr = (b.x - a.x) * (c.y - a.y) - (b.y - a.y) * (c.x - a.x);
    return cr.sign();
}

// Exact point location under the even-odd rule, boundary checked first.
inline PointLocation locate(const Point& p, const Polygon& poly) {
    bool inside = false;
    for (const auto* ring : poly.rings()) {
        const std::size_t n = ring->size();
        for (std::size_t i = 0; i < n; ++i) {
            const Point& a = (*ring)[i];
            const Point& b = (*ring)[(i + 1) % n];
            Rational cr = (b.x - a.x) * (p.y - a.y) - (b.y - a.y) * (p.x - a.x);
            if (cr.sign() == 0) {
                const Rational& lo_x = a.x < b.x ? a.x : b.x;
                const Rational& hi_x = a.x < b.x ? b.x : a.x;
                const Rational& lo_y = a.y < b.y ? a.y : b.y;
                const Rational& hi_y = a.y < b.y ? b.y : a.y;
                if (lo_x <= p.x && p.x <= hi_x && lo_y <= p.y && p.y <= hi_y)
                    return PointLocation::Boundary;
            }
            // Half-open horizontal ray casting to the right of p.
            bool above_a = a.y > p.y;
            bool above_b = b.y > p.y;
            if (above_a != above_b) {
                Rational x_int = a.x + (p.y - a.y) * (b.x - a.x) / (b.y - a.y);
                if (x_int > p.x) inside = !inside;
            }
        }
    }
    return inside ? PointLocation::Inside : PointLocation::Outside;
}

// Evidence tying a parameter value back to a polygon edge.
struct EdgeRef {
    std::size_t ring = 0;   // 0 = outer ring, 1.. = holes
    std::size_t edge = 0;   // edge from vertex i to vertex (i+1) % n
    bool overlap = false;   // true if the ray is collinear with this edge
    Rational s0, s1;        // edge parameter(s) of contact; s0 == s1 for a point hit
};

// Maximal parameter interval where the ray lies in the polygon interior.
struct Interval {
    Rational lo, hi;
    bool lo_closed = false;    // true only when lo == 0 and the origin is interior
    bool hi_closed = false;
    bool hi_infinite = false;  // ray never leaves the polygon
    std::vector<EdgeRef> lo_edges; // boundary evidence at lo (empty if lo == 0, closed)
    std::vector<EdgeRef> hi_edges; // boundary evidence at hi (empty if hi_infinite)
};

// Parameter range where the ray lies exactly on the boundary.
// lo == hi: isolated boundary point (e.g. a vertex touch). lo < hi: the ray
// overlaps one or more collinear edges.
struct BoundaryPiece {
    Rational lo, hi;
    std::vector<EdgeRef> edges;
};

struct RayIntersection {
    std::vector<Interval> interior;     // sorted by lo
    std::vector<BoundaryPiece> boundary; // sorted by lo
};

namespace detail {

struct Overlap {
    Rational lo, hi;
    std::vector<EdgeRef> edges;
};

} // namespace detail

// Computes the intersection of a rational ray with the interior of an
// even-odd filled polygon (holes supported). Boundary contact is reported
// separately in `boundary`; each parameter value appears at most once, so a
// ray through a shared vertex is never double counted.
inline RayIntersection intersect(const Ray& ray, const Polygon& poly) {
    if (ray.dir.x.sign() == 0 && ray.dir.y.sign() == 0)
        throw std::invalid_argument("ray direction must be non-zero");

    std::map<Rational, std::vector<EdgeRef>> point_events; // t -> evidence
    std::vector<detail::Overlap> overlaps;

    auto rings = poly.rings();
    for (std::size_t ri = 0; ri < rings.size(); ++ri) {
        const auto& ring = *rings[ri];
        const std::size_t n = ring.size();
        for (std::size_t ei = 0; ei < n; ++ei) {
            const Point& a = ring[ei];
            const Point& b = ring[(ei + 1) % n];
            Rational ex = b.x - a.x, ey = b.y - a.y;
            Rational wx = a.x - ray.origin.x, wy = a.y - ray.origin.y;
            Rational denom = ray.dir.x * ey - ray.dir.y * ex; // cross(dir, e)
            if (denom.sign() != 0) {
                Rational t = (wx * ey - wy * ex) / denom;              // cross(w, e)
                Rational s = (wx * ray.dir.y - wy * ray.dir.x) / denom; // cross(w, dir)
                if (t.sign() >= 0 && s.sign() >= 0 && s <= Rational(1))
                    point_events[t].push_back(EdgeRef{ri, ei, false, s, s});
            } else {
                Rational c = wx * ray.dir.y - wy * ray.dir.x; // cross(w, dir)
                if (c.sign() != 0) continue;                  // parallel, disjoint
                // Collinear: project the edge onto the ray parameter.
                Rational ta, tb;
                if (ray.dir.x.sign() != 0) {
                    ta = (a.x - ray.origin.x) / ray.dir.x;
                    tb = (b.x - ray.origin.x) / ray.dir.x;
                } else {
                    ta = (a.y - ray.origin.y) / ray.dir.y;
                    tb = (b.y - ray.origin.y) / ray.dir.y;
                }
                Rational lo = ta < tb ? ta : tb;
                Rational hi = ta < tb ? tb : ta;
                Rational s_lo = ta < tb ? Rational(0) : Rational(1);
                Rational s_hi = ta < tb ? Rational(1) : Rational(0);
                if (hi.sign() < 0) continue; // entirely behind the origin
                if (lo == hi) {              // degenerate (zero-length) edge
                    point_events[lo].push_back(EdgeRef{ri, ei, false, s_lo, s_lo});
                    continue;
                }
                if (lo.sign() < 0) lo = Rational(0);
                overlaps.push_back(detail::Overlap{lo, hi, {EdgeRef{ri, ei, true, s_lo, s_hi}}});
            }
        }
    }

    // Sorted, deduplicated critical parameters: a vertex shared by two edges
    // (or hit by several edges) yields exactly one critical value.
    std::vector<Rational> crit;
    crit.reserve(point_events.size() + 2 * overlaps.size());
    for (const auto& kv : point_events) crit.push_back(kv.first);
    for (const auto& ov : overlaps) {
        crit.push_back(ov.lo);
        crit.push_back(ov.hi);
    }
    std::sort(crit.begin(), crit.end());
    crit.erase(std::unique(crit.begin(), crit.end()), crit.end());

    auto evidence_at = [&](const Rational& t) {
        std::vector<EdgeRef> out;
        auto it = point_events.find(t);
        if (it != point_events.end()) out = it->second;
        for (const auto& ov : overlaps) {
            if (t == ov.lo || t == ov.hi)
                out.insert(out.end(), ov.edges.begin(), ov.edges.end());
        }
        return out;
    };

    RayIntersection result;

    // Boundary pieces: overlap segments, plus isolated point events. A point
    // event falling on an overlap merges its evidence into that piece.
    for (const auto& ov : overlaps)
        result.boundary.push_back(BoundaryPiece{ov.lo, ov.hi, ov.edges});
    for (const auto& kv : point_events) {
        bool merged = false;
        for (auto& bp : result.boundary) {
            if (bp.lo <= kv.first && kv.first <= bp.hi) {
                bp.edges.insert(bp.edges.end(), kv.second.begin(), kv.second.end());
                merged = true;
                break;
            }
        }
        if (!merged) result.boundary.push_back(BoundaryPiece{kv.first, kv.first, kv.second});
    }
    std::sort(result.boundary.begin(), result.boundary.end(),
              [](const BoundaryPiece& a, const BoundaryPiece& b) {
                  return a.lo != b.lo ? a.lo < b.lo : a.hi < b.hi;
              });

    // Interior intervals: sample one rational point per open cell between
    // consecutive critical parameters and classify it exactly.
    auto classify_at = [&](const Rational& t) {
        Point p{ray.origin.x + t * ray.dir.x, ray.origin.y + t * ray.dir.y};
        return locate(p, poly);
    };
    auto add_cell = [&](const Rational& lo, const Rational* hi, bool lo_closed) {
        Rational rep = hi ? (*hi - lo) / Rational(2) + lo : lo + Rational(1);
        if (classify_at(rep) != PointLocation::Inside) return;
        Interval iv;
        iv.lo = lo;
        iv.lo_closed = lo_closed;
        if (hi) {
            iv.hi = *hi;
            iv.hi_edges = evidence_at(*hi);
        } else {
            iv.hi_infinite = true;
        }
        if (!lo_closed) iv.lo_edges = evidence_at(lo);
        result.interior.push_back(std::move(iv));
    };

    Rational lo_bound(0);
    bool lo_closed = true; // t = 0 belongs to the ray domain
    for (const Rational& c : crit) {
        if (lo_bound < c) add_cell(lo_bound, &c, lo_closed);
        lo_bound = c;
        lo_closed = false;
    }
    add_cell(lo_bound, nullptr, lo_closed);

    return result;
}

} // namespace raypoly
