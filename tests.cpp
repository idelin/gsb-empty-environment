// tests.cpp -- correctness checks for scaler.hpp.
//
// The reference below computes every output pixel independently, directly
// from the 2D coverage definition, with exact integer arithmetic. It shares
// no code with the streaming implementation, so matching it validates the
// separable filter, the row cache, and the single-final-rounding rule.

#include "scaler.hpp"

#include <cinttypes>
#include <cstdio>
#include <cstdlib>
#include <random>
#include <vector>

namespace {

int gFailures = 0;

#define CHECK(cond)                                                        \
    do {                                                                   \
        if (!(cond)) {                                                     \
            std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);    \
            ++gFailures;                                                   \
        }                                                                  \
    } while (0)

// Exact reference for one output pixel: value = round(num / (srcW*srcH)),
// where num sums overlapX * overlapY * pixel over all covered source pixels.
// Overlaps are measured in units of 1/dstW and 1/dstH respectively.
int64_t refPixelNum(const uint8_t* img, int32_t srcW, int32_t srcH,
                    int32_t dstW, int32_t dstH, int32_t x, int32_t y,
                    int32_t c, int32_t ch) {
    const int64_t xlo = static_cast<int64_t>(x) * srcW;
    const int64_t xhi = static_cast<int64_t>(x + 1) * srcW;
    const int64_t ylo = static_cast<int64_t>(y) * srcH;
    const int64_t yhi = static_cast<int64_t>(y + 1) * srcH;
    int64_t num = 0;
    for (int32_t j = static_cast<int32_t>(ylo / dstH);
         j <= static_cast<int32_t>((yhi - 1) / dstH); ++j) {
        const int64_t oy = std::min(yhi, static_cast<int64_t>(j + 1) * dstH) -
                           std::max(ylo, static_cast<int64_t>(j) * dstH);
        for (int32_t i = static_cast<int32_t>(xlo / dstW);
             i <= static_cast<int32_t>((xhi - 1) / dstW); ++i) {
            const int64_t ox = std::min(xhi, static_cast<int64_t>(i + 1) * dstW) -
                               std::max(xlo, static_cast<int64_t>(i) * dstW);
            num += ox * oy * img[(static_cast<size_t>(j) * srcW + i) * ch + c];
        }
    }
    return num;
}

uint8_t refPixel(const uint8_t* img, int32_t srcW, int32_t srcH, int32_t dstW,
                 int32_t dstH, int32_t x, int32_t y, int32_t c, int32_t ch) {
    const int64_t num = refPixelNum(img, srcW, srcH, dstW, dstH, x, y, c, ch);
    const int64_t den = static_cast<int64_t>(srcW) * srcH;
    return static_cast<uint8_t>((num + den / 2) / den);
}

std::vector<uint8_t> randomImage(int32_t w, int32_t h, int32_t ch,
                                 uint32_t seed) {
    std::mt19937 rng(seed);
    std::uniform_int_distribution<int> dist(0, 255);
    std::vector<uint8_t> img(static_cast<size_t>(w) * h * ch);
    for (auto& v : img) v = static_cast<uint8_t>(dist(rng));
    return img;
}

// --- 1. Constant images must come through bit-exact, any size. -----------
void testConstant() {
    const int32_t sizes[][4] = {
        {1, 1, 1, 1},   {1, 1, 7, 5},   {5, 3, 1, 1},   {16, 16, 7, 9},
        {7, 9, 16, 16}, {251, 241, 173, 167}, {97, 89, 256, 300},
        {2, 2, 3, 3},   {300, 1, 13, 1},    {1, 300, 1, 13},
    };
    for (const auto& s : sizes) {
        for (int value : {0, 1, 128, 254, 255}) {
            std::vector<uint8_t> img(static_cast<size_t>(s[0]) * s[1],
                                     static_cast<uint8_t>(value));
            auto out = imgs::scale(img.data(), s[0], s[1], s[2], s[3]);
            bool ok = true;
            for (uint8_t v : out) ok &= (v == value);
            CHECK(ok);
            if (!ok)
                std::printf("  constant %d -> %dx%d -> %dx%d broke\n", value,
                            s[0], s[1], s[2], s[3]);
        }
    }
}

