#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <bit>
#include <cmath>
#include <cstring>
#include <limits>
#include <map>
#include <mutex>
#include <set>
#include <stdexcept>
#include <unordered_map>
#include <unordered_set>
#include <utility>

namespace netsec {
namespace {

constexpr std::array<std::uint32_t, 64> kSha256RoundConstants = {
    0x428a2f98U, 0x71374491U, 0xb5c0fbcfU, 0xe9b5dba5U,
    0x3956c25bU, 0x59f111f1U, 0x923f82a4U, 0xab1c5ed5U,
    0xd807aa98U, 0x12835b01U, 0x243185beU, 0x550c7dc3U,
    0x72be5d74U, 0x80deb1feU, 0x9bdc06a7U, 0xc19bf174U,
    0xe49b69c1U, 0xefbe4786U, 0x0fc19dc6U, 0x240ca1ccU,
    0x2de92c6fU, 0x4a7484aaU, 0x5cb0a9dcU, 0x76f988daU,
    0x983e5152U, 0xa831c66dU, 0xb00327c8U, 0xbf597fc7U,
    0xc6e00bf3U, 0xd5a79147U, 0x06ca6351U, 0x14292967U,
    0x27b70a85U, 0x2e1b2138U, 0x4d2c6dfcU, 0x53380d13U,
    0x650a7354U, 0x766a0abbU, 0x81c2c92eU, 0x92722c85U,
    0xa2bfe8a1U, 0xa81a664bU, 0xc24b8b70U, 0xc76c51a3U,
    0xd192e819U, 0xd6990624U, 0xf40e3585U, 0x106aa070U,
    0x19a4c116U, 0x1e376c08U, 0x2748774cU, 0x34b0bcb5U,
    0x391c0cb3U, 0x4ed8aa4aU, 0x5b9cca4fU, 0x682e6ff3U,
    0x748f82eeU, 0x78a5636fU, 0x84c87814U, 0x8cc70208U,
    0x90befffaU, 0xa4506cebU, 0xbef9a3f7U, 0xc67178f2U
};

constexpr std::uint32_t rotr(std::uint32_t value, unsigned count) noexcept {
    return (value >> count) | (value << (32U - count));
}

class Sha256 {
public:
    void update(const std::uint8_t* data, std::size_t size) noexcept {
        total_bytes_ += size;
        while (size != 0) {
            const std::size_t space = buffer_.size() - buffer_size_;
            const std::size_t take = std::min(space, size);
            std::memcpy(buffer_.data() + buffer_size_, data, take);
            buffer_size_ += take;
            data += take;
            size -= take;
            if (buffer_size_ == buffer_.size()) {
                transform(buffer_.data());
                buffer_size_ = 0;
            }
        }
    }

