#include "netsec.hpp"

#include <chrono>
#include <cstdint>
#include <exception>
#include <iostream>
#include <stdexcept>
#include <string_view>
#include <vector>

namespace {

using namespace netsec;
using namespace std::chrono_literals;

int failures = 0;

void check(bool condition, std::string_view expression, int line) {
    if (!condition) {
        std::cerr << "FAIL line " << line << ": " << expression << '\n';
        ++failures;
    }
}

#define CHECK(expression) check((expression), #expression, __LINE__)

TimePoint at(std::int64_t milliseconds) {
    return TimePoint{} + std::chrono::milliseconds(milliseconds);
}

bool contains(const std::vector<RowAddress>& rows, RowAddress row) {
    for (const auto& candidate : rows) {
        if (candidate == row) {
            return true;
        }
    }
    return false;
}

MonitorConfig fast_config() {
    MonitorConfig config;
    config.activation_window = 100ms;
    config.activation_threshold = 8;
    config.page_table_activation_threshold = 4;
    config.max_refresh_interval = 1000ms;
    config.page_table_max_refresh_interval = 200ms;
    config.neighbor_refresh_distance = 1;
    config.isolation_radius = 1;
    config.corrected_ecc_threshold = 3;
    config.ecc_activation_weight = 4;
    config.warning_temperature_c = 75.0;
    config.critical_temperature_c = 90.0;
    config.warning_threshold_scale = 0.75;
    config.critical_threshold_scale = 0.50;
    config.warning_refresh_scale = 0.75;
    config.critical_refresh_scale = 0.50;
    return config;
}

void test_legitimate_workload_stays_available() {
    MonitorConfig config = fast_config();
    config.activation_threshold = 32;
    config.max_refresh_interval = 1000ms;
    RowActivationMonitor monitor(config);

    CHECK(monitor.register_region(1, RegionKind::Normal));
    const RowAddress row{1, 0, 100};
    for (int i = 0; i < 20; ++i) {
        const auto report = monitor.record_activation(row, at(i * 5));
        CHECK(report.accepted);
        CHECK(!report.threshold_exceeded);
    }

    const auto stats = monitor.statistics(row, at(99));
    CHECK(stats.has_value());
    CHECK(stats->total_activations == 20);
    CHECK(stats->window_activations == 20);
    CHECK(stats->activation_rate_hz == 200.0);
    CHECK(!stats->isolated);
    CHECK(stats->blocked_activations == 0);

    const auto accepted = monitor.record_activation(row, at(200));
    CHECK(accepted.accepted);
    const auto later = monitor.statistics(row, at(200));
    CHECK(later->window_activations == 1);

    const auto maintenance = monitor.maintain(at(1500));
    CHECK(contains(maintenance.refreshed, row));
    const auto refreshed = monitor.statistics(row, at(1500));
    CHECK(refreshed->preventive_refreshes == 1);
    CHECK(refreshed->since_last_refresh == 0ms);
    CHECK(!refreshed->isolated);
}

void test_high_frequency_activation_is_contained() {
    const RowAddress target{10, 2, 50};
    const RowAddress below{10, 2, 49};
    const RowAddress above{10, 2, 51};
    const RowAddress outside{10, 2, 52};

    std::vector<RefreshReason> refreshes;
    std::vector<IsolationReason> isolations;
    // Replace the monitor with one that observes platform actions.
    RowActivationMonitor observed(
        fast_config(),
        HardwareHooks{
            .refresh_row =
                [&](const RowAddress& row, RefreshReason reason) {
                    CHECK(row.region == 10);
                    refreshes.push_back(reason);
                    return true;
                },
            .isolate_row =
                [&](const RowAddress& row, IsolationReason reason) {
                    CHECK(row.region == 10);
                    isolations.push_back(reason);
                    return true;
                },
        });
    CHECK(observed.register_region(10, RegionKind::Normal));

    for (int i = 0; i < 7; ++i) {
        const auto report = observed.record_activation(target, at(i));
        CHECK(report.accepted);
        CHECK(!report.threshold_exceeded);
    }
    CHECK(refreshes.empty());
    CHECK(isolations.empty());

    const auto threshold = observed.record_activation(target, at(7));
    CHECK(threshold.accepted);
    CHECK(threshold.threshold_exceeded);
    CHECK(threshold.refreshed.size() == 3);
    CHECK(threshold.isolated.size() == 3);
    CHECK(contains(threshold.refreshed, below));
    CHECK(contains(threshold.refreshed, target));
    CHECK(contains(threshold.refreshed, above));
    CHECK(contains(threshold.isolated, below));
    CHECK(contains(threshold.isolated, target));
    CHECK(contains(threshold.isolated, above));
    CHECK(refreshes.size() == 3);
    CHECK(isolations.size() == 3);
    CHECK(observed.is_isolated(target));
    CHECK(observed.is_isolated(below));
    CHECK(observed.is_isolated(above));
    CHECK(!observed.is_isolated(outside));
    CHECK(observed.isolation_reason(target) ==
          IsolationReason::ActivationThreshold);

    // Model a victim row whose charge disturbance flips only after the
    // threshold is exceeded. Quarantine must reject every further activation.
    constexpr int kFlipAfterAcceptedActivations = 8;
    int accepted_activations = 8;
    bool victim_flipped = false;
    for (int i = 0; i < 10000; ++i) {
        const auto report = observed.record_activation(target, at(100 + i));
        CHECK(!report.accepted);
        CHECK(report.already_isolated);
        if (report.accepted) {
            ++accepted_activations;
            if (accepted_activations > kFlipAfterAcceptedActivations) {
                victim_flipped = true;
            }
        }
    }
    CHECK(!victim_flipped);

    const auto stats = observed.statistics(target, at(10100));
    CHECK(stats->total_activations == 8);
    CHECK(stats->blocked_activations == 10000);
    CHECK(stats->isolated);
}

void test_page_table_region_is_independent() {
    RowActivationMonitor monitor(fast_config());
    CHECK(monitor.register_region(1, RegionKind::Normal));
    CHECK(monitor.register_page_table_region(7));
    CHECK(monitor.region_kind(7) == RegionKind::PageTable);

    const RowAddress normal_target{1, 4, 100};
    const RowAddress page_table_target{7, 4, 100};
    const RowAddress page_table_below{7, 4, 99};
    const RowAddress page_table_above{7, 4, 101};

    // Establish the page-table row before the normal-memory attack.
    CHECK(monitor.record_activation(page_table_target, at(0)).accepted);
    for (int i = 0; i < 8; ++i) {
        monitor.record_activation(normal_target, at(i));
    }
    CHECK(monitor.is_isolated(normal_target));
    CHECK(!monitor.is_isolated(page_table_target));
    CHECK(!monitor.is_isolated(page_table_below));
    CHECK(!monitor.is_isolated(page_table_above));

    const auto normal_stats = monitor.statistics(normal_target, at(10));
    const auto table_stats = monitor.statistics(page_table_target, at(10));
    CHECK(normal_stats->region_kind == RegionKind::Normal);
    CHECK(table_stats->region_kind == RegionKind::PageTable);
    CHECK(monitor.effective_activation_threshold(normal_target) == 8);
    CHECK(monitor.effective_activation_threshold(page_table_target) == 4);

    // The page-table threshold is independent and stricter. The old
    // activation has aged out, so four fresh accesses trigger quarantine.
    for (int i = 0; i < 4; ++i) {
        monitor.record_activation(page_table_target, at(1000 + i));
    }
    CHECK(monitor.is_isolated(page_table_target));
    CHECK(monitor.is_isolated(page_table_below));
    CHECK(monitor.is_isolated(page_table_above));
    CHECK(monitor.isolation_reason(page_table_target) ==
          IsolationReason::ActivationThreshold);
}

void test_temperature_changes_policy() {
    MonitorConfig config = fast_config();
    config.activation_threshold = 20;
    config.max_refresh_interval = 1000ms;
    RowActivationMonitor monitor(config);
    CHECK(monitor.register_region(1, RegionKind::Normal));
    const RowAddress row{1, 0, 300};

    CHECK(monitor.temperature() == 25.0);
    CHECK(monitor.effective_activation_threshold(row) == 20);
    CHECK(monitor.effective_refresh_interval(row) == 1000ms);
    for (int i = 0; i < 10; ++i) {
        CHECK(monitor.record_activation(row, at(i)).accepted);
    }
    CHECK(!monitor.is_isolated(row));

    monitor.set_temperature(95.0);
    CHECK(monitor.temperature() == 95.0);
    CHECK(monitor.effective_activation_threshold(row) == 10);
    CHECK(monitor.effective_refresh_interval(row) == 500ms);

    const auto report = monitor.record_activation(row, at(10));
    CHECK(report.threshold_exceeded);
    CHECK(monitor.is_isolated(row));
}

void test_ecc_feedback_refreshes_then_quarantines() {
    MonitorConfig config = fast_config();
    config.activation_threshold = 1000;
    config.corrected_ecc_threshold = 3;
    config.max_refresh_interval = 5000ms;
    RowActivationMonitor monitor(config);
    CHECK(monitor.register_region(1, RegionKind::Normal));

    const RowAddress row{1, 1, 200};
    const RowAddress below{1, 1, 199};
    const RowAddress above{1, 1, 201};

    const auto first = monitor.record_ecc(row, {.corrected_errors = 1},
                                          at(0));
    CHECK(!first.threshold_exceeded);
    CHECK(first.refreshed.size() == 3);
    CHECK(contains(first.refreshed, row));
    CHECK(contains(first.refreshed, below));
    CHECK(contains(first.refreshed, above));
    CHECK(first.isolated.empty());
    CHECK(!monitor.is_isolated(row));

    const auto second = monitor.record_ecc(row, {.corrected_errors = 2},
                                           at(1));
    CHECK(second.threshold_exceeded);
    CHECK(second.isolated.size() == 3);
    CHECK(monitor.is_isolated(row));
    CHECK(monitor.isolation_reason(row) ==
          IsolationReason::CorrectedEccThreshold);
    const auto stats = monitor.statistics(row, at(2));
    CHECK(stats->corrected_ecc_errors == 3);

    const RowAddress uncorrectable{1, 1, 400};
    const auto fatal = monitor.record_ecc(
        uncorrectable, {.uncorrectable_errors = 1}, at(3));
    CHECK(fatal.threshold_exceeded);
    CHECK(fatal.isolated.size() == 3);
    CHECK(monitor.is_isolated(uncorrectable));
    CHECK(monitor.isolation_reason(uncorrectable) ==
          IsolationReason::UncorrectableEcc);

    // A quarantined row remains unavailable after ECC recovery actions.
    CHECK(!monitor.record_activation(row, at(4)).accepted);
}

void test_refresh_interval_is_enforced() {
    MonitorConfig config = fast_config();
    config.activation_threshold = 1000;
    config.page_table_activation_threshold = 1000;
    config.max_refresh_interval = 50ms;
    config.page_table_max_refresh_interval = 20ms;
    RowActivationMonitor monitor(config);
    CHECK(monitor.register_region(1, RegionKind::Normal));
    CHECK(monitor.register_page_table_region(2));

    const RowAddress normal{1, 0, 10};
    const RowAddress page_table{2, 0, 20};
    CHECK(monitor.record_activation(normal, at(0)).accepted);
    CHECK(monitor.record_activation(page_table, at(0)).accepted);

    CHECK(monitor.maintain(at(10)).refreshed.empty());
    const auto normal_due = monitor.record_activation(normal, at(60));
    CHECK(contains(normal_due.refreshed, normal));
    CHECK(monitor.statistics(normal, at(60))->preventive_refreshes == 1);

    const auto page_table_due =
        monitor.record_activation(page_table, at(61));
    CHECK(contains(page_table_due.refreshed, page_table));
    CHECK(monitor.statistics(page_table, at(61))->preventive_refreshes == 1);

    const auto maintenance = monitor.maintain(at(120));
    CHECK(contains(maintenance.refreshed, normal));
    CHECK(contains(maintenance.refreshed, page_table));
}

void test_record_refresh_does_not_invoke_hook() {
    int refresh_calls = 0;
    RowActivationMonitor monitor(
        fast_config(),
        HardwareHooks{.refresh_row =
                          [&](const RowAddress&, RefreshReason) {
                              ++refresh_calls;
                              return true;
                          }});
    CHECK(monitor.register_region(1, RegionKind::Normal));
    const RowAddress row{1, 0, 60};
    monitor.record_activation(row, at(0));
    monitor.record_refresh(row, at(25));
    CHECK(refresh_calls == 0);
    CHECK(monitor.statistics(row, at(25))->since_last_refresh == 0ms);
}

void test_isolation_failure_is_fail_closed() {
    RowActivationMonitor monitor(
        fast_config(),
        HardwareHooks{.isolate_row =
                          [](const RowAddress&, IsolationReason) {
                              return false;
                          }});
    CHECK(monitor.register_region(1, RegionKind::Normal));
    const RowAddress row{1, 0, 80};
    ActionReport report;
    for (int i = 0; i < 8; ++i) {
        report = monitor.record_activation(row, at(i));
    }
    CHECK(!report.isolation_failed.empty());
    CHECK(monitor.is_isolated(row));
    CHECK(!monitor.record_activation(row, at(9)).accepted);
}

void test_hooks_can_reenter_monitor() {
    RowActivationMonitor* monitor = nullptr;
    RowActivationMonitor reentrant(
        fast_config(),
        HardwareHooks{
            .refresh_row =
                [&](const RowAddress& row, RefreshReason) {
                    CHECK(monitor != nullptr);
                    CHECK(monitor->tracked_rows() >= 3);
                    CHECK(!monitor->is_isolated(row) ||
                          monitor->isolation_reason(row).has_value());
                    return true;
                },
            .isolate_row =
                [&](const RowAddress& row, IsolationReason) {
                    CHECK(monitor != nullptr);
                    CHECK(monitor->is_isolated(row));
                    return true;
                },
        });
    monitor = &reentrant;
    CHECK(reentrant.register_region(1, RegionKind::Normal));
    const RowAddress row{1, 0, 90};
    for (int i = 0; i < 8; ++i) {
        reentrant.record_activation(row, at(i));
    }
    CHECK(reentrant.is_isolated(row));
}

void test_configuration_validation() {
    bool threw = false;
    MonitorConfig invalid = fast_config();
    invalid.activation_threshold = 0;
    try {
        RowActivationMonitor monitor(invalid);
        (void)monitor;
    } catch (const std::invalid_argument&) {
        threw = true;
    }
    CHECK(threw);

    threw = false;
    invalid = fast_config();
    invalid.warning_temperature_c = 100.0;
    invalid.critical_temperature_c = 90.0;
    try {
        RowActivationMonitor monitor(invalid);
        (void)monitor;
    } catch (const std::invalid_argument&) {
        threw = true;
    }
    CHECK(threw);
}

}  // namespace

int main() {
    try {
        test_legitimate_workload_stays_available();
        test_high_frequency_activation_is_contained();
        test_page_table_region_is_independent();
        test_temperature_changes_policy();
        test_ecc_feedback_refreshes_then_quarantines();
        test_refresh_interval_is_enforced();
        test_record_refresh_does_not_invoke_hook();
        test_isolation_failure_is_fail_closed();
        test_hooks_can_reenter_monitor();
        test_configuration_validation();
    } catch (const std::exception& error) {
        std::cerr << "UNEXPECTED EXCEPTION: " << error.what() << '\n';
        return 2;
    }

    if (failures != 0) {
        std::cerr << failures << " test assertion(s) failed\n";
        return 1;
    }
    std::cout << "all netsec tests passed\n";
    return 0;
}
