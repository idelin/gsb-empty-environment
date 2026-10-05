#include "sgm.hpp"

#include <algorithm>
#include <cassert>
#include <limits>
#include <stdexcept>

namespace sgm {

namespace {

// 4 个扫描方向: 左->右, 右->左, 上->下, 下->上
constexpr int kDx[Aggregator::kNumDirections] = {1, -1, 0, 0};
constexpr int kDy[Aggregator::kNumDirections] = {0, 0, 1, -1};

}  // namespace

Aggregator::Aggregator(int width, int height, int num_disparities, Params params)
    : width_(width),
      height_(height),
      num_disparities_(num_disparities),
      params_(params) {
    if (width_ <= 0 || height_ <= 0 || num_disparities_ <= 0) {
        throw std::invalid_argument("width/height/num_disparities must be positive");
    }
    if (params_.p1 < 0 || params_.p2 < 0) {
        throw std::invalid_argument("p1/p2 must be non-negative");
    }
    const size_t volume = static_cast<size_t>(width_) * height_ * num_disparities_;
    for (auto& dc : dir_costs_) dc.assign(volume, 0);
    sum_costs_.assign(volume, 0);
}

std::vector<int> Aggregator::compute(const std::vector<int32_t>& costs) {
    const size_t volume = static_cast<size_t>(width_) * height_ * num_disparities_;
    if (costs.size() != volume) {
        throw std::invalid_argument("cost volume size mismatch");
    }

    std::fill(sum_costs_.begin(), sum_costs_.end(), 0);
    for (int dir = 0; dir < kNumDirections; ++dir) {
        scan_direction(dir, costs);
        // 累加进总代价 S(p, d)
        for (size_t i = 0; i < volume; ++i) sum_costs_[i] += dir_costs_[dir][i];
    }

    std::vector<int> disparity(static_cast<size_t>(width_) * height_);
    for (size_t p = 0; p < disparity.size(); ++p) {
        disparity[p] = select_disparity(&sum_costs_[p * num_disparities_]);
    }
    return disparity;
}

void Aggregator::scan_direction(int dir, const std::vector<int32_t>& costs) {
    const int dx = kDx[dir];
    const int dy = kDy[dir];
    const int d_count = num_disparities_;
    std::vector<int32_t>& out = dir_costs_[dir];

    // 按扫描方向确定遍历顺序, 保证前驱 p - r 先被计算
    const int x_begin = dx >= 0 ? 0 : width_ - 1;
    const int x_end = dx >= 0 ? width_ : -1;
    const int x_step = dx >= 0 ? 1 : -1;
    const int y_begin = dy >= 0 ? 0 : height_ - 1;
    const int y_end = dy >= 0 ? height_ : -1;
    const int y_step = dy >= 0 ? 1 : -1;

    for (int y = y_begin; y != y_end; y += y_step) {
        for (int x = x_begin; x != x_end; x += x_step) {
            const size_t base = (static_cast<size_t>(y) * width_ + x) * d_count;
            const int32_t* local = &costs[base];
            int32_t* cur = &out[base];

            const int px = x - dx;
            const int py = y - dy;
            if (px < 0 || px >= width_ || py < 0 || py >= height_) {
                // 边界重启: 无前驱, 路径代价直接等于本地代价
                for (int d = 0; d < d_count; ++d) cur[d] = local[d];
                continue;
            }

            const size_t prev_base = (static_cast<size_t>(py) * width_ + px) * d_count;
            const int32_t* prev = &out[prev_base];

            // 前驱路径最小值, 既用于大跳惩罚项, 也用于归一化
            const int32_t prev_min = *std::min_element(prev, prev + d_count);

            for (int d = 0; d < d_count; ++d) {
                int32_t best = prev[d];                       // 视差不变
                if (d > 0) best = std::min(best, prev[d - 1] + params_.p1);        // 小跳 +1
                if (d + 1 < d_count) best = std::min(best, prev[d + 1] + params_.p1);  // 小跳 -1
                best = std::min(best, prev_min + params_.p2);  // 大跳
                // 代价归一化: 减去前驱最小值, 本地代价每像素每方向只累计一次,
                // 同时把路径代价限制在有界范围内, 避免溢出
                cur[d] = local[d] + best - prev_min;
            }
        }
    }
}

int Aggregator::select_disparity(const int32_t* cost_row) const {
    int best_d = 0;
    for (int d = 1; d < num_disparities_; ++d) {
        // 严格小于才更新: 并列时固定保留最小视差
        if (cost_row[d] < cost_row[best_d]) best_d = d;
    }
    return best_d;
}

std::vector<int> Aggregator::direction_disparities(int dir) const {
    assert(dir >= 0 && dir < kNumDirections);
    std::vector<int> disparity(static_cast<size_t>(width_) * height_);
    const std::vector<int32_t>& dc = dir_costs_[dir];
    for (size_t p = 0; p < disparity.size(); ++p) {
        disparity[p] = select_disparity(&dc[p * num_disparities_]);
    }
    return disparity;
}

}  // namespace sgm
