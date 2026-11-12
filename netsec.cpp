#include "netsec.hpp"

#include <cstdint>

namespace netsec {

const char* to_string(ViolationKind kind) {
    switch (kind) {
        case ViolationKind::SlotOverwritten: return "SlotOverwritten";
        case ViolationKind::PrematureReuse: return "PrematureReuse";
        case ViolationKind::DanglingSelfPointer: return "DanglingSelfPointer";
        case ViolationKind::IllegalMove: return "IllegalMove";
    }
    return "Unknown";
}

std::uint64_t PinRegistry::checksum(const void* data, std::size_t n) {
    // FNV-1a over the slot bytes; any overwrite of the pinned slot changes it.
    const auto* p = static_cast<const unsigned char*>(data);
    std::uint64_t h = 14695981039346656037ull;
    for (std::size_t i = 0; i < n; ++i) {
        h ^= p[i];
        h *= 1099511628211ull;
    }
    return h;
}

PinRegistry::Entry* PinRegistry::find(const void* slot) {
    for (auto& e : entries_) {
        if (e.slot == slot) return &e;
    }
    return nullptr;
}

const PinRegistry::Entry* PinRegistry::find(const void* slot) const {
    for (const auto& e : entries_) {
        if (e.slot == slot) return &e;
    }
    return nullptr;
}

void PinRegistry::report(ViolationKind kind, const Entry& entry,
                         std::string detail) {
    violations_.push_back(
        Violation{kind, entry.slot, entry.name, std::move(detail),
                  next_sequence_++});
}

void PinRegistry::pin(const void* slot, std::size_t size,
                      const void* const* self_ptr, std::string name,
                      bool movable) {
    if (Entry* existing = find(slot)) {
        report(ViolationKind::PrematureReuse, *existing,
               "slot re-pinned while still registered (previous object not "
               "destroyed)");
        *existing = Entry{slot, size, self_ptr, std::move(name), movable,
                          false, checksum(slot, size)};
        return;
    }
    entries_.push_back(Entry{slot, size, self_ptr, std::move(name), movable,
                             false, checksum(slot, size)});
}

void PinRegistry::unpin(const void* slot) {
    for (auto it = entries_.begin(); it != entries_.end(); ++it) {
        if (it->slot == slot) {
            if (!it->destructed) {
                report(ViolationKind::PrematureReuse, *it,
                       "deregistered before destruction completed");
            }
            entries_.erase(it);
            return;
        }
    }
}

void PinRegistry::notify_move(const void* from, const void* to) {
    Entry* entry = find(from);
    if (entry == nullptr) return;
    if (Entry* target = find(to)) {
        report(ViolationKind::SlotOverwritten, *target,
               "move destination overlaps another pinned slot");
    }
    if (!entry->movable) {
        report(ViolationKind::IllegalMove, *entry,
               "object of non-movable type was moved");
        return;
    }
    // Relocatable object: migrate the registration. The self-pointer field
    // shifts by the same delta; the object is expected to have fixed up its
    // self pointer by now, so the moved-to bytes become the new baseline.
    const auto delta = static_cast<const char*>(to) -
                       static_cast<const char*>(from);
    entry->slot = to;
    entry->self_ptr = reinterpret_cast<const void* const*>(
        reinterpret_cast<const char*>(entry->self_ptr) + delta);
    entry->checksum = checksum(to, entry->size);
}

void PinRegistry::notify_write(const void* addr, std::size_t n) {
    const auto* begin = static_cast<const char*>(addr);
    const char* end = begin + n;
    for (auto& e : entries_) {
        const auto* slot_begin = static_cast<const char*>(e.slot);
        const char* slot_end = slot_begin + e.size;
        if (begin < slot_end && end > slot_begin) {
            report(ViolationKind::SlotOverwritten, e,
                   "write of " + std::to_string(n) +
                       " bytes overlaps pinned slot");
        }
    }
}

void PinRegistry::notify_destruct_begin(const void*) {}

void PinRegistry::notify_destruct_end(const void* slot) {
    if (Entry* e = find(slot)) e->destructed = true;
}

void PinRegistry::notify_free(const void* addr) {
    for (auto it = entries_.begin(); it != entries_.end(); ++it) {
        if (it->slot == addr) {
            if (!it->destructed) {
                report(ViolationKind::PrematureReuse, *it,
                       "slot freed before destruction completed");
            }
            entries_.erase(it);
            return;
        }
    }
}

void PinRegistry::notify_reuse(const void* addr) {
    if (Entry* e = find(addr)) {
        if (!e->destructed) {
            report(ViolationKind::PrematureReuse, *e,
                   "slot reused for a new object before destruction "
                   "completed");
        }
    }
}

bool PinRegistry::check(const void* slot) {
    Entry* e = find(slot);
    if (e == nullptr) return true;
    if (e->destructed) return true;  // invariants hold until destruction
    bool clean = true;
    if (checksum(e->slot, e->size) != e->checksum) {
        report(ViolationKind::SlotOverwritten, *e,
               "slot bytes changed since registration");
        clean = false;
    }
    if (*e->self_ptr != e->slot) {
        report(ViolationKind::DanglingSelfPointer, *e,
               "self-referential pointer does not equal the registered slot "
               "address");
        clean = false;
    }
    return clean;
}

bool PinRegistry::check_all() {
    bool clean = true;
    // Index-based loop: check() may append violations but never entries.
    for (std::size_t i = 0; i < entries_.size(); ++i) {
        if (!check(entries_[i].slot)) clean = false;
    }
    return clean;
}

bool PinRegistry::is_pinned(const void* slot) const {
    return find(slot) != nullptr;
}

const Violation* PinRegistry::first_violation() const {
    return violations_.empty() ? nullptr : &violations_.front();
}

}  // namespace netsec
