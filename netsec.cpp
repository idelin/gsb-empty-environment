#include "netsec.hpp"

#include <algorithm>
#include <cstring>
#include <iterator>
#include <limits>
#include <map>
#include <stdexcept>
#include <utility>

namespace netsec {
namespace detail {

void secure_zero(void* data, std::size_t size) noexcept;

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

constexpr std::uint32_t rotr(std::uint32_t value, unsigned int count) noexcept {
    return (value >> count) | (value << (32U - count));
}

class Sha256 {
public:
    void update(ByteSpan data) noexcept {
        total_bytes_ += data.size();
        while (!data.empty()) {
            const std::size_t take =
                std::min(buffer_.size() - buffered_, data.size());
            std::memcpy(buffer_.data() + buffered_, data.data(), take);
            buffered_ += take;
            data = data.subspan(take);
            if (buffered_ == buffer_.size()) {
                transform(buffer_.data());
                buffered_ = 0;
            }
        }
    }

    [[nodiscard]] std::array<std::uint8_t, 32> finish() noexcept {
        const std::uint64_t bit_length = total_bytes_ * 8U;
        buffer_[buffered_++] = 0x80U;
        if (buffered_ > 56U) {
            std::fill(buffer_.begin() + static_cast<std::ptrdiff_t>(buffered_),
                      buffer_.end(), 0U);
            transform(buffer_.data());
            buffered_ = 0;
        }
        std::fill(buffer_.begin() + static_cast<std::ptrdiff_t>(buffered_),
                  buffer_.begin() + 56, 0U);
        for (unsigned int index = 0; index < 8U; ++index) {
            buffer_[63U - index] =
                static_cast<std::uint8_t>(bit_length >> (index * 8U));
        }
        transform(buffer_.data());

        std::array<std::uint8_t, 32> digest{};
        for (std::size_t word = 0; word < state_.size(); ++word) {
            digest[word * 4U] =
                static_cast<std::uint8_t>(state_[word] >> 24U);
            digest[word * 4U + 1U] =
                static_cast<std::uint8_t>(state_[word] >> 16U);
            digest[word * 4U + 2U] =
                static_cast<std::uint8_t>(state_[word] >> 8U);
            digest[word * 4U + 3U] = static_cast<std::uint8_t>(state_[word]);
        }
        return digest;
    }

private:
    void transform(const std::uint8_t* block) noexcept {
        std::array<std::uint32_t, 64> schedule{};
        for (std::size_t index = 0; index < 16U; ++index) {
            schedule[index] =
                (static_cast<std::uint32_t>(block[index * 4U]) << 24U) |
                (static_cast<std::uint32_t>(block[index * 4U + 1U]) << 16U) |
                (static_cast<std::uint32_t>(block[index * 4U + 2U]) << 8U) |
                static_cast<std::uint32_t>(block[index * 4U + 3U]);
        }
        for (std::size_t index = 16U; index < 64U; ++index) {
            const std::uint32_t s0 =
                rotr(schedule[index - 15U], 7U) ^
                rotr(schedule[index - 15U], 18U) ^
                (schedule[index - 15U] >> 3U);
            const std::uint32_t s1 =
                rotr(schedule[index - 2U], 17U) ^
                rotr(schedule[index - 2U], 19U) ^
                (schedule[index - 2U] >> 10U);
            schedule[index] = schedule[index - 16U] + s0 +
                              schedule[index - 7U] + s1;
        }

        std::uint32_t a = state_[0];
        std::uint32_t b = state_[1];
        std::uint32_t c = state_[2];
        std::uint32_t d = state_[3];
        std::uint32_t e = state_[4];
        std::uint32_t f = state_[5];
        std::uint32_t g = state_[6];
        std::uint32_t h = state_[7];

        for (std::size_t index = 0; index < 64U; ++index) {
            const std::uint32_t sum1 =
                rotr(e, 6U) ^ rotr(e, 11U) ^ rotr(e, 25U);
            const std::uint32_t choose = (e & f) ^ (~e & g);
            const std::uint32_t temp1 =
                h + sum1 + choose + kSha256RoundConstants[index] +
                schedule[index];
            const std::uint32_t sum0 =
                rotr(a, 2U) ^ rotr(a, 13U) ^ rotr(a, 22U);
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
    std::size_t buffered_ = 0;
    std::uint64_t total_bytes_ = 0;
};

[[nodiscard]] std::array<std::uint8_t, 32> hmac_sha256(
    ByteSpan key, ByteSpan data) noexcept {
    std::array<std::uint8_t, 64> normalized_key{};
    if (key.size() > normalized_key.size()) {
        Sha256 key_hash;
        key_hash.update(key);
        const auto digest = key_hash.finish();
        std::copy(digest.begin(), digest.end(), normalized_key.begin());
    } else {
        std::copy(key.begin(), key.end(), normalized_key.begin());
    }

    std::array<std::uint8_t, 64> inner_pad{};
    std::array<std::uint8_t, 64> outer_pad{};
    for (std::size_t index = 0; index < normalized_key.size(); ++index) {
        inner_pad[index] = static_cast<std::uint8_t>(
            normalized_key[index] ^ 0x36U);
        outer_pad[index] = static_cast<std::uint8_t>(
            normalized_key[index] ^ 0x5cU);
    }

    Sha256 inner;
    inner.update(inner_pad);
    inner.update(data);
    const auto inner_digest = inner.finish();

    Sha256 outer;
    outer.update(outer_pad);
    outer.update(inner_digest);
    const auto result = outer.finish();
    secure_zero(normalized_key.data(), normalized_key.size());
    secure_zero(inner_pad.data(), inner_pad.size());
    secure_zero(outer_pad.data(), outer_pad.size());
    return result;
}

[[nodiscard]] bool constant_time_equal(ByteSpan left,
                                       ByteSpan right) noexcept {
    if (left.size() != right.size()) {
        return false;
    }
    std::uint8_t difference = 0;
    for (std::size_t index = 0; index < left.size(); ++index) {
        difference |= static_cast<std::uint8_t>(left[index] ^ right[index]);
    }
    return difference == 0;
}

[[nodiscard]] std::uint16_t read_u16(const std::uint8_t* data) noexcept {
    return static_cast<std::uint16_t>(
        (static_cast<std::uint16_t>(data[0]) << 8U) |
        static_cast<std::uint16_t>(data[1]));
}

[[nodiscard]] std::uint32_t read_u32(const std::uint8_t* data) noexcept {
    return (static_cast<std::uint32_t>(data[0]) << 24U) |
           (static_cast<std::uint32_t>(data[1]) << 16U) |
           (static_cast<std::uint32_t>(data[2]) << 8U) |
           static_cast<std::uint32_t>(data[3]);
}

[[nodiscard]] std::array<std::uint8_t, 32> derive_epoch_key(
    const Key& master_key, std::uint32_t session_id, std::uint32_t ssrc,
    std::uint16_t epoch) noexcept {
    static constexpr std::string_view salt =
        "netsec stream authentication v1";
    static constexpr std::string_view label = "NETSEC-EPOCH-KEY-v1";

    auto prk = hmac_sha256(
        ByteSpan(reinterpret_cast<const std::uint8_t*>(salt.data()),
                 salt.size()),
        master_key);

    std::array<std::uint8_t, label.size() + 10U + 1U> info{};
    std::size_t cursor = 0U;
    std::copy(label.begin(), label.end(),
              info.begin() + static_cast<std::ptrdiff_t>(cursor));
    cursor += label.size();
    info[cursor++] = static_cast<std::uint8_t>(session_id >> 24U);
    info[cursor++] = static_cast<std::uint8_t>(session_id >> 16U);
    info[cursor++] = static_cast<std::uint8_t>(session_id >> 8U);
    info[cursor++] = static_cast<std::uint8_t>(session_id);
    info[cursor++] = static_cast<std::uint8_t>(ssrc >> 24U);
    info[cursor++] = static_cast<std::uint8_t>(ssrc >> 16U);
    info[cursor++] = static_cast<std::uint8_t>(ssrc >> 8U);
    info[cursor++] = static_cast<std::uint8_t>(ssrc);
    info[cursor++] = static_cast<std::uint8_t>(epoch >> 8U);
    info[cursor++] = static_cast<std::uint8_t>(epoch);
    info[cursor++] = 0x01U;  // HKDF-Expand block counter.
    const auto result = hmac_sha256(prk, ByteSpan(info.data(), cursor));
    secure_zero(prk.data(), prk.size());
    return result;
}

void secure_zero(void* data, std::size_t size) noexcept {
    volatile auto* bytes = static_cast<volatile std::uint8_t*>(data);
    while (size-- > 0) {
        *bytes++ = 0;
    }
}

}  // namespace detail

const char* to_string(VerifyStatus status) noexcept {
    switch (status) {
        case VerifyStatus::accepted:
            return "accepted";
        case VerifyStatus::accepted_reordered:
            return "accepted_reordered";
        case VerifyStatus::rejected_malformed:
            return "rejected_malformed";
        case VerifyStatus::rejected_session:
            return "rejected_session";
        case VerifyStatus::rejected_ssrc:
            return "rejected_ssrc";
        case VerifyStatus::rejected_unknown_epoch:
            return "rejected_unknown_epoch";
        case VerifyStatus::rejected_expired_epoch:
            return "rejected_expired_epoch";
        case VerifyStatus::rejected_authentication:
            return "rejected_authentication";
        case VerifyStatus::rejected_duplicate:
            return "rejected_duplicate";
        case VerifyStatus::rejected_rollback:
            return "rejected_rollback";
        case VerifyStatus::rejected_forward_gap:
            return "rejected_forward_gap";
        case VerifyStatus::rejected_key_exhausted:
            return "rejected_key_exhausted";
    }
    return "rejected_unknown";
}

namespace {

struct WireHeader {
    std::uint16_t key_epoch{};
    std::uint32_t session_id{};
    std::uint32_t ssrc{};
    std::uint16_t sequence{};
    std::uint32_t timestamp{};
    std::uint32_t payload_length{};
};

[[nodiscard]] bool parse_wire(ByteSpan wire, WireHeader& header) noexcept {
    if (wire.size() < kWireOverhead) {
        return false;
    }
    if (wire[0] != 'N' || wire[1] != 'S' || wire[2] != 'E' || wire[3] != 'C') {
        return false;
    }
    if (wire[4] != 0x01U || wire[5] != 0x00U) {
        return false;
    }

    header.key_epoch = detail::read_u16(wire.data() + 6U);
    header.session_id = detail::read_u32(wire.data() + 8U);
    header.ssrc = detail::read_u32(wire.data() + 12U);
    header.sequence = detail::read_u16(wire.data() + 16U);
    header.timestamp = detail::read_u32(wire.data() + 18U);
    header.payload_length = detail::read_u32(wire.data() + 22U);

    if (header.payload_length > kMaxPayloadSize) {
        return false;
    }
    const std::uint64_t expected =
        static_cast<std::uint64_t>(kWireHeaderSize) +
        static_cast<std::uint64_t>(header.payload_length) + kAuthTagSize;
    return expected == wire.size();
}

[[nodiscard]] std::uint64_t replay_word_count(std::size_t window) noexcept {
    return (window + 63U) / 64U;
}

[[nodiscard]] ReceiverConfig validate_receiver_config(ReceiverConfig config) {
    if (config.replay.window == 0U || config.replay.window > kMaxReplayWindow) {
        throw std::invalid_argument(
            "replay window must be in [1, kMaxReplayWindow]");
    }
    if (config.replay.max_forward_gap == 0U ||
        config.replay.max_forward_gap > kMaxForwardGap) {
        throw std::invalid_argument(
            "max_forward_gap must be in [1, kMaxForwardGap]");
    }
    if (config.retained_epochs > kMaxRetainedEpochs) {
        throw std::invalid_argument(
            "retained_epochs exceeds kMaxRetainedEpochs");
    }
    if (config.max_epoch_advance > kMaxEpochAdvance) {
        throw std::invalid_argument("max_epoch_advance exceeds kMaxEpochAdvance");
    }
    if (config.max_packets_per_epoch == 0U ||
        config.max_packets_per_epoch > kMaxPacketsPerEpoch) {
        throw std::invalid_argument(
            "max_packets_per_epoch is outside the supported range");
    }
    return config;
}

}  // namespace

struct Sender::Impl {
    explicit Impl(SenderConfig config) : config_(std::move(config)) {
        if (config_.max_packets_per_epoch == 0U ||
            config_.max_packets_per_epoch > kMaxPacketsPerEpoch) {
            throw std::invalid_argument(
                "max_packets_per_epoch is outside the supported range");
        }
        epoch_key_ = detail::derive_epoch_key(
            config_.master_key, config_.session_id, config_.ssrc,
            config_.initial_epoch);
    }

