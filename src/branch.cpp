#include "ebpf/verifier.hpp"

#include "domain_internal.hpp"

#include <algorithm>
#include <cstdint>
#include <limits>
#include <optional>

namespace ebpf {
namespace {

constexpr std::uint64_t kU32Mask = static_cast<std::uint64_t>(kU32Max);

[[nodiscard]] Scalar dead_scalar() noexcept {
    Scalar scalar;
    scalar.empty = true;
    return scalar;
}

void mark_dead(Scalar& lhs, Scalar& rhs) noexcept {
    lhs = dead_scalar();
    rhs = dead_scalar();
}

[[nodiscard]] std::optional<Scalar> exclude_constant64(const Scalar& input,
                                                       std::uint64_t value) {
    Scalar out = input.normalized();
    if (out.empty) {
        return std::nullopt;
    }
    const auto only = out.constant_value();
    if (only.has_value() && *only == value) {
        return std::nullopt;
    }
    if (out.umin64 == value && out.umin64 < out.umax64) {
        ++out.umin64;
    } else if (out.umax64 == value && out.umin64 < out.umax64) {
        --out.umax64;
    }
    out.normalize();
    if (out.empty) {
        return std::nullopt;
    }
    return out;
}

[[nodiscard]] std::optional<Scalar> exclude_constant32(const Scalar& input,
                                                       std::uint32_t value) {
    Scalar out = input.normalized();
    if (out.empty) {
        return std::nullopt;
    }
    if (out.u32_min == out.u32_max && out.u32_min == value) {
        return std::nullopt;
    }
    if ((out.tnum.mask & kU32Mask) == 0 &&
        static_cast<std::uint32_t>(out.tnum.value) == value) {
        return std::nullopt;
    }
    if (out.u32_min == value && out.u32_min < out.u32_max) {
        ++out.u32_min;
    } else if (out.u32_max == value && out.u32_min < out.u32_max) {
        --out.u32_max;
    }
    out.normalize();
    if (out.empty) {
        return std::nullopt;
    }
    return out;
}

[[nodiscard]] std::optional<Scalar> refine_unsigned64(const Scalar& input,
                                                      CompareOp op,
                                                      std::uint64_t value,
                                                      bool truth) {
    Scalar out = input.normalized();
    if (out.empty) {
        return std::nullopt;
    }
    switch (op) {
        case CompareOp::Eq:
            if (truth) {
                if (!out.contains(value)) {
                    return std::nullopt;
                }
                return Scalar::constant(value);
            }
            return exclude_constant64(out, value);
        case CompareOp::Ne:
            if (!truth) {
                if (!out.contains(value)) {
                    return std::nullopt;
                }
                return Scalar::constant(value);
            }
            return exclude_constant64(out, value);
        case CompareOp::Lt:
            if (truth) {
                if (value == 0) {
                    return std::nullopt;
                }
                out.umax64 = std::min(out.umax64, value - 1);
            } else {
                out.umin64 = std::max(out.umin64, value);
            }
            break;
        case CompareOp::Le:
            if (truth) {
                out.umax64 = std::min(out.umax64, value);
            } else {
                if (value == kU64Max) {
                    return std::nullopt;
                }
                out.umin64 = std::max(out.umin64, value + 1);
            }
            break;
        case CompareOp::Gt:
            if (truth) {
                if (value == kU64Max) {
                    return std::nullopt;
                }
                out.umin64 = std::max(out.umin64, value + 1);
            } else {
                out.umax64 = std::min(out.umax64, value);
            }
            break;
        case CompareOp::Ge:
            if (truth) {
                out.umin64 = std::max(out.umin64, value);
            } else {
                if (value == 0) {
                    return std::nullopt;
                }
                out.umax64 = std::min(out.umax64, value - 1);
            }
            break;
        default:
            break;
    }
    out.normalize();
    if (out.empty) {
        return std::nullopt;
    }
    return out;
}

[[nodiscard]] std::optional<Scalar> refine_signed64(const Scalar& input,
                                                    CompareOp op,
                                                    std::int64_t value,
                                                    bool truth) {
    Scalar out = input.normalized();
    if (out.empty) {
        return std::nullopt;
    }
    switch (op) {
        case CompareOp::Slt:
            if (truth) {
                if (value == kI64Min) {
                    return std::nullopt;
                }
                out.smax64 = std::min(out.smax64, value - 1);
            } else {
                out.smin64 = std::max(out.smin64, value);
            }
            break;
        case CompareOp::Sle:
            if (truth) {
                out.smax64 = std::min(out.smax64, value);
            } else {
                if (value == kI64Max) {
                    return std::nullopt;
                }
                out.smin64 = std::max(out.smin64, value + 1);
            }
            break;
        case CompareOp::Sgt:
            if (truth) {
                if (value == kI64Max) {
                    return std::nullopt;
                }
                out.smin64 = std::max(out.smin64, value + 1);
            } else {
                out.smax64 = std::min(out.smax64, value);
            }
            break;
        case CompareOp::Sge:
            if (truth) {
                out.smin64 = std::max(out.smin64, value);
            } else {
                if (value == kI64Min) {
                    return std::nullopt;
                }
                out.smax64 = std::min(out.smax64, value - 1);
            }
            break;
        default:
            break;
    }
    out.normalize();
    if (out.empty) {
        return std::nullopt;
    }
    return out;
}

[[nodiscard]] std::optional<Scalar> refine_unsigned32(const Scalar& input,
                                                      CompareOp op,
                                                      std::uint32_t value,
                                                      bool truth) {
    Scalar out = input.normalized();
    if (out.empty) {
        return std::nullopt;
    }
    switch (op) {
        case CompareOp::Eq:
            if (truth) {
                out.u32_min = value;
                out.u32_max = value;
                out.normalize();
                if (out.empty) {
                    return std::nullopt;
                }
                return out;
            }
            return exclude_constant32(out, value);
        case CompareOp::Ne:
            if (!truth) {
                out.u32_min = value;
                out.u32_max = value;
                out.normalize();
                if (out.empty) {
                    return std::nullopt;
                }
                return out;
            }
            return exclude_constant32(out, value);
        case CompareOp::Lt:
            if (truth) {
                if (value == 0) {
                    return std::nullopt;
                }
                out.u32_max = std::min(out.u32_max, value - 1);
            } else {
                out.u32_min = std::max(out.u32_min, value);
            }
            break;
        case CompareOp::Le:
            if (truth) {
                out.u32_max = std::min(out.u32_max, value);
            } else {
                if (value == kU32Max) {
                    return std::nullopt;
                }
                out.u32_min = std::max(out.u32_min, value + 1);
            }
            break;
        case CompareOp::Gt:
            if (truth) {
                if (value == kU32Max) {
                    return std::nullopt;
                }
                out.u32_min = std::max(out.u32_min, value + 1);
            } else {
                out.u32_max = std::min(out.u32_max, value);
            }
            break;
        case CompareOp::Ge:
            if (truth) {
                out.u32_min = std::max(out.u32_min, value);
            } else {
                if (value == 0) {
                    return std::nullopt;
                }
                out.u32_max = std::min(out.u32_max, value - 1);
            }
            break;
        default:
            break;
    }
    out.normalize();
    if (out.empty) {
        return std::nullopt;
    }
    return out;
}

[[nodiscard]] std::optional<Scalar> refine_signed32(const Scalar& input,
                                                    CompareOp op,
                                                    std::int32_t value,
                                                    bool truth) {
    Scalar out = input.normalized();
    if (out.empty) {
        return std::nullopt;
    }
    switch (op) {
        case CompareOp::Slt:
            if (truth) {
                if (value == kI32Min) {
                    return std::nullopt;
                }
                out.s32_max = std::min(out.s32_max, value - 1);
            } else {
                out.s32_min = std::max(out.s32_min, value);
            }
            break;
        case CompareOp::Sle:
            if (truth) {
                out.s32_max = std::min(out.s32_max, value);
            } else {
                if (value == kI32Max) {
                    return std::nullopt;
                }
                out.s32_min = std::max(out.s32_min, value + 1);
            }
            break;
        case CompareOp::Sgt:
            if (truth) {
                if (value == kI32Max) {
                    return std::nullopt;
                }
                out.s32_min = std::max(out.s32_min, value + 1);
            } else {
                out.s32_max = std::min(out.s32_max, value);
            }
            break;
        case CompareOp::Sge:
            if (truth) {
                out.s32_min = std::max(out.s32_min, value);
            } else {
                if (value == kI32Min) {
                    return std::nullopt;
                }
                out.s32_max = std::min(out.s32_max, value - 1);
            }
            break;
        default:
            break;
    }
    out.normalize();
    if (out.empty) {
        return std::nullopt;
    }
    return out;
}

void apply_lt_u64(Scalar& x, Scalar& y, bool truth) noexcept {
    if (truth) {
        if (y.umax64 == 0) {
            mark_dead(x, y);
            return;
        }
        x.umax64 = std::min(x.umax64, y.umax64 - 1);
        if (x.umin64 == kU64Max) {
            mark_dead(x, y);
            return;
        }
        y.umin64 = std::max(y.umin64, x.umin64 + 1);
    } else {
        x.umin64 = std::max(x.umin64, y.umin64);
        y.umax64 = std::min(y.umax64, x.umax64);
    }
    x.normalize();
    y.normalize();
    if (x.empty || y.empty) {
        mark_dead(x, y);
    }
}

void apply_le_u64(Scalar& x, Scalar& y, bool truth) noexcept {
    if (truth) {
        x.umax64 = std::min(x.umax64, y.umax64);
        y.umin64 = std::max(y.umin64, x.umin64);
    } else {
        if (y.umin64 == kU64Max || x.umax64 == 0) {
            mark_dead(x, y);
            return;
        }
        x.umin64 = std::max(x.umin64, y.umin64 + 1);
        y.umax64 = std::min(y.umax64, x.umax64 - 1);
    }
    x.normalize();
    y.normalize();
    if (x.empty || y.empty) {
        mark_dead(x, y);
    }
}

void apply_lt_s64(Scalar& x, Scalar& y, bool truth) noexcept {
    if (truth) {
        if (y.smax64 == kI64Min) {
            mark_dead(x, y);
            return;
        }
        x.smax64 = std::min(x.smax64, y.smax64 - 1);
        if (x.smin64 == kI64Max) {
            mark_dead(x, y);
            return;
        }
        y.smin64 = std::max(y.smin64, x.smin64 + 1);
    } else {
        x.smin64 = std::max(x.smin64, y.smin64);
        y.smax64 = std::min(y.smax64, x.smax64);
    }
    x.normalize();
    y.normalize();
    if (x.empty || y.empty) {
        mark_dead(x, y);
    }
}

void apply_le_s64(Scalar& x, Scalar& y, bool truth) noexcept {
    if (truth) {
        x.smax64 = std::min(x.smax64, y.smax64);
        y.smin64 = std::max(y.smin64, x.smin64);
    } else {
        if (y.smin64 == kI64Max || x.smax64 == kI64Min) {
            mark_dead(x, y);
            return;
        }
        x.smin64 = std::max(x.smin64, y.smin64 + 1);
        y.smax64 = std::min(y.smax64, x.smax64 - 1);
    }
    x.normalize();
    y.normalize();
    if (x.empty || y.empty) {
        mark_dead(x, y);
    }
}

void apply_lt_u32(Scalar& x, Scalar& y, bool truth) noexcept {
    if (truth) {
        if (y.u32_max == 0) {
            mark_dead(x, y);
            return;
        }
        x.u32_max = std::min(x.u32_max, static_cast<std::uint32_t>(y.u32_max - 1));
        if (x.u32_min == kU32Max) {
            mark_dead(x, y);
            return;
        }
        y.u32_min =
            std::max(y.u32_min, static_cast<std::uint32_t>(x.u32_min + 1));
    } else {
        x.u32_min = std::max(x.u32_min, y.u32_min);
        y.u32_max = std::min(y.u32_max, x.u32_max);
    }
    x.normalize();
    y.normalize();
    if (x.empty || y.empty) {
        mark_dead(x, y);
    }
}

void apply_le_u32(Scalar& x, Scalar& y, bool truth) noexcept {
    if (truth) {
        x.u32_max = std::min(x.u32_max, y.u32_max);
        y.u32_min = std::max(y.u32_min, x.u32_min);
    } else {
        if (y.u32_min == kU32Max || x.u32_max == 0) {
            mark_dead(x, y);
            return;
        }
        x.u32_min =
            std::max(x.u32_min, static_cast<std::uint32_t>(y.u32_min + 1));
        y.u32_max =
            std::min(y.u32_max, static_cast<std::uint32_t>(x.u32_max - 1));
    }
    x.normalize();
    y.normalize();
    if (x.empty || y.empty) {
        mark_dead(x, y);
    }
}

void apply_lt_s32(Scalar& x, Scalar& y, bool truth) noexcept {
    if (truth) {
        if (y.s32_max == kI32Min) {
            mark_dead(x, y);
            return;
        }
        x.s32_max = std::min(x.s32_max, static_cast<std::int32_t>(y.s32_max - 1));
        if (x.s32_min == kI32Max) {
            mark_dead(x, y);
            return;
        }
        y.s32_min =
            std::max(y.s32_min, static_cast<std::int32_t>(x.s32_min + 1));
    } else {
        x.s32_min = std::max(x.s32_min, y.s32_min);
        y.s32_max = std::min(y.s32_max, x.s32_max);
    }
    x.normalize();
    y.normalize();
    if (x.empty || y.empty) {
        mark_dead(x, y);
    }
}

void apply_le_s32(Scalar& x, Scalar& y, bool truth) noexcept {
    if (truth) {
        x.s32_max = std::min(x.s32_max, y.s32_max);
        y.s32_min = std::max(y.s32_min, x.s32_min);
    } else {
        if (y.s32_min == kI32Max || x.s32_max == kI32Min) {
            mark_dead(x, y);
            return;
        }
        x.s32_min =
            std::max(x.s32_min, static_cast<std::int32_t>(y.s32_min + 1));
        y.s32_max =
            std::min(y.s32_max, static_cast<std::int32_t>(x.s32_max - 1));
    }
    x.normalize();
    y.normalize();
    if (x.empty || y.empty) {
        mark_dead(x, y);
    }
}

void apply_eq64(Scalar& lhs, Scalar& rhs) noexcept {
    const auto lhs_const = lhs.constant_value();
    if (lhs_const.has_value()) {
        if (!rhs.contains(*lhs_const)) {
            mark_dead(lhs, rhs);
            return;
        }
        lhs = Scalar::constant(*lhs_const);
        rhs = Scalar::constant(*lhs_const);
        return;
    }
    const auto rhs_const = rhs.constant_value();
    if (rhs_const.has_value()) {
        if (!lhs.contains(*rhs_const)) {
            mark_dead(lhs, rhs);
            return;
        }
        lhs = Scalar::constant(*rhs_const);
        rhs = Scalar::constant(*rhs_const);
        return;
    }

    const std::uint64_t ulo = std::max(lhs.umin64, rhs.umin64);
    const std::uint64_t uhi = std::min(lhs.umax64, rhs.umax64);
    const std::int64_t slo = std::max(lhs.smin64, rhs.smin64);
    const std::int64_t shi = std::min(lhs.smax64, rhs.smax64);
    const std::uint32_t llo = std::max(lhs.u32_min, rhs.u32_min);
    const std::uint32_t lhi = std::min(lhs.u32_max, rhs.u32_max);
    const std::int32_t lslo = std::max(lhs.s32_min, rhs.s32_min);
    const std::int32_t lshi = std::min(lhs.s32_max, rhs.s32_max);
    if (ulo > uhi || slo > shi || llo > lhi || lslo > lshi) {
        mark_dead(lhs, rhs);
        return;
    }
    lhs.umin64 = rhs.umin64 = ulo;
    lhs.umax64 = rhs.umax64 = uhi;
    lhs.smin64 = rhs.smin64 = slo;
    lhs.smax64 = rhs.smax64 = shi;
    lhs.u32_min = rhs.u32_min = llo;
    lhs.u32_max = rhs.u32_max = lhi;
    lhs.s32_min = rhs.s32_min = lslo;
    lhs.s32_max = rhs.s32_max = lshi;
    lhs.normalize();
    rhs.normalize();
    if (lhs.empty || rhs.empty) {
        mark_dead(lhs, rhs);
    }
}

void apply_eq32(Scalar& lhs, Scalar& rhs) noexcept {
    if (lhs.u32_min == lhs.u32_max && !lhs.empty) {
        const std::uint32_t value = lhs.u32_min;
        rhs.u32_min = rhs.u32_max = value;
        rhs.normalize();
        if (rhs.empty) {
            mark_dead(lhs, rhs);
        }
        return;
    }
    if (rhs.u32_min == rhs.u32_max && !rhs.empty) {
        const std::uint32_t value = rhs.u32_min;
        lhs.u32_min = lhs.u32_max = value;
        lhs.normalize();
        if (lhs.empty) {
            mark_dead(lhs, rhs);
        }
        return;
    }
    const std::uint32_t lo = std::max(lhs.u32_min, rhs.u32_min);
    const std::uint32_t hi = std::min(lhs.u32_max, rhs.u32_max);
    const std::int32_t slo = std::max(lhs.s32_min, rhs.s32_min);
    const std::int32_t shi = std::min(lhs.s32_max, rhs.s32_max);
    if (lo > hi || slo > shi) {
        mark_dead(lhs, rhs);
        return;
    }
    lhs.u32_min = rhs.u32_min = lo;
    lhs.u32_max = rhs.u32_max = hi;
    lhs.s32_min = rhs.s32_min = slo;
    lhs.s32_max = rhs.s32_max = shi;
    lhs.normalize();
    rhs.normalize();
    if (lhs.empty || rhs.empty) {
        mark_dead(lhs, rhs);
    }
}

void apply_relation64(CompareOp op, Scalar& lhs, Scalar& rhs,
                      bool truth) noexcept {
    for (int iteration = 0; iteration < 4; ++iteration) {
        const auto lhs_before = lhs;
        const auto rhs_before = rhs;
        switch (op) {
            case CompareOp::Lt:
                apply_lt_u64(lhs, rhs, truth);
                break;
            case CompareOp::Le:
                apply_le_u64(lhs, rhs, truth);
                break;
            case CompareOp::Gt:
                apply_lt_u64(rhs, lhs, truth);
                break;
            case CompareOp::Ge:
                apply_le_u64(rhs, lhs, truth);
                break;
            case CompareOp::Slt:
                apply_lt_s64(lhs, rhs, truth);
                break;
            case CompareOp::Sle:
                apply_le_s64(lhs, rhs, truth);
                break;
            case CompareOp::Sgt:
                apply_lt_s64(rhs, lhs, truth);
                break;
            case CompareOp::Sge:
                apply_le_s64(rhs, lhs, truth);
                break;
            default:
                break;
        }
        if (lhs.empty || rhs.empty) {
            mark_dead(lhs, rhs);
            return;
        }
        if (lhs.umin64 == lhs_before.umin64 &&
            lhs.umax64 == lhs_before.umax64 &&
            rhs.umin64 == rhs_before.umin64 &&
            rhs.umax64 == rhs_before.umax64 &&
            lhs.smin64 == lhs_before.smin64 &&
            lhs.smax64 == lhs_before.smax64 &&
            rhs.smin64 == rhs_before.smin64 &&
            rhs.smax64 == rhs_before.smax64 &&
            lhs.u32_min == lhs_before.u32_min &&
            lhs.u32_max == lhs_before.u32_max &&
            rhs.u32_min == rhs_before.u32_min &&
            rhs.u32_max == rhs_before.u32_max &&
            lhs.s32_min == lhs_before.s32_min &&
            lhs.s32_max == lhs_before.s32_max &&
            rhs.s32_min == rhs_before.s32_min &&
            rhs.s32_max == rhs_before.s32_max) {
            break;
        }
    }
}

void apply_relation32(CompareOp op, Scalar& lhs, Scalar& rhs,
                      bool truth) noexcept {
    for (int iteration = 0; iteration < 4; ++iteration) {
        const auto lhs_before = lhs;
        const auto rhs_before = rhs;
        switch (op) {
            case CompareOp::Lt:
                apply_lt_u32(lhs, rhs, truth);
                break;
            case CompareOp::Le:
                apply_le_u32(lhs, rhs, truth);
                break;
            case CompareOp::Gt:
                apply_lt_u32(rhs, lhs, truth);
                break;
            case CompareOp::Ge:
                apply_le_u32(rhs, lhs, truth);
                break;
            case CompareOp::Slt:
                apply_lt_s32(lhs, rhs, truth);
                break;
            case CompareOp::Sle:
                apply_le_s32(lhs, rhs, truth);
                break;
            case CompareOp::Sgt:
                apply_lt_s32(rhs, lhs, truth);
                break;
            case CompareOp::Sge:
                apply_le_s32(rhs, lhs, truth);
                break;
            default:
                break;
        }
        if (lhs.empty || rhs.empty) {
            mark_dead(lhs, rhs);
            return;
        }
        if (lhs.u32_min == lhs_before.u32_min &&
            lhs.u32_max == lhs_before.u32_max &&
            rhs.u32_min == rhs_before.u32_min &&
            rhs.u32_max == rhs_before.u32_max &&
            lhs.s32_min == lhs_before.s32_min &&
            lhs.s32_max == lhs_before.s32_max &&
            rhs.s32_min == rhs_before.s32_min &&
            rhs.s32_max == rhs_before.s32_max) {
            break;
        }
    }
}

void apply_ne64(Scalar& lhs, Scalar& rhs) noexcept {
    const auto lhs_const = lhs.constant_value();
    if (lhs_const.has_value()) {
        const auto refined = exclude_constant64(rhs, *lhs_const);
        if (!refined.has_value()) {
            mark_dead(lhs, rhs);
            return;
        }
        rhs = *refined;
        return;
    }
    const auto rhs_const = rhs.constant_value();
    if (rhs_const.has_value()) {
        const auto refined = exclude_constant64(lhs, *rhs_const);
        if (!refined.has_value()) {
            mark_dead(lhs, rhs);
            return;
        }
        lhs = *refined;
    }
}

void apply_ne32(Scalar& lhs, Scalar& rhs) noexcept {
    if (lhs.u32_min == lhs.u32_max) {
        const auto refined = exclude_constant32(rhs, lhs.u32_min);
        if (!refined.has_value()) {
            mark_dead(lhs, rhs);
            return;
        }
        rhs = *refined;
        return;
    }
    if (rhs.u32_min == rhs.u32_max) {
        const auto refined = exclude_constant32(lhs, rhs.u32_min);
        if (!refined.has_value()) {
            mark_dead(lhs, rhs);
            return;
        }
        lhs = *refined;
    }
}

}  // namespace

std::optional<Scalar> refine_constant(const Scalar& lhs, CompareOp op,
                                      std::uint64_t rhs, bool truth,
                                      bool is32) noexcept {
    if (is32) {
        const auto value = static_cast<std::uint32_t>(rhs);
        switch (op) {
            case CompareOp::Eq:
            case CompareOp::Ne:
            case CompareOp::Lt:
            case CompareOp::Le:
            case CompareOp::Gt:
            case CompareOp::Ge:
                return refine_unsigned32(lhs, op, value, truth);
            case CompareOp::Slt:
            case CompareOp::Sle:
            case CompareOp::Sgt:
            case CompareOp::Sge:
                return refine_signed32(lhs, op,
                                       static_cast<std::int32_t>(value), truth);
        }
    }
    switch (op) {
        case CompareOp::Eq:
        case CompareOp::Ne:
        case CompareOp::Lt:
        case CompareOp::Le:
        case CompareOp::Gt:
        case CompareOp::Ge:
            return refine_unsigned64(lhs, op, rhs, truth);
        case CompareOp::Slt:
        case CompareOp::Sle:
        case CompareOp::Sgt:
        case CompareOp::Sge:
            return refine_signed64(lhs, op, static_cast<std::int64_t>(rhs),
                                   truth);
    }
    return std::nullopt;
}

BranchResult refine_branch_constant(const Scalar& lhs, CompareOp op,
                                    std::uint64_t rhs, bool is32) noexcept {
    BranchResult result;
    const auto taken = refine_constant(lhs, op, rhs, true, is32);
    const auto fallthrough = refine_constant(lhs, op, rhs, false, is32);
    result.taken.reachable = taken.has_value();
    if (taken.has_value()) {
        result.taken.lhs = *taken;
        result.taken.rhs =
            Scalar::constant(is32 ? rhs & kU32Mask : rhs);
    }
    result.fallthrough.reachable = fallthrough.has_value();
    if (fallthrough.has_value()) {
        result.fallthrough.lhs = *fallthrough;
        result.fallthrough.rhs =
            Scalar::constant(is32 ? rhs & kU32Mask : rhs);
    }
    return result;
}

BranchResult refine_branch(const Scalar& lhs, const Scalar& rhs, CompareOp op,
                           bool is32) noexcept {
    BranchResult result;
    for (int side = 0; side < 2; ++side) {
        const bool truth = side == 0;
        Scalar left = lhs.normalized();
        Scalar right = rhs.normalized();
        if (left.empty || right.empty) {
            continue;
        }
        if (op == CompareOp::Eq || op == CompareOp::Ne) {
            const bool equality_truth =
                (op == CompareOp::Eq) ? truth : !truth;
            if (equality_truth) {
                if (is32) {
                    apply_eq32(left, right);
                } else {
                    apply_eq64(left, right);
                }
            } else {
                if (is32) {
                    apply_ne32(left, right);
                } else {
                    apply_ne64(left, right);
                }
            }
        } else if (is32) {
            apply_relation32(op, left, right, truth);
        } else {
            apply_relation64(op, left, right, truth);
        }
        auto& split = truth ? result.taken : result.fallthrough;
        split.reachable = !left.empty && !right.empty;
        split.lhs = left;
        split.rhs = right;
    }
    return result;
}

}  // namespace ebpf
