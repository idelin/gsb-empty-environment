// Tests for area_scale::Scaler.
//
//  * constant images must come out bit-exact for every size combination
//    (catches boundary/coverage bugs and any premature rounding);
//  * single-pixel impulses: total energy preserved, outputs exactly zero
//    outside the predicted coverage range, edge pixels covered exactly;
//  * prime dimensions in both up- and down-scale directions;
//  * sharp cases that fail if horizontal results are rounded before the
//    vertical pass;
//  * streaming output compared pixel-by-pixel against an independent
//    direct reference implementation on random images.

#include "../include/area_scaler.h"

#include <algorithm>
#include <cmath>
#include <cstdio>
#include <random>
#include <vector>

using area_scale::Scaler;

static int g_failures = 0;
static int g_checks = 0;

#define CHECK(cond)                                                        \
    do {                                                                   \
        ++g_checks;                                                        \
        if (!(cond)) {                                                     \
            ++g_failures;                                                  \
            std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);    \
        }                                                                  \
    } while (0)

// Independent reference: per output pixel, recompute coverage directly with
// the min/max overlap formula (no shared weight tables), accumulate in
// __int128, round once at the end.
template <typename Pixel>
static std::vector<Pixel> referenceScale(const std::vector<Pixel>& src,
                                         int srcW, int srcH,
                                         int dstW, int dstH) {
    std::vector<Pixel> dst((size_t)dstW * dstH);
    const long long denom = (long long)srcW * srcH;
    for (int oy = 0; oy < dstH; ++oy) {
        const long long y0 = (long long)oy * srcH, y1 = (long long)(oy + 1) * srcH;
        for (int ox = 0; ox < dstW; ++ox) {
            const long long x0 = (long long)ox * srcW, x1 = (long long)(ox + 1) * srcW;
            __int128 acc = 0;
            const int sy0 = (int)(y0 / dstH), sy1 = (int)((y1 - 1) / dstH);
            const int sx0 = (int)(x0 / dstW), sx1 = (int)((x1 - 1) / dstW);
            for (int sy = sy0; sy <= sy1; ++sy) {
                const long long vy = std::min(y1, (long long)(sy + 1) * dstH) -
                                     std::max(y0, (long long)sy * dstH);
                for (int sx = sx0; sx <= sx1; ++sx) {
                    const long long vx = std::min(x1, (long long)(sx + 1) * dstW) -
                                         std::max(x0, (long long)sx * dstW);
                    acc += (__int128)vy * vx * src[(size_t)sy * srcW + sx];
                }
            }
            dst[(size_t)oy * dstW + ox] = (Pixel)((acc + denom / 2) / denom);
        }
    }
    return dst;
}

static void testConstant() {
    struct Sz { int sw, sh, dw, dh; };
    const Sz sizes[] = {
        {1, 1, 1, 1},   {1, 1, 7, 9},   {9, 7, 1, 1},   {16, 16, 16, 16},
        {7, 5, 13, 11}, {13, 11, 7, 5}, {541, 547, 523, 521},
        {523, 521, 541, 547}, {2, 3, 1024, 769}, {1024, 769, 2, 3},
    };
    const int values[] = {0, 1, 127, 200, 255};
    for (const auto& s : sizes) {
        for (int v : values) {
            std::vector<uint8_t> src((size_t)s.sw * s.sh, (uint8_t)v);
            auto dst = Scaler<uint8_t>::scale(src, s.sw, s.sh, s.dw, s.dh);
            CHECK((int)dst.size() == s.dw * s.dh);
            bool ok = true;
            for (uint8_t p : dst) ok &= (p == v);
            if (!ok) {
                ++g_checks; ++g_failures;
                std::printf("FAIL constant %dx%d -> %dx%d value %d\n",
                            s.sw, s.sh, s.dw, s.dh, v);
            }
        }
    }
}

// Output indices whose coverage interval contains source pixel i.
static std::pair<int, int> coveringOutputs(int srcN, int dstN, int i) {
    int first = -1, last = -1;
    for (int j = 0; j < dstN; ++j) {
        const int lo = (int)((long long)j * srcN / dstN);
        const int hi = (int)(((long long)(j + 1) * srcN - 1) / dstN);
        if (lo <= i && i <= hi) {
            if (first < 0) first = j;
            last = j;
        }
    }
    return {first, last};
}

