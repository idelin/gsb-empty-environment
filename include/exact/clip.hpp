// Exact convex polygon clipping over rational coordinates (header-only, C++17).
//
// - All arithmetic is exact (arbitrary-precision rationals); no floating-point
//   thresholds anywhere, so arbitrarily small intersection areas are preserved.
// - Inputs may be CW or CCW and may contain duplicate adjacent vertices and
//   collinear edges; they are normalized internally.
// - The result distinguishes area / line / point / empty intersections and, for
//   area results, yields a normalized counter-clockwise vertex sequence (no
//   duplicate adjacent vertices, no collinear vertices).
#pragma once

#include "rational.hpp"
#include <vector>
#include <string>

namespace exact {

struct Point {
    Rational x, y;
    Point() = default;
    Point(Rational px, Rational py) : x(std::move(px)), y(std::move(py)) {}
    Point(long long px, long long py) : x(px), y(py) {}
    friend bool operator==(const Point& a, const Point& b) { return a.x == b.x && a.y == b.y; }
    friend bool operator!=(const Point& a, const Point& b) { return !(a == b); }
    std::string toString() const { return "(" + x.toString() + ", " + y.toString() + ")"; }
};

// Exact sign of the cross product (b-a) x (c-a): +1 left, -1 right, 0 collinear.
inline int orient(const Point& a, const Point& b, const Point& c) {
    return ((b.x - a.x) * (c.y - a.y) - (b.y - a.y) * (c.x - a.x)).sign();
}

// Twice the signed area; positive iff vertices are counter-clockwise.
inline Rational area2(const std::vector<Point>& p) {
    Rational s;
    for (size_t i = 0; i < p.size(); ++i) {
        const Point& a = p[i];
        const Point& b = p[(i + 1) % p.size()];
        s += a.x * b.y - a.y * b.x;
    }
    return s;
}

// Exact intersection of the (infinite) lines ab and cd.
// Precondition: the lines are not parallel (checked, throws otherwise).
inline Point lineIntersect(const Point& a, const Point& b, const Point& c, const Point& d) {
    Rational dx1 = b.x - a.x, dy1 = b.y - a.y;
    Rational dx2 = d.x - c.x, dy2 = d.y - c.y;
    Rational den = dx1 * dy2 - dy1 * dx2;
    if (den.isZero()) throw std::domain_error("lineIntersect: parallel lines");
    Rational t = ((c.x - a.x) * dy2 - (c.y - a.y) * dx2) / den;
    return Point(a.x + t * dx1, a.y + t * dy1);
}

enum class ClipKind { Empty, Point, Segment, Polygon };

struct ClipResult {
    ClipKind kind = ClipKind::Empty;
    // Empty: {}, Point: {p}, Segment: {p0, p1},
    // Polygon: normalized CCW vertices (>= 3, no duplicate adjacent, no collinear).
    std::vector<Point> verts;

