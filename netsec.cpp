#include "netsec.hpp"

#include <algorithm>
#include <atomic>
#include <cstring>
#include <fstream>
#include <limits>
#include <random>
#include <utility>

#if defined(__APPLE__) || defined(__FreeBSD__) || defined(__OpenBSD__) || \
    defined(__NetBSD__)
#include <cstdlib>
#endif

namespace netsec {
namespace {

constexpr std::array<std::uint8_t, 4> kMagic{{'N', 'S', 'E', 'N'}};
constexpr std::uint16_t kVersion = 1;
constexpr std::uint8_t kFlagEntropy = 0x01;
constexpr std::size_t kSaltSize = 32;
constexpr std::size_t kNonceSize = 12;
constexpr std::size_t kTagSize = 32;
constexpr std::size_t kRevocationSize = 16;
constexpr std::size_t kFixedPrefixSize =
    kMagic.size() + 2 + 1 + 1 + 4 + kRevocationSize + kSaltSize + kNonceSize;

constexpr std::array<std::uint8_t, 10> kAuthDomain{
    {'N', 'S', 'E', 'N', '-', 'A', 'U', 'T', 'H', '1'}};
constexpr std::array<std::uint8_t, 12> kKdfDomain{
    {'n', 'e', 't', 's', 'e', 'c', '/', 'e', 'n', 'v', '/', '1'}};

[[noreturn]] void fail(ErrorCode code, std::string message) {
    throw ProtectionError(code, std::move(message));
}

void secure_wipe_bytes(void* data, std::size_t size) noexcept {
    auto* p = static_cast<volatile std::uint8_t*>(data);
    for (std::size_t i = 0; i < size; ++i) {
        p[i] = 0;
    }
    std::atomic_signal_fence(std::memory_order_seq_cst);
}

template <typename Container>
void secure_wipe(Container& value) noexcept {
    if (!value.empty()) {
        secure_wipe_bytes(value.data(), value.size() * sizeof(value[0]));
    }
}

template <typename Container>
class ScopedWipe {
public:
    explicit ScopedWipe(Container& value) noexcept : value_(value) {}
    ScopedWipe(const ScopedWipe&) = delete;
    ScopedWipe& operator=(const ScopedWipe&) = delete;
    ~ScopedWipe() { secure_wipe(value_); }

private:
    Container& value_;
};

bool constant_time_equal(std::span<const std::uint8_t> left,
                         std::span<const std::uint8_t> right) noexcept {
    if (left.size() != right.size()) {
        return false;
    }
    std::uint8_t difference = 0;
    for (std::size_t i = 0; i < left.size(); ++i) {
        difference |= static_cast<std::uint8_t>(left[i] ^ right[i]);
    }
    std::atomic_signal_fence(std::memory_order_seq_cst);
    return difference == 0;
}

inline std::uint32_t rotate_right(std::uint32_t value,
                                  unsigned amount) noexcept {
    return (value >> amount) | (value << (32U - amount));
}

inline std::uint32_t rotate_left(std::uint32_t value,
                                 unsigned amount) noexcept {
    return (value << amount) | (value >> (32U - amount));
}

inline std::uint32_t load_be32(const std::uint8_t* p) noexcept {
    return (static_cast<std::uint32_t>(p[0]) << 24) |
           (static_cast<std::uint32_t>(p[1]) << 16) |
           (static_cast<std::uint32_t>(p[2]) << 8) |
           static_cast<std::uint32_t>(p[3]);
}

inline std::uint32_t load_le32(const std::uint8_t* p) noexcept {
    return static_cast<std::uint32_t>(p[0]) |
           (static_cast<std::uint32_t>(p[1]) << 8) |
           (static_cast<std::uint32_t>(p[2]) << 16) |
           (static_cast<std::uint32_t>(p[3]) << 24);
}

inline void store_be32(std::uint8_t* p, std::uint32_t value) noexcept {
    p[0] = static_cast<std::uint8_t>(value >> 24);
    p[1] = static_cast<std::uint8_t>(value >> 16);
    p[2] = static_cast<std::uint8_t>(value >> 8);
    p[3] = static_cast<std::uint8_t>(value);
}

inline void store_le32(std::uint8_t* p, std::uint32_t value) noexcept {
    p[0] = static_cast<std::uint8_t>(value);
    p[1] = static_cast<std::uint8_t>(value >> 8);
    p[2] = static_cast<std::uint8_t>(value >> 16);
    p[3] = static_cast<std::uint8_t>(value >> 24);
}

class Sha256 {
public:
    Sha256() = default;
    ~Sha256() {
        secure_wipe(buffer_);
        secure_wipe(state_);
    }

    void update(const std::uint8_t* data, std::size_t size) noexcept {
        total_bytes_ += size;
        append(data, size);
    }

    void update(std::span<const std::uint8_t> data) noexcept {
        update(data.data(), data.size());
    }