    ~Impl() {
        detail::secure_zero(epoch_key_.data(), epoch_key_.size());
        detail::secure_zero(config_.master_key.data(),
                            config_.master_key.size());
    }

    [[nodiscard]] bool seal(std::uint16_t sequence, std::uint32_t timestamp,
                            ByteSpan payload, Bytes& out) noexcept {
        if (payload.size() > kMaxPayloadSize ||
            packets_in_epoch_ >= config_.max_packets_per_epoch) {
            return false;
        }

        try {
            out.assign(kWireHeaderSize + payload.size() + kAuthTagSize, 0U);
            out[0] = 'N';
            out[1] = 'S';
            out[2] = 'E';
            out[3] = 'C';
            out[4] = 0x01U;
            out[5] = 0x00U;
            out[6] = static_cast<std::uint8_t>(epoch_ >> 8U);
            out[7] = static_cast<std::uint8_t>(epoch_);
            out[8] = static_cast<std::uint8_t>(config_.session_id >> 24U);
            out[9] = static_cast<std::uint8_t>(config_.session_id >> 16U);
            out[10] = static_cast<std::uint8_t>(config_.session_id >> 8U);
            out[11] = static_cast<std::uint8_t>(config_.session_id);
            out[12] = static_cast<std::uint8_t>(config_.ssrc >> 24U);
            out[13] = static_cast<std::uint8_t>(config_.ssrc >> 16U);
            out[14] = static_cast<std::uint8_t>(config_.ssrc >> 8U);
            out[15] = static_cast<std::uint8_t>(config_.ssrc);
            out[16] = static_cast<std::uint8_t>(sequence >> 8U);
            out[17] = static_cast<std::uint8_t>(sequence);
            out[18] = static_cast<std::uint8_t>(timestamp >> 24U);
            out[19] = static_cast<std::uint8_t>(timestamp >> 16U);
            out[20] = static_cast<std::uint8_t>(timestamp >> 8U);
            out[21] = static_cast<std::uint8_t>(timestamp);
            out[22] = static_cast<std::uint8_t>(payload.size() >> 24U);
            out[23] = static_cast<std::uint8_t>(payload.size() >> 16U);
            out[24] = static_cast<std::uint8_t>(payload.size() >> 8U);
            out[25] = static_cast<std::uint8_t>(payload.size());
            if (!payload.empty()) {
                std::copy(payload.begin(), payload.end(),
                          out.begin() +
                              static_cast<std::ptrdiff_t>(kWireHeaderSize));
            }

            const std::size_t authenticated_size =
                kWireHeaderSize + payload.size();
            const auto tag = detail::hmac_sha256(
                epoch_key_, ByteSpan(out.data(), authenticated_size));
            std::copy(tag.begin(),
                      tag.begin() + static_cast<std::ptrdiff_t>(kAuthTagSize),
                      out.begin() +
                          static_cast<std::ptrdiff_t>(authenticated_size));
        } catch (...) {
            out.clear();
            return false;
        }

        ++packets_in_epoch_;
        ++stats_.sealed;
        return true;
    }

