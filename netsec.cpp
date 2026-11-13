#include "netsec.hpp"

#include <fcntl.h>
#include <unistd.h>

#include <cstdio>
#include <cstring>
#include <fstream>
#include <stdexcept>
#include <algorithm>

namespace netsec {

namespace {

constexpr uint32_t kStateMagic = 0x4E50494Eu;  // "NPIN"
constexpr uint32_t kStateVersion = 1;
constexpr std::size_t kStateSize = 32;

void putLe32(std::array<uint8_t, kStateSize>& buf, std::size_t offset, uint32_t value) {
    buf[offset] = static_cast<uint8_t>(value);
    buf[offset + 1] = static_cast<uint8_t>(value >> 8);
    buf[offset + 2] = static_cast<uint8_t>(value >> 16);
    buf[offset + 3] = static_cast<uint8_t>(value >> 24);
}

uint32_t getLe32(const std::array<uint8_t, kStateSize>& buf, std::size_t offset) {
    return static_cast<uint32_t>(buf[offset]) |
           (static_cast<uint32_t>(buf[offset + 1]) << 8) |
           (static_cast<uint32_t>(buf[offset + 2]) << 16) |
           (static_cast<uint32_t>(buf[offset + 3]) << 24);
}

const std::array<uint8_t, 13>& defaultSchemeKey() {
    static constexpr std::array<uint8_t, 13> key = {
        'n', 'e', 't', 's', 'e', 'c', '-', 'w', 'p', 's', '-', 'v', '1'};
    return key;
}

Seconds wallClockSeconds() {
    return std::chrono::duration_cast<Seconds>(
        std::chrono::system_clock::now().time_since_epoch());
}

}  // namespace

bool Pin::isValidFormat() const {
    for (uint8_t digit : digits) {
        if (digit > 9) {
            return false;
        }
    }
    return true;
}

int Pin::firstHalf() const {
    return digits[0] * 1000 + digits[1] * 100 + digits[2] * 10 + digits[3];
}

int Pin::secondHalf() const {
    return digits[4] * 100 + digits[5] * 10 + digits[6];
}

uint8_t Pin::checkDigit() const {
    return digits[7];
}

std::string Pin::toString() const {
    std::string text;
    text.resize(kPinDigits);
    for (std::size_t i = 0; i < kPinDigits; ++i) {
        text[i] = static_cast<char>('0' + digits[i]);
    }
    return text;
}

bool parsePin(std::string_view text, Pin& out) {
    if (text.size() != kPinDigits) {
        return false;
    }
    std::array<uint8_t, kPinDigits> digits{};
    for (std::size_t i = 0; i < kPinDigits; ++i) {
        if (text[i] < '0' || text[i] > '9') {
            return false;
        }
        digits[i] = static_cast<uint8_t>(text[i] - '0');
    }
    out = Pin(digits);
    return true;
}

uint8_t computeCheckDigit(const Pin& pin) {
    int oddPositionSum = pin.digits[0] + pin.digits[2] + pin.digits[4] + pin.digits[6];
    int evenPositionSum = pin.digits[1] + pin.digits[3] + pin.digits[5];
    int weighted = (oddPositionSum * 3 + evenPositionSum) % 10;
    return static_cast<uint8_t>((10 - weighted) % 10);
}

bool hasValidCheckDigit(const Pin& pin) {
    return pin.isValidFormat() && computeCheckDigit(pin) == pin.checkDigit();
}

Pin pinWithCheckDigit(const std::array<uint8_t, 7>& firstSeven) {
    std::array<uint8_t, kPinDigits> digits{};
    for (std::size_t i = 0; i < 7; ++i) {
        digits[i] = firstSeven[i];
    }
    Pin partial(digits);
    digits[7] = computeCheckDigit(partial);
    return Pin(digits);
}

bool parseMac(std::string_view text, MacAddress& out) {
    if (text.size() != 17) {
        return false;
    }
    std::array<uint8_t, kMacOctets> octets{};
    for (std::size_t group = 0; group < kMacOctets; ++group) {
        std::size_t pos = group * 3;
        if (group != 0 && text[pos - 1] != ':' && text[pos - 1] != '-') {
            return false;
        }
        auto nibble = [](char c) -> int {
            if (c >= '0' && c <= '9') return c - '0';
            if (c >= 'a' && c <= 'f') return c - 'a' + 10;
            if (c >= 'A' && c <= 'F') return c - 'A' + 10;
            return -1;
        };
        int high = nibble(text[pos]);
        int low = nibble(text[pos + 1]);
        if (high < 0 || low < 0) {
            return false;
        }
        octets[group] = static_cast<uint8_t>((high << 4) | low);
    }
    out = MacAddress{octets};
    return true;
}

bool constantTimeEqual(const uint8_t* a, const uint8_t* b, std::size_t length) {
    uint8_t difference = 0;
    for (std::size_t i = 0; i < length; ++i) {
        difference |= static_cast<uint8_t>(a[i] ^ b[i]);
    }
    return difference == 0;
}

bool constantTimeEqual(const Commitment& a, const Commitment& b) {
    return constantTimeEqual(a.bytes.data(), b.bytes.data(), a.bytes.size());
}

namespace detail {

namespace {

uint32_t rotr(uint32_t value, std::size_t shift) {
    return (value >> shift) | (value << (32 - shift));
}

constexpr std::array<uint32_t, 64> kSha256K = {
    0x428a2f98u, 0x71374491u, 0xb5c0fbcfu, 0xe9b5dba5u, 0x3956c25bu, 0x59f111f1u,
    0x923f82a4u, 0xab1c5ed5u, 0xd807aa98u, 0x12835b01u, 0x243185beu, 0x550c7dc3u,
    0x72be5d74u, 0x80deb1feu, 0x9bdc06a7u, 0xc19bf174u, 0xe49b69c1u, 0xefbe4786u,
    0x0fc19dc6u, 0x240ca1ccu, 0x2de92c6fu, 0x4a7484aau, 0x5cb0a9dcu, 0x76f988dau,
    0x983e5152u, 0xa831c66du, 0xb00327c8u, 0xbf597fc7u, 0xc6e00bf3u, 0xd5a79147u,
    0x06ca6351u, 0x14292967u, 0x27b70a85u, 0x2e1b2138u, 0x4d2c6dfcu, 0x53380d13u,
    0x650a7354u, 0x766a0abbu, 0x81c2c92eu, 0x92722c85u, 0xa2bfe8a1u, 0xa81a664bu,
    0xc24b8b70u, 0xc76c51a3u, 0xd192e819u, 0xd6990624u, 0xf40e3585u, 0x106aa070u,
    0x19a4c116u, 0x1e376c08u, 0x2748774cu, 0x34b0bcb5u, 0x391c0cb3u, 0x4ed8aa4au,
    0x5b9cca4fu, 0x682e6ff3u, 0x748f82eeu, 0x78a5636fu, 0x84c87814u, 0x8cc70208u,
    0x90befffau, 0xa4506cebu, 0xbef9a3f7u, 0xc67178f2u};

void sha256Compress(std::array<uint32_t, 8>& hash, const uint8_t block[64]) {
    std::array<uint32_t, 64> words{};
    for (std::size_t i = 0; i < 16; ++i) {
        words[i] = (static_cast<uint32_t>(block[i * 4]) << 24) |
                   (static_cast<uint32_t>(block[i * 4 + 1]) << 16) |
                   (static_cast<uint32_t>(block[i * 4 + 2]) << 8) |
                   static_cast<uint32_t>(block[i * 4 + 3]);
    }
    for (std::size_t i = 16; i < 64; ++i) {
        uint32_t s0 = rotr(words[i - 15], 7) ^ rotr(words[i - 15], 18) ^
                      (words[i - 15] >> 3);
        uint32_t s1 = rotr(words[i - 2], 17) ^ rotr(words[i - 2], 19) ^
                      (words[i - 2] >> 10);
        words[i] = words[i - 16] + s0 + words[i - 7] + s1;
    }

    uint32_t a = hash[0];
    uint32_t b = hash[1];
    uint32_t c = hash[2];
    uint32_t d = hash[3];
    uint32_t e = hash[4];
    uint32_t f = hash[5];
    uint32_t g = hash[6];
    uint32_t h = hash[7];

    for (std::size_t i = 0; i < 64; ++i) {
        uint32_t s1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25);
        uint32_t ch = (e & f) ^ (~e & g);
        uint32_t temp1 = h + s1 + ch + kSha256K[i] + words[i];
        uint32_t s0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22);
        uint32_t maj = (a & b) ^ (a & c) ^ (b & c);
        uint32_t temp2 = s0 + maj;
        h = g;
        g = f;
        f = e;
        e = d + temp1;
        d = c;
        c = b;
        b = a;
        a = temp1 + temp2;
    }

