// stroke_rasterizer.hpp
//
// C++17 header-only library that rasterizes a stroked (widened) polyline into
// a pixel bitmap.
//
// Conventions:
//  - Pixel (x, y) is covered iff its center (x + 0.5, y + 0.5) lies inside the
//    stroke. All shape tests are inclusive on the boundary, so adjacent shapes
//    that share an edge leave no cracks between them.
//  - Round joins/caps compare squared distances (dx*dx + dy*dy <= r*r); no
//    square root is taken in the rasterization loops.
//  - Miter joins are kept while miterLength <= miterLimit * width (the SVG
//    miterlimit convention); otherwise the join falls back to bevel.
//  - Zero-length segments (consecutive coincident points) are dropped. A fully
//    degenerate path renders as a cap-shaped dot (round/square) or nothing
//    (butt, matching the PostScript convention).
//  - A 180-degree foldback renders as a semicircular cap for round joins and
//    as a bevel cap for bevel/miter joins (the miter would be infinitely long).
//  - Coverage is accumulated into a bitmap with OR semantics, so overlapping
//    segments and joins can never double-count pixels.

#pragma once

#include <algorithm>
#include <cmath>
#include <cstddef>
#include <cstdint>
#include <utility>
#include <vector>

namespace stroke {

struct Point {
    double x = 0.0;
    double y = 0.0;
};

inline Point operator+(Point a, Point b) { return {a.x + b.x, a.y + b.y}; }
inline Point operator-(Point a, Point b) { return {a.x - b.x, a.y - b.y}; }
inline Point operator-(Point a) { return {-a.x, -a.y}; }
inline Point operator*(Point a, double s) { return {a.x * s, a.y * s}; }
inline double dot(Point a, Point b) { return a.x * b.x + a.y * b.y; }
inline double cross(Point a, Point b) { return a.x * b.y - a.y * b.x; }
inline double squaredLength(Point a) { return dot(a, a); }
inline double length(Point a) { return std::sqrt(squaredLength(a)); }

enum class JoinStyle { kMiter, kRound, kBevel };
enum class CapStyle { kButt, kSquare, kRound };

struct StrokeOptions {
    double width = 1.0;                 // stroke width, same units as the coordinates
    JoinStyle join = JoinStyle::kMiter; // join between consecutive segments
    CapStyle cap = CapStyle::kButt;     // cap at the two open ends
    double miterLimit = 4.0;            // miter kept while miterLength <= miterLimit * width
};

// A coverage bitmap. set() is idempotent, which is what guarantees that
// overlapping stroke parts never produce duplicate pixels.
class Bitmap {
  public:
    Bitmap(int width, int height)
        : width_(width), height_(height),
          cells_(static_cast<std::size_t>(width) * static_cast<std::size_t>(height), 0) {}

    int width() const { return width_; }
    int height() const { return height_; }

    void set(int x, int y) {
        if (x >= 0 && x < width_ && y >= 0 && y < height_)
            cells_[index(x, y)] = 1;
    }

    bool get(int x, int y) const {
        return x >= 0 && x < width_ && y >= 0 && y < height_ && cells_[index(x, y)] != 0;
    }

    std::size_t count() const {
        std::size_t n = 0;
        for (std::uint8_t c : cells_) n += c;
        return n;
    }

    // All covered pixels in row-major order; unique by construction.
    std::vector<std::pair<int, int>> pixels() const {
        std::vector<std::pair<int, int>> result;
        for (int y = 0; y < height_; ++y)
            for (int x = 0; x < width_; ++x)
                if (get(x, y)) result.emplace_back(x, y);
        return result;
    }

  private:
    std::size_t index(int x, int y) const {
        return static_cast<std::size_t>(y) * static_cast<std::size_t>(width_) +
               static_cast<std::size_t>(x);
    }

