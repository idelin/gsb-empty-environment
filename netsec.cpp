#include "netsec.hpp"

#include <algorithm>
#include <atomic>
#include <cstring>
#include <list>
#include <set>
#include <stdexcept>

namespace netsec {
namespace {

struct PageKey {
    VersionId version;
    std::uint64_t page_index;

    bool operator==(const PageKey& other) const noexcept {
        return version == other.version && page_index == other.page_index;
    }
};

struct PageKeyHash {
    std::size_t operator()(const PageKey& key) const noexcept {
        return std::hash<VersionId>{}(key.version) ^
               (std::hash<std::uint64_t>{}(key.page_index) << 1);
    }
};

// Immutable contents of one fetched page. Shared between the cache and every
// lease that pinned it; shared_ptr ownership is what keeps bytes valid across
// eviction, cancellation and version replacement.
struct PageBlock {
    VersionId version = 0;
    std::uint64_t page_index = 0;
    std::vector<std::byte> data;
    std::size_t valid_bytes = 0; // Tail pages may be shorter than page_size.
};

struct VersionState {
    VersionId id = 0;
    std::uint64_t length = 0;
    std::shared_ptr<IPageReader> reader;
};

struct LeaseRegistry {
    std::mutex mutex;
    std::set<Lease::Impl*> leases;
};

// Process-wide (per-volume) LRU cache of fetched pages.
class PageCache {
public:
    explicit PageCache(std::size_t capacity) : capacity_(std::max<std::size_t>(1, capacity)) {}

    struct Fetch {
        ReadOutcome outcome = ReadOutcome::ReadError;
        std::shared_ptr<PageBlock> block; // Non-null iff outcome == Complete.
    };

    // Returns the cached block or loads it through the version's reader.
    // A loaded block is inserted into the cache; eviction only drops the
    // cache's reference, never blocks pinned by a lease.
    Fetch getOrLoad(const std::shared_ptr<VersionState>& version,
                    std::uint64_t page_index,
                    std::size_t page_size) {
        const PageKey key{version->id, page_index};
        const std::uint64_t page_start = page_index * page_size;

        {
            std::lock_guard<std::mutex> lock(mutex_);
            auto found = entries_.find(key);
            if (found != entries_.end()) {
                touch(found);
                return {ReadOutcome::Complete, found->second.block};
            }
        }

        if (page_start >= version->length) {
            return {ReadOutcome::Truncated, nullptr}; // File shortened past here.
        }

        const std::uint64_t remaining = version->length - page_start;
        const std::size_t want = static_cast<std::size_t>(
            std::min<std::uint64_t>(page_size, remaining));

        auto block = std::make_shared<PageBlock>();
        block->version = version->id;
        block->page_index = page_index;
        block->data.resize(want);

        const PageRead pr = version->reader->readPage(
            version->id, page_start, block->data.data(), want);
        if (pr.status != PageStatus::Ok || pr.bytes == 0) {
            return {pr.status == PageStatus::IoError ? ReadOutcome::ReadError
                                                      : ReadOutcome::Truncated,
                    nullptr};
        }
        block->valid_bytes = std::min(pr.bytes, want);
        block->data.resize(block->valid_bytes);

        std::lock_guard<std::mutex> lock(mutex_);
        auto raced = entries_.find(key);
        if (raced != entries_.end()) {
            touch(raced);
            return {ReadOutcome::Complete, raced->second.block};
        }
        while (lru_.size() >= capacity_) {
            const PageKey victim = lru_.front();
            lru_.pop_front();
            entries_.erase(victim);
        }
        auto iter = lru_.insert(lru_.end(), key);
        entries_.emplace(key, Entry{block, iter});
        return {ReadOutcome::Complete, block};
    }

    std::size_t size() const {
        std::lock_guard<std::mutex> lock(mutex_);
        return entries_.size();
    }

private:
    struct Entry {
        std::shared_ptr<PageBlock> block;
        std::list<PageKey>::iterator lru_iter;
    };

    void touch(std::unordered_map<PageKey, Entry, PageKeyHash>::iterator iter) {
        lru_.splice(lru_.end(), lru_, iter->second.lru_iter);
    }