// --- 2. Impulse response: exact per-pixel match + exact energy. ----------
void testImpulse() {
    const int32_t sizes[][4] = {
        {251, 241, 173, 167},  // prime downscale
        {97, 89, 256, 300},    // upscale
        {173, 167, 251, 241},  // prime upscale
        {16, 16, 7, 9},
    };
    for (const auto& s : sizes) {
        const int32_t sw = s[0], sh = s[1], dw = s[2], dh = s[3];
        const int32_t spots[][2] = {
            {0, 0}, {sw - 1, sh - 1}, {sw / 2, sh / 2}, {0, sh - 1}, {sw - 1, 0}};
        for (const auto& p : spots) {
            std::vector<uint8_t> img(static_cast<size_t>(sw) * sh, 0);
            img[static_cast<size_t>(p[1]) * sw + p[0]] = 255;
            auto out = imgs::scale(img.data(), sw, sh, dw, dh);

            int64_t roundedSum = 0;
            int nonzero = 0;
            for (int32_t y = 0; y < dh; ++y) {
                for (int32_t x = 0; x < dw; ++x) {
                    const uint8_t got = out[static_cast<size_t>(y) * dw + x];
                    const uint8_t want =
                        refPixel(img.data(), sw, sh, dw, dh, x, y, 0, 1);
                    CHECK(got == want);
                    roundedSum += got;
                    nonzero += got != 0;
                }
            }
            CHECK(nonzero > 0);
            // Exact rational energy: sum of unrounded outputs equals
            // 255 * dstW*dstH / (srcW*srcH). Verify in integers.
            int64_t totalNum = 0;
            for (int32_t y = 0; y < dh; ++y)
                for (int32_t x = 0; x < dw; ++x)
                    totalNum +=
                        refPixelNum(img.data(), sw, sh, dw, dh, x, y, 0, 1);
            CHECK(totalNum ==
                  255LL * dw * dh);  // denominator srcW*srcH cancels out
            // Rounded sum stays within half a unit per lit pixel.
            const int64_t exactTimesDen =
                255LL * dw * dh;  // in units of 1/(srcW*srcH)
            const int64_t den = static_cast<int64_t>(sw) * sh;
            const double exactSum =
                static_cast<double>(exactTimesDen) / den;
            CHECK(std::abs(roundedSum - exactSum) <= nonzero / 2.0 + 1e-9);
        }
    }
}

// --- 3. Random images at prime sizes: full per-pixel reference match. ----
void testRandomPrimeSizes() {
    const int32_t sizes[][4] = {
        {251, 241, 173, 167}, {173, 167, 251, 241}, {2, 2, 251, 241},
        {251, 241, 2, 2},     {53, 59, 61, 47},     {1, 1, 2, 3},
        {3, 2, 1, 1},         {256, 256, 255, 255},
    };
    for (const auto& s : sizes) {
        const int32_t sw = s[0], sh = s[1], dw = s[2], dh = s[3];
        auto img = randomImage(sw, sh, 1, static_cast<uint32_t>(sw * 131 + sh));
        auto out = imgs::scale(img.data(), sw, sh, dw, dh);
        CHECK(out.size() == static_cast<size_t>(dw) * dh);

        int64_t totalNum = 0, srcSum = 0;
        for (uint8_t v : img) srcSum += v;
        for (int32_t y = 0; y < dh; ++y) {
            for (int32_t x = 0; x < dw; ++x) {
                const uint8_t got = out[static_cast<size_t>(y) * dw + x];
                const uint8_t want =
                    refPixel(img.data(), sw, sh, dw, dh, x, y, 0, 1);
                CHECK(got == want);
                totalNum += refPixelNum(img.data(), sw, sh, dw, dh, x, y, 0, 1);
            }
        }
        // Global energy: sum of exact outputs == sum of inputs, rescaled.
        CHECK(totalNum == srcSum * static_cast<int64_t>(dw) * dh);
    }
}

