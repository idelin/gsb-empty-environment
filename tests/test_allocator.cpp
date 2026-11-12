#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cstdint>
#include <cstdlib>
#include <deque>
#include <iostream>
#include <map>
#include <random>
#include <set>
#include <string>
#include <unordered_map>
#include <utility>
#include <vector>

using namespace netsec;

namespace {

int g_failures = 0;

#define CHECK(condition)                                                       \
    do {                                                                       \
        if (!(condition)) {                                                    \
            std::cerr << __FILE__ << ':' << __LINE__                          \
                      << ": CHECK failed: " #condition << '\n';                \
            ++g_failures;                                                      \
        }                                                                      \
    } while (false)

std::uintptr_t page_floor(std::uintptr_t address, std::size_t page_size) {
    return address & ~static_cast<std::uintptr_t>(page_size - 1);
}

std::size_t round_up_pages(std::size_t size, std::size_t page_size) {
    const std::size_t remainder = size % page_size;
    return remainder == 0 ? size : size + (page_size - remainder);
}

AllocatorConfig common_config() {
    AllocatorConfig config;
    config.slab_size = 64u * 1024u;
    config.large_threshold = 16u * 1024u;
    config.min_block_size = 16;
    config.quarantine_blocks = 64;
    config.quarantine_bytes = 64u * 1024u;
    config.quarantine_ops = 64;
    config.scrub_on_free = true;
    return config;
}

AllocatorConfig random_config() {
    AllocatorConfig config;
    config.slab_size = 64u * 1024u;
    config.large_threshold = 32u * 1024u;
    config.min_block_size = 16;
    config.quarantine_blocks = 24;
    config.quarantine_bytes = 96u * 1024u;
    config.quarantine_ops = 32;
    config.scrub_on_free = true;
    return config;
}

void test_invalid_arguments() {
    PartitionedAllocator allocator(common_config());
    int stack_object = 0;

    CHECK(allocator.Allocate(64, 3, 1) == nullptr);
    CHECK(allocator.Allocate(64, 16, kNoDomain) == nullptr);
    CHECK(allocator.PageOwner(nullptr) == kNoDomain);
    CHECK(allocator.PageOwner(&stack_object) == kNoDomain);
    CHECK(!allocator.Owns(&stack_object));
    CHECK(!allocator.IsAllocated(&stack_object));
    CHECK(!allocator.IsQuarantined(&stack_object));
    CHECK(allocator.KindOf(&stack_object) == AllocationKind::None);
    CHECK(!allocator.Deallocate(nullptr));
    CHECK(!allocator.Deallocate(&stack_object));
}

void test_cross_domain_page_isolation() {
    const std::size_t page = PartitionedAllocator::PageSize();
    AllocatorConfig config = common_config();
    PartitionedAllocator allocator(config);

    const DomainId domain_a = 11;
    const DomainId domain_b = 22;
    std::vector<void*> a_blocks;
    std::vector<void*> b_blocks;

    for (int i = 0; i < 300; ++i) {
        void* p = allocator.Allocate(512, 64, domain_a);
        CHECK(p != nullptr);
        if (p != nullptr) {
            CHECK(reinterpret_cast<std::uintptr_t>(p) % 64 == 0);
            CHECK(allocator.PageOwner(p) == domain_a);
            a_blocks.push_back(p);
        }
        p = allocator.Allocate(512, 64, domain_b);
        CHECK(p != nullptr);
        if (p != nullptr) {
            CHECK(reinterpret_cast<std::uintptr_t>(p) % 64 == 0);
            CHECK(allocator.PageOwner(p) == domain_b);
            b_blocks.push_back(p);
        }
    }

    std::map<std::uintptr_t, DomainId> page_owners;
    auto record_pages = [&](const std::vector<void*>& blocks,
                            DomainId domain, std::size_t span) {
        for (void* block : blocks) {
            const std::uintptr_t start =
                page_floor(reinterpret_cast<std::uintptr_t>(block), page);
            const std::uintptr_t end =
                reinterpret_cast<std::uintptr_t>(block) + span;
            for (std::uintptr_t p = start; p < end; p += page) {
                auto inserted = page_owners.emplace(p, domain);
                CHECK(inserted.second || inserted.first->second == domain);
                CHECK(allocator.PageOwner(reinterpret_cast<void*>(p)) ==
                      domain);
            }
        }
    };
    record_pages(a_blocks, domain_a, 512);
    record_pages(b_blocks, domain_b, 512);

    const DomainStats stats_a = allocator.StatsFor(domain_a);
    const DomainStats stats_b = allocator.StatsFor(domain_b);
    CHECK(stats_a.live_blocks == a_blocks.size());
    CHECK(stats_b.live_blocks == b_blocks.size());
    CHECK(stats_a.slab_count > 0);
    CHECK(stats_b.slab_count > 0);

    for (void* block : a_blocks) {
        CHECK(allocator.Deallocate(block));
        CHECK(allocator.IsQuarantined(block));
    }
    for (void* block : b_blocks) {
        CHECK(allocator.Deallocate(block));
        CHECK(allocator.IsQuarantined(block));
    }

    CHECK(allocator.StatsFor(domain_a).live_blocks == 0);
    CHECK(allocator.StatsFor(domain_b).live_blocks == 0);
}

void test_quarantine_age_and_immediate_reallocation() {
    AllocatorConfig config = common_config();
    config.quarantine_blocks = 128;
    config.quarantine_bytes = 256u * 1024u;
    config.quarantine_ops = 8;
    PartitionedAllocator allocator(config);

    std::vector<void*> live;
    void* a = allocator.Allocate(32, 16, 7);
    CHECK(a != nullptr);
    CHECK(allocator.Deallocate(a));
    CHECK(allocator.IsQuarantined(a));
    CHECK(!allocator.IsAllocated(a));
    CHECK(!allocator.Deallocate(a));

    // The very next allocation of the same domain/class must not get the
    // quarantined block back.
    void* b = allocator.Allocate(32, 16, 7);
    CHECK(b != nullptr);
    CHECK(b != a);
    CHECK(allocator.IsQuarantined(a));
    live.push_back(b);

    // q_ops = 8 means A remains isolated through eight further operations.
    for (int i = 0; i < 7; ++i) {
        void* p = allocator.Allocate(32, 16, 7);
        CHECK(p != nullptr);
        CHECK(p != a);
        CHECK(allocator.IsQuarantined(a));
        live.push_back(p);
    }
    CHECK(allocator.IsQuarantined(a));

    void* ninth = allocator.Allocate(32, 16, 7);
    CHECK(ninth != nullptr);
    CHECK(!allocator.IsQuarantined(a));
    if (ninth != a) {
        live.push_back(ninth);
    } else {
        CHECK(allocator.IsAllocated(ninth));
    }

    // The stats must account for the block either live again or still free.
    CHECK(allocator.PageOwner(a) == 7);
    CHECK(allocator.Stats().quarantined_blocks == 0);

    for (void* p : live) {
        CHECK(allocator.Deallocate(p));
    }
}

void test_quarantine_capacity_evicts_oldest_only() {
    AllocatorConfig config = common_config();
    config.quarantine_blocks = 1;
    config.quarantine_bytes = 1u * 1024u * 1024u;
    config.quarantine_ops = 100000;
    PartitionedAllocator allocator(config);

    void* a = allocator.Allocate(64, 16, 3);
    void* b = allocator.Allocate(64, 16, 3);
    CHECK(a != nullptr && b != nullptr && a != b);

    CHECK(allocator.Deallocate(a));
    CHECK(allocator.IsQuarantined(a));
    CHECK(allocator.Deallocate(b));
    CHECK(allocator.IsQuarantined(b));
    CHECK(!allocator.IsQuarantined(a));

    void* c = allocator.Allocate(64, 16, 3);
    CHECK(c != nullptr);
    CHECK(c != b);  // the newest quarantine entry must not be reused
    CHECK(allocator.IsQuarantined(b));
    CHECK(allocator.PageOwner(c) == 3);

    CHECK(allocator.Deallocate(c));
    CHECK(allocator.IsQuarantined(c));
    CHECK(!allocator.IsQuarantined(b));
}

void test_freed_blocks_do_not_cross_domains() {
    AllocatorConfig config = common_config();
    config.quarantine_ops = 4;
    PartitionedAllocator allocator(config);

    void* a = allocator.Allocate(128, 16, 1);
    CHECK(a != nullptr);
    CHECK(allocator.Deallocate(a));

    void* b = allocator.Allocate(128, 16, 2);
    CHECK(b != nullptr);
    CHECK(b != a);
    CHECK(allocator.PageOwner(b) == 2);
    CHECK(allocator.PageOwner(a) == 1);

    // Let domain 2 operate enough to retire its own blocks.  Domain 1's
    // quarantined block cannot migrate into domain 2's free list.
    for (int i = 0; i < 16; ++i) {
        void* p = allocator.Allocate(128, 16, 2);
        CHECK(p != a);
        CHECK(allocator.PageOwner(p) == 2);
        CHECK(allocator.Deallocate(p));
    }
    CHECK(allocator.PageOwner(a) == 1);
}

void test_large_mmap_ownership() {
    const std::size_t page = PartitionedAllocator::PageSize();
    AllocatorConfig config = common_config();
    config.large_threshold = 16u * 1024u;
    config.quarantine_blocks = 1;
    config.quarantine_bytes = 1;
    config.quarantine_ops = 100000;
    PartitionedAllocator allocator(config);

    const std::size_t size = 3 * page + 37;
    const std::size_t mapped_len = round_up_pages(size, page);
    const DomainId domain = 5;

    CHECK(allocator.Stats().slab_mapped_bytes == 0);
    void* large = allocator.Allocate(size, page, domain);
    CHECK(large != nullptr);
    if (large == nullptr) {
        return;
    }

    CHECK(allocator.KindOf(large) == AllocationKind::LargeMmap);
    CHECK(reinterpret_cast<std::uintptr_t>(large) % page == 0);
    CHECK(allocator.IsAllocated(large));
    CHECK(allocator.PageOwner(large) == domain);
    CHECK(allocator.PageOwner(
              static_cast<char*>(large) + size - 1) == domain);

    for (std::size_t offset = 0; offset < mapped_len; offset += page) {
        CHECK(allocator.PageOwner(static_cast<char*>(large) + offset) ==
              domain);
    }

    AllocatorStats stats = allocator.Stats();
    CHECK(stats.slab_mapped_bytes == 0);
    CHECK(stats.large_live_blocks == 1);
    CHECK(stats.large_mapped_bytes == mapped_len);

    CHECK(allocator.Deallocate(large));
    CHECK(allocator.IsQuarantined(large));
    CHECK(!allocator.IsAllocated(large));
    CHECK(allocator.PageOwner(large) == domain);  // mapping is quarantined
    CHECK(!allocator.Deallocate(large));

    CHECK(allocator.Stats().large_live_blocks == 0);

    // A same-size allocation must not receive the quarantined mapping.
    void* second = allocator.Allocate(size, page, domain);
    CHECK(second != nullptr && second != large);
    if (second == nullptr) {
        return;
    }
    CHECK(allocator.PageOwner(second) == domain);

    // The one-entry quarantine retires the first mapping when the second is
    // freed, so its address is no longer owned.
    CHECK(allocator.Deallocate(second));
    CHECK(allocator.PageOwner(large) == kNoDomain);
    CHECK(allocator.PageOwner(second) == domain);

    // Over-aligned requests also take the direct-mmap path.
    void* aligned = allocator.Allocate(100, 2 * page, 9);
    CHECK(aligned != nullptr);
    if (aligned != nullptr) {
        CHECK(reinterpret_cast<std::uintptr_t>(aligned) % (2 * page) == 0);
        CHECK(allocator.KindOf(aligned) == AllocationKind::LargeMmap);
        CHECK(allocator.PageOwner(aligned) == 9);
        CHECK(allocator.Deallocate(aligned));
    }

    CHECK(allocator.StatsFor(9).large_live_blocks == 0);
}

struct ModelRecord {
    DomainId domain = kNoDomain;
    std::size_t size = 0;
    std::size_t alignment = 0;
    std::size_t span = 0;
    std::size_t live_bytes = 0;
    std::size_t quarantine_bytes = 0;
    bool large = false;
    bool live = false;
    bool quarantined = false;
    std::uint64_t free_epoch = 0;
};

struct ModelQueueEntry {
    std::uintptr_t ptr = 0;
    std::size_t bytes = 0;
    std::uint64_t free_epoch = 0;
};

class ReferenceModel {
public:
    ReferenceModel(const AllocatorConfig& config, std::size_t page_size)
        : config_(config), page_size_(page_size) {}

