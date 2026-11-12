#pragma once

#include <cstddef>
#include <cstdint>

namespace netsec {

inline constexpr std::size_t kCanFdMaxPayload = 64;
inline constexpr std::size_t kCanFdMaxWhitelistEntries = 32;

enum class CanFdIde : std::uint8_t {
    Standard = 0,
    Extended = 1,
};

// DLC values 0..8 map directly to payload bytes. CAN FD DLC values 9..15
// map to the non-linear payload sizes 12, 16, 20, 24, 32, 48 and 64.
// Invalid DLC values return 0; use isValidCanFdDlc() to distinguish them from
// the valid zero-length frame.
std::size_t canFdPayloadLength(std::uint8_t dlc) noexcept;
bool isValidCanFdDlc(std::uint8_t dlc) noexcept;

struct CanFdFrame {
    std::uint32_t id{};
    CanFdIde ide{CanFdIde::Standard};
    std::uint8_t dlc{};
    const std::uint8_t* payload{};
    std::size_t payloadLength{};
    // Caller-provided, monotonically increasing time base in microseconds.
    std::uint64_t timestampUs{};
    // Meaningful only for whitelist entries that require freshness.
    std::uint32_t counter{};
};

struct CanFdWhitelistEntry {
    std::uint32_t id{};
    CanFdIde ide{CanFdIde::Standard};
    std::uint8_t dlc{};
    std::uint64_t minPeriodUs{};
    bool requiresFreshCounter{};
    bool requiresMac{};
};

enum class CanFdFilterStatus : std::uint8_t {
    Accepted = 0,
    InvalidIdentifier,
    InvalidDlc,
    DlcMismatch,
    PayloadLengthMismatch,
    PayloadUnavailable,
    NotWhitelisted,
    IdeMismatch,
    TooSoon,
    TimestampRegressed,
    ReplayedCounter,
    MacVerifierMissing,
    MacRejected,
};

constexpr bool isAccepted(CanFdFilterStatus status) noexcept {
    return status == CanFdFilterStatus::Accepted;
}

// The callback owns the key and the truncated-MAC comparison. The filter
// invokes it only after identity, size, timing and freshness checks pass.
// A false result rejects the frame without advancing per-ID state.
using CanFdMacVerifier = bool (*)(const CanFdFrame& frame,
                                  const CanFdWhitelistEntry& entry,
                                  void* context);

class CanFdFilter {
public:
    CanFdFilter() noexcept = default;
    explicit CanFdFilter(CanFdMacVerifier verifier, void* context = nullptr) noexcept;

    // Rejects duplicate ID+IDE keys, invalid identifier/IDE combinations,
    // invalid DLCs and a full table.
    bool addEntry(const CanFdWhitelistEntry& entry) noexcept;

    void setMacVerifier(CanFdMacVerifier verifier, void* context = nullptr) noexcept;

    // Performs the complete check and commits per-ID state only on acceptance.
    CanFdFilterStatus evaluate(const CanFdFrame& frame) noexcept;

    // Use this overload when the forwarding path has already verified the
    // truncated MAC. Pass true only after successful verification.
    CanFdFilterStatus evaluate(const CanFdFrame& frame, bool macVerified) noexcept;

    bool accept(const CanFdFrame& frame) noexcept;
    bool accept(const CanFdFrame& frame, bool macVerified) noexcept;

    // Clears learned timestamps and counters while retaining the whitelist.
    // Call after an ECU/network restart when counters may restart.
    void resetTracking() noexcept;

    std::size_t size() const noexcept;
    std::size_t capacity() const noexcept;

private:
    struct EntryState {
        CanFdWhitelistEntry entry{};
        bool hasTimestamp{};
        std::uint64_t lastAcceptedTimestampUs{};
        bool hasCounter{};
        std::uint32_t lastAcceptedCounter{};
    };

    CanFdFilterStatus evaluateImpl(const CanFdFrame& frame, bool macPreverified) noexcept;

    EntryState entries_[kCanFdMaxWhitelistEntries]{};
    std::size_t size_{};
    CanFdMacVerifier verifier_{};
    void* verifierContext_{};
};

}  // namespace netsec
