#include "netsec.hpp"

#include <algorithm>
#include <chrono>
#include <mutex>
#include <numeric>
#include <random>
#include <utility>

namespace netsec {

namespace {

constexpr std::uint8_t kInitialCounter = 1;  // weakly not-taken

std::uint64_t splitmix64(std::uint64_t value) noexcept {
    value += 0x9E3779B97F4A7C15ULL;
    value = (value ^ (value >> 30)) * 0xBF58476D1CE4E5B9ULL;
    value = (value ^ (value >> 27)) * 0x94D049BB133111EBULL;
    return value ^ (value >> 31);
}

std::uint64_t make_process_seed() noexcept {
    std::random_device device;
    const auto now = static_cast<std::uint64_t>(
        std::chrono::steady_clock::now().time_since_epoch().count());
    return splitmix64((static_cast<std::uint64_t>(device()) << 32U) ^ now);
}

}  // namespace

std::string_view to_string(Status status) noexcept {
    switch (status) {
        case Status::Ok:
            return "ok";
        case Status::Rejected:
            return "rejected";
        case Status::UnknownDomain:
            return "unknown-domain";
        case Status::NotResident:
            return "not-resident";
        case Status::CrossDomainViolation:
            return "cross-domain-violation";
        case Status::DuplicateDomain:
            return "duplicate-domain";
        case Status::TagConflict:
            return "tag-conflict";
        case Status::TagMismatch:
            return "tag-mismatch";
        case Status::EmptyInterruptStack:
            return "empty-interrupt-stack";
        case Status::InvalidState:
            return "invalid-state";
        case Status::ImportRejected:
            return "import-rejected";
    }
    return "unknown";
}

std::string_view to_string(AlertKind kind) noexcept {
    switch (kind) {
        case AlertKind::UnknownDomain:
            return "unknown-domain";
        case AlertKind::DuplicateDomain:
            return "duplicate-domain";
        case AlertKind::TagConflict:
            return "tag-conflict";
        case AlertKind::CrossDomainTraining:
            return "cross-domain-training";
        case AlertKind::CrossDomainPrediction:
            return "cross-domain-prediction";
        case AlertKind::TagMismatch:
            return "tag-mismatch";
        case AlertKind::PredictionRateAnomaly:
            return "prediction-rate-anomaly";
        case AlertKind::IllegalMigration:
            return "illegal-migration";
        case AlertKind::InterruptStackUnderflow:
            return "interrupt-stack-underflow";
        case AlertKind::SensitiveStateWiped:
            return "sensitive-state-wiped";
        case AlertKind::StateImportRejected:
            return "state-import-rejected";
        case AlertKind::SchedulerMisuse:
            return "scheduler-misuse";
    }
    return "unknown";
}

struct PredictorIsolator::Impl {
    struct Entry {
        Pc pc{};
        PredictorTag tag{};
        std::uint8_t counter{kInitialCounter};
        bool valid{false};
    };

    struct DomainState {
        Epoch epoch{1};
        std::vector<Entry> table;
        std::unordered_map<Pc, Prediction> pending;
        std::deque<std::uint8_t> recent_mispredicts;
        bool anomaly_latched{false};
    };

    struct DomainRecord {
        SecurityDomain domain;
        std::uint64_t label{0};
        std::uint64_t registration_nonce{0};
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
    };

    struct SavedFrame {
        std::optional<DomainId> domain;
        bool wiped{false};
    };

    struct CpuBank {
        std::optional<DomainId> resident;
        std::unordered_map<DomainId, DomainState> partitions;
        std::vector<SavedFrame> interrupt_stack;
        std::uint64_t switches{0};
    };

    IsolatorConfig cfg;
    std::uint64_t seed{0};
    mutable std::mutex mutex;
    std::unordered_map<DomainId, DomainRecord> domains;
    std::unordered_map<std::uint64_t, DomainId> label_owner;
    std::unordered_map<CpuId, CpuBank> cpus;
    std::deque<Alert> alert_log;
    std::uint64_t next_alert_sequence{1};
    std::uint64_t next_domain_nonce{1};

    explicit Impl(IsolatorConfig config) : cfg(std::move(config)) {
        cfg.entries_per_domain = std::max<std::size_t>(2, cfg.entries_per_domain);
        cfg.history_window = std::max<std::size_t>(1, cfg.history_window);
        cfg.anomaly_min_samples = std::clamp<std::size_t>(
            cfg.anomaly_min_samples, 1, cfg.history_window);
        cfg.anomaly_mispredict_threshold =
            std::clamp(cfg.anomaly_mispredict_threshold, 0.0, 1.0);
        cfg.max_domains = std::max<std::size_t>(1, cfg.max_domains);
        cfg.max_alerts = std::max<std::size_t>(1, cfg.max_alerts);
        seed = cfg.seed != 0 ? cfg.seed : make_process_seed();
    }

    std::uint64_t derive_label(DomainId domain) {
        std::uint64_t nonce = 0;
        for (;;) {
            const std::uint64_t candidate =
                splitmix64(seed ^ splitmix64(domain) ^
                           splitmix64(++nonce) ^ 0xD1B54A32D192ED03ULL);
            if (candidate != 0 && !label_owner.contains(candidate)) {
                return candidate;
            }
        }
    }

    DomainRecord* record(DomainId id) {
        const auto it = domains.find(id);
        return it == domains.end() ? nullptr : &it->second;
    }

    const DomainRecord* record(DomainId id) const {
        const auto it = domains.find(id);
        return it == domains.end() ? nullptr : &it->second;
    }

