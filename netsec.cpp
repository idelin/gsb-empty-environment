#include "netsec.hpp"

#include <algorithm>
#include <cstdint>
#include <cstring>
#include <deque>
#include <limits>
#include <map>
#include <mutex>
#include <stdexcept>
#include <unordered_map>
#include <utility>
#include <vector>

#include <sys/mman.h>
#include <unistd.h>

namespace netsec {
namespace {

constexpr std::size_t kSizeMax = std::numeric_limits<std::size_t>::max();

bool is_power_of_two(std::size_t value) {
    return value != 0 && (value & (value - 1)) == 0;
}

bool next_power_of_two(std::size_t value, std::size_t& out) {
    if (value <= 1) {
        out = 1;
        return true;
    }
    std::size_t power = 1;
    while (power < value) {
        if (power > kSizeMax / 2) {
            return false;
        }
        power <<= 1;
    }
    out = power;
    return true;
}

std::size_t floor_power_of_two(std::size_t value) {
    std::size_t power = 1;
    while (power <= value / 2) {
        power <<= 1;
    }
    return power;
}

bool round_up(std::size_t value, std::size_t unit, std::size_t& out) {
    if (unit == 0) {
        return false;
    }
    const std::size_t remainder = value % unit;
    if (remainder == 0) {
        out = value;
        return true;
    }
    const std::size_t add = unit - remainder;
    if (value > kSizeMax - add) {
        return false;
    }
    out = value + add;
    return true;
}

std::size_t log2_power_of_two(std::size_t value) {
    std::size_t bits = 0;
    std::size_t power = 1;
    while (power < value) {
        power <<= 1;
        ++bits;
    }
    return bits;
}

std::size_t detect_page_size() {
    const long page = sysconf(_SC_PAGESIZE);
    if (page <= 0 || !is_power_of_two(static_cast<std::size_t>(page))) {
        throw std::runtime_error("netsec: unsupported system page size");
    }
    return static_cast<std::size_t>(page);
}

void* map_pages(std::size_t bytes) {
    const int flags =
#if defined(MAP_ANONYMOUS)
        MAP_PRIVATE | MAP_ANONYMOUS;
#else
        MAP_PRIVATE | MAP_ANON;
#endif
    void* ptr = ::mmap(nullptr, bytes, PROT_READ | PROT_WRITE, flags, -1, 0);
    return ptr == MAP_FAILED ? nullptr : ptr;
}

}  // namespace

struct PartitionedAllocator::Impl {
    struct Slab;

    struct FreeNode {
        void* next;
        Slab* slab;
    };

    struct Slab {
        DomainId domain = kNoDomain;
        std::uintptr_t base = 0;
        std::size_t slab_size = 0;
        std::size_t block_size = 0;
        std::uint32_t class_index = 0;
        std::size_t block_count = 0;
        std::size_t free_blocks = 0;
        std::size_t live_blocks = 0;
        std::vector<std::uint64_t> allocated_bits;
    };

    struct SizeClass {
        std::size_t block_size = 0;
        void* free_list = nullptr;
    };

    struct QuarantineEntry {
        void* ptr = nullptr;
        std::size_t bytes = 0;
        std::uint32_t class_index = 0;
        Slab* slab = nullptr;
        bool large = false;
        std::uint64_t free_epoch = 0;
    };

    struct DomainState {
        DomainId id = kNoDomain;
        std::vector<SizeClass> classes;
        std::deque<QuarantineEntry> quarantine;
        std::size_t quarantined_bytes = 0;
        std::vector<std::unique_ptr<Slab>> slabs;

        std::size_t live_blocks = 0;
        std::size_t live_bytes = 0;
        std::size_t large_live_blocks = 0;
        std::size_t large_live_bytes = 0;
        std::size_t slab_count = 0;
        std::size_t slab_mapped_bytes = 0;
    };

    struct Region {
        bool large = false;
        bool live = true;
        DomainId domain = kNoDomain;
        std::uintptr_t map_base = 0;
        std::size_t map_len = 0;
        std::uintptr_t user_base = 0;
        std::size_t user_len = 0;
        std::size_t requested_len = 0;
        Slab* slab = nullptr;
    };

    AllocatorConfig config;

