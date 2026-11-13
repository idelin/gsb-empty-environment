#include "netsec.hpp"

#include <algorithm>
#include <cstring>

namespace netsec {

namespace {

constexpr uint8_t kPciSingleFrame = 0x0;
constexpr uint8_t kPciFirstFrame = 0x1;
constexpr uint8_t kPciConsecutiveFrame = 0x2;
constexpr uint8_t kSfDlcOverhead = 1;
constexpr uint8_t kFfDlcOverhead = 2;
constexpr uint8_t kFfEscapeDlcOverhead = 6;
constexpr uint8_t kCfDlcOverhead = 1;
constexpr uint16_t kClassicFfLimit = 4095;

}  // namespace

CanFrame FlowControl::to_can_frame() const {
    CanFrame frame;
    frame.id = id;
    frame.dlc = kClassicCanDlc;
    frame.data[0] = static_cast<uint8_t>(0x30 | static_cast<uint8_t>(status));
    frame.data[1] = block_size;
    frame.data[2] = st_min;
    return frame;
}

IsoTpReassembler::IsoTpReassembler(const ReassemblerConfig& config) : config_(config) {
    buffer_.resize(config_.max_payload);
}

void IsoTpReassembler::reset_session() {
    status_ = RxStatus::Idle;
    session_id_ = 0;
    expected_ = 0;
    received_ = 0;
    expected_sn_ = 1;
    last_activity_ = 0;
}

RxResult IsoTpReassembler::make_result(RxEvent event) {
    RxResult result;
    result.event = event;
    return result;
}

RxResult IsoTpReassembler::reject(RxEvent event) {
    reset_session();
    return make_result(event);
}

RxResult IsoTpReassembler::complete(uint32_t sender_id) {
    const std::size_t completed_length = received_;
    RxResult result;
    result.event = RxEvent::Complete;
    result.payload = buffer_.data();
    result.length = completed_length;
    result.sender_id = sender_id;
    reset_session();
    return result;
}

void IsoTpReassembler::queue_fc(uint32_t id, FlowStatus flow_status) {
    FlowControl fc;
    fc.id = id;
    fc.status = flow_status;
    fc.block_size = flow_status == FlowStatus::Overflow ? 0 : config_.block_size;
    fc.st_min = flow_status == FlowStatus::Overflow ? 0 : config_.st_min;
    tx_flow_.push_back(fc);
}

RxResult IsoTpReassembler::feed(const CanFrame& frame, uint64_t now) {
    if (frame.dlc == 0 || frame.id > 0x7FF) {
        return make_result(RxEvent::Ignored);
    }

    const uint8_t pci_type = static_cast<uint8_t>(frame.data[0] >> 4);

    if (pci_type == kPciSingleFrame) {
        const uint8_t sf_len = static_cast<uint8_t>(frame.data[0] & 0x0F);
        if (sf_len < 1 || sf_len > 7 ||
            static_cast<uint8_t>(sf_len + kSfDlcOverhead) > frame.dlc) {
            return reject(RxEvent::ProtocolRejected);
        }
        reset_session();
        std::memcpy(buffer_.data(), frame.data + 1, sf_len);
        received_ = sf_len;
        return complete(frame.id);
    }

    if (pci_type == kPciFirstFrame) {
        const uint16_t ff_len_12 =
            static_cast<uint16_t>((static_cast<uint16_t>(frame.data[0] & 0x0F) << 8) |
                                  frame.data[1]);

        uint32_t total_len = ff_len_12;
        std::size_t ff_overhead = kFfDlcOverhead;
        if (ff_len_12 == 0) {
            // Escape format: bytes 2..5 carry the real 32-bit length.
            if (frame.dlc < kClassicCanDlc) {
                return reject(RxEvent::ProtocolRejected);
            }
            total_len = (static_cast<uint32_t>(frame.data[2]) << 24) |
                        (static_cast<uint32_t>(frame.data[3]) << 16) |
                        (static_cast<uint32_t>(frame.data[4]) << 8) |
                        static_cast<uint32_t>(frame.data[5]);
            if (total_len <= kClassicFfLimit) {
                // Escaped length must not alias a classic 12-bit length.
                return reject(RxEvent::ProtocolRejected);
            }
            ff_overhead = kFfEscapeDlcOverhead;
        }

        if (total_len == 0 || static_cast<uint8_t>(ff_overhead) > frame.dlc) {
            return reject(RxEvent::ProtocolRejected);
        }
        if (static_cast<std::size_t>(total_len) > config_.max_payload) {
            queue_fc(frame.id, FlowStatus::Overflow);
            return reject(RxEvent::OverflowRejected);
        }

        // Any in-progress reassembly is abandoned in favor of this FF.
        reset_session();
        status_ = RxStatus::Reassembling;
        session_id_ = frame.id;
        expected_ = total_len;
        expected_sn_ = 1;
        last_activity_ = now;

        const std::size_t ff_bytes =
            std::min<std::size_t>(expected_, frame.dlc - ff_overhead);
        std::memcpy(buffer_.data(), frame.data + ff_overhead, ff_bytes);
        received_ = ff_bytes;

        if (received_ == expected_) {
            return complete(session_id_);
        }
        queue_fc(frame.id, FlowStatus::ContinueToSend);
        return make_result(RxEvent::None);
    }

    if (pci_type == kPciConsecutiveFrame) {
        if (status_ != RxStatus::Reassembling || frame.id != session_id_) {
            return make_result(RxEvent::Ignored);
        }
        if (now - last_activity_ > config_.n_cr_ticks) {
            return reject(RxEvent::Timeout);
        }

        const uint8_t sn = static_cast<uint8_t>(frame.data[0] & 0x0F);
        if (sn != expected_sn_) {
            // Duplicate, reordered or stale CF: discard the whole reassembly.
            return reject(RxEvent::ProtocolRejected);
        }
        if (frame.dlc < kClassicCanDlc) {
            return reject(RxEvent::ProtocolRejected);
        }

        const std::size_t remaining = expected_ - received_;
        const std::size_t cf_bytes =
            std::min<std::size_t>(remaining, frame.dlc - kCfDlcOverhead);
        std::memcpy(buffer_.data() + received_, frame.data + 1, cf_bytes);
        received_ += cf_bytes;
        expected_sn_ = static_cast<uint8_t>((expected_sn_ + 1) & 0x0F);
        last_activity_ = now;

        if (received_ == expected_) {
            return complete(session_id_);
        }
        return make_result(RxEvent::None);
    }

    // Flow control / reserved frames have no role on the receive path.
    return make_result(RxEvent::Ignored);
}

RxResult IsoTpReassembler::poll(uint64_t now) {
    if (status_ == RxStatus::Reassembling &&
        now - last_activity_ > config_.n_cr_ticks) {
        return reject(RxEvent::Timeout);
    }
    return make_result(RxEvent::None);
}

bool IsoTpReassembler::take_flow_control(FlowControl& out) {
    if (tx_flow_.empty()) {
        return false;
    }
    out = tx_flow_.front();
    tx_flow_.pop_front();
    return true;
}

RxStatus IsoTpReassembler::status() const {
    return status_;
}

std::size_t IsoTpReassembler::reassembled_bytes() const {
    return received_;
}

std::size_t IsoTpReassembler::expected_bytes() const {
    return expected_;
}

}  // namespace netsec
