#pragma once

#include <cstddef>
#include <cstdint>
#include <memory>
#include <vector>

namespace netsec {

using ThreadId = std::uint64_t;
using SyncId = std::uint64_t;
using Address = std::uintptr_t;
using Generation = std::uint64_t;
using Sequence = std::uint64_t;
using VectorClock = std::vector<std::uint64_t>;

// An allocation token. Address-only operations act on the current generation
// of an address, while token operations are pinned to this exact generation.
struct Allocation {
    Address address = 0;
    Generation generation = 0;
    std::size_t size = 0;
    Sequence allocation_sequence = 0;

    explicit operator bool() const noexcept {
        return generation != 0;
    }
};

enum class ReportKind {
    // free happens-before access: an access after free in the happens-before
    // order, for example through a release/acquire pair.
    OrderedUseAfterFree,

    // free and access have no happens-before order in either direction.
    ConcurrentFreeAccess,
};

struct Report {
    ReportKind kind = ReportKind::ConcurrentFreeAccess;
    Address address = 0;
    Generation generation = 0;
    std::size_t size = 0;

    ThreadId free_thread = 0;
    ThreadId access_thread = 0;
    Sequence allocation_sequence = 0;
    Sequence free_sequence = 0;
    Sequence access_sequence = 0;

    VectorClock free_clock;
    VectorClock access_clock;
};

const char* report_kind_name(ReportKind kind) noexcept;

// A small, embeddable detector for a runtime allocator. Every operation
// consumes one event from the supplied thread. Threads are identified by
// caller-controlled integer ids, so no TLS or dependency on std::thread is
// required.
//
// The detector is internally synchronized and can therefore be fed by several
// runtime threads. For deterministic traces, callers can also feed events
// serially in a chosen order.
class FreeAccessDetector {
public:
    FreeAccessDetector();
    ~FreeAccessDetector();

    FreeAccessDetector(const FreeAccessDetector&) = delete;
    FreeAccessDetector& operator=(const FreeAccessDetector&) = delete;

    // alloc/free/access are the allocator events being checked.
    Allocation alloc(ThreadId thread, Address address,
                     std::size_t size = 0);
    // A bool result means the event matched a retained allocation generation;
    // it is an event-accounting result, not a safety verdict.
    bool free(ThreadId thread, Address address);
    bool free(ThreadId thread, const Allocation& allocation);
    bool access(ThreadId thread, Address address);
    bool access(ThreadId thread, const Allocation& allocation);

    // release publishes the releasing thread's vector clock to sync_id;
    // acquire merges that clock into the acquiring thread. Reusing the same
    // sync_id for unrelated synchronization objects is not supported.
    void release(ThreadId thread, SyncId sync_id);
    void acquire(ThreadId thread, SyncId sync_id);

    // Reports are returned in detection order. An access that happens-before
    // a free is not a report. A concurrent access/free pair is reported once
    // when the second event is observed.
    std::vector<Report> reports() const;
    std::size_t report_count() const;
    std::size_t report_count(ReportKind kind) const;
    void clear_reports();

    // Forget all allocations, clocks, synchronization edges and reports.
    void reset();

private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
};

using Detector = FreeAccessDetector;

}  // namespace netsec
