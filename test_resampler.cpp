// test_resampler.cpp — RationalResampler 单元测试与切块一致性压力测试。
#include "resampler.hpp"

#include <cstdlib>
#include <iostream>
#include <random>
#include <vector>

using resample::RationalResampler;
using resample::detail::BigInt;

static int g_failures = 0;

#define CHECK(cond)                                                        \
    do {                                                                   \
        if (!(cond)) {                                                     \
            std::cerr << "FAIL " << __FILE__ << ":" << __LINE__ << "  "    \
                      << #cond << std::endl;                               \
            ++g_failures;                                                  \
        }                                                                  \
    } while (0)

// 独立朴素参考实现：直接按定义计算，不走流式状态机。
static std::vector<int64_t> reference(const std::vector<int64_t>& in, int64_t up,
                                      int64_t down,
                                      const std::vector<RationalResampler::Coeff>& kernel) {
    int64_t g = std::gcd(up, down);
    const int64_t P = up / g, Q = down / g;
    const int64_t L = static_cast<int64_t>(kernel.size());
    // 通分。
    int64_t lcm = 1;
    std::vector<int64_t> nn(kernel.size()), dd(kernel.size());
    for (size_t i = 0; i < kernel.size(); ++i) {
        int64_t n = kernel[i].num, d = kernel[i].den;
        if (d < 0) { n = -n; d = -d; }
        int64_t cg = std::gcd(n < 0 ? -n : n, d);
        nn[i] = n / cg; dd[i] = d / cg;
        lcm = lcm / std::gcd(lcm, dd[i]) * dd[i];
    }
    const int64_t N = static_cast<int64_t>(in.size());
    const int64_t M = N == 0 ? 0 : ((N - 1) * P + (L - 1)) / Q + 1;
    std::vector<int64_t> out;
    for (int64_t m = 0; m < M; ++m) {
        BigInt acc;
        for (int64_t j = 0; j < L; ++j) {
            int64_t t = m * Q - j;
            if (t < 0) break;
            if (t % P != 0) continue;
            int64_t i = t / P;
            if (i >= N) continue;
            acc.addProduct(in[static_cast<size_t>(i)],
                           nn[static_cast<size_t>(j)] * (lcm / dd[static_cast<size_t>(j)]));
        }
        out.push_back(acc.divRoundHalfAway(lcm));
    }
    return out;
}

static std::vector<int64_t> chunked(const std::vector<int64_t>& in, int64_t up,
                                    int64_t down,
                                    const std::vector<RationalResampler::Coeff>& kernel,
                                    const std::vector<size_t>& chunks) {
    RationalResampler r(up, down, kernel);
    std::vector<int64_t> out;
    size_t pos = 0;
    for (size_t c : chunks) {
        size_t n = std::min(c, in.size() - pos);
        r.process(in.data() + pos, n, out);
        pos += n;
    }
    if (pos < in.size()) r.process(in.data() + pos, in.size() - pos, out);
    r.flush(out);
    return out;
}

static void expectEq(const std::vector<int64_t>& a, const std::vector<int64_t>& b,
                     const char* what) {
    if (a != b) {
        std::cerr << "FAIL mismatch: " << what << " (sizes " << a.size() << " vs "
                  << b.size() << ")" << std::endl;
        size_t n = std::min(a.size(), b.size());
        for (size_t i = 0; i < n; ++i)
            if (a[i] != b[i]) {
                std::cerr << "  first diff at " << i << ": " << a[i] << " vs " << b[i]
                          << std::endl;
                break;
            }
        ++g_failures;
    }
}

// ---------- 手工可验证的算例 ----------

static void testIdentity() {
    std::vector<int64_t> in = {5, -3, 0, 7, 1000000007LL};
    auto out = resample::resample(in, 1, 1, {{1, 1}});
    expectEq(out, in, "identity 1/1 kernel{1}");
}

