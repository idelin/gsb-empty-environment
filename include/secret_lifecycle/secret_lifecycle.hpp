#pragma once

#include <algorithm>
#include <cstddef>
#include <cstdint>
#include <cstring>
#include <functional>
#include <limits>
#include <map>
#include <memory>
#include <set>
#include <span>
#include <stdexcept>
#include <string>
#include <unordered_map>
#include <utility>
#include <variant>
#include <vector>

namespace secret_lifecycle {

using PageId = std::uint64_t;
using GenerationId = std::uint64_t;
using ScratchId = std::uint64_t;
using EraseId = std::uint64_t;

inline constexpr PageId invalid_page_id = 0;
inline constexpr GenerationId invalid_generation = 0;

struct ByteRange {
    std::uint64_t offset = 0;
    std::uint64_t size = 0;

    [[nodiscard]] std::uint64_t end() const noexcept { return offset + size; }
    friend bool operator==(const ByteRange&, const ByteRange&) = default;
};

struct PageAddress {
    PageId allocation_generation = invalid_page_id;
    std::uint64_t address = 0;

    friend bool operator==(const PageAddress&, const PageAddress&) = default;
    friend bool operator<(const PageAddress& left, const PageAddress& right) noexcept {
        if (left.address != right.address) return left.address < right.address;
        return left.allocation_generation < right.allocation_generation;
    }
};

class SecretLifecycleError : public std::runtime_error {
public:
    explicit SecretLifecycleError(const std::string& message) : std::runtime_error(message) {}
};

class BorrowRejected : public SecretLifecycleError {
public:
    using SecretLifecycleError::SecretLifecycleError;
};

class InjectedScratchFailure : public SecretLifecycleError {
public:
    ScratchId scratch_id = 0;
    explicit InjectedScratchFailure(ScratchId id)
        : SecretLifecycleError("injected scratch copy failure"), scratch_id(id) {}
};

class InjectedAllocationFailure : public SecretLifecycleError {
public:
    explicit InjectedAllocationFailure(const std::string& message)
        : SecretLifecycleError(message) {}
};

struct AllocationFailurePolicy {
    std::size_t fail_after_allocations = std::numeric_limits<std::size_t>::max();
    std::size_t fail_after_scratch_copies = std::numeric_limits<std::size_t>::max();
    bool fail_next_scratch_copy = false;

    void reset() noexcept {
        fail_after_allocations = std::numeric_limits<std::size_t>::max();
        fail_after_scratch_copies = std::numeric_limits<std::size_t>::max();
        fail_next_scratch_copy = false;
    }
};

class IPageAllocator {
public:
    virtual ~IPageAllocator() = default;
    [[nodiscard]] virtual std::size_t page_size() const = 0;
    [[nodiscard]] virtual std::span<std::byte> resolve(const PageAddress& page) = 0;
    virtual PageAddress allocate_page() = 0;
    virtual void free_page(const PageAddress& page) = 0;
    virtual ScratchId make_scratch_copy(const PageAddress& source) = 0;
    [[nodiscard]] virtual std::span<const std::byte> scratch_span(ScratchId id) const = 0;
    virtual void free_scratch(ScratchId id) = 0;
};

class IErasableAllocator {
public:
    virtual ~IErasableAllocator() = default;
    [[nodiscard]] virtual std::span<std::byte> erase_target(const PageAddress& page) = 0;
    [[nodiscard]] virtual std::span<std::byte> erase_scratch(ScratchId id) = 0;
};

struct EraseRequest {
    EraseId id = 0;
    PageAddress page{};
    ScratchId scratch_id = 0;
    ByteRange range{};
    bool scratch = false;

    friend bool operator==(const EraseRequest&, const EraseRequest&) = default;
};

struct EraseReceipt {
    EraseId id = 0;
    PageAddress page{};
    ScratchId scratch_id = 0;
    ByteRange range{};
    bool scratch = false;

    friend bool operator==(const EraseReceipt&, const EraseReceipt&) = default;
};

using EraseCallback = std::function<void(const EraseReceipt&)>;

class IEraser {
public:
    virtual ~IEraser() = default;
    virtual EraseReceipt erase(const EraseRequest& request, IErasableAllocator& allocator) = 0;
    virtual void erase_async(const EraseRequest& request, IErasableAllocator& allocator,
                             EraseCallback callback) = 0;
};

class TrackingPageAllocator final : public IPageAllocator, public IErasableAllocator {
public:
    struct PageRecord {
        PageAddress address{};
        std::vector<std::byte> bytes;
        bool live = true;
        std::size_t allocation_sequence = 0;
    };

    struct ScratchRecord {
        ScratchId id = 0;
        PageAddress source{};
        std::vector<std::byte> bytes;
        bool live = true;
    };

    explicit TrackingPageAllocator(std::size_t size = 4096) : page_size_(size) {}

    [[nodiscard]] std::size_t page_size() const override { return page_size_; }

    PageAddress allocate_page() override {
        ++allocation_count_;
        if (allocation_count_ > failure_policy_.fail_after_allocations) {
            throw InjectedAllocationFailure("injected page allocation failure");
        }

        std::uint64_t address = 0;
        if (!free_addresses_.empty()) {
            address = *free_addresses_.begin();
            free_addresses_.erase(free_addresses_.begin());
        } else {
            address = static_cast<std::uint64_t>(next_address_++);
        }

        ++epoch_by_address_[address];
        PageAddress result{epoch_by_address_[address], address};
        pages_[address].push_back(
            PageRecord{result, std::vector<std::byte>(page_size_), true, allocation_count_});
        return result;
    }

    void free_page(const PageAddress& page) override {
        auto it = pages_.find(page.address);
        if (it == pages_.end()) return;
        for (auto& record : it->second) {
            if (record.address == page && record.live) {
                record.live = false;
                free_addresses_.insert(page.address);
                return;
            }
        }
    }

    ScratchId make_scratch_copy(const PageAddress& source) override {
        ++scratch_count_;
        auto source_span = resolve(source);
        ScratchId id = next_scratch_id_++;
        scratch_[id] = ScratchRecord{
            id, source, std::vector<std::byte>(source_span.begin(), source_span.end()), true};
        if (failure_policy_.fail_next_scratch_copy ||
            scratch_count_ > failure_policy_.fail_after_scratch_copies) {
            failure_policy_.fail_next_scratch_copy = false;
            throw InjectedScratchFailure(id);
        }
        return id;
    }

    [[nodiscard]] std::span<const std::byte> scratch_span(ScratchId id) const override {
        return scratch_.at(id).bytes;
    }

    [[nodiscard]] bool has_scratch(ScratchId id) const noexcept {
        return scratch_.find(id) != scratch_.end();
    }

