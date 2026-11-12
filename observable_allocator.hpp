#pragma once

#include <algorithm>
#include <cstddef>
#include <cstdint>
#include <memory>
#include <new>
#include <vector>

namespace netsec {
namespace test {

// Shared instrumentation state for observable_allocator instances.
//
// Two failure-injection primitives are provided:
//   * fail_at  : fail the Nth allocation call (0-based allocation sequence).
//   * fail_when: fail while a predicate over the requested size holds.
//
// Every deallocation snapshots the block's bytes before the memory is
// returned, including whether the block was already all-zero. Because the
// snapshot is read inside the allocator (an observable use of the memory), the
// container's secure clear is verifiable even when the test binary is built
// with aggressive optimization.
struct allocator_state {
    struct allocation_event {
        std::size_t size = 0;
        bool failed = false;
    };

    struct deallocation_snapshot {
        std::size_t size = 0;
        std::vector<unsigned char> bytes;
        bool all_zero = false;
    };

    struct live_block {
        unsigned char* address = nullptr;
        std::size_t size = 0;
    };

    std::size_t allocation_count = 0;
    std::size_t deallocation_count = 0;
    std::size_t bytes_outstanding = 0;
    std::size_t live_block_count = 0;

    // 0-based index of the allocation that must throw, or -1 to disable.
    long long fail_at = -1;

    // When non-null, an allocation matching the predicate throws.
    bool (*fail_predicate)(std::size_t) = nullptr;

    std::vector<allocation_event> allocations;
    std::vector<deallocation_snapshot> deallocations;
    std::vector<live_block> live;

    void reset_failures() noexcept {
        fail_at = -1;
        fail_predicate = nullptr;
    }

    bool has_live_blocks() const noexcept {
        return !live.empty() || bytes_outstanding != 0;
    }
};

template <class T>
class observable_allocator {
public:
    using value_type = T;
    using pointer = T*;
    using const_pointer = const T*;
    using size_type = std::size_t;
    using difference_type = std::ptrdiff_t;

    using propagate_on_container_copy_assignment = std::true_type;
    using propagate_on_container_move_assignment = std::true_type;
    using propagate_on_container_swap = std::true_type;

    template <class U>
    struct rebind {
        using other = observable_allocator<U>;
    };

    explicit observable_allocator(std::shared_ptr<allocator_state> state)
        : state_(std::move(state)) {}

    observable_allocator()
        : state_(std::make_shared<allocator_state>()) {}

    template <class U>
    observable_allocator(const observable_allocator<U>& other) noexcept
        : state_(other.state_ptr()) {}

    observable_allocator(const observable_allocator&) = default;
    observable_allocator(observable_allocator&&) = default;
    observable_allocator& operator=(const observable_allocator&) = default;
    observable_allocator& operator=(observable_allocator&&) = default;

    T* allocate(std::size_t count) {
        const std::size_t sequence = state_->allocation_count;
        state_->allocation_count += 1;

        const bool forced_failure =
            (state_->fail_at >= 0 &&
             static_cast<std::size_t>(state_->fail_at) == sequence) ||
            (state_->fail_predicate != nullptr &&
             state_->fail_predicate(count));

        state_->allocations.push_back(allocator_state::allocation_event{
            count * sizeof(T), forced_failure});

        if (forced_failure) {
            throw std::bad_alloc{};
        }

        void* raw = ::operator new(count * sizeof(T));
        T* block = static_cast<T*>(raw);

        state_->live.push_back(
            allocator_state::live_block{reinterpret_cast<unsigned char*>(raw),
                                        count * sizeof(T)});
        state_->live_block_count = state_->live.size();
        state_->bytes_outstanding += count * sizeof(T);
        return block;
    }

    void deallocate(T* block, std::size_t count) noexcept {
        const std::size_t byte_count = count * sizeof(T);
        const unsigned char* raw = reinterpret_cast<const unsigned char*>(block);

        allocator_state::deallocation_snapshot snapshot;
        snapshot.size = byte_count;
        snapshot.bytes.assign(raw, raw + byte_count);
        snapshot.all_zero =
            std::all_of(snapshot.bytes.begin(), snapshot.bytes.end(),
                        [](unsigned char value) { return value == 0; });
        state_->deallocations.push_back(std::move(snapshot));
        state_->deallocation_count += 1;

        auto position =
            std::find_if(state_->live.begin(), state_->live.end(),
                         [raw](const allocator_state::live_block& entry) {
                             return entry.address == raw;
                         });
        if (position != state_->live.end()) {
            state_->bytes_outstanding -= position->size;
            state_->live.erase(position);
            state_->live_block_count = state_->live.size();
        }

        ::operator delete(block);
    }

    bool operator==(const observable_allocator& other) const noexcept {
        return state_.get() == other.state_.get();
    }

    bool operator!=(const observable_allocator& other) const noexcept {
        return !(*this == other);
    }

    const std::shared_ptr<allocator_state>& state_ptr() const noexcept {
        return state_;
    }

private:
    std::shared_ptr<allocator_state> state_;
};

}  // namespace test
}  // namespace netsec
