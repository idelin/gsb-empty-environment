#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <mutex>
#include <optional>
#include <span>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

inline constexpr std::uint32_t kVotingProtocolVersion = 1;
inline constexpr std::size_t kDigestBytes = 32;

// The two controller slots are fixed identities. They are never inferred from
// argument order and may never be duplicated.
enum class ChannelId : std::uint8_t {
    A = 1,
    B = 2,
};

enum class SyncState : std::uint8_t {
    InSync = 0,
    Drifting = 1,
    Lost = 2,
    Faulted = 3,
};

enum class Decision : std::uint8_t {
    SafeStop = 0,
    Actuate = 1,
};

enum class StopReason : std::uint8_t {
    None = 0,
    InvalidConfiguration,
    InvalidChannelIdentity,
    DuplicateChannelIdentity,
    ChannelIsolated,
    CycleMismatch,
    ReplayedCycle,
    SyncStateInvalid,
    SyncMismatch,
    InputMismatch,
    ResultDigestMismatch,
    ResultMismatch,
};

[[nodiscard]] std::string_view to_string(ChannelId id) noexcept;
[[nodiscard]] std::string_view to_string(SyncState state) noexcept;
[[nodiscard]] std::string_view to_string(Decision decision) noexcept;
[[nodiscard]] std::string_view to_string(StopReason reason) noexcept;

struct Digest {
    std::array<std::uint8_t, kDigestBytes> bytes{};

    friend constexpr bool operator==(const Digest&, const Digest&) noexcept = default;
    [[nodiscard]] std::string to_hex() const;
};

[[nodiscard]] Digest sha256(std::span<const std::byte> data);
[[nodiscard]] Digest sha256(std::string_view data);

// A voter-owned snapshot of the current control cycle. `input` is the raw,
// common input from which each channel independently computes its result.
struct CycleFrame {
    std::uint64_t cycle{0};
    std::uint64_t sync_epoch{0};
    std::uint64_t sync_counter{0};
    std::span<const std::byte> input{};
};

// A channel's independently produced evidence for one cycle.
//
// The voter recomputes `input_digest` from `CycleFrame::input` and recomputes
// `result_digest` from `result`; neither digest is trusted as supplied.
struct ChannelReport {
    ChannelId id{ChannelId::A};
    std::uint64_t cycle{0};
    std::uint64_t sync_epoch{0};
    std::uint64_t sync_counter{0};
    // Fail closed: a report must explicitly attest that it is in sync.
    SyncState sync{SyncState::Lost};
    Digest input_digest{};
    Digest result_digest{};
    std::span<const std::byte> result{};
};

struct ChannelState {
    ChannelId id{ChannelId::A};
    bool isolated{false};
    StopReason reason{StopReason::None};
};

struct VoteDecision {
    Decision decision{Decision::SafeStop};
    StopReason reason{StopReason::None};
    std::uint64_t cycle{0};
    std::vector<std::byte> output;
    std::array<ChannelState, 2> channels{};

    // Only this predicate may gate the actuator. A safe-stop decision never
    // authorizes actuation, even if `output` happens to be non-empty.
    [[nodiscard]] bool actuator_authorized() const noexcept;
    [[nodiscard]] bool is_isolated(ChannelId id) const noexcept;
};

struct VoterConfig {
    ChannelId channel_a{ChannelId::A};
    ChannelId channel_b{ChannelId::B};
};

// Stateful, thread-safe fail-stop voter.
//
// Invariants:
//  * Both channels must be present with distinct, expected identities.
//  * Both must report the same cycle, the same healthy sync epoch/counter,
//    identical input digests, and identical results.
//  * Every mismatch produces SafeStop. The component never selects one
//    channel's result and never falls back to a single remaining channel.
//  * Once a channel fault is attributed, its isolation is latched until an
//    explicit recovery call clears it.
class VotingComponent {
public:
    explicit VotingComponent(VoterConfig config = {});

    VotingComponent(const VotingComponent&) = delete;
    VotingComponent& operator=(const VotingComponent&) = delete;
    VotingComponent(VotingComponent&&) = delete;
    VotingComponent& operator=(VotingComponent&&) = delete;

    [[nodiscard]] VoteDecision vote(const CycleFrame& frame,
                                   const ChannelReport& channel_a,
                                   const ChannelReport& channel_b);

    [[nodiscard]] VoteDecision evaluate(const CycleFrame& frame,
                                       const ChannelReport& channel_a,
                                       const ChannelReport& channel_b) {
        return vote(frame, channel_a, channel_b);
    }

    [[nodiscard]] VoteDecision compare(const CycleFrame& frame,
                                      const ChannelReport& channel_a,
                                      const ChannelReport& channel_b) {
        return vote(frame, channel_a, channel_b);
    }

    [[nodiscard]] ChannelState channel_state(ChannelId id) const;
    [[nodiscard]] bool is_isolated(ChannelId id) const;

    // Recovery is deliberately explicit. Clearing isolation does not reset the
    // replay window; use reset() only for a fresh voting epoch.
    [[nodiscard]] bool clear_isolation(ChannelId id);
    void clear_all_isolation() noexcept;
    void reset() noexcept;

private:
    [[nodiscard]] static bool valid_channel(ChannelId id) noexcept;
    [[nodiscard]] static std::optional<std::size_t> channel_index(ChannelId id) noexcept;
    [[nodiscard]] VoteDecision snapshot_decision(std::uint64_t cycle,
                                                 StopReason reason) const;
    void isolate(std::size_t index, StopReason reason) noexcept;
    void isolate_both(StopReason reason) noexcept;

    static constexpr std::size_t kChannelCount = 2;

    VoterConfig config_;
    bool config_valid_{false};
    std::array<bool, kChannelCount> isolated_{{false, false}};
    std::array<StopReason, kChannelCount> fault_reason_{
        StopReason::None, StopReason::None};
    std::optional<std::uint64_t> last_cycle_;
    mutable std::mutex mutex_;
};

using VoteOutcome = Decision;
using RedundantVoter = VotingComponent;
using Voter = VotingComponent;

// Convenience constructor for a correctly cross-hashed channel report.
[[nodiscard]] ChannelReport make_report(const CycleFrame& frame,
                                       ChannelId id,
                                       std::span<const std::byte> result);

} // namespace netsec
