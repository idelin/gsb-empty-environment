#include "test_util.hpp"

#include <cstdint>
#include <vector>

using namespace testutil;

namespace {

void assert_matches_oracle(
    const gsb::TrustSnapshot& snapshot,
    const std::vector<gsb::FragmentRecord>& records) {
    gsb::ReassemblyEngine engine;
    gsb::QueryReport report = engine.query(snapshot, records);
    std::optional<OracleChoice> expected = oracle(snapshot, records);

    if (!expected.has_value()) {
        CHECK(!report.deliverable);
        return;
    }
    CHECK(report.deliverable);
    CHECK(report.certificate.has_value());
    CHECK(report.certificate->records.size() == expected->count);
    CHECK(report.certificate->total_referenced_bytes == expected->bytes);
    CHECK(report.certificate->version.value == expected->version);
    std::vector<std::uint64_t> actual_ids;
    for (const gsb::FragmentRecord& fragment :
         report.certificate->records) {
        actual_ids.push_back(fragment.id.value);
    }
    CHECK(actual_ids == expected->ids);
}

}  // namespace

void test_exhaustive_subset_oracle() {
    gsb::TrustSnapshot snapshot = two_leaf_snapshot();
    gsb::Digest content = digest("oracle-content");

    // Nine records with mixed spans, nested intervals and two versions so the
    // engine must consider every subset (up to 511) like the oracle.
    std::vector<gsb::FragmentRecord> records = {
        record(1, 1, 1, 0, 100, content, 0, 200),
        record(2, 1, 2, 0, 100, content, 0, 200),
        record(3, 1, 3, 100, 200, content, 0, 200),
        record(4, 1, 4, 100, 200, content, 0, 200),
        record(5, 1, 5, 20, 80, digest("nested"), 0, 200),
        record(6, 1, 1, 120, 180, content, 0, 200),
        record(7, 2, 2, 0, 60, content, 0, 200),
        record(8, 2, 3, 60, 140, content, 0, 200),
        record(9, 2, 4, 140, 200, content, 0, 200),
    };
    snapshot.trusted_root =
        root_of(snapshot, gsb::Version{1}, records);
    assert_matches_oracle(snapshot, records);

    // Remove the trusted root: nothing should be deliverable.
    gsb::TrustSnapshot untrusted = snapshot;
    untrusted.trusted_root = digest("a-different-root");
    assert_matches_oracle(untrusted, records);

    // A small partial-authority layout where only some subsets cover leaves.
    gsb::TrustSnapshot partial = snapshot;
    std::vector<gsb::FragmentRecord> partial_records = {
        record(11, 1, 1, 0, 50, content, 0, 50),
        record(12, 1, 2, 0, 100, content, 0, 100),
        record(13, 1, 3, 100, 150, content, 100, 150),
        record(14, 1, 4, 100, 200, content, 100, 200),
        record(15, 1, 5, 50, 100, content, 0, 100),
        record(16, 1, 6, 150, 200, content, 100, 200),
    };
    partial.trusted_root =
        root_of(partial, gsb::Version{1}, partial_records);
    assert_matches_oracle(partial, partial_records);
}
