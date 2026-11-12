#pragma once

#include <atomic>
#include <cstddef>
#include <cstdint>
#include <limits>
#include <stdexcept>
#include <vector>

namespace netsec {

// Returns the smallest power of two that is >= n. n == 0 yields 1 so that
// even an empty table owns one addressable slot and a valid (zero) mask.
// Throws std::length_error if no power of two fits in std::size_t, instead
// of silently shifting into undefined behavior.
inline std::size_t next_power_of_two(std::size_t n) {
    if (n == 0) {
        return 1;
    }
    constexpr std::size_t kMaxPow2 =
        std::size_t(1) << (std::numeric_limits<std::size_t>::digits - 1);
    if (n > kMaxPow2) {
        throw std::length_error(
            "netsec: requested size cannot be padded to a power of two");
    }
    std::size_t power = 1;
    while (power < n) {
        power <<= 1;
    }
    return power;
}

// Branchless index fold. The AND is unconditional and data-independent, so
// there is no compare-and-branch on the load path for a trained predictor to
// mispredict. Given a power-of-two capacity C and mask = C - 1, the result is
// always in [0, C) for every possible std::size_t input, including SIZE_MAX
// and the unsigned conversion of a negative signed index.
constexpr std::size_t mask_index(std::size_t index,
                                 std::size_t capacity_mask) noexcept {
    return index & capacity_mask;
}

// Compiler-only speculation fence: no machine instruction is emitted, but the
// optimizer cannot re-derive a conditional (branch/cmov) data path across it.
// Keeping the masked load unconditional is the Spectre-v1 mitigation.
inline void speculation_barrier() noexcept {
    std::atomic_signal_fence(std::memory_order_seq_cst);
}

// Test hook signature. Returning true simulates a bounds check that was
// bypassed on a speculative path; the load must still stay inside the padded
// allocation because every index is folded through mask_index().
using SkipBoundsCheckHook = bool (*)(std::size_t raw_index,
                                     void* user_data) noexcept;

// A lookup table whose backing store is padded from a non-power-of-two
// logical size up to the next power of two. Reads always address the
// allocation through a branchless mask, so a bypassed bounds check can only
// reach an in-allocation (possibly padding) slot, never adjacent memory.
template <typename T>
class PaddedTable {
public:
    using value_type = T;
    using size_type = std::size_t;

    explicit PaddedTable(size_type logical_size)
        : size_(logical_size),
          capacity_(next_power_of_two(logical_size)),
          mask_(capacity_ - 1),
          storage_(capacity_) {}

    PaddedTable(size_type logical_size, const T& fill)
        : size_(logical_size),
          capacity_(next_power_of_two(logical_size)),
          mask_(capacity_ - 1),
          storage_(capacity_, fill) {}

    // Interpreter-visible entry count; may be smaller than capacity().
    size_type size() const noexcept { return size_; }

    // Power-of-two backing allocation size.
    size_type capacity() const noexcept { return capacity_; }
    size_type capacity_mask() const noexcept { return mask_; }
    bool is_padded() const noexcept { return capacity_ != size_; }

    // Injects (or replaces) the hook that simulates a bypassed bounds check.
    // user_data is passed back unchanged for test-side state.
    void set_skip_check_hook(SkipBoundsCheckHook hook,
                             void* user_data = nullptr) noexcept {
        hook_ = hook;
        hook_data_ = user_data;
    }

    void clear_skip_check_hook() noexcept {
        hook_ = nullptr;
        hook_data_ = nullptr;
    }

    bool has_skip_check_hook() const noexcept { return hook_ != nullptr; }

    // Unconditional, branch-free access. Never throws and never leaves the
    // allocation: this is the speculative-safe lookup primitive.
    T& operator[](size_type index) noexcept {
        speculation_barrier();
        return storage_[mask_index(index, mask_)];
    }

    const T& operator[](size_type index) const noexcept {
        speculation_barrier();
        return storage_[mask_index(index, mask_)];
    }

    // Architectural access. Out-of-range indices throw std::out_of_range
    // unless the injected hook reports a bypassed check; even when bypassed,
    // the masked load resolves to an in-allocation slot.
    T& at(size_type index) {
        if (!bounds_check_bypassed(index) && index >= size_) {
            throw std::out_of_range("netsec: index out of range");
        }
        return (*this)[index];
    }

    const T& at(size_type index) const {
        if (!bounds_check_bypassed(index) && index >= size_) {
            throw std::out_of_range("netsec: index out of range");
        }
        return (*this)[index];
    }

    // Signed convenience overloads for interpreter operands. A negative value
    // is converted modulo 2^N (well-defined) and then folded by the mask.
    T& at_signed(long long index) {
        return at(static_cast<size_type>(index));
    }

    const T& at_signed(long long index) const {
        return at(static_cast<size_type>(index));
    }

    T& get_signed(long long index) noexcept {
        return (*this)[static_cast<size_type>(index)];
    }

    const T& get_signed(long long index) const noexcept {
        return (*this)[static_cast<size_type>(index)];
    }

    T* data() noexcept { return storage_.data(); }
    const T* data() const noexcept { return storage_.data(); }

    // Containment test used by the test suite to prove a resolved reference
    // lies within the power-of-two allocation.
    bool within_allocation(const T* pointer) const noexcept {
        const auto base = reinterpret_cast<std::uintptr_t>(storage_.data());
        const auto address = reinterpret_cast<std::uintptr_t>(pointer);
        return address >= base &&
               address < base + capacity_ * sizeof(T);
    }

private:
    bool bounds_check_bypassed(size_type index) const noexcept {
        return hook_ != nullptr && hook_(index, hook_data_);
    }

    size_type size_;
    size_type capacity_;
    size_type mask_;
    std::vector<T> storage_;
    SkipBoundsCheckHook hook_ = nullptr;
    void* hook_data_ = nullptr;
};

}  // namespace netsec