    explicit Impl(AllocatorConfig cfg) : config(std::move(cfg)) {
        page_size_ = detect_page_size();

        if (!is_power_of_two(config.slab_size) ||
            config.slab_size < 2 * page_size_) {
            throw std::invalid_argument(
                "slab_size must be a power of two and at least two pages");
        }
        if (!is_power_of_two(config.min_block_size) ||
            config.min_block_size < 2 * sizeof(void*)) {
            throw std::invalid_argument(
                "min_block_size must be a power of two and hold a free node");
        }
        if (config.large_threshold < config.min_block_size ||
            config.large_threshold > config.slab_size) {
            throw std::invalid_argument(
                "large_threshold must be between min_block_size and slab_size");
        }
        if (config.quarantine_blocks == 0) {
            throw std::invalid_argument("quarantine_blocks must be at least 1");
        }
        if (config.quarantine_ops == 0) {
            throw std::invalid_argument("quarantine_ops must be at least 1");
        }

        min_class_size_ = std::max(config.min_block_size, 2 * sizeof(void*));
        max_class_size_ = std::min(
            floor_power_of_two(config.large_threshold), config.slab_size);
        if (max_class_size_ < min_class_size_) {
            throw std::invalid_argument(
                "large_threshold is too small for min_block_size");
        }
        max_class_log2_ = log2_power_of_two(max_class_size_);
    }

    ~Impl() {
        for (auto& entry : domains_) {
            DomainState& domain = *entry.second;
            while (!domain.quarantine.empty()) {
                retire_front(domain);
            }
        }

        for (auto& entry : regions_) {
            Region& region = entry.second;
            if (region.large) {
                ::munmap(reinterpret_cast<void*>(region.map_base),
                         region.map_len);
            }
        }

        for (auto& entry : domains_) {
            for (auto& slab : entry.second->slabs) {
                ::munmap(reinterpret_cast<void*>(slab->base),
                         slab->slab_size);
            }
        }
    }

    std::size_t page_size_ = 0;
    std::size_t min_class_size_ = 0;
    std::size_t max_class_size_ = 0;
    std::size_t max_class_log2_ = 0;
    std::uint64_t epoch_ = 0;

    mutable std::mutex mutex_;
    std::map<std::uintptr_t, Region> regions_;
    std::unordered_map<DomainId, std::unique_ptr<DomainState>> domains_;

    static std::size_t bit_words(std::size_t blocks) {
        return (blocks + 63u) / 64u;
    }

    static bool bit_test(const Slab& slab, std::size_t index) {
        return (slab.allocated_bits[index / 64u] &
                (std::uint64_t(1) << (index % 64u))) != 0;
    }

    static void bit_set(Slab& slab, std::size_t index) {
        slab.allocated_bits[index / 64u] |=
            std::uint64_t(1) << (index % 64u);
    }

    static void bit_clear(Slab& slab, std::size_t index) {
        slab.allocated_bits[index / 64u] &=
            ~(std::uint64_t(1) << (index % 64u));
    }

    DomainState* find_domain(DomainId id) {
        auto it = domains_.find(id);
        return it == domains_.end() ? nullptr : it->second.get();
    }

    const DomainState* find_domain(DomainId id) const {
        auto it = domains_.find(id);
        return it == domains_.end() ? nullptr : it->second.get();
    }

    DomainState& ensure_domain(DomainId id) {
        auto it = domains_.find(id);
        if (it != domains_.end()) {
            return *it->second;
        }
        auto state = std::make_unique<DomainState>();
        state->id = id;
        state->classes.resize(max_class_log2_ + 1);
        auto result = domains_.emplace(id, std::move(state));
        return *result.first->second;
    }

    Region* find_region(std::uintptr_t address) {
        auto it = regions_.upper_bound(address);
        if (it == regions_.begin()) {
            return nullptr;
        }
        --it;
        Region& region = it->second;
        if (address < region.user_base ||
            address - region.user_base >= region.user_len) {
            return nullptr;
        }
        return &region;
    }

    const Region* find_region(std::uintptr_t address) const {
        auto it = regions_.upper_bound(address);
        if (it == regions_.begin()) {
            return nullptr;
        }
        --it;
        const Region& region = it->second;
        if (address < region.user_base ||
            address - region.user_base >= region.user_len) {
            return nullptr;
        }
        return &region;
    }

