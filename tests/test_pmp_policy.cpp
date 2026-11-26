#include "pmp/pmp_policy.hpp"
#include "reference_matcher.hpp"

#include <cstdint>
#include <cstdlib>
#include <iostream>
#include <optional>
#include <string>
#include <vector>

namespace {

int g_failures = 0;
int g_checks = 0;

void record(bool condition, const char* expr, const char* file, int line) {
    ++g_checks;
    if (!condition) {
        ++g_failures;
        std::cerr << "FAIL: " << expr << " (" << file << ':' << line
                  << ")\n";
    }
}

#define CHECK(cond) record((cond), #cond, __FILE__, __LINE__)

#define CHECK_EQ(a, b)                                                      \
    do {                                                                    \
        auto va_ = (a);                                                     \
        auto vb_ = (b);                                                     \
        record(va_ == vb_, #a " == " #b, __FILE__, __LINE__);              \
    } while (0)

pmp::Perm perm(bool r, bool w, bool x) {
    return pmp::Perm{r, w, x};
}

pmp::PmpEntry off_entry() {
    pmp::PmpEntry e;
    return e;
}

pmp::PmpEntry make_entry(pmp::Mode mode, std::uint64_t addr, bool l,
                         bool r, bool w, bool x) {
    pmp::PmpEntry e;
    e.mode = mode;
    e.addr = addr;
    e.l = l;
    e.r = r;
    e.w = w;
    e.x = x;
    return e;
}

// ----------------------------------------------------------------------
// Decoder tests
// ----------------------------------------------------------------------

void test_decoder_tor() {
    // TOR [8, 32): pmpaddr 2 and 8.
    auto region = pmp::decode_region(make_entry(pmp::Mode::Tor, 8, false,
                                                true, true, false),
                                     2, 12);
    CHECK(region.has_value());
    CHECK_EQ(region->lo, 8u);
    CHECK_EQ(region->hi, 32u);

    // Backwards / equal TOR intervals are empty.
    CHECK(!pmp::decode_region(make_entry(pmp::Mode::Tor, 4, false, true,
                                         false, false),
                              4, 12)
              .has_value());
    CHECK(!pmp::decode_region(make_entry(pmp::Mode::Tor, 3, false, true,
                                         false, false),
                              7, 12)
              .has_value());

    // First entry with prev == 0 starts at address zero.
    auto from_zero = pmp::decode_region(
        make_entry(pmp::Mode::Tor, 5, false, true, false, false), 0, 12);
    CHECK(from_zero.has_value());
    CHECK_EQ(from_zero->lo, 0u);
    CHECK_EQ(from_zero->hi, 20u);
}

void test_decoder_na4() {
    // Low bit of pmpaddr is masked off: addr 9 and 8 decode identically.
    auto a = pmp::decode_region(
        make_entry(pmp::Mode::Na4, 9, false, true, false, false), 0, 12);
    auto b = pmp::decode_region(
        make_entry(pmp::Mode::Na4, 8, false, true, false, false), 0, 12);
    CHECK(a.has_value());
    CHECK(b.has_value());
    CHECK_EQ(a->lo, b->lo);
    CHECK_EQ(a->lo, 32u);
    CHECK_EQ(a->hi, 36u);
}

void test_decoder_napot() {
    struct Case {
        std::uint64_t addr;
        std::uint64_t lo;
        std::uint64_t size;
    };
    const Case cases[] = {
        {0b0, 0, 4},          // even addr -> 4-byte NA4-like region
        {0b1, 0, 8},          // 8-byte
        {0b10, 8, 4},         // even addr -> 4-byte at byte 8
        {0b11, 0, 16},        // 16-byte
        {0b100011, 128, 16},  // 16-byte region at byte 128
        {0b101111, 128, 64},  // 64-byte region at byte 128
        {0b1001111, 256, 64}, // 64-byte region at byte 256
    };
    for (const Case& c : cases) {
        auto region = pmp::decode_region(
            make_entry(pmp::Mode::Napot, c.addr, false, true, true, false),
            0, 12);
        CHECK(region.has_value());
        CHECK_EQ(region->lo, c.lo);
        CHECK_EQ(region->hi - region->lo, c.size);
    }

    // NAPOT region straddling the modeled PA top is clipped, not dropped.
    // pa_bits=12 -> top 4096; pmpaddr with many trailing ones near the top.
    const std::uint64_t top_addr = (1ULL << 10u) - 1u; // pmpaddr 1023
    auto clipped = pmp::decode_region(
        make_entry(pmp::Mode::Napot, top_addr, false, true, false, false),
        0, 12);
    CHECK(clipped.has_value());
    CHECK_EQ(clipped->hi, 4096u);
}

void test_validation() {
    pmp::PmpAnalyzer analyzer(12);

    pmp::PmpConfig good;
    good.entries.push_back(
        make_entry(pmp::Mode::Na4, 100, false, true, false, false));
    CHECK(!analyzer.validate(good).has_value());

    pmp::PmpConfig bad_addr;
    bad_addr.entries.push_back(
        make_entry(pmp::Mode::Na4, 1ULL << 10u, false, true, false,
                   false));
    CHECK(analyzer.validate(bad_addr).has_value());

    pmp::PmpConfig too_many;
    too_many.entries.resize(65);
    CHECK(analyzer.validate(too_many).has_value());
}

// ----------------------------------------------------------------------
// Smepmp MML=1 16-entry truth table
// ----------------------------------------------------------------------

void test_mml_truth_table() {
    for (unsigned code = 0; code < 16u; ++code) {
        pmp::PmpConfig cfg;
        cfg.mseccfg.mml = true;
        cfg.entries.push_back(make_entry(
            pmp::Mode::Tor,
            0x10, // covers [0, 64)
            (code & 8u) != 0u, (code & 4u) != 0u, (code & 2u) != 0u,
            (code & 1u) != 0u));

        pmp::PmpAnalyzer analyzer(12);
        auto atoms = analyzer.analyze(cfg);
        CHECK(!atoms.empty());

        const auto picture = pmp::reference::exhaust(cfg, 12);
        const pmp::Perm inside_m = analyzer.m_perm_at(atoms, 8);
        const pmp::Perm inside_su = analyzer.su_perm_at(atoms, 8);
        CHECK(inside_m == picture[8].m);
        CHECK(inside_su == picture[8].su);

        // Outside the single region: M default-denies under MML, S/U deny.
        const pmp::Perm outside_m = analyzer.m_perm_at(atoms, 100);
        const pmp::Perm outside_su = analyzer.su_perm_at(atoms, 100);
        CHECK(outside_m == picture[100].m);
        CHECK(outside_su == picture[100].su);
        CHECK(outside_m.none());
        CHECK(outside_su.none());
    }

    // Explicit spot checks of the notable encodings.
    pmp::PmpConfig shared_ro;
    shared_ro.mseccfg.mml = true;
    shared_ro.entries.push_back(
        make_entry(pmp::Mode::Tor, 0x10, true, true, false, false));
    pmp::PmpAnalyzer analyzer(12);
    auto atoms = analyzer.analyze(shared_ro);
    CHECK(analyzer.m_perm_at(atoms, 0) == perm(true, false, false));
    CHECK(analyzer.su_perm_at(atoms, 0) == perm(true, false, false));

    pmp::PmpConfig m_private;
    m_private.mseccfg.mml = true;
    m_private.entries.push_back(
        make_entry(pmp::Mode::Tor, 0x10, true, true, true, true));
    atoms = analyzer.analyze(m_private);
    CHECK(analyzer.m_perm_at(atoms, 0) == perm(true, true, true));
    CHECK(analyzer.su_perm_at(atoms, 0) == perm(true, false, true));

    // L=1 R=0 W=0 X=1: M must not execute U-mode code.
    pmp::PmpConfig u_code;
    u_code.mseccfg.mml = true;
    u_code.entries.push_back(
        make_entry(pmp::Mode::Tor, 0x10, true, false, false, true));
    atoms = analyzer.analyze(u_code);
    CHECK(analyzer.m_perm_at(atoms, 0).none());
    CHECK(analyzer.su_perm_at(atoms, 0) == perm(false, false, true));
}

// ----------------------------------------------------------------------
// Classic PMP (MML=0) semantics
// ----------------------------------------------------------------------

void test_classic_semantics() {
    pmp::PmpAnalyzer analyzer(12);

    // Unlocked entry: invisible to M-mode (default allow), applies to S/U.
    pmp::PmpConfig unlocked;
    unlocked.entries.push_back(
        make_entry(pmp::Mode::Tor, 0x10, false, true, true, false));
    auto atoms = analyzer.analyze(unlocked);
    CHECK(analyzer.m_perm_at(atoms, 8) ==
          perm(true, true, true)); // default allow
    CHECK(analyzer.su_perm_at(atoms, 8) ==
          perm(true, true, false));

    // Locked entry: M loses W, keeps R/X; S/U keeps R/W/X.
    pmp::PmpConfig locked;
    locked.entries.push_back(
        make_entry(pmp::Mode::Tor, 0x10, true, true, true, true));
    atoms = analyzer.analyze(locked);
    CHECK(analyzer.m_perm_at(atoms, 8) ==
          perm(true, false, true));
    CHECK(analyzer.su_perm_at(atoms, 8) ==
          perm(true, true, true));

    // MMWP=1 flips the M-mode no-match default to deny; unlocked entries
    // still cannot arbitrate M-mode.
    pmp::PmpConfig mmwp;
    mmwp.mseccfg.mmwp = true;
    mmwp.entries.push_back(
        make_entry(pmp::Mode::Tor, 0x10, false, true, true, true));
    atoms = analyzer.analyze(mmwp);
    CHECK(analyzer.m_perm_at(atoms, 8).none());
    CHECK(analyzer.m_perm_at(atoms, 200).none());
    CHECK(analyzer.su_perm_at(atoms, 8) ==
          perm(true, true, true));

    // MMWP with a locked entry: M gets R/X where locked, deny elsewhere.
    pmp::PmpConfig mmwp_locked;
    mmwp_locked.mseccfg.mmwp = true;
    mmwp_locked.entries.push_back(
        make_entry(pmp::Mode::Tor, 0x10, true, true, true, true));
    atoms = analyzer.analyze(mmwp_locked);
    CHECK(analyzer.m_perm_at(atoms, 8) ==
          perm(true, false, true));
    CHECK(analyzer.m_perm_at(atoms, 200).none());
}

void test_priority_shadowing() {
    pmp::PmpAnalyzer analyzer(12);
    pmp::PmpConfig cfg;
    cfg.mseccfg.mml = true;
    // Entry 0: narrow locked read-only NAPOT region [128, 192).
    cfg.entries.push_back(
        make_entry(pmp::Mode::Napot, 0b100111, true, true, false, false));
    // Entry 1: broad RWX NAPOT region [0, 256); entry 0 shadows it on
    // its own bytes because the lower index wins hardware arbitration.
    cfg.entries.push_back(
        make_entry(pmp::Mode::Napot, 0b111111, false, true, true, true));

    auto atoms = analyzer.analyze(cfg);
    // Within [128,192) entry 1 wins: shared read-only.
    CHECK(analyzer.m_perm_at(atoms, 140) ==
          perm(true, false, false));
    CHECK(analyzer.su_perm_at(atoms, 140) ==
          perm(true, false, false));
    // Entry 1 governs bytes outside the narrow region: lowest covering
    // index there is 1.
    CHECK(analyzer.m_perm_at(atoms, 8) ==
          perm(true, true, true));
    CHECK(analyzer.su_perm_at(atoms, 8) ==
          perm(true, true, true));

    // Atom partition must be disjoint, ordered and cover the whole space.
    std::uint64_t cursor = 0;
    for (const auto& atom : atoms) {
        CHECK_EQ(atom.lo, cursor);
        CHECK(atom.hi > atom.lo);
        cursor = atom.hi;
    }
    CHECK_EQ(cursor, analyzer.pa_top());
}

void test_default_policies() {
    pmp::PmpAnalyzer analyzer(12);
    pmp::PmpConfig empty;
    auto atoms = analyzer.analyze(empty);
    CHECK_EQ(atoms.size(), static_cast<std::size_t>(1));
    CHECK(analyzer.m_perm_at(atoms, 0) ==
          perm(true, true, true));
    CHECK(analyzer.su_perm_at(atoms, 0).none());

    pmp::PmpConfig mmwp;
    mmwp.mseccfg.mmwp = true;
    atoms = analyzer.analyze(mmwp);
    CHECK(analyzer.m_perm_at(atoms, 0).none());

    pmp::PmpConfig mml;
    mml.mseccfg.mml = true;
    atoms = analyzer.analyze(mml);
    CHECK(analyzer.m_perm_at(atoms, 0).none());
    CHECK(analyzer.su_perm_at(atoms, 0).none());
}

// ----------------------------------------------------------------------
// Cross-state transition checks
// ----------------------------------------------------------------------

void test_transition_locks() {
    pmp::PmpConfig from;
    from.entries.push_back(
        make_entry(pmp::Mode::Tor, 0x10, true, true, false, false));
    from.entries.push_back(
        make_entry(pmp::Mode::Tor, 0x20, true, true, true, false));

    // Identical configuration is fine.
    CHECK(pmp::check_transition(from, from).ok);

    // Locked pmpcfg cannot change with RLB == 0.
    pmp::PmpConfig to = from;
    to.entries[0].w = true;
    CHECK(!pmp::check_transition(from, to).ok);

    // Locked pmpaddr cannot change with RLB == 0.
    to = from;
    to.entries[0].addr = 0x11;
    CHECK(!pmp::check_transition(from, to).ok);

    // TOR pmpaddr[0] is also locked by locked entry 1 above it.
    // Entry 0 is unlocked, but its pmpaddr is the lower bound of the
    // locked TOR entry 1 above, so writes to it are rejected.
    pmp::PmpConfig tor_lock;
    tor_lock.entries.push_back(off_entry());
    tor_lock.entries[0].addr = 0x08;
    tor_lock.entries[0].mode = pmp::Mode::Tor;
    tor_lock.entries[0].r = true;
    tor_lock.entries.push_back(
        make_entry(pmp::Mode::Tor, 0x10, true, true, true, false));
    pmp::PmpConfig tor_lock_touched = tor_lock;
    tor_lock_touched.entries[0].addr = 0x09;
    CHECK(!pmp::check_transition(tor_lock, tor_lock_touched).ok);

    // The lower-neighbor pmpaddr lock applies regardless of the upper
    // neighbor's own addressing mode.
    pmp::PmpConfig napot_upper;
    napot_upper.entries.push_back(
        make_entry(pmp::Mode::Tor, 0x10, false, true, false, false));
    napot_upper.entries.push_back(
        make_entry(pmp::Mode::Napot, 0b1111, true, true, true, false));
    pmp::PmpConfig napot_upper_touched = napot_upper;
    napot_upper_touched.entries[0].addr = 0x12;
    CHECK(!pmp::check_transition(napot_upper, napot_upper_touched).ok);

    // RLB == 1 permits changing a locked entry (only valid while MML == 0).
    pmp::PmpConfig rlb_from = from;
    rlb_from.mseccfg.rlb = true;
    pmp::PmpConfig rlb_to = rlb_from;
    rlb_to.entries[0].addr = 0x13;
    rlb_to.entries[0].w = true;
    CHECK(pmp::check_transition(rlb_from, rlb_to).ok);

    // RLB has no effect when MML is set.
    pmp::PmpConfig mml_from = from;
    mml_from.mseccfg.mml = true;
    mml_from.mseccfg.rlb = true; // illegal state itself
    pmp::PmpConfig mml_to = mml_from;
    mml_to.entries[0].addr = 0x14;
    CHECK(!pmp::check_transition(mml_from, mml_to).ok);
}

void test_transition_mseccfg() {
    pmp::PmpConfig from;
    from.entries.push_back(off_entry());

    pmp::PmpConfig clear_mml = from;
    clear_mml.mseccfg.mml = true;
    pmp::PmpConfig mml_off = clear_mml;
    mml_off.mseccfg.mml = false;
    CHECK(!pmp::check_transition(clear_mml, mml_off).ok);

    pmp::PmpConfig clear_mmwp = from;
    clear_mmwp.mseccfg.mmwp = true;
    pmp::PmpConfig mmwp_off = clear_mmwp;
    mmwp_off.mseccfg.mmwp = false;
    CHECK(!pmp::check_transition(clear_mmwp, mmwp_off).ok);

    // RLB cannot be enabled while MML is set.
    pmp::PmpConfig rlb_with_mml = from;
    rlb_with_mml.mseccfg.mml = true;
    rlb_with_mml.mseccfg.rlb = true;
    CHECK(!pmp::check_transition(from, rlb_with_mml).ok);

    // Setting both sticky bits is a legal transition.
    pmp::PmpConfig enable = from;
    enable.mseccfg.mml = true;
    enable.mseccfg.mmwp = true;
    CHECK(pmp::check_transition(from, enable).ok);
}

// ----------------------------------------------------------------------
// Minimum-violation counterexample solving
// ----------------------------------------------------------------------

void test_violation_m_private() {
    pmp::PmpAnalyzer analyzer(12);
    pmp::PmpConfig cfg;
    // Entry 0 gives S/U RW over [0, 128).
    cfg.entries.push_back(
        make_entry(pmp::Mode::Tor, 32, false, true, true, false));

    pmp::IsolationSpec spec;
    spec.m_private.push_back(pmp::SpecRange{0, 63});

    auto v = analyzer.find_violation(cfg, spec);
    CHECK(v.has_value());
    CHECK_EQ(v->address, 0u);
    CHECK_EQ(v->entry_index, 0);
    CHECK(v->kind == pmp::Violation::Kind::MPrivateLeakToSU);

    // Spec only covers bytes starting at 64: first leak is there.
    pmp::IsolationSpec shifted;
    shifted.m_private.push_back(pmp::SpecRange{64, 100});
    v = analyzer.find_violation(cfg, shifted);
    CHECK(v.has_value());
    CHECK_EQ(v->address, 64u);

    // No overlap -> no violation.
    pmp::IsolationSpec clean;
    clean.m_private.push_back(pmp::SpecRange{256, 300});
    CHECK(!analyzer.find_violation(cfg, clean).has_value());
}

void test_violation_su_isolated() {
    pmp::PmpAnalyzer analyzer(12);

    // Under MMWP=1, M defaults to deny, so an S/U region is isolated.
    pmp::PmpConfig secure;
    secure.mseccfg.mmwp = true;
    secure.entries.push_back(
        make_entry(pmp::Mode::Tor, 32, false, true, true, false));
    pmp::IsolationSpec spec;
    spec.su_isolated.push_back(pmp::SpecRange{128, 255});
    CHECK(!analyzer.find_violation(secure, spec).has_value());

    // Default PMP lets M access everything: violation at the region start.
    pmp::PmpConfig loose;
    loose.entries.push_back(
        make_entry(pmp::Mode::Tor, 32, false, true, true, false));
    auto v = analyzer.find_violation(loose, spec);
    CHECK(v.has_value());
    CHECK_EQ(v->address, 128u);
    CHECK_EQ(v->entry_index, -1); // default policy, not an entry
    CHECK(v->kind == pmp::Violation::Kind::SUIsolatedLeakToM);

    // A locked entry giving M R access inside the region also violates.
    pmp::PmpConfig locked;
    locked.mseccfg.mmwp = true;
    locked.entries.push_back(
        make_entry(pmp::Mode::Tor, 64, true, true, false, false));
    v = analyzer.find_violation(locked, spec);
    CHECK(v.has_value());
    CHECK_EQ(v->address, 128u);
}

void test_violation_wx() {
    pmp::PmpAnalyzer analyzer(12);
    pmp::PmpConfig cfg;
    // RWX unlocked entry: S/U sees W+X (classic), M default allow also W+X.
    cfg.entries.push_back(
        make_entry(pmp::Mode::Tor, 32, false, true, true, true));

    pmp::IsolationSpec spec;
    spec.no_wx.push_back(pmp::SpecRange{0, 100});
    auto v = analyzer.find_violation(cfg, spec);
    CHECK(v.has_value());
    CHECK_EQ(v->address, 0u);
    CHECK(v->kind == pmp::Violation::Kind::WXConflict);

    // Beyond the region, MMWP denies the default M access and S/U has no
    // match, so the W^X rule is satisfied.
    pmp::PmpConfig wx_mmwp = cfg;
    wx_mmwp.mseccfg.mmwp = true;
    pmp::IsolationSpec later;
    later.no_wx.push_back(pmp::SpecRange{200, 300});
    CHECK(!analyzer.find_violation(wx_mmwp, later).has_value());

    // Locking strips M's W, but S/U still has W+X.
    pmp::PmpConfig locked = cfg;
    locked.entries[0].l = true;
    v = analyzer.find_violation(locked, spec);
    CHECK(v.has_value());
    CHECK_EQ(v->entry_index, 0);
}

// ----------------------------------------------------------------------
// Differential testing against the exhaustive 12-bit reference model
// ----------------------------------------------------------------------

class Rng {
public:
    explicit Rng(std::uint64_t seed) : state_(seed | 1u) {}

    std::uint64_t next() {
        std::uint64_t x = state_;
        x ^= x << 13u;
        x ^= x >> 7u;
        x ^= x << 17u;
        state_ = x;
        return x;
    }

    std::uint64_t below(std::uint64_t bound) {
        return next() % bound;
    }

private:
    std::uint64_t state_;
};

pmp::PmpConfig random_config(Rng& rng, unsigned pa_bits) {
    pmp::PmpConfig cfg;
    const std::uint64_t max_addr = (1ULL << (pa_bits - 2u)) - 1u;
    const std::size_t count = static_cast<std::size_t>(rng.below(9));
    cfg.entries.reserve(count);
    for (std::size_t i = 0; i < count; ++i) {
        pmp::PmpEntry e;
        const unsigned mode_pick =
            static_cast<unsigned>(rng.below(4)); // OFF/TOR/NA4/NAPOT
        e.mode = static_cast<pmp::Mode>(mode_pick);
        e.l = (rng.below(2) == 0u);
        e.r = (rng.below(2) == 0u);
        e.w = (rng.below(2) == 0u);
        e.x = (rng.below(2) == 0u);

        if (e.mode == pmp::Mode::Napot) {
            // Build a legal NAPOT encoding directly.
            const unsigned run =
                1u + static_cast<unsigned>(rng.below(pa_bits - 2u));
            const std::uint64_t base =
                (rng.below((max_addr >> run) + 1u)) << run;
            const std::uint64_t mask = (1ULL << run) - 1u;
            e.addr = base | mask;
            if (e.addr > max_addr) {
                e.addr = max_addr;
            }
        } else {
            e.addr = rng.below(max_addr + 1u);
        }
        cfg.entries.push_back(e);
    }

    const unsigned sec = static_cast<unsigned>(rng.below(4));
    cfg.mseccfg.mml = (sec == 1u || sec == 2u);
    cfg.mseccfg.mmwp = (sec == 2u || sec == 3u);
    cfg.mseccfg.rlb = false;
    return cfg;
}

void assert_partition_invariants(const pmp::PmpAnalyzer& analyzer,
                                 const std::vector<pmp::AtomicRegion>& atoms) {
    CHECK(!atoms.empty());
    std::uint64_t cursor = 0;
    for (const pmp::AtomicRegion& atom : atoms) {
        CHECK_EQ(atom.lo, cursor);
        CHECK(atom.hi > atom.lo);
        cursor = atom.hi;
    }
    CHECK_EQ(cursor, analyzer.pa_top());
}

void test_differential_random() {
    constexpr unsigned kPaBits = 12;
    pmp::PmpAnalyzer analyzer(kPaBits);
    Rng rng(0x9e3779b97f4a7c15ULL);

    for (unsigned iter = 0; iter < 4000u; ++iter) {
        pmp::PmpConfig cfg = random_config(rng, kPaBits);
        if (analyzer.validate(cfg).has_value()) {
            continue;
        }
        const auto atoms = analyzer.analyze(cfg);
        assert_partition_invariants(analyzer, atoms);
        const auto picture = pmp::reference::exhaust(cfg, kPaBits);

        for (std::uint64_t address = 0; address < (1ULL << kPaBits);
             ++address) {
            const auto idx = static_cast<std::size_t>(address);
            const pmp::Perm m = analyzer.m_perm_at(atoms, address);
            const pmp::Perm su = analyzer.su_perm_at(atoms, address);
            CHECK(m == picture[idx].m);
            CHECK(su == picture[idx].su);

            // Entry indices reported by the symbolic partition must match
            // the independently selected lowest-index winners.
            const pmp::AtomicRegion* owner = nullptr;
            for (const auto& a : atoms) {
                if (a.lo <= address && address < a.hi) {
                    owner = &a;
                    break;
                }
            }
            CHECK(owner != nullptr);
            CHECK_EQ(owner->m_entry, picture[idx].m_entry);
            CHECK_EQ(owner->su_entry, picture[idx].su_entry);
        }
    }
}

void test_differential_all_mseccfg_modes() {
    constexpr unsigned kPaBits = 12;
    pmp::PmpAnalyzer analyzer(kPaBits);
    Rng rng(0xdeadbeefcafeULL);

    const bool mml_modes[] = {false, true};
    const bool mmwp_modes[] = {false, true};
    for (bool mml : mml_modes) {
        for (bool mmwp : mmwp_modes) {
            for (unsigned iter = 0; iter < 200u; ++iter) {
                pmp::PmpConfig cfg = random_config(rng, kPaBits);
                cfg.mseccfg.mml = mml;
                cfg.mseccfg.mmwp = mmwp;
                if (analyzer.validate(cfg).has_value()) {
                    continue;
                }
                const auto atoms = analyzer.analyze(cfg);
                const auto picture = pmp::reference::exhaust(cfg, kPaBits);
                for (std::uint64_t address = 0;
                     address < (1ULL << kPaBits); ++address) {
                    const auto idx = static_cast<std::size_t>(address);
                    CHECK(analyzer.m_perm_at(atoms, address) ==
                          picture[idx].m);
                    CHECK(analyzer.su_perm_at(atoms, address) ==
                          picture[idx].su);
                }
            }
        }
    }
}

// ----------------------------------------------------------------------
// Production 56-bit address space smoke tests (symbolic, never byte loops)
// ----------------------------------------------------------------------

void test_56bit_symbolic() {
    pmp::PmpAnalyzer analyzer(56);
    const std::uint64_t top = analyzer.pa_top();
    CHECK_EQ(top, 1ULL << 56);

    pmp::PmpConfig cfg;
    cfg.mseccfg.mml = true;
    // Giant NAPOT region straddling the top of PA space.
    // pmpaddr all ones over 54 bits -> region of 2^56 bytes from zero.
    const std::uint64_t all_ones = (1ULL << 54u) - 1u;
    cfg.entries.push_back(
        make_entry(pmp::Mode::Napot, all_ones, true, true, false, false));
    CHECK(!analyzer.validate(cfg).has_value());

    auto atoms = analyzer.analyze(cfg);
    CHECK_EQ(atoms.size(), static_cast<std::size_t>(1));
    CHECK_EQ(atoms.front().lo, 0u);
    CHECK_EQ(atoms.front().hi, top);
    CHECK(analyzer.m_perm_at(atoms, top - 1u) ==
          perm(true, false, false));

    // Two TOR entries split the space; empty (reversed) entry contributes
    // no atoms.
    pmp::PmpConfig tor;
    tor.mseccfg.mmwp = true;
    tor.entries.push_back(
        make_entry(pmp::Mode::Tor, 0x1000, false, true, false, false));
    // Explicit empty reversed TOR pair.
    tor.entries.push_back(
        make_entry(pmp::Mode::Tor, 0x0800, false, true, true, false));
    tor.entries.push_back(
        make_entry(pmp::Mode::Tor, 0x0400, false, true, true, false));
    // Locked NAPOT region high in the space.
    tor.entries.push_back(
        make_entry(pmp::Mode::Napot, (1ULL << 52u) | 0x3ffULL, true,
                   true, false, true));
    atoms = analyzer.analyze(tor);
    assert_partition_invariants(analyzer, atoms);
    CHECK(analyzer.su_perm_at(atoms, 0x10) ==
          perm(true, false, false));
    // The reversed TOR pair contributes nothing; beyond 0x1000 only the
    // high NAPOT region can match.
    CHECK(analyzer.su_perm_at(atoms, 0x4000).none());
    CHECK(analyzer.su_perm_at(atoms, 0x0fff) ==
          perm(true, false, false));
    CHECK(analyzer.m_perm_at(atoms, (1ULL << 54u)) ==
          perm(true, false, true));
    CHECK(analyzer.m_perm_at(atoms, top - 1u).none()); // MMWP default deny

    // W^X violation at the lowest possible offending address.
    pmp::PmpConfig wx;
    wx.entries.push_back(
        make_entry(pmp::Mode::Napot, 0b11, false, true, true, true));
    pmp::IsolationSpec spec;
    spec.no_wx.push_back(pmp::SpecRange{0, top - 1u});
    auto v = analyzer.find_violation(wx, spec);
    CHECK(v.has_value());
    CHECK_EQ(v->address, 0u);
    // At address 0 the default M policy (entry -1) already holds W+X.
    CHECK_EQ(v->entry_index, -1);

    // With MMWP the default is clean; the NAPOT entry itself is the culprit.
    pmp::PmpConfig wx_locked = wx;
    wx_locked.mseccfg.mmwp = true;
    v = analyzer.find_violation(wx_locked, spec);
    CHECK(v.has_value());
    CHECK_EQ(v->entry_index, 0);
}

// ----------------------------------------------------------------------
// Minimum partition and lowest-address counterexample guarantees
// ----------------------------------------------------------------------

void test_minimal_partition_and_lowest_violation() {
    pmp::PmpAnalyzer analyzer(12);
    pmp::PmpConfig cfg;
    cfg.mseccfg.mml = true;
    // NAPOT [0,64), NA4 [128,132), TOR [256,384): five distinct endpoints
    // plus 0 and top -> exactly 4 atomic intervals.
    cfg.entries.push_back(
        make_entry(pmp::Mode::Napot, 0b1111, false, true, true, true));
    cfg.entries.push_back(
        make_entry(pmp::Mode::Na4, 32, false, true, false, false));
    cfg.entries.push_back(
        make_entry(pmp::Mode::Tor, 96, false, true, true, false));
    // Overlapping broad entry; must not introduce additional endpoints.
    cfg.entries.push_back(
        make_entry(pmp::Mode::Tor, 200, false, false, false, true));

    auto atoms = analyzer.analyze(cfg);
    assert_partition_invariants(analyzer, atoms);
    // Endpoints after dedup: 0, 64, 128, 132, 384, 800, 4096 -> 6 atoms
    // (the TOR lower bound 128 coincides with the NA4 region start).
    CHECK_EQ(atoms.size(), static_cast<std::size_t>(6));

    // Two separate M-private violations: [0,64) and [256,384). The solver
    // must return the lowest address and the winning entry index.
    pmp::IsolationSpec spec;
    spec.m_private.push_back(pmp::SpecRange{300, 320}); // later violation
    spec.m_private.push_back(pmp::SpecRange{10, 20});   // earlier violation
    auto v = analyzer.find_violation(cfg, spec);
    CHECK(v.has_value());
    CHECK_EQ(v->address, 10u);
    CHECK_EQ(v->entry_index, 0);
    CHECK(v->kind == pmp::Violation::Kind::MPrivateLeakToSU);

    // Removing the earlier violation surfaces the later one at its start.
    pmp::IsolationSpec later_only;
    later_only.m_private.push_back(pmp::SpecRange{300, 320});
    v = analyzer.find_violation(cfg, later_only);
    CHECK(v.has_value());
    CHECK_EQ(v->address, 300u);
    CHECK_EQ(v->entry_index, 2);
}

void test_sixty_four_entries() {
    pmp::PmpAnalyzer analyzer(12);
    pmp::PmpConfig cfg;
    for (unsigned i = 0; i < 64u; ++i) {
        cfg.entries.push_back(
            make_entry(pmp::Mode::Na4, i * 2u, false, true, true, false));
    }
    CHECK(!analyzer.validate(cfg).has_value());
    auto atoms = analyzer.analyze(cfg);
    assert_partition_invariants(analyzer, atoms);

    pmp::PmpConfig too_many = cfg;
    too_many.entries.push_back(off_entry());
    CHECK(analyzer.validate(too_many).has_value());
}

} // namespace

int main() {
    test_decoder_tor();
    test_decoder_na4();
    test_decoder_napot();
    test_validation();
    test_mml_truth_table();
    test_classic_semantics();
    test_priority_shadowing();
    test_default_policies();
    test_transition_locks();
    test_transition_mseccfg();
    test_violation_m_private();
    test_violation_su_isolated();
    test_violation_wx();
    test_differential_random();
    test_differential_all_mseccfg_modes();
    test_56bit_symbolic();
    test_minimal_partition_and_lowest_violation();
    test_sixty_four_entries();

    std::cout << "checks: " << g_checks << " failures: " << g_failures
              << '\n';
    return g_failures == 0 ? 0 : 1;
}
