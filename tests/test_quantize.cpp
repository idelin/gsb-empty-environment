// ediff 测试：精确累计、取整规则、边界丢弃计量、镜像↔反向扫描、分块不变性。
#include "ediff/quantize.h"

#include <cstdio>
#include <cstdlib>
#include <vector>

using namespace ediff;

static int g_failures = 0;

#define CHECK(cond)                                                       \
    do {                                                                  \
        if (!(cond)) {                                                    \
            std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);   \
            ++g_failures;                                                 \
        }                                                                 \
    } while (0)

// 确定性伪随机图像（LCG），保证测试可复现。
static Image randomImage(int w, int h, uint32_t seed) {
    std::vector<int> px(static_cast<size_t>(w) * h);
    uint32_t s = seed;
    for (auto& v : px) {
        s = s * 1664525u + 1013904223u;
        v = static_cast<int>((s >> 16) & 0xFF);
    }
    return Image(w, h, std::move(px));
}

// 独立参考实现：整图 int64 分子网格，逐像素模拟同一套规则。
// 与库的行缓冲实现结构不同，用于交叉验证精确累计与扫描顺序。
static Result referenceQuantize(const Image& img, const Options& opt) {
    const int w = img.width, h = img.height;
    const int64_t den = opt.kernel.den;
    std::vector<int64_t> acc(static_cast<size_t>(w) * h, 0);
    Result res;
    res.out.assign(static_cast<size_t>(w) * h, 0);
    res.discarded_den = den;
    for (int y = 0; y < h; ++y) {
        const bool forward = (y % 2 == 0) == (opt.start == StartDir::LeftToRight);
        for (int i = 0; i < w; ++i) {
            const int x = forward ? i : w - 1 - i;
            const int64_t v = static_cast<int64_t>(img.at(x, y)) * den + acc[static_cast<size_t>(y) * w + x];
            const int q = nearestLevel(v, den, opt.levels);
            res.out[static_cast<size_t>(y) * w + x] = q;
            const int64_t e = v - static_cast<int64_t>(q) * den;
            for (const Tap& t : opt.kernel.taps) {
                const int tx = x + (forward ? t.dx : -t.dx);
                const int ty = y + t.dy;
                if (tx >= 0 && tx < w && ty < h)
                    acc[static_cast<size_t>(ty) * w + tx] += e * t.num;
                else
                    res.discarded_num += e * t.num;
            }
        }
    }
    return res;
}

static void testNearestLevel() {
    const std::vector<int> bw{0, 255};
    CHECK(nearestLevel(0, 16, bw) == 0);
    CHECK(nearestLevel(127 * 16, 16, bw) == 0);    // 127 < 127.5 -> 0
    CHECK(nearestLevel(128 * 16, 16, bw) == 255);  // 128 > 127.5 -> 255
    CHECK(nearestLevel(-1, 16, bw) == 0);          // 超出范围钳到端级
    CHECK(nearestLevel(255 * 16 + 1, 16, bw) == 255);
    // 恰好中点 -> 较高级（round-half-up）
    const std::vector<int> even{0, 254};
    CHECK(nearestLevel(127 * 16, 16, even) == 254);
    // 多级调色板
    const std::vector<int> multi{0, 64, 128, 255};
    CHECK(nearestLevel(96 * 16, 16, multi) == 128);  // 96 = (64+128)/2 中点 -> 128
    CHECK(nearestLevel(95 * 16, 16, multi) == 64);
    CHECK(nearestLevel(200 * 16, 16, multi) == 255);
}

static void testSinglePixelDiscard() {
    Options opt;  // levels {0,255}, Floyd-Steinberg
    Image one(1, 1, {128});
    Result r = quantize(one, opt);
    CHECK(r.out[0] == 255);
    // 误差分子 (128-255)*16 = -2032 全部出界：-2032 * (7+3+5+1)，分母 16
    CHECK(r.discarded_num == -2032 * 16);
    CHECK(r.discarded_den == 16);
}

static void testHandComputedRow() {
    // 4x1，像素全 100，前向扫描；逐步手算（分子，分母 16）：
    // x0: v=1600  -> 0,   e=1600    -> x1 += 11200,  丢弃 1600*9=14400
    // x1: v=12800 -> 255, e=8720    -> x2 += 61040,  丢弃 8720*9=78480
    // x2: v=62640 -> 255, e=58560   -> x3 += 409920, 丢弃 58560*9=527040
    // x3: v=411520-> 255, e=407440  -> 全部出界，     丢弃 407440*16=6519040
    Options opt;
    Image row(4, 1, {100, 100, 100, 100});
    Result r = quantize(row, opt);
    CHECK((r.out == std::vector<int>{0, 255, 255, 255}));
    CHECK(r.discarded_num == 14400 + 78480 + 527040 + 6519040);
}