static void testImpulse(int sw, int sh, int dw, int dh) {
    const int spots[][2] = {
        {0, 0}, {sw - 1, sh - 1}, {0, sh - 1}, {sw - 1, 0},
        {sw / 2, sh / 2}, {sw / 3, 2 * sh / 3},
    };
    const int V = 200;
    for (const auto& sp : spots) {
        const int ix = sp[0], iy = sp[1];
        std::vector<uint8_t> src((size_t)sw * sh, 0);
        src[(size_t)iy * sw + ix] = (uint8_t)V;
        auto dst = Scaler<uint8_t>::scale(src, sw, sh, dw, dh);

        auto xr = coveringOutputs(sw, dw, ix);
        auto yr = coveringOutputs(sh, dh, iy);
        const int coverArea = (xr.second - xr.first + 1) *
                              (yr.second - yr.first + 1);

        // Boundary: edge source pixels are covered by exactly the edge
        // output pixels -- nothing falls off the border, nothing clamps.
        if (ix == 0)      CHECK(xr.first == 0);
        if (ix == sw - 1) CHECK(xr.second == dw - 1);
        if (iy == 0)      CHECK(yr.first == 0);
        if (iy == sh - 1) CHECK(yr.second == dh - 1);

        // Locality: exactly zero outside the predicted coverage range.
        long long sum = 0;
        bool outsideOk = true;
        for (int y = 0; y < dh; ++y)
            for (int x = 0; x < dw; ++x) {
                const int p = dst[(size_t)y * dw + x];
                const bool inside = xr.first <= x && x <= xr.second &&
                                    yr.first <= y && y <= yr.second;
                if (!inside && p != 0) outsideOk = false;
                sum += p;
            }
        if (!outsideOk) {
            ++g_checks; ++g_failures;
            std::printf("FAIL impulse %dx%d->%dx%d @(%d,%d): leak outside coverage\n",
                        sw, sh, dw, dh, ix, iy);
        }

        // Energy: exact pre-rounding sum is V*dw*dh/(sw*sh); each covered
        // output rounds with error < 1/2, so the total error is bounded.
        const double expect = (double)V * dw * dh / ((double)sw * sh);
        CHECK(std::fabs(sum - expect) <= coverArea / 2.0 + 1e-9);

        // Exact match with the independent reference (same single rounding).
        auto want = referenceScale(src, sw, sh, dw, dh);
        if (dst != want) {
            ++g_checks; ++g_failures;
            std::printf("FAIL impulse %dx%d->%dx%d @(%d,%d): mismatch vs reference\n",
                        sw, sh, dw, dh, ix, iy);
        } else {
            ++g_checks;
        }
    }
}

static void testNoDoubleRounding() {
    // 2x2 -> 1x1, impulse of 1: exact value 1/4 rounds to 0.
    // Rounding the horizontal pass first would give round((1+0)/2) = 1.
    {
        std::vector<uint8_t> src = {1, 0, 0, 0};
        auto dst = Scaler<uint8_t>::scale(src, 2, 2, 1, 1);
        CHECK(dst.size() == 1 && dst[0] == 0);
    }
    // Impulse of 3: exact 3/4 rounds to 1.
    {
        std::vector<uint8_t> src = {3, 0, 0, 0};
        auto dst = Scaler<uint8_t>::scale(src, 2, 2, 1, 1);
        CHECK(dst.size() == 1 && dst[0] == 1);
    }
    // Diagonal 1s: exact 2/4 rounds to 1.
    {
        std::vector<uint8_t> src = {1, 0, 0, 1};
        auto dst = Scaler<uint8_t>::scale(src, 2, 2, 1, 1);
        CHECK(dst.size() == 1 && dst[0] == 1);
    }
    // Exhaustive binary 3x3 -> 2x2 vs reference: any double-rounding
    // discrepancy would show up as a mismatch somewhere in the 512 cases.
    for (int bits = 0; bits < 512; ++bits) {
        std::vector<uint8_t> src(9);
        for (int i = 0; i < 9; ++i) src[i] = (uint8_t)((bits >> i) & 1);
        auto got = Scaler<uint8_t>::scale(src, 3, 3, 2, 2);
        auto want = referenceScale(src, 3, 3, 2, 2);
        CHECK(got == want);
    }
}

