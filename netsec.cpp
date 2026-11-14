#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cstdint>
#include <string>

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
    0x90befffaU, 0xa4506cebU, 0xbef9a3f7U, 0xc67178f2U,
};

constexpr std::uint32_t rotate_right(std::uint32_t value, unsigned count) noexcept {
    return (value >> count) | (value << (32U - count));
}

class Sha256 {
public:
    Sha256() = default;

    void update(std::span<const std::byte> data) noexcept {
        for (const std::byte value : data) {
            buffer_[buffer_size_] = std::to_integer<std::uint8_t>(value);
            ++buffer_size_;
            ++total_bytes_;
            if (buffer_size_ == buffer_.size()) {
                compress();
                buffer_size_ = 0;
            }
        }
    }

    [[nodiscard]] std::array<std::uint8_t, netsec::kDigestBytes> finish() {
        const std::uint64_t total_bits = total_bytes_ * 8U;

        buffer_[buffer_size_] = 0x80U;
        ++buffer_size_;
        if (buffer_size_ > 56U) {
            std::fill(buffer_.begin() + static_cast<std::ptrdiff_t>(buffer_size_),
                      buffer_.end(), 0U);
            compress();
            buffer_size_ = 0;
        }
        std::fill(buffer_.begin() + static_cast<std::ptrdiff_t>(buffer_size_),
                  buffer_.begin() + 56, 0U);

        for (unsigned index = 0; index < 8U; ++index) {
            buffer_[63U - index] =
                static_cast<std::uint8_t>((total_bits >> (index * 8U)) & 0xffU);
        }
        compress();

        std::array<std::uint8_t, netsec::kDigestBytes> digest{};
        for (size_t word = 0; word < state_.size(); ++word) {
            digest[word * 4U] =
                static_cast<std::uint8_t>((state_[word] >> 24U) & 0xffU);
            digest[word * 4U + 1U] =
                static_cast<std::uint8_t>((state_[word] >> 16U) & 0xffU);
            digest[word * 4U + 2U] =
                static_cast<std::uint8_t>((state_[word] >> 8U) & 0xffU);
            digest[word * 4U + 3U] =
                static_cast<std::uint8_t>(state_[word] & 0xffU);
        }
        return digest;
    }

private:
    void compress() noexcept {
        std::array<std::uint32_t, 64> words{};
        for (size_t index = 0; index < 16U; ++index) {
            const size_t offset = index * 4U;
            words[index] =
                (static_cast<std::uint32_t>(buffer_[offset]) << 24U) |
                (static_cast<std::uint32_t>(buffer_[offset + 1U]) << 16U) |
                (static_cast<std::uint32_t>(buffer_[offset + 2U]) << 8U) |
                static_cast<std::uint32_t>(buffer_[offset + 3U]);
        }
        for (size_t index = 16U; index < words.size(); ++index) {
            const std::uint32_t s0 =
                rotate_right(words[index - 15U], 7U) ^
                rotate_right(words[index - 15U], 18U) ^
                (words[index - 15U] >> 3U);
            const std::uint32_t s1 =
                rotate_right(words[index - 2U], 17U) ^
                rotate_right(words[index - 2U], 19U) ^
                (words[index - 2U] >> 10U);
            words[index] = words[index - 16U] + s0 + words[index - 7U] + s1;
        }

        std::uint32_t a = state_[0];
        std::uint32_t b = state_[1];
        std::uint32_t c = state_[2];
        std::uint32_t d = state_[3];
        std::uint32_t e = state_[4];
        std::uint32_t f = state_[5];
        std::uint32_t g = state_[6];
        std::uint32_t h = state_[7];

        for (size_t index = 0; index < words.size(); ++index) {
            const std::uint32_t sum1 =
                rotate_right(e, 6U) ^ rotate_right(e, 11U) ^ rotate_right(e, 25U);
            const std::uint32_t choose = (e & f) ^ ((~e) & g);
            const std::uint32_t temp1 =
                h + sum1 + choose + kSha256RoundConstants[index] + words[index];
            const std::uint32_t sum0 =
                rotate_right(a, 2U) ^ rotate_right(a, 13U) ^ rotate_right(a, 22U);
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
        0x6a09e667U, 0xbb67ae85U, 0x3c6ef372U, 0xa54ff53aU,
        0x510e527fU, 0x9b05688cU, 0x1f83d9abU, 0x5be0cd19U,
    };
    std::array<std::uint8_t, 64> buffer_{};
    size_t buffer_size_{0};
    std::uint64_t total_bytes_{0};
};

void append_u32_be(Sha256& hash, std::uint32_t value) noexcept {
    const std::array<std::byte, 4> encoded{
        static_cast<std::byte>((value >> 24U) & 0xffU),
        static_cast<std::byte>((value >> 16U) & 0xffU),
        static_cast<std::byte>((value >> 8U) & 0xffU),
        static_cast<std::byte>(value & 0xffU),
    };
    hash.update(encoded);
}

void append_u64_be(Sha256& hash, std::uint64_t value) noexcept {
    const std::array<std::byte, 8> encoded{
        static_cast<std::byte>((value >> 56U) & 0xffU),
        static_cast<std::byte>((value >> 48U) & 0xffU),
        static_cast<std::byte>((value >> 40U) & 0xffU),
        static_cast<std::byte>((value >> 32U) & 0xffU),
        static_cast<std::byte>((value >> 24U) & 0xffU),
        static_cast<std::byte>((value >> 16U) & 0xffU),
        static_cast<std::byte>((value >> 8U) & 0xffU),
        static_cast<std::byte>(value & 0xffU),
    };
    hash.update(encoded);
}

void append_domain(Sha256& hash, std::string_view domain) noexcept {
    hash.update(std::as_bytes(std::span(domain)));
}

[[nodiscard]] netsec::Digest input_digest(std::uint64_t cycle,
                                          std::span<const std::byte> input) {
    Sha256 hash;
    append_domain(hash, "netsec.vote.input.v1");
    append_u32_be(hash, netsec::kVotingProtocolVersion);
    append_u64_be(hash, cycle);
    append_u64_be(hash, static_cast<std::uint64_t>(input.size()));
    hash.update(input);
    return netsec::Digest{hash.finish()};
}

[[nodiscard]] netsec::Digest result_digest(
    std::uint64_t cycle,
    const netsec::Digest& bound_input_digest,
    std::span<const std::byte> result) {
    Sha256 hash;
    append_domain(hash, "netsec.vote.result.v1");
    append_u32_be(hash, netsec::kVotingProtocolVersion);
    append_u64_be(hash, cycle);
    hash.update(std::as_bytes(std::span(bound_input_digest.bytes)));
    append_u64_be(hash, static_cast<std::uint64_t>(result.size()));
    hash.update(result);
    return netsec::Digest{hash.finish()};
}

[[nodiscard]] std::size_t channel_slot(netsec::ChannelId id) noexcept {
    return id == netsec::ChannelId::A ? 0U : 1U;
}

[[nodiscard]] bool same_bytes(std::span<const std::byte> lhs,
                              std::span<const std::byte> rhs) noexcept {
    return lhs.size() == rhs.size() && std::equal(lhs.begin(), lhs.end(), rhs.begin());
}

} // namespace

