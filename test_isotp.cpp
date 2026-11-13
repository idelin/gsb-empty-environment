#include "netsec.hpp"

#include <cstring>
#include <iostream>
#include <vector>

using namespace netsec;

namespace {

int g_failures = 0;

#define CHECK(cond)                                                         \
    do {                                                                    \
        if (!(cond)) {                                                      \
            std::cerr << "FAIL " << __FILE__ << ":" << __LINE__ << ": "     \
                      << #cond << "\n";                                     \
            ++g_failures;                                                   \
        }                                                                   \
    } while (0)

#define CHECK_EQ(a, b) CHECK((a) == (b))

CanFrame make_sf(uint32_t id, const std::vector<uint8_t>& payload, int dlc = 8) {
    CanFrame frame;
    frame.id = id;
    frame.dlc = static_cast<uint8_t>(dlc);
    frame.data[0] = static_cast<uint8_t>(payload.size());
    std::memcpy(frame.data + 1, payload.data(), payload.size());
    return frame;
}

CanFrame make_ff_classic(uint32_t id, uint16_t length, const uint8_t* payload6) {
    CanFrame frame;
    frame.id = id;
    frame.dlc = 8;
    frame.data[0] = static_cast<uint8_t>(0x10 | ((length >> 8) & 0x0F));
    frame.data[1] = static_cast<uint8_t>(length & 0xFF);
    std::memcpy(frame.data + 2, payload6, 6);
    return frame;
}

CanFrame make_ff_escape(uint32_t id, uint32_t length, const uint8_t* payload2) {
    CanFrame frame;
    frame.id = id;
    frame.dlc = 8;
    frame.data[0] = 0x10;
    frame.data[1] = 0x00;
    frame.data[2] = static_cast<uint8_t>((length >> 24) & 0xFF);
    frame.data[3] = static_cast<uint8_t>((length >> 16) & 0xFF);
    frame.data[4] = static_cast<uint8_t>((length >> 8) & 0xFF);
    frame.data[5] = static_cast<uint8_t>(length & 0xFF);
    std::memcpy(frame.data + 6, payload2, 2);
    return frame;
}

CanFrame make_cf(uint32_t id, uint8_t sn, const uint8_t* payload7, uint8_t dlc = 8) {
    CanFrame frame;
    frame.id = id;
    frame.dlc = dlc;
    frame.data[0] = static_cast<uint8_t>(0x20 | (sn & 0x0F));
    std::memcpy(frame.data + 1, payload7, 7);
    return frame;
}

bool no_pending_fc(IsoTpReassembler& rx) {
    FlowControl fc;
    return !rx.take_flow_control(fc);
}

void drain_fc(IsoTpReassembler& rx) {
    FlowControl fc;
    while (rx.take_flow_control(fc)) {
    }
}

// Drives a complete multi-frame transfer of `payload` and returns the last event.
RxEvent run_transfer(IsoTpReassembler& rx, uint32_t id,
                     const std::vector<uint8_t>& payload, uint64_t t0) {
    CanFrame ff;
    ff.id = id;
    ff.dlc = 8;
    if (payload.size() <= 4095) {
        uint16_t length = static_cast<uint16_t>(payload.size());
        ff.data[0] = static_cast<uint8_t>(0x10 | ((length >> 8) & 0x0F));
        ff.data[1] = static_cast<uint8_t>(length & 0xFF);
        std::memcpy(ff.data + 2, payload.data(), 6);
    } else {
        uint32_t length = static_cast<uint32_t>(payload.size());
        ff.data[0] = 0x10;
        ff.data[1] = 0x00;
        ff.data[2] = static_cast<uint8_t>((length >> 24) & 0xFF);
        ff.data[3] = static_cast<uint8_t>((length >> 16) & 0xFF);
        ff.data[4] = static_cast<uint8_t>((length >> 8) & 0xFF);
        ff.data[5] = static_cast<uint8_t>(length & 0xFF);
        std::memcpy(ff.data + 6, payload.data(), 2);
    }

    RxResult result = rx.feed(ff, t0);
    CHECK(result.event == RxEvent::None);

    std::size_t offset = payload.size() <= 4095 ? 6 : 2;
    uint8_t sn = 1;
    uint64_t now = t0;
    while (offset < payload.size()) {
        uint8_t chunk[7] = {};
        std::size_t take = std::min<std::size_t>(7, payload.size() - offset);
        std::memcpy(chunk, payload.data() + offset, take);
        result = rx.feed(make_cf(id, sn, chunk), ++now);
        offset += take;
        sn = static_cast<uint8_t>((sn + 1) & 0x0F);
        if (offset < payload.size()) {
            CHECK(result.event == RxEvent::None);
        }
    }
    return result.event;
}

std::vector<uint8_t> pattern(std::size_t length) {
    std::vector<uint8_t> data(length);
    for (std::size_t i = 0; i < length; ++i) {
        data[i] = static_cast<uint8_t>(i & 0xFF);
    }
    return data;
}

void test_single_frame() {
    IsoTpReassembler rx(ReassemblerConfig{});

    std::vector<uint8_t> payload = {10, 20, 30, 40, 50, 60, 70};
    RxResult result = rx.feed(make_sf(0x123, payload), 0);
    CHECK(result.event == RxEvent::Complete);
    CHECK(result.length == 7);
    CHECK(result.sender_id == 0x123);
    CHECK(std::memcmp(result.payload, payload.data(), 7) == 0);
    CHECK(rx.status() == RxStatus::Idle);

    // SF length 0 is illegal.
    CanFrame zero;
    zero.id = 0x123;
    zero.dlc = 8;
    zero.data[0] = 0x00;
    CHECK(rx.feed(zero, 1).event == RxEvent::ProtocolRejected);

    // SF length 8 exceeds the 1..7 range.
    CanFrame too_long;
    too_long.id = 0x123;
    too_long.dlc = 8;
    too_long.data[0] = 0x08;
    CHECK(rx.feed(too_long, 2).event == RxEvent::ProtocolRejected);

    // Declared length does not fit the actual DLC.
    CanFrame short_dlc = make_sf(0x123, {1, 2, 3}, 2);
    CHECK(rx.feed(short_dlc, 3).event == RxEvent::ProtocolRejected);
    CHECK(no_pending_fc(rx));
}

void test_escape_small_values_rejected() {
    ReassemblerConfig config;
    config.max_payload = 8192;
    IsoTpReassembler rx(config);
    uint8_t payload[2] = {0xAA, 0xBB};

    const uint32_t small_values[] = {0u, 1u, 7u, 4094u, 4095u};
    for (uint32_t length : small_values) {
        RxResult result = rx.feed(make_ff_escape(0x200, length, payload), 0);
        CHECK(result.event == RxEvent::ProtocolRejected);
        CHECK(rx.status() == RxStatus::Idle);
        CHECK(no_pending_fc(rx));
    }

    // The smallest legal escaped length is exactly 4096.
    RxResult accepted = rx.feed(make_ff_escape(0x200, 4096, payload), 1);
    CHECK(accepted.event == RxEvent::None);
    CHECK(rx.status() == RxStatus::Reassembling);
    CHECK_EQ(rx.expected_bytes(), 4096u);
    FlowControl fc;
    CHECK(rx.take_flow_control(fc));
    CHECK(fc.status == FlowStatus::ContinueToSend);
    CHECK(fc.id == 0x200);
}

void test_escape_overflow_sends_overflow_fc() {
    ReassemblerConfig config;
    config.max_payload = 4096;
    IsoTpReassembler rx(config);
    uint8_t payload[2] = {0x11, 0x22};

    RxResult result = rx.feed(make_ff_escape(0x311, 4097, payload), 0);
    CHECK(result.event == RxEvent::OverflowRejected);
    CHECK(rx.status() == RxStatus::Idle);

    FlowControl fc;
    CHECK(rx.take_flow_control(fc));
    CHECK(fc.status == FlowStatus::Overflow);
    CHECK(fc.id == 0x311);
    CanFrame wire = fc.to_can_frame();
    CHECK(wire.id == 0x311);
    CHECK(wire.data[0] == 0x32);
    CHECK(wire.data[1] == 0x00);
    CHECK(wire.data[2] == 0x00);
    CHECK(no_pending_fc(rx));

    // Classic 12-bit maximum still fits this buffer.
    uint8_t ff_payload[6] = {0};
    RxResult classic = rx.feed(make_ff_classic(0x311, 4095, ff_payload), 1);
    CHECK(classic.event == RxEvent::None);
    CHECK_EQ(rx.expected_bytes(), 4095u);
}

void test_sn_wraparound() {
    ReassemblerConfig config;
    config.max_payload = 8192;
    IsoTpReassembler rx(config);

    // 255 bytes: 6 in the FF, 249 in 36 CFs -> SN wraps twice
    // (1..15, 0..15, 0..4).
    std::vector<uint8_t> payload = pattern(255);
    RxEvent event = run_transfer(rx, 0x456, payload, 1000);
    CHECK(event == RxEvent::Complete);

    // Re-run while manually checking the wrap points.
    ReassemblerConfig big;
    big.max_payload = 8192;
    IsoTpReassembler manual_rx(big);
    uint8_t ff_payload[6] = {};
    std::memcpy(ff_payload, payload.data(), 6);
    CHECK(manual_rx.feed(make_ff_classic(0x456, 255, ff_payload), 0).event ==
          RxEvent::None);
    drain_fc(manual_rx);

    std::size_t offset = 6;
    for (int frame_index = 1; frame_index <= 36; ++frame_index) {
        uint8_t expected_sn = static_cast<uint8_t>(frame_index % 16);
        uint8_t chunk[7] = {};
        std::size_t take = std::min<std::size_t>(7, payload.size() - offset);
        std::memcpy(chunk, payload.data() + offset, take);

        // One-off SN 0 at frame 16 and frame 32 proves 15 -> 0 wraparound.
        if (frame_index == 16 || frame_index == 32) {
            CHECK(expected_sn == 0);
        }
        RxResult result = manual_rx.feed(
            make_cf(0x456, expected_sn, chunk),
            static_cast<uint64_t>(frame_index));
        offset += take;
        if (frame_index < 36) {
            CHECK(result.event == RxEvent::None);
        } else {
            CHECK(result.event == RxEvent::Complete);
            CHECK(result.length == 255);
            CHECK(std::memcmp(result.payload, payload.data(), 255) == 0);
        }
    }
}

void test_out_of_order_and_duplicate_abort() {
    IsoTpReassembler rx(ReassemblerConfig{});
    uint8_t ff_payload[6] = {1, 2, 3, 4, 5, 6};
    CHECK(rx.feed(make_ff_classic(0x100, 20, ff_payload), 0).event ==
          RxEvent::None);
    drain_fc(rx);

    uint8_t chunk[7] = {7, 8, 9, 10, 11, 12, 13};
    CHECK(rx.feed(make_cf(0x100, 1, chunk), 1).event == RxEvent::None);

    // Gap: expected SN 2, received SN 3 -> whole reassembly discarded.
    CHECK(rx.feed(make_cf(0x100, 3, chunk), 2).event ==
          RxEvent::ProtocolRejected);
    CHECK(rx.status() == RxStatus::Idle);

    // Late SN 2 is now an orphan and must be ignored.
    CHECK(rx.feed(make_cf(0x100, 2, chunk), 3).event == RxEvent::Ignored);

    // Duplicate SN after a fresh start is also an abort.
    CHECK(rx.feed(make_ff_classic(0x100, 20, ff_payload), 4).event ==
          RxEvent::None);
    drain_fc(rx);
    CHECK(rx.feed(make_cf(0x100, 1, chunk), 5).event == RxEvent::None);
    CHECK(rx.feed(make_cf(0x100, 1, chunk), 6).event ==
          RxEvent::ProtocolRejected);
    CHECK(rx.status() == RxStatus::Idle);
    CHECK(no_pending_fc(rx));
}

void test_orphan_consecutive_frame_ignored() {
    IsoTpReassembler rx(ReassemblerConfig{});
    uint8_t chunk[7] = {};
    for (uint8_t sn = 0; sn <= 15; ++sn) {
        CHECK(rx.feed(make_cf(0x7FF, sn, chunk), 0).event == RxEvent::Ignored);
    }
    CHECK(rx.status() == RxStatus::Idle);
    CHECK(no_pending_fc(rx));
}

void test_first_frame_restarts_session() {
    IsoTpReassembler rx(ReassemblerConfig{});

    uint8_t old_ff[6] = {9, 9, 9, 9, 9, 9};
    CHECK(rx.feed(make_ff_classic(0x100, 30, old_ff), 0).event ==
          RxEvent::None);
    uint8_t chunk[7] = {8, 8, 8, 8, 8, 8, 8};
    CHECK(rx.feed(make_cf(0x100, 1, chunk), 1).event == RxEvent::None);
    CHECK(rx.status() == RxStatus::Reassembling);
    CHECK_EQ(rx.reassembled_bytes(), 13u);

    // A new FF abandons the partial message and restarts cleanly.
    uint8_t new_ff[6] = {0, 1, 2, 3, 4, 5};
    CHECK(rx.feed(make_ff_classic(0x100, 13, new_ff), 2).event ==
          RxEvent::None);
    CHECK_EQ(rx.expected_bytes(), 13u);
    CHECK_EQ(rx.reassembled_bytes(), 6u);

    uint8_t cf1[7] = {6, 7, 8, 9, 10, 11, 12};
    RxResult result = rx.feed(make_cf(0x100, 1, cf1), 3);
    CHECK(result.event == RxEvent::Complete);
    CHECK(result.length == 13);
    std::vector<uint8_t> expected = {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12};
    CHECK(std::memcmp(result.payload, expected.data(), 13) == 0);
}

void test_n_cr_timeout() {
    ReassemblerConfig config;
    config.n_cr_ticks = 100;
    IsoTpReassembler rx(config);

    // 30 bytes: FF(6) + three CFs, so none of the boundary CFs finishes early.
    uint8_t ff_payload[6] = {1, 2, 3, 4, 5, 6};
    CHECK(rx.feed(make_ff_classic(0x100, 30, ff_payload), 0).event ==
          RxEvent::None);
    uint8_t chunk[7] = {7, 8, 9, 10, 11, 12, 13};
    CHECK(rx.feed(make_cf(0x100, 1, chunk), 50).event == RxEvent::None);

    // Exactly at the limit is still valid (timeout is strictly greater than).
    CHECK(rx.feed(make_cf(0x100, 2, chunk), 100).event == RxEvent::None);

    // One tick past the refreshed deadline: N_Cr exceeded, session discarded.
    CHECK(rx.feed(make_cf(0x100, 3, chunk), 201).event == RxEvent::Timeout);
    CHECK(rx.status() == RxStatus::Idle);

    // Timed-out session cannot be continued.
    CHECK(rx.feed(make_cf(0x100, 4, chunk), 210).event == RxEvent::Ignored);

    // poll() path with the same boundary semantics.
    CHECK(rx.feed(make_ff_classic(0x100, 30, ff_payload), 1000).event ==
          RxEvent::None);
    drain_fc(rx);
    CHECK(rx.poll(1050).event == RxEvent::None);
    CHECK(rx.poll(1100).event == RxEvent::None);
    CHECK(rx.poll(1101).event == RxEvent::Timeout);
    CHECK(rx.status() == RxStatus::Idle);
    CHECK(no_pending_fc(rx));
}

void test_cts_flow_control_fields() {
    ReassemblerConfig config;
    config.block_size = 8;
    config.st_min = 16;
    IsoTpReassembler rx(config);

    uint8_t ff_payload[6] = {};
    CHECK(rx.feed(make_ff_classic(0x201, 100, ff_payload), 0).event ==
          RxEvent::None);

    FlowControl fc;
    CHECK(rx.take_flow_control(fc));
    CHECK(fc.status == FlowStatus::ContinueToSend);
    CHECK(fc.block_size == 8);
    CHECK(fc.st_min == 16);
    CanFrame wire = fc.to_can_frame();
    CHECK(wire.id == 0x201);
    CHECK(wire.dlc == 8);
    CHECK(wire.data[0] == 0x30);
    CHECK(wire.data[1] == 8);
    CHECK(wire.data[2] == 16);
    CHECK(no_pending_fc(rx));
}

void test_escape_full_message_integrity() {
    ReassemblerConfig config;
    config.max_payload = 8192;
    IsoTpReassembler rx(config);

    std::vector<uint8_t> payload = pattern(4096);
    RxEvent event = run_transfer(rx, 0x555, payload, 7);
    CHECK(event == RxEvent::Complete);

    IsoTpReassembler verify(config);
    RxResult result = verify.feed(
        make_ff_escape(0x555, 4096, payload.data()), 0);
    CHECK(result.event == RxEvent::None);
    CHECK_EQ(verify.reassembled_bytes(), 2u);

    std::size_t offset = 2;
    uint8_t sn = 1;
    uint64_t now = 0;
    while (offset < payload.size()) {
        uint8_t chunk[7] = {};
        std::size_t take = std::min<std::size_t>(7, payload.size() - offset);
        std::memcpy(chunk, payload.data() + offset, take);
        result = verify.feed(make_cf(0x555, sn, chunk), ++now);
        offset += take;
        sn = static_cast<uint8_t>((sn + 1) & 0x0F);
    }
    CHECK(result.event == RxEvent::Complete);
    CHECK(result.length == 4096);
    CHECK(std::memcmp(result.payload, payload.data(), payload.size()) == 0);
    CHECK(result.sender_id == 0x555);
}

void test_foreign_id_and_bad_frames_ignored() {
    IsoTpReassembler rx(ReassemblerConfig{});

    uint8_t ff_payload[6] = {};
    CHECK(rx.feed(make_ff_classic(0x100, 20, ff_payload), 0).event ==
          RxEvent::None);

    // CFs from another sender id never touch the active session.
    uint8_t chunk[7] = {};
    CHECK(rx.feed(make_cf(0x101, 1, chunk), 1).event == RxEvent::Ignored);
    CHECK(rx.status() == RxStatus::Reassembling);

    // Extended 29-bit identifiers are out of scope.
    CanFrame ext;
    ext.id = 0x800;
    ext.dlc = 8;
    ext.data[0] = 0x01;
    CHECK(rx.feed(ext, 2).event == RxEvent::Ignored);

    // Zero-DLC garbage is ignored.
    CanFrame empty;
    empty.id = 0x100;
    CHECK(rx.feed(empty, 3).event == RxEvent::Ignored);

    // Correct session still completes afterwards.
    uint8_t real_cf1[7] = {1, 2, 3, 4, 5, 6, 7};
    CHECK(rx.feed(make_cf(0x100, 1, real_cf1), 4).event == RxEvent::None);
    uint8_t real_cf2[7] = {8, 9, 10, 11, 12, 13, 14};
    RxResult result = rx.feed(make_cf(0x100, 2, real_cf2), 5);
    CHECK(result.event == RxEvent::Complete);
    CHECK(result.length == 20);
}

}  // namespace

int main() {
    test_single_frame();
    test_escape_small_values_rejected();
    test_escape_overflow_sends_overflow_fc();
    test_sn_wraparound();
    test_out_of_order_and_duplicate_abort();
    test_orphan_consecutive_frame_ignored();
    test_first_frame_restarts_session();
    test_n_cr_timeout();
    test_cts_flow_control_fields();
    test_escape_full_message_integrity();
    test_foreign_id_and_bad_frames_ignored();

    if (g_failures != 0) {
        std::cerr << g_failures << " check(s) failed\n";
        return 1;
    }
    std::cout << "all ISO-TP reassembler tests passed\n";
    return 0;
}
