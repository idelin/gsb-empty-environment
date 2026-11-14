#include "netsec.hpp"

#include <algorithm>
#include <chrono>
#include <cmath>
#include <cstdint>
#include <deque>
#include <limits>
#include <optional>
#include <string>
#include <unordered_map>
#include <utility>

namespace netsec {
namespace {

constexpr double kKnotsToMetresPerSecond = 0.5144444444444445;
constexpr double kEarthRadiusM = 6371008.8;
constexpr double kPositionEpsilon = 1e-9;
constexpr double kPositionEqualityEpsilon = 1e-6;

[[nodiscard]] Timestamp now_seconds() noexcept {
    const auto now = std::chrono::system_clock::now().time_since_epoch();
    return std::chrono::duration_cast<std::chrono::seconds>(now).count();
}

[[nodiscard]] std::string trim(std::string_view value) {
    std::size_t first = 0;
    while (first < value.size() &&
           (value[first] == ' ' || value[first] == '\t' || value[first] == '\r' ||
            value[first] == '\n')) {
        ++first;
    }
    std::size_t last = value.size();
    while (last > first &&
           (value[last - 1] == ' ' || value[last - 1] == '\t' ||
            value[last - 1] == '\r' || value[last - 1] == '\n')) {
        --last;
    }
    return std::string(value.substr(first, last - first));
}

[[nodiscard]] bool is_ascii_digit(char value) noexcept {
    return value >= '0' && value <= '9';
}

[[nodiscard]] bool is_ascii_alnum(char value) noexcept {
    return is_ascii_digit(value) || (value >= 'A' && value <= 'Z') ||
           (value >= 'a' && value <= 'z');
}

[[nodiscard]] bool is_printable_ascii(char value) noexcept {
    const auto byte = static_cast<unsigned char>(value);
    return byte >= 0x20U && byte <= 0x7eU;
}

[[nodiscard]] char ascii_upper(char value) noexcept {
    if (value >= 'a' && value <= 'z') {
        return static_cast<char>(value - 'a' + 'A');
    }
    return value;
}

[[nodiscard]] std::string canonical_text(std::string_view value) {
    const std::string trimmed = trim(value);
    std::string canonical;
    canonical.reserve(trimmed.size());
    bool previous_space = false;
    for (const char ch : trimmed) {
        const char upper = ascii_upper(ch);
        if (upper == ' ') {
            if (!previous_space && !canonical.empty()) {
                canonical.push_back(' ');
            }
            previous_space = true;
        } else {
            canonical.push_back(upper);
            previous_space = false;
        }
    }
    while (!canonical.empty() && canonical.back() == ' ') {
        canonical.pop_back();
    }
    return canonical;
}

[[nodiscard]] bool valid_text(std::string_view value, std::size_t max_length,
                              bool required) {
    const std::string trimmed = trim(value);
    if (trimmed.empty()) {
        return !required;
    }
    if (trimmed.size() > max_length) {
        return false;
    }
    return std::all_of(trimmed.begin(), trimmed.end(), is_printable_ascii);
}

[[nodiscard]] bool valid_callsign(std::string_view value, bool required) {
    const std::string trimmed = trim(value);
    if (trimmed.empty()) {
        return !required;
    }
    if (trimmed.size() > 7) {
        return false;
    }
    return std::all_of(trimmed.begin(), trimmed.end(), is_ascii_alnum);
}

[[nodiscard]] bool finite(double value) noexcept {
    return std::isfinite(value);
}

[[nodiscard]] bool same_position(const Position& lhs, const Position& rhs) noexcept {
    return std::abs(lhs.latitude - rhs.latitude) <= kPositionEqualityEpsilon &&
           std::abs(lhs.longitude - rhs.longitude) <= kPositionEqualityEpsilon;
}

[[nodiscard]] double haversine_m(const Position& lhs, const Position& rhs) noexcept {
    constexpr double degrees_to_radians = 0.017453292519943295;
    const double lat1 = lhs.latitude * degrees_to_radians;
    const double lat2 = rhs.latitude * degrees_to_radians;
    const double dlat = (rhs.latitude - lhs.latitude) * degrees_to_radians;
    const double dlon = (rhs.longitude - lhs.longitude) * degrees_to_radians;

    const double sin_lat = std::sin(dlat / 2.0);
    const double sin_lon = std::sin(dlon / 2.0);
    const double a =
        sin_lat * sin_lat + std::cos(lat1) * std::cos(lat2) * sin_lon * sin_lon;
    const double clamped = std::clamp(a, 0.0, 1.0);
    return 2.0 * kEarthRadiusM * std::asin(std::sqrt(clamped));
}

[[nodiscard]] std::string normalize_mmsi(std::string_view value) {
    return trim(value);
}

[[nodiscard]] std::string normalize_imo(std::string_view value) {
    std::string compact;
    compact.reserve(value.size());
    for (const char ch : value) {
        if (ch == ' ' || ch == '\t' || ch == '-') {
            continue;
        }
        compact.push_back(ascii_upper(ch));
    }
    if (compact.size() > 3 && compact.rfind("IMO", 0) == 0) {
        compact.erase(0, 3);
    }
    return compact;
}

[[nodiscard]] Timestamp effective_times(Timestamp observed, Timestamp received,
                                         Timestamp& effective_observed) noexcept {
    const Timestamp fallback = now_seconds();
    const Timestamp rec = received > 0 ? received : (observed > 0 ? observed : fallback);
    effective_observed = observed > 0 ? observed : rec;
    return rec;
}

[[nodiscard]] bool has_field(std::string_view value) noexcept {
    return !trim(value).empty();
}

void add_finding(Assessment& assessment, FindingCode code, FindingSeverity severity,
                 std::string detail) {
    assessment.findings.push_back(Finding{code, severity, std::move(detail)});
}

[[nodiscard]] bool has_different_text(const std::string& existing,
                                      std::string_view candidate) {
    return !existing.empty() && has_field(candidate) &&
           existing != canonical_text(candidate);
}

struct SeenObservation {
    Position position;
    Timestamp observed_at{0};
    std::string source_id;
    MessageType message_type{MessageType::ClassAPosition1};
};

struct SourceObservation {
    Position position;
    Timestamp observed_at{0};
    std::string source_id;
};

struct Claim {
    std::string value;
    Timestamp observed_at{0};
};

struct VesselState {
    ShipProfile profile;
    bool has_profile{false};
    bool has_imo{false};
    bool has_name{false};
    bool has_callsign{false};
    bool has_destination{false};