    mutable std::mutex mutex_;
    std::size_t capacity_;
    std::list<PageKey> lru_;
    std::unordered_map<PageKey, Entry, PageKeyHash> entries_;
};

} // namespace

class Lease::Impl {
public:
    Impl(std::shared_ptr<PageCache> cache,
         std::shared_ptr<VersionState> version,
         std::shared_ptr<LeaseRegistry> registry,
         std::uint64_t start,
         std::uint64_t end,
         std::size_t page_size)
        : cache_(std::move(cache)),
          version_(std::move(version)),
          registry_(std::move(registry)),
          start_(start),
          end_(end),
          page_size_(page_size) {
        std::lock_guard<std::mutex> lock(registry_->mutex);
        registry_->leases.insert(this);
    }

    ~Impl() {
        std::lock_guard<std::mutex> lock(registry_->mutex);
        registry_->leases.erase(this);
    }

    ReadResult read(std::uint64_t offset, void* dst, std::size_t len) {
        if (len == 0 || dst == nullptr) {
            return {ReadOutcome::BadRange, 0};
        }
        if (offset < start_ || offset >= end_) {
            return {ReadOutcome::BadRange, 0};
        }

        auto* out = static_cast<std::byte*>(dst);
        std::size_t done = 0;

        while (done < len) {
            // Fast path for leases cancelled before/after the last page:
            // no backing I/O is issued and the prefix stays untouched.
            if (cancelled_.load(std::memory_order_acquire)) {
                return {ReadOutcome::Cancelled, done};
            }

            const std::uint64_t cur = offset + done;
            const std::uint64_t page_index = cur / page_size_;
            const std::size_t page_offset =
                static_cast<std::size_t>(cur % page_size_);

            std::shared_ptr<PageBlock> block_ptr;
            {
                std::lock_guard<std::mutex> lock(pin_mutex_);
                auto pinned = pins_.find(PageKey{version_->id, page_index});
                if (pinned != pins_.end()) {
                    block_ptr = pinned->second;
                }
            }

            PageCache::Fetch fetched =
                block_ptr
                    ? PageCache::Fetch{ReadOutcome::Complete, block_ptr}
                    : cache_->getOrLoad(version_, page_index, page_size_);
            if (fetched.outcome != ReadOutcome::Complete) {
                // A cancel that landed while fetching still reports the
                // precise prefix completed before this page boundary.
                if (cancelled_.load(std::memory_order_acquire))
                    return {ReadOutcome::Cancelled, done};
                // Exact prefix: everything copied before this failed page.
                return {fetched.outcome, done};
            }

            // Pin for the lease lifetime so later eviction cannot reclaim
            // bytes the parser may still reach through this lease.
            {
                std::lock_guard<std::mutex> lock(pin_mutex_);
                pins_.emplace(PageKey{version_->id, page_index}, fetched.block);
            }

            const PageBlock& block = *fetched.block;
            if (page_offset >= block.valid_bytes ||
                cur >= version_->length) {
                // Valid tail ends inside this page: the rest is beyond EOF.
                return {ReadOutcome::Truncated, done};
            }
            const std::size_t avail = block.valid_bytes - page_offset;
            const std::size_t chunk = std::min(avail, len - done);
            std::memcpy(out + done, block.data.data() + page_offset, chunk);
            done += chunk;

            // Check after delivery: cancellation takes effect at the next
            // page boundary while preserving the exact completed prefix.
            if (cancelled_.load(std::memory_order_acquire)) {
                return {ReadOutcome::Cancelled, done};
            }
        }

        return {ReadOutcome::Complete, done};
    }

    void cancel() { cancelled_.store(true, std::memory_order_release); }
    bool cancelled() const {
        return cancelled_.load(std::memory_order_acquire);
    }

    VersionId version() const { return version_->id; }
    std::uint64_t start() const { return start_; }
    std::uint64_t end() const { return end_; }

private:
    std::shared_ptr<PageCache> cache_;
    std::shared_ptr<VersionState> version_;
    std::shared_ptr<LeaseRegistry> registry_;
    std::uint64_t start_;
    std::uint64_t end_;
    std::size_t page_size_;
    std::atomic<bool> cancelled_{false};

    std::mutex pin_mutex_;
    std::unordered_map<PageKey, std::shared_ptr<PageBlock>, PageKeyHash> pins_;
};

class VolumeImpl final : public Volume {
public:
    explicit VolumeImpl(const VolumeConfig& config)
        : page_size_(config.page_size == 0 ? 4096 : config.page_size),
          cache_(std::make_shared<PageCache>(config.cache_capacity_pages)),
          registry_(std::make_shared<LeaseRegistry>()) {
    }