    hash[0] += a;
    hash[1] += b;
    hash[2] += c;
    hash[3] += d;
    hash[4] += e;
    hash[5] += f;
    hash[6] += g;
    hash[7] += h;
}

}  // namespace

std::array<uint8_t, 32> sha256(const uint8_t* data, std::size_t length) {
    std::array<uint32_t, 8> hash = {0x6a09e667u, 0xbb67ae85u, 0x3c6ef372u,
                                    0xa54ff53au, 0x510e527fu, 0x9b05688cu,
                                    0x1f83d9abu, 0x5be0cd19u};

    std::size_t fullBlocks = length / 64;
    for (std::size_t block = 0; block < fullBlocks; ++block) {
        sha256Compress(hash, data + block * 64);
    }

    std::array<uint8_t, 128> tail{};
    std::size_t remainder = length % 64;
    for (std::size_t i = 0; i < remainder; ++i) {
        tail[i] = data[fullBlocks * 64 + i];
    }
    tail[remainder] = 0x80;

    std::size_t paddedLength = remainder + 1;
    if (paddedLength > 56) {
        paddedLength = 128;
    } else {
        paddedLength = 64;
    }
    uint64_t bitLength = static_cast<uint64_t>(length) * 8;
    for (std::size_t i = 0; i < 8; ++i) {
        tail[paddedLength - 1 - i] = static_cast<uint8_t>(bitLength >> (8 * i));
    }
    std::size_t tailBlocks = paddedLength / 64;
    for (std::size_t block = 0; block < tailBlocks; ++block) {
        sha256Compress(hash, tail.data() + block * 64);
    }

    std::array<uint8_t, 32> digest{};
    for (std::size_t i = 0; i < 8; ++i) {
        digest[i * 4] = static_cast<uint8_t>(hash[i] >> 24);
        digest[i * 4 + 1] = static_cast<uint8_t>(hash[i] >> 16);
        digest[i * 4 + 2] = static_cast<uint8_t>(hash[i] >> 8);
        digest[i * 4 + 3] = static_cast<uint8_t>(hash[i]);
    }
    return digest;
}