    std::string last_static_fingerprint;
    std::string last_static_source;
    Timestamp last_static_received{0};
    Timestamp last_static_generated{0};

    Claim dynamic_name;
    Claim dynamic_callsign;
    Claim dynamic_destination;

    std::deque<TrackPoint> track;
    std::deque<SourceObservation> source_observations;
    std::deque<SeenObservation> seen_observations;
};

[[nodiscard]] bool claim_conflicts(const Claim& claim, std::string_view candidate,
                                   Timestamp now, Timestamp window) {
    return !claim.value.empty() && has_field(candidate) && claim.value != canonical_text(candidate) &&
           now >= claim.observed_at && now - claim.observed_at <= window;
}

void remember_claim(Claim& claim, std::string_view value, Timestamp observed_at) {
    if (!has_field(value)) {
        return;
    }
    claim.value = canonical_text(value);
    claim.observed_at = std::max(claim.observed_at, observed_at);
}

void add_source(VesselState& state, std::string_view source_id) {
    std::string source = source_id.empty() ? "default" : std::string(source_id);
    if (std::find(state.profile.source_ids.begin(), state.profile.source_ids.end(), source) ==
        state.profile.source_ids.end()) {
        state.profile.source_ids.push_back(std::move(source));
    }
}

void prune_state(VesselState& state, Timestamp now, const Policy& policy) {
    const Timestamp track_cutoff = now - std::max<Timestamp>(0, policy.track_retention_seconds);
    while (!state.track.empty() && state.track.front().observed_at < track_cutoff) {
        state.track.pop_front();
    }
    while (state.track.size() > policy.max_track_points) {
        state.track.pop_front();
    }

    const Timestamp source_cutoff =
        now - std::max<Timestamp>(policy.source_sync_window_seconds * 2, 60);
    while (!state.source_observations.empty() &&
           state.source_observations.front().observed_at < source_cutoff) {
        state.source_observations.pop_front();
    }
    while (state.source_observations.size() > 128) {
        state.source_observations.pop_front();
    }

    const Timestamp replay_cutoff = now - std::max<Timestamp>(0, policy.replay_window_seconds);
    while (!state.seen_observations.empty() &&
           state.seen_observations.front().observed_at < replay_cutoff) {
        state.seen_observations.pop_front();
    }
    while (state.seen_observations.size() > std::max<std::size_t>(2048, policy.max_track_points)) {
        state.seen_observations.pop_front();
    }
}

[[nodiscard]] const TrackPoint* latest_track_point(const VesselState& state) {
    return state.track.empty() ? nullptr : &state.track.back();
}

[[nodiscard]] bool seen_exact(const VesselState& state, const DynamicReport& report,
                              const Position& position, Timestamp observed_at,
                              bool require_same_source, bool& newer_exists) {
    newer_exists = false;
    bool exact = false;
    for (const SeenObservation& seen : state.seen_observations) {
        if (seen.observed_at > observed_at + 1) {
            newer_exists = true;
        }
        if (seen.observed_at == observed_at && seen.message_type == report.message_type &&
            same_position(seen.position, position) &&
            (!require_same_source || seen.source_id == report.source_id)) {
            exact = true;
        }
    }
    return exact;
}

[[nodiscard]] bool replay_window_conflict(const VesselState& state, Position position,
                                          Timestamp observed_at, Timestamp window) {
    const TrackPoint* latest = latest_track_point(state);
    if (latest != nullptr && latest->observed_at > observed_at + 1 &&
        latest->observed_at - observed_at <= window && same_position(latest->position, position)) {
        return true;
    }
    for (const SeenObservation& seen : state.seen_observations) {
        if (seen.observed_at > observed_at + 1 && seen.observed_at - observed_at <= window &&
            same_position(seen.position, position)) {
            return true;
        }
    }
    return false;
}

[[nodiscard]] bool multi_source_conflict(const VesselState& state,
                                         const DynamicReport& report,
                                         const Position& position, Timestamp observed_at,
                                         const Policy& policy, std::string& other_source) {
    if (policy.multi_source_distance_m < 0.0) {
        return false;
    }
    for (const SourceObservation& observation : state.source_observations) {
        if (observation.source_id == report.source_id) {
            continue;
        }
        if (std::abs(observation.observed_at - observed_at) >
            std::max<Timestamp>(0, policy.source_sync_window_seconds)) {
            continue;
        }
        if (haversine_m(observation.position, position) > policy.multi_source_distance_m) {
            other_source = observation.source_id;
            return true;
        }
    }
    return false;
}

[[nodiscard]] bool dynamic_claim_mismatch(const VesselState& state,
                                          const DynamicReport& report,
                                          Timestamp observed_at, const Policy& policy,
                                          std::string& detail) {
    if (!state.has_profile) {
        return false;
    }

    const auto check = [&](const std::string& existing, std::string_view candidate,
                           const char* label) -> bool {
        if (has_different_text(existing, candidate)) {
            detail = std::string(label) + " claim does not match static profile";
            return true;
        }
        return false;
    };

    if (check(state.profile.name, report.claimed_name, "name") ||
        check(state.profile.callsign, report.claimed_callsign, "callsign") ||
        check(state.profile.destination, report.claimed_destination, "destination")) {
        return true;
    }

    if (state.profile.last_seen > 0 && observed_at > 0 &&
        state.profile.last_seen - observed_at > policy.static_profile_ttl_seconds) {
        return false;
    }
    return false;
}

[[nodiscard]] bool static_claim_mismatch(const VesselState& state,
                                         const StaticReport& report, Timestamp now,
                                         const Policy& policy, std::string& detail) {
    if (state.has_profile) {
        const auto check = [&](bool present, const std::string& existing,
                               std::string_view candidate, const char* label) -> bool {
            if (present && has_field(candidate) &&
                existing != canonical_text(candidate) && now >= state.profile.last_seen &&
                now - state.profile.last_seen <= policy.static_conflict_window_seconds) {
                detail = std::string(label) + " conflicts with the existing static profile";
                return true;
            }
            return false;
        };

        if (check(state.has_imo, state.profile.imo, report.imo, "IMO") ||
            check(state.has_name, state.profile.name, report.name, "name") ||
            check(state.has_callsign, state.profile.callsign, report.callsign,
                  "callsign") ||
            check(state.has_destination, state.profile.destination,
                  report.destination, "destination")) {
            return true;
        }
    }

    if (state.dynamic_name.observed_at > 0 &&
        claim_conflicts(state.dynamic_name, report.name, now,
                        policy.static_conflict_window_seconds)) {
        detail = "name conflicts with a previously observed dynamic claim";
        return true;
    }
    if (state.dynamic_callsign.observed_at > 0 &&
        claim_conflicts(state.dynamic_callsign, report.callsign, now,
                        policy.static_conflict_window_seconds)) {
        detail = "callsign conflicts with a previously observed dynamic claim";
        return true;
    }
    if (state.dynamic_destination.observed_at > 0 &&
        claim_conflicts(state.dynamic_destination, report.destination, now,
                        policy.static_conflict_window_seconds)) {
        detail = "destination conflicts with a previously observed dynamic claim";
        return true;
    }
    return false;
}

[[nodiscard]] std::string static_fingerprint(const StaticReport& report) {
    return canonical_text(report.name) + "|" + canonical_text(report.callsign) + "|" +
           canonical_text(report.destination) + "|" + normalize_imo(report.imo) + "|" +
           std::to_string(static_cast<int>(report.message_type)) + "|" +
           std::to_string(report.part_number);
}

[[nodiscard]] std::string report_source(const std::string& source_id) {
    return source_id.empty() ? "default" : source_id;
}

void update_profile_from_static(VesselState& state, const StaticReport& report,
                                Timestamp received_at, bool update_imo, bool update_name,
                                bool update_callsign, bool update_destination) {
    ShipProfile& profile = state.profile;
    profile.mmsi = normalize_mmsi(report.mmsi);
    if (profile.first_seen == 0) {
        profile.first_seen = received_at;
    }
    profile.last_seen = std::max(profile.last_seen, received_at);
    profile.last_source_id = report_source(report.source_id);

    if (update_imo && has_field(report.imo)) {
        profile.imo = normalize_imo(report.imo);
        state.has_imo = true;
    }
    if (update_name && has_field(report.name)) {
        profile.name = canonical_text(report.name);
        state.has_name = true;
    }
    if (update_callsign && has_field(report.callsign)) {
        profile.callsign = canonical_text(report.callsign);
        state.has_callsign = true;
    }
    if (update_destination && has_field(report.destination)) {
        profile.destination = canonical_text(report.destination);
        state.has_destination = true;
    }
    state.has_profile = true;
}

void expire_profile_if_needed(VesselState& state, Timestamp received_at,
                              const Policy& policy, Assessment& assessment) {
    if (!state.has_profile || state.profile.last_seen <= 0) {
        return;
    }
    if (received_at > state.profile.last_seen &&
        received_at - state.profile.last_seen > policy.static_profile_ttl_seconds) {
        const std::string mmsi = state.profile.mmsi;
        const std::vector<std::string> sources = state.profile.source_ids;
        state = VesselState{};
        state.profile.mmsi = mmsi;
        state.profile.source_ids = sources;
        add_finding(assessment, FindingCode::ExpiredStaticProfile, FindingSeverity::Info,
                    "previous static profile exceeded its validity window");
    }
}

void finalize_trust(Assessment& assessment, bool has_current_static_profile) {
    if (!assessment.valid) {
        assessment.accepted = false;
        assessment.trusted = false;
        assessment.trust = TrustLevel::Unknown;
        return;
    }

    assessment.accepted = !assessment.has_critical();
    if (assessment.has_critical()) {
        assessment.trust = TrustLevel::Untrusted;
    } else if (assessment.has_warning()) {
        assessment.trust = TrustLevel::Suspect;
    } else if (has_current_static_profile) {
        assessment.trust = TrustLevel::Trusted;
    } else {
        assessment.trust = TrustLevel::Unverified;
    }
    assessment.trusted = assessment.trust == TrustLevel::Trusted;
}

}  // namespace

bool Assessment::has(FindingCode code) const noexcept {
    return std::any_of(findings.begin(), findings.end(),
                       [code](const Finding& finding) { return finding.code == code; });
}

bool Assessment::has_critical() const noexcept {
    return std::any_of(findings.begin(), findings.end(), [](const Finding& finding) {
        return finding.severity == FindingSeverity::Critical;
    });
}

bool Assessment::has_warning() const noexcept {
    return std::any_of(findings.begin(), findings.end(), [](const Finding& finding) {
        return finding.severity == FindingSeverity::Warning;
    });
}

bool is_static_message(MessageType type) noexcept {
    return type == MessageType::StaticVoyage || type == MessageType::ClassBStatic;
}

bool is_dynamic_message(MessageType type) noexcept {
    switch (type) {
        case MessageType::ClassAPosition1:
        case MessageType::ClassAPosition2:
        case MessageType::ClassAPosition3:
        case MessageType::ClassBPosition:
        case MessageType::ClassBExtended:
        case MessageType::LongRangePosition:
            return true;
        default:
            return false;
    }
}

bool is_valid_mmsi(std::string_view value) {
    const std::string trimmed = trim(value);
    if (trimmed.size() != 9) {
        return false;
    }
    if (!std::all_of(trimmed.begin(), trimmed.end(), is_ascii_digit)) {
        return false;
    }
    if (trimmed[0] < '2' || trimmed[0] > '7') {
        return false;
    }
    const int mid = (trimmed[0] - '0') * 100 + (trimmed[1] - '0') * 10 +
                    (trimmed[2] - '0');
    return mid >= 201 && mid <= 775;
}

bool is_valid_imo(std::string_view value) {
    const std::string compact = normalize_imo(value);
    if (compact.size() != 7 ||
        !std::all_of(compact.begin(), compact.end(), is_ascii_digit)) {
        return false;
    }
    int sum = 0;
    for (std::size_t index = 0; index < 6; ++index) {
        sum += (compact[index] - '0') * static_cast<int>(7 - index);
    }
    return sum % 10 == compact[6] - '0';
}

bool is_valid_position(const Position& position) noexcept {
    if (!finite(position.latitude) || !finite(position.longitude)) {
        return false;
    }
    if (position.latitude < -90.0 || position.latitude > 90.0 ||
        position.longitude < -180.0 || position.longitude > 180.0) {
        return false;
    }
    return !(std::abs(position.latitude) <= kPositionEpsilon &&
             std::abs(position.longitude) <= kPositionEpsilon);
}

const char* to_string(MessageType type) noexcept {
    switch (type) {
        case MessageType::ClassAPosition1:
            return "AIS-1";
        case MessageType::ClassAPosition2:
            return "AIS-2";
        case MessageType::ClassAPosition3:
            return "AIS-3";
        case MessageType::StaticVoyage:
            return "AIS-5";
        case MessageType::ClassBPosition:
            return "AIS-18";
        case MessageType::ClassBExtended:
            return "AIS-19";
        case MessageType::ClassBStatic:
            return "AIS-24";
        case MessageType::LongRangePosition:
            return "AIS-27";
    }
    return "unknown";
}

const char* to_string(FindingCode code) noexcept {
    switch (code) {
        case FindingCode::InvalidMessageType:
            return "invalid-message-type";
        case FindingCode::MissingIdentifier:
            return "missing-identifier";
        case FindingCode::InvalidIdentifier:
            return "invalid-identifier";
        case FindingCode::InvalidImo:
            return "invalid-imo";
        case FindingCode::InvalidName:
            return "invalid-name";
        case FindingCode::InvalidCallsign:
            return "invalid-callsign";
        case FindingCode::InvalidDestination:
            return "invalid-destination";
        case FindingCode::MissingPosition:
            return "missing-position";
        case FindingCode::InvalidPosition:
            return "invalid-position";
        case FindingCode::InvalidSpeed:
            return "invalid-speed";
        case FindingCode::InvalidCourse:
            return "invalid-course";
        case FindingCode::InvalidTimestamp:
            return "invalid-timestamp";
        case FindingCode::FutureObservation:
            return "future-observation";
        case FindingCode::StaleObservation:
            return "stale-observation";
        case FindingCode::MissingStaticProfile:
            return "missing-static-profile";
        case FindingCode::ExpiredStaticProfile:
            return "expired-static-profile";
        case FindingCode::StaticDynamicMismatch:
            return "static-dynamic-mismatch";
        case FindingCode::IdentityConflict:
            return "identity-conflict";
        case FindingCode::PositionJump:
            return "position-jump";
        case FindingCode::DuplicateMessage:
            return "duplicate-message";
        case FindingCode::ReplayDetected:
            return "replay-detected";
        case FindingCode::TimestampRegression:
            return "timestamp-regression";
        case FindingCode::MultiSourceConflict:
            return "multi-source-conflict";
        case FindingCode::TrackGap:
            return "track-gap";
    }
    return "unknown";
}

const char* to_string(FindingSeverity severity) noexcept {
    switch (severity) {
        case FindingSeverity::Info:
            return "info";
        case FindingSeverity::Warning:
            return "warning";
        case FindingSeverity::Critical:
            return "critical";
    }
    return "unknown";
}

const char* to_string(TrustLevel level) noexcept {
    switch (level) {
        case TrustLevel::Unknown:
            return "unknown";
        case TrustLevel::Unverified:
            return "unverified";
        case TrustLevel::Suspect:
            return "suspect";
        case TrustLevel::Untrusted:
            return "untrusted";
        case TrustLevel::Trusted:
            return "trusted";
    }
    return "unknown";
}

struct ShipIdentityMonitor::Impl {
    explicit Impl(Policy value) : policy(value) {}

