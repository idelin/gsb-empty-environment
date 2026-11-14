#include "netsec.hpp"

#include <array>
#include <atomic>
#include <cstdint>
#include <exception>
#include <iostream>
#include <span>
#include <string>
#include <string_view>
#include <thread>
#include <utility>
#include <vector>

namespace {

using netsec::ClockSample;
using netsec::ClockSource;
using netsec::ComparisonStatus;
using netsec::Confirmation;
using netsec::FaultCode;
using netsec::HardwareConfirmRegister;
using netsec::TimingGuard;
using netsec::TimingPolicy;

class CheckFailure final : public std::exception {
public:
    explicit CheckFailure(std::string expression) : expression_(std::move(expression)) {}
    [[nodiscard]] const char* what() const noexcept override {
        return expression_.c_str();
    }

private:
    std::string expression_;
};

#define CHECK(expression)                                                      \
    do {                                                                       \
        if (!(expression)) {                                                   \
            throw CheckFailure(#expression " at " __FILE__ ":" +               \
                               std::to_string(__LINE__));                      \
        }                                                                      \
    } while (false)

class CounterClock final : public ClockSource {
public:
    explicit CounterClock(std::uint64_t step) : step_(step) {}

    [[nodiscard]] ClockSample sample() noexcept override {
        value_ += step_;
        return {value_, true};
    }

private:
    std::uint64_t value_{};
    std::uint64_t step_;
};

class ScriptedClock final : public ClockSource {
public:
    ScriptedClock(std::initializer_list<ClockSample> samples) : samples_(samples) {}

    [[nodiscard]] ClockSample sample() noexcept override {
        if (samples_.empty()) {
            return {0, false};
        }
        if (index_ < samples_.size()) {
            return samples_[index_++];
        }
        return samples_.back();
    }

private:
    std::vector<ClockSample> samples_;
    std::size_t index_{};
};

class AtomicCounterClock final : public ClockSource {
public:
    explicit AtomicCounterClock(std::uint64_t step) : step_(step) {}

    [[nodiscard]] ClockSample sample() noexcept override {
        const std::uint64_t value =
            value_.fetch_add(step_, std::memory_order_relaxed) + step_;
        return {value, true};
    }

private:
    std::atomic<std::uint64_t> value_{0};
    std::uint64_t step_;
};

class RecordingRegister final : public HardwareConfirmRegister {
public:
    [[nodiscard]] bool latch(std::uint64_t operation_id,
                             std::uint32_t token,
                             bool primary_ok,
                             bool secondary_ok) noexcept override {
        ++latch_calls;
        last_primary = primary_ok;
        last_secondary = secondary_ok;
        return inner_.latch(operation_id, token, primary_ok, secondary_ok);
    }

    [[nodiscard]] std::optional<Confirmation>
    read(std::uint64_t operation_id, std::uint32_t token) noexcept override {
        return inner_.read(operation_id, token);
    }

    void invalidate() noexcept override {
        ++invalidate_calls;
        inner_.invalidate();
    }

    std::uint64_t latch_calls{};
    std::uint64_t invalidate_calls{};
    bool last_primary{};
    bool last_secondary{};

private:
    netsec::SoftwareConfirmRegister inner_;
};

class RejectingRegister final : public HardwareConfirmRegister {
public:
    [[nodiscard]] bool latch(std::uint64_t,
                             std::uint32_t,
                             bool,
                             bool) noexcept override {
        return false;
    }

    [[nodiscard]] std::optional<Confirmation>
    read(std::uint64_t, std::uint32_t) noexcept override {
        return std::nullopt;
    }

    void invalidate() noexcept override {
        ++invalidate_calls;
    }

    std::uint64_t invalidate_calls{};
};

enum class RegisterFault {
    publish_true,
    inconsistent_channels,
};

class FaultyRegister final : public HardwareConfirmRegister {
public:
    explicit FaultyRegister(RegisterFault fault) : fault_(fault) {}

    [[nodiscard]] bool latch(std::uint64_t operation_id,
                             std::uint32_t token,
                             bool primary_ok,
                             bool secondary_ok) noexcept override {
        operation_id_ = operation_id;
        token_ = token;
        primary_ = primary_ok;
        secondary_ = secondary_ok;
        valid_ = true;
        return true;
    }

