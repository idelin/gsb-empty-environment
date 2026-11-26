#pragma once

// Precise symbolic interval PMP / Smepmp policy analysis library.
//
// Zero external dependencies, C++17, strict-warning clean:
//   -Wall -Wextra -Wconversion -Werror
//
// The analyzer never iterates over physical addresses byte by byte. Every
// active PMP entry is decoded into a closed-form interval, the full physical
// address space is partitioned into the minimum set of disjoint atomic
// intervals, and each atom is annotated with the architecturally effective
// (R,W,X) permissions for M-mode and S/U-mode according to the lowest-index
// hardware arbitration rule.

#include <cstdint>
#include <optional>
#include <string>
#include <vector>

namespace pmp {

// Physical address width used by the production analyzer.
constexpr unsigned kDefaultPaBits = 56;
// Architectural maximum number of PMP entries.
constexpr std::size_t kMaxEntries = 64;

// Effective permission bits for one privilege domain.
struct Perm {
    bool r = false;
    bool w = false;
    bool x = false;

    bool none() const noexcept { return !r && !w && !x; }
    // True when the region is simultaneously writable and executable.
    bool wx() const noexcept { return w && x; }

    bool operator==(const Perm& other) const noexcept {
        return r == other.r && w == other.w && x == other.x;
    }
};

enum class Mode : std::uint8_t {
    Off = 0,
    Tor = 1,
    Na4 = 2,
    Napot = 3,
};

// One ordered pmpcfg[i] / pmpaddr[i] table entry.
struct PmpEntry {
    Mode mode = Mode::Off;
    bool l = false;
    bool r = false;
    bool w = false;
    bool x = false;
    // Raw pmpaddr register value (WARL bits assumed already filtered by the
    // caller; must fit in pa_bits-2 bits).
    std::uint64_t addr = 0;
};

struct Mseccfg {
    bool mml = false;
    bool mmwp = false;
    bool rlb = false;
};

// A fully validated PMP configuration.
struct PmpConfig {
    std::vector<PmpEntry> entries;
    Mseccfg mseccfg{};
};

// One constant-permission atomic interval [lo, hi) covering the full PA
// space. The set of atoms returned by PmpAnalyzer is disjoint and ordered.
struct AtomicRegion {
    std::uint64_t lo = 0;
    std::uint64_t hi = 0;

    // Index of the entry that arbitrates each domain, -1 when no entry
    // matches and the default policy applies.
    int m_entry = -1;
    int su_entry = -1;

    Perm m_perm;
    Perm su_perm;
};

// Caller-declared isolation requirements. All addresses are inclusive on
// both ends; [lo, hi] is clipped to the modeled PA space during checking.
struct SpecRange {
    std::uint64_t lo = 0;
    std::uint64_t hi = 0;
};

struct IsolationSpec {
    // M-mode private: every byte must deny S/U access entirely.
    std::vector<SpecRange> m_private;
    // S/U isolated: every byte must deny M access entirely.
    std::vector<SpecRange> su_isolated;
    // No W^X: no domain may hold simultaneous W and X on any byte.
    std::vector<SpecRange> no_wx;
};

// Lowest-address counterexample to the declared isolation specification.
struct Violation {
    std::uint64_t address = 0;
    // Entry index that grants the offending permission; -1 when the offense
    // is caused by the default (no-match) policy rather than an entry.
    int entry_index = -1;
    // Which requirement was broken.
    enum class Kind : std::uint8_t {
        MPrivateLeakToSU,
        SUIsolatedLeakToM,
        WXConflict,
    } kind = Kind::MPrivateLeakToSU;
};

// Result of a cross-state mseccfg / PMP table migration check.
struct TransitionReport {
    bool ok = true;
    // Human-readable descriptions of every detected violation.
    std::vector<std::string> violations;
};

class PmpAnalyzer {
public:
    explicit PmpAnalyzer(unsigned pa_bits = kDefaultPaBits);

    unsigned pa_bits() const noexcept { return pa_bits_; }
    std::uint64_t pa_top() const noexcept { return top_; }

    // Validate a raw configuration. A human-readable reason is returned on
    // failure. Up to 64 ordered entries are accepted.
    std::optional<std::string> validate(const PmpConfig& cfg) const;

    // Build the minimum disjoint atomic partition with exact effective
    // permissions for both privilege domains.
    //
    // Call validate() first; an invalid configuration yields an empty result.
    std::vector<AtomicRegion> analyze(const PmpConfig& cfg) const;

    // Find the lowest physical address (with the winning entry index) that
    // violates the declared isolation specification.
    std::optional<Violation> find_violation(const PmpConfig& cfg,
                                           const IsolationSpec& spec) const;

    // Effective permissions of a single physical address; convenience wrapper
    // over the atomic partition (binary search, never scans addresses).
    Perm m_perm_at(const std::vector<AtomicRegion>& atoms,
                   std::uint64_t address) const;
    Perm su_perm_at(const std::vector<AtomicRegion>& atoms,
                    std::uint64_t address) const;

private:
    unsigned pa_bits_;
    std::uint64_t top_;
    std::uint64_t max_addr_; // largest legal pmpaddr: 2^(pa_bits-2) - 1
};

// Architectural migration rules:
//   * a locked entry (and, for TOR, the pmpaddr of its lower neighbor) is
//     immutable while effective RLB == 0;
//   * MML and MMWP are sticky once set and may never be cleared;
//   * RLB cannot be enabled while MML == 1.
TransitionReport check_transition(const PmpConfig& from,
                                  const PmpConfig& to);

// Decoded closed-form coverage [lo, hi) of one entry within a PA space of
// width pa_bits. OFF entries and empty TOR intervals return std::nullopt.
// Exposed for direct unit testing of the decoder.
struct DecodedRegion {
    std::uint64_t lo;
    std::uint64_t hi;
};
std::optional<DecodedRegion> decode_region(const PmpEntry& entry,
                                           std::uint64_t prev_pmpaddr,
                                           unsigned pa_bits);

// Debug helpers.
const char* mode_name(Mode mode) noexcept;
const char* violation_kind_name(Violation::Kind kind) noexcept;

} // namespace pmp
