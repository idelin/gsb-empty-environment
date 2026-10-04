// ediff: 灰度误差扩散量化库（C++17）
//
// 核心规则：
//  - 逐行往返（蛇形）扫描：第 0 行按起始方向扫描，每行结束换向。
//  - 误差按有理权重 num/den 分配给尚未处理的像素；反向扫描时权重水平镜像。
//  - 全程整数精确累计：每个像素的累计误差以“分子（int64）/公共分母 den”保存，
//    无任何浮点运算，因此结果与分块方式无关。
//  - 最终取整规则：对累计值 v（= 原像素 + 累计误差，精确有理数）取“最近灰度级”；
//    恰好位于两级中点时，取较高的灰度级（round-half-up）。
//  - 指向图像边界之外的误差份额被丢弃，其总和以精确有理数单独计量。
#pragma once

#include <cstdint>
#include <vector>

namespace ediff {

// 前向（左->右）扫描方向下的一个扩散抽头：
// 相对位移 (dx, dy)，权重 num/den。要求 dy > 0，或 dy == 0 且 dx > 0
// （即只流向尚未处理的像素）。
struct Tap {
    int dx;
    int dy;
    int64_t num;
};

struct Kernel {
    std::vector<Tap> taps;  // 以前向扫描方向给出
    int64_t den = 1;        // 公共分母，必须 > 0
};

// 经典 Floyd–Steinberg 权重：7/16, 3/16, 5/16, 1/16。
Kernel floydSteinberg();

struct Image {
    int width = 0;
    int height = 0;
    std::vector<int> px;  // 行优先，取值 0..255

    Image() = default;
    Image(int w, int h, std::vector<int> data);
    int at(int x, int y) const { return px[static_cast<size_t>(y) * width + x]; }
};

// 蛇形扫描第 0 行的方向；奇数行相反，依次交替。
enum class StartDir { LeftToRight, RightToLeft };

struct Options {
    std::vector<int> levels{0, 255};  // 升序灰度级，至少 2 级
    Kernel kernel = floydSteinberg();
    StartDir start = StartDir::LeftToRight;
};

struct Result {
    std::vector<int> out;   // 量化后的灰度级，尺寸同输入
    int64_t discarded_num = 0;  // 边界丢弃误差总和的分子（精确）
    int64_t discarded_den = 1;  // 其分母（== kernel.den）
};

// 精确取整：返回离 value_num/den 最近的灰度级；中点取较高级。
int nearestLevel(int64_t value_num, int64_t den, const std::vector<int>& levels);

// 整图一次性量化。
Result quantize(const Image& img, const Options& opt);

// 分块量化：每次处理 rows_per_tile 行，块间携带误差状态。
// 对任意 rows_per_tile >= 1，输出与 quantize 完全一致（精确算术保证）。
Result quantizeTiled(const Image& img, const Options& opt, int rows_per_tile);

// 水平镜像辅助（用于镜像/反向扫描性质验证）。
Image mirrorHorizontal(const Image& img);
std::vector<int> mirrorHorizontal(const std::vector<int>& row_major, int w, int h);

}  // namespace ediff