    SenderConfig config_;
    std::array<std::uint8_t, 32> epoch_key_{};
    std::uint16_t epoch_ = 0;
    std::uint64_t packets_in_epoch_ = 0;
    SenderStats stats_{};
};

Sender::Sender(SenderConfig config)
    : impl_(std::make_unique<Impl>(std::move(config))) {
    impl_->epoch_ = impl_->config_.initial_epoch;
}

Sender::~Sender() = default;
Sender::Sender(Sender&&) noexcept = default;
Sender& Sender::operator=(Sender&&) noexcept = default;

std::uint32_t Sender::session_id() const noexcept {
    return impl_->config_.session_id;
}

std::uint32_t Sender::ssrc() const noexcept { return impl_->config_.ssrc; }

std::uint16_t Sender::key_epoch() const noexcept { return impl_->epoch_; }

std::uint64_t Sender::packets_in_epoch() const noexcept {
    return impl_->packets_in_epoch_;
}

const SenderStats& Sender::stats() const noexcept { return impl_->stats_; }

bool Sender::try_seal(const MediaPacket& packet, Bytes& out) noexcept {
    return impl_->seal(packet.sequence, packet.timestamp, packet.payload, out);
}

bool Sender::try_seal(std::uint16_t sequence, std::uint32_t timestamp,
                      ByteSpan payload, Bytes& out) noexcept {
    return impl_->seal(sequence, timestamp, payload, out);
}

Bytes Sender::seal(const MediaPacket& packet) {
    if (packet.payload.size() > kMaxPayloadSize) {
        throw std::invalid_argument("payload exceeds kMaxPayloadSize");
    }
    Bytes out;
    if (!try_seal(packet, out)) {
        throw std::runtime_error(
            "epoch key exhausted; rotate the key before sealing more media");
    }
    return out;
}

Bytes Sender::seal(std::uint16_t sequence, std::uint32_t timestamp,
                   ByteSpan payload) {
    if (payload.size() > kMaxPayloadSize) {
        throw std::invalid_argument("payload exceeds kMaxPayloadSize");
    }
    Bytes out;
    if (!try_seal(sequence, timestamp, payload, out)) {
        throw std::runtime_error(
            "epoch key exhausted; rotate the key before sealing more media");
    }
    return out;
}

bool Sender::rotate_key() noexcept {
    if (impl_->epoch_ == std::numeric_limits<std::uint16_t>::max()) {
        return false;
    }
    ++impl_->epoch_;
    impl_->packets_in_epoch_ = 0;
    impl_->epoch_key_ = detail::derive_epoch_key(
        impl_->config_.master_key, impl_->config_.session_id,
        impl_->config_.ssrc, impl_->epoch_);
    ++impl_->stats_.rotations;
    return true;
}

bool Sender::set_master_key(Key key, std::uint16_t epoch) noexcept {
    if (epoch <= impl_->epoch_) {
        detail::secure_zero(key.data(), key.size());
        return false;
    }
    detail::secure_zero(impl_->config_.master_key.data(),
                        impl_->config_.master_key.size());
    impl_->config_.master_key = key;
    impl_->epoch_ = epoch;
    impl_->packets_in_epoch_ = 0;
    impl_->epoch_key_ = detail::derive_epoch_key(
        impl_->config_.master_key, impl_->config_.session_id,
        impl_->config_.ssrc, impl_->epoch_);
    ++impl_->stats_.rotations;
    detail::secure_zero(key.data(), key.size());
    return true;
}

struct Receiver::Impl {
    explicit Impl(ReceiverConfig config)
        : config_(validate_receiver_config(std::move(config))),
          window_(replay_word_count(config_.replay.window), 0U),
          active_epoch_(config_.initial_epoch) {
        accepted_per_epoch_[active_epoch_] = 0;
        master_keys_[active_epoch_] = config_.master_key;
    }

