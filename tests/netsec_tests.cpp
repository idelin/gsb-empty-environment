#include "netsec.hpp"

#include <array>
#include <cinttypes>
#include <cstdio>
#include <cstdint>
#include <random>

using netsec::JumpOp;
using netsec::ScalarRange;

static int g_failures = 0;

#define CHECK(cond)                                                          \
    do {                                                                     \
        if (!(cond)) {                                                       \
            std::fprintf(stderr, "CHECK failed %s:%d: %s\n", __FILE__,       \
                         __LINE__, #cond);                                   \
            ++g_failures;                                                    \
        }                                                                    \
    } while (0)

// A negative value masked with a small constant must narrow to a
// non-negative range in both signed and unsigned domains.
static void testAndNarrowsNegative() {
    ScalarRange v = ScalarRange::constant(~0ULL);  // -1
    v.bitwiseAnd(ScalarRange::constant(0xff));
    CHECK(v.isConstant() && v.constValue() == 0xff);
    CHECK(v.smin() == 0xff && v.smax() == 0xff);

    ScalarRange w = ScalarRange::constant(~0ULL);
    w.bitwiseAnd(ScalarRange::raw(0, 0xff, 0, 0xff));
    CHECK(w.umin() == 0 && w.umax() == 0xff);
    CHECK(w.smin() == 0 && w.smax() == 0xff);
}

// Unsigned wrap on addition must degrade the unsigned interval to the
// full domain while the signed interval stays precise.
static void testAddWrapDegrades() {
    ScalarRange v = ScalarRange::raw(-11, -2, ~0ULL - 10, ~0ULL - 1);
    v.add(ScalarRange::constant(10));
    CHECK(v.umin() == 0 && v.umax() == UINT64_MAX);
    CHECK(v.smin() == -1 && v.smax() == 8);
}

// Signed less-than against zero must narrow the taken branch to
// negative values, which cross-deduces to huge unsigned bounds.
static void testSignedBranchNarrow() {
    ScalarRange a = ScalarRange::raw(-10, 10, 0, UINT64_MAX);
    ScalarRange b = ScalarRange::constant(0);
    auto br = ScalarRange::narrowBranch(JumpOp::SLt, a, b);
    CHECK(!br.takenLeft.isEmpty());
    CHECK(br.takenLeft.smin() == -10 && br.takenLeft.smax() == -1);
    CHECK(br.takenLeft.umin() == static_cast<uint64_t>(-10));
    CHECK(br.takenLeft.umax() == static_cast<uint64_t>(-1));
    CHECK(br.fallLeft.smin() == 0 && br.fallLeft.smax() == 10);
    CHECK(br.fallLeft.umin() == 0 && br.fallLeft.umax() == 10);
}

// Branches whose signed and unsigned intervals contradict each other
// are unreachable.
static void testContradictionUnreachable() {
    // All-negative signed range can never be <u 3.
    ScalarRange neg = ScalarRange::raw(-5, -1, 0, UINT64_MAX);
    auto br = ScalarRange::narrowBranch(JumpOp::ULt, neg, ScalarRange::constant(3));
    CHECK(br.takenLeft.isEmpty());
    CHECK(!br.fallLeft.isEmpty());

    // x != 5 is impossible when x is known to be 5.
    auto br2 = ScalarRange::narrowBranch(JumpOp::Ne, ScalarRange::constant(5),
                                         ScalarRange::constant(5));
    CHECK(br2.takenLeft.isEmpty());
    CHECK(!br2.fallLeft.isEmpty());

    // x == 5 is impossible when x is known to be 7.
    auto br3 = ScalarRange::narrowBranch(JumpOp::Eq, ScalarRange::constant(7),
                                         ScalarRange::constant(5));
    CHECK(br3.takenLeft.isEmpty());
    CHECK(!br3.fallLeft.isEmpty());
}

// [offset, offset + width) must stay inside the map value and the
// offset interval must not contain negatives.
static void testBoundaryAccess() {
    const uint64_t kSize = 64;
    CHECK(netsec::checkMapValueAccess(ScalarRange::raw(0, 56, 0, 56), 8, kSize));
    CHECK(netsec::checkMapValueAccess(ScalarRange::constant(56), 8, kSize));
    CHECK(netsec::checkMapValueAccess(ScalarRange::constant(0), 1, kSize));
    // offset.umax() + width overflows past the value end.
    CHECK(!netsec::checkMapValueAccess(ScalarRange::raw(0, 57, 0, 57), 8, kSize));
    CHECK(!netsec::checkMapValueAccess(ScalarRange::constant(60), 8, kSize));
    // Interval contains a negative offset.
    CHECK(!netsec::checkMapValueAccess(ScalarRange::raw(-1, 8, 0, UINT64_MAX), 8, kSize));
    // Width larger than the value.
    CHECK(!netsec::checkMapValueAccess(ScalarRange::constant(0), 8, 4));
    // Unreachable (empty) offset range.
    CHECK(!netsec::checkMapValueAccess(ScalarRange::raw(1, 0, 1, 0), 1, kSize));
}

static bool evalCond(JumpOp op, uint64_t a, uint64_t b) {
    switch (op) {
        case JumpOp::Eq: return a == b;
        case JumpOp::Ne: return a != b;
        case JumpOp::UGt: return a > b;
        case JumpOp::UGe: return a >= b;
        case JumpOp::ULt: return a < b;
        case JumpOp::ULe: return a <= b;
        case JumpOp::SGt: return static_cast<int64_t>(a) > static_cast<int64_t>(b);
        case JumpOp::SGe: return static_cast<int64_t>(a) >= static_cast<int64_t>(b);
        case JumpOp::SLt: return static_cast<int64_t>(a) < static_cast<int64_t>(b);
        case JumpOp::SLe: return static_cast<int64_t>(a) <= static_cast<int64_t>(b);
    }
    return false;
}

static uint64_t randLe(std::mt19937_64& rng, uint64_t v) {
    return v == UINT64_MAX ? rng() : rng() % (v + 1);
}

static uint64_t randGe(std::mt19937_64& rng, uint64_t v) {
    return v == 0 ? rng() : v + rng() % (UINT64_MAX - v + 1);
}

// Differential test: random straight-line programs (alu ops, shifts,
// constants, conditional branches) are executed concretely and every
// concrete value must stay inside the verifier's inferred range; a
// branch the verifier marks unreachable must never be taken
// concretely, and an access the verifier accepts must be in bounds.
static void testRandomPrograms() {
    std::mt19937_64 rng(20261113ULL);
    const int kPrograms = 20000;
    const int kSteps = 24;
    for (int prog = 0; prog < kPrograms; ++prog) {
        std::array<ScalarRange, 3> ranges;
        std::array<uint64_t, 3> concrete;
        for (int i = 0; i < 3; ++i) {
            uint64_t v = rng();
            switch (rng() % 3) {
                case 0:
                    ranges[i] = ScalarRange::constant(v);
                    break;
                case 1:
                    ranges[i] = ScalarRange::full();
                    break;
                default: {
                    uint64_t umin = randLe(rng, v);
                    uint64_t umax = randGe(rng, v);
                    int64_t sv = static_cast<int64_t>(v);
                    int64_t smin, smax;
                    do { smin = static_cast<int64_t>(rng()); } while (smin > sv);
                    do { smax = static_cast<int64_t>(rng()); } while (smax < sv);
                    ranges[i] = ScalarRange::raw(smin, smax, umin, umax);
                    break;
                }
            }
            concrete[i] = v;
            CHECK(ranges[i].contains(concrete[i]));
        }
        for (int step = 0; step < kSteps; ++step) {
            int op = static_cast<int>(rng() % 8);
            int i = static_cast<int>(rng() % 3);
            int j = static_cast<int>(rng() % 3);
            switch (op) {
                case 0:
                    ranges[i].add(ranges[j]);
                    concrete[i] += concrete[j];
                    break;
                case 1:
                    ranges[i].sub(ranges[j]);
                    concrete[i] -= concrete[j];
                    break;
                case 2:
                    ranges[i].bitwiseAnd(ranges[j]);
                    concrete[i] &= concrete[j];
                    break;
                case 3:
                case 4:
                case 5: {
                    uint64_t sh = rng() % 64;
                    ranges[j] = ScalarRange::constant(sh);
                    concrete[j] = sh;
                    if (op == 3) {
                        ranges[i].shl(ranges[j]);
                        concrete[i] <<= sh;
                    } else if (op == 4) {
                        ranges[i].lshr(ranges[j]);
                        concrete[i] >>= sh;
                    } else {
                        ranges[i].ashr(ranges[j]);
                        concrete[i] = static_cast<uint64_t>(
                            static_cast<int64_t>(concrete[i]) >> sh);
                    }
                    break;
                }
                case 6: {
                    uint64_t v = rng();
                    ranges[i] = ScalarRange::constant(v);
                    concrete[i] = v;
                    break;
                }
                default: {
                    JumpOp jop = static_cast<JumpOp>(rng() % 10);
                    auto br = ScalarRange::narrowBranch(jop, ranges[i], ranges[j]);
                    if (evalCond(jop, concrete[i], concrete[j])) {
                        CHECK(br.takenLeft.contains(concrete[i]));
                        CHECK(br.takenRight.contains(concrete[j]));
                        ranges[i] = br.takenLeft;
                        ranges[j] = br.takenRight;
                    } else {
                        CHECK(br.fallLeft.contains(concrete[i]));
                        CHECK(br.fallRight.contains(concrete[j]));
                        ranges[i] = br.fallLeft;
                        ranges[j] = br.fallRight;
                    }
                    break;
                }
            }
            if (op <= 6) CHECK(ranges[i].contains(concrete[i]));
            if ((rng() & 0xf) == 0) {
                uint64_t width = 1ULL << (rng() % 4);
                uint64_t size = 1 + rng() % 256;
                if (netsec::checkMapValueAccess(ranges[i], width, size)) {
                    CHECK(static_cast<int64_t>(concrete[i]) >= 0);
                    CHECK(concrete[i] <= size - width);
                }
            }
        }
    }
}

int main() {
    testAndNarrowsNegative();
    testAddWrapDegrades();
    testSignedBranchNarrow();
    testContradictionUnreachable();
    testBoundaryAccess();
    testRandomPrograms();
    if (g_failures != 0) {
        std::fprintf(stderr, "%d check(s) failed\n", g_failures);
        return 1;
    }
    std::printf("all netsec range tests passed\n");
    return 0;
}