    VersionId publishVersion(std::shared_ptr<IPageReader> reader,
                             std::uint64_t length) override {
        if (!reader) {
            throw std::invalid_argument("reader must not be null");
        }
        auto state = std::make_shared<VersionState>();
        {
            std::lock_guard<std::mutex> lock(mutex_);
            state->id = ++next_version_;
            state->length = length;
            state->reader = std::move(reader);
            versions_.emplace(state->id, state);
            current_ = state;
        }
        pruneStaleVersions();
        return state->id;
    }

    VersionId truncateTo(std::uint64_t length) override {
        std::shared_ptr<IPageReader> reader;
        {
            std::lock_guard<std::mutex> lock(mutex_);
            if (!current_) {
                throw std::logic_error("no version published");
            }
            reader = current_->reader;
        }
        return publishVersion(std::move(reader), length);
    }

    VersionId replaceWith(std::shared_ptr<IPageReader> reader,
                          std::uint64_t length) override {
        return publishVersion(std::move(reader), length);
    }

    Lease acquireLease(std::uint64_t start, std::uint64_t length) override {
        if (length == 0) {
            return Lease{};
        }
        std::shared_ptr<VersionState> state;
        {
            std::lock_guard<std::mutex> lock(mutex_);
            if (!current_) {
                return Lease{};
            }
            state = current_;
        }
        if (start > std::uint64_t(-1) - length) {
            return Lease{};
        }
        const std::uint64_t end = start + length;
        auto impl = std::make_shared<Lease::Impl>(cache_, std::move(state),
                                                  registry_, start, end,
                                                  page_size_);
        return Lease(std::move(impl));
    }

    VersionId currentVersion() const override {
        std::lock_guard<std::mutex> lock(mutex_);
        return current_ ? current_->id : 0;
    }

    std::size_t pageSize() const override { return page_size_; }

    std::size_t cachedPageCount() const override { return cache_->size(); }

    std::size_t liveLeaseCount() const override {
        std::lock_guard<std::mutex> lock(registry_->mutex);
        return registry_->leases.size();
    }

private:
    // Drops metadata for generations no lease can reach. Each live lease
    // holds a shared_ptr to its VersionState, so use_count reveals whether
    // the version map is the last owner; pinned pages stay alive regardless.
    void pruneStaleVersions() {
        std::lock_guard<std::mutex> lock(mutex_);
        for (auto it = versions_.begin(); it != versions_.end();) {
            if (it->second == current_ || it->second.use_count() > 1) {
                ++it;
            } else {
                it = versions_.erase(it);
            }
        }
    }

    std::size_t page_size_;
    std::shared_ptr<PageCache> cache_;
    std::shared_ptr<LeaseRegistry> registry_;

    mutable std::mutex mutex_;
    VersionId next_version_ = 0;
    std::shared_ptr<VersionState> current_;
    std::unordered_map<VersionId, std::shared_ptr<VersionState>> versions_;
};

// --- Lease facade -----------------------------------------------------------

Lease::Lease(std::shared_ptr<Impl> impl) noexcept : impl_(std::move(impl)) {}

Lease::~Lease() = default;

Lease::Lease(Lease&& other) noexcept : impl_(std::move(other.impl_)) {}

Lease& Lease::operator=(Lease&& other) noexcept {
    if (this != &other) {
        impl_ = std::move(other.impl_);
    }
    return *this;
}

ReadResult Lease::read(std::uint64_t offset, void* dst, std::size_t len) {
    if (!impl_) {
        return {ReadOutcome::Expired, 0};
    }
    return impl_->read(offset, dst, len);
}

void Lease::cancel() {
    if (impl_) {
        impl_->cancel();
    }
}

bool Lease::cancelled() const { return impl_ && impl_->cancelled(); }

bool Lease::valid() const { return static_cast<bool>(impl_); }

void Lease::release() { impl_.reset(); }

VersionId Lease::version() const { return impl_ ? impl_->version() : 0; }

std::uint64_t Lease::start() const { return impl_ ? impl_->start() : 0; }

std::uint64_t Lease::end() const { return impl_ ? impl_->end() : 0; }

std::unique_ptr<Volume> makeVolume(const VolumeConfig& config) {
    return std::unique_ptr<Volume>(new VolumeImpl(config));
}

} // namespace netsec