    std::array<std::uint8_t, 32> finish() noexcept {
        const std::uint64_t bit_length = total_bytes_ * 8U;
        buffer_[buffer_size_++] = 0x80U;
        if (buffer_size_ > 56U) {
            std::fill(buffer_.begin() + static_cast<std::ptrdiff_t>(buffer_size_),
                      buffer_.end(), 0U);
            transform(buffer_.data());
            buffer_size_ = 0;
        }
        std::fill(buffer_.begin() + static_cast<std::ptrdiff_t>(buffer_size_),
                  buffer_.begin() + 56, 0U);
        for (unsigned i = 0; i < 8; ++i) {
            buffer_[56U + i] =
                static_cast<std::uint8_t>(bit_length >> (56U - 8U * i));
        }
        transform(buffer_.data());

        std::array<std::uint8_t, 32> result{};
        for (std::size_t i = 0; i < state_.size(); ++i) {
            result[i * 4U] = static_cast<std::uint8_t>(state_[i] >> 24U);
            result[i * 4U + 1U] = static_cast<std::uint8_t>(state_[i] >> 16U);
            result[i * 4U + 2U] = static_cast<std::uint8_t>(state_[i] >> 8U);
            result[i * 4U + 3U] = static_cast<std::uint8_t>(state_[i]);
        }
        return result;
    }

private:
    void transform(const std::uint8_t* block) noexcept {
        std::array<std::uint32_t, 64> words{};
        for (std::size_t i = 0; i < 16; ++i) {
            words[i] = (static_cast<std::uint32_t>(block[i * 4U]) << 24U) |
                       (static_cast<std::uint32_t>(block[i * 4U + 1U]) << 16U) |
                       (static_cast<std::uint32_t>(block[i * 4U + 2U]) << 8U) |
                       static_cast<std::uint32_t>(block[i * 4U + 3U]);
        }
        for (std::size_t i = 16; i < 64; ++i) {
            const std::uint32_t s0 = rotr(words[i - 15], 7) ^
                                     rotr(words[i - 15], 18) ^
                                     (words[i - 15] >> 3);
            const std::uint32_t s1 = rotr(words[i - 2], 17) ^
                                     rotr(words[i - 2], 19) ^
                                     (words[i - 2] >> 10);
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
            const std::uint32_t s1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25);
            const std::uint32_t choice = (e & f) ^ (~e & g);
            const std::uint32_t temp1 = h + s1 + choice +
                                        kSha256RoundConstants[i] + words[i];
            const std::uint32_t s0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22);
            const std::uint32_t majority = (a & b) ^ (a & c) ^ (b & c);
            const std::uint32_t temp2 = s0 + majority;

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
        0x510e527fU, 0x9b05688cU, 0x1f83d9abU, 0x5be0cd19U
    };
    std::array<std::uint8_t, 64> buffer_{};
    std::size_t buffer_size_{};
    std::uint64_t total_bytes_{};
};

std::array<std::uint8_t, 32> hmac_sha256(
    std::span<const std::uint8_t> key,
    std::span<const std::uint8_t> message) noexcept {
    std::array<std::uint8_t, 64> key_block{};
    if (key.size() > key_block.size()) {
        Sha256 hash;
        hash.update(key.data(), key.size());
        const auto digest = hash.finish();
        std::copy(digest.begin(), digest.end(), key_block.begin());
    } else {
        std::copy(key.begin(), key.end(), key_block.begin());
    }

    std::array<std::uint8_t, 64> inner_pad{};
    std::array<std::uint8_t, 64> outer_pad{};
    for (std::size_t i = 0; i < key_block.size(); ++i) {
        inner_pad[i] = static_cast<std::uint8_t>(key_block[i] ^ 0x36U);
        outer_pad[i] = static_cast<std::uint8_t>(key_block[i] ^ 0x5cU);
    }

    Sha256 inner;
    inner.update(inner_pad.data(), inner_pad.size());
    inner.update(message.data(), message.size());
    const auto inner_digest = inner.finish();

    Sha256 outer;
    outer.update(outer_pad.data(), outer_pad.size());
    outer.update(inner_digest.data(), inner_digest.size());
    return outer.finish();
}

class CanonicalMessage {
public:
    void text(std::string_view value) noexcept {
        bytes(reinterpret_cast<const std::uint8_t*>(value.data()), value.size());
    }

    void u8(std::uint8_t value) noexcept {
        if (size_ < data_.size()) {
            data_[size_++] = value;
        }
    }

    void u16(std::uint16_t value) noexcept {
        u8(static_cast<std::uint8_t>(value >> 8U));
        u8(static_cast<std::uint8_t>(value));
    }

    void u32(std::uint32_t value) noexcept {
        u8(static_cast<std::uint8_t>(value >> 24U));
        u8(static_cast<std::uint8_t>(value >> 16U));
        u8(static_cast<std::uint8_t>(value >> 8U));
        u8(static_cast<std::uint8_t>(value));
    }

    void u64(std::uint64_t value) noexcept {
        u32(static_cast<std::uint32_t>(value >> 32U));
        u32(static_cast<std::uint32_t>(value));
    }

    void bytes(const std::uint8_t* data, std::size_t size) noexcept {
        const std::size_t copy_size = std::min(size, data_.size() - size_);
        std::copy(data, data + copy_size, data_.begin() +
                  static_cast<std::ptrdiff_t>(size_));
        size_ += copy_size;
    }

