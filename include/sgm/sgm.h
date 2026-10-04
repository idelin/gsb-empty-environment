#pragma once

#include <array>
#include <cstdint>
#include <vector>

namespace sgm {

// 聚合参数：P1 惩罚相邻视差 ±1 的小跳变，P2 惩罚更大的视差跳变。
struct Params {
    int p1 = 10;
    int p2 = 120;
};

// 四个聚合方向（扫描前进的步进方向）。
enum class Direction : int {
    LeftToRight = 0,
    RightToLeft = 1,
    TopToBottom = 2,
    BottomToTop = 3,
};
inline constexpr int kNumDirections = 4;

// 半全局匹配代价聚合器。
//
// 输入为逐像素逐视差的整数代价体 C(y, x, d)，布局为 [(y * W + x) * D + d]。
// 沿四个方向分别做动态规划：
//   L_r(p, d) = C(p, d) + min( L_r(p-r, d),
//                              L_r(p-r, d-1) + P1,
//                              L_r(p-r, d+1) + P1,
//                              min_k L_r(p-r, k) + P2 )
//               - min_k L_r(p-r, k)            // 代价归一化，防止数值膨胀
// 边界重启：p-r 越界时 L_r(p, d) = C(p, d)。
// 聚合时本地代价被四个方向各计一次，需减去 3 份以避免重复累计：
//   S(p, d) = sum_r L_r(p, d) - 3 * C(p, d)
// 视差选择采用赢家通吃，并列时按固定规则取最小视差。
class Aggregator {
public:
    Aggregator(int width, int height, int num_disparities, Params params);

    int width() const { return width_; }
    int height() const { return height_; }
    int num_disparities() const { return num_disparities_; }

    // 运行聚合，返回逐像素视差图（尺寸 W * H）。
    std::vector<int> compute(const std::vector<int32_t>& cost);

    // 某一方向 DP 后的路径代价 L_r，布局与输入代价体一致。
    const std::vector<int32_t>& path_cost(Direction dir) const;

    // 某一方向单独做赢家通吃得到的视差图（用于逐方向对照）。
    std::vector<int> direction_disparity(Direction dir) const;

    // 聚合代价 S(p, d) = sum_r L_r - 3 * C，布局与输入代价体一致。
    const std::vector<int32_t>& aggregated_cost() const { return aggregated_; }

    // 固定并列规则：扫描视差从 0 到 D-1，严格更小才更新，故并列取最小视差。
    static int select_disparity(const int32_t* costs, int num_disparities);

private:
    void run_direction(Direction dir, const std::vector<int32_t>& cost,
                       std::vector<int32_t>& out);

    int width_;
    int height_;
    int num_disparities_;
    Params params_;

    std::array<std::vector<int32_t>, kNumDirections> path_costs_;
    std::vector<int32_t> aggregated_;
};

}  // namespace sgm
