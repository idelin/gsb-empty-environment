#include "isotp.hpp"

namespace netsec {
namespace isotp {

namespace {

constexpr std::uint8_t kPciSingleFrame = 0x00;
constexpr std::uint8_t kPciFirstFrame = 0x01;
constexpr std::uint8_t kPciConsecutiveFrame = 0x02;
constexpr std::uint8_t kPciFlowControl = 0x03;

constexpr std::uint8_t kFcContinue = 0x00;
constexpr std::uint8_t kFcOverflow = 0x02;

constexpr std::uint32_t kCan11BitMaxId = 0x7FFu;

std::uint8_t pciType(const CanFrame& frame) {
    return static_cast<std::uint8_t>(frame.data[0] >> 4);
}

}  // namespace

void IsoTpChannel::setBufferLimit(std::size_t limit) {
    reset();
    buffer_.assign(limit, 0);
}

void IsoTpChannel::reset() {
    inProgress_ = false;
    expectedLength_ = 0;
    receivedLength_ = 0;
    expectedSequence_ = 0;
    lastFrameTimeMs_ = 0;
}

bool IsoTpChannel::expired(std::uint64_t nowMs) const {
    return inProgress_ && nCrTimeoutMs_ > 0 &&
           nowMs - lastFrameTimeMs_ > static_cast<std::uint64_t>(nCrTimeoutMs_);
}

CanFrame IsoTpChannel::makeFlowControl(bool overflow, std::uint32_t id) const {
    CanFrame fc{};
    fc.id = id;
    fc.dlc = kMaxCanDlc;
    fc.data[0] = static_cast<std::uint8_t>((kPciFlowControl << 4) |
                                           (overflow ? kFcOverflow : kFcContinue));
    // Block size 0 = continue without further flow control; STmin = 0 ms.
    fc.data[1] = 0;
    fc.data[2] = 0;
    for (std::uint8_t i = 3; i < kMaxCanDlc; ++i) {
        fc.data[i] = 0;
    }
    return fc;
}

RxResult IsoTpChannel::reject(const CanFrame& frame, bool overflow) {
    reset();
    RxResult result;
    result.status = RxStatus::kRejected;
    if (overflow) {
        result.sendFlowControl = true;
        result.flowControl = makeFlowControl(true, frame.id);
    }
    return result;
}

void IsoTpChannel::startReassembly(std::uint32_t totalLength, std::uint64_t nowMs) {
    reset();
    inProgress_ = true;
    expectedLength_ = totalLength;
    receivedLength_ = 0;
    expectedSequence_ = 1;
    lastFrameTimeMs_ = nowMs;
}

RxResult IsoTpChannel::handleSingleFrame(const CanFrame& frame) {
    const std::uint8_t sfLength = static_cast<std::uint8_t>(frame.data[0] & 0x0Fu);
    if (sfLength < 1 || sfLength > 7 ||
        frame.dlc < static_cast<std::uint8_t>(sfLength + 1) ||
        static_cast<std::size_t>(sfLength) > buffer_.size()) {
        RxResult result;
        result.status = RxStatus::kRejected;
        return result;
    }

    RxResult result;
    result.status = RxStatus::kComplete;
    result.length = sfLength;
    for (std::uint8_t i = 0; i < sfLength; ++i) {
        buffer_[i] = frame.data[1 + i];
    }
    result.payload = buffer_.data();
    return result;
}

RxResult IsoTpChannel::handleFirstFrame(const CanFrame& frame, std::uint64_t nowMs) {
    if (frame.dlc < kMaxCanDlc) {
        return reject(frame, false);
    }

    std::uint32_t totalLength =
        static_cast<std::uint32_t>((static_cast<std::uint16_t>(frame.data[0] & 0x0Fu) << 8) |
                                   frame.data[1]);
    std::size_t ffPayloadOffset = 2;

    if (totalLength == 0) {
        totalLength = (static_cast<std::uint32_t>(frame.data[2]) << 24) |
                      (static_cast<std::uint32_t>(frame.data[3]) << 16) |
                      (static_cast<std::uint32_t>(frame.data[4]) << 8) |
                      static_cast<std::uint32_t>(frame.data[5]);
        // Escape format must actually escape the 12-bit range.
        if (totalLength <= kMaxClassicFirstFrameLength) {
            return reject(frame, false);
        }
        ffPayloadOffset = 6;
    }

    if (buffer_.empty() || totalLength > buffer_.size()) {
        return reject(frame, true);
    }

    startReassembly(totalLength, nowMs);

    const std::size_t available = kMaxCanDlc - ffPayloadOffset;
    const std::size_t chunk =
        available < static_cast<std::size_t>(totalLength) ? available : static_cast<std::size_t>(totalLength);
    for (std::size_t i = 0; i < chunk; ++i) {
        buffer_[i] = frame.data[ffPayloadOffset + i];
    }
    receivedLength_ = chunk;

    if (receivedLength_ == static_cast<std::size_t>(expectedLength_)) {
        RxResult result;
        result.status = RxStatus::kComplete;
        result.payload = buffer_.data();
        result.length = receivedLength_;
        reset();
        return result;
    }

    RxResult result;
    result.status = RxStatus::kFlowControl;
    result.sendFlowControl = true;
    result.flowControl = makeFlowControl(false, frame.id);
    return result;
}

RxResult IsoTpChannel::handleConsecutiveFrame(const CanFrame& frame, std::uint64_t nowMs) {
    const std::uint8_t sequenceNumber = static_cast<std::uint8_t>(frame.data[0] & 0x0Fu);
    if (frame.dlc < 2 || sequenceNumber != expectedSequence_) {
        // Duplicate, skipped or out-of-order frame aborts the whole reassembly.
        return reject(frame, false);
    }

    const std::size_t remaining = static_cast<std::size_t>(expectedLength_) - receivedLength_;
    const std::size_t available = static_cast<std::size_t>(frame.dlc) - 1;
    const std::size_t chunk = remaining < available ? remaining : available;
    for (std::size_t i = 0; i < chunk; ++i) {
        buffer_[receivedLength_ + i] = frame.data[1 + i];
    }
    receivedLength_ += chunk;
    lastFrameTimeMs_ = nowMs;
    expectedSequence_ = static_cast<std::uint8_t>((expectedSequence_ + 1) % 16);

    if (receivedLength_ == static_cast<std::size_t>(expectedLength_)) {
        RxResult result;
        result.status = RxStatus::kComplete;
        result.payload = buffer_.data();
        result.length = receivedLength_;
        reset();
        return result;
    }

    RxResult result;
    result.status = RxStatus::kIgnored;
    return result;
}

RxResult IsoTpChannel::onCanFrame(const CanFrame& frame, std::uint64_t nowMs) {
    RxResult result;
    bool wasTimedOut = false;

    if (frame.dlc == 0 || frame.dlc > kMaxCanDlc) {
        result.status = RxStatus::kRejected;
        return result;
    }

    if (expired(nowMs)) {
        wasTimedOut = true;
        reset();
    }

    switch (pciType(frame)) {
        case kPciSingleFrame:
            result = handleSingleFrame(frame);
            break;
        case kPciFirstFrame:
            result = handleFirstFrame(frame, nowMs);
            break;
        case kPciConsecutiveFrame:
            if (!inProgress_) {
                // Orphan consecutive frame: no first frame has been accepted.
                result.status = RxStatus::kIgnored;
            } else {
                result = handleConsecutiveFrame(frame, nowMs);
            }
            break;
        default:
            result.status = RxStatus::kIgnored;
            break;
    }

    result.timedOut = wasTimedOut;
    return result;
}

bool IsoTpChannel::pollTimeouts(std::uint64_t nowMs) {
    if (!expired(nowMs)) {
        return false;
    }
    reset();
    return true;
}

IsoTpChannel& IsoTpRouter::channelFor(std::uint32_t id) {
    auto entry = channels_.find(id);
    if (entry == channels_.end()) {
        auto channel = std::make_unique<IsoTpChannel>(bufferLimit_, nCrTimeoutMs_);
        IsoTpChannel* raw = channel.get();
        channels_.emplace(id, std::move(channel));
        return *raw;
    }
    return *entry->second;
}

RxResult IsoTpRouter::onCanFrame(const CanFrame& frame, std::uint64_t nowMs) {
    RxResult result;
    if (frame.id > kCan11BitMaxId) {
        result.status = RxStatus::kRejected;
        return result;
    }
    return channelFor(frame.id).onCanFrame(frame, nowMs);
}

bool IsoTpRouter::pollTimeouts(std::uint64_t nowMs) {
    bool anyExpired = false;
    for (auto& entry : channels_) {
        anyExpired = entry.second->pollTimeouts(nowMs) || anyExpired;
    }
    return anyExpired;
}

void IsoTpRouter::resetAll() {
    channels_.clear();
}

}  // namespace isotp
}  // namespace netsec
