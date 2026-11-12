#include "netsec.hpp"

#include <algorithm>
#include <mutex>
#include <unordered_map>
#include <utility>

namespace netsec {
namespace {

constexpr std::size_t kRetainedGenerationsPerAddress = 32;

bool happens_before(const VectorClock& lhs, const VectorClock& rhs) noexcept {
    const std::size_t width = std::max(lhs.size(), rhs.size());
    bool strictly_less = false;
    for (std::size_t index = 0; index < width; ++index) {
        const std::uint64_t left = index < lhs.size() ? lhs[index] : 0;
        const std::uint64_t right = index < rhs.size() ? rhs[index] : 0;
        if (left > right) {
            return false;
        }
        if (left < right) {
            strictly_less = true;
        }
    }
    return strictly_less;
}

void merge_clock(VectorClock& target, const VectorClock& source) {
    if (target.size() < source.size()) {
        target.resize(source.size(), 0);
    }
    for (std::size_t index = 0; index < source.size(); ++index) {
        target[index] = std::max(target[index], source[index]);
    }
}

}  // namespace

struct FreeAccessDetector::Impl {
    struct AccessRecord {
        ThreadId thread = 0;
        Sequence sequence = 0;
        VectorClock clock;
    };

    struct GenerationRecord {
        Allocation allocation;
        VectorClock allocation_clock;

        bool freed = false;
        bool retired = false;
        ThreadId free_thread = 0;
        Sequence free_sequence = 0;
        VectorClock free_clock;

        // Accesses seen while the generation is live. They are classified
        // when free arrives. Once a generation is freed, later accesses are
        // classified immediately and no access list is retained.
        std::vector<AccessRecord> pending_accesses;
    };

    struct AddressRecord {
        Generation next_generation = 1;
        Generation current_generation = 0;
        std::unordered_map<Generation, GenerationRecord> generations;
    };

    mutable std::mutex mutex;

    std::unordered_map<ThreadId, std::size_t> thread_slots;
    std::vector<VectorClock> thread_clocks;
    std::unordered_map<SyncId, VectorClock> release_clocks;
    std::unordered_map<Address, AddressRecord> addresses;

    Sequence next_sequence = 1;
    std::vector<Report> reports;

    std::size_t ensure_thread(ThreadId thread) {
        const auto found = thread_slots.find(thread);
        if (found != thread_slots.end()) {
            return found->second;
        }

        const std::size_t slot = thread_clocks.size();
        thread_slots.emplace(thread, slot);
        for (auto& clock : thread_clocks) {
            clock.resize(slot + 1, 0);
        }
        thread_clocks.emplace_back(slot + 1, 0);
        return slot;
    }

    Sequence advance(ThreadId thread) {
        const std::size_t slot = ensure_thread(thread);
        ++thread_clocks[slot][slot];
        return next_sequence++;
    }

    void add_report(ReportKind kind, const GenerationRecord& generation,
                    const AccessRecord& access) {
        Report report;
        report.kind = kind;
        report.address = generation.allocation.address;
        report.generation = generation.allocation.generation;
        report.size = generation.allocation.size;
        report.free_thread = generation.free_thread;
        report.access_thread = access.thread;
        report.allocation_sequence =
            generation.allocation.allocation_sequence;
        report.free_sequence = generation.free_sequence;
        report.access_sequence = access.sequence;
        report.free_clock = generation.free_clock;
        report.access_clock = access.clock;
        reports.push_back(std::move(report));
    }

    // Compare an access with a generation's already-recorded free event.
    // An access ordered before free is legal. A free ordered before the
    // access is an ordered use-after-free. If neither direction is ordered,
    // the two events form a concurrent free/access window.
    void classify_after_free(const GenerationRecord& generation,
                             const AccessRecord& access) {
        const bool access_before_free =
            happens_before(access.clock, generation.free_clock);
        const bool free_before_access =
            happens_before(generation.free_clock, access.clock);

        if (free_before_access) {
            add_report(ReportKind::OrderedUseAfterFree, generation, access);
        } else if (!access_before_free) {
            add_report(ReportKind::ConcurrentFreeAccess, generation, access);
        }
    }