    std::span<const std::uint8_t> span() const noexcept {
        return {data_.data(), size_};
    }

private:
    std::array<std::uint8_t, 160> data_{};
    std::size_t size_{};
};

bool valid_layout(const SignalSpec& signal, std::uint8_t payload_size) noexcept {
    const std::size_t payload_bits =
        static_cast<std::size_t>(payload_size) * 8U;
    if (signal.bit_length == 0U) {
        return false;
    }
    if (signal.byte_order == ByteOrder::Intel) {
        return static_cast<std::size_t>(signal.start_bit) + signal.bit_length <=
               payload_bits;
    }

    int bit = signal.start_bit;
    for (unsigned i = 0; i < signal.bit_length; ++i) {
        if (bit < 0 || static_cast<std::size_t>(bit) >= payload_bits) {
            return false;
        }
        bit = (bit % 8 == 0) ? bit + 15 : bit - 1;
    }
    return true;
}

struct DecodedSignal {
    std::uint64_t raw{};
    double physical{};
};

std::optional<DecodedSignal> decode_signal(
    const SignalSpec& signal, const Payload& payload,
    std::uint8_t payload_size) noexcept {
    if (!valid_layout(signal, payload_size)) {
        return std::nullopt;
    }

    std::uint64_t raw = 0;
    if (signal.byte_order == ByteOrder::Intel) {
        for (unsigned i = 0; i < signal.bit_length; ++i) {
            const unsigned position =
                static_cast<unsigned>(signal.start_bit) + i;
            const auto bit =
                static_cast<std::uint64_t>(
                    (payload[position / 8U] >> (position % 8U)) & 1U);
            raw |= bit << i;
        }
    } else {
        int position = signal.start_bit;
        for (unsigned i = 0; i < signal.bit_length; ++i) {
            const auto bit =
                static_cast<std::uint64_t>(
                    (payload[static_cast<std::size_t>(position) / 8U] >>
                     (static_cast<unsigned>(position) % 8U)) & 1U);
            raw = (raw << 1U) | bit;
            position = (position % 8 == 0) ? position + 15 : position - 1;
        }
    }

    if (signal.is_signed && signal.bit_length < 64U &&
        (raw & (std::uint64_t{1} << (signal.bit_length - 1U))) != 0U) {
        const std::uint64_t mask =
            (std::uint64_t{1} << signal.bit_length) - 1U;
        raw |= ~mask;
    }

    const std::int64_t signed_raw =
        signal.bit_length == 64U
            ? std::bit_cast<std::int64_t>(raw)
            : static_cast<std::int64_t>(raw);
    const auto numeric = signal.is_signed
        ? static_cast<double>(signed_raw)
        : static_cast<double>(raw);
    const double physical = numeric * signal.scale + signal.offset;
    if (!std::isfinite(physical)) {
        return std::nullopt;
    }
    return DecodedSignal{raw, physical};
}

struct RuntimeFrame {
    std::size_t spec_index{};
    bool seen{};
    std::int64_t last_timestamp_us{};
    std::uint64_t last_sequence{};
};

struct ConflictSource {
    std::uint32_t identifier{};
    std::uint16_t sender_id{};
    std::int64_t last_timestamp_us{};
};

struct TrustedEntry {
    TrustedSignal signal;
    std::int64_t freshness_window_us{};
};

struct SessionPolicyKey {
    SessionType type{};
    std::uint16_t principal_id{};

    bool operator<(const SessionPolicyKey& other) const noexcept {
        if (type != other.type) {
            return static_cast<std::uint8_t>(type) <
                   static_cast<std::uint8_t>(other.type);
        }
        return principal_id < other.principal_id;
    }
};

std::int64_t saturating_add(std::int64_t left, std::int64_t right) noexcept {
    if (right > 0 &&
        left > std::numeric_limits<std::int64_t>::max() - right) {
        return std::numeric_limits<std::int64_t>::max();
    }
    if (right < 0 &&
        left < std::numeric_limits<std::int64_t>::min() - right) {
        return std::numeric_limits<std::int64_t>::min();
    }
    return left + right;
}

[[noreturn]] void invalid_config(const char* message) {
    throw std::invalid_argument(std::string("netsec configuration: ") + message);
}

std::optional<SessionType> required_session_type(ServiceClass service) {
    switch (service) {
        case ServiceClass::Diagnostic:
            return SessionType::Diagnostic;
        case ServiceClass::Flashing:
            return SessionType::Flashing;
    }
    return std::nullopt;
}

bool elapsed_within(
    std::int64_t now_us, std::int64_t then_us, std::int64_t window_us) {
    if (now_us < then_us) {
        return true;
    }
    const auto delta =
        static_cast<std::uint64_t>(now_us) -
        static_cast<std::uint64_t>(then_us);
    return delta <= static_cast<std::uint64_t>(window_us);
}

}  // namespace