    void free_scratch(ScratchId id) override { scratch_.erase(id); }

    [[nodiscard]] std::span<std::byte> resolve(const PageAddress& page) override {
        return find(page).bytes;
    }

    [[nodiscard]] std::span<std::byte> erase_target(const PageAddress& page) override {
        return find(page).bytes;
    }

    [[nodiscard]] std::span<std::byte> erase_scratch(ScratchId id) override {
        return scratch_.at(id).bytes;
    }

    AllocationFailurePolicy& failure_policy() noexcept { return failure_policy_; }
    [[nodiscard]] std::size_t allocation_count() const noexcept { return allocation_count_; }
    [[nodiscard]] std::size_t scratch_count() const noexcept { return scratch_count_; }
    [[nodiscard]] const std::map<std::uint64_t, std::vector<PageRecord>>& pages() const noexcept {
        return pages_;
    }
    [[nodiscard]] const std::map<ScratchId, ScratchRecord>& scratches() const noexcept {
        return scratch_;
    }

    [[nodiscard]] std::vector<std::byte> snapshot(const PageAddress& page) const {
        return find(page).bytes;
    }

    [[nodiscard]] bool is_zero(const PageAddress& page, const ByteRange& range) const {
        const auto& bytes = find(page).bytes;
        if (range.end() > page_size_) throw SecretLifecycleError("erase range outside page");
        return std::all_of(bytes.begin() + static_cast<std::ptrdiff_t>(range.offset),
                           bytes.begin() + static_cast<std::ptrdiff_t>(range.end()),
                           [](std::byte value) { return value == std::byte{}; });
    }

    [[nodiscard]] bool all_live_secret_memory_zero() const {
        for (const auto& [address, records] : pages_) {
            (void)address;
            for (const auto& record : records) {
                if (!record.live) continue;
                if (std::any_of(record.bytes.begin(), record.bytes.end(),
                                [](std::byte value) { return value != std::byte{}; })) {
                    return false;
                }
            }
        }
        for (const auto& [id, record] : scratch_) {
            (void)id;
            if (std::any_of(record.bytes.begin(), record.bytes.end(),
                            [](std::byte value) { return value != std::byte{}; })) {
                return false;
            }
        }
        return true;
    }

private:
    [[nodiscard]] PageRecord& find(const PageAddress& page) {
        auto& records = pages_.at(page.address);
        for (auto& record : records) {
            if (record.address == page) return record;
        }
        throw SecretLifecycleError("unknown page address");
    }

    [[nodiscard]] const PageRecord& find(const PageAddress& page) const {
        const auto& records = pages_.at(page.address);
        for (const auto& record : records) {
            if (record.address == page) return record;
        }
        throw SecretLifecycleError("unknown page address");
    }

    std::size_t page_size_;
    std::size_t allocation_count_ = 0;
    std::size_t scratch_count_ = 0;
    std::uint64_t next_address_ = 1;
    ScratchId next_scratch_id_ = 1;
    std::set<std::uint64_t> free_addresses_;
    std::map<std::uint64_t, PageId> epoch_by_address_;
    std::map<std::uint64_t, std::vector<PageRecord>> pages_;
    std::map<ScratchId, ScratchRecord> scratch_;
    AllocationFailurePolicy failure_policy_;
};

class PausableEraser final : public IEraser {
public:
    struct PendingRequest {
        EraseRequest request;
        EraseCallback callback;
    };

    EraseReceipt erase(const EraseRequest& request, IErasableAllocator& allocator) override {
        zero(request, allocator);
        ++completion_count_;
        return {request.id, request.page, request.scratch_id, request.range, request.scratch};
    }

    void erase_async(const EraseRequest& request, IErasableAllocator& allocator,
                     EraseCallback callback) override {
        ++received_count_;
        if (paused_) {
            pending_.push_back(PendingRequest{request, std::move(callback)});
            return;
        }
        complete(PendingRequest{request, std::move(callback)}, allocator);
    }

    void pause() noexcept { paused_ = true; }

    void resume(IErasableAllocator& allocator) {
        paused_ = false;
        auto pending = std::move(pending_);
        pending_.clear();
        for (auto& item : pending) complete(item, allocator);
    }

    void deliver_late_or_duplicate(const EraseReceipt& receipt,
                                   const EraseCallback& callback) const {
        if (callback) callback(receipt);
    }

    [[nodiscard]] bool paused() const noexcept { return paused_; }
    [[nodiscard]] std::size_t pending_count() const noexcept { return pending_.size(); }
    [[nodiscard]] std::size_t received_count() const noexcept { return received_count_; }
    [[nodiscard]] std::size_t completion_count() const noexcept { return completion_count_; }

private:
    void complete(const PendingRequest& item, IErasableAllocator& allocator) {
        zero(item.request, allocator);
        EraseReceipt receipt{item.request.id, item.request.page, item.request.scratch_id,
                            item.request.range, item.request.scratch};
        ++completion_count_;
        if (item.callback) item.callback(receipt);
    }

    static void zero(const EraseRequest& request, IErasableAllocator& allocator) {
        auto target = request.scratch ? allocator.erase_scratch(request.scratch_id)
                                      : allocator.erase_target(request.page);
        if (request.range.end() > target.size()) {
            throw SecretLifecycleError("erase range outside target");
        }
        volatile std::byte* begin = target.data() + request.range.offset;
        volatile std::byte* end = target.data() + request.range.end();
        for (; begin != end; ++begin) *begin = std::byte{};
    }