    GenerationRecord* find_generation(Address address, Generation generation) {
        const auto address_it = addresses.find(address);
        if (address_it == addresses.end()) {
            return nullptr;
        }
        const auto generation_it =
            address_it->second.generations.find(generation);
        if (generation_it == address_it->second.generations.end()) {
            return nullptr;
        }
        return &generation_it->second;
    }

    void prune_generations(AddressRecord& record) {
        while (record.generations.size() > kRetainedGenerationsPerAddress) {
            Generation oldest = record.current_generation;
            const GenerationRecord* oldest_record = nullptr;
            for (const auto& entry : record.generations) {
                if (entry.first == record.current_generation) {
                    continue;
                }
                if (oldest_record == nullptr || entry.first < oldest) {
                    oldest = entry.first;
                    oldest_record = &entry.second;
                }
            }
            if (oldest_record == nullptr) {
                break;
            }
            record.generations.erase(oldest);
        }
    }

    void mark_freed(ThreadId thread, Sequence sequence,
                    const VectorClock& clock,
                    GenerationRecord& generation) {
        generation.freed = true;
        generation.free_thread = thread;
        generation.free_sequence = sequence;
        generation.free_clock = clock;

        for (const auto& access : generation.pending_accesses) {
            classify_after_free(generation, access);
        }
        generation.pending_accesses.clear();
    }
};

const char* report_kind_name(ReportKind kind) noexcept {
    switch (kind) {
        case ReportKind::OrderedUseAfterFree:
            return "ordered-use-after-free";
        case ReportKind::ConcurrentFreeAccess:
            return "concurrent-free-access";
    }
    return "unknown";
}

FreeAccessDetector::FreeAccessDetector() : impl_(new Impl()) {}

FreeAccessDetector::~FreeAccessDetector() = default;

Allocation FreeAccessDetector::alloc(ThreadId thread, Address address,
                                     std::size_t size) {
    std::lock_guard<std::mutex> lock(impl_->mutex);

    const Sequence sequence = impl_->advance(thread);
    const VectorClock clock =
        impl_->thread_clocks[impl_->thread_slots.at(thread)];

    Impl::AddressRecord& address_record = impl_->addresses[address];
    if (address_record.current_generation != 0) {
        const auto previous = address_record.generations.find(
            address_record.current_generation);
        if (previous != address_record.generations.end() &&
            !previous->second.freed) {
            // The allocator handed out a live address again. The old
            // generation no longer owns this address; keep it isolated so a
            // stale token cannot be charged to the new object.
            previous->second.retired = true;
            previous->second.pending_accesses.clear();
        }
    }

    const Generation generation = address_record.next_generation++;
    Impl::GenerationRecord generation_record;
    generation_record.allocation.address = address;
    generation_record.allocation.generation = generation;
    generation_record.allocation.size = size;
    generation_record.allocation.allocation_sequence = sequence;
    generation_record.allocation_clock = clock;

    address_record.current_generation = generation;
    address_record.generations.emplace(generation,
                                       std::move(generation_record));
    impl_->prune_generations(address_record);

    return Allocation{address, generation, size, sequence};
}

bool FreeAccessDetector::free(ThreadId thread, Address address) {
    std::lock_guard<std::mutex> lock(impl_->mutex);

    const auto address_it = impl_->addresses.find(address);
    if (address_it == impl_->addresses.end() ||
        address_it->second.current_generation == 0) {
        return false;
    }

    const auto generation_it = address_it->second.generations.find(
        address_it->second.current_generation);
    if (generation_it == address_it->second.generations.end() ||
        generation_it->second.freed || generation_it->second.retired) {
        return false;
    }

    const Sequence sequence = impl_->advance(thread);
    const VectorClock& clock =
        impl_->thread_clocks[impl_->thread_slots.at(thread)];
    impl_->mark_freed(thread, sequence, clock, generation_it->second);
    return true;
}

bool FreeAccessDetector::free(ThreadId thread,
                              const Allocation& allocation) {
    std::lock_guard<std::mutex> lock(impl_->mutex);

    Impl::GenerationRecord* generation =
        impl_->find_generation(allocation.address, allocation.generation);
    if (generation == nullptr || generation->freed || generation->retired) {
        return false;
    }

    const Sequence sequence = impl_->advance(thread);
    const VectorClock& clock =
        impl_->thread_clocks[impl_->thread_slots.at(thread)];
    impl_->mark_freed(thread, sequence, clock, *generation);
    return true;
}

bool FreeAccessDetector::access(ThreadId thread, Address address) {
    std::lock_guard<std::mutex> lock(impl_->mutex);

    const auto address_it = impl_->addresses.find(address);
    if (address_it == impl_->addresses.end() ||
        address_it->second.current_generation == 0) {
        return false;
    }

    const auto generation_it = address_it->second.generations.find(
        address_it->second.current_generation);
    if (generation_it == address_it->second.generations.end() ||
        generation_it->second.retired) {
        return false;
    }

    const Sequence sequence = impl_->advance(thread);
    Impl::AccessRecord access;
    access.thread = thread;
    access.sequence = sequence;
    access.clock = impl_->thread_clocks[impl_->thread_slots.at(thread)];

    if (generation_it->second.freed) {
        impl_->classify_after_free(generation_it->second, access);
    } else {
        generation_it->second.pending_accesses.push_back(std::move(access));
    }
    return true;
}

bool FreeAccessDetector::access(ThreadId thread,
                                const Allocation& allocation) {
    std::lock_guard<std::mutex> lock(impl_->mutex);

    Impl::GenerationRecord* generation =
        impl_->find_generation(allocation.address, allocation.generation);
    if (generation == nullptr || generation->retired) {
        return false;
    }

    const Sequence sequence = impl_->advance(thread);
    Impl::AccessRecord access;
    access.thread = thread;
    access.sequence = sequence;
    access.clock = impl_->thread_clocks[impl_->thread_slots.at(thread)];

    if (generation->freed) {
        impl_->classify_after_free(*generation, access);
    } else {
        generation->pending_accesses.push_back(std::move(access));
    }
    return true;
}

void FreeAccessDetector::release(ThreadId thread, SyncId sync_id) {
    std::lock_guard<std::mutex> lock(impl_->mutex);

    impl_->advance(thread);
    // An acquire after several releases of the same synchronization object
    // observes all of them. Keep the union rather than overwriting earlier
    // release histories.
    merge_clock(impl_->release_clocks[sync_id],
                impl_->thread_clocks[impl_->thread_slots.at(thread)]);
}

void FreeAccessDetector::acquire(ThreadId thread, SyncId sync_id) {
    std::lock_guard<std::mutex> lock(impl_->mutex);

    impl_->advance(thread);
    const auto release_it = impl_->release_clocks.find(sync_id);
    if (release_it != impl_->release_clocks.end()) {
        merge_clock(impl_->thread_clocks[impl_->thread_slots.at(thread)],
                    release_it->second);
    }
}

std::vector<Report> FreeAccessDetector::reports() const {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    return impl_->reports;
}

std::size_t FreeAccessDetector::report_count() const {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    return impl_->reports.size();
}

std::size_t FreeAccessDetector::report_count(ReportKind kind) const {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    std::size_t count = 0;
    for (const auto& report : impl_->reports) {
        if (report.kind == kind) {
            ++count;
        }
    }
    return count;
}

void FreeAccessDetector::clear_reports() {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    impl_->reports.clear();
}

void FreeAccessDetector::reset() {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    impl_->thread_slots.clear();
    impl_->thread_clocks.clear();
    impl_->release_clocks.clear();
    impl_->addresses.clear();
    impl_->next_sequence = 1;
    impl_->reports.clear();
}

}  // namespace netsec
