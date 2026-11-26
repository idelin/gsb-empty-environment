#include "ebpf/verifier.hpp"

#include "domain_internal.hpp"

#include <algorithm>
#include <cstdint>
#include <limits>

namespace ebpf {
namespace {

[[nodiscard]] bool same(detail::URange range, std::uint64_t lo,
                        std::uint64_t hi) {
    return range.lo == lo && range.hi == hi;
}

void mark_empty(Scalar& scalar) noexcept {
    scalar.empty = true;
}

}  // namespace

Scalar Scalar::constant(std::uint64_t value) noexcept {
    Scalar out;
    out.tnum = Tnum::constant(value);
    out.umin64 = value;
    out.umax64 = value;
    out.smin64 = static_cast<std::int64_t>(value);
    out.smax64 = static_cast<std::int64_t>(value);
    out.u32_min = static_cast<std::uint32_t>(value);
    out.u32_max = out.u32_min;
    out.s32_min = static_cast<std::int32_t>(out.u32_min);
    out.s32_max = out.s32_min;
    return out;
}

Scalar Scalar::from_tnum(Tnum value) noexcept {
    Scalar out;
    out.tnum = value;
    out.umin64 = value.umin();
    out.umax64 = value.umax();
    out.smin64 = value.smin();
    out.smax64 = value.smax();
    out.u32_min = value.u32_min();
    out.u32_max = value.u32_max();
    out.s32_min = value.s32_min();
    out.s32_max = value.s32_max();
    out.normalize();
    return out;
}

Scalar Scalar::from_unsigned_range(std::uint64_t lo,
                                   std::uint64_t hi) noexcept {
    Scalar out;
    if (lo > hi) {
        out.empty = true;
        return out;
    }
    out.tnum = Tnum::from_unsigned_range(lo, hi);
    out.umin64 = lo;
    out.umax64 = hi;
    const auto signed_range =
        detail::signed_hull_from_unsigned64(detail::URange{lo, hi});
    out.smin64 = signed_range.lo;
    out.smax64 = signed_range.hi;
    out.normalize();
    return out;
}

Scalar Scalar::from_signed_range(std::int64_t lo, std::int64_t hi) noexcept {
    Scalar out;
    if (lo > hi) {
        out.empty = true;
        return out;
    }
    out.smin64 = lo;
    out.smax64 = hi;
    const auto unsigned_ranges = detail::unsigned_from_signed64(lo, hi);
    if (unsigned_ranges.empty()) {
        out.empty = true;
        return out;
    }
    out.umin64 = unsigned_ranges.front().lo;
    out.umax64 = unsigned_ranges.back().hi;
    if (lo >= 0 || hi < 0) {
        out.tnum =
            Tnum::from_unsigned_range(static_cast<std::uint64_t>(lo),
                                      static_cast<std::uint64_t>(hi));
    }
    out.normalize();
    return out;
}

bool Scalar::contains(std::uint64_t value) const noexcept {
    if (empty) {
        return false;
    }
    if (!tnum.contains(value)) {
        return false;
    }
    if (value < umin64 || value > umax64) {
        return false;
    }
    const auto signed_value = static_cast<std::int64_t>(value);
    if (signed_value < smin64 || signed_value > smax64) {
        return false;
    }
    const auto low = static_cast<std::uint32_t>(value);
    if (low < u32_min || low > u32_max) {
        return false;
    }
    const auto signed_low = static_cast<std::int32_t>(low);
    return signed_low >= s32_min && signed_low <= s32_max;
}

std::optional<std::uint64_t> Scalar::constant_value() const noexcept {
    if (empty) {
        return std::nullopt;
    }
    if (tnum.is_constant() && contains(tnum.value)) {
        return tnum.value;
    }
    if (umin64 == umax64 && contains(umin64)) {
        return umin64;
    }
    return std::nullopt;
}

bool Scalar::is_pointer_safe_nonnegative() const noexcept {
    return !empty && smin64 >= 0;
}

bool Scalar::has_value() const noexcept {
    Scalar copy = *this;
    copy.normalize();
    return !copy.empty;
}

void Scalar::normalize() noexcept {
    if (empty) {
        return;
    }

    for (unsigned iteration = 0; iteration < 64; ++iteration) {
        bool changed = false;
        if (umin64 > umax64 || smin64 > smax64 || u32_min > u32_max ||
            s32_min > s32_max) {
            mark_empty(*this);
            return;
        }

        const std::uint64_t tnum_umin = tnum.umin();
        const std::uint64_t tnum_umax = tnum.umax();
        if (tnum_umin > umin64) {
            umin64 = tnum_umin;
            changed = true;
        }
        if (tnum_umax < umax64) {
            umax64 = tnum_umax;
            changed = true;
        }
        if (tnum.smin() > smin64) {
            smin64 = tnum.smin();
            changed = true;
        }
        if (tnum.smax() < smax64) {
            smax64 = tnum.smax();
            changed = true;
        }

        const auto signed_projection =
            detail::unsigned_hull_from_signed64({umin64, umax64}, smin64,
                                                smax64);
        if (!signed_projection.has_value()) {
            mark_empty(*this);
            return;
        }
        if (!same(*signed_projection, umin64, umax64)) {
            umin64 = signed_projection->lo;
            umax64 = signed_projection->hi;
            changed = true;
        }

        const auto unsigned_signed_hull =
            detail::signed_hull_from_unsigned64({umin64, umax64});
        if (unsigned_signed_hull.lo > smin64) {
            smin64 = unsigned_signed_hull.lo;
            changed = true;
        }
        if (unsigned_signed_hull.hi < smax64) {
            smax64 = unsigned_signed_hull.hi;
            changed = true;
        }

        const auto low_projection = detail::low32_projection(umin64, umax64);
        if (low_projection.lo > u32_min) {
            u32_min = static_cast<std::uint32_t>(low_projection.lo);
            changed = true;
        }
        if (low_projection.hi < u32_max) {
            u32_max = static_cast<std::uint32_t>(low_projection.hi);
            changed = true;
        }
        if (u32_min > u32_max) {
            mark_empty(*this);
            return;
        }

        const auto low_signed_projection =
            detail::unsigned32_hull_from_signed32({u32_min, u32_max}, s32_min,
                                                  s32_max);
        if (!low_signed_projection.has_value()) {
            mark_empty(*this);
            return;
        }
        if (low_signed_projection->lo > u32_min) {
            u32_min = static_cast<std::uint32_t>(low_signed_projection->lo);
            changed = true;
        }
        if (low_signed_projection->hi < u32_max) {
            u32_max = static_cast<std::uint32_t>(low_signed_projection->hi);
            changed = true;
        }

        const auto low_unsigned_hull =
            detail::signed_hull_from_unsigned32({u32_min, u32_max});
        if (low_unsigned_hull.lo > s32_min) {
            s32_min = low_unsigned_hull.lo;
            changed = true;
        }
        if (low_unsigned_hull.hi < s32_max) {
            s32_max = low_unsigned_hull.hi;
            changed = true;
        }

        if (tnum.u32_min() > u32_min) {
            u32_min = tnum.u32_min();
            changed = true;
        }
        if (tnum.u32_max() < u32_max) {
            u32_max = tnum.u32_max();
            changed = true;
        }
        if (tnum.s32_min() > s32_min) {
            s32_min = tnum.s32_min();
            changed = true;
        }
        if (tnum.s32_max() < s32_max) {
            s32_max = tnum.s32_max();
            changed = true;
        }

        if (u32_min > u32_max || s32_min > s32_max) {
            mark_empty(*this);
            return;
        }

        // If every upper 32-bit bit is known, the value is base | low32.
        if ((tnum.mask >> 32) == 0) {
            const std::uint64_t base = tnum.value & ~detail::kU32Mask;
            const std::uint64_t lower = base | u32_min;
            const std::uint64_t upper = base | u32_max;
            if (lower > umin64) {
                umin64 = lower;
                changed = true;
            }
            if (upper < umax64) {
                umax64 = upper;
                changed = true;
            }
        }

        if (umax64 <= static_cast<std::uint64_t>(kU32Max)) {
            if (umin64 > u32_min) {
                u32_min = static_cast<std::uint32_t>(umin64);
                changed = true;
            }
            if (umax64 < u32_max) {
                u32_max = static_cast<std::uint32_t>(umax64);
                changed = true;
            }
        }

        if (!detail::refine_tnum_from_range(tnum, umin64, umax64, 64)) {
            mark_empty(*this);
            return;
        }
        if (!detail::refine_tnum_from_range(tnum, u32_min, u32_max, 32)) {
            mark_empty(*this);
            return;
        }

        if (!changed) {
            break;
        }
    }

    if (!detail::has_value(*this)) {
        mark_empty(*this);
    }
}

Scalar cast32(const Scalar& value) noexcept {
    Scalar out;
    if (value.empty) {
        out.empty = true;
        return out;
    }
    out.tnum = tnum_cast32(value.tnum);
    const auto projection =
        detail::low32_projection(value.umin64, value.umax64);
    const auto ranges = detail::intersect_ranges(
        projection,
        std::vector<detail::URange>{
            {value.u32_min, value.u32_max}});
    if (ranges.empty()) {
        out.empty = true;
        return out;
    }
    out.u32_min = static_cast<std::uint32_t>(ranges.front().lo);
    out.u32_max = static_cast<std::uint32_t>(ranges.back().hi);
    out.s32_min = value.s32_min;
    out.s32_max = value.s32_max;
    out.umin64 = out.u32_min;
    out.umax64 = out.u32_max;
    out.smin64 = static_cast<std::int64_t>(out.umin64);
    out.smax64 = static_cast<std::int64_t>(out.umax64);
    out.normalize();
    return out;
}

Scalar join_scalar(const Scalar& lhs, const Scalar& rhs) noexcept {
    if (lhs.empty) {
        return rhs;
    }
    if (rhs.empty) {
        return lhs;
    }
    Scalar out;
    out.tnum = tnum_join(lhs.tnum, rhs.tnum);
    out.umin64 = std::min(lhs.umin64, rhs.umin64);
    out.umax64 = std::max(lhs.umax64, rhs.umax64);
    out.smin64 = std::min(lhs.smin64, rhs.smin64);
    out.smax64 = std::max(lhs.smax64, rhs.smax64);
    out.u32_min = std::min(lhs.u32_min, rhs.u32_min);
    out.u32_max = std::max(lhs.u32_max, rhs.u32_max);
    out.s32_min = std::min(lhs.s32_min, rhs.s32_min);
    out.s32_max = std::max(lhs.s32_max, rhs.s32_max);
    out.normalize();
    return out;
}

}  // namespace ebpf