AuthenticationKey make_authentication_key(
    std::uint32_t id, std::span<const std::uint8_t> material) {
    if (material.empty() || material.size() > 32U) {
        throw std::invalid_argument(
            "netsec authentication key must contain 1 to 32 bytes");
    }
    AuthenticationKey key;
    key.id = id;
    key.size = static_cast<std::uint8_t>(material.size());
    std::copy(material.begin(), material.end(), key.material.begin());
    return key;
}

std::string_view to_string(DecisionReason reason) noexcept {
    switch (reason) {
        case DecisionReason::None: return "none";
        case DecisionReason::UnknownIdentifier: return "unknown_identifier";
        case DecisionReason::SenderNotAllowed: return "sender_not_allowed";
        case DecisionReason::PayloadSizeMismatch: return "payload_size_mismatch";
        case DecisionReason::InvalidSignalLayout: return "invalid_signal_layout";
        case DecisionReason::MissingAuthentication: return "missing_authentication";
        case DecisionReason::AuthenticationFailed: return "authentication_failed";
        case DecisionReason::SequenceReplay: return "sequence_replay";
        case DecisionReason::TimestampRegression: return "timestamp_regression";
        case DecisionReason::PeriodTooShort: return "period_too_short";
        case DecisionReason::PeriodTooLong: return "period_too_long";
        case DecisionReason::SignalOutOfRange: return "signal_out_of_range";
        case DecisionReason::SignalDecodeError: return "signal_decode_error";
        case DecisionReason::ConflictingFrame: return "conflicting_frame";
        case DecisionReason::SessionNotProvisioned: return "session_not_provisioned";
        case DecisionReason::SessionAuthenticationFailed:
            return "session_authentication_failed";
        case DecisionReason::SessionReplay: return "session_replay";
        case DecisionReason::SessionUnknown: return "session_unknown";
        case DecisionReason::SessionExpired: return "session_expired";
        case DecisionReason::SessionTypeMismatch: return "session_type_mismatch";
    }
    return "unknown_reason";
}

AuthenticationTag compute_frame_authentication_tag(
    const AuthenticationKey& key, const BusFrame& frame) noexcept {
    CanonicalMessage message;
    message.text("netsec/can-frame/v1");
    message.u32(key.id);
    message.u32(frame.identifier);
    message.u16(frame.sender_id);
    message.u64(frame.sequence);
    message.u64(static_cast<std::uint64_t>(frame.timestamp_us));
    message.u8(frame.size);
    message.bytes(frame.data.data(),
                  std::min<std::size_t>(frame.size, frame.data.size()));

    const auto full_tag = hmac_sha256(
        {key.material.data(), key.size}, message.span());
    AuthenticationTag tag{};
    std::copy_n(full_tag.begin(), tag.size(), tag.begin());
    return tag;
}

AuthenticationTag compute_session_authentication_tag(
    const AuthenticationKey& key, const SessionRequest& request) noexcept {
    CanonicalMessage message;
    message.text("netsec/session/v1");
    message.u8(static_cast<std::uint8_t>(request.type));
    message.u32(key.id);
    message.u16(request.principal_id);
    message.u64(request.nonce);
    message.u64(static_cast<std::uint64_t>(request.timestamp_us));

    const auto full_tag = hmac_sha256(
        {key.material.data(), key.size}, message.span());
    AuthenticationTag tag{};
    std::copy_n(full_tag.begin(), tag.size(), tag.begin());
    return tag;
}

bool constant_time_equal(
    const AuthenticationTag& lhs, const AuthenticationTag& rhs) noexcept {
    std::uint8_t difference = 0;
    for (std::size_t i = 0; i < lhs.size(); ++i) {
        difference = static_cast<std::uint8_t>(
            difference | static_cast<std::uint8_t>(lhs[i] ^ rhs[i]));
    }
    return difference == 0;
}

