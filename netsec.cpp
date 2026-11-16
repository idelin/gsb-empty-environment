#include "netsec.hpp"

#include <algorithm>
#include <atomic>
#include <cerrno>
#include <filesystem>
#include <fstream>
#include <limits>
#include <stdexcept>
#include <system_error>

#include <fcntl.h>
#include <unistd.h>

namespace netsec {
namespace {

constexpr char kStateMagic[] = "NETSEC_WPS_LOCK_V1";

bool write_all(int fd, const char* data, std::size_t size) {
    while (size != 0) {
        const ssize_t written = ::write(fd, data, size);
        if (written < 0) {
            if (errno == EINTR) {
                continue;
            }
            return false;
        }
        data += written;
        size -= static_cast<std::size_t>(written);
    }
    return true;
}

PinDigits require_valid_pin(std::string_view text) {
    const std::optional<PinDigits> parsed = parse_pin(text);
    if (!parsed.has_value()) {
        throw std::invalid_argument("expected PIN must contain 8 digits");
    }
    return *parsed;
}

}  // namespace

std::uint8_t wps_checksum(const PinDigits& pin) noexcept {
    const unsigned odd_positions =
        static_cast<unsigned>(pin[0]) + pin[2] + pin[4] + pin[6];
    const unsigned even_positions =
        static_cast<unsigned>(pin[1]) + pin[3] + pin[5];
    const unsigned total = odd_positions * 3U + even_positions;
    return static_cast<std::uint8_t>((10U - total % 10U) % 10U);
}

bool wps_checksum_valid(const PinDigits& pin) noexcept {
    for (const std::uint8_t digit : pin) {
        if (digit > 9) {
            return false;
        }
    }
    return pin[7] == wps_checksum(pin);
}

std::optional<PinDigits> parse_pin(std::string_view text) noexcept {
    if (text.size() != kWpsPinDigits) {
        return std::nullopt;
    }
    PinDigits result{};
    for (std::size_t index = 0; index < result.size(); ++index) {
        const char character = text[index];
        if (character < '0' || character > '9') {
            return std::nullopt;
        }
        result[index] = static_cast<std::uint8_t>(character - '0');
    }
    return result;
}

std::string pin_to_string(const PinDigits& pin) {
    std::string result(kWpsPinDigits, '0');
    for (std::size_t index = 0; index < pin.size(); ++index) {
        if (pin[index] > 9) {
            throw std::invalid_argument("PIN digit is outside 0..9");
        }
        result[index] = static_cast<char>('0' + pin[index]);
    }
    return result;
}

WpsPinVerifier::WpsPinVerifier(const PinDigits& expected_pin,
                               std::string state_file,
                               LockoutPolicy policy,
                               Clock clock)
    : expected_pin_(expected_pin),
      state_file_(std::move(state_file)),
      policy_(policy),
      clock_(std::move(clock)) {
    validate_expected_pin();
    validate_configuration();
    if (state_file_.empty()) {
        throw std::invalid_argument("state file must not be empty");
    }
    if (!clock_) {
        clock_ = [] { return std::chrono::system_clock::now(); };
    }
    load_state();
}

WpsPinVerifier::WpsPinVerifier(std::string_view expected_pin,
                               std::string state_file,
                               LockoutPolicy policy,
                               Clock clock)
    : WpsPinVerifier(require_valid_pin(expected_pin), std::move(state_file),
                     policy, std::move(clock)) {}

VerificationResult WpsPinVerifier::verify(const PinDigits& submitted,
                                          const RegistrarMac& registrar_mac) {
    (void)registrar_mac;
    std::lock_guard<std::mutex> guard(mutex_);

    const std::int64_t now = now_epoch_seconds();
    VerificationResult lock_result = reject_if_locked(now);
    if (lock_result.locked()) {
        return lock_result;
    }
    return verify_unlocked(submitted);
}

VerificationResult WpsPinVerifier::verify(std::string_view submitted,
                                          const RegistrarMac& registrar_mac) {
    (void)registrar_mac;
    std::lock_guard<std::mutex> guard(mutex_);

    const std::int64_t now = now_epoch_seconds();
    VerificationResult lock_result = reject_if_locked(now);
    if (lock_result.locked()) {
        return lock_result;
    }

    const std::optional<PinDigits> parsed = parse_pin(submitted);
    if (!parsed.has_value()) {
        return {VerificationCode::InvalidPin, std::chrono::seconds{0}};
    }
    return verify_unlocked(*parsed);
}

VerificationResult WpsPinVerifier::verify(
    const PinCommitment& commitment,
    const RegistrarMac& registrar_mac) {
    (void)registrar_mac;
    std::lock_guard<std::mutex> guard(mutex_);

    const std::int64_t now = now_epoch_seconds();
    VerificationResult lock_result = reject_if_locked(now);
    if (lock_result.locked()) {
        return lock_result;
    }

    PinDigits submitted{};
    std::copy(commitment.first.begin(), commitment.first.end(),
              submitted.begin());
    std::copy(commitment.second.begin(), commitment.second.end(),
              submitted.begin() + kWpsFirstHalfDigits);
    submitted[7] = commitment.checksum;
    return verify_unlocked(submitted);
}

VerificationResult WpsPinVerifier::verify_unlocked(
    const PinDigits& submitted) {
    std::uint8_t non_digit = 0;
    for (const std::uint8_t digit : submitted) {
        non_digit |= static_cast<std::uint8_t>(digit > 9);
    }
    if (non_digit != 0) {
        return {VerificationCode::InvalidPin, std::chrono::seconds{0}};
    }

    if (submitted[7] != wps_checksum(submitted)) {
        return {VerificationCode::InvalidChecksum, std::chrono::seconds{0}};
    }

    std::uint8_t difference = 0;
    for (std::size_t index = 0; index < kWpsFirstHalfDigits; ++index) {
        difference |= static_cast<std::uint8_t>(
            submitted[index] ^ expected_pin_[index]);
    }
    for (std::size_t index = 0; index < kWpsSecondHalfDigits; ++index) {
        const std::size_t pin_index = kWpsFirstHalfDigits + index;
        difference |= static_cast<std::uint8_t>(
            submitted[pin_index] ^ expected_pin_[pin_index]);
    }
    std::atomic_signal_fence(std::memory_order_seq_cst);

    const std::int64_t now = now_epoch_seconds();
    if (difference == 0) {
        state_.failures = 0;
        state_.lockouts = 0;
        state_.lock_until_epoch_seconds = 0;
        save_state();
        return {VerificationCode::Success, std::chrono::seconds{0}};
    }
    return record_failure(now);
}

VerificationResult WpsPinVerifier::reject_if_locked(
    std::int64_t now_epoch_seconds) {
    if (state_.lock_until_epoch_seconds == 0) {
        return {VerificationCode::Failure, std::chrono::seconds{0}};
    }
    if (now_epoch_seconds < state_.lock_until_epoch_seconds) {
        return {VerificationCode::Locked,
                remaining_locked(now_epoch_seconds)};
    }
    state_.lock_until_epoch_seconds = 0;
    save_state();
    return {VerificationCode::Failure, std::chrono::seconds{0}};
}

VerificationResult WpsPinVerifier::record_failure(
    std::int64_t now_epoch_seconds) {
    ++state_.failures;
    if (state_.failures < policy_.failure_threshold) {
        save_state();
        return {VerificationCode::Failure, std::chrono::seconds{0}};
    }

    state_.failures = 0;
    if (state_.lockouts != std::numeric_limits<std::uint32_t>::max()) {
        ++state_.lockouts;
    }
    const std::chrono::seconds duration = stage_duration(state_.lockouts);
    const std::uint64_t until =
        static_cast<std::uint64_t>(std::max<std::int64_t>(
            0, now_epoch_seconds)) +
        static_cast<std::uint64_t>(duration.count());
    state_.lock_until_epoch_seconds = static_cast<std::int64_t>(
        std::min<std::uint64_t>(
            until, static_cast<std::uint64_t>(
                       std::numeric_limits<std::int64_t>::max())));
    save_state();
    return {VerificationCode::Locked, duration};
}

std::chrono::seconds WpsPinVerifier::stage_duration(
    std::uint32_t lockout_number) const {
    std::uint64_t duration =
        static_cast<std::uint64_t>(policy_.initial_lockout.count());
    const std::uint64_t maximum =
        static_cast<std::uint64_t>(policy_.maximum_lockout.count());

    for (std::uint32_t stage = 1;
         stage < lockout_number && duration < maximum; ++stage) {
        if (duration > maximum / 2U) {
            duration = maximum;
        } else {
            duration *= 2U;
        }
    }
    duration = std::min(duration, maximum);
    return std::chrono::seconds{static_cast<std::int64_t>(duration)};
}

std::chrono::seconds WpsPinVerifier::remaining_locked(
    std::int64_t now_epoch_seconds) const {
    if (state_.lock_until_epoch_seconds <= now_epoch_seconds) {
        return std::chrono::seconds{0};
    }
    return std::chrono::seconds{
        state_.lock_until_epoch_seconds - now_epoch_seconds};
}

std::int64_t WpsPinVerifier::now_epoch_seconds() const {
    const auto since_epoch = clock_().time_since_epoch();
    const auto seconds =
        std::chrono::duration_cast<std::chrono::seconds>(since_epoch);
    return seconds.count();
}

bool WpsPinVerifier::is_locked() const {
    std::lock_guard<std::mutex> guard(mutex_);
    return state_.lock_until_epoch_seconds != 0 &&
           now_epoch_seconds() < state_.lock_until_epoch_seconds;
}

std::chrono::seconds WpsPinVerifier::remaining_lockout() const {
    std::lock_guard<std::mutex> guard(mutex_);
    const std::int64_t now = now_epoch_seconds();
    if (state_.lock_until_epoch_seconds <= now) {
        return std::chrono::seconds{0};
    }
    return remaining_locked(now);
}

std::uint32_t WpsPinVerifier::failure_count() const {
    std::lock_guard<std::mutex> guard(mutex_);
    return state_.failures;
}

std::uint32_t WpsPinVerifier::lockout_count() const {
    std::lock_guard<std::mutex> guard(mutex_);
    return state_.lockouts;
}

const LockoutPolicy& WpsPinVerifier::policy() const noexcept {
    return policy_;
}

const std::string& WpsPinVerifier::state_file() const noexcept {
    return state_file_;
}

void WpsPinVerifier::load_state() {
    std::error_code exists_error;
    const bool exists = std::filesystem::exists(state_file_, exists_error);
    if (exists_error) {
        throw std::runtime_error("unable to inspect lock state: " + state_file_);
    }
    if (!exists) {
        return;
    }

    std::ifstream input(state_file_);
    if (!input) {
        throw std::runtime_error("unable to open lock state: " + state_file_);
    }

    std::string magic;
    std::uint64_t failures = 0;
    std::uint64_t lockouts = 0;
    std::int64_t lock_until = 0;
    input >> magic >> failures >> lockouts >> lock_until;
    if (!input || magic != kStateMagic ||
        failures > std::numeric_limits<std::uint32_t>::max() ||
        lockouts > std::numeric_limits<std::uint32_t>::max() ||
        lock_until < 0) {
        throw std::runtime_error("invalid lock state: " + state_file_);
    }

    std::string trailing;
    if (input >> trailing) {
        throw std::runtime_error("invalid lock state: " + state_file_);
    }

    state_.failures = static_cast<std::uint32_t>(failures);
    state_.lockouts = static_cast<std::uint32_t>(lockouts);
    state_.lock_until_epoch_seconds = lock_until;
}

void WpsPinVerifier::save_state() const {
    const std::filesystem::path target(state_file_);
    const std::filesystem::path parent = target.parent_path();
    if (!parent.empty()) {
        std::error_code directory_error;
        std::filesystem::create_directories(parent, directory_error);
        if (directory_error) {
            throw std::runtime_error("unable to create lock state directory: " +
                                     parent.string());
        }
    }

    const std::string temporary = state_file_ + ".tmp";
    const int fd = ::open(temporary.c_str(),
                          O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC, 0600);
    if (fd < 0) {
        throw std::runtime_error("unable to write lock state: " + temporary);
    }

    const std::string contents =
        std::string(kStateMagic) + "\n" +
        std::to_string(state_.failures) + "\n" +
        std::to_string(state_.lockouts) + "\n" +
        std::to_string(state_.lock_until_epoch_seconds) + "\n";
    const bool written = write_all(fd, contents.data(), contents.size());
    const bool synced = written && ::fsync(fd) == 0;
    const bool closed = ::close(fd) == 0;
    if (!written || !synced || !closed) {
        ::unlink(temporary.c_str());
        throw std::runtime_error("unable to persist lock state: " + temporary);
    }

    if (::rename(temporary.c_str(), state_file_.c_str()) != 0) {
        const int error = errno;
        ::unlink(temporary.c_str());
        throw std::system_error(error, std::generic_category(),
                                "unable to replace lock state");
    }
}

void WpsPinVerifier::validate_configuration() const {
    if (policy_.failure_threshold == 0) {
        throw std::invalid_argument("failure threshold must be positive");
    }
    if (policy_.initial_lockout <= std::chrono::seconds{0}) {
        throw std::invalid_argument("initial lockout must be positive");
    }
    if (policy_.maximum_lockout < policy_.initial_lockout) {
        throw std::invalid_argument(
            "maximum lockout must not be less than initial lockout");
    }
    if (policy_.maximum_lockout.count() >
        std::numeric_limits<std::int64_t>::max() / 2) {
        throw std::invalid_argument("maximum lockout is too large");
    }
}

void WpsPinVerifier::validate_expected_pin() const {
    for (const std::uint8_t digit : expected_pin_) {
        if (digit > 9) {
            throw std::invalid_argument("expected PIN digit is outside 0..9");
        }
    }
    if (expected_pin_[7] != wps_checksum(expected_pin_)) {
        throw std::invalid_argument("expected PIN has an invalid checksum");
    }
}

}  // namespace netsec
