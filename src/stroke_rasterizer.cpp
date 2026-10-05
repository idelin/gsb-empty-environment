#include "stroke_rasterizer.h"

#include <algorithm>
#include <cmath>

namespace stroke {

Bitmap::Bitmap(int originX, int originY, int width, int height)
    : originX_(originX),
      originY_(originY),
      width_(width),
      height_(height),
      pixels_(static_cast<std::size_t>(width) * static_cast<std::size_t>(height),
              0) {}

bool Bitmap::at(int x, int y) const {
  const int ix = x - originX_;
  const int iy = y - originY_;
  if (ix < 0 || iy < 0 || ix >= width_ || iy >= height_) return false;
  return pixels_[static_cast<std::size_t>(iy) * static_cast<std::size_t>(width_) +
                 static_cast<std::size_t>(ix)] != 0;
}

void Bitmap::set(int x, int y) {
  const int ix = x - originX_;
  const int iy = y - originY_;
  if (ix < 0 || iy < 0 || ix >= width_ || iy >= height_) return;
  std::uint8_t& cell =
      pixels_[static_cast<std::size_t>(iy) * static_cast<std::size_t>(width_) +
              static_cast<std::size_t>(ix)];
  if (!cell) {
    cell = 1;
    ++count_;
  }
}

std::string Bitmap::toString() const {
  std::string out;
  for (int y = originY_ + height_ - 1; y >= originY_; --y) {
    for (int x = originX_; x < originX_ + width_; ++x)
      out += at(x, y) ? '#' : '.';
    out += '\n';
  }
  return out;
}

namespace {

// 连续两点距离小于该值时视为零长线段并忽略
constexpr double kMinSegmentLen = 1e-9;

struct Segment {
  Point a;     // 起点
  Point dir;   // 单位方向向量
  double len;  // 长度
};

double cross(Point u, Point v) { return u.x * v.y - u.y * v.x; }

// 线段矩形：中心点满足 0 <= t <= len 且 |s| <= r（t 沿方向、s 沿法向）
void fillSegmentRect(Bitmap& bm, const Segment& seg, double r) {
  const Point n{-seg.dir.y, seg.dir.x};  // 左法向
  const double ex = seg.dir.x * seg.len;
  const double ey = seg.dir.y * seg.len;
  const int x0 = static_cast<int>(std::floor(std::min(seg.a.x, seg.a.x + ex) - r)) - 1;
  const int x1 = static_cast<int>(std::ceil(std::max(seg.a.x, seg.a.x + ex) + r)) + 1;
  const int y0 = static_cast<int>(std::floor(std::min(seg.a.y, seg.a.y + ey) - r)) - 1;
  const int y1 = static_cast<int>(std::ceil(std::max(seg.a.y, seg.a.y + ey) + r)) + 1;
  for (int y = y0; y < y1; ++y) {
    for (int x = x0; x < x1; ++x) {
      const double cx = x + 0.5 - seg.a.x;
      const double cy = y + 0.5 - seg.a.y;
      const double t = cx * seg.dir.x + cy * seg.dir.y;
      if (t < 0.0 || t > seg.len) continue;
      const double s = cx * n.x + cy * n.y;
      if (s >= -r && s <= r) bm.set(x, y);
    }
  }
}

// 圆盘：平方距离比较，避免开方
void fillDisk(Bitmap& bm, Point c, double r) {
  const double r2 = r * r;
  const int x0 = static_cast<int>(std::floor(c.x - r)) - 1;
  const int x1 = static_cast<int>(std::ceil(c.x + r)) + 1;
  const int y0 = static_cast<int>(std::floor(c.y - r)) - 1;
  const int y1 = static_cast<int>(std::ceil(c.y + r)) + 1;
  for (int y = y0; y < y1; ++y) {
    for (int x = x0; x < x1; ++x) {
      const double dx = x + 0.5 - c.x;
      const double dy = y + 0.5 - c.y;
      if (dx * dx + dy * dy <= r2) bm.set(x, y);
    }
  }
}

// 三角形：三条边同侧（含边界）判定
void fillTriangle(Bitmap& bm, Point a, Point b, Point c) {
  const int x0 = static_cast<int>(std::floor(std::min({a.x, b.x, c.x}))) - 1;
  const int x1 = static_cast<int>(std::ceil(std::max({a.x, b.x, c.x}))) + 1;
  const int y0 = static_cast<int>(std::floor(std::min({a.y, b.y, c.y}))) - 1;
  const int y1 = static_cast<int>(std::ceil(std::max({a.y, b.y, c.y}))) + 1;
  for (int y = y0; y < y1; ++y) {
    for (int x = x0; x < x1; ++x) {
      const Point p{x + 0.5, y + 0.5};
      const double d1 = cross({b.x - a.x, b.y - a.y}, {p.x - a.x, p.y - a.y});
      const double d2 = cross({c.x - b.x, c.y - b.y}, {p.x - b.x, p.y - b.y});
      const double d3 = cross({a.x - c.x, a.y - c.y}, {p.x - c.x, p.y - c.y});
      const bool hasNeg = (d1 < 0.0) || (d2 < 0.0) || (d3 < 0.0);
      const bool hasPos = (d1 > 0.0) || (d2 > 0.0) || (d3 > 0.0);
      if (!(hasNeg && hasPos)) bm.set(x, y);
    }
  }
}

// 顶点 p 处的连接处理。d1/d2 为入/出线段的单位方向。
void fillJoin(Bitmap& bm, Point p, Point d1, Point d2, double r,
              const Options& opt) {
  const double turn = cross(d1, d2);
  if (std::abs(turn) < 1e-9) {
    // 共线：直线延续或 180 折返时两侧矩形已完全重叠，无需补形；
    // 圆角样式下为折返的端部补一个半圆头，避免平头切口。
    if (opt.join == JoinStyle::kRound &&
        d1.x * d2.x + d1.y * d2.y < 0.0) {
      fillDisk(bm, p, r);
    }
    return;
  }

  const Point n1{-d1.y, d1.x};  // 入线左法向
  const Point n2{-d2.y, d2.x};  // 出线左法向

  if (opt.join == JoinStyle::kRound) {
    fillDisk(bm, p, r);
    return;
  }

  // 斜接点 m 满足 n1.(m-p) = r 且 n2.(m-p) = r（法向侧），
  // 镜像侧取相反数即可，两侧都填充（内侧本就被矩形覆盖，幂等无害）。
  bool useMiter = false;
  double mx = 0.0, my = 0.0;
  if (opt.join == JoinStyle::kMiter) {
    const double det = n1.x * n2.y - n1.y * n2.x;  // 等于 turn，非零
    mx = r * (n2.y - n1.y) / det;
    my = r * (n1.x - n2.x) / det;
    const double miterLen2 = mx * mx + my * my;  // 平方距离比较
    const double limit = std::max(opt.miterLimit, 1.0) * r;
    useMiter = miterLen2 <= limit * limit;
  }

  const Point a1{p.x + n1.x * r, p.y + n1.y * r};
  const Point b1{p.x + n2.x * r, p.y + n2.y * r};
  const Point a2{p.x - n1.x * r, p.y - n1.y * r};
  const Point b2{p.x - n2.x * r, p.y - n2.y * r};

  if (useMiter) {
    const Point m1{p.x + mx, p.y + my};
    const Point m2{p.x - mx, p.y - my};
    fillTriangle(bm, p, a1, m1);
    fillTriangle(bm, p, m1, b1);
    fillTriangle(bm, p, a2, m2);
    fillTriangle(bm, p, m2, b2);
  } else {
    // 截角（或斜接超上限退化）
    fillTriangle(bm, p, a1, b1);
    fillTriangle(bm, p, a2, b2);
  }
}

}  // namespace

Bitmap rasterizePolyline(const std::vector<Point>& points,
                         const Options& options) {
  // 去除零长线段（重复点）
  std::vector<Point> pts;
  pts.reserve(points.size());
  for (const Point& p : points) {
    if (pts.empty()) {
      pts.push_back(p);
    } else {
      const double dx = p.x - pts.back().x;
      const double dy = p.y - pts.back().y;
      if (dx * dx + dy * dy > kMinSegmentLen * kMinSegmentLen) pts.push_back(p);
    }
  }

  const double r = options.width * 0.5;
  if (pts.empty() || !(r > 0.0)) return Bitmap();

  // 位图范围：所有顶点外扩（斜接最大伸长 + 余量）
  const double margin = std::max(options.miterLimit, 1.0) * r + 2.0;
  double minX = pts[0].x, maxX = pts[0].x, minY = pts[0].y, maxY = pts[0].y;
  for (const Point& p : pts) {
    minX = std::min(minX, p.x);
    maxX = std::max(maxX, p.x);
    minY = std::min(minY, p.y);
    maxY = std::max(maxY, p.y);
  }
  const int ox = static_cast<int>(std::floor(minX - margin));
  const int oy = static_cast<int>(std::floor(minY - margin));
  const int w = static_cast<int>(std::ceil(maxX + margin)) - ox;
  const int h = static_cast<int>(std::ceil(maxY + margin)) - oy;
  Bitmap bm(ox, oy, w, h);

  if (pts.size() == 1) {
    fillDisk(bm, pts[0], r);  // 退化为圆点
    return bm;
  }

  std::vector<Segment> segs;
  segs.reserve(pts.size() - 1);
  for (std::size_t i = 0; i + 1 < pts.size(); ++i) {
    const double dx = pts[i + 1].x - pts[i].x;
    const double dy = pts[i + 1].y - pts[i].y;
    const double len = std::sqrt(dx * dx + dy * dy);
    segs.push_back({pts[i], {dx / len, dy / len}, len});
  }

  for (const Segment& seg : segs) fillSegmentRect(bm, seg, r);
  for (std::size_t i = 1; i + 1 < pts.size(); ++i)
    fillJoin(bm, pts[i], segs[i - 1].dir, segs[i].dir, r, options);

  return bm;
}

std::vector<std::pair<int, int>> rasterizePixels(
    const std::vector<Point>& points, const Options& options) {
  const Bitmap bm = rasterizePolyline(points, options);
  std::vector<std::pair<int, int>> out;
  out.reserve(bm.count());
  for (int y = bm.originY(); y < bm.originY() + bm.height(); ++y)
    for (int x = bm.originX(); x < bm.originX() + bm.width(); ++x)
      if (bm.at(x, y)) out.emplace_back(x, y);
  return out;
}

}  // namespace stroke
