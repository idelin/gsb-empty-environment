// SPDX-License-Identifier: MIT

#include "dtls13/reassembly.hpp"
#include "test_framework.hpp"

using namespace dtls13;

namespace {

Bytes encodeFragment(std::uint8_t msg_type,
                     std::uint32_t total_length,
                     std::uint16_t message_seq,
                     std::uint32_t offset,
                     const Bytes& body) {
    Bytes out;
    out.reserve(kHandshakeHeaderSize + body.size());
    out.push_back(msg_type);
    std::uint8_t total3[3];
    std::uint8_t offset3[3];
    std::uint8_t length3[3];
    std::uint8_t seq2[2];
    writeBe24(total3, total_length);
    out.insert(out.end(), total3, total3 + 3);
    writeBe16(seq2, message_seq);
    out.insert(out.end(), seq2, seq2 + 2);
    writeBe24(offset3, offset);
    out.insert(out.end(), offset3, offset3 + 3);
    const std::uint32_t fragment_length =
        static_cast<std::uint32_t>(body.size());
    writeBe24(length3, fragment_length);
    out.insert(out.end(), length3, length3 + 3);
    out.insert(out.end(), body.begin(), body.end());
    return out;
}

HandshakeFragment fragment(std::uint8_t msg_type,
                           std::uint32_t total_length,
                           std::uint16_t message_seq,
                           std::uint32_t offset,
                           const Bytes& body) {
    HandshakeFragment f;
    f.msg_type = msg_type;
    f.total_length = total_length;
    f.message_seq = message_seq;
    f.fragment_offset = offset;
    f.fragment_length = static_cast<std::uint32_t>(body.size());
    f.body = ByteView(body);
    return f;
}

}  // namespace

TEST_CASE(handshake_header_roundtrip_parse) {
    Bytes wire = encodeFragment(
        static_cast<std::uint8_t>(HandshakeType::ClientHello), 10, 2, 3,
        {1, 2, 3, 4, 5, 6, 7});
    auto parsed = parseHandshakeFragment(ByteView(wire));
    CHECK(parsed.ok());
    CHECK_EQ(parsed.value.msg_type,
             static_cast<std::uint8_t>(HandshakeType::ClientHello));
    CHECK_EQ(parsed.value.total_length, 10u);
    CHECK_EQ(parsed.value.message_seq, 2u);
    CHECK_EQ(parsed.value.fragment_offset, 3u);
    CHECK_EQ(parsed.value.fragment_length, 7u);
}

TEST_CASE(handshake_header_rejects_length_mismatch) {
    Bytes wire = encodeFragment(1, 10, 0, 0, {1, 2});
    wire.pop_back();  // Declared fragment_length no longer matches body.
    auto parsed = parseHandshakeFragment(ByteView(wire));
    CHECK_EQ(parsed.code, ErrorCode::MalformedRecord);
}

TEST_CASE(reassembly_out_of_order_fragments_complete_message) {
    HandshakeReassembler reassembler;
    const std::uint8_t kType =
        static_cast<std::uint8_t>(HandshakeType::Certificate);

    auto third = reassembler.addFragment(
        fragment(kType, 10, 0, 6, {6, 7, 8, 9}));
    CHECK_EQ(third.code, ErrorCode::Ok);
    CHECK(!third.completed);

    auto first = reassembler.addFragment(
        fragment(kType, 10, 0, 0, {0, 1, 2}));
    CHECK_EQ(first.code, ErrorCode::Ok);

    auto middle = reassembler.addFragment(
        fragment(kType, 10, 0, 3, {3, 4, 5}));
    CHECK_EQ(middle.code, ErrorCode::Ok);
    CHECK(middle.completed);
    CHECK_EQ(middle.message.total_length, 10u);
    for (std::uint8_t i = 0; i < 10; ++i) {
        CHECK_EQ(middle.message.body[i], i);
    }
    CHECK_EQ(reassembler.openMessageCount(), 0u);
}

