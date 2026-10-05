#pragma once

#include <cstdint>
#include <vector>

namespace sgm {

// 聚合参数
struct Params {
    int p1 = 10;   // 小跳惩罚: 相邻像素视差差 == 1
    int p2 = 120;  // 大跳惩罚: 相邻像素视差差 >= 2
};

// 视差聚合器:
//   输入逐像素逐视差整数代价体, 沿 4 个方向(左->右, 右->左, 上->下, 下->上)
//   做带 P1/P2 惩罚的动态规划, 逐方向代价归一化并在边界重启,
//   最后对四方向代价求和, 按 WTA 选视差; 并列时固定取最小视差。
class Aggregator {
public:
    static constexpr int kNumDirections = 4;

    Aggregator(int width, int height, int num_disparities, Params params = {});

    // costs 布局: costs[(y * width + x) * num_disparities + d]
    // 返回视差图, 大小 width * height, 元素取值 [0, num_disparities)
    std::vector<int> compute(const std::vector<int32_t>& costs);

    // 第 dir 个方向的聚合代价 L_r, 布局与输入代价体一致
    const std::vector<int32_t>& direction_costs(int dir) const { return dir_costs_[dir]; }

    // 四方向求和后的总代价 S(p, d)
    const std::vector<int32_t>& aggregated_costs() const { return sum_costs_; }

    // 仅按单个方向代价做 WTA 得到的视差图(用于逐方向对照)
    std::vector<int> direction_disparities(int dir) const;

    int width() const { return width_; }
    int height() const { return height_; }
    int num_disparities() const { return num_disparities_; }

private:
    void scan_direction(int dir, const std::vector<int32_t>& costs);

    // 固定并列规则: 取最小视差
    int select_disparity(const int32_t* cost_row) const;

    int width_;
    int height_;
    int num_disparities_;
    Params params_;

    std::vector<int32_t> dir_costs_[kNumDirections];
    std::vector<int32_t> sum_costs_;
};

}  // namespace sgm