    void expect_allocation(DomainId domain) {
        domains_.insert(domain);
        ++epoch_;
        retire_expired(domain);
    }

    bool add_allocation(void* ptr, DomainId domain, std::size_t size,
                        std::size_t alignment, AllocationKind kind,
                        const std::string& where) {
        const std::uintptr_t address = reinterpret_cast<std::uintptr_t>(ptr);
        if (records_.find(address) != records_.end()) {
            fail("allocation returned a live/quarantined pointer", where);
            return false;
        }

        ModelRecord record;
        record.domain = domain;
        record.size = size == 0 ? 1 : size;
        record.alignment = alignment;
        record.large = kind == AllocationKind::LargeMmap;
        if (kind == AllocationKind::None) {
            fail("allocation has no backing kind", where);
            return false;
        }
        if (record.large) {
            record.span = round_up_pages(record.size, page_size_);
        } else {
            record.span = class_size(record.size, alignment);
        }
        record.live_bytes = record.large ? record.size : record.span;
        record.quarantine_bytes = record.span;
        record.live = true;

        if (!record.large) {
            auto history = small_history_domain_.find(address);
            if (history != small_history_domain_.end() &&
                history->second != domain) {
                fail("a slab address was reused by another domain", where);
                return false;
            }
            auto span_history = small_history_span_.find(address);
            if (span_history != small_history_span_.end() &&
                span_history->second != record.span) {
                fail("a slab block changed size class", where);
                return false;
            }
            small_history_domain_[address] = domain;
            small_history_span_[address] = record.span;
        }

        records_.emplace(address, record);
        return true;
    }