std::array<uint8_t, 32> hmacSha256(const uint8_t* key, std::size_t keyLength,
                                   const uint8_t* message,
                                   std::size_t messageLength) {
    std::array<uint8_t, 64> normalizedKey{};
    if (keyLength > 64) {
        auto hashed = sha256(key, keyLength);
        std::memcpy(normalizedKey.data(), hashed.data(), hashed.size());
    } else {
        std::memcpy(normalizedKey.data(), key, keyLength);
    }

    std::array<uint8_t, 64> innerPad{};
    std::array<uint8_t, 64> outerPad{};
    for (std::size_t i = 0; i < 64; ++i) {
        innerPad[i] = static_cast<uint8_t>(normalizedKey[i] ^ 0x36);
        outerPad[i] = static_cast<uint8_t>(normalizedKey[i] ^ 0x5c);
    }

    std::vector<uint8_t> innerInput;
    innerInput.reserve(64 + messageLength);
    innerInput.insert(innerInput.end(), innerPad.begin(), innerPad.end());
    innerInput.insert(innerInput.end(), message, message + messageLength);
    auto innerDigest = sha256(innerInput.data(), innerInput.size());

    std::array<uint8_t, 64 + 32> outerInput{};
    std::memcpy(outerInput.data(), outerPad.data(), 64);
    std::memcpy(outerInput.data() + 64, innerDigest.data(), innerDigest.size());
    return sha256(outerInput.data(), outerInput.size());
}

uint32_t crc32Ieee(const uint8_t* data, std::size_t length) {
    uint32_t crc = 0xFFFFFFFFu;
    for (std::size_t i = 0; i < length; ++i) {
        crc ^= data[i];
        for (int bit = 0; bit < 8; ++bit) {
            uint32_t mask = (crc & 1u) ? 0xFFFFFFFFu : 0u;
            crc = (crc >> 1) ^ (0xEDB88320u & mask);
        }
    }
    return crc ^ 0xFFFFFFFFu;
}

}  // namespace detail

HmacSha256CommitmentScheme::HmacSha256CommitmentScheme(std::vector<uint8_t> key)
    : key_(std::move(key)) {}

HmacSha256CommitmentScheme::HmacSha256CommitmentScheme(
    const uint8_t* key, std::size_t keyLength)
    : key_(key, key + keyLength) {}

