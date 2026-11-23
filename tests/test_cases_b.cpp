#include "test_util.hpp"

#include <cstdint>

using namespace testutil;

void test_alias_duplicate_votes() {
    gsb::TrustSnapshot snapshot = two_leaf_snapshot();
    snapshot.key_of_sender[6] = 101;
    gsb::Digest content = digest("alias-content");
    std::vector<gsb::FragmentRecord> records = {
        record(1, 1, 1, 0, 100, content, 0, 200),
        record(2, 1, 6, 0, 100, content, 0, 200),
        record(3, 1, 3, 100, 200, content, 0, 200),
        record(4, 1, 4, 100, 200, content, 0, 200),
    };

    gsb::ReassemblyEngine engine;
    gsb::QueryReport report = engine.query(snapshot, records);
    CHECK(!report.deliverable);
    bool leaf_zero_deficit = false;
    for (const gsb::CellDeficit& deficit : report.deficits) {
        if (deficit.cell.begin < 100) {
            leaf_zero_deficit = true;
            CHECK(deficit.actual == 1);
            CHECK(deficit.required == 2);
        }
    }
    CHECK(leaf_zero_deficit);

    records.push_back(record(5, 1, 2, 0, 100, content, 0, 200));
    snapshot.trusted_root =
        root_of(snapshot, gsb::Version{1}, records);
    gsb::QueryReport fixed = engine.query(snapshot, records);
    CHECK(fixed.deliverable);
}

void test_nested_intervals() {
    gsb::TrustSnapshot snapshot = two_leaf_snapshot();
    gsb::Digest outer = digest("outer");
    gsb::Digest inner = digest("inner");
    std::vector<gsb::FragmentRecord> records = {
        record(1, 1, 1, 0, 100, outer, 0, 200),
        record(2, 1, 2, 20, 60, inner, 0, 200),
        record(3, 1, 3, 0, 100, outer, 0, 200),
        record(4, 1, 1, 100, 200, outer, 0, 200),
        record(5, 1, 2, 100, 200, outer, 0, 200),
    };
    snapshot.trusted_root =
        root_of(snapshot, gsb::Version{1}, records);

    gsb::ReassemblyEngine engine;
    gsb::QueryReport report = engine.query(snapshot, records);
    CHECK(report.deliverable);
    // Nested same-version fragments with different digests are not conflicts.
    CHECK(report.conflicts.empty());

    // A partially overlapping different digest is a retained hard conflict.
    std::vector<gsb::FragmentRecord> clashing = records;
    clashing.push_back(
        record(6, 1, 4, 50, 90, digest("clash"), 0, 200));
    gsb::QueryReport clash_report = engine.query(snapshot, clashing);
    // Record 6 partially overlaps the nested record 2; it is fully inside
    // record 1, which is a nested (non-conflicting) relationship.
    CHECK(has_conflict(
        clash_report, gsb::RecordId{2}, gsb::RecordId{6}, true));
    CHECK(!clash_report.deliverable);
}

void test_minimality_tie_breaks() {
    gsb::TrustSnapshot snapshot = two_leaf_snapshot();
    gsb::Digest content = digest("tie");
    std::vector<gsb::FragmentRecord> records = {
        record(10, 1, 1, 0, 100, content, 0, 200),
        record(20, 1, 2, 0, 100, content, 0, 200),
        record(30, 1, 3, 100, 200, content, 0, 200),
        record(40, 1, 4, 100, 200, content, 0, 200),
        // Redundant extra principal on leaf 0 with a larger span.
        record(50, 1, 5, 0, 100, content, 0, 200),
    };
    snapshot.trusted_root =
        root_of(snapshot, gsb::Version{1}, records);

    gsb::ReassemblyEngine engine;
    gsb::QueryReport report = engine.query(snapshot, records);
    CHECK(report.deliverable);
    CHECK(report.certificate->records.size() == 4);
    std::vector<std::uint64_t> ids;
    for (const gsb::FragmentRecord& fragment :
         report.certificate->records) {
        ids.push_back(fragment.id.value);
    }
    std::vector<std::uint64_t> expected{10, 20, 30, 40};
    CHECK(ids == expected);

    std::optional<OracleChoice> expected_oracle =
        oracle(snapshot, records);
    CHECK(expected_oracle.has_value());
    CHECK(expected_oracle->ids == ids);
}
