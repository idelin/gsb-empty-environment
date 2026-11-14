#pragma once

#include <array>
#include <chrono>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <memory>
#include <optional>
#include <span>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

using Timestamp = std::chrono::nanoseconds;

// ---------------------------------------------------------------------------
// PTP identities and announcement data
// ---------------------------------------------------------------------------

struct ClockIdentity {
    std::array<std::uint8_t, 8> bytes{};

    auto operator<=>(const ClockIdentity&) const = default;

    [[nodiscard]] bool isZero() const noexcept;
    [[nodiscard]] std::string toString() const;
    [[nodiscard]] std::string to_string() const {
        return toString();
    }
    [[nodiscard]] static std::optional<ClockIdentity> fromString(std::string_view text);
    [[nodiscard]] static std::optional<ClockIdentity> from_string(
        std::string_view text) {
        return fromString(text);
    }
};

struct PortIdentity {
    ClockIdentity clock_identity{};
    std::uint16_t port_number{1};

    PortIdentity() = default;
    PortIdentity(const ClockIdentity& identity, std::uint16_t port = 1) noexcept
        : clock_identity(identity), port_number(port) {}

    auto operator<=>(const PortIdentity&) const = default;
    [[nodiscard]] bool isZero() const noexcept;
};

// The numeric values follow the PTP clockQuality conventions. A smaller value
// is better for every field; stratum uses the usual NTP-like convention where
// zero is best.
struct ClockQuality {
    std::uint8_t priority1{128};
    std::uint8_t clock_class{248};
    std::uint8_t clock_accuracy{0xFE};
    std::uint16_t offset_scaled_log_variance{0xFFFF};
    std::uint8_t priority2{128};
    std::uint8_t stratum{255};

    auto operator<=>(const ClockQuality&) const = default;
};

using AuthTag = std::array<std::uint8_t, 32>;

struct Authentication {
    std::uint32_t key_id{0};
    AuthTag tag{};

    auto operator<=>(const Authentication&) const = default;
    [[nodiscard]] bool isEmpty() const noexcept;
};

struct Announcement {
    std::uint8_t version{2};
    std::uint8_t domain{0};
    PortIdentity source{};
    ClockIdentity grandmaster{};
    ClockQuality quality{};
    std::uint16_t sequence_id{0};
    std::uint16_t steps_removed{0};
    std::optional<Timestamp> transmit_time{};
    std::optional<std::int32_t> utc_offset_seconds{};
    std::optional<std::int8_t> leap_seconds{};
    std::optional<Authentication> authentication{};

    auto operator<=>(const Announcement&) const = default;

    [[nodiscard]] bool hasAuthentication() const noexcept;
    [[nodiscard]] ClockIdentity effectiveGrandmaster() const noexcept;
};

struct PriorityRange {
    std::uint8_t minimum{0};
    std::uint8_t maximum{255};

    [[nodiscard]] bool contains(std::uint8_t value) const noexcept;
    auto operator<=>(const PriorityRange&) const = default;
};

struct TrustedMaster {
    ClockIdentity clock_identity{};
    std::vector<std::uint8_t> shared_secret{};

    // A configured port pins the exact PTP port. Empty means any port of the
    // trusted clock identity is allowed.
    std::optional<std::uint16_t> port_number{};
    std::optional<std::uint32_t> key_id{};
    std::optional<ClockIdentity> expected_grandmaster_identity{};

    // Optional per-master restrictions layered on top of AdmissionPolicy.
    std::optional<PriorityRange> allowed_priority1{};
    std::optional<PriorityRange> allowed_priority2{};
    std::optional<std::uint8_t> min_stratum{};
    std::optional<std::uint8_t> max_stratum{};
    std::optional<std::uint8_t> max_clock_class{};

    // Optional pins prevent a trusted source from silently improving its
    // advertised quality outside the operator-approved values.
    std::optional<std::uint8_t> expected_priority1{};
    std::optional<std::uint8_t> expected_priority2{};
    std::optional<std::uint8_t> expected_stratum{};
    std::optional<std::uint8_t> expected_clock_class{};

    bool enabled{true};
};

enum class BmcaMode {
    // IEEE 1588 dataset comparison: priority1, clockClass, accuracy,
    // variance, priority2, stratum tie-break, grandmaster identity,
    // stepsRemoved, source.
    Ptp1588,
    // A stricter policy-first profile: priority1, stratum, priority2,
    // clockClass, then identity.
    PriorityStratumIdentity,
};

struct AdmissionPolicy {
    // Secure default. Setting this to false is a legacy escape hatch and
    // cannot bypass authentication for a trusted master that has key material.
    bool require_authentication{true};
    bool require_grandmaster_in_trust_list{false};
    std::uint8_t domain{0};
    std::uint8_t minimum_version{2};
    std::uint8_t maximum_version{2};