Commitment HmacSha256CommitmentScheme::derive(int half, const Pin& devicePin,
                                              const MacAddress& mac,
                                              const Nonce& nonce) {
    std::array<uint8_t, 32> message{};
    std::memcpy(message.data(), mac.octets.data(), kMacOctets);
    std::memcpy(message.data() + 6, nonce.octets.data(), kNonceSize);
    message[22] = static_cast<uint8_t>(half);
    const int halfValue = half == 1 ? devicePin.firstHalf() : devicePin.secondHalf();
    message[23] = static_cast<uint8_t>((halfValue >> 24) & 0xFF);
    message[24] = static_cast<uint8_t>((halfValue >> 16) & 0xFF);
    message[25] = static_cast<uint8_t>((halfValue >> 8) & 0xFF);
    message[26] = static_cast<uint8_t>(halfValue & 0xFF);
    message[27] = static_cast<uint8_t>(devicePin.digits[7]);
    message[28] = static_cast<uint8_t>((halfValue / 1000) % 10);
    message[29] = static_cast<uint8_t>((halfValue / 100) % 10);
    message[30] = static_cast<uint8_t>((halfValue / 10) % 10);
    message[31] = static_cast<uint8_t>(halfValue % 10);

    auto tag = detail::hmacSha256(key_.data(), key_.size(), message.data(),
                                  message.size());
    Commitment commitment;
    commitment.bytes = tag;
    return commitment;
}

Commitment HmacSha256CommitmentScheme::deriveFirst(
    const Pin& devicePin, const MacAddress& mac, const Nonce& nonce) {
    return derive(1, devicePin, mac, nonce);
}

Commitment HmacSha256CommitmentScheme::deriveSecond(
    const Pin& devicePin, const MacAddress& mac, const Nonce& nonce) {
    return derive(2, devicePin, mac, nonce);
}

struct PinAuthenticator::State {
    unsigned failureCount = 0;
    unsigned lockTier = 0;
    int64_t lockStart = 0;
    int64_t lockDuration = 0;
};

PinAuthenticator::PinAuthenticator(Pin devicePin, std::string stateFilePath,
                                   LockoutPolicy policy,
                                   std::shared_ptr<CommitmentScheme> scheme,
                                   ClockFn clock)
    : devicePin_(devicePin),
      stateFilePath_(std::move(stateFilePath)),
      policy_(policy),
      scheme_(std::move(scheme)),
      clock_(std::move(clock)) {
    if (!devicePin_.isValidFormat() || !hasValidCheckDigit(devicePin_)) {
        throw std::invalid_argument("device PIN must be 8 digits with a valid check digit");
    }
    if (stateFilePath_.empty()) {
        throw std::invalid_argument("state file path must not be empty");
    }
    if (policy_.failureThreshold == 0) {
        policy_.failureThreshold = 3;
    }
    if (policy_.baseLockDuration <= Seconds(0)) {
        policy_.baseLockDuration = Seconds(60);
    }
    if (policy_.maxLockDuration < policy_.baseLockDuration) {
        policy_.maxLockDuration = policy_.baseLockDuration;
    }
    if (!scheme_) {
        const auto& key = defaultSchemeKey();
        scheme_ = std::make_shared<HmacSha256CommitmentScheme>(key.data(), key.size());
    }
    if (!clock_) {
        clock_ = wallClockSeconds;
    }
}

PinAuthenticator::~PinAuthenticator() = default;

Seconds PinAuthenticator::now() const {
    return clock_();
}

Seconds PinAuthenticator::durationForTier(unsigned tier) const {
    using Rep = Seconds::rep;
    Rep seconds = policy_.baseLockDuration.count();
    for (unsigned i = 1; i < tier; ++i) {
        if (seconds > policy_.maxLockDuration.count() / 2) {
            seconds = policy_.maxLockDuration.count();
            break;
        }
        seconds *= 2;
    }
    Rep capped = std::min<Rep>(seconds, policy_.maxLockDuration.count());
    return Seconds(capped);
}

bool PinAuthenticator::stateIsLocked(const State& state) const {
    if (state.lockDuration <= 0) {
        return false;
    }
    int64_t elapsed = now().count() - state.lockStart;
    return elapsed < state.lockDuration;
}

bool PinAuthenticator::isLocked() const {
    return stateIsLocked(loadState());
}

unsigned PinAuthenticator::failureCount() const {
    return loadState().failureCount;
}

unsigned PinAuthenticator::lockTier() const {
    return loadState().lockTier;
}

Seconds PinAuthenticator::currentLockDuration() const {
    State state = loadState();
    if (state.lockDuration <= 0) {
        return Seconds(0);
    }
    return Seconds(state.lockDuration);
}

Seconds PinAuthenticator::remainingLockTime() const {
    State state = loadState();
    if (!stateIsLocked(state)) {
        return Seconds(0);
    }
    int64_t remaining = state.lockDuration - (now().count() - state.lockStart);
    return Seconds(std::max<int64_t>(remaining, 1));
}