    void emit(AlertKind kind, DomainId domain, DomainId other, CpuId cpu, Pc pc,
              double value, std::string detail) {
        Alert alert;
        alert.kind = kind;
        alert.domain = domain;
        alert.other_domain = other;
        alert.cpu = cpu;
        alert.pc = pc;
        alert.value = value;
        alert.sequence = next_alert_sequence++;
        alert.detail = std::move(detail);
        alert_log.push_back(std::move(alert));
        while (alert_log.size() > cfg.max_alerts) {
            alert_log.pop_front();
        }
    }

    CpuBank& bank(CpuId cpu) { return cpus[cpu]; }

    const CpuBank* find_bank(CpuId cpu) const {
        const auto it = cpus.find(cpu);
        return it == cpus.end() ? nullptr : &it->second;
    }

    PredictorTag make_tag(const DomainRecord& rec, Epoch epoch, CpuId cpu) const {
        PredictorTag tag;
        tag.domain = rec.domain.id;
        tag.label = rec.label;
        tag.epoch = epoch;
        tag.cpu = cpu;
        tag.sensitive = rec.domain.sensitive;

        std::uint64_t folded = splitmix64(rec.label) ^
                               splitmix64(rec.domain.id) ^
                               splitmix64(rec.registration_nonce) ^
                               splitmix64((static_cast<std::uint64_t>(cpu) << 32U) |
                                          0x43505530ULL) ^
                               splitmix64(epoch);
        if (tag.sensitive) {
            folded ^= 0xA5A5A5A55A5A5A5AULL;
        }
        tag.value = splitmix64(folded);
        return tag;
    }

    DomainState& state_for(CpuId cpu, DomainId id) {
        auto& cpu_bank = bank(cpu);
        auto [it, inserted] = cpu_bank.partitions.try_emplace(id);
        if (inserted || it->second.table.size() != cfg.entries_per_domain) {
            it->second.table.assign(cfg.entries_per_domain, Entry{});
        }
        return it->second;
    }

    const DomainState* find_state(CpuId cpu, DomainId id) const {
        const CpuBank* cpu_bank = find_bank(cpu);
        if (cpu_bank == nullptr) {
            return nullptr;
        }
        const auto it = cpu_bank->partitions.find(id);
        return it == cpu_bank->partitions.end() ? nullptr : &it->second;
    }

    static std::size_t index_for(Pc pc, std::size_t entries) {
        return static_cast<std::size_t>(splitmix64(pc) % entries);
    }

    static bool state_has_entries(const DomainState& state) {
        return std::any_of(state.table.begin(), state.table.end(),
                           [](const Entry& entry) { return entry.valid; });
    }

    void wipe(DomainState& state, DomainRecord& rec) {
        for (Entry& entry : state.table) {
            entry = Entry{};
        }
        state.pending.clear();
        state.recent_mispredicts.clear();
        state.anomaly_latched = false;
        ++state.epoch;
        ++rec.flushes;
    }

    bool should_wipe_on_switch(const DomainRecord& rec) const {
        return rec.domain.sensitive ? cfg.wipe_sensitive_on_cross_domain
                                    : !cfg.partition_non_sensitive;
    }

    bool should_wipe_on_interrupt(const DomainRecord& rec) const {
        return rec.domain.sensitive ? cfg.wipe_sensitive_on_cross_domain
                                    : !cfg.partition_non_sensitive;
    }

    bool should_wipe_on_migration(const DomainRecord& rec) const {
        return rec.domain.sensitive ? cfg.wipe_sensitive_on_cross_domain
                                    : !cfg.partition_non_sensitive;
    }

    Prediction build_prediction(const DomainRecord& rec, const DomainState& state,
                                Pc pc, CpuId cpu) const {
        Prediction prediction;
        prediction.domain = rec.domain.id;
        prediction.tag = make_tag(rec, state.epoch, cpu);

        const std::size_t index = index_for(pc, state.table.size());
        const Entry& entry = state.table[index];
        if (entry.valid && entry.tag.domain != rec.domain.id) {
            prediction.tag_match = false;
            return prediction;
        }
        if (entry.valid && entry.tag == prediction.tag && entry.pc == pc) {
            prediction.hit = true;
            prediction.tag_match = true;
            prediction.taken = entry.counter >= 2;
            prediction.confidence = entry.counter;
        }
        return prediction;
    }

    double window_rate(const DomainState& state) const {
        if (state.recent_mispredicts.empty()) {
            return 0.0;
        }
        const auto sum = std::accumulate(state.recent_mispredicts.begin(),
                                         state.recent_mispredicts.end(),
                                         std::uint64_t{0});
        return static_cast<double>(sum) /
               static_cast<double>(state.recent_mispredicts.size());
    }

    bool record_outcome(DomainRecord& rec, DomainState& state,
                        bool predicted_taken, bool actual_taken, DomainId domain,
                        CpuId cpu) {
        const bool mispredicted = predicted_taken != actual_taken;
        if (mispredicted) {
            ++rec.mispredicts;
        } else {
            ++rec.correct;
        }

        state.recent_mispredicts.push_back(mispredicted ? 1U : 0U);
        while (state.recent_mispredicts.size() > cfg.history_window) {
            state.recent_mispredicts.pop_front();
        }

        const double rate = window_rate(state);
        bool raised = false;
        if (state.recent_mispredicts.size() >= cfg.anomaly_min_samples &&
            rate >= cfg.anomaly_mispredict_threshold) {
            if (!state.anomaly_latched) {
                state.anomaly_latched = true;
                ++rec.anomaly_episodes;
                raised = true;
                emit(AlertKind::PredictionRateAnomaly, domain, 0, cpu, 0, rate,
                     "misprediction rate exceeded the configured threshold");
            }
        } else {
            state.anomaly_latched = false;
        }
        return raised;
    }

