#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <optional>
#include <ostream>
#include <string>
#include <string_view>
#include <unordered_map>
#include <vector>

namespace netsec {

inline constexpr std::uint32_t kSmpCntModulus = 4000;

class MacAddress {
public:
    using Octets = std::array<std::uint8_t, 6>;

    MacAddress() = default;
    explicit MacAddress(const Octets& octets) : octets_(octets) {}
    MacAddress(std::uint8_t first, std::uint8_t second, std::uint8_t third,
               std::uint8_t fourth, std::uint8_t fifth, std::uint8_t sixth)
        : octets_{first, second, third, fourth, fifth, sixth} {}
    explicit MacAddress(const char* text);
    explicit MacAddress(std::string_view text);

    static MacAddress parse(std::string_view text);
    static std::optional<MacAddress> tryParse(std::string_view text) noexcept;

    const Octets& octets() const noexcept { return octets_; }
    Octets& octets() noexcept { return octets_; }
    std::string toString() const;

    bool operator==(const MacAddress& other) const noexcept {
        return octets_ == other.octets_;
    }
    bool operator!=(const MacAddress& other) const noexcept {
        return !(*this == other);
    }
    bool operator<(const MacAddress& other) const noexcept {
        return octets_ < other.octets_;
    }

private:
    Octets octets_{};
};

std::ostream& operator<<(std::ostream& stream, const MacAddress& address);

struct SvFrame {
    std::string svID;
    MacAddress sourceMac;
    std::uint32_t confRev{0};
    std::uint32_t smpCnt{0};
};

enum class SvStatus : std::uint8_t {
    Accepted = 0,
    Duplicate,
    Replay,
    RejectedSourceMac,
    RejectedConfRev,
    RejectedSourceMacAndConfRev,
    RejectedAwaitingReconfirmation,
    RejectedMalformed
};

const char* toString(SvStatus status) noexcept;
std::ostream& operator<<(std::ostream& stream, SvStatus status);

struct SvDecision {
    SvStatus status{SvStatus::Accepted};
    std::uint32_t receivedSmpCnt{0};
    std::uint32_t expectedSmpCnt{0};
    std::uint32_t lostSamples{0};
    std::uint32_t gapStart{0};
    std::uint32_t gapEnd{0};
    std::uint64_t totalLostSamples{0};
    std::vector<std::uint32_t> invalidSmpCnts;

    bool accepted() const noexcept { return status == SvStatus::Accepted; }
    bool rejected() const noexcept { return !accepted(); }
    bool duplicate() const noexcept { return status == SvStatus::Duplicate; }
    bool replay() const noexcept { return status == SvStatus::Replay; }
    bool awaitingReconfirmation() const noexcept {
        return status == SvStatus::RejectedAwaitingReconfirmation;
    }
    std::uint32_t lost() const noexcept { return lostSamples; }
};

struct SvBinding {
    MacAddress sourceMac;
    std::uint32_t confRev{0};
    bool awaitingReconfirmation{false};
};

struct SvStreamStats {
    std::uint64_t receivedFrames{0};
    std::uint64_t acceptedFrames{0};
    std::uint64_t duplicateFrames{0};
    std::uint64_t replayFrames{0};
    std::uint64_t identityRejectedFrames{0};
    std::uint64_t awaitingReconfirmationFrames{0};
    std::uint64_t malformedFrames{0};
    std::uint64_t lostSamples{0};
    std::uint64_t invalidSamples{0};
    std::uint64_t gapEvents{0};
    bool hasLastSmpCnt{false};
    std::uint32_t lastSmpCnt{0};
};

class SvSubscriber {
public:
    SvDecision receive(const SvFrame& frame);
    SvDecision onFrame(const SvFrame& frame) { return receive(frame); }
    SvDecision ingest(const SvFrame& frame) { return receive(frame); }
    SvDecision feed(const SvFrame& frame) { return receive(frame); }

    bool confirm(const std::string& svID, const MacAddress& sourceMac,
                 std::uint32_t confRev);
    bool confirm(const SvFrame& frame) {
        return confirm(frame.svID, frame.sourceMac, frame.confRev);
    }
    bool reconfirm(const std::string& svID, const MacAddress& sourceMac,
                   std::uint32_t confRev) {
        return confirm(svID, sourceMac, confRev);
    }
    bool reconfirm(const SvFrame& frame) { return confirm(frame); }
    bool bind(const std::string& svID, const MacAddress& sourceMac,
              std::uint32_t confRev) {
        return confirm(svID, sourceMac, confRev);
    }
    bool reconfigure(const std::string& svID, const MacAddress& sourceMac,
                     std::uint32_t confRev) {
        return confirm(svID, sourceMac, confRev);
    }

    bool resetSequence(const std::string& svID);
    bool unbind(const std::string& svID);
    void clear();

    bool isBound(const std::string& svID) const;
    bool isLocked(const std::string& svID) const;
    bool isAwaitingReconfirmation(const std::string& svID) const {
        return isLocked(svID);
    }
    std::optional<SvBinding> binding(const std::string& svID) const;
    SvStreamStats stats(const std::string& svID) const;
    std::size_t size() const noexcept { return streams_.size(); }

private:
    struct StreamState {
        bool bound{false};
        bool locked{false};
        MacAddress sourceMac;
        std::uint32_t confRev{0};
        bool hasLastSmpCnt{false};
        std::uint32_t lastSmpCnt{0};
        SvStreamStats stats;
    };

    static SvStatus identityStatus(bool macMismatch, bool confRevMismatch) noexcept;
    SvDecision reject(const SvFrame& frame, StreamState& state, SvStatus status);
    SvDecision acceptCounter(const SvFrame& frame, StreamState& state);

    std::unordered_map<std::string, StreamState> streams_;
};

using SVSubscriber = SvSubscriber;
using SvSubscription = SvSubscriber;
using SvReceiver = SvSubscriber;
using SampledValueSubscriber = SvSubscriber;

}  // namespace netsec