    Rational area() const { return kind == ClipKind::Polygon ? area2(verts) / Rational(2) : Rational(0); }
};

namespace detail {

// Removes cyclic duplicate adjacent vertices.
inline void dedupAdjacent(std::vector<Point>& p) {
    std::vector<Point> r;
    for (const auto& v : p)
        if (r.empty() || v != r.back()) r.push_back(v);
    while (r.size() > 1 && r.front() == r.back()) r.pop_back();
    p = std::move(r);
}

// Removes vertices that lie on the segment between their neighbors
// (collinear non-spike vertices). Spikes are kept.
inline void removeCollinear(std::vector<Point>& p) {
    bool changed = true;
    while (changed && p.size() > 2) {
        changed = false;
        for (size_t i = 0; i < p.size(); ++i) {
            const Point& prev = p[(i + p.size() - 1) % p.size()];
            const Point& cur = p[i];
            const Point& next = p[(i + 1) % p.size()];
            if (orient(prev, cur, next) == 0 &&
                (prev.x - cur.x) * (next.x - cur.x) + (prev.y - cur.y) * (next.y - cur.y) <= Rational(0)) {
                p.erase(p.begin() + (ptrdiff_t)i);
                changed = true;
                break;
            }
        }
    }
}

// Normalizes an input polygon: dedup, drop redundant collinear vertices, force CCW.
// The result may have 0, 1, 2 (degenerate input) or >= 3 vertices.
inline std::vector<Point> normalizeInput(std::vector<Point> p) {
    dedupAdjacent(p);
    removeCollinear(p);
    if (p.size() >= 3 && area2(p).sign() < 0) std::reverse(p.begin(), p.end());
    return p;
}

// True iff p lies in every half-plane of the CCW convex polygon h (boundary included).
inline bool insideConvex(const std::vector<Point>& h, const Point& p) {
    for (size_t i = 0; i < h.size(); ++i)
        if (orient(h[i], h[(i + 1) % h.size()], p) < 0) return false;
    return true;
}

// Normalizes raw clip output into the public result representation.
inline ClipResult classify(std::vector<Point> p) {
    dedupAdjacent(p);
    removeCollinear(p);
    ClipResult res;
    if (p.empty()) { res.kind = ClipKind::Empty; return res; }
    if (p.size() == 1) { res.kind = ClipKind::Point; res.verts = p; return res; }
    if (p.size() == 2) { res.kind = ClipKind::Segment; res.verts = p; return res; }
    Rational a2 = area2(p);
    if (a2.sign() == 0) {
        // Degenerate: all vertices collinear. The extremes are the segment ends.
        auto lex = [](const Point& u, const Point& v) {
            return u.x != v.x ? u.x < v.x : u.y < v.y;
        };
        auto lo = *std::min_element(p.begin(), p.end(), lex);
        auto hi = *std::max_element(p.begin(), p.end(), lex);
        if (lo == hi) { res.kind = ClipKind::Point; res.verts = {lo}; }
        else { res.kind = ClipKind::Segment; res.verts = {lo, hi}; }
        return res;
    }
    if (a2.sign() < 0) std::reverse(p.begin(), p.end());
    res.kind = ClipKind::Polygon;
    res.verts = std::move(p);
    return res;
}

// Clips the segment p0-p1 against the CCW convex polygon h (Cyrus-Beck, exact).
inline ClipResult clipSegment(const Point& p0, const Point& p1, const std::vector<Point>& h) {
    Rational t0(0), t1(1);
    Rational dx = p1.x - p0.x, dy = p1.y - p0.y;
    for (size_t i = 0; i < h.size(); ++i) {
        const Point& a = h[i];
        const Point& b = h[(i + 1) % h.size()];
        Rational ex = b.x - a.x, ey = b.y - a.y;
        Rational c0 = ex * (p0.y - a.y) - ey * (p0.x - a.x);
        Rational cd = ex * dy - ey * dx;
        int s = cd.sign();
        if (s == 0) {
            if (c0.sign() < 0) return {}; // parallel and outside
        } else {
            Rational t = -c0 / cd;
            if (s > 0) { if (t > t0) t0 = t; }
            else       { if (t < t1) t1 = t; }
        }
    }
    if (t0 > t1) return {};
    Point q0(p0.x + t0 * dx, p0.y + t0 * dy);
    Point q1(p0.x + t1 * dx, p0.y + t1 * dy);
    if (q0 == q1) return {ClipKind::Point, {q0}};
    return {ClipKind::Segment, {q0, q1}};
}

} // namespace detail

// Clips convex polygon `subject` against convex polygon `clip`.
// Inputs may be CW/CCW and may contain duplicate adjacent vertices and
// collinear edges. The intersection is returned as a ClipResult whose kind
// separates area (Polygon), line (Segment), point (Point) and empty outcomes.
inline ClipResult clipConvex(std::vector<Point> subject, std::vector<Point> clip) {
    std::vector<Point> sub = detail::normalizeInput(std::move(subject));
    std::vector<Point> cl = detail::normalizeInput(std::move(clip));

    // Degenerate inputs (0, 1 or 2 distinct vertices after normalization).
    if (sub.size() < 3 || cl.size() < 3) {
        if (sub.empty() || cl.empty()) return {};
        if (sub.size() == 1 && cl.size() == 1)
            return sub[0] == cl[0] ? ClipResult{ClipKind::Point, {sub[0]}} : ClipResult{};
        if (sub.size() == 1)
            return detail::insideConvex(cl, sub[0]) ? ClipResult{ClipKind::Point, {sub[0]}} : ClipResult{};
        if (cl.size() == 1)
            return detail::insideConvex(sub, cl[0]) ? ClipResult{ClipKind::Point, {cl[0]}} : ClipResult{};
        // One of them is a segment, the other a segment or polygon.
        if (sub.size() == 2 && cl.size() == 2) {
            // Intersect two segments exactly: clip sub against cl's line strip is
            // overkill; handle via endpoint tests plus crossing point.
            // Fall through to generic treatment below by lifting cl to a thin
            // check: use segment-segment intersection.
            const Point& a = sub[0]; const Point& b = sub[1];
            const Point& c = cl[0];  const Point& d = cl[1];
            int o1 = orient(a, b, c), o2 = orient(a, b, d);
            int o3 = orient(c, d, a), o4 = orient(c, d, b);
            if (o1 == 0 && o2 == 0) {
                // Collinear: intersect 1-D intervals via lexicographic order.
                auto lex = [](const Point& u, const Point& v) {
                    return u.x != v.x ? u.x < v.x : u.y < v.y;
                };
                Point lo1 = std::min(a, b, lex), hi1 = std::max(a, b, lex);
                Point lo2 = std::min(c, d, lex), hi2 = std::max(c, d, lex);
                Point lo = std::max(lo1, lo2, lex), hi = std::min(hi1, hi2, lex);
                if (lex(hi, lo)) return {};
                if (lo == hi) return {ClipKind::Point, {lo}};
                return {ClipKind::Segment, {lo, hi}};
            }
            if (o1 * o2 <= 0 && o3 * o4 <= 0) {
                // Proper or endpoint crossing.
                if (o1 == 0) return {ClipKind::Point, {c}};
                if (o2 == 0) return {ClipKind::Point, {d}};
                if (o3 == 0) return {ClipKind::Point, {a}};
                if (o4 == 0) return {ClipKind::Point, {b}};
                return {ClipKind::Point, {lineIntersect(a, b, c, d)}};
            }
            return {};
        }
        if (sub.size() == 2) return detail::clipSegment(sub[0], sub[1], cl);
        return detail::clipSegment(cl[0], cl[1], sub);
    }

    // Sutherland-Hodgman with exact arithmetic.
    std::vector<Point> out = sub;
    for (size_t i = 0; i < cl.size() && !out.empty(); ++i) {
        const Point& A = cl[i];
        const Point& B = cl[(i + 1) % cl.size()];
        std::vector<Point> in = std::move(out);
        out.clear();
        for (size_t j = 0; j < in.size(); ++j) {
            const Point& s = in[j];
            const Point& e = in[(j + 1) % in.size()];
            bool sin = orient(A, B, s) >= 0;
            bool ein = orient(A, B, e) >= 0;
            if (ein) {
                if (!sin) out.push_back(lineIntersect(s, e, A, B));
                out.push_back(e);
            } else if (sin) {
                out.push_back(lineIntersect(s, e, A, B));
            }
        }
    }
    return detail::classify(std::move(out));
}

// Independent check: every vertex of `verts` satisfies all half-planes of the
// convex polygon `poly` (boundary included). `poly` may be CW or CCW and may
// contain duplicate/collinear vertices.
inline bool satisfiesHalfPlanes(const std::vector<Point>& verts, std::vector<Point> poly) {
    std::vector<Point> h = detail::normalizeInput(std::move(poly));
    if (h.size() < 3) return true;
    for (const auto& v : verts)
        if (!detail::insideConvex(h, v)) return false;
    return true;
}

} // namespace exact
