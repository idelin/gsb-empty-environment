#include "test_util.hpp"

#include <cstdint>

using namespace testutil;

void test_endpoint_sweep_sparse() {
    gsb::TrustSnapshot snapshot;
    snapshot.generation = 1;
    snapshot.leaves = {
        gsb::ManifestLeaf{
            gsb::ByteRange{1000000000000ULL, 1000000000100ULL}, 1},
    };
    snapshot.key_of_sender = {{7, 707}};
    std::vector<gsb::FragmentRecord> records = {
        record(1, 1, 7, 1000000000000ULL, 1000000000040ULL,
               digest("a"), 1000000000000ULL, 1000000000100ULL),
        record(2, 1, 7, 1000000000040ULL, 1000000000100ULL,
               digest("b"), 1000000000000ULL, 1000000000100ULL),
    };
    snapshot.trusted_root =
        root_of(snapshot, gsb::Version{1}, records);

    gsb::ReassemblyEngine engine;
    gsb::QueryReport report = engine.query(snapshot, records);
    CHECK(report.deliverable);
    CHECK(report.certificate->records.size() == 2);
    CHECK(report.certificate->total_referenced_bytes == 100);
}

void test_cross_leaf_partial_authority() {
    gsb::TrustSnapshot snapshot = two_leaf_snapshot();
    gsb::Digest content = digest("shared-content");
    std::vector<gsb::FragmentRecord> records = {
        record(1, 9, 1, 0, 50, content, 0, 50),
        record(2, 9, 2, 0, 100, content, 0, 100),
        record(7, 9, 6, 50, 100, content, 50, 100),
        record(3, 9, 3, 150, 200, content, 150, 200),
        record(4, 9, 4, 100, 200, content, 100, 200),
        record(8, 9, 5, 100, 150, content, 100, 150),
    };
    snapshot.trusted_root =
        root_of(snapshot, gsb::Version{9}, records);

    gsb::ReassemblyEngine engine;
    gsb::QueryReport report = engine.query(snapshot, records);
    CHECK(report.deliverable);
    CHECK(report.certificate->records.size() == 6);

    std::vector<gsb::FragmentRecord> unauthorized = records;
    unauthorized.push_back(
        record(5, 9, 5, 0, 100, content, 0, 40));
    gsb::QueryReport bad = engine.query(snapshot, unauthorized);
    CHECK(has_rejection(
        bad, gsb::RecordId{5}, gsb::RejectReason::authority_violation));
}

void test_same_bytes_different_versions() {
    gsb::TrustSnapshot snapshot = two_leaf_snapshot();
    gsb::Digest content = digest("same-bytes");
    std::vector<gsb::FragmentRecord> version_one = {
        record(1, 1, 1, 0, 100, content, 0, 200),
        record(2, 1, 2, 0, 100, content, 0, 200),
        record(3, 1, 3, 100, 200, content, 0, 200),
        record(4, 1, 4, 100, 200, content, 0, 200),
    };
    std::vector<gsb::FragmentRecord> records = version_one;
    records.push_back(record(5, 2, 5, 0, 100, content, 0, 200));
    records.push_back(record(6, 2, 6, 100, 200, content, 0, 200));

    snapshot.trusted_root =
        root_of(snapshot, gsb::Version{1}, version_one);

    gsb::ReassemblyEngine engine;
    gsb::QueryReport report = engine.query(snapshot, records);
    CHECK(report.deliverable);
    CHECK(report.certificate->version == gsb::Version{1});
    CHECK(has_conflict(
        report, gsb::RecordId{1}, gsb::RecordId{5}, false));
    CHECK(has_conflict(
        report, gsb::RecordId{4}, gsb::RecordId{6}, false));
    // Version 2 still has only one distinct principal on each leaf.
    CHECK(!report.deficits.empty());
}