struct SignalAuthorizer::Impl {
    explicit Impl(AuthorizationConfig config) : config_(std::move(config)) {
        validate();
        runtimes_.resize(config_.frames.size());
        for (std::size_t i = 0; i < config_.frames.size(); ++i) {
            runtimes_[i].spec_index = i;
            frame_index_.emplace(config_.frames[i].identifier, i);
        }
    }

    void validate() {
        std::unordered_set<std::uint32_t> key_ids;
        for (const auto& key : config_.keys) {
            if (key.size == 0U || key.size > key.material.size()) {
                invalid_config("authentication key size must be 1..32 bytes");
            }
            if (!key_ids.insert(key.id).second) {
                invalid_config("duplicate authentication key id");
            }
            keys_.emplace(key.id, key);
        }

        std::unordered_set<std::uint32_t> identifiers;
        std::unordered_set<std::string> signal_names;
        for (const auto& frame : config_.frames) {
            if (frame.identifier > kMaxCanIdentifier) {
                invalid_config("CAN identifier exceeds 29 bits");
            }
            if (!identifiers.insert(frame.identifier).second) {
                invalid_config("duplicate frame identifier");
            }
            if (frame.expected_period_us <= 0) {
                invalid_config("expected period must be positive");
            }
            if (frame.period_tolerance_us < 0) {
                invalid_config("period tolerance must not be negative");
            }
            if (frame.period_tolerance_us >
                std::numeric_limits<std::int64_t>::max() -
                    frame.expected_period_us) {
                invalid_config("period tolerance overflows");
            }
            if (frame.payload_size == 0U ||
                frame.payload_size > kMaxPayloadBytes) {
                invalid_config("payload size must be 1..64 bytes");
            }
            if (frame.critical &&
                keys_.find(frame.authentication_key_id) == keys_.end()) {
                invalid_config(
                    "critical frame references an unknown authentication key");
            }
            if (frame.conflict_window_us < 0) {
                invalid_config("conflict window must not be negative");
            }
            for (const auto& signal : frame.signals) {
                if (signal.name.empty()) {
                    invalid_config("signal name must not be empty");
                }
                if (!signal_names.insert(signal.name).second) {
                    invalid_config("signal names must be unique");
                }
                if (signal.bit_length == 0U || signal.bit_length > 64U) {
                    invalid_config("signal bit length must be 1..64");
                }
                if (signal.byte_order != ByteOrder::Intel &&
                    signal.byte_order != ByteOrder::Motorola) {
                    invalid_config("signal byte order is invalid");
                }
                if (!valid_layout(signal, frame.payload_size)) {
                    invalid_config("signal layout exceeds the payload");
                }
                if (!std::isfinite(signal.scale) || signal.scale == 0.0 ||
                    !std::isfinite(signal.offset)) {
                    invalid_config("signal scale/offset must be finite");
                }
                if (!std::isfinite(signal.minimum) ||
                    !std::isfinite(signal.maximum) ||
                    signal.minimum > signal.maximum) {
                    invalid_config("signal range must be finite and ordered");
                }
            }
        }

        std::set<SessionPolicyKey> session_keys;
        for (const auto& session : config_.sessions) {
            if (session.type != SessionType::Diagnostic &&
                session.type != SessionType::Flashing) {
                invalid_config("session type is invalid");
            }
            if (session.lifetime_us <= 0) {
                invalid_config("session lifetime must be positive");
            }
            if (keys_.find(session.authentication_key_id) == keys_.end()) {
                invalid_config("session references an unknown authentication key");
            }
            if (!session_keys.insert(
                    SessionPolicyKey{session.type, session.principal_id}).second) {
                invalid_config("duplicate session policy");
            }
            session_policies_.emplace(
                SessionPolicyKey{session.type, session.principal_id}, session);
        }
    }

    const FrameSpec* frame_spec(std::size_t index) const noexcept {
        return &config_.frames[index];
    }

