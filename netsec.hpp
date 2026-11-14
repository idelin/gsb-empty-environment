#pragma once

#include <array>
#include <atomic>
#include <cstddef>
#include <cstdint>
#include <mutex>
#include <optional>
#include <span>
#include <string_view>

namespace netsec {

//------------------------------------------------------------------------------
// Diagnostics
//------------------------------------------------------------------------------

enum class FaultCode : std::uint8_t {
    none = 0,
    invalid_policy,
    clock_invalid,
    clock_stalled,
    clock_reversed,
    clock_rate_violation,
    channel_mismatch,
    confirmation_rejected,
    confirmation_mismatch,
};

[[nodiscard]] constexpr std::string_view to_string(FaultCode code) noexcept {
    switch (code) {
    case FaultCode::none:
        return "none";
    case FaultCode::invalid_policy:
        return "invalid_policy";
    case FaultCode::clock_invalid:
        return "clock_invalid";
    case FaultCode::clock_stalled:
        return "clock_stalled";
    case FaultCode::clock_reversed:
        return "clock_reversed";
    case FaultCode::clock_rate_violation:
        return "clock_rate_violation";
    case FaultCode::channel_mismatch:
        return "channel_mismatch";
    case FaultCode::confirmation_rejected:
        return "confirmation_rejected";
    case FaultCode::confirmation_mismatch:
        return "confirmation_mismatch";
    }
    return "unknown";
}

struct FaultRecord {
    FaultCode code{FaultCode::none};
    std::uint64_t operation_id{};
    std::uint64_t start_cycles{};
    std::uint64_t observed_cycles{};
    std::uint32_t detail{};
};

class FaultSink {
public:
    virtual ~FaultSink() = default;
    virtual void record(const FaultRecord& fault) noexcept = 0;
};

// Fixed-size so fault reporting never needs to allocate.
class FaultLog final {
public:
    static constexpr std::size_t capacity = 32;

    void record(const FaultRecord& fault) noexcept;
    [[nodiscard]] std::size_t size() const noexcept;
    [[nodiscard]] std::optional<FaultRecord> at(std::size_t index) const noexcept;
    [[nodiscard]] std::optional<FaultRecord> latest() const noexcept;
    void clear() noexcept;

private:
    mutable std::mutex mutex_;
    std::array<FaultRecord, capacity> records_{};
    std::size_t next_{};
    std::size_t count_{};
};

//------------------------------------------------------------------------------
// Hardware-facing interfaces
//------------------------------------------------------------------------------

struct ClockSample {
    std::uint64_t cycles{};
    bool valid{false};
};

// A deployment adapter should read a hardware cycle counter or clock monitor.
// "cycles" is an opaque monotonic tick count; it does not have to be a CPU clock.
class ClockSource {
public:
    virtual ~ClockSource() = default;
    [[nodiscard]] virtual ClockSample sample() noexcept = 0;
};

// Portable fallback using steady_clock ticks. Production code should prefer a
// hardware-backed ClockSource so a glitched CPU clock is observable.
class SteadyClockSource final : public ClockSource {
public:
    [[nodiscard]] ClockSample sample() noexcept override;
};

struct Confirmation {
    bool result{false};
    bool channels_consistent{false};
};

// A hardware adapter latches both channel decisions into a write-protected
// register and returns them only for the matching transaction token. The
// register must compute/retain the final result in hardware; software-provided
// success bits alone are not sufficient in a real deployment.
class HardwareConfirmRegister {
public:
    virtual ~HardwareConfirmRegister() = default;

    [[nodiscard]] virtual bool latch(std::uint64_t operation_id,
                                     std::uint32_t token,
                                     bool primary_ok,
                                     bool secondary_ok) noexcept = 0;
    [[nodiscard]] virtual std::optional<Confirmation>
    read(std::uint64_t operation_id, std::uint32_t token) noexcept = 0;
    virtual void invalidate() noexcept = 0;
};

// Redundant software model of a hardware confirmation register. The two channel
// decisions and their complements are stored separately; a torn or inconsistent
// read is rejected.
class SoftwareConfirmRegister final : public HardwareConfirmRegister {
public:
    [[nodiscard]] bool latch(std::uint64_t operation_id,
                             std::uint32_t token,
                             bool primary_ok,
                             bool secondary_ok) noexcept override;
    [[nodiscard]] std::optional<Confirmation>
    read(std::uint64_t operation_id, std::uint32_t token) noexcept override;
    void invalidate() noexcept override;

private:
    std::mutex mutex_;
    std::uint64_t operation_id_{};
    std::uint32_t token_{};
    std::uint32_t primary_word_{};
    std::uint32_t primary_mirror_{};
    std::uint32_t secondary_word_{};
    std::uint32_t secondary_mirror_{};
    bool valid_{false};
};

//------------------------------------------------------------------------------
// Guarded comparison
//------------------------------------------------------------------------------

struct TimingPolicy {
    // The complete compare/latch sequence must consume at least this many ticks.
    std::uint64_t minimum_cycles{512};

