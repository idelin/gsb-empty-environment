#include "geom.h"

#include <algorithm>
#include <stdexcept>

bool operator==(const Pt& a, const Pt& b) { return a.x == b.x && a.y == b.y; }
bool operator!=(const Pt& a, const Pt& b) { return !(a == b); }

std::string ptToString(const Pt& p) {
    return "(" + p.x.toString() + ", " + p.y.toString() + ")";
}

Rat cross2(const Pt& a, const Pt& b, const Pt& c) {
    return (b.x - a.x) * (c.y - a.y) - (b.y - a.y) * (c.x - a.x);
}

int orient(const Pt& a, const Pt& b, const Pt& c) { return cross2(a, b, c).sign(); }

static bool between(const Rat& v, const Rat& a, const Rat& b) {
    return (a <= v && v <= b) || (b <= v && v <= a);
}

bool onSegment(const Pt& p, const Pt& a, const Pt& b) {
    if (orient(a, b, p) != 0) return false;
    return between(p.x, a.x, b.x) && between(p.y, a.y, b.y);
}

bool properCross(const Pt& a, const Pt& b, const Pt& c, const Pt& d) {
    int o1 = orient(a, b, c), o2 = orient(a, b, d);
    int o3 = orient(c, d, a), o4 = orient(c, d, b);
    return o1 * o2 < 0 && o3 * o4 < 0;
}

bool segmentsIntersect(const Pt& a, const Pt& b, const Pt& c, const Pt& d) {
    int o1 = orient(a, b, c), o2 = orient(a, b, d);
    int o3 = orient(c, d, a), o4 = orient(c, d, b);
    if (o1 * o2 < 0 && o3 * o4 < 0) return true;
    if (o1 == 0 && onSegment(c, a, b)) return true;
    if (o2 == 0 && onSegment(d, a, b)) return true;
    if (o3 == 0 && onSegment(a, c, d)) return true;
    if (o4 == 0 && onSegment(b, c, d)) return true;
    return false;
}

Pt lineIntersection(const Pt& a, const Pt& b, const Pt& c, const Pt& d) {
    Rat dx1 = b.x - a.x, dy1 = b.y - a.y;
    Rat dx2 = d.x - c.x, dy2 = d.y - c.y;
    Rat denom = dx1 * dy2 - dy1 * dx2;
    if (denom.isZero()) throw std::runtime_error("lineIntersection: parallel lines");
    Rat t = ((c.x - a.x) * dy2 - (c.y - a.y) * dx2) / denom;
    return Pt{a.x + t * dx1, a.y + t * dy1};
}

void touchPoints(const Pt& a, const Pt& b, const Pt& c, const Pt& d,
                 std::vector<Pt>& out) {
    int o1 = orient(a, b, c), o2 = orient(a, b, d);
    if (o1 == 0 && o2 == 0) { // collinear: overlap endpoints are endpoints of either
        if (onSegment(c, a, b)) out.push_back(c);
        if (onSegment(d, a, b)) out.push_back(d);
        return;
    }
    int o3 = orient(c, d, a), o4 = orient(c, d, b);
    if (o1 * o2 < 0 && o3 * o4 < 0) {
        out.push_back(lineIntersection(a, b, c, d));
        return;
    }
    // Non-collinear, non-crossing: any common point is an endpoint of one
    // segment lying on the other.
    if (o1 == 0 && onSegment(c, a, b)) out.push_back(c);
    if (o2 == 0 && onSegment(d, a, b)) out.push_back(d);
    if (o3 == 0 && onSegment(a, c, d)) out.push_back(a);
    if (o4 == 0 && onSegment(b, c, d)) out.push_back(b);
}

std::vector<std::pair<Pt, Pt>> splitEdge(const Pt& a, const Pt& b,
                                         std::vector<Pt> pts) {
    pts.push_back(a);
    pts.push_back(b);
    std::vector<Pt> u;
    for (const Pt& p : pts) {
        if (!onSegment(p, a, b)) continue;
        bool dup = false;
        for (const Pt& q : u)
            if (q == p) {
                dup = true;
                break;
            }
        if (!dup) u.push_back(p);
    }
    // Points are collinear: lexicographic order is monotone along the line.
    std::sort(u.begin(), u.end(), [](const Pt& p, const Pt& q) {
        if (p.x != q.x) return p.x < q.x;
        return p.y < q.y;
    });
    std::vector<std::pair<Pt, Pt>> out;
    for (size_t i = 0; i + 1 < u.size(); ++i) out.push_back({u[i], u[i + 1]});
    return out;
}

Pt midpoint(const Pt& a, const Pt& b) {
    Rat half = Rat::ratio(1, 2);
    return Pt{(a.x + b.x) * half, (a.y + b.y) * half};
}

Loc locatePoint(const std::vector<Pt>& poly, const Pt& p) {
    size_t n = poly.size();
    for (size_t i = 0; i < n; ++i)
        if (onSegment(p, poly[i], poly[(i + 1) % n])) return Loc::ON_BOUNDARY;
    bool inside = false;
    for (size_t i = 0; i < n; ++i) {
        const Pt& a = poly[i];
        const Pt& b = poly[(i + 1) % n];
        if ((a.y > p.y) != (b.y > p.y)) {
            Rat xint = a.x + (p.y - a.y) * (b.x - a.x) / (b.y - a.y);
            if (xint > p.x) inside = !inside;
        }
    }
    return inside ? Loc::INSIDE : Loc::OUTSIDE;
}

bool strictlyInsideTriangle(const Pt& p, const Tri& t) {
    int o1 = orient(t.v[0], t.v[1], p);
    int o2 = orient(t.v[1], t.v[2], p);
    int o3 = orient(t.v[2], t.v[0], p);
    return (o1 > 0 && o2 > 0 && o3 > 0) || (o1 < 0 && o2 < 0 && o3 < 0);
}

Rat polygonDoubleArea(const std::vector<Pt>& poly) {
    Rat s(0);
    for (size_t i = 0; i < poly.size(); ++i) {
        const Pt& a = poly[i];
        const Pt& b = poly[(i + 1) % poly.size()];
        s = s + (a.x * b.y - b.x * a.y);
    }
    return s;
}
