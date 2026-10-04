#include "sgm/sgm.h"

#include <algorithm>
#include <limits>
#include <stdexcept>

namespace sgm {

namespace {

struct Step {
    int dx;
    int dy;
};

constexpr std::array<Step, kNumDirections> kSteps = {{
    {1, 0},   // LeftToRight
    {-1, 0},  // RightToLeft
    {0, 1},   // TopToBottom
    {0, -1},  // BottomToTop
}};

}  // namespace

Aggregator::Aggregator(int width, int height, int num_disparities, Params params)
    : width_(width),
      height_(height),
      num_disparities_(num_disparities),
      params_(params) {
    if (width_ <= 0 || height_ <= 0 || num_disparities_ <= 0) {
        throw std::invalid_argument("width/height/num_disparities must be positive");
    }
    if (params_.p1 < 0 || params_.p2 < 0 || params_.p2 < params_.p1) {
        throw std::invalid_argument("require 0 <= p1 <= p2");
    }
    const size_t volume = static_cast<size_t>(width_) * height_ * num_disparities_;
    for (auto& pc : path_costs_) pc.assign(volume, 0);
    aggregated_.assign(volume, 0);
}

const std::vector<int32_t>& Aggregator::path_cost(Direction dir) const {
    return path_costs_[static_cast<int>(dir)];
}

int Aggregator::select_disparity(const int32_t* costs, int num_disparities) {
    int best_d = 0;
    for (int d = 1; d < num_disparities; ++d) {
        // 严格小于才更新：并列时保留更小的视差。
        if (costs[d] < costs[best_d]) best_d = d;
    }
    return best_d;
}

void Aggregator::run_direction(Direction dir, const std::vector<int32_t>& cost,
                               std::vector<int32_t>& out) {
    const int w = width_;
    const int h = height_;
    const int nd = num_disparities_;
    const Step step = kSteps[static_cast<int>(dir)];

    // 按方向确定扫描范围，保证前驱 p-step 已计算。
    const int x_begin = step.dx > 0 ? 0 : (step.dx < 0 ? w - 1 : 0);
    const int x_end = step.dx > 0 ? w : (step.dx < 0 ? -1 : w);
    const int y_begin = step.dy > 0 ? 0 : (step.dy < 0 ? h - 1 : 0);
    const int y_end = step.dy > 0 ? h : (step.dy < 0 ? -1 : h);
    const int x_inc = step.dx < 0 ? -1 : 1;
    const int y_inc = step.dy < 0 ? -1 : 1;

    for (int y = y_begin; y != y_end; y += y_inc) {
        for (int x = x_begin; x != x_end; x += x_inc) {
            const size_t base = (static_cast<size_t>(y) * w + x) * nd;
            const int px = x - step.dx;
            const int py = y - step.dy;

            if (px < 0 || px >= w || py < 0 || py >= h) {
                // 边界重启：无前驱，路径代价直接等于本地代价。
                for (int d = 0; d < nd; ++d) out[base + d] = cost[base + d];
                continue;
            }

            const size_t prev_base = (static_cast<size_t>(py) * w + px) * nd;
            int32_t min_prev = std::numeric_limits<int32_t>::max();
            for (int d = 0; d < nd; ++d) {
                min_prev = std::min(min_prev, out[prev_base + d]);
            }

            for (int d = 0; d < nd; ++d) {
                int32_t best = out[prev_base + d];
                if (d > 0) best = std::min(best, out[prev_base + d - 1] + params_.p1);
                if (d + 1 < nd) best = std::min(best, out[prev_base + d + 1] + params_.p1);
                best = std::min(best, min_prev + params_.p2);
                // 代价归一化：减去前驱最小值，数值不随路径长度膨胀。
                out[base + d] = cost[base + d] + best - min_prev;
            }
        }
    }
}

std::vector<int> Aggregator::compute(const std::vector<int32_t>& cost) {
    const size_t volume = static_cast<size_t>(width_) * height_ * num_disparities_;
    if (cost.size() != volume) {
        throw std::invalid_argument("cost volume size mismatch");
    }

    for (int i = 0; i < kNumDirections; ++i) {
        run_direction(static_cast<Direction>(i), cost, path_costs_[i]);
    }

    // 聚合：本地代价被四个方向各累计一次，减去 3 份避免重复。
    for (size_t i = 0; i < volume; ++i) {
        int32_t sum = 0;
        for (int r = 0; r < kNumDirections; ++r) sum += path_costs_[r][i];
        aggregated_[i] = sum - (kNumDirections - 1) * cost[i];
    }

    std::vector<int> disparity(static_cast<size_t>(width_) * height_);
    const int nd = num_disparities_;
    for (size_t p = 0; p < disparity.size(); ++p) {
        disparity[p] = select_disparity(&aggregated_[p * nd], nd);
    }
    return disparity;
}

std::vector<int> Aggregator::direction_disparity(Direction dir) const {
    const auto& pc = path_costs_[static_cast<int>(dir)];
    const int nd = num_disparities_;
    std::vector<int> disparity(static_cast<size_t>(width_) * height_);
    for (size_t p = 0; p < disparity.size(); ++p) {
        disparity[p] = select_disparity(&pc[p * nd], nd);
    }
    return disparity;
}

}  // namespace sgm
