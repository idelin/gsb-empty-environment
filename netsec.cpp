#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cmath>
#include <iomanip>
#include <limits>
#include <mutex>
#include <sstream>
#include <unordered_map>
#include <utility>

namespace netsec {
namespace {

constexpr double kPi = 3.14159265358979323846;
constexpr double kEarthRadiusM = 6'371'008.8;

bool finite(double value) {
    return std::isfinite(value);
}

bool valid_point(const GeoPoint& point) {
    return finite(point.latitude_deg) && finite(point.longitude_deg) &&
           finite(point.altitude_m) && point.latitude_deg >= -90.0 &&
           point.latitude_deg <= 90.0 && point.longitude_deg >= -180.0 &&
           point.longitude_deg <= 180.0;
}

double clamp_score(double value) {
    return std::clamp(value, 0.0, 1.0);
}

double horizontal_distance_m(const GeoPoint& lhs, const GeoPoint& rhs) {
    const double lat1 = lhs.latitude_deg * kPi / 180.0;
    const double lat2 = rhs.latitude_deg * kPi / 180.0;
    const double dlat = lat2 - lat1;
    double dlon = (rhs.longitude_deg - lhs.longitude_deg) * kPi / 180.0;
    if (dlon > kPi) {
        dlon -= 2.0 * kPi;
    } else if (dlon < -kPi) {
        dlon += 2.0 * kPi;
    }

    const double sin_dlat = std::sin(dlat * 0.5);
    const double sin_dlon = std::sin(dlon * 0.5);
    const double a = sin_dlat * sin_dlat +
                     std::cos(lat1) * std::cos(lat2) * sin_dlon * sin_dlon;
    return 2.0 * kEarthRadiusM * std::asin(std::sqrt(std::clamp(a, 0.0, 1.0)));
}

double distance_3d_m(const GeoPoint& lhs, const GeoPoint& rhs) {
    const double horizontal = horizontal_distance_m(lhs, rhs);
    const double vertical = rhs.altitude_m - lhs.altitude_m;
    return std::hypot(horizontal, vertical);
}

GeoPoint weighted_center(const std::vector<const GnssSourceFix*>& sources,
                         const std::vector<double>& weights,
                         const std::vector<std::size_t>& indices) {
    double total = 0.0;
    double lat = 0.0;
    double lon = 0.0;
    double alt = 0.0;
    for (const std::size_t index : indices) {
        const double weight = weights[index];
        total += weight;
        lat += sources[index]->position.latitude_deg * weight;
        lon += sources[index]->position.longitude_deg * weight;
        alt += sources[index]->position.altitude_m * weight;
    }
    if (total <= 0.0) {
        return sources[indices.front()]->position;
    }
    return GeoPoint{lat / total, lon / total, alt / total};
}

double segment_distance_m(const GeoPoint& point, const GeoPoint& start,
                          const GeoPoint& end) {
    const double reference_lat =
        (point.latitude_deg + start.latitude_deg + end.latitude_deg) / 3.0;
    const double cos_lat = std::cos(reference_lat * kPi / 180.0);
    constexpr double meters_per_degree = kEarthRadiusM * kPi / 180.0;

    auto project = [cos_lat](const GeoPoint& value) {
        return std::array<double, 2>{
            value.longitude_deg * meters_per_degree * cos_lat,
            value.latitude_deg * meters_per_degree,
        };
    };

    const auto p = project(point);
    const auto a = project(start);
    const auto b = project(end);
    const double dx = b[0] - a[0];
    const double dy = b[1] - a[1];
    const double length_squared = dx * dx + dy * dy;
    double t = 0.0;
    if (length_squared > 0.0) {
        t = std::clamp(
            ((p[0] - a[0]) * dx + (p[1] - a[1]) * dy) / length_squared, 0.0,
            1.0);
    }
    return std::hypot(p[0] - (a[0] + t * dx), p[1] - (a[1] + t * dy));
}

std::optional<double> route_distance_m(const MapContext& map,
                                       const GeoPoint& point) {
    if (!valid_point(point)) {
        return std::nullopt;
    }
    if (map.route.size() >= 2) {
        double best = std::numeric_limits<double>::infinity();
        bool evaluated = false;
        for (std::size_t i = 1; i < map.route.size(); ++i) {
            if (!valid_point(map.route[i - 1]) || !valid_point(map.route[i])) {
                continue;
            }
            best = std::min(
                best, segment_distance_m(point, map.route[i - 1], map.route[i]));
            evaluated = true;
        }
        if (evaluated) {
            return best;
        }
    }
    if (map.distance_to_route_m.has_value() &&
        finite(*map.distance_to_route_m) && *map.distance_to_route_m >= 0.0) {
        return map.distance_to_route_m;
    }
    return std::nullopt;
}

bool point_in_polygon(const GeoPoint& point,
                      const std::vector<GeoPoint>& polygon) {
    if (polygon.size() < 3) {
        return true;
    }

    for (std::size_t i = 0; i < polygon.size(); ++i) {
        const GeoPoint& a = polygon[i];
        const GeoPoint& b = polygon[(i + 1) % polygon.size()];
        if (valid_point(a) && valid_point(b) &&
            segment_distance_m(point, a, b) <= 0.5) {
            return true;
        }
    }

    bool inside = false;
    for (std::size_t i = 0, j = polygon.size() - 1; i < polygon.size();
         j = i++) {
        const GeoPoint& a = polygon[i];
        const GeoPoint& b = polygon[j];
        if (!valid_point(a) || !valid_point(b)) {
            continue;
        }

        const bool crosses_latitude =
            (a.latitude_deg > point.latitude_deg) !=
            (b.latitude_deg > point.latitude_deg);
        if (!crosses_latitude) {
            continue;
        }
        const double denominator = b.latitude_deg - a.latitude_deg;
        if (std::abs(denominator) < std::numeric_limits<double>::epsilon()) {
            continue;
        }
        const double intersection_longitude =
            (b.longitude_deg - a.longitude_deg) *
                (point.latitude_deg - a.latitude_deg) / denominator +
            a.longitude_deg;
        if (point.longitude_deg < intersection_longitude) {
            inside = !inside;
        }
    }
    return inside;
}

double median(std::vector<double> values) {
    if (values.empty()) {
        return 0.0;
    }
    const auto middle =
        values.begin() + static_cast<std::vector<double>::difference_type>(
                             values.size() / 2);
    std::nth_element(values.begin(), middle, values.end());
    const double upper = *middle;
    if (values.size() % 2 != 0) {
        return upper;
    }
    const double lower = *std::max_element(values.begin(), middle);
    return (lower + upper) * 0.5;
}

std::string make_source_id(const GnssSourceFix& source, std::size_t index) {
    if (!source.source_id.empty()) {
        return source.source_id;
    }
    std::ostringstream stream;
    stream << to_string(source.constellation) << ':'
           << to_string(source.band) << ':' << index;
    return stream.str();
}

std::string format(double value, int precision = 1) {
    std::ostringstream stream;
    stream << std::fixed << std::setprecision(precision) << value;
    return stream.str();
}

struct EvaluatedSource {
    const GnssSourceFix* fix{};
    std::string id;
    double weight{1.0};
    bool critical{};
    bool has_history{};
    GeoPoint previous_position{};
};

struct Cluster {
    std::vector<std::size_t> members;
    double weight{};
    double maximum_spread_m{};
    double anchor_distance_m{std::numeric_limits<double>::infinity()};
    GeoPoint center{};
};

struct Displacement {
    double east_m{};
    double north_m{};
};

Displacement displacement_xy(const GeoPoint& from, const GeoPoint& to) {
    const double reference_lat =
        (from.latitude_deg + to.latitude_deg) * 0.5 * kPi / 180.0;
    double delta_lon = (to.longitude_deg - from.longitude_deg) * kPi / 180.0;
    if (delta_lon > kPi) {
        delta_lon -= 2.0 * kPi;
    } else if (delta_lon < -kPi) {
        delta_lon += 2.0 * kPi;
    }
    constexpr double meters_per_radian = kEarthRadiusM;
    return Displacement{
        .east_m = delta_lon * meters_per_radian * std::cos(reference_lat),
        .north_m = (to.latitude_deg - from.latitude_deg) * kPi / 180.0 *
                   meters_per_radian,
    };
}

void add_issue(TrustDecision& decision, IssueCode code, Severity severity,
               std::string source_id, std::string detail,
               double magnitude = 0.0) {
    decision.issues.push_back(
        TrustIssue{code, severity, std::move(source_id), std::move(detail),
                   magnitude});
}

bool has_nonfinite_source_value(const GnssSourceFix& source) {
    return !finite(source.cn0_dbhz) || !finite(source.toa_residual_ns) ||
           !finite(source.horizontal_accuracy_m) ||
           !finite(source.vertical_accuracy_m) ||
           !finite(source.ephemeris_age_s);
}

}  // namespace

struct PositioningTrustComponent::Impl {
    explicit Impl(TrustConfig value) : config(std::move(value)) {}