    bool paused_ = false;
    std::size_t received_count_ = 0;
    std::size_t completion_count_ = 0;
    std::vector<PendingRequest> pending_;
};

struct GenerationAllocated {
    GenerationId generation = invalid_generation;
    ByteRange logical_range;
    std::uint64_t version = 0;
};

struct PageExtentEvent {
    PageAddress page;
    ByteRange page_range;
    GenerationId generation = invalid_generation;
};

struct ScratchCopied {
    ScratchId scratch_id = 0;
    PageAddress source;
    std::vector<PageExtentEvent> extents;
};

struct GenerationWritten {
    GenerationId generation = invalid_generation;
    ByteRange logical_range;
    std::uint64_t version = 0;
    std::vector<PageExtentEvent> physical_extents;
    std::vector<ScratchCopied> scratch_copies;
};

struct BorrowOpened {
    GenerationId generation = invalid_generation;
    std::uint64_t barrier = 0;
    std::uint64_t version = 0;
    ByteRange logical_range;
};

struct BorrowClosed {
    GenerationId generation = invalid_generation;
    std::uint64_t barrier = 0;
};

struct GenerationDestroyRequested {
    GenerationId generation = invalid_generation;
    std::uint64_t borrow_barrier = 0;
};

struct ExtentErased {
    GenerationId generation = invalid_generation;
    EraseReceipt receipt;
};

struct ScratchErased {
    GenerationId generation = invalid_generation;
    ScratchId scratch_id = 0;
    EraseReceipt receipt;
};

struct OverwriteRetired {
    GenerationId generation = invalid_generation;
    PageAddress page;
    ByteRange page_range;
};

struct LogicalIntervalRetired {
    GenerationId generation = invalid_generation;
    ByteRange logical_range;
};

using SecretEvent = std::variant<GenerationAllocated, GenerationWritten, BorrowOpened, BorrowClosed,
                                 GenerationDestroyRequested, ExtentErased, ScratchErased,
                                 OverwriteRetired, LogicalIntervalRetired>;

struct DestructionProof {
    GenerationId generation = invalid_generation;
    std::vector<ByteRange> logical_intervals;
    std::uint64_t borrow_barrier = 0;
    std::vector<EraseReceipt> physical_receipts;
    std::vector<EraseReceipt> scratch_receipts;
};

class EventAcceptor {
public:
    void observe(const SecretEvent& event) {
        std::visit([this](const auto& value) { this->handle(value); }, event);
    }

    [[nodiscard]] bool accept(const DestructionProof& proof) const {
        if (proof.generation == invalid_generation) return false;
        auto request_it = destroy_requests_.find(proof.generation);
        if (request_it == destroy_requests_.end()) return false;
        if (request_it->second != proof.borrow_barrier) return false;

        auto interval_it = logical_intervals_.find(proof.generation);
        if (interval_it == logical_intervals_.end() || interval_it->second.empty()) return false;
        if (!same_intervals(interval_it->second, proof.logical_intervals)) return false;

        auto expected_it = active_extents_.find(proof.generation);
        if (expected_it == active_extents_.end() || expected_it->second.empty()) return false;
        if (!receipts_cover_extents(expected_it->second, proof.physical_receipts)) return false;

        auto scratch_it = scratch_receipts_.find(proof.generation);
        std::vector<EraseReceipt> expected_scratch;
        if (scratch_it != scratch_receipts_.end()) expected_scratch = scratch_it->second;
        if (!same_receipts(expected_scratch, proof.scratch_receipts)) return false;

        for (const auto& receipt : proof.physical_receipts) {
            if (!receipt_matches_generation(receipt, proof.generation) || receipt.scratch) {
                return false;
            }
        }
        for (const auto& receipt : proof.scratch_receipts) {
            if (!receipt_matches_generation(receipt, proof.generation) || !receipt.scratch) {
                return false;
            }
        }
        return true;
    }

private:
    struct ExtentKey {
        PageAddress page;
        ByteRange range;

        friend bool operator<(const ExtentKey& left, const ExtentKey& right) {
            if (left.page < right.page) return true;
            if (right.page < left.page) return false;
            if (left.range.offset != right.range.offset) {
                return left.range.offset < right.range.offset;
            }
            return left.range.size < right.range.size;
        }
    };

    void handle(const GenerationAllocated& event) {
        add_interval(event.generation, event.logical_range);
    }

    void handle(const GenerationWritten& event) {
        add_interval(event.generation, event.logical_range);
        for (const auto& extent : event.physical_extents) {
            active_extents_[event.generation].insert(ExtentKey{extent.page, extent.page_range});
        }
        for (const auto& scratch : event.scratch_copies) {
            for (const auto& extent : scratch.extents) {
                pending_scratch_[extent.generation][scratch.scratch_id].push_back(extent);
            }
        }
    }

    void handle(const BorrowOpened&) {}
    void handle(const BorrowClosed&) {}

    void handle(const GenerationDestroyRequested& event) {
        destroy_requests_[event.generation] = event.borrow_barrier;
    }

    void handle(const ExtentErased& event) {
        observed_receipts_[event.receipt.id] = ReceiptOwner{event.generation, event.receipt};
    }

    void handle(const ScratchErased& event) {
        auto pending = pending_scratch_.find(event.generation);
        if (pending == pending_scratch_.end() ||
            pending->second.find(event.scratch_id) == pending->second.end()) {
            return;
        }
        pending->second.erase(event.scratch_id);
        if (pending->second.empty()) pending_scratch_.erase(pending);
        scratch_receipts_[event.generation].push_back(event.receipt);
        observed_receipts_[event.receipt.id] = ReceiptOwner{event.generation, event.receipt};
    }

    void handle(const OverwriteRetired& event) {
        auto it = active_extents_.find(event.generation);
        if (it == active_extents_.end()) return;
        std::set<ExtentKey> updated;
        for (const auto& key : it->second) {
            if (key.page != event.page) {
                updated.insert(key);
                continue;
            }
            if (event.page_range.end() <= key.range.offset ||
                event.page_range.offset >= key.range.end()) {
                updated.insert(key);
                continue;
            }
            if (event.page_range.offset > key.range.offset) {
                updated.insert(ExtentKey{
                    key.page,
                    ByteRange{key.range.offset,
                              event.page_range.offset - key.range.offset}});
            }
            if (event.page_range.end() < key.range.end()) {
                updated.insert(ExtentKey{
                    key.page,
                    ByteRange{event.page_range.end(),
                              key.range.end() - event.page_range.end()}});
            }
        }
        it->second = std::move(updated);
    }

    void handle(const LogicalIntervalRetired& event) {
        auto it = logical_intervals_.find(event.generation);
        if (it == logical_intervals_.end()) return;
        it->second = subtract_ranges(it->second, event.logical_range);
    }

    void add_interval(GenerationId generation, ByteRange range) {
        auto& intervals = logical_intervals_[generation];
        intervals.push_back(range);
        intervals = normalize(intervals);
    }

    [[nodiscard]] bool receipt_matches_generation(const EraseReceipt& receipt,
                                                   GenerationId generation) const {
        auto it = observed_receipts_.find(receipt.id);
        if (it == observed_receipts_.end()) return false;
        return it->second.generation == generation && it->second.receipt == receipt;
    }

    [[nodiscard]] static bool covers(const ByteRange& outer, const ByteRange& inner) {
        return outer.offset <= inner.offset && inner.end() <= outer.end();
    }

    [[nodiscard]] static std::vector<ByteRange> normalize(std::vector<ByteRange> ranges) {
        std::sort(ranges.begin(), ranges.end(), [](const ByteRange& left, const ByteRange& right) {
            if (left.offset != right.offset) return left.offset < right.offset;
            return left.size < right.size;
        });
        std::vector<ByteRange> result;
        for (const auto& range : ranges) {
            if (result.empty() || range.offset > result.back().end()) {
                result.push_back(range);
            } else if (range.end() > result.back().end()) {
                result.back().size = range.end() - result.back().offset;
            }
        }
        return result;
    }