    Prediction predict_locked(DomainId domain, Pc pc, CpuId cpu,
                              std::optional<std::uint64_t> label) {
        DomainRecord* rec = record(domain);
        if (rec == nullptr) {
            emit(AlertKind::UnknownDomain, domain, 0, cpu, pc, 0.0,
                 "prediction for an unregistered domain");
            Prediction rejected;
            rejected.domain = domain;
            rejected.rejected = true;
            return rejected;
        }
        if (label.has_value() && *label != 0 && *label != rec->label) {
            ++rec->cross_domain_attempts;
            emit(AlertKind::CrossDomainPrediction, domain, 0, cpu, pc, 0.0,
                 "prediction presented a label that does not belong to the domain");
            Prediction rejected;
            rejected.domain = domain;
            rejected.rejected = true;
            return rejected;
        }

        const CpuBank* cpu_bank = find_bank(cpu);
        if (cpu_bank == nullptr || !cpu_bank->resident.has_value()) {
            emit(AlertKind::SchedulerMisuse, domain, 0, cpu, pc, 0.0,
                 "prediction attempted without an active domain");
            Prediction rejected;
            rejected.domain = domain;
            rejected.rejected = true;
            return rejected;
        }
        if (*cpu_bank->resident != domain) {
            ++rec->cross_domain_attempts;
            emit(AlertKind::CrossDomainPrediction, domain,
                 *cpu_bank->resident,
                 cpu, pc, 0.0,
                 "prediction attempted while a different domain is resident");
            Prediction rejected;
            rejected.domain = domain;
            rejected.rejected = true;
            return rejected;
        }

        DomainState& state = state_for(cpu, domain);
        Entry& entry = state.table[index_for(pc, state.table.size())];
        if (entry.valid && entry.tag.domain != domain) {
            ++rec->cross_domain_attempts;
            emit(AlertKind::TagMismatch, domain, entry.tag.domain, cpu, pc, 0.0,
                 "a foreign tag was found in a domain partition");
            entry = Entry{};
        }
        Prediction prediction = build_prediction(*rec, state, pc, cpu);
        ++rec->predictions;
        if (prediction.hit) {
            ++rec->hits;
        } else {
            ++rec->misses;
        }
        if (state.pending.size() > state.table.size() * 4) {
            state.pending.clear();
        }
        state.pending[pc] = prediction;
        return prediction;
    }

    TrainResult train_locked(DomainId domain, Pc pc, bool taken, CpuId cpu,
                             std::optional<std::uint64_t> label) {
        TrainResult result;
        DomainRecord* rec = record(domain);
        if (rec == nullptr) {
            result.status = Status::UnknownDomain;
            emit(AlertKind::UnknownDomain, domain, 0, cpu, pc, 0.0,
                 "training for an unregistered domain");
            return result;
        }
        if (label.has_value() && *label != 0 && *label != rec->label) {
            ++rec->cross_domain_attempts;
            result.status = Status::CrossDomainViolation;
            emit(AlertKind::CrossDomainTraining, domain, 0, cpu, pc, 0.0,
                 "training presented a label that does not belong to the domain");
            return result;
        }

        CpuBank* cpu_bank = nullptr;
        const auto bank_it = cpus.find(cpu);
        if (bank_it != cpus.end()) {
            cpu_bank = &bank_it->second;
        }
        if (cpu_bank == nullptr || !cpu_bank->resident.has_value()) {
            result.status = Status::NotResident;
            emit(AlertKind::SchedulerMisuse, domain, 0, cpu, pc, 0.0,
                 "training attempted without an active domain");
            return result;
        }
        if (*cpu_bank->resident != domain) {
            ++rec->cross_domain_attempts;
            result.status = Status::CrossDomainViolation;
            emit(AlertKind::CrossDomainTraining, domain,
                 *cpu_bank->resident,
                 cpu, pc, 0.0,
                 "training attempted while a different domain is resident");
            return result;
        }

        DomainState& state = state_for(cpu, domain);
        const PredictorTag tag = make_tag(*rec, state.epoch, cpu);
        Entry& entry = state.table[index_for(pc, state.table.size())];

        if (entry.valid && entry.tag.domain != domain) {
            ++rec->cross_domain_attempts;
            emit(AlertKind::TagMismatch, domain, entry.tag.domain, cpu, pc, 0.0,
                 "a foreign tag was found in a domain partition");
            entry = Entry{};
        }

        Prediction predicted;
        const auto pending_it = state.pending.find(pc);
        if (pending_it != state.pending.end()) {
            predicted = pending_it->second;
            state.pending.erase(pending_it);
        } else {
            predicted = build_prediction(*rec, state, pc, cpu);
        }

        const bool same_entry =
            entry.valid && entry.tag == tag && entry.pc == pc;
        if (!same_entry) {
            entry = Entry{};
            entry.pc = pc;
            entry.tag = tag;
            entry.counter = kInitialCounter;
            entry.valid = true;
        }
        if (taken) {
            entry.counter = static_cast<std::uint8_t>(std::min<unsigned>(
                entry.counter + 1U, 3U));
        } else if (entry.counter > 0) {
            entry.counter = static_cast<std::uint8_t>(entry.counter - 1U);
        }

        ++rec->trainings;
        result.accepted = true;
        result.status = Status::Ok;
        result.predicted_taken = predicted.taken;
        result.mispredicted = predicted.taken != taken;
        result.mispredict_rate = 0.0;
        result.anomaly =
            record_outcome(*rec, state, predicted.taken, taken, domain, cpu);
        result.mispredict_rate = window_rate(state);
        result.epoch = state.epoch;
        return result;
    }

