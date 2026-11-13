#include "netsec.hpp"

#include <algorithm>
#include <utility>

namespace netsec {

std::optional<MemTableError> GuestMemoryMap::setRegions(std::vector<MemoryRegion> regions) {
    if (regions.size() > kMaxMemoryRegions) {
        return MemTableError::kTooManyRegions;
    }
    for (const auto& region : regions) {
        if (region.memory_size == 0) {
            return MemTableError::kZeroSizeRegion;
        }
        if (region.memory_size - 1 > UINT64_MAX - region.guest_phys_addr) {
            return MemTableError::kRegionWrapAround;
        }
    }
    std::sort(regions.begin(), regions.end(), [](const MemoryRegion& a, const MemoryRegion& b) {
        return a.guest_phys_addr < b.guest_phys_addr;
    });
    for (std::size_t i = 1; i < regions.size(); ++i) {
        const MemoryRegion& prev = regions[i - 1];
        if (regions[i].guest_phys_addr <= prev.guest_phys_addr + prev.memory_size - 1) {
            return MemTableError::kOverlappingRegions;
        }
    }
    regions_ = std::move(regions);
    ++generation_;
    for (auto& entry : cache_) {
        entry.valid = false;
    }
    return std::nullopt;
}

TranslateResult GuestMemoryMap::translate(std::uint64_t gpa, std::uint64_t len) const {
    if (len == 0) {
        return {0, TranslateError::kZeroLength};
    }
    if (len - 1 > UINT64_MAX - gpa) {
        return {0, TranslateError::kWrapAround};
    }
    const std::size_t slot = static_cast<std::size_t>(gpa % cache_.size());
    const CacheEntry& entry = cache_[slot];
    if (entry.valid && entry.generation == generation_ && entry.gpa == gpa && entry.len == len) {
        return {entry.host_addr, std::nullopt};
    }
    TranslateResult result = translateUncached(gpa, len);
    if (result.ok()) {
        cache_[slot] = CacheEntry{generation_, gpa, len, result.host_addr, true};
    }
    return result;
}

TranslateResult GuestMemoryMap::translateUncached(std::uint64_t gpa, std::uint64_t len) const {
    const std::uint64_t end = gpa + len - 1;  // inclusive; caller ruled out wrap
    for (const auto& region : regions_) {
        if (gpa < region.guest_phys_addr) {
            break;  // sorted: no later region can contain gpa
        }
        if (end <= region.guest_phys_addr + region.memory_size - 1) {
            return {region.userspace_addr + (gpa - region.guest_phys_addr), std::nullopt};
        }
    }
    return {0, TranslateError::kOutOfRegion};
}

ChainResult Virtqueue::walkChain(const VringDesc* desc_table, std::uint16_t head) const {
    ChainResult result;
    if (head >= queue_size_) {
        result.error = ChainError::kBadIndex;
        return result;
    }
    std::vector<bool> visited(queue_size_, false);
    std::uint64_t total_len = 0;
    std::uint32_t desc_count = 0;
    std::uint16_t index = head;

    auto fail = [&result](ChainError error) {
        result.error = error;
        result.segments.clear();
        return result;
    };

    while (true) {
        if (index >= queue_size_) {
            return fail(ChainError::kBadIndex);
        }
        if (visited[index]) {
            return fail(ChainError::kCycle);
        }
        visited[index] = true;
        if (++desc_count > queue_size_) {
            return fail(ChainError::kChainTooLong);
        }
        const VringDesc& desc = desc_table[index];

        if (desc.flags & kVringDescFIndirect) {
            const std::uint32_t max_table_len =
                static_cast<std::uint32_t>(queue_size_) * kVringDescSize;
            if (desc.len == 0 || desc.len % kVringDescSize != 0 || desc.len > max_table_len) {
                return fail(ChainError::kBadIndirectLength);
            }
            TranslateResult table = memory_.translate(desc.addr, desc.len);
            if (!table.ok()) {
                return fail(ChainError::kBadDescriptorAddress);
            }
            const auto* indirect =
                reinterpret_cast<const VringDesc*>(static_cast<std::uintptr_t>(table.host_addr));
            const std::uint32_t entries = desc.len / kVringDescSize;
            for (std::uint32_t i = 0; i < entries; ++i) {
                const VringDesc& inner = indirect[i];
                if (inner.flags & kVringDescFIndirect) {
                    return fail(ChainError::kNestedIndirect);
                }
                if (++desc_count > queue_size_) {
                    return fail(ChainError::kChainTooLong);
                }
                total_len += inner.len;
                if (total_len > kMaxChainTotalBytes) {
                    return fail(ChainError::kTotalLengthExceeded);
                }
                TranslateResult host = memory_.translate(inner.addr, inner.len);
                if (!host.ok()) {
                    return fail(ChainError::kBadDescriptorAddress);
                }
                result.segments.push_back(
                    {host.host_addr, inner.len, (inner.flags & kVringDescFWrite) != 0});
            }
        } else {
            total_len += desc.len;
            if (total_len > kMaxChainTotalBytes) {
                return fail(ChainError::kTotalLengthExceeded);
            }
            TranslateResult host = memory_.translate(desc.addr, desc.len);
            if (!host.ok()) {
                return fail(ChainError::kBadDescriptorAddress);
            }
            result.segments.push_back(
                {host.host_addr, desc.len, (desc.flags & kVringDescFWrite) != 0});
        }

        if (desc.flags & kVringDescFNext) {
            index = desc.next;
        } else {
            break;
        }
    }
    return result;
}

const char* toString(MemTableError error) {
    switch (error) {
        case MemTableError::kTooManyRegions: return "too many memory regions";
        case MemTableError::kZeroSizeRegion: return "zero-size memory region";
        case MemTableError::kRegionWrapAround: return "memory region wraps address space";
        case MemTableError::kOverlappingRegions: return "overlapping memory regions";
    }
    return "unknown";
}

const char* toString(TranslateError error) {
    switch (error) {
        case TranslateError::kZeroLength: return "zero-length translation";
        case TranslateError::kWrapAround: return "gpa+len wraps address space";
        case TranslateError::kOutOfRegion: return "range not contained in a single region";
    }
    return "unknown";
}

const char* toString(ChainError error) {
    switch (error) {
        case ChainError::kNone: return "ok";
        case ChainError::kBadIndex: return "descriptor index out of range";
        case ChainError::kChainTooLong: return "chain exceeds queue size";
        case ChainError::kCycle: return "descriptor chain cycle";
        case ChainError::kBadIndirectLength: return "bad indirect table length";
        case ChainError::kNestedIndirect: return "nested indirect descriptor";
        case ChainError::kTotalLengthExceeded: return "chain total length exceeds 2^30";
        case ChainError::kBadDescriptorAddress: return "descriptor address translation failed";
    }
    return "unknown";
}

}  // namespace netsec
