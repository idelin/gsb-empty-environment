#include "ebpf/verifier.hpp"

#include "domain_internal.hpp"

#include <algorithm>
#include <bit>
#include <cstdint>
#include <limits>
#include <optional>

namespace ebpf {
namespace {

[[nodiscard]] Scalar empty_scalar() noexcept {
    Scalar out;
    out.empty = true;
    return out;
}

[[nodiscard]] Scalar make64(Tnum tnum, std::uint64_t umin, std::uint64_t umax,
                            std::int64_t smin, std::int64_t smax) noexcept {
    Scalar out;
    out.tnum = tnum;
    out.umin64 = umin;
    out.umax64 = umax;
    out.smin64 = smin;
    out.smax64 = smax;
    out.u32_min = 0;
    out.u32_max = kU32Max;
    out.s32_min = kI32Min;
    out.s32_max = kI32Max;
    out.normalize();
    return out;
}

[[nodiscard]] Scalar make32(Tnum tnum, std::uint32_t umin,
                            std::uint32_t umax, std::int32_t smin,
                            std::int32_t smax) noexcept {
    Scalar out;
    out.tnum =
        Tnum::make(tnum.value & static_cast<std::uint64_t>(kU32Max),
                   tnum.mask & static_cast<std::uint64_t>(kU32Max));
    out.umin64 = umin;
    out.umax64 = umax;
    out.smin64 = static_cast<std::int64_t>(umin);
    out.smax64 = static_cast<std::int64_t>(umax);
    out.u32_min = umin;
    out.u32_max = umax;
    out.s32_min = smin;
    out.s32_max = smax;
    out.normalize();
    return out;
}

[[nodiscard]] bool signed_add64(std::int64_t alo, std::int64_t ahi,
                                std::int64_t blo, std::int64_t bhi,
                                std::int64_t& lo, std::int64_t& hi) noexcept {
    const __int128 min_value = static_cast<__int128>(alo) + blo;
    const __int128 max_value = static_cast<__int128>(ahi) + bhi;
    if (min_value < kI64Min || max_value > kI64Max) {
        return false;
    }
    lo = static_cast<std::int64_t>(min_value);
    hi = static_cast<std::int64_t>(max_value);
    return true;
}

[[nodiscard]] bool signed_sub64(std::int64_t alo, std::int64_t ahi,
                                std::int64_t blo, std::int64_t bhi,
                                std::int64_t& lo, std::int64_t& hi) noexcept {
    const __int128 min_value = static_cast<__int128>(alo) - bhi;
    const __int128 max_value = static_cast<__int128>(ahi) - blo;
    if (min_value < kI64Min || max_value > kI64Max) {
        return false;
    }
    lo = static_cast<std::int64_t>(min_value);
    hi = static_cast<std::int64_t>(max_value);
    return true;
}

[[nodiscard]] bool signed_mul64(std::int64_t alo, std::int64_t ahi,
                                std::int64_t blo, std::int64_t bhi,
                                std::int64_t& lo, std::int64_t& hi) noexcept {
    const __int128 products[4] = {
        static_cast<__int128>(alo) * blo, static_cast<__int128>(alo) * bhi,
        static_cast<__int128>(ahi) * blo, static_cast<__int128>(ahi) * bhi};
    __int128 min_value = products[0];
    __int128 max_value = products[0];
    for (__int128 value : products) {
        min_value = std::min(min_value, value);
        max_value = std::max(max_value, value);
    }
    if (min_value < kI64Min || max_value > kI64Max) {
        return false;
    }
    lo = static_cast<std::int64_t>(min_value);
    hi = static_cast<std::int64_t>(max_value);
    return true;
}

[[nodiscard]] bool signed_add32(std::int32_t alo, std::int32_t ahi,
                                std::int32_t blo, std::int32_t bhi,
                                std::int32_t& lo, std::int32_t& hi) noexcept {
    const std::int64_t min_value = static_cast<std::int64_t>(alo) + blo;
    const std::int64_t max_value = static_cast<std::int64_t>(ahi) + bhi;
    if (min_value < kI32Min || max_value > kI32Max) {
        return false;
    }
    lo = static_cast<std::int32_t>(min_value);
    hi = static_cast<std::int32_t>(max_value);
    return true;
}

[[nodiscard]] bool signed_sub32(std::int32_t alo, std::int32_t ahi,
                                std::int32_t blo, std::int32_t bhi,
                                std::int32_t& lo, std::int32_t& hi) noexcept {
    const std::int64_t min_value = static_cast<std::int64_t>(alo) - bhi;
    const std::int64_t max_value = static_cast<std::int64_t>(ahi) - blo;
    if (min_value < kI32Min || max_value > kI32Max) {
        return false;
    }
    lo = static_cast<std::int32_t>(min_value);
    hi = static_cast<std::int32_t>(max_value);
    return true;
}

[[nodiscard]] bool signed_mul32(std::int32_t alo, std::int32_t ahi,
                                std::int32_t blo, std::int32_t bhi,
                                std::int32_t& lo, std::int32_t& hi) noexcept {
    const std::int64_t products[4] = {
        static_cast<std::int64_t>(alo) * blo,
        static_cast<std::int64_t>(alo) * bhi,
        static_cast<std::int64_t>(ahi) * blo,
        static_cast<std::int64_t>(ahi) * bhi};
    std::int64_t min_value = products[0];
    std::int64_t max_value = products[0];
    for (std::int64_t value : products) {
        min_value = std::min(min_value, value);
        max_value = std::max(max_value, value);
    }
    if (min_value < kI32Min || max_value > kI32Max) {
        return false;
    }
    lo = static_cast<std::int32_t>(min_value);
    hi = static_cast<std::int32_t>(max_value);
    return true;
}

[[nodiscard]] std::int64_t arshift64(std::int64_t value,
                                     unsigned shift) noexcept {
    if (shift == 0) {
        return value;
    }
    const __int128 divisor =
        static_cast<__int128>(1) << static_cast<unsigned>(shift);
    __int128 quotient = static_cast<__int128>(value) / divisor;
    if (value < 0 && quotient * divisor != value) {
        --quotient;
    }
    return static_cast<std::int64_t>(quotient);
}

[[nodiscard]] std::int32_t arshift32(std::int32_t value,
                                     unsigned shift) noexcept {
    if (shift == 0) {
        return value;
    }
    const std::int64_t divisor = std::int64_t{1} << shift;
    std::int64_t quotient = static_cast<std::int64_t>(value) / divisor;
    if (value < 0 && quotient * divisor != value) {
        --quotient;
    }
    return static_cast<std::int32_t>(quotient);
}

[[nodiscard]] std::uint64_t cover_mask64(std::uint64_t value) noexcept {
    if (value == 0) {
        return 0;
    }
    const unsigned bits = std::bit_width(value);
    return bits == 64 ? kU64Max : ((std::uint64_t{1} << bits) - 1);
}

[[nodiscard]] std::uint32_t cover_mask32(std::uint32_t value) noexcept {
    if (value == 0) {
        return 0;
    }
    const unsigned bits = std::bit_width(value);
    return bits == 32 ? kU32Max : ((std::uint32_t{1} << bits) - 1);
}

[[nodiscard]] Tnum tnum_mul(Tnum a, Tnum b, unsigned width) noexcept {
    const auto a_const = a.is_constant() ? std::optional<std::uint64_t>(a.value)
                                        : std::nullopt;
    const auto b_const = b.is_constant() ? std::optional<std::uint64_t>(b.value)
                                        : std::nullopt;
    if ((a_const && *a_const == 0) || (b_const && *b_const == 0)) {
        return Tnum::constant(0);
    }
    if (a_const && *a_const == 1) {
        return b;
    }
    if (b_const && *b_const == 1) {
        return a;
    }
    if (a.is_constant() && std::has_single_bit(a.value)) {
        const unsigned shift = std::countr_zero(a.value);
        if (shift < width) {
            return width == 32 ? tnum_lshift32(b, shift)
                               : tnum_lshift(b, shift);
        }
    }
    if (b.is_constant() && std::has_single_bit(b.value)) {
        const unsigned shift = std::countr_zero(b.value);
        if (shift < width) {
            return width == 32 ? tnum_lshift32(a, shift)
                               : tnum_lshift(a, shift);
        }
    }
    return Tnum::full();
}

[[nodiscard]] Scalar alu64_impl(AluOp op, const Scalar& lhs,
                                const Scalar& rhs) noexcept {
    if (lhs.empty || rhs.empty) {
        return empty_scalar();
    }

    Tnum tnum = Tnum::full();
    std::uint64_t umin = 0;
    std::uint64_t umax = kU64Max;
    std::int64_t smin = kI64Min;
    std::int64_t smax = kI64Max;

    switch (op) {
        case AluOp::Add:
            tnum = tnum_add(lhs.tnum, rhs.tnum);
            if (lhs.umax64 <= kU64Max - rhs.umax64) {
                umin = lhs.umin64 + rhs.umin64;
                umax = lhs.umax64 + rhs.umax64;
            }
            (void)signed_add64(lhs.smin64, lhs.smax64, rhs.smin64,
                               rhs.smax64, smin, smax);
            break;
        case AluOp::Sub:
            tnum = tnum_sub(lhs.tnum, rhs.tnum);
            if (lhs.umin64 >= rhs.umax64) {
                umin = lhs.umin64 - rhs.umax64;
                umax = lhs.umax64 - rhs.umin64;
            }
            (void)signed_sub64(lhs.smin64, lhs.smax64, rhs.smin64,
                               rhs.smax64, smin, smax);
            break;
        case AluOp::Mul:
            tnum = tnum_mul(lhs.tnum, rhs.tnum, 64);
            if (lhs.umax64 == 0 || rhs.umax64 == 0 ||
                lhs.umax64 <= kU64Max / rhs.umax64) {
                umin = lhs.umin64 * rhs.umin64;
                umax = lhs.umax64 * rhs.umax64;
            }
            (void)signed_mul64(lhs.smin64, lhs.smax64, rhs.smin64,
                               rhs.smax64, smin, smax);
            break;
        case AluOp::Div:
            if (lhs.tnum.is_constant() && rhs.tnum.is_constant() &&
                rhs.tnum.value != 0) {
                tnum = Tnum::constant(lhs.tnum.value / rhs.tnum.value);
            } else if (rhs.tnum.is_constant() && rhs.tnum.value == 1) {
                tnum = lhs.tnum;
            } else if (lhs.tnum.is_constant() && lhs.tnum.value == 0 &&
                       rhs.umin64 != 0) {
                tnum = Tnum::constant(0);
            }
            if (rhs.umin64 != 0) {
                umin = lhs.umin64 / rhs.umax64;
                umax = lhs.umax64 / rhs.umin64;
            }
            break;
        case AluOp::Mod:
            if (rhs.tnum.is_constant() && rhs.tnum.value == 1) {
                tnum = Tnum::constant(0);
            } else if (lhs.tnum.is_constant() && rhs.tnum.is_constant() &&
                       rhs.tnum.value != 0) {
                tnum = Tnum::constant(lhs.tnum.value % rhs.tnum.value);
            }
            if (rhs.umin64 != 0) {
                umin = 0;
                umax = std::min(lhs.umax64, rhs.umax64 - 1);
            }
            break;
        case AluOp::And:
            tnum = tnum_and(lhs.tnum, rhs.tnum);
            umin = 0;
            umax = std::min(lhs.umax64, rhs.umax64);
            break;
        case AluOp::Or:
            tnum = tnum_or(lhs.tnum, rhs.tnum);
            umin = std::max(lhs.umin64, rhs.umin64);
            umax = std::min(kU64Max, cover_mask64(lhs.umax64) |
                                         cover_mask64(rhs.umax64));
            break;
        case AluOp::Xor:
            tnum = tnum_xor(lhs.tnum, rhs.tnum);
            umin = 0;
            umax = std::min(kU64Max, cover_mask64(lhs.umax64) |
                                         cover_mask64(rhs.umax64));
            break;
        case AluOp::Lsh: {
            const auto shift = rhs.constant_value();
            if (!shift || *shift >= 64) {
                if (lhs.tnum.is_constant() && lhs.tnum.value == 0) {
                    tnum = Tnum::constant(0);
                }
                break;
            }
            tnum = tnum_lshift(lhs.tnum, static_cast<unsigned>(*shift));
            if (lhs.umax64 <= (kU64Max >> *shift)) {
                umin = lhs.umin64 << *shift;
                umax = lhs.umax64 << *shift;
            }
            break;
        }
        case AluOp::Rsh: {
            const auto shift = rhs.constant_value();
            if (!shift || *shift >= 64) {
                if (lhs.tnum.is_constant() && lhs.tnum.value == 0) {
                    tnum = Tnum::constant(0);
                }
                break;
            }
            tnum = tnum_rshift(lhs.tnum, static_cast<unsigned>(*shift));
            umin = lhs.umin64 >> *shift;
            umax = lhs.umax64 >> *shift;
            break;
        }
        case AluOp::Arsh: {
            const auto shift = rhs.constant_value();
            if (!shift || *shift >= 64) {
                break;
            }
            tnum = tnum_arshift(lhs.tnum, static_cast<unsigned>(*shift));
            smin = arshift64(lhs.smin64, static_cast<unsigned>(*shift));
            smax = arshift64(lhs.smax64, static_cast<unsigned>(*shift));
            const auto ranges = detail::unsigned_from_signed64(smin, smax);
            if (!ranges.empty()) {
                umin = ranges.front().lo;
                umax = ranges.back().hi;
            }
            break;
        }
        case AluOp::Neg:
            tnum = tnum_sub(Tnum::constant(0), lhs.tnum);
            if (lhs.umax64 == 0) {
                umin = umax = 0;
            }
            if (lhs.smin64 != kI64Min) {
                smin = -lhs.smax64;
                smax = -lhs.smin64;
            }
            break;
        case AluOp::Mov:
            return rhs;
    }
    return make64(tnum, umin, umax, smin, smax);
}

[[nodiscard]] Scalar alu32_impl(AluOp op, const Scalar& lhs,
                                const Scalar& rhs) noexcept {
    if (lhs.empty || rhs.empty) {
        return empty_scalar();
    }
    const Scalar lhs32 = cast32(lhs);
    const Scalar rhs32 = cast32(rhs);
    if (lhs32.empty || rhs32.empty) {
        return empty_scalar();
    }

    Tnum tnum = Tnum::full();
    std::uint32_t umin = 0;
    std::uint32_t umax = kU32Max;
    std::int32_t smin = kI32Min;
    std::int32_t smax = kI32Max;

    switch (op) {
        case AluOp::Add:
            tnum = tnum_add(lhs32.tnum, rhs32.tnum);
            if (static_cast<std::uint64_t>(lhs32.umax64) + rhs32.umax64 <=
                kU32Max) {
                umin = static_cast<std::uint32_t>(lhs32.umin64 + rhs32.umin64);
                umax = static_cast<std::uint32_t>(lhs32.umax64 + rhs32.umax64);
            }
            (void)signed_add32(lhs32.s32_min, lhs32.s32_max, rhs32.s32_min,
                               rhs32.s32_max, smin, smax);
            break;
        case AluOp::Sub:
            tnum = tnum_sub(lhs32.tnum, rhs32.tnum);
            if (lhs32.umin64 >= rhs32.umax64) {
                umin = static_cast<std::uint32_t>(lhs32.umin64 - rhs32.umax64);
                umax = static_cast<std::uint32_t>(lhs32.umax64 - rhs32.umin64);
            }
            (void)signed_sub32(lhs32.s32_min, lhs32.s32_max, rhs32.s32_min,
                               rhs32.s32_max, smin, smax);
            break;
        case AluOp::Mul:
            tnum = tnum_mul(lhs32.tnum, rhs32.tnum, 32);
            if (lhs32.umax64 == 0 || rhs32.umax64 == 0 ||
                lhs32.umax64 <= kU32Max / rhs32.umax64) {
                umin = static_cast<std::uint32_t>(lhs32.umin64 * rhs32.umin64);
                umax = static_cast<std::uint32_t>(lhs32.umax64 * rhs32.umax64);
            }
            (void)signed_mul32(lhs32.s32_min, lhs32.s32_max, rhs32.s32_min,
                               rhs32.s32_max, smin, smax);
            break;
        case AluOp::Div:
            if (lhs32.tnum.is_constant() && rhs32.tnum.is_constant() &&
                rhs32.tnum.value != 0) {
                tnum = Tnum::constant(lhs32.tnum.value / rhs32.tnum.value);
            } else if (rhs32.tnum.is_constant() && rhs32.tnum.value == 1) {
                tnum = lhs32.tnum;
            } else if (lhs32.tnum.is_constant() && lhs32.tnum.value == 0 &&
                       rhs32.umin64 != 0) {
                tnum = Tnum::constant(0);
            }
            if (rhs32.umin64 != 0) {
                umin = static_cast<std::uint32_t>(lhs32.umin64 / rhs32.umax64);
                umax = static_cast<std::uint32_t>(lhs32.umax64 / rhs32.umin64);
            }
            break;
        case AluOp::Mod:
            if (rhs32.tnum.is_constant() && rhs32.tnum.value == 1) {
                tnum = Tnum::constant(0);
            } else if (lhs32.tnum.is_constant() &&
                       rhs32.tnum.is_constant() && rhs32.tnum.value != 0) {
                tnum = Tnum::constant(lhs32.tnum.value % rhs32.tnum.value);
            }
            if (rhs32.umin64 != 0) {
                umin = 0;
                umax = static_cast<std::uint32_t>(
                    std::min(lhs32.umax64, rhs32.umax64 - 1));
            }
            break;
        case AluOp::And:
            tnum = tnum_and(lhs32.tnum, rhs32.tnum);
            umin = 0;
            umax = static_cast<std::uint32_t>(
                std::min(lhs32.umax64, rhs32.umax64));
            break;
        case AluOp::Or:
            tnum = tnum_or(lhs32.tnum, rhs32.tnum);
            umin = static_cast<std::uint32_t>(
                std::max(lhs32.umin64, rhs32.umin64));
            umax = cover_mask32(static_cast<std::uint32_t>(lhs32.umax64)) |
                   cover_mask32(static_cast<std::uint32_t>(rhs32.umax64));
            break;
        case AluOp::Xor:
            tnum = tnum_xor(lhs32.tnum, rhs32.tnum);
            umin = 0;
            umax = cover_mask32(static_cast<std::uint32_t>(lhs32.umax64)) |
                   cover_mask32(static_cast<std::uint32_t>(rhs32.umax64));
            break;
        case AluOp::Lsh: {
            const auto shift = rhs32.constant_value();
            if (!shift || *shift >= 32) {
                if (lhs32.tnum.is_constant() && lhs32.tnum.value == 0) {
                    tnum = Tnum::constant(0);
                }
                break;
            }
            tnum = tnum_lshift32(lhs32.tnum, static_cast<unsigned>(*shift));
            if (lhs32.umax64 <= (kU32Max >> *shift)) {
                umin = static_cast<std::uint32_t>(lhs32.umin64 << *shift);
                umax = static_cast<std::uint32_t>(lhs32.umax64 << *shift);
            }
            break;
        }
        case AluOp::Rsh: {
            const auto shift = rhs32.constant_value();
            if (!shift || *shift >= 32) {
                if (lhs32.tnum.is_constant() && lhs32.tnum.value == 0) {
                    tnum = Tnum::constant(0);
                }
                break;
            }
            tnum = tnum_rshift32(lhs32.tnum, static_cast<unsigned>(*shift));
            umin = static_cast<std::uint32_t>(lhs32.umin64 >> *shift);
            umax = static_cast<std::uint32_t>(lhs32.umax64 >> *shift);
            break;
        }
        case AluOp::Arsh: {
            const auto shift = rhs32.constant_value();
            if (!shift || *shift >= 32) {
                break;
            }
            tnum = tnum_arshift32(lhs32.tnum, static_cast<unsigned>(*shift));
            smin = arshift32(lhs32.s32_min, static_cast<unsigned>(*shift));
            smax = arshift32(lhs32.s32_max, static_cast<unsigned>(*shift));
            const auto ranges = detail::unsigned_from_signed32(smin, smax);
            if (!ranges.empty()) {
                umin = static_cast<std::uint32_t>(ranges.front().lo);
                umax = static_cast<std::uint32_t>(ranges.back().hi);
            }
            break;
        }
        case AluOp::Neg:
            tnum = tnum_sub(Tnum::constant(0), lhs32.tnum);
            if (lhs32.umax64 == 0) {
                umin = umax = 0;
            }
            if (lhs32.s32_min != kI32Min) {
                smin = static_cast<std::int32_t>(-lhs32.s32_max);
                smax = static_cast<std::int32_t>(-lhs32.s32_min);
            }
            break;
        case AluOp::Mov:
            return lhs32;
    }
    return make32(tnum, umin, umax, smin, smax);
}

}  // namespace

Scalar alu64(AluOp op, const Scalar& lhs, const Scalar& rhs) noexcept {
    return alu64_impl(op, lhs, rhs);
}

Scalar alu32(AluOp op, const Scalar& lhs, const Scalar& rhs) noexcept {
    return alu32_impl(op, lhs, rhs);
}

Scalar alu(AluOp op, const Scalar& lhs, const Scalar& rhs,
           bool is32) noexcept {
    return is32 ? alu32(op, lhs, rhs) : alu64(op, lhs, rhs);
}

Scalar neg64(const Scalar& value) noexcept {
    return alu64(AluOp::Neg, value, Scalar::constant(0));
}

Scalar neg32(const Scalar& value) noexcept {
    return alu32(AluOp::Neg, value, Scalar::constant(0));
}

}  // namespace ebpf