    [[nodiscard]] static bool same_intervals(const std::vector<ByteRange>& expected,
                                             const std::vector<ByteRange>& actual) {
        return normalize(expected) == normalize(actual);
    }

    [[nodiscard]] static std::vector<ByteRange> subtract_ranges(
        std::vector<ByteRange> ranges, const ByteRange& removed) {
        std::vector<ByteRange> result;
        for (const auto& range : normalize(ranges)) {
            if (removed.end() <= range.offset || removed.offset >= range.end()) {
                result.push_back(range);
                continue;
            }
            if (removed.offset > range.offset) {
                result.push_back(ByteRange{range.offset, removed.offset - range.offset});
            }
            if (removed.end() < range.end()) {
                result.push_back(ByteRange{removed.end(), range.end() - removed.end()});
            }
        }
        return result;
    }

    [[nodiscard]] static bool same_receipts(const std::vector<EraseReceipt>& expected,
                                            const std::vector<EraseReceipt>& actual) {
        if (expected.size() != actual.size()) return false;
        auto left = expected;
        auto right = actual;
        auto less = [](const EraseReceipt& a, const EraseReceipt& b) { return a.id < b.id; };
        std::sort(left.begin(), left.end(), less);
        std::sort(right.begin(), right.end(), less);
        return left == right;
    }

    [[nodiscard]] static bool receipts_cover_extents(
        const std::set<ExtentKey>& expected, const std::vector<EraseReceipt>& receipts) {
        std::map<PageAddress, std::vector<ByteRange>> by_page;
        for (const auto& receipt : receipts) {
            if (receipt.scratch) return false;
            by_page[receipt.page].push_back(receipt.range);
        }
        for (const auto& key : expected) {
            auto it = by_page.find(key.page);
            if (it == by_page.end()) return false;
            bool covered = false;
            for (const auto& range : normalize(it->second)) {
                if (covers(range, key.range)) {
                    covered = true;
                    break;
                }
            }
            if (!covered) return false;
        }
        return true;
    }

    struct ReceiptOwner {
        GenerationId generation = invalid_generation;
        EraseReceipt receipt;
    };

    std::map<GenerationId, std::vector<ByteRange>> logical_intervals_;
    std::map<GenerationId, std::set<ExtentKey>> active_extents_;
    std::map<GenerationId, std::uint64_t> destroy_requests_;
    std::map<GenerationId, std::vector<EraseReceipt>> scratch_receipts_;
    std::map<GenerationId, std::map<ScratchId, std::vector<PageExtentEvent>>> pending_scratch_;
    std::map<EraseId, ReceiptOwner> observed_receipts_;
};

class SecretHost {
public:
    SecretHost(IPageAllocator& allocator, IErasableAllocator& erasable, IEraser& eraser)
        : allocator_(allocator), erasable_(erasable), eraser_(eraser) {}

    SecretHost(const SecretHost&) = delete;
    SecretHost& operator=(const SecretHost&) = delete;

    class ReadView {
    public:
        ReadView() = default;
        ReadView(const ReadView&) = delete;
        ReadView& operator=(const ReadView&) = delete;

        ReadView(ReadView&& other) noexcept
            : host_(other.host_),
              barrier_(other.barrier_),
              generations_(std::move(other.generations_)),
              pages_(std::move(other.pages_)),
              chunks_(std::move(other.chunks_)) {
            other.host_ = nullptr;
        }

        ReadView& operator=(ReadView&& other) noexcept {
            if (this != &other) {
                release();
                host_ = other.host_;
                barrier_ = other.barrier_;
                generations_ = std::move(other.generations_);
                pages_ = std::move(other.pages_);
                chunks_ = std::move(other.chunks_);
                other.host_ = nullptr;
            }
            return *this;
        }

        ~ReadView() { release(); }

        [[nodiscard]] std::span<const std::byte> chunk(std::size_t index) const {
            return chunks_.at(index);
        }

        [[nodiscard]] std::size_t chunk_count() const noexcept { return chunks_.size(); }

        [[nodiscard]] std::vector<std::byte> materialize() const {
            std::vector<std::byte> result;
            for (const auto& chunk : chunks_) {
                result.insert(result.end(), chunk.begin(), chunk.end());
            }
            return result;
        }

        [[nodiscard]] std::span<const GenerationId> generations() const noexcept {
            return generations_;
        }
        [[nodiscard]] std::uint64_t barrier() const noexcept { return barrier_; }

    private:
        friend class SecretHost;

        ReadView(SecretHost* host, GenerationId generation, std::uint64_t barrier,
                 std::vector<std::span<const std::byte>> chunks)
            : host_(host),
              barrier_(barrier),
              generations_{generation},
              pages_{},
              chunks_(std::move(chunks)) {}

        ReadView(SecretHost* host, std::vector<GenerationId> generations,
                 std::vector<PageAddress> pages, std::uint64_t barrier,
                 std::vector<std::span<const std::byte>> chunks)
            : host_(host),
              barrier_(barrier),
              generations_(std::move(generations)),
              pages_(std::move(pages)),
              chunks_(std::move(chunks)) {}

        void release();

        SecretHost* host_ = nullptr;
        std::uint64_t barrier_ = 0;
        std::vector<GenerationId> generations_;
        std::vector<PageAddress> pages_;
        std::vector<std::span<const std::byte>> chunks_;
    };

    class SecretStore {
    public:
        SecretStore() = default;
        SecretStore(const SecretStore&) = delete;
        SecretStore& operator=(const SecretStore&) = delete;

        SecretStore(SecretStore&& other) noexcept
            : host_(other.host_), store_id_(other.store_id_) {
            other.host_ = nullptr;
            other.store_id_ = 0;
        }

        SecretStore& operator=(SecretStore&& other) noexcept {
            if (this != &other) {
                if (host_ != nullptr) host_->release_store(store_id_);
                host_ = other.host_;
                store_id_ = other.store_id_;
                other.host_ = nullptr;
                other.store_id_ = 0;
            }
            return *this;
        }

        ~SecretStore() {
            if (host_ != nullptr) host_->release_store(store_id_);
        }

        void write(GenerationId generation, ByteRange range, std::span<const std::byte> bytes) {
            host_->write(store_id_, generation, range, bytes);
        }

        [[nodiscard]] ReadView read(ByteRange range) {
            return host_->read(store_id_, range);
        }

        [[nodiscard]] SecretStore clone() { return host_->clone_store(store_id_); }

