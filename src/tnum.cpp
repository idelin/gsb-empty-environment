#include "ebpf/verifier.hpp"

#include <bit>
#include <cstdint>

namespace ebpf {

Tnum Tnum::from_unsigned_range(std::uint64_t lo, std::uint64_t hi) noexcept {
    if (lo > hi) {
        return Tnum::full();
    }
    const std::uint64_t diff = lo ^ hi;
    if (diff == 0) {
        return Tnum::constant(lo);
    }
    const unsigned varying_bits = std::bit_width(diff);
    const std::uint64_t varying = kU64Max >> (64 - varying_bits);
    return Tnum::make(lo & ~varying, varying);
}

namespace {

[[nodiscard]] Tnum tnum_add_sub(Tnum a, Tnum b, bool subtract) noexcept {
    std::uint64_t value = 0;
    std::uint64_t mask = 0;
    bool carries[3] = {false, true, false};  // indexed as carry + 1
    for (unsigned bit = 0; bit < 64; ++bit) {
        bool next_carries[3] = {};
        bool output_zero = false;
        bool output_one = false;
        const bool a_free = ((a.mask >> bit) & 1) != 0;
        const bool b_free = ((b.mask >> bit) & 1) != 0;
        const unsigned a_known = static_cast<unsigned>((a.value >> bit) & 1);
        const unsigned b_known = static_cast<unsigned>((b.value >> bit) & 1);
        for (int carry = -1; carry <= 1; ++carry) {
            if (!carries[carry + 1]) {
                continue;
            }
            for (unsigned abit = 0; abit < 2; ++abit) {
                if (!a_free && abit != a_known) {
                    continue;
                }
                for (unsigned bbit = 0; bbit < 2; ++bbit) {
                    if (!b_free && bbit != b_known) {
                        continue;
                    }
                    const int sum =
                        subtract ? static_cast<int>(abit) -
                                       static_cast<int>(bbit) + carry
                                 : static_cast<int>(abit) +
                                       static_cast<int>(bbit) + carry;
                    const int output = ((sum % 2) + 2) % 2;
                    const int carry_out = sum >= 0 ? sum / 2 : -1;
                    if (output == 0) {
                        output_zero = true;
                    } else {
                        output_one = true;
                    }
                    next_carries[carry_out + 1] = true;
                }
            }
        }
        if (!output_zero && !output_one) {
            return Tnum::full();
        }
        if (output_zero && output_one) {
            mask |= std::uint64_t{1} << bit;
        } else if (output_one) {
            value |= std::uint64_t{1} << bit;
        }
        carries[0] = next_carries[0];
        carries[1] = next_carries[1];
        carries[2] = next_carries[2];
    }
    return Tnum::make(value, mask);
}

}  // namespace

Tnum tnum_add(Tnum a, Tnum b) noexcept {
    return tnum_add_sub(a, b, false);
}

Tnum tnum_sub(Tnum a, Tnum b) noexcept {
    return tnum_add_sub(a, b, true);
}

Tnum tnum_and(Tnum a, Tnum b) noexcept {
    const std::uint64_t alpha = a.value | a.mask;
    const std::uint64_t beta = b.value | b.mask;
    const std::uint64_t unknown = alpha & beta;
    return Tnum::make(a.value & b.value, unknown);
}

Tnum tnum_or(Tnum a, Tnum b) noexcept {
    const std::uint64_t alpha = a.value | a.mask;
    const std::uint64_t beta = b.value | b.mask;
    const std::uint64_t value = a.value | b.value;
    return Tnum::make(value, (alpha | beta) & ~value);
}

Tnum tnum_xor(Tnum a, Tnum b) noexcept {
    return Tnum::make(a.value ^ b.value, a.mask | b.mask);
}

Tnum tnum_lshift(Tnum a, unsigned shift) noexcept {
    if (shift >= 64) {
        return Tnum::full();
    }
    return Tnum::make(a.value << shift, a.mask << shift);
}

Tnum tnum_rshift(Tnum a, unsigned shift) noexcept {
    if (shift >= 64) {
        return Tnum::full();
    }
    return Tnum::make(a.value >> shift, a.mask >> shift);
}

Tnum tnum_arshift(Tnum a, unsigned shift) noexcept {
    if (shift == 0) {
        return a;
    }
    if (shift >= 64) {
        return Tnum::full();
    }
    std::uint64_t value = a.value >> shift;
    std::uint64_t mask = a.mask >> shift;
    const std::uint64_t fill = kU64Max << (64 - shift);
    const std::uint64_t sign = std::uint64_t{1} << 63;
    if ((a.mask & sign) != 0) {
        value &= ~fill;
        mask |= fill;
    } else if ((a.value & sign) != 0) {
        value |= fill;
        mask &= ~fill;
    } else {
        value &= ~fill;
        mask &= ~fill;
    }
    return Tnum::make(value, mask);
}

Tnum tnum_lshift32(Tnum a, unsigned shift) noexcept {
    if (shift >= 32) {
        return Tnum::full();
    }
    a = tnum_cast32(a);
    return Tnum::make((a.value << shift) & kU32Max,
                      (a.mask << shift) & kU32Max);
}

Tnum tnum_rshift32(Tnum a, unsigned shift) noexcept {
    if (shift >= 32) {
        return Tnum::full();
    }
    a = tnum_cast32(a);
    return Tnum::make(a.value >> shift, a.mask >> shift);
}

Tnum tnum_arshift32(Tnum a, unsigned shift) noexcept {
    if (shift == 0) {
        return tnum_cast32(a);
    }
    if (shift >= 32) {
        return Tnum::full();
    }
    a = tnum_cast32(a);
    std::uint64_t value = a.value >> shift;
    std::uint64_t mask = a.mask >> shift;
    const std::uint64_t fill =
        (std::uint64_t{1} << (32 - shift)) - 1;
    const std::uint64_t fill_mask = (fill << shift) & kU32Max;
    const std::uint64_t sign = std::uint64_t{1} << 31;
    if ((a.mask & sign) != 0) {
        value &= ~fill_mask;
        mask |= fill_mask;
    } else if ((a.value & sign) != 0) {
        value |= fill_mask;
        mask &= ~fill_mask;
    } else {
        value &= ~fill_mask;
        mask &= ~fill_mask;
    }
    return Tnum::make(value & static_cast<std::uint64_t>(kU32Max),
                      mask & static_cast<std::uint64_t>(kU32Max));
}

Tnum tnum_cast32(Tnum a) noexcept {
    return Tnum::make(a.value & static_cast<std::uint64_t>(kU32Max),
                      a.mask & static_cast<std::uint64_t>(kU32Max));
}

Tnum tnum_join(Tnum a, Tnum b) noexcept {
    const std::uint64_t same_known =
        ~(a.mask | b.mask) & ~(a.value ^ b.value);
    return Tnum::make(a.value & same_known, ~same_known);
}

}  // namespace ebpf
