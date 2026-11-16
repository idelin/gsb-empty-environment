#include "netsec.hpp"

#include <algorithm>
#include <chrono>
#include <cstdint>
#include <cstdlib>
#include <filesystem>
#include <iostream>
#include <stdexcept>
#include <string>
#include <string_view>
#include <system_error>

#include <unistd.h>

namespace {

using namespace std::chrono_literals;
using netsec::PinCommitment;
using netsec::PinDigits;
using netsec::RegistrarMac;
using netsec::VerificationCode;
using netsec::VerificationResult;
using netsec::WpsPinVerifier;

int failures = 0;

void check(bool condition, const char* expression, int line) {
    if (!condition) {
        std::cerr << "FAIL line " << line << ": " << expression << '\n';
        ++failures;
    }
}

#define CHECK(expression) check(static_cast<bool>(expression), #expression, __LINE__)

class TempDirectory {
public:
    TempDirectory() {
        const std::filesystem::path base =
            std::filesystem::temp_directory_path();
        for (unsigned attempt = 0; attempt < 100; ++attempt) {
            path_ = base / ("netsec-test-" + std::to_string(::getpid()) + "-" +
                            std::to_string(attempt));
            std::error_code error;
            if (std::filesystem::create_directory(path_, error)) {
                return;
            }
            if (error && error != std::errc::file_exists) {
                throw std::runtime_error("unable to create test directory");
            }
        }
        throw std::runtime_error("unable to allocate test directory");
    }

    ~TempDirectory() {
        std::error_code error;
        std::filesystem::remove_all(path_, error);
    }

    [[nodiscard]] std::filesystem::path file(std::string_view name) const {
        return path_ / std::string(name);
    }

private:
    std::filesystem::path path_;
};

class FakeClock {
public:
    FakeClock()
        : now_(std::chrono::system_clock::time_point{1700000000s}) {}

    [[nodiscard]] WpsPinVerifier::Clock function() {
        return [this] { return now_; };
    }