    bool small_class(std::size_t size, std::size_t alignment,
                     std::size_t& block_size, std::uint32_t& index) const {
        std::size_t required = size == 0 ? 1 : size;
        required = std::max(required, alignment);
        required = std::max(required, min_class_size_);

        std::size_t rounded = 0;
        if (!next_power_of_two(required, rounded) ||
            rounded > max_class_size_) {
            return false;
        }
        block_size = rounded;
        index = static_cast<std::uint32_t>(log2_power_of_two(rounded));
        return true;
    }

    Slab* create_slab(DomainState& domain, std::size_t block_size,
                      std::uint32_t class_index) {
        void* mapping = map_pages(config.slab_size);
        if (mapping == nullptr) {
            return nullptr;
        }

        std::unique_ptr<Slab> slab;
        try {
            slab = std::make_unique<Slab>();
            slab->domain = domain.id;
            slab->base = reinterpret_cast<std::uintptr_t>(mapping);
            slab->slab_size = config.slab_size;
            slab->block_size = block_size;
            slab->class_index = class_index;
            slab->block_count = config.slab_size / block_size;
            slab->allocated_bits.assign(bit_words(slab->block_count), 0);
        } catch (...) {
            ::munmap(mapping, config.slab_size);
            throw;
        }

        Region region;
        region.large = false;
        region.live = true;
        region.domain = domain.id;
        region.map_base = slab->base;
        region.map_len = slab->slab_size;
        region.user_base = slab->base;
        region.user_len = slab->slab_size;
        region.requested_len = slab->slab_size;
        region.slab = slab.get();

        const std::uintptr_t slab_base = slab->base;
        bool inserted_region = false;
        try {
            inserted_region =
                regions_.emplace(slab_base, std::move(region)).second;
            if (!inserted_region) {
                throw std::logic_error("netsec: duplicate slab region");
            }
            domain.slabs.push_back(std::move(slab));
        } catch (...) {
            if (inserted_region) {
                regions_.erase(slab_base);
            }
            ::munmap(mapping, config.slab_size);
            throw;
        }

        SizeClass& size_class = domain.classes[class_index];
        size_class.block_size = block_size;
        Slab* raw_slab = domain.slabs.back().get();
        for (std::size_t i = 0; i < raw_slab->block_count; ++i) {
            void* block = reinterpret_cast<void*>(
                raw_slab->base + i * block_size);
            FreeNode* node = static_cast<FreeNode*>(block);
            node->next = size_class.free_list;
            node->slab = raw_slab;
            size_class.free_list = node;
            ++raw_slab->free_blocks;
        }

        ++domain.slab_count;
        domain.slab_mapped_bytes += config.slab_size;
        return raw_slab;
    }

    void push_free(Slab* slab, void* block) {
        SizeClass& size_class =
            find_domain(slab->domain)->classes[slab->class_index];
        FreeNode* node = static_cast<FreeNode*>(block);
        node->next = size_class.free_list;
        node->slab = slab;
        size_class.free_list = node;
        ++slab->free_blocks;
    }

    void* pop_free(DomainState& domain, SizeClass& size_class) {
        FreeNode* node = static_cast<FreeNode*>(size_class.free_list);
        void* block = node;
        size_class.free_list = node->next;

        Slab* slab = node->slab;
        const std::size_t block_index =
            (reinterpret_cast<std::uintptr_t>(block) - slab->base) /
            size_class.block_size;
        bit_set(*slab, block_index);
        --slab->free_blocks;
        ++slab->live_blocks;

        ++domain.live_blocks;
        domain.live_bytes += size_class.block_size;
        return block;
    }

    void retire_front(DomainState& domain) {
        QuarantineEntry entry = std::move(domain.quarantine.front());
        domain.quarantine.pop_front();
        domain.quarantined_bytes -= entry.bytes;

        if (entry.large) {
            auto it = regions_.find(
                reinterpret_cast<std::uintptr_t>(entry.ptr));
            if (it != regions_.end() && it->second.large) {
                ::munmap(reinterpret_cast<void*>(it->second.map_base),
                         it->second.map_len);
                regions_.erase(it);
            }
            return;
        }

        push_free(entry.slab, entry.ptr);
    }

