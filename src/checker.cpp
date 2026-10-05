#include "checker.h"

static std::string segStr(const Pt& a, const Pt& b) {
    return ptToString(a) + " -> " + ptToString(b);
}

static std::pair<Pt, Pt> triEdge(const Tri& t, int k) {
    return {t.v[k], t.v[(k + 1) % 3]};
}
static const Pt& triOpp(const Tri& t, int k) { return t.v[(k + 2) % 3]; }

static void validatePolygon(const std::vector<Pt>& poly,
                            std::vector<Diagnostic>& D) {
    size_t n = poly.size();
    if (n < 3) {
        D.push_back({"polygon", "polygon has fewer than 3 vertices"});
        return;
    }
    for (size_t i = 0; i < n; ++i)
        if (poly[i] == poly[(i + 1) % n])
            D.push_back({"polygon", "zero-length edge at vertex " +
                                        std::to_string(i) + " " +
                                        ptToString(poly[i])});
    if (polygonDoubleArea(poly).isZero())
        D.push_back({"polygon", "polygon has zero signed area"});
    if (!D.empty()) return;
    for (size_t i = 0; i < n; ++i) {
        for (size_t j = i + 1; j < n; ++j) {
            bool adjacent = (j == i + 1) || (i == 0 && j == n - 1);
            Pt a = poly[i], b = poly[(i + 1) % n];
            Pt c = poly[j], d = poly[(j + 1) % n];
            if (adjacent) {
                // Shared vertex plus the two free endpoints; a spike means
                // the free endpoints are collinear with and on opposite
                // sides of (or coincident with) the shared vertex.
                Pt shared, free1, free2;
                if (j == i + 1) { // edges (i,i+1) and (i+1,i+2)
                    shared = b; free1 = a; free2 = d;
                } else { // wrap-around pair (0, n-1): shared vertex poly[0]
                    shared = a; free1 = b; free2 = c;
                }
                if (orient(free1, shared, free2) == 0 &&
                    (onSegment(free1, shared, free2) ||
                     onSegment(free2, shared, free1)))
                    D.push_back({"polygon", "consecutive edges " +
                                              std::to_string(i) + " and " +
                                              std::to_string(j) +
                                              " overlap (spike)"});
            } else if (segmentsIntersect(a, b, c, d)) {
                D.push_back({"polygon", "edges " + std::to_string(i) + " " +
                                            segStr(a, b) + " and " +
                                            std::to_string(j) + " " +
                                            segStr(c, d) +
                                            " intersect: not a simple polygon"});
            }
        }
    }
}

// Detect positive-area intersection of two non-degenerate triangles.
static void checkPairOverlap(size_t i, const Tri& A, size_t j, const Tri& B,
                             std::vector<Diagnostic>& D) {
    std::string ti = std::to_string(i), tj = std::to_string(j);
    // 1. Proper edge crossings.
    for (int ea = 0; ea < 3; ++ea) {
        auto a = triEdge(A, ea);
        for (int eb = 0; eb < 3; ++eb) {
            auto b = triEdge(B, eb);
            if (properCross(a.first, a.second, b.first, b.second)) {
                D.push_back({"overlap",
                             "triangles " + ti + " and " + tj +
                                 " overlap: edge " + segStr(a.first, a.second) +
                                 " crosses edge " + segStr(b.first, b.second) +
                                 " at interior point " +
                                 ptToString(lineIntersection(
                                     a.first, a.second, b.first, b.second))});
                return;
            }
        }
    }
    // 2. Vertex strictly inside the other triangle.
    for (int k = 0; k < 3; ++k) {
        if (strictlyInsideTriangle(A.v[k], B)) {
            D.push_back({"overlap", "triangles " + ti + " and " + tj +
                                        " overlap: vertex " +
                                        ptToString(A.v[k]) + " of triangle " +
                                        ti + " lies strictly inside triangle " +
                                        tj});
            return;
        }
        if (strictlyInsideTriangle(B.v[k], A)) {
            D.push_back({"overlap", "triangles " + ti + " and " + tj +
                                        " overlap: vertex " +
                                        ptToString(B.v[k]) + " of triangle " +
                                        tj + " lies strictly inside triangle " +
                                        ti});
            return;
        }
    }
    // 3. Collinear edge overlap with both triangles on the same side.
    for (int ea = 0; ea < 3; ++ea) {
        auto a = triEdge(A, ea);
        for (int eb = 0; eb < 3; ++eb) {
            auto b = triEdge(B, eb);
            if (orient(a.first, a.second, b.first) != 0 ||
                orient(a.first, a.second, b.second) != 0)
                continue;
            std::vector<Pt> shared;
            auto add = [&](const Pt& p) {
                for (const Pt& q : shared)
                    if (q == p) return;
                shared.push_back(p);
            };
            if (onSegment(a.first, b.first, b.second)) add(a.first);
            if (onSegment(a.second, b.first, b.second)) add(a.second);
            if (onSegment(b.first, a.first, a.second)) add(b.first);
            if (onSegment(b.second, a.first, a.second)) add(b.second);
            if (shared.size() >= 2) { // positive-length coincidence
                int sA = orient(a.first, a.second, triOpp(A, ea));
                int sB = orient(a.first, a.second, triOpp(B, eb));
                if (sA == sB) { // both are nonzero (non-degenerate)
                    D.push_back({"overlap",
                                 "triangles " + ti + " and " + tj +
                                     " overlap: edges coincide on " +
                                     segStr(shared[0], shared[1]) +
                                     " and both triangles lie on the same "
                                     "side of it"});
                    return;
                }
            }
        }
    }
    // 4. Vertex of one on the interior of the other's edge, same side.
    for (int pass = 0; pass < 2; ++pass) {
        const Tri& T1 = pass == 0 ? A : B;
        const Tri& T2 = pass == 0 ? B : A;
        size_t t1 = pass == 0 ? i : j, t2 = pass == 0 ? j : i;
        for (int kv = 0; kv < 3; ++kv) {
            for (int ke = 0; ke < 3; ++ke) {
                auto e = triEdge(T2, ke);
                const Pt& v = T1.v[kv];
                if (v == e.first || v == e.second) continue;
                if (!onSegment(v, e.first, e.second)) continue;
                int s2 = orient(e.first, e.second, triOpp(T2, ke));
                for (int w = 0; w < 3; ++w) {
                    if (w == kv) continue;
                    if (orient(e.first, e.second, T1.v[w]) == s2) {
                        D.push_back({"overlap",
                                     "triangles " + std::to_string(t1) +
                                         " and " + std::to_string(t2) +
                                         " overlap: vertex " + ptToString(v) +
                                         " of triangle " + std::to_string(t1) +
                                         " lies on edge " +
                                         segStr(e.first, e.second) +
                                         " of triangle " + std::to_string(t2) +
                                         " with both interiors on the same "
                                         "side"});
                        return;
                    }
                }
            }
        }
    }
}