static void testMovingSumTail() {
    // P=Q=1, h={1,1,1}: y = 滑动和，尾部零填充。
    auto out = resample::resample({1, 2, 3}, 1, 1, {{1, 1}, {1, 1}, {1, 1}});
    expectEq(out, {1, 3, 6, 5, 3}, "moving sum with tail flush");
}

static void testUpsample2() {
    // P=2,Q=1, h={1,1}（线性插值的一半结构）：y[2i]=x[i], y[2i+1]=x[i]。
    auto out = resample::resample({4, 6}, 2, 1, {{1, 1}, {1, 1}});
    // m=0: x0*h0 = 4; m=1: x0*h1 = 4; m=2: x1*h0 = 6; m=3: x1*h1 = 6
    expectEq(out, {4, 4, 6, 6}, "upsample 2/1");
}

static void testDownsample2() {
    // P=1,Q=2, h={1}: y[m] = x[2m]。
    auto out = resample::resample({1, 2, 3, 4, 5}, 1, 2, {{1, 1}});
    expectEq(out, {1, 3, 5}, "downsample 1/2");
}

static void testRoundingOnlyAtOutput() {
    // h = {1/2}：x=1 -> 0.5 -> 1（半值远离零）；x=-1 -> -1；x=3 -> 1.5 -> 2。
    auto out = resample::resample({1, -1, 3, -3}, 1, 1, {{1, 2}});
    expectEq(out, {1, -1, 2, -2}, "round half away from zero");
    // 中间不舍入：h = {1/3, 1/3, 1/3}，x = {1,1,1}，y[1] = 3*(1/3) = 1 精确。
    // 若中途逐点舍入会得到 0。
    auto out2 = resample::resample({1, 1, 1}, 1, 1, {{1, 3}, {1, 3}, {1, 3}});
    CHECK(out2.size() == 5);
    CHECK(out2[1] == 1); // 0.333+0.333 = 0.667 -> 1（整体舍入）
    CHECK(out2[2] == 1); // 1.0 精确
}

static void testNonCoprimeRatio() {
    std::vector<int64_t> in = {3, -1, 4, 1, 5, -9, 2, 6};
    std::vector<RationalResampler::Coeff> k = {{1, 2}, {3, 4}, {-1, 3}, {2, 5}};
    expectEq(resample::resample(in, 2, 4, k), resample::resample(in, 1, 2, k),
             "2/4 == 1/2");
    expectEq(resample::resample(in, 6, 9, k), resample::resample(in, 2, 3, k),
             "6/9 == 2/3");
    expectEq(resample::resample(in, 5, 5, k), resample::resample(in, 1, 1, k),
             "5/5 == 1/1");
}

static void testEmptyAndSingle() {
    // 空输入 -> 空输出。
    auto e1 = resample::resample({}, 3, 2, {{1, 1}, {1, 2}});
    CHECK(e1.empty());
    // 空输入 + 仅 flush。
    {
        RationalResampler r(2, 3, {{1, 1}});
        std::vector<int64_t> out;
        r.process(nullptr, 0, out);
        r.flush(out);
        CHECK(out.empty());
    }
    // 单样本输入：N=1 -> M = floor((L-1)/Q)+1。
    auto s1 = resample::resample({6}, 1, 1, {{1, 2}, {1, 3}, {1, 6}});
    // y[0]=6*1/2=3, y[1]=6*1/3=2, y[2]=6*1/6=1
    expectEq(s1, {3, 2, 1}, "single sample tail");
    auto s2 = resample::resample({6}, 2, 1, {{1, 2}, {1, 2}});
    // 上采样域：y[0]=6*1/2=3, y[1]=6*1/2=3
    expectEq(s2, {3, 3}, "single sample upsampled");
}

