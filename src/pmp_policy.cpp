#include "pmp/pmp_policy.hpp"

#include <algorithm>
#include <cstdint>
#include <string>

namespace pmp {

namespace {

constexpr std::uint8_t kBitR = 0x4;
constexpr std::uint8_t kBitW = 0x2;
constexpr std::uint8_t kBitX = 0x1;

struct RawPerm {
    std::uint8_t m;
    std::uint8_t su;
};

Perm from_bits(std::uint8_t bits) noexcept {
    Perm p;
    p.r = (bits & kBitR) != 0u;
    p.w = (bits & kBitW) != 0u;
    p.x = (bits & kBitX) != 0u;
    return p;
}

// Smepmp MML == 1 truth table, indexed by the 4-bit L/R/W/X encoding.
// Reference: RISC-V Smepmp table "PMP permissions when MML = 1"; reserved
// encodings deny every access on both privilege domains.
constexpr RawPerm kMmlTable[16] = {
    {0x0, 0x0}, //  0 L=0 R=0 W=0 X=0  denied everywhere
    {0x1, 0x1}, //  1 L=0 R=0 W=0 X=1  shared X only
    {0x2, 0x2}, //  2 L=0 R=0 W=1 X=0  shared W only
    {0x3, 0x3}, //  3 L=0 R=0 W=1 X=1  shared WX
    {0x4, 0x4}, //  4 L=0 R=1 W=0 X=0  shared R only
    {0x5, 0x5}, //  5 L=0 R=1 W=0 X=1  shared RX
    {0x6, 0x6}, //  6 L=0 R=1 W=1 X=0  shared RW
    {0x7, 0x7}, //  7 L=0 R=1 W=1 X=1  shared RWX
    {0x0, 0x1}, //  8 L=1 R=0 W=0 X=0  M denied, S/U X only
    {0x0, 0x1}, //  9 L=1 R=0 W=0 X=1  M denied (no U-code execution), S/U X
    {0x2, 0x0}, // 10 L=1 R=0 W=1 X=0  M-only W
    {0x3, 0x0}, // 11 L=1 R=0 W=1 X=1  M-only WX
    {0x4, 0x4}, // 12 L=1 R=1 W=0 X=0  shared read-only
    {0x5, 0x5}, // 13 L=1 R=1 W=0 X=1  shared executable RX
    {0x6, 0x4}, // 14 L=1 R=1 W=1 X=0  M RW, S/U read-only
    {0x7, 0x5}, // 15 L=1 R=1 W=1 X=1  M RWX private, S/U RX
};

std::uint8_t table_index(const PmpEntry& e) noexcept {
    return static_cast<std::uint8_t>((e.l ? 8u : 0u) | (e.r ? 4u : 0u) |
                                     (e.w ? 2u : 0u) | (e.x ? 1u : 0u));
}

// Classic (MML == 0) permissions of a covering entry.
Perm classic_m_perm(const PmpEntry& e) noexcept {
    // Only locked entries arbitrate M-mode; a locked entry never grants W.
    Perm p;
    p.r = e.r;
    p.w = false;
    p.x = e.x;
    return p;
}

Perm classic_su_perm(const PmpEntry& e) noexcept {
    Perm p;
    p.r = e.r;
    p.w = e.w;
    p.x = e.x;
    return p;
}

unsigned trailing_ones(std::uint64_t value) noexcept {
    unsigned count = 0;
    while ((value & 1u) != 0u) {
        ++count;
        value >>= 1;
    }
    return count;
}

bool contains(const DecodedRegion& region, std::uint64_t address) noexcept {
    return region.lo <= address && address < region.hi;
}

} // namespace

std::optional<DecodedRegion> decode_region(const PmpEntry& entry,
                                           std::uint64_t prev_pmpaddr,
                                           unsigned pa_bits) {
    if (entry.mode == Mode::Off || pa_bits < 2u) {
        return std::nullopt;
    }
    const std::uint64_t top = 1ULL << pa_bits;

    std::uint64_t lo = 0;
    std::uint64_t hi = 0;

    switch (entry.mode) {
    case Mode::Tor:
        // Empty / backwards intervals (prev >= cur) match nothing.
        if (prev_pmpaddr >= entry.addr) {
            return std::nullopt;
        }
        lo = prev_pmpaddr << 2;
        hi = entry.addr << 2;
        break;
    case Mode::Na4:
        // Low two address bits are ignored: the pmpaddr low bit is a
        // don't-care zero for the 4-byte naturally aligned region.
        lo = (entry.addr & ~1ULL) << 2;
        hi = lo + 4u;
        break;
    case Mode::Napot: {
        const unsigned k = trailing_ones(entry.addr);
        const std::uint64_t size = 1ULL << (k + 2u);
        const std::uint64_t addr_mask = (size >> 2) - 1u;
        lo = (entry.addr & ~addr_mask) << 2;
        hi = lo + size;
        break;
    }
    case Mode::Off:
        return std::nullopt;
    }

    // Regions may straddle the top of the modeled physical space.
    if (lo >= top) {
        return std::nullopt;
    }
    if (hi > top || hi < lo) {
        hi = top;
    }
    return DecodedRegion{lo, hi};
}

PmpAnalyzer::PmpAnalyzer(unsigned pa_bits)
    : pa_bits_(pa_bits),
      top_(1ULL << pa_bits),
      max_addr_(pa_bits >= 2u ? (1ULL << (pa_bits - 2u)) - 1u : 0u) {}

std::optional<std::string> PmpAnalyzer::validate(const PmpConfig& cfg) const {
    if (pa_bits_ < 2u || pa_bits_ > 63u) {
        return std::string("pa_bits out of supported range [2,63]");
    }
    if (cfg.entries.size() > kMaxEntries) {
        return std::string("too many PMP entries (maximum is 64)");
    }
    for (std::size_t i = 0; i < cfg.entries.size(); ++i) {
        const PmpEntry& e = cfg.entries[i];
        switch (e.mode) {
        case Mode::Off:
        case Mode::Tor:
        case Mode::Na4:
        case Mode::Napot:
            break;
        default:
            return std::string("entry ") + std::to_string(i) +
                   " has invalid mode";
        }
        if (e.addr > max_addr_) {
            return std::string("entry ") + std::to_string(i) +
                   " pmpaddr exceeds the physical address width";
        }
    }
    return std::nullopt;
}

std::vector<AtomicRegion> PmpAnalyzer::analyze(const PmpConfig& cfg) const {
    std::vector<AtomicRegion> result;
    if (validate(cfg).has_value()) {
        return result;
    }

    const std::size_t n = cfg.entries.size();
    std::vector<std::optional<DecodedRegion>> decoded(n);
    std::vector<std::uint64_t> endpoints;
    endpoints.reserve(2u * n + 2u);
    endpoints.push_back(0u);
    endpoints.push_back(top_);

    for (std::size_t i = 0; i < n; ++i) {
        const std::uint64_t prev =
            (i == 0u) ? 0u : cfg.entries[i - 1u].addr;
        decoded[i] = decode_region(cfg.entries[i], prev, pa_bits_);
        if (decoded[i].has_value()) {
            endpoints.push_back(decoded[i]->lo);
            endpoints.push_back(decoded[i]->hi);
        }
    }

    std::sort(endpoints.begin(), endpoints.end());
    endpoints.erase(std::unique(endpoints.begin(), endpoints.end()),
                    endpoints.end());

    const bool mml = cfg.mseccfg.mml;
    const bool m_default_allow = !cfg.mseccfg.mmwp && !mml;

    for (std::size_t k = 0; k + 1u < endpoints.size(); ++k) {
        const std::uint64_t lo = endpoints[k];
        const std::uint64_t hi = endpoints[k + 1u];
        if (lo == hi) {
            continue;
        }

        AtomicRegion atom;
        atom.lo = lo;
        atom.hi = hi;

        int m_winner = -1;
        int su_winner = -1;
        for (std::size_t i = 0; i < n; ++i) {
            if (!decoded[i].has_value() || !contains(*decoded[i], lo)) {
                continue;
            }
            // Lowest-index entry wins hardware arbitration.
            if (su_winner < 0) {
                su_winner = static_cast<int>(i);
            }
            // With MML == 0 only locked entries arbitrate M-mode.
            if (m_winner < 0 && (mml || cfg.entries[i].l)) {
                m_winner = static_cast<int>(i);
            }
            if (m_winner >= 0 && su_winner >= 0) {
                break;
            }
        }

        atom.m_entry = m_winner;
        atom.su_entry = su_winner;

        if (su_winner < 0) {
            atom.su_perm = Perm{};
        } else if (mml) {
            const auto& e = cfg.entries[static_cast<std::size_t>(su_winner)];
            atom.su_perm = from_bits(kMmlTable[table_index(e)].su);
        } else {
            atom.su_perm = classic_su_perm(
                cfg.entries[static_cast<std::size_t>(su_winner)]);
        }

        if (m_winner < 0) {
            // No-match default policy for M-mode.
            Perm def;
            def.r = m_default_allow;
            def.w = m_default_allow;
            def.x = m_default_allow;
            atom.m_perm = def;
        } else if (mml) {
            const auto& e = cfg.entries[static_cast<std::size_t>(m_winner)];
            atom.m_perm = from_bits(kMmlTable[table_index(e)].m);
        } else {
            atom.m_perm = classic_m_perm(
                cfg.entries[static_cast<std::size_t>(m_winner)]);
        }

        result.push_back(atom);
    }

    return result;
}

namespace {

// Inclusive [lo, hi] clipped against the modeled PA space, returned as an
// empty interval when it does not intersect [0, top).
std::optional<DecodedRegion> clip_spec(const SpecRange& range,
                                       std::uint64_t top) {
    if (range.hi < range.lo) {
        return std::nullopt;
    }
    DecodedRegion out;
    out.lo = range.lo;
    out.hi = range.hi >= top ? top : range.hi + 1u;
    if (out.lo >= out.hi) {
        return std::nullopt;
    }
    return out;
}

} // namespace

std::optional<Violation>
PmpAnalyzer::find_violation(const PmpConfig& cfg,
                            const IsolationSpec& spec) const {
    if (validate(cfg).has_value()) {
        return std::nullopt;
    }
    const std::vector<AtomicRegion> atoms = analyze(cfg);

    struct Candidate {
        std::uint64_t address;
        int entry_index;
        Violation::Kind kind;
    };
    std::optional<Candidate> best;

    auto consider = [&](std::uint64_t address, int entry_index,
                        Violation::Kind kind) {
        if (!best.has_value() || address < best->address) {
            best = Candidate{address, entry_index, kind};
        }
    };

    for (const AtomicRegion& atom : atoms) {
        for (const SpecRange& range : spec.m_private) {
            const auto clipped = clip_spec(range, top_);
            if (!clipped.has_value()) {
                continue;
            }
            const std::uint64_t ov_lo = std::max(atom.lo, clipped->lo);
            const std::uint64_t ov_hi = std::min(atom.hi, clipped->hi);
            if (ov_lo < ov_hi && !atom.su_perm.none()) {
                consider(ov_lo, atom.su_entry,
                         Violation::Kind::MPrivateLeakToSU);
            }
        }
        for (const SpecRange& range : spec.su_isolated) {
            const auto clipped = clip_spec(range, top_);
            if (!clipped.has_value()) {
                continue;
            }
            const std::uint64_t ov_lo = std::max(atom.lo, clipped->lo);
            const std::uint64_t ov_hi = std::min(atom.hi, clipped->hi);
            if (ov_lo < ov_hi && !atom.m_perm.none()) {
                consider(ov_lo, atom.m_entry,
                         Violation::Kind::SUIsolatedLeakToM);
            }
        }
        for (const SpecRange& range : spec.no_wx) {
            const auto clipped = clip_spec(range, top_);
            if (!clipped.has_value()) {
                continue;
            }
            const std::uint64_t ov_lo = std::max(atom.lo, clipped->lo);
            const std::uint64_t ov_hi = std::min(atom.hi, clipped->hi);
            if (ov_lo >= ov_hi) {
                continue;
            }
            if (atom.m_perm.wx()) {
                consider(ov_lo, atom.m_entry, Violation::Kind::WXConflict);
            }
            if (atom.su_perm.wx()) {
                consider(ov_lo, atom.su_entry, Violation::Kind::WXConflict);
            }
        }
    }

    if (!best.has_value()) {
        return std::nullopt;
    }
    Violation v;
    v.address = best->address;
    v.entry_index = best->entry_index;
    v.kind = best->kind;
    return v;
}

namespace {

const AtomicRegion& region_at(const std::vector<AtomicRegion>& atoms,
                              std::uint64_t address) {
    // Atoms are sorted and cover [0, top); upper_bound on lo finds the atom.
    auto it = std::upper_bound(
        atoms.begin(), atoms.end(), address,
        [](std::uint64_t value, const AtomicRegion& atom) {
            return value < atom.lo;
        });
    if (it == atoms.begin()) {
        return atoms.front();
    }
    --it;
    return *it;
}

} // namespace

Perm PmpAnalyzer::m_perm_at(const std::vector<AtomicRegion>& atoms,
                            std::uint64_t address) const {
    return region_at(atoms, address).m_perm;
}

Perm PmpAnalyzer::su_perm_at(const std::vector<AtomicRegion>& atoms,
                             std::uint64_t address) const {
    return region_at(atoms, address).su_perm;
}

TransitionReport check_transition(const PmpConfig& from,
                                  const PmpConfig& to) {
    TransitionReport report;
    auto fail = [&](std::string message) {
        report.ok = false;
        report.violations.push_back(std::move(message));
    };

    // mseccfg architectural rules.
    if (from.mseccfg.mml && !to.mseccfg.mml) {
        fail("mseccfg.MML is sticky and cannot be cleared once set");
    }
    if (from.mseccfg.mmwp && !to.mseccfg.mmwp) {
        fail("mseccfg.MMWP is sticky and cannot be cleared once set");
    }
    if (to.mseccfg.mml && to.mseccfg.rlb) {
        fail("mseccfg.RLB cannot be enabled while MML is set");
    }

    if (from.entries.size() != to.entries.size()) {
        fail("PMP table entry count changed across the transition");
        return report;
    }

    // Effective RLB: RLB has no effect while MML is set.
    const bool lock_bypass =
        from.mseccfg.rlb && !from.mseccfg.mml;
    if (lock_bypass) {
        return report;
    }

    const std::size_t n = from.entries.size();
    for (std::size_t i = 0; i < n; ++i) {
        const PmpEntry& old = from.entries[i];
        const PmpEntry& now = to.entries[i];
        // A locked entry i also locks pmpaddr[i-1], which is the lower
        // bound of its TOR region regardless of entry i-1's own mode.
        const bool addr_locked_here = old.l;
        const bool addr_locked_above =
            (i + 1u < n) && from.entries[i + 1u].l;
        if ((addr_locked_here || addr_locked_above) && old.addr != now.addr) {
            fail("locked pmpaddr[" + std::to_string(i) +
                 "] was modified while RLB == 0");
        }

        if (!old.l) {
            continue;
        }

        // A locked pmpcfg register is read-only in full.
        if (old.mode != now.mode || old.l != now.l || old.r != now.r ||
            old.w != now.w || old.x != now.x) {
            fail("locked pmpcfg[" + std::to_string(i) +
                 "] was modified while RLB == 0");
        }
    }

    return report;
}

const char* mode_name(Mode mode) noexcept {
    switch (mode) {
    case Mode::Off:
        return "OFF";
    case Mode::Tor:
        return "TOR";
    case Mode::Na4:
        return "NA4";
    case Mode::Napot:
        return "NAPOT";
    }
    return "UNKNOWN";
}

const char* violation_kind_name(Violation::Kind kind) noexcept {
    switch (kind) {
    case Violation::Kind::MPrivateLeakToSU:
        return "M-private region accessible from S/U";
    case Violation::Kind::SUIsolatedLeakToM:
        return "S/U-isolated region accessible from M";
    case Violation::Kind::WXConflict:
        return "simultaneous writable and executable region";
    }
    return "UNKNOWN";
}

} // namespace pmp
