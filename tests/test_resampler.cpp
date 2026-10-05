#include "rational_resampler.hpp"

#include <cmath>
#include <cstdio>
#include <random>
#include <stdexcept>
#include <vector>

using rrs::Rational;
using rrs::RationalResampler;
using i64 = std::int64_t;

static int failures = 0;

#define CHECK(cond)                                                       \
    do {                                                                  \
        if (!(cond)) {                                                    \
            std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);   \
            ++failures;                                                   \
        }                                                                 \
    } while (0)

static void check_eq(const std::vector<i64>& a, const std::vector<i64>& b,
                     const char* what, int line) {
    if (a != b) {
        std::printf("FAIL line %d: %s (sizes %zu vs %zu)\n", line, what,
                    a.size(), b.size());
        for (size_t i = 0; i < a.size() && i < b.size(); ++i)
            if (a[i] != b[i]) {
                std::printf("  first diff at %zu: %lld vs %lld\n", i,
                            (long long)a[i], (long long)b[i]);
                break;
            }
        ++failures;
    }
}
#define CHECK_VEC(a, b) check_eq((a), (b), #a " == " #b, __LINE__)

// Feed `input` through `r` in chunks given by `sizes` (cycled if short),
// then flush. Empty chunk sizes are allowed.
static std::vector<i64> run_chunked(RationalResampler& r,
                                    const std::vector<i64>& input,
                                    const std::vector<size_t>& sizes) {
    std::vector<i64> out;
    size_t pos = 0, si = 0;
    while (pos < input.size()) {
        size_t n = sizes[si++ % sizes.size()];
        if (n > input.size() - pos) n = input.size() - pos;
        std::vector<i64> chunk(input.begin() + (ptrdiff_t)pos,
                               input.begin() + (ptrdiff_t)(pos + n));
        std::vector<i64> o = r.process(chunk);
        out.insert(out.end(), o.begin(), o.end());
        pos += n;
    }
    std::vector<i64> tail = r.flush();
    out.insert(out.end(), tail.begin(), tail.end());
    return out;
}

static void test_identity() {
    std::vector<i64> in = {3, -7, 0, 42, -1000000};
    auto out = RationalResampler::resample(in, 1, 1, {{1, 1}});
    CHECK_VEC(out, in);
}

static void test_known_upsample() {
    // P=2, Q=1, h=[1,1]: y = [x0, x0, x1, x1, ...]
    std::vector<i64> in = {5, -2, 9};
    auto out = RationalResampler::resample(in, 2, 1, {{1, 1}, {1, 1}});
    CHECK_VEC(out, (std::vector<i64>{5, 5, -2, -2, 9, 9}));
}

static void test_known_downsample() {
    // P=1, Q=2, h=[1]: y[n] = x[2n]
    std::vector<i64> in = {1, 2, 3, 4, 5};
    auto out = RationalResampler::resample(in, 1, 2, {{1, 1}});
    CHECK_VEC(out, (std::vector<i64>{1, 3, 5}));
}

static void test_deferred_rounding() {
    // h = [1/2, 1/2], ratio 1/1: y[n] = (x[n] + x[n-1]) / 2, rounded once.
    std::vector<i64> in = {1, 2};
    auto out = RationalResampler::resample(in, 1, 1, {{1, 2}, {1, 2}});
    // y0 = 1/2 -> 1 (tie away from zero), y1 = 3/2 -> 2, y2 = 2/2 -> 1
    CHECK_VEC(out, (std::vector<i64>{1, 2, 1}));

    auto neg = RationalResampler::resample({-1}, 1, 1, {{1, 2}});
    CHECK_VEC(neg, (std::vector<i64>{-1}));  // -1/2 -> -1

    // Exact accumulation across many taps: 1/3 + 1/3 + 1/3 == 1 exactly,
    // not 0.999... rounded down.
    auto third = RationalResampler::resample({1, 1, 1}, 1, 1,
                                             {{1, 3}, {1, 3}, {1, 3}});
    CHECK(third.size() == 5);
    CHECK(third[2] == 1);  // (1/3+1/3+1/3) = 1 exactly
    CHECK(third[0] == 0);  // 1/3 rounds to 0
}

static void test_non_coprime() {
    std::vector<i64> in = {4, -3, 7, 0, 11, -6};
    std::vector<Rational> h = {{1, 2}, {3, 4}, {-1, 3}, {2, 5}, {1, 1}, {0, 1}};
    // (4, 6) must equal (2, 3) with every 2nd tap.
    auto a = RationalResampler::resample(in, 4, 6, h);
    auto b = RationalResampler::resample(in, 2, 3, {h[0], h[2], h[4]});
    CHECK_VEC(a, b);
    // (3, 3) is identity ratio: only h[0], h[3] survive decimation by 3.
    auto c = RationalResampler::resample(in, 3, 3, h);
    auto d = RationalResampler::resample(in, 1, 1, {h[0], h[3]});
    CHECK_VEC(c, d);
}

static void test_empty_input() {
    RationalResampler r(3, 2, {{1, 1}, {1, 2}});
    auto o1 = r.process({});
    auto o2 = r.flush();
    CHECK(o1.empty());
    CHECK(o2.empty());
    CHECK(r.output_size_for(0) == 0);
}