    Policy policy;
    std::unordered_map<std::string, VesselState> vessels;
};

ShipIdentityMonitor::ShipIdentityMonitor(Policy policy)
    : impl_(std::make_unique<Impl>(policy)) {}

ShipIdentityMonitor::~ShipIdentityMonitor() = default;

ShipIdentityMonitor::ShipIdentityMonitor(const ShipIdentityMonitor& other)
    : impl_(std::make_unique<Impl>(*other.impl_)) {}

ShipIdentityMonitor& ShipIdentityMonitor::operator=(const ShipIdentityMonitor& other) {
    if (this != &other) {
        impl_ = std::make_unique<Impl>(*other.impl_);
    }
    return *this;
}

ShipIdentityMonitor::ShipIdentityMonitor(ShipIdentityMonitor&& other) noexcept = default;

ShipIdentityMonitor& ShipIdentityMonitor::operator=(ShipIdentityMonitor&& other) noexcept =
    default;

Assessment ShipIdentityMonitor::ingest(const StaticReport& report) {
    Assessment assessment;
    assessment.mmsi = normalize_mmsi(report.mmsi);

    if (!is_static_message(report.message_type)) {
        add_finding(assessment, FindingCode::InvalidMessageType, FindingSeverity::Critical,
                    "message type is not a static ship identity report");
        return assessment;
    }
    if (!has_field(report.mmsi)) {
        add_finding(assessment, FindingCode::MissingIdentifier, FindingSeverity::Critical,
                    "MMSI is required");
        return assessment;
    }
    if (!is_valid_mmsi(report.mmsi)) {
        add_finding(assessment, FindingCode::InvalidIdentifier, FindingSeverity::Critical,
                    "MMSI must be nine digits with a valid ship MID");
        return assessment;
    }
    if (report.generated_at < 0 || report.received_at < 0) {
        add_finding(assessment, FindingCode::InvalidTimestamp, FindingSeverity::Critical,
                    "timestamps cannot be negative");
        return assessment;
    }

    assessment.valid = true;
    assessment.observed_at = report.received_at > 0
                                 ? report.received_at
                                 : (report.generated_at > 0 ? report.generated_at
                                                            : now_seconds());

    const bool is_type_5 = report.message_type == MessageType::StaticVoyage;
    int part = report.part_number;
    if (report.message_type == MessageType::ClassBStatic) {
        if (part < 0 || part > 2) {
            add_finding(assessment, FindingCode::InvalidMessageType, FindingSeverity::Critical,
                        "AIS-24 part must be 0 (infer), 1 (A), or 2 (B)");
            assessment.valid = false;
            return assessment;
        }
        if (part == 0) {
            if (!has_field(report.name) && !has_field(report.callsign)) {
                add_finding(assessment, FindingCode::InvalidMessageType,
                            FindingSeverity::Critical,
                            "AIS-24 must carry a name (part A) or callsign (part B)");
                assessment.valid = false;
                return assessment;
            }
            part = has_field(report.name) ? 1 : 2;
        }
    } else {
        part = 0;
    }

    const bool name_required = is_type_5 || part == 1;
    const bool callsign_required = is_type_5 || part == 2;
    if (!valid_text(report.name, 20, name_required)) {
        add_finding(assessment, FindingCode::InvalidName, FindingSeverity::Critical,
                    "name is missing or not valid printable AIS text (max 20)");
    }
    if (!valid_callsign(report.callsign, callsign_required)) {
        add_finding(assessment, FindingCode::InvalidCallsign, FindingSeverity::Critical,
                    "callsign is missing or not 1-7 alphanumeric characters");
    }
    if (!valid_text(report.destination, 20, is_type_5)) {
        add_finding(assessment, FindingCode::InvalidDestination, FindingSeverity::Critical,
                    "destination is missing or not valid printable AIS text (max 20)");
    }
    if (has_field(report.imo) && !is_valid_imo(report.imo)) {
        add_finding(assessment, FindingCode::InvalidImo, FindingSeverity::Critical,
                    "IMO number must be seven digits with a valid check digit");
    }
    if (!assessment.valid || assessment.has_critical()) {
        assessment.valid = false;
        return assessment;
    }

    const Timestamp received_at = assessment.observed_at;
    if (report.generated_at > 0 &&
        report.generated_at > received_at + impl_->policy.future_tolerance_seconds) {
        add_finding(assessment, FindingCode::FutureObservation, FindingSeverity::Critical,
                    "static report generation time is in the future");
    }
    if (report.generated_at > 0 && report.received_at > 0 &&
        report.received_at - report.generated_at >
            impl_->policy.max_transport_delay_seconds) {
        add_finding(assessment, FindingCode::ReplayDetected, FindingSeverity::Critical,
                    "static report exceeded the permitted transport delay");
    }

    VesselState& state = impl_->vessels[assessment.mmsi];
    if (state.profile.mmsi.empty()) {
        state.profile.mmsi = assessment.mmsi;
    }
    prune_state(state, received_at, impl_->policy);
    expire_profile_if_needed(state, received_at, impl_->policy, assessment);

    if (report.generated_at > 0 && state.last_static_generated > 0 &&
        report.generated_at + impl_->policy.out_of_order_tolerance_seconds <
            state.last_static_generated) {
        const bool exact_repeat = static_fingerprint(report) == state.last_static_fingerprint;
        add_finding(assessment,
                    exact_repeat ? FindingCode::ReplayDetected
                                 : FindingCode::TimestampRegression,
                    exact_repeat ? FindingSeverity::Critical : FindingSeverity::Warning,
                    "static report timestamp regressed behind an earlier report");
    }

    std::string conflict_detail;
    if (static_claim_mismatch(state, report, received_at, impl_->policy,
                              conflict_detail)) {
        add_finding(assessment, FindingCode::IdentityConflict, FindingSeverity::Critical,
                    std::move(conflict_detail));
    }

    const std::string fingerprint = static_fingerprint(report);
    const bool duplicate =
        !fingerprint.empty() && fingerprint == state.last_static_fingerprint &&
        report_source(report.source_id) == state.last_static_source &&
        state.last_static_received > 0 &&
        received_at >= state.last_static_received &&
        received_at - state.last_static_received <= impl_->policy.duplicate_window_seconds;
    if (duplicate) {
        add_finding(assessment, FindingCode::DuplicateMessage, FindingSeverity::Info,
                    "identical static report repeated inside the duplicate window");
    }

    if (!assessment.has_critical()) {
        const bool update_imo =
            has_field(report.imo) &&
            (!state.has_imo || state.profile.imo == normalize_imo(report.imo) ||
             received_at - state.profile.last_seen >
                 impl_->policy.static_conflict_window_seconds);
        const bool update_name =
            has_field(report.name) &&
            (!state.has_name || state.profile.name == canonical_text(report.name) ||
             received_at - state.profile.last_seen >
                 impl_->policy.static_conflict_window_seconds);
        const bool update_callsign =
            has_field(report.callsign) &&
            (!state.has_callsign ||
             state.profile.callsign == canonical_text(report.callsign) ||
             received_at - state.profile.last_seen >
                 impl_->policy.static_conflict_window_seconds);
        const bool update_destination =
            has_field(report.destination) &&
            (!state.has_destination ||
             state.profile.destination == canonical_text(report.destination) ||
             received_at - state.profile.last_seen >
                 impl_->policy.static_conflict_window_seconds);

        update_profile_from_static(state, report, received_at, update_imo, update_name,
                                   update_callsign, update_destination);
        state.last_static_fingerprint = fingerprint;
        state.last_static_source = report_source(report.source_id);
        state.last_static_received = std::max(state.last_static_received, received_at);
        if (report.generated_at > 0) {
            state.last_static_generated =
                std::max(state.last_static_generated, report.generated_at);
        }
        add_source(state, report.source_id);
    }

    assessment.track_points = state.track.size();
    assessment.source_count = state.profile.source_ids.size();
    finalize_trust(assessment, state.has_profile);
    return assessment;
}

Assessment ShipIdentityMonitor::ingest(const DynamicReport& report) {
    Assessment assessment;
    assessment.mmsi = normalize_mmsi(report.mmsi);

    if (!is_dynamic_message(report.message_type)) {
        add_finding(assessment, FindingCode::InvalidMessageType, FindingSeverity::Critical,
                    "message type is not a dynamic position report");
        return assessment;
    }
    if (!has_field(report.mmsi)) {
        add_finding(assessment, FindingCode::MissingIdentifier, FindingSeverity::Critical,
                    "MMSI is required");
        return assessment;
    }
    if (!is_valid_mmsi(report.mmsi)) {
        add_finding(assessment, FindingCode::InvalidIdentifier, FindingSeverity::Critical,
                    "MMSI must be nine digits with a valid ship MID");
        return assessment;
    }
    if (report.observed_at < 0 || report.received_at < 0) {
        add_finding(assessment, FindingCode::InvalidTimestamp, FindingSeverity::Critical,
                    "timestamps cannot be negative");
        return assessment;
    }

    const Position position{report.latitude, report.longitude};
    if ((std::abs(report.latitude) <= kPositionEpsilon &&
         std::abs(report.longitude) <= kPositionEpsilon)) {
        add_finding(assessment, FindingCode::MissingPosition, FindingSeverity::Critical,
                    "position was not supplied (0,0 sentinel)");
        return assessment;
    }
    if (!is_valid_position(position)) {
        add_finding(assessment, FindingCode::InvalidPosition, FindingSeverity::Critical,
                    "latitude/longitude is non-finite or outside valid range");
        return assessment;
    }
    if (!finite(report.speed_over_ground_knots) ||
        report.speed_over_ground_knots < 0.0 ||
        report.speed_over_ground_knots > 102.3) {
        add_finding(assessment, FindingCode::InvalidSpeed, FindingSeverity::Critical,
                    "speed over ground must be in [0, 102.3] knots");
    }
    if (!finite(report.course_over_ground_degrees) ||
        report.course_over_ground_degrees < 0.0 ||
        report.course_over_ground_degrees > 360.0) {
        add_finding(assessment, FindingCode::InvalidCourse, FindingSeverity::Critical,
                    "course over ground must be in [0, 360] degrees");
    }
    if (!finite(report.true_heading_degrees) ||
        ((report.true_heading_degrees < 0.0 || report.true_heading_degrees > 359.0) &&
         std::abs(report.true_heading_degrees - 511.0) > kPositionEpsilon)) {
        add_finding(assessment, FindingCode::InvalidCourse, FindingSeverity::Critical,
                    "true heading must be in [0, 359] or the 511 unavailable value");
    }
    if (report.message_type == MessageType::ClassBExtended &&
        !valid_text(report.claimed_name, 20, false)) {
        add_finding(assessment, FindingCode::InvalidName, FindingSeverity::Critical,
                    "extended Class B name is not valid printable AIS text");
    }
    if (has_field(report.claimed_callsign) &&
        !valid_callsign(report.claimed_callsign, false)) {
        add_finding(assessment, FindingCode::InvalidCallsign, FindingSeverity::Critical,
                    "dynamic callsign claim is not 1-7 alphanumeric characters");
    }
    if (has_field(report.claimed_destination) &&
        !valid_text(report.claimed_destination, 20, false)) {
        add_finding(assessment, FindingCode::InvalidDestination, FindingSeverity::Critical,
                    "dynamic destination claim is not valid printable AIS text");
    }
    if (assessment.has_critical()) {
        return assessment;
    }

    Timestamp observed_at = 0;
    const Timestamp received_at =
        effective_times(report.observed_at, report.received_at, observed_at);
    assessment.valid = true;
    assessment.observed_at = observed_at;

    if (observed_at > received_at + impl_->policy.future_tolerance_seconds) {
        add_finding(assessment, FindingCode::FutureObservation, FindingSeverity::Critical,
                    "observation time is in the future");
    }
    if (received_at - observed_at > impl_->policy.max_transport_delay_seconds) {
        add_finding(assessment, FindingCode::StaleObservation, FindingSeverity::Warning,
                    "report exceeded the permitted transport delay");
    }

    VesselState& state = impl_->vessels[assessment.mmsi];
    if (state.profile.mmsi.empty()) {
        state.profile.mmsi = assessment.mmsi;
    }
    prune_state(state, received_at, impl_->policy);

    add_source(state, report.source_id);

    std::string other_source;
    if (multi_source_conflict(state, report, position, observed_at, impl_->policy,
                              other_source)) {
        add_finding(assessment, FindingCode::MultiSourceConflict,
                    FindingSeverity::Critical,
                    "position disagrees with source " + other_source +
                        " inside the synchronization window");
    }

    bool newer_exists = false;
    const bool exact_same_source =
        seen_exact(state, report, position, observed_at, true, newer_exists);
    bool exact_any_source = exact_same_source;
    if (!exact_any_source) {
        bool unused_newer = false;
        exact_any_source =
            seen_exact(state, report, position, observed_at, false, unused_newer);
    }

    bool duplicate = false;
    bool replayed = false;
    bool out_of_order = false;
    if (exact_same_source) {
        if (newer_exists ||
            replay_window_conflict(state, position, observed_at,
                                   impl_->policy.replay_window_seconds)) {
            add_finding(assessment, FindingCode::ReplayDetected,
                        FindingSeverity::Critical,
                        "an older observation was replayed after newer traffic");
            replayed = true;
        } else {
            add_finding(assessment, FindingCode::DuplicateMessage, FindingSeverity::Info,
                        "identical position report repeated from the same source");
            duplicate = true;
        }
    } else {
        const TrackPoint* latest = latest_track_point(state);
        if (latest != nullptr &&
            observed_at + impl_->policy.out_of_order_tolerance_seconds <
                latest->observed_at) {
            if (replay_window_conflict(state, position, observed_at,
                                       impl_->policy.replay_window_seconds)) {
                add_finding(assessment, FindingCode::ReplayDetected,
                            FindingSeverity::Critical,
                            "timestamp regression matches an older track position");
                replayed = true;
            } else {
                add_finding(assessment, FindingCode::TimestampRegression,
                            FindingSeverity::Warning,
                            "observation timestamp is behind the current track");
                out_of_order = true;
            }
        }
    }

    if (!exact_any_source && !replayed && !out_of_order) {
        const TrackPoint* latest = latest_track_point(state);
        if (latest != nullptr) {
            const Timestamp delta = observed_at - latest->observed_at;
            if (delta > impl_->policy.track_gap_seconds) {
                add_finding(assessment, FindingCode::TrackGap, FindingSeverity::Info,
                            "physical continuity check skipped across a track gap");
            } else if (delta >= 0) {
                const double maximum_distance =
                    std::max(0.0, impl_->policy.max_plausible_speed_knots) *
                        kKnotsToMetresPerSecond * static_cast<double>(delta) +
                    std::max(0.0, impl_->policy.position_noise_m);
                const double distance = haversine_m(latest->position, position);
                if (distance > maximum_distance) {
                    add_finding(assessment, FindingCode::PositionJump,
                                FindingSeverity::Critical,
                                "position is not reachable from the previous track point "
                                "within the speed and noise envelope");
                }
            }
        }
    }

    const bool has_current_profile =
        state.has_profile && state.profile.last_seen > 0 &&
        received_at <= state.profile.last_seen + impl_->policy.static_profile_ttl_seconds;
    if (!state.has_profile) {
        add_finding(assessment, FindingCode::MissingStaticProfile, FindingSeverity::Info,
                    "no static identity report has been correlated with this MMSI");
    } else if (!has_current_profile) {
        add_finding(assessment, FindingCode::ExpiredStaticProfile, FindingSeverity::Info,
                    "correlated static profile is outside its validity window");
    } else {
        std::string mismatch_detail;
        if (dynamic_claim_mismatch(state, report, observed_at, impl_->policy,
                                   mismatch_detail)) {
            add_finding(assessment, FindingCode::StaticDynamicMismatch,
                        FindingSeverity::Critical, std::move(mismatch_detail));
        }
    }

    const auto check_dynamic_claim = [&](const Claim& previous, std::string_view current,
                                         const char* label) {
        if (claim_conflicts(previous, current, received_at,
                            impl_->policy.static_conflict_window_seconds)) {
            add_finding(assessment, FindingCode::IdentityConflict,
                        FindingSeverity::Critical,
                        std::string("dynamic ") + label +
                            " claim conflicts with an earlier observation");
        }
    };
    check_dynamic_claim(state.dynamic_name, report.claimed_name, "name");
    check_dynamic_claim(state.dynamic_callsign, report.claimed_callsign, "callsign");
    check_dynamic_claim(state.dynamic_destination, report.claimed_destination,
                        "destination");

    if (!replayed && !out_of_order) {
        remember_claim(state.dynamic_name, report.claimed_name, observed_at);
        remember_claim(state.dynamic_callsign, report.claimed_callsign, observed_at);
        remember_claim(state.dynamic_destination, report.claimed_destination, observed_at);
    }

    const bool can_append =
        !assessment.has_critical() && !duplicate && !exact_any_source && !replayed &&
        !out_of_order;
    if (can_append) {
        state.track.push_back(TrackPoint{position, observed_at, received_at,
                                         report_source(report.source_id),
                                         report.message_type});
        while (state.track.size() > impl_->policy.max_track_points) {
            state.track.pop_front();
        }
    }

    if (!replayed && !out_of_order) {
        state.source_observations.push_back(
            SourceObservation{position, observed_at, report_source(report.source_id)});
        state.seen_observations.push_back(
            SeenObservation{position, observed_at, report_source(report.source_id),
                            report.message_type});
        while (state.source_observations.size() > 128) {
            state.source_observations.pop_front();
        }
        while (state.seen_observations.size() >
               std::max<std::size_t>(2048, impl_->policy.max_track_points)) {
            state.seen_observations.pop_front();
        }
    }

    assessment.track_points = state.track.size();
    assessment.source_count = state.profile.source_ids.size();
    finalize_trust(assessment, has_current_profile);
    return assessment;
}

std::optional<ShipProfile> ShipIdentityMonitor::profile(std::string_view mmsi) const {
    const auto iterator = impl_->vessels.find(normalize_mmsi(mmsi));
    if (iterator == impl_->vessels.end() || !iterator->second.has_profile) {
        return std::nullopt;
    }
    ShipProfile result = iterator->second.profile;
    std::sort(result.source_ids.begin(), result.source_ids.end());
    return result;
}

std::vector<TrackPoint> ShipIdentityMonitor::track(std::string_view mmsi) const {
    const auto iterator = impl_->vessels.find(normalize_mmsi(mmsi));
    if (iterator == impl_->vessels.end()) {
        return {};
    }
    return {iterator->second.track.begin(), iterator->second.track.end()};
}

const Policy& ShipIdentityMonitor::policy() const noexcept {
    return impl_->policy;
}

void ShipIdentityMonitor::reset() {
    impl_->vessels.clear();
}

}  // namespace netsec