    bool deallocate(void* ptr, const std::string& where) {
        const std::uintptr_t address = reinterpret_cast<std::uintptr_t>(ptr);
        auto it = records_.find(address);
        if (it == records_.end() || !it->second.live) {
            fail("reference model deallocated an unknown/live pointer", where);
            return false;
        }

        const DomainId domain = it->second.domain;
        const std::size_t bytes = it->second.quarantine_bytes;
        ++epoch_;

        auto& queue = queues_[domain];
        auto& queue_bytes = queue_bytes_[domain];
        while (!queue.empty() &&
               (queue.size() >= config_.quarantine_blocks ||
                queue_bytes + bytes > config_.quarantine_bytes)) {
            retire_front(domain);
        }

        it->second.live = false;
        it->second.quarantined = true;
        it->second.free_epoch = epoch_;
        queue.push_back(ModelQueueEntry{address, bytes, epoch_});
        queue_bytes += bytes;
        return true;
    }

    bool is_quarantined(std::uintptr_t address) const {
        auto it = records_.find(address);
        return it != records_.end() && it->second.quarantined;
    }

    std::uint64_t epoch() const {
        return epoch_;
    }

    const std::set<DomainId>& domains() const {
        return domains_;
    }

    const std::unordered_map<std::uintptr_t, ModelRecord>& records() const {
        return records_;
    }

