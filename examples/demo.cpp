// 小图逐方向对照示例：构造已知视差的小代价体，打印四个方向各自的
// 视差图与聚合后的最终视差图，并抽样展示单像素的逐方向代价。
#include <cstdint>
#include <cstdio>
#include <vector>

#include "sgm/sgm.h"

namespace {

constexpr int kWidth = 12;
constexpr int kHeight = 8;
constexpr int kNumDisp = 6;

// 真值视差：左半部分为 1，右半部分为 4，中间一列过渡带为 2。
int ground_truth(int x, int /*y*/) {
    if (x < 5) return 1;
    if (x < 7) return 2;
    return 4;
}

// 伪随机噪声（确定性，便于复现）。
int noise(int x, int y, int d) {
    uint32_t h = static_cast<uint32_t>(x * 7349 + y * 15731 + d * 971);
    h ^= h >> 13;
    h *= 0x5bd1e995u;
    h ^= h >> 15;
    return static_cast<int>(h % 7);  // [0, 6]
}

std::vector<int32_t> make_cost() {
    std::vector<int32_t> cost(kWidth * kHeight * kNumDisp);
    for (int y = 0; y < kHeight; ++y) {
        for (int x = 0; x < kWidth; ++x) {
            const int gt = ground_truth(x, y);
            for (int d = 0; d < kNumDisp; ++d) {
                const int diff = d > gt ? d - gt : gt - d;
                cost[(y * kWidth + x) * kNumDisp + d] =
                    16 * diff + noise(x, y, d);
            }
        }
    }
    return cost;
}

void print_map(const char* title, const std::vector<int>& disp) {
    std::printf("%s\n", title);
    for (int y = 0; y < kHeight; ++y) {
        std::printf("  ");
        for (int x = 0; x < kWidth; ++x) std::printf("%2d", disp[y * kWidth + x]);
        std::printf("\n");
    }
}

void print_pixel_costs(const sgm::Aggregator& agg, int x, int y) {
    const size_t base = (static_cast<size_t>(y) * kWidth + x) * kNumDisp;
    std::printf("pixel (%d, %d) per-disparity path cost L_r / aggregated S:\n", x, y);
    std::printf("  d   L2R  R2L  T2B  B2T    S\n");
    for (int d = 0; d < kNumDisp; ++d) {
        std::printf("  %d %5d %4d %4d %4d %5d\n", d,
                    agg.path_cost(sgm::Direction::LeftToRight)[base + d],
                    agg.path_cost(sgm::Direction::RightToLeft)[base + d],
                    agg.path_cost(sgm::Direction::TopToBottom)[base + d],
                    agg.path_cost(sgm::Direction::BottomToTop)[base + d],
                    agg.aggregated_cost()[base + d]);
    }
}

}  // namespace

int main() {
    const std::vector<int32_t> cost = make_cost();

    sgm::Params params;
    params.p1 = 10;
    params.p2 = 40;
    sgm::Aggregator agg(kWidth, kHeight, kNumDisp, params);
    const std::vector<int> final_disp = agg.compute(cost);

    std::vector<int> gt(kWidth * kHeight);
    for (int y = 0; y < kHeight; ++y)
        for (int x = 0; x < kWidth; ++x) gt[y * kWidth + x] = ground_truth(x, y);
    print_map("ground truth:", gt);

    print_map("direction LeftToRight:", agg.direction_disparity(sgm::Direction::LeftToRight));
    print_map("direction RightToLeft:", agg.direction_disparity(sgm::Direction::RightToLeft));
    print_map("direction TopToBottom:", agg.direction_disparity(sgm::Direction::TopToBottom));
    print_map("direction BottomToTop:", agg.direction_disparity(sgm::Direction::BottomToTop));
    print_map("aggregated (4 dirs, local cost counted once):", final_disp);

    std::printf("\n");
    print_pixel_costs(agg, 3, 3);   // 平坦区，真值 1
    print_pixel_costs(agg, 8, 3);   // 跳变边缘附近，真值 4
    return 0;
}
