#pragma once

#include <array>
#include <chrono>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <memory>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

inline constexpr std::size_t kPinDigits = 8;
inline constexpr std::size_t kFirstHalfValues = 10000;
inline constexpr std::size_t kSecondHalfValues = 1000;
inline constexpr std::size_t kMacOctets = 6;
inline constexpr std::size_t kNonceSize = 16;
inline constexpr std::size_t kCommitmentSize = 32;

using Seconds = std::chrono::seconds;
using ClockFn = std::function<Seconds()>;

struct Pin {
    std::array<uint8_t, kPinDigits> digits{};

    Pin() = default;
    explicit Pin(std::array<uint8_t, kPinDigits> value) : digits(value) {}

    bool isValidFormat() const;
    int firstHalf() const;
    int secondHalf() const;
    uint8_t checkDigit() const;
    std::string toString() const;
};

bool parsePin(std::string_view text, Pin& out);
Pin pinWithCheckDigit(const std::array<uint8_t, 7>& firstSeven);
uint8_t computeCheckDigit(const Pin& pin);
bool hasValidCheckDigit(const Pin& pin);

struct MacAddress {
    std::array<uint8_t, kMacOctets> octets{};
};

bool parseMac(std::string_view text, MacAddress& out);

struct Nonce {
    std::array<uint8_t, kNonceSize> octets{};
};

struct Commitment {
    std::array<uint8_t, kCommitmentSize> bytes{};
};

bool constantTimeEqual(const uint8_t* a, const uint8_t* b, std::size_t length);
bool constantTimeEqual(const Commitment& a, const Commitment& b);

struct RegistrarRequest {
    MacAddress mac;
    Nonce nonce;
    Commitment firstCommitment;
    Commitment secondCommitment;
};

enum class VerifyResult {
    Success,
    Failed,
    InvalidCheckDigit,
    InvalidInput,
    Locked
};

struct LockoutPolicy {
    unsigned failureThreshold = 3;
    Seconds baseLockDuration{60};
    Seconds maxLockDuration{24 * 60 * 60};
};

class CommitmentScheme {
public:
    virtual ~CommitmentScheme() = default;
    virtual Commitment deriveFirst(const Pin& devicePin, const MacAddress& mac,
                                  const Nonce& nonce) = 0;
    virtual Commitment deriveSecond(const Pin& devicePin, const MacAddress& mac,
                                   const Nonce& nonce) = 0;
};

class HmacSha256CommitmentScheme final : public CommitmentScheme {
public:
    explicit HmacSha256CommitmentScheme(std::vector<uint8_t> key);
    HmacSha256CommitmentScheme(const uint8_t* key, std::size_t keyLength);

    Commitment deriveFirst(const Pin& devicePin, const MacAddress& mac,
                           const Nonce& nonce) override;
    Commitment deriveSecond(const Pin& devicePin, const MacAddress& mac,
                            const Nonce& nonce) override;

private:
    Commitment derive(int half, const Pin& devicePin, const MacAddress& mac,
                      const Nonce& nonce);

    std::vector<uint8_t> key_;
};

class PinAuthenticator {
public:
    PinAuthenticator(Pin devicePin, std::string stateFilePath,
                     LockoutPolicy policy = {},
                     std::shared_ptr<CommitmentScheme> scheme = nullptr,
                     ClockFn clock = nullptr);
    ~PinAuthenticator();

    PinAuthenticator(const PinAuthenticator&) = delete;
    PinAuthenticator& operator=(const PinAuthenticator&) = delete;

    VerifyResult verify(const RegistrarRequest& request, const Pin& submittedPin);

    bool isLocked() const;
    unsigned failureCount() const;
    unsigned lockTier() const;
    Seconds currentLockDuration() const;
    Seconds remainingLockTime() const;
    void resetState();

private:
    struct State;

    State loadState() const;
    void storeState(const State& state) const;
    Seconds now() const;
    bool stateIsLocked(const State& state) const;
    Seconds durationForTier(unsigned tier) const;

    Pin devicePin_;
    std::string stateFilePath_;
    LockoutPolicy policy_;
    std::shared_ptr<CommitmentScheme> scheme_;
    ClockFn clock_;
};

namespace detail {

std::array<uint8_t, 32> sha256(const uint8_t* data, std::size_t length);
std::array<uint8_t, 32> hmacSha256(const uint8_t* key, std::size_t keyLength,
                                   const uint8_t* message, std::size_t messageLength);
uint32_t crc32Ieee(const uint8_t* data, std::size_t length);

}  // namespace detail

}  // namespace netsec