    struct SourceMemory {
        GeoPoint position{};
        std::uint64_t timestamp_ns{};
        double cn0_dbhz{};
        std::optional<std::uint64_t> ephemeris_id{};
    };

    mutable std::mutex mutex;
    TrustConfig config;
    std::optional<GeoPoint> last_trusted_position;
    double last_trusted_accuracy_m{};
    std::uint64_t last_trusted_timestamp_ns{};
    std::uint64_t last_timestamp_ns{};
    bool has_last_timestamp{};
    std::unordered_map<std::string, SourceMemory> source_memory;
};

TrustDecision PositioningTrustComponent::evaluate_impl(Impl& state,
                                                       const TrustInput& input) {
    TrustDecision decision;
    decision.reported_position = input.receiver_fix.position;

    const bool receiver_valid =
        input.receiver_fix.valid && valid_point(input.receiver_fix.position) &&
        finite(input.receiver_fix.horizontal_accuracy_m) &&
        finite(input.receiver_fix.vertical_accuracy_m) &&
        finite(input.receiver_fix.time_residual_ns);

    std::uint64_t effective_timestamp = input.timestamp_ns;
    if (effective_timestamp == 0) {
        effective_timestamp = input.receiver_fix.timestamp_ns;
    }
    const bool map_available =
        input.map.available || !input.map.route.empty() ||
        !input.map.allowed_polygon.empty() ||
        input.map.distance_to_route_m.has_value() ||
        !input.map.position_allowed;

    if (!receiver_valid) {
        add_issue(decision, IssueCode::NoFix, Severity::Critical, "receiver",
                  "receiver fix is invalid or has a non-finite position");
    }

    if (effective_timestamp > 0 && state.has_last_timestamp &&
        effective_timestamp < state.last_timestamp_ns) {
        add_issue(decision, IssueCode::TimeAnomaly, Severity::Critical,
                  "receiver", "evaluation timestamp moved backwards",
                  static_cast<double>(state.last_timestamp_ns -
                                      effective_timestamp));
    }

    if (input.receiver_fix.timestamp_ns > 0 && effective_timestamp > 0) {
        const double skew = std::abs(
            static_cast<double>(input.receiver_fix.timestamp_ns) -
            static_cast<double>(effective_timestamp));
        if (skew > state.config.max_time_skew_ns) {
            add_issue(decision, IssueCode::TimeAnomaly, Severity::Critical,
                      "receiver", "receiver fix timestamp is inconsistent",
                      skew);
        }
    }
    if (std::abs(input.receiver_fix.time_residual_ns) >
        state.config.max_time_skew_ns) {
        add_issue(decision, IssueCode::TimeAnomaly, Severity::Critical,
                  "receiver", "receiver clock residual exceeds threshold",
                  std::abs(input.receiver_fix.time_residual_ns));
    }
    if (input.inertial.available && input.inertial.timestamp_ns > 0 &&
        effective_timestamp > 0) {
        const double skew = std::abs(
            static_cast<double>(input.inertial.timestamp_ns) -
            static_cast<double>(effective_timestamp));
        if (skew > state.config.max_time_skew_ns) {
            add_issue(decision, IssueCode::TimeAnomaly, Severity::Critical,
                      "inertial",
                      "inertial timestamp is inconsistent with the receiver "
                      "epoch",
                      skew);
        }
    }
    if (receiver_valid && finite(input.receiver_fix.cn0_dbhz)) {
        if (input.receiver_fix.cn0_dbhz < state.config.min_cn0_dbhz) {
            add_issue(decision, IssueCode::LowSignal, Severity::Warning,
                      "receiver", "aggregate receiver C/N0 is below the floor",
                      input.receiver_fix.cn0_dbhz);
        }
        if (input.receiver_fix.cn0_dbhz > state.config.max_cn0_dbhz) {
            add_issue(decision, IssueCode::ExcessiveSignal, Severity::Critical,
                      "receiver",
                      "aggregate receiver C/N0 is above the plausibility limit",
                      input.receiver_fix.cn0_dbhz);
        }
    }

    std::vector<EvaluatedSource> sources;
    sources.reserve(input.source_fixes.size());
    std::unordered_map<std::string, std::size_t> id_counts;
    std::vector<double> all_cn0;

    for (std::size_t i = 0; i < input.source_fixes.size(); ++i) {
        const GnssSourceFix& fix = input.source_fixes[i];
        std::string id = make_source_id(fix, i);
        const std::size_t count = ++id_counts[id];
        if (count > 1) {
            id += "#" + std::to_string(count);
        }

        const bool position_valid = valid_point(fix.position);
        const bool values_valid = !has_nonfinite_source_value(fix);
        if (!position_valid || !values_valid) {
            add_issue(decision, IssueCode::InvalidInput, Severity::Critical, id,
                      "source fix contains an invalid or non-finite value");
            continue;
        }

        bool source_critical = false;
        if (fix.timestamp_ns > 0 && effective_timestamp > 0) {
            const double skew =
                std::abs(static_cast<double>(fix.timestamp_ns) -
                         static_cast<double>(effective_timestamp));
            if (skew > state.config.max_time_skew_ns) {
                source_critical = true;
                add_issue(decision, IssueCode::TimeAnomaly,
                          Severity::Critical, id,
                          "source timestamp is inconsistent with the receiver "
                          "epoch",
                          skew);
            }
        }

        if (fix.cn0_dbhz < state.config.min_cn0_dbhz) {
            add_issue(decision, IssueCode::LowSignal, Severity::Warning, id,
                      "C/N0 is below the configured floor", fix.cn0_dbhz);
        }
        if (!fix.signal_healthy) {
            add_issue(decision, IssueCode::LowSignal, Severity::Warning, id,
                      "receiver marked the signal as unhealthy",
                      fix.cn0_dbhz);
        }
        if (fix.cn0_dbhz > state.config.max_cn0_dbhz) {
            source_critical = true;
            add_issue(decision, IssueCode::ExcessiveSignal, Severity::Critical,
                      id, "C/N0 is above the absolute plausibility limit",
                      fix.cn0_dbhz);
        }
        if (fix.expected_cn0_dbhz.has_value() &&
            finite(*fix.expected_cn0_dbhz)) {
            const double excess = fix.cn0_dbhz - *fix.expected_cn0_dbhz;
            if (excess > state.config.max_expected_cn0_excess_db) {
                source_critical = true;
                add_issue(decision, IssueCode::ExcessiveSignal,
                          Severity::Critical, id,
                          "C/N0 exceeds the source's expected level by " +
                              format(excess) + " dB",
                          excess);
            }
        }
        all_cn0.push_back(fix.cn0_dbhz);

        if (std::abs(fix.toa_residual_ns) >
            state.config.max_toa_residual_ns) {
            source_critical = true;
            add_issue(decision, IssueCode::ToaInconsistency,
                      Severity::Critical, id,
                      "TOA residual exceeds the consistency threshold",
                      std::abs(fix.toa_residual_ns));
        }

        if (!fix.ephemeris_valid) {
            source_critical = true;
            add_issue(decision, IssueCode::EphemerisMismatch,
                      Severity::Critical, id, "ephemeris is marked invalid");
        }
        if (!fix.ephemeris_consistent) {
            source_critical = true;
            add_issue(decision, IssueCode::EphemerisMismatch,
                      Severity::Critical, id,
                      "ephemeris failed cross-source consistency checks");
        }
        if (fix.ephemeris_age_s > state.config.max_ephemeris_age_s) {
            add_issue(decision, IssueCode::EphemerisStale, Severity::Warning,
                      id, "ephemeris age exceeds the configured limit",
                      fix.ephemeris_age_s);
        }

        bool has_history = false;
        GeoPoint previous_position{};
        const auto memory = state.source_memory.find(id);
        if (memory != state.source_memory.end()) {
            const Impl::SourceMemory& previous = memory->second;
            const double dt_s =
                (effective_timestamp > 0 && previous.timestamp_ns > 0 &&
                 effective_timestamp >= previous.timestamp_ns)
                    ? static_cast<double>(effective_timestamp -
                                          previous.timestamp_ns) /
                          1'000'000'000.0
                    : 0.0;
            const bool within_history =
                effective_timestamp == 0 || previous.timestamp_ns == 0 ||
                dt_s <= state.config.history_window_s;

            if (within_history && previous.cn0_dbhz > 0.0 &&
                fix.cn0_dbhz - previous.cn0_dbhz >
                    state.config.max_cn0_jump_db) {
                source_critical = true;
                add_issue(decision, IssueCode::SignalJump, Severity::Critical,
                          id, "single-source C/N0 increased abruptly",
                          fix.cn0_dbhz - previous.cn0_dbhz);
            }

            if (within_history) {
                has_history = true;
                previous_position = previous.position;
            }

            if (within_history && previous.ephemeris_id.has_value() &&
                fix.ephemeris_id.has_value() &&
                *previous.ephemeris_id != *fix.ephemeris_id) {
                source_critical = true;
                add_issue(decision, IssueCode::EphemerisMismatch,
                          Severity::Critical, id,
                          "ephemeris identity changed within the history window");
            }
        }

        double weight =
            1.0 /
            (1.0 + std::max(0.0, fix.horizontal_accuracy_m) / 10.0);
        if (source_critical) {
            weight *= 0.15;
        } else if (fix.cn0_dbhz < state.config.min_cn0_dbhz) {
            weight *= 0.6;
        }
        sources.push_back(
            EvaluatedSource{&fix, std::move(id), weight, source_critical,
                            has_history, previous_position});
    }

    if (sources.empty()) {
        add_issue(decision, IssueCode::InsufficientRedundancy,
                  Severity::Critical, "gnss",
                  "no valid independent source fixes were supplied");
    }

    std::vector<std::size_t> history_indices;
    std::vector<Displacement> displacements;
    for (std::size_t i = 0; i < sources.size(); ++i) {
        if (!sources[i].has_history) {
            continue;
        }
        history_indices.push_back(i);
        displacements.push_back(displacement_xy(
            sources[i].previous_position, sources[i].fix->position));
    }
    if (history_indices.size() >= 2) {
        std::vector<double> east_components;
        std::vector<double> north_components;
        east_components.reserve(displacements.size());
        north_components.reserve(displacements.size());
        for (const Displacement& displacement : displacements) {
            east_components.push_back(displacement.east_m);
            north_components.push_back(displacement.north_m);
        }
        const double median_east = median(std::move(east_components));
        const double median_north = median(std::move(north_components));

        for (std::size_t i = 0; i < history_indices.size(); ++i) {
            const std::size_t source_index = history_indices[i];
            const double deviation =
                std::hypot(displacements[i].east_m - median_east,
                           displacements[i].north_m - median_north);
            if (deviation > state.config.max_source_jump_m) {
                if (!sources[source_index].critical) {
                    sources[source_index].critical = true;
                    sources[source_index].weight *= 0.15;
                }
                add_issue(decision, IssueCode::SourceAbruptChange,
                          Severity::Critical, sources[source_index].id,
                          "single-source displacement is inconsistent with "
                          "peer sources",
                          deviation);
            }
        }
    }

    if (sources.size() >= 3 && !all_cn0.empty()) {
        const double peer_median = median(all_cn0);
        for (const EvaluatedSource& source : sources) {
            if (source.fix->cn0_dbhz - peer_median >
                state.config.peer_cn0_outlier_db) {
                add_issue(decision, IssueCode::ExcessiveSignal,
                          Severity::Critical, source.id,
                          "C/N0 is an outlier relative to peer sources",
                          source.fix->cn0_dbhz - peer_median);
            }
        }
    }

    std::vector<std::size_t> valid_constellations;
    std::vector<std::size_t> valid_bands;
    std::vector<std::pair<Constellation, FrequencyBand>> combinations;
    for (const EvaluatedSource& source : sources) {
        if (source.fix->constellation != Constellation::Unknown &&
            std::find(valid_constellations.begin(), valid_constellations.end(),
                      static_cast<std::size_t>(source.fix->constellation)) ==
                valid_constellations.end()) {
            valid_constellations.push_back(
                static_cast<std::size_t>(source.fix->constellation));
        }
        if (source.fix->band != FrequencyBand::Unknown &&
            std::find(valid_bands.begin(), valid_bands.end(),
                      static_cast<std::size_t>(source.fix->band)) ==
                valid_bands.end()) {
            valid_bands.push_back(static_cast<std::size_t>(source.fix->band));
        }
        const auto combination =
            std::make_pair(source.fix->constellation, source.fix->band);
        if (std::find(combinations.begin(), combinations.end(), combination) ==
            combinations.end()) {
            combinations.push_back(combination);
        }
    }

    if (sources.size() < state.config.min_sources) {
        add_issue(decision, IssueCode::InsufficientRedundancy,
                  Severity::Critical, "gnss",
                  "not enough independent source fixes for cross-checking",
                  static_cast<double>(sources.size()));
    }
    if (valid_constellations.size() < state.config.min_constellations) {
        add_issue(decision, IssueCode::SingleConstellation, Severity::Warning,
                  "gnss", "multi-constellation cross-check is unavailable",
                  static_cast<double>(valid_constellations.size()));
    }
    if (valid_bands.size() < state.config.min_frequency_bands) {
        add_issue(decision, IssueCode::SingleFrequency, Severity::Warning,
                  "gnss", "multi-frequency cross-check is unavailable",
                  static_cast<double>(valid_bands.size()));
    }
    if (sources.size() > 1 && combinations.size() <= 1) {
        add_issue(
            decision, IssueCode::InsufficientRedundancy, Severity::Critical,
            "gnss",
            "all source fixes belong to one constellation/frequency cell",
            static_cast<double>(combinations.size()));
    }

    std::optional<GeoPoint> anchor;
    if (state.last_trusted_position.has_value()) {
        anchor = state.last_trusted_position;
    } else if (input.inertial.available && valid_point(input.inertial.position)) {
        anchor = input.inertial.position;
    }

    auto anchor_distance = [&](const GeoPoint& point) {
        double best = std::numeric_limits<double>::infinity();
        if (anchor.has_value()) {
            best = std::min(best, horizontal_distance_m(point, *anchor));
        }
        if (map_available) {
            if (const auto route_distance =
                    route_distance_m(input.map, point);
                route_distance.has_value()) {
                best = std::min(best, *route_distance);
            } else if (!input.map.allowed_polygon.empty() &&
                       !point_in_polygon(point, input.map.allowed_polygon)) {
                best = std::min(best, 1'000'000.0);
            }
        }
        return best;
    };

    std::optional<Cluster> best_cluster;
    if (!sources.empty()) {
        std::vector<const GnssSourceFix*> pointers;
        std::vector<double> weights;
        pointers.reserve(sources.size());
        weights.reserve(sources.size());
        for (const EvaluatedSource& source : sources) {
            pointers.push_back(source.fix);
            weights.push_back(source.weight);
        }

        std::vector<Cluster> clusters;
        clusters.reserve(sources.size());
        for (std::size_t i = 0; i < sources.size(); ++i) {
            Cluster cluster;
            cluster.members.push_back(i);
            cluster.weight = sources[i].weight;
            for (std::size_t j = 0; j < sources.size(); ++j) {
                if (i == j) {
                    continue;
                }
                if (horizontal_distance_m(sources[i].fix->position,
                                          sources[j].fix->position) <=
                    state.config.source_disagreement_m) {
                    cluster.members.push_back(j);
                    cluster.weight += sources[j].weight;
                }
            }
            cluster.center =
                weighted_center(pointers, weights, cluster.members);

            double anchor_total = 0.0;
            for (const std::size_t member : cluster.members) {
                anchor_total += anchor_distance(sources[member].fix->position);
                for (const std::size_t other : cluster.members) {
                    cluster.maximum_spread_m =
                        std::max(cluster.maximum_spread_m,
                                 horizontal_distance_m(
                                     sources[member].fix->position,
                                     sources[other].fix->position));
                }
            }
            if (!cluster.members.empty() && std::isfinite(anchor_total)) {
                cluster.anchor_distance_m =
                    anchor_total /
                    static_cast<double>(cluster.members.size());
            }
            clusters.push_back(std::move(cluster));
        }

        auto better = [](const Cluster& lhs, const Cluster& rhs) {
            constexpr double weight_epsilon = 1e-9;
            if (std::abs(lhs.weight - rhs.weight) > weight_epsilon) {
                return lhs.weight > rhs.weight;
            }
            if (lhs.anchor_distance_m < rhs.anchor_distance_m) {
                return true;
            }
            if (lhs.anchor_distance_m > rhs.anchor_distance_m) {
                return false;
            }
            return lhs.maximum_spread_m < rhs.maximum_spread_m;
        };

        best_cluster = clusters.front();
        for (std::size_t i = 1; i < clusters.size(); ++i) {
            if (better(clusters[i], *best_cluster)) {
                best_cluster = clusters[i];
            }
        }
    }

    double consensus_fraction = 0.0;
    if (best_cluster.has_value() && !sources.empty()) {
        decision.consensus_position = best_cluster->center;
        double total_weight = 0.0;
        for (const EvaluatedSource& source : sources) {
            total_weight += source.weight;
        }
        if (total_weight > 0.0) {
            consensus_fraction =
                std::clamp(best_cluster->weight / total_weight, 0.0, 1.0);
        }

        if (best_cluster->members.size() == 1 && sources.size() > 1) {
            add_issue(decision, IssueCode::SourceDisagreement,
                      Severity::Critical, "gnss",
                      "source fixes do not form a cross-validated consensus");
        }

        for (std::size_t i = 0; i < sources.size(); ++i) {
            const double distance = horizontal_distance_m(
                sources[i].fix->position, best_cluster->center);
            if (distance <= state.config.source_disagreement_m) {
                continue;
            }
            const bool large_outlier =
                distance > 3.0 * state.config.source_disagreement_m;
            const Severity severity =
                (large_outlier || sources[i].critical) ? Severity::Critical
                                                        : Severity::Warning;
            add_issue(decision, IssueCode::SourceOutlier, severity,
                      sources[i].id,
                      "source fix is outside the cross-validated cluster",
                      distance);
        }
    }

    if (receiver_valid && decision.consensus_position.has_value()) {
        const double consensus_accuracy =
            std::max(0.0, best_cluster->maximum_spread_m);
        const double distance = horizontal_distance_m(
            input.receiver_fix.position, *decision.consensus_position);
        const double allowed =
            state.config.max_receiver_consensus_error_m +
            std::max(0.0, input.receiver_fix.horizontal_accuracy_m) +
            consensus_accuracy;
        if (distance > allowed) {
            decision.offset_detected = true;
            decision.observed_offset_m =
                std::max(decision.observed_offset_m, distance);
            add_issue(decision, IssueCode::UnsupportedReportedFix,
                      Severity::Critical, "receiver",
                      "reported fix is not supported by the source consensus",
                      distance);
        }
    }

    const bool have_dt =
        effective_timestamp > 0 && state.last_trusted_timestamp_ns > 0 &&
        effective_timestamp >= state.last_trusted_timestamp_ns;
    const double dt_s =
        have_dt ? static_cast<double>(effective_timestamp -
                                      state.last_trusted_timestamp_ns) /
                      1'000'000'000.0
                : 0.0;

    if (receiver_valid && state.last_trusted_position.has_value() &&
        (!have_dt || dt_s <= state.config.history_window_s)) {
        const double moved = distance_3d_m(input.receiver_fix.position,
                                           *state.last_trusted_position);
        double allowance =
            state.config.max_position_jump_m +
            std::max(0.0, input.receiver_fix.horizontal_accuracy_m) +
            state.last_trusted_accuracy_m;
        if (have_dt) {
            allowance =
                std::max(allowance, state.config.max_speed_mps * dt_s +
                                        std::max(
                                            0.0,
                                            input.receiver_fix
                                                    .horizontal_accuracy_m) +
                                        state.last_trusted_accuracy_m);
        }
        if (moved > allowance) {
            decision.offset_detected = true;
            decision.observed_offset_m =
                std::max(decision.observed_offset_m, moved);
            add_issue(decision, IssueCode::PositionJump, Severity::Critical,
                      "receiver",
                      "reported position jumped from the last trusted position",
                      moved);
            if (have_dt && dt_s > 0.0 &&
                moved / dt_s > state.config.max_speed_mps) {
                add_issue(decision, IssueCode::ImpossibleSpeed,
                          Severity::Critical, "receiver",
                          "position jump implies an impossible ground speed",
                          moved / dt_s);
            }
        }
    }

    if (receiver_valid && input.inertial.available &&
        valid_point(input.inertial.position)) {
        const double disagreement = horizontal_distance_m(
            input.receiver_fix.position, input.inertial.position);
        const double allowed =
            state.config.max_imu_disagreement_m +
            std::max(0.0, input.inertial.horizontal_accuracy_m) +
            std::max(0.0, input.receiver_fix.horizontal_accuracy_m);
        if (disagreement > allowed) {
            decision.offset_detected = true;
            decision.observed_offset_m =
                std::max(decision.observed_offset_m, disagreement);
            add_issue(decision, IssueCode::ImuDisagreement,
                      Severity::Critical, "receiver",
                      "reported position disagrees with inertial prediction",
                      disagreement);
        }
    }

    if (receiver_valid && map_available) {
        if (!input.map.position_allowed) {
            decision.offset_detected = true;
            add_issue(decision, IssueCode::MapViolation, Severity::Critical,
                      "receiver",
                      "map context marks the position as disallowed");
        }
        if (!input.map.allowed_polygon.empty() &&
            !point_in_polygon(input.receiver_fix.position,
                              input.map.allowed_polygon)) {
            decision.offset_detected = true;
            add_issue(decision, IssueCode::MapViolation, Severity::Critical,
                      "receiver",
                      "reported position is outside the allowed map polygon");
        }
        if (const auto distance =
                route_distance_m(input.map, input.receiver_fix.position);
            distance.has_value() &&
            *distance > state.config.max_map_distance_m) {
            decision.offset_detected = true;
            decision.observed_offset_m =
                std::max(decision.observed_offset_m, *distance);
            add_issue(decision, IssueCode::MapViolation, Severity::Critical,
                      "receiver",
                      "reported position is outside the allowed route corridor",
                      *distance);
        }
    }

    if (decision.consensus_position.has_value() && map_available) {
        if (const auto distance =
                route_distance_m(input.map, *decision.consensus_position);
            distance.has_value() &&
            *distance > state.config.max_map_distance_m) {
            decision.offset_detected = true;
            decision.observed_offset_m =
                std::max(decision.observed_offset_m, *distance);
            add_issue(decision, IssueCode::MapViolation, Severity::Critical,
                      "consensus",
                      "cross-source consensus is outside the allowed route "
                      "corridor",
                      *distance);
        }
    }

    int critical_count = 0;
    int warning_count = 0;
    int info_count = 0;
    for (const TrustIssue& issue : decision.issues) {
        switch (issue.severity) {
            case Severity::Critical:
                ++critical_count;
                break;
            case Severity::Warning:
                ++warning_count;
                break;
            case Severity::Info:
                ++info_count;
                break;
        }
    }

    decision.trust_score = clamp_score(
        1.0 - critical_count * state.config.critical_penalty -
        warning_count * state.config.warning_penalty -
        info_count * state.config.info_penalty -
        (1.0 - consensus_fraction) * state.config.consensus_penalty);

    const bool no_usable_consensus =
        !decision.consensus_position.has_value() || sources.empty();
    if (!receiver_valid || sources.empty() || no_usable_consensus) {
        decision.level = TrustLevel::Rejected;
    } else if (critical_count > 0 ||
               decision.trust_score < state.config.manual_review_score) {
        decision.level = TrustLevel::ManualConfirmation;
    } else if (decision.trust_score < state.config.trusted_score) {
        decision.level = TrustLevel::Degraded;
    } else {
        decision.level = TrustLevel::Trusted;
    }

    decision.accepted = decision.level == TrustLevel::Trusted ||
                        decision.level == TrustLevel::Degraded;
    decision.requires_manual_confirmation = !decision.accepted;

    if (decision.accepted) {
        decision.safe_position = input.receiver_fix.position;
        state.last_trusted_position = input.receiver_fix.position;
        state.last_trusted_accuracy_m =
            std::max(0.0, input.receiver_fix.horizontal_accuracy_m);
        state.last_trusted_timestamp_ns = effective_timestamp;
    } else {
        if (state.last_trusted_position.has_value()) {
            decision.safe_position = state.last_trusted_position;
        } else if (input.inertial.available &&
                   valid_point(input.inertial.position)) {
            decision.safe_position = input.inertial.position;
        }
    }

    if (effective_timestamp > 0) {
        state.last_timestamp_ns =
            std::max(state.last_timestamp_ns, effective_timestamp);
        state.has_last_timestamp = true;
    }

    for (const EvaluatedSource& source : sources) {
        Impl::SourceMemory memory;
        memory.position = source.fix->position;
        memory.timestamp_ns = effective_timestamp;
        memory.cn0_dbhz = source.fix->cn0_dbhz;
        memory.ephemeris_id = source.fix->ephemeris_id;
        state.source_memory[source.id] = memory;
    }

    if (decision.level == TrustLevel::Trusted) {
        decision.explanation =
            "position accepted: cross-source integrity checks passed without "
            "warning conditions";
    } else if (decision.level == TrustLevel::Degraded) {
        decision.explanation =
            "position accepted with reduced confidence: no integrity conflict, "
            "but warning conditions are present";
    } else if (decision.level == TrustLevel::ManualConfirmation) {
        decision.explanation =
            "position not accepted: anomaly threshold reached, manual "
            "confirmation required";
    } else {
        decision.explanation =
            "position rejected: no sufficiently reliable position is available";
    }

    return decision;
}

bool TrustDecision::has_issue(IssueCode code) const noexcept {
    return std::any_of(issues.begin(), issues.end(),
                       [code](const TrustIssue& issue) {
                           return issue.code == code;
                       });
}

bool TrustDecision::has_critical_issue() const noexcept {
    return std::any_of(issues.begin(), issues.end(),
                       [](const TrustIssue& issue) {
                           return issue.severity == Severity::Critical;
                       });
}

PositioningTrustComponent::PositioningTrustComponent(TrustConfig config)
    : impl_(std::make_unique<Impl>(std::move(config))) {}

PositioningTrustComponent::~PositioningTrustComponent() = default;

PositioningTrustComponent::PositioningTrustComponent(
    PositioningTrustComponent&&) noexcept = default;

PositioningTrustComponent& PositioningTrustComponent::operator=(
    PositioningTrustComponent&&) noexcept = default;

TrustDecision PositioningTrustComponent::evaluate(const TrustInput& input) {
    const std::lock_guard lock(impl_->mutex);
    return evaluate_impl(*impl_, input);
}

void PositioningTrustComponent::reset() {
    const std::lock_guard lock(impl_->mutex);
    impl_->last_trusted_position.reset();
    impl_->last_trusted_accuracy_m = 0.0;
    impl_->last_trusted_timestamp_ns = 0;
    impl_->last_timestamp_ns = 0;
    impl_->has_last_timestamp = false;
    impl_->source_memory.clear();
}

TrustConfig PositioningTrustComponent::config() const {
    const std::lock_guard lock(impl_->mutex);
    return impl_->config;
}

void PositioningTrustComponent::set_config(TrustConfig config) {
    const std::lock_guard lock(impl_->mutex);
    impl_->config = std::move(config);
    impl_->last_trusted_position.reset();
    impl_->last_trusted_accuracy_m = 0.0;
    impl_->last_trusted_timestamp_ns = 0;
    impl_->last_timestamp_ns = 0;
    impl_->has_last_timestamp = false;
    impl_->source_memory.clear();
}

std::string_view to_string(Constellation value) noexcept {
    switch (value) {
        case Constellation::Gps:
            return "GPS";
        case Constellation::Glonass:
            return "GLONASS";
        case Constellation::Galileo:
            return "Galileo";
        case Constellation::BeiDou:
            return "BeiDou";
        case Constellation::Qzss:
            return "QZSS";
        case Constellation::NavIc:
            return "NavIC";
        case Constellation::Other:
            return "Other";
        case Constellation::Unknown:
            return "Unknown";
    }
    return "Unknown";
}

std::string_view to_string(FrequencyBand value) noexcept {
    switch (value) {
        case FrequencyBand::L1:
            return "L1";
        case FrequencyBand::L2:
            return "L2";
        case FrequencyBand::L5:
            return "L5";
        case FrequencyBand::E1:
            return "E1";
        case FrequencyBand::E5a:
            return "E5a";
        case FrequencyBand::E5b:
            return "E5b";
        case FrequencyBand::E6:
            return "E6";
        case FrequencyBand::B1:
            return "B1";
        case FrequencyBand::B2:
            return "B2";
        case FrequencyBand::B3:
            return "B3";
        case FrequencyBand::G1:
            return "G1";
        case FrequencyBand::G2:
            return "G2";
        case FrequencyBand::Other:
            return "Other";
        case FrequencyBand::Unknown:
            return "Unknown";
    }
    return "Unknown";
}

std::string_view to_string(TrustLevel value) noexcept {
    switch (value) {
        case TrustLevel::Trusted:
            return "Trusted";
        case TrustLevel::Degraded:
            return "Degraded";
        case TrustLevel::ManualConfirmation:
            return "ManualConfirmation";
        case TrustLevel::Rejected:
            return "Rejected";
    }
    return "Rejected";
}

std::string_view to_string(IssueCode value) noexcept {
    switch (value) {
        case IssueCode::InvalidInput:
            return "InvalidInput";
        case IssueCode::NoFix:
            return "NoFix";
        case IssueCode::InsufficientRedundancy:
            return "InsufficientRedundancy";
        case IssueCode::SingleConstellation:
            return "SingleConstellation";
        case IssueCode::SingleFrequency:
            return "SingleFrequency";
        case IssueCode::LowSignal:
            return "LowSignal";
        case IssueCode::ExcessiveSignal:
            return "ExcessiveSignal";
        case IssueCode::SignalJump:
            return "SignalJump";
        case IssueCode::ToaInconsistency:
            return "ToaInconsistency";
        case IssueCode::EphemerisMismatch:
            return "EphemerisMismatch";
        case IssueCode::EphemerisStale:
            return "EphemerisStale";
        case IssueCode::SourceAbruptChange:
            return "SourceAbruptChange";
        case IssueCode::SourceOutlier:
            return "SourceOutlier";
        case IssueCode::SourceDisagreement:
            return "SourceDisagreement";
        case IssueCode::PositionJump:
            return "PositionJump";
        case IssueCode::ImpossibleSpeed:
            return "ImpossibleSpeed";
        case IssueCode::ImuDisagreement:
            return "ImuDisagreement";
        case IssueCode::MapViolation:
            return "MapViolation";
        case IssueCode::TimeAnomaly:
            return "TimeAnomaly";
        case IssueCode::UnsupportedReportedFix:
            return "UnsupportedReportedFix";
    }
    return "InvalidInput";
}

std::string_view to_string(Severity value) noexcept {
    switch (value) {
        case Severity::Info:
            return "Info";
        case Severity::Warning:
            return "Warning";
        case Severity::Critical:
            return "Critical";
    }
    return "Warning";
}

}  // namespace netsec