    SwitchReport context_switch_locked(DomainId next, CpuId cpu) {
        SwitchReport report;
        report.kind = SwitchKind::ContextSwitch;
        report.to = next;
        report.cpu = cpu;

        DomainRecord* next_rec = record(next);
        if (next_rec == nullptr) {
            report.status = Status::UnknownDomain;
            emit(AlertKind::UnknownDomain, next, 0, cpu, 0, 0.0,
                 "context switch to an unregistered domain");
            return report;
        }

        CpuBank& cpu_bank = bank(cpu);
        report.from = cpu_bank.resident.value_or(0);
        if (cpu_bank.resident.has_value() && *cpu_bank.resident == next) {
            const DomainState& state = state_for(cpu, next);
            report.accepted = true;
            report.from_epoch = state.epoch;
            report.to_epoch = report.from_epoch;
            report.to_restored = state_has_entries(state);
            return report;
        }

        if (cpu_bank.resident.has_value()) {
            const DomainId from = *cpu_bank.resident;
            if (DomainRecord* from_rec = record(from); from_rec != nullptr) {
                if (should_wipe_on_switch(*from_rec)) {
                    auto state_it = cpu_bank.partitions.find(from);
                    if (state_it != cpu_bank.partitions.end()) {
                        report.from_epoch = state_it->second.epoch;
                        wipe(state_it->second, *from_rec);
                        report.from_flushed = true;
                        if (from_rec->domain.sensitive) {
                            emit(AlertKind::SensitiveStateWiped, from, next, cpu,
                                 0, 0.0,
                                 "sensitive predictor partition wiped on switch");
                        }
                    }
                } else {
                    const DomainState* state = find_state(cpu, from);
                    report.from_epoch = state != nullptr ? state->epoch : 0;
                }
            }
        }

        cpu_bank.resident = next;
        ++cpu_bank.switches;
        const DomainState& next_state = state_for(cpu, next);
        report.to_epoch = next_state.epoch;
        report.to_restored = state_has_entries(next_state);
        report.accepted = true;
        return report;
    }

    SwitchReport enter_interrupt_locked(DomainId handler, CpuId cpu) {
        SwitchReport report;
        report.kind = SwitchKind::Interrupt;
        report.to = handler;
        report.cpu = cpu;

        DomainRecord* handler_rec = record(handler);
        if (handler_rec == nullptr) {
            report.status = Status::UnknownDomain;
            emit(AlertKind::UnknownDomain, handler, 0, cpu, 0, 0.0,
                 "interrupt handler is not a registered domain");
            return report;
        }

        CpuBank& cpu_bank = bank(cpu);
        const std::optional<DomainId> from = cpu_bank.resident;
        report.from = from.value_or(0);

        SavedFrame frame;
        frame.domain = from;
        if (from.has_value() && *from != handler) {
            DomainRecord* from_rec = record(*from);
            if (from_rec != nullptr && should_wipe_on_interrupt(*from_rec)) {
                auto state_it = cpu_bank.partitions.find(*from);
                if (state_it != cpu_bank.partitions.end()) {
                    report.from_epoch = state_it->second.epoch;
                    wipe(state_it->second, *from_rec);
                    frame.wiped = true;
                    report.from_flushed = true;
                    if (from_rec->domain.sensitive) {
                        emit(AlertKind::SensitiveStateWiped, *from, handler, cpu,
                             0, 0.0,
                             "sensitive predictor partition wiped on interrupt");
                    }
                }
            } else if (from_rec != nullptr) {
                const DomainState* state = find_state(cpu, *from);
                report.from_epoch = state != nullptr ? state->epoch : 0;
            }
        }
        cpu_bank.interrupt_stack.push_back(frame);

        cpu_bank.resident = handler;
        const DomainState& handler_state = state_for(cpu, handler);
        report.to_epoch = handler_state.epoch;
        report.to_restored = state_has_entries(handler_state);
        report.accepted = true;
        return report;
    }

