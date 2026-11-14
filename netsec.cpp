#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cerrno>
#include <cmath>
#include <cstring>
#include <limits>
#include <random>
#include <stdexcept>
#include <utility>

#include <sys/mman.h>
#include <unistd.h>

#if defined(__APPLE__) || defined(__FreeBSD__) || defined(__OpenBSD__) || defined(__NetBSD__)
#include <cstdlib>
#elif defined(__linux__)
#include <sys/random.h>
#endif

namespace netsec::biometric {
namespace {

using Byte = std::byte;

constexpr std::array<Byte, 8> kMagic{
    static_cast<Byte>('B'),
    static_cast<Byte>('S'),
    static_cast<Byte>('T'),
    static_cast<Byte>('P'),
    static_cast<Byte>('L'),
    static_cast<Byte>('0'),
    static_cast<Byte>('0'),
    static_cast<Byte>('1')};
constexpr std::uint16_t kFormatVersion = 1;
constexpr std::size_t kNonceLength = 16;
constexpr std::size_t kTagLength = 32;
constexpr std::size_t kFixedHeaderLength = 8 + 2 + 2 + 8 + 8 + 4 + 16;
constexpr std::size_t kMaxIdentifierLength = 4096;
constexpr std::size_t kMaxPlaintextLength = 64U * 1024U * 1024U;
constexpr std::string_view kStreamLabel = "netsec.biometric.template.stream.v1";

[[nodiscard]] std::span<const Byte> as_bytes(std::string_view text) noexcept
{
    return {reinterpret_cast<const Byte*>(text.data()), text.size()};
}

void secure_wipe(void* memory, std::size_t size) noexcept
{
    volatile auto* bytes = static_cast<volatile std::uint8_t*>(memory);
    for (std::size_t index = 0; index < size; ++index) {
        bytes[index] = 0;
    }
}

template <std::size_t N>
void secure_wipe(std::array<Byte, N>& bytes) noexcept
{
    secure_wipe(bytes.data(), bytes.size());
}

[[nodiscard]] constexpr std::uint32_t rotate_right(
    std::uint32_t value,
    std::uint32_t amount) noexcept
{
    return (value >> amount) | (value << (32U - amount));
}

constexpr std::array<std::uint32_t, 64> kSha256RoundConstants{
    0x428a2f98U, 0x71374491U, 0xb5c0fbcfU, 0xe9b5dba5U, 0x3956c25bU, 0x59f111f1U,
    0x923f82a4U, 0xab1c5ed5U, 0xd807aa98U, 0x12835b01U, 0x243185beU, 0x550c7dc3U,
    0x72be5d74U, 0x80deb1feU, 0x9bdc06a7U, 0xc19bf174U, 0xe49b69c1U, 0xefbe4786U,
    0x0fc19dc6U, 0x240ca1ccU, 0x2de92c6fU, 0x4a7484aaU, 0x5cb0a9dcU, 0x76f988daU,
    0x983e5152U, 0xa831c66dU, 0xb00327c8U, 0xbf597fc7U, 0xc6e00bf3U, 0xd5a79147U,
    0x06ca6351U, 0x14292967U, 0x27b70a85U, 0x2e1b2138U, 0x4d2c6dfcU, 0x53380d13U,
    0x650a7354U, 0x766a0abbU, 0x81c2c92eU, 0x92722c85U, 0xa2bfe8a1U, 0xa81a664bU,
    0xc24b8b70U, 0xc76c51a3U, 0xd192e819U, 0xd6990624U, 0xf40e3585U, 0x106aa070U,
    0x19a4c116U, 0x1e376c08U, 0x2748774cU, 0x34b0bcb5U, 0x391c0cb3U, 0x4ed8aa4aU,
    0x5b9cca4fU, 0x682e6ff3U, 0x748f82eeU, 0x78a5636fU, 0x84c87814U, 0x8cc70208U,
    0x90befffaU, 0xa4506cebU, 0xbef9a3f7U, 0xc67178f2U};

constexpr std::array<std::uint32_t, 8> kSha256InitialState{
    0x6a09e667U, 0xbb67ae85U, 0x3c6ef372U, 0xa54ff53aU,
    0x510e527fU, 0x9b05688cU, 0x1f83d9abU, 0x5be0cd19U};

class Sha256 final {
public:
    Sha256() noexcept = default;

