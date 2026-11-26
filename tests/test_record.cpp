// SPDX-License-Identifier: MIT

#include "dtls13/record_layer.hpp"
#include "test_framework.hpp"

using namespace dtls13;

namespace {

Bytes plaintextRecord(std::uint16_t epoch,
                      std::uint64_t seq,
                      bool seq16,
                      const Bytes& body,
                      bool include_epoch = true) {
    EncodeOptions options;
    options.epoch = epoch;
    options.include_epoch = include_epoch;
    options.seq48 = seq;
    options.use_16bit_seq = seq16;
    options.include_length = true;
    options.body_length = static_cast<std::uint16_t>(body.size());
    Bytes record = encodeUnifiedHeader(ContentType::Handshake, options);
    record.insert(record.end(), body.begin(), body.end());
    return record;
}

}  // namespace

TEST_CASE(unified_header_decode_8bit_seq_and_length) {
    const Bytes body = {0x0A, 0x0B, 0x0C};
    Bytes datagram = plaintextRecord(0x0102, 0x05, false, body);
    auto parsed = parseDatagram(ByteView(datagram), 0);
    CHECK(parsed.ok());
    CHECK_EQ(parsed.value.size(), 1u);
    const auto& record = parsed.value[0];
    CHECK_EQ(record.outer_type, ContentType::Handshake);
    CHECK(record.header.epoch_present);
    CHECK_EQ(record.header.epoch, 0x0102);
    CHECK(!record.header.seq_16_bits);
    CHECK_EQ(record.header.truncated_seq, 0x05u);
    CHECK_EQ(record.header.seq_bits, 8u);
    CHECK(record.header.has_length);
    CHECK_EQ(record.body.size(), body.size());
}

TEST_CASE(unified_header_decode_16bit_seq) {
    const Bytes body = {0x01};
    Bytes datagram = plaintextRecord(0x0007, 0x01AB, true, body);
    auto parsed = parseDatagram(ByteView(datagram), 0);
    CHECK(parsed.ok());
    const auto& record = parsed.value[0];
    CHECK(record.header.seq_16_bits);
    CHECK_EQ(record.header.truncated_seq, 0x01ABu);
    CHECK_EQ(record.header.seq_bits, 16u);
}

TEST_CASE(unified_header_rejects_bad_fixed_pattern_and_reserved_bit) {
    const Bytes bad = {
        static_cast<std::uint8_t>(ContentType::Handshake),
        0x00,  // pattern != 001
        0x00, 0x00, 0x00};
    auto parsed = parseDatagram(ByteView(bad), 0);
    CHECK_EQ(parsed.code, ErrorCode::UnsupportedHeader);

    const Bytes reserved = {
        static_cast<std::uint8_t>(ContentType::Handshake),
        static_cast<std::uint8_t>(kUhFixedPattern | kUhReservedFlag),
        0x00, 0x00, 0x00};
    parsed = parseDatagram(ByteView(reserved), 0);
    CHECK_EQ(parsed.code, ErrorCode::UnsupportedHeader);
}

TEST_CASE(unified_header_truncated_datagram_is_rejected) {
    const Bytes cut = {
        static_cast<std::uint8_t>(ContentType::Handshake),
        static_cast<std::uint8_t>(kUhFixedPattern | kUhLengthFlag |
                                  kUhEpochFlag)};
    auto parsed = parseDatagram(ByteView(cut), 0);
    CHECK_EQ(parsed.code, ErrorCode::TruncatedDatagram);
}

TEST_CASE(epoch_elision_uses_current_epoch) {
    const Bytes body = {0x11};
    Bytes first = plaintextRecord(0x0003, 0x00, false, body);
    Bytes second = plaintextRecord(0, 0x01, false, body,
                                   /*include_epoch=*/false);
    Bytes datagram = first;
    datagram.insert(datagram.end(), second.begin(), second.end());

    auto parsed = parseDatagram(ByteView(datagram), 0);
    CHECK(parsed.ok());
    CHECK_EQ(parsed.value.size(), 2u);
    CHECK_EQ(parsed.value[1].header.epoch, 0x0003);
}