static void test_errors() {
    RationalResampler r(1, 1, {{1, 1}});
    r.flush();
    bool threw = false;
    try { r.flush(); } catch (const std::logic_error&) { threw = true; }
    CHECK(threw);
    threw = false;
    try { r.process({1}); } catch (const std::logic_error&) { threw = true; }
    CHECK(threw);
    threw = false;
    try { RationalResampler bad(0, 1, {{1, 1}}); }
    catch (const std::invalid_argument&) { threw = true; }
    CHECK(threw);
    threw = false;
    try { RationalResampler bad(1, 1, {}); }
    catch (const std::invalid_argument&) { threw = true; }
    CHECK(threw);
}

// Independent long-double reference for non-tie cross-checking.
static void cross_check_reference(const std::vector<i64>& in, i64 P, i64 Q,
                                  const std::vector<Rational>& h,
                                  const std::vector<i64>& out) {
    i64 g = std::gcd(P, Q);
    P /= g; Q /= g;
    std::vector<long double> hd;
    for (size_t i = 0; i < h.size(); i += (size_t)g)
        hd.push_back((long double)h[i].num / (long double)h[i].den);
    size_t L = hd.size();
    size_t expected = in.empty()
        ? 0
        : (size_t)(((long long)in.size() - 1) * P + (long long)L - 1) / Q + 1;
    CHECK(out.size() == expected);
    for (size_t n = 0; n < out.size(); ++n) {
        long double acc = 0;
        long long nq = (long long)n * Q;
        for (long long k = nq / P; k >= 0 && nq - k * P < (long long)L; --k) {
            if (k < (long long)in.size())
                acc += (long double)in[(size_t)k] * hd[(size_t)(nq - k * P)];
        }
        long double diff = std::fabs(acc - (long double)out[(size_t)n]);
        // Exact rational arithmetic rounds only true ties away from zero;
        // a long-double reference may be off by one near ties, so only
        // enforce the bound strictly when clearly away from a tie.
        long double frac = std::fabs(acc - std::floor(acc));
        if (std::fabs(frac - 0.5L) > 1e-9L) CHECK(diff <= 0.5L + 1e-6L);
        else CHECK(diff <= 1.5L + 1e-6L);
    }
}

static void test_randomized() {
    std::mt19937_64 rng(20261005);
    for (int iter = 0; iter < 400; ++iter) {
        i64 P = 1 + (i64)(rng() % 8);
        i64 Q = 1 + (i64)(rng() % 8);
        size_t L = 1 + rng() % 12;
        std::vector<Rational> h(L);
        for (auto& c : h) {
            c.num = (i64)(rng() % 41) - 20;
            c.den = 1 + (i64)(rng() % 7);
        }
        size_t N = rng() % 200;
        std::vector<i64> in(N);
        for (auto& s : in) s = (i64)(rng() % 2001) - 1000;

        auto oneshot = RationalResampler::resample(in, P, Q, h);

        // Random chunking, including empty chunks.
        std::vector<size_t> sizes;
        for (int i = 0; i < 8; ++i) sizes.push_back(rng() % 37);
        {
            RationalResampler r(P, Q, h);
            auto chunked = run_chunked(r, in, sizes);
            CHECK_VEC(chunked, oneshot);
        }
        // Single-sample chunks.
        {
            RationalResampler r(P, Q, h);
            auto chunked = run_chunked(r, in, {1});
            CHECK_VEC(chunked, oneshot);
        }
        // Alternating empty and 2-sample chunks.
        {
            RationalResampler r(P, Q, h);
            auto chunked = run_chunked(r, in, {0, 2, 0, 0, 3});
            CHECK_VEC(chunked, oneshot);
        }
        // Length contract.
        RationalResampler probe(P, Q, h);
        CHECK((size_t)probe.output_size_for((i64)N) == oneshot.size());

        cross_check_reference(in, P, Q, h, oneshot);
    }
}

static void test_flush_tail_contents() {
    // Ratio 1/1, h = [1, 2, 3] (den 1): tail must carry the kernel ring-down.
    std::vector<i64> in = {10};
    auto out = RationalResampler::resample(in, 1, 1,
                                           {{1, 1}, {2, 1}, {3, 1}});
    CHECK_VEC(out, (std::vector<i64>{10, 20, 30}));

    // Streaming: nothing beyond determined outputs before flush.
    RationalResampler r(1, 1, {{1, 1}, {2, 1}, {3, 1}});
    auto o1 = r.process({10});
    CHECK_VEC(o1, (std::vector<i64>{10}));
    auto o2 = r.flush();
    CHECK_VEC(o2, (std::vector<i64>{20, 30}));
}

int main() {
    test_identity();
    test_known_upsample();
    test_known_downsample();
    test_deferred_rounding();
    test_non_coprime();
    test_empty_input();
    test_errors();
    test_flush_tail_contents();
    test_randomized();
    if (failures == 0) {
        std::printf("all tests passed\n");
        return 0;
    }
    std::printf("%d check(s) failed\n", failures);
    return 1;
}