        [[nodiscard]] std::uint64_t version() const { return host_->store_version(store_id_); }
        [[nodiscard]] std::uint64_t id() const noexcept { return store_id_; }

    private:
        friend class SecretHost;

        SecretStore(SecretHost* host, std::uint64_t id) : host_(host), store_id_(id) {}

        SecretHost* host_ = nullptr;
        std::uint64_t store_id_ = 0;
    };

    [[nodiscard]] SecretStore create_store() {
        auto id = next_store_id_++;
        stores_.try_emplace(id, StoreState{});
        return SecretStore{this, id};
    }

    [[nodiscard]] const std::vector<SecretEvent>& events() const noexcept { return events_; }

    [[nodiscard]] const DestructionProof* proof(GenerationId generation) const {
        auto it = proofs_.find(generation);
        return it == proofs_.end() ? nullptr : &it->second;
    }

    std::uint64_t destroy_generation(GenerationId generation) {
        auto& gen = require_generation(generation);
        if (gen.state != GenerationState::active) {
            throw SecretLifecycleError("generation is not active");
        }
        gen.state = GenerationState::deleting;
        auto barrier = next_barrier_++;
        gen.destroy_barrier = barrier;
        emit(GenerationDestroyRequested{generation, barrier});
        if (gen.open_borrows == 0) begin_generation_erasure(generation);
        return barrier;
    }

    void process_erase_receipt(const EraseReceipt& receipt) {
        auto pending_it = pending_erase_.find(receipt.id);
        if (pending_it == pending_erase_.end()) return;
        auto entry = pending_it->second;
        if (entry.receipt_template != receipt) return;
        pending_erase_.erase(pending_it);

        auto& gen = generations_[entry.generation];
        if (entry.scratch) {
            auto owners = entry.scratch_generations.empty()
                              ? std::vector<GenerationId>{entry.generation}
                              : entry.scratch_generations;
            for (auto owner : owners) {
                generations_[owner].scratch_receipts.push_back(receipt);
                emit(ScratchErased{owner, receipt.scratch_id, receipt});
            }
            allocator_.free_scratch(receipt.scratch_id);
        } else {
            gen.physical_receipts.push_back(receipt);
            emit(ExtentErased{entry.generation, receipt});
            release_erased_page(entry.generation, receipt.page);
        }
        if (entry.scratch) {
            auto owners = entry.scratch_generations.empty()
                              ? std::vector<GenerationId>{entry.generation}
                              : entry.scratch_generations;
            for (auto owner : owners) maybe_complete_generation(owner);
        } else {
            maybe_complete_generation(entry.generation);
        }
    }

private:
    enum class GenerationState { active, deleting, erased };

    struct LogicalSegment {
        std::uint64_t logical_offset = 0;
        std::uint64_t size = 0;
        GenerationId generation = invalid_generation;
        std::uint64_t version = 0;
    };

    struct PhysicalPage {
        PageAddress address{};
        std::uint64_t mapping_count = 0;
        std::uint64_t borrow_count = 0;
        bool erase_pending = false;
        std::map<std::uint64_t, GenerationId> slice_generations;
    };

    struct LogicalPage {
        std::uint64_t page_index = 0;
        PageAddress physical{};
        std::map<std::uint64_t, LogicalSegment> segments;
    };

    struct StoreState {
        std::uint64_t version = 0;
        std::map<std::uint64_t, LogicalPage> pages;
    };

    struct GenerationRecord {
        GenerationState state = GenerationState::active;
        std::uint64_t open_borrows = 0;
        std::uint64_t destroy_barrier = 0;
        std::vector<ByteRange> logical_intervals;
        std::map<PageAddress, std::vector<ByteRange>> physical_extents;
        std::set<ScratchId> scratches;
        std::vector<EraseReceipt> physical_receipts;
        std::vector<EraseReceipt> scratch_receipts;
    };

    struct PendingErase {
        GenerationId generation = invalid_generation;
        EraseReceipt receipt_template;
        bool scratch = false;
        std::vector<GenerationId> scratch_generations;
    };

    struct PreparedPage {
        std::uint64_t page_index = 0;
        PageAddress old_physical{};
        PageAddress new_physical{};
        ScratchId scratch_id = 0;
        bool fresh = false;
        bool copied = false;
        bool in_place = false;
        std::vector<PageExtentEvent> scratch_extents;
    };

    struct WriteTransaction {
        GenerationId generation = invalid_generation;
        ByteRange range{};
        std::span<const std::byte> bytes;
        std::uint64_t version = 0;
        std::vector<PreparedPage> pages;
        std::vector<PageExtentEvent> physical_events;
        std::vector<OverwriteRetired> retired_events;
        std::vector<LogicalIntervalRetired> retired_logical_events;
        std::vector<ScratchCopied> scratch_events;
    };

    template <typename Event>
    void emit(Event event) {
        events_.push_back(SecretEvent{std::move(event)});
    }

    GenerationRecord& require_generation(GenerationId generation) {
        auto it = generations_.find(generation);
        if (it == generations_.end()) {
            it = generations_.emplace(generation, GenerationRecord{}).first;
        }
        return it->second;
    }

    [[nodiscard]] std::size_t page_size() const { return allocator_.page_size(); }

    void validate_range(const ByteRange& range, std::size_t data_size = 0) const {
        if (range.size == 0) throw SecretLifecycleError("empty secret range");
        if (range.offset > std::numeric_limits<std::uint64_t>::max() - range.size) {
            throw SecretLifecycleError("secret range overflow");
        }
        if (data_size != 0 && range.size != data_size) {
            throw SecretLifecycleError("secret range does not match input size");
        }
    }

    PhysicalPage& require_physical(const PageAddress& address) {
        auto it = physical_.find(address);
        if (it == physical_.end()) throw SecretLifecycleError("unknown physical page");
        return it->second;
    }

    void write(std::uint64_t store_id, GenerationId generation, ByteRange range,
               std::span<const std::byte> bytes) {
        validate_range(range, bytes.size());
        auto store_it = stores_.find(store_id);
        if (store_it == stores_.end()) throw SecretLifecycleError("unknown store");
        auto& store = store_it->second;
        auto& gen = require_generation(generation);
        if (gen.state != GenerationState::active) {
            throw BorrowRejected("deleted generation cannot receive new writes");
        }

        emit(GenerationAllocated{generation, range, store.version + 1});
        WriteTransaction tx{};
        tx.generation = generation;
        tx.range = range;
        tx.bytes = bytes;
        tx.version = store.version + 1;
        try {
            prepare_write(store, tx);
            commit_write(store, tx);
        } catch (const InjectedScratchFailure& failure) {
            rollback_write(tx);
            erase_scratch_now(generation, failure.scratch_id, true);
            throw;
        } catch (...) {
            rollback_write(tx);
            throw;
        }
        store.version = tx.version;
    }

