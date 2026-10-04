#include "reconstruction.hpp"

#include <cstdio>
#include <random>

using namespace morpho;

static int g_failures = 0;

#define CHECK(cond)                                                       \
    do {                                                                  \
        if (!(cond)) {                                                    \
            std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);   \
            ++g_failures;                                                 \
        }                                                                 \
    } while (0)

// 朴素参考实现：整图反复扫描直到稳定，用于对拍。
static GrayImage naive_reconstruct(const GrayImage& marker, const GrayImage& mask,
                                   Connectivity conn) {
    GrayImage J{marker.width, marker.height, marker.pixels};
    const std::size_t w = marker.width, h = marker.height;
    const int dirs8[8][2] = {{-1,-1},{0,-1},{1,-1},{-1,0},{1,0},{-1,1},{0,1},{1,1}};
    const int dirs4[4][2] = {{0,-1},{-1,0},{1,0},{0,1}};
    bool changed = true;
    while (changed) {
        changed = false;
        for (std::size_t y = 0; y < h; ++y) {
            for (std::size_t x = 0; x < w; ++x) {
                std::uint8_t best = J.at(x, y);
                const int ndirs = (conn == Connectivity::Eight) ? 8 : 4;
                const int (*dirs)[2] = (conn == Connectivity::Eight) ? dirs8 : dirs4;
                for (int d = 0; d < ndirs; ++d) {
                    const int nx = static_cast<int>(x) + dirs[d][0];
                    const int ny = static_cast<int>(y) + dirs[d][1];
                    if (nx < 0 || ny < 0 || nx >= static_cast<int>(w) || ny >= static_cast<int>(h))
                        continue;
                    if (J.at(nx, ny) > best) best = J.at(nx, ny);
                }
                const std::uint8_t v = std::min(best, mask.at(x, y));
                if (v != J.at(x, y)) {
                    J.at(x, y) = v;
                    changed = true;
                }
            }
        }
    }
    return J;
}

// 狭长蜿蜒通道：掩码中一条 1 像素宽的高值走廊，其余为低值背景。
static void test_narrow_channel() {
    const std::size_t w = 40, h = 7;
    GrayImage mask = GrayImage::filled(w, h, 50);
    // 蛇形走廊：从 (0,1) 向右，下行，折返，再下行到 (39,5)。
    for (std::size_t x = 0; x < w; ++x) mask.at(x, 1) = 200;
    mask.at(w - 1, 2) = 200;
    for (std::size_t x = 0; x < w; ++x) mask.at(x, 3) = 200;
    mask.at(0, 4) = 200;
    for (std::size_t x = 0; x < w; ++x) mask.at(x, 5) = 200;

    GrayImage marker = GrayImage::filled(w, h, 0);
    marker.at(0, 1) = 200;  // 仅在走廊入口放种子

    auto res = reconstruct_by_dilation(marker, mask, Connectivity::Eight);
    CHECK(res.has_value());
    CHECK(res->fixed_point);
    // 走廊被填满 200；背景掩码为 50，与种子在 mask>=50 上连通，故提升到 50。
    for (std::size_t x = 0; x < w; ++x) {
        CHECK(res->image.at(x, 1) == 200);
        CHECK(res->image.at(x, 3) == 200);
        CHECK(res->image.at(x, 5) == 200);
    }
    CHECK(res->image.at(w - 1, 2) == 200);
    CHECK(res->image.at(0, 4) == 200);
    CHECK(res->image.at(20, 0) == 50);
    CHECK(res->image.at(20, 2) == 50);
    CHECK(res->image.at(20, 4) == 50);
    CHECK(res->image.at(20, 6) == 50);
    // 走廊 122 像素中 121 个被提升，背景 158 像素也被提升，更新次数不少于 279。
    CHECK(res->update_count >= 121 + 158);
    std::printf("narrow_channel: updates=%zu fixed_point=%d\n", res->update_count,
                static_cast<int>(res->fixed_point));
}