TEST_CASE(reassembly_identical_overlap_is_deduped) {
    HandshakeReassembler reassembler;
    const std::uint8_t kType = 1;
    auto first = reassembler.addFragment(
        fragment(kType, 6, 0, 0, {0, 1, 2, 3}));
    CHECK_EQ(first.code, ErrorCode::Ok);

    // Retransmission with identical overlapping bytes plus new bytes.
    auto second = reassembler.addFragment(
        fragment(kType, 6, 0, 2, {2, 3, 4, 5}));
    CHECK_EQ(second.code, ErrorCode::Ok);
    CHECK(second.completed);
    CHECK_EQ(second.message.body.size(), 6u);
    for (std::uint8_t i = 0; i < 6; ++i) {
        CHECK_EQ(second.message.body[i], i);
    }
}

TEST_CASE(reassembly_conflicting_overlap_is_injection) {
    HandshakeReassembler reassembler;
    const std::uint8_t kType = 1;
    auto first = reassembler.addFragment(
        fragment(kType, 6, 0, 0, {0, 1, 2, 3}));
    CHECK_EQ(first.code, ErrorCode::Ok);

    // One overlapping byte (index 2) disagrees: 99 instead of 2.
    auto attack = reassembler.addFragment(
        fragment(kType, 6, 0, 2, {99, 3, 4, 5}));
    CHECK_EQ(attack.code, ErrorCode::OverlapConflict);
    CHECK(!attack.completed);

    // The slot is poisoned: later benign fragments cannot resurrect it.
    auto follow_up = reassembler.addFragment(
        fragment(kType, 6, 0, 4, {4, 5}));
    CHECK_EQ(follow_up.code, ErrorCode::ReassemblyPoisoned);
    CHECK_EQ(reassembler.openMessageCount(), 1u);
}

TEST_CASE(reassembly_fragment_running_past_total_is_rejected) {
    HandshakeReassembler reassembler;
    HandshakeFragment bad;
    bad.msg_type = 1;
    bad.total_length = 5;
    bad.message_seq = 0;
    bad.fragment_offset = 4;
    bad.fragment_length = 4;  // 4 + 4 > 5
    static const Bytes body = {1, 2, 3, 4};
    bad.body = ByteView(body);

    auto result = reassembler.addFragment(bad);
    CHECK_EQ(result.code, ErrorCode::FragmentOverflow);
    CHECK_EQ(reassembler.openMessageCount(), 0u);
}

TEST_CASE(reassembly_metadata_change_is_rejected) {
    HandshakeReassembler reassembler;
    auto first = reassembler.addFragment(
        fragment(1, 8, 0, 0, {0, 1, 2}));
    CHECK_EQ(first.code, ErrorCode::Ok);

    // total_length changed for the same message_seq.
    auto changed_length = reassembler.addFragment(
        fragment(1, 9, 0, 3, {3, 4}));
    CHECK_EQ(changed_length.code, ErrorCode::InconsistentHeader);

    // msg_type changed for the same message_seq.
    auto changed_type = reassembler.addFragment(
        fragment(2, 8, 0, 3, {3, 4}));
    CHECK_EQ(changed_type.code, ErrorCode::ReassemblyPoisoned);
}

TEST_CASE(reassembly_distinct_message_seq_are_independent) {
    HandshakeReassembler reassembler;
    auto a = reassembler.addFragment(fragment(1, 4, 0, 0, {0, 1}));
    auto b = reassembler.addFragment(fragment(2, 4, 1, 0, {0, 1}));
    CHECK_EQ(a.code, ErrorCode::Ok);
    CHECK_EQ(b.code, ErrorCode::Ok);
    CHECK_EQ(reassembler.openMessageCount(), 2u);

    auto a_done = reassembler.addFragment(
        fragment(1, 4, 0, 2, {2, 3}));
    CHECK(a_done.completed);
    CHECK_EQ(a_done.message.msg_type, 1);
    CHECK_EQ(a_done.message.message_seq, 0u);

    auto b_done = reassembler.addFragment(
        fragment(2, 4, 1, 2, {2, 3}));
    CHECK(b_done.completed);
    CHECK_EQ(b_done.message.message_seq, 1u);
}