    void prepare_write(StoreState& store, WriteTransaction& tx) {
        auto size = page_size();
        auto first = tx.range.offset / size;
        auto last = (tx.range.end() - 1) / size;
        for (auto page_index = first; page_index <= last; ++page_index) {
            PreparedPage prepared;
            prepared.page_index = page_index;
            auto existing = store.pages.find(page_index);
            if (existing == store.pages.end()) {
                prepared.fresh = true;
                prepared.new_physical = allocator_.allocate_page();
                physical_.emplace(prepared.new_physical,
                                  PhysicalPage{prepared.new_physical, 1, 0, false, {}});
                tx.pages.push_back(prepared);
                continue;
            }

            prepared.old_physical = existing->second.physical;
            auto& old_page = require_physical(prepared.old_physical);
            bool needs_copy = old_page.mapping_count > 1 || old_page.borrow_count > 0 ||
                              old_page.erase_pending;
            if (!needs_copy) {
                prepared.new_physical = prepared.old_physical;
                prepared.in_place = true;
                tx.pages.push_back(prepared);
                continue;
            }

            try {
                prepared.scratch_id = allocator_.make_scratch_copy(prepared.old_physical);
                prepared.copied = true;
                tx.pages.push_back(prepared);

                prepared.scratch_extents = scratch_generation_extents(prepared.old_physical);
                tx.scratch_events.push_back(ScratchCopied{
                    prepared.scratch_id, prepared.old_physical, prepared.scratch_extents});
                for (const auto& extent : prepared.scratch_extents) {
                    require_generation(extent.generation).scratches.insert(prepared.scratch_id);
                }

                prepared.new_physical = allocator_.allocate_page();
                auto scratch = allocator_.scratch_span(prepared.scratch_id);
                auto target = allocator_.resolve(prepared.new_physical);
                std::memcpy(target.data(), scratch.data(), target.size());
                physical_.emplace(prepared.new_physical,
                                  PhysicalPage{prepared.new_physical, 1, 0, false,
                                               old_page.slice_generations});
                tx.pages.back() = prepared;
            } catch (const InjectedScratchFailure&) {
                throw;
            }
        }
    }

    std::vector<PageExtentEvent> scratch_generation_extents(const PageAddress& source) const {
        std::vector<PageExtentEvent> result;
        auto it = physical_.find(source);
        if (it == physical_.end()) return result;
        for (const auto& [offset, generation] : it->second.slice_generations) {
            auto bound = next_slice_bound(it->second, offset);
            auto page_end = static_cast<std::uint64_t>(page_size());
            result.push_back(PageExtentEvent{
                source, ByteRange{offset, std::min(bound, page_end) - offset}, generation});
        }
        return result;
    }

    static std::uint64_t next_slice_bound(const PhysicalPage& page, std::uint64_t offset) {
        auto it = page.slice_generations.upper_bound(offset);
        return it == page.slice_generations.end() ? std::numeric_limits<std::uint64_t>::max()
                                                  : it->first;
    }

    void commit_write(StoreState& store, WriteTransaction& tx) {
        auto size = page_size();
        for (auto& prepared : tx.pages) {
            if (prepared.fresh) {
                LogicalPage logical;
                logical.page_index = prepared.page_index;
                logical.physical = prepared.new_physical;
                store.pages.emplace(prepared.page_index, std::move(logical));
                continue;
            }

            auto& logical = store.pages.at(prepared.page_index);
            if (prepared.copied) {
                auto& old_page = require_physical(prepared.old_physical);
                --old_page.mapping_count;
                logical.physical = prepared.new_physical;
            }
        }

        for (auto page_index = tx.range.offset / size;
             page_index <= (tx.range.end() - 1) / size; ++page_index) {
            auto& logical = store.pages.at(page_index);
            auto page_start = page_index * size;
            auto page_end = page_start + size;
            auto write_begin = std::max(tx.range.offset, page_start);
            auto write_end = std::min(tx.range.end(), page_end);
            replace_logical_segments(logical, tx, write_begin, write_end);

            auto& physical = require_physical(logical.physical);
            auto prepared_it = std::find_if(tx.pages.begin(), tx.pages.end(),
                                            [page_index](const PreparedPage& candidate) {
                                                return candidate.page_index == page_index;
                                            });
            if (prepared_it != tx.pages.end() && prepared_it->in_place) {
                record_overwritten_extents(tx, physical, write_begin - page_start,
                                           write_end - write_begin, write_begin, write_end);
            }
            replace_physical_slices(physical, tx, write_begin, write_end);
            auto target = allocator_.resolve(logical.physical);
            auto input_offset = write_begin - tx.range.offset;
            std::memcpy(target.data() + write_begin - page_start,
                        tx.bytes.data() + input_offset, write_end - write_begin);

            tx.physical_events.push_back(PageExtentEvent{
                logical.physical,
                ByteRange{write_begin - page_start, write_end - write_begin},
                tx.generation});
            record_generation_extent(tx, logical.physical, write_begin - page_start,
                                     write_end - write_begin);
        }

        auto logical_intervals = require_generation(tx.generation).logical_intervals;
        logical_intervals.push_back(tx.range);
        require_generation(tx.generation).logical_intervals = merge_ranges(logical_intervals);
        for (const auto& retired : tx.retired_logical_events) emit(retired);
        for (const auto& retired : tx.retired_events) emit(retired);
        emit(GenerationWritten{tx.generation, tx.range, tx.version, tx.physical_events,
                               tx.scratch_events});
        for (const auto& prepared : tx.pages) {
            if (prepared.copied) erase_scratch_now(tx.generation, prepared.scratch_id, false);
        }
    }

    void replace_logical_segments(LogicalPage& logical, const WriteTransaction& tx,
                                  std::uint64_t begin, std::uint64_t end) {
        std::map<std::uint64_t, LogicalSegment> updated;
        for (auto item : logical.segments) {
            auto segment_begin = item.first;
            auto segment_end = item.first + item.second.size;
            if (segment_end <= begin || segment_begin >= end) {
                updated.emplace(item.first, item.second);
                continue;
            }
            if (segment_begin < begin) {
                item.second.size = begin - segment_begin;
                updated.emplace(item.first, item.second);
            }
            if (segment_end > end) {
                auto prefix_size = end - segment_begin;
                LogicalSegment tail = item.second;
                tail.logical_offset += prefix_size;
                tail.size = segment_end - end;
                updated.emplace(end, tail);
            }
        }
        LogicalSegment segment{begin, end - begin, tx.generation, tx.version};
        updated.emplace(begin, segment);
        logical.segments = std::move(updated);
    }

