#pragma once

#include <compare>
#include <cstddef>
#include <cstdint>
#include <memory>
#include <optional>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace netsec {

using DomainId = std::uint64_t;
using CpuId = std::uint32_t;
using Pc = std::uint64_t;
using Epoch = std::uint64_t;

// A security domain is the unit the scheduler uses when it changes the
// address-space or privilege context.  Domains are deliberately independent
// of thread and process identifiers: a process may contain several domains,
// and several processes may share a non-sensitive domain (for example a
// system service).  "label" is the domain's predictor label.  A zero label
// asks the isolator to derive a unique, unpredictable label.
struct SecurityDomain {
    DomainId id{};
    bool sensitive{false};
    std::uint64_t label{0};
    std::string name{};

    auto operator<=>(const SecurityDomain&) const = default;
};

enum class SwitchKind : std::uint8_t {
    ContextSwitch = 0,
    Interrupt,
    InterruptReturn,
    Migration,
};

enum class Status : std::uint8_t {
    Ok = 0,
    Rejected,
    UnknownDomain,
    NotResident,
    CrossDomainViolation,
    DuplicateDomain,
    TagConflict,
    TagMismatch,
    EmptyInterruptStack,
    InvalidState,
    ImportRejected,
};

std::string_view to_string(Status status) noexcept;

enum class AlertKind : std::uint8_t {
    UnknownDomain = 0,
    DuplicateDomain,
    TagConflict,
    CrossDomainTraining,
    CrossDomainPrediction,
    TagMismatch,
    PredictionRateAnomaly,
    IllegalMigration,
    InterruptStackUnderflow,
    SensitiveStateWiped,
    StateImportRejected,
    SchedulerMisuse,
};

std::string_view to_string(AlertKind kind) noexcept;

struct Alert {
    AlertKind kind{AlertKind::SchedulerMisuse};
    DomainId domain{};
    DomainId other_domain{};
    CpuId cpu{};
    Pc pc{};
    double value{0.0};
    std::uint64_t sequence{0};
    std::string detail{};
};

// A tag is attached to every predictor entry.  An entry is usable only when
// the complete tag (including the domain label, epoch and CPU) matches the
// tags of the currently resident domain.  Consequently stale entries left by
// an earlier context cannot be selected by a later one, even when a table
// index collides.
struct PredictorTag {
    DomainId domain{};
    std::uint64_t label{};
    Epoch epoch{};
    CpuId cpu{};
    bool sensitive{false};
    std::uint64_t value{};

    auto operator<=>(const PredictorTag&) const = default;
};

struct Prediction {
    bool taken{false};
    bool hit{false};
    bool tag_match{false};
    bool rejected{false};
    std::uint8_t confidence{1};
    DomainId domain{};
    PredictorTag tag{};
};

struct TrainResult {
    bool accepted{false};
    Status status{Status::Ok};
    bool predicted_taken{false};
    bool mispredicted{false};
    bool anomaly{false};
    double mispredict_rate{0.0};
    Epoch epoch{};

    explicit operator bool() const noexcept { return accepted; }
};

struct SwitchReport {
    bool accepted{false};
    Status status{Status::Ok};
    SwitchKind kind{SwitchKind::ContextSwitch};
    DomainId from{};
    DomainId to{};
    CpuId cpu{};
    bool from_flushed{false};
    bool to_restored{false};
    Epoch from_epoch{};
    Epoch to_epoch{};
};

struct DomainStats {
    DomainId domain{};
    bool sensitive{false};
    Epoch epoch{0};
    std::uint64_t trainings{0};
    std::uint64_t predictions{0};
    std::uint64_t hits{0};
    std::uint64_t misses{0};
    std::uint64_t correct{0};
    std::uint64_t mispredicts{0};
    std::uint64_t cross_domain_attempts{0};
    std::uint64_t flushes{0};
    std::uint64_t migrations{0};
    std::uint64_t anomaly_episodes{0};
    std::size_t window_samples{0};
    double mispredict_rate{0.0};
};

// Portable state transfer unit.  Import is bound to the registered domain
// label, so a snapshot cannot be replayed into a different domain.  It is
// useful for scheduler hand-off and for checkpoint/restore of a domain.
struct PredictorSnapshot {
    SecurityDomain domain{};
    CpuId cpu{};
    Epoch epoch{};
    PredictorTag tag{};
    std::vector<std::pair<Pc, std::uint8_t>> entries{};
    std::uint64_t accuracy_samples{0};
    std::uint64_t correct{0};
    std::uint64_t mispredicts{0};
};

struct ImportResult {
    bool accepted{false};
    bool flushed{false};
    Status status{Status::Ok};
    Epoch epoch{};

    explicit operator bool() const noexcept { return accepted; }
};

struct IsolatorConfig {
    // Number of predictor entries in every (CPU, domain) partition.
    std::size_t entries_per_domain{256};

    // Sliding window used for prediction-rate anomaly detection.
    std::size_t history_window{64};
    std::size_t anomaly_min_samples{16};
    double anomaly_mispredict_threshold{0.50};

    // The default is logical partitioning with independent labels.  Setting
    // this to true additionally wipes a sensitive partition whenever a
    // different domain becomes resident, an interrupt is taken, or the domain
    // is migrated.  Partitioning alone already prevents tag matches across
    // domains; wiping is the stricter deployment policy.
    bool wipe_sensitive_on_cross_domain{false};

    // If false, even non-sensitive partitions are discarded on a domain
    // switch.  This is useful for very small predictor structures.
    bool partition_non_sensitive{true};

    // Zero asks for a process-unique unpredictable seed.
    std::uint64_t seed{0};