    void retire_expired(DomainState& domain, std::uint64_t now) {
        while (!domain.quarantine.empty() &&
               now > domain.quarantine.front().free_epoch +
                         config.quarantine_ops) {
            retire_front(domain);
        }
    }

    void enqueue_quarantine(DomainState& domain, QuarantineEntry entry) {
        while (!domain.quarantine.empty() &&
               (domain.quarantine.size() >= config.quarantine_blocks ||
                domain.quarantined_bytes + entry.bytes >
                    config.quarantine_bytes)) {
            retire_front(domain);
        }
        domain.quarantine.push_back(entry);
        domain.quarantined_bytes += entry.bytes;
    }

    void* allocate_large(std::size_t size, std::size_t alignment,
                         DomainId domain_id) {
        std::size_t mapped_len = 0;
        if (!round_up(size, page_size_, mapped_len)) {
            return nullptr;
        }

        std::uintptr_t raw_base = 0;
        std::size_t raw_len = mapped_len;

        if (alignment > page_size_) {
            if (size > kSizeMax - alignment ||
                !round_up(size + alignment, page_size_, raw_len)) {
                return nullptr;
            }
            void* raw = map_pages(raw_len);
            if (raw == nullptr) {
                return nullptr;
            }
            raw_base = reinterpret_cast<std::uintptr_t>(raw);
            if (raw_base > std::numeric_limits<std::uintptr_t>::max() -
                               (alignment - 1)) {
                ::munmap(raw, raw_len);
                return nullptr;
            }
            const std::uintptr_t user_base =
                (raw_base + alignment - 1) &
                ~static_cast<std::uintptr_t>(alignment - 1);
            if (user_base + mapped_len > raw_base + raw_len) {
                ::munmap(raw, raw_len);
                return nullptr;
            }

            if (user_base > raw_base) {
                ::munmap(raw, user_base - raw_base);
            }
            const std::uintptr_t suffix_base = user_base + mapped_len;
            const std::size_t suffix_len =
                raw_base + raw_len - suffix_base;
            if (suffix_len != 0) {
                ::munmap(reinterpret_cast<void*>(suffix_base), suffix_len);
            }
            raw_base = user_base;
        } else {
            void* raw = map_pages(mapped_len);
            if (raw == nullptr) {
                return nullptr;
            }
            raw_base = reinterpret_cast<std::uintptr_t>(raw);
        }

        Region region;
        region.large = true;
        region.live = true;
        region.domain = domain_id;
        region.map_base = raw_base;
        region.map_len = mapped_len;
        region.user_base = raw_base;
        region.user_len = mapped_len;
        region.requested_len = size;
        region.slab = nullptr;

        bool inserted = false;
        try {
            inserted = regions_.emplace(raw_base, std::move(region)).second;
        } catch (...) {
            ::munmap(reinterpret_cast<void*>(raw_base), mapped_len);
            throw;
        }
        if (!inserted) {
            ::munmap(reinterpret_cast<void*>(raw_base), mapped_len);
            return nullptr;
        }

        DomainState& domain = *find_domain(domain_id);
        ++domain.live_blocks;
        domain.live_bytes += size;
        ++domain.large_live_blocks;
        domain.large_live_bytes += size;
        return reinterpret_cast<void*>(raw_base);
    }

    void* allocate(std::size_t size, std::size_t alignment,
                   DomainId domain_id) {
        if (domain_id == kNoDomain ||
            !is_power_of_two(alignment)) {
            return nullptr;
        }
        if (size == 0) {
            size = 1;
        }

        DomainState& domain = ensure_domain(domain_id);
        const std::uint64_t now = ++epoch_;
        retire_expired(domain, now);

        std::size_t block_size = 0;
        std::uint32_t class_index = 0;
        const bool large =
            size > config.large_threshold ||
            alignment > page_size_ ||
            !small_class(size, alignment, block_size, class_index);
        if (large) {
            return allocate_large(size, alignment, domain_id);
        }

        SizeClass& size_class = domain.classes[class_index];
        if (size_class.free_list == nullptr) {
            if (create_slab(domain, block_size, class_index) == nullptr) {
                return nullptr;
            }
        }
        return pop_free(domain, size_class);
    }