static void testSerpentineDirection() {
    // 验证换行换向：2x2 图像，第 1 行从右向左处理。
    // 像素: row0 = [200, 200], row1 = [10, 10]
    // (0,0): v=3200 -> 255, e=3200-4080=-880
    //        -> (1,0) += -880*7=-6160; (0,1) += -880*5=-4400; (1,1) += -880*1=-880; (-1,1) 丢弃 -880*3=-2640
    // (1,0): v=3200-6160=-2960 -> 0, e=-2960
    //        -> (2,0) 丢弃 -2960*7=-20720; (0,1) += -2960*3=-8880; (1,1) += -2960*5=-14800; (2,1) 丢弃 -2960*1=-2960
    // 第 1 行反向：先 (1,1): v=160-880-14800=-15520 -> 0, e=-15520
    //        -> (0,1) += -15520*7=-108640; (2,1) 丢弃 -15520*3=-46560; (1,2),(0,2) 丢弃 -15520*6=-93120
    // 再 (0,1): v=160-4400-8880-108640=-121760 -> 0, e=-121760
    //        -> (-1,1) 丢弃 -121760*7=-852320; (-1,2),(0,2),(1,2) 丢弃 -121760*9=-1095840
    Options opt;
    Image img(2, 2, {200, 200, 10, 10});
    Result r = quantize(img, opt);
    CHECK((r.out == std::vector<int>{255, 0, 0, 0}));
    const int64_t expect = -2640 - 20720 - 2960 - 46560 - 93120 - 852320 - 1095840;
    CHECK(r.discarded_num == expect);
}

static void testMirrorReverseScan() {
    // 核心性质：镜像输入 + 正向起始 == 原输入 + 反向起始 的镜像。
    for (uint32_t seed = 1; seed <= 20; ++seed) {
        Image img = randomImage(17, 9, seed);
        Image mir = mirrorHorizontal(img);

        Options fwd;  // 起始 LeftToRight
        Options rev;
        rev.start = StartDir::RightToLeft;

        Result a = quantize(mir, fwd);                        // 镜像图，正向起始
        Result b = quantize(img, rev);                        // 原图，反向起始
        CHECK(a.out == mirrorHorizontal(b.out, img.width, img.height));
        CHECK(a.discarded_num == b.discarded_num);

        // 反向也应对称：镜像图 + 反向起始 == 原图 + 正向起始 的镜像
        Result c = quantize(mir, rev);
        Result d = quantize(img, fwd);
        CHECK(c.out == mirrorHorizontal(d.out, img.width, img.height));
        CHECK(c.discarded_num == d.discarded_num);
    }
}

static void testTilingInvariance() {
    for (uint32_t seed = 7; seed <= 12; ++seed) {
        Image img = randomImage(13, 11, seed);  // 奇数尺寸，块不能整除
        for (auto start : {StartDir::LeftToRight, StartDir::RightToLeft}) {
            Options opt;
            opt.start = start;
            Result whole = quantize(img, opt);
            for (int rows : {1, 2, 3, 4, 5, 7, 11, 1000}) {
                Result tiled = quantizeTiled(img, opt, rows);
                CHECK(tiled.out == whole.out);
                CHECK(tiled.discarded_num == whole.discarded_num);
            }
        }
    }
}

static void testAgainstReference() {
    // 库（行缓冲）与独立参考实现（整图网格）在随机图像上必须完全一致。
    for (uint32_t seed = 100; seed < 130; ++seed) {
        Image img = randomImage(1 + seed % 23, 1 + (seed * 7) % 19, seed);
        for (auto start : {StartDir::LeftToRight, StartDir::RightToLeft}) {
            Options opt;
            opt.start = start;
            Result a = quantize(img, opt);
            Result b = referenceQuantize(img, opt);
            CHECK(a.out == b.out);
            CHECK(a.discarded_num == b.discarded_num);
        }
    }
}

static void testCustomKernelAndLevels() {
    // 自定义有理权重（Jarvis 简化版）与多级量化，验证参考一致性。
    Kernel jarvis{{{1, 0, 7}, {2, 0, 5}, {-2, 1, 3}, {-1, 1, 5}, {0, 1, 7}, {1, 1, 5}, {2, 1, 3}}, 48};
    Options opt;
    opt.kernel = jarvis;
    opt.levels = {0, 85, 170, 255};
    Image img = randomImage(21, 14, 42);
    Result a = quantize(img, opt);
    Result b = referenceQuantize(img, opt);
    CHECK(a.out == b.out);
    CHECK(a.discarded_num == b.discarded_num);
    Result t = quantizeTiled(img, opt, 3);
    CHECK(t.out == a.out);
    CHECK(t.discarded_num == a.discarded_num);
    // 输出只取给定灰度级
    for (int v : a.out)
        CHECK(v == 0 || v == 85 || v == 170 || v == 255);
}

int main() {
    testNearestLevel();
    testSinglePixelDiscard();
    testHandComputedRow();
    testSerpentineDirection();
    testMirrorReverseScan();
    testTilingInvariance();
    testAgainstReference();
    testCustomKernelAndLevels();
    if (g_failures == 0) {
        std::printf("ALL TESTS PASSED\n");
        return 0;
    }
    std::printf("%d CHECK(S) FAILED\n", g_failures);
    return 1;
}