    bool check_stats(const PartitionedAllocator& allocator,
                     const std::string& where) const {
        std::size_t live_blocks = 0;
        std::size_t live_bytes = 0;
        std::size_t quarantined_blocks = 0;
        std::size_t quarantined_bytes = 0;
        std::map<DomainId, DomainStats> per_domain;
        for (const auto& entry : records_) {
            const ModelRecord& record = entry.second;
            DomainStats& domain_stats = per_domain[record.domain];
            if (record.live) {
                ++live_blocks;
                live_bytes += record.live_bytes;
                ++domain_stats.live_blocks;
                domain_stats.live_bytes += record.live_bytes;
                if (record.large) {
                    ++domain_stats.large_live_blocks;
                    domain_stats.large_live_bytes += record.size;
                }
            } else if (record.quarantined) {
                ++quarantined_blocks;
                quarantined_bytes += record.quarantine_bytes;
                ++domain_stats.quarantined_blocks;
                domain_stats.quarantined_bytes += record.quarantine_bytes;
            }
        }

        const AllocatorStats stats = allocator.Stats();
        bool ok = true;
        ok &= check_value(stats.live_blocks, live_blocks, "live_blocks", where);
        ok &= check_value(stats.live_bytes, live_bytes, "live_bytes", where);
        ok &= check_value(stats.quarantined_blocks, quarantined_blocks,
                          "quarantined_blocks", where);
        ok &= check_value(stats.quarantined_bytes, quarantined_bytes,
                          "quarantined_bytes", where);
        ok &= check_value(stats.epoch, epoch_, "epoch", where);
        ok &= check_value(stats.domain_count, domains_.size(),
                          "domain_count", where);

        for (DomainId domain : domains_) {
            const DomainStats actual = allocator.StatsFor(domain);
            const DomainStats expected = per_domain[domain];
            ok &= check_value(actual.live_blocks, expected.live_blocks,
                              "domain live_blocks", where);
            ok &= check_value(actual.live_bytes, expected.live_bytes,
                              "domain live_bytes", where);
            ok &= check_value(actual.quarantined_blocks,
                              expected.quarantined_blocks,
                              "domain quarantined_blocks", where);
            ok &= check_value(actual.quarantined_bytes,
                              expected.quarantined_bytes,
                              "domain quarantined_bytes", where);
        }
        return ok;
    }