    ~Impl() {
        detail::secure_zero(config_.master_key.data(),
                            config_.master_key.size());
        for (auto& entry : master_keys_) {
            detail::secure_zero(entry.second.data(), entry.second.size());
        }
    }

    [[nodiscard]] bool bit(std::uint64_t distance) const noexcept {
        return (window_[distance / 64U] &
                (std::uint64_t{1} << (distance % 64U))) != 0U;
    }

    void set_bit(std::uint64_t distance) noexcept {
        window_[distance / 64U] |= std::uint64_t{1} << (distance % 64U);
    }

    void shift_window(std::uint64_t distance) noexcept {
        const std::uint64_t bits = config_.replay.window;
        if (distance >= bits) {
            std::fill(window_.begin(), window_.end(), 0U);
            return;
        }
        const std::uint64_t words = window_.size();
        const std::uint64_t word_shift = distance / 64U;
        const unsigned int bit_shift =
            static_cast<unsigned int>(distance % 64U);
        if (bit_shift == 0U) {
            for (std::uint64_t index = words; index-- > 0U;) {
                window_[index] =
                    index >= word_shift ? window_[index - word_shift] : 0U;
            }
        } else {
            for (std::uint64_t index = words; index-- > 0U;) {
                std::uint64_t value = 0U;
                if (index >= word_shift) {
                    value = window_[index - word_shift] << bit_shift;
                    if (index > word_shift) {
                        value |= window_[index - word_shift - 1U] >>
                                 (64U - bit_shift);
                    }
                }
                window_[index] = value;
            }
        }
    }

