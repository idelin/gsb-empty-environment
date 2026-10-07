// Triangulation checker: verifies that candidate triangles exactly tile a
// simple polygon (rational vertices, collinear edges allowed, extra interior
// Steiner points allowed) without overlaps, crossings, or holes.
//
// Checks (exact rational arithmetic throughout):
//   1. polygon sanity: non-degenerate, simple (no self-intersections)
//   2. every triangle non-degenerate
//   3. no proper crossings: triangle/triangle edges, triangle/polygon edges
//   4. boundary coverage: every polygon boundary point covered by exactly 1 triangle edge
//   5. interior pairing: every interior edge segment covered by exactly 2 triangle edges
//   6. area equality: sum of triangle areas == polygon area
// Area equality alone is NOT sufficient (equal-area overlap+hole fakes exist);
// steps 3-5 catch those. Conversely step 6 catches "folded" double-covers
// whose edges all pair up.
//
// Input (stdin or file argv[1]):
//   N
//   x1 y1 ... (N lines, polygon vertices in order; numbers: int, a/b, or decimal)
//   M
//   ax ay bx by cx cy ... (M lines, one triangle per line)
// Output: "VALID" or "INVALID" followed by concrete evidence lines.
// Exit code: 0 valid, 1 invalid, 2 input/usage error.

#include "exact.h"
#include "geom.h"
#include <iostream>
#include <fstream>
#include <sstream>
#include <vector>
#include <array>
#include <map>
#include <set>
#include <string>
#include <algorithm>

struct Edge {
    Point a, b;
    int owner;   // triangle index, or -1 for polygon
    int index;   // edge index within owner
};

struct Interval {
    Rat lo, hi;
};

struct LineData {
    BigInt dx, dy;
    Point ref;                 // a point on the line
    std::vector<Interval> tri; // triangle-edge intervals (1D coords)
    std::vector<Interval> bnd; // polygon-edge intervals
};

static std::vector<std::string> issues;

static void addInterval(std::map<LineKey, LineData, LineKeyLess>& lines,
                        const Point& a, const Point& b, bool boundary) {
    LineKey k = lineOf(a, b);
    auto it = lines.find(k);
    if (it == lines.end()) {
        LineData d; d.dx = k.dx; d.dy = k.dy; d.ref = a;
        it = lines.emplace(k, d).first;
    }
    LineData& d = it->second;
    Rat t1 = lineCoord(a, d.dx, d.dy);
    Rat t2 = lineCoord(b, d.dx, d.dy);
    Interval iv{ Rat::cmp(t1, t2) <= 0 ? t1 : t2, Rat::cmp(t1, t2) <= 0 ? t2 : t1 };
    (boundary ? d.bnd : d.tri).push_back(iv);
}

// reconstruct the point on the line with 1D coordinate t
static Point pointAt(const LineData& d, const Rat& t) {
    Rat dd = Rat(d.dx * d.dx + d.dy * d.dy, BigInt(1));
    Rat t0 = lineCoord(d.ref, d.dx, d.dy);
    Rat k = (t - t0) / dd;
    return { d.ref.x + Rat(d.dx, BigInt(1)) * k, d.ref.y + Rat(d.dy, BigInt(1)) * k };
}

static size_t countCover(const std::vector<Interval>& v, const Rat& lo, const Rat& hi) {
    Rat mid = (lo + hi) / Rat(2);
    size_t c = 0;
    for (const Interval& iv : v)
        if (Rat::cmp(iv.lo, mid) <= 0 && Rat::cmp(mid, iv.hi) <= 0) ++c;
    return c;
}