    std::int64_t conflict_window(const FrameSpec& spec) const noexcept {
        if (spec.conflict_window_us > 0) {
            return spec.conflict_window_us;
        }
        const std::int64_t maximum =
            std::numeric_limits<std::int64_t>::max();
        if (spec.expected_period_us >
            (maximum - spec.period_tolerance_us) / 2) {
            return maximum;
        }
        return spec.expected_period_us * 2 + spec.period_tolerance_us;
    }

    std::int64_t freshness_window(const FrameSpec& spec) const noexcept {
        const std::int64_t maximum =
            std::numeric_limits<std::int64_t>::max();
        if (spec.expected_period_us >
            (maximum - spec.period_tolerance_us) / 2) {
            return maximum;
        }
        return spec.expected_period_us * 2 + spec.period_tolerance_us;
    }

    AuthorizationDecision reject(DecisionReason reason) {
        degraded_ = true;
        AuthorizationDecision decision;
        decision.degraded = true;
        decision.reason = reason;
        return decision;
    }

    AuthorizationDecision authorize(const BusFrame& frame) {
        std::lock_guard lock(mutex_);

        const auto frame_it = frame_index_.find(frame.identifier);
        if (frame_it == frame_index_.end()) {
            return reject(DecisionReason::UnknownIdentifier);
        }

        const std::size_t index = frame_it->second;
        auto& runtime = runtimes_[index];
        const FrameSpec& spec = config_.frames[index];

        if (frame.size != spec.payload_size ||
            frame.size > frame.data.size()) {
            return reject(DecisionReason::PayloadSizeMismatch);
        }
        if (frame.sender_id != spec.sender_id) {
            return reject(DecisionReason::SenderNotAllowed);
        }

        if (spec.critical) {
            if (!frame.has_authentication) {
                return reject(DecisionReason::MissingAuthentication);
            }
            if (frame.authentication_key_id !=
                spec.authentication_key_id) {
                return reject(DecisionReason::AuthenticationFailed);
            }
            const auto key_it = keys_.find(spec.authentication_key_id);
            if (key_it == keys_.end()) {
                return reject(DecisionReason::AuthenticationFailed);
            }
            const auto expected =
                compute_frame_authentication_tag(key_it->second, frame);
            if (!constant_time_equal(
                    expected, frame.authentication_tag)) {
                return reject(DecisionReason::AuthenticationFailed);
            }
        }

        if (runtime.seen) {
            if (frame.timestamp_us < runtime.last_timestamp_us) {
                return reject(DecisionReason::TimestampRegression);
            }
            if (frame.sequence <= runtime.last_sequence) {
                return reject(DecisionReason::SequenceReplay);
            }

            const auto delta =
                static_cast<std::uint64_t>(frame.timestamp_us) -
                static_cast<std::uint64_t>(runtime.last_timestamp_us);
            if (delta >
                static_cast<std::uint64_t>(
                    std::numeric_limits<std::int64_t>::max())) {
                return reject(DecisionReason::PeriodTooLong);
            }
            const auto elapsed = static_cast<std::int64_t>(delta);
            const std::int64_t minimum_elapsed =
                spec.period_tolerance_us >= spec.expected_period_us
                    ? 0
                    : spec.expected_period_us - spec.period_tolerance_us;
            const std::int64_t maximum_elapsed =
                spec.expected_period_us + spec.period_tolerance_us;
            if (elapsed < minimum_elapsed) {
                return reject(DecisionReason::PeriodTooShort);
            }
            if (elapsed > maximum_elapsed) {
                return reject(DecisionReason::PeriodTooLong);
            }
        }

        const auto conflict_window = this->conflict_window(spec);
        std::unordered_set<std::string> frame_groups;
        for (const auto& signal : spec.signals) {
            if (signal.conflict_group.empty() ||
                !frame_groups.insert(signal.conflict_group).second) {
                continue;
            }
            const auto group_it =
                conflict_sources_.find(signal.conflict_group);
            if (group_it != conflict_sources_.end()) {
                const auto& source = group_it->second;
                const bool same_source =
                    source.identifier == frame.identifier &&
                    source.sender_id == frame.sender_id;
                const bool active =
                    elapsed_within(frame.timestamp_us,
                                   source.last_timestamp_us,
                                   conflict_window);
                if (!same_source && active) {
                    return reject(DecisionReason::ConflictingFrame);
                }
            }
        }

        std::vector<AuthorizedSignal> authorized;
        authorized.reserve(spec.signals.size());
        for (const auto& signal : spec.signals) {
            const auto decoded =
                decode_signal(signal, frame.data, frame.size);
            if (!decoded.has_value()) {
                return reject(DecisionReason::SignalDecodeError);
            }
            if (decoded->physical < signal.minimum ||
                decoded->physical > signal.maximum) {
                return reject(DecisionReason::SignalOutOfRange);
            }
            authorized.push_back(AuthorizedSignal{
                signal.name,
                signal.conflict_group,
                decoded->raw,
                decoded->physical,
                spec.critical
            });
        }

        runtime.seen = true;
        runtime.last_timestamp_us = frame.timestamp_us;
        runtime.last_sequence = frame.sequence;

        const auto freshness = freshness_window(spec);
        for (const auto& signal : authorized) {
            trust_[signal.name] = TrustedEntry{
                TrustedSignal{
                    signal.name,
                    signal.value,
                    signal.raw_value,
                    frame.timestamp_us,
                    frame.identifier,
                    frame.sender_id,
                    spec.critical,
                    true
                },
                freshness
            };
        }
        for (const auto& signal : spec.signals) {
            if (!signal.conflict_group.empty()) {
                conflict_sources_[signal.conflict_group] = ConflictSource{
                    frame.identifier, frame.sender_id, frame.timestamp_us};
            }
        }

        AuthorizationDecision decision;
        decision.authorized = true;
        decision.degraded = degraded_;
        decision.reason = DecisionReason::None;
        decision.signals = std::move(authorized);
        return decision;
    }

