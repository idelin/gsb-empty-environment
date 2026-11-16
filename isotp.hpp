#pragma once

#include <cstddef>
#include <cstdint>
#include <memory>
#include <unordered_map>
#include <vector>

namespace netsec {
namespace isotp {

constexpr std::size_t kMaxCanDlc = 8;
constexpr std::uint32_t kMaxClassicFirstFrameLength = 4095;

struct CanFrame {
    std::uint32_t id = 0;
    std::uint8_t dlc = 0;
    std::uint8_t data[kMaxCanDlc] = {};
};

enum class RxStatus {
    kIgnored,
    kRejected,
    kFlowControl,
    kComplete,
    kTimeout,
};

struct RxResult {
    RxStatus status = RxStatus::kIgnored;
    const std::uint8_t* payload = nullptr;
    std::size_t length = 0;
    bool sendFlowControl = false;
    CanFrame flowControl = {};
    bool timedOut = false;

    bool completed() const { return status == RxStatus::kComplete; }
};

// Single logical ISO-TP receiver (one CAN identifier / normal addressing).
// Invoke onCanFrame() from the CAN receive callback with a monotonically
// increasing millisecond clock and pollTimeouts() from a timer.
class IsoTpChannel {
public:
    IsoTpChannel() = default;
    IsoTpChannel(std::size_t bufferLimit, std::uint32_t nCrTimeoutMs)
        : nCrTimeoutMs_(nCrTimeoutMs), buffer_(bufferLimit) {}

    // Resizing while a reassembly is in progress discards it.
    void setBufferLimit(std::size_t limit);
    std::size_t bufferLimit() const { return buffer_.size(); }

    void setNCrTimeoutMs(std::uint32_t timeoutMs) { nCrTimeoutMs_ = timeoutMs; }
    std::uint32_t nCrTimeoutMs() const { return nCrTimeoutMs_; }

    bool active() const { return inProgress_; }

    void reset();
    RxResult onCanFrame(const CanFrame& frame, std::uint64_t nowMs);
    bool pollTimeouts(std::uint64_t nowMs);

private:
    RxResult handleSingleFrame(const CanFrame& frame);
    RxResult handleFirstFrame(const CanFrame& frame, std::uint64_t nowMs);
    RxResult handleConsecutiveFrame(const CanFrame& frame, std::uint64_t nowMs);
    RxResult reject(const CanFrame& frame, bool overflow);
    void startReassembly(std::uint32_t totalLength, std::uint64_t nowMs);
    CanFrame makeFlowControl(bool overflow, std::uint32_t id) const;
    bool expired(std::uint64_t nowMs) const;

    std::uint32_t nCrTimeoutMs_ = 0;

    bool inProgress_ = false;
    std::uint32_t expectedLength_ = 0;
    std::size_t receivedLength_ = 0;
    std::uint8_t expectedSequence_ = 0;
    std::uint64_t lastFrameTimeMs_ = 0;
    std::vector<std::uint8_t> buffer_;
};

// Routes 11-bit CAN identifiers to independent reassembly channels.
class IsoTpRouter {
public:
    IsoTpRouter(std::size_t bufferLimit, std::uint32_t nCrTimeoutMs)
        : bufferLimit_(bufferLimit), nCrTimeoutMs_(nCrTimeoutMs) {}

    RxResult onCanFrame(const CanFrame& frame, std::uint64_t nowMs);
    bool pollTimeouts(std::uint64_t nowMs);
    void resetAll();

private:
    IsoTpChannel& channelFor(std::uint32_t id);

    std::size_t bufferLimit_;
    std::uint32_t nCrTimeoutMs_;
    std::unordered_map<std::uint32_t, std::unique_ptr<IsoTpChannel>> channels_;
};

}  // namespace isotp
}  // namespace netsec