    void replace_physical_slices(PhysicalPage& physical, const WriteTransaction& tx,
                                 std::uint64_t begin, std::uint64_t end) {
        std::map<std::uint64_t, GenerationId> updated;
        for (const auto& [offset, generation] : physical.slice_generations) {
            auto next = next_slice_bound(physical, offset);
            auto slice_end = std::min(next, static_cast<std::uint64_t>(page_size()));
            if (slice_end <= begin || offset >= end) {
                updated.emplace(offset, generation);
                continue;
            }
            if (offset < begin) {
                updated.emplace(offset, generation);
            }
            if (slice_end > end) {
                updated.emplace(end, generation);
            }
        }
        updated.emplace(begin, tx.generation);
        physical.slice_generations = std::move(updated);
    }

    void record_overwritten_extents(WriteTransaction& tx, const PhysicalPage& physical,
                                    std::uint64_t begin, std::uint64_t end,
                                    std::uint64_t logical_begin,
                                    std::uint64_t logical_end) {
        for (const auto& [offset, generation] : physical.slice_generations) {
            if (generation == tx.generation) continue;
            auto next = next_slice_bound(physical, offset);
            auto slice_end = std::min(next, static_cast<std::uint64_t>(page_size()));
            auto overlap_begin = std::max(offset, begin);
            auto overlap_end = std::min(slice_end, end);
            if (overlap_begin >= overlap_end) continue;
            ByteRange retired{overlap_begin, overlap_end - overlap_begin};
            tx.retired_events.push_back(OverwriteRetired{generation, physical.address, retired});

            auto& gen = require_generation(generation);
            auto it = gen.physical_extents.find(physical.address);
            if (it == gen.physical_extents.end()) continue;
            it->second = subtract_ranges(it->second, retired);
            if (it->second.empty()) gen.physical_extents.erase(it);
            gen.logical_intervals = subtract_ranges(
                gen.logical_intervals,
                ByteRange{logical_begin, logical_end - logical_begin});
            tx.retired_logical_events.push_back(LogicalIntervalRetired{
                generation, ByteRange{logical_begin, logical_end - logical_begin}});
        }
    }

    void record_generation_extent(const WriteTransaction& tx, const PageAddress& page,
                                  std::uint64_t offset, std::uint64_t size) {
        auto& gen = require_generation(tx.generation);
        auto& ranges = gen.physical_extents[page];
        ranges.push_back(ByteRange{offset, size});
        ranges = merge_ranges(ranges);
    }

    void rollback_write(WriteTransaction& tx) {
        for (const auto& prepared : tx.pages) {
            if (prepared.copied) {
                erase_scratch_now(tx.generation, prepared.scratch_id, true);
            }
            if ((prepared.fresh || prepared.copied) &&
                prepared.new_physical != PageAddress{}) {
                auto span = allocator_.resolve(prepared.new_physical);
                secure_zero(span);
                physical_.erase(prepared.new_physical);
                allocator_.free_page(prepared.new_physical);
            }
        }
    }

    ReadView read(std::uint64_t store_id, ByteRange range) {
        validate_range(range);
        auto& store = stores_.at(store_id);
        auto size = page_size();
        std::map<GenerationId, std::uint64_t> generation_counts;
        std::vector<std::span<const std::byte>> chunks;
        std::vector<PageAddress> borrowed_pages;
        for (auto logical_offset = range.offset; logical_offset < range.end();) {
            auto page_index = logical_offset / size;
            auto page_end = std::min(range.end(), (page_index + 1) * size);
            auto& logical = store.pages.at(page_index);
            auto segment_it = find_segment(logical, logical_offset);
            auto segment = segment_it->second;
            auto generation = segment.generation;
            auto& gen = require_generation(generation);
            if (gen.state != GenerationState::active) {
                throw BorrowRejected("generation deleted: new borrow rejected");
            }
            ++generation_counts[generation];
            auto segment_end = std::min(page_end, segment_it->first + segment.size);
            auto physical = allocator_.resolve(logical.physical);
            auto chunk_begin = logical_offset - page_index * size;
            chunks.emplace_back(physical.data() + chunk_begin, segment_end - logical_offset);
            logical_offset = segment_end;
        }

        auto barrier = next_barrier_++;
        std::vector<GenerationId> pinned_generations;
        for (const auto& [generation, count] : generation_counts) {
            auto& gen = require_generation(generation);
            gen.open_borrows += count;
            pinned_generations.push_back(generation);
            emit(BorrowOpened{generation, barrier, store.version, range});
        }
        for (auto page_index = range.offset / size;
             page_index <= (range.end() - 1) / size; ++page_index) {
            auto& physical = require_physical(store.pages.at(page_index).physical);
            ++physical.borrow_count;
            if (std::find(borrowed_pages.begin(), borrowed_pages.end(), physical.address) ==
                borrowed_pages.end()) {
                borrowed_pages.push_back(physical.address);
            }
        }
        return ReadView{this, std::move(pinned_generations), std::move(borrowed_pages), barrier,
                        std::move(chunks)};
    }

    std::map<std::uint64_t, LogicalSegment>::iterator find_segment(
        LogicalPage& logical, std::uint64_t logical_offset) {
        auto it = logical.segments.upper_bound(logical_offset);
        if (it == logical.segments.begin()) {
            throw SecretLifecycleError("uninitialized secret range");
        }
        --it;
        if (logical_offset >= it->first + it->second.size) {
            throw SecretLifecycleError("uninitialized secret range");
        }
        return it;
    }

    void release_borrow(const std::vector<GenerationId>& generations,
                        const std::vector<PageAddress>& pages, std::uint64_t barrier) {
        for (const auto& page : pages) {
            auto& physical = require_physical(page);
            if (physical.borrow_count == 0) throw SecretLifecycleError("borrow underflow");
            --physical.borrow_count;
        }
        for (const auto& generation : generations) {
            emit(BorrowClosed{generation, barrier});
            auto& gen = require_generation(generation);
            if (gen.open_borrows == 0) throw SecretLifecycleError("borrow underflow");
            --gen.open_borrows;
            if (gen.state == GenerationState::deleting && gen.open_borrows == 0) {
                begin_generation_erasure(generation);
            }
        }
    }

    SecretStore clone_store(std::uint64_t source_id) {
        auto id = next_store_id_++;
        auto source = stores_.at(source_id);
        for (auto& [index, logical] : source.pages) {
            ++require_physical(logical.physical).mapping_count;
        }
        stores_.emplace(id, std::move(source));
        return SecretStore{this, id};
    }