    std::array<std::uint8_t, 32> finish() noexcept {
        const std::uint64_t bit_length = total_bytes_ * 8U;
        std::array<std::uint8_t, 64> padding{};
        padding[0] = 0x80;
        const std::size_t padding_size =
            (buffer_size_ < 56) ? (56 - buffer_size_)
                                : (120 - buffer_size_);
        append(padding.data(), padding_size);

        std::array<std::uint8_t, 8> length{};
        for (int i = 7; i >= 0; --i) {
            length[static_cast<std::size_t>(i)] =
                static_cast<std::uint8_t>(bit_length >> ((7 - i) * 8));
        }
        append(length.data(), length.size());

        std::array<std::uint8_t, 32> digest{};
        for (std::size_t i = 0; i < state_.size(); ++i) {
            store_be32(digest.data() + i * 4, state_[i]);
        }
        secure_wipe(padding);
        secure_wipe(length);
        return digest;
    }

private:
    static constexpr std::array<std::uint32_t, 64> kRoundConstants{{
        0x428a2f98U, 0x71374491U, 0xb5c0fbcfU, 0xe9b5dba5U, 0x3956c25bU,
        0x59f111f1U, 0x923f82a4U, 0xab1c5ed5U, 0xd807aa98U, 0x12835b01U,
        0x243185beU, 0x550c7dc3U, 0x72be5d74U, 0x80deb1feU, 0x9bdc06a7U,
        0xc19bf174U, 0xe49b69c1U, 0xefbe4786U, 0x0fc19dc6U, 0x240ca1ccU,
        0x2de92c6fU, 0x4a7484aaU, 0x5cb0a9dcU, 0x76f988daU, 0x983e5152U,
        0xa831c66dU, 0xb00327c8U, 0xbf597fc7U, 0xc6e00bf3U, 0xd5a79147U,
        0x06ca6351U, 0x14292967U, 0x27b70a85U, 0x2e1b2138U, 0x4d2c6dfcU,
        0x53380d13U, 0x650a7354U, 0x766a0abbU, 0x81c2c92eU, 0x92722c85U,
        0xa2bfe8a1U, 0xa81a664bU, 0xc24b8b70U, 0xc76c51a3U, 0xd192e819U,
        0xd6990624U, 0xf40e3585U, 0x106aa070U, 0x19a4c116U, 0x1e376c08U,
        0x2748774cU, 0x34b0bcb5U, 0x391c0cb3U, 0x4ed8aa4aU, 0x5b9cca4fU,
        0x682e6ff3U, 0x748f82eeU, 0x78a5636fU, 0x84c87814U, 0x8cc70208U,
        0x90befffaU, 0xa4506cebU, 0xbef9a3f7U, 0xc67178f2U,
    }};

    void append(const std::uint8_t* data, std::size_t size) noexcept {
        while (size > 0) {
            const std::size_t available = 64 - buffer_size_;
            const std::size_t take = std::min(available, size);
            std::memcpy(buffer_.data() + buffer_size_, data, take);
            buffer_size_ += take;
            data += take;
            size -= take;
            if (buffer_size_ == 64) {
                process_block(buffer_.data());
                buffer_size_ = 0;
            }
        }
    }

    void process_block(const std::uint8_t* block) noexcept {
        std::array<std::uint32_t, 64> words{};
        for (std::size_t i = 0; i < 16; ++i) {
            words[i] = load_be32(block + i * 4);
        }
        for (std::size_t i = 16; i < 64; ++i) {
            const std::uint32_t s0 =
                rotate_right(words[i - 15], 7) ^
                rotate_right(words[i - 15], 18) ^ (words[i - 15] >> 3);
            const std::uint32_t s1 =
                rotate_right(words[i - 2], 17) ^
                rotate_right(words[i - 2], 19) ^ (words[i - 2] >> 10);
            words[i] = words[i - 16] + s0 + words[i - 7] + s1;
        }

        std::uint32_t a = state_[0];
        std::uint32_t b = state_[1];
        std::uint32_t c = state_[2];
        std::uint32_t d = state_[3];
        std::uint32_t e = state_[4];
        std::uint32_t f = state_[5];
        std::uint32_t g = state_[6];
        std::uint32_t h = state_[7];

        for (std::size_t i = 0; i < 64; ++i) {
            const std::uint32_t sum1 =
                rotate_right(e, 6) ^ rotate_right(e, 11) ^ rotate_right(e, 25);
            const std::uint32_t choose = (e & f) ^ (~e & g);
            const std::uint32_t temp1 =
                h + sum1 + choose + kRoundConstants[i] + words[i];
            const std::uint32_t sum0 =
                rotate_right(a, 2) ^ rotate_right(a, 13) ^ rotate_right(a, 22);
            const std::uint32_t majority = (a & b) ^ (a & c) ^ (b & c);
            const std::uint32_t temp2 = sum0 + majority;
            h = g;
            g = f;
            f = e;
            e = d + temp1;
            d = c;
            c = b;
            b = a;
            a = temp1 + temp2;
        }

        state_[0] += a;
        state_[1] += b;
        state_[2] += c;
        state_[3] += d;
        state_[4] += e;
        state_[5] += f;
        state_[6] += g;
        state_[7] += h;
    }