    void update(std::span<const Byte> input) noexcept
    {
        total_bytes_ += input.size();
        while (!input.empty()) {
            const std::size_t amount = std::min(input.size(), buffer_.size() - buffered_);
            std::memcpy(buffer_.data() + buffered_, input.data(), amount);
            buffered_ += amount;
            input = input.subspan(amount);
            if (buffered_ == buffer_.size()) {
                compress(buffer_.data());
                buffered_ = 0;
            }
        }
    }

    [[nodiscard]] std::array<Byte, 32> finish() noexcept
    {
        const std::uint64_t bit_length = total_bytes_ * 8U;
        buffer_[buffered_++] = 0x80U;
        if (buffered_ > 56) {
            std::fill(buffer_.begin() + static_cast<std::ptrdiff_t>(buffered_), buffer_.end(), 0);
            compress(buffer_.data());
            buffered_ = 0;
        }
        std::fill(
            buffer_.begin() + static_cast<std::ptrdiff_t>(buffered_),
            buffer_.begin() + 56,
            0);
        for (std::size_t index = 0; index < 8; ++index) {
            buffer_[63 - index] = static_cast<std::uint8_t>(bit_length >> (index * 8U));
        }
        compress(buffer_.data());

        std::array<Byte, 32> digest{};
        for (std::size_t word = 0; word < state_.size(); ++word) {
            const std::uint32_t value = state_[word];
            digest[word * 4] = static_cast<Byte>(value >> 24U);
            digest[word * 4 + 1] = static_cast<Byte>(value >> 16U);
            digest[word * 4 + 2] = static_cast<Byte>(value >> 8U);
            digest[word * 4 + 3] = static_cast<Byte>(value);
        }
        return digest;
    }

private:
    void compress(const std::uint8_t* block) noexcept
    {
        std::array<std::uint32_t, 64> schedule{};
        for (std::size_t index = 0; index < 16; ++index) {
            schedule[index] =
                (static_cast<std::uint32_t>(block[index * 4]) << 24U)
                | (static_cast<std::uint32_t>(block[index * 4 + 1]) << 16U)
                | (static_cast<std::uint32_t>(block[index * 4 + 2]) << 8U)
                | static_cast<std::uint32_t>(block[index * 4 + 3]);
        }
        for (std::size_t index = 16; index < schedule.size(); ++index) {
            const std::uint32_t s0 = rotate_right(schedule[index - 15], 7)
                ^ rotate_right(schedule[index - 15], 18)
                ^ (schedule[index - 15] >> 3U);
            const std::uint32_t s1 = rotate_right(schedule[index - 2], 17)
                ^ rotate_right(schedule[index - 2], 19)
                ^ (schedule[index - 2] >> 10U);
            schedule[index] = schedule[index - 16] + s0 + schedule[index - 7] + s1;
        }

        std::uint32_t a = state_[0];
        std::uint32_t b = state_[1];
        std::uint32_t c = state_[2];
        std::uint32_t d = state_[3];
        std::uint32_t e = state_[4];
        std::uint32_t f = state_[5];
        std::uint32_t g = state_[6];
        std::uint32_t h = state_[7];

        for (std::size_t index = 0; index < schedule.size(); ++index) {
            const std::uint32_t sum1 = rotate_right(e, 6)
                ^ rotate_right(e, 11)
                ^ rotate_right(e, 25);
            const std::uint32_t choice = (e & f) ^ ((~e) & g);
            const std::uint32_t temp1 =
                h + sum1 + choice + kSha256RoundConstants[index] + schedule[index];
            const std::uint32_t sum0 = rotate_right(a, 2)
                ^ rotate_right(a, 13)
                ^ rotate_right(a, 22);
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

    std::array<std::uint32_t, 8> state_{kSha256InitialState};
    std::array<std::uint8_t, 64> buffer_{};
    std::uint64_t total_bytes_{0};
    std::size_t buffered_{0};
};

class HmacSha256 final {
public:
    explicit HmacSha256(std::span<const Byte> key)
    {
        std::array<std::uint8_t, 64> normalized{};
        if (key.size() > normalized.size()) {
            Sha256 hasher;
            hasher.update(key);
            const auto digest = hasher.finish();
            std::memcpy(normalized.data(), digest.data(), digest.size());
        } else {
            std::memcpy(normalized.data(), key.data(), key.size());
        }

        std::array<std::uint8_t, 64> inner_pad{};
        for (std::size_t index = 0; index < normalized.size(); ++index) {
            inner_pad[index] = static_cast<std::uint8_t>(normalized[index] ^ 0x36U);
            operator_pad_[index] = static_cast<Byte>(normalized[index] ^ 0x5cU);
        }
        inner_.update({reinterpret_cast<const Byte*>(inner_pad.data()), inner_pad.size()});
        secure_wipe(normalized.data(), normalized.size());
        secure_wipe(inner_pad.data(), inner_pad.size());
    }

    ~HmacSha256()
    {
        secure_wipe(operator_pad_);
    }

    HmacSha256(const HmacSha256&) = delete;
    HmacSha256& operator=(const HmacSha256&) = delete;

    void update(std::span<const Byte> input) noexcept
    {
        inner_.update(input);
    }

    [[nodiscard]] std::array<Byte, 32> finish() noexcept
    {
        const auto inner_digest = inner_.finish();
        Sha256 outer;
        outer.update(operator_pad_);
        outer.update(inner_digest);
        auto result = outer.finish();
        secure_wipe(operator_pad_);
        return result;
    }

private:
    Sha256 inner_;
    std::array<Byte, 64> operator_pad_{};
};

[[nodiscard]] bool constant_time_equal(
    std::span<const Byte> left,
    std::span<const Byte> right) noexcept
{
    if (left.size() != right.size()) {
        return false;
    }
    std::uint8_t difference = 0;
    for (std::size_t index = 0; index < left.size(); ++index) {
        difference |= std::to_integer<std::uint8_t>(left[index] ^ right[index]);
    }
    return difference == 0;
}

struct Secret32 final {
    std::array<Byte, 32> bytes{};

    Secret32() = default;
    explicit Secret32(std::array<Byte, 32> value) noexcept
        : bytes(value)
    {
    }
    ~Secret32()
    {
        secure_wipe(bytes);
    }

    Secret32(const Secret32&) = delete;
    Secret32& operator=(const Secret32&) = delete;
};

[[nodiscard]] Secret32 derive_key(
    std::span<const Byte> master,
    std::string_view label)
{
    HmacSha256 hmac(master);
    hmac.update(as_bytes(label));
    return Secret32(hmac.finish());
}

[[nodiscard]] std::array<Byte, 32> keyed_mac(
    std::span<const Byte> key,
    std::span<const Byte> data)
{
    HmacSha256 hmac(key);
    hmac.update(data);
    return hmac.finish();
}

void xor_keystream(
    std::span<const Byte> key,
    const std::array<Byte, kNonceLength>& nonce,
    std::span<Byte> data)
{
    std::uint64_t counter = 0;
    std::size_t offset = 0;
    while (offset < data.size()) {
        std::array<Byte, 8> encoded_counter{};
        for (std::size_t index = 0; index < encoded_counter.size(); ++index) {
            encoded_counter[index] =
                static_cast<Byte>(counter >> ((encoded_counter.size() - index - 1) * 8U));
        }

        HmacSha256 hmac(key);
        hmac.update(as_bytes(kStreamLabel));
        hmac.update(nonce);
        hmac.update(encoded_counter);
        auto block = hmac.finish();

        const std::size_t amount = std::min(block.size(), data.size() - offset);
        for (std::size_t index = 0; index < amount; ++index) {
            data[offset + index] ^= block[index];
        }
        secure_wipe(block);
        offset += amount;
        ++counter;
    }
}

void fill_random(std::span<Byte> output)
{
#if defined(__APPLE__) || defined(__FreeBSD__) || defined(__OpenBSD__) || defined(__NetBSD__)
    arc4random_buf(output.data(), output.size());
#elif defined(__linux__)
    std::size_t offset = 0;
    while (offset < output.size()) {
        const auto result = ::getrandom(output.data() + offset, output.size() - offset, 0);
        if (result < 0) {
            if (errno == EINTR) {
                continue;
            }
            throw std::runtime_error("operating-system random source failed");
        }
        offset += static_cast<std::size_t>(result);
    }
#else
    std::random_device random;
    for (auto& byte : output) {
        byte = static_cast<Byte>(random());
    }
#endif
}

void append_u16(std::vector<Byte>& output, std::uint16_t value)
{
    output.push_back(static_cast<Byte>(value >> 8U));
    output.push_back(static_cast<Byte>(value));
}

void append_u32(std::vector<Byte>& output, std::uint32_t value)
{
    output.push_back(static_cast<Byte>(value >> 24U));
    output.push_back(static_cast<Byte>(value >> 16U));
    output.push_back(static_cast<Byte>(value >> 8U));
    output.push_back(static_cast<Byte>(value));
}

void append_u64(std::vector<Byte>& output, std::uint64_t value)
{
    for (std::size_t index = 0; index < 8; ++index) {
        output.push_back(static_cast<Byte>(value >> ((7U - index) * 8U)));
    }
}

void append_string(std::vector<Byte>& output, std::string_view value)
{
    append_u16(output, static_cast<std::uint16_t>(value.size()));
    const auto bytes = as_bytes(value);
    output.insert(output.end(), bytes.begin(), bytes.end());
}

[[nodiscard]] bool valid_identifier(std::string_view value) noexcept
{
    return !value.empty() && value.size() <= kMaxIdentifierLength;
}

class Reader final {
public:
    explicit Reader(std::span<const Byte> input) noexcept
        : input_(input)
    {
    }

    [[nodiscard]] bool read_u16(std::uint16_t& value) noexcept
    {
        if (remaining() < 2) {
            return false;
        }
        value = static_cast<std::uint16_t>(
            (std::to_integer<std::uint16_t>(input_[offset_]) << 8U)
            | std::to_integer<std::uint16_t>(input_[offset_ + 1]));
        offset_ += 2;
        return true;
    }

    [[nodiscard]] bool read_u32(std::uint32_t& value) noexcept
    {
        if (remaining() < 4) {
            return false;
        }
        value = (std::to_integer<std::uint32_t>(input_[offset_]) << 24U)
            | (std::to_integer<std::uint32_t>(input_[offset_ + 1]) << 16U)
            | (std::to_integer<std::uint32_t>(input_[offset_ + 2]) << 8U)
            | std::to_integer<std::uint32_t>(input_[offset_ + 3]);
        offset_ += 4;
        return true;
    }

    [[nodiscard]] bool read_u64(std::uint64_t& value) noexcept
    {
        if (remaining() < 8) {
            return false;
        }
        value = 0;
        for (std::size_t index = 0; index < 8; ++index) {
            value = (value << 8U)
                | std::to_integer<std::uint64_t>(input_[offset_ + index]);
        }
        offset_ += 8;
        return true;
    }

    [[nodiscard]] bool read_bytes(std::size_t size, std::span<const Byte>& value) noexcept
    {
        if (remaining() < size) {
            return false;
        }
        value = input_.subspan(offset_, size);
        offset_ += size;
        return true;
    }

    [[nodiscard]] bool read_string(std::string& value)
    {
        std::uint16_t size = 0;
        if (!read_u16(size) || size > kMaxIdentifierLength) {
            return false;
        }
        std::span<const Byte> bytes;
        if (!read_bytes(size, bytes)) {
            return false;
        }
        value.assign(reinterpret_cast<const char*>(bytes.data()), bytes.size());
        return true;
    }

    [[nodiscard]] std::size_t remaining() const noexcept
    {
        return input_.size() - offset_;
    }

    [[nodiscard]] std::size_t offset() const noexcept
    {
        return offset_;
    }

private:
    std::span<const Byte> input_;
    std::size_t offset_{0};
};

struct ParsedEnvelope {
    Algorithm algorithm{Algorithm::unknown};
    std::uint64_t generation{0};
    std::uint64_t issued_at_unix{0};
    std::uint32_t plaintext_size{0};
    std::array<Byte, kNonceLength> nonce{};
    std::string user_id;
    std::string device_id;
    std::string enrollment_session_id;
    std::string key_id;
    std::span<const Byte> ciphertext;
    std::span<const Byte> tag;
    std::span<const Byte> authenticated_bytes;
};

[[nodiscard]] std::optional<ParsedEnvelope> parse_envelope(
    std::span<const Byte> encoded)
{
    if (encoded.size() < kFixedHeaderLength + 8 + kTagLength) {
        return std::nullopt;
    }

    Reader reader(encoded);
    std::span<const Byte> magic;
    std::uint16_t format = 0;
    std::uint16_t algorithm = 0;
    ParsedEnvelope parsed;

    if (!reader.read_bytes(kMagic.size(), magic)
        || !std::equal(magic.begin(), magic.end(), kMagic.begin())
        || !reader.read_u16(format)
        || format != kFormatVersion
        || !reader.read_u16(algorithm)
        || !reader.read_u64(parsed.generation)
        || !reader.read_u64(parsed.issued_at_unix)
        || !reader.read_u32(parsed.plaintext_size)) {
        return std::nullopt;
    }
    parsed.algorithm = static_cast<Algorithm>(algorithm);

    std::span<const Byte> nonce;
    if (!reader.read_bytes(kNonceLength, nonce)) {
        return std::nullopt;
    }
    std::copy(nonce.begin(), nonce.end(), parsed.nonce.begin());

    if (!reader.read_string(parsed.user_id)
        || !reader.read_string(parsed.device_id)
        || !reader.read_string(parsed.enrollment_session_id)
        || !reader.read_string(parsed.key_id)) {
        return std::nullopt;
    }
    if (parsed.user_id.empty()
        || parsed.device_id.empty()
        || parsed.enrollment_session_id.empty()
        || parsed.key_id.empty()
        || parsed.plaintext_size > kMaxPlaintextLength) {
        return std::nullopt;
    }

    if (reader.remaining()
        != static_cast<std::size_t>(parsed.plaintext_size) + kTagLength) {
        return std::nullopt;
    }
    if (!reader.read_bytes(parsed.plaintext_size, parsed.ciphertext)
        || !reader.read_bytes(kTagLength, parsed.tag)) {
        return std::nullopt;
    }

    const std::size_t authenticated_size = encoded.size() - kTagLength;
    parsed.authenticated_bytes = encoded.first(authenticated_size);
    return parsed;
}

[[nodiscard]] std::size_t page_size() noexcept
{
    static const std::size_t value = []() noexcept {
        const long result = ::sysconf(_SC_PAGESIZE);
        return result > 0 ? static_cast<std::size_t>(result) : std::size_t{4096};
    }();
    return value;
}

} // namespace

namespace detail {

struct KeyAccess {
    [[nodiscard]] static std::span<const Byte> material(
        const EncryptionKey& key) noexcept
    {
        return key.material_;
    }
};

struct ProtectedBytesAccess {
    [[nodiscard]] static std::span<Byte> mutable_bytes(
        ProtectedBytes& bytes) noexcept
    {
        return {bytes.data_, bytes.size_};
    }
};

} // namespace detail

std::string_view status_name(Status status) noexcept
{
    switch (status) {
    case Status::ok:
        return "ok";
    case Status::invalid_argument:
        return "invalid_argument";
    case Status::malformed_template:
        return "malformed_template";
    case Status::integrity_failure:
        return "integrity_failure";
    case Status::key_mismatch:
        return "key_mismatch";
    case Status::user_mismatch:
        return "user_mismatch";
    case Status::device_mismatch:
        return "device_mismatch";
    case Status::enrollment_session_mismatch:
        return "enrollment_session_mismatch";
    case Status::stale_generation:
        return "stale_generation";
    case Status::template_expired:
        return "template_expired";
    case Status::template_not_yet_valid:
        return "template_not_yet_valid";
    case Status::algorithm_downgrade:
        return "algorithm_downgrade";
    case Status::algorithm_mismatch:
        return "algorithm_mismatch";
    case Status::unsupported_algorithm:
        return "unsupported_algorithm";
    case Status::secure_memory_failure:
        return "secure_memory_failure";
    }
    return "unknown";
}

EncryptionKey::EncryptionKey(
    std::string key_id,
    std::span<const std::byte> key_material)
{
    if (!valid_identifier(key_id) || key_material.size() != material_.size()) {
        throw std::invalid_argument("encryption key id or material has invalid size");
    }
    key_id_ = std::move(key_id);
    std::copy(key_material.begin(), key_material.end(), material_.begin());
}

EncryptionKey::~EncryptionKey() noexcept
{
    wipe();
}

EncryptionKey::EncryptionKey(EncryptionKey&& other) noexcept
    : key_id_(std::move(other.key_id_)),
      material_(other.material_)
{
    other.wipe();
}

EncryptionKey& EncryptionKey::operator=(EncryptionKey&& other) noexcept
{
    if (this != &other) {
        wipe();
        key_id_ = std::move(other.key_id_);
        material_ = other.material_;
        other.wipe();
    }
    return *this;
}

const std::string& EncryptionKey::id() const noexcept
{
    return key_id_;
}

EncryptionKey EncryptionKey::from_hex(
    std::string key_id,
    std::string_view hex_key)
{
    if (hex_key.size() != 64) {
        throw std::invalid_argument("encryption key must be 64 hexadecimal digits");
    }

    const auto nibble = [](char value) -> std::uint8_t {
        if (value >= '0' && value <= '9') {
            return static_cast<std::uint8_t>(value - '0');
        }
        if (value >= 'a' && value <= 'f') {
            return static_cast<std::uint8_t>(value - 'a' + 10);
        }
        if (value >= 'A' && value <= 'F') {
            return static_cast<std::uint8_t>(value - 'A' + 10);
        }
        throw std::invalid_argument("encryption key contains a non-hex digit");
    };

    std::array<std::byte, 32> material{};
    for (std::size_t index = 0; index < material.size(); ++index) {
        material[index] = static_cast<std::byte>(
            static_cast<std::uint8_t>(
                (nibble(hex_key[index * 2]) << 4U) | nibble(hex_key[index * 2 + 1])));
    }
    return EncryptionKey(std::move(key_id), material);
}

void EncryptionKey::wipe() noexcept
{
    secure_wipe(material_);
}

ProtectedBytes::ProtectedBytes(std::size_t size)
{
    if (size == 0) {
        return;
    }

    const std::size_t page = page_size();
    if (size > std::numeric_limits<std::size_t>::max() - (page - 1)) {
        throw std::bad_alloc();
    }
    const std::size_t data_size = ((size + page - 1) / page) * page;
    if (data_size > (std::numeric_limits<std::size_t>::max() / page - 2) * page) {
        throw std::bad_alloc();
    }
    const std::size_t total_size = data_size + page * 2;

    void* base = ::mmap(
        nullptr,
        total_size,
        PROT_READ | PROT_WRITE,
        MAP_PRIVATE | MAP_ANONYMOUS,
        -1,
        0);
    if (base == MAP_FAILED) {
        throw std::runtime_error("unable to reserve protected memory");
    }

    auto* bytes = static_cast<std::byte*>(base);
    if (::mprotect(bytes, page, PROT_NONE) != 0
        || ::mprotect(bytes + page + data_size, page, PROT_NONE) != 0) {
        ::munmap(base, total_size);
        throw std::runtime_error("unable to create protected-memory guards");
    }

    void* data = bytes + page;
    if (::mlock(data, data_size) != 0) {
        ::munmap(base, total_size);
        throw std::runtime_error("unable to lock protected memory");
    }

    region_ = base;
    region_size_ = total_size;
    data_ = static_cast<std::byte*>(data);
    size_ = size;
    locked_ = true;
}

ProtectedBytes::~ProtectedBytes() noexcept
{
    release();
}

ProtectedBytes::ProtectedBytes(ProtectedBytes&& other) noexcept
    : data_(other.data_),
      region_(other.region_),
      region_size_(other.region_size_),
      size_(other.size_),
      locked_(other.locked_)
{
    other.data_ = nullptr;
    other.region_ = nullptr;
    other.region_size_ = 0;
    other.size_ = 0;
    other.locked_ = false;
}

ProtectedBytes& ProtectedBytes::operator=(ProtectedBytes&& other) noexcept
{
    if (this != &other) {
        release();
        data_ = other.data_;
        region_ = other.region_;
        region_size_ = other.region_size_;
        size_ = other.size_;
        locked_ = other.locked_;

        other.data_ = nullptr;
        other.region_ = nullptr;
        other.region_size_ = 0;
        other.size_ = 0;
        other.locked_ = false;
    }
    return *this;
}

std::span<const std::byte> ProtectedBytes::bytes() const noexcept
{
    return {data_, size_};
}

std::size_t ProtectedBytes::size() const noexcept
{
    return size_;
}

bool ProtectedBytes::empty() const noexcept
{
    return size_ == 0;
}

bool ProtectedBytes::is_locked() const noexcept
{
    return locked_;
}

void ProtectedBytes::release() noexcept
{
    if (region_ == nullptr) {
        return;
    }
    if (data_ != nullptr && size_ != 0) {
        const std::size_t page = page_size();
        const std::size_t data_size = ((size_ + page - 1) / page) * page;
        secure_wipe(data_, data_size);
    }
    if (locked_) {
        const std::size_t page = page_size();
        const std::size_t data_size = ((size_ + page - 1) / page) * page;
        ::munlock(data_, data_size);
    }
    ::munmap(region_, region_size_);

    data_ = nullptr;
    region_ = nullptr;
    region_size_ = 0;
    size_ = 0;
    locked_ = false;
}

TemplateEnvelope::TemplateEnvelope(std::vector<std::byte> encoded) noexcept
    : encoded_(std::move(encoded))
{
}

std::span<const std::byte> TemplateEnvelope::encoded() const noexcept
{
    return encoded_;
}

std::size_t TemplateEnvelope::size() const noexcept
{
    return encoded_.size();
}

TemplateEnvelope::operator bool() const noexcept
{
    return !encoded_.empty();
}

SealResult seal_template(
    std::span<const std::byte> plaintext,
    const TemplateRegistration& registration,
    const EncryptionKey& key)
{
    if (!valid_identifier(registration.user_id)
        || !valid_identifier(registration.device_id)
        || !valid_identifier(registration.enrollment_session_id)
        || !is_known_algorithm(registration.algorithm)
        || plaintext.size() > kMaxPlaintextLength
        || !valid_identifier(key.id())) {
        return SealResult{Status::invalid_argument, TemplateEnvelope{}};
    }

    const Secret32 encryption_key = derive_key(
        detail::KeyAccess::material(key),
        "netsec.biometric.template.encryption-key.v1");
    const Secret32 mac_key = derive_key(
        detail::KeyAccess::material(key),
        "netsec.biometric.template.mac-key.v1");

    std::array<Byte, kNonceLength> nonce{};
    fill_random(nonce);

    std::vector<Byte> encoded;
    encoded.reserve(
        kFixedHeaderLength
        + registration.user_id.size()
        + registration.device_id.size()
        + registration.enrollment_session_id.size()
        + key.id().size()
        + plaintext.size()
        + 8
        + kTagLength);

    encoded.insert(encoded.end(), kMagic.begin(), kMagic.end());
    append_u16(encoded, kFormatVersion);
    append_u16(encoded, algorithm_version(registration.algorithm));
    append_u64(encoded, registration.generation);
    append_u64(encoded, registration.issued_at_unix);
    append_u32(encoded, static_cast<std::uint32_t>(plaintext.size()));
    encoded.insert(encoded.end(), nonce.begin(), nonce.end());
    append_string(encoded, registration.user_id);
    append_string(encoded, registration.device_id);
    append_string(encoded, registration.enrollment_session_id);
    append_string(encoded, key.id());

    std::vector<Byte> ciphertext(plaintext.begin(), plaintext.end());
    xor_keystream(encryption_key.bytes, nonce, ciphertext);
    encoded.insert(encoded.end(), ciphertext.begin(), ciphertext.end());

    const auto tag = keyed_mac(mac_key.bytes, encoded);
    encoded.insert(encoded.end(), tag.begin(), tag.end());
    return SealResult{Status::ok, TemplateEnvelope(std::move(encoded))};
}

ImportResult import_template(std::span<const std::byte> encoded)
{
    if (!parse_envelope(encoded).has_value()) {
        return ImportResult{Status::malformed_template, TemplateEnvelope{}};
    }
    return ImportResult{
        Status::ok,
        TemplateEnvelope(std::vector<Byte>(encoded.begin(), encoded.end()))};
}

OpenResult open_template(
    const TemplateEnvelope& envelope,
    const TemplateExpectation& expectation,
    const ValidationPolicy& policy,
    const EncryptionKey& key)
{
    if (!valid_identifier(expectation.user_id)
        || !valid_identifier(expectation.device_id)
        || !valid_identifier(expectation.enrollment_session_id)
        || !is_known_algorithm(policy.minimum_algorithm)
        || (policy.expected_algorithm.has_value()
            && !is_known_algorithm(*policy.expected_algorithm))) {
        return OpenResult{Status::invalid_argument, {}, {}};
    }

    const auto parsed = parse_envelope(envelope.encoded());
    if (!parsed.has_value()) {
        return OpenResult{Status::malformed_template, {}, {}};
    }

    if (parsed->key_id != key.id()) {
        return OpenResult{Status::key_mismatch, {}, {}};
    }

    const Secret32 encryption_key = derive_key(
        detail::KeyAccess::material(key),
        "netsec.biometric.template.encryption-key.v1");
    const Secret32 mac_key = derive_key(
        detail::KeyAccess::material(key),
        "netsec.biometric.template.mac-key.v1");

    const auto expected_tag = keyed_mac(mac_key.bytes, parsed->authenticated_bytes);
    if (!constant_time_equal(expected_tag, parsed->tag)) {
        return OpenResult{Status::integrity_failure, {}, {}};
    }

    if (!is_known_algorithm(parsed->algorithm)) {
        return OpenResult{Status::unsupported_algorithm, {}, {}};
    }
    if (policy.expected_algorithm.has_value()
        && parsed->algorithm != *policy.expected_algorithm) {
        if (algorithm_version(parsed->algorithm)
            < algorithm_version(*policy.expected_algorithm)) {
            return OpenResult{Status::algorithm_downgrade, {}, {}};
        }
        return OpenResult{Status::algorithm_mismatch, {}, {}};
    }
    if (algorithm_version(parsed->algorithm)
        < algorithm_version(policy.minimum_algorithm)) {
        return OpenResult{Status::algorithm_downgrade, {}, {}};
    }

    if (parsed->user_id != expectation.user_id) {
        return OpenResult{Status::user_mismatch, {}, {}};
    }
    if (parsed->device_id != expectation.device_id) {
        return OpenResult{Status::device_mismatch, {}, {}};
    }
    if (parsed->enrollment_session_id != expectation.enrollment_session_id) {
        return OpenResult{Status::enrollment_session_mismatch, {}, {}};
    }
    if (expectation.expected_generation.has_value()
        && parsed->generation != *expectation.expected_generation) {
        return OpenResult{Status::stale_generation, {}, {}};
    }
    if (parsed->generation < policy.minimum_generation) {
        return OpenResult{Status::stale_generation, {}, {}};
    }

    if (policy.now_unix != 0) {
        std::uint64_t future_limit = policy.now_unix;
        if (policy.allowed_clock_skew_seconds
            > std::numeric_limits<std::uint64_t>::max() - future_limit) {
            future_limit = std::numeric_limits<std::uint64_t>::max();
        } else {
            future_limit += policy.allowed_clock_skew_seconds;
        }
        if (parsed->issued_at_unix > future_limit) {
            return OpenResult{Status::template_not_yet_valid, {}, {}};
        }
        if (policy.maximum_age_seconds != 0
            && parsed->issued_at_unix <= policy.now_unix
            && policy.now_unix - parsed->issued_at_unix > policy.maximum_age_seconds) {
            return OpenResult{Status::template_expired, {}, {}};
        }
    }

    ProtectedBytes plaintext;
    try {
        plaintext = ProtectedBytes(static_cast<std::size_t>(parsed->plaintext_size));
    } catch (const std::exception&) {
        return OpenResult{Status::secure_memory_failure, {}, {}};
    }

    std::copy(
        parsed->ciphertext.begin(),
        parsed->ciphertext.end(),
        detail::ProtectedBytesAccess::mutable_bytes(plaintext).begin());
    xor_keystream(
        encryption_key.bytes,
        parsed->nonce,
        detail::ProtectedBytesAccess::mutable_bytes(plaintext));

    TemplateMetadata metadata;
    metadata.user_id = parsed->user_id;
    metadata.device_id = parsed->device_id;
    metadata.enrollment_session_id = parsed->enrollment_session_id;
    metadata.algorithm = parsed->algorithm;
    metadata.generation = parsed->generation;
    metadata.issued_at_unix = parsed->issued_at_unix;
    metadata.key_id = parsed->key_id;
    return OpenResult{Status::ok, std::move(plaintext), std::move(metadata)};
}

std::string_view match_decision_reason_name(MatchDecisionReason reason) noexcept
{
    switch (reason) {
    case MatchDecisionReason::accepted:
        return "accepted";
    case MatchDecisionReason::invalid_evidence:
        return "invalid_evidence";
    case MatchDecisionReason::match_score_below_threshold:
        return "match_score_below_threshold";
    case MatchDecisionReason::liveness_failed:
        return "liveness_failed";
    case MatchDecisionReason::liveness_score_below_threshold:
        return "liveness_score_below_threshold";
    }
    return "invalid_evidence";
}

MatchDecision decide_match(
    double match_score,
    const LivenessResult& liveness,
    const MatchPolicy& policy) noexcept
{
    const auto valid_probability = [](double value) noexcept {
        return std::isfinite(value) && value >= 0.0 && value <= 1.0;
    };

    if (!valid_probability(match_score)
        || !valid_probability(liveness.score)
        || !valid_probability(policy.minimum_match_score)
        || !valid_probability(policy.minimum_liveness_score)) {
        return MatchDecision{false, MatchDecisionReason::invalid_evidence};
    }
    if (!liveness.passed) {
        return MatchDecision{false, MatchDecisionReason::liveness_failed};
    }
    if (liveness.score < policy.minimum_liveness_score) {
        return MatchDecision{
            false,
            MatchDecisionReason::liveness_score_below_threshold};
    }
    if (match_score < policy.minimum_match_score) {
        return MatchDecision{
            false,
            MatchDecisionReason::match_score_below_threshold};
    }
    return MatchDecision{true, MatchDecisionReason::accepted};
}

MatchDecision decide_match(
    double match_score,
    bool liveness_passed,
    const MatchPolicy& policy) noexcept
{
    return decide_match(
        match_score,
        LivenessResult{liveness_passed, liveness_passed ? 1.0 : 0.0},
        policy);
}

} // namespace netsec::biometric
