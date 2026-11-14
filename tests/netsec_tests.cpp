#include "netsec.hpp"

#include <cmath>
#include <cstdio>
#include <string>
#include <utility>

namespace {

int failures = 0;

void check(bool condition, const char* expression, int line) {
    if (!condition) {
        std::fprintf(stderr, "FAIL line %d: %s\n", line, expression);
        ++failures;
    }
}

#define CHECK(expression) check((expression), #expression, __LINE__)

constexpr netsec::Timestamp kBase = 1'700'000'000;

netsec::StaticReport legitimate_static() {
    netsec::StaticReport report;
    report.message_type = netsec::MessageType::StaticVoyage;
    report.mmsi = "412345678";
    report.imo = "IMO9074729";
    report.name = "COSCO SHANGHAI";
    report.callsign = "BQAB";
    report.destination = "SHANGHAI";
    report.source_id = "rx-a";
    report.generated_at = kBase;
    report.received_at = kBase;
    return report;
}

netsec::DynamicReport dynamic_at(double latitude, double longitude,
                                 netsec::Timestamp observed_at,
                                 std::string source = "rx-a") {
    netsec::DynamicReport report;
    report.message_type = netsec::MessageType::ClassAPosition1;
    report.mmsi = "412345678";
    report.latitude = latitude;
    report.longitude = longitude;
    report.speed_over_ground_knots = 12.0;
    report.course_over_ground_degrees = 90.0;
    report.true_heading_degrees = 90.0;
    report.source_id = std::move(source);
    report.observed_at = observed_at;
    report.received_at = observed_at;
    return report;
}

void test_legitimate_ship() {
    netsec::ShipIdentityMonitor monitor;
    const auto static_result = monitor.ingest(legitimate_static());
    CHECK(static_result.valid);
    CHECK(static_result.accepted);
    CHECK(static_result.trusted);
    CHECK(static_result.trust == netsec::TrustLevel::Trusted);
    CHECK(!static_result.has_critical());

    const auto first = monitor.ingest(dynamic_at(31.2304, 121.4737, kBase + 10));
    CHECK(first.valid);
    CHECK(first.accepted);
    CHECK(first.trusted);
    CHECK(!first.has(netsec::FindingCode::MissingStaticProfile));
    CHECK(first.track_points == 1);

    const auto second = monitor.ingest(dynamic_at(31.2315, 121.4750, kBase + 20));
    CHECK(second.trusted);
    CHECK(second.track_points == 2);

    const auto profile = monitor.profile("412345678");
    CHECK(profile.has_value());
    CHECK(profile->name == "COSCO SHANGHAI");
    CHECK(profile->callsign == "BQAB");
    CHECK(profile->destination == "SHANGHAI");
    CHECK(profile->source_ids.size() == 1);

    const auto track = monitor.track("412345678");
    CHECK(track.size() == 2);
}

void test_identity_impersonation() {
    netsec::ShipIdentityMonitor monitor;
    CHECK(monitor.ingest(legitimate_static()).trusted);

    auto forged = legitimate_static();
    forged.callsign = "EVIL";
    forged.destination = "SINGAPORE";
    forged.source_id = "rx-b";
    forged.received_at = kBase + 60;
    forged.generated_at = kBase + 60;
    const auto result = monitor.ingest(forged);
    CHECK(result.valid);
    CHECK(!result.accepted);
    CHECK(!result.trusted);
    CHECK(result.trust == netsec::TrustLevel::Untrusted);
    CHECK(result.has(netsec::FindingCode::IdentityConflict));

    const auto unchanged = monitor.profile("412345678");
    CHECK(unchanged.has_value());
    CHECK(unchanged->callsign == "BQAB");
    CHECK(unchanged->destination == "SHANGHAI");
}

void test_static_dynamic_identity_mismatch() {
    netsec::ShipIdentityMonitor monitor;
    CHECK(monitor.ingest(legitimate_static()).trusted);

    auto report = dynamic_at(31.2304, 121.4737, kBase + 10);
    report.message_type = netsec::MessageType::ClassBExtended;
    report.claimed_name = "OTHER VESSEL";
    const auto result = monitor.ingest(report);
    CHECK(result.valid);
    CHECK(!result.trusted);
    CHECK(result.has(netsec::FindingCode::StaticDynamicMismatch));
    CHECK(result.track_points == 0);
}

void test_static_arriving_after_dynamic_claim() {
    netsec::ShipIdentityMonitor monitor;
    auto dynamic = dynamic_at(31.2304, 121.4737, kBase);
    dynamic.message_type = netsec::MessageType::ClassBExtended;
    dynamic.claimed_name = "OTHER VESSEL";
    const auto before_static = monitor.ingest(dynamic);
    CHECK(before_static.accepted);
    CHECK(before_static.trust == netsec::TrustLevel::Unverified);

    const auto static_result = monitor.ingest(legitimate_static());
    CHECK(static_result.valid);
    CHECK(!static_result.accepted);
    CHECK(!static_result.trusted);
    CHECK(static_result.has(netsec::FindingCode::IdentityConflict));
    CHECK(!monitor.profile("412345678").has_value());
}

void test_position_jump() {
    netsec::ShipIdentityMonitor monitor;
    CHECK(monitor.ingest(legitimate_static()).trusted);
    CHECK(monitor.ingest(dynamic_at(31.2304, 121.4737, kBase + 10)).trusted);

    const auto jumped = monitor.ingest(dynamic_at(32.5000, 122.5000, kBase + 15));
    CHECK(jumped.valid);
    CHECK(!jumped.accepted);
    CHECK(!jumped.trusted);
    CHECK(jumped.trust == netsec::TrustLevel::Untrusted);
    CHECK(jumped.has(netsec::FindingCode::PositionJump));
    CHECK(jumped.track_points == 1);
}

void test_replay_and_duplicate() {
    netsec::ShipIdentityMonitor monitor;
    CHECK(monitor.ingest(legitimate_static()).trusted);

    const auto original = dynamic_at(31.2304, 121.4737, kBase + 10);
    CHECK(monitor.ingest(original).trusted);

    auto duplicate = original;
    duplicate.received_at = kBase + 20;
    const auto duplicate_result = monitor.ingest(duplicate);
    CHECK(duplicate_result.valid);
    CHECK(duplicate_result.accepted);
    CHECK(duplicate_result.trusted);
    CHECK(duplicate_result.has(netsec::FindingCode::DuplicateMessage));
    CHECK(!duplicate_result.has(netsec::FindingCode::ReplayDetected));

    CHECK(monitor.ingest(dynamic_at(31.2320, 121.4760, kBase + 40)).trusted);
    auto replay = original;
    replay.received_at = kBase + 50;
    const auto replay_result = monitor.ingest(replay);
    CHECK(replay_result.valid);
    CHECK(!replay_result.accepted);
    CHECK(!replay_result.trusted);
    CHECK(replay_result.has(netsec::FindingCode::ReplayDetected));
    CHECK(replay_result.track_points == 2);
}

void test_multi_source_conflict() {
    netsec::ShipIdentityMonitor monitor;
    CHECK(monitor.ingest(legitimate_static()).trusted);
    CHECK(monitor.ingest(dynamic_at(31.2304, 121.4737, kBase + 10, "rx-a")).trusted);

    const auto conflict =
        monitor.ingest(dynamic_at(31.3304, 121.6737, kBase + 12, "rx-b"));
    CHECK(conflict.valid);
    CHECK(!conflict.accepted);
    CHECK(!conflict.trusted);
    CHECK(conflict.has(netsec::FindingCode::MultiSourceConflict));
    CHECK(conflict.source_count == 2);

    netsec::ShipIdentityMonitor consensus_monitor;
    CHECK(consensus_monitor.ingest(legitimate_static()).trusted);
    CHECK(consensus_monitor.ingest(dynamic_at(31.2304, 121.4737, kBase + 10, "rx-a"))
              .trusted);
    const auto consensus =
        consensus_monitor.ingest(dynamic_at(31.2304, 121.4737, kBase + 10, "rx-b"));
    CHECK(consensus.trusted);
    CHECK(!consensus.has(netsec::FindingCode::MultiSourceConflict));
}

void test_per_message_validation() {
    netsec::ShipIdentityMonitor monitor;

    auto bad_mmsi = legitimate_static();
    bad_mmsi.mmsi = "123456789";
    const auto mmsi_result = monitor.ingest(bad_mmsi);
    CHECK(!mmsi_result.valid);
    CHECK(mmsi_result.has(netsec::FindingCode::InvalidIdentifier));

    auto bad_destination = legitimate_static();
    bad_destination.destination = "THIS DESTINATION IS MUCH TOO LONG";
    const auto destination_result = monitor.ingest(bad_destination);
    CHECK(!destination_result.valid);
    CHECK(destination_result.has(netsec::FindingCode::InvalidDestination));

    netsec::StaticReport part_a;
    part_a.message_type = netsec::MessageType::ClassBStatic;
    part_a.part_number = 1;
    part_a.mmsi = "412345678";
    part_a.callsign = "BQAB";
    part_a.source_id = "rx-a";
    part_a.received_at = kBase;
    const auto part_a_result = monitor.ingest(part_a);
    CHECK(!part_a_result.valid);
    CHECK(part_a_result.has(netsec::FindingCode::InvalidName));

    auto bad_position = dynamic_at(91.0, 181.0, kBase + 10);
    const auto position_result = monitor.ingest(bad_position);
    CHECK(!position_result.valid);
    CHECK(position_result.has(netsec::FindingCode::InvalidPosition));

    auto bad_speed = dynamic_at(31.2304, 121.4737, kBase + 10);
    bad_speed.speed_over_ground_knots = 500.0;
    const auto speed_result = monitor.ingest(bad_speed);
    CHECK(!speed_result.valid);
    CHECK(speed_result.has(netsec::FindingCode::InvalidSpeed));
}

void test_time_window_and_unverified_track() {
    netsec::Policy policy;
    policy.track_gap_seconds = 60;
    netsec::ShipIdentityMonitor monitor(policy);
    CHECK(monitor.ingest(legitimate_static()).trusted);
    CHECK(monitor.ingest(dynamic_at(31.2304, 121.4737, kBase + 10)).trusted);

    const auto after_gap =
        monitor.ingest(dynamic_at(32.5000, 122.5000, kBase + 200));
    CHECK(after_gap.valid);
    CHECK(after_gap.accepted);
    CHECK(after_gap.trusted);
    CHECK(after_gap.has(netsec::FindingCode::TrackGap));
    CHECK(!after_gap.has(netsec::FindingCode::PositionJump));
    CHECK(after_gap.track_points == 2);

    netsec::ShipIdentityMonitor dynamic_only;
    const auto unverified =
        dynamic_only.ingest(dynamic_at(31.2304, 121.4737, kBase + 10));
    CHECK(unverified.valid);
    CHECK(unverified.accepted);
    CHECK(!unverified.trusted);
    CHECK(unverified.trust == netsec::TrustLevel::Unverified);
    CHECK(unverified.has(netsec::FindingCode::MissingStaticProfile));
}

void test_class_b_partial_profile() {
    netsec::ShipIdentityMonitor monitor;

    netsec::StaticReport part_a;
    part_a.message_type = netsec::MessageType::ClassBStatic;
    part_a.part_number = 1;
    part_a.mmsi = "412345678";
    part_a.name = "HARBOR PILOT";
    part_a.source_id = "rx-a";
    part_a.received_at = kBase;
    CHECK(monitor.ingest(part_a).trusted);

    netsec::StaticReport part_b;
    part_b.message_type = netsec::MessageType::ClassBStatic;
    part_b.part_number = 2;
    part_b.mmsi = "412345678";
    part_b.callsign = "PILOT7";
    part_b.source_id = "rx-a";
    part_b.received_at = kBase + 120;
    const auto merged = monitor.ingest(part_b);
    CHECK(merged.trusted);

    const auto profile = monitor.profile("412345678");
    CHECK(profile.has_value());
    CHECK(profile->name == "HARBOR PILOT");
    CHECK(profile->callsign == "PILOT7");
}

}  // namespace

int main() {
    test_legitimate_ship();
    test_identity_impersonation();
    test_static_dynamic_identity_mismatch();
    test_static_arriving_after_dynamic_claim();
    test_position_jump();
    test_replay_and_duplicate();
    test_multi_source_conflict();
    test_per_message_validation();
    test_time_window_and_unverified_track();
    test_class_b_partial_profile();

    if (failures == 0) {
        std::puts("all netsec identity tests passed");
        return 0;
    }
    std::fprintf(stderr, "%d test assertion(s) failed\n", failures);
    return 1;
}
