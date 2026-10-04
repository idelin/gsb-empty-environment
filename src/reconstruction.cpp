#include "reconstruction.hpp"

#include <algorithm>
#include <deque>
#include <utility>

namespace morpho {
namespace {

struct Offsets {
    std::vector<std::pair<int, int>> all;       // 全部邻居
    std::vector<std::pair<int, int>> forward;   // 光栅序中位于当前像素之后的邻居
    std::vector<std::pair<int, int>> backward;  // 光栅序中位于当前像素之前的邻居
};

Offsets make_offsets(Connectivity conn) {
    Offsets off;
    for (int dy = -1; dy <= 1; ++dy) {
        for (int dx = -1; dx <= 1; ++dx) {
            if (dx == 0 && dy == 0) continue;
            if (conn == Connectivity::Four && dx != 0 && dy != 0) continue;
            off.all.emplace_back(dx, dy);
            if (dy > 0 || (dy == 0 && dx > 0)) off.forward.emplace_back(dx, dy);
            if (dy < 0 || (dy == 0 && dx < 0)) off.backward.emplace_back(dx, dy);
        }
    }
    return off;
}

template <typename F>
void for_each_neighbor(std::size_t x, std::size_t y, std::size_t w, std::size_t h,
                       const std::vector<std::pair<int, int>>& offsets, F&& fn) {
    for (const auto& [dx, dy] : offsets) {
        const int nx = static_cast<int>(x) + dx;
        const int ny = static_cast<int>(y) + dy;
        if (nx < 0 || ny < 0 || nx >= static_cast<int>(w) || ny >= static_cast<int>(h))
            continue;
        fn(static_cast<std::size_t>(ny) * w + static_cast<std::size_t>(nx));
    }
}

}  // namespace

std::optional<ReconstructionResult> reconstruct_by_dilation(
    const GrayImage& marker, const GrayImage& mask, Connectivity conn) {
    if (marker.width != mask.width || marker.height != mask.height) return std::nullopt;
    const std::size_t w = marker.width;
    const std::size_t h = marker.height;
    const std::size_t n = w * h;
    if (marker.pixels.size() != n || mask.pixels.size() != n) return std::nullopt;

    // 输入校验：标记必须处处不高于掩码，否则拒绝。
    for (std::size_t i = 0; i < n; ++i) {
        if (marker.pixels[i] > mask.pixels[i]) return std::nullopt;
    }

    ReconstructionResult result;
    result.image = GrayImage{w, h, marker.pixels};
    std::vector<std::uint8_t>& J = result.image.pixels;
    const std::vector<std::uint8_t>& M = mask.pixels;
    std::size_t updates = 0;

    const Offsets off = make_offsets(conn);

    // 阶段 1：正向光栅扫描（用已访问的“前序”邻居传播）。
    for (std::size_t y = 0; y < h; ++y) {
        for (std::size_t x = 0; x < w; ++x) {
            const std::size_t i = y * w + x;
            std::uint8_t best = J[i];
            for_each_neighbor(x, y, w, h, off.backward,
                              [&](std::size_t q) { best = std::max(best, J[q]); });
            const std::uint8_t v = std::min(best, M[i]);
            if (v != J[i]) {
                J[i] = v;
                ++updates;
            }
        }
    }

    // 阶段 2：反向光栅扫描（用“后序”邻居传播）。
    for (std::size_t yy = h; yy-- > 0;) {
        for (std::size_t xx = w; xx-- > 0;) {
            const std::size_t i = yy * w + xx;
            std::uint8_t best = J[i];
            for_each_neighbor(xx, yy, w, h, off.forward,
                              [&](std::size_t q) { best = std::max(best, J[q]); });
            const std::uint8_t v = std::min(best, M[i]);
            if (v != J[i]) {
                J[i] = v;
                ++updates;
            }
        }
    }

    // 阶段 3：FIFO 队列传播，只处理仍可能提升邻居的像素。
    std::deque<std::size_t> queue;
    std::vector<bool> in_queue(n, false);
    for (std::size_t y = 0; y < h; ++y) {
        for (std::size_t x = 0; x < w; ++x) {
            const std::size_t i = y * w + x;
            bool can_raise = false;
            for_each_neighbor(x, y, w, h, off.all, [&](std::size_t q) {
                if (J[q] < J[i] && J[q] < M[q]) can_raise = true;
            });
            if (can_raise) {
                queue.push_back(i);
                in_queue[i] = true;
            }
        }
    }

    while (!queue.empty()) {
        const std::size_t p = queue.front();
        queue.pop_front();
        in_queue[p] = false;
        const std::size_t px = p % w;
        const std::size_t py = p / w;
        for_each_neighbor(px, py, w, h, off.all, [&](std::size_t q) {
            if (J[q] >= J[p] || J[q] >= M[q]) return;
            const std::uint8_t v = std::min(J[p], M[q]);
            if (v > J[q]) {
                J[q] = v;
                ++updates;
                if (!in_queue[q]) {
                    queue.push_back(q);
                    in_queue[q] = true;
                }
            }
        });
    }

    // 固定点校验：对每个像素验证 J[p] == min(mask[p], max(J[p], 邻居 J 最大值))。
    bool fixed_point = true;
    for (std::size_t y = 0; y < h && fixed_point; ++y) {
        for (std::size_t x = 0; x < w; ++x) {
            const std::size_t i = y * w + x;
            std::uint8_t best = J[i];
            for_each_neighbor(x, y, w, h, off.all,
                              [&](std::size_t q) { best = std::max(best, J[q]); });
            if (J[i] != std::min(best, M[i])) {
                fixed_point = false;
                break;
            }
        }
    }

    result.update_count = updates;
    result.fixed_point = fixed_point;
    return result;
}

}  // namespace morpho