static void testFlushIdempotentAndProcessAfterFlush() {
    RationalResampler r(1, 1, {{1, 1}, {1, 1}});
    std::vector<int64_t> out;
    r.process(std::vector<int64_t>{1, 2}, out);
    r.flush(out);
    size_t n = out.size();
    r.flush(out); // 幂等
    CHECK(out.size() == n);
    bool threw = false;
    try {
        r.process(std::vector<int64_t>{1}, out);
    } catch (const std::logic_error&) {
        threw = true;
    }
    CHECK(threw);
}

static void testInvalidArgs() {
    bool t1 = false, t2 = false, t3 = false;
    try { RationalResampler r(0, 1, {{1, 1}}); } catch (const std::invalid_argument&) { t1 = true; }
    try { RationalResampler r(1, 1, {}); } catch (const std::invalid_argument&) { t2 = true; }
    try { RationalResampler r(1, 1, {{1, 0}}); } catch (const std::invalid_argument&) { t3 = true; }
    CHECK(t1 && t2 && t3);
}

// ---------- 切块一致性 ----------

static void testChunkingEquivalence() {
    std::mt19937_64 rng(20261005);
    for (int iter = 0; iter < 400; ++iter) {
        int64_t p = 1 + rng() % 6, q = 1 + rng() % 6;
        if (rng() % 3 == 0) { // 故意制造非互质倍率
            int64_t k = 2 + rng() % 3;
            p *= k;
            q *= k;
        }
        size_t L = 1 + rng() % 12;
        std::vector<RationalResampler::Coeff> k(L);
        for (auto& c : k) {
            c.num = static_cast<int64_t>(rng() % 17) - 8;
            c.den = 1 + rng() % 6;
            if (rng() % 4 == 0) c.den = -c.den; // 负分母也应规范处理
        }
        size_t N = rng() % 41; // 含 0 与 1
        std::vector<int64_t> in(N);
        for (auto& v : in) v = static_cast<int64_t>(rng() % 2001) - 1000;

        auto oneshot = resample::resample(in, p, q, k);
        expectEq(oneshot, reference(in, p, q, k), "oneshot vs reference");

        // 逐样本块。
        {
            std::vector<size_t> chunks(N, 1);
            expectEq(chunked(in, p, q, k, chunks), oneshot, "sample-by-sample");
        }
        // 随机块（含空块）。
        {
            std::vector<size_t> chunks;
            size_t nchunks = 1 + rng() % (N + 3);
            for (size_t i = 0; i < nchunks; ++i) chunks.push_back(rng() % 6);
            expectEq(chunked(in, p, q, k, chunks), oneshot, "random chunks");
        }
        // 全空块 + 一次性。
        {
            std::vector<size_t> chunks = {0, 0, 0};
            expectEq(chunked(in, p, q, k, chunks), oneshot, "empty chunks");
        }
    }
}

// 大数值：验证精确累加（int64 会溢出的乘积和）。
static void testBigValues() {
    std::vector<int64_t> in = {INT64_MAX / 2, INT64_MAX / 2, -INT64_MAX / 2};
    std::vector<RationalResampler::Coeff> k = {{3, 7}, {5, 11}, {-2, 13}};
    auto out = resample::resample(in, 1, 1, k);
    expectEq(out, reference(in, 1, 1, k), "big values exact");
    // 中间和超过 128 位场景难以构造，但超过 int64 的和必须精确：
    // y[1] = (INT64_MAX/2)*(3/7 + 5/11) ≈ 4.3e18 * 0.88，超出 int64 逐项上界。
    CHECK(out.size() == 5);
}

int main() {
    testIdentity();
    testMovingSumTail();
    testUpsample2();
    testDownsample2();
    testRoundingOnlyAtOutput();
    testNonCoprimeRatio();
    testEmptyAndSingle();
    testFlushIdempotentAndProcessAfterFlush();
    testInvalidArgs();
    testChunkingEquivalence();
    testBigValues();

    if (g_failures == 0) {
        std::cout << "All tests passed." << std::endl;
        return 0;
    }
    std::cerr << g_failures << " failure(s)." << std::endl;
    return 1;
}
