#pragma once

// Independent byte-exhaustive reference model for a small physical address
// space. It deliberately re-derives every matching and permission rule from
// the architecture manuals instead of reusing library internals, so it can
// serve as an independent oracle in differential tests.

#include <cstdint>
#include <vector>

#include "pmp/pmp_policy.hpp"

namespace pmp::reference {

struct BytePerm {
    Perm m;
    Perm su;
    int m_entry;
    int su_entry;
};

namespace detail {

inline bool entry_covers(const PmpEntry& e, std::uint64_t prev_pmpaddr,
                         std::uint64_t address) {
    switch (e.mode) {
    case Mode::Off:
        return false;
    case Mode::Tor:
        return (prev_pmpaddr << 2) <= address &&
               address < (e.addr << 2) && prev_pmpaddr < e.addr;
    case Mode::Na4: {
        const std::uint64_t base = (e.addr & ~1ULL) << 2;
        return base <= address && address < base + 4u;
    }
    case Mode::Napot: {
        // Find the run of 1 bits at the bottom of pmpaddr.
        std::uint64_t a = e.addr;
        unsigned trailing = 0;
        while ((a & 1u) != 0u) {
            ++trailing;
            a >>= 1;
        }
        const std::uint64_t size = 1ULL << (trailing + 2u);
        const std::uint64_t base =
            (e.addr & ~((size >> 2) - 1u)) << 2;
        return base <= address && address < base + size;
    }
    }
    return false;
}

inline std::uint8_t mml_lookup_m(const PmpEntry& e) {
    constexpr std::uint8_t table[16] = {0, 1, 2, 3, 4, 5, 6, 7, 0, 0,
                                        2, 3, 4, 5, 6, 7};
    const unsigned idx = (e.l ? 8u : 0u) | (e.r ? 4u : 0u) |
                         (e.w ? 2u : 0u) | (e.x ? 1u : 0u);
    return table[idx];
}

inline std::uint8_t mml_lookup_su(const PmpEntry& e) {
    constexpr std::uint8_t table[16] = {0, 1, 2, 3, 4, 5, 6, 7, 1, 1,
                                        0, 0, 4, 5, 4, 5};
    const unsigned idx = (e.l ? 8u : 0u) | (e.r ? 4u : 0u) |
                         (e.w ? 2u : 0u) | (e.x ? 1u : 0u);
    return table[idx];
}

inline Perm bits_to_perm(std::uint8_t bits) {
    Perm p;
    p.r = (bits & 0x4u) != 0u;
    p.w = (bits & 0x2u) != 0u;
    p.x = (bits & 0x1u) != 0u;
    return p;
}

} // namespace detail

// Evaluate one physical address by scanning entries in index order.
inline BytePerm evaluate(const PmpConfig& cfg, std::uint64_t address) {
    BytePerm out;
    out.m_entry = -1;
    out.su_entry = -1;

    for (std::size_t i = 0; i < cfg.entries.size(); ++i) {
        const PmpEntry& e = cfg.entries[i];
        const std::uint64_t prev =
            (i == 0u) ? 0u : cfg.entries[i - 1u].addr;
        if (!detail::entry_covers(e, prev, address)) {
            continue;
        }

        if (out.su_entry < 0) {
            out.su_entry = static_cast<int>(i);
        }
        if (out.m_entry < 0 && (cfg.mseccfg.mml || e.l)) {
            out.m_entry = static_cast<int>(i);
        }
    }

    const bool default_allow =
        !cfg.mseccfg.mmwp && !cfg.mseccfg.mml;
    if (out.su_entry < 0) {
        out.su = Perm{};
    } else if (cfg.mseccfg.mml) {
        out.su = detail::bits_to_perm(detail::mml_lookup_su(
            cfg.entries[static_cast<std::size_t>(out.su_entry)]));
    } else {
        const PmpEntry& e =
            cfg.entries[static_cast<std::size_t>(out.su_entry)];
        out.su = Perm{e.r, e.w, e.x};
    }

    if (out.m_entry < 0) {
        out.m = Perm{default_allow, default_allow, default_allow};
    } else if (cfg.mseccfg.mml) {
        out.m = detail::bits_to_perm(detail::mml_lookup_m(
            cfg.entries[static_cast<std::size_t>(out.m_entry)]));
    } else {
        const PmpEntry& e =
            cfg.entries[static_cast<std::size_t>(out.m_entry)];
        out.m = Perm{e.r, false, e.x};
    }
    return out;
}

// Full exhaustive picture of a 2^pa_bits byte address space.
inline std::vector<BytePerm> exhaust(const PmpConfig& cfg, unsigned pa_bits) {
    const std::uint64_t count = 1ULL << pa_bits;
    std::vector<BytePerm> picture(static_cast<std::size_t>(count));
    for (std::uint64_t address = 0; address < count; ++address) {
        picture[static_cast<std::size_t>(address)] =
            evaluate(cfg, address);
    }
    return picture;
}

} // namespace pmp::reference