    bool deallocate_small(Region& region, void* ptr) {
        Slab* slab = region.slab;
        const std::uintptr_t address = reinterpret_cast<std::uintptr_t>(ptr);
        if (address < slab->base ||
            address - slab->base >= slab->slab_size) {
            return false;
        }
        const std::uintptr_t offset = address - slab->base;
        if (offset % slab->block_size != 0) {
            return false;
        }
        const std::size_t block_index =
            static_cast<std::size_t>(offset / slab->block_size);
        if (!bit_test(*slab, block_index)) {
            return false;
        }

        bit_clear(*slab, block_index);
        --slab->live_blocks;

        DomainState& domain = *find_domain(region.domain);
        --domain.live_blocks;
        domain.live_bytes -= slab->block_size;

        const std::uint64_t now = ++epoch_;
        if (config.scrub_on_free) {
            std::memset(ptr, 0xA5, slab->block_size);
        }

        QuarantineEntry entry;
        entry.ptr = ptr;
        entry.bytes = slab->block_size;
        entry.class_index = slab->class_index;
        entry.slab = slab;
        entry.large = false;
        entry.free_epoch = now;
        enqueue_quarantine(domain, entry);
        return true;
    }

    bool deallocate_large(Region& region, void* ptr) {
        const std::uintptr_t address = reinterpret_cast<std::uintptr_t>(ptr);
        if (!region.live || address != region.user_base) {
            return false;
        }

        region.live = false;
        DomainState& domain = *find_domain(region.domain);
        --domain.live_blocks;
        domain.live_bytes -= region.requested_len;
        --domain.large_live_blocks;
        domain.large_live_bytes -= region.requested_len;

        const std::uint64_t now = ++epoch_;
        if (config.scrub_on_free) {
            std::memset(ptr, 0xA5, region.user_len);
        }

        QuarantineEntry entry;
        entry.ptr = ptr;
        entry.bytes = region.map_len;
        entry.large = true;
        entry.free_epoch = now;
        enqueue_quarantine(domain, entry);
        return true;
    }

    bool deallocate(void* ptr) {
        if (ptr == nullptr) {
            return false;
        }
        Region* region = find_region(reinterpret_cast<std::uintptr_t>(ptr));
        if (region == nullptr) {
            return false;
        }
        if (region->large) {
            return deallocate_large(*region, ptr);
        }
        return deallocate_small(*region, ptr);
    }

    bool is_allocated_small(const Region& region, std::uintptr_t address) const {
        const Slab& slab = *region.slab;
        if (address < slab.base ||
            address - slab.base >= slab.slab_size) {
            return false;
        }
        const std::uintptr_t offset = address - slab.base;
        if (offset % slab.block_size != 0) {
            return false;
        }
        return bit_test(slab, static_cast<std::size_t>(
            offset / slab.block_size));
    }

