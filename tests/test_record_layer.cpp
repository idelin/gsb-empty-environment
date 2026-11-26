// SPDX-License-Identifier: MIT

#include "dtls13/record_layer.hpp"
#include "test_framework.hpp"

#include <array>
#include <memory>

using namespace dtls13;

namespace {

std::array<std::uint8_t, 32> makeKey(std::uint8_t fill) {
    std::array<std::uint8_t, 32> key{};
    key.fill(fill);
    return key;
}

std::array<std::uint8_t, 12> makeIv(std::uint8_t fill) {
    std::array<std::uint8_t, 12> iv{};
    iv.fill(fill);
    return iv;
}

Bytes plaintextRecord(std::uint16_t epoch,
                      std::uint64_t seq,
                      const Bytes& body) {
    EncodeOptions options;
    options.epoch = epoch;
    options.seq48 = seq;
    options.body_length = static_cast<std::uint16_t>(body.size());
    Bytes record = encodeUnifiedHeader(ContentType::Handshake, options);
    record.insert(record.end(), body.begin(), body.end());
    return record;
}

Bytes protectedRecord(std::uint16_t epoch,
                      std::uint64_t seq,
                      bool seq16,
                      const Bytes& inner_payload,
                      ContentType inner_type) {
    ReferenceAead aead(makeKey(static_cast<std::uint8_t>(0x30 + epoch)));
    XorNonceMasker masker(makeIv(static_cast<std::uint8_t>(0x60 + epoch)));

    Bytes plaintext = inner_payload;
    plaintext.push_back(static_cast<std::uint8_t>(inner_type));

    EncodeOptions options;
    options.epoch = epoch;
    options.seq48 = seq;
    options.use_16bit_seq = seq16;
    return buildProtectedRecord(aead, masker, ContentType::ApplicationData,
                                options, ByteView(plaintext));
}

RecordLayer makeLayerWithEpoch(std::uint16_t epoch) {
    RecordLayer layer;
    auto aead = std::make_unique<ReferenceAead>(
        makeKey(static_cast<std::uint8_t>(0x30 + epoch)));
    auto masker = std::make_unique<XorNonceMasker>(
        makeIv(static_cast<std::uint8_t>(0x60 + epoch)));
    layer.installEpochKeys(epoch, std::move(aead), std::move(masker));
    return layer;
}

}  // namespace

TEST_CASE(protected_record_roundtrip_and_inner_type) {
    RecordLayer layer = makeLayerWithEpoch(2);
    const Bytes payload = {0xDE, 0xAD, 0xBE, 0xEF};
    Bytes record = protectedRecord(2, 0, false, payload,
                                   ContentType::Handshake);
    auto result = layer.ingest(ByteView(record));
    CHECK_EQ(result.code, ErrorCode::Ok);
    CHECK_EQ(result.records.size(), 1u);
    CHECK(result.records[0].was_encrypted);
    CHECK_EQ(result.records[0].content_type, ContentType::Handshake);
    CHECK(result.records[0].payload == payload);
    CHECK_EQ(result.records[0].seq48, 0u);
}

TEST_CASE(protected_record_16bit_seq_roundtrip) {
    RecordLayer layer = makeLayerWithEpoch(4);
    const Bytes payload = {7};
    Bytes record = protectedRecord(4, 0x01AB, true, payload,
                                   ContentType::ApplicationData);
    auto result = layer.ingest(ByteView(record));
    CHECK_EQ(result.code, ErrorCode::Ok);
    CHECK_EQ(result.records[0].seq48, 0x01ABu);
}

TEST_CASE(replayed_protected_record_is_rejected) {
    RecordLayer layer = makeLayerWithEpoch(2);
    Bytes record = protectedRecord(2, 7, false, {1, 2, 3},
                                   ContentType::ApplicationData);
    auto first = layer.ingest(ByteView(record));
    CHECK_EQ(first.code, ErrorCode::Ok);
    auto second = layer.ingest(ByteView(record));
    CHECK_EQ(second.code, ErrorCode::Replayed);
    CHECK(second.records.empty());
}

TEST_CASE(aead_failure_does_not_advance_replay_window) {
    RecordLayer layer = makeLayerWithEpoch(2);
    Bytes record = protectedRecord(2, 3, false, {9, 9, 9},
                                   ContentType::ApplicationData);
    // Flip a ciphertext byte (after the header) to break the tag.
    const std::size_t body_offset = record.size() - 4;
    record[body_offset] =
        static_cast<std::uint8_t>(record[body_offset] ^ 0xFF);

    auto tampered = layer.ingest(ByteView(record));
    CHECK_EQ(tampered.code, ErrorCode::AeadDecryptFailed);

    // No window state may exist for seq 3 after a failed AEAD check.
    const ReplayWindow* window = layer.replayTable().find(2);
    CHECK(window != nullptr);
    CHECK(!window->hasSeenAny());

    Bytes genuine = protectedRecord(2, 3, false, {9, 9, 9},
                                    ContentType::ApplicationData);
    auto accepted = layer.ingest(ByteView(genuine));
    CHECK_EQ(accepted.code, ErrorCode::Ok);
    CHECK_EQ(accepted.records[0].seq48, 3u);
}

