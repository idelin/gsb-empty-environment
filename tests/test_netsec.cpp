#include "netsec.hpp"

#include <chrono>
#include <cstdint>
#include <iostream>
#include <string>
#include <vector>

namespace {

using namespace std::chrono_literals;
using netsec::AlarmCode;
using netsec::Announcement;
using netsec::BoundedMonotonicClock;
using netsec::ClockIdentity;
using netsec::ClockSafetyPolicy;
using netsec::JumpPolicy;
using netsec::MasterClockAdmission;
using netsec::PriorityRange;
using netsec::RejectionReason;
using netsec::SyncSample;
using netsec::TimeSyncGuard;
using netsec::Timestamp;
using netsec::TrustedMaster;

int failures = 0;

#define CHECK(condition)                                                     \
    do {                                                                     \
        if (!(condition)) {                                                  \
            std::cerr << __FILE__ << ":" << __LINE__                         \
                      << ": check failed: " #condition "\n";                 \
            ++failures;                                                      \
        }                                                                    \
    } while (false)

ClockIdentity identity(std::uint8_t value) {
    ClockIdentity result;
    result.bytes = {0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, value};
    return result;
}

std::vector<std::uint8_t> secret(std::string_view text) {
    return std::vector<std::uint8_t>(text.begin(), text.end());
}

Announcement signedAnnouncement(
    const ClockIdentity& source,
    std::uint8_t priority,
    std::uint8_t stratum,
    std::uint16_t sequence,
    std::string_view key,
    std::uint32_t key_id = 7) {
    Announcement announcement =
        netsec::makeAnnouncement(source, priority, stratum, sequence);
    return netsec::signAnnouncement(announcement, secret(key), key_id);
}

void testHmacVector() {
    const auto key = secret("key");
    const std::string message = "The quick brown fox jumps over the lazy dog";
    const std::vector<std::uint8_t> bytes(message.begin(), message.end());
    const auto tag = netsec::hmacSha256(key, bytes);
    const std::string expected =
        "f7bc83f430538424b13298e6aa6fb143"
        "ef4d59a14946175997479dbc2d1a3cd8";
    std::string actual;
    for (std::uint8_t value : tag) {
        const char* digits = "0123456789abcdef";
        actual.push_back(digits[value >> 4]);
        actual.push_back(digits[value & 0x0F]);
    }
    CHECK(actual == expected);

    const std::vector<std::uint8_t> long_key(131, 0xAA);
    const std::string long_message =
        "Test Using Larger Than Block-Size Key - Hash Key First";
    const std::vector<std::uint8_t> long_bytes(
        long_message.begin(), long_message.end());
    const auto long_tag = netsec::hmacSha256(long_key, long_bytes);
    const std::string long_expected =
        "60e431591ee0b67f0d8a26aacbf5b77f"
        "8e0bc6213728c5140546040f0ee37f54";
    std::string long_actual;
    for (std::uint8_t value : long_tag) {
        const char* digits = "0123456789abcdef";
        long_actual.push_back(digits[value >> 4]);
        long_actual.push_back(digits[value & 0x0F]);
    }
    CHECK(long_actual == long_expected);
}

void testClockIdentityParsing() {
    const auto parsed =
        ClockIdentity::fromString("00:11:22:33:44:55:66:a1");
    CHECK(parsed.has_value());
    CHECK(parsed->bytes[7] == 0xA1);
    CHECK(parsed->toString() == "00:11:22:33:44:55:66:a1");
    CHECK(!ClockIdentity::fromString("not-an-identity").has_value());
    CHECK(!ClockIdentity::fromString("00:11").has_value());
}

void testLegitimateMasterAccepted() {
    const ClockIdentity trusted = identity(0xA1);
    const auto key = secret("master-secret");
    MasterClockAdmission admission(
        netsec::AdmissionPolicy{},
        {netsec::makeTrustedMaster(trusted, "master-secret", 1, 7)});

    const auto announcement =
        signedAnnouncement(trusted, 10, 2, 1, "master-secret");
    const auto result = admission.submit(announcement, Timestamp{0});
    CHECK(result.accepted);
    CHECK(result.reason == RejectionReason::None);
    CHECK(result.selected.has_value());
    CHECK(result.selected->source.clock_identity == trusted);
    CHECK(admission.selectedMaster().has_value());
    CHECK(result.alarms.empty());
}

void testForgedBetterMasterCannotLowerSelection() {
    const ClockIdentity trusted = identity(0xA1);
    const ClockIdentity attacker = identity(0xE1);
    MasterClockAdmission admission(
        netsec::AdmissionPolicy{},
        {netsec::makeTrustedMaster(trusted, "master-secret", 1, 7)});

    const auto legitimate =
        signedAnnouncement(trusted, 20, 3, 1, "master-secret");
    const auto first = admission.submit(legitimate, Timestamp{0});
    CHECK(first.accepted);
    CHECK(first.selected->source.clock_identity == trusted);

    auto forged = signedAnnouncement(attacker, 0, 0, 1, "attacker-secret");
    forged.quality.clock_class = 0;
    const auto result = admission.submit(forged, Timestamp{1s});
    CHECK(!result.accepted);
    CHECK(result.reason == RejectionReason::UntrustedSource);
    CHECK(result.hasAlarm(AlarmCode::UntrustedSource));
    CHECK(result.selected.has_value());
    CHECK(result.selected->source.clock_identity == trusted);
    CHECK(admission.policy().priority1_range.minimum == 0);
    CHECK(admission.admittedCandidateCount() == 1);
}

void testMissingAndTamperedAuthentication() {
    const ClockIdentity trusted = identity(0xA1);
    MasterClockAdmission admission(
        netsec::AdmissionPolicy{},
        {netsec::makeTrustedMaster(trusted, "master-secret", 1, 7)});

    auto unsigned_announcement =
        netsec::makeAnnouncement(trusted, 10, 2, 1);
    const auto missing = admission.submit(unsigned_announcement, Timestamp{0});
    CHECK(!missing.accepted);
    CHECK(missing.reason == RejectionReason::AuthenticationRequired);
    CHECK(missing.hasAlarm(AlarmCode::AuthenticationMissing));

    auto valid = signedAnnouncement(trusted, 10, 2, 2, "master-secret");
    valid.quality.priority1 = 0;
    valid.quality.priority2 = 0;
    const auto tampered = admission.submit(valid, Timestamp{1s});
    CHECK(!tampered.accepted);
    CHECK(tampered.reason == RejectionReason::AuthenticationFailed);
    CHECK(tampered.hasAlarm(AlarmCode::AuthenticationFailed));
    CHECK(!admission.selectedMaster().has_value());
}

void testPriorityAndStratumPolicy() {
    const ClockIdentity trusted = identity(0xA1);
    auto master = netsec::makeTrustedMaster(trusted, "master-secret", 1, 7);
    master.allowed_priority1 = PriorityRange{10, 10};
    master.expected_priority1 = 10;
    master.max_stratum = 2;

    netsec::AdmissionPolicy policy;
    policy.max_stratum = 3;

    MasterClockAdmission admission(policy, {master});

    const auto legitimate =
        signedAnnouncement(trusted, 10, 2, 1, "master-secret");
    const auto accepted = admission.submit(legitimate, Timestamp{0});
    CHECK(accepted.accepted);

    const auto better =
        signedAnnouncement(trusted, 0, 1, 2, "master-secret");
    const auto priority = admission.submit(better, Timestamp{1s});
    CHECK(!priority.accepted);
    CHECK(priority.reason == RejectionReason::PriorityOutOfPolicy);
    CHECK(priority.hasAlarm(AlarmCode::PolicyViolation));
    CHECK(priority.selected->source.clock_identity == trusted);

    const auto poor_stratum =
        signedAnnouncement(trusted, 10, 4, 3, "master-secret");
    const auto stratum = admission.submit(poor_stratum, Timestamp{2s});
    CHECK(!stratum.accepted);
    CHECK(stratum.reason == RejectionReason::StratumOutOfPolicy);
    CHECK(admission.admittedCandidateCount() == 1);
}

void testReplayAndExpiry() {
    const ClockIdentity trusted = identity(0xA1);
    MasterClockAdmission admission(
        netsec::AdmissionPolicy{},
        {netsec::makeTrustedMaster(trusted, "master-secret", 1, 7)});

    const auto announcement =
        signedAnnouncement(trusted, 10, 2, 5, "master-secret");
    const auto first = admission.submit(announcement, Timestamp{0});
    CHECK(first.accepted);
    const auto replay = admission.submit(announcement, Timestamp{1s});
    CHECK(!replay.accepted);
    CHECK(replay.reason == RejectionReason::ReplayDetected);
    CHECK(replay.hasAlarm(AlarmCode::ReplayDetected));

    const auto expired = admission.selected(Timestamp{30s});
    CHECK(!expired.has_value());
    CHECK(admission.admittedCandidateCount() == 0);
}

void testSequenceNumberWrap() {
    const ClockIdentity trusted = identity(0xA1);
    MasterClockAdmission admission(
        netsec::AdmissionPolicy{},
        {netsec::makeTrustedMaster(trusted, "master-secret", 1, 7)});

    CHECK(admission
              .submit(
                  signedAnnouncement(
                      trusted, 10, 2, 65535, "master-secret"),
                  Timestamp{0})
              .accepted);
    CHECK(admission
              .submit(
                  signedAnnouncement(trusted, 10, 2, 0, "master-secret"),
                  Timestamp{1s})
              .accepted);
    const auto old = admission.submit(
        signedAnnouncement(trusted, 10, 2, 65535, "master-secret"),
        Timestamp{2s});
    CHECK(!old.accepted);
    CHECK(old.reason == RejectionReason::ReplayDetected);
}

void testStaleAnnouncementAndGrandmasterPin() {
    const ClockIdentity trusted = identity(0xA1);
    const ClockIdentity pinned_grandmaster = identity(0xB1);
    auto master = netsec::makeTrustedMaster(trusted, "master-secret", 1, 7);
    master.expected_grandmaster_identity = pinned_grandmaster;

    MasterClockAdmission admission(netsec::AdmissionPolicy{}, {master});
    auto stale = netsec::makeAnnouncement(trusted, 10, 2, 1);
    stale.transmit_time = Timestamp{0};
    stale = netsec::signAnnouncement(stale, secret("master-secret"), 7);
    const auto stale_result = admission.submit(stale, Timestamp{10s});
    CHECK(!stale_result.accepted);
    CHECK(stale_result.reason == RejectionReason::StaleAnnouncement);
    CHECK(stale_result.hasAlarm(AlarmCode::StaleAnnouncement));

    auto wrong_grandmaster = netsec::makeAnnouncement(trusted, 10, 2, 2);
    wrong_grandmaster.grandmaster = identity(0xE1);
    wrong_grandmaster =
        netsec::signAnnouncement(wrong_grandmaster, secret("master-secret"), 7);
    const auto gm_result = admission.submit(wrong_grandmaster, Timestamp{0});
    CHECK(!gm_result.accepted);
    CHECK(gm_result.reason == RejectionReason::GrandmasterMismatch);

    auto correct_grandmaster = netsec::makeAnnouncement(trusted, 10, 2, 3);
    correct_grandmaster.grandmaster = pinned_grandmaster;
    correct_grandmaster =
        netsec::signAnnouncement(correct_grandmaster, secret("master-secret"), 7);
    const auto accepted = admission.submit(correct_grandmaster, Timestamp{0});
    CHECK(accepted.accepted);
    CHECK(accepted.selected->grandmaster == pinned_grandmaster);
}

void testBmcaChoosesBestAdmittedMaster() {
    const ClockIdentity first_id = identity(0xA1);
    const ClockIdentity second_id = identity(0xA2);
    MasterClockAdmission admission(
        netsec::AdmissionPolicy{},
        {
            netsec::makeTrustedMaster(first_id, "first-secret", 1, 1),
            netsec::makeTrustedMaster(second_id, "second-secret", 1, 2),
        });

    const auto first =
        signedAnnouncement(first_id, 20, 3, 1, "first-secret", 1);
    const auto second =
        signedAnnouncement(second_id, 10, 2, 1, "second-secret", 2);
    CHECK(admission.submit(first, Timestamp{0}).accepted);
    const auto best = admission.submit(second, Timestamp{1s});
    CHECK(best.accepted);
    CHECK(best.selected->source.clock_identity == second_id);
}

void testJumpIsBoundedAndMonotonic() {
    ClockSafetyPolicy policy;
    policy.max_step = 1s;
    policy.jump_policy = JumpPolicy::Reject;
    BoundedMonotonicClock clock(policy, Timestamp{0});

    CHECK(clock.now(Timestamp{0}) == Timestamp{0});
    SyncSample jump;
    jump.master_time = Timestamp{20s};
    jump.local_time = Timestamp{2s};
    const auto rejected = clock.synchronize(jump);
    CHECK(rejected.accepted);
    CHECK(rejected.hasAlarm(AlarmCode::TimeJumpExceedsLimit));
    CHECK(rejected.applied_step == Timestamp{0});
    CHECK(rejected.jump_rejected);
    CHECK(!rejected.correction_applied);
    CHECK(rejected.monotonic);
    CHECK(rejected.after >= rejected.before);

    const Timestamp after_jump = clock.now(Timestamp{3s});
    CHECK(after_jump >= rejected.after);

    policy.jump_policy = JumpPolicy::Clamp;
    clock.setPolicy(policy);
    SyncSample clamped;
    clamped.master_time = Timestamp{40s};
    clamped.local_time = Timestamp{3s};
    const auto clamped_result = clock.synchronize(clamped);
    CHECK(clamped_result.applied_step <= policy.max_step);
    CHECK(clamped_result.hasAlarm(AlarmCode::TimeJumpExceedsLimit));
    CHECK(!clamped_result.jump_rejected);
    CHECK(clamped_result.correction_applied);
    CHECK(clamped_result.monotonic);
    CHECK(clamped_result.after >= clamped_result.before);
}

void testBackwardCorrectionNeverMovesBackwards() {
    ClockSafetyPolicy policy;
    policy.max_step = 1s;
    policy.jump_policy = JumpPolicy::Reject;
    BoundedMonotonicClock clock(policy, Timestamp{100s});
    CHECK(clock.now(Timestamp{0}) == Timestamp{100s});

    SyncSample backward;
    backward.master_time = Timestamp{50s};
    backward.local_time = Timestamp{1s};
    const auto result = clock.synchronize(backward);
    CHECK(result.accepted);
    CHECK(result.hasAlarm(AlarmCode::BackwardCorrectionSuppressed));
    CHECK(result.after >= result.before);
    CHECK(result.monotonic);

    const Timestamp later = clock.now(Timestamp{2s});
    CHECK(later >= result.after);
    CHECK(later >= Timestamp{100s});
}

void testLeapsAndDriftAreBounded() {
    ClockSafetyPolicy policy;
    policy.max_step = 1s;
    policy.max_leap_adjustment = 1s;
    policy.max_slew_ppm = 500.0;
    policy.max_drift_ppm = 100.0;
    BoundedMonotonicClock clock(policy, Timestamp{0});
    CHECK(clock.now(Timestamp{0}) == Timestamp{0});

    SyncSample leap;
    leap.master_time = Timestamp{2s};
    leap.local_time = Timestamp{1s};
    leap.leap_seconds = 1;
    const auto leap_result = clock.synchronize(leap);
    CHECK(leap_result.accepted);
    CHECK(leap_result.hasAlarm(AlarmCode::LeapSecond));
    CHECK(leap_result.monotonic);
    CHECK(leap_result.after >= leap_result.before);
    CHECK(leap_result.applied_step <= policy.max_step + policy.max_leap_adjustment);

    SyncSample huge_leap;
    huge_leap.master_time = Timestamp{50s};
    huge_leap.local_time = Timestamp{2s};
    huge_leap.leap_seconds = 5;
    const auto huge_result = clock.synchronize(huge_leap);
    CHECK(huge_result.hasAlarm(AlarmCode::LeapSecondOutOfBounds));
    CHECK(huge_result.monotonic);

    SyncSample drift;
    drift.master_time = clock.now(Timestamp{3s});
    drift.local_time = Timestamp{3s};
    drift.frequency_ppm = 250.0;
    const auto drift_result = clock.synchronize(drift);
    CHECK(drift_result.accepted);
    CHECK(drift_result.hasAlarm(AlarmCode::DriftExceedsLimit));
    CHECK(drift_result.applied_rate_ppm <= policy.max_drift_ppm + 1e-9);
    CHECK(drift_result.applied_rate_ppm >= -policy.max_drift_ppm - 1e-9);
}

void testGuardBindsSyncToSelectedMaster() {
    const ClockIdentity trusted = identity(0xA1);
    const ClockIdentity attacker = identity(0xE1);
    MasterClockAdmission admission(
        netsec::AdmissionPolicy{},
        {netsec::makeTrustedMaster(trusted, "master-secret", 1, 7)});
    TimeSyncGuard guard(
        netsec::AdmissionPolicy{},
        {netsec::makeTrustedMaster(trusted, "master-secret", 1, 7)},
        ClockSafetyPolicy{},
        Timestamp{0});
    (void)admission;

    const auto announcement =
        signedAnnouncement(trusted, 10, 2, 1, "master-secret");
    const auto admitted = guard.submitAnnouncement(announcement, Timestamp{0});
    CHECK(admitted.accepted);
    CHECK(guard.admission().selectedMaster().has_value());

    SyncSample legitimate;
    legitimate.source = netsec::PortIdentity{trusted, 1};
    legitimate.master_time = Timestamp{1s};
    legitimate.local_time = Timestamp{1s};
    const auto applied = guard.submitSync(legitimate);
    CHECK(applied.accepted);
    CHECK(applied.stepped);

    SyncSample forged;
    forged.source = netsec::PortIdentity{attacker, 1};
    forged.master_time = Timestamp{100s};
    forged.local_time = Timestamp{2s};
    const auto blocked = guard.submitSync(forged);
    CHECK(!blocked.accepted);
    CHECK(blocked.hasAlarm(AlarmCode::SyncSourceMismatch));
    CHECK(guard.now(Timestamp{3s}) < Timestamp{100s});
}

void testAlarmSinksReceiveSecurityAndTimeAlarms() {
    const ClockIdentity trusted = identity(0xA1);
    const ClockIdentity attacker = identity(0xE1);
    MasterClockAdmission admission(
        netsec::AdmissionPolicy{},
        {netsec::makeTrustedMaster(trusted, "master-secret", 1, 7)});

    std::vector<AlarmCode> admission_alarms;
    admission.setAlarmSink([&admission_alarms](const netsec::Alarm& alarm) {
        admission_alarms.push_back(alarm.code);
    });

    const auto legitimate =
        signedAnnouncement(trusted, 20, 3, 1, "master-secret");
    CHECK(admission.submit(legitimate, Timestamp{0}).accepted);
    const auto forged =
        signedAnnouncement(attacker, 0, 0, 1, "attacker-secret");
    CHECK(!admission.submit(forged, Timestamp{1s}).accepted);
    CHECK(!admission_alarms.empty());
    CHECK(admission_alarms.back() == AlarmCode::UntrustedSource);

    ClockSafetyPolicy policy;
    policy.max_step = 100ms;
    BoundedMonotonicClock clock(policy, Timestamp{0});
    std::vector<AlarmCode> clock_alarms;
    clock.setAlarmSink([&clock_alarms](const netsec::Alarm& alarm) {
        clock_alarms.push_back(alarm.code);
    });
    CHECK(clock.now(Timestamp{0}) == Timestamp{0});
    SyncSample jump;
    jump.master_time = Timestamp{10s};
    jump.local_time = Timestamp{1s};
    CHECK(clock.synchronize(jump).accepted);
    CHECK(!clock_alarms.empty());
    CHECK(clock_alarms.back() == AlarmCode::TimeJumpExceedsLimit);
}

}  // namespace

int main() {
    testHmacVector();
    testClockIdentityParsing();
    testLegitimateMasterAccepted();
    testForgedBetterMasterCannotLowerSelection();
    testMissingAndTamperedAuthentication();
    testPriorityAndStratumPolicy();
    testReplayAndExpiry();
    testSequenceNumberWrap();
    testStaleAnnouncementAndGrandmasterPin();
    testBmcaChoosesBestAdmittedMaster();
    testJumpIsBoundedAndMonotonic();
    testBackwardCorrectionNeverMovesBackwards();
    testLeapsAndDriftAreBounded();
    testGuardBindsSyncToSelectedMaster();
    testAlarmSinksReceiveSecurityAndTimeAlarms();

    if (failures != 0) {
        std::cerr << failures << " test check(s) failed\n";
        return 1;
    }
    std::cout << "all netsec tests passed\n";
    return 0;
}