    void release_store(std::uint64_t store_id) {
        auto it = stores_.find(store_id);
        if (it == stores_.end()) return;
        for (const auto& [index, logical] : it->second.pages) {
            (void)index;
            auto& physical = require_physical(logical.physical);
            if (physical.mapping_count == 0) throw SecretLifecycleError("mapping underflow");
            --physical.mapping_count;
            if (!physical.erase_pending) release_physical_if_orphan(physical);
        }
        stores_.erase(it);
    }

    [[nodiscard]] std::uint64_t store_version(std::uint64_t store_id) const {
        return stores_.at(store_id).version;
    }

    static std::vector<ByteRange> merge_ranges(std::vector<ByteRange> ranges) {
        std::sort(ranges.begin(), ranges.end(), [](const ByteRange& left, const ByteRange& right) {
            if (left.offset != right.offset) return left.offset < right.offset;
            return left.size < right.size;
        });
        std::vector<ByteRange> result;
        for (const auto& range : ranges) {
            if (result.empty() || range.offset > result.back().end()) {
                result.push_back(range);
            } else if (range.end() > result.back().end()) {
                result.back().size = range.end() - result.back().offset;
            }
        }
        return result;
    }

    static std::vector<ByteRange> subtract_ranges(std::vector<ByteRange> ranges,
                                                  const ByteRange& removed) {
        std::vector<ByteRange> result;
        for (const auto& range : merge_ranges(ranges)) {
            if (removed.end() <= range.offset || removed.offset >= range.end()) {
                result.push_back(range);
                continue;
            }
            if (removed.offset > range.offset) {
                result.push_back(ByteRange{range.offset, removed.offset - range.offset});
            }
            if (removed.end() < range.end()) {
                result.push_back(ByteRange{removed.end(), range.end() - removed.end()});
            }
        }
        return result;
    }

    static void secure_zero(std::span<std::byte> target) {
        volatile std::byte* begin = target.data();
        volatile std::byte* end = target.data() + target.size();
        for (; begin != end; ++begin) *begin = std::byte{};
    }

    EraseId submit_erase(GenerationId generation, const PageAddress& page, ByteRange range,
                         bool scratch, ScratchId scratch_id = 0) {
        EraseRequest request{next_erase_id_++, page, scratch_id, range, scratch};
        EraseReceipt receipt{request.id, request.page, request.scratch_id, request.range,
                             request.scratch};
        pending_erase_.emplace(
            request.id, PendingErase{generation, receipt, scratch, {}});
        if (!scratch) require_physical(page).erase_pending = true;
        eraser_.erase_async(request, erasable_, [this](const EraseReceipt& completed) {
            this->process_erase_receipt(completed);
        });
        return request.id;
    }

    void erase_scratch_now(GenerationId generation, ScratchId scratch_id) {
        if (scratch_id == 0) return;
        erase_scratch_now(generation, scratch_id, true);
    }

    void erase_scratch_now(GenerationId generation, ScratchId scratch_id, bool synchronous) {
        if (scratch_id == 0) return;
        auto* tracking = dynamic_cast<TrackingPageAllocator*>(&allocator_);
        if (tracking != nullptr && !tracking->has_scratch(scratch_id)) return;
        EraseRequest request{next_erase_id_++, PageAddress{}, scratch_id,
                             ByteRange{0, page_size()}, true};
        EraseReceipt receipt{request.id, request.page, request.scratch_id, request.range,
                             request.scratch};
        if (synchronous) {
            receipt = eraser_.erase(request, erasable_);
        } else {
            std::vector<GenerationId> owners;
            for (auto& [id, record] : generations_) {
                if (record.scratches.erase(scratch_id) != 0) owners.push_back(id);
            }
            PendingErase pending{generation, receipt, true, owners};
            pending_erase_.emplace(request.id, std::move(pending));
            eraser_.erase_async(request, erasable_, [this](const EraseReceipt& completed) {
                this->process_erase_receipt(completed);
            });
            return;
        }
        for (auto& [id, record] : generations_) {
            if (record.scratches.erase(scratch_id) != 0) {
                record.scratch_receipts.push_back(receipt);
                emit(ScratchErased{id, scratch_id, receipt});
            }
        }
        (void)generation;
        allocator_.free_scratch(scratch_id);
    }

    void begin_generation_erasure(GenerationId generation) {
        auto& gen = require_generation(generation);
        if (gen.state != GenerationState::deleting) return;

        for (const auto& [page, ranges] : gen.physical_extents) {
            for (const auto& range : merge_ranges(ranges)) {
                submit_erase(generation, page, range, false);
            }
        }

        for (auto scratch_id : gen.scratches) {
            submit_erase(generation, PageAddress{}, ByteRange{0, page_size()}, true, scratch_id);
        }
        gen.scratches.clear();
    }

    void release_erased_page(GenerationId generation, const PageAddress& page) {
        (void)generation;
        auto it = physical_.find(page);
        if (it == physical_.end()) return;
        it->second.erase_pending = false;
        release_physical_if_orphan(it->second);
    }

    void release_physical_if_orphan(PhysicalPage& physical) {
        if (physical.mapping_count != 0 || physical.borrow_count != 0 || physical.erase_pending) {
            return;
        }
        auto address = physical.address;
        physical_.erase(address);
        allocator_.free_page(address);
    }

    void maybe_complete_generation(GenerationId generation) {
        auto& gen = require_generation(generation);
        if (gen.state != GenerationState::deleting || gen.open_borrows != 0) return;
        for (const auto& [id, pending] : pending_erase_) {
            if (pending.generation == generation) return;
        }

        gen.state = GenerationState::erased;
        DestructionProof proof;
        proof.generation = generation;
        proof.logical_intervals = merge_ranges(gen.logical_intervals);
        proof.borrow_barrier = gen.destroy_barrier;
        proof.physical_receipts = gen.physical_receipts;
        proof.scratch_receipts = gen.scratch_receipts;
        proofs_[generation] = std::move(proof);
    }

    IPageAllocator& allocator_;
    IErasableAllocator& erasable_;
    IEraser& eraser_;
    std::uint64_t next_store_id_ = 1;
    std::uint64_t next_barrier_ = 1;
    EraseId next_erase_id_ = 1;
    std::map<std::uint64_t, StoreState> stores_;
    std::map<PageAddress, PhysicalPage> physical_;
    std::map<GenerationId, GenerationRecord> generations_;
    std::map<EraseId, PendingErase> pending_erase_;
    std::map<GenerationId, DestructionProof> proofs_;
    std::vector<SecretEvent> events_;
};

inline void SecretHost::ReadView::release() {
    if (host_ != nullptr) {
        host_->release_borrow(generations_, pages_, barrier_);
        host_ = nullptr;
    }
}

}  // namespace secret_lifecycle