TEST_CASE(epochs_have_independent_sequence_spaces) {
    RecordLayer layer;
    const std::uint16_t epochs[] = {2u, 3u};
    for (std::uint16_t epoch : epochs) {
        auto aead = std::make_unique<ReferenceAead>(
            makeKey(static_cast<std::uint8_t>(0x30 + epoch)));
        auto masker = std::make_unique<XorNonceMasker>(
            makeIv(static_cast<std::uint8_t>(0x60 + epoch)));
        CHECK_EQ(layer.installEpochKeys(epoch, std::move(aead),
                                        std::move(masker)),
                 ErrorCode::Ok);
    }

    Bytes epoch2 = protectedRecord(2, 5, false, {1},
                                   ContentType::ApplicationData);
    Bytes epoch3 = protectedRecord(3, 5, false, {2},
                                   ContentType::ApplicationData);
    CHECK_EQ(layer.ingest(ByteView(epoch2)).code, ErrorCode::Ok);
    CHECK_EQ(layer.ingest(ByteView(epoch3)).code, ErrorCode::Ok);

    // Truncated seq 5 is consumed independently in both epochs.
    CHECK_EQ(layer.ingest(ByteView(epoch2)).code, ErrorCode::Replayed);
    CHECK_EQ(layer.ingest(ByteView(epoch3)).code, ErrorCode::Replayed);

    CHECK_EQ(layer.replayTable().find(2)->windowEnd(), 5u);
    CHECK_EQ(layer.replayTable().find(3)->windowEnd(), 5u);
}

TEST_CASE(stale_plaintext_epoch_records_are_discarded) {
    RecordLayer layer;
    Bytes newer = plaintextRecord(2, 0, {1});
    CHECK_EQ(layer.ingest(ByteView(newer)).code, ErrorCode::Ok);

    Bytes older = plaintextRecord(0, 0, {2});
    CHECK_EQ(layer.ingest(ByteView(older)).code, ErrorCode::StaleEpoch);

    Bytes same = plaintextRecord(2, 1, {3});
    CHECK_EQ(layer.ingest(ByteView(same)).code, ErrorCode::Ok);
}

TEST_CASE(plaintext_replay_is_tracked_per_epoch) {
    RecordLayer layer;
    Bytes record = plaintextRecord(0, 4, {1});
    CHECK_EQ(layer.ingest(ByteView(record)).code, ErrorCode::Ok);
    CHECK_EQ(layer.ingest(ByteView(record)).code, ErrorCode::Replayed);
}

TEST_CASE(datagram_with_multiple_records_processes_all) {
    Bytes datagram = plaintextRecord(0, 0, {1});
    Bytes second = plaintextRecord(0, 1, {2, 2});
    datagram.insert(datagram.end(), second.begin(), second.end());

    RecordLayer layer;
    auto result = layer.ingest(ByteView(datagram));
    CHECK_EQ(result.code, ErrorCode::Ok);
    CHECK_EQ(result.records.size(), 2u);
    CHECK_EQ(result.records[0].payload.size(), 1u);
    CHECK_EQ(result.records[1].payload.size(), 2u);
}

TEST_CASE(length_elided_record_consumes_remainder_of_datagram) {
    EncodeOptions options;
    options.epoch = 0;
    options.seq48 = 0;
    options.include_length = false;
    const Bytes body = {1, 2, 3, 4};
    Bytes record = encodeUnifiedHeader(ContentType::Handshake, options);
    record.insert(record.end(), body.begin(), body.end());

    RecordLayer layer;
    auto result = layer.ingest(ByteView(record));
    CHECK_EQ(result.code, ErrorCode::Ok);
    CHECK_EQ(result.records.size(), 1u);
    CHECK(result.records[0].payload == body);
}

TEST_CASE(oversized_declared_record_body_is_rejected) {
    EncodeOptions options;
    options.epoch = 0;
    options.seq48 = 0;
    options.body_length = 0xFFFF;
    Bytes record = encodeUnifiedHeader(ContentType::Handshake, options);
    // No body follows: the declared length cannot be satisfied.
    RecordLayer layer;
    auto result = layer.ingest(ByteView(record));
    CHECK(result.code == ErrorCode::TruncatedDatagram ||
          result.code == ErrorCode::MalformedRecord);
}
