#include <cstdint>
#include <cstdio>
#include <vector>

#include "sgm/sgm.h"

namespace {

int g_failures = 0;

void check(bool ok, const char* name) {
    std::printf("[%s] %s\n", ok ? "PASS" : "FAIL", name);
    if (!ok) ++g_failures;
}

size_t idx(int x, int y, int d, int w, int nd) {
    return (static_cast<size_t>(y) * w + x) * nd + d;
}

// 均匀代价下：L_r == C 处处成立（归一化使路径代价不随长度膨胀），
// 聚合 S = 4C - 3C = C（本地代价只计一次），全部并列时视差取 0。
void test_uniform_cost() {
    const int w = 7, h = 5, nd = 4;
    std::vector<int32_t> cost(w * h * nd, 42);
    sgm::Params p{3, 30};
    sgm::Aggregator agg(w, h, nd, p);
    const auto disp = agg.compute(cost);

    bool path_ok = true, agg_ok = true, disp_ok = true;
    for (int r = 0; r < sgm::kNumDirections; ++r) {
        const auto& pc = agg.path_cost(static_cast<sgm::Direction>(r));
        for (auto v : pc) path_ok &= (v == 42);
    }
    for (auto v : agg.aggregated_cost()) agg_ok &= (v == 42);
    for (auto v : disp) disp_ok &= (v == 0);
    check(path_ok, "uniform cost: normalized path cost stays 42");
    check(agg_ok, "uniform cost: aggregation counts local cost once");
    check(disp_ok, "uniform cost: all-tie picks disparity 0");
}

// 四个方向的扫描起点（边界）必须重启为本地代价。
void test_boundary_restart() {
    const int w = 4, h = 3, nd = 3;
    std::vector<int32_t> cost(w * h * nd);
    for (size_t i = 0; i < cost.size(); ++i) cost[i] = static_cast<int32_t>((i * 7 + 3) % 11);

    sgm::Aggregator agg(w, h, nd, sgm::Params{2, 20});
    agg.compute(cost);

    const auto& l2r = agg.path_cost(sgm::Direction::LeftToRight);
    const auto& r2l = agg.path_cost(sgm::Direction::RightToLeft);
    const auto& t2b = agg.path_cost(sgm::Direction::TopToBottom);
    const auto& b2t = agg.path_cost(sgm::Direction::BottomToTop);

    bool ok = true;
    for (int y = 0; y < h; ++y) {
        for (int d = 0; d < nd; ++d) {
            ok &= l2r[idx(0, y, d, w, nd)] == cost[idx(0, y, d, w, nd)];
            ok &= r2l[idx(w - 1, y, d, w, nd)] == cost[idx(w - 1, y, d, w, nd)];
        }
    }
    for (int x = 0; x < w; ++x) {
        for (int d = 0; d < nd; ++d) {
            ok &= t2b[idx(x, 0, d, w, nd)] == cost[idx(x, 0, d, w, nd)];
            ok &= b2t[idx(x, h - 1, d, w, nd)] == cost[idx(x, h - 1, d, w, nd)];
        }
    }
    check(ok, "boundary restart: L == C at scan origins");
}

// 手工核算的 1 行 DP 递推（含 P1 小跳与归一化）。
void test_dp_recurrence() {
    const int w = 3, h = 1, nd = 3;
    // cost(x, d) = |d - 1| * 5，与 x 无关。
    std::vector<int32_t> cost(w * h * nd);
    for (int x = 0; x < w; ++x) {
        cost[idx(x, 0, 0, w, nd)] = 5;
        cost[idx(x, 0, 1, w, nd)] = 0;
        cost[idx(x, 0, 2, w, nd)] = 5;
    }
    sgm::Aggregator agg(w, h, nd, sgm::Params{1, 100});
    const auto disp = agg.compute(cost);
    const auto& l2r = agg.path_cost(sgm::Direction::LeftToRight);

    bool ok = true;
    // x=0：边界重启。
    ok &= l2r[idx(0, 0, 0, w, nd)] == 5 && l2r[idx(0, 0, 1, w, nd)] == 0 &&
          l2r[idx(0, 0, 2, w, nd)] == 5;
    // x>=1：best = min(L[d], L[d±1]+1, min+100)，min_prev = 0。
    for (int x = 1; x < w; ++x) {
        ok &= l2r[idx(x, 0, 0, w, nd)] == 6;   // 5 + min(5, 0+1, 100)
        ok &= l2r[idx(x, 0, 1, w, nd)] == 0;   // 0 + min(0, 6, 6, 100)
        ok &= l2r[idx(x, 0, 2, w, nd)] == 6;
    }
    for (auto v : disp) ok &= (v == 1);
    check(ok, "dp recurrence with P1 jump and normalization");
}

// 大跳惩罚：相邻像素视差突变时走 P2 分支。
void test_p2_branch() {
    const int w = 2, h = 1, nd = 3;
    // x=0 偏好 d=0，x=1 强烈偏好 d=2。
    std::vector<int32_t> cost = {
        0, 50, 50,   // x = 0
        50, 50, 0,   // x = 1
    };
    sgm::Aggregator agg(w, h, nd, sgm::Params{4, 10});
    agg.compute(cost);
    const auto& l2r = agg.path_cost(sgm::Direction::LeftToRight);

    // x=1, d=2：best = min(L0[2]=50, L0[1]+4=54, min_prev+10=10) = 10（P2 分支）。
    const bool ok = l2r[idx(1, 0, 2, w, nd)] == 0 + 10 - 0;
    check(ok, "large jump uses P2 branch");
}

// 并列视差按固定规则取最小值。
void test_tie_break() {
    std::vector<int32_t> costs = {9, 3, 3, 7, 3};
    check(sgm::Aggregator::select_disparity(costs.data(), 5) == 1,
          "tie break: smallest disparity wins");
}

}  // namespace

int main() {
    test_uniform_cost();
    test_boundary_restart();
    test_dp_recurrence();
    test_p2_branch();
    test_tie_break();
    if (g_failures == 0) {
        std::printf("all tests passed\n");
        return 0;
    }
    std::printf("%d test(s) failed\n", g_failures);
    return 1;
}