    bool check_ownership(const PartitionedAllocator& allocator,
                         const std::string& where) const {
        std::map<std::uintptr_t, DomainId> page_domains;
        bool ok = true;

        for (const auto& entry : records_) {
            const std::uintptr_t address = entry.first;
            const ModelRecord& record = entry.second;
            if (!record.live && !record.quarantined) {
                continue;
            }

            const DomainId owner =
                allocator.PageOwner(reinterpret_cast<void*>(address));
            if (owner != record.domain) {
                fail("PageOwner returned the wrong domain", where);
                ok = false;
            }
            const DomainId last_owner = allocator.PageOwner(
                reinterpret_cast<void*>(address + record.size - 1));
            if (last_owner != record.domain) {
                fail("PageOwner failed on the last requested byte", where);
                ok = false;
            }

            const AllocationKind kind =
                allocator.KindOf(reinterpret_cast<void*>(address));
            const AllocationKind expected_kind =
                record.large ? AllocationKind::LargeMmap
                             : AllocationKind::Slab;
            if (kind != expected_kind) {
                fail("allocation kind mismatch", where);
                ok = false;
            }

            if (record.live) {
                if (!allocator.IsAllocated(
                        reinterpret_cast<void*>(address))) {
                    fail("live block is not marked allocated", where);
                    ok = false;
                }
            } else {
                if (!allocator.IsQuarantined(
                        reinterpret_cast<void*>(address))) {
                    fail("quarantined block is not in the queue", where);
                    ok = false;
                }
                if (allocator.IsAllocated(
                        reinterpret_cast<void*>(address))) {
                    fail("quarantined block is marked live", where);
                    ok = false;
                }
            }

            const std::uintptr_t start = page_floor(address, page_size_);
            const std::uintptr_t end = address + record.span;
            for (std::uintptr_t page = start; page < end;
                 page += page_size_) {
                auto inserted = page_domains.emplace(page, record.domain);
                if (!inserted.second &&
                    inserted.first->second != record.domain) {
                    fail("different domains share a page", where);
                    ok = false;
                }
                if (allocator.PageOwner(
                        reinterpret_cast<void*>(page)) != record.domain) {
                    fail("page table disagrees with PageOwner", where);
                    ok = false;
                }
            }
        }
        return ok;
    }

private:
    static void fail(const std::string& message, const std::string& where) {
        std::cerr << "model check failed [" << where << "]: " << message
                  << '\n';
        ++g_failures;
    }