PinAuthenticator::State PinAuthenticator::loadState() const {
    State state;
    std::ifstream file(stateFilePath_, std::ios::binary);
    if (!file) {
        return state;
    }
    std::array<uint8_t, kStateSize> buffer{};
    file.read(reinterpret_cast<char*>(buffer.data()),
              static_cast<std::streamsize>(buffer.size()));
    if (!file || file.gcount() != static_cast<std::streamsize>(buffer.size())) {
        return State{};
    }
    char extra;
    if (file.get(extra)) {
        return State{};
    }
    if (getLe32(buffer, 0) != kStateMagic || getLe32(buffer, 4) != kStateVersion) {
        return State{};
    }
    uint32_t storedChecksum = getLe32(buffer, 28);
    if (detail::crc32Ieee(buffer.data(), 28) != storedChecksum) {
        return State{};
    }
    state.failureCount = getLe32(buffer, 8);
    state.lockTier = getLe32(buffer, 12);
    state.lockStart = static_cast<int64_t>(getLe32(buffer, 16));
    state.lockDuration = static_cast<int64_t>(getLe32(buffer, 20));
    return state;
}

void PinAuthenticator::storeState(const State& state) const {
    std::array<uint8_t, kStateSize> buffer{};
    putLe32(buffer, 0, kStateMagic);
    putLe32(buffer, 4, kStateVersion);
    putLe32(buffer, 8, state.failureCount);
    putLe32(buffer, 12, state.lockTier);
    putLe32(buffer, 16, static_cast<uint32_t>(state.lockStart));
    putLe32(buffer, 20, static_cast<uint32_t>(state.lockDuration));
    putLe32(buffer, 28, detail::crc32Ieee(buffer.data(), 28));

    std::string tempPath = stateFilePath_ + ".tmp";
    int fd = ::open(tempPath.c_str(), O_WRONLY | O_CREAT | O_TRUNC, 0600);
    if (fd < 0) {
        throw std::runtime_error("failed to open WPS state temporary file");
    }
    const char* data = reinterpret_cast<const char*>(buffer.data());
    std::size_t remaining = buffer.size();
    while (remaining > 0) {
        ssize_t written = ::write(fd, data, remaining);
        if (written <= 0) {
            ::close(fd);
            throw std::runtime_error("failed to write WPS state file");
        }
        data += written;
        remaining -= static_cast<std::size_t>(written);
    }
    if (::fsync(fd) != 0) {
        ::close(fd);
        throw std::runtime_error("failed to flush WPS state file");
    }
    if (::close(fd) != 0) {
        throw std::runtime_error("failed to close WPS state file");
    }
    if (std::rename(tempPath.c_str(), stateFilePath_.c_str()) != 0) {
        std::remove(tempPath.c_str());
        throw std::runtime_error("failed to commit WPS state file");
    }
}

void PinAuthenticator::resetState() {
    storeState(State{});
}

VerifyResult PinAuthenticator::verify(const RegistrarRequest& request,
                                      const Pin& submittedPin) {
    if (!submittedPin.isValidFormat()) {
        return VerifyResult::InvalidInput;
    }

    State state = loadState();
    if (stateIsLocked(state)) {
        return VerifyResult::Locked;
    }

    if (!hasValidCheckDigit(submittedPin)) {
        return VerifyResult::InvalidCheckDigit;
    }

    Commitment expectedFirst =
        scheme_->deriveFirst(devicePin_, request.mac, request.nonce);
    Commitment expectedSecond =
        scheme_->deriveSecond(devicePin_, request.mac, request.nonce);

    bool firstMatches =
        constantTimeEqual(expectedFirst, request.firstCommitment) &&
        submittedPin.firstHalf() == devicePin_.firstHalf();
    bool secondMatches =
        constantTimeEqual(expectedSecond, request.secondCommitment) &&
        submittedPin.secondHalf() == devicePin_.secondHalf();
    bool accepted = firstMatches & secondMatches;

    if (accepted) {
        State cleared = state;
        cleared.failureCount = 0;
        cleared.lockStart = 0;
        cleared.lockDuration = 0;
        storeState(cleared);
        return VerifyResult::Success;
    }

    state.failureCount += 1;
    if (state.failureCount >= policy_.failureThreshold) {
        state.lockTier += 1;
        state.lockStart = now().count();
        state.lockDuration = durationForTier(state.lockTier).count();
    }
    storeState(state);
    return VerifyResult::Failed;
}

}  // namespace netsec