    std::optional<TrustedSignal> trusted_signal(
        std::string_view name, std::int64_t now_us) const {
        std::lock_guard lock(mutex_);
        const auto it = trust_.find(std::string(name));
        if (it == trust_.end()) {
            return std::nullopt;
        }
        TrustedSignal result = it->second.signal;
        result.fresh =
            now_us >= result.timestamp_us &&
            elapsed_within(now_us, result.timestamp_us,
                           it->second.freshness_window_us);
        return result;
    }

    std::vector<TrustedSignal> trusted_signals(std::int64_t now_us) const {
        std::lock_guard lock(mutex_);
        std::vector<TrustedSignal> result;
        result.reserve(trust_.size());
        for (const auto& [name, entry] : trust_) {
            (void)name;
            TrustedSignal signal = entry.signal;
            signal.fresh =
                now_us >= signal.timestamp_us &&
                elapsed_within(now_us, signal.timestamp_us,
                               entry.freshness_window_us);
            result.push_back(std::move(signal));
        }
        std::sort(result.begin(), result.end(),
                  [](const TrustedSignal& lhs, const TrustedSignal& rhs) {
                      return lhs.name < rhs.name;
                  });
        return result;
    }

    SessionDecision authorize_session(const SessionRequest& request) {
        std::lock_guard lock(mutex_);
        const SessionPolicyKey policy_key{request.type, request.principal_id};
        const auto policy_it = session_policies_.find(policy_key);
        if (policy_it == session_policies_.end()) {
            return SessionDecision{
                false, DecisionReason::SessionNotProvisioned, {}};
        }
        const SessionSpec& policy = policy_it->second;
        if (request.authentication_key_id !=
            policy.authentication_key_id) {
            return SessionDecision{
                false, DecisionReason::SessionAuthenticationFailed, {}};
        }
        const auto key_it = keys_.find(request.authentication_key_id);
        if (key_it == keys_.end()) {
            return SessionDecision{
                false, DecisionReason::SessionAuthenticationFailed, {}};
        }
        const auto expected =
            compute_session_authentication_tag(key_it->second, request);
        if (!constant_time_equal(expected, request.authentication_tag)) {
            return SessionDecision{
                false, DecisionReason::SessionAuthenticationFailed, {}};
        }

        const auto nonce_it = session_nonces_.find(policy_key);
        if (nonce_it != session_nonces_.end() &&
            request.nonce <= nonce_it->second) {
            return SessionDecision{
                false, DecisionReason::SessionReplay, {}};
        }

        const std::int64_t expires_at =
            saturating_add(request.timestamp_us, policy.lifetime_us);
        if (next_grant_id_ == 0U) {
            next_grant_id_ = 1U;
        }
        const SessionGrant grant{
            next_grant_id_++,
            request.type,
            request.principal_id,
            request.timestamp_us,
            expires_at
        };
        grants_.emplace(grant.grant_id, grant);
        session_nonces_[policy_key] = request.nonce;
        return SessionDecision{true, DecisionReason::None, grant};
    }