    // A single observation beyond this many ticks is treated as a clock-rate
    // fault (for example an injected over-frequency glitch).
    std::uint64_t maximum_cycles{50'000'000};

    // A source that does not advance for this many consecutive probes is
    // considered stalled. This also bounds recovery from a frozen clock.
    std::uint64_t maximum_stalled_samples{1'000'000};
};

enum class ComparisonStatus : std::uint8_t {
    equal,
    not_equal,
    rejected_policy,
    rejected_clock,
    rejected_channel,
    rejected_confirmation,
};

struct ComparisonResult {
    bool equal{false};
    ComparisonStatus status{ComparisonStatus::rejected_policy};
    FaultCode fault{FaultCode::invalid_policy};
    std::uint64_t operation_id{};
    std::uint64_t start_cycles{};
    std::uint64_t elapsed_cycles{};
    std::uint64_t reset_count{};

    [[nodiscard]] constexpr explicit operator bool() const noexcept {
        return equal && status == ComparisonStatus::equal;
    }
};

class TimingGuard final {
public:
    TimingGuard() noexcept;
    explicit TimingGuard(TimingPolicy policy) noexcept;
    TimingGuard(TimingPolicy policy,
                ClockSource& clock,
                HardwareConfirmRegister& confirmation,
                FaultSink* sink = nullptr) noexcept;
    ~TimingGuard() = default;

    TimingGuard(const TimingGuard&) = delete;
    TimingGuard& operator=(const TimingGuard&) = delete;
    TimingGuard(TimingGuard&&) = delete;
    TimingGuard& operator=(TimingGuard&&) = delete;

    // Neither length mismatch nor a byte mismatch returns early. Both channels
    // traverse the full public length, wait for the minimum period, and only a
    // matching hardware read-back can produce equal == true.
    [[nodiscard]] ComparisonResult
    compare(std::span<const std::uint8_t> reference,
            std::span<const std::uint8_t> candidate) noexcept;

    [[nodiscard]] ComparisonResult compare(std::string_view reference,
                                           std::string_view candidate) noexcept;

    // Clears the latched result and invalidates the confirmation register.
    // The fault log is retained.
    void reset() noexcept;

    [[nodiscard]] TimingPolicy policy() const noexcept;
    [[nodiscard]] std::uint64_t reset_count() const noexcept;
    [[nodiscard]] std::uint64_t operation_count() const noexcept;
    [[nodiscard]] bool latched_result() const noexcept;
    [[nodiscard]] const FaultLog& faults() const noexcept;
    [[nodiscard]] std::optional<FaultRecord> last_fault() const noexcept;

private:
    [[nodiscard]] ComparisonResult
    compare_locked(std::span<const std::uint8_t> reference,
                   std::span<const std::uint8_t> candidate) noexcept;
    [[nodiscard]] ComparisonResult reject(FaultCode code,
                                          ComparisonStatus status,
                                          ComparisonResult result,
                                          std::uint32_t detail) noexcept;
    void record_fault(const FaultRecord& fault) noexcept;
    void acquire_operation_lock() noexcept;
    void release_operation_lock() noexcept;

    TimingPolicy policy_;
    SteadyClockSource default_clock_;
    SoftwareConfirmRegister default_confirmation_;
    ClockSource* clock_;
    HardwareConfirmRegister* confirmation_;
    FaultSink* sink_;
    FaultLog faults_;
    std::atomic_flag operation_lock_;
    std::atomic<std::uint64_t> reset_count_{0};
    std::atomic<std::uint64_t> operation_count_{0};
    std::atomic<bool> latched_result_{false};
};

// Stand-alone constant-time primitive. It performs both redundant channels and
// accepts only when the channels agree. Use TimingGuard when clock monitoring and
// hardware confirmation are required.
[[nodiscard]] bool
constant_time_equal(std::span<const std::uint8_t> reference,
                    std::span<const std::uint8_t> candidate) noexcept;

[[nodiscard]] bool constant_time_equal(std::string_view reference,
                                       std::string_view candidate) noexcept;

// Common spelling retained for callers that only need the constant-time check.
[[nodiscard]] bool
secure_compare(std::span<const std::uint8_t> reference,
               std::span<const std::uint8_t> candidate) noexcept;

[[nodiscard]] bool secure_compare(std::string_view reference,
                                  std::string_view candidate) noexcept;

namespace detail {

struct ChannelVerdict {
    bool consistent{false};
    bool equal{false};
};

// Exposed for fault-injection tests; TimingGuard uses this same decision.
[[nodiscard]] constexpr ChannelVerdict
reconcile_channels(bool primary_ok, bool secondary_ok) noexcept {
    return {primary_ok == secondary_ok, primary_ok && secondary_ok};
}

[[nodiscard]] constexpr bool
channels_consistent(bool primary_ok, bool secondary_ok) noexcept {
    return reconcile_channels(primary_ok, secondary_ok).consistent;
}

} // namespace detail
} // namespace netsec
