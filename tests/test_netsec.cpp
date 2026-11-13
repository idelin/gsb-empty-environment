#include "netsec.hpp"

#include <cstdint>
#include <cstdio>
#include <initializer_list>
#include <vector>

namespace {

int failures = 0;

void expect(bool condition, const char *message) {
  if (!condition) {
    std::fprintf(stderr, "FAIL: %s\n", message);
    ++failures;
  }
}

netsec::CanFrame cm_bam(std::uint8_t source, std::uint32_t total_bytes,
                        std::uint8_t total_packets, std::uint32_t pgn,
                        std::uint64_t timestamp_ms = 0u) {
  netsec::CanFrame frame;
  frame.id = netsec::make_can_id(source, netsec::TP_CM_PGN);
  frame.data[0] = netsec::TP_CM_BAM;
  frame.data[1] = static_cast<std::uint8_t>(total_bytes & 0xFFu);
  frame.data[2] = static_cast<std::uint8_t>((total_bytes >> 8u) & 0xFFu);
  frame.data[3] = total_packets;
  frame.data[4] = static_cast<std::uint8_t>(pgn & 0xFFu);
  frame.data[5] = static_cast<std::uint8_t>((pgn >> 8u) & 0xFFu);
  frame.data[6] = static_cast<std::uint8_t>((pgn >> 16u) & 0xFFu);
  frame.data[7] = 0xFFu;
  frame.dlc = 8u;
  frame.timestamp_ms = timestamp_ms;
  return frame;
}

netsec::CanFrame tp_dt(std::uint8_t source, std::uint8_t sequence,
                       std::initializer_list<std::uint8_t> bytes,
                       std::uint64_t timestamp_ms = 0u) {
  netsec::CanFrame frame;
  frame.id = netsec::make_can_id(source, netsec::TP_DT_PGN);
  frame.data[0] = sequence;
  std::uint8_t index = 1u;
  for (std::uint8_t value : bytes) {
    if (index >= 8u) {
      break;
    }
    frame.data[index++] = value;
  }
  frame.dlc = 8u;
  frame.timestamp_ms = timestamp_ms;
  return frame;
}

netsec::CanFrame address_claim(std::uint8_t source, std::uint64_t name,
                               std::uint64_t timestamp_ms = 0u) {
  netsec::CanFrame frame;
  frame.id = netsec::make_can_id(source, netsec::ADDRESS_CLAIM_PGN);
  for (std::uint8_t i = 0u; i < 8u; ++i) {
    frame.data[i] = static_cast<std::uint8_t>(name >> (8u * i));
  }
  frame.dlc = 8u;
  frame.timestamp_ms = timestamp_ms;
  return frame;
}

void test_packet_count_mismatch() {
  netsec::TpReassembler receiver;
  const auto result = receiver.on_frame(cm_bam(0x80u, 10u, 1u, 0x00FEF0u));

  expect(result.status == netsec::TpStatus::Rejected,
         "packet count mismatch is rejected");
  expect(result.reason == netsec::TpAbortReason::PacketCountMismatch,
         "packet count mismatch reason");
  expect(!receiver.has_session(0x80u),
         "packet count mismatch starts no session");

  const auto eight_bytes = receiver.on_frame(cm_bam(0x81u, 8u, 2u, 0x00FEF0u));
  expect(eight_bytes.status == netsec::TpStatus::Rejected,
         "total byte count below 9 is rejected");

  const auto too_many = receiver.on_frame(cm_bam(0x82u, 1786u, 0u, 0x00FEF0u));
  expect(too_many.status == netsec::TpStatus::Rejected,
         "total byte count above 1785 is rejected");
}

void test_sequence_errors_abort_session() {
  {
    netsec::TpReassembler receiver;
    receiver.on_frame(cm_bam(0x90u, 15u, 3u, 0x00FEF0u));
    receiver.on_frame(tp_dt(0x90u, 1u, {1u, 2u, 3u, 4u, 5u, 6u, 7u}, 0u));
    const auto result =
        receiver.on_frame(tp_dt(0x90u, 3u, {15u, 16u, 17u}, 10u));

    expect(result.status == netsec::TpStatus::Aborted,
           "out-of-order packet aborts");
    expect(result.reason == netsec::TpAbortReason::OutOfSequence,
           "out-of-order reason");
    expect(!receiver.has_session(0x90u), "out-of-order clears session");
  }

  {
    netsec::TpReassembler receiver;
    receiver.on_frame(cm_bam(0x91u, 15u, 3u, 0x00FEF0u));
    receiver.on_frame(tp_dt(0x91u, 1u, {1u, 2u, 3u, 4u, 5u, 6u, 7u}, 0u));
    const auto result = receiver.on_frame(tp_dt(0x91u, 1u, {8u, 9u, 10u}, 10u));

    expect(result.status == netsec::TpStatus::Aborted,
           "duplicate packet aborts");
    expect(result.reason == netsec::TpAbortReason::DuplicateSequence,
           "duplicate reason");
    expect(!receiver.has_session(0x91u), "duplicate clears session");
  }
}

void test_second_bam_replaces_first() {
  netsec::TpReassembler receiver;
  receiver.on_frame(cm_bam(0xA0u, 15u, 3u, 0x00AAA0u));
  receiver.on_frame(
      tp_dt(0xA0u, 1u, {0x10u, 0x11u, 0x12u, 0x13u, 0x14u, 0x15u, 0x16u}, 0u));

  const auto replacement =
      receiver.on_frame(cm_bam(0xA0u, 9u, 2u, 0x00BBB0u, 10u));
  expect(replacement.status == netsec::TpStatus::Started,
         "second BAM starts a session");
  expect(replacement.replaced,
         "second BAM reports replacement of the old session");
  expect(receiver.session_count() == 1u,
         "only one BAM session exists per source");

  receiver.on_frame(
      tp_dt(0xA0u, 1u, {0x20u, 0x21u, 0x22u, 0x23u, 0x24u, 0x25u, 0x26u}, 20u));
  const auto completion =
      receiver.on_frame(tp_dt(0xA0u, 2u, {0x27u, 0x28u}, 30u));
  expect(completion.status == netsec::TpStatus::Complete,
         "replacement BAM completes");
  expect(completion.pgn == 0x00BBB0u, "completion belongs to replacement PGN");
  expect(completion.payload.size() == 9u,
         "replacement payload has declared size");
  expect(completion.payload[0] == 0x20u && completion.payload[8] == 0x28u,
         "replacement payload is reassembled");
}

void test_timeout() {
  {
    netsec::TpReassembler receiver;
    receiver.on_frame(cm_bam(0xB0u, 21u, 3u, 0x00FEF0u, 0u));
    receiver.on_frame(tp_dt(0xB0u, 1u, {1u, 2u, 3u, 4u, 5u, 6u, 7u}, 0u));
    const auto at_limit = receiver.on_frame(
        tp_dt(0xB0u, 2u, {8u, 9u, 10u, 11u, 12u, 13u, 14u}, 750u));
    expect(at_limit.status == netsec::TpStatus::InProgress,
           "exactly 750 ms is accepted");
    expect(receiver.has_session(0xB0u), "session survives exactly 750 ms");
    const auto too_late =
        receiver.on_frame(tp_dt(0xB0u, 3u, {15u, 16u, 17u}, 1501u));
    expect(too_late.status == netsec::TpStatus::Aborted, "751 ms gap aborts");
    expect(too_late.reason == netsec::TpAbortReason::Timeout, "timeout reason");
    expect(!receiver.has_session(0xB0u), "timeout clears session");
  }

  {
    netsec::TpReassembler receiver;
    receiver.on_frame(cm_bam(0xB1u, 15u, 3u, 0x00FEF0u, 0u));
    receiver.on_frame(tp_dt(0xB1u, 1u, {1u, 2u, 3u, 4u, 5u, 6u, 7u}, 0u));
    const auto result =
        receiver.on_frame(tp_dt(0xB1u, 2u, {8u, 9u, 10u}, 751u));
    expect(result.status == netsec::TpStatus::Aborted,
           "first packet gap over 750 ms aborts");
  }

  {
    netsec::TpReassembler receiver;
    receiver.on_frame(cm_bam(0xB2u, 15u, 3u, 0x00FEF0u, 0u));
    const std::size_t expired = receiver.expire_sessions(751u);
    expect(expired == 1u, "expire_sessions reports timeout");
    expect(!receiver.has_session(0xB2u), "expire_sessions clears idle session");
  }
}

void test_address_claim_conflict_clears_session() {
  netsec::TpReassembler receiver;

  const auto high_claim = receiver.on_frame(address_claim(0xC0u, 0x200u));
  expect(high_claim.status == netsec::TpStatus::AddressClaimAccepted,
         "first claim is accepted");

  receiver.on_frame(cm_bam(0xC0u, 15u, 3u, 0x00FEF0u));
  receiver.on_frame(tp_dt(0xC0u, 1u, {1u, 2u, 3u, 4u, 5u, 6u, 7u}, 0u));
  expect(receiver.has_session(0xC0u), "high NAME owns an active session");

  const auto low_claim = receiver.on_frame(address_claim(0xC0u, 0x100u));
  expect(low_claim.status == netsec::TpStatus::AddressClaimAccepted,
         "lower NAME wins the address");
  expect(receiver.name_for_address(0xC0u).value_or(0u) == 0x100u,
         "lower NAME is retained");
  expect(!receiver.has_session(0xC0u), "previous owner's session is cleared");

  const auto higher_loses = receiver.on_frame(address_claim(0xC0u, 0x300u));
  expect(higher_loses.status == netsec::TpStatus::AddressClaimRejected,
         "higher NAME is rejected");
  expect(higher_loses.reason == netsec::TpAbortReason::AddressClaimConflict,
         "address conflict reason");
  expect(receiver.name_for_address(0xC0u).value_or(0u) == 0x100u,
         "lower NAME keeps ownership");
  expect(!receiver.has_session(0xC0u), "losing claimant's session is cleared");

  const auto duplicate = receiver.on_frame(address_claim(0xC0u, 0x100u));
  expect(duplicate.status == netsec::TpStatus::AddressClaimAccepted,
         "same NAME duplicate claim is harmless");
}

void test_reserved_source_addresses_are_ignored() {
  netsec::TpReassembler receiver;
  const auto null_cm =
      receiver.on_frame(cm_bam(netsec::J1939_NULL_ADDRESS, 9u, 2u, 0x00FEF0u));
  const auto global_dt =
      receiver.on_frame(tp_dt(netsec::J1939_GLOBAL_ADDRESS, 1u, {1u, 2u, 3u}));
  const auto global_cm = receiver.on_frame(
      cm_bam(netsec::J1939_GLOBAL_ADDRESS, 9u, 2u, 0x00FEF0u));

  expect(null_cm.status == netsec::TpStatus::Ignored,
         "TP frame from 254 is ignored");
  expect(global_dt.status == netsec::TpStatus::Ignored,
         "TP.DT frame from 255 is ignored");
  expect(global_cm.status == netsec::TpStatus::Ignored,
         "TP.CM frame from 255 is ignored");
  expect(!receiver.has_session(netsec::J1939_NULL_ADDRESS),
         "reserved source starts no session");
  expect(!receiver.has_session(netsec::J1939_GLOBAL_ADDRESS),
         "global source starts no session");
}

void test_exact_payload_length() {
  netsec::TpReassembler receiver;
  receiver.on_frame(cm_bam(0xD0u, 9u, 2u, 0x00FEF0u));
  receiver.on_frame(
      tp_dt(0xD0u, 1u, {0xAAu, 0xBBu, 0xCCu, 0xDDu, 0xEEu, 0xFFu, 0x11u}));
  const auto result = receiver.on_frame(
      tp_dt(0xD0u, 2u, {0x22u, 0x33u, 0x44u, 0x55u, 0x66u, 0x77u, 0x88u}));

  expect(result.status == netsec::TpStatus::Complete,
         "two-packet message completes");
  expect(result.payload.size() == 9u, "padding bytes are discarded");
  expect(result.data.size() == 9u, "result data mirrors payload");
  expect(result.message.has_value() && result.message->payload.size() == 9u,
         "message payload is populated");
  expect(result.payload[0] == 0xAAu && result.payload[8] == 0x33u,
         "payload content is correct");
}

} // namespace

int main() {
  test_packet_count_mismatch();
  test_sequence_errors_abort_session();
  test_second_bam_replaces_first();
  test_timeout();
  test_address_claim_conflict_clears_session();
  test_reserved_source_addresses_are_ignored();
  test_exact_payload_length();

  if (failures != 0) {
    std::fprintf(stderr, "%d test failure(s)\n", failures);
    return 1;
  }
  std::printf("all netsec tests passed\n");
  return 0;
}