    ServiceDecision authorize_service(
        const SessionGrant& grant, ServiceClass service,
        std::int64_t now_us) const {
        std::lock_guard lock(mutex_);
        const auto grant_it = grants_.find(grant.grant_id);
        if (grant_it == grants_.end()) {
            return ServiceDecision{false, DecisionReason::SessionUnknown};
        }
        const auto& stored = grant_it->second;
        if (stored.type != grant.type ||
            stored.principal_id != grant.principal_id ||
            stored.issued_at_us != grant.issued_at_us ||
            stored.expires_at_us != grant.expires_at_us) {
            return ServiceDecision{false, DecisionReason::SessionUnknown};
        }
        if (now_us >= stored.expires_at_us) {
            return ServiceDecision{false, DecisionReason::SessionExpired};
        }
        const auto expected_type = required_session_type(service);
        if (!expected_type.has_value() ||
            stored.type != *expected_type) {
            return ServiceDecision{false, DecisionReason::SessionTypeMismatch};
        }
        return ServiceDecision{true, DecisionReason::None};
    }

    bool end_session(std::uint64_t grant_id) {
        std::lock_guard lock(mutex_);
        return grants_.erase(grant_id) != 0U;
    }

    AuthorizationConfig config_;
    std::unordered_map<std::uint32_t, std::size_t> frame_index_;
    std::unordered_map<std::uint32_t, AuthenticationKey> keys_;
    std::map<SessionPolicyKey, SessionSpec> session_policies_;
    std::vector<RuntimeFrame> runtimes_;
    std::unordered_map<std::string, ConflictSource> conflict_sources_;
    std::unordered_map<std::string, TrustedEntry> trust_;
    std::map<SessionPolicyKey, std::uint64_t> session_nonces_;
    std::unordered_map<std::uint64_t, SessionGrant> grants_;
    std::uint64_t next_grant_id_{1};
    bool degraded_{false};
    mutable std::mutex mutex_;
};

SignalAuthorizer::SignalAuthorizer(AuthorizationConfig config)
    : impl_(std::make_unique<Impl>(std::move(config))) {}

SignalAuthorizer::~SignalAuthorizer() = default;

SignalAuthorizer::SignalAuthorizer(SignalAuthorizer&&) noexcept = default;

SignalAuthorizer& SignalAuthorizer::operator=(
    SignalAuthorizer&&) noexcept = default;

AuthorizationDecision SignalAuthorizer::authorize(const BusFrame& frame) {
    return impl_->authorize(frame);
}

std::optional<TrustedSignal> SignalAuthorizer::trusted_signal(
    std::string_view name, std::int64_t now_us) const {
    return impl_->trusted_signal(name, now_us);
}

std::vector<TrustedSignal> SignalAuthorizer::trusted_signals(
    std::int64_t now_us) const {
    return impl_->trusted_signals(now_us);
}

bool SignalAuthorizer::degraded() const noexcept {
    std::lock_guard lock(impl_->mutex_);
    return impl_->degraded_;
}

void SignalAuthorizer::clear_degradation() noexcept {
    std::lock_guard lock(impl_->mutex_);
    impl_->degraded_ = false;
}

SessionDecision SignalAuthorizer::authorize_session(
    const SessionRequest& request) {
    return impl_->authorize_session(request);
}

ServiceDecision SignalAuthorizer::authorize_service(
    const SessionGrant& grant, ServiceClass service,
    std::int64_t now_us) const {
    return impl_->authorize_service(grant, service, now_us);
}

bool SignalAuthorizer::end_session(std::uint64_t grant_id) noexcept {
    return impl_->end_session(grant_id);
}

}  // namespace netsec
