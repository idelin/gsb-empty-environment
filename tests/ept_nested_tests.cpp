#include "ept_nested.hpp"

#include <cstdio>
#include <exception>

namespace {

int g_failures = 0;

#define CHECK(condition)                                                       \
    do {                                                                       \
        if (!(condition)) {                                                    \
            ++g_failures;                                                      \
            std::fprintf(stderr, "CHECK failed %s:%d: %s\n", __FILE__,         \
                         __LINE__, #condition);                                \
        }                                                                      \
    } while (0)

#define CHECK_EQUAL(actual, expected)                                          \
    do {                                                                       \
        const auto actual_value_ = (actual);                                  \
        const auto expected_value_ = (expected);                              \
        if (!(actual_value_ == expected_value_)) {                            \
            ++g_failures;                                                      \
            std::fprintf(stderr, "EQUAL failed %s:%d\n", __FILE__, __LINE__);  \
        }                                                                      \
    } while (0)

using namespace eptn;

Permissions allPerms() {
    return Permissions::all();
}

void testPermissionIntersection() {
    Permissions p01{true, true, false, true};
    Permissions p12{true, false, true, true};
    Permissions intersection = p01 & p12;
    CHECK(intersection.r);
    CHECK(!intersection.w);
    CHECK(!intersection.x_sup);
    CHECK(intersection.x_usr);
    CHECK(intersection == Permissions({true, false, false, true}));
}

void testFullTranslationsAndMisconfiguration() {
    EptPageTable ept(Geometry::x86_48());
    CHECK(ept.map(0x100000, 0x200000, allPerms(), PageKind::Small) ==
          MapError::Ok);
    CHECK(ept.map(0x400000, 0x800000, allPerms(), PageKind::Medium) ==
          MapError::Ok);
    CHECK(ept.map(Address(0x40000000), Address(0x80000000), allPerms(),
                  PageKind::Large) == MapError::Ok);

    WalkResult small = ept.walk(0x100123);
    CHECK(small.status == WalkStatus::Mapped);
    CHECK_EQUAL(small.hpa, Address(0x200123));
    CHECK(small.permissions == Permissions::all());

    WalkResult medium = ept.walk(0x412345);
    CHECK(medium.status == WalkStatus::Mapped);
    CHECK_EQUAL(medium.hpa, Address(0x812345));
    CHECK_EQUAL(medium.leaf_level, 2U);

    WalkResult large = ept.walk(Address(0x40123456));
    CHECK(large.status == WalkStatus::Mapped);
    CHECK_EQUAL(large.hpa, Address(0x80123456));
    CHECK_EQUAL(large.leaf_level, 3U);

    WalkResult absent = ept.walk(0x200000);
    CHECK(absent.status == WalkStatus::Unmapped);

    WalkResult mapped = ept.walk(0x100000);
    CHECK(mapped.witness.count == 4);
    const WitnessStep& pte =
        mapped.witness.steps[mapped.witness.count - 1U];
    ept.rawEntry(pte.node_id, pte.index) |= Address(1) << 50;
    WalkResult reserved = ept.walk(0x100000);
    CHECK(reserved.status == WalkStatus::Misconfigured);
    CHECK(reserved.reason == MisconfigReason::ReservedBits);
    CHECK(reserved.reserved_bits != 0);

    ept.rawEntry(pte.node_id, pte.index) &= ~ept.geometry().bit_r;
    ept.rawEntry(pte.node_id, pte.index) |= ept.geometry().bit_w;
    WalkResult write_no_read = ept.walk(0x100000);
    CHECK(write_no_read.status == WalkStatus::Misconfigured);
    CHECK(write_no_read.reason == MisconfigReason::WriteWithoutRead);

    const WitnessStep& pml4e = small.witness.steps[0];
    ept.rawEntry(pml4e.node_id, pml4e.index) |= Address(1) << 3;
    WalkResult parent_reserved = ept.walk(0x100000);
    CHECK(parent_reserved.status == WalkStatus::Misconfigured);
    CHECK(parent_reserved.reason == MisconfigReason::ReservedBits);
    CHECK_EQUAL(parent_reserved.witness.count, 1U);
}

void testExactLargePageSplitAndVerification() {
    EptPageTable ept01(Geometry::x86_48());
    EptPageTable ept12(Geometry::x86_48());

    CHECK(ept01.map(0x800000, 0x2000000, allPerms(), PageKind::Medium) ==
          MapError::Ok);
    CHECK(ept01.map(0x800000, 0x3000000,
                    Permissions{true, false, false, false},
                    PageKind::Small) == MapError::Ok);
    CHECK(ept01.map(0x801000, 0x3001000,
                    Permissions{true, true, true, false},
                    PageKind::Small) == MapError::Ok);

    CHECK(ept12.map(0x400000, 0x800000, allPerms(), PageKind::Medium) ==
          MapError::Ok);

    ComposeResult composed = composeExact(ept01, ept12);
    CHECK(composed.ok);
    if (!composed.ok) {
        std::fprintf(stderr, "compose failed: %s\n",
                     composed.message.c_str());
        return;
    }

    WalkResult first = composed.ept02.walk(0x400000);
    CHECK(first.status == WalkStatus::Mapped);
    CHECK_EQUAL(first.hpa, Address(0x3000000));
    CHECK(first.permissions ==
          Permissions({true, false, false, false}));

    WalkResult second = composed.ept02.walk(0x4010f0);
    CHECK(second.status == WalkStatus::Mapped);
    CHECK_EQUAL(second.hpa, Address(0x30010f0));
    CHECK(second.permissions ==
          Permissions({true, true, true, false}));

    WalkResult rest = composed.ept02.walk(0x402345);
    CHECK(rest.status == WalkStatus::Mapped);
    CHECK_EQUAL(rest.hpa, Address(0x2002345));
    CHECK(rest.permissions == Permissions::all());

    VerificationReport report = verifyCandidate(
        ept01, ept12, composed.ept02,
        VerifyOptions::exactComposition());
    if (!report.clean()) {
        for (const Violation& violation : report.violations) {
            std::fprintf(stderr, "unexpected %s at %s\n%s\n",
                         violationKindName(violation.kind),
                         hex(violation.l2_gpa).c_str(),
                         formatWitnessBundle(violation.witness).c_str());
        }
    }
    CHECK(report.clean());
}

void testMbecEscalationWitness() {
    EptPageTable ept01(Geometry::x86_48());
    EptPageTable ept12(Geometry::x86_48());
    CHECK(ept01.map(0x1000, 0x2000,
                    Permissions{true, false, false, false},
                    PageKind::Small) == MapError::Ok);
    CHECK(ept12.map(0x3000, 0x1000,
                    Permissions{true, false, false, true},
                    PageKind::Small) == MapError::Ok);

    ComposeResult composed = composeExact(ept01, ept12);
    CHECK(composed.ok);
    WalkResult walk = composed.ept02.walk(0x3000);
    CHECK(walk.status == WalkStatus::Mapped);
    CHECK(!walk.permissions.x_usr);

    const WitnessStep& pte = walk.witness.steps[walk.witness.count - 1U];
    composed.ept02.rawEntry(pte.node_id, pte.index) |=
        composed.ept02.geometry().bit_x_usr;

    VerificationReport report = verifyCandidate(
        ept01, ept12, composed.ept02, VerifyOptions::securityOnly());
    CHECK(!report.clean());
    CHECK(report.lowest_gpa == 0x3000);
    bool found_escalation = false;
    for (const Violation& violation : report.violations) {
        if (violation.kind == ViolationKind::PermissionEscalation) {
            found_escalation = true;
            CHECK(violation.diff_permissions.x_usr);
            CHECK(violation.security_breach);
        }
    }
    CHECK(found_escalation);
    CHECK(report.lowest_witness.ept02.witness.count == 4);
}

void testReservedAndHpaAttacks() {
    EptPageTable ept01(Geometry::x86_48());
    EptPageTable ept12(Geometry::x86_48());
    CHECK(ept01.map(0x1000, 0x2000, allPerms(), PageKind::Small) ==
          MapError::Ok);
    CHECK(ept12.map(0x3000, 0x1000, allPerms(), PageKind::Small) ==
          MapError::Ok);
    ComposeResult composed = composeExact(ept01, ept12);
    CHECK(composed.ok);

    WalkResult walk = composed.ept02.walk(0x3000);
    const WitnessStep& pte = walk.witness.steps[walk.witness.count - 1U];
    composed.ept02.rawEntry(pte.node_id, pte.index) |= Address(1) << 51;

    VerificationReport reserved_report = verifyCandidate(
        ept01, ept12, composed.ept02, VerifyOptions::securityOnly());
    CHECK(reserved_report.lowest_gpa == 0x3000);
    CHECK(!reserved_report.violations.empty());
    CHECK(reserved_report.violations[0].kind ==
          ViolationKind::CandidateMisconfiguration);
    CHECK(reserved_report.lowest_witness.ept02.reason ==
          MisconfigReason::ReservedBits);

    composed.ept02.rawEntry(pte.node_id, pte.index) &=
        ~(Address(1) << 51);
    composed.ept02.rawEntry(pte.node_id, pte.index) ^= Address(1) << 12;
    VerificationReport hpa_report = verifyCandidate(
        ept01, ept12, composed.ept02,
        VerifyOptions::exactComposition());
    bool hpa_mismatch = false;
    for (const Violation& violation : hpa_report.violations) {
        if (violation.kind == ViolationKind::HpaMismatch) {
            hpa_mismatch = true;
            CHECK(violation.security_breach);
        }
    }
    CHECK(hpa_mismatch);
}

void testUnalignedLargePageMisconfiguration() {
    EptPageTable ept(Geometry::x86_48());
    CHECK(ept.map(0x400000, 0x800000, allPerms(), PageKind::Medium) ==
          MapError::Ok);

    WalkResult valid = ept.walk(0x400000);
    CHECK(valid.status == WalkStatus::Mapped);
    const WitnessStep& pde =
        valid.witness.steps[valid.witness.count - 1U];
    CHECK_EQUAL(pde.level, 2U);

    ept.rawEntry(pde.node_id, pde.index) |= Address(1) << 12;
    WalkResult misconfigured = ept.walk(0x400000);
    CHECK(misconfigured.status == WalkStatus::Misconfigured);
    CHECK(misconfigured.reason == MisconfigReason::UnalignedPage);

    const std::vector<FlatRange> ranges = ept.flatten();
    const FlatRange* range = detail::rangeAt(ranges, 0x400000);
    CHECK(range != nullptr);
    if (range != nullptr) {
        CHECK(range->status == WalkStatus::Misconfigured);
        CHECK(range->reason == MisconfigReason::UnalignedPage);
    }
}

void testSymbolicHighAddressLowest() {
    EptPageTable ept01(Geometry::x86_48());
    EptPageTable ept12(Geometry::x86_48());
    const Address high_l2 = Address(0x80) << 40;
    const Address high_l1 = Address(0x40) << 40;
    const Address high_hpa = Address(0x20) << 40;

    CHECK(ept01.map(high_l1, high_hpa,
                    Permissions{true, true, false, false},
                    PageKind::Large) == MapError::Ok);
    CHECK(ept12.map(high_l2, high_l1,
                    Permissions{true, true, true, true},
                    PageKind::Large) == MapError::Ok);

    ComposeResult composed = composeExact(ept01, ept12);
    CHECK(composed.ok);
    VerificationReport clean = verifyCandidate(
        ept01, ept12, composed.ept02,
        VerifyOptions::exactComposition());
    CHECK(clean.clean());
    CHECK(clean.symbolic_ranges > 0);

    WalkResult walk = composed.ept02.walk(high_l2);
    CHECK(walk.status == WalkStatus::Mapped);
    CHECK_EQUAL(walk.hpa, high_hpa);
    const WitnessStep& leaf =
        walk.witness.steps[walk.witness.count - 1U];
    composed.ept02.rawEntry(leaf.node_id, leaf.index) |=
        composed.ept02.geometry().bit_x_usr;

    VerificationReport report = verifyCandidate(
        ept01, ept12, composed.ept02, VerifyOptions::securityOnly());
    CHECK_EQUAL(report.lowest_gpa, high_l2);
    CHECK(!report.violations.empty());
    CHECK_EQUAL(report.violations.front().l2_end,
                high_l2 + Geometry::x86_48().pageSize(PageKind::Large));
    CHECK(report.lowest_witness.ept02.witness.count >= 2);
}

void testMiniLargeSplit() {
    Geometry mini = Geometry::mini16();
    EptPageTable ept01(mini);
    EptPageTable ept12(mini);

    CHECK(ept01.map(0x0800, 0x2000, allPerms(), PageKind::Medium) ==
          MapError::Ok);
    CHECK(ept01.map(0x0800, 0x3000,
                    Permissions{true, false, false, false},
                    PageKind::Small) == MapError::Ok);
    CHECK(ept12.map(0x0400, 0x0800, allPerms(), PageKind::Medium) ==
          MapError::Ok);

    ComposeResult composed = composeExact(ept01, ept12);
    CHECK(composed.ok);
    WalkResult first = composed.ept02.walk(0x0400);
    CHECK(first.status == WalkStatus::Mapped);
    CHECK_EQUAL(first.hpa, Address(0x3000));
    CHECK(first.permissions ==
          Permissions({true, false, false, false}));

    WalkResult next = composed.ept02.walk(0x0500);
    CHECK(next.status == WalkStatus::Mapped);
    CHECK_EQUAL(next.hpa, Address(0x2100));
    CHECK(next.permissions == Permissions::all());

    VerificationReport report = verifyCandidate(
        ept01, ept12, composed.ept02,
        VerifyOptions::exactComposition());
    CHECK(report.clean());
}

void testMbecAccessDecisions() {
    EptPageTable ept(Geometry::x86_48());
    Permissions permissions{true, true, true, false};
    CHECK(ept.map(0x4000, 0x8000, permissions, PageKind::Small) ==
          MapError::Ok);

    CHECK(checkAccess(ept, 0x4000, AccessRequest::readAccess()).allowed);
    CHECK(checkAccess(ept, 0x4000, AccessRequest::writeAccess()).allowed);
    CHECK(checkAccess(ept, 0x4000,
                      AccessRequest::supervisorFetch()).allowed);

    AccessDecision user_fetch =
        checkAccess(ept, 0x4000, AccessRequest::userFetch());
    CHECK(!user_fetch.allowed);
    CHECK(user_fetch.fault == AccessFault::EptViolation);

    WalkResult mapped = ept.walk(0x4000);
    CHECK(mapped.witness.count == 4);
    const WitnessStep& pte =
        mapped.witness.steps[mapped.witness.count - 1U];
    ept.rawEntry(pte.node_id, pte.index) |= Address(1) << 49;
    AccessDecision misconfig =
        checkAccess(ept, 0x4000, AccessRequest::readAccess());
    CHECK(!misconfig.allowed);
    CHECK(misconfig.fault == AccessFault::EptMisconfiguration);
}

void testReadOnlyExecutionEscalation() {
    EptPageTable ept01(Geometry::x86_48());
    EptPageTable ept12(Geometry::x86_48());
    CHECK(ept01.map(0x1000, 0x2000,
                    Permissions{true, false, false, false},
                    PageKind::Small) == MapError::Ok);
    CHECK(ept12.map(0x3000, 0x1000, Permissions::all(),
                    PageKind::Small) == MapError::Ok);

    ComposeResult composed = composeExact(ept01, ept12);
    CHECK(composed.ok);
    const WalkResult clean = composed.ept02.walk(0x3000);
    CHECK(clean.permissions ==
          Permissions({true, false, false, false}));
    CHECK(checkAccess(clean, AccessRequest::readAccess()).allowed);
    CHECK(!checkAccess(clean, AccessRequest::writeAccess()).allowed);
    CHECK(!checkAccess(clean, AccessRequest::supervisorFetch()).allowed);
    CHECK(!checkAccess(clean, AccessRequest::userFetch()).allowed);

    const WitnessStep& pte =
        clean.witness.steps[clean.witness.count - 1U];
    Address& raw = composed.ept02.rawEntry(pte.node_id, pte.index);
    raw |= composed.ept02.geometry().bit_x_sup |
           composed.ept02.geometry().bit_x_usr;

    VerificationReport report = verifyCandidate(
        ept01, ept12, composed.ept02, VerifyOptions::securityOnly());
    CHECK_EQUAL(report.lowest_gpa, 0x3000);
    bool escalation = false;
    for (const Violation& violation : report.violations) {
        if (violation.kind == ViolationKind::PermissionEscalation) {
            escalation = true;
            CHECK(violation.diff_permissions.x_sup);
            CHECK(violation.diff_permissions.x_usr);
            CHECK(violation.security_breach);
        }
    }
    CHECK(escalation);
}

void testSourceReservedBitWitness() {
    EptPageTable ept01(Geometry::x86_48());
    EptPageTable ept12(Geometry::x86_48());
    CHECK(ept01.map(0x1000, 0x2000, allPerms(), PageKind::Small) ==
          MapError::Ok);
    CHECK(ept12.map(0x3000, 0x1000, allPerms(), PageKind::Small) ==
          MapError::Ok);

    WalkResult source = ept01.walk(0x1000);
    const WitnessStep& pte =
        source.witness.steps[source.witness.count - 1U];
    ept01.rawEntry(pte.node_id, pte.index) |= Address(1) << 50;
    CHECK(ept01.walk(0x1000).status == WalkStatus::Misconfigured);

    ComposeResult composed = composeExact(ept01, ept12);
    CHECK(composed.ok);
    CHECK(composed.ept02.walk(0x3000).status == WalkStatus::Unmapped);

    VerificationReport report = verifyCandidate(
        ept01, ept12, composed.ept02,
        VerifyOptions::exactComposition());
    bool source_fault = false;
    for (const Violation& violation : report.violations) {
        if (violation.kind == ViolationKind::SourceMisconfiguration) {
            source_fault = true;
            CHECK(violation.l2_gpa == 0x3000);
            CHECK(violation.misconfig_reason ==
                  MisconfigReason::ReservedBits);
        }
    }
    CHECK(source_fault);
    CHECK(!report.clean());
    CHECK(report.securityOk());
    CHECK(report.lowest_witness.has_ept01);
    CHECK(report.lowest_witness.ept01.status ==
          WalkStatus::Misconfigured);
}

void testComposerInterface() {
    EptPageTable ept01(Geometry::x86_48());
    EptPageTable ept12(Geometry::x86_48());
    CHECK(ept01.map(0x1000, 0x2000, allPerms(), PageKind::Small) ==
          MapError::Ok);
    CHECK(ept12.map(0x3000, 0x1000, allPerms(), PageKind::Small) ==
          MapError::Ok);

    VerificationReport report = verifyComposer(
        ept01, ept12,
        [](const EptPageTable& left, const EptPageTable& right) {
            return composeExact(left, right).ept02;
        },
        VerifyOptions::exactComposition());
    CHECK(report.clean());
}

void test16BitExhaustiveCrossValidation() {
    ExhaustiveReport exhaustive = run16BitExhaustiveCrossCheck(80);
    if (!exhaustive.ok) {
        std::fprintf(stderr, "exhaustive failure: %s\n",
                     exhaustive.error.c_str());
    }
    CHECK(exhaustive.ok);
    CHECK(exhaustive.cases == 80);
    CHECK(exhaustive.pages_checked == 80U * 256U);
    CHECK(exhaustive.point_checks == 80U * 256U);
    CHECK(exhaustive.byte_checks == 80ULL * 65536ULL);
    std::printf("16-bit exhaustive: %u cases, %llu page-checks, "
                "%llu point-checks, %llu byte-checks, zero-error=%d\n",
                exhaustive.cases,
                static_cast<unsigned long long>(exhaustive.pages_checked),
                static_cast<unsigned long long>(exhaustive.point_checks),
                static_cast<unsigned long long>(exhaustive.byte_checks),
                exhaustive.ok ? 1 : 0);
}

} // namespace

int main() {
    struct TestCase {
        const char* name;
        void (*function)();
    };
    const TestCase tests[] = {
        {"permission-intersection", testPermissionIntersection},
        {"full-translations", testFullTranslationsAndMisconfiguration},
        {"exact-large-split", testExactLargePageSplitAndVerification},
        {"mbec-witness", testMbecEscalationWitness},
        {"reserved-hpa-attacks", testReservedAndHpaAttacks},
        {"unaligned-large-page", testUnalignedLargePageMisconfiguration},
        {"symbolic-high-address", testSymbolicHighAddressLowest},
        {"mini-large-split", testMiniLargeSplit},
        {"mbec-access-decisions", testMbecAccessDecisions},
        {"readonly-execution-escalation", testReadOnlyExecutionEscalation},
        {"source-reserved-witness", testSourceReservedBitWitness},
        {"composer-interface", testComposerInterface},
        {"16bit-exhaustive", test16BitExhaustiveCrossValidation},
    };

    for (const TestCase& test : tests) {
        const int before = g_failures;
        try {
            test.function();
        } catch (const std::exception& exception) {
            ++g_failures;
            std::fprintf(stderr, "EXCEPTION %s: %s\n", test.name,
                         exception.what());
        } catch (...) {
            ++g_failures;
            std::fprintf(stderr, "UNKNOWN EXCEPTION %s\n", test.name);
        }
        std::printf("[%s] %s\n",
                    before == g_failures ? "PASS" : "FAIL", test.name);
    }

    if (g_failures != 0) {
        std::fprintf(stderr, "%d check(s) failed\n", g_failures);
        return 1;
    }
    std::puts("All EPT nested composition checks passed.");
    return 0;
}
