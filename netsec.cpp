#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <limits>
#include <map>
#include <mutex>
#include <random>
#include <set>
#include <span>
#include <utility>

namespace netsec {
namespace {

// ---------------------------------------------------------------------------
// Small self-contained SHA-256 / HMAC / HKDF implementation.
// Keeping the component dependency-free makes it easy for a coordinator to
// import without pulling a TLS stack into the embedded build.
// ---------------------------------------------------------------------------

constexpr std::array<std::uint32_t, 64> kSha256RoundConstants = {
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
};

constexpr std::uint32_t rotate_right(std::uint32_t value, unsigned bits) noexcept {
    return (value >> bits) | (value << (32U - bits));
}

class Sha256 {
public:
    void update(const std::uint8_t* data, std::size_t length) {
        total_length_ += length;
        while (length > 0) {
            const std::size_t available = buffer_.size() - buffer_length_;
            const std::size_t take = std::min(available, length);
            std::copy_n(data, take, buffer_.begin() + static_cast<std::ptrdiff_t>(buffer_length_));
            buffer_length_ += take;
            data += take;
            length -= take;
            if (buffer_length_ == buffer_.size()) {
                transform();
                buffer_length_ = 0;
            }
        }
    }

    void update(std::span<const std::uint8_t> data) {
        update(data.data(), data.size());
    }