    void prune_epochs() noexcept {
        while (!accepted_per_epoch_.empty()) {
            const auto oldest = accepted_per_epoch_.begin();
            if (oldest->first <= active_epoch_ &&
                static_cast<std::uint32_t>(active_epoch_ - oldest->first) >
                    config_.retained_epochs) {
                accepted_per_epoch_.erase(oldest);
            } else {
                break;
            }
        }
        // A master key remains relevant until the key that supersedes it is
        // also outside the retained epoch range.
        while (master_keys_.size() > 1U) {
            auto current = master_keys_.begin();
            auto next = std::next(current);
            if (next->first <= active_epoch_ &&
                static_cast<std::uint32_t>(active_epoch_ - next->first) >
                    config_.retained_epochs) {
                detail::secure_zero(current->second.data(),
                                    current->second.size());
                master_keys_.erase(current);
            } else {
                break;
            }
        }
    }

    [[nodiscard]] const Key* master_key_for(
        std::uint16_t epoch) const noexcept {
        auto position = master_keys_.upper_bound(epoch);
        if (position == master_keys_.begin()) {
            return nullptr;
        }
        --position;
        return &position->second;
    }

    [[nodiscard]] VerifyResult reject(VerifyStatus status) noexcept {
        ++stats_.policy_rejections;
        if (status == VerifyStatus::rejected_authentication) {
            ++stats_.authentication_failures;
        }
        if (status == VerifyStatus::rejected_duplicate) {
            ++stats_.duplicates;
        }
        return {status};
    }