    [[nodiscard]] std::optional<Confirmation>
    read(std::uint64_t operation_id, std::uint32_t token) noexcept override {
        if (!valid_ || operation_id != operation_id_ || token != token_) {
            return std::nullopt;
        }
        if (fault_ == RegisterFault::publish_true) {
            return Confirmation{true, true};
        }
        return Confirmation{primary_ && secondary_, false};
    }

    void invalidate() noexcept override {
        valid_ = false;
    }

private:
    RegisterFault fault_;
    std::uint64_t operation_id_{};
    std::uint32_t token_{};
    bool primary_{};
    bool secondary_{};
    bool valid_{};
};

class CountingSink final : public netsec::FaultSink {
public:
    void record(const netsec::FaultRecord& fault) noexcept override {
        ++calls;
        last = fault;
    }

    std::uint64_t calls{};
    netsec::FaultRecord last{};
};

[[nodiscard]] std::array<std::uint8_t, 32> sample_key() {
    std::array<std::uint8_t, 32> key{};
    for (std::size_t index = 0; index < key.size(); ++index) {
        key[index] = static_cast<std::uint8_t>(index * 7u + 3u);
    }
    return key;
}

void test_constant_time_primitive() {
    const auto key = sample_key();
    CHECK(netsec::constant_time_equal(key, key));
    CHECK(netsec::secure_compare(key, key));

    for (const std::size_t position : {std::size_t{0}, std::size_t{7},
                                       key.size() - 1}) {
        auto changed = key;
        changed[position] ^= 0x80u;
        CHECK(!netsec::constant_time_equal(key, changed));
        CHECK(!netsec::secure_compare(key, changed));
    }

    const std::span<const std::uint8_t> prefix(key.data(), key.size() - 1);
    CHECK(!netsec::constant_time_equal(prefix, key));
    CHECK(netsec::constant_time_equal(std::string_view{}, std::string_view{}));
    CHECK(netsec::constant_time_equal("same", "same"));
    CHECK(!netsec::secure_compare("same", "diff"));
}

void test_legal_operation_is_latched() {
    TimingPolicy policy{64, 10'000, 32};
    CounterClock clock(7);
    RecordingRegister register_;
    TimingGuard guard(policy, clock, register_);
    const auto key = sample_key();

    const auto result = guard.compare(key, key);
    CHECK(result.equal);
    CHECK(result.status == ComparisonStatus::equal);
    CHECK(result.fault == FaultCode::none);
    CHECK(result.elapsed_cycles >= policy.minimum_cycles);
    CHECK(result.reset_count == 0);
    CHECK(guard.latched_result());
    CHECK(guard.operation_count() == 1);
    CHECK(!guard.last_fault().has_value());
    CHECK(register_.latch_calls == 1);
    CHECK(register_.last_primary);
    CHECK(register_.last_secondary);
}

void test_first_byte_mismatch_is_not_short_circuited() {
    TimingPolicy policy{64, 10'000, 32};
    const auto key = sample_key();

    auto run = [&](std::size_t position) {
        auto changed = key;
        changed[position] ^= 1u;
        CounterClock clock(7);
        RecordingRegister register_;
        TimingGuard guard(policy, clock, register_);
        const auto result = guard.compare(key, changed);
        CHECK(!result.equal);
        CHECK(result.status == ComparisonStatus::not_equal);
        CHECK(result.elapsed_cycles >= policy.minimum_cycles);
        CHECK(register_.latch_calls == 1);
        CHECK(!register_.last_primary);
        CHECK(!register_.last_secondary);
        return result;
    };

    const auto first = run(0);
    const auto last = run(key.size() - 1);
    CHECK(first.elapsed_cycles == last.elapsed_cycles);
}

void test_invalid_clock_glitch_resets_and_logs() {
    TimingPolicy policy{64, 10'000, 32};
    ScriptedClock clock({ClockSample{100, true}, ClockSample{0, false}});
    RecordingRegister register_;
    CountingSink sink;
    TimingGuard guard(policy, clock, register_, &sink);
    const auto key = sample_key();

    const auto result = guard.compare(key, key);
    CHECK(!result.equal);
    CHECK(result.status == ComparisonStatus::rejected_clock);
    CHECK(result.fault == FaultCode::clock_invalid);
    CHECK(result.reset_count == 1);
    CHECK(!guard.latched_result());
    CHECK(register_.latch_calls == 0);
    CHECK(register_.invalidate_calls >= 2);
    CHECK(guard.last_fault().has_value());
    CHECK(guard.last_fault()->code == FaultCode::clock_invalid);
    CHECK(sink.calls == 1);
    CHECK(sink.last.code == FaultCode::clock_invalid);
}

void test_reversed_clock_resets() {
    TimingPolicy policy{64, 10'000, 32};
    ScriptedClock clock({ClockSample{1'000, true}, ClockSample{999, true}});
    RecordingRegister register_;
    TimingGuard guard(policy, clock, register_);
    const auto key = sample_key();

    const auto result = guard.compare(key, key);
    CHECK(!result.equal);
    CHECK(result.status == ComparisonStatus::rejected_clock);
    CHECK(result.fault == FaultCode::clock_reversed);
    CHECK(guard.reset_count() == 1);
    CHECK(!guard.latched_result());
}

void test_overclock_glitch_is_rejected() {
    TimingPolicy policy{64, 1'000, 32};
    ScriptedClock clock({ClockSample{100, true}, ClockSample{1'101, true}});
    RecordingRegister register_;
    TimingGuard guard(policy, clock, register_);
    const auto key = sample_key();

    const auto result = guard.compare(key, key);
    CHECK(!result.equal);
    CHECK(result.status == ComparisonStatus::rejected_clock);
    CHECK(result.fault == FaultCode::clock_rate_violation);
    CHECK(guard.reset_count() == 1);
}

void test_frozen_clock_is_rejected() {
    TimingPolicy policy{64, 10'000, 4};
    ScriptedClock clock({ClockSample{77, true}});
    RecordingRegister register_;
    TimingGuard guard(policy, clock, register_);
    const auto key = sample_key();

    const auto result = guard.compare(key, key);
    CHECK(!result.equal);
    CHECK(result.status == ComparisonStatus::rejected_clock);
    CHECK(result.fault == FaultCode::clock_stalled);
    CHECK(guard.reset_count() == 1);
}

void test_confirmation_failure_is_rejected() {
    TimingPolicy policy{64, 10'000, 32};
    CounterClock clock(7);
    RejectingRegister register_;
    TimingGuard guard(policy, clock, register_);
    const auto key = sample_key();

    const auto result = guard.compare(key, key);
    CHECK(!result.equal);
    CHECK(result.status == ComparisonStatus::rejected_confirmation);
    CHECK(result.fault == FaultCode::confirmation_rejected);
    CHECK(guard.reset_count() == 1);
    CHECK(!guard.latched_result());
    CHECK(register_.invalidate_calls >= 2);
}

void test_hardware_cannot_publish_false_success() {
    TimingPolicy policy{64, 10'000, 32};
    CounterClock clock(7);
    FaultyRegister register_(RegisterFault::publish_true);
    TimingGuard guard(policy, clock, register_);
    auto key = sample_key();
    auto changed = key;
    changed[0] ^= 1u;

    const auto result = guard.compare(key, changed);
    CHECK(!result.equal);
    CHECK(result.status == ComparisonStatus::rejected_confirmation);
    CHECK(result.fault == FaultCode::confirmation_mismatch);
    CHECK(guard.reset_count() == 1);
}

void test_inconsistent_hardware_channels_are_rejected() {
    const auto mismatch_a = netsec::detail::reconcile_channels(true, false);
    const auto mismatch_b = netsec::detail::reconcile_channels(false, true);
    const auto match_true = netsec::detail::reconcile_channels(true, true);
    const auto match_false = netsec::detail::reconcile_channels(false, false);
    CHECK(!mismatch_a.consistent);
    CHECK(!mismatch_a.equal);
    CHECK(!mismatch_b.consistent);
    CHECK(!mismatch_b.equal);
    CHECK(match_true.consistent);
    CHECK(match_true.equal);
    CHECK(match_false.consistent);
    CHECK(!match_false.equal);

    TimingPolicy policy{64, 10'000, 32};
    CounterClock clock(7);
    FaultyRegister register_(RegisterFault::inconsistent_channels);
    TimingGuard guard(policy, clock, register_);
    const auto key = sample_key();

    const auto result = guard.compare(key, key);
    CHECK(!result.equal);
    CHECK(result.status == ComparisonStatus::rejected_channel);
    CHECK(result.fault == FaultCode::channel_mismatch);
    CHECK(guard.reset_count() == 1);
}

void test_reset_clears_latch_and_allows_retry() {
    TimingPolicy policy{64, 10'000, 32};
    CounterClock clock(7);
    RecordingRegister register_;
    TimingGuard guard(policy, clock, register_);
    const auto key = sample_key();

    CHECK(guard.compare(key, key).equal);
    CHECK(guard.latched_result());
    const auto invalidations_before_reset = register_.invalidate_calls;

    guard.reset();
    CHECK(!guard.latched_result());
    CHECK(guard.reset_count() == 1);
    CHECK(register_.invalidate_calls > invalidations_before_reset);

    const auto retry = guard.compare(key, key);
    CHECK(retry.equal);
    CHECK(retry.reset_count == 1);
    CHECK(guard.latched_result());
}

void test_invalid_policy_fails_closed() {
    TimingPolicy policy{0, 10'000, 32};
    CounterClock clock(7);
    RecordingRegister register_;
    TimingGuard guard(policy, clock, register_);
    const auto key = sample_key();

    const auto result = guard.compare(key, key);
    CHECK(!result.equal);
    CHECK(result.status == ComparisonStatus::rejected_policy);
    CHECK(result.fault == FaultCode::invalid_policy);
    CHECK(guard.reset_count() == 1);
    CHECK(!guard.latched_result());
}

void test_software_confirmation_register_rejects_stale_reads() {
    netsec::SoftwareConfirmRegister register_;
    CHECK(register_.latch(7, 0x1234'5679u, true, true));
    CHECK(register_.read(7, 0x1234'5679u).has_value());
    CHECK(register_.read(7, 0x1234'5678u) == std::nullopt);
    CHECK(register_.read(8, 0x1234'5679u) == std::nullopt);

    register_.invalidate();
    CHECK(register_.read(7, 0x1234'5679u) == std::nullopt);
}

void test_concurrent_calls_are_serialized_per_guard() {
    TimingPolicy policy{32, 100'000, 64};
    AtomicCounterClock clock(3);
    RecordingRegister register_;
    TimingGuard guard(policy, clock, register_);
    const auto key = sample_key();
    std::atomic<std::uint64_t> successes{0};
    std::vector<std::thread> workers;

    for (int worker = 0; worker < 4; ++worker) {
        workers.emplace_back([&] {
            for (int iteration = 0; iteration < 16; ++iteration) {
                if (guard.compare(key, key)) {
                    successes.fetch_add(1, std::memory_order_relaxed);
                }
            }
        });
    }
    for (auto& worker : workers) {
        worker.join();
    }

    CHECK(successes.load() == 64);
    CHECK(guard.operation_count() == 64);
    CHECK(guard.reset_count() == 0);
    CHECK(!guard.last_fault().has_value());
    CHECK(guard.latched_result());
}

} // namespace

int main() {
    const std::vector<std::pair<std::string_view, void (*)()>> tests{
        {"constant_time_primitive", test_constant_time_primitive},
        {"legal_operation_is_latched", test_legal_operation_is_latched},
        {"first_byte_mismatch_is_not_short_circuited",
         test_first_byte_mismatch_is_not_short_circuited},
        {"invalid_clock_glitch_resets_and_logs",
         test_invalid_clock_glitch_resets_and_logs},
        {"reversed_clock_resets", test_reversed_clock_resets},
        {"overclock_glitch_is_rejected", test_overclock_glitch_is_rejected},
        {"frozen_clock_is_rejected", test_frozen_clock_is_rejected},
        {"confirmation_failure_is_rejected", test_confirmation_failure_is_rejected},
        {"hardware_cannot_publish_false_success",
         test_hardware_cannot_publish_false_success},
        {"inconsistent_hardware_channels_are_rejected",
         test_inconsistent_hardware_channels_are_rejected},
        {"reset_clears_latch_and_allows_retry",
         test_reset_clears_latch_and_allows_retry},
        {"invalid_policy_fails_closed", test_invalid_policy_fails_closed},
        {"software_confirmation_register_rejects_stale_reads",
         test_software_confirmation_register_rejects_stale_reads},
        {"concurrent_calls_are_serialized_per_guard",
         test_concurrent_calls_are_serialized_per_guard},
    };

    std::size_t failures = 0;
    for (const auto& [name, test] : tests) {
        try {
            test();
            std::cout << "PASS " << name << '\n';
        } catch (const std::exception& error) {
            ++failures;
            std::cerr << "FAIL " << name << ": " << error.what() << '\n';
        }
    }

    if (failures != 0) {
        std::cerr << failures << " test(s) failed\n";
        return 1;
    }
    std::cout << tests.size() << " tests passed\n";
    return 0;
}