    SwitchReport return_from_interrupt_locked(CpuId cpu) {
        SwitchReport report;
        report.kind = SwitchKind::InterruptReturn;
        report.cpu = cpu;

        const auto cpu_it = cpus.find(cpu);
        if (cpu_it == cpus.end() || cpu_it->second.interrupt_stack.empty()) {
            report.status = Status::EmptyInterruptStack;
            emit(AlertKind::InterruptStackUnderflow, 0, 0, cpu, 0, 0.0,
                 "interrupt return without a matching interrupt entry");
            return report;
        }

        CpuBank& cpu_bank = cpu_it->second;
        const std::optional<DomainId> handler = cpu_bank.resident;
        report.from = handler.value_or(0);
        const SavedFrame frame = cpu_bank.interrupt_stack.back();
        cpu_bank.interrupt_stack.pop_back();

        if (handler.has_value()) {
            DomainRecord* handler_rec = record(*handler);
            if (handler_rec != nullptr && frame.domain != handler &&
                should_wipe_on_interrupt(*handler_rec)) {
                auto state_it = cpu_bank.partitions.find(*handler);
                if (state_it != cpu_bank.partitions.end()) {
                    report.from_epoch = state_it->second.epoch;
                    wipe(state_it->second, *handler_rec);
                    report.from_flushed = true;
                    if (handler_rec->domain.sensitive) {
                        emit(AlertKind::SensitiveStateWiped, *handler,
                             frame.domain.value_or(0), cpu, 0, 0.0,
                             "sensitive handler partition wiped on interrupt return");
                    }
                }
            } else {
                const DomainState* state = find_state(cpu, *handler);
                report.from_epoch = state != nullptr ? state->epoch : 0;
            }
        }

        if (frame.domain.has_value() && record(*frame.domain) == nullptr) {
            cpu_bank.resident.reset();
            report.to = 0;
            report.to_epoch = 0;
            report.status = Status::Rejected;
            emit(AlertKind::SchedulerMisuse, *frame.domain, handler.value_or(0),
                 cpu, 0, 0.0,
                 "interrupted domain was unregistered while the handler ran");
            return report;
        }

        cpu_bank.resident = frame.domain;
        report.to = frame.domain.value_or(0);
        if (frame.domain.has_value()) {
            const DomainState& state = state_for(cpu, *frame.domain);
            report.to_epoch = state.epoch;
            report.to_restored = state_has_entries(state);
        }
        report.accepted = true;
        return report;
    }