    [[nodiscard]] VerifyResult receive(ByteSpan wire, MediaPacket& out) {
        ++stats_.received;
        out = MediaPacket{};

        WireHeader header;
        if (!parse_wire(wire, header)) {
            return reject(VerifyStatus::rejected_malformed);
        }
        if (header.session_id != config_.session_id) {
            return reject(VerifyStatus::rejected_session);
        }
        if (header.ssrc != config_.ssrc) {
            return reject(VerifyStatus::rejected_ssrc);
        }

        if (header.key_epoch > active_epoch_) {
            if (static_cast<std::uint32_t>(header.key_epoch - active_epoch_) >
                config_.max_epoch_advance) {
                return reject(VerifyStatus::rejected_unknown_epoch);
            }
        } else if (static_cast<std::uint32_t>(active_epoch_ -
                                              header.key_epoch) >
                   config_.retained_epochs) {
            return reject(VerifyStatus::rejected_expired_epoch);
        }

        const Key* master_key = master_key_for(header.key_epoch);
        if (master_key == nullptr) {
            return reject(VerifyStatus::rejected_unknown_epoch);
        }
        auto epoch_key = detail::derive_epoch_key(
            *master_key, config_.session_id, config_.ssrc, header.key_epoch);
        const std::size_t authenticated_size =
            wire.size() - kAuthTagSize;
        const auto tag = detail::hmac_sha256(
            epoch_key, wire.first(authenticated_size));
        if (!detail::constant_time_equal(
                ByteSpan(tag.data(), kAuthTagSize),
                wire.last(kAuthTagSize))) {
            detail::secure_zero(epoch_key.data(), epoch_key.size());
            return reject(VerifyStatus::rejected_authentication);
        }
        detail::secure_zero(epoch_key.data(), epoch_key.size());

        const auto count_it = accepted_per_epoch_.find(header.key_epoch);
        const std::uint64_t epoch_count =
            count_it == accepted_per_epoch_.end() ? 0U : count_it->second;
        if (epoch_count >= config_.max_packets_per_epoch) {
            return reject(VerifyStatus::rejected_key_exhausted);
        }

        VerifyStatus accepted_status = VerifyStatus::accepted;
        std::uint64_t candidate_extension = 0U;
        std::uint64_t distance_behind = 0U;

        if (!sequence_initialized_) {
            candidate_extension = header.sequence;
        } else {
            const std::uint32_t current_low =
                static_cast<std::uint32_t>(highest_extension_ & 0xFFFFU);
            std::int32_t delta =
                static_cast<std::int32_t>(header.sequence) -
                static_cast<std::int32_t>(current_low);
            if (delta > 32767) {
                delta -= 65536;
            } else if (delta < -32768) {
                delta += 65536;
            }

            if (delta > 0) {
                if (static_cast<std::uint32_t>(delta) >
                        config_.replay.max_forward_gap &&
                    config_.replay.gap == GapPolicy::reject) {
                    return reject(VerifyStatus::rejected_forward_gap);
                }
                candidate_extension =
                    highest_extension_ + static_cast<std::uint64_t>(delta);
                distance_behind = static_cast<std::uint64_t>(delta);
            } else if (delta == 0) {
                return reject(VerifyStatus::rejected_duplicate);
            } else {
                distance_behind = static_cast<std::uint64_t>(-delta);
                if (config_.replay.reorder == ReorderPolicy::reject ||
                    distance_behind >= config_.replay.window) {
                    return reject(VerifyStatus::rejected_rollback);
                }
                if (bit(distance_behind)) {
                    return reject(VerifyStatus::rejected_duplicate);
                }
                accepted_status = VerifyStatus::accepted_reordered;
            }
        }

        if (!sequence_initialized_) {
            sequence_initialized_ = true;
            highest_extension_ = candidate_extension;
            std::fill(window_.begin(), window_.end(), 0U);
            set_bit(0U);
        } else if (accepted_status == VerifyStatus::accepted) {
            shift_window(distance_behind);
            set_bit(0U);
            highest_extension_ = candidate_extension;
        } else {
            set_bit(distance_behind);
        }

        if (header.key_epoch > active_epoch_) {
            active_epoch_ = header.key_epoch;
            prune_epochs();
        }
        auto& epoch_count_ref = accepted_per_epoch_[header.key_epoch];
        ++epoch_count_ref;

        out.session_id = header.session_id;
        out.ssrc = header.ssrc;
        out.sequence = header.sequence;
        out.timestamp = header.timestamp;
        out.key_epoch = header.key_epoch;
        out.payload.assign(
            wire.begin() + static_cast<std::ptrdiff_t>(kWireHeaderSize),
            wire.begin() + static_cast<std::ptrdiff_t>(authenticated_size));

        ++stats_.accepted;
        if (accepted_status == VerifyStatus::accepted_reordered) {
            ++stats_.reordered;
        }
        return {accepted_status};
    }

