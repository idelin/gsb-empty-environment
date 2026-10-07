// Exact planar geometry over rational points.
#pragma once
#include "exact.h"
#include <string>

struct Point {
    Rat x, y;
    std::string str() const { return "(" + x.toString() + "," + y.toString() + ")"; }
};

// sign of cross product (b-a) x (c-a): >0 left turn, 0 collinear, <0 right turn
static int orient(const Point& a, const Point& b, const Point& c) {
    Rat v = (b.x - a.x) * (c.y - a.y) - (b.y - a.y) * (c.x - a.x);
    return v.sign();
}

// twice signed area of polygon (shoelace), exact
static Rat signedArea2(const std::vector<Point>& poly) {
    Rat s(0);
    for (size_t i = 0; i < poly.size(); ++i) {
        const Point& a = poly[i];
        const Point& b = poly[(i + 1) % poly.size()];
        s = s + (a.x * b.y - a.y * b.x);
    }
    return s;
}

// p known collinear with ab: check p within bounding box
static bool withinBox(const Point& a, const Point& b, const Point& p) {
    const Rat& loX = Rat::cmp(a.x, b.x) <= 0 ? a.x : b.x;
    const Rat& hiX = Rat::cmp(a.x, b.x) <= 0 ? b.x : a.x;
    const Rat& loY = Rat::cmp(a.y, b.y) <= 0 ? a.y : b.y;
    const Rat& hiY = Rat::cmp(a.y, b.y) <= 0 ? b.y : a.y;
    return Rat::cmp(loX, p.x) <= 0 && Rat::cmp(p.x, hiX) <= 0 &&
           Rat::cmp(loY, p.y) <= 0 && Rat::cmp(p.y, hiY) <= 0;
}

static bool pointEq(const Point& a, const Point& b) { return a.x == b.x && a.y == b.y; }

// proper crossing: interiors of both segments meet at a single point (non-collinear)
static bool properCross(const Point& a, const Point& b, const Point& c, const Point& d) {
    int o1 = orient(a, b, c), o2 = orient(a, b, d);
    int o3 = orient(c, d, a), o4 = orient(c, d, b);
    return o1 * o2 < 0 && o3 * o4 < 0;
}

// any intersection at all (including endpoint touches and collinear overlaps)
static bool anyIntersect(const Point& a, const Point& b, const Point& c, const Point& d) {
    int o1 = orient(a, b, c), o2 = orient(a, b, d);
    int o3 = orient(c, d, a), o4 = orient(c, d, b);
    if (o1 * o2 < 0 && o3 * o4 < 0) return true;
    if (o1 == 0 && withinBox(a, b, c)) return true;
    if (o2 == 0 && withinBox(a, b, d)) return true;
    if (o3 == 0 && withinBox(c, d, a)) return true;
    if (o4 == 0 && withinBox(c, d, b)) return true;
    return false;
}

// collinear segments overlap with positive length
static bool collinearOverlap(const Point& a, const Point& b, const Point& c, const Point& d) {
    if (orient(a, b, c) != 0 || orient(a, b, d) != 0) return false;
    // 1D overlap on x (or y if vertical)
    bool vert = (a.x == b.x);
    const Rat& a1 = vert ? a.y : a.x; const Rat& a2 = vert ? b.y : b.x;
    const Rat& c1 = vert ? c.y : c.x; const Rat& c2 = vert ? d.y : d.x;
    const Rat& lo1 = Rat::cmp(a1, a2) <= 0 ? a1 : a2;
    const Rat& hi1 = Rat::cmp(a1, a2) <= 0 ? a2 : a1;
    const Rat& lo2 = Rat::cmp(c1, c2) <= 0 ? c1 : c2;
    const Rat& hi2 = Rat::cmp(c1, c2) <= 0 ? c2 : c1;
    const Rat& lo = Rat::cmp(lo1, lo2) >= 0 ? lo1 : lo2;
    const Rat& hi = Rat::cmp(hi1, hi2) <= 0 ? hi1 : hi2;
    return Rat::cmp(lo, hi) < 0;
}

// Normalized supporting line of a segment: primitive integer direction (dx,dy)
// with sign convention, plus offset c = dx*p.y - dy*p.x (constant on the line).
struct LineKey {
    BigInt dx, dy;
    Rat c;
};
struct LineKeyLess {
    bool operator()(const LineKey& a, const LineKey& b) const {
        int c1 = BigInt::cmp(a.dx, b.dx); if (c1) return c1 < 0;
        int c2 = BigInt::cmp(a.dy, b.dy); if (c2) return c2 < 0;
        return Rat::cmp(a.c, b.c) < 0;
    }
};

static LineKey lineOf(const Point& p, const Point& q) {
    Rat rx = q.x - p.x, ry = q.y - p.y;
    // scale to integers
    BigInt L = Rat(BigInt(1), BigInt(1)).den; // 1
    L = rx.den * ry.den / BigInt::gcd(rx.den, ry.den);
    BigInt dx = rx.num * (L / rx.den);
    BigInt dy = ry.num * (L / ry.den);
    BigInt g = BigInt::gcd(dx.abs(), dy.abs());
    if (!g.isZero()) { dx = dx / g; dy = dy / g; }
    if (dx.sign < 0 || (dx.sign == 0 && dy.sign < 0)) { dx = -dx; dy = -dy; }
    LineKey k; k.dx = dx; k.dy = dy;
    k.c = Rat(dx, BigInt(1)) * p.y - Rat(dy, BigInt(1)) * p.x;
    return k;
}

// 1D coordinate of point on a line with integer direction d: t = p . d
static Rat lineCoord(const Point& p, const BigInt& dx, const BigInt& dy) {
    return p.x * Rat(dx, BigInt(1)) + p.y * Rat(dy, BigInt(1));
}