    template <typename T>
    static bool check_value(T actual, T expected, const char* label,
                            const std::string& where) {
        if (actual != expected) {
            fail(std::string(label) + " actual=" + std::to_string(actual) +
                     " expected=" + std::to_string(expected),
                 where);
            return false;
        }
        return true;
    }

    std::size_t class_size(std::size_t size, std::size_t alignment) const {
        std::size_t required = std::max(size, alignment);
        required = std::max(required, config_.min_block_size);
        std::size_t power = 1;
        while (power < required) {
            power <<= 1;
        }
        return power;
    }

    void retire_expired(DomainId domain) {
        auto it = queues_.find(domain);
        if (it == queues_.end()) {
            return;
        }
        while (!it->second.empty() &&
               epoch_ > it->second.front().free_epoch +
                            config_.quarantine_ops) {
            retire_front(domain);
        }
    }

    void retire_front(DomainId domain) {
        auto& queue = queues_[domain];
        ModelQueueEntry entry = queue.front();
        queue.pop_front();
        queue_bytes_[domain] -= entry.bytes;
        records_.erase(entry.ptr);
    }

    AllocatorConfig config_;
    std::size_t page_size_ = 0;
    std::uint64_t epoch_ = 0;
    std::unordered_map<std::uintptr_t, ModelRecord> records_;
    std::unordered_map<DomainId, std::deque<ModelQueueEntry>> queues_;
    std::unordered_map<DomainId, std::size_t> queue_bytes_;
    std::unordered_map<std::uintptr_t, DomainId> small_history_domain_;
    std::unordered_map<std::uintptr_t, std::size_t> small_history_span_;
    std::set<DomainId> domains_;
};

struct Request {
    std::size_t size = 0;
    std::size_t alignment = 1;
};

Request random_request(std::mt19937_64& rng, const AllocatorConfig& config,
                       std::size_t page_size) {
    std::array<std::size_t, 5> alignments = {
        1, 8, 16, 32, std::max<std::size_t>(64, page_size)};
    Request request;
    const std::uint64_t bucket = rng() % 100;
    if (bucket < 60) {
        request.size = 1 + static_cast<std::size_t>(rng() % 512);
    } else if (bucket < 85) {
        request.size =
            1 + static_cast<std::size_t>(rng() % config.large_threshold);
    } else {
        request.size = config.large_threshold + 1 +
                       static_cast<std::size_t>(rng() % (8 * page_size));
    }
    request.alignment = alignments[static_cast<std::size_t>(
        rng() % alignments.size())];
    if (rng() % 17 == 0) {
        request.alignment = page_size * (rng() % 2 == 0 ? 2 : 4);
    }
    return request;
}

void run_random_case(std::uint64_t seed, std::size_t operations) {
    const std::size_t page = PartitionedAllocator::PageSize();
    AllocatorConfig config = random_config();
    PartitionedAllocator allocator(config);
    ReferenceModel model(config, page);
    std::mt19937_64 rng(seed);
    std::vector<void*> live;
    const std::string where = "seed " + std::to_string(seed);

    for (std::size_t op = 0; op < operations; ++op) {
        const bool do_allocate =
            live.empty() || (rng() % 100 < 55);

        if (do_allocate) {
            const DomainId domain = static_cast<DomainId>(1 + rng() % 7);
            const Request request = random_request(rng, config, page);
            model.expect_allocation(domain);
            void* ptr =
                allocator.Allocate(request.size, request.alignment, domain);
            if (ptr == nullptr) {
                std::cerr << "allocation unexpectedly failed in " << where
                          << '\n';
                ++g_failures;
                continue;
            }
            CHECK(allocator.PageOwner(ptr) == domain);
            model.add_allocation(ptr, domain, request.size,
                                 request.alignment, allocator.KindOf(ptr),
                                 where);
            live.push_back(ptr);
        } else {
            const std::size_t index =
                static_cast<std::size_t>(rng() % live.size());
            void* ptr = live[index];
            live[index] = live.back();
            live.pop_back();

            const std::uintptr_t address =
                reinterpret_cast<std::uintptr_t>(ptr);
            auto record = model.records().find(address);
            if (record == model.records().end()) {
                std::cerr << "model lost a live record in " << where << '\n';
                ++g_failures;
                continue;
            }
            const ModelRecord saved = record->second;

            CHECK(model.deallocate(ptr, where));
            CHECK(allocator.Deallocate(ptr));

            // Probe the exact post-free state: with a non-empty queue the
            // just-freed block must still be quarantined.
            CHECK(model.is_quarantined(address));
            CHECK(allocator.IsQuarantined(ptr));
            CHECK(!allocator.IsAllocated(ptr));

            // Frequently demand exactly the same shape from the same domain.
            // The allocation must not return a quarantined address.
            if (rng() % 4 == 0) {
                const DomainId domain = saved.domain;
                const Request request{saved.size, saved.alignment};
                model.expect_allocation(domain);
                void* probe =
                    allocator.Allocate(request.size, request.alignment, domain);
                if (probe == nullptr) {
                    std::cerr << "probe allocation failed in " << where
                              << '\n';
                    ++g_failures;
                } else {
                    CHECK(probe != ptr);
                    CHECK(model.add_allocation(
                        probe, domain, request.size, request.alignment,
                        allocator.KindOf(probe), where));
                    live.push_back(probe);
                }
            }
        }

        if (op % 4 == 0) {
            model.check_stats(allocator, where);
            model.check_ownership(allocator, where);
        }
    }

    for (void* ptr : live) {
        const std::uintptr_t address =
            reinterpret_cast<std::uintptr_t>(ptr);
        auto record = model.records().find(address);
        if (record == model.records().end()) {
            std::cerr << "model lost a final live record in " << where << '\n';
            ++g_failures;
            continue;
        }
        CHECK(model.deallocate(ptr, where));
        CHECK(allocator.Deallocate(ptr));
    }
    model.check_stats(allocator, where);
    model.check_ownership(allocator, where);
}

void test_allocation_alignment() {
    PartitionedAllocator allocator(common_config());
    const std::size_t page = PartitionedAllocator::PageSize();
    const std::array<std::size_t, 8> alignments = {
        1, 8, 16, 32, 64, 128, page, 2 * page};
    for (std::size_t alignment : alignments) {
        void* ptr = allocator.Allocate(37, alignment, 4);
        CHECK(ptr != nullptr);
        if (ptr == nullptr) {
            continue;
        }
        CHECK(reinterpret_cast<std::uintptr_t>(ptr) % alignment == 0);
        CHECK(allocator.PageOwner(ptr) == 4);
        CHECK(allocator.Deallocate(ptr));
    }
}

}  // namespace

int main() {
    test_invalid_arguments();
    test_cross_domain_page_isolation();
    test_quarantine_age_and_immediate_reallocation();
    test_quarantine_capacity_evicts_oldest_only();
    test_freed_blocks_do_not_cross_domains();
    test_large_mmap_ownership();
    test_allocation_alignment();

    for (std::uint64_t seed = 1; seed <= 4; ++seed) {
        run_random_case(seed, 1200);
    }

    if (g_failures != 0) {
        std::cerr << g_failures << " check(s) failed\n";
        return 1;
    }
    std::cout << "all allocator checks passed (page size "
              << PartitionedAllocator::PageSize() << ")\n";
    return 0;
}
