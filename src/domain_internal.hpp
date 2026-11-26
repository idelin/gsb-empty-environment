#ifndef EBPF_VERIFIER_DOMAIN_INTERNAL_HPP
#define EBPF_VERIFIER_DOMAIN_INTERNAL_HPP

#include "ebpf/verifier.hpp"

#include <algorithm>
#include <bit>
#include <cstdint>
#include <optional>
#include <vector>

namespace ebpf::detail {

inline constexpr std::uint64_t kU32Mask = static_cast<std::uint64_t>(kU32Max);

struct URange {
    std::uint64_t lo = 0;
    std::uint64_t hi = 0;
};

[[nodiscard]] constexpr bool valid(URange range) noexcept {
    return range.lo <= range.hi;
}

[[nodiscard]] inline std::vector<URange> merge_ranges(
    std::vector<URange> ranges) {
    std::erase_if(ranges, [](URange range) { return !valid(range); });
    std::sort(ranges.begin(), ranges.end(),
              [](URange a, URange b) { return a.lo < b.lo; });
    std::vector<URange> out;
    for (URange range : ranges) {
        if (out.empty() || range.lo > out.back().hi) {
            out.push_back(range);
        } else {
            out.back().hi = std::max(out.back().hi, range.hi);
        }
    }
    return out;
}

[[nodiscard]] inline std::vector<URange> intersect_ranges(
    const std::vector<URange>& lhs, const std::vector<URange>& rhs) {
    std::vector<URange> out;
    std::size_t i = 0;
    std::size_t j = 0;
    while (i < lhs.size() && j < rhs.size()) {
        const auto lo = std::max(lhs[i].lo, rhs[j].lo);
        const auto hi = std::min(lhs[i].hi, rhs[j].hi);
        if (lo <= hi) {
            out.push_back({lo, hi});
        }
        if (lhs[i].hi < rhs[j].hi) {
            ++i;
        } else {
            ++j;
        }
    }
    return out;
}

[[nodiscard]] inline std::vector<URange> intersect_ranges(
    URange range, const std::vector<URange>& ranges) {
    return intersect_ranges(std::vector<URange>{range}, ranges);
}

[[nodiscard]] inline std::vector<URange> unsigned_from_signed64(
    std::int64_t lo, std::int64_t hi) {
    if (lo > hi) {
        return {};
    }
    if (lo >= 0 || hi < 0) {
        return {{static_cast<std::uint64_t>(lo), static_cast<std::uint64_t>(hi)}};
    }
    return merge_ranges({{0, static_cast<std::uint64_t>(hi)},
                         {static_cast<std::uint64_t>(lo), kU64Max}});
}

[[nodiscard]] inline std::vector<URange> unsigned_from_signed32(
    std::int32_t lo, std::int32_t hi) {
    if (lo > hi) {
        return {};
    }
    if (lo >= 0 || hi < 0) {
        return {{static_cast<std::uint32_t>(lo), static_cast<std::uint32_t>(hi)}};
    }
    return merge_ranges({{0, static_cast<std::uint32_t>(hi)},
                         {static_cast<std::uint32_t>(lo), kU32Max}});
}

[[nodiscard]] inline std::optional<URange> unsigned_hull_from_signed64(
    URange unsigned_range, std::int64_t lo, std::int64_t hi) {
    const auto ranges =
        intersect_ranges(unsigned_range, unsigned_from_signed64(lo, hi));
    if (ranges.empty()) {
        return std::nullopt;
    }
    return URange{ranges.front().lo, ranges.back().hi};
}

[[nodiscard]] inline std::optional<URange> unsigned32_hull_from_signed32(
    URange unsigned_range, std::int32_t lo, std::int32_t hi) {
    const auto ranges =
        intersect_ranges(unsigned_range, unsigned_from_signed32(lo, hi));
    if (ranges.empty()) {
        return std::nullopt;
    }
    return URange{ranges.front().lo, ranges.back().hi};
}

struct SignedRange64 {
    std::int64_t lo = kI64Min;
    std::int64_t hi = kI64Max;
};

struct SignedRange32 {
    std::int32_t lo = kI32Min;
    std::int32_t hi = kI32Max;
};

[[nodiscard]] inline SignedRange64 signed_hull_from_unsigned64(URange range) {
    if (range.hi <= static_cast<std::uint64_t>(kI64Max)) {
        return {static_cast<std::int64_t>(range.lo),
                static_cast<std::int64_t>(range.hi)};
    }
    if (range.lo > static_cast<std::uint64_t>(kI64Max)) {
        return {static_cast<std::int64_t>(range.lo),
                static_cast<std::int64_t>(range.hi)};
    }
    return {kI64Min, kI64Max};
}

[[nodiscard]] inline SignedRange32 signed_hull_from_unsigned32(URange range) {
    if (range.hi <= static_cast<std::uint32_t>(kI32Max)) {
        return {static_cast<std::int32_t>(range.lo),
                static_cast<std::int32_t>(range.hi)};
    }
    if (range.lo > static_cast<std::uint32_t>(kI32Max)) {
        return {static_cast<std::int32_t>(range.lo),
                static_cast<std::int32_t>(range.hi)};
    }
    return {kI32Min, kI32Max};
}

[[nodiscard]] inline URange low32_projection(std::uint64_t lo,
                                             std::uint64_t hi) {
    if (lo > hi) {
        return {1, 0};
    }
    if (hi - lo >= kU32Mask) {
        return {0, kU32Max};
    }
    if ((lo >> 32) == (hi >> 32)) {
        return {static_cast<std::uint32_t>(lo), static_cast<std::uint32_t>(hi)};
    }
    return {0, kU32Max};
}

[[nodiscard]] inline bool fix_tnum_bit(Tnum& tnum, unsigned bit,
                                       bool desired) {
    const std::uint64_t mask = std::uint64_t{1} << bit;
    if ((tnum.mask & mask) != 0) {
        tnum.mask &= ~mask;
        if (desired) {
            tnum.value |= mask;
        } else {
            tnum.value &= ~mask;
        }
        return true;
    }
    return ((tnum.value & mask) != 0) == desired;
}

// Fixes only the common prefix of [lo, hi]. Lower varying bits are left as the
// existing tnum has them; this is an intersection with a tnum superset.
[[nodiscard]] inline bool refine_tnum_from_range(Tnum& tnum, std::uint64_t lo,
                                                 std::uint64_t hi,
                                                 unsigned width) {
    const std::uint64_t width_mask =
        width == 64 ? kU64Max : ((std::uint64_t{1} << width) - 1);
    lo &= width_mask;
    hi &= width_mask;
    const std::uint64_t diff = lo ^ hi;
    const std::uint64_t varying =
        diff == 0
            ? 0
            : (width_mask >> (width - static_cast<unsigned>(std::bit_width(diff))));
    const std::uint64_t fixed = width_mask & ~varying;
    for (unsigned bit = 0; bit < width; ++bit) {
        const std::uint64_t mask = std::uint64_t{1} << bit;
        if ((fixed & mask) != 0 &&
            !fix_tnum_bit(tnum, bit, (lo & mask) != 0)) {
            return false;
        }
    }
    return true;
}

[[nodiscard]] inline std::vector<URange> full_ranges(const Scalar& scalar) {
    return intersect_ranges(URange{scalar.umin64, scalar.umax64},
                            unsigned_from_signed64(scalar.smin64,
                                                   scalar.smax64));
}

[[nodiscard]] inline std::vector<URange> low_ranges(const Scalar& scalar) {
    return intersect_ranges(URange{scalar.u32_min, scalar.u32_max},
                            unsigned_from_signed32(scalar.s32_min,
                                                   scalar.s32_max));
}

// Exact reachability for the component intersection. `full` constrains the
// complete 64-bit value and `low` constrains its low 32 bits. The tnum fixes
// each bit independently, so a small two-phase bit DP is exact here.
[[nodiscard]] inline bool feasible_for_ranges(Tnum tnum, URange full,
                                              URange low) {
    bool high[2][2] = {{true, false}, {false, false}};
    for (int bit = 63; bit >= 32; --bit) {
        bool next[2][2] = {};
        const std::uint64_t mask = std::uint64_t{1} << bit;
        const bool free = (tnum.mask & mask) != 0;
        const bool fixed = (tnum.value & mask) != 0;
        const bool lo_bit = (full.lo & mask) != 0;
        const bool hi_bit = (full.hi & mask) != 0;
        for (int rel_lo = 0; rel_lo < 2; ++rel_lo) {
            for (int rel_hi = 0; rel_hi < 2; ++rel_hi) {
                if (!high[rel_lo][rel_hi]) {
                    continue;
                }
                for (int choice = 0; choice < 2; ++choice) {
                    if (!free && choice != static_cast<int>(fixed)) {
                        continue;
                    }
                    int nlo = rel_lo;
                    int nhi = rel_hi;
                    if (rel_lo == 0) {
                        if (choice < static_cast<int>(lo_bit)) {
                            continue;
                        }
                        if (choice > static_cast<int>(lo_bit)) {
                            nlo = 1;
                        }
                    }
                    if (rel_hi == 0) {
                        if (choice > static_cast<int>(hi_bit)) {
                            continue;
                        }
                        if (choice < static_cast<int>(hi_bit)) {
                            nhi = 1;
                        }
                    }
                    next[nlo][nhi] = true;
                }
            }
        }
        for (int a = 0; a < 2; ++a) {
            for (int b = 0; b < 2; ++b) {
                high[a][b] = next[a][b];
            }
        }
    }

    bool states[2][2][2][2] = {};
    for (int fl = 0; fl < 2; ++fl) {
        for (int fh = 0; fh < 2; ++fh) {
            if (high[fl][fh]) {
                states[fl][fh][0][0] = true;
            }
        }
    }

    for (int bit = 31; bit >= 0; --bit) {
        bool next[2][2][2][2] = {};
        const std::uint64_t mask = std::uint64_t{1} << bit;
        const bool free = (tnum.mask & mask) != 0;
        const bool fixed = (tnum.value & mask) != 0;
        const bool flo_bit = (full.lo & mask) != 0;
        const bool fhi_bit = (full.hi & mask) != 0;
        const bool llo_bit = (low.lo & mask) != 0;
        const bool lhi_bit = (low.hi & mask) != 0;
        for (int fl = 0; fl < 2; ++fl) {
            for (int fh = 0; fh < 2; ++fh) {
                for (int ll = 0; ll < 2; ++ll) {
                    for (int lh = 0; lh < 2; ++lh) {
                        if (!states[fl][fh][ll][lh]) {
                            continue;
                        }
                        for (int choice = 0; choice < 2; ++choice) {
                            if (!free && choice != static_cast<int>(fixed)) {
                                continue;
                            }
                            int nfl = fl;
                            int nfh = fh;
                            int nll = ll;
                            int nlh = lh;
                            if (fl == 0) {
                                if (choice < static_cast<int>(flo_bit)) {
                                    continue;
                                }
                                if (choice > static_cast<int>(flo_bit)) {
                                    nfl = 1;
                                }
                            }
                            if (fh == 0) {
                                if (choice > static_cast<int>(fhi_bit)) {
                                    continue;
                                }
                                if (choice < static_cast<int>(fhi_bit)) {
                                    nfh = 1;
                                }
                            }
                            if (ll == 0) {
                                if (choice < static_cast<int>(llo_bit)) {
                                    continue;
                                }
                                if (choice > static_cast<int>(llo_bit)) {
                                    nll = 1;
                                }
                            }
                            if (lh == 0) {
                                if (choice > static_cast<int>(lhi_bit)) {
                                    continue;
                                }
                                if (choice < static_cast<int>(lhi_bit)) {
                                    nlh = 1;
                                }
                            }
                            next[nfl][nfh][nll][nlh] = true;
                        }
                    }
                }
            }
        }
        for (int a = 0; a < 2; ++a) {
            for (int b = 0; b < 2; ++b) {
                for (int c = 0; c < 2; ++c) {
                    for (int d = 0; d < 2; ++d) {
                        states[a][b][c][d] = next[a][b][c][d];
                    }
                }
            }
        }
    }

    for (int a = 0; a < 2; ++a) {
        for (int b = 0; b < 2; ++b) {
            for (int c = 0; c < 2; ++c) {
                for (int d = 0; d < 2; ++d) {
                    if (states[a][b][c][d]) {
                        return true;
                    }
                }
            }
        }
    }
    return false;
}

[[nodiscard]] inline bool has_value(const Scalar& scalar) {
    if (scalar.empty || scalar.umin64 > scalar.umax64 ||
        scalar.smin64 > scalar.smax64 || scalar.u32_min > scalar.u32_max ||
        scalar.s32_min > scalar.s32_max) {
        return false;
    }
    const auto full = full_ranges(scalar);
    const auto low = low_ranges(scalar);
    if (full.empty() || low.empty()) {
        return false;
    }
    for (URange f : full) {
        for (URange l : low) {
            if (feasible_for_ranges(scalar.tnum, f, l)) {
                return true;
            }
        }
    }
    return false;
}

}  // namespace ebpf::detail

#endif