// 大面积相同灰度平台：整幅图同一灰度，单点种子应填满全部。
static void test_large_plateau() {
    const std::size_t w = 200, h = 150;
    GrayImage mask = GrayImage::filled(w, h, 128);
    GrayImage marker = GrayImage::filled(w, h, 0);
    marker.at(0, 0) = 128;

    auto res = reconstruct_by_dilation(marker, mask, Connectivity::Eight);
    CHECK(res.has_value());
    CHECK(res->fixed_point);
    for (std::size_t i = 0; i < w * h; ++i) CHECK(res->image.pixels[i] == 128);
    // 除种子外每个像素恰好被提升一次。
    CHECK(res->update_count == w * h - 1);
    std::printf("large_plateau: updates=%zu fixed_point=%d\n", res->update_count,
                static_cast<int>(res->fixed_point));
}

// 非法输入：标记高于掩码时必须拒绝。
static void test_reject_marker_above_mask() {
    GrayImage mask = GrayImage::filled(8, 8, 100);
    GrayImage marker = GrayImage::filled(8, 8, 0);
    marker.at(3, 4) = 101;
    auto res = reconstruct_by_dilation(marker, mask);
    CHECK(!res.has_value());

    // 尺寸不一致同样拒绝。
    GrayImage other = GrayImage::filled(4, 4, 0);
    CHECK(!reconstruct_by_dilation(marker, other).has_value());
    std::printf("reject_invalid: ok\n");
}

// 对角通道：8 连通可传播，4 连通不可传播。
static void test_connectivity() {
    const std::size_t n = 10;
    GrayImage mask = GrayImage::filled(n, n, 0);
    for (std::size_t i = 0; i < n; ++i) mask.at(i, i) = 255;
    GrayImage marker = GrayImage::filled(n, n, 0);
    marker.at(0, 0) = 255;

    auto r8 = reconstruct_by_dilation(marker, mask, Connectivity::Eight);
    CHECK(r8.has_value() && r8->fixed_point);
    CHECK(r8->image.at(n - 1, n - 1) == 255);

    auto r4 = reconstruct_by_dilation(marker, mask, Connectivity::Four);
    CHECK(r4.has_value() && r4->fixed_point);
    CHECK(r4->image.at(0, 0) == 255);
    CHECK(r4->image.at(n - 1, n - 1) == 0);
    std::printf("connectivity: updates8=%zu updates4=%zu\n", r8->update_count,
                r4->update_count);
}

// 随机图与朴素实现对拍（两种连通性）。
static void test_against_naive() {
    std::mt19937 rng(42);
    for (int trial = 0; trial < 50; ++trial) {
        const std::size_t w = 3 + rng() % 20;
        const std::size_t h = 3 + rng() % 20;
        GrayImage mask = GrayImage::filled(w, h, 0);
        GrayImage marker = GrayImage::filled(w, h, 0);
        for (std::size_t i = 0; i < w * h; ++i) {
            mask.pixels[i] = static_cast<std::uint8_t>(rng() % 256);
            marker.pixels[i] = static_cast<std::uint8_t>(rng() % 256);
            if (marker.pixels[i] > mask.pixels[i]) marker.pixels[i] = mask.pixels[i];
        }
        for (Connectivity conn : {Connectivity::Four, Connectivity::Eight}) {
            auto res = reconstruct_by_dilation(marker, mask, conn);
            CHECK(res.has_value());
            CHECK(res->fixed_point);
            GrayImage ref = naive_reconstruct(marker, mask, conn);
            CHECK(res->image.pixels == ref.pixels);
        }
    }
    std::printf("random_vs_naive: 50 trials ok\n");
}

// 空图与单像素边界情况。
static void test_edge_cases() {
    GrayImage empty{};
    auto r0 = reconstruct_by_dilation(empty, empty);
    CHECK(r0.has_value() && r0->fixed_point && r0->update_count == 0);

    GrayImage one = GrayImage::filled(1, 1, 7);
    auto r1 = reconstruct_by_dilation(one, one);
    CHECK(r1.has_value() && r1->fixed_point && r1->update_count == 0);
    CHECK(r1->image.at(0, 0) == 7);
    std::printf("edge_cases: ok\n");
}

int main() {
    test_narrow_channel();
    test_large_plateau();
    test_reject_marker_above_mask();
    test_connectivity();
    test_against_naive();
    test_edge_cases();
    if (g_failures == 0) {
        std::printf("ALL TESTS PASSED\n");
        return 0;
    }
    std::printf("%d CHECK(S) FAILED\n", g_failures);
    return 1;
}