    DomainStats stats_for(const DomainState& domain) const {
        DomainStats out;
        out.live_blocks = domain.live_blocks;
        out.live_bytes = domain.live_bytes;
        out.slab_count = domain.slab_count;
        out.slab_mapped_bytes = domain.slab_mapped_bytes;
        out.large_live_blocks = domain.large_live_blocks;
        out.large_live_bytes = domain.large_live_bytes;
        out.quarantined_blocks = domain.quarantine.size();
        out.quarantined_bytes = domain.quarantined_bytes;
        return out;
    }
};

PartitionedAllocator::PartitionedAllocator(AllocatorConfig config)
    : impl_(std::make_unique<Impl>(std::move(config))) {}

PartitionedAllocator::~PartitionedAllocator() = default;

void* PartitionedAllocator::Allocate(std::size_t size,
                                     std::size_t alignment,
                                     DomainId domain) {
    try {
        std::lock_guard<std::mutex> lock(impl_->mutex_);
        return impl_->allocate(size, alignment, domain);
    } catch (const std::exception&) {
        return nullptr;
    }
}

bool PartitionedAllocator::Deallocate(void* ptr) {
    try {
        std::lock_guard<std::mutex> lock(impl_->mutex_);
        return impl_->deallocate(ptr);
    } catch (const std::exception&) {
        return false;
    }
}

DomainId PartitionedAllocator::PageOwner(const void* ptr) const {
    if (ptr == nullptr) {
        return kNoDomain;
    }
    std::lock_guard<std::mutex> lock(impl_->mutex_);
    const Impl::Region* region =
        impl_->find_region(reinterpret_cast<std::uintptr_t>(ptr));
    return region == nullptr ? kNoDomain : region->domain;
}

bool PartitionedAllocator::Owns(const void* ptr) const {
    if (ptr == nullptr) {
        return false;
    }
    std::lock_guard<std::mutex> lock(impl_->mutex_);
    return impl_->find_region(reinterpret_cast<std::uintptr_t>(ptr)) != nullptr;
}

bool PartitionedAllocator::IsAllocated(const void* ptr) const {
    if (ptr == nullptr) {
        return false;
    }
    std::lock_guard<std::mutex> lock(impl_->mutex_);
    const std::uintptr_t address = reinterpret_cast<std::uintptr_t>(ptr);
    const Impl::Region* region = impl_->find_region(address);
    if (region == nullptr) {
        return false;
    }
    if (region->large) {
        return region->live && address == region->user_base;
    }
    return impl_->is_allocated_small(*region, address);
}

bool PartitionedAllocator::IsQuarantined(const void* ptr) const {
    if (ptr == nullptr) {
        return false;
    }
    std::lock_guard<std::mutex> lock(impl_->mutex_);
    const std::uintptr_t address = reinterpret_cast<std::uintptr_t>(ptr);
    const Impl::Region* region = impl_->find_region(address);
    if (region == nullptr) {
        return false;
    }
    const Impl::DomainState* domain = impl_->find_domain(region->domain);
    if (domain == nullptr) {
        return false;
    }
    for (const Impl::QuarantineEntry& entry : domain->quarantine) {
        if (reinterpret_cast<std::uintptr_t>(entry.ptr) == address) {
            return true;
        }
    }
    return false;
}

AllocationKind PartitionedAllocator::KindOf(const void* ptr) const {
    if (ptr == nullptr) {
        return AllocationKind::None;
    }
    std::lock_guard<std::mutex> lock(impl_->mutex_);
    const Impl::Region* region =
        impl_->find_region(reinterpret_cast<std::uintptr_t>(ptr));
    if (region == nullptr) {
        return AllocationKind::None;
    }
    return region->large ? AllocationKind::LargeMmap
                         : AllocationKind::Slab;
}

AllocatorStats PartitionedAllocator::Stats() const {
    std::lock_guard<std::mutex> lock(impl_->mutex_);
    AllocatorStats out;
    out.domain_count = impl_->domains_.size();
    out.epoch = impl_->epoch_;
    for (const auto& entry : impl_->domains_) {
        const DomainStats stats = impl_->stats_for(*entry.second);
        out.live_blocks += stats.live_blocks;
        out.live_bytes += stats.live_bytes;
        out.slab_count += stats.slab_count;
        out.slab_mapped_bytes += stats.slab_mapped_bytes;
        out.large_live_blocks += stats.large_live_blocks;
        out.large_live_bytes += stats.large_live_bytes;
        out.quarantined_blocks += stats.quarantined_blocks;
        out.quarantined_bytes += stats.quarantined_bytes;
    }
    for (const auto& entry : impl_->regions_) {
        if (entry.second.large) {
            out.large_mapped_bytes += entry.second.map_len;
        }
    }
    return out;
}

DomainStats PartitionedAllocator::StatsFor(DomainId domain_id) const {
    std::lock_guard<std::mutex> lock(impl_->mutex_);
    const Impl::DomainState* domain = impl_->find_domain(domain_id);
    if (domain == nullptr) {
        return DomainStats{};
    }
    return impl_->stats_for(*domain);
}

const AllocatorConfig& PartitionedAllocator::Config() const noexcept {
    return impl_->config;
}

std::size_t PartitionedAllocator::PageSize() {
    static const std::size_t page_size = detect_page_size();
    return page_size;
}

}  // namespace netsec