CheckResult checkTriangulation(const std::vector<Pt>& poly,
                               const std::vector<Tri>& tris) {
    CheckResult res;
    auto& D = res.diagnostics;
    const size_t n = poly.size();
    const size_t m = tris.size();

    Rat polyArea2 = absRat(polygonDoubleArea(poly));
    res.polygonArea = halveRat(polyArea2);
    Rat triArea2Sum(0);
    for (const Tri& t : tris)
        triArea2Sum = triArea2Sum + absRat(cross2(t.v[0], t.v[1], t.v[2]));
    res.triangleAreaSum = halveRat(triArea2Sum);

    // 0. The input must be a simple polygon.
    validatePolygon(poly, D);
    if (!D.empty()) return res;

    // 1. Triangles must be non-degenerate.
    std::vector<bool> ok(m, true);
    for (size_t i = 0; i < m; ++i) {
        if (orient(tris[i].v[0], tris[i].v[1], tris[i].v[2]) == 0) {
            ok[i] = false;
            D.push_back({"degenerate", "triangle " + std::to_string(i) +
                                           " has zero area (collinear or "
                                           "coincident vertices)"});
        }
    }

    // 2. Containment: every triangle must lie inside the polygon.
    for (size_t i = 0; i < m; ++i) {
        if (!ok[i]) continue;
        const Tri& t = tris[i];
        for (int k = 0; k < 3; ++k)
            if (locatePoint(poly, t.v[k]) == Loc::OUTSIDE)
                D.push_back({"outside", "vertex " + ptToString(t.v[k]) +
                                            " of triangle " +
                                            std::to_string(i) +
                                            " lies outside the polygon"});
        for (int k = 0; k < 3; ++k) {
            auto e = triEdge(t, k);
            for (size_t pe = 0; pe < n; ++pe) {
                Pt c = poly[pe], d = poly[(pe + 1) % n];
                if (properCross(e.first, e.second, c, d))
                    D.push_back({"crossing",
                                 "edge " + segStr(e.first, e.second) +
                                     " of triangle " + std::to_string(i) +
                                     " crosses polygon edge " + segStr(c, d) +
                                     " at " +
                                     ptToString(lineIntersection(
                                         e.first, e.second, c, d))});
            }
            // Split the edge at every polygon touch point and sample each
            // elementary piece: catches excursions that slip out through a
            // polygon vertex without properly crossing any edge.
            std::vector<Pt> pts;
            for (size_t pe = 0; pe < n; ++pe)
                touchPoints(e.first, e.second, poly[pe], poly[(pe + 1) % n],
                            pts);
            for (auto& sub : splitEdge(e.first, e.second, pts)) {
                Pt mid = midpoint(sub.first, sub.second);
                if (locatePoint(poly, mid) == Loc::OUTSIDE)
                    D.push_back({"outside",
                                 "edge " + segStr(sub.first, sub.second) +
                                     " of triangle " + std::to_string(i) +
                                     " lies outside the polygon (midpoint " +
                                     ptToString(mid) + ")"});
            }
        }
    }

    // 3. Pairwise interior overlap.
    for (size_t i = 0; i < m; ++i) {
        if (!ok[i]) continue;
        for (size_t j = i + 1; j < m; ++j) {
            if (!ok[j]) continue;
            checkPairOverlap(i, tris[i], j, tris[j], D);
        }
    }

    // 4. Boundary coverage: every elementary piece of the polygon boundary
    //    must be covered by some triangle edge.
    for (size_t pe = 0; pe < n; ++pe) {
        Pt c = poly[pe], d = poly[(pe + 1) % n];
        std::vector<Pt> pts;
        for (size_t i = 0; i < m; ++i) {
            if (!ok[i]) continue;
            for (int k = 0; k < 3; ++k) {
                auto e = triEdge(tris[i], k);
                touchPoints(c, d, e.first, e.second, pts);
            }
        }
        for (auto& sub : splitEdge(c, d, pts)) {
            Pt mid = midpoint(sub.first, sub.second);
            bool covered = false;
            for (size_t i = 0; i < m && !covered; ++i) {
                if (!ok[i]) continue;
                for (int k = 0; k < 3; ++k) {
                    auto e = triEdge(tris[i], k);
                    if (onSegment(mid, e.first, e.second)) {
                        covered = true;
                        break;
                    }
                }
            }
            if (!covered)
                D.push_back({"boundary-gap",
                             "polygon boundary segment " +
                                 segStr(sub.first, sub.second) + " (edge " +
                                 std::to_string(pe) +
                                 ") is not covered by any triangle edge"});
        }
    }

    // 5. Interior edge pairing: every elementary interior edge piece must be
    //    shared by exactly one other triangle on the opposite side. An
    //    unmatched piece borders an uncovered region (hole evidence).
    for (size_t i = 0; i < m; ++i) {
        if (!ok[i]) continue;
        for (int k = 0; k < 3; ++k) {
            auto e = triEdge(tris[i], k);
            std::vector<Pt> pts;
            for (size_t pe = 0; pe < n; ++pe)
                touchPoints(e.first, e.second, poly[pe], poly[(pe + 1) % n],
                            pts);
            for (size_t j = 0; j < m; ++j) {
                if (!ok[j] || j == i) continue;
                for (int k2 = 0; k2 < 3; ++k2) {
                    auto e2 = triEdge(tris[j], k2);
                    touchPoints(e.first, e.second, e2.first, e2.second, pts);
                }
            }
            for (auto& sub : splitEdge(e.first, e.second, pts)) {
                Pt mid = midpoint(sub.first, sub.second);
                if (locatePoint(poly, mid) != Loc::INSIDE) continue;
                int sideI = orient(sub.first, sub.second, triOpp(tris[i], k));
                int oppPartners = 0, samePartners = 0;
                size_t sameIdx = 0;
                for (size_t j = 0; j < m; ++j) {
                    if (!ok[j] || j == i) continue;
                    for (int k2 = 0; k2 < 3; ++k2) {
                        auto e2 = triEdge(tris[j], k2);
                        if (onSegment(sub.first, e2.first, e2.second) &&
                            onSegment(sub.second, e2.first, e2.second)) {
                            int sideJ = orient(sub.first, sub.second,
                                               triOpp(tris[j], k2));
                            if (sideJ * sideI < 0)
                                ++oppPartners;
                            else if (sideJ * sideI > 0) {
                                ++samePartners;
                                sameIdx = j;
                            }
                        }
                    }
                }
                if (oppPartners == 0)
                    D.push_back({"hole",
                                 "interior edge " +
                                     segStr(sub.first, sub.second) +
                                     " of triangle " + std::to_string(i) +
                                     " has no triangle on its opposite side: "
                                     "it borders an uncovered region (hole)"});
                if (oppPartners > 1)
                    D.push_back({"overlap",
                                 "interior edge " +
                                     segStr(sub.first, sub.second) +
                                     " of triangle " + std::to_string(i) +
                                     " is matched by " +
                                     std::to_string(oppPartners) +
                                     " triangles on its opposite side"});
                if (samePartners > 0)
                    D.push_back({"overlap",
                                 "interior edge " +
                                     segStr(sub.first, sub.second) +
                                     " of triangle " + std::to_string(i) +
                                     " is duplicated on the same side by "
                                     "triangle " +
                                     std::to_string(sameIdx)});
            }
        }
    }

    // 6. Area consistency (necessary but, by itself, NOT sufficient).
    if (triArea2Sum != polyArea2)
        D.push_back({"area-mismatch",
                     "triangle areas sum to " + res.triangleAreaSum.toString() +
                         " but polygon area is " +
                         res.polygonArea.toString()});

    res.valid = D.empty();
    return res;
}