    [[nodiscard]] std::array<std::uint8_t, 32> finish() {
        const std::uint64_t bit_length = total_length_ * 8U;
        buffer_[buffer_length_++] = 0x80U;
        if (buffer_length_ > 56U) {
            std::fill(buffer_.begin() + static_cast<std::ptrdiff_t>(buffer_length_),
                      buffer_.end(), 0U);
            transform();
            buffer_length_ = 0;
        }
        std::fill(buffer_.begin() + static_cast<std::ptrdiff_t>(buffer_length_),
                  buffer_.begin() + 56, 0U);
        for (std::size_t i = 0; i < 8; ++i) {
            buffer_[56 + i] =
                static_cast<std::uint8_t>(bit_length >> (56U - 8U * i));
        }
        transform();

        std::array<std::uint8_t, 32> digest{};
        for (std::size_t i = 0; i < state_.size(); ++i) {
            digest[i * 4] = static_cast<std::uint8_t>(state_[i] >> 24U);
            digest[i * 4 + 1] = static_cast<std::uint8_t>(state_[i] >> 16U);
            digest[i * 4 + 2] = static_cast<std::uint8_t>(state_[i] >> 8U);
            digest[i * 4 + 3] = static_cast<std::uint8_t>(state_[i]);
        }
        return digest;
    }

private:
    void transform() {
        std::array<std::uint32_t, 64> words{};
        for (std::size_t i = 0; i < 16; ++i) {
            words[i] = (static_cast<std::uint32_t>(buffer_[i * 4]) << 24U) |
                       (static_cast<std::uint32_t>(buffer_[i * 4 + 1]) << 16U) |
                       (static_cast<std::uint32_t>(buffer_[i * 4 + 2]) << 8U) |
                       static_cast<std::uint32_t>(buffer_[i * 4 + 3]);
        }
        for (std::size_t i = 16; i < 64; ++i) {
            const std::uint32_t s0 = rotate_right(words[i - 15], 7U) ^
                                     rotate_right(words[i - 15], 18U) ^
                                     (words[i - 15] >> 3U);
            const std::uint32_t s1 = rotate_right(words[i - 2], 17U) ^
                                     rotate_right(words[i - 2], 19U) ^
                                     (words[i - 2] >> 10U);
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
            const std::uint32_t sum1 = rotate_right(e, 6U) ^ rotate_right(e, 11U) ^
                                       rotate_right(e, 25U);
            const std::uint32_t choose = (e & f) ^ ((~e) & g);
            const std::uint32_t temp1 =
                h + sum1 + choose + kSha256RoundConstants[i] + words[i];
            const std::uint32_t sum0 = rotate_right(a, 2U) ^ rotate_right(a, 13U) ^
                                       rotate_right(a, 22U);
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

    std::array<std::uint32_t, 8> state_ = {
        0x6a09e667U, 0xbb67ae85U, 0x3c6ef372U, 0xa54ff53aU,
        0x510e527fU, 0x9b05688cU, 0x1f83d9abU, 0x5be0cd19U,
    };
    std::array<std::uint8_t, 64> buffer_{};
    std::size_t buffer_length_ = 0;
    std::uint64_t total_length_ = 0;
};

Bytes sha256(std::span<const std::uint8_t> data) {
    Sha256 hash;
    hash.update(data);
    const auto digest = hash.finish();
    return Bytes(digest.begin(), digest.end());
}

Bytes hmac_sha256(std::span<const std::uint8_t> key,
                  std::span<const std::uint8_t> data) {
    constexpr std::size_t kBlockSize = 64;
    std::array<std::uint8_t, kBlockSize> normalized_key{};
    if (key.size() > kBlockSize) {
        const Bytes hashed_key = sha256(key);
        std::copy(hashed_key.begin(), hashed_key.end(), normalized_key.begin());
    } else {
        std::copy(key.begin(), key.end(), normalized_key.begin());
    }

    std::array<std::uint8_t, kBlockSize> inner_pad{};
    std::array<std::uint8_t, kBlockSize> outer_pad{};
    for (std::size_t i = 0; i < kBlockSize; ++i) {
        inner_pad[i] = static_cast<std::uint8_t>(normalized_key[i] ^ 0x36U);
        outer_pad[i] = static_cast<std::uint8_t>(normalized_key[i] ^ 0x5cU);
    }

    Sha256 inner;
    inner.update(inner_pad);
    inner.update(data);
    const auto inner_digest = inner.finish();

    Sha256 outer;
    outer.update(outer_pad);
    outer.update(inner_digest);
    const auto outer_digest = outer.finish();
    return Bytes(outer_digest.begin(), outer_digest.end());
}

Bytes hkdf_sha256(std::span<const std::uint8_t> input_key_material,
                  std::span<const std::uint8_t> salt,
                  std::span<const std::uint8_t> info,
                  std::size_t output_length) {
    static constexpr std::array<std::uint8_t, 32> kZeroSalt{};
    const std::span<const std::uint8_t> effective_salt =
        salt.empty() ? std::span<const std::uint8_t>(kZeroSalt)
                     : salt;
    const Bytes prk = hmac_sha256(effective_salt, input_key_material);

    Bytes output;
    output.reserve(output_length);
    Bytes previous;
    std::uint8_t counter = 1;
    while (output.size() < output_length) {
        Bytes message;
        message.reserve(previous.size() + info.size() + 1);
        message.insert(message.end(), previous.begin(), previous.end());
        message.insert(message.end(), info.begin(), info.end());
        message.push_back(counter);
        previous = hmac_sha256(prk, message);
        const std::size_t remaining = output_length - output.size();
        const std::size_t take = std::min(remaining, previous.size());
        output.insert(output.end(), previous.begin(),
                      previous.begin() + static_cast<std::ptrdiff_t>(take));
        ++counter;
    }
    return output;
}

Bytes random_bytes(std::size_t count) {
    Bytes output(count);
    std::random_device source;
    for (auto& byte : output) {
        byte = static_cast<std::uint8_t>(source());
    }
    return output;
}

bool constant_time_equal(std::span<const std::uint8_t> left,
                         std::span<const std::uint8_t> right) noexcept {
    if (left.size() != right.size()) {
        return false;
    }
    std::uint8_t difference = 0;
    for (std::size_t i = 0; i < left.size(); ++i) {
        difference |= static_cast<std::uint8_t>(left[i] ^ right[i]);
    }
    return difference == 0;
}

void append_u32(Bytes& output, std::uint32_t value) {
    output.push_back(static_cast<std::uint8_t>(value >> 24U));
    output.push_back(static_cast<std::uint8_t>(value >> 16U));
    output.push_back(static_cast<std::uint8_t>(value >> 8U));
    output.push_back(static_cast<std::uint8_t>(value));
}

void append_u64(Bytes& output, std::uint64_t value) {
    for (int shift = 56; shift >= 0; shift -= 8) {
        output.push_back(static_cast<std::uint8_t>(value >> shift));
    }
}

void append_length_prefixed(Bytes& output, std::string_view value) {
    append_u32(output, static_cast<std::uint32_t>(value.size()));
    output.insert(output.end(), value.begin(), value.end());
}

std::string hex_encode(std::span<const std::uint8_t> bytes) {
    static constexpr char kDigits[] = "0123456789abcdef";
    std::string output;
    output.reserve(bytes.size() * 2);
    for (const std::uint8_t byte : bytes) {
        output.push_back(kDigits[byte >> 4U]);
        output.push_back(kDigits[byte & 0x0fU]);
    }
    return output;
}

int hex_value(char character) noexcept {
    if (character >= '0' && character <= '9') {
        return character - '0';
    }
    if (character >= 'a' && character <= 'f') {
        return character - 'a' + 10;
    }
    if (character >= 'A' && character <= 'F') {
        return character - 'A' + 10;
    }
    return -1;
}

std::optional<Bytes> hex_decode(std::string_view text) {
    if (text.size() % 2 != 0) {
        return std::nullopt;
    }
    Bytes output;
    output.reserve(text.size() / 2);
    for (std::size_t i = 0; i < text.size(); i += 2) {
        const int high = hex_value(text[i]);
        const int low = hex_value(text[i + 1]);
        if (high < 0 || low < 0) {
            return std::nullopt;
        }
        output.push_back(static_cast<std::uint8_t>((high << 4) | low));
    }
    return output;
}

Bytes delivery_aad(const std::string& device_id, std::uint64_t generation) {
    Bytes aad;
    append_length_prefixed(aad, "netsec/network-key-delivery/v1");
    append_length_prefixed(aad, device_id);
    append_u64(aad, generation);
    return aad;
}

std::pair<Bytes, Bytes> derive_wrap_keys(
    std::span<const std::uint8_t> device_secret,
    std::span<const std::uint8_t> nonce,
    std::span<const std::uint8_t> aad) {
    Bytes output = hkdf_sha256(device_secret, nonce, aad, 64);
    Bytes encryption_key(output.begin(), output.begin() + 32);
    Bytes authentication_key(output.begin() + 32, output.end());
    return {std::move(encryption_key), std::move(authentication_key)};
}

Bytes authentication_tag(std::span<const std::uint8_t> authentication_key,
                         std::span<const std::uint8_t> aad,
                         std::span<const std::uint8_t> nonce,
                         std::span<const std::uint8_t> ciphertext) {
    Bytes message;
    append_length_prefixed(message, "netsec/key-wrap-tag/v1");
    message.insert(message.end(), aad.begin(), aad.end());
    message.insert(message.end(), nonce.begin(), nonce.end());
    message.insert(message.end(), ciphertext.begin(), ciphertext.end());
    return hmac_sha256(authentication_key, message);
}

Bytes xor_keystream(std::span<const std::uint8_t> key,
                    std::span<const std::uint8_t> nonce,
                    std::span<const std::uint8_t> input) {
    Bytes output(input.begin(), input.end());
    std::uint32_t counter = 0;
    for (std::size_t offset = 0; offset < output.size();) {
        Bytes message(nonce.begin(), nonce.end());
        append_u32(message, counter++);
        const Bytes block = hmac_sha256(key, message);
        const std::size_t take = std::min(block.size(), output.size() - offset);
        for (std::size_t i = 0; i < take; ++i) {
            output[offset + i] =
                static_cast<std::uint8_t>(output[offset + i] ^ block[i]);
        }
        offset += take;
    }
    return output;
}

KeyDelivery seal_network_key(const Bytes& network_key,
                             std::span<const std::uint8_t> device_secret,
                             const std::string& device_id,
                             std::uint64_t generation) {
    KeyDelivery delivery;
    delivery.device_id = device_id;
    delivery.generation = generation;
    delivery.nonce = random_bytes(24);

    const Bytes aad = delivery_aad(device_id, generation);
    auto [encryption_key, authentication_key] =
        derive_wrap_keys(device_secret, delivery.nonce, aad);
    delivery.ciphertext =
        xor_keystream(encryption_key, delivery.nonce, network_key);
    delivery.tag = authentication_tag(authentication_key, aad, delivery.nonce,
                                      delivery.ciphertext);
    return delivery;
}

std::optional<Bytes> open_network_key(
    const KeyDelivery& delivery,
    std::span<const std::uint8_t> device_secret) {
    if (delivery.device_id.empty() || delivery.nonce.size() != 24 ||
        delivery.ciphertext.size() != kNetworkKeySize ||
        delivery.tag.size() != 32 || device_secret.empty()) {
        return std::nullopt;
    }

    const Bytes aad = delivery_aad(delivery.device_id, delivery.generation);
    auto [encryption_key, authentication_key] =
        derive_wrap_keys(device_secret, delivery.nonce, aad);
    const Bytes expected_tag =
        authentication_tag(authentication_key, aad, delivery.nonce,
                           delivery.ciphertext);
    if (!constant_time_equal(expected_tag, delivery.tag)) {
        return std::nullopt;
    }
    return xor_keystream(encryption_key, delivery.nonce,
                         delivery.ciphertext);
}

struct ParsedInstallCode {
    Bytes nonce;
    Bytes tag;
};

std::optional<ParsedInstallCode> parse_install_code(std::string_view token) {
    constexpr std::string_view kPrefix = "nic1.";
    if (!token.starts_with(kPrefix)) {
        return std::nullopt;
    }
    const std::size_t first_dot = token.find('.', kPrefix.size());
    if (first_dot == std::string_view::npos) {
        return std::nullopt;
    }
    auto nonce = hex_decode(token.substr(kPrefix.size(),
                                         first_dot - kPrefix.size()));
    auto tag = hex_decode(token.substr(first_dot + 1));
    if (!nonce || !tag || nonce->size() != 16 || tag->size() != 16) {
        return std::nullopt;
    }
    return ParsedInstallCode{std::move(*nonce), std::move(*tag)};
}

std::string make_install_code(const Bytes& root_secret,
                              const std::string& device_id,
                              const Bytes& nonce,
                              std::uint64_t generation) {
    Bytes message;
    append_length_prefixed(message, "netsec/install-code/v1");
    append_length_prefixed(message, device_id);
    message.insert(message.end(), nonce.begin(), nonce.end());
    append_u64(message, generation);
    const Bytes mac = hmac_sha256(root_secret, message);

    Bytes tag(mac.begin(), mac.begin() + 16);
    return "nic1." + hex_encode(nonce) + "." + hex_encode(tag);
}

Bytes install_code_mac(std::span<const std::uint8_t> root_secret,
                       const std::string& device_id,
                       std::span<const std::uint8_t> nonce,
                       std::uint64_t generation) {
    Bytes message;
    append_length_prefixed(message, "netsec/install-code/v1");
    append_length_prefixed(message, device_id);
    message.insert(message.end(), nonce.begin(), nonce.end());
    append_u64(message, generation);
    return hmac_sha256(root_secret, message);
}

Bytes decode_device_secret(std::string_view encoded) {
    auto decoded = hex_decode(encoded);
    if (!decoded || decoded->size() != 32) {
        return {};
    }
    return std::move(*decoded);
}

}  // namespace

std::optional<Bytes> open_key_update(const KeyDelivery& delivery,
                                     std::string_view device_secret) {
    const Bytes decoded_secret = decode_device_secret(device_secret);
    if (decoded_secret.empty()) {
        return std::nullopt;
    }
    return open_network_key(delivery, decoded_secret);
}

struct TrustCenter::Impl {
    struct InstallRecord {
        std::string device_id;
        Bytes nonce;
        std::uint64_t generation = 0;
        bool consumed = false;
    };