    int width_;
    int height_;
    std::vector<std::uint8_t> cells_;
};

namespace detail {

// Pixel index range whose centers [i + 0.5] cover the coordinate range [lo, hi].
inline int pixelLo(double lo) { return static_cast<int>(std::ceil(lo - 0.5)); }
inline int pixelHi(double hi) { return static_cast<int>(std::floor(hi - 0.5)); }

// Fills the disc of radius r around c. Uses squared distances only.
inline void fillDisc(Bitmap& out, Point c, double r) {
    const double r2 = r * r;
    const int x0 = pixelLo(c.x - r), x1 = pixelHi(c.x + r);
    const int y0 = pixelLo(c.y - r), y1 = pixelHi(c.y + r);
    for (int y = y0; y <= y1; ++y) {
        const double dy = y + 0.5 - c.y;
        for (int x = x0; x <= x1; ++x) {
            const double dx = x + 0.5 - c.x;
            if (dx * dx + dy * dy <= r2) out.set(x, y);
        }
    }
}

// Fills the axis-aligned square of half-size `half` around c.
inline void fillBox(Bitmap& out, Point c, double half) {
    const int x0 = pixelLo(c.x - half), x1 = pixelHi(c.x + half);
    const int y0 = pixelLo(c.y - half), y1 = pixelHi(c.y + half);
    for (int y = y0; y <= y1; ++y)
        for (int x = x0; x <= x1; ++x) out.set(x, y);
}

// Fills a triangle via inclusive half-plane tests (either winding).
inline void fillTriangle(Bitmap& out, Point a, Point b, Point c) {
    const double orient = cross(b - a, c - a);
    if (orient == 0.0) return; // degenerate (collinear) triangle
    const double minX = std::min({a.x, b.x, c.x});
    const double maxX = std::max({a.x, b.x, c.x});
    const double minY = std::min({a.y, b.y, c.y});
    const double maxY = std::max({a.y, b.y, c.y});
    for (int y = pixelLo(minY); y <= pixelHi(maxY); ++y)
        for (int x = pixelLo(minX); x <= pixelHi(maxX); ++x) {
            const Point p{x + 0.5, y + 0.5};
            const double e0 = cross(b - a, p - a);
            const double e1 = cross(c - b, p - b);
            const double e2 = cross(a - c, p - c);
            const bool inside = orient > 0.0 ? (e0 >= 0.0 && e1 >= 0.0 && e2 >= 0.0)
                                             : (e0 <= 0.0 && e1 <= 0.0 && e2 <= 0.0);
            if (inside) out.set(x, y);
        }
}

// Fills the rectangle of half-width `halfWidth` around the segment p0 -> p1,
// extended by ext0 / ext1 beyond the endpoints (used for square caps).
inline void fillSegment(Bitmap& out, Point p0, Point p1, double halfWidth, double ext0,
                        double ext1) {
    const Point d = p1 - p0;
    const double len = length(d);
    if (len == 0.0) return;
    const Point dir = d * (1.0 / len);
    const Point nrm{-dir.y, dir.x};
    const Point c0 = p0 - dir * ext0;
    const Point c1 = p1 + dir * ext1;
    const Point off = nrm * halfWidth;
    const double minX =
        std::min(std::min(c0.x - off.x, c0.x + off.x), std::min(c1.x - off.x, c1.x + off.x));
    const double maxX =
        std::max(std::max(c0.x - off.x, c0.x + off.x), std::max(c1.x - off.x, c1.x + off.x));
    const double minY =
        std::min(std::min(c0.y - off.y, c0.y + off.y), std::min(c1.y - off.y, c1.y + off.y));
    const double maxY =
        std::max(std::max(c0.y - off.y, c0.y + off.y), std::max(c1.y - off.y, c1.y + off.y));
    for (int y = pixelLo(minY); y <= pixelHi(maxY); ++y)
        for (int x = pixelLo(minX); x <= pixelHi(maxX); ++x) {
            const Point p{x + 0.5, y + 0.5};
            const double t = dot(p - p0, dir);
            const double s = dot(p - p0, nrm);
            if (t >= -ext0 && t <= len + ext1 && s >= -halfWidth && s <= halfWidth)
                out.set(x, y);
        }
}

// Fills the join shape at vertex p between segments with unit directions
// d1 (incoming) and d2 (outgoing). The join shape shares full edges with the
// butt-ended segment rectangles, so the union has no cracks; because the
// bitmap ORs coverage, the overlaps never double-count.
inline void joinAt(Bitmap& out, Point p, Point d1, Point d2, double h,
                   const StrokeOptions& opt) {
    const Point n1{-d1.y, d1.x}; // left normal of the incoming segment
    const Point n2{-d2.y, d2.x}; // left normal of the outgoing segment
    const double turn = cross(d1, d2);
    constexpr double kCollinear = 1e-12;

    if (std::fabs(turn) < kCollinear) {
        if (dot(d1, d2) > 0.0) return; // straight ahead: rectangles already overlap
        // 180-degree foldback: cap the far side of the vertex.
        if (opt.join == JoinStyle::kRound) {
            fillDisc(out, p, h);
        } else {
            // Bevel; a miter here would be infinitely long, so it falls back too.
            fillTriangle(out, p + n1 * h, p - n1 * h, p + d1 * h);
        }
        return;
    }

    // Outer side of the corner: right side for a left turn, left side otherwise.
    const Point o1 = turn > 0.0 ? -n1 : n1;
    const Point o2 = turn > 0.0 ? -n2 : n2;
    const Point a = p + o1 * h;
    const Point b = p + o2 * h;

    switch (opt.join) {
        case JoinStyle::kRound:
            // The disc minus the two segment rectangles is exactly the outer
            // wedge, so filling the whole disc yields a correct round join.
            fillDisc(out, p, h);
            break;
        case JoinStyle::kBevel:
            fillTriangle(out, p, a, b);
            break;
        case JoinStyle::kMiter: {
            const Point sum = o1 + o2;
            const double denom = 1.0 + dot(o1, o2); // 1 + cos(theta), in (0, 2]
            // Miter point is p + h * sum / denom; compare squared lengths so
            // no square root is needed for the limit test.
            const double limitLen = opt.miterLimit * opt.width;
            const double miterLenSq = h * h * squaredLength(sum) / (denom * denom);
            if (denom > 1e-9 && miterLenSq <= limitLen * limitLen) {
                const Point m = p + sum * (h / denom);
                fillTriangle(out, p, a, m);
                fillTriangle(out, p, m, b);
            } else {
                fillTriangle(out, p, a, b); // miter limit exceeded: bevel
            }
            break;
        }
    }
}

} // namespace detail

// Rasterizes the stroked polyline into `out` (OR-ed with existing content).
inline void rasterize(Bitmap& out, const std::vector<Point>& polyline,
                      const StrokeOptions& opt) {
    if (opt.width <= 0.0 || polyline.empty()) return;
    const double h = 0.5 * opt.width;

    // Drop zero-length segments (consecutive coincident points).
    std::vector<Point> pts;
    pts.reserve(polyline.size());
    for (Point p : polyline) {
        if (pts.empty() || squaredLength(p - pts.back()) > 1e-18) pts.push_back(p);
    }

    if (pts.size() == 1) {
        switch (opt.cap) {
            case CapStyle::kRound: detail::fillDisc(out, pts[0], h); break;
            case CapStyle::kSquare: detail::fillBox(out, pts[0], h); break;
            case CapStyle::kButt: break; // PostScript convention: draw nothing
        }
        return;
    }

    const int segCount = static_cast<int>(pts.size()) - 1;
    std::vector<Point> dirs(segCount);
    for (int i = 0; i < segCount; ++i) {
        const Point d = pts[i + 1] - pts[i];
        dirs[i] = d * (1.0 / length(d));
    }

    for (int i = 0; i < segCount; ++i) {
        const double ext0 = (i == 0 && opt.cap == CapStyle::kSquare) ? h : 0.0;
        const double ext1 = (i == segCount - 1 && opt.cap == CapStyle::kSquare) ? h : 0.0;
        detail::fillSegment(out, pts[i], pts[i + 1], h, ext0, ext1);
    }
    if (opt.cap == CapStyle::kRound) {
        detail::fillDisc(out, pts.front(), h);
        detail::fillDisc(out, pts.back(), h);
    }

    for (int i = 1; i + 1 < static_cast<int>(pts.size()); ++i)
        detail::joinAt(out, pts[i], dirs[i - 1], dirs[i], h, opt);
}

} // namespace stroke
