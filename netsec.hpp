#pragma once

#include <cstddef>
#include <cstdint>
#include <memory>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

using Timestamp = std::int64_t;

enum class MessageType : int {
    ClassAPosition1 = 1,
    ClassAPosition2 = 2,
    ClassAPosition3 = 3,
    StaticVoyage = 5,
    ClassBPosition = 18,
    ClassBExtended = 19,
    ClassBStatic = 24,
    LongRangePosition = 27,
};

enum class FindingSeverity {
    Info,
    Warning,
    Critical,
};

enum class FindingCode {
    InvalidMessageType,
    MissingIdentifier,
    InvalidIdentifier,
    InvalidImo,
    InvalidName,
    InvalidCallsign,
    InvalidDestination,
    MissingPosition,
    InvalidPosition,
    InvalidSpeed,
    InvalidCourse,
    InvalidTimestamp,
    FutureObservation,
    StaleObservation,
    MissingStaticProfile,
    ExpiredStaticProfile,
    StaticDynamicMismatch,
    IdentityConflict,
    PositionJump,
    DuplicateMessage,
    ReplayDetected,
    TimestampRegression,
    MultiSourceConflict,
    TrackGap,
};

enum class TrustLevel {
    Unknown,
    Unverified,
    Suspect,
    Untrusted,
    Trusted,
};

struct Position {
    double latitude{0.0};
    double longitude{0.0};
};

struct Finding {
    FindingCode code{FindingCode::InvalidMessageType};
    FindingSeverity severity{FindingSeverity::Critical};
    std::string detail;
};

struct Policy {
    double max_plausible_speed_knots{60.0};
    double position_noise_m{500.0};
    double multi_source_distance_m{2000.0};

    Timestamp duplicate_window_seconds{120};
    Timestamp replay_window_seconds{6 * 60 * 60};
    Timestamp source_sync_window_seconds{30};
    Timestamp track_gap_seconds{30 * 60};
    Timestamp track_retention_seconds{6 * 60 * 60};
    Timestamp static_conflict_window_seconds{6 * 60 * 60};
    Timestamp static_profile_ttl_seconds{24 * 60 * 60};
    Timestamp max_transport_delay_seconds{10 * 60};
    Timestamp future_tolerance_seconds{60};
    Timestamp out_of_order_tolerance_seconds{15};

    std::size_t max_track_points{1024};
};

struct StaticReport {
    MessageType message_type{MessageType::StaticVoyage};
    std::string mmsi;
    std::string imo;
    std::string name;
    std::string callsign;
    std::string destination;
    std::string source_id{"default"};

    // AIS type 24 has part A (name) and part B (callsign). Zero infers the
    // part from the fields that are present.
    int part_number{0};
    Timestamp generated_at{0};
    Timestamp received_at{0};
    std::uint64_t sequence{0};
};

struct DynamicReport {
    MessageType message_type{MessageType::ClassAPosition1};
    std::string mmsi;
    double latitude{0.0};
    double longitude{0.0};
    double speed_over_ground_knots{0.0};
    double course_over_ground_degrees{0.0};
    double true_heading_degrees{511.0};
    std::string source_id{"default"};

    // AIS type 19 carries a name. The other claim fields are optional; when
    // supplied they are cross-checked against the static profile.
    std::string claimed_name;
    std::string claimed_callsign;
    std::string claimed_destination;

    Timestamp observed_at{0};
    Timestamp received_at{0};
    std::uint64_t sequence{0};
};

struct ShipProfile {
    std::string mmsi;
    std::string imo;
    std::string name;
    std::string callsign;
    std::string destination;
    std::string last_source_id;
    std::vector<std::string> source_ids;
    Timestamp first_seen{0};
    Timestamp last_seen{0};
};

struct TrackPoint {
    Position position;
    Timestamp observed_at{0};
    Timestamp received_at{0};
    std::string source_id;
    MessageType message_type{MessageType::ClassAPosition1};
};

struct Assessment {
    bool valid{false};
    bool accepted{false};
    bool trusted{false};
    TrustLevel trust{TrustLevel::Unknown};
    std::string mmsi;
    Timestamp observed_at{0};
    std::vector<Finding> findings;
    std::size_t track_points{0};
    std::size_t source_count{0};

    [[nodiscard]] bool has(FindingCode code) const noexcept;
    [[nodiscard]] bool has_critical() const noexcept;
    [[nodiscard]] bool has_warning() const noexcept;
};

using IdentityAssessment = Assessment;
using VerificationResult = Assessment;

class ShipIdentityMonitor {
public:
    explicit ShipIdentityMonitor(Policy policy = {});
    ~ShipIdentityMonitor();

    ShipIdentityMonitor(const ShipIdentityMonitor& other);
    ShipIdentityMonitor& operator=(const ShipIdentityMonitor& other);
    ShipIdentityMonitor(ShipIdentityMonitor&& other) noexcept;
    ShipIdentityMonitor& operator=(ShipIdentityMonitor&& other) noexcept;

    Assessment ingest(const StaticReport& report);
    Assessment ingest(const DynamicReport& report);

    Assessment ingest_static(const StaticReport& report) {
        return ingest(report);
    }
    Assessment ingest_dynamic(const DynamicReport& report) {
        return ingest(report);
    }

    [[nodiscard]] std::optional<ShipProfile> profile(std::string_view mmsi) const;
    [[nodiscard]] std::vector<TrackPoint> track(std::string_view mmsi) const;
    [[nodiscard]] const Policy& policy() const noexcept;

    void reset();

private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
};

using ShipIdentityService = ShipIdentityMonitor;
using ShipIdentityVerifier = ShipIdentityMonitor;
using IdentityMonitor = ShipIdentityMonitor;

[[nodiscard]] bool is_static_message(MessageType type) noexcept;
[[nodiscard]] bool is_dynamic_message(MessageType type) noexcept;
[[nodiscard]] bool is_valid_mmsi(std::string_view value);
[[nodiscard]] bool is_valid_imo(std::string_view value);
[[nodiscard]] bool is_valid_position(const Position& position) noexcept;

[[nodiscard]] const char* to_string(MessageType type) noexcept;
[[nodiscard]] const char* to_string(FindingCode code) noexcept;
[[nodiscard]] const char* to_string(FindingSeverity severity) noexcept;
[[nodiscard]] const char* to_string(TrustLevel level) noexcept;

}  // namespace netsec