    struct DeviceState {
        bool revoked = false;
        bool joined = false;
        Bytes secret;
    };

    struct PendingRotation {
        std::uint64_t generation = 0;
        Bytes key;
        std::vector<KeyDelivery> deliveries;
        std::set<std::string> awaiting;
    };

    explicit Impl(Bytes root)
        : root_secret(std::move(root)), current_key(random_bytes(kNetworkKeySize)) {
        if (root_secret.empty()) {
            root_secret = random_bytes(kNetworkKeySize);
        }
    }

    void consume_active_code(const std::string& device_id) {
        const auto active = active_code_by_device.find(device_id);
        if (active == active_code_by_device.end()) {
            return;
        }
        const auto record = install_codes.find(active->second);
        if (record != install_codes.end()) {
            record->second.consumed = true;
        }
        active_code_by_device.erase(active);
    }

    void commit_pending() {
        if (!pending) {
            return;
        }
        current_key = std::move(pending->key);
        current_generation = pending->generation;
        pending.reset();
    }

    mutable std::mutex mutex;
    Bytes root_secret;
    std::uint64_t current_generation = 0;
    Bytes current_key;
    std::map<std::string, DeviceState> devices;
    std::map<std::string, InstallRecord> install_codes;
    std::map<std::string, std::string> active_code_by_device;
    std::optional<PendingRotation> pending;
};

TrustCenter::TrustCenter()
    : impl_(std::make_unique<Impl>(random_bytes(kNetworkKeySize))) {}

TrustCenter::TrustCenter(Bytes root_secret)
    : impl_(std::make_unique<Impl>(std::move(root_secret))) {}

TrustCenter::TrustCenter(std::string_view root_secret)
    : impl_(std::make_unique<Impl>(
          Bytes(root_secret.begin(), root_secret.end()))) {}

TrustCenter::~TrustCenter() = default;
TrustCenter::TrustCenter(TrustCenter&&) noexcept = default;
TrustCenter& TrustCenter::operator=(TrustCenter&&) noexcept = default;

RegistrationStatus TrustCenter::register_device(const std::string& device_id) {
    if (device_id.empty()) {
        return RegistrationStatus::EmptyDeviceId;
    }
    const std::lock_guard lock(impl_->mutex);
    const auto existing = impl_->devices.find(device_id);
    if (existing != impl_->devices.end()) {
        return existing->second.revoked ? RegistrationStatus::DeviceRevoked
                                        : RegistrationStatus::AlreadyRegistered;
    }
    impl_->devices.emplace(device_id, Impl::DeviceState{});
    return RegistrationStatus::Registered;
}

bool TrustCenter::revoke_device(const std::string& device_id) {
    const std::lock_guard lock(impl_->mutex);
    const auto device = impl_->devices.find(device_id);
    if (device == impl_->devices.end() || device->second.revoked) {
        return false;
    }

    device->second.revoked = true;
    device->second.joined = false;
    std::fill(device->second.secret.begin(), device->second.secret.end(), 0U);
    device->second.secret.clear();
    impl_->consume_active_code(device_id);

    if (impl_->pending) {
        impl_->pending->awaiting.erase(device_id);
        if (impl_->pending->awaiting.empty()) {
            impl_->commit_pending();
        }
    }
    return true;
}

IssueResult TrustCenter::issue_install_code(const std::string& device_id) {
    if (device_id.empty()) {
        return {IssueStatus::EmptyDeviceId, {}};
    }

    const std::lock_guard lock(impl_->mutex);
    const auto device = impl_->devices.find(device_id);
    if (device == impl_->devices.end()) {
        return {IssueStatus::UnknownDevice, {}};
    }
    if (device->second.revoked) {
        return {IssueStatus::DeviceRevoked, {}};
    }
    if (device->second.joined) {
        return {IssueStatus::DeviceAlreadyJoined, {}};
    }
    if (impl_->pending) {
        return {IssueStatus::RotationInProgress, {}};
    }

    impl_->consume_active_code(device_id);

    Bytes nonce;
    std::string nonce_key;
    do {
        nonce = random_bytes(16);
        nonce_key = hex_encode(nonce);
    } while (impl_->install_codes.contains(nonce_key));

    const std::string token = make_install_code(
        impl_->root_secret, device_id, nonce, impl_->current_generation);
    impl_->install_codes.emplace(
        nonce_key,
        Impl::InstallRecord{device_id, nonce, impl_->current_generation, false});
    impl_->active_code_by_device[device_id] = nonce_key;
    return {IssueStatus::Issued, InstallCode(token)};
}

JoinResult TrustCenter::join(const JoinRequest& request) {
    if (request.device_id.empty()) {
        return {JoinStatus::MalformedRequest, std::nullopt, {}, 0};
    }
    if (request.install_code.empty()) {
        return {JoinStatus::InvalidInstallCode, std::nullopt, {}, 0};
    }

    const std::lock_guard lock(impl_->mutex);
    const auto device = impl_->devices.find(request.device_id);
    if (device == impl_->devices.end()) {
        return {JoinStatus::UnknownDevice, std::nullopt, {}, 0};
    }
    if (device->second.revoked) {
        return {JoinStatus::DeviceRevoked, std::nullopt, {}, 0};
    }
    if (device->second.joined) {
        return {JoinStatus::DeviceAlreadyJoined, std::nullopt, {}, 0};
    }

    const auto parsed = parse_install_code(request.install_code.token);
    if (!parsed) {
        return {JoinStatus::InvalidInstallCode, std::nullopt, {}, 0};
    }

    const std::string nonce_key = hex_encode(parsed->nonce);
    const auto record = impl_->install_codes.find(nonce_key);
    if (record == impl_->install_codes.end()) {
        return {JoinStatus::InvalidInstallCode, std::nullopt, {}, 0};
    }
    if (record->second.device_id != request.device_id) {
        return {JoinStatus::InstallCodeDeviceMismatch, std::nullopt, {}, 0};
    }
    if (record->second.consumed) {
        return {JoinStatus::InstallCodeAlreadyUsed, std::nullopt, {}, 0};
    }
    if (record->second.generation != impl_->current_generation) {
        return {JoinStatus::StaleInstallCode, std::nullopt, {}, 0};
    }

    const Bytes expected_mac = install_code_mac(
        impl_->root_secret, record->second.device_id, record->second.nonce,
        record->second.generation);
    const std::span<const std::uint8_t> expected_tag(expected_mac.data(), 16);
    if (!constant_time_equal(expected_tag, parsed->tag)) {
        return {JoinStatus::InvalidInstallCode, std::nullopt, {}, 0};
    }
    if (impl_->pending) {
        return {JoinStatus::RotationInProgress, std::nullopt, {}, 0};
    }

    record->second.consumed = true;
    impl_->active_code_by_device.erase(request.device_id);
    device->second.joined = true;
    device->second.secret = random_bytes(32);

    JoinResult result;
    result.status = JoinStatus::Accepted;
    result.network_key = impl_->current_key;
    result.device_secret = hex_encode(device->second.secret);
    result.generation = impl_->current_generation;
    return result;
}

JoinResult TrustCenter::join(const std::string& device_id,
                             const InstallCode& install_code) {
    return join(JoinRequest{device_id, install_code});
}

RotationResult TrustCenter::begin_key_rotation() {
    const std::lock_guard lock(impl_->mutex);
    if (impl_->pending) {
        return {RotationStatus::AlreadyInProgress, impl_->pending->generation, {}};
    }
    if (impl_->current_generation == std::numeric_limits<std::uint64_t>::max()) {
        return {RotationStatus::GenerationExhausted, impl_->current_generation, {}};
    }

    Impl::PendingRotation rotation;
    rotation.generation = impl_->current_generation + 1;
    rotation.key = random_bytes(kNetworkKeySize);
    rotation.deliveries.reserve(impl_->devices.size());

    for (const auto& [device_id, device] : impl_->devices) {
        if (!device.joined || device.revoked || device.secret.empty()) {
            continue;
        }
        rotation.deliveries.push_back(seal_network_key(
            rotation.key, device.secret, device_id, rotation.generation));
        rotation.awaiting.insert(device_id);
    }

    if (rotation.awaiting.empty()) {
        impl_->current_key = std::move(rotation.key);
        impl_->current_generation = rotation.generation;
        return {RotationStatus::Committed, impl_->current_generation, {}};
    }

    const std::uint64_t generation = rotation.generation;
    impl_->pending = std::move(rotation);
    RotationResult result;
    result.status = RotationStatus::AwaitingConfirmation;
    result.generation = generation;
    result.deliveries = impl_->pending->deliveries;
    return result;
}

ConfirmationResult TrustCenter::confirm_key_update(
    const std::string& device_id,
    std::uint64_t generation,
    std::string_view device_secret) {
    const std::lock_guard lock(impl_->mutex);

    if (generation < impl_->current_generation) {
        return {ConfirmationStatus::GenerationRollbackBlocked,
                impl_->current_generation,
                impl_->pending ? impl_->pending->awaiting.size() : 0};
    }
    if (!impl_->pending) {
        return {ConfirmationStatus::NoPendingRotation,
                impl_->current_generation, 0};
    }
    if (generation <= impl_->current_generation) {
        return {ConfirmationStatus::GenerationRollbackBlocked,
                impl_->current_generation,
                impl_->pending->awaiting.size()};
    }
    if (generation != impl_->pending->generation) {
        return {ConfirmationStatus::GenerationMismatch,
                impl_->pending->generation,
                impl_->pending->awaiting.size()};
    }

    const auto device = impl_->devices.find(device_id);
    if (device == impl_->devices.end()) {
        return {ConfirmationStatus::UnknownDevice,
                impl_->pending->generation,
                impl_->pending->awaiting.size()};
    }
    if (device->second.revoked) {
        return {ConfirmationStatus::DeviceRevoked,
                impl_->pending->generation,
                impl_->pending->awaiting.size()};
    }
    if (!device->second.joined) {
        return {ConfirmationStatus::DeviceNotJoined,
                impl_->pending->generation,
                impl_->pending->awaiting.size()};
    }
    if (!impl_->pending->awaiting.contains(device_id)) {
        return {ConfirmationStatus::AlreadyConfirmed,
                impl_->pending->generation,
                impl_->pending->awaiting.size()};
    }

    const Bytes supplied_secret = decode_device_secret(device_secret);
    if (supplied_secret.empty()) {
        return {ConfirmationStatus::InvalidDeviceSecret,
                impl_->pending->generation,
                impl_->pending->awaiting.size()};
    }
    if (!constant_time_equal(supplied_secret, device->second.secret)) {
        return {ConfirmationStatus::AuthenticationFailed,
                impl_->pending->generation,
                impl_->pending->awaiting.size()};
    }

    impl_->pending->awaiting.erase(device_id);
    if (impl_->pending->awaiting.empty()) {
        const std::uint64_t committed_generation =
            impl_->pending->generation;
        impl_->commit_pending();
        return {ConfirmationStatus::Committed, committed_generation, 0};
    }
    return {ConfirmationStatus::AwaitingConfirmation,
            impl_->pending->generation,
            impl_->pending->awaiting.size()};
}

bool TrustCenter::abort_key_rotation() {
    const std::lock_guard lock(impl_->mutex);
    if (!impl_->pending) {
        return false;
    }
    impl_->pending.reset();
    return true;
}

bool TrustCenter::rollback_generation(std::uint64_t target_generation) noexcept {
    (void)target_generation;
    return false;
}

std::uint64_t TrustCenter::current_generation() const {
    const std::lock_guard lock(impl_->mutex);
    return impl_->current_generation;
}

std::optional<std::uint64_t> TrustCenter::pending_generation() const {
    const std::lock_guard lock(impl_->mutex);
    if (!impl_->pending) {
        return std::nullopt;
    }
    return impl_->pending->generation;
}

std::size_t TrustCenter::pending_confirmation_count() const {
    const std::lock_guard lock(impl_->mutex);
    return impl_->pending ? impl_->pending->awaiting.size() : 0;
}

bool TrustCenter::is_registered(const std::string& device_id) const {
    const std::lock_guard lock(impl_->mutex);
    return impl_->devices.contains(device_id);
}

bool TrustCenter::is_joined(const std::string& device_id) const {
    const std::lock_guard lock(impl_->mutex);
    const auto device = impl_->devices.find(device_id);
    return device != impl_->devices.end() && device->second.joined &&
           !device->second.revoked;
}

bool TrustCenter::is_revoked(const std::string& device_id) const {
    const std::lock_guard lock(impl_->mutex);
    const auto device = impl_->devices.find(device_id);
    return device != impl_->devices.end() && device->second.revoked;
}

std::size_t TrustCenter::joined_device_count() const {
    const std::lock_guard lock(impl_->mutex);
    std::size_t count = 0;
    for (const auto& [device_id, device] : impl_->devices) {
        (void)device_id;
        if (device.joined && !device.revoked) {
            ++count;
        }
    }
    return count;
}

std::string_view to_string(RegistrationStatus status) noexcept {
    switch (status) {
        case RegistrationStatus::Registered:
            return "Registered";
        case RegistrationStatus::AlreadyRegistered:
            return "AlreadyRegistered";
        case RegistrationStatus::DeviceRevoked:
            return "DeviceRevoked";
        case RegistrationStatus::EmptyDeviceId:
            return "EmptyDeviceId";
    }
    return "Unknown";
}

std::string_view to_string(IssueStatus status) noexcept {
    switch (status) {
        case IssueStatus::Issued:
            return "Issued";
        case IssueStatus::EmptyDeviceId:
            return "EmptyDeviceId";
        case IssueStatus::UnknownDevice:
            return "UnknownDevice";
        case IssueStatus::DeviceRevoked:
            return "DeviceRevoked";
        case IssueStatus::DeviceAlreadyJoined:
            return "DeviceAlreadyJoined";
        case IssueStatus::RotationInProgress:
            return "RotationInProgress";
    }
    return "Unknown";
}

std::string_view to_string(JoinStatus status) noexcept {
    switch (status) {
        case JoinStatus::Accepted:
            return "Accepted";
        case JoinStatus::MalformedRequest:
            return "MalformedRequest";
        case JoinStatus::UnknownDevice:
            return "UnknownDevice";
        case JoinStatus::DeviceRevoked:
            return "DeviceRevoked";
        case JoinStatus::DeviceAlreadyJoined:
            return "DeviceAlreadyJoined";
        case JoinStatus::InvalidInstallCode:
            return "InvalidInstallCode";
        case JoinStatus::InstallCodeDeviceMismatch:
            return "InstallCodeDeviceMismatch";
        case JoinStatus::InstallCodeAlreadyUsed:
            return "InstallCodeAlreadyUsed";
        case JoinStatus::StaleInstallCode:
            return "StaleInstallCode";
        case JoinStatus::RotationInProgress:
            return "RotationInProgress";
    }
    return "Unknown";
}

std::string_view to_string(RotationStatus status) noexcept {
    switch (status) {
        case RotationStatus::AwaitingConfirmation:
            return "AwaitingConfirmation";
        case RotationStatus::Committed:
            return "Committed";
        case RotationStatus::AlreadyInProgress:
            return "AlreadyInProgress";
        case RotationStatus::GenerationExhausted:
            return "GenerationExhausted";
    }
    return "Unknown";
}

std::string_view to_string(ConfirmationStatus status) noexcept {
    switch (status) {
        case ConfirmationStatus::AwaitingConfirmation:
            return "AwaitingConfirmation";
        case ConfirmationStatus::Committed:
            return "Committed";
        case ConfirmationStatus::NoPendingRotation:
            return "NoPendingRotation";
        case ConfirmationStatus::UnknownDevice:
            return "UnknownDevice";
        case ConfirmationStatus::DeviceNotJoined:
            return "DeviceNotJoined";
        case ConfirmationStatus::DeviceRevoked:
            return "DeviceRevoked";
        case ConfirmationStatus::GenerationRollbackBlocked:
            return "GenerationRollbackBlocked";
        case ConfirmationStatus::GenerationMismatch:
            return "GenerationMismatch";
        case ConfirmationStatus::AlreadyConfirmed:
            return "AlreadyConfirmed";
        case ConfirmationStatus::AuthenticationFailed:
            return "AuthenticationFailed";
        case ConfirmationStatus::InvalidDeviceSecret:
            return "InvalidDeviceSecret";
    }
    return "Unknown";
}

}  // namespace netsec