    PriorityRange priority1_range{0, 255};
    PriorityRange priority2_range{0, 255};
    std::uint8_t min_stratum{0};
    std::uint8_t max_stratum{255};
    std::uint8_t max_clock_class{255};
    std::uint8_t max_clock_accuracy{0xFF};
    std::uint16_t max_offset_scaled_log_variance{0xFFFF};

    // Announcement freshness is checked only when transmit_time is present.
    bool enforce_announcement_timestamps{true};
    Timestamp max_announcement_age{std::chrono::seconds(5)};
    Timestamp max_future_skew{std::chrono::seconds(1)};
    Timestamp candidate_holdover{std::chrono::seconds(5)};

    // A signed announcement reusing or rolling back a sequence number is a
    // replay even when the HMAC is valid.
    bool enforce_sequence_monotonicity{true};

    BmcaMode bmca_mode{BmcaMode::Ptp1588};
};

enum class RejectionReason {
    None,
    MalformedAnnouncement,
    VersionUnsupported,
    DomainMismatch,
    UntrustedSource,
    TrustedSourceDisabled,
    AuthenticationRequired,
    AuthenticationFailed,
    KeyIdMismatch,
    ReplayDetected,
    StaleAnnouncement,
    FutureAnnouncement,
    GrandmasterMismatch,
    PriorityOutOfPolicy,
    StratumOutOfPolicy,
    ClockClassOutOfPolicy,
    ClockAccuracyOutOfPolicy,
    VarianceOutOfPolicy,
    NoSelectedMaster,
    SyncSourceMismatch,
};

[[nodiscard]] std::string_view toString(RejectionReason reason) noexcept;

enum class AlarmCode {
    None,
    InvalidAnnouncement,
    UntrustedSource,
    AuthenticationMissing,
    AuthenticationFailed,
    ReplayDetected,
    StaleAnnouncement,
    PolicyViolation,
    GrandmasterMismatch,
    MasterChanged,
    NoTrustedSource,
    TimeJumpExceedsLimit,
    BackwardCorrectionSuppressed,
    LeapSecond,
    LeapSecondOutOfBounds,
    DriftExceedsLimit,
    UncertaintyExceedsLimit,
    InvalidTimeSample,
    SyncSourceMismatch,
};

enum class Severity {
    Info,
    Warning,
    Critical,
};

[[nodiscard]] std::string_view toString(AlarmCode code) noexcept;
[[nodiscard]] std::string_view toString(Severity severity) noexcept;

struct Alarm {
    AlarmCode code{AlarmCode::None};
    Severity severity{Severity::Warning};
    std::string detail{};
    Timestamp at{};

    [[nodiscard]] bool isCritical() const noexcept {
        return severity == Severity::Critical;
    }
};

struct SelectedMaster {
    PortIdentity source{};
    ClockIdentity grandmaster{};
    ClockQuality quality{};
    std::uint16_t sequence_id{0};
    std::uint16_t steps_removed{0};
    Timestamp received_at{};
    Timestamp expires_at{};

    auto operator<=>(const SelectedMaster&) const = default;
};

struct AdmissionResult {
    bool accepted{false};
    RejectionReason reason{RejectionReason::None};
    std::optional<SelectedMaster> selected{};
    Timestamp evaluated_at{};
    std::vector<Alarm> alarms{};

    [[nodiscard]] explicit operator bool() const noexcept {
        return accepted;
    }

    [[nodiscard]] bool hasAlarm(AlarmCode code) const noexcept;
};

// ---------------------------------------------------------------------------
// Authentication helpers
// ---------------------------------------------------------------------------

[[nodiscard]] std::vector<std::uint8_t> canonicalAnnouncementPayload(
    const Announcement& announcement, std::uint32_t key_id);

[[nodiscard]] AuthTag hmacSha256(
    std::span<const std::uint8_t> key,
    std::span<const std::uint8_t> message);

// Signs all announcement fields except the authentication container itself.
// The key id is covered by the MAC to prevent key-id downgrade attacks.
[[nodiscard]] Announcement signAnnouncement(
    Announcement announcement,
    std::span<const std::uint8_t> key,
    std::uint32_t key_id = 0);
[[nodiscard]] Announcement signAnnouncement(
    Announcement announcement,
    std::string_view key,
    std::uint32_t key_id = 0);

// Verifies both presence and validity. expected_key_id is optional; when
// supplied it must match the key id carried by the announcement.
[[nodiscard]] bool verifyAuthentication(
    const Announcement& announcement,
    std::span<const std::uint8_t> key,
    std::optional<std::uint32_t> expected_key_id = std::nullopt);