    void advance(std::chrono::seconds duration) {
        now_ += duration;
    }

private:
    std::chrono::system_clock::time_point now_;
};

PinDigits pin_from_halves(unsigned first, unsigned second) {
    PinDigits pin{};
    first %= 10000U;
    second %= 1000U;
    pin[0] = static_cast<std::uint8_t>(first / 1000U);
    pin[1] = static_cast<std::uint8_t>((first / 100U) % 10U);
    pin[2] = static_cast<std::uint8_t>((first / 10U) % 10U);
    pin[3] = static_cast<std::uint8_t>(first % 10U);
    pin[4] = static_cast<std::uint8_t>(second / 100U);
    pin[5] = static_cast<std::uint8_t>((second / 10U) % 10U);
    pin[6] = static_cast<std::uint8_t>(second % 10U);
    pin[7] = netsec::wps_checksum(pin);
    return pin;
}

RegistrarMac mac_with_last_byte(std::uint8_t value) {
    return RegistrarMac{0x02, 0x11, 0x22, 0x33, 0x44, value};
}

void test_checksum_and_parsing() {
    const PinDigits known = *netsec::parse_pin("12345670");
    CHECK(netsec::wps_checksum(known) == 0);
    CHECK(netsec::wps_checksum_valid(known));
    CHECK(netsec::pin_to_string(known) == "12345670");

    PinDigits bad_checksum = known;
    bad_checksum[7] = 1;
    CHECK(!netsec::wps_checksum_valid(bad_checksum));

    PinDigits bad_digit = known;
    bad_digit[2] = 10;
    CHECK(!netsec::wps_checksum_valid(bad_digit));

    CHECK(!netsec::parse_pin("1234567").has_value());
    CHECK(!netsec::parse_pin("1234567x").has_value());
    CHECK(!netsec::parse_pin("123456700").has_value());

    bool rejected = false;
    try {
        WpsPinVerifier invalid_expected("12345671", "unused-state");
    } catch (const std::invalid_argument&) {
        rejected = true;
    }
    CHECK(rejected);
}

void test_checksum_rejected_before_protocol() {
    TempDirectory directory;
    FakeClock clock;
    WpsPinVerifier verifier("12345670", directory.file("checksum.state").string(),
                            {}, clock.function());

    for (int attempt = 0; attempt < 5; ++attempt) {
        const VerificationResult result = verifier.verify("12345671");
        CHECK(result.code == VerificationCode::InvalidChecksum);
        CHECK(result.retry_after == 0s);
    }
    CHECK(verifier.failure_count() == 0);
    CHECK(!verifier.is_locked());

    const VerificationResult malformed = verifier.verify("1234");
    CHECK(malformed.code == VerificationCode::InvalidPin);
    CHECK(verifier.failure_count() == 0);
}

void test_half_failures_have_one_result() {
    TempDirectory directory;
    FakeClock clock;
    netsec::LockoutPolicy policy;
    policy.failure_threshold = 10;

    WpsPinVerifier first_half_verifier(
        "12345670", directory.file("first.state").string(), policy,
        clock.function());
    WpsPinVerifier second_half_verifier(
        "12345670", directory.file("second.state").string(), policy,
        clock.function());

    const VerificationResult first_half =
        first_half_verifier.verify(pin_from_halves(2234, 567));
    const VerificationResult second_half =
        second_half_verifier.verify(pin_from_halves(1234, 568));

    CHECK(first_half.code == VerificationCode::Failure);
    CHECK(second_half.code == VerificationCode::Failure);
    CHECK(first_half == second_half);
    CHECK(first_half_verifier.failure_count() == 1);
    CHECK(second_half_verifier.failure_count() == 1);

    const VerificationResult commitment = first_half_verifier.verify(
        PinCommitment{{2, 2, 3, 4}, {5, 6, 7}, 7});
    CHECK(commitment.code == VerificationCode::Failure);
    CHECK(first_half_verifier.failure_count() == 2);
}

void test_lockout_persistence_and_mac_change() {
    TempDirectory directory;
    FakeClock clock;
    const std::string state = directory.file("lock.state").string();

    {
        WpsPinVerifier verifier("12345670", state, {}, clock.function());
        const PinDigits wrong_first = pin_from_halves(2234, 567);
        const RegistrarMac original_mac = mac_with_last_byte(0x55);

        CHECK(verifier.verify(wrong_first, original_mac).code ==
              VerificationCode::Failure);
        CHECK(verifier.verify(wrong_first, original_mac).code ==
              VerificationCode::Failure);
        const VerificationResult lock =
            verifier.verify(wrong_first, original_mac);
        CHECK(lock.code == VerificationCode::Locked);
        CHECK(lock.retry_after == 60s);
        CHECK(verifier.is_locked());
        CHECK(verifier.remaining_lockout() == 60s);
        CHECK(verifier.failure_count() == 0);
        CHECK(verifier.lockout_count() == 1);
    }

    {
        clock.advance(25s);
        WpsPinVerifier restarted("12345670", state, {}, clock.function());
        CHECK(restarted.is_locked());
        CHECK(restarted.remaining_lockout() == 35s);

        const RegistrarMac changed_mac = mac_with_last_byte(0x99);
        CHECK(restarted.verify("12345670", changed_mac).code ==
              VerificationCode::Locked);
        CHECK(restarted.verify("12345671", changed_mac).code ==
              VerificationCode::Locked);
        CHECK(restarted.verify("not-a-pin", changed_mac).code ==
              VerificationCode::Locked);
        CHECK(restarted.failure_count() == 0);
        CHECK(restarted.lockout_count() == 1);

        clock.advance(34s);
        const VerificationResult almost =
            restarted.verify("12345670", changed_mac);
        CHECK(almost.code == VerificationCode::Locked);
        CHECK(almost.retry_after == 1s);

        clock.advance(1s);
        CHECK(restarted.verify("12345670", changed_mac).code ==
              VerificationCode::Success);
        CHECK(!restarted.is_locked());
        CHECK(restarted.failure_count() == 0);
        CHECK(restarted.lockout_count() == 0);
    }
}

void test_failure_count_persists_across_mac_change() {
    TempDirectory directory;
    FakeClock clock;
    const std::string state = directory.file("failure-count.state").string();
    const PinDigits wrong = pin_from_halves(2234, 567);
    const RegistrarMac first_mac = mac_with_last_byte(0x10);
    const RegistrarMac second_mac = mac_with_last_byte(0x20);

    {
        WpsPinVerifier verifier("12345670", state, {}, clock.function());
        CHECK(verifier.verify(wrong, first_mac).code ==
              VerificationCode::Failure);
        CHECK(verifier.verify(wrong, first_mac).code ==
              VerificationCode::Failure);
        CHECK(verifier.failure_count() == 2);
    }

    WpsPinVerifier restarted("12345670", state, {}, clock.function());
    CHECK(restarted.failure_count() == 2);
    const VerificationResult locked = restarted.verify(wrong, second_mac);
    CHECK(locked.code == VerificationCode::Locked);
    CHECK(locked.retry_after == 60s);
}

void test_lockout_backoff_doubles_and_caps() {
    TempDirectory directory;
    FakeClock clock;
    const std::string state = directory.file("backoff.state").string();
    WpsPinVerifier verifier("12345670", state, {}, clock.function());
    const PinDigits wrong = pin_from_halves(2234, 567);

    std::uint64_t expected_seconds = 60;
    for (unsigned lockout = 1; lockout <= 13; ++lockout) {
        CHECK(verifier.verify(wrong).code == VerificationCode::Failure);
        CHECK(verifier.verify(wrong).code == VerificationCode::Failure);
        const VerificationResult locked = verifier.verify(wrong);
        CHECK(locked.code == VerificationCode::Locked);
        CHECK(locked.retry_after == std::chrono::seconds(expected_seconds));
        CHECK(verifier.lockout_count() == lockout);
        CHECK(verifier.remaining_lockout() ==
              std::chrono::seconds(expected_seconds));
        clock.advance(std::chrono::seconds(expected_seconds));
        expected_seconds = std::min<std::uint64_t>(86400,
                                                   expected_seconds * 2);
    }

    CHECK(verifier.verify(wrong).code == VerificationCode::Failure);
    CHECK(verifier.verify(wrong).code == VerificationCode::Failure);
    CHECK(verifier.verify("12345670").code == VerificationCode::Success);
    CHECK(verifier.failure_count() == 0);
    CHECK(verifier.lockout_count() == 0);

    CHECK(verifier.verify(wrong).code == VerificationCode::Failure);
    CHECK(verifier.verify(wrong).code == VerificationCode::Failure);
    const VerificationResult reset_backoff = verifier.verify(wrong);
    CHECK(reset_backoff.code == VerificationCode::Locked);
    CHECK(reset_backoff.retry_after == 60s);
}

void test_success_clears_failure_count() {
    TempDirectory directory;
    FakeClock clock;
    WpsPinVerifier verifier("12345670", directory.file("success.state").string(),
                            {}, clock.function());
    const PinDigits wrong = pin_from_halves(2234, 567);

    CHECK(verifier.verify(wrong).code == VerificationCode::Failure);
    CHECK(verifier.verify(wrong).code == VerificationCode::Failure);
    CHECK(verifier.failure_count() == 2);
    CHECK(verifier.verify("12345670").code == VerificationCode::Success);
    CHECK(verifier.failure_count() == 0);

    CHECK(verifier.verify(wrong).code == VerificationCode::Failure);
    CHECK(verifier.verify(wrong).code == VerificationCode::Failure);
    CHECK(!verifier.is_locked());
    CHECK(verifier.failure_count() == 2);
}

template <typename Callback>
std::chrono::nanoseconds measure(std::size_t count, Callback&& callback) {
    const auto start = std::chrono::steady_clock::now();
    for (std::size_t index = 0; index < count; ++index) {
        callback(index);
    }
    return std::chrono::steady_clock::now() - start;
}

void test_all_first_half_values_timing() {
    TempDirectory directory;
    FakeClock clock;
    netsec::LockoutPolicy policy;
    policy.failure_threshold = 1000000;
    WpsPinVerifier verifier("12345670", directory.file("timing.state").string(),
                            policy, clock.function());

    for (unsigned first = 0; first < 1000; ++first) {
        const VerificationResult warmup = verifier.verify(pin_from_halves(first, 0));
        CHECK(warmup.code == VerificationCode::Failure);
    }

    std::size_t first_failures = 0;
    const auto first_elapsed = measure(10000, [&](std::size_t index) {
        const VerificationResult result =
            verifier.verify(pin_from_halves(static_cast<unsigned>(index), 568));
        if (result.code == VerificationCode::Failure) {
            ++first_failures;
        }
    });
    CHECK(first_failures == 10000);

    std::size_t second_failures = 0;
    const auto second_elapsed = measure(10000, [&](std::size_t index) {
        const VerificationResult result = verifier.verify(
            pin_from_halves(9999, static_cast<unsigned>(index % 1000)));
        if (result.code == VerificationCode::Failure) {
            ++second_failures;
        }
    });
    CHECK(second_failures == 10000);

    const auto first_per_call =
        first_elapsed / static_cast<std::int64_t>(10000);
    const auto second_per_call =
        second_elapsed / static_cast<std::int64_t>(10000);
    const auto slower = std::max(first_per_call, second_per_call);
    const auto faster = std::min(first_per_call, second_per_call);
    CHECK(slower <= faster * 2 + 5us);

    std::cout << "timing: first-half=" << first_per_call.count()
              << "ns/call, second-half=" << second_per_call.count()
              << "ns/call\n";
}

}  // namespace

int main() {
    try {
        test_checksum_and_parsing();
        test_checksum_rejected_before_protocol();
        test_half_failures_have_one_result();
        test_lockout_persistence_and_mac_change();
        test_failure_count_persists_across_mac_change();
        test_lockout_backoff_doubles_and_caps();
        test_success_clears_failure_count();
        test_all_first_half_values_timing();
    } catch (const std::exception& error) {
        std::cerr << "UNCAUGHT: " << error.what() << '\n';
        return 2;
    }

    if (failures != 0) {
        std::cerr << failures << " checks failed\n";
        return 1;
    }
    std::cout << "all checks passed\n";
    return 0;
}