TEST_CASE(reassembly_enforces_open_message_slot_budget) {
    ReassemblyLimits limits;
    limits.max_open_messages = 2;
    limits.max_message_bytes = 1024;
    limits.max_total_buffered_bytes = 1u << 20;
    HandshakeReassembler reassembler(limits);

    CHECK_EQ(reassembler.addFragment(fragment(1, 4, 0, 0, {1})).code,
             ErrorCode::Ok);
    CHECK_EQ(reassembler.addFragment(fragment(1, 4, 1, 0, {1})).code,
             ErrorCode::Ok);
    auto third = reassembler.addFragment(fragment(1, 4, 2, 0, {1}));
    CHECK_EQ(third.code, ErrorCode::TooManyMessages);
}

TEST_CASE(reassembly_enforces_max_message_bytes) {
    ReassemblyLimits limits;
    limits.max_message_bytes = 8;
    HandshakeReassembler reassembler(limits);
    auto result = reassembler.addFragment(fragment(1, 9, 0, 0, {1}));
    CHECK_EQ(result.code, ErrorCode::MessageTooLarge);
}

TEST_CASE(reassembly_enforces_total_buffered_bytes_budget) {
    ReassemblyLimits limits;
    limits.max_open_messages = 16;
    limits.max_message_bytes = 64;
    limits.max_total_buffered_bytes = 20;
    HandshakeReassembler reassembler(limits);
    // Each slot reserves total_length + bitmap bytes; 16-byte message
    // allocates 18 bytes, the second 18-byte allocation exceeds 20 total.
    CHECK_EQ(reassembler.addFragment(fragment(1, 16, 0, 0, {1})).code,
             ErrorCode::Ok);
    auto second = reassembler.addFragment(fragment(1, 16, 1, 0, {1}));
    CHECK_EQ(second.code, ErrorCode::MessageTooLarge);
}

TEST_CASE(reassembly_discard_frees_budget) {
    ReassemblyLimits limits;
    limits.max_open_messages = 1;
    HandshakeReassembler reassembler(limits);
    CHECK_EQ(reassembler.addFragment(fragment(1, 4, 0, 0, {1})).code,
             ErrorCode::Ok);
    reassembler.discard(0);
    CHECK_EQ(reassembler.openMessageCount(), 0u);
    CHECK_EQ(reassembler.addFragment(fragment(1, 4, 1, 0, {1})).code,
             ErrorCode::Ok);
}

TEST_CASE(reassembly_empty_final_fragment_completes) {
    HandshakeReassembler reassembler;
    CHECK_EQ(reassembler.addFragment(
                 fragment(1, 3, 0, 0, {0, 1})).code,
             ErrorCode::Ok);
    auto tail = reassembler.addFragment(fragment(1, 3, 0, 3, {}));
    CHECK_EQ(tail.code, ErrorCode::Ok);
    CHECK(!tail.completed);

    auto final_byte = reassembler.addFragment(
        fragment(1, 3, 0, 2, {2}));
    CHECK(final_byte.completed);
    CHECK_EQ(final_byte.message.body.size(), 3u);
}

TEST_CASE(reassembly_zero_length_message_completes) {
    HandshakeReassembler reassembler;
    auto result = reassembler.addFragment(fragment(1, 0, 7, 0, {}));
    CHECK_EQ(result.code, ErrorCode::Ok);
    CHECK(result.completed);
    CHECK_EQ(result.message.total_length, 0u);
    CHECK(result.message.body.empty());
    CHECK_EQ(reassembler.openMessageCount(), 0u);
}
