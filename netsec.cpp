#include "netsec.hpp"

#include <algorithm>
#include <atomic>
#include <chrono>
#include <limits>
#include <thread>

#if defined(_MSC_VER)
#include <intrin.h>
#endif

#if defined(__clang__) || defined(__GNUC__)
#define NETSEC_NOINLINE __attribute__((noinline))
#define NETSEC_COMPILER_BARRIER() __asm__ __volatile__("" ::: "memory")
#elif defined(_MSC_VER)
#define NETSEC_NOINLINE __declspec(noinline)
#define NETSEC_COMPILER_BARRIER() _ReadWriteBarrier()
#else
#define NETSEC_NOINLINE
#define NETSEC_COMPILER_BARRIER() std::atomic_signal_fence(std::memory_order_seq_cst)
#endif

namespace netsec {
namespace {

constexpr std::uint32_t kPrimaryMagic = 0x243F6A88u;
constexpr std::uint32_t kSecondaryMagic = 0x85A308D3u;

[[nodiscard]] constexpr std::uint32_t encode_bool(bool value,
                                                  std::uint32_t magic,
                                                  std::uint32_t token) noexcept {
    return magic ^ token ^ (value ? 0xFFFF'0000u : 0x0000'FFFFu);
}

[[nodiscard]] constexpr bool valid_word_pair(std::uint32_t word,
                                             std::uint32_t mirror,
                                             std::uint32_t magic,
                                             std::uint32_t token,
                                             bool& value) noexcept {
    if ((word ^ mirror) != std::numeric_limits<std::uint32_t>::max()) {
        return false;
    }
    const std::uint32_t decoded = word ^ magic ^ token;
    if (decoded == 0xFFFF'0000u) {
        value = true;
        return true;
    }
    if (decoded == 0x0000'FFFFu) {
        value = false;
        return true;
    }
    return false;
}

[[nodiscard]] std::uint8_t byte_or_zero(std::span<const std::uint8_t> data,
                                        std::size_t index) noexcept {
    return index < data.size() ? data[index] : std::uint8_t{0};
}

// Independent channel A: forward traversal, OR accumulation.
[[nodiscard]] NETSEC_NOINLINE bool
compare_primary(std::span<const std::uint8_t> reference,
                std::span<const std::uint8_t> candidate) noexcept {
    const std::size_t length = std::max(reference.size(), candidate.size());
    volatile std::uint32_t difference = 0;

    for (std::size_t index = 0; index < length; ++index) {
        const std::uint32_t byte_difference =
            static_cast<std::uint32_t>(byte_or_zero(reference, index)) ^
            static_cast<std::uint32_t>(byte_or_zero(candidate, index));
        difference = difference | byte_difference;
        NETSEC_COMPILER_BARRIER();
    }

    difference = difference |
                 static_cast<std::uint32_t>(reference.size() != candidate.size());
    NETSEC_COMPILER_BARRIER();
    return difference == 0;
}

// Independent channel B: reverse traversal, a different mix, OR accumulation.
[[nodiscard]] NETSEC_NOINLINE bool
compare_secondary(std::span<const std::uint8_t> reference,
                  std::span<const std::uint8_t> candidate) noexcept {
    const std::size_t length = std::max(reference.size(), candidate.size());
    volatile std::uint32_t difference = 0;

    for (std::size_t index = length; index-- > 0;) {
        const std::uint32_t byte_difference =
            static_cast<std::uint32_t>(byte_or_zero(reference, index)) ^
            static_cast<std::uint32_t>(byte_or_zero(candidate, index));
        difference = difference | (byte_difference * 0x9E37'79B1u) |
                     (byte_difference >> 8);
        NETSEC_COMPILER_BARRIER();
    }

    difference =
        difference |
        (static_cast<std::uint32_t>(reference.size() != candidate.size()) *
         0xA5A5'A5A5u);
    NETSEC_COMPILER_BARRIER();
    return difference == 0;
}

[[nodiscard]] constexpr bool policy_is_valid(const TimingPolicy& policy) noexcept {
    return policy.minimum_cycles > 0 &&
           policy.maximum_cycles >= policy.minimum_cycles &&
           policy.maximum_stalled_samples > 0;
}

struct ClockWait {
    bool ok{false};
    FaultCode fault{FaultCode::clock_invalid};
    std::uint64_t last_cycles{};
};

// The loop always terminates: no progress is bounded by maximum_stalled_samples,
// and forward progress beyond maximum_cycles is rejected immediately.
[[nodiscard]] ClockWait wait_for_minimum(ClockSource& clock,
                                         std::uint64_t start_cycles,
                                         const TimingPolicy& policy) noexcept {
    std::uint64_t last_cycles = start_cycles;
    std::uint64_t stalled_samples = 0;

    for (;;) {
        const ClockSample sample = clock.sample();
        if (!sample.valid) {
            return {false, FaultCode::clock_invalid, last_cycles};
        }
        if (sample.cycles < start_cycles || sample.cycles < last_cycles) {
            return {false, FaultCode::clock_reversed, sample.cycles};
        }

        const std::uint64_t elapsed = sample.cycles - start_cycles;
        if (elapsed > policy.maximum_cycles) {
            return {false, FaultCode::clock_rate_violation, sample.cycles};
        }

        if (sample.cycles == last_cycles) {
            ++stalled_samples;
            if (stalled_samples > policy.maximum_stalled_samples) {
                return {false, FaultCode::clock_stalled, sample.cycles};
            }
        } else {
            stalled_samples = 0;
            last_cycles = sample.cycles;
        }

        if (elapsed >= policy.minimum_cycles) {
            return {true, FaultCode::none, sample.cycles};
        }

        std::this_thread::yield();
    }
}

[[nodiscard]] constexpr std::uint32_t
confirmation_token(std::uint64_t operation_id, std::uint64_t start_cycles) noexcept {
    std::uint64_t mixed = operation_id * 0x9E37'79B9'7F4A'7C15ULL;
    mixed ^= start_cycles + 0xBB67'AE85'3C6E'F372ULL + (mixed << 6) + (mixed >> 2);
    mixed ^= mixed >> 33;
    return static_cast<std::uint32_t>(mixed ^ (mixed >> 32)) | 1u;
}

[[nodiscard]] constexpr std::uint64_t
add_saturating(std::uint64_t lhs, std::uint64_t rhs) noexcept {
    return lhs > std::numeric_limits<std::uint64_t>::max() - rhs
               ? std::numeric_limits<std::uint64_t>::max()
               : lhs + rhs;
}

} // namespace

//------------------------------------------------------------------------------
// FaultLog
//------------------------------------------------------------------------------

void FaultLog::record(const FaultRecord& fault) noexcept {
    try {
        const std::lock_guard lock(mutex_);
        records_[next_] = fault;
        next_ = (next_ + 1) % capacity;
        if (count_ < capacity) {
            ++count_;
        }
    } catch (...) {
        // Diagnostics must never change the comparison's control flow.
    }
}

std::size_t FaultLog::size() const noexcept {
    try {
        const std::lock_guard lock(mutex_);
        return count_;
    } catch (...) {
        return 0;
    }
}

std::optional<FaultRecord> FaultLog::at(std::size_t index) const noexcept {
    try {
        const std::lock_guard lock(mutex_);
        if (index >= count_) {
            return std::nullopt;
        }
        const std::size_t first = count_ == capacity ? next_ : 0;
        return records_[(first + index) % capacity];
    } catch (...) {
        return std::nullopt;
    }
}

std::optional<FaultRecord> FaultLog::latest() const noexcept {
    try {
        const std::lock_guard lock(mutex_);
        if (count_ == 0) {
            return std::nullopt;
        }
        const std::size_t last = (next_ + capacity - 1) % capacity;
        return records_[last];
    } catch (...) {
        return std::nullopt;
    }
}

void FaultLog::clear() noexcept {
    try {
        const std::lock_guard lock(mutex_);
        records_ = {};
        next_ = 0;
        count_ = 0;
    } catch (...) {
    }
}

//------------------------------------------------------------------------------
// ClockSource
//------------------------------------------------------------------------------

ClockSample SteadyClockSource::sample() noexcept {
    const auto ticks = std::chrono::steady_clock::now().time_since_epoch();
    const auto value =
        std::chrono::duration_cast<std::chrono::nanoseconds>(ticks).count();
    return {static_cast<std::uint64_t>(value), true};
}

//------------------------------------------------------------------------------
// SoftwareConfirmRegister
//------------------------------------------------------------------------------

bool SoftwareConfirmRegister::latch(std::uint64_t operation_id,
                                    std::uint32_t token,
                                    bool primary_ok,
                                    bool secondary_ok) noexcept {
    if (token == 0) {
        return false;
    }

    try {
        const std::uint32_t primary_word =
            encode_bool(primary_ok, kPrimaryMagic, token);
        const std::uint32_t secondary_word =
            encode_bool(secondary_ok, kSecondaryMagic, token);

        const std::lock_guard lock(mutex_);
        operation_id_ = operation_id;
        token_ = token;
        primary_word_ = primary_word;
        primary_mirror_ = ~primary_word;
        secondary_word_ = secondary_word;
        secondary_mirror_ = ~secondary_word;
        valid_ = true;
        return true;
    } catch (...) {
        return false;
    }
}

std::optional<Confirmation>
SoftwareConfirmRegister::read(std::uint64_t operation_id,
                              std::uint32_t token) noexcept {
    try {
        const std::lock_guard lock(mutex_);
        if (!valid_ || operation_id_ != operation_id || token_ != token) {
            return std::nullopt;
        }

        bool primary_ok = false;
        bool secondary_ok = false;
        if (!valid_word_pair(primary_word_, primary_mirror_, kPrimaryMagic, token,
                             primary_ok) ||
            !valid_word_pair(secondary_word_, secondary_mirror_, kSecondaryMagic,
                             token, secondary_ok)) {
            return std::nullopt;
        }

        return Confirmation{primary_ok && secondary_ok,
                            detail::channels_consistent(primary_ok, secondary_ok)};
    } catch (...) {
        return std::nullopt;
    }
}

void SoftwareConfirmRegister::invalidate() noexcept {
    try {
        const std::lock_guard lock(mutex_);
        operation_id_ = 0;
        token_ = 0;
        primary_word_ = 0;
        primary_mirror_ = 0;
        secondary_word_ = 0;
        secondary_mirror_ = 0;
        valid_ = false;
    } catch (...) {
    }
}

//------------------------------------------------------------------------------
// Constant-time primitives
//------------------------------------------------------------------------------

bool constant_time_equal(std::span<const std::uint8_t> reference,
                         std::span<const std::uint8_t> candidate) noexcept {
    const bool primary_ok = compare_primary(reference, candidate);
    const bool secondary_ok = compare_secondary(reference, candidate);
    const detail::ChannelVerdict verdict =
        detail::reconcile_channels(primary_ok, secondary_ok);
    if (!verdict.consistent) {
        return false;
    }
    return verdict.equal;
}

bool constant_time_equal(std::string_view reference,
                         std::string_view candidate) noexcept {
    return constant_time_equal(
        std::span<const std::uint8_t>(
            reinterpret_cast<const std::uint8_t*>(reference.data()),
            reference.size()),
        std::span<const std::uint8_t>(
            reinterpret_cast<const std::uint8_t*>(candidate.data()),
            candidate.size()));
}

bool secure_compare(std::span<const std::uint8_t> reference,
                    std::span<const std::uint8_t> candidate) noexcept {
    return constant_time_equal(reference, candidate);
}

bool secure_compare(std::string_view reference,
                    std::string_view candidate) noexcept {
    return constant_time_equal(reference, candidate);
}

//------------------------------------------------------------------------------
// TimingGuard
//------------------------------------------------------------------------------

TimingGuard::TimingGuard() noexcept
    : TimingGuard(TimingPolicy{}) {}

TimingGuard::TimingGuard(TimingPolicy policy) noexcept
    : policy_(policy),
      clock_(&default_clock_),
      confirmation_(&default_confirmation_),
      sink_(nullptr) {}

TimingGuard::TimingGuard(TimingPolicy policy,
                         ClockSource& clock,
                         HardwareConfirmRegister& confirmation,
                         FaultSink* sink) noexcept
    : policy_(policy),
      clock_(&clock),
      confirmation_(&confirmation),
      sink_(sink) {}

void TimingGuard::record_fault(const FaultRecord& fault) noexcept {
    faults_.record(fault);
    if (sink_ != nullptr) {
        sink_->record(fault);
    }
}

void TimingGuard::acquire_operation_lock() noexcept {
    while (operation_lock_.test_and_set(std::memory_order_acquire)) {
        std::this_thread::yield();
    }
}

void TimingGuard::release_operation_lock() noexcept {
    operation_lock_.clear(std::memory_order_release);
}

ComparisonResult TimingGuard::reject(FaultCode code,
                                     ComparisonStatus status,
                                     ComparisonResult result,
                                     std::uint32_t detail) noexcept {
    confirmation_->invalidate();
    latched_result_.store(false, std::memory_order_release);

    const std::uint64_t resets =
        reset_count_.fetch_add(1, std::memory_order_acq_rel) + 1;
    result.equal = false;
    result.status = status;
    result.fault = code;
    result.reset_count = resets;

    record_fault(FaultRecord{code, result.operation_id, result.start_cycles,
                             add_saturating(result.start_cycles,
                                            result.elapsed_cycles),
                             detail});
    return result;
}

ComparisonResult
TimingGuard::compare(std::span<const std::uint8_t> reference,
                     std::span<const std::uint8_t> candidate) noexcept {
    acquire_operation_lock();
    const ComparisonResult result = compare_locked(reference, candidate);
    release_operation_lock();
    return result;
}

ComparisonResult
TimingGuard::compare_locked(std::span<const std::uint8_t> reference,
                            std::span<const std::uint8_t> candidate) noexcept {
    ComparisonResult result;
    const std::uint64_t operation_id =
        operation_count_.fetch_add(1, std::memory_order_acq_rel) + 1;
    result.operation_id = operation_id;

    if (!policy_is_valid(policy_)) {
        return reject(FaultCode::invalid_policy, ComparisonStatus::rejected_policy,
                      result, 0);
    }

    // Start each transaction from a cleared latch. A stale hardware value can
    // therefore never be mistaken for this operation's result.
    confirmation_->invalidate();
    latched_result_.store(false, std::memory_order_release);

    const ClockSample start = clock_->sample();
    result.start_cycles = start.cycles;
    if (!start.valid) {
        return reject(FaultCode::clock_invalid, ComparisonStatus::rejected_clock,
                      result, 1);
    }

    const bool primary_ok = compare_primary(reference, candidate);
    const bool secondary_ok = compare_secondary(reference, candidate);

    const ClockWait waited = wait_for_minimum(*clock_, start.cycles, policy_);
    result.elapsed_cycles = waited.last_cycles > start.cycles
                                ? waited.last_cycles - start.cycles
                                : 0;
    if (!waited.ok) {
        return reject(waited.fault, ComparisonStatus::rejected_clock, result, 0);
    }

    const detail::ChannelVerdict verdict =
        detail::reconcile_channels(primary_ok, secondary_ok);
    if (!verdict.consistent) {
        return reject(FaultCode::channel_mismatch,
                      ComparisonStatus::rejected_channel, result,
                      static_cast<std::uint32_t>(primary_ok) |
                          (static_cast<std::uint32_t>(secondary_ok) << 1));
    }

    const bool computed_equal = verdict.equal;
    const std::uint32_t token = confirmation_token(operation_id, start.cycles);
    if (!confirmation_->latch(operation_id, token, primary_ok, secondary_ok)) {
        return reject(FaultCode::confirmation_rejected,
                      ComparisonStatus::rejected_confirmation, result, 0);
    }

    const std::optional<Confirmation> confirmed =
        confirmation_->read(operation_id, token);
    if (!confirmed.has_value()) {
        return reject(FaultCode::confirmation_rejected,
                      ComparisonStatus::rejected_confirmation, result, 1);
    }
    if (!confirmed->channels_consistent) {
        return reject(FaultCode::channel_mismatch,
                      ComparisonStatus::rejected_channel, result, 4);
    }
    if (confirmed->result != computed_equal) {
        return reject(FaultCode::confirmation_mismatch,
                      ComparisonStatus::rejected_confirmation, result, 2);
    }

    // Confirm the clock remained monotonic and within policy through the
    // hardware latch/read-back, then require the whole operation to have met
    // its minimum period.
    const ClockSample finish = clock_->sample();
    if (!finish.valid) {
        return reject(FaultCode::clock_invalid, ComparisonStatus::rejected_clock,
                      result, 2);
    }
    if (finish.cycles < start.cycles || finish.cycles < waited.last_cycles) {
        return reject(FaultCode::clock_reversed, ComparisonStatus::rejected_clock,
                      result, 1);
    }

    result.elapsed_cycles = finish.cycles - start.cycles;
    if (result.elapsed_cycles > policy_.maximum_cycles) {
        return reject(FaultCode::clock_rate_violation,
                      ComparisonStatus::rejected_clock, result, 0);
    }
    if (result.elapsed_cycles < policy_.minimum_cycles) {
        return reject(FaultCode::clock_reversed, ComparisonStatus::rejected_clock,
                      result, 3);
    }

    // The caller-visible success bit comes only from the hardware read-back.
    result.equal = confirmed->result;
    result.status =
        result.equal ? ComparisonStatus::equal : ComparisonStatus::not_equal;
    result.fault = FaultCode::none;
    result.reset_count = reset_count_.load(std::memory_order_acquire);
    latched_result_.store(result.equal, std::memory_order_release);
    return result;
}

ComparisonResult TimingGuard::compare(std::string_view reference,
                                      std::string_view candidate) noexcept {
    return compare(
        std::span<const std::uint8_t>(
            reinterpret_cast<const std::uint8_t*>(reference.data()),
            reference.size()),
        std::span<const std::uint8_t>(
            reinterpret_cast<const std::uint8_t*>(candidate.data()),
            candidate.size()));
}

void TimingGuard::reset() noexcept {
    acquire_operation_lock();
    confirmation_->invalidate();
    latched_result_.store(false, std::memory_order_release);
    reset_count_.fetch_add(1, std::memory_order_acq_rel);
    release_operation_lock();
}

TimingPolicy TimingGuard::policy() const noexcept {
    return policy_;
}

std::uint64_t TimingGuard::reset_count() const noexcept {
    return reset_count_.load(std::memory_order_acquire);
}

std::uint64_t TimingGuard::operation_count() const noexcept {
    return operation_count_.load(std::memory_order_acquire);
}

bool TimingGuard::latched_result() const noexcept {
    return latched_result_.load(std::memory_order_acquire);
}

const FaultLog& TimingGuard::faults() const noexcept {
    return faults_;
}

std::optional<FaultRecord> TimingGuard::last_fault() const noexcept {
    return faults_.latest();
}

} // namespace netsec

#undef NETSEC_NOINLINE
#undef NETSEC_COMPILER_BARRIER
