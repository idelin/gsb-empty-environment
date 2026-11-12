#include "netsec.hpp"

#include <cstdint>
#include <iostream>

namespace {

using netsec::AccessType;
using netsec::PmpAccess;
using netsec::PmpDecision;
using netsec::PmpEntry;
using netsec::PmpEvaluator;
using netsec::PmpMode;
using netsec::PrivilegeMode;

int failures = 0;

void expect(bool condition, const char* description) {
    if (!condition) {
        std::cerr << "FAIL: " << description << '\n';
        ++failures;
    }
}

PmpEntry makeEntry(std::uint64_t pmpaddr, PmpMode mode, bool locked,
                   bool read, bool write, bool execute) {
    return PmpEntry{pmpaddr, mode, read, write, execute, locked};
}

PmpDecision evaluate(const PmpEvaluator::EntryArray& entries,
                     std::uint64_t address, std::uint64_t size,
                     PrivilegeMode privilege, AccessType type) {
    return netsec::evaluatePmpAccess(
        entries, PmpAccess{address, size, privilege, type});
}

void testFirstCoveringEntryDecides() {
    PmpEvaluator::EntryArray entries{};
    // NAPOT pmpaddr=0x100 has zero trailing ones: [0x400, 0x407].
    entries[0] = makeEntry(0x100, PmpMode::Napot, false, true, false, false);
    // The second entry covers the whole address space but must not override
    // a permission decision made by entry zero.
    entries[1] = makeEntry(UINT64_MAX, PmpMode::Napot, false, true, true, true);

    expect(evaluate(entries, 0x401, 4, PrivilegeMode::S, AccessType::Read) ==
               PmpDecision::Allow,
           "first full-covering entry grants a permitted read");
    expect(evaluate(entries, 0x401, 4, PrivilegeMode::S, AccessType::Write) ==
               PmpDecision::Deny,
           "later permissive entry cannot override first entry denial");
}

void testPartialOverlapFails() {
    PmpEvaluator::EntryArray entries{};
    entries[0] = makeEntry(0x100, PmpMode::Napot, false, true, false, false);
    entries[1] = makeEntry(UINT64_MAX, PmpMode::Napot, false, true, true, true);

    expect(evaluate(entries, 0x3fe, 4, PrivilegeMode::S, AccessType::Read) ==
               PmpDecision::Deny,
           "access straddling an entry start fails");
    expect(evaluate(entries, 0x407, 2, PrivilegeMode::S, AccessType::Read) ==
               PmpDecision::Deny,
           "access straddling an entry end fails");
    expect(evaluate(entries, 0x404, 4, PrivilegeMode::S, AccessType::Read) ==
               PmpDecision::Allow,
           "access ending exactly at an entry end is fully covered");
}

void testTorFirstEntryAndPreviousLowerBound() {
    PmpEvaluator::EntryArray entries{};
    // A TOR entry zero has an implicit lower bound of address zero.
    entries[0] = makeEntry(0x102, PmpMode::Tor, true, true, false, false);
    expect(evaluate(entries, 0, 8, PrivilegeMode::M, AccessType::Read) ==
               PmpDecision::Allow,
           "TOR entry zero starts at address zero");
    expect(evaluate(entries, 0x3f8, 16, PrivilegeMode::S, AccessType::Read) ==
               PmpDecision::Allow,
           "TOR upper bound is exclusive");

    PmpEvaluator::EntryArray chained{};
    // The previous pmpaddr is the lower bound even when the previous entry
    // itself is OFF.
    chained[0] = makeEntry(0x110, PmpMode::Off, false, false, false, false);
    chained[1] = makeEntry(0x120, PmpMode::Tor, false, true, false, false);
    expect(evaluate(chained, 0x440, 1, PrivilegeMode::S, AccessType::Read) ==
               PmpDecision::Allow,
           "TOR lower bound is previous pmpaddr even if previous mode is OFF");
    expect(evaluate(chained, 0x43f, 1, PrivilegeMode::S, AccessType::Read) ==
               PmpDecision::Deny,
           "TOR range excludes bytes below previous pmpaddr");
    expect(evaluate(chained, 0x480, 1, PrivilegeMode::S, AccessType::Read) ==
               PmpDecision::Deny,
           "TOR upper bound is exclusive in byte addressing");
}

void testNapotEncodingBoundaries() {
    PmpEvaluator::EntryArray zeroOnes{};
    zeroOnes[0] =
        makeEntry(0x100, PmpMode::Napot, false, true, false, false);
    expect(evaluate(zeroOnes, 0x400, 8, PrivilegeMode::S,
                    AccessType::Read) == PmpDecision::Allow,
           "NAPOT with zero trailing ones spans 8 bytes");
    expect(evaluate(zeroOnes, 0x408, 1, PrivilegeMode::S,
                    AccessType::Read) == PmpDecision::Deny,
           "NAPOT with zero trailing ones ends at pmpaddr*4+8");

    PmpEvaluator::EntryArray oneOne{};
    oneOne[0] = makeEntry(0x101, PmpMode::Napot, false, true, false, false);
    expect(evaluate(oneOne, 0x400, 16, PrivilegeMode::S, AccessType::Read) ==
               PmpDecision::Allow,
           "NAPOT with one trailing one spans 16 bytes");
    expect(evaluate(oneOne, 0x401, 15, PrivilegeMode::S, AccessType::Read) ==
               PmpDecision::Allow,
           "NAPOT interior start remains fully covered");
    expect(evaluate(oneOne, 0x410, 1, PrivilegeMode::S, AccessType::Read) ==
               PmpDecision::Deny,
           "NAPOT with one trailing one ends at 0x410");

    PmpEvaluator::EntryArray threeOnes{};
    threeOnes[0] =
        makeEntry(0x107, PmpMode::Napot, false, true, false, false);
    expect(evaluate(threeOnes, 0x43f, 1, PrivilegeMode::S,
                    AccessType::Read) == PmpDecision::Allow,
           "NAPOT with three trailing ones includes its final byte");
    expect(evaluate(threeOnes, 0x440, 1, PrivilegeMode::S,
                    AccessType::Read) == PmpDecision::Deny,
           "NAPOT with three trailing ones ends at 0x440");
}

void testNa4() {
    PmpEvaluator::EntryArray entries{};
    entries[0] = makeEntry(0x100, PmpMode::Na4, false, true, false, false);
    expect(evaluate(entries, 0x400, 4, PrivilegeMode::S, AccessType::Read) ==
               PmpDecision::Allow,
           "NA4 covers one four-byte naturally aligned word");
    expect(evaluate(entries, 0x404, 1, PrivilegeMode::S, AccessType::Read) ==
               PmpDecision::Deny,
           "NA4 does not extend past its four-byte word");
}

void testLockedAndUnlockedEntries() {
    PmpEvaluator::EntryArray unlockedDeny{};
    unlockedDeny[0] =
        makeEntry(UINT64_MAX, PmpMode::Napot, false, false, false, false);
    expect(evaluate(unlockedDeny, 0x1000, 4, PrivilegeMode::M,
                    AccessType::Read) == PmpDecision::Allow,
           "unlocked deny entry does not constrain M-mode");
    expect(evaluate(unlockedDeny, 0x1000, 4, PrivilegeMode::S,
                    AccessType::Read) == PmpDecision::Deny,
           "unlocked deny entry constrains S-mode");
    expect(evaluate(unlockedDeny, 0x1000, 4, PrivilegeMode::U,
                    AccessType::Read) == PmpDecision::Deny,
           "unlocked deny entry constrains U-mode");

    PmpEvaluator::EntryArray unlockedPartial{};
    unlockedPartial[0] =
        makeEntry(0x100, PmpMode::Napot, false, false, false, false);
    expect(evaluate(unlockedPartial, 0x3fe, 4, PrivilegeMode::M,
                    AccessType::Read) == PmpDecision::Allow,
           "unlocked partial overlap does not fail an M-mode access");

    PmpEvaluator::EntryArray lockedDeny{};
    lockedDeny[0] =
        makeEntry(UINT64_MAX, PmpMode::Napot, true, false, false, false);
    expect(evaluate(lockedDeny, 0x1000, 4, PrivilegeMode::M,
                    AccessType::Read) == PmpDecision::Deny,
           "locked entry applies to M-mode");

    PmpEvaluator::EntryArray lockedAllow{};
    lockedAllow[0] =
        makeEntry(UINT64_MAX, PmpMode::Napot, true, true, false, false);
    expect(evaluate(lockedAllow, 0x1000, 4, PrivilegeMode::M,
                    AccessType::Read) == PmpDecision::Allow,
           "locked entry can grant M-mode permission");
}

void testNoMatchFallback() {
    PmpEvaluator::EntryArray entries{};
    entries[0] =
        makeEntry(0x100, PmpMode::Napot, false, true, true, true);
    expect(evaluate(entries, 0x800, 4, PrivilegeMode::M, AccessType::Write) ==
               PmpDecision::Allow,
           "M-mode defaults to allow when no entry matches");
    expect(evaluate(entries, 0x800, 4, PrivilegeMode::S, AccessType::Read) ==
               PmpDecision::Deny,
           "S-mode defaults to deny when no entry matches");
    expect(evaluate(entries, 0x800, 4, PrivilegeMode::U, AccessType::Read) ==
               PmpDecision::Deny,
           "U-mode defaults to deny when no entry matches");
}

void testPermissionsAndInvalidAccesses() {
    PmpEvaluator::EntryArray entries{};
    entries[0] =
        makeEntry(0x100, PmpMode::Napot, false, true, true, false);
    expect(evaluate(entries, 0x400, 4, PrivilegeMode::S, AccessType::Read) ==
               PmpDecision::Allow,
           "matching entry grants configured read permission");
    expect(evaluate(entries, 0x400, 4, PrivilegeMode::S, AccessType::Write) ==
               PmpDecision::Allow,
           "matching entry grants configured write permission");
    expect(evaluate(entries, 0x400, 4, PrivilegeMode::S, AccessType::Execute) ==
               PmpDecision::Deny,
           "matching entry denies unconfigured execute permission");
    expect(evaluate(entries, 0, 0, PrivilegeMode::M, AccessType::Read) ==
               PmpDecision::Deny,
           "zero-byte access is rejected");
    expect(evaluate(entries, UINT64_MAX, 2, PrivilegeMode::M,
                    AccessType::Read) == PmpDecision::Deny,
           "access wrapping the address space is rejected");
}

}  // namespace

int main() {
    testFirstCoveringEntryDecides();
    testPartialOverlapFails();
    testTorFirstEntryAndPreviousLowerBound();
    testNapotEncodingBoundaries();
    testNa4();
    testLockedAndUnlockedEntries();
    testNoMatchFallback();
    testPermissionsAndInvalidAccesses();

    if (failures != 0) {
        std::cerr << failures << " test(s) failed\n";
        return 1;
    }

    std::cout << "all PMP evaluator tests passed\n";
    return 0;
}
