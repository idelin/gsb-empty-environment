#pragma once

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

namespace netsec {

enum class ViolationKind {
    // A pinned slot's bytes were overwritten (memcpy / swap / assignment).
    SlotOverwritten,
    // A pinned slot was freed, reused, or deregistered before its
    // destruction completed.
    PrematureReuse,
    // The object's internal self-referential pointer no longer equals the
    // registered slot address.
    DanglingSelfPointer,
    // An object not registered as movable was moved.
    IllegalMove,
};

const char* to_string(ViolationKind kind);

struct Violation {
    ViolationKind kind;
    const void* slot;
    std::string object_name;
    std::string detail;
    std::uint64_t sequence;  // monotonically increasing event order
};

// Scheduler-facing registry for pinned-object invariants. The simulated
// runtime calls the notify_* hooks around object lifecycle events and calls
// check()/check_all() at poll/await boundaries.
class PinRegistry {
public:
    // Registers [slot, slot+size) as pinned. self_ptr is the address of the
    // object's internal self-referential pointer field; *self_ptr must equal
    // slot at every check. movable=true registers the object as relocatable.
    void pin(const void* slot, std::size_t size, const void* const* self_ptr,
             std::string name, bool movable = false);

    // Deregisters a slot. Only legal after notify_destruct_end.
    void unpin(const void* slot);

    // Lifecycle / memory hooks invoked by the scheduler.
    void notify_move(const void* from, const void* to);
    void notify_write(const void* addr, std::size_t n);  // memcpy/swap/assign
    void notify_destruct_begin(const void* slot);
    void notify_destruct_end(const void* slot);
    void notify_free(const void* addr);
    void notify_reuse(const void* addr);

    // Invariant checks. Return true when no (new) violation was found.
    bool check(const void* slot);
    bool check_all();

    bool is_pinned(const void* slot) const;
    const std::vector<Violation>& violations() const { return violations_; }
    const Violation* first_violation() const;

private:
    struct Entry {
        const void* slot;
        std::size_t size;
        const void* const* self_ptr;
        std::string name;
        bool movable;
        bool destructed = false;
        std::uint64_t checksum;
    };

    Entry* find(const void* slot);
    const Entry* find(const void* slot) const;
    void report(ViolationKind kind, const Entry& entry, std::string detail);
    static std::uint64_t checksum(const void* data, std::size_t n);

    std::vector<Entry> entries_;
    std::vector<Violation> violations_;
    std::uint64_t next_sequence_ = 1;
};

}  // namespace netsec