    std::array<std::uint32_t, 8> state_{
        {0x6a09e667U, 0xbb67ae85U, 0x3c6ef372U, 0xa54ff53aU,
         0x510e527fU, 0x9b05688cU, 0x1f83d9abU, 0x5be0cd19U}};
    std::array<std::uint8_t, 64> buffer_{};
    std::size_t buffer_size_ = 0;
    std::uint64_t total_bytes_ = 0;
};

std::array<std::uint8_t, 32> sha256(
    std::span<const std::uint8_t> input) noexcept {
    Sha256 hash;
    hash.update(input);
    return hash.finish();
}

class HmacSha256 {
public:
    explicit HmacSha256(std::span<const std::uint8_t> key) noexcept {
        std::array<std::uint8_t, 64> block{};
        if (key.size() > block.size()) {
            const auto digest = sha256(key);
            std::memcpy(block.data(), digest.data(), digest.size());
        } else {
            std::memcpy(block.data(), key.data(), key.size());
        }

        std::array<std::uint8_t, 64> inner_pad{};
        std::array<std::uint8_t, 64> outer_pad{};
        for (std::size_t i = 0; i < block.size(); ++i) {
            inner_pad[i] = static_cast<std::uint8_t>(block[i] ^ 0x36U);
            outer_pad[i] = static_cast<std::uint8_t>(block[i] ^ 0x5cU);
        }
        inner_.update(inner_pad);
        outer_.update(outer_pad);
        secure_wipe(block);
        secure_wipe(inner_pad);
        secure_wipe(outer_pad);
    }

    void update(std::span<const std::uint8_t> data) noexcept {
        inner_.update(data);
    }

    void update(const std::uint8_t* data, std::size_t size) noexcept {
        inner_.update(data, size);
    }

    std::array<std::uint8_t, 32> finish() noexcept {
        auto inner_digest = inner_.finish();
        outer_.update(inner_digest);
        auto result = outer_.finish();
        secure_wipe(inner_digest);
        return result;
    }

private:
    Sha256 inner_;
    Sha256 outer_;
};

std::array<std::uint8_t, 32> hmac_sha256(
    std::span<const std::uint8_t> key,
    std::span<const std::uint8_t> data) noexcept {
    HmacSha256 mac(key);
    mac.update(data);
    return mac.finish();
}

void chacha20_block(const Key& key, std::uint32_t counter,
                    const std::array<std::uint8_t, kNonceSize>& nonce,
                    std::array<std::uint8_t, 64>& output) noexcept {
    std::array<std::uint32_t, 16> state{};
    state[0] = 0x61707865U;
    state[1] = 0x3320646eU;
    state[2] = 0x79622d32U;
    state[3] = 0x6b206574U;
    for (std::size_t i = 0; i < 8; ++i) {
        state[4 + i] = load_le32(key.data() + i * 4);
    }
    state[12] = counter;
    for (std::size_t i = 0; i < 3; ++i) {
        state[13 + i] = load_le32(nonce.data() + i * 4);
    }

    auto working = state;
    auto quarter = [](std::uint32_t& a, std::uint32_t& b, std::uint32_t& c,
                      std::uint32_t& d) {
        a += b;
        d ^= a;
        d = rotate_left(d, 16);
        c += d;
        b ^= c;
        b = rotate_left(b, 12);
        a += b;
        d ^= a;
        d = rotate_left(d, 8);
        c += d;
        b ^= c;
        b = rotate_left(b, 7);
    };

    for (int round = 0; round < 10; ++round) {
        quarter(working[0], working[4], working[8], working[12]);
        quarter(working[1], working[5], working[9], working[13]);
        quarter(working[2], working[6], working[10], working[14]);
        quarter(working[3], working[7], working[11], working[15]);
        quarter(working[0], working[5], working[10], working[15]);
        quarter(working[1], working[6], working[11], working[12]);
        quarter(working[2], working[7], working[8], working[13]);
        quarter(working[3], working[4], working[9], working[14]);
    }

    for (std::size_t i = 0; i < 16; ++i) {
        store_le32(output.data() + i * 4, working[i] + state[i]);
    }
    secure_wipe(working);
    secure_wipe(state);
}

std::vector<std::uint8_t> chacha20_xor(
    const Key& key, const std::array<std::uint8_t, kNonceSize>& nonce,
    std::span<const std::uint8_t> input) {
    std::vector<std::uint8_t> output(input.size());
    std::array<std::uint8_t, 64> block{};
    std::uint32_t counter = 0;
    std::size_t offset = 0;
    while (offset < input.size()) {
        chacha20_block(key, counter, nonce, block);
        const std::size_t take = std::min(block.size(), input.size() - offset);
        for (std::size_t i = 0; i < take; ++i) {
            output[offset + i] =
                static_cast<std::uint8_t>(input[offset + i] ^ block[i]);
        }
        offset += take;
        if (counter == std::numeric_limits<std::uint32_t>::max() &&
            offset < input.size()) {
            secure_wipe(block);
            fail(ErrorCode::ProtectionFailure, "ciphertext is too large");
        }
        ++counter;
    }
    secure_wipe(block);
    return output;
}

void fill_random(std::span<std::uint8_t> output) {
    if (output.empty()) {
        return;
    }
#if defined(__APPLE__) || defined(__FreeBSD__) || defined(__OpenBSD__) || \
    defined(__NetBSD__)
    arc4random_buf(output.data(), output.size());
    return;
#else
    {
        std::ifstream source("/dev/urandom", std::ios::binary);
        if (source) {
            std::size_t offset = 0;
            while (offset < output.size()) {
                source.read(
                    reinterpret_cast<char*>(output.data() + offset),
                    static_cast<std::streamsize>(output.size() - offset));
                const std::streamsize got = source.gcount();
                if (got <= 0) {
                    break;
                }
                offset += static_cast<std::size_t>(got);
            }
            if (offset == output.size()) {
                return;
            }
        }
    }
    try {
        std::random_device random;
        for (auto& byte : output) {
            byte = static_cast<std::uint8_t>(random());
        }
    } catch (...) {
        fail(ErrorCode::ProtectionFailure,
             "no secure random source is available");
    }
#endif
}

void append_u16(std::vector<std::uint8_t>& output, std::uint16_t value) {
    output.push_back(static_cast<std::uint8_t>(value >> 8));
    output.push_back(static_cast<std::uint8_t>(value));
}

void append_u32(std::vector<std::uint8_t>& output, std::uint32_t value) {
    const std::size_t offset = output.size();
    output.resize(offset + 4);
    store_be32(output.data() + offset, value);
}

class Reader {
public:
    explicit Reader(std::span<const std::uint8_t> data) : data_(data) {}

