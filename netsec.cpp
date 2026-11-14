#include "netsec.hpp"

#include <algorithm>
#include <cmath>
#include <deque>
#include <limits>
#include <mutex>
#include <stdexcept>
#include <unordered_map>
#include <utility>

namespace netsec {
namespace {

constexpr double kInitialTemperatureC = 25.0;
constexpr std::uint32_t kMaxNeighbourRadius = 1024;

struct RowHash {
    std::size_t operator()(const RowAddress& row) const noexcept {
        std::uint64_t value = row.region;
        value ^= static_cast<std::uint64_t>(row.bank) + 0x9e3779b97f4a7c15ULL +
                 (value << 6U) + (value >> 2U);
        value ^= static_cast<std::uint64_t>(row.row) + 0x9e3779b97f4a7c15ULL +
                 (value << 6U) + (value >> 2U);
        return static_cast<std::size_t>(value);
    }
};

struct RowState {
    RegionKind kind{RegionKind::Normal};
    std::deque<TimePoint> activation_window;
    TimePoint first_seen{};
    TimePoint last_seen{};
    TimePoint last_refresh{};
    std::uint64_t total_activations{0};
    std::uint64_t blocked_activations{0};
    std::uint64_t corrected_ecc_errors{0};
    std::uint64_t uncorrectable_ecc_errors{0};
    std::uint64_t preventive_refreshes{0};
    std::uint64_t isolation_events{0};
    bool isolated{false};
    IsolationReason isolation_reason{IsolationReason::ActivationThreshold};
};

struct RefreshRequest {
    RowAddress address;
    RefreshReason reason{RefreshReason::Manual};
};

struct IsolationRequest {
    RowAddress address;
    IsolationReason reason{IsolationReason::ActivationThreshold};
};

struct Work {
    std::vector<RefreshRequest> refreshes;
    std::vector<IsolationRequest> isolations;
};

}  // namespace

namespace detail {

struct MonitorImpl {
    explicit MonitorImpl(MonitorConfig policy, HardwareHooks hardware)
        : config(policy), hooks(std::move(hardware)) {}

