#pragma once
// Exact convex polygon clipping (Sutherland-Hodgman) over rational coordinates.
//
// - Input polygons may be CW or CCW and may contain duplicate adjacent
//   vertices and collinear edges; they are normalized internally.
// - The result distinguishes Empty / Point / Segment / Polygon, so degenerate
//   contacts (point touch, shared edge) are reported exactly, never swallowed
//   by a floating-point epsilon.
// - Polygon results are normalized: strictly convex, counter-clockwise, no
//   duplicate or collinear vertices, starting at the lexicographically
//   smallest vertex.

#include <vector>
#include <algorithm>
#include "rational.hpp"

namespace exactclip {

struct Point {
    Rational x, y;
    friend bool operator==(const Point& a, const Point& b) { return a.x == b.x && a.y == b.y; }
    friend bool operator!=(const Point& a, const Point& b) { return !(a == b); }
    std::string toString() const { return "(" + x.toString() + ", " + y.toString() + ")"; }
};

// Exact signed area * 2 of the triangle (a, b, c).
inline Rational cross(const Point& a, const Point& b, const Point& c) {
    return (b.x - a.x) * (c.y - a.y) - (b.y - a.y) * (c.x - a.x);
}

// Exact orientation predicate: +1 left turn, 0 collinear, -1 right turn.
inline int orient(const Point& a, const Point& b, const Point& c) {
    return cross(a, b, c).sign();
}

// Exact intersection of the line through (a, b) with the line through (s, e).
// The two lines must not be parallel (caller guarantees a crossing exists).
inline Point lineIntersect(const Point& a, const Point& b, const Point& s, const Point& e) {
    Rational c1 = cross(a, b, s);
    Rational c2 = cross(a, b, e);
    Rational t = c1 / (c1 - c2); // c1 != c2 because the lines cross
    return Point{s.x + t * (e.x - s.x), s.y + t * (e.y - s.y)};
}

// Twice the signed area; positive iff the polygon is counter-clockwise.
inline Rational polygonArea2(const std::vector<Point>& poly) {
    Rational sum;
    for (size_t i = 0; i < poly.size(); ++i) {
        const Point& p = poly[i];
        const Point& q = poly[(i + 1) % poly.size()];
        sum = sum + (p.x * q.y - p.y * q.x);
    }
    return sum;
}

// Normalize a polygon: drop duplicate adjacent vertices, drop collinear
// vertices, force counter-clockwise order (when non-degenerate), and rotate
// so the lexicographically smallest vertex comes first.
inline std::vector<Point> normalizePolygon(const std::vector<Point>& poly) {
    std::vector<Point> out;
    for (const Point& p : poly)
        if (out.empty() || p != out.back()) out.push_back(p);
    while (out.size() > 1 && out.front() == out.back()) out.pop_back();

    // Remove collinear middle vertices until stable.
    bool changed = true;
    while (changed && out.size() >= 3) {
        changed = false;
        for (size_t i = 0; i < out.size() && out.size() >= 3;) {
            const Point& prev = out[(i + out.size() - 1) % out.size()];
            const Point& cur = out[i];
            const Point& next = out[(i + 1) % out.size()];
            bool collinear = orient(prev, cur, next) == 0;
            // cur lies on segment(prev, next) iff the dot product is >= 0.
            Rational dot = (cur.x - prev.x) * (next.x - cur.x) +
                           (cur.y - prev.y) * (next.y - cur.y);
            if (collinear && dot.sign() >= 0) {
                out.erase(out.begin() + static_cast<long>(i));
                changed = true;
            } else {
                ++i;
            }
        }
    }

    if (out.size() >= 3 && polygonArea2(out).sign() < 0)
        std::reverse(out.begin(), out.end());

    if (!out.empty()) {
        size_t best = 0;
        for (size_t i = 1; i < out.size(); ++i) {
            const Point& p = out[i];
            const Point& q = out[best];
            if (p.x < q.x || (p.x == q.x && p.y < q.y)) best = i;
        }
        std::rotate(out.begin(), out.begin() + static_cast<long>(best), out.end());
    }
    return out;
}

enum class Kind { Empty, Point, Segment, Polygon };

struct ClipResult {
    Kind kind = Kind::Empty;
    // Empty: {}. Point: {p}. Segment: {p, q}.
    // Polygon: normalized CCW vertices (>= 3, strictly convex).
    std::vector<Point> vertices;
};

namespace detail {

// Clip the polygon against the left half-plane of the directed edge a -> b
// (boundary counts as inside). Exact Sutherland-Hodgman step.
inline std::vector<Point> clipHalfplane(const std::vector<Point>& in,
                                        const Point& a, const Point& b) {
    std::vector<Point> out;
    if (in.empty()) return out;
    auto inside = [&](const Point& p) { return orient(a, b, p) >= 0; };
    Point s = in.back();
    bool sIn = inside(s);
    for (const Point& e : in) {
        bool eIn = inside(e);
        if (eIn) {
            if (!sIn) out.push_back(lineIntersect(a, b, s, e));
            out.push_back(e);
        } else if (sIn) {
            out.push_back(lineIntersect(a, b, s, e));
        }
        s = e;
        sIn = eIn;
    }
    return out;
}

} // namespace detail

// Intersect two convex polygons given as closed regions (boundary included).
// Inputs may be CW/CCW and contain duplicate adjacent points or collinear
// edges. A degenerate input (fewer than 3 vertices or zero area after
// normalization) is treated as having empty interior and yields Empty.
inline ClipResult clipConvex(const std::vector<Point>& subject,
                             const std::vector<Point>& clip) {
    std::vector<Point> s = normalizePolygon(subject);
    std::vector<Point> c = normalizePolygon(clip);
    if (s.size() < 3 || c.size() < 3) return {Kind::Empty, {}};
    if (polygonArea2(s).isZero() || polygonArea2(c).isZero()) return {Kind::Empty, {}};

    std::vector<Point> out = s;
    for (size_t i = 0; i < c.size() && !out.empty(); ++i)
        out = detail::clipHalfplane(out, c[i], c[(i + 1) % c.size()]);

    std::vector<Point> norm = normalizePolygon(out);
    if (norm.size() >= 3 && !polygonArea2(norm).isZero())
        return {Kind::Polygon, norm};
    if (norm.size() == 2) return {Kind::Segment, norm};
    if (norm.size() == 1) return {Kind::Point, norm};
    return {Kind::Empty, {}};
}

// ---- Independent verification helpers (exact, no floating point) ----

// True iff p satisfies every half-plane of the normalized CCW polygon.
inline bool onOrInside(const std::vector<Point>& ccwPoly, const Point& p) {
    for (size_t i = 0; i < ccwPoly.size(); ++i)
        if (orient(ccwPoly[i], ccwPoly[(i + 1) % ccwPoly.size()], p) < 0) return false;
    return true;
}

// True iff the polygon is strictly convex and counter-clockwise.
inline bool strictlyConvexCCW(const std::vector<Point>& poly) {
    if (poly.size() < 3) return false;
    for (size_t i = 0; i < poly.size(); ++i)
        if (orient(poly[i], poly[(i + 1) % poly.size()], poly[(i + 2) % poly.size()]) <= 0)
            return false;
    return true;
}

// Separating-axis test for two convex polygons (exact). True iff some edge
// normal of either polygon strictly separates them.
inline bool separated(const std::vector<Point>& a, const std::vector<Point>& b) {
    const std::vector<Point>* polys[2] = {&a, &b};
    for (const auto* poly : polys) {
        for (size_t i = 0; i < poly->size(); ++i) {
            const Point& p = (*poly)[i];
            const Point& q = (*poly)[(i + 1) % poly->size()];
            Rational nx = q.y - p.y, ny = -(q.x - p.x); // edge normal
            Rational minA, maxA, minB, maxB;
            bool first = true;
            for (const Point& v : a) {
                Rational d = nx * v.x + ny * v.y;
                if (first || d < minA) minA = d;
                if (first || d > maxA) maxA = d;
                first = false;
            }
            first = true;
            for (const Point& v : b) {
                Rational d = nx * v.x + ny * v.y;
                if (first || d < minB) minB = d;
                if (first || d > maxB) maxB = d;
                first = false;
            }
            if (maxA < minB || maxB < minA) return true;
        }
    }
    return false;
}

} // namespace exactclip