TEST_CASE(sequence_reconstruction_8bit_basic) {
    CHECK_EQ(reconstructSequence(5, 8, 4), 5u);
    CHECK_EQ(reconstructSequence(9, 8, 10), 9u);
    CHECK_EQ(reconstructSequence(0, 8, 255), 256u);
    CHECK_EQ(reconstructSequence(10, 8, 255), 266u);
    CHECK_EQ(reconstructSequence(128, 8, 255), 128u);
    // Exact half-window tie resolves toward the higher sequence number.
    CHECK_EQ(reconstructSequence(127, 8, 255), 383u);
}

TEST_CASE(sequence_reconstruction_16bit_basic) {
    CHECK_EQ(reconstructSequence(42, 16, 100), 42u);
    CHECK_EQ(reconstructSequence(0, 16, 65535), 65536u);
    CHECK_EQ(reconstructSequence(1, 16, 65535), 65537u);
    CHECK_EQ(reconstructSequence(32768, 16, 65535), 32768u);
    CHECK_EQ(reconstructSequence(32767, 16, 65535), 98303u);
}

TEST_CASE(sequence_reconstruction_wraps_at_48_bits) {
    const std::uint64_t top = kSeqSpaceMask;  // 2^48 - 1
    CHECK_EQ(reconstructSequence(0, 8, top), 0u);
    CHECK_EQ(reconstructSequence(1, 8, top), 1u);
    CHECK_EQ(reconstructSequence(255, 8, top), top);
    CHECK_EQ(reconstructSequence(5, 16, top) & ~kSeqSpaceMask, 0u);
}

TEST_CASE(replay_window_accepts_fresh_rejects_duplicates) {
    ReplayWindow window;
    CHECK_EQ(window.inspect(10), ReplayWindow::Verdict::Fresh);
    window.commit(10);
    CHECK_EQ(window.inspect(10), ReplayWindow::Verdict::Replayed);
    CHECK_EQ(window.inspect(11), ReplayWindow::Verdict::Fresh);
    window.commit(11);
    CHECK_EQ(window.inspect(9), ReplayWindow::Verdict::Fresh);
    window.commit(9);
    CHECK_EQ(window.inspect(9), ReplayWindow::Verdict::Replayed);
    // 11 - 0 = 11 < 64: still inside the window and never seen.
    CHECK_EQ(window.inspect(0), ReplayWindow::Verdict::Fresh);
    // 11 - 64 is negative; the first value outside the window behind 11
    // is 11 - 64 on the non-wrapping ring, i.e. anything <= -53. Use a
    // high-water mark large enough to express "64 behind" directly.
    ReplayWindow wide;
    wide.commit(100);
    CHECK_EQ(wide.inspect(36), ReplayWindow::Verdict::TooOld);  // 64 behind
    CHECK_EQ(wide.inspect(37), ReplayWindow::Verdict::Fresh);   // 63 behind
}

TEST_CASE(replay_window_slides_and_forgets_old_entries) {
    ReplayWindow window;
    window.commit(5);
    window.commit(100);
    CHECK_EQ(window.windowEnd(), 100u);
    // seq 5 is 95 behind the high-water mark, outside the 64-entry window.
    CHECK_EQ(window.inspect(5), ReplayWindow::Verdict::TooOld);
    CHECK_EQ(window.inspect(99), ReplayWindow::Verdict::Fresh);
    window.commit(99);
    CHECK_EQ(window.inspect(99), ReplayWindow::Verdict::Replayed);
}

TEST_CASE(replay_window_handles_48bit_wrap) {
    ReplayWindow window;
    const std::uint64_t top = kSeqSpaceMask;
    window.commit(top);
    CHECK_EQ(window.inspect(0), ReplayWindow::Verdict::Fresh);
    window.commit(0);
    CHECK_EQ(window.windowEnd(), 0u);
    CHECK_EQ(window.inspect(0), ReplayWindow::Verdict::Replayed);
    // On the 48-bit ring, top is exactly one step behind 0 and was seen
    // before the wrap, so its bit remains set after sliding.
    CHECK_EQ(window.inspect(top), ReplayWindow::Verdict::Replayed);
    window.commit(top);
    CHECK_EQ(window.inspect(top), ReplayWindow::Verdict::Replayed);
}
