#include "netsec.hpp"

#include <algorithm>

namespace netsec {

MemTableError GuestMemTable::set_regions(
    const std::vector<MemoryRegion>& regions) {
  if (regions.size() > kMaxRegions) {
    return MemTableError::kTooManyRegions;
  }
  for (const MemoryRegion& region : regions) {
    if (region.memory_size == 0) {
      return MemTableError::kZeroSizeRegion;
    }
    if (region.guest_phys_addr + region.memory_size < region.guest_phys_addr) {
      return MemTableError::kRegionWrapAround;
    }
  }
  for (std::size_t i = 0; i < regions.size(); ++i) {
    for (std::size_t j = i + 1; j < regions.size(); ++j) {
      const uint64_t a_begin = regions[i].guest_phys_addr;
      const uint64_t a_end = a_begin + regions[i].memory_size;
      const uint64_t b_begin = regions[j].guest_phys_addr;
      const uint64_t b_end = b_begin + regions[j].memory_size;
      if (a_begin < b_end && b_begin < a_end) {
        return MemTableError::kOverlappingRegions;
      }
    }
  }
  regions_ = regions;
  ++generation_;
  cache_.clear();  // stale translations must never survive a table update
  return MemTableError::kNone;
}

std::optional<uint64_t> GuestMemTable::translate(uint64_t gpa,
                                                 uint64_t len) const {
  TranslateError err = TranslateError::kNone;
  return translate(gpa, len, err);
}

std::optional<uint64_t> GuestMemTable::translate(uint64_t gpa, uint64_t len,
                                                 TranslateError& err) const {
  err = TranslateError::kNone;
  if (len == 0) {
    err = TranslateError::kZeroLength;
    return std::nullopt;
  }
  if (gpa + len < gpa) {
    err = TranslateError::kWrapAround;
    return std::nullopt;
  }

  const auto cached = cache_.find(gpa);
  if (cached != cache_.end() && cached->second.len == len) {
    return cached->second.host_addr;
  }

  for (const MemoryRegion& region : regions_) {
    const uint64_t begin = region.guest_phys_addr;
    const uint64_t end = begin + region.memory_size;  // overflow-checked
    if (gpa >= begin && gpa + len <= end) {
      const uint64_t host = region.userspace_addr + (gpa - begin);
      cache_[gpa] = CacheEntry{len, host};
      return host;
    }
  }
  err = TranslateError::kOutOfRange;
  return std::nullopt;
}

VirtqueueWalker::VirtqueueWalker(const GuestMemTable& mem,
                                 uint64_t desc_table_gpa, uint16_t queue_size)
    : mem_(mem), desc_table_gpa_(desc_table_gpa), queue_size_(queue_size) {}

const Descriptor* VirtqueueWalker::desc_at(uint16_t index) const {
  const uint64_t gpa =
      desc_table_gpa_ + static_cast<uint64_t>(index) * sizeof(Descriptor);
  const std::optional<uint64_t> host = mem_.translate(gpa, sizeof(Descriptor));
  if (!host.has_value()) {
    return nullptr;
  }
  return reinterpret_cast<const Descriptor*>(*host);
}

std::optional<ChainResult> VirtqueueWalker::walk(uint16_t head,
                                                 ChainError& err) const {
  err = ChainError::kNone;
  if (head >= queue_size_) {
    err = ChainError::kHeadOutOfRange;
    return std::nullopt;
  }

  ChainResult result;
  std::vector<bool> visited(queue_size_, false);
  uint16_t index = head;
  uint32_t chain_len = 0;

  while (true) {
    if (index >= queue_size_) {
      err = ChainError::kIndexOutOfRange;
      return std::nullopt;
    }
    if (visited[index]) {
      err = ChainError::kCycle;
      return std::nullopt;
    }
    visited[index] = true;
    if (chain_len >= queue_size_) {
      err = ChainError::kChainTooLong;
      return std::nullopt;
    }
    ++chain_len;

    const Descriptor* desc = desc_at(index);
    if (desc == nullptr) {
      err = ChainError::kAddressTranslationFailed;
      return std::nullopt;
    }

    if (desc->flags & kDescFIndirect) {
      if (desc->flags & kDescFNext) {
        err = ChainError::kIndirectWithNext;
        return std::nullopt;
      }
      const uint32_t table_len = desc->len;
      if (table_len == 0 || table_len % sizeof(Descriptor) != 0 ||
          table_len > static_cast<uint32_t>(queue_size_) *
                          sizeof(Descriptor)) {
        err = ChainError::kBadIndirectLength;
        return std::nullopt;
      }
      const std::optional<uint64_t> table_host =
          mem_.translate(desc->addr, table_len);
      if (!table_host.has_value()) {
        err = ChainError::kAddressTranslationFailed;
        return std::nullopt;
      }
      const auto* table = reinterpret_cast<const Descriptor*>(*table_host);
      const uint32_t count = table_len / sizeof(Descriptor);
      for (uint32_t i = 0; i < count; ++i) {
        const Descriptor& entry = table[i];
        if (entry.flags & kDescFIndirect) {
          err = ChainError::kNestedIndirect;
          return std::nullopt;
        }
        if (!mem_.translate(entry.addr, entry.len).has_value()) {
          err = ChainError::kAddressTranslationFailed;
          return std::nullopt;
        }
        result.total_length += entry.len;
        if (result.total_length > kMaxChainTotalLen) {
          err = ChainError::kTotalLengthTooLarge;
          return std::nullopt;
        }
        result.descriptors.push_back(entry);
      }
      return result;  // an indirect table is the whole chain
    }

    if (!mem_.translate(desc->addr, desc->len).has_value()) {
      err = ChainError::kAddressTranslationFailed;
      return std::nullopt;
    }
    result.total_length += desc->len;
    if (result.total_length > kMaxChainTotalLen) {
      err = ChainError::kTotalLengthTooLarge;
      return std::nullopt;
    }
    result.descriptors.push_back(*desc);

    if (!(desc->flags & kDescFNext)) {
      return result;
    }
    index = desc->next;
  }
}

}  // namespace netsec
