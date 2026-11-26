#include "ebpf/verifier.hpp"

#include <algorithm>
#include <cstdint>
#include <cstdlib>
#include <iostream>
#include <optional>
#include <random>
#include <string>
#include <vector>

namespace {

using ebpf::AluOp;
using ebpf::CompareOp;
using ebpf::Instruction;
using ebpf::Program;
using ebpf::Scalar;
using ebpf::Tnum;

[[noreturn]] void fail(const std::string& message) {
    std::cerr << "FAIL: " << message << '\n';
    std::exit(1);
}

void require(bool condition, const std::string& message) {
    if (!condition) {
        fail(message);
    }
}

[[nodiscard]] std::vector<std::uint64_t> tnum_values(Tnum tnum,
                                                     unsigned bits) {
    const std::uint64_t mask = tnum.mask & ((std::uint64_t{1} << bits) - 1);
    std::vector<std::uint64_t> values;
    std::uint64_t subset = 0;
    do {
        values.push_back(tnum.value | subset);
        subset = (subset - mask) & mask;
    } while (subset != 0);
    return values;
}

[[nodiscard]] std::vector<std::uint64_t> tnum_values(Tnum tnum) {
    std::vector<std::uint64_t> values;
    std::uint64_t subset = 0;
    do {
        values.push_back(tnum.value | subset);
        subset = (subset - tnum.mask) & tnum.mask;
    } while (subset != 0);
    return values;
}

[[nodiscard]] Tnum random_tnum(std::mt19937_64& rng, unsigned bits) {
    const std::uint64_t mask = (std::uint64_t{1} << bits) - 1;
    const std::uint64_t unknown = rng() & mask;
    const std::uint64_t ones = rng() & (mask & ~unknown);
    return Tnum::make(ones, unknown);
}

[[nodiscard]] Scalar scalar_from_samples(
    const std::vector<std::uint64_t>& values, Tnum tnum) {
    require(!values.empty(), "sample set must not be empty");
    std::uint64_t umin = values.front();
    std::uint64_t umax = values.front();
    std::int64_t smin = static_cast<std::int64_t>(values.front());
    std::int64_t smax = smin;
    std::uint32_t l32min = static_cast<std::uint32_t>(values.front());
    std::uint32_t l32max = l32min;
    std::int32_t s32min = static_cast<std::int32_t>(l32min);
    std::int32_t s32max = s32min;
    for (std::uint64_t value : values) {
        umin = std::min(umin, value);
        umax = std::max(umax, value);
        const auto signed_value = static_cast<std::int64_t>(value);
        smin = std::min(smin, signed_value);
        smax = std::max(smax, signed_value);
        const auto low = static_cast<std::uint32_t>(value);
        l32min = std::min(l32min, low);
        l32max = std::max(l32max, low);
        const auto signed_low = static_cast<std::int32_t>(low);
        s32min = std::min(s32min, signed_low);
        s32max = std::max(s32max, signed_low);
    }
    Scalar scalar;
    scalar.tnum = tnum;
    scalar.umin64 = umin;
    scalar.umax64 = umax;
    scalar.smin64 = smin;
    scalar.smax64 = smax;
    scalar.u32_min = l32min;
    scalar.u32_max = l32max;
    scalar.s32_min = s32min;
    scalar.s32_max = s32max;
    scalar.normalize();
    require(!scalar.empty, "generated scalar unexpectedly empty");
    return scalar;
}

void test_tnum_soundness() {
    for (unsigned bits : {3U, 4U, 5U}) {
        const std::uint64_t mask = (std::uint64_t{1} << bits) - 1;
        for (Tnum a : {Tnum::constant(0), Tnum::full(),
                       Tnum::make(0b101, 0b010), Tnum::make(0, mask)}) {
            if ((a.mask & ~mask) != 0) {
                continue;
            }
            for (Tnum b : {Tnum::constant(1), Tnum::make(0b011, 0b100),
                           Tnum::make(mask, 0), Tnum::make(0, mask)}) {
                if ((b.mask & ~mask) != 0) {
                    continue;
                }
                const auto av = tnum_values(a, bits);
                const auto bv = tnum_values(b, bits);
                for (std::uint64_t x : av) {
                    for (std::uint64_t y : bv) {
                        require(ebpf::tnum_add(a, b).contains(x + y),
                                "tnum add unsound");
                        require(ebpf::tnum_sub(a, b).contains(x - y),
                                "tnum sub unsound");
                        require(ebpf::tnum_and(a, b).contains(x & y),
                                "tnum and unsound");
                        require(ebpf::tnum_or(a, b).contains(x | y),
                                "tnum or unsound");
                        require(ebpf::tnum_xor(a, b).contains(x ^ y),
                                "tnum xor unsound");
                        require(ebpf::tnum_cast32(a).contains(
                                    x & static_cast<std::uint64_t>(ebpf::kU32Max)),
                                "tnum cast32 unsound");
                        for (unsigned shift = 0; shift < bits; ++shift) {
                            require(ebpf::tnum_lshift(a, shift).contains(x << shift),
                                    "tnum lshift unsound");
                            require(ebpf::tnum_rshift(a, shift).contains(x >> shift),
                                    "tnum rshift unsound");
                            const auto signed_x = static_cast<std::int64_t>(x);
                            const auto shifted = static_cast<std::uint64_t>(
                                static_cast<std::int64_t>(signed_x >> shift));
                            require(ebpf::tnum_arshift(a, shift).contains(shifted),
                                    "tnum arshift unsound");
                        }
                    }
                }
            }
        }
    }

    std::mt19937_64 rng(0x5eed);
    for (int iteration = 0; iteration < 3000; ++iteration) {
        const Tnum a = random_tnum(rng, 8);
        const Tnum b = random_tnum(rng, 8);
        for (std::uint64_t x : tnum_values(a, 8)) {
            for (std::uint64_t y : tnum_values(b, 8)) {
                require(ebpf::tnum_add(a, b).contains(x + y),
                        "random tnum add unsound");
                require(ebpf::tnum_sub(a, b).contains(x - y),
                        "random tnum sub unsound");
                require(ebpf::tnum_and(a, b).contains(x & y),
                        "random tnum and unsound");
                require(ebpf::tnum_or(a, b).contains(x | y),
                        "random tnum or unsound");
                require(ebpf::tnum_xor(a, b).contains(x ^ y),
                        "random tnum xor unsound");
            }
        }
    }
}

[[nodiscard]] std::uint64_t concrete_alu64(AluOp op, std::uint64_t a,
                                           std::uint64_t b) {
    switch (op) {
        case AluOp::Add:
            return a + b;
        case AluOp::Sub:
            return a - b;
        case AluOp::Mul:
            return a * b;
        case AluOp::Div:
            return b == 0 ? 0 : a / b;
        case AluOp::Mod:
            return b == 0 ? a : a % b;
        case AluOp::And:
            return a & b;
        case AluOp::Or:
            return a | b;
        case AluOp::Xor:
            return a ^ b;
        case AluOp::Lsh:
            return a << (b & 63);
        case AluOp::Rsh:
            return a >> (b & 63);
        case AluOp::Arsh: {
            const auto signed_value = static_cast<std::int64_t>(a);
            return static_cast<std::uint64_t>(signed_value >> (b & 63));
        }
        case AluOp::Neg:
            return 0 - a;
        case AluOp::Mov:
            return b;
    }
    return 0;
}

[[nodiscard]] std::uint32_t concrete_alu32(AluOp op, std::uint32_t a,
                                           std::uint32_t b) {
    return static_cast<std::uint32_t>(concrete_alu64(op, a, b));
}

void test_alu_soundness() {
    std::mt19937_64 rng(0xabcdef);
    const std::vector<AluOp> ops = {
        AluOp::Add, AluOp::Sub, AluOp::Mul, AluOp::Div, AluOp::Mod,
        AluOp::And, AluOp::Or,  AluOp::Xor, AluOp::Lsh, AluOp::Rsh,
        AluOp::Arsh};

    for (int iteration = 0; iteration < 80; ++iteration) {
        const Tnum at = random_tnum(rng, 6);
        const Tnum bt = random_tnum(rng, 5);
        const auto av = tnum_values(at, 6);
        const auto bv = tnum_values(bt, 5);
        const Scalar as = scalar_from_samples(av, at);
        const Scalar bs = scalar_from_samples(bv, bt);
        for (AluOp op : ops) {
            const Scalar out = ebpf::alu64(op, as, bs);
            for (std::uint64_t a : av) {
                for (std::uint64_t b : bv) {
                    const std::uint64_t result = concrete_alu64(op, a, b);
                    require(out.contains(result),
                            "ALU64 transfer is unsound");
                }
            }
            const Scalar out32 = ebpf::alu32(op, as, bs);
            for (std::uint64_t a : av) {
                for (std::uint64_t b : bv) {
                    const std::uint32_t result = concrete_alu32(
                        op, static_cast<std::uint32_t>(a),
                        static_cast<std::uint32_t>(b));
                    require(out32.contains(result),
                            "ALU32 transfer is unsound");
                    require(out32.umax64 <= ebpf::kU32Max,
                            "ALU32 result was not zero-extended");
                }
            }
        }
        require(ebpf::alu64(AluOp::Neg, as, bs).contains(0 - av.front()),
                "ALU64 neg unsound");
        require(ebpf::alu32(AluOp::Neg, as, bs).contains(
                    static_cast<std::uint32_t>(0 - av.front())),
                "ALU32 neg unsound");
    }
}

void test_alu_edge_soundness() {
    const std::vector<AluOp> ops = {
        AluOp::Add, AluOp::Sub, AluOp::Mul, AluOp::And, AluOp::Or,
        AluOp::Xor, AluOp::Lsh, AluOp::Rsh, AluOp::Arsh};
    const std::vector<std::uint64_t> bases = {
        0xFFFFFFFFFFFFFF00ULL, 0xFFFFFFFFFFFFFFF0ULL,
        0x7FFFFFFFFFFFFFF0ULL, 0x8000000000000000ULL,
        0x00000000FFFFFFF0ULL, 0xFFFFFFFF00000000ULL};
    for (std::uint64_t base : bases) {
        const Tnum tnum = Tnum::make(base, 0x0f);
        const auto values = tnum_values(tnum);
        const Scalar scalar = scalar_from_samples(values, tnum);
        for (AluOp op : ops) {
            for (std::uint64_t lhs : values) {
                for (std::uint64_t rhs : values) {
                    require(scalar.contains(lhs) && scalar.contains(rhs),
                            "edge scalar dropped enumerated member");
                    require(ebpf::alu64(op, scalar, scalar).contains(
                                concrete_alu64(op, lhs, rhs)),
                            "edge ALU64 transfer is unsound");
                    require(ebpf::alu32(op, scalar, scalar).contains(
                                concrete_alu32(
                                    op, static_cast<std::uint32_t>(lhs),
                                    static_cast<std::uint32_t>(rhs))),
                            "edge ALU32 transfer is unsound");
                }
            }
        }
    }
}

void test_tnum_range_membership() {
    for (std::uint64_t lo = 0; lo < 32; ++lo) {
        for (std::uint64_t hi = lo; hi < 32; ++hi) {
            const Tnum tnum = Tnum::from_unsigned_range(lo, hi);
            for (std::uint64_t value = lo; value <= hi; ++value) {
                require(tnum.contains(value),
                        "range tnum did not contain range element");
            }
        }
    }
}

void test_tnum_signed_extrema_and_arshift() {
    std::mt19937_64 rng(0x1234abcd);
    for (int iteration = 0; iteration < 400; ++iteration) {
        std::uint64_t mask = 0;
        const unsigned unknowns = static_cast<unsigned>(rng() % 10);
        for (unsigned bit = 0; bit < unknowns; ++bit) {
            mask |= std::uint64_t{1} << (rng() % 64);
        }
        const std::uint64_t value = rng() & ~mask;
        const Tnum tnum = Tnum::make(value, mask);
        const auto values = tnum_values(tnum);
        std::int64_t expected_min = INT64_MAX;
        std::int64_t expected_max = INT64_MIN;
        for (std::uint64_t concrete : values) {
            const auto signed_value = static_cast<std::int64_t>(concrete);
            expected_min = std::min(expected_min, signed_value);
            expected_max = std::max(expected_max, signed_value);
        }
        require(tnum.smin() == expected_min,
                "tnum.smin does not equal concrete signed minimum");
        require(tnum.smax() == expected_max,
                "tnum.smax does not equal concrete signed maximum");

        for (std::uint64_t concrete : values) {
            const auto signed_value = static_cast<std::int64_t>(concrete);
            for (unsigned shift : {0U, 1U, 7U, 31U, 63U}) {
                const __int128 shifted =
                    static_cast<__int128>(signed_value) >> shift;
                require(ebpf::tnum_arshift(tnum, shift).contains(
                            static_cast<std::uint64_t>(shifted)),
                        "tnum arshift unsound for signed input");
            }
            require(ebpf::tnum_lshift(tnum, 3).contains(concrete << 3),
                    "tnum lshift unsound for high input");
            require(ebpf::tnum_rshift(tnum, 3).contains(concrete >> 3),
                    "tnum rshift unsound for high input");
        }
    }

    for (int iteration = 0; iteration < 500; ++iteration) {
        const Tnum a = random_tnum(rng, 6);
        const Tnum b = random_tnum(rng, 6);
        const Tnum joined = ebpf::tnum_join(a, b);
        for (std::uint64_t concrete : tnum_values(a, 6)) {
            require(joined.contains(concrete), "tnum join dropped lhs value");
        }
        for (std::uint64_t concrete : tnum_values(b, 6)) {
            require(joined.contains(concrete), "tnum join dropped rhs value");
        }
    }
}

void test_scalar_refinement_cases() {
    const Scalar masked =
        ebpf::alu64(AluOp::And, Scalar::full(), Scalar::constant(0xff));
    require(!masked.empty, "mask result empty");
    require(masked.umax64 <= 0xff, "tnum/range mask did not cap umax");
    require((masked.tnum.mask >> 8) == 0, "mask did not become known zero");

    const Scalar add32 =
        ebpf::alu32(AluOp::Add, Scalar::constant(0xffffffffULL),
                    Scalar::constant(2));
    require(add32.constant_value().value_or(~0ULL) == 1,
            "ALU32 wrap or zero extension failed");
    require(add32.umax64 <= ebpf::kU32Max, "ALU32 high bits not cleared");

    const Scalar nonnegative = Scalar::from_unsigned_range(0, 100);
    const auto exact_zero =
        ebpf::refine_constant(nonnegative, CompareOp::Sle, 0, true);
    require(exact_zero.has_value(), "signed/unsigned branch unexpectedly dead");
    require(exact_zero->constant_value().value_or(~0ULL) == 0,
            "signed/unsigned branch failed to derive exact constant");

    const Scalar bounded = Scalar::from_unsigned_range(0, 10);
    const auto impossible =
        ebpf::refine_constant(bounded, CompareOp::Sgt, 20, true);
    require(!impossible.has_value(),
            "contradictory signed/unsigned branch was not dead");

    const Scalar overflow =
        ebpf::alu64(AluOp::Add, Scalar::from_unsigned_range(0, ebpf::kU64Max),
                    Scalar::constant(1));
    require(!overflow.empty, "full-range overflow became empty");
    require(overflow.umin64 == 0 && overflow.umax64 == ebpf::kU64Max,
            "overflow was not widened to the full unsigned set");

    const Scalar high_values =
        Scalar::from_unsigned_range(0x200000000ULL, 0x300000000ULL);
    const auto low_zero =
        ebpf::refine_constant(high_values, CompareOp::Eq, 0, true, true);
    require(low_zero.has_value(), "32-bit equality should remain reachable");
    require(low_zero->u32_min == 0 && low_zero->u32_max == 0,
            "32-bit branch did not constrain low 32 bits");
    require(!low_zero->is_constant(),
            "32-bit comparison incorrectly equated the high halves");
}

void test_branch_soundness() {
    std::mt19937_64 rng(0x31415926);
    const std::vector<CompareOp> ops = {
        CompareOp::Eq,  CompareOp::Ne,  CompareOp::Lt,
        CompareOp::Le,  CompareOp::Gt,  CompareOp::Ge,
        CompareOp::Slt, CompareOp::Sle, CompareOp::Sgt,
        CompareOp::Sge};
    auto holds = [](CompareOp op, std::uint64_t a, std::uint64_t b) {
        const auto sa = static_cast<std::int64_t>(a);
        const auto sb = static_cast<std::int64_t>(b);
        switch (op) {
            case CompareOp::Eq:
                return a == b;
            case CompareOp::Ne:
                return a != b;
            case CompareOp::Lt:
                return a < b;
            case CompareOp::Le:
                return a <= b;
            case CompareOp::Gt:
                return a > b;
            case CompareOp::Ge:
                return a >= b;
            case CompareOp::Slt:
                return sa < sb;
            case CompareOp::Sle:
                return sa <= sb;
            case CompareOp::Sgt:
                return sa > sb;
            case CompareOp::Sge:
                return sa >= sb;
        }
        return false;
    };

    for (int iteration = 0; iteration < 1000; ++iteration) {
        const std::uint64_t a = rng();
        const std::uint64_t b = rng();
        const Scalar lhs = Scalar::constant(a);
        for (CompareOp op : ops) {
            const ebpf::BranchResult split =
                ebpf::refine_branch(lhs, Scalar::constant(b), op, false);
            const bool truth = holds(op, a, b);
            require(split.taken.reachable == truth,
                    "branch truth split is incorrect");
            require(split.fallthrough.reachable == !truth,
                    "branch false split is incorrect");
        }
        const Scalar range =
            Scalar::from_unsigned_range(a & ~0xffULL, a | 0xff);
        for (CompareOp op : ops) {
            const ebpf::BranchResult split =
                ebpf::refine_branch_constant(range, op, b, false);
            if (split.taken.reachable) {
                require(split.taken.lhs.contains(a),
                        "taken branch dropped a concrete value");
            } else {
                require(!holds(op, a, b),
                        "taken branch was unsoundly marked dead");
            }
            if (split.fallthrough.reachable) {
                require(split.fallthrough.lhs.contains(a),
                        "false branch dropped a concrete value");
            } else {
                require(holds(op, a, b),
                        "false branch was unsoundly marked dead");
            }
        }
    }
}

[[nodiscard]] Tnum random_tnum_containing(std::mt19937_64& rng,
                                          std::uint64_t value) {
    std::uint64_t mask = 0;
    const unsigned unknowns = static_cast<unsigned>(rng() % 9);
    for (unsigned bit = 0; bit < unknowns; ++bit) {
        mask |= std::uint64_t{1} << (rng() % 64);
    }
    return Tnum::make(value & ~mask, mask);
}

[[nodiscard]] Scalar random_scalar_containing(std::mt19937_64& rng,
                                              std::uint64_t value) {
    const unsigned span_bits = static_cast<unsigned>(rng() % 9);
    const std::uint64_t span =
        span_bits == 0 ? 0 : ((std::uint64_t{1} << span_bits) - 1);
    const std::uint64_t base = value & ~span;
    const std::uint64_t high = base | span;
    const std::int64_t signed_value = static_cast<std::int64_t>(value);
    const std::int64_t signed_span =
        static_cast<std::int64_t>(rng() % (span + 1));
    const std::int32_t low = static_cast<std::int32_t>(
        static_cast<std::uint32_t>(value));
    const std::uint32_t low_span = static_cast<std::uint32_t>(span);

    Scalar scalar;
    scalar.tnum = random_tnum_containing(rng, value);
    scalar.umin64 = base;
    scalar.umax64 = high;
    scalar.smin64 =
        signed_value < INT64_MIN + signed_span
            ? INT64_MIN
            : signed_value - signed_span;
    scalar.smax64 =
        signed_value > INT64_MAX - signed_span
            ? INT64_MAX
            : signed_value + signed_span;
    scalar.u32_min =
        static_cast<std::uint32_t>(low) & ~low_span;
    scalar.u32_max = scalar.u32_min | low_span;
    const std::int64_t signed_low = low;
    scalar.s32_min = static_cast<std::int32_t>(
        std::max<std::int64_t>(INT32_MIN, signed_low - signed_span));
    scalar.s32_max = static_cast<std::int32_t>(
        std::min<std::int64_t>(INT32_MAX, signed_low + signed_span));
    scalar.normalize();
    require(!scalar.empty, "random scalar containing a value normalized empty");
    require(scalar.contains(value),
            "normalized scalar dropped a concrete member");
    return scalar;
}

void test_random_scalar_and_alu64_soundness() {
    std::mt19937_64 rng(0xfeedface);
    for (int iteration = 0; iteration < 5000; ++iteration) {
        const std::uint64_t value = rng();
        (void)random_scalar_containing(rng, value);
    }

    const std::vector<AluOp> ops = {
        AluOp::Add,  AluOp::Sub, AluOp::Mul, AluOp::And, AluOp::Or,
        AluOp::Xor,  AluOp::Lsh, AluOp::Rsh, AluOp::Arsh};
    for (int iteration = 0; iteration < 1200; ++iteration) {
        const std::uint64_t a = rng();
        const std::uint64_t b = rng();
        const Scalar as = random_scalar_containing(rng, a);
        const Scalar bs = random_scalar_containing(rng, b);
        for (AluOp op : ops) {
            const Scalar out = ebpf::alu64(op, as, bs);
            require(out.contains(concrete_alu64(op, a, b)),
                    "random ALU64 transfer is unsound");
            const Scalar out32 = ebpf::alu32(op, as, bs);
            require(out32.contains(concrete_alu32(
                        op, static_cast<std::uint32_t>(a),
                        static_cast<std::uint32_t>(b))),
                    "random ALU32 transfer is unsound");
        }
    }
}

[[nodiscard]] bool concrete_compare(CompareOp op, std::uint64_t a,
                                    std::uint64_t b, bool is32) {
    if (is32) {
        const auto lhs = static_cast<std::uint32_t>(a);
        const auto rhs = static_cast<std::uint32_t>(b);
        const auto signed_lhs = static_cast<std::int32_t>(lhs);
        const auto signed_rhs = static_cast<std::int32_t>(rhs);
        switch (op) {
            case CompareOp::Eq:
                return lhs == rhs;
            case CompareOp::Ne:
                return lhs != rhs;
            case CompareOp::Lt:
                return lhs < rhs;
            case CompareOp::Le:
                return lhs <= rhs;
            case CompareOp::Gt:
                return lhs > rhs;
            case CompareOp::Ge:
                return lhs >= rhs;
            case CompareOp::Slt:
                return signed_lhs < signed_rhs;
            case CompareOp::Sle:
                return signed_lhs <= signed_rhs;
            case CompareOp::Sgt:
                return signed_lhs > signed_rhs;
            case CompareOp::Sge:
                return signed_lhs >= signed_rhs;
        }
    }
    const auto signed_lhs = static_cast<std::int64_t>(a);
    const auto signed_rhs = static_cast<std::int64_t>(b);
    switch (op) {
        case CompareOp::Eq:
            return a == b;
        case CompareOp::Ne:
            return a != b;
        case CompareOp::Lt:
            return a < b;
        case CompareOp::Le:
            return a <= b;
        case CompareOp::Gt:
            return a > b;
        case CompareOp::Ge:
            return a >= b;
        case CompareOp::Slt:
            return signed_lhs < signed_rhs;
        case CompareOp::Sle:
            return signed_lhs <= signed_rhs;
        case CompareOp::Sgt:
            return signed_lhs > signed_rhs;
        case CompareOp::Sge:
            return signed_lhs >= signed_rhs;
    }
    return false;
}

void test_random_pair_branch_soundness() {
    std::mt19937_64 rng(0x10293847);
    const std::vector<CompareOp> ops = {
        CompareOp::Eq,  CompareOp::Ne,  CompareOp::Lt,
        CompareOp::Le,  CompareOp::Gt,  CompareOp::Ge,
        CompareOp::Slt, CompareOp::Sle, CompareOp::Sgt,
        CompareOp::Sge};
    for (int iteration = 0; iteration < 1200; ++iteration) {
        const std::uint64_t a = rng();
        const std::uint64_t b = rng();
        const Scalar lhs = random_scalar_containing(rng, a);
        const Scalar rhs = random_scalar_containing(rng, b);
        for (bool is32 : {false, true}) {
            for (CompareOp op : ops) {
                const ebpf::BranchResult split =
                    ebpf::refine_branch(lhs, rhs, op, is32);
                const bool truth = concrete_compare(op, a, b, is32);
                const auto& active =
                    truth ? split.taken : split.fallthrough;
                require(active.reachable,
                        "pair branch unsoundly discarded concrete execution");
                require(active.lhs.contains(a),
                        "pair branch dropped concrete lhs");
                require(active.rhs.contains(b),
                        "pair branch dropped concrete rhs");
            }
        }
    }
}

constexpr std::uint8_t kAlu64 = 0x07;
constexpr std::uint8_t kAlu32 = 0x04;
constexpr std::uint8_t kJmp = 0x05;
constexpr std::uint8_t kAdd = 0x00;
constexpr std::uint8_t kAnd = 0x50;
constexpr std::uint8_t kMov = 0xb0;
constexpr std::uint8_t kK = 0x00;
constexpr std::uint8_t kX = 0x08;
constexpr std::uint8_t kJge = 0x30;
constexpr std::uint8_t kJgt = 0x20;
constexpr std::uint8_t kJeq = 0x10;
constexpr std::uint8_t kExit = 0x95;
constexpr std::uint8_t kMemByte = 0x71;

Instruction alu64_imm(std::uint8_t op, std::uint8_t dst, std::int32_t imm) {
    return {static_cast<std::uint8_t>(kAlu64 | op | kK), dst, 0, 0, imm};
}

Instruction alu64_reg(std::uint8_t op, std::uint8_t dst, std::uint8_t src) {
    return {static_cast<std::uint8_t>(kAlu64 | op | kX), dst, src, 0, 0};
}

Instruction alu32_imm(std::uint8_t op, std::uint8_t dst, std::int32_t imm) {
    return {static_cast<std::uint8_t>(kAlu32 | op | kK), dst, 0, 0, imm};
}

Instruction jump_imm(std::uint8_t op, std::uint8_t dst, std::int32_t imm,
                     std::int16_t off) {
    return {static_cast<std::uint8_t>(kJmp | op | kK), dst, 0, off, imm};
}

Instruction jump_reg(std::uint8_t op, std::uint8_t dst, std::uint8_t src,
                     std::int16_t off) {
    return {static_cast<std::uint8_t>(kJmp | op | kX), dst, src, off, 0};
}

Instruction load_byte(std::uint8_t dst, std::uint8_t src, std::int16_t off) {
    return {kMemByte, dst, src, off, 0};
}

Program make_program(std::vector<Instruction> instructions,
                     std::uint64_t packet_length) {
    Program program;
    program.instructions = std::move(instructions);
    program.packet_length = packet_length;
    return program;
}

void test_verifier_packet_bounds() {
    {
        Program program = make_program(
            {
                alu64_imm(kAdd, 1, 1),
                jump_reg(kJge, 1, 2, 1),
                load_byte(0, 1, 0),
                {kExit, 0, 0, 0, 0},
            },
            64);
        const auto result = ebpf::verify_dag(program);
        require(result.ok,
                "safe pkt_end-bounded load rejected: " + result.error);
    }

    {
        Program program = make_program(
            {
                alu64_imm(kAdd, 1, 64),
                jump_reg(kJgt, 1, 2, 1),
                load_byte(0, 1, 0),
                {kExit, 0, 0, 0, 0},
            },
            64);
        const auto result = ebpf::verify_dag(program);
        require(!result.ok, "off-by-one pkt_end load was not rejected");
        require(result.error.find("unsafe memory access") != std::string::npos,
                "wrong off-by-one rejection reason");
    }

    {
        Program program = make_program(
            {
                alu64_imm(kAdd, 1, 64),
                load_byte(0, 1, 0),
                {kExit, 0, 0, 0, 0},
            },
            64);
        const auto result = ebpf::verify_dag(program);
        require(!result.ok, "direct off-by-one overrun was not rejected");
    }

    {
        Program program = make_program(
            {
                alu64_imm(kMov, 3, -1),
                alu32_imm(kAdd, 3, 2),
                alu64_imm(kAnd, 3, 0xff),
                alu64_reg(kAdd, 1, 3),
                load_byte(0, 1, 0),
                {kExit, 0, 0, 0, 0},
            },
            2);
        const auto result = ebpf::verify_dag(program);
        require(result.ok,
                "ALU32/mask-safe packet load rejected: " + result.error);
    }

    {
        Program program = make_program(
            {
                alu64_imm(kAnd, 0, 0xff),
                alu64_reg(kAdd, 1, 0),
                load_byte(0, 1, 0),
                {kExit, 0, 0, 0, 0},
            },
            256);
        const auto result = ebpf::verify_dag(program);
        require(result.ok,
                "r &= 0xff safe boundary rejected: " + result.error);
    }

    {
        Program program = make_program(
            {
                alu64_imm(kAnd, 0, 0xff),
                alu64_reg(kAdd, 1, 0),
                load_byte(0, 1, 0),
                {kExit, 0, 0, 0, 0},
            },
            255);
        const auto result = ebpf::verify_dag(program);
        require(!result.ok,
                "r &= 0xff one-byte-over boundary was not rejected");
    }

    {
        Program program = make_program(
            {
                alu64_imm(kMov, 3, 2),
                alu64_reg(kAdd, 1, 3),
                load_byte(0, 1, 0),
                {kExit, 0, 0, 0, 0},
            },
            2);
        const auto result = ebpf::verify_dag(program);
        require(!result.ok, "known overrun was not rejected");
    }

    {
        Program program = make_program(
            {
                alu64_imm(kAdd, 1, 8),
                alu64_imm(0x10, 1, 4),
                load_byte(0, 1, 0),
                {kExit, 0, 0, 0, 0},
            },
            5);
        const auto result = ebpf::verify_dag(program);
        require(result.ok,
                "pointer immediate subtraction miscompiled: " + result.error);
    }

    {
        Program program = make_program(
            {
                alu64_imm(0x10, 1, 100),
                load_byte(0, 1, -100),
                {kExit, 0, 0, 0, 0},
            },
            64);
        const auto result = ebpf::verify_dag(program);
        require(!result.ok,
                "pointer subtraction plus negative load offset escaped");
    }

    {
        Program program;
        program.packet_length = 2;
        program.instructions = {
            {0x18, 3, 0, 0, 1},
            {0x18, 0, 0, 0, 0},
            alu64_reg(kAdd, 1, 3),
            load_byte(0, 1, 0),
            {kExit, 0, 0, 0, 0},
        };
        const auto result = ebpf::verify_dag(program);
        require(result.ok,
                "LD_IMM64 payload was mistaken for an instruction: " +
                    result.error);
    }

    {
        Program program = make_program(
            {
                alu64_imm(kMov, 3, 0),
                jump_imm(kJeq, 0, 0, 1),
                alu64_imm(kMov, 3, 5),
                alu64_reg(kAdd, 1, 3),
                load_byte(0, 1, 0),
                {kExit, 0, 0, 0, 0},
            },
            10);
        const auto result = ebpf::verify_dag(program);
        require(result.ok, "safe DAG join rejected: " + result.error);
    }
}

}  // namespace

int main() {
    test_tnum_soundness();
    test_tnum_range_membership();
    test_tnum_signed_extrema_and_arshift();
    test_alu_soundness();
    test_alu_edge_soundness();
    test_scalar_refinement_cases();
    test_random_scalar_and_alu64_soundness();
    test_branch_soundness();
    test_random_pair_branch_soundness();
    test_verifier_packet_bounds();
    std::cout << "all ebpf_verifier tests passed\n";
    return 0;
}