    std::size_t position() const noexcept { return position_; }
    std::size_t remaining() const noexcept { return data_.size() - position_; }

    std::uint8_t u8() {
        require(1);
        return data_[position_++];
    }

    std::uint16_t u16() {
        require(2);
        const std::uint16_t value = static_cast<std::uint16_t>(
            (static_cast<std::uint16_t>(data_[position_]) << 8) |
            static_cast<std::uint16_t>(data_[position_ + 1]));
        position_ += 2;
        return value;
    }

    std::uint32_t u32() {
        require(4);
        const std::uint32_t value = load_be32(data_.data() + position_);
        position_ += 4;
        return value;
    }

    std::span<const std::uint8_t> bytes(std::size_t size) {
        require(size);
        auto result = data_.subspan(position_, size);
        position_ += size;
        return result;
    }

private:
    void require(std::size_t size) const {
        if (size > remaining()) {
            fail(ErrorCode::InvalidEnvelope, "truncated envelope");
        }
    }

    std::span<const std::uint8_t> data_;
    std::size_t position_ = 0;
};

bool all_zero(std::span<const std::uint8_t> data) noexcept {
    std::uint8_t value = 0;
    for (std::uint8_t byte : data) {
        value |= byte;
    }
    return value == 0;
}

}  // namespace

std::string_view to_string(ErrorCode code) noexcept {
    switch (code) {
        case ErrorCode::InvalidArgument:
            return "invalid argument";
        case ErrorCode::InvalidEnvelope:
            return "invalid envelope";
        case ErrorCode::UnsupportedVersion:
            return "unsupported envelope version";
        case ErrorCode::UnknownKeyGeneration:
            return "unknown key generation";
        case ErrorCode::RevokedKeyGeneration:
            return "revoked key generation";
        case ErrorCode::RevokedEnvelope:
            return "revoked envelope";
        case ErrorCode::IdentityMismatch:
            return "caller SID does not own this envelope";
        case ErrorCode::SessionMismatch:
            return "caller logon session does not own this envelope";
        case ErrorCode::ScopeMismatch:
            return "caller protection scope does not match envelope";
        case ErrorCode::MachineScopeNotAllowed:
            return "machine scope requires a service account and explicit declaration";
        case ErrorCode::EntropyMismatch:
            return "optional entropy does not match";
        case ErrorCode::AuthenticationFailed:
            return "envelope authentication failed";
        case ErrorCode::NoActiveKeyGeneration:
            return "no active key generation";
        case ErrorCode::ProtectionFailure:
            return "protection operation failed";
    }
    return "unknown error";
}

ProtectionError::ProtectionError(ErrorCode code, std::string message)
    : std::runtime_error(std::move(message)), code_(code) {}

ProtectionContext::ProtectionContext() {
    Key key{};
    ScopedWipe<Key> key_guard(key);
    fill_random(key);
    keys_.emplace(1, key);
    active_generation_ = 1;
    highest_generation_ = 1;
}

ProtectionContext::ProtectionContext(Key master_key, std::uint32_t generation,
                                     bool make_active) {
    ScopedWipe<Key> key_guard(master_key);
    if (generation == 0) {
        fail(ErrorCode::InvalidArgument, "key generation must be non-zero");
    }
    keys_.emplace(generation, master_key);
    highest_generation_ = generation;
    if (make_active) {
        active_generation_ = generation;
    }
}

ProtectionContext::~ProtectionContext() {
    for (auto& entry : keys_) {
        secure_wipe(entry.second);
    }
}

std::uint32_t ProtectionContext::active_generation() const {
    std::lock_guard<std::mutex> lock(mutex_);
    return active_generation_;
}

std::uint32_t ProtectionContext::import_generation(std::uint32_t generation,
                                                   Key master_key,
                                                   bool make_active) {
    ScopedWipe<Key> key_guard(master_key);
    if (generation == 0) {
        fail(ErrorCode::InvalidArgument, "key generation must be non-zero");
    }
    std::lock_guard<std::mutex> lock(mutex_);
    if (keys_.find(generation) != keys_.end()) {
        fail(ErrorCode::InvalidArgument, "key generation already exists");
    }
    keys_.emplace(generation, master_key);
    highest_generation_ = std::max(highest_generation_, generation);
    if (make_active) {
        active_generation_ = generation;
    }
    return generation;
}

std::uint32_t ProtectionContext::next_generation_locked() const {
    if (highest_generation_ == std::numeric_limits<std::uint32_t>::max()) {
        fail(ErrorCode::ProtectionFailure,
             "key generation space is exhausted");
    }
    return highest_generation_ + 1;
}

std::uint32_t ProtectionContext::rotate() {
    Key next{};
    fill_random(next);
    return rotate(next);
}

std::uint32_t ProtectionContext::rotate(Key next_master_key) {
    ScopedWipe<Key> key_guard(next_master_key);
    std::lock_guard<std::mutex> lock(mutex_);
    const std::uint32_t generation = next_generation_locked();
    keys_.emplace(generation, next_master_key);
    highest_generation_ = generation;
    active_generation_ = generation;
    return generation;
}

void ProtectionContext::revoke_generation(std::uint32_t generation) {
    if (generation == 0) {
        fail(ErrorCode::InvalidArgument, "key generation must be non-zero");
    }
    std::lock_guard<std::mutex> lock(mutex_);
    revoked_generations_[generation] = true;
    if (active_generation_ == generation) {
        active_generation_ = 0;
    }
}

bool ProtectionContext::is_generation_revoked(
    std::uint32_t generation) const {
    std::lock_guard<std::mutex> lock(mutex_);
    const auto it = revoked_generations_.find(generation);
    return it != revoked_generations_.end() && it->second;
}

void ProtectionContext::revoke_envelope(const RevocationId& id) {
    std::lock_guard<std::mutex> lock(mutex_);
    revoked_envelopes_[id] = true;
}

bool ProtectionContext::is_envelope_revoked(const RevocationId& id) const {
    std::lock_guard<std::mutex> lock(mutex_);
    const auto it = revoked_envelopes_.find(id);
    return it != revoked_envelopes_.end() && it->second;
}

Key ProtectionContext::active_key_locked() const {
    if (active_generation_ == 0) {
        fail(ErrorCode::NoActiveKeyGeneration,
             "the protection context has no active key generation");
    }
    const auto it = keys_.find(active_generation_);
    if (it == keys_.end()) {
        fail(ErrorCode::UnknownKeyGeneration,
             "the active key generation is not present");
    }
    const auto revoked = revoked_generations_.find(active_generation_);
    if (revoked != revoked_generations_.end() && revoked->second) {
        fail(ErrorCode::RevokedKeyGeneration,
             "the active key generation has been revoked");
    }
    return it->second;
}

Key ProtectionContext::generation_key_locked(std::uint32_t generation) const {
    const auto it = keys_.find(generation);
    if (it == keys_.end()) {
        fail(ErrorCode::UnknownKeyGeneration,
             "the requested key generation is not present");
    }
    const auto revoked = revoked_generations_.find(generation);
    if (revoked != revoked_generations_.end() && revoked->second) {
        fail(ErrorCode::RevokedKeyGeneration,
             "the requested key generation has been revoked");
    }
    return it->second;
}

void ProtectionContext::check_scope_policy(const CallerContext& caller,
                                           ProtectionScope scope) {
    if (scope == ProtectionScope::Machine &&
        (!caller.is_service_account ||
         !caller.machine_scope_explicitly_declared)) {
        fail(ErrorCode::MachineScopeNotAllowed,
             "machine scope requires a service account and an explicit "
             "machine-scope declaration");
    }
}

void ProtectionContext::authorize_caller(const CallerContext& caller,
                                         const Subject& owner,
                                         ProtectionScope scope) {
    if (caller.subject.sid != owner.sid) {
        fail(ErrorCode::IdentityMismatch,
             "the caller SID does not match the envelope owner");
    }
    if (caller.subject.logon_session != owner.logon_session) {
        fail(ErrorCode::SessionMismatch,
             "the caller logon session does not match the envelope owner");
    }
    if (caller.requested_scope != scope) {
        fail(ErrorCode::ScopeMismatch,
             "the caller protection scope does not match the envelope");
    }
    check_scope_policy(caller, scope);
}

void ProtectionContext::authorize_writer(const CallerContext& caller,
                                         const Subject& owner,
                                         ProtectionScope scope) {
    if (caller.requested_scope != scope) {
        fail(ErrorCode::ScopeMismatch,
             "the caller protection scope does not match the requested scope");
    }
    check_scope_policy(caller, scope);
    if (caller.subject == owner) {
        return;
    }
    // A verified service account may seal material on behalf of a user. It
    // still cannot open that material: unprotect() always requires the
    // envelope owner's SID and logon session.
    if (!caller.is_service_account) {
        if (caller.subject.sid != owner.sid) {
            fail(ErrorCode::IdentityMismatch,
                 "the caller SID does not match the requested owner");
        }
        fail(ErrorCode::SessionMismatch,
             "the caller logon session does not match the requested owner");
    }
}

ProtectionContext::ParsedEnvelope ProtectionContext::parse(
    std::span<const std::uint8_t> envelope) {
    if (envelope.size() < kFixedPrefixSize + 4 + 2 + 32 + 4 + kTagSize) {
        fail(ErrorCode::InvalidEnvelope, "envelope is too short");
    }
    if (!std::equal(kMagic.begin(), kMagic.end(), envelope.begin())) {
        fail(ErrorCode::InvalidEnvelope, "invalid envelope magic");
    }

    Reader reader(envelope);
    reader.bytes(kMagic.size());
    const std::uint16_t version = reader.u16();
    if (version != kVersion) {
        fail(ErrorCode::UnsupportedVersion,
             "envelope version is not supported");
    }

    ParsedEnvelope parsed;
    parsed.info.version = version;
    const std::uint8_t scope = reader.u8();
    if (scope != static_cast<std::uint8_t>(ProtectionScope::User) &&
        scope != static_cast<std::uint8_t>(ProtectionScope::Machine)) {
        fail(ErrorCode::InvalidEnvelope, "invalid protection scope");
    }
    parsed.info.scope = static_cast<ProtectionScope>(scope);

    parsed.flags = reader.u8();
    if ((parsed.flags & ~kFlagEntropy) != 0) {
        fail(ErrorCode::InvalidEnvelope, "unknown envelope flags");
    }
    parsed.info.has_entropy = (parsed.flags & kFlagEntropy) != 0;

    parsed.info.key_generation = reader.u32();
    if (parsed.info.key_generation == 0) {
        fail(ErrorCode::InvalidEnvelope, "invalid key generation");
    }

    const auto revocation = reader.bytes(kRevocationSize);
    std::copy(revocation.begin(), revocation.end(),
              parsed.info.revocation_id.begin());

    const auto salt = reader.bytes(kSaltSize);
    std::copy(salt.begin(), salt.end(), parsed.salt.begin());
    const auto nonce = reader.bytes(kNonceSize);
    std::copy(nonce.begin(), nonce.end(), parsed.nonce.begin());

    const std::uint16_t sid_size = reader.u16();
    if (sid_size == 0) {
        fail(ErrorCode::InvalidEnvelope, "envelope has an empty SID");
    }
    const auto sid = reader.bytes(sid_size);
    parsed.info.subject.sid.assign(
        reinterpret_cast<const char*>(sid.data()), sid.size());

    const std::uint16_t session_size = reader.u16();
    if (session_size == 0) {
        fail(ErrorCode::InvalidEnvelope,
             "envelope has an empty logon session");
    }
    const auto session = reader.bytes(session_size);
    parsed.info.subject.logon_session.assign(
        reinterpret_cast<const char*>(session.data()), session.size());

    const auto entropy_digest = reader.bytes(32);
    std::copy(entropy_digest.begin(), entropy_digest.end(),
              parsed.entropy_digest.begin());
    if (!parsed.info.has_entropy && !all_zero(parsed.entropy_digest)) {
        fail(ErrorCode::InvalidEnvelope,
             "entropy digest present without entropy flag");
    }

    const std::uint32_t ciphertext_size = reader.u32();
    const std::size_t header_size = reader.position();
    if (reader.remaining() !=
        static_cast<std::size_t>(ciphertext_size) + kTagSize) {
        fail(ErrorCode::InvalidEnvelope, "invalid envelope length");
    }

    parsed.header.assign(envelope.begin(),
                         envelope.begin() +
                             static_cast<std::ptrdiff_t>(header_size));
    const auto ciphertext = reader.bytes(ciphertext_size);
    parsed.ciphertext.assign(ciphertext.begin(), ciphertext.end());
    const auto tag = reader.bytes(kTagSize);
    std::copy(tag.begin(), tag.end(), parsed.tag.begin());
    parsed.info.ciphertext_size = ciphertext_size;
    return parsed;
}

ProtectionContext::DerivedKeys ProtectionContext::derive_key(
    const Key& master_key, const ParsedEnvelope& parsed) {
    // HKDF-Extract: PRK = HMAC-SHA256(salt, master_key).
    auto prk = hmac_sha256(parsed.salt, master_key);

    std::vector<std::uint8_t> info;
    info.reserve(kKdfDomain.size() + parsed.header.size());
    info.insert(info.end(), kKdfDomain.begin(), kKdfDomain.end());
    info.insert(info.end(), parsed.header.begin(), parsed.header.end());

    std::vector<std::uint8_t> first_input = info;
    first_input.push_back(0x01);
    auto encryption = hmac_sha256(prk, first_input);

    std::vector<std::uint8_t> second_input;
    second_input.reserve(encryption.size() + info.size() + 1);
    second_input.insert(second_input.end(), encryption.begin(),
                        encryption.end());
    second_input.insert(second_input.end(), info.begin(), info.end());
    second_input.push_back(0x02);
    auto authentication = hmac_sha256(prk, second_input);

    DerivedKeys result{encryption, authentication};
    secure_wipe(prk);
    secure_wipe(encryption);
    secure_wipe(authentication);
    secure_wipe(info);
    secure_wipe(first_input);
    secure_wipe(second_input);
    return result;
}

std::vector<std::uint8_t> ProtectionContext::protect(
    const CallerContext& caller, const Subject& owner, ProtectionScope scope,
    std::span<const std::uint8_t> plaintext,
    Entropy optional_entropy,
    const ProtectOptions& options) {
    authorize_writer(caller, owner, scope);

    if (owner.sid.empty() || owner.logon_session.empty()) {
        fail(ErrorCode::InvalidArgument,
             "the envelope owner must have a SID and logon session");
    }
    if (owner.sid.size() > std::numeric_limits<std::uint16_t>::max() ||
        owner.logon_session.size() >
            std::numeric_limits<std::uint16_t>::max()) {
        fail(ErrorCode::InvalidArgument, "SID or logon session is too long");
    }
    if (plaintext.size() > std::numeric_limits<std::uint32_t>::max()) {
        fail(ErrorCode::InvalidArgument, "plaintext is too large");
    }

    ParsedEnvelope parsed;
    parsed.info.version = kVersion;
    parsed.info.scope = scope;
    parsed.info.subject = owner;
    parsed.info.has_entropy = !optional_entropy.empty();
    parsed.info.ciphertext_size = plaintext.size();
    parsed.flags =
        parsed.info.has_entropy ? static_cast<std::uint8_t>(kFlagEntropy) : 0;

    Key master_key{};
    ScopedWipe<Key> master_guard(master_key);
    {
        std::lock_guard<std::mutex> lock(mutex_);
        parsed.info.key_generation =
            options.key_generation.value_or(active_generation_);
        if (options.key_generation.has_value()) {
            master_key = generation_key_locked(*options.key_generation);
        } else {
            master_key = active_key_locked();
            parsed.info.key_generation = active_generation_;
        }
    }

    if (options.revocation_id.has_value()) {
        parsed.info.revocation_id = *options.revocation_id;
    } else {
        fill_random(parsed.info.revocation_id);
    }
    fill_random(parsed.salt);
    fill_random(parsed.nonce);

    if (parsed.info.has_entropy) {
        parsed.entropy_digest = sha256(optional_entropy.bytes());
    }

    parsed.header.reserve(kFixedPrefixSize + 4 + owner.sid.size() + 2 +
                          owner.logon_session.size() + 32 + 4);
    parsed.header.insert(parsed.header.end(), kMagic.begin(), kMagic.end());
    append_u16(parsed.header, kVersion);
    parsed.header.push_back(static_cast<std::uint8_t>(scope));
    parsed.header.push_back(parsed.flags);
    append_u32(parsed.header, parsed.info.key_generation);
    parsed.header.insert(parsed.header.end(),
                         parsed.info.revocation_id.begin(),
                         parsed.info.revocation_id.end());
    parsed.header.insert(parsed.header.end(), parsed.salt.begin(),
                         parsed.salt.end());
    parsed.header.insert(parsed.header.end(), parsed.nonce.begin(),
                         parsed.nonce.end());
    append_u16(parsed.header, static_cast<std::uint16_t>(owner.sid.size()));
    parsed.header.insert(parsed.header.end(), owner.sid.begin(), owner.sid.end());
    append_u16(parsed.header,
               static_cast<std::uint16_t>(owner.logon_session.size()));
    parsed.header.insert(parsed.header.end(), owner.logon_session.begin(),
                         owner.logon_session.end());
    parsed.header.insert(parsed.header.end(), parsed.entropy_digest.begin(),
                         parsed.entropy_digest.end());
    append_u32(parsed.header, static_cast<std::uint32_t>(plaintext.size()));

    DerivedKeys derived = derive_key(master_key, parsed);
    struct KeyGuard {
        Key encryption;
        Key authentication;
        ~KeyGuard() {
            secure_wipe(encryption);
            secure_wipe(authentication);
        }
    } guard{derived.encryption, derived.authentication};
    secure_wipe(derived.encryption);
    secure_wipe(derived.authentication);

    parsed.ciphertext =
        chacha20_xor(guard.encryption, parsed.nonce, plaintext);

    HmacSha256 mac(guard.authentication);
    mac.update(kAuthDomain);
    mac.update(parsed.header);
    mac.update(parsed.ciphertext);
    parsed.tag = mac.finish();

    std::vector<std::uint8_t> envelope;
    envelope.reserve(parsed.header.size() + parsed.ciphertext.size() + kTagSize);
    envelope.insert(envelope.end(), parsed.header.begin(), parsed.header.end());
    envelope.insert(envelope.end(), parsed.ciphertext.begin(),
                    parsed.ciphertext.end());
    envelope.insert(envelope.end(), parsed.tag.begin(), parsed.tag.end());

    return envelope;
}

std::vector<std::uint8_t> ProtectionContext::protect(
    const CallerContext& caller, std::span<const std::uint8_t> plaintext,
    Entropy optional_entropy,
    const ProtectOptions& options) {
    return protect(caller, caller.subject, caller.requested_scope, plaintext,
                   optional_entropy, options);
}

std::vector<std::uint8_t> ProtectionContext::protect(
    const CallerContext& caller, const Subject& owner, ProtectionScope scope,
    std::string_view plaintext, Entropy optional_entropy,
    const ProtectOptions& options) {
    return protect(
        caller, owner, scope,
        std::span<const std::uint8_t>(
            reinterpret_cast<const std::uint8_t*>(plaintext.data()),
            plaintext.size()),
        optional_entropy, options);
}

std::vector<std::uint8_t> ProtectionContext::protect(
    const CallerContext& caller, std::string_view plaintext,
    Entropy optional_entropy,
    const ProtectOptions& options) {
    return protect(caller, caller.subject, caller.requested_scope, plaintext,
                   optional_entropy, options);
}

std::vector<std::uint8_t> ProtectionContext::unprotect(
    const CallerContext& caller, std::span<const std::uint8_t> envelope,
    Entropy optional_entropy) {
    const ParsedEnvelope parsed = parse(envelope);

    Key master_key{};
    ScopedWipe<Key> master_guard(master_key);
    {
        std::lock_guard<std::mutex> lock(mutex_);
        master_key = generation_key_locked(parsed.info.key_generation);
        if (revoked_envelopes_.find(parsed.info.revocation_id) !=
            revoked_envelopes_.end()) {
            fail(ErrorCode::RevokedEnvelope,
                 "the envelope revocation ID has been revoked");
        }
    }

    DerivedKeys derived = derive_key(master_key, parsed);
    struct KeyGuard {
        Key encryption;
        Key authentication;
        ~KeyGuard() {
            secure_wipe(encryption);
            secure_wipe(authentication);
        }
    } guard{derived.encryption, derived.authentication};
    secure_wipe(derived.encryption);
    secure_wipe(derived.authentication);

    std::array<std::uint8_t, 32> expected_tag{};
    {
        HmacSha256 mac(guard.authentication);
        mac.update(kAuthDomain);
        mac.update(parsed.header);
        mac.update(parsed.ciphertext);
        expected_tag = mac.finish();
    }
    const bool authentic =
        constant_time_equal(expected_tag, parsed.tag);
    secure_wipe(expected_tag);
    if (!authentic) {
        fail(ErrorCode::AuthenticationFailed,
             "the envelope failed authentication");
    }

    authorize_caller(caller, parsed.info.subject, parsed.info.scope);

    const bool supplied_entropy = !optional_entropy.empty();
    if (supplied_entropy != parsed.info.has_entropy) {
        fail(ErrorCode::EntropyMismatch,
             "optional entropy presence does not match");
    }
    if (supplied_entropy) {
        const auto digest = sha256(optional_entropy.bytes());
        const bool matches =
            constant_time_equal(digest, parsed.entropy_digest);
        auto digest_copy = digest;
        secure_wipe(digest_copy);
        if (!matches) {
            fail(ErrorCode::EntropyMismatch,
                 "optional entropy does not match");
        }
    }

    auto plaintext = decrypt(guard.encryption, parsed.nonce,
                             parsed.ciphertext);
    return plaintext;
}

std::vector<std::uint8_t> ProtectionContext::decrypt(
    const Key& key, const std::array<std::uint8_t, kNonceSize>& nonce,
    std::span<const std::uint8_t> ciphertext) {
    return chacha20_xor(key, nonce, ciphertext);
}

EnvelopeInfo ProtectionContext::inspect(
    std::span<const std::uint8_t> envelope) const {
    return parse(envelope).info;
}

std::string ProtectionContext::revocation_id_hex(const RevocationId& id) {
    static constexpr char kHex[] = "0123456789abcdef";
    std::string result;
    result.reserve(id.size() * 2);
    for (std::uint8_t byte : id) {
        result.push_back(kHex[byte >> 4]);
        result.push_back(kHex[byte & 0x0f]);
    }
    return result;
}

RevocationId ProtectionContext::revocation_id_from_hex(
    std::string_view hex) {
    if (hex.size() != 32) {
        fail(ErrorCode::InvalidArgument,
             "a revocation ID must contain exactly 32 hexadecimal digits");
    }
    auto digit = [](char value) -> int {
        if (value >= '0' && value <= '9') {
            return value - '0';
        }
        if (value >= 'a' && value <= 'f') {
            return value - 'a' + 10;
        }
        if (value >= 'A' && value <= 'F') {
            return value - 'A' + 10;
        }
        return -1;
    };

    RevocationId result{};
    for (std::size_t i = 0; i < result.size(); ++i) {
        const int high = digit(hex[i * 2]);
        const int low = digit(hex[i * 2 + 1]);
        if (high < 0 || low < 0) {
            fail(ErrorCode::InvalidArgument,
                 "revocation ID contains a non-hexadecimal digit");
        }
        result[i] = static_cast<std::uint8_t>((high << 4) | low);
    }
    return result;
}

}  // namespace netsec
