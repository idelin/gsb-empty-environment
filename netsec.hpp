#pragma once

#include <cstddef>
#include <cstdint>
#include <deque>
#include <vector>

namespace netsec {

inline constexpr std::size_t kClassicCanDlc = 8;

struct CanFrame {
    uint32_t id = 0;                       // 11-bit CAN identifier (0..0x7FF)
    uint8_t data[kClassicCanDlc] = {};
    uint8_t dlc = 0;
};

enum class FlowStatus : uint8_t {
    ContinueToSend = 0,
    Wait = 1,
    Overflow = 2,
};

struct FlowControl {
    uint32_t id = 0;                       // echoed sender CAN id
    FlowStatus status = FlowStatus::ContinueToSend;
    uint8_t block_size = 0;                // BS byte (0 = no block)
    uint8_t st_min = 0;                    // STmin byte

    CanFrame to_can_frame() const;
};

struct ReassemblerConfig {
    std::size_t max_payload = 4096;        // caller-supplied buffer ceiling
    uint32_t n_cr_ticks = 1000;            // N_Cr timeout in caller clock units
    uint8_t block_size = 0;                // FC block size sent after FF (0 = no block)
    uint8_t st_min = 0;                    // FC STmin sent after FF
};

enum class RxStatus {
    Idle,              // no reassembly in progress
    Reassembling,      // expecting more consecutive frames
};

enum class RxEvent {
    None,              // frame consumed, nothing else happened
    Complete,          // a full message is available in result().payload
    Ignored,           // frame was ignored, state unchanged
    ProtocolRejected,  // malformed frame: any active reassembly is discarded
    OverflowRejected,  // FF exceeds max_payload: reassembly discarded, overflow FC queued
    Timeout,           // N_Cr elapsed: partial reassembly discarded
};

struct RxResult {
    RxEvent event = RxEvent::None;
    const uint8_t* payload = nullptr;      // valid only when event == Complete
    std::size_t length = 0;
    uint32_t sender_id = 0;
};

// ISO 15765-2 unsegmented/multi-frame reassembler for classic CAN,
// 11-bit identifiers, normal (unextended) addressing.
// Not thread-safe: feed()/poll() are meant to run from one CAN RX context.
class IsoTpReassembler {
public:
    explicit IsoTpReassembler(const ReassemblerConfig& config);

    // Feed one received CAN frame. now is the caller clock value; the same
    // clock unit is used for N_Cr. The returned payload pointer references
    // internal storage and stays valid until the next feed()/poll() call.
    RxResult feed(const CanFrame& frame, uint64_t now);

    // Expire stale reassemblies. Call periodically with the caller clock.
    RxResult poll(uint64_t now);

    // Drain flow control frames the gateway must transmit (CTS or overflow).
    bool take_flow_control(FlowControl& out);

    RxStatus status() const;
    std::size_t reassembled_bytes() const;
    std::size_t expected_bytes() const;

private:
    void reset_session();
    RxResult make_result(RxEvent event);
    RxResult reject(RxEvent event);
    RxResult complete(uint32_t sender_id);
    void queue_fc(uint32_t id, FlowStatus status);

    ReassemblerConfig config_;
    std::vector<uint8_t> buffer_;
    std::deque<FlowControl> tx_flow_;

    RxStatus status_ = RxStatus::Idle;
    uint32_t session_id_ = 0;
    std::size_t expected_ = 0;
    std::size_t received_ = 0;
    uint8_t expected_sn_ = 1;
    uint64_t last_activity_ = 0;
};

}  // namespace netsec
