#pragma once

#include <cstddef>
#include <cstdint>
#include <optional>
#include <vector>

namespace morpho {

enum class Connectivity { Four, Eight };

struct GrayImage {
    std::size_t width = 0;
    std::size_t height = 0;
    std::vector<std::uint8_t> pixels;  // row-major, size == width * height

    static GrayImage filled(std::size_t w, std::size_t h, std::uint8_t value) {
        return GrayImage{w, h, std::vector<std::uint8_t>(w * h, value)};
    }

    std::uint8_t& at(std::size_t x, std::size_t y) { return pixels[y * width + x]; }
    std::uint8_t at(std::size_t x, std::size_t y) const { return pixels[y * width + x]; }
};

struct ReconstructionResult {
    GrayImage image;              // 重建结果
    std::size_t update_count = 0; // 算法过程中像素值被提升的总次数
    bool fixed_point = false;     // 固定点校验：J == min(mask, 邻域极大膨胀(J))
};

// 灰度形态学重建（膨胀型）：从 marker 出发，在 mask 上界约束下反复做
// 邻域极大膨胀直到稳定。采用 Vincent 混合算法（正/反光栅扫描 + FIFO
// 队列传播），无需整图反复扫描。
//
// 返回 std::nullopt 当且仅当输入非法：
//   - 尺寸不一致或像素缓冲大小不符
//   - 存在 marker[p] > mask[p]（标记必须低于掩码）
std::optional<ReconstructionResult> reconstruct_by_dilation(
    const GrayImage& marker, const GrayImage& mask,
    Connectivity conn = Connectivity::Eight);

}  // namespace morpho
