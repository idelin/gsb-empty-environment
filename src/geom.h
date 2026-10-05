#pragma once

// Exact 2D geometry over rational points.

#include "rat.h"
#include <string>
#include <utility>
#include <vector>

struct Pt {
    Rat x, y;
};

bool operator==(const Pt& a, const Pt& b);
bool operator!=(const Pt& a, const Pt& b);
std::string ptToString(const Pt& p);

struct Tri {
    Pt v[3];
};

// Signed double area of triangle abc (as a Rat; sign gives orientation).
Rat cross2(const Pt& a, const Pt& b, const Pt& c);
// Sign of cross2: +1 ccw, -1 cw, 0 collinear.
int orient(const Pt& a, const Pt& b, const Pt& c);

// p lies on closed segment ab.
bool onSegment(const Pt& p, const Pt& a, const Pt& b);
// Segments cross at a point interior to both.
bool properCross(const Pt& a, const Pt& b, const Pt& c, const Pt& d);
// Segments share at least one point.
bool segmentsIntersect(const Pt& a, const Pt& b, const Pt& c, const Pt& d);
// Intersection point of two non-parallel lines (throws if parallel).
Pt lineIntersection(const Pt& a, const Pt& b, const Pt& c, const Pt& d);

// Append to `out` every point where segment cd "touches" segment ab:
// proper crossing point, endpoints of one lying on the other, and (for
// collinear segments) the overlap endpoints. Used to split edges into
// elementary sub-segments.
void touchPoints(const Pt& a, const Pt& b, const Pt& c, const Pt& d,
                 std::vector<Pt>& out);

// Split segment ab at the given points (endpoints are added automatically)
// into elementary consecutive sub-segments.
std::vector<std::pair<Pt, Pt>> splitEdge(const Pt& a, const Pt& b,
                                         std::vector<Pt> pts);

Pt midpoint(const Pt& a, const Pt& b);

enum class Loc { OUTSIDE, ON_BOUNDARY, INSIDE };
// Exact point-in-polygon (ray casting; boundary reported separately).
Loc locatePoint(const std::vector<Pt>& poly, const Pt& p);

bool strictlyInsideTriangle(const Pt& p, const Tri& t);

// Signed double area of the polygon.
Rat polygonDoubleArea(const std::vector<Pt>& poly);