    std::size_t max_domains{128};
    std::size_t max_alerts{4096};
};

// Branch-predictor isolation for a C++20 scheduler.
//
// Typical integration:
//   iso.register_domain({.id = attacker, .sensitive = false});
//   iso.register_domain({.id = victim,   .sensitive = true});
//   iso.context_switch(victim, cpu);          // scheduler hook
//   auto p = iso.predict(victim, pc);         // before executing branch
//   iso.train(victim, pc, actual_taken);      // after resolving branch
//   iso.enter_interrupt(handler, cpu);        // interrupt entry
//   iso.return_from_interrupt(cpu);           // interrupt return
//   iso.migrate(victim, old_cpu, new_cpu);    // task migration
//
// Every operation is serialized internally and may be called from scheduler
// or tracing contexts on different CPUs.
class PredictorIsolator {
public:
    explicit PredictorIsolator(IsolatorConfig config = {});
    ~PredictorIsolator();

    PredictorIsolator(const PredictorIsolator&) = delete;
    PredictorIsolator& operator=(const PredictorIsolator&) = delete;
    PredictorIsolator(PredictorIsolator&&) noexcept;
    PredictorIsolator& operator=(PredictorIsolator&&) noexcept;

    // Domain registry -------------------------------------------------------
    bool register_domain(const SecurityDomain& domain);
    bool register_domain(DomainId id, bool sensitive = false,
                         std::uint64_t label = 0, std::string name = {}) {
        return register_domain(
            SecurityDomain{id, sensitive, label, std::move(name)});
    }
    bool unregister_domain(DomainId domain);
    bool is_registered(DomainId domain) const;
    std::optional<SecurityDomain> domain(DomainId id) const;

    // Scheduler hooks -------------------------------------------------------
    // A switch to an unknown domain is rejected and the current residency is
    // left untouched.  Switching between two threads that belong to the same
    // domain is a legal no-op and keeps the domain's trained state.
    SwitchReport context_switch(DomainId next, CpuId cpu = 0);
    SwitchReport switch_to(DomainId next, CpuId cpu = 0) {
        return context_switch(next, cpu);
    }

    // Enter an interrupt/exception handler.  The interrupted context is
    // pushed on a per-CPU stack; handler training stays in the handler's
    // partition.  Nested interrupts are supported.
    SwitchReport enter_interrupt(DomainId handler, CpuId cpu = 0);
    SwitchReport return_from_interrupt(CpuId cpu = 0);

    // Move a domain's partition between CPUs.  The source CPU is purged so a
    // following task on that CPU cannot observe the migrated state.  For a
    // non-sensitive domain the counters are re-tagged and preserved.
    SwitchReport migrate(DomainId domain, CpuId from_cpu, CpuId to_cpu);

    // Alternative names used by several scheduler integrations.
    SwitchReport on_context_switch(DomainId next, CpuId cpu = 0) {
        return context_switch(next, cpu);
    }
    SwitchReport on_interrupt(DomainId handler, CpuId cpu = 0) {
        return enter_interrupt(handler, cpu);
    }
    SwitchReport on_interrupt_return(CpuId cpu = 0) {
        return return_from_interrupt(cpu);
    }
    SwitchReport on_migration(DomainId domain, CpuId from_cpu, CpuId to_cpu) {
        return migrate(domain, from_cpu, to_cpu);
    }

    // Predictor access ------------------------------------------------------
    // Explicit-domain forms reject calls whose domain is not the resident
    // domain on that CPU.  The overloads taking a SecurityDomain additionally
    // validate a non-zero label.
    Prediction predict(DomainId domain, Pc pc, CpuId cpu = 0);
    Prediction predict(const SecurityDomain& domain, Pc pc, CpuId cpu = 0);

    TrainResult train(DomainId domain, Pc pc, bool taken, CpuId cpu = 0);
    TrainResult train(const SecurityDomain& domain, Pc pc, bool taken,
                      CpuId cpu = 0);

    // Convenience forms for the domain currently resident on the CPU.
    Prediction predict_active(Pc pc, CpuId cpu = 0) {
        return predict_current(pc, cpu);
    }
    Prediction predict_current(Pc pc, CpuId cpu = 0);
    TrainResult train_active(Pc pc, bool taken, CpuId cpu = 0) {
        return train_current(pc, taken, cpu);
    }
    TrainResult train_current(Pc pc, bool taken, CpuId cpu = 0);

    // State inspection / lifecycle -----------------------------------------
    std::optional<DomainId> active_domain(CpuId cpu = 0) const;
    std::optional<DomainStats> stats(DomainId domain) const;
    std::optional<DomainStats> stats(DomainId domain, CpuId cpu) const;
    std::optional<PredictorTag> tag_for(DomainId domain, CpuId cpu = 0) const;

    std::size_t flush_domain(DomainId domain);
    std::size_t flush_cpu(CpuId cpu);
    std::size_t flush_all();

    std::optional<PredictorSnapshot> export_snapshot(DomainId domain,
                                                     CpuId cpu = 0) const;
    ImportResult import_snapshot(const PredictorSnapshot& snapshot, CpuId cpu,
                                 bool allow_migration = false);

    // Alerts are bounded and retained until take_alerts()/clear_alerts().
    std::vector<Alert> alerts() const;
    std::vector<Alert> take_alerts();
    std::size_t alert_count(AlertKind kind) const;
    std::optional<Alert> last_alert() const;
    void clear_alerts();

    const IsolatorConfig& config() const;

private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
};

using BranchPredictorIsolator = PredictorIsolator;
using PredictorIsolation = PredictorIsolator;

}  // namespace netsec