// --- 4. Streaming with tiny output buffers equals one-shot scale. --------
void testStreamingEquivalence() {
    const int32_t sw = 251, sh = 241, dw = 173, dh = 167;
    auto img = randomImage(sw, sh, 1, 42);
    auto want = imgs::scale(img.data(), sw, sh, dw, dh);

    imgs::StreamScaler<uint8_t> sc(sw, sh, dw, dh);
    std::vector<uint8_t> got(static_cast<size_t>(dw) * dh, 0);
    uint8_t small[3 * 173];  // deliberately tiny: at most 3 rows per drain
    size_t row = 0;
    for (int32_t y = 0; y < sh; ++y) {
        size_t n = sc.pushRow(img.data() + static_cast<size_t>(y) * sw, small, 3);
        for (size_t k = 0; k < n; ++k)
            std::copy(small + k * dw, small + (k + 1) * dw,
                      got.begin() + static_cast<ptrdiff_t>((row + k) * dw));
        row += n;
    }
    size_t n;
    while ((n = sc.finish(small, 3)) > 0) {
        for (size_t k = 0; k < n; ++k)
            std::copy(small + k * dw, small + (k + 1) * dw,
                      got.begin() + static_cast<ptrdiff_t>((row + k) * dw));
        row += n;
    }
    CHECK(row == static_cast<size_t>(dh));
    CHECK(sc.done());
    CHECK(got == want);
}

// --- 5. Multi-channel (interleaved RGB) matches per-channel reference. ---
void testMultiChannel() {
    const int32_t sw = 97, sh = 89, dw = 64, dh = 48, ch = 3;
    auto img = randomImage(sw, sh, ch, 7);
    auto out = imgs::scale(img.data(), sw, sh, dw, dh, ch);
    for (int32_t y = 0; y < dh; ++y)
        for (int32_t x = 0; x < dw; ++x)
            for (int32_t c = 0; c < ch; ++c) {
                const uint8_t got =
                    out[(static_cast<size_t>(y) * dw + x) * ch + c];
                const uint8_t want =
                    refPixel(img.data(), sw, sh, dw, dh, x, y, c, ch);
                CHECK(got == want);
            }
}

// --- 6. No intermediate rounding: construct a case where rounding the ---
// --- horizontal pass first would change the result. ----------------------
void testNoIntermediateRounding() {
    // srcW=2 -> dstW=1 gives horizontal weights (1,1)/2, so a row pair
    // (a,b) averages to (a+b)/2. With srcH=2 -> dstH=1 the final value is
    // the average of the two row averages. Pick values where the exact
    // 4-pixel average is an exact quarter that two-stage rounding gets
    // wrong: pixels 0,0,0,1 -> exact 1/4 -> rounds to 0; rounding each row
    // first gives rows 0 and 1 (1/2 rounds up), then (0+1)/2 rounds to 1.
    const uint8_t img[4] = {0, 0, 0, 1};
    auto out = imgs::scale(img, 2, 2, 1, 1);
    CHECK(out.size() == 1);
    CHECK(out[0] == 0);  // exact 0.25 rounds down; two-stage would give 1
}

// --- 7. uint16_t pixels and degenerate 1-pixel dimensions. ---------------
void testUint16AndDegenerate() {
    const uint16_t img[1] = {65535};
    auto big = imgs::scale(img, 1, 1, 5, 7);
    CHECK(big.size() == 35);
    for (uint16_t v : big) CHECK(v == 65535);

    auto img2 = randomImage(31, 1, 1, 5);  // single row
    auto out2 = imgs::scale(img2.data(), 31, 1, 17, 1);
    CHECK(out2.size() == 17);
    for (int32_t x = 0; x < 17; ++x)
        CHECK(out2[static_cast<size_t>(x)] ==
              refPixel(img2.data(), 31, 1, 17, 1, x, 0, 0, 1));
}

}  // namespace

int main() {
    testConstant();
    testImpulse();
    testRandomPrimeSizes();
    testStreamingEquivalence();
    testMultiChannel();
    testNoIntermediateRounding();
    testUint16AndDegenerate();
    if (gFailures == 0) {
        std::printf("all tests passed\n");
        return 0;
    }
    std::printf("%d check(s) failed\n", gFailures);
    return 1;
}