    ReceiverConfig config_;
    ReceiverStats stats_{};
    std::vector<std::uint64_t> window_;
    bool sequence_initialized_ = false;
    std::uint64_t highest_extension_ = 0U;
    std::uint16_t active_epoch_ = 0;
    std::map<std::uint16_t, std::uint64_t> accepted_per_epoch_;
    std::map<std::uint16_t, Key> master_keys_;
};

Receiver::Receiver(ReceiverConfig config)
    : impl_(std::make_unique<Impl>(std::move(config))) {}

Receiver::~Receiver() = default;
Receiver::Receiver(Receiver&&) noexcept = default;
Receiver& Receiver::operator=(Receiver&&) noexcept = default;

std::uint32_t Receiver::session_id() const noexcept {
    return impl_->config_.session_id;
}

std::uint32_t Receiver::ssrc() const noexcept { return impl_->config_.ssrc; }

std::uint16_t Receiver::active_epoch() const noexcept {
    return impl_->active_epoch_;
}

const ReceiverStats& Receiver::stats() const noexcept { return impl_->stats_; }

VerifyResult Receiver::receive(ByteSpan wire, MediaPacket& out) {
    return impl_->receive(wire, out);
}

VerifyResult Receiver::receive(ByteSpan wire) {
    MediaPacket unused;
    return impl_->receive(wire, unused);
}

bool Receiver::rotate_to(std::uint16_t epoch) noexcept {
    if (epoch <= impl_->active_epoch_) {
        return false;
    }
    impl_->active_epoch_ = epoch;
    impl_->prune_epochs();
    return true;
}

bool Receiver::set_master_key(Key key, std::uint16_t epoch) {
    if (epoch <= impl_->active_epoch_) {
        detail::secure_zero(key.data(), key.size());
        return false;
    }
    impl_->master_keys_[epoch] = key;
    detail::secure_zero(key.data(), key.size());
    impl_->active_epoch_ = epoch;
    impl_->accepted_per_epoch_[epoch] = 0;
    impl_->prune_epochs();
    return true;
}

}  // namespace netsec
