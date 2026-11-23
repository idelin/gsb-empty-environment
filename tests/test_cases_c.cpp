#include "test_util.hpp"

#include <cstdint>
#include <stdexcept>

using namespace testutil;

namespace {

std::vector<gsb::FragmentRecord> lease_records() {
    gsb::Digest content = digest("lease-content");
    return {
        record(1, 1, 1, 0, 100, content, 0, 200),
        record(2, 1, 2, 0, 100, content, 0, 200),
        record(3, 1, 3, 100, 200, content, 0, 200),
        record(4, 1, 4, 100, 200, content, 0, 200),
    };
}

}  // namespace

void test_revocation_and_old_lease_readback() {
    gsb::TrustSnapshot current = two_leaf_snapshot();
    std::vector<gsb::FragmentRecord> records = lease_records();
    current.trusted_root =
        root_of(current, gsb::Version{1}, records);

    gsb::ReassemblyEngine engine;
    gsb::QueryReport issued = engine.query(current, records);
    CHECK(issued.deliverable);
    gsb::Certificate certificate = *issued.certificate;

    gsb::CertificateVerification old_readback =
        engine.verify_certificate(certificate);
    CHECK(old_readback.valid);

    // New snapshot revokes sender 1 with a host-injected timestamp.
    gsb::TrustSnapshot revoked = current;
    revoked.generation = 2;
    revoked.revocation_times[1] = 424242;

    gsb::QueryReport after_revocation = engine.query(revoked, records);
    CHECK(!after_revocation.deliverable);
    CHECK(has_rejection(
        after_revocation,
        gsb::RecordId{1},
        gsb::RejectReason::revoked_sender));
    bool stamped = false;
    for (const gsb::RejectionEvidence& evidence :
         after_revocation.rejected) {
        if (evidence.record == gsb::RecordId{1}) {
            stamped = evidence.revocation_timestamp == 424242;
        }
    }
    CHECK(stamped);

    // The previously issued certificate still verifies under snapshot 1.
    gsb::CertificateVerification old_lease =
        engine.verify_certificate(certificate);
    CHECK(old_lease.valid);
    CHECK(certificate.snapshot.generation == 1);
}

void test_threshold_change_snapshot_isolation() {
    gsb::TrustSnapshot snapshot = two_leaf_snapshot();
    std::vector<gsb::FragmentRecord> records = lease_records();
    snapshot.trusted_root =
        root_of(snapshot, gsb::Version{1}, records);

    gsb::ReassemblyEngine engine;
    gsb::QueryReport issued = engine.query(snapshot, records);
    CHECK(issued.deliverable);
    gsb::Certificate certificate = *issued.certificate;

    gsb::TrustSnapshot stricter = snapshot;
    stricter.generation = 3;
    for (gsb::ManifestLeaf& leaf : stricter.leaves) {
        leaf.threshold = 3;
    }
    gsb::QueryReport re_judged = engine.query(stricter, records);
    CHECK(!re_judged.deliverable);
    for (const gsb::CellDeficit& deficit : re_judged.deficits) {
        CHECK(deficit.required == 3);
        CHECK(deficit.actual == 2);
    }

    gsb::CertificateVerification old_lease =
        engine.verify_certificate(certificate);
    CHECK(old_lease.valid);
}

void test_host_hooks_injection() {
    gsb::TrustSnapshot snapshot = two_leaf_snapshot();
    std::vector<gsb::FragmentRecord> records = lease_records();
    snapshot.trusted_root =
        root_of(snapshot, gsb::Version{1}, records);

    gsb::HostHooks hooks = gsb::default_hooks();
    int signature_checks = 0;
    hooks.verify_signature =
        [&](const gsb::FragmentRecord& record,
            const gsb::TrustSnapshot&) {
            ++signature_checks;
            return record.id != gsb::RecordId{2};
        };
    gsb::ReassemblyEngine strict(hooks);
    gsb::QueryReport report = strict.query(snapshot, records);
    CHECK(signature_checks == static_cast<int>(records.size()));
    CHECK(has_rejection(
        report, gsb::RecordId{2}, gsb::RejectReason::bad_signature));
    CHECK(!report.deliverable);
}

void test_candidate_cap_and_duplicates() {
    gsb::TrustSnapshot snapshot = two_leaf_snapshot();
    std::vector<gsb::FragmentRecord> records;
    for (std::uint64_t i = 1; i <= 23; ++i) {
        records.push_back(record(
            i, 1, 1 + (i % 5), 0, 100, digest("x"), 0, 200));
    }
    gsb::ReassemblyEngine engine;
    bool threw = false;
    try {
        (void)engine.query(snapshot, records);
    } catch (const std::invalid_argument&) {
        threw = true;
    }
    CHECK(threw);

    std::vector<gsb::FragmentRecord> duplicate = lease_records();
    duplicate.push_back(duplicate.front());
    bool duplicate_threw = false;
    try {
        (void)engine.query(snapshot, duplicate);
    } catch (const std::invalid_argument&) {
        duplicate_threw = true;
    }
    CHECK(duplicate_threw);
}

void test_certificate_tamper_detection() {
    gsb::TrustSnapshot snapshot = two_leaf_snapshot();
    std::vector<gsb::FragmentRecord> records = lease_records();
    snapshot.trusted_root =
        root_of(snapshot, gsb::Version{1}, records);

    gsb::ReassemblyEngine engine;
    gsb::Certificate certificate = *engine.query(snapshot, records).certificate;
    CHECK(engine.verify_certificate(certificate).valid);

    gsb::Certificate tampered = certificate;
    tampered.records.front().digest = digest("tampered");
    gsb::CertificateVerification result =
        engine.verify_certificate(tampered);
    CHECK(!result.valid);

    gsb::Certificate foreign_snapshot = certificate;
    foreign_snapshot.snapshot.generation = 99;
    foreign_snapshot.snapshot.trusted_root = digest("foreign-root");
    CHECK(!engine.verify_certificate(foreign_snapshot).valid);
}
