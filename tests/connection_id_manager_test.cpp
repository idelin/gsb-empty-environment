#include "netsec.hpp"

#include <cstdint>
#include <iostream>
#include <string>
#include <vector>

namespace {

int failures = 0;

void check(bool condition, const std::string& message) {
  if (!condition) {
    std::cerr << "FAIL: " << message << '\n';
    ++failures;
  }
}

void check_error(netsec::ConnectionIdError actual,
                 netsec::ConnectionIdError expected,
                 const std::string& message) {
  check(actual == expected,
        message + ": expected " + netsec::to_string(expected) + ", got " +
            netsec::to_string(actual));
}

netsec::ConnectionId cid(std::uint8_t first_byte, std::size_t length = 8) {
  std::vector<std::uint8_t> bytes(length);
  for (std::size_t index = 0; index < length; ++index) {
    bytes[index] = static_cast<std::uint8_t>(first_byte + index);
  }
  return netsec::ConnectionId{std::move(bytes)};
}

netsec::StatelessResetToken token(std::uint8_t value) {
  std::vector<std::uint8_t> bytes(netsec::kStatelessResetTokenLength, value);
  return netsec::StatelessResetToken{bytes};
}

void test_peer_id_limit() {
  netsec::ConnectionIdManager manager(2);

  auto result = manager.process_new_connection_id(0, 0, cid(1));
  check(result.ok(), "first peer CID is accepted");
  check(manager.active_peer_connection_id_count() == 1,
        "first peer CID is stored");

  result = manager.process_new_connection_id(1, 0, cid(11));
  check(result.ok(), "second peer CID is accepted");
  check(manager.active_peer_connection_id_count() == 2,
        "two peer CIDs fill the limit");

  result = manager.process_new_connection_id(2, 0, cid(21));
  check_error(result.error,
              netsec::ConnectionIdError::connection_id_limit_error,
              "third peer CID exceeds the limit");
  check(manager.active_peer_connection_id_count() == 2,
        "a rejected CID does not mutate the active set");
}

void test_retirement_makes_room_before_limit_check() {
  netsec::ConnectionIdManager manager(2);
  check(manager.process_new_connection_id(0, 0, cid(1)).ok(),
        "seed peer CID 0");
  check(manager.process_new_connection_id(1, 0, cid(11)).ok(),
        "seed peer CID 1");

  auto result = manager.process_new_connection_id(2, 2, cid(21));
  check(result.ok(), "retire_prior_to may first free enough slots");
  check(result.retired_peer_connection_ids == 2,
        "both older peer CIDs are retired");
  check(manager.active_peer_connection_id_count() == 1,
        "only the new peer CID remains active");
}

void test_duplicate_sequence_content() {
  netsec::ConnectionIdManager manager(4);
  const auto id = cid(1);
  const auto reset_token = token(0x31);

  auto result = manager.process_new_connection_id(7, 0, id, reset_token);
  check(result.ok(), "initial sequence 7 is accepted");

  result = manager.process_new_connection_id(7, 0, id, reset_token);
  check(result.ok(), "exact duplicate frame is accepted as a no-op");
  check(result.duplicate, "exact duplicate frame is marked duplicate");
  check(manager.active_peer_connection_id_count() == 1,
        "exact duplicate does not grow the active set");

  result = manager.process_new_connection_id(7, 3, id, reset_token);
  check(result.ok(), "same CID and token with another retire_prior_to is ignored");
  check(result.duplicate, "same CID and token is marked duplicate");
  check(manager.peer_retire_prior_to() == 0,
        "duplicate active CID does not change retirement state");

  result = manager.process_new_connection_id(7, 0, cid(5), reset_token);
  check_error(result.error,
              netsec::ConnectionIdError::protocol_violation,
              "same sequence with different CID is a protocol violation");

  result = manager.process_new_connection_id(7, 0, id, token(0x41));
  check_error(result.error,
              netsec::ConnectionIdError::protocol_violation,
              "same sequence with different reset token is a protocol violation");
}

void test_retire_prior_to_validation() {
  netsec::ConnectionIdManager manager(2);

  auto result = manager.process_new_connection_id(3, 4, cid(1));
  check_error(result.error,
              netsec::ConnectionIdError::frame_encoding_error,
              "retire_prior_to greater than sequence is malformed");
  check(manager.active_peer_connection_id_count() == 0,
        "malformed NEW_CONNECTION_ID does not store a CID");
}

void test_connection_id_length_validation() {
  netsec::ConnectionIdManager manager(2);

  auto result = manager.process_new_connection_id(0, 0, netsec::ConnectionId{});
  check_error(result.error,
              netsec::ConnectionIdError::frame_encoding_error,
              "zero-length CID on a non-zero-length connection is malformed");

  result = manager.process_new_connection_id(0, 0, cid(1, 21));
  check_error(result.error,
              netsec::ConnectionIdError::frame_encoding_error,
              "21-byte CID is malformed");

  result = manager.process_new_connection_id(0, 0, cid(1, 20));
  check(result.ok(), "20-byte CID is valid");
}

void test_zero_length_connection_rejects_new_cid() {
  netsec::ConnectionIdManager manager(
      netsec::ConnectionIdManagerOptions{
          4, true, false, {}});

  auto result = manager.process_new_connection_id(0, 0, cid(1));
  check_error(result.error,
              netsec::ConnectionIdError::protocol_violation,
              "zero-length connection rejects NEW_CONNECTION_ID");
  check(manager.active_peer_connection_id_count() == 0,
        "zero-length connection stores no peer CIDs");

  netsec::ConnectionIdManager initial_cid_manager(
      4, netsec::ConnectionId{});
  result = initial_cid_manager.process_new_connection_id(0, 0, cid(1));
  check_error(result.error,
              netsec::ConnectionIdError::protocol_violation,
              "zero-length initial CID selects zero-length mode");
}

void test_retire_sequence_validation_and_duplicates() {
  netsec::ConnectionIdManager manager(4);
  check(manager.next_local_connection_id_sequence() == 1,
        "the initial local CID occupies sequence zero");

  auto result = manager.process_retire_connection_id(1);
  check_error(result.error,
              netsec::ConnectionIdError::protocol_violation,
              "retiring an unissued sequence is a protocol violation");
  check(manager.retired_local_connection_id_count() == 0,
        "invalid retirement is not recorded");

  result = manager.process_retire_connection_id(0);
  check(result.ok(), "retiring the initial local CID is valid");
  check(result.replacement.has_value(),
        "a valid retirement schedules one replacement");
  check(manager.retired_local_connection_id_count() == 1,
        "valid retirement is counted once");

  result = manager.process_retire_connection_id(0);
  check(result.ok(), "duplicate retirement is a no-op");
  check(result.duplicate, "duplicate retirement is marked duplicate");
  check(!result.replacement.has_value(),
        "duplicate retirement schedules no replacement");
  check(manager.retired_local_connection_id_count() == 1,
        "duplicate retirement is not counted again");
}

void test_retire_flood_is_rate_limited() {
  netsec::ConnectionIdManager manager(16);

  // Sequence zero is the initial local CID. Reserve ten more issued IDs.
  for (std::size_t index = 0; index < 10; ++index) {
    const auto issued = manager.issue_connection_id();
    check(issued.sequence_number == index + 1,
          "issued local sequence numbers are contiguous");
  }

  std::vector<netsec::ConnectionIdFrame> flood;
  for (std::uint64_t sequence = 0; sequence < 10; ++sequence) {
    flood.emplace_back(netsec::RetireConnectionIdFrame{sequence});
  }

  auto result = manager.process_packet(flood);
  check(result.ok(), "the retirement flood is accepted");
  check(result.retired_local_connection_ids == 10,
        "all distinct retirements are recorded");
  check(result.replacement.has_value(),
        "the packet may emit one replacement");
  check(manager.replacement_connection_id_count() == 1,
        "a packet emits at most one replacement CID");
  check(manager.replacement_connection_id_count() <=
            manager.retired_local_connection_id_count(),
        "replacement count never exceeds retirement count");

  // Retiring more local IDs in later packets can replenish the spare pool,
  // but the cumulative replacement count stays bounded by retirements.
  result = manager.process_packet(
      {netsec::RetireConnectionIdFrame{10}, netsec::RetireConnectionIdFrame{0},
       netsec::RetireConnectionIdFrame{1}});
  check(result.ok(), "mixed duplicate retirements are accepted");
  check(result.replacement.has_value(),
        "one newly retired sequence permits one replacement");
  check(manager.replacement_connection_id_count() == 2,
        "only one replacement is generated for the second packet");
  check(manager.replacement_connection_id_count() <=
            manager.retired_local_connection_id_count(),
        "cumulative replacement count remains bounded");
}

void test_incremental_packet_processor() {
  netsec::ConnectionIdManager manager(4);
  manager.issue_connection_id();
  manager.issue_connection_id();

  auto packet = manager.begin_packet();
  check(packet.process(netsec::NewConnectionIdFrame{0, 0, cid(1)}) ==
            netsec::ConnectionIdError::none,
        "incremental processor accepts a NEW_CONNECTION_ID frame");
  check(packet.process(netsec::RetireConnectionIdFrame{0}) ==
            netsec::ConnectionIdError::none,
        "incremental processor accepts the first retirement");
  check(packet.process(netsec::RetireConnectionIdFrame{1}) ==
            netsec::ConnectionIdError::none,
        "incremental processor accepts the second retirement");

  const std::uint64_t next_sequence =
      manager.next_local_connection_id_sequence();
  const auto result = packet.finish();
  check(result.ok(), "incremental packet completes successfully");
  check(result.replacement.has_value(),
        "incremental packet emits a replacement");
  check(manager.next_local_connection_id_sequence() == next_sequence + 1,
        "only one local sequence is allocated");

  const auto repeated_finish = packet.finish();
  check(repeated_finish.replacement.has_value(),
        "repeated finish returns the same result");
  check(repeated_finish.replacement->sequence_number ==
            result.replacement->sequence_number,
        "repeated finish does not issue another CID");
}

void test_duplicate_new_cid_not_counted_against_limit() {
  netsec::ConnectionIdManager manager(1);

  auto result = manager.process_new_connection_id(0, 0, cid(1));
  check(result.ok(), "sequence 0 fills a one-ID limit");

  result = manager.process_new_connection_id(0, 0, cid(1));
  check(result.ok(), "duplicate sequence does not exceed the limit");
  check(result.duplicate, "duplicate sequence is reported");
  check(manager.active_peer_connection_id_count() == 1,
        "duplicate sequence does not create another entry");
}

}  // namespace

int main() {
  test_peer_id_limit();
  test_retirement_makes_room_before_limit_check();
  test_duplicate_sequence_content();
  test_retire_prior_to_validation();
  test_connection_id_length_validation();
  test_zero_length_connection_rejects_new_cid();
  test_retire_sequence_validation_and_duplicates();
  test_retire_flood_is_rate_limited();
  test_incremental_packet_processor();
  test_duplicate_new_cid_not_counted_against_limit();

  if (failures != 0) {
    std::cerr << failures << " test assertion(s) failed\n";
    return 1;
  }

  std::cout << "all connection ID manager tests passed\n";
  return 0;
}
