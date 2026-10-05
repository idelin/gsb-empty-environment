#include "stroke_rasterizer.h"

#include <cmath>
#include <cstdio>
#include <queue>
#include <set>
#include <utility>
#include <vector>

using stroke::Bitmap;
using stroke::JoinStyle;
using stroke::Options;
using stroke::Point;

static int g_failures = 0;

#define CHECK(cond) do { if (!(cond)) { ++g_failures; std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond); } } while (0)

namespace {

Options opts(double width, JoinStyle join, double miterLimit = 4.0) {
  Options o;
  o.width = width;
  o.join = join;
  o.miterLimit = miterLimit;
  return o;
}

// 在两张位图的并集范围上逐像素比较（at 越界返回 false）
bool bitmapsEqual(const Bitmap& a, const Bitmap& b) {
  const int x0 = std::min(a.originX(), b.originX());
  const int y0 = std::min(a.originY(), b.originY());
  const int x1 = std::max(a.originX() + a.width(), b.originX() + b.width());
  const int y1 = std::max(a.originY() + a.height(), b.originY() + b.height());
  for (int y = y0; y < y1; ++y)
    for (int x = x0; x < x1; ++x)
      if (a.at(x, y) != b.at(x, y)) return false;
  return true;
}

bool isSubset(const Bitmap& sub, const Bitmap& super) {
  for (int y = sub.originY(); y < sub.originY() + sub.height(); ++y)
    for (int x = sub.originX(); x < sub.originX() + sub.width(); ++x)
      if (sub.at(x, y) && !super.at(x, y)) return false;
  return true;
}

// 从边界洪泛填充背景，若存在无法到达的未置位像素则为封闭孔洞
bool hasEnclosedHole(const Bitmap& bm) {
  const int w = bm.width(), h = bm.height();
  std::vector<unsigned char> vis(static_cast<size_t>(w) * h, 0);
  std::queue<std::pair<int, int>> q;
  auto tryPush = [&](int x, int y) {
    if (x < 0 || y < 0 || x >= w || y >= h) return;
    if (vis[static_cast<size_t>(y) * w + x]) return;
    if (bm.at(x + bm.originX(), y + bm.originY())) return;
    vis[static_cast<size_t>(y) * w + x] = 1;
    q.push({x, y});
  };
  for (int x = 0; x < w; ++x) { tryPush(x, 0); tryPush(x, h - 1); }
  for (int y = 0; y < h; ++y) { tryPush(0, y); tryPush(w - 1, y); }
  while (!q.empty()) {
    const auto front = q.front();
    q.pop();
    tryPush(front.first + 1, front.second);
    tryPush(front.first - 1, front.second);
    tryPush(front.first, front.second + 1);
    tryPush(front.first, front.second - 1);
  }
  for (int y = 0; y < h; ++y)
    for (int x = 0; x < w; ++x)
      if (!vis[static_cast<size_t>(y) * w + x] &&
          !bm.at(x + bm.originX(), y + bm.originY()))
        return true;
  return false;
}

void testDegenerate() {
  CHECK(stroke::rasterizePolyline({}, opts(2.0, JoinStyle::kMiter)).count() == 0);
  // 零宽 / 负宽
  CHECK(stroke::rasterizePolyline({{0, 0}, {10, 0}}, opts(0.0, JoinStyle::kMiter))
            .count() == 0);
  // 单点退化为圆点：r=1 时恰好 4 个像素中心落在盘内
  CHECK(stroke::rasterizePolyline({{5, 5}}, opts(2.0, JoinStyle::kRound))
            .count() == 4);
  // 全部为零长线段等同于单点
  CHECK(stroke::rasterizePolyline({{5, 5}, {5, 5}, {5, 5}},
                                  opts(2.0, JoinStyle::kMiter))
            .count() == 4);
}

void testStraightLine() {
  Bitmap bm = stroke::rasterizePolyline({{0, 5}, {10, 5}},
                                        opts(2.0, JoinStyle::kMiter));
  CHECK(bm.count() == 20);  // 行 4、5，列 0..9
  CHECK(bm.at(0, 4) && bm.at(0, 5) && bm.at(9, 4) && bm.at(9, 5));
  CHECK(!bm.at(10, 4) && !bm.at(-1, 5) && !bm.at(5, 3) && !bm.at(5, 6));
}

void testZeroLengthSegments() {
  const Bitmap a =
      stroke::rasterizePolyline({{0, 5}, {10, 5}}, opts(2.0, JoinStyle::kMiter));
  const Bitmap b = stroke::rasterizePolyline(
      {{0, 5}, {0, 5}, {5, 5}, {5, 5}, {10, 5}, {10, 5}},
      opts(2.0, JoinStyle::kMiter));
  CHECK(bitmapsEqual(a, b));
  // 转角处的零长线段不改变结果
  const Bitmap c = stroke::rasterizePolyline({{0, 5}, {10, 5}, {10, 15}},
                                             opts(2.0, JoinStyle::kBevel));
  const Bitmap d = stroke::rasterizePolyline(
      {{0, 5}, {10, 5}, {10, 5}, {10, 5}, {10, 15}},
      opts(2.0, JoinStyle::kBevel));
  CHECK(bitmapsEqual(c, d));
}

void testReversal() {
  for (JoinStyle join : {JoinStyle::kMiter, JoinStyle::kBevel}) {
    const Bitmap a =
        stroke::rasterizePolyline({{0, 5}, {10, 5}}, opts(2.0, join));
    // 完全折返：与单线段一致
    const Bitmap rev = stroke::rasterizePolyline({{0, 5}, {10, 5}, {0, 5}},
                                                 opts(2.0, join));
    CHECK(bitmapsEqual(a, rev));
    // 部分折返：后段矩形被前段覆盖
    const Bitmap spike = stroke::rasterizePolyline({{0, 5}, {10, 5}, {3, 5}},
                                                   opts(2.0, join));
    CHECK(bitmapsEqual(a, spike));
  }
  // 圆角样式：折返端部补半圆，结果为直线段的超集且无孔洞
  const Bitmap a =
      stroke::rasterizePolyline({{0, 5}, {10, 5}}, opts(2.0, JoinStyle::kRound));
  const Bitmap rev = stroke::rasterizePolyline({{0, 5}, {10, 5}, {0, 5}},
                                               opts(2.0, JoinStyle::kRound));
  CHECK(isSubset(a, rev));
  CHECK(rev.at(10, 5));  // 折返端 (10,5) 右侧半圆覆盖
  CHECK(!hasEnclosedHole(rev));
}

void testBevelJoin() {
  // L 形 90 度，w=2，r=1，顶点 (10,5)
  const Bitmap bm = stroke::rasterizePolyline({{0, 5}, {10, 5}, {10, 15}},
                                              opts(2.0, JoinStyle::kBevel));
  CHECK(bm.at(10, 5));   // 连接处无孔洞
  CHECK(bm.at(10, 4));   // 截角斜边恰好经过该中心点（含边界）
  CHECK(!bm.at(11, 4));  // 截角之外
}

void testRoundJoin() {
  const Bitmap bm = stroke::rasterizePolyline({{0, 5}, {10, 5}, {10, 15}},
                                              opts(2.0, JoinStyle::kRound));
  CHECK(bm.at(10, 5));
  CHECK(bm.at(10, 4));   // 距顶点 sqrt(0.5) <= 1
  CHECK(!bm.at(11, 4));  // 距顶点 sqrt(2.5) > 1
  // 顶点周围 0.95r 圆盘内的像素中心必须全部覆盖（平方距离判定）
  const double rr = 0.95 * 0.95;
  for (int y = 3; y <= 7; ++y)
    for (int x = 8; x <= 12; ++x) {
      const double dx = x + 0.5 - 10.0, dy = y + 0.5 - 5.0;
      if (dx * dx + dy * dy <= rr) CHECK(bm.at(x, y));
    }
}

void testMiterJoin() {
  // L 形 90 度，w=4，r=2，斜接比 = 1/cos(45) = 1.414 <= 4
  const Bitmap miter = stroke::rasterizePolyline({{0, 5}, {10, 5}, {10, 15}},
                                                 opts(4.0, JoinStyle::kMiter));
  const Bitmap bevel = stroke::rasterizePolyline({{0, 5}, {10, 5}, {10, 15}},
                                                 opts(4.0, JoinStyle::kBevel));
  CHECK(miter.at(11, 3));   // 斜接尖角覆盖
  CHECK(!bevel.at(11, 3));  // 截角切掉了该处
  CHECK(isSubset(bevel, miter));
  CHECK(miter.count() > bevel.count());
}

void testMiterLimitFallback() {
  // 170 度尖角：斜接比 = 1/cos(85) = 11.43
  const std::vector<Point> pl = {{5, 30}, {45, 30}, {5.6, 36.95}};
  const Bitmap bevel =
      stroke::rasterizePolyline(pl, opts(3.0, JoinStyle::kBevel));
  const Bitmap miter4 =
      stroke::rasterizePolyline(pl, opts(3.0, JoinStyle::kMiter, 4.0));
  const Bitmap miter20 =
      stroke::rasterizePolyline(pl, opts(3.0, JoinStyle::kMiter, 20.0));
  // 超过上限退化为截角：与截角结果完全一致
  CHECK(bitmapsEqual(bevel, miter4));
  // 放宽上限后使用斜接：截角是其严格子集
  CHECK(isSubset(bevel, miter20));
  CHECK(miter20.count() > bevel.count());
}

void testMiterLimitThreshold() {
  // 150 度转角：斜接比 = 1/cos(75) = 3.8637
  const std::vector<Point> pl = {{5, 30}, {45, 30}, {10.36, 50}};
  const Bitmap bevel =
      stroke::rasterizePolyline(pl, opts(3.0, JoinStyle::kBevel));
  const Bitmap below =
      stroke::rasterizePolyline(pl, opts(3.0, JoinStyle::kMiter, 3.8));
  const Bitmap above =
      stroke::rasterizePolyline(pl, opts(3.0, JoinStyle::kMiter, 3.9));
  CHECK(bitmapsEqual(bevel, below));   // 3.8637 > 3.8 退化截角
  CHECK(isSubset(below, above));       // 3.8637 <= 3.9 使用斜接
  CHECK(above.count() > below.count());
}

void testNoJointHoles() {
  const double kDeg = 3.14159265358979323846 / 180.0;
  for (double deg : {5.0, 15.0, 30.0, 45.0, 60.0, 90.0, 120.0, 150.0, 170.0,
                     179.0}) {
    const double cs = std::cos(deg * kDeg), sn = std::sin(deg * kDeg);
    const std::vector<Point> pl = {{5, 30}, {45, 30},
                                   {45 + 40 * cs, 30 + 40 * sn}};
    for (JoinStyle join :
         {JoinStyle::kMiter, JoinStyle::kRound, JoinStyle::kBevel}) {
      const Bitmap bm = stroke::rasterizePolyline(pl, opts(3.0, join));
      // 整体无封闭孔洞
      CHECK(!hasEnclosedHole(bm));
      // 中心线采样（避开顶点 0.8 范围）：落点像素必须置位
      for (double t = 0.0; t <= 40.0; t += 0.05) {
        if (std::abs(40.0 - t) < 0.8) continue;
        CHECK(bm.at(static_cast<int>(std::floor(5 + t)),
                    static_cast<int>(std::floor(30.0))));
      }
      // 避开末端 0.8：平头端帽外的末端像素本就不在笔触内
      for (double s = 0.8; s <= 39.2; s += 0.05) {
        CHECK(bm.at(static_cast<int>(std::floor(45 + s * cs)),
                    static_cast<int>(std::floor(30 + s * sn))));
      }
    }
  }
}

void testUniquePixels() {
  // 自相交 + 折返 + 零长线段的复杂折线
  const std::vector<Point> pl = {{0, 0},   {20, 0},  {20, 20}, {5, 20},
                                 {5, 5},   {15, 5},  {15, 15}, {10, 15},
                                 {10, 10}, {10, 10}, {0, 0},   {20, 0}};
  for (JoinStyle join :
       {JoinStyle::kMiter, JoinStyle::kRound, JoinStyle::kBevel}) {
    const Options o = opts(3.0, join);
    const Bitmap bm = stroke::rasterizePolyline(pl, o);
    const auto pixels = stroke::rasterizePixels(pl, o);
    // 列表无重复且与位图计数一致
    const std::set<std::pair<int, int>> uniq(pixels.begin(), pixels.end());
    CHECK(uniq.size() == pixels.size());
    CHECK(pixels.size() == bm.count());
    for (const auto& p : pixels) CHECK(bm.at(p.first, p.second));
  }
}

}  // namespace

int main() {
  testDegenerate();
  testStraightLine();
  testZeroLengthSegments();
  testReversal();
  testBevelJoin();
  testRoundJoin();
  testMiterJoin();
  testMiterLimitFallback();
  testMiterLimitThreshold();
  testNoJointHoles();
  testUniquePixels();
  if (g_failures == 0) {
    std::printf("ALL TESTS PASSED\n");
    return 0;
  }
  std::printf("%d CHECK(S) FAILED\n", g_failures);
  return 1;
}
