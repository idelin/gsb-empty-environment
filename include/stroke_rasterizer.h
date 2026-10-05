#pragma once

#include <cstddef>
#include <cstdint>
#include <string>
#include <utility>
#include <vector>

namespace stroke {

struct Point {
  double x = 0.0;
  double y = 0.0;
};

// 折线连接处的转角样式
enum class JoinStyle {
  kMiter,  // 斜接：沿两侧描边边线延长相交，尖角可超出半宽
  kRound,  // 圆角：以顶点为圆心、半宽为半径的圆盘
  kBevel,  // 截角：用两侧边线端点连线切掉尖角
};

struct Options {
  double width = 2.0;             // 描边宽度（像素），<= 0 时结果为空
  JoinStyle join = JoinStyle::kMiter;
  // 斜接长度（顶点到尖角的距离）与半宽（width/2）之比的上限，
  // 超过该上限时退化为截角。小于 1 时按 1 处理（等价于总是截角）。
  double miterLimit = 4.0;
};

// 像素位图。像素 (x, y) 覆盖 [x, x+1) x [y, y+1)，
// 判定以其中心点 (x+0.5, y+0.5) 是否落在笔触内为准。
class Bitmap {
public:
  Bitmap() = default;
  Bitmap(int originX, int originY, int width, int height);

  bool at(int x, int y) const;  // 越界返回 false
  void set(int x, int y);       // 幂等：重复置位不会重复计数
  std::size_t count() const { return count_; }

  int originX() const { return originX_; }
  int originY() const { return originY_; }
  int width() const { return width_; }
  int height() const { return height_; }

  std::string toString() const;  // 调试用 ASCII 图，'#' 为已置位

private:
  int originX_ = 0;
  int originY_ = 0;
  int width_ = 0;
  int height_ = 0;
  std::size_t count_ = 0;
  std::vector<std::uint8_t> pixels_;
};

// 栅格化一条等宽折线。端点使用平头（butt）端帽。
// 长度小于 1e-9 的连续零长线段会被忽略；退化到单个点时绘制一个半径为半宽的圆点。
Bitmap rasterizePolyline(const std::vector<Point>& points, const Options& options);

// 等价于 rasterizePolyline，但以扫描线顺序返回被置位的像素坐标列表。
// 保证列表内无重复像素（同一像素不会被重复计数）。
std::vector<std::pair<int, int>> rasterizePixels(
    const std::vector<Point>& points, const Options& options);

}  // namespace stroke