    SwitchReport migrate_locked(DomainId domain, CpuId from_cpu,
                                CpuId to_cpu) {
        SwitchReport report;
        report.kind = SwitchKind::Migration;
        report.from = from_cpu;
        report.to = to_cpu;
        report.cpu = to_cpu;

        DomainRecord* rec = record(domain);
        if (rec == nullptr) {
            report.status = Status::UnknownDomain;
            emit(AlertKind::UnknownDomain, domain, 0, to_cpu, 0, 0.0,
                 "migration of an unregistered domain");
            return report;
        }
        if (from_cpu == to_cpu) {
            const DomainState* state = find_state(to_cpu, domain);
            report.accepted = true;
            report.to_epoch = state != nullptr ? state->epoch : 0;
            return report;
        }

        CpuBank& source = bank(from_cpu);
        CpuBank& target = bank(to_cpu);

        if (target.resident.has_value() && *target.resident != domain) {
            if (DomainRecord* old = record(*target.resident);
                old != nullptr && should_wipe_on_switch(*old)) {
                auto old_state = target.partitions.find(*target.resident);
                if (old_state != target.partitions.end()) {
                    wipe(old_state->second, *old);
                    if (old->domain.sensitive) {
                        emit(AlertKind::SensitiveStateWiped, *target.resident,
                             domain, to_cpu, 0, 0.0,
                             "sensitive target-CPU partition wiped on migration");
                    }
                }
            }
        }

        DomainState moved;
        auto source_state = source.partitions.find(domain);
        if (source_state != source.partitions.end()) {
            moved = std::move(source_state->second);
            source.partitions.erase(source_state);
        } else {
            moved.table.assign(cfg.entries_per_domain, Entry{});
        }
        if (source.resident.has_value() && *source.resident == domain) {
            source.resident.reset();
        }

        bool flushed = false;
        if (should_wipe_on_migration(*rec)) {
            wipe(moved, *rec);
            flushed = true;
            if (rec->domain.sensitive) {
                emit(AlertKind::SensitiveStateWiped, domain, 0, to_cpu, 0, 0.0,
                     "sensitive predictor partition wiped on migration");
            }
        } else {
            ++moved.epoch;
            std::vector<Entry> retagged(cfg.entries_per_domain, Entry{});
            const PredictorTag tag = make_tag(*rec, moved.epoch, to_cpu);
            for (const Entry& entry : moved.table) {
                if (!entry.valid || entry.tag.domain != domain) {
                    continue;
                }
                Entry copy;
                copy.pc = entry.pc;
                copy.tag = tag;
                copy.counter = entry.counter;
                copy.valid = true;
                retagged[index_for(copy.pc, retagged.size())] = copy;
            }
            moved.table = std::move(retagged);
            moved.pending.clear();
        }

        if (target.partitions.contains(domain)) {
            target.partitions.erase(domain);
        }
        target.partitions[domain] = std::move(moved);
        target.resident = domain;
        ++rec->migrations;

        report.accepted = true;
        report.from_flushed = flushed;
        report.to_epoch = target.partitions[domain].epoch;
        report.to_restored = state_has_entries(target.partitions[domain]);
        return report;
    }
};

PredictorIsolator::PredictorIsolator(IsolatorConfig config)
    : impl_(std::make_unique<Impl>(std::move(config))) {}

PredictorIsolator::~PredictorIsolator() = default;

PredictorIsolator::PredictorIsolator(PredictorIsolator&&) noexcept = default;

PredictorIsolator& PredictorIsolator::operator=(PredictorIsolator&&) noexcept =
    default;

bool PredictorIsolator::register_domain(const SecurityDomain& domain) {
    std::lock_guard lock(impl_->mutex);
    if (impl_->domains.contains(domain.id)) {
        impl_->emit(AlertKind::DuplicateDomain, domain.id, 0, 0, 0, 0.0,
                    "domain id is already registered");
        return false;
    }
    if (impl_->domains.size() >= impl_->cfg.max_domains) {
        impl_->emit(AlertKind::SchedulerMisuse, domain.id, 0, 0, 0, 0.0,
                    "domain registry is full");
        return false;
    }

    const std::uint64_t label = domain.label != 0
                                    ? domain.label
                                    : impl_->derive_label(domain.id);
    if (impl_->label_owner.contains(label)) {
        impl_->emit(AlertKind::TagConflict, domain.id,
                    impl_->label_owner.at(label), 0, 0, 0.0,
                    "predictor label is already owned by another domain");
        return false;
    }

    SecurityDomain stored = domain;
    stored.label = label;
    Impl::DomainRecord rec;
    rec.domain = stored;
    rec.label = label;
    rec.registration_nonce = impl_->next_domain_nonce++;
    impl_->domains.emplace(domain.id, std::move(rec));
    impl_->label_owner.emplace(label, domain.id);
    return true;
}

bool PredictorIsolator::unregister_domain(DomainId domain) {
    std::lock_guard lock(impl_->mutex);
    const auto rec_it = impl_->domains.find(domain);
    if (rec_it == impl_->domains.end()) {
        return false;
    }
    for (auto& [cpu, cpu_bank] : impl_->cpus) {
        (void)cpu;
        cpu_bank.partitions.erase(domain);
        if (cpu_bank.resident.has_value() &&
            *cpu_bank.resident == domain) {
            cpu_bank.resident.reset();
        }
        for (auto& frame : cpu_bank.interrupt_stack) {
            if (frame.domain.has_value() && *frame.domain == domain) {
                frame.domain.reset();
            }
        }
    }
    impl_->label_owner.erase(rec_it->second.label);
    impl_->domains.erase(rec_it);
    return true;
}

bool PredictorIsolator::is_registered(DomainId domain) const {
    std::lock_guard lock(impl_->mutex);
    return impl_->domains.contains(domain);
}

std::optional<SecurityDomain> PredictorIsolator::domain(DomainId id) const {
    std::lock_guard lock(impl_->mutex);
    const Impl::DomainRecord* rec = impl_->record(id);
    if (rec == nullptr) {
        return std::nullopt;
    }
    return rec->domain;
}

SwitchReport PredictorIsolator::context_switch(DomainId next, CpuId cpu) {
    std::lock_guard lock(impl_->mutex);
    return impl_->context_switch_locked(next, cpu);
}

SwitchReport PredictorIsolator::enter_interrupt(DomainId handler, CpuId cpu) {
    std::lock_guard lock(impl_->mutex);
    return impl_->enter_interrupt_locked(handler, cpu);
}

SwitchReport PredictorIsolator::return_from_interrupt(CpuId cpu) {
    std::lock_guard lock(impl_->mutex);
    return impl_->return_from_interrupt_locked(cpu);
}

SwitchReport PredictorIsolator::migrate(DomainId domain, CpuId from_cpu,
                                        CpuId to_cpu) {
    std::lock_guard lock(impl_->mutex);
    return impl_->migrate_locked(domain, from_cpu, to_cpu);
}

Prediction PredictorIsolator::predict(DomainId domain, Pc pc, CpuId cpu) {
    std::lock_guard lock(impl_->mutex);
    return impl_->predict_locked(domain, pc, cpu, std::nullopt);
}

Prediction PredictorIsolator::predict(const SecurityDomain& domain, Pc pc,
                                      CpuId cpu) {
    std::lock_guard lock(impl_->mutex);
    return impl_->predict_locked(domain.id, pc, cpu, domain.label);
}

Prediction PredictorIsolator::predict_current(Pc pc, CpuId cpu) {
    std::lock_guard lock(impl_->mutex);
    const Impl::CpuBank* cpu_bank = impl_->find_bank(cpu);
    if (cpu_bank == nullptr || !cpu_bank->resident.has_value()) {
        Prediction rejected;
        rejected.rejected = true;
        impl_->emit(AlertKind::SchedulerMisuse, 0, 0, cpu, pc, 0.0,
                    "prediction requested without an active domain");
        return rejected;
    }
    return impl_->predict_locked(*cpu_bank->resident, pc, cpu, std::nullopt);
}

TrainResult PredictorIsolator::train(DomainId domain, Pc pc, bool taken,
                                     CpuId cpu) {
    std::lock_guard lock(impl_->mutex);
    return impl_->train_locked(domain, pc, taken, cpu, std::nullopt);
}

TrainResult PredictorIsolator::train(const SecurityDomain& domain, Pc pc,
                                     bool taken, CpuId cpu) {
    std::lock_guard lock(impl_->mutex);
    return impl_->train_locked(domain.id, pc, taken, cpu, domain.label);
}

TrainResult PredictorIsolator::train_current(Pc pc, bool taken, CpuId cpu) {
    std::lock_guard lock(impl_->mutex);
    const Impl::CpuBank* cpu_bank = impl_->find_bank(cpu);
    if (cpu_bank == nullptr || !cpu_bank->resident.has_value()) {
        TrainResult result;
        result.status = Status::NotResident;
        impl_->emit(AlertKind::SchedulerMisuse, 0, 0, cpu, pc, 0.0,
                    "training requested without an active domain");
        return result;
    }
    return impl_->train_locked(*cpu_bank->resident, pc, taken, cpu,
                               std::nullopt);
}

std::optional<DomainId> PredictorIsolator::active_domain(CpuId cpu) const {
    std::lock_guard lock(impl_->mutex);
    const Impl::CpuBank* cpu_bank = impl_->find_bank(cpu);
    if (cpu_bank == nullptr || !cpu_bank->resident.has_value()) {
        return std::nullopt;
    }
    return cpu_bank->resident;
}

std::optional<DomainStats> PredictorIsolator::stats(DomainId domain) const {
    std::lock_guard lock(impl_->mutex);
    const Impl::DomainRecord* rec = impl_->record(domain);
    if (rec == nullptr) {
        return std::nullopt;
    }
    DomainStats stats;
    stats.domain = domain;
    stats.sensitive = rec->domain.sensitive;
    stats.trainings = rec->trainings;
    stats.predictions = rec->predictions;
    stats.hits = rec->hits;
    stats.misses = rec->misses;
    stats.correct = rec->correct;
    stats.mispredicts = rec->mispredicts;
    stats.cross_domain_attempts = rec->cross_domain_attempts;
    stats.flushes = rec->flushes;
    stats.migrations = rec->migrations;
    stats.anomaly_episodes = rec->anomaly_episodes;

    const Impl::DomainState* preferred = impl_->find_state(0, domain);
    if (preferred == nullptr) {
        for (const auto& [cpu, cpu_bank] : impl_->cpus) {
            (void)cpu;
            const auto it = cpu_bank.partitions.find(domain);
            if (it != cpu_bank.partitions.end()) {
                preferred = &it->second;
                break;
            }
        }
    }
    if (preferred != nullptr) {
        stats.epoch = preferred->epoch;
        stats.window_samples = preferred->recent_mispredicts.size();
        stats.mispredict_rate = impl_->window_rate(*preferred);
    }
    return stats;
}

std::optional<DomainStats> PredictorIsolator::stats(DomainId domain,
                                                    CpuId cpu) const {
    std::lock_guard lock(impl_->mutex);
    const Impl::DomainRecord* rec = impl_->record(domain);
    if (rec == nullptr) {
        return std::nullopt;
    }
    DomainStats stats;
    stats.domain = domain;
    stats.sensitive = rec->domain.sensitive;
    stats.trainings = rec->trainings;
    stats.predictions = rec->predictions;
    stats.hits = rec->hits;
    stats.misses = rec->misses;
    stats.correct = rec->correct;
    stats.mispredicts = rec->mispredicts;
    stats.cross_domain_attempts = rec->cross_domain_attempts;
    stats.flushes = rec->flushes;
    stats.migrations = rec->migrations;
    stats.anomaly_episodes = rec->anomaly_episodes;
    if (const Impl::DomainState* state = impl_->find_state(cpu, domain);
        state != nullptr) {
        stats.epoch = state->epoch;
        stats.window_samples = state->recent_mispredicts.size();
        stats.mispredict_rate = impl_->window_rate(*state);
    }
    return stats;
}

std::optional<PredictorTag> PredictorIsolator::tag_for(DomainId domain,
                                                       CpuId cpu) const {
    std::lock_guard lock(impl_->mutex);
    const Impl::DomainRecord* rec = impl_->record(domain);
    if (rec == nullptr) {
        return std::nullopt;
    }
    const Impl::DomainState* state = impl_->find_state(cpu, domain);
    const Epoch epoch = state != nullptr ? state->epoch : 0;
    return impl_->make_tag(*rec, epoch, cpu);
}

std::size_t PredictorIsolator::flush_domain(DomainId domain) {
    std::lock_guard lock(impl_->mutex);
    Impl::DomainRecord* rec = impl_->record(domain);
    if (rec == nullptr) {
        return 0;
    }
    std::size_t count = 0;
    for (auto& [cpu, cpu_bank] : impl_->cpus) {
        (void)cpu;
        const auto it = cpu_bank.partitions.find(domain);
        if (it != cpu_bank.partitions.end()) {
            impl_->wipe(it->second, *rec);
            ++count;
        }
    }
    return count;
}

std::size_t PredictorIsolator::flush_cpu(CpuId cpu) {
    std::lock_guard lock(impl_->mutex);
    const auto cpu_it = impl_->cpus.find(cpu);
    if (cpu_it == impl_->cpus.end()) {
        return 0;
    }
    std::size_t count = 0;
    for (auto& [domain, state] : cpu_it->second.partitions) {
        if (Impl::DomainRecord* rec = impl_->record(domain);
            rec != nullptr) {
            impl_->wipe(state, *rec);
            ++count;
        }
    }
    return count;
}

std::size_t PredictorIsolator::flush_all() {
    std::lock_guard lock(impl_->mutex);
    std::size_t count = 0;
    for (auto& [cpu, cpu_bank] : impl_->cpus) {
        (void)cpu;
        for (auto& [domain, state] : cpu_bank.partitions) {
            if (Impl::DomainRecord* rec = impl_->record(domain);
                rec != nullptr) {
                impl_->wipe(state, *rec);
                ++count;
            }
        }
    }
    return count;
}

std::optional<PredictorSnapshot> PredictorIsolator::export_snapshot(
    DomainId domain, CpuId cpu) const {
    std::lock_guard lock(impl_->mutex);
    const Impl::DomainRecord* rec = impl_->record(domain);
    const Impl::DomainState* state = impl_->find_state(cpu, domain);
    if (rec == nullptr || state == nullptr) {
        return std::nullopt;
    }

    PredictorSnapshot snapshot;
    snapshot.domain = rec->domain;
    snapshot.cpu = cpu;
    snapshot.epoch = state->epoch;
    snapshot.tag = impl_->make_tag(*rec, state->epoch, cpu);
    snapshot.accuracy_samples = state->recent_mispredicts.size();
    snapshot.correct = rec->correct;
    snapshot.mispredicts = rec->mispredicts;
    for (const Impl::Entry& entry : state->table) {
        if (entry.valid && entry.tag == snapshot.tag) {
            snapshot.entries.emplace_back(entry.pc, entry.counter);
        }
    }
    return snapshot;
}

ImportResult PredictorIsolator::import_snapshot(const PredictorSnapshot& snapshot,
                                                CpuId cpu,
                                                bool allow_migration) {
    std::lock_guard lock(impl_->mutex);
    ImportResult result;
    Impl::DomainRecord* rec = impl_->record(snapshot.domain.id);
    if (rec == nullptr) {
        result.status = Status::UnknownDomain;
        impl_->emit(AlertKind::StateImportRejected, snapshot.domain.id, 0, cpu,
                    0, 0.0, "snapshot domain is not registered");
        return result;
    }
    if (snapshot.domain.label != 0 && snapshot.domain.label != rec->label) {
        impl_->emit(AlertKind::StateImportRejected, snapshot.domain.id, 0, cpu,
                    0, 0.0, "snapshot label does not match the registered domain");
        result.status = Status::ImportRejected;
        return result;
    }
    const PredictorTag expected = impl_->make_tag(*rec, snapshot.epoch,
                                                  snapshot.cpu);
    if (snapshot.tag != expected || snapshot.tag.domain != rec->domain.id ||
        snapshot.tag.label != rec->label ||
        snapshot.tag.sensitive != rec->domain.sensitive) {
        impl_->emit(AlertKind::StateImportRejected, snapshot.domain.id,
                    snapshot.tag.domain, cpu, 0, 0.0,
                    "snapshot tag is not bound to this domain");
        result.status = Status::TagMismatch;
        return result;
    }
    if (snapshot.cpu != cpu && !allow_migration) {
        impl_->emit(AlertKind::StateImportRejected, snapshot.domain.id, 0, cpu,
                    0, 0.0, "cross-CPU import requires migration permission");
        result.status = Status::ImportRejected;
        return result;
    }

    Impl::CpuBank& cpu_bank = impl_->bank(cpu);
    bool takeover = false;
    if (cpu_bank.resident.has_value() && *cpu_bank.resident != snapshot.domain.id) {
        if (!allow_migration || snapshot.cpu == cpu) {
            impl_->emit(AlertKind::StateImportRejected, snapshot.domain.id,
                        *cpu_bank.resident, cpu, 0, 0.0,
                        "cannot import state while another domain is resident");
            result.status = Status::CrossDomainViolation;
            return result;
        }
        takeover = true;
        if (Impl::DomainRecord* old = impl_->record(*cpu_bank.resident);
            old != nullptr && impl_->should_wipe_on_switch(*old)) {
            auto old_state = cpu_bank.partitions.find(*cpu_bank.resident);
            if (old_state != cpu_bank.partitions.end()) {
                impl_->wipe(old_state->second, *old);
                if (old->domain.sensitive) {
                    impl_->emit(AlertKind::SensitiveStateWiped,
                                *cpu_bank.resident, snapshot.domain.id, cpu, 0,
                                0.0,
                                "sensitive target-CPU partition wiped on import");
                }
            }
        }
    }

    Impl::DomainState& state = impl_->state_for(cpu, snapshot.domain.id);
    const Epoch current_epoch = state.epoch;
    impl_->wipe(state, *rec);
    state.epoch = std::max(current_epoch + 1, snapshot.epoch + 1);
    const bool wipe_import =
        snapshot.cpu != cpu && snapshot.domain.sensitive &&
        impl_->cfg.wipe_sensitive_on_cross_domain;
    if (!wipe_import) {
        const PredictorTag tag = impl_->make_tag(*rec, state.epoch, cpu);
        for (const auto& [pc, raw_counter] : snapshot.entries) {
            Impl::Entry entry;
            entry.pc = pc;
            entry.tag = tag;
            entry.counter = static_cast<std::uint8_t>(
                std::clamp<unsigned>(raw_counter, 0, 3));
            entry.valid = true;
            state.table[Impl::index_for(pc, state.table.size())] = entry;
        }
    } else {
        result.flushed = true;
        impl_->emit(AlertKind::SensitiveStateWiped, snapshot.domain.id, 0, cpu,
                    0, 0.0,
                    "sensitive snapshot wiped during cross-CPU import");
    }

    if (!cpu_bank.resident.has_value() || takeover) {
        cpu_bank.resident = snapshot.domain.id;
    }
    result.accepted = true;
    result.status = Status::Ok;
    result.epoch = state.epoch;
    return result;
}

std::vector<Alert> PredictorIsolator::alerts() const {
    std::lock_guard lock(impl_->mutex);
    return {impl_->alert_log.begin(), impl_->alert_log.end()};
}

std::vector<Alert> PredictorIsolator::take_alerts() {
    std::lock_guard lock(impl_->mutex);
    std::vector<Alert> out(impl_->alert_log.begin(), impl_->alert_log.end());
    impl_->alert_log.clear();
    return out;
}

std::size_t PredictorIsolator::alert_count(AlertKind kind) const {
    std::lock_guard lock(impl_->mutex);
    return static_cast<std::size_t>(std::count_if(
        impl_->alert_log.begin(), impl_->alert_log.end(),
        [kind](const Alert& alert) { return alert.kind == kind; }));
}

std::optional<Alert> PredictorIsolator::last_alert() const {
    std::lock_guard lock(impl_->mutex);
    if (impl_->alert_log.empty()) {
        return std::nullopt;
    }
    return impl_->alert_log.back();
}

void PredictorIsolator::clear_alerts() {
    std::lock_guard lock(impl_->mutex);
    impl_->alert_log.clear();
}

const IsolatorConfig& PredictorIsolator::config() const { return impl_->cfg; }

}  // namespace netsec
