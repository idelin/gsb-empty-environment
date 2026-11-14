#pragma once

#include <cstddef>
#include <cstdint>
#include <memory>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

enum class Constellation : std::uint8_t {
    Gps,
    Glonass,
    Galileo,
    BeiDou,
    Qzss,
    NavIc,
    Other,
    Unknown,
};

enum class FrequencyBand : std::uint8_t {
    L1,
    L2,
    L5,
    E1,
    E5a,
    E5b,
    E6,
    B1,
    B2,
    B3,
    G1,
    G2,
    Other,
    Unknown,
};

enum class TrustLevel : std::uint8_t {
    Trusted,
    Degraded,
    ManualConfirmation,
    Rejected,
};

enum class IssueCode : std::uint8_t {
    InvalidInput,
    NoFix,
    InsufficientRedundancy,
    SingleConstellation,
    SingleFrequency,
    LowSignal,
    ExcessiveSignal,
    SignalJump,
    ToaInconsistency,
    EphemerisMismatch,
    EphemerisStale,
    SourceAbruptChange,
    SourceOutlier,
    SourceDisagreement,
    PositionJump,
    ImpossibleSpeed,
    ImuDisagreement,
    MapViolation,
    TimeAnomaly,
    UnsupportedReportedFix,
};

enum class Severity : std::uint8_t {
    Info,
    Warning,
    Critical,
};

struct GeoPoint {
    double latitude_deg{};
    double longitude_deg{};
    double altitude_m{};
};

// One independent position solution per constellation/frequency combination.
// C/N0, TOA residual and ephemeris fields can come directly from the receiver
// measurement engine; the position is required for cross-source checking.
struct GnssSourceFix {
    std::string source_id;
    Constellation constellation{Constellation::Unknown};
    FrequencyBand band{FrequencyBand::Unknown};
    GeoPoint position{};
    double horizontal_accuracy_m{5.0};
    double vertical_accuracy_m{10.0};
    std::uint64_t timestamp_ns{};
    double cn0_dbhz{40.0};
    std::optional<double> expected_cn0_dbhz{};
    double toa_residual_ns{};
    bool ephemeris_valid{true};
    bool ephemeris_consistent{true};
    bool signal_healthy{true};
    std::optional<std::uint64_t> ephemeris_id{};
    double ephemeris_age_s{};
    unsigned satellite_count{4};
};

struct ReceiverFix {
    GeoPoint position{};
    double horizontal_accuracy_m{5.0};
    double vertical_accuracy_m{10.0};
    std::uint64_t timestamp_ns{};
    double cn0_dbhz{40.0};
    double time_residual_ns{};
    bool valid{true};
};

struct InertialPrediction {
    bool available{};
    GeoPoint position{};
    double horizontal_accuracy_m{10.0};
    double speed_mps{};
    std::uint64_t timestamp_ns{};
};

struct MapContext {
    bool available{};
    bool position_allowed{true};

    // Either provide a route polyline and/or a precomputed distance. When a
    // route is present it takes precedence over the precomputed value.
    std::vector<GeoPoint> route;
    std::optional<double> distance_to_route_m{};

    // Optional geofence. Points on the boundary are treated as inside.
    std::vector<GeoPoint> allowed_polygon;
};

struct TrustInput {
    std::uint64_t timestamp_ns{};
    ReceiverFix receiver_fix{};
    std::vector<GnssSourceFix> source_fixes;
    InertialPrediction inertial{};
    MapContext map{};
};

struct TrustConfig {
    // Signal and measurement quality.
    double min_cn0_dbhz{24.0};
    double max_cn0_dbhz{60.0};
    double max_expected_cn0_excess_db{10.0};
    double peer_cn0_outlier_db{15.0};
    double max_cn0_jump_db{8.0};
    double max_toa_residual_ns{250.0};
    double max_ephemeris_age_s{14'400.0};

    // Source diversity and cross-source agreement.
    std::size_t min_sources{2};
    std::size_t min_constellations{2};
    std::size_t min_frequency_bands{2};
    double source_disagreement_m{30.0};
    double max_receiver_consensus_error_m{35.0};

    // Motion, time and map guard rails.
    double max_source_jump_m{250.0};
    double max_position_jump_m{75.0};
    double max_speed_mps{60.0};
    double max_imu_disagreement_m{45.0};
    double max_time_skew_ns{200'000'000.0};
    double max_map_distance_m{20.0};
    double history_window_s{60.0};

    // Any critical issue always requires manual review, irrespective of the
    // numeric score.
    double trusted_score{0.93};
    double manual_review_score{0.55};
    double info_penalty{0.015};
    double warning_penalty{0.10};
    double critical_penalty{0.28};
    double consensus_penalty{0.20};
};

struct TrustIssue {
    IssueCode code{IssueCode::InvalidInput};
    Severity severity{Severity::Warning};
    std::string source_id;
    std::string detail;
    double magnitude{};
};

struct TrustDecision {
    TrustLevel level{TrustLevel::Rejected};
    double trust_score{};

    // Trusted and Degraded are accepted. Degraded carries warnings but has no
    // position-integrity conflict. ManualConfirmation and Rejected are never
    // accepted as navigation truth.
    bool accepted{};
    bool requires_manual_confirmation{true};
    bool offset_detected{};

    GeoPoint reported_position{};
    double observed_offset_m{};
    std::optional<GeoPoint> consensus_position{};
    std::optional<GeoPoint> safe_position{};
    std::vector<TrustIssue> issues{};
    std::string explanation;

    [[nodiscard]] bool has_issue(IssueCode code) const noexcept;
    [[nodiscard]] bool has_critical_issue() const noexcept;
};

class PositioningTrustComponent {
public:
    explicit PositioningTrustComponent(TrustConfig config = {});
    ~PositioningTrustComponent();

    PositioningTrustComponent(const PositioningTrustComponent&) = delete;
    PositioningTrustComponent& operator=(const PositioningTrustComponent&) = delete;
    PositioningTrustComponent(PositioningTrustComponent&&) noexcept;
    PositioningTrustComponent& operator=(PositioningTrustComponent&&) noexcept;

    // Stateful and thread-safe. History is used for position-jump, source
    // power/ephemeris transitions and impossible-speed checks.
    [[nodiscard]] TrustDecision evaluate(const TrustInput& input);
    void reset();

    [[nodiscard]] TrustConfig config() const;
    void set_config(TrustConfig config);

private:
    struct Impl;
    static TrustDecision evaluate_impl(Impl& state, const TrustInput& input);
    std::unique_ptr<Impl> impl_;
};

using PositionTrustEvaluator = PositioningTrustComponent;

[[nodiscard]] std::string_view to_string(Constellation value) noexcept;
[[nodiscard]] std::string_view to_string(FrequencyBand value) noexcept;
[[nodiscard]] std::string_view to_string(TrustLevel value) noexcept;
[[nodiscard]] std::string_view to_string(IssueCode value) noexcept;
[[nodiscard]] std::string_view to_string(Severity value) noexcept;

}  // namespace netsec
