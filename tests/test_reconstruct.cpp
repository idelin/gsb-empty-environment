#include "grayrecon/reconstruct.hpp"

#include <algorithm>
#include <cstdint>
#include <cstdlib>
#include <iostream>
#include <random>
#include <string>

namespace {

using grayrecon::Connectivity;
using grayrecon::Image;

int g_failures = 0;

#define CHECK(cond)                                                            \
    do {                                                                       \
        if (!(cond)) {                                                         \
            ++g_failures;                                                      \
            std::cerr << "FAIL " << __FILE__ << ":" << __LINE__ << ": "        \
                      << #cond << "\n";                                        \
        }                                                                      \
    } while (0)

// Reference: naive repeated full-image dilation sweeps until stable.
Image naiveReconstruct(const Image& marker, const Image& mask, Connectivity conn) {
    Image J = marker;
    const std::size_t w = marker.width, h = marker.height;
    const std::vector<std::pair<int, int>> offsets =
        conn == Connectivity::Eight
            ? std::vector<std::pair<int, int>>{{-1, -1}, {-1, 0}, {-1, 1}, {0, -1},
                                               {0, 1},   {1, -1}, {1, 0},  {1, 1}}
            : std::vector<std::pair<int, int>>{{-1, 0}, {0, -1}, {0, 1}, {1, 0}};
    bool changed = true;
    while (changed) {
        changed = false;
        for (std::size_t r = 0; r < h; ++r) {
            for (std::size_t c = 0; c < w; ++c) {
                std::uint8_t m = J.at(r, c);
                for (const auto& [dr, dc] : offsets) {
                    const int nr = static_cast<int>(r) + dr;
                    const int nc = static_cast<int>(c) + dc;
                    if (nr >= 0 && nr < static_cast<int>(h) && nc >= 0 &&
                        nc < static_cast<int>(w))
                        m = std::max(m, J.at(nr, nc));
                }
                const std::uint8_t v = std::min(m, mask.at(r, c));
                if (v != J.at(r, c)) {
                    J.at(r, c) = v;
                    changed = true;
                }
            }
        }
    }
    return J;
}

bool imagesEqual(const Image& a, const Image& b) {
    return a.width == b.width && a.height == b.height && a.data == b.data;
}

// 1-pixel-wide winding channel of value 200 in a zero mask.
void testNarrowChannel(Connectivity conn) {
    const std::size_t n = 21;
    Image mask(n, n, 0);
    // Serpentine 1-wide path: rows 0,2,4,...,20 connected by vertical segments.
    for (std::size_t r = 0; r < n; r += 2)
        for (std::size_t c = 0; c < n; ++c)
            mask.at(r, c) = 200;
    for (std::size_t r = 0; r + 2 < n; r += 2) {
        const std::size_t c = (r / 2) % 2 == 0 ? n - 1 : 0;
        mask.at(r + 1, c) = 200;
    }
    Image marker(n, n, 0);
    marker.at(0, 0) = 200;

    auto res = grayrecon::reconstructByDilation(marker, mask, conn);
    Image expected = naiveReconstruct(marker, mask, conn);

    CHECK(imagesEqual(res.image, expected));
    CHECK(res.fixedPoint);
    CHECK(grayrecon::isFixedPoint(expected, mask, conn));
    // Channel pixels must be filled, off-channel pixels must stay 0.
    CHECK(res.image.at(0, 0) == 200);
    CHECK(res.image.at(n - 1, (n / 2) % 2 == 0 ? n - 1 : 0) == 200);
    CHECK(res.image.at(1, n / 2) == 0);
    CHECK(res.image.at(3, n / 2) == 0);
    std::cout << "narrow channel (" << (conn == Connectivity::Eight ? "8" : "4")
              << "-conn): updates=" << res.updates << " pops=" << res.queuePops
              << " fixedPoint=" << res.fixedPoint << "\n";
}

// Large plateau of identical gray values: reconstruction must flood it fully.
void testLargePlateau(Connectivity conn) {
    const std::size_t n = 64;
    Image mask(n, n, 150);
    Image marker(n, n, 0);
    marker.at(0, 0) = 150;

    auto res = grayrecon::reconstructByDilation(marker, mask, conn);
    CHECK(res.fixedPoint);
    for (std::uint8_t v : res.image.data)
        CHECK(v == 150);
    // Queue algorithm must not do naive full sweeps: updates well below n*n*2.
    CHECK(res.updates < 2 * n * n);
    std::cout << "plateau (" << (conn == Connectivity::Eight ? "8" : "4")
              << "-conn): updates=" << res.updates << " pops=" << res.queuePops
              << " fixedPoint=" << res.fixedPoint << "\n";
}

// Plateaus separated by a low barrier: reconstruction must not cross.
void testBarrier(Connectivity conn) {
    const std::size_t n = 32;
    Image mask(n, n, 200);
    for (std::size_t r = 0; r < n; ++r)
        mask.at(r, n / 2) = 50; // vertical barrier
    Image marker(n, n, 0);
    marker.at(3, 3) = 200;

    auto res = grayrecon::reconstructByDilation(marker, mask, conn);
    Image expected = naiveReconstruct(marker, mask, conn);
    CHECK(imagesEqual(res.image, expected));
    CHECK(res.fixedPoint);
    CHECK(res.image.at(10, 10) == 200);        // left side flooded at 200
    CHECK(res.image.at(10, n / 2) == 50);      // barrier capped at its own level
    CHECK(res.image.at(10, n - 2) == 50);      // right side only reaches barrier level
}

// Marker above mask must be rejected.
void testRejection() {
    Image mask(4, 4, 100);
    Image marker(4, 4, 0);
    marker.at(2, 3) = 101;
    bool thrown = false;
    try {
        grayrecon::reconstructByDilation(marker, mask);
    } catch (const grayrecon::MarkerExceedsMaskError& e) {
        thrown = true;
        CHECK(e.row == 2 && e.col == 3);
    }
    CHECK(thrown);

    Image wrongSize(5, 4, 0);
    bool badArg = false;
    try {
        grayrecon::reconstructByDilation(wrongSize, mask);
    } catch (const std::invalid_argument&) {
        badArg = true;
    }
    CHECK(badArg);
}

// Randomized cross-validation against the naive reference.
void testRandom(Connectivity conn, std::mt19937& rng) {
    std::uniform_int_distribution<int> sizeDist(1, 30);
    std::uniform_int_distribution<int> valDist(0, 255);
    for (int trial = 0; trial < 200; ++trial) {
        const std::size_t w = sizeDist(rng), h = sizeDist(rng);
        Image mask(w, h), marker(w, h);
        for (std::size_t i = 0; i < w * h; ++i) {
            mask.data[i] = static_cast<std::uint8_t>(valDist(rng));
            marker.data[i] = static_cast<std::uint8_t>(valDist(rng));
            if (marker.data[i] > mask.data[i])
                marker.data[i] = mask.data[i];
        }
        auto res = grayrecon::reconstructByDilation(marker, mask, conn);
        Image expected = naiveReconstruct(marker, mask, conn);
        if (!imagesEqual(res.image, expected)) {
            CHECK(false);
            std::cerr << "mismatch at trial " << trial << " (" << w << "x" << h
                      << ")\n";
            return;
        }
        CHECK(res.fixedPoint);
    }
}

// Random images with many equal-gray regions (few distinct levels).
void testRandomFlatRegions(Connectivity conn, std::mt19937& rng) {
    std::uniform_int_distribution<int> sizeDist(8, 40);
    std::uniform_int_distribution<int> levelDist(0, 3);
    for (int trial = 0; trial < 200; ++trial) {
        const std::size_t w = sizeDist(rng), h = sizeDist(rng);
        Image mask(w, h), marker(w, h);
        for (std::size_t i = 0; i < w * h; ++i) {
            mask.data[i] = static_cast<std::uint8_t>(levelDist(rng) * 60);
            marker.data[i] = static_cast<std::uint8_t>(levelDist(rng) * 60);
            if (marker.data[i] > mask.data[i])
                marker.data[i] = mask.data[i];
        }
        auto res = grayrecon::reconstructByDilation(marker, mask, conn);
        Image expected = naiveReconstruct(marker, mask, conn);
        if (!imagesEqual(res.image, expected)) {
            CHECK(false);
            std::cerr << "flat-region mismatch at trial " << trial << "\n";
            return;
        }
        CHECK(res.fixedPoint);
    }
}

void testDegenerate() {
    Image mask(0, 0), marker(0, 0);
    auto res = grayrecon::reconstructByDilation(marker, mask);
    CHECK(res.fixedPoint);
    CHECK(res.updates == 0);

    Image one(1, 1, 42), oneM(1, 1, 42);
    auto res1 = grayrecon::reconstructByDilation(oneM, one);
    CHECK(res1.fixedPoint);
    CHECK(res1.image.at(0, 0) == 42);
}

} // namespace

int main() {
    std::mt19937 rng(12345);
    for (auto conn : {Connectivity::Four, Connectivity::Eight}) {
        testNarrowChannel(conn);
        testLargePlateau(conn);
        testBarrier(conn);
        testRandom(conn, rng);
        testRandomFlatRegions(conn, rng);
    }
    testRejection();
    testDegenerate();

    if (g_failures == 0) {
        std::cout << "all tests passed\n";
        return 0;
    }
    std::cerr << g_failures << " check(s) failed\n";
    return 1;
}
