#include "netsec.hpp"

#include <limits>
#include <stdexcept>

namespace netsec {
namespace {

struct ByteRange {
    std::uint64_t first;
    std::uint64_t last;
    bool valid;
};

constexpr std::uint64_t allOnes =
    std::numeric_limits<std::uint64_t>::max();

ByteRange accessRange(const PmpAccess& access) noexcept {
    if (access.size == 0 || access.size - 1 > allOnes - access.address) {
        return {0, 0, false};
    }
    return {access.address, access.address + access.size - 1, true};
}

std::uint64_t trailingOnes(std::uint64_t value) noexcept {
    std::uint64_t count = 0;
    while ((value & std::uint64_t{1}) != 0) {
        ++count;
        value >>= 1;
    }
    return count;
}

// Correctly shifts a pmpaddr base by two bits. Values beyond the
// representable physical address space saturate at the address limit.
std::uint64_t encodedBase(std::uint64_t encoded) noexcept {
    if (encoded > (allOnes >> 2)) {
        return allOnes;
    }
    return encoded << 2;
}

ByteRange decodeEntry(const PmpEvaluator::EntryArray& entries,
                      std::size_t index) noexcept {
    const PmpEntry& current = entries[index];

    switch (current.mode) {
    case PmpMode::Off:
        return {0, 0, false};

    case PmpMode::Tor: {
        const std::uint64_t lowerEncoded =
            index == 0 ? 0 : entries[index - 1].pmpaddr;
        if (current.pmpaddr <= lowerEncoded) {
            return {0, 0, false};
        }

        const std::uint64_t first = encodedBase(lowerEncoded);
        if (current.pmpaddr > (allOnes >> 2)) {
            return {first, allOnes, first <= allOnes};
        }
        const std::uint64_t upper = current.pmpaddr << 2;
        if (upper == 0 || first >= upper) {
            return {0, 0, false};
        }
        return {first, upper - 1, true};
    }

    case PmpMode::Na4: {
        if (current.pmpaddr > (allOnes >> 2)) {
            return {0, 0, false};
        }
        const std::uint64_t first = current.pmpaddr << 2;
        return {first, first + 3, true};
    }

    case PmpMode::Napot: {
        const std::uint64_t ones = trailingOnes(current.pmpaddr);

        // A region of 2^64 bytes or larger covers the complete 64-bit
        // address space. This includes the all-ones encoding.
        if (ones >= 61) {
            return {0, allOnes, true};
        }

        const std::uint64_t blockBits = ones + 1;
        const std::uint64_t blockMask =
            (std::uint64_t{1} << blockBits) - 1;
        const std::uint64_t first =
            encodedBase(current.pmpaddr & ~blockMask);
        const std::uint64_t size = std::uint64_t{1} << (ones + 3);
        return {first, first + size - 1, true};
    }
    }

    return {0, 0, false};
}

bool entryAppliesToPrivilege(const PmpEntry& entry,
                             PrivilegeMode privilege) noexcept {
    return privilege != PrivilegeMode::M || entry.locked;
}

bool permits(const PmpEntry& entry, AccessType type) noexcept {
    switch (type) {
    case AccessType::Read:
        return entry.read;
    case AccessType::Write:
        return entry.write;
    case AccessType::Execute:
        return entry.execute;
    }
    return false;
}

}  // namespace

PmpEvaluator::PmpEvaluator(const EntryArray& entries) : entries_(entries) {}

void PmpEvaluator::setEntry(std::size_t index, const PmpEntry& entry) {
    if (index >= entries_.size()) {
        throw std::out_of_range("PMP entry index must be in [0, 15]");
    }
    entries_[index] = entry;
}

const PmpEntry& PmpEvaluator::entry(std::size_t index) const {
    return entries_.at(index);
}

const PmpEvaluator::EntryArray& PmpEvaluator::entries() const noexcept {
    return entries_;
}

PmpDecision PmpEvaluator::evaluate(const PmpAccess& access) const noexcept {
    return evaluatePmpAccess(entries_, access);
}

PmpDecision PmpEvaluator::evaluate(std::uint64_t address, std::uint64_t size,
                                   PrivilegeMode privilege,
                                   AccessType type) const noexcept {
    return evaluate(PmpAccess{address, size, privilege, type});
}

bool PmpEvaluator::allows(const PmpAccess& access) const noexcept {
    return evaluate(access) == PmpDecision::Allow;
}

PmpDecision evaluatePmpAccess(const PmpEvaluator::EntryArray& entries,
                              const PmpAccess& access) noexcept {
    const ByteRange requested = accessRange(access);
    if (!requested.valid) {
        return PmpDecision::Deny;
    }

    for (std::size_t index = 0; index < entries.size(); ++index) {
        const PmpEntry& candidate = entries[index];
        if (!entryAppliesToPrivilege(candidate, access.privilege)) {
            continue;
        }

        const ByteRange region = decodeEntry(entries, index);
        if (!region.valid ||
            region.last < requested.first ||
            region.first > requested.last) {
            continue;
        }

        const bool coversAll = region.first <= requested.first &&
                               region.last >= requested.last;
        if (!coversAll) {
            return PmpDecision::Deny;
        }

        return permits(candidate, access.type) ? PmpDecision::Allow
                                               : PmpDecision::Deny;
    }

    return access.privilege == PrivilegeMode::M ? PmpDecision::Allow
                                                : PmpDecision::Deny;
}

}  // namespace netsec