static void testStreamingOrderAndFlush() {
    // Push rows one at a time; rows must come back in order, all dstH of
    // them, and the last push must flush everything.
    const int sw = 17, sh = 13, dw = 29, dh = 23;
    std::vector<uint8_t> src((size_t)sw * sh);
    for (int i = 0; i < sw * sh; ++i) src[i] = (uint8_t)(i % 251);
    Scaler<uint8_t> s(sw, sh, dw, dh);
    std::vector<uint8_t> dst((size_t)dw * dh, 0xFF);
    int produced = 0;
    for (int y = 0; y < sh; ++y) {
        auto rows = s.pushRow(&src[(size_t)y * sw]);
        for (auto& p : rows) {
            CHECK(p.first == produced);
            std::copy(p.second.begin(), p.second.end(),
                      dst.begin() + (size_t)p.first * dw);
            ++produced;
        }
    }
    CHECK(produced == dh);
    auto whole = Scaler<uint8_t>::scale(src, sw, sh, dw, dh);
    CHECK(dst == whole);
}

static void testRandomVsReference() {
    std::mt19937 rng(12345);
    struct Sz { int sw, sh, dw, dh; };
    const Sz sizes[] = {
        {1, 1, 1, 1},     {1, 1, 5, 3},   {5, 3, 1, 1},
        {2, 2, 3, 3},     {3, 3, 2, 2},   {16, 16, 16, 16},
        {17, 13, 29, 23}, {29, 23, 17, 13},
        {97, 89, 101, 103},   // primes, upscale
        {101, 103, 97, 89},   // primes, downscale
        {541, 547, 523, 521}, // larger primes, downscale
        {64, 64, 200, 150}, {200, 150, 64, 64},
    };
    for (const auto& s : sizes) {
        std::vector<uint8_t> src((size_t)s.sw * s.sh);
        for (auto& p : src) p = (uint8_t)(rng() & 0xFF);
        auto got = Scaler<uint8_t>::scale(src, s.sw, s.sh, s.dw, s.dh);
        auto want = referenceScale(src, s.sw, s.sh, s.dw, s.dh);
        if (got != want) {
            ++g_checks; ++g_failures;
            std::printf("FAIL random %dx%d -> %dx%d\n", s.sw, s.sh, s.dw, s.dh);
            for (int i = 0; i < s.dw * s.dh; ++i)
                if (got[i] != want[i]) {
                    std::printf("  first diff at %d: got %d want %d\n",
                                i, got[i], want[i]);
                    break;
                }
        } else {
            ++g_checks;
        }
    }
}

static void testIdentityAndAverage() {
    // Identity: same size in/out reproduces the input exactly.
    std::mt19937 rng(999);
    std::vector<uint8_t> src(31 * 37);
    for (auto& p : src) p = (uint8_t)(rng() & 0xFF);
    auto id = Scaler<uint8_t>::scale(src, 31, 37, 31, 37);
    CHECK(id == src);

    // NxM -> 1x1 equals the rounded exact average.
    std::vector<uint8_t> img(7 * 11);
    long long total = 0;
    for (auto& p : img) { p = (uint8_t)(rng() & 0xFF); total += p; }
    auto avg = Scaler<uint8_t>::scale(img, 7, 11, 1, 1);
    const long long expect = (total + 77 / 2) / 77;
    CHECK(avg.size() == 1 && avg[0] == (uint8_t)expect);
}

int main() {
    testConstant();
    testImpulse(541, 547, 523, 521);   // primes, slight downscale
    testImpulse(523, 521, 541, 547);   // primes, slight upscale
    testImpulse(97, 89, 101, 103);     // primes, upscale
    testImpulse(101, 103, 97, 89);     // primes, downscale
    testImpulse(2, 2, 512, 512);       // extreme upscale, corners
    testImpulse(512, 512, 2, 2);       // extreme downscale, corners
    testNoDoubleRounding();
    testStreamingOrderAndFlush();
    testRandomVsReference();
    testIdentityAndAverage();

    std::printf("%d checks, %d failures\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}