[[nodiscard]] bool verifyAuthentication(
    const Announcement& announcement,
    std::string_view key,
    std::optional<std::uint32_t> expected_key_id = std::nullopt);

[[nodiscard]] std::vector<std::uint8_t> keyFromHex(std::string_view hex);
[[nodiscard]] std::vector<std::uint8_t> keyFromText(std::string_view text);

[[nodiscard]] TrustedMaster makeTrustedMaster(
    ClockIdentity identity,
    std::string_view shared_secret,
    std::optional<std::uint16_t> port_number = std::nullopt,
    std::optional<std::uint32_t> key_id = std::nullopt);

[[nodiscard]] Announcement makeAnnouncement(
    ClockIdentity source,
    std::uint8_t priority1,
    std::uint8_t stratum,
    std::uint16_t sequence_id,
    std::uint8_t clock_class = 6);

[[nodiscard]] Announcement makeAnnouncement(
    PortIdentity source,
    ClockQuality quality,
    std::uint16_t sequence_id,
    ClockIdentity grandmaster = {},
    std::uint16_t steps_removed = 0);

// ---------------------------------------------------------------------------
// Trusted announcement admission and BMCA
// ---------------------------------------------------------------------------

class MasterClockAdmission {
public:
    explicit MasterClockAdmission(
        AdmissionPolicy policy = {},
        std::vector<TrustedMaster> trusted_masters = {});
    MasterClockAdmission(
        std::vector<TrustedMaster> trusted_masters,
        AdmissionPolicy policy = {});

    MasterClockAdmission(const MasterClockAdmission&) = delete;
    MasterClockAdmission& operator=(const MasterClockAdmission&) = delete;
    MasterClockAdmission(MasterClockAdmission&&) noexcept;
    MasterClockAdmission& operator=(MasterClockAdmission&&) noexcept;
    ~MasterClockAdmission();

    [[nodiscard]] bool addTrustedMaster(TrustedMaster master);
    [[nodiscard]] bool upsertTrustedMaster(TrustedMaster master);
    [[nodiscard]] bool removeTrustedMaster(const ClockIdentity& identity);
    void clearTrustedMasters();
    void resetSourceState(const ClockIdentity& identity);

    [[nodiscard]] std::vector<TrustedMaster> trustedMasters() const;
    [[nodiscard]] AdmissionPolicy policy() const;
    void setPolicy(AdmissionPolicy policy);

    [[nodiscard]] AdmissionResult submit(
        const Announcement& announcement,
        Timestamp received_at);
    [[nodiscard]] AdmissionResult submit(const Announcement& announcement);
    [[nodiscard]] AdmissionResult submitAnnouncement(
        const Announcement& announcement,
        Timestamp received_at);
    [[nodiscard]] AdmissionResult submitAnnouncement(
        const Announcement& announcement);
    [[nodiscard]] AdmissionResult admit(
        const Announcement& announcement,
        Timestamp received_at);
    [[nodiscard]] AdmissionResult admit(const Announcement& announcement);

    [[nodiscard]] std::optional<SelectedMaster> selected(Timestamp now);
    [[nodiscard]] std::optional<SelectedMaster> bestMaster(Timestamp now);
    [[nodiscard]] std::optional<SelectedMaster> bestMaster() const;
    [[nodiscard]] std::optional<SelectedMaster> selectedMaster() const;
    [[nodiscard]] std::optional<SelectedMaster> currentMaster() const;

    void expire(Timestamp now);
    [[nodiscard]] std::size_t admittedCandidateCount() const;

    void setAlarmSink(std::function<void(const Alarm&)> sink);

private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
};

// ---------------------------------------------------------------------------
// Bounded monotonic time correction
// ---------------------------------------------------------------------------

enum class JumpPolicy {
    // Reject a correction larger than max_step. The previous time is held.
    Reject,
    // Apply at most max_step and slew the remainder.
    Clamp,
    // Do not step; slew toward the target within max_slew_ppm.
    Slew,
};

struct ClockSafetyPolicy {
    Timestamp max_step{std::chrono::seconds(1)};
    Timestamp max_leap_adjustment{std::chrono::seconds(1)};
    double max_slew_ppm{500.0};
    double max_drift_ppm{100.0};
    Timestamp max_uncertainty{std::chrono::seconds(1)};
    double correction_horizon_seconds{60.0};
    bool allow_forward_step{true};
    JumpPolicy jump_policy{JumpPolicy::Reject};
};

struct SyncSample {
    Timestamp master_time{};
    Timestamp local_time{};
    Timestamp uncertainty{};
    std::optional<double> frequency_ppm{};
    std::optional<std::int32_t> utc_offset_seconds{};
    std::optional<std::int8_t> leap_seconds{};
    PortIdentity source{};
};

