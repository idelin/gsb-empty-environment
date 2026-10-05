#include <cstdint>
#include <cstdio>
#include <vector>

#include "sgm.hpp"

namespace {

void print_map(const char* title, const std::vector<int>& map, int w, int h) {
    std::printf("%s\n", title);
    for (int y = 0; y < h; ++y) {
        std::printf("  ");
        for (int x = 0; x < w; ++x) std::printf("%2d", map[static_cast<size_t>(y) * w + x]);
        std::printf("\n");
    }
}

}  // namespace

int main() {
    // ---- 小图示例: 12x6, 视差范围 [0, 6) ----
    const int w = 12, h = 6, nd = 6;

    // 真值视差: 左半为 1, 右半为 3, 中间一列 2 形成过渡
    std::vector<int> gt(static_cast<size_t>(w) * h);
    for (int y = 0; y < h; ++y)
        for (int x = 0; x < w; ++x)
            gt[static_cast<size_t>(y) * w + x] = (x < 5) ? 1 : (x < 7 ? 2 : 3);

    // 整数代价体: |d - gt| * 20 加确定性小扰动
    std::vector<int32_t> costs(static_cast<size_t>(w) * h * nd);
    for (int y = 0; y < h; ++y)
        for (int x = 0; x < w; ++x)
            for (int d = 0; d < nd; ++d) {
                const int g = gt[static_cast<size_t>(y) * w + x];
                const int noise = ((x * 7 + y * 13 + d * 3) % 11) * 3;
                costs[(static_cast<size_t>(y) * w + x) * nd + d] =
                    20 * std::abs(d - g) + noise;
            }

    sgm::Params params;
    params.p1 = 10;
    params.p2 = 120;
    sgm::Aggregator agg(w, h, nd, params);
    const std::vector<int> disp = agg.compute(costs);

    print_map("ground truth:", gt, w, h);

    const char* dir_names[sgm::Aggregator::kNumDirections] = {
        "L->R", "R->L", "T->B", "B->T"};
    for (int dir = 0; dir < sgm::Aggregator::kNumDirections; ++dir) {
        char title[64];
        std::snprintf(title, sizeof(title), "direction %d (%s) WTA disparity:", dir, dir_names[dir]);
        print_map(title, agg.direction_disparities(dir), w, h);
    }
    print_map("final aggregated disparity (4-dir sum, WTA):", disp, w, h);

    // 抽样像素 (x=6, y=2): 逐方向聚合代价对照
    const int sx = 6, sy = 2;
    const size_t base = (static_cast<size_t>(sy) * w + sx) * nd;
    std::printf("per-direction aggregated costs at pixel (x=%d, y=%d):\n", sx, sy);
    std::printf("  d      ");
    for (int d = 0; d < nd; ++d) std::printf("%6d", d);
    std::printf("\n");
    for (int dir = 0; dir < sgm::Aggregator::kNumDirections; ++dir) {
        std::printf("  %-5s ", dir_names[dir]);
        for (int d = 0; d < nd; ++d)
            std::printf("%6d", agg.direction_costs(dir)[base + d]);
        std::printf("\n");
    }
    std::printf("  %-5s ", "sum");
    for (int d = 0; d < nd; ++d) std::printf("%6d", agg.aggregated_costs()[base + d]);
    std::printf("\n");

    // ---- 并列视差固定规则演示: 1x1 图, d=0 与 d=1 代价相同 ----
    sgm::Aggregator tie_agg(1, 1, 3, params);
    std::vector<int32_t> tie_costs = {5, 5, 9};
    const std::vector<int> tie_disp = tie_agg.compute(tie_costs);
    std::printf("tie-break demo: costs {5, 5, 9} -> disparity %d "
                "(fixed rule: smallest disparity wins)\n", tie_disp[0]);

    return 0;
}
