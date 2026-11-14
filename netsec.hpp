#pragma once

#include <chrono>
#include <compare>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <memory>
#include <optional>
#include <string_view>
#include <vector>

#if defined(_WIN32) && defined(NETSEC_SHARED)
#if defined(NETSEC_BUILDING_LIBRARY)
#define NETSEC_API __declspec(dllexport)
#else
#define NETSEC_API __declspec(dllimport)
#endif
#else
#define NETSEC_API
#endif

namespace netsec {

using RegionId = std::uint32_t;
using TimePoint = std::chrono::steady_clock::time_point;
using Duration = std::chrono::milliseconds;

namespace detail {
struct MonitorImpl;
}

// Normal and page-table rows live in separate address regions. Neighbour
// refreshes and quarantines never cross a region boundary, so traffic against
// ordinary memory cannot disturb the independent page-table region.
enum class RegionKind : std::uint8_t {
    Normal = 0,
    PageTable = 1,
};

enum class RefreshReason : std::uint8_t {
    Manual = 0,
    RefreshInterval = 1,
    ActivationPressure = 2,
    EccRecovery = 3,
};

enum class IsolationReason : std::uint8_t {
    ActivationThreshold = 0,
    CorrectedEccThreshold = 1,
    UncorrectableEcc = 2,
};

struct RowAddress {
    RegionId region{};
    std::uint32_t bank{};
    std::uint32_t row{};

    friend bool operator==(const RowAddress&, const RowAddress&) = default;
    friend auto operator<=>(const RowAddress&, const RowAddress&) = default;
};

struct MonitorConfig {
    // The defaults are deliberately conservative for a host agent that may be
    // deployed on unknown DIMMs. A platform integrator should tune them from
    // the DIMM's row-hammer profile and the measured refresh interval.
    Duration activation_window{100};
    std::uint64_t activation_threshold{64};
    std::uint64_t page_table_activation_threshold{16};

    Duration max_refresh_interval{64};
    Duration page_table_max_refresh_interval{16};

    std::uint32_t neighbor_refresh_distance{1};
    std::uint32_t isolation_radius{1};

    // A corrected ECC event adds ecc_activation_weight units of pressure to
    // the current activation window. Reaching corrected_ecc_threshold also
    // quarantines the affected neighbourhood. Zero means "on first event".
    std::uint64_t corrected_ecc_threshold{4};
    std::uint64_t ecc_activation_weight{4};

    double warning_temperature_c{75.0};
    double critical_temperature_c{90.0};
    double warning_threshold_scale{0.75};
    double critical_threshold_scale{0.50};
    double warning_refresh_scale{0.75};
    double critical_refresh_scale{0.50};
};

// The monitor never touches physical memory itself. The host supplies these
// operations from its DRAM controller, ECC driver, or platform firmware
// interface. A null hook means the monitor records the action as successful,
// which is useful for deterministic policy tests.
struct HardwareHooks {
    std::function<bool(const RowAddress&, RefreshReason)> refresh_row;
    std::function<bool(const RowAddress&, IsolationReason)> isolate_row;
};

struct EccEvent {
    std::uint32_t corrected_errors{0};
    std::uint32_t uncorrectable_errors{0};
};

struct ActionReport {
    bool accepted{true};
    bool already_isolated{false};
    bool threshold_exceeded{false};
    std::vector<RowAddress> refreshed;
    std::vector<RowAddress> isolated;
    std::vector<RowAddress> refresh_failed;
    std::vector<RowAddress> isolation_failed;
};

struct RowStatistics {
    RowAddress address{};
    RegionKind region_kind{RegionKind::Normal};
    std::uint64_t total_activations{0};
    std::uint64_t blocked_activations{0};
    std::uint64_t window_activations{0};
    double activation_rate_hz{0.0};
    TimePoint last_refresh{};
    Duration since_last_refresh{0};
    Duration effective_refresh_interval{0};
    std::uint64_t effective_activation_threshold{0};
    std::uint64_t corrected_ecc_errors{0};
    std::uint64_t uncorrectable_ecc_errors{0};
    bool isolated{false};
    std::optional<IsolationReason> isolation_reason;
    std::uint64_t preventive_refreshes{0};
    std::uint64_t isolation_events{0};
};

NETSEC_API std::string_view to_string(RegionKind kind) noexcept;
NETSEC_API std::string_view to_string(RefreshReason reason) noexcept;
NETSEC_API std::string_view to_string(IsolationReason reason) noexcept;

// Thread-safe row-activation policy engine. All row state is protected by an
// internal mutex; hardware hooks are invoked after state updates and outside
// that mutex, so a hook may safely call back into the monitor.
class NETSEC_API RowActivationMonitor {
public:
    explicit RowActivationMonitor(MonitorConfig config = {},
                                  HardwareHooks hooks = {});
    ~RowActivationMonitor();

    RowActivationMonitor(RowActivationMonitor&&) noexcept;
    RowActivationMonitor& operator=(RowActivationMonitor&&) noexcept;

    RowActivationMonitor(const RowActivationMonitor&) = delete;
    RowActivationMonitor& operator=(const RowActivationMonitor&) = delete;

    // Register page-table regions before recording their first row event.
    // Re-registering the same kind is idempotent. Changing a kind that already
    // owns row state is rejected.
    bool register_region(RegionId region, RegionKind kind);
    bool register_page_table_region(RegionId region);
    std::optional<RegionKind> region_kind(RegionId region) const;

    // Record one DRAM row activation. The activation that crosses a threshold
    // is accepted; the monitor then refreshes the local neighbourhood and
    // quarantines it, so subsequent activations are rejected.
    ActionReport record_activation(
        RowAddress row,
        TimePoint now = std::chrono::steady_clock::now());

    // Feed ECC corrections from the host. Corrected errors refresh the row and
    // add pressure; repeated/uncorrectable errors quarantine the neighbourhood.
    ActionReport record_ecc(
        RowAddress row, EccEvent event,
        TimePoint now = std::chrono::steady_clock::now());

    // Periodic policy pass. It proactively refreshes rows whose refresh age
    // exceeds their current (temperature/ECC-adjusted) interval.
    ActionReport maintain(
        TimePoint now = std::chrono::steady_clock::now());

    // Ask the host to refresh one row immediately. This is useful after a
    // platform event that the monitor did not itself observe.
    ActionReport refresh_now(
        RowAddress row,
        TimePoint now = std::chrono::steady_clock::now());

    // Inform the monitor that hardware already refreshed a row, without
    // invoking the refresh hook.
    void record_refresh(
        RowAddress row,
        TimePoint now = std::chrono::steady_clock::now());

    void set_temperature(double celsius);
    double temperature() const;

    std::optional<RowStatistics> statistics(
        RowAddress row,
        TimePoint now = std::chrono::steady_clock::now()) const;
    std::vector<RowStatistics> snapshot(
        TimePoint now = std::chrono::steady_clock::now()) const;

    bool is_isolated(RowAddress row) const;
    std::optional<IsolationReason> isolation_reason(RowAddress row) const;
    std::size_t tracked_rows() const;

    // Effective policy values after applying region kind, temperature, and
    // accumulated corrected ECC information.
    std::uint64_t effective_activation_threshold(
        RowAddress row,
        TimePoint now = std::chrono::steady_clock::now()) const;
    Duration effective_refresh_interval(
        RowAddress row,
        TimePoint now = std::chrono::steady_clock::now()) const;

private:
    std::unique_ptr<detail::MonitorImpl> impl_;
};

}  // namespace netsec