struct TimeUpdateResult {
    bool accepted{false};
    Timestamp before{};
    Timestamp after{};
    Timestamp target{};
    Timestamp offset{};
    Timestamp applied_step{};
    double applied_rate_ppm{0.0};
    bool monotonic{true};
    bool stepped{false};
    bool correction_applied{false};
    bool jump_rejected{false};
    std::vector<Alarm> alarms{};

    [[nodiscard]] explicit operator bool() const noexcept {
        return accepted;
    }

    [[nodiscard]] bool hasAlarm(AlarmCode code) const noexcept;
};

class BoundedMonotonicClock {
public:
    explicit BoundedMonotonicClock(
        ClockSafetyPolicy policy = {},
        Timestamp initial_time = Timestamp{0});
    BoundedMonotonicClock(
        Timestamp initial_time,
        ClockSafetyPolicy policy);
    ~BoundedMonotonicClock();

    BoundedMonotonicClock(const BoundedMonotonicClock&) = delete;
    BoundedMonotonicClock& operator=(const BoundedMonotonicClock&) = delete;
    BoundedMonotonicClock(BoundedMonotonicClock&&) noexcept;
    BoundedMonotonicClock& operator=(BoundedMonotonicClock&&) noexcept;

    [[nodiscard]] Timestamp now(Timestamp local_now) const;
    [[nodiscard]] TimeUpdateResult synchronize(const SyncSample& sample);
    [[nodiscard]] TimeUpdateResult applyTimeUpdate(const SyncSample& sample);
    [[nodiscard]] TimeUpdateResult update(const SyncSample& sample) {
        return synchronize(sample);
    }

    [[nodiscard]] ClockSafetyPolicy policy() const;
    void setPolicy(ClockSafetyPolicy policy);
    void setAlarmSink(std::function<void(const Alarm&)> sink);

private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
};

// ---------------------------------------------------------------------------
// Convenience facade for a time synchronization service
// ---------------------------------------------------------------------------

class TimeSyncGuard {
public:
    TimeSyncGuard(
        AdmissionPolicy admission_policy,
        std::vector<TrustedMaster> trusted_masters,
        ClockSafetyPolicy clock_policy = {},
        Timestamp initial_time = Timestamp{0});
    TimeSyncGuard(
        std::vector<TrustedMaster> trusted_masters,
        ClockSafetyPolicy clock_policy = {},
        Timestamp initial_time = Timestamp{0});
    ~TimeSyncGuard();

    TimeSyncGuard(const TimeSyncGuard&) = delete;
    TimeSyncGuard& operator=(const TimeSyncGuard&) = delete;
    TimeSyncGuard(TimeSyncGuard&&) noexcept;
    TimeSyncGuard& operator=(TimeSyncGuard&&) noexcept;

    [[nodiscard]] AdmissionResult submitAnnouncement(
        const Announcement& announcement,
        Timestamp received_at);
    [[nodiscard]] AdmissionResult submitAnnouncement(
        const Announcement& announcement);
    [[nodiscard]] AdmissionResult submit(
        const Announcement& announcement,
        Timestamp received_at) {
        return submitAnnouncement(announcement, received_at);
    }
    [[nodiscard]] AdmissionResult submit(const Announcement& announcement) {
        return submitAnnouncement(announcement);
    }

    // A Sync sample is applied only when it comes from the currently selected
    // admitted master. This binds time corrections to the authenticated BMCA
    // result instead of merely relying on the caller's source label.
    [[nodiscard]] TimeUpdateResult submitSync(const SyncSample& sample);
    [[nodiscard]] TimeUpdateResult submitSync(
        const ClockIdentity& source,
        const SyncSample& sample);
    [[nodiscard]] TimeUpdateResult applySync(const SyncSample& sample) {
        return submitSync(sample);
    }

    [[nodiscard]] Timestamp now(Timestamp local_now) const;
    [[nodiscard]] std::optional<SelectedMaster> selectedMaster(Timestamp now);

    [[nodiscard]] MasterClockAdmission& admission();
    [[nodiscard]] const MasterClockAdmission& admission() const;
    [[nodiscard]] BoundedMonotonicClock& clock();
    [[nodiscard]] const BoundedMonotonicClock& clock() const;

    void setAlarmSink(std::function<void(const Alarm&)> sink);

private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
};

using MasterAdmission = MasterClockAdmission;
using PtpAdmission = MasterClockAdmission;
using PtpAdmissionController = MasterClockAdmission;
using AdmissionController = MasterClockAdmission;
using MonotonicClock = BoundedMonotonicClock;
using TimeGuard = TimeSyncGuard;
using TimeSourceGuard = TimeSyncGuard;

}  // namespace netsec