    mutable std::mutex mutex;
    MonitorConfig config;
    HardwareHooks hooks;
    std::unordered_map<RegionId, RegionKind> regions;
    std::unordered_map<RowAddress, RowState, RowHash> rows;
    double current_temperature_c{kInitialTemperatureC};
};

}  // namespace detail

namespace {

using Impl = detail::MonitorImpl;

bool finite_positive(double value) noexcept {
    return std::isfinite(value) && value > 0.0;
}

void validate_config(const MonitorConfig& config) {
    if (config.activation_window.count() <= 0 ||
        config.max_refresh_interval.count() <= 0 ||
        config.page_table_max_refresh_interval.count() <= 0) {
        throw std::invalid_argument("netsec: time windows must be positive");
    }
    if (config.activation_threshold == 0 ||
        config.page_table_activation_threshold == 0) {
        throw std::invalid_argument("netsec: activation thresholds must be positive");
    }
    if (config.neighbor_refresh_distance > kMaxNeighbourRadius ||
        config.isolation_radius > kMaxNeighbourRadius) {
        throw std::invalid_argument("netsec: neighbour radius is too large");
    }
    if (!std::isfinite(config.warning_temperature_c) ||
        !std::isfinite(config.critical_temperature_c) ||
        config.warning_temperature_c >= config.critical_temperature_c) {
        throw std::invalid_argument("netsec: invalid temperature thresholds");
    }
    if (!finite_positive(config.warning_threshold_scale) ||
        !finite_positive(config.critical_threshold_scale) ||
        !finite_positive(config.warning_refresh_scale) ||
        !finite_positive(config.critical_refresh_scale) ||
        config.warning_threshold_scale > 1.0 ||
        config.critical_threshold_scale > 1.0 ||
        config.warning_refresh_scale > 1.0 ||
        config.critical_refresh_scale > 1.0) {
        throw std::invalid_argument("netsec: policy scales must be in (0, 1]");
    }
}

std::uint64_t saturating_add(std::uint64_t left, std::uint64_t right) noexcept {
    if (right > std::numeric_limits<std::uint64_t>::max() - left) {
        return std::numeric_limits<std::uint64_t>::max();
    }
    return left + right;
}

std::uint64_t saturating_multiply(std::uint64_t left,
                                  std::uint64_t right) noexcept {
    if (left != 0 &&
        right > std::numeric_limits<std::uint64_t>::max() / left) {
        return std::numeric_limits<std::uint64_t>::max();
    }
    return left * right;
}

Duration scale_duration(Duration duration, double scale) noexcept {
    const auto scaled = static_cast<std::int64_t>(
        std::llround(static_cast<double>(duration.count()) * scale));
    return Duration(std::max<std::int64_t>(1, scaled));
}

RowState& get_or_create_row(Impl& impl, const RowAddress& address,
                            TimePoint now) {
    const auto found = impl.rows.find(address);
    if (found != impl.rows.end()) {
        return found->second;
    }

    RegionKind kind = RegionKind::Normal;
    const auto region = impl.regions.find(address.region);
    if (region != impl.regions.end()) {
        kind = region->second;
    } else {
        impl.regions.emplace(address.region, RegionKind::Normal);
    }

    RowState state;
    state.kind = kind;
    state.first_seen = now;
    state.last_seen = now;
    state.last_refresh = now;
    return impl.rows.emplace(address, std::move(state)).first->second;
}

RegionKind kind_for(const Impl& impl, const RowAddress& address) noexcept {
    const auto row = impl.rows.find(address);
    if (row != impl.rows.end()) {
        return row->second.kind;
    }
    const auto region = impl.regions.find(address.region);
    if (region != impl.regions.end()) {
        return region->second;
    }
    return RegionKind::Normal;
}

double temperature_threshold_scale(const Impl& impl) noexcept {
    if (impl.current_temperature_c >= impl.config.critical_temperature_c) {
        return impl.config.critical_threshold_scale;
    }
    if (impl.current_temperature_c >= impl.config.warning_temperature_c) {
        return impl.config.warning_threshold_scale;
    }
    return 1.0;
}

double temperature_refresh_scale(const Impl& impl) noexcept {
    if (impl.current_temperature_c >= impl.config.critical_temperature_c) {
        return impl.config.critical_refresh_scale;
    }
    if (impl.current_temperature_c >= impl.config.warning_temperature_c) {
        return impl.config.warning_refresh_scale;
    }
    return 1.0;
}

double ecc_policy_scale(const Impl& impl, std::uint64_t corrected) noexcept {
    if (corrected == 0) {
        return 1.0;
    }
    const auto limit = std::max<std::uint64_t>(1, impl.config.corrected_ecc_threshold);
    return std::max(0.25, 1.0 -
                              (static_cast<double>(corrected) /
                               (2.0 * static_cast<double>(limit))));
}

std::uint64_t effective_threshold(const Impl& impl, RegionKind kind,
                                  std::uint64_t corrected) noexcept {
    const auto base = kind == RegionKind::PageTable
                          ? impl.config.page_table_activation_threshold
                          : impl.config.activation_threshold;
    const double scale =
        temperature_threshold_scale(impl) * ecc_policy_scale(impl, corrected);
    const auto adjusted = static_cast<std::uint64_t>(
        std::llround(static_cast<double>(base) * scale));
    return std::max<std::uint64_t>(1, adjusted);
}

Duration effective_interval(const Impl& impl, RegionKind kind,
                            std::uint64_t corrected) noexcept {
    const auto base = kind == RegionKind::PageTable
                          ? impl.config.page_table_max_refresh_interval
                          : impl.config.max_refresh_interval;
    return scale_duration(
        base, temperature_refresh_scale(impl) * ecc_policy_scale(impl, corrected));
}

void prune_window(Impl& impl, RowState& state, TimePoint now) {
    while (!state.activation_window.empty() &&
           now - state.activation_window.front() >=
               impl.config.activation_window) {
        state.activation_window.pop_front();
    }
}

std::uint64_t window_count(const Impl& impl, const RowState& state,
                           TimePoint now) noexcept {
    const auto cutoff = now - impl.config.activation_window;
    std::uint64_t count = 0;
    for (auto it = state.activation_window.rbegin();
         it != state.activation_window.rend() && *it > cutoff; ++it) {
        ++count;
    }
    return count;
}

void add_refresh(Work& work, const RowAddress& address, RefreshReason reason) {
    const auto found =
        std::find_if(work.refreshes.begin(), work.refreshes.end(),
                     [&](const RefreshRequest& request) {
                         return request.address == address;
                     });
    if (found == work.refreshes.end()) {
        work.refreshes.push_back({address, reason});
        return;
    }
    if (static_cast<unsigned>(reason) > static_cast<unsigned>(found->reason)) {
        found->reason = reason;
    }
}

void add_isolation(Impl& impl, Work& work, const RowAddress& address,
                   IsolationReason reason, TimePoint now) {
    RowState& state = get_or_create_row(impl, address, now);
    if (!state.isolated) {
        state.isolated = true;
        state.isolation_reason = reason;
        ++state.isolation_events;
        work.isolations.push_back({address, reason});
        return;
    }
    const auto pending =
        std::find_if(work.isolations.begin(), work.isolations.end(),
                     [&](const IsolationRequest& request) {
                         return request.address == address;
                     });
    if (pending != work.isolations.end() &&
        static_cast<unsigned>(reason) > static_cast<unsigned>(pending->reason)) {
        pending->reason = reason;
    }
    if (static_cast<unsigned>(reason) >
        static_cast<unsigned>(state.isolation_reason)) {
        state.isolation_reason = reason;
    }
}

template <typename Callback>
void for_each_neighbour(std::uint32_t row, std::uint32_t radius,
                        Callback&& callback) {
    const std::uint64_t first = row > radius ? row - radius : 0;
    const std::uint64_t last =
        std::min<std::uint64_t>(std::numeric_limits<std::uint32_t>::max(),
                                static_cast<std::uint64_t>(row) + radius);
    for (std::uint64_t candidate = first; candidate <= last; ++candidate) {
        callback(static_cast<std::uint32_t>(candidate));
    }
}

void refresh_neighbourhood(Impl& impl, Work& work, const RowAddress& center,
                           std::uint32_t radius, RefreshReason reason,
                           TimePoint now) {
    for_each_neighbour(center.row, radius, [&](std::uint32_t row) {
        const RowAddress address{center.region, center.bank, row};
        (void)get_or_create_row(impl, address, now);
        add_refresh(work, address, reason);
    });
}

void isolate_neighbourhood(Impl& impl, Work& work, const RowAddress& center,
                           std::uint32_t radius, IsolationReason reason,
                           TimePoint now) {
    for_each_neighbour(center.row, radius, [&](std::uint32_t row) {
        add_isolation(impl, work, {center.region, center.bank, row}, reason,
                      now);
    });
}

void apply_refresh(Impl& impl, const RefreshRequest& request, TimePoint now,
                   ActionReport& report) {
    bool success = true;
    if (impl.hooks.refresh_row) {
        try {
            success = impl.hooks.refresh_row(request.address, request.reason);
        } catch (...) {
            success = false;
        }
    }

    if (!success) {
        report.refresh_failed.push_back(request.address);
        return;
    }

    report.refreshed.push_back(request.address);
    std::lock_guard lock(impl.mutex);
    const auto found = impl.rows.find(request.address);
    if (found != impl.rows.end()) {
        found->second.last_refresh =
            std::max(found->second.last_refresh, now);
        ++found->second.preventive_refreshes;
    }
}

void apply_isolation(Impl& impl, const IsolationRequest& request,
                     ActionReport& report) {
    bool success = true;
    if (impl.hooks.isolate_row) {
        try {
            success = impl.hooks.isolate_row(request.address, request.reason);
        } catch (...) {
            success = false;
        }
    }
    if (!success) {
        // The logical quarantine remains active even if the platform hook is
        // unavailable. The host can observe the failure and take emergency
        // action without the monitor silently re-opening the row.
        report.isolation_failed.push_back(request.address);
        return;
    }
    report.isolated.push_back(request.address);
}

void execute(Impl& impl, Work work, TimePoint now, ActionReport& report) {
    for (const auto& request : work.refreshes) {
        apply_refresh(impl, request, now, report);
    }
    for (const auto& request : work.isolations) {
        apply_isolation(impl, request, report);
    }
}

RowStatistics make_statistics(const Impl& impl, const RowAddress& address,
                              const RowState& state, TimePoint now) {
    now = std::max(now, state.last_seen);
    const auto count = window_count(impl, state, now);
    const auto window_ms =
        std::max<std::int64_t>(1, impl.config.activation_window.count());
    const auto since_refresh =
        now >= state.last_refresh
            ? std::chrono::duration_cast<Duration>(now - state.last_refresh)
            : Duration(0);

    return RowStatistics{
        .address = address,
        .region_kind = state.kind,
        .total_activations = state.total_activations,
        .blocked_activations = state.blocked_activations,
        .window_activations = count,
        .activation_rate_hz =
            static_cast<double>(count) * 1000.0 / static_cast<double>(window_ms),
        .last_refresh = state.last_refresh,
        .since_last_refresh = since_refresh,
        .effective_refresh_interval =
            effective_interval(impl, state.kind, state.corrected_ecc_errors),
        .effective_activation_threshold =
            effective_threshold(impl, state.kind, state.corrected_ecc_errors),
        .corrected_ecc_errors = state.corrected_ecc_errors,
        .uncorrectable_ecc_errors = state.uncorrectable_ecc_errors,
        .isolated = state.isolated,
        .isolation_reason =
            state.isolated ? std::optional<IsolationReason>(state.isolation_reason)
                           : std::nullopt,
        .preventive_refreshes = state.preventive_refreshes,
        .isolation_events = state.isolation_events,
    };
}

}  // namespace

std::string_view to_string(RegionKind kind) noexcept {
    switch (kind) {
        case RegionKind::Normal:
            return "normal";
        case RegionKind::PageTable:
            return "page-table";
    }
    return "unknown";
}

std::string_view to_string(RefreshReason reason) noexcept {
    switch (reason) {
        case RefreshReason::Manual:
            return "manual";
        case RefreshReason::RefreshInterval:
            return "refresh-interval";
        case RefreshReason::ActivationPressure:
            return "activation-pressure";
        case RefreshReason::EccRecovery:
            return "ecc-recovery";
    }
    return "unknown";
}

std::string_view to_string(IsolationReason reason) noexcept {
    switch (reason) {
        case IsolationReason::ActivationThreshold:
            return "activation-threshold";
        case IsolationReason::CorrectedEccThreshold:
            return "corrected-ecc-threshold";
        case IsolationReason::UncorrectableEcc:
            return "uncorrectable-ecc";
    }
    return "unknown";
}

RowActivationMonitor::RowActivationMonitor(MonitorConfig config,
                                           HardwareHooks hooks)
    : impl_(std::make_unique<Impl>(config, std::move(hooks))) {
    validate_config(config);
}

RowActivationMonitor::~RowActivationMonitor() = default;

RowActivationMonitor::RowActivationMonitor(RowActivationMonitor&&) noexcept =
    default;

RowActivationMonitor& RowActivationMonitor::operator=(
    RowActivationMonitor&&) noexcept = default;

bool RowActivationMonitor::register_region(RegionId region, RegionKind kind) {
    std::lock_guard lock(impl_->mutex);
    const auto existing = impl_->regions.find(region);
    if (existing == impl_->regions.end()) {
        impl_->regions.emplace(region, kind);
        return true;
    }
    if (existing->second == kind) {
        return true;
    }
    const bool has_rows =
        std::any_of(impl_->rows.begin(), impl_->rows.end(),
                    [&](const auto& entry) {
                        return entry.first.region == region;
                    });
    if (has_rows) {
        return false;
    }
    existing->second = kind;
    return true;
}

bool RowActivationMonitor::register_page_table_region(RegionId region) {
    return register_region(region, RegionKind::PageTable);
}

std::optional<RegionKind> RowActivationMonitor::region_kind(
    RegionId region) const {
    std::lock_guard lock(impl_->mutex);
    const auto found = impl_->regions.find(region);
    if (found == impl_->regions.end()) {
        return std::nullopt;
    }
    return found->second;
}

ActionReport RowActivationMonitor::record_activation(RowAddress row,
                                                     TimePoint now) {
    ActionReport report;
    Work work;

    {
        std::lock_guard lock(impl_->mutex);
        RowState& state = get_or_create_row(*impl_, row, now);
        now = std::max(now, state.last_seen);
        state.last_seen = now;

        if (state.isolated) {
            ++state.blocked_activations;
            report.accepted = false;
            report.already_isolated = true;
            return report;
        }

        prune_window(*impl_, state, now);
        if (now - state.last_refresh >=
            effective_interval(*impl_, state.kind,
                               state.corrected_ecc_errors)) {
            refresh_neighbourhood(*impl_, work, row, 0,
                                  RefreshReason::RefreshInterval, now);
        }

        state.activation_window.push_back(now);
        ++state.total_activations;

        const auto threshold = effective_threshold(
            *impl_, state.kind, state.corrected_ecc_errors);
        std::uint64_t pressure = state.activation_window.size();
        pressure = saturating_add(
            pressure,
            saturating_multiply(state.corrected_ecc_errors,
                                impl_->config.ecc_activation_weight));
        if (pressure >= threshold) {
            report.threshold_exceeded = true;
            const auto refresh_radius =
                std::max(impl_->config.neighbor_refresh_distance,
                         impl_->config.isolation_radius);
            refresh_neighbourhood(
                *impl_, work, row, refresh_radius,
                RefreshReason::ActivationPressure, now);
            isolate_neighbourhood(*impl_, work, row,
                                  impl_->config.isolation_radius,
                                  IsolationReason::ActivationThreshold, now);
        }
    }

    execute(*impl_, std::move(work), now, report);
    return report;
}

ActionReport RowActivationMonitor::record_ecc(RowAddress row, EccEvent event,
                                              TimePoint now) {
    ActionReport report;
    Work work;

    {
        std::lock_guard lock(impl_->mutex);
        RowState& state = get_or_create_row(*impl_, row, now);
        now = std::max(now, state.last_seen);
        state.last_seen = now;
        prune_window(*impl_, state, now);

        state.corrected_ecc_errors =
            saturating_add(state.corrected_ecc_errors, event.corrected_errors);
        state.uncorrectable_ecc_errors = saturating_add(
            state.uncorrectable_ecc_errors, event.uncorrectable_errors);

        if (event.corrected_errors > 0 || event.uncorrectable_errors > 0) {
            const auto refresh_radius =
                std::max(impl_->config.neighbor_refresh_distance,
                         impl_->config.isolation_radius);
            refresh_neighbourhood(
                *impl_, work, row, refresh_radius,
                RefreshReason::EccRecovery, now);
        }

        if (state.corrected_ecc_errors > 0 &&
            (impl_->config.corrected_ecc_threshold == 0 ||
             state.corrected_ecc_errors >=
                 impl_->config.corrected_ecc_threshold)) {
            report.threshold_exceeded = true;
            isolate_neighbourhood(*impl_, work, row,
                                  impl_->config.isolation_radius,
                                  IsolationReason::CorrectedEccThreshold, now);
        }
        if (state.uncorrectable_ecc_errors > 0) {
            report.threshold_exceeded = true;
            isolate_neighbourhood(*impl_, work, row,
                                  impl_->config.isolation_radius,
                                  IsolationReason::UncorrectableEcc, now);
        }

        const auto threshold = effective_threshold(
            *impl_, state.kind, state.corrected_ecc_errors);
        std::uint64_t pressure = state.activation_window.size();
        pressure = saturating_add(
            pressure,
            saturating_multiply(state.corrected_ecc_errors,
                                impl_->config.ecc_activation_weight));
        if (pressure >= threshold) {
            report.threshold_exceeded = true;
            const auto refresh_radius =
                std::max(impl_->config.neighbor_refresh_distance,
                         impl_->config.isolation_radius);
            refresh_neighbourhood(*impl_, work, row, refresh_radius,
                                  RefreshReason::EccRecovery, now);
            isolate_neighbourhood(*impl_, work, row,
                                  impl_->config.isolation_radius,
                                  IsolationReason::ActivationThreshold, now);
        }
    }

    execute(*impl_, std::move(work), now, report);
    return report;
}

ActionReport RowActivationMonitor::maintain(TimePoint now) {
    ActionReport report;
    Work work;

    {
        std::lock_guard lock(impl_->mutex);
        for (auto& [address, state] : impl_->rows) {
            if (state.isolated) {
                continue;
            }
            const auto row_now = std::max(now, state.last_seen);
            if (row_now - state.last_refresh >=
                effective_interval(*impl_, state.kind,
                                   state.corrected_ecc_errors)) {
                add_refresh(work, address, RefreshReason::RefreshInterval);
            }
            prune_window(*impl_, state, row_now);
        }
    }

    execute(*impl_, std::move(work), now, report);
    return report;
}

ActionReport RowActivationMonitor::refresh_now(RowAddress row,
                                               TimePoint now) {
    ActionReport report;
    Work work;
    {
        std::lock_guard lock(impl_->mutex);
        RowState& state = get_or_create_row(*impl_, row, now);
        now = std::max(now, state.last_seen);
        state.last_seen = now;
        add_refresh(work, row, RefreshReason::Manual);
    }
    execute(*impl_, std::move(work), now, report);
    return report;
}

void RowActivationMonitor::record_refresh(RowAddress row, TimePoint now) {
    std::lock_guard lock(impl_->mutex);
    RowState& state = get_or_create_row(*impl_, row, now);
    now = std::max(now, state.last_seen);
    state.last_seen = now;
    state.last_refresh = now;
}

void RowActivationMonitor::set_temperature(double celsius) {
    if (!std::isfinite(celsius)) {
        throw std::invalid_argument("netsec: temperature must be finite");
    }
    std::lock_guard lock(impl_->mutex);
    impl_->current_temperature_c = celsius;
}

double RowActivationMonitor::temperature() const {
    std::lock_guard lock(impl_->mutex);
    return impl_->current_temperature_c;
}

std::optional<RowStatistics> RowActivationMonitor::statistics(
    RowAddress row, TimePoint now) const {
    std::lock_guard lock(impl_->mutex);
    const auto found = impl_->rows.find(row);
    if (found == impl_->rows.end()) {
        return std::nullopt;
    }
    return make_statistics(*impl_, row, found->second, now);
}

std::vector<RowStatistics> RowActivationMonitor::snapshot(
    TimePoint now) const {
    std::lock_guard lock(impl_->mutex);
    std::vector<RowStatistics> result;
    result.reserve(impl_->rows.size());
    for (const auto& [address, state] : impl_->rows) {
        result.push_back(make_statistics(*impl_, address, state, now));
    }
    std::sort(result.begin(), result.end(),
              [](const RowStatistics& left, const RowStatistics& right) {
                  return left.address < right.address;
              });
    return result;
}

bool RowActivationMonitor::is_isolated(RowAddress row) const {
    std::lock_guard lock(impl_->mutex);
    const auto found = impl_->rows.find(row);
    return found != impl_->rows.end() && found->second.isolated;
}

std::optional<IsolationReason> RowActivationMonitor::isolation_reason(
    RowAddress row) const {
    std::lock_guard lock(impl_->mutex);
    const auto found = impl_->rows.find(row);
    if (found == impl_->rows.end() || !found->second.isolated) {
        return std::nullopt;
    }
    return found->second.isolation_reason;
}

std::size_t RowActivationMonitor::tracked_rows() const {
    std::lock_guard lock(impl_->mutex);
    return impl_->rows.size();
}

std::uint64_t RowActivationMonitor::effective_activation_threshold(
    RowAddress row, TimePoint) const {
    std::lock_guard lock(impl_->mutex);
    const auto found = impl_->rows.find(row);
    const auto corrected =
        found == impl_->rows.end() ? 0 : found->second.corrected_ecc_errors;
    return effective_threshold(*impl_, kind_for(*impl_, row), corrected);
}

Duration RowActivationMonitor::effective_refresh_interval(
    RowAddress row, TimePoint) const {
    std::lock_guard lock(impl_->mutex);
    const auto found = impl_->rows.find(row);
    const auto corrected =
        found == impl_->rows.end() ? 0 : found->second.corrected_ecc_errors;
    return effective_interval(*impl_, kind_for(*impl_, row), corrected);
}

}  // namespace netsec