namespace netsec {

std::string_view to_string(ChannelId id) noexcept {
    switch (id) {
    case ChannelId::A:
        return "A";
    case ChannelId::B:
        return "B";
    }
    return "invalid";
}

std::string_view to_string(SyncState state) noexcept {
    switch (state) {
    case SyncState::InSync:
        return "in-sync";
    case SyncState::Drifting:
        return "drifting";
    case SyncState::Lost:
        return "lost";
    case SyncState::Faulted:
        return "faulted";
    }
    return "invalid";
}

std::string_view to_string(Decision decision) noexcept {
    switch (decision) {
    case Decision::SafeStop:
        return "safe-stop";
    case Decision::Actuate:
        return "actuate";
    }
    return "invalid";
}

std::string_view to_string(StopReason reason) noexcept {
    switch (reason) {
    case StopReason::None:
        return "none";
    case StopReason::InvalidConfiguration:
        return "invalid-configuration";
    case StopReason::InvalidChannelIdentity:
        return "invalid-channel-identity";
    case StopReason::DuplicateChannelIdentity:
        return "duplicate-channel-identity";
    case StopReason::ChannelIsolated:
        return "channel-isolated";
    case StopReason::CycleMismatch:
        return "cycle-mismatch";
    case StopReason::ReplayedCycle:
        return "replayed-cycle";
    case StopReason::SyncStateInvalid:
        return "sync-state-invalid";
    case StopReason::SyncMismatch:
        return "sync-mismatch";
    case StopReason::InputMismatch:
        return "input-mismatch";
    case StopReason::ResultDigestMismatch:
        return "result-digest-mismatch";
    case StopReason::ResultMismatch:
        return "result-mismatch";
    }
    return "unknown";
}

std::string Digest::to_hex() const {
    std::string result;
    result.reserve(bytes.size() * 2U);
    for (const std::uint8_t value : bytes) {
        const char high = static_cast<char>(value >> 4U);
        const char low = static_cast<char>(value & 0x0fU);
        result.push_back(static_cast<char>('0' + (high <= 9 ? high : high + 39)));
        result.push_back(static_cast<char>('0' + (low <= 9 ? low : low + 39)));
    }
    return result;
}

Digest sha256(std::span<const std::byte> data) {
    Sha256 hash;
    hash.update(data);
    return Digest{hash.finish()};
}

Digest sha256(std::string_view data) {
    return sha256(std::as_bytes(std::span(data)));
}

bool VoteDecision::actuator_authorized() const noexcept {
    return decision == Decision::Actuate && reason == StopReason::None &&
           channels[0].id == ChannelId::A && channels[1].id == ChannelId::B &&
           !channels[0].isolated && !channels[1].isolated &&
           channels[0].reason == StopReason::None &&
           channels[1].reason == StopReason::None;
}

bool VoteDecision::is_isolated(ChannelId id) const noexcept {
    if (id != ChannelId::A && id != ChannelId::B) {
        return true;
    }
    for (const ChannelState& state : channels) {
        if (state.id == id) {
            return state.isolated;
        }
    }
    return false;
}

VotingComponent::VotingComponent(VoterConfig config) : config_(config) {
    config_valid_ = valid_channel(config.channel_a) &&
                    valid_channel(config.channel_b) &&
                    config.channel_a != config.channel_b;
    if (!config_valid_) {
        isolate_both(StopReason::InvalidConfiguration);
    }
}

bool VotingComponent::valid_channel(ChannelId id) noexcept {
    return id == ChannelId::A || id == ChannelId::B;
}

std::optional<std::size_t> VotingComponent::channel_index(ChannelId id) noexcept {
    if (!valid_channel(id)) {
        return std::nullopt;
    }
    return channel_slot(id);
}

VoteDecision VotingComponent::snapshot_decision(std::uint64_t cycle,
                                                StopReason reason) const {
    VoteDecision decision;
    decision.decision = Decision::SafeStop;
    decision.reason = reason;
    decision.cycle = cycle;
    decision.channels[0] = ChannelState{ChannelId::A, isolated_[0], fault_reason_[0]};
    decision.channels[1] = ChannelState{ChannelId::B, isolated_[1], fault_reason_[1]};
    return decision;
}

void VotingComponent::isolate(std::size_t index, StopReason reason) noexcept {
    if (index < kChannelCount) {
        isolated_[index] = true;
        fault_reason_[index] = reason;
    }
}

void VotingComponent::isolate_both(StopReason reason) noexcept {
    isolate(0, reason);
    isolate(1, reason);
}

VoteDecision VotingComponent::vote(const CycleFrame& frame,
                                   const ChannelReport& channel_a,
                                   const ChannelReport& channel_b) {
    std::lock_guard<std::mutex> lock(mutex_);

    if (!config_valid_) {
        return snapshot_decision(frame.cycle, StopReason::InvalidConfiguration);
    }
    if (isolated_[0] || isolated_[1]) {
        return snapshot_decision(frame.cycle, StopReason::ChannelIsolated);
    }

    // Resolve identity from the report itself, never from call-site ordering.
    std::array<const ChannelReport*, kChannelCount> reports{};
    for (const ChannelReport* report : {&channel_a, &channel_b}) {
        const std::optional<std::size_t> index = channel_index(report->id);
        if (!index.has_value()) {
            isolate_both(StopReason::InvalidChannelIdentity);
            return snapshot_decision(frame.cycle,
                                     StopReason::InvalidChannelIdentity);
        }
        if (reports[*index] != nullptr) {
            isolate_both(StopReason::DuplicateChannelIdentity);
            return snapshot_decision(frame.cycle,
                                     StopReason::DuplicateChannelIdentity);
        }
        reports[*index] = report;
    }
    if (reports[0] == nullptr || reports[1] == nullptr) {
        isolate_both(StopReason::InvalidChannelIdentity);
        return snapshot_decision(frame.cycle, StopReason::InvalidChannelIdentity);
    }

    const ChannelReport& report_a = *reports[0];
    const ChannelReport& report_b = *reports[1];
    const bool config_pair =
        (config_.channel_a == ChannelId::A && config_.channel_b == ChannelId::B) ||
        (config_.channel_a == ChannelId::B && config_.channel_b == ChannelId::A);
    if (!config_pair) {
        isolate_both(StopReason::InvalidConfiguration);
        return snapshot_decision(frame.cycle, StopReason::InvalidConfiguration);
    }

    // A cycle is a freshness token as well as a pairing key.
    if (last_cycle_.has_value() && frame.cycle <= *last_cycle_) {
        return snapshot_decision(frame.cycle, StopReason::ReplayedCycle);
    }

    bool cycle_fault = false;
    if (report_a.cycle != frame.cycle) {
        isolate(0, StopReason::CycleMismatch);
        cycle_fault = true;
    }
    if (report_b.cycle != frame.cycle) {
        isolate(1, StopReason::CycleMismatch);
        cycle_fault = true;
    }
    if (cycle_fault) {
        return snapshot_decision(frame.cycle, StopReason::CycleMismatch);
    }

    bool sync_state_fault = false;
    if (report_a.sync != SyncState::InSync) {
        isolate(0, StopReason::SyncStateInvalid);
        sync_state_fault = true;
    }
    if (report_b.sync != SyncState::InSync) {
        isolate(1, StopReason::SyncStateInvalid);
        sync_state_fault = true;
    }
    if (sync_state_fault) {
        return snapshot_decision(frame.cycle, StopReason::SyncStateInvalid);
    }

    bool sync_fault = false;
    if (report_a.sync_epoch != frame.sync_epoch ||
        report_a.sync_counter != frame.sync_counter) {
        isolate(0, StopReason::SyncMismatch);
        sync_fault = true;
    }
    if (report_b.sync_epoch != frame.sync_epoch ||
        report_b.sync_counter != frame.sync_counter) {
        isolate(1, StopReason::SyncMismatch);
        sync_fault = true;
    }
    if (sync_fault ||
        report_a.sync_epoch != report_b.sync_epoch ||
        report_a.sync_counter != report_b.sync_counter) {
        return snapshot_decision(frame.cycle, StopReason::SyncMismatch);
    }

    // The voter independently fingerprints the common input. A channel may
    // supply a syntactically valid digest and still be pinned as faulty here.
    const Digest expected_input_digest = input_digest(frame.cycle, frame.input);
    bool input_fault = false;
    if (report_a.input_digest != expected_input_digest) {
        isolate(0, StopReason::InputMismatch);
        input_fault = true;
    }
    if (report_b.input_digest != expected_input_digest) {
        isolate(1, StopReason::InputMismatch);
        input_fault = true;
    }
    if (input_fault || report_a.input_digest != report_b.input_digest) {
        return snapshot_decision(frame.cycle, StopReason::InputMismatch);
    }

    // Result evidence is bound to the cycle and the verified input transcript.
    // Supplied digests are recomputed from raw result bytes, so altering a
    // digest alone pins that channel without accepting its result.
    const Digest expected_result_a =
        result_digest(frame.cycle, expected_input_digest, report_a.result);
    const Digest expected_result_b =
        result_digest(frame.cycle, expected_input_digest, report_b.result);
    bool result_digest_fault = false;
    if (report_a.result_digest != expected_result_a) {
        isolate(0, StopReason::ResultDigestMismatch);
        result_digest_fault = true;
    }
    if (report_b.result_digest != expected_result_b) {
        isolate(1, StopReason::ResultDigestMismatch);
        result_digest_fault = true;
    }
    if (result_digest_fault) {
        return snapshot_decision(frame.cycle, StopReason::ResultDigestMismatch);
    }

    if (report_a.result_digest != report_b.result_digest ||
        !same_bytes(report_a.result, report_b.result)) {
        // Dual-channel divergence cannot be adjudicated safely. There is no
        // trusted third opinion and majority selection is forbidden, so both
        // channels are isolated and the actuator remains disabled.
        isolate_both(StopReason::ResultMismatch);
        return snapshot_decision(frame.cycle, StopReason::ResultMismatch);
    }

    last_cycle_ = frame.cycle;
    VoteDecision decision = snapshot_decision(frame.cycle, StopReason::None);
    decision.decision = Decision::Actuate;
    decision.output.assign(report_a.result.begin(), report_a.result.end());
    return decision;
}

ChannelState VotingComponent::channel_state(ChannelId id) const {
    std::lock_guard<std::mutex> lock(mutex_);
    const std::optional<std::size_t> index = channel_index(id);
    if (!index.has_value()) {
        return ChannelState{id, true, StopReason::InvalidChannelIdentity};
    }
    return ChannelState{id, isolated_[*index], fault_reason_[*index]};
}

bool VotingComponent::is_isolated(ChannelId id) const {
    std::lock_guard<std::mutex> lock(mutex_);
    const std::optional<std::size_t> index = channel_index(id);
    return !index.has_value() || isolated_[*index];
}

bool VotingComponent::clear_isolation(ChannelId id) {
    std::lock_guard<std::mutex> lock(mutex_);
    if (!config_valid_) {
        return false;
    }
    const std::optional<std::size_t> index = channel_index(id);
    if (!index.has_value()) {
        return false;
    }
    isolated_[*index] = false;
    fault_reason_[*index] = StopReason::None;
    return true;
}

void VotingComponent::clear_all_isolation() noexcept {
    std::lock_guard<std::mutex> lock(mutex_);
    if (!config_valid_) {
        return;
    }
    isolated_.fill(false);
    fault_reason_.fill(StopReason::None);
}

void VotingComponent::reset() noexcept {
    std::lock_guard<std::mutex> lock(mutex_);
    last_cycle_.reset();
    if (config_valid_) {
        isolated_.fill(false);
        fault_reason_.fill(StopReason::None);
    } else {
        isolate_both(StopReason::InvalidConfiguration);
    }
}

ChannelReport make_report(const CycleFrame& frame,
                          ChannelId id,
                          std::span<const std::byte> result) {
    const Digest input = input_digest(frame.cycle, frame.input);
    return ChannelReport{
        id,
        frame.cycle,
        frame.sync_epoch,
        frame.sync_counter,
        SyncState::InSync,
        input,
        result_digest(frame.cycle, input, result),
        result,
    };
}

} // namespace netsec
