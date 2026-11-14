#include "netsec.hpp"

#include <cmath>
#include <exception>
#include <iostream>
#include <stdexcept>
#include <string>

namespace {

constexpr double kBaseLat = 31.2304;
constexpr double kBaseLon = 121.4737;

#define CHECK(condition)                                                     \
    do {                                                                     \
        if (!(condition)) {                                                  \
            throw std::runtime_error(std::string("CHECK failed: ") +         \
                                     #condition + " at line " +             \
                                     std::to_string(__LINE__));             \
        }                                                                    \
    } while (false)

netsec::GeoPoint point(double latitude_offset_deg = 0.0,
                       double longitude_offset_deg = 0.0,
                       double altitude_m = 10.0) {
    return netsec::GeoPoint{kBaseLat + latitude_offset_deg,
                            kBaseLon + longitude_offset_deg, altitude_m};
}

netsec::GnssSourceFix source(std::string id,
                             netsec::Constellation constellation,
                             netsec::FrequencyBand band,
                             netsec::GeoPoint position,
                             std::uint64_t timestamp_ns,
                             double cn0 = 44.0) {
    netsec::GnssSourceFix fix;
    fix.source_id = std::move(id);
    fix.constellation = constellation;
    fix.band = band;
    fix.position = position;
    fix.timestamp_ns = timestamp_ns;
    fix.cn0_dbhz = cn0;
    fix.expected_cn0_dbhz = 44.0;
    return fix;
}

netsec::TrustInput legitimate_input(std::uint64_t timestamp_ns) {
    netsec::TrustInput input;
    input.timestamp_ns = timestamp_ns;
    input.receiver_fix.position = point();
    input.receiver_fix.timestamp_ns = timestamp_ns;
    input.receiver_fix.horizontal_accuracy_m = 4.0;
    input.receiver_fix.cn0_dbhz = 44.0;
    input.source_fixes = {
        source("gps-l1", netsec::Constellation::Gps, netsec::FrequencyBand::L1,
               point(), timestamp_ns),
        source("gal-e1", netsec::Constellation::Galileo,
               netsec::FrequencyBand::E1, point(), timestamp_ns),
        source("bds-b1", netsec::Constellation::BeiDou,
               netsec::FrequencyBand::B1, point(), timestamp_ns),
    };
    return input;
}

bool same_position(const netsec::GeoPoint& lhs, const netsec::GeoPoint& rhs) {
    return std::abs(lhs.latitude_deg - rhs.latitude_deg) < 1e-9 &&
           std::abs(lhs.longitude_deg - rhs.longitude_deg) < 1e-9 &&
           std::abs(lhs.altitude_m - rhs.altitude_m) < 1e-6;
}

void verify_no_silent_offset(const netsec::TrustDecision& decision,
                             const netsec::GeoPoint& expected_safe) {
    CHECK(!decision.accepted);
    CHECK(decision.requires_manual_confirmation);
    CHECK(decision.safe_position.has_value());
    CHECK(same_position(*decision.safe_position, expected_safe));
}

void test_legitimate_position_is_trusted() {
    netsec::PositioningTrustComponent component;
    const netsec::GeoPoint expected = point();
    const netsec::TrustDecision decision = component.evaluate(
        legitimate_input(1'000'000'000ULL));

    CHECK(decision.level == netsec::TrustLevel::Trusted);
    CHECK(decision.accepted);
    CHECK(!decision.requires_manual_confirmation);
    CHECK(!decision.has_critical_issue());
    CHECK(!decision.offset_detected);
    CHECK(decision.consensus_position.has_value());
    CHECK(same_position(*decision.consensus_position, expected));
    CHECK(decision.safe_position.has_value());
    CHECK(same_position(*decision.safe_position, expected));
}

void test_limited_diversity_is_degraded_but_accepted() {
    netsec::PositioningTrustComponent component;
    netsec::TrustInput input = legitimate_input(2'000'000'000ULL);
    input.source_fixes = {
        source("gps-l1", netsec::Constellation::Gps, netsec::FrequencyBand::L1,
               point(), input.timestamp_ns),
        source("gps-l5", netsec::Constellation::Gps, netsec::FrequencyBand::L5,
               point(), input.timestamp_ns),
    };

    const netsec::TrustDecision decision = component.evaluate(input);
    CHECK(decision.level == netsec::TrustLevel::Degraded);
    CHECK(decision.accepted);
    CHECK(!decision.requires_manual_confirmation);
    CHECK(decision.has_issue(netsec::IssueCode::SingleConstellation));
    CHECK(!decision.has_issue(netsec::IssueCode::SingleFrequency));
}

void test_imu_and_map_accept_legitimate_position() {
    netsec::PositioningTrustComponent component;
    netsec::TrustInput input = legitimate_input(2'500'000'000ULL);
    input.inertial.available = true;
    input.inertial.position = point();
    input.inertial.timestamp_ns = input.timestamp_ns;
    input.map.available = true;
    input.map.route = {point(-0.02, 0.0), point(0.02, 0.0)};

    const netsec::TrustDecision decision = component.evaluate(input);
    CHECK(decision.level == netsec::TrustLevel::Trusted);
    CHECK(decision.accepted);
    CHECK(!decision.has_issue(netsec::IssueCode::ImuDisagreement));
    CHECK(!decision.has_issue(netsec::IssueCode::MapViolation));
}

void test_single_source_spoof_is_not_accepted() {
    netsec::PositioningTrustComponent component;
    netsec::TrustInput input = legitimate_input(3'000'000'000ULL);
    input.source_fixes.push_back(
        source("spoofed-gps-l5", netsec::Constellation::Gps,
               netsec::FrequencyBand::L5, point(0.02, 0.0),
               input.timestamp_ns));

    const netsec::TrustDecision decision = component.evaluate(input);
    CHECK(decision.level == netsec::TrustLevel::ManualConfirmation);
    CHECK(!decision.accepted);
    CHECK(decision.requires_manual_confirmation);
    CHECK(decision.has_issue(netsec::IssueCode::SourceOutlier));
    CHECK(!decision.safe_position.has_value());
}

void test_single_source_only_is_not_trusted() {
    netsec::PositioningTrustComponent component;
    netsec::TrustInput input = legitimate_input(2'800'000'000ULL);
    input.source_fixes.resize(1);

    const netsec::TrustDecision decision = component.evaluate(input);
    CHECK(decision.level == netsec::TrustLevel::ManualConfirmation);
    CHECK(!decision.accepted);
    CHECK(decision.has_issue(netsec::IssueCode::InsufficientRedundancy));
}

void test_signal_forgery_requires_manual_review() {
    netsec::PositioningTrustComponent component;
    netsec::TrustInput input = legitimate_input(4'000'000'000ULL);
    input.receiver_fix.position = point(0.02, 0.0);

    netsec::GnssSourceFix forged =
        source("forged-gps-l5", netsec::Constellation::Gps,
               netsec::FrequencyBand::L5, point(0.02, 0.0),
               input.timestamp_ns, 70.0);
    forged.expected_cn0_dbhz = 44.0;
    forged.toa_residual_ns = 900.0;
    forged.ephemeris_consistent = false;
    input.source_fixes.push_back(forged);

    const netsec::TrustDecision decision = component.evaluate(input);
    CHECK(decision.level == netsec::TrustLevel::ManualConfirmation);
    CHECK(!decision.accepted);
    CHECK(decision.requires_manual_confirmation);
    CHECK(decision.offset_detected);
    CHECK(decision.has_issue(netsec::IssueCode::ExcessiveSignal));
    CHECK(decision.has_issue(netsec::IssueCode::ToaInconsistency));
    CHECK(decision.has_issue(netsec::IssueCode::EphemerisMismatch));
    CHECK(decision.has_issue(netsec::IssueCode::UnsupportedReportedFix));
}

void test_position_jump_holds_last_trusted_position() {
    netsec::PositioningTrustComponent component;
    const netsec::GeoPoint trusted = point();
    const auto first =
        component.evaluate(legitimate_input(5'000'000'000ULL));
    CHECK(first.accepted);

    netsec::TrustInput jumped = legitimate_input(6'000'000'000ULL);
    jumped.receiver_fix.position = point(0.045, 0.0);
    jumped.inertial.available = true;
    jumped.inertial.position = point();
    jumped.inertial.timestamp_ns = jumped.timestamp_ns;
    for (auto& fix : jumped.source_fixes) {
        fix.position = point(0.045, 0.0);
    }

    const netsec::TrustDecision decision = component.evaluate(jumped);
    CHECK(decision.level == netsec::TrustLevel::ManualConfirmation);
    CHECK(decision.has_issue(netsec::IssueCode::PositionJump));
    CHECK(decision.has_issue(netsec::IssueCode::ImuDisagreement));
    CHECK(decision.offset_detected);
    verify_no_silent_offset(decision, trusted);
}

void test_normal_vehicle_motion_is_not_a_jump() {
    netsec::PositioningTrustComponent component;
    const auto first =
        component.evaluate(legitimate_input(20'000'000'000ULL));
    CHECK(first.accepted);

    netsec::TrustInput moving = legitimate_input(80'000'000'000ULL);
    const netsec::GeoPoint moved = point(0.009, 0.0);
    moving.receiver_fix.position = moved;
    for (auto& fix : moving.source_fixes) {
        fix.position = moved;
    }
    moving.inertial.available = true;
    moving.inertial.position = moved;
    moving.inertial.speed_mps = 17.0;
    moving.inertial.timestamp_ns = moving.timestamp_ns;

    const netsec::TrustDecision decision = component.evaluate(moving);
    CHECK(decision.level == netsec::TrustLevel::Trusted);
    CHECK(decision.accepted);
    CHECK(!decision.has_issue(netsec::IssueCode::PositionJump));
    CHECK(!decision.has_issue(netsec::IssueCode::SourceAbruptChange));
}

void test_consistent_spoof_is_caught_by_inertial_and_map() {
    netsec::PositioningTrustComponent component;
    netsec::TrustInput input = legitimate_input(6'500'000'000ULL);
    const netsec::GeoPoint spoofed = point(0.045, 0.0);
    input.receiver_fix.position = spoofed;
    for (auto& fix : input.source_fixes) {
        fix.position = spoofed;
    }
    input.inertial.available = true;
    input.inertial.position = point();
    input.inertial.timestamp_ns = input.timestamp_ns;
    input.map.available = true;
    input.map.route = {point(-0.02, 0.0), point(0.02, 0.0)};

    const netsec::TrustDecision decision = component.evaluate(input);
    CHECK(decision.level == netsec::TrustLevel::ManualConfirmation);
    CHECK(decision.has_issue(netsec::IssueCode::ImuDisagreement));
    CHECK(decision.has_issue(netsec::IssueCode::MapViolation));
    verify_no_silent_offset(decision, point());
    CHECK(!same_position(*decision.safe_position, spoofed));
}

void test_map_constraint_blocks_off_route_solution() {
    netsec::PositioningTrustComponent component;
    netsec::TrustInput input = legitimate_input(7'000'000'000ULL);
    input.receiver_fix.position = point(0.03, 0.0);
    for (auto& fix : input.source_fixes) {
        fix.position = point(0.03, 0.0);
    }
    input.map.available = true;
    input.map.route = {point(-0.02, 0.0), point(0.02, 0.0)};

    const netsec::TrustDecision decision = component.evaluate(input);
    CHECK(decision.level == netsec::TrustLevel::ManualConfirmation);
    CHECK(!decision.accepted);
    CHECK(decision.has_issue(netsec::IssueCode::MapViolation));
    CHECK(decision.offset_detected);
}

void test_time_anomaly_is_marked() {
    netsec::PositioningTrustComponent component;
    netsec::TrustInput input = legitimate_input(8'000'000'000ULL);
    input.source_fixes[1].timestamp_ns = input.timestamp_ns + 2'000'000'000ULL;

    const netsec::TrustDecision decision = component.evaluate(input);
    CHECK(decision.level == netsec::TrustLevel::ManualConfirmation);
    CHECK(!decision.accepted);
    CHECK(decision.has_issue(netsec::IssueCode::TimeAnomaly));
}

void test_single_source_abrupt_change_is_marked() {
    netsec::PositioningTrustComponent component;
    const auto first =
        component.evaluate(legitimate_input(9'000'000'000ULL));
    CHECK(first.accepted);

    netsec::TrustInput input = legitimate_input(10'000'000'000ULL);
    input.source_fixes[2].position = point(0.005, 0.0);

    const netsec::TrustDecision decision = component.evaluate(input);
    CHECK(decision.level == netsec::TrustLevel::ManualConfirmation);
    CHECK(!decision.accepted);
    CHECK(decision.has_issue(netsec::IssueCode::SourceAbruptChange));
    CHECK(decision.has_issue(netsec::IssueCode::SourceOutlier));
    verify_no_silent_offset(decision, point());
}

void test_invalid_or_insufficient_fix_is_rejected() {
    netsec::PositioningTrustComponent component;
    netsec::TrustInput input = legitimate_input(11'000'000'000ULL);
    input.source_fixes.clear();

    const netsec::TrustDecision decision = component.evaluate(input);
    CHECK(decision.level == netsec::TrustLevel::Rejected);
    CHECK(!decision.accepted);
    CHECK(decision.requires_manual_confirmation);
    CHECK(decision.has_issue(netsec::IssueCode::InsufficientRedundancy));
}

void test_reset_discards_history() {
    netsec::PositioningTrustComponent component;
    const auto first =
        component.evaluate(legitimate_input(12'000'000'000ULL));
    CHECK(first.accepted);

    component.reset();
    const netsec::TrustDecision second = component.evaluate(
        legitimate_input(13'000'000'000ULL));
    CHECK(second.level == netsec::TrustLevel::Trusted);
    CHECK(second.accepted);
}

}  // namespace

int main() {
    struct TestCase {
        const char* name;
        void (*run)();
    };

    const TestCase tests[] = {
        {"legitimate position is trusted", test_legitimate_position_is_trusted},
        {"limited diversity is degraded", test_limited_diversity_is_degraded_but_accepted},
        {"imu and map accept legitimate position", test_imu_and_map_accept_legitimate_position},
        {"single-source spoof is not accepted", test_single_source_spoof_is_not_accepted},
        {"single source only is not trusted", test_single_source_only_is_not_trusted},
        {"signal forgery requires manual review", test_signal_forgery_requires_manual_review},
        {"position jump holds last trusted", test_position_jump_holds_last_trusted_position},
        {"normal vehicle motion is allowed", test_normal_vehicle_motion_is_not_a_jump},
        {"consistent spoof caught by guards", test_consistent_spoof_is_caught_by_inertial_and_map},
        {"map blocks off-route solution", test_map_constraint_blocks_off_route_solution},
        {"time anomaly is marked", test_time_anomaly_is_marked},
        {"single-source abrupt change", test_single_source_abrupt_change_is_marked},
        {"insufficient fix is rejected", test_invalid_or_insufficient_fix_is_rejected},
        {"reset discards history", test_reset_discards_history},
    };

    int failures = 0;
    for (const TestCase& test : tests) {
        try {
            test.run();
            std::cout << "[PASS] " << test.name << '\n';
        } catch (const std::exception& error) {
            ++failures;
            std::cerr << "[FAIL] " << test.name << ": " << error.what()
                      << '\n';
        }
    }

    if (failures != 0) {
        std::cerr << failures << " test(s) failed\n";
        return 1;
    }
    std::cout << "All positioning trust tests passed\n";
    return 0;
}