int main(int argc, char** argv) {
    std::istream* in = &std::cin;
    std::ifstream fin;
    if (argc > 1) {
        fin.open(argv[1]);
        if (!fin) { std::cerr << "cannot open " << argv[1] << "\n"; return 2; }
        in = &fin;
    }
    std::vector<std::string> tok;
    {
        std::string s;
        while (*in >> s) tok.push_back(s);
    }
    size_t pos = 0;
    auto next = [&]() -> std::string {
        if (pos >= tok.size()) { std::cerr << "unexpected end of input\n"; exit(2); }
        return tok[pos++];
    };
    int n;
    try { n = std::stoi(next()); } catch (...) { std::cerr << "bad vertex count\n"; return 2; }
    if (n < 3) { std::cerr << "polygon needs >= 3 vertices\n"; return 2; }
    std::vector<Point> poly(n);
    try {
        for (int i = 0; i < n; ++i) { poly[i].x = Rat::parse(next()); poly[i].y = Rat::parse(next()); }
    } catch (const std::exception& e) { std::cerr << "parse error: " << e.what() << "\n"; return 2; }
    int m;
    try { m = std::stoi(next()); } catch (...) { std::cerr << "bad triangle count\n"; return 2; }
    std::vector<std::array<Point, 3>> tris(m);
    try {
        for (int i = 0; i < m; ++i)
            for (int j = 0; j < 3; ++j) { tris[i][j].x = Rat::parse(next()); tris[i][j].y = Rat::parse(next()); }
    } catch (const std::exception& e) { std::cerr << "parse error: " << e.what() << "\n"; return 2; }

    // ---- 1. polygon sanity -------------------------------------------------
    Rat polyArea2 = signedArea2(poly);
    if (polyArea2.isZero())
        issues.push_back("polygon has zero signed area (degenerate input)");
    for (int i = 0; i < n; ++i) {
        const Point& a = poly[i];
        const Point& b = poly[(i + 1) % n];
        if (pointEq(a, b))
            issues.push_back("polygon edge " + std::to_string(i) + " has zero length at " + a.str());
    }
    for (int i = 0; i < n; ++i) {
        Point a = poly[i], b = poly[(i + 1) % n];
        for (int j = i + 1; j < n; ++j) {
            Point c = poly[j], d = poly[(j + 1) % n];
            bool adjacent = (j == (i + 1) % n) || (i == (j + 1) % n);
            if (adjacent) {
                if (collinearOverlap(a, b, c, d))
                    issues.push_back("adjacent polygon edges " + std::to_string(i) + " and " +
                                     std::to_string(j) + " overlap (backtracking boundary)");
            } else if (anyIntersect(a, b, c, d)) {
                issues.push_back("polygon self-intersection: edge " + std::to_string(i) + " " +
                                 a.str() + "--" + b.str() + " meets edge " + std::to_string(j) +
                                 " " + c.str() + "--" + d.str());
            }
        }
    }

    // ---- 2. degenerate triangles -------------------------------------------
    std::vector<bool> good(m, true);
    for (int i = 0; i < m; ++i) {
        if (orient(tris[i][0], tris[i][1], tris[i][2]) == 0) {
            issues.push_back("degenerate triangle #" + std::to_string(i) + " (zero area): " +
                             tris[i][0].str() + " " + tris[i][1].str() + " " + tris[i][2].str());
            good[i] = false;
        }
    }

    // collect edges
    std::vector<Edge> triEdges, polyEdges;
    for (int i = 0; i < m; ++i) {
        if (!good[i]) continue;
        for (int e = 0; e < 3; ++e)
            triEdges.push_back({ tris[i][e], tris[i][(e + 1) % 3], i, e });
    }
    for (int i = 0; i < n; ++i)
        polyEdges.push_back({ poly[i], poly[(i + 1) % n], -1, i });

    // ---- 3. proper crossings ------------------------------------------------
    for (size_t i = 0; i < triEdges.size(); ++i)
        for (size_t j = i + 1; j < triEdges.size(); ++j) {
            if (triEdges[i].owner == triEdges[j].owner) continue;
            if (properCross(triEdges[i].a, triEdges[i].b, triEdges[j].a, triEdges[j].b))
                issues.push_back("crossing edges: triangle " + std::to_string(triEdges[i].owner) +
                                 " edge " + triEdges[i].a.str() + "--" + triEdges[i].b.str() +
                                 " crosses triangle " + std::to_string(triEdges[j].owner) +
                                 " edge " + triEdges[j].a.str() + "--" + triEdges[j].b.str());
        }
    for (const Edge& te : triEdges)
        for (const Edge& pe : polyEdges)
            if (properCross(te.a, te.b, pe.a, pe.b))
                issues.push_back("triangle " + std::to_string(te.owner) + " edge " + te.a.str() +
                                 "--" + te.b.str() + " crosses polygon boundary edge " +
                                 std::to_string(pe.index) + " " + pe.a.str() + "--" + pe.b.str() +
                                 " (triangle leaks outside)");

    // ---- 4+5. line-sweep coverage: boundary coverage & interior pairing -----
    std::map<LineKey, LineData, LineKeyLess> lines;
    for (const Edge& e : triEdges) addInterval(lines, e.a, e.b, false);
    for (const Edge& e : polyEdges) if (!pointEq(e.a, e.b)) addInterval(lines, e.a, e.b, true);

    for (auto& kv : lines) {
        LineData& d = kv.second;
        std::vector<Rat> ts;
        for (const Interval& iv : d.tri) { ts.push_back(iv.lo); ts.push_back(iv.hi); }
        for (const Interval& iv : d.bnd) { ts.push_back(iv.lo); ts.push_back(iv.hi); }
        std::sort(ts.begin(), ts.end(), [](const Rat& a, const Rat& b) { return Rat::cmp(a, b) < 0; });
        ts.erase(std::unique(ts.begin(), ts.end(), [](const Rat& a, const Rat& b) { return a == b; }), ts.end());
        for (size_t i = 0; i + 1 < ts.size(); ++i) {
            size_t tc = countCover(d.tri, ts[i], ts[i + 1]);
            size_t bc = countCover(d.bnd, ts[i], ts[i + 1]);
            if (tc == 0 && bc == 0) continue;
            Point p = pointAt(d, ts[i]), q = pointAt(d, ts[i + 1]);
            std::string seg = p.str() + "--" + q.str();
            if (bc > 0) {
                if (tc == 0)
                    issues.push_back("uncovered boundary segment " + seg +
                                     " (hole reaching the polygon boundary)");
                else if (tc > 1)
                    issues.push_back("boundary segment " + seg + " covered by " +
                                     std::to_string(tc) + " triangle edges, expected exactly 1");
            } else {
                if (tc == 1)
                    issues.push_back("unpaired interior edge segment " + seg +
                                     " covered by 1 triangle edge, expected 2 (hole or dangling edge)");
                else if (tc > 2)
                    issues.push_back("interior segment " + seg + " covered by " +
                                     std::to_string(tc) + " triangle edges, expected 2 (overlap)");
            }
        }
    }

    // ---- 6. area equality ----------------------------------------------------
    Rat triArea2(0);
    for (int i = 0; i < m; ++i) {
        if (!good[i]) continue;
        const Point& a = tris[i][0]; const Point& b = tris[i][1]; const Point& c = tris[i][2];
        Rat s = (b.x - a.x) * (c.y - a.y) - (b.y - a.y) * (c.x - a.x);
        triArea2 = triArea2 + s.abs();
    }
    if (triArea2 != polyArea2.abs())
        issues.push_back("area mismatch: sum of triangle areas = " + (triArea2 / Rat(2)).toString() +
                         ", polygon area = " + (polyArea2.abs() / Rat(2)).toString());

    // ---- verdict --------------------------------------------------------------
    if (issues.empty()) {
        std::cout << "VALID\n";
        std::cout << "triangles: " << m << ", polygon area: " << (polyArea2.abs() / Rat(2)).toString() << "\n";
        return 0;
    }
    std::cout << "INVALID\n";
    for (const std::string& s : issues) std::cout << "  " << s << "\n";
    return 1;
}
