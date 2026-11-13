#include "netsec.hpp"

#include <cstdint>
#include <cstdio>

namespace {

int failures = 0;

void check(bool condition, const char* expression, int line) {
  if (!condition) {
    std::fprintf(stderr, "FAIL line %d: %s\n", line, expression);
    ++failures;
  }
}

#define CHECK(expression) check((expression), #expression, __LINE__)

void test_one_byte_proposal_rejects_and_terminates() {
  netsec::KeyLengthNegotiator negotiator;

  const auto result = negotiator.propose(1);

  CHECK(result.rejected());
  CHECK(result.link_terminated());
  CHECK(result.reason == netsec::RejectionReason::BelowLocalMinimum);
  CHECK(result.round_trip == 1);
  CHECK(negotiator.state() == netsec::NegotiationState::Terminated);
  CHECK(!negotiator.link_active());
  CHECK(!negotiator.negotiated_key_bytes().has_value());

  const auto later = negotiator.propose(16);
  CHECK(later.link_terminated());
  CHECK(later.reason == netsec::RejectionReason::AlreadyTerminated);
  CHECK(!negotiator.negotiated_key_bytes().has_value());
}

void test_gradual_downgrade_never_changes_accepted_length() {
  netsec::KeyLengthNegotiator negotiator;

  const auto accepted = negotiator.propose(16);
  CHECK(accepted.accepted());
  CHECK(accepted.negotiated_key_bytes == std::optional<std::uint8_t>(16));
  CHECK(negotiator.state() == netsec::NegotiationState::Completed);

  const auto downgrade_to_12 = negotiator.propose(12);
  CHECK(downgrade_to_12.link_terminated());
  CHECK(downgrade_to_12.reason == netsec::RejectionReason::NegotiationLocked);
  CHECK(negotiator.negotiated_key_bytes() == std::optional<std::uint8_t>(16));

  const auto downgrade_to_8 = negotiator.propose(8);
  CHECK(downgrade_to_8.link_terminated());
  CHECK(downgrade_to_8.reason == netsec::RejectionReason::AlreadyTerminated);
  CHECK(negotiator.negotiated_key_bytes() == std::optional<std::uint8_t>(16));

  const auto downgrade_to_one = negotiator.propose(1);
  CHECK(downgrade_to_one.link_terminated());
  CHECK(downgrade_to_one.reason ==
        netsec::RejectionReason::AlreadyTerminated);
  CHECK(negotiator.negotiated_key_bytes() == std::optional<std::uint8_t>(16));
}

void test_downgrade_after_floor_value_is_locked() {
  netsec::KeyLengthNegotiator negotiator;

  const auto accepted = negotiator.propose(7);
  CHECK(accepted.accepted());
  CHECK(accepted.negotiated_key_bytes == std::optional<std::uint8_t>(7));

  const auto shorter = negotiator.propose(6);
  CHECK(shorter.link_terminated());
  CHECK(shorter.reason == netsec::RejectionReason::NegotiationLocked);
  CHECK(negotiator.negotiated_key_bytes() == std::optional<std::uint8_t>(7));
}

void test_fourth_round_is_rejected_and_link_terminates() {
  netsec::KeyLengthNegotiator negotiator;

  for (unsigned round = 1; round <= netsec::kMaxRoundTripsPerNegotiation;
       ++round) {
    const auto result = negotiator.propose(17);
    CHECK(result.rejected());
    CHECK(!result.link_terminated());
    CHECK(result.decision == netsec::ProposalDecision::RejectedRetry);
    CHECK(result.reason == netsec::RejectionReason::AboveLocalMaximum);
    CHECK(result.round_trip == round);
    CHECK(negotiator.state() == netsec::NegotiationState::Negotiating);
    CHECK(negotiator.round_trips_used() == round);
    CHECK(negotiator.round_trips_remaining() ==
          netsec::kMaxRoundTripsPerNegotiation - round);
  }

  const auto fourth = negotiator.propose(16);
  CHECK(fourth.link_terminated());
  CHECK(fourth.reason == netsec::RejectionReason::RoundTripLimitExceeded);
  CHECK(fourth.round_trip == 4);
  CHECK(negotiator.state() == netsec::NegotiationState::Terminated);
  CHECK(!negotiator.link_active());
  CHECK(!negotiator.negotiated_key_bytes().has_value());
  CHECK(negotiator.round_trips_remaining() == 0);

  const auto after_termination = negotiator.propose(16);
  CHECK(after_termination.link_terminated());
  CHECK(after_termination.reason ==
        netsec::RejectionReason::AlreadyTerminated);
  CHECK(!negotiator.negotiated_key_bytes().has_value());
}

void test_secure_connections_requires_sixteen_bytes() {
  netsec::KeyLengthNegotiator accepted_negotiator(
      netsec::NegotiationMode::SecureConnections);

  const auto accepted = accepted_negotiator.propose(16);
  CHECK(accepted.accepted());
  CHECK(accepted.negotiated_key_bytes == std::optional<std::uint8_t>(16));
  CHECK(accepted_negotiator.state() == netsec::NegotiationState::Completed);

  const auto changed = accepted_negotiator.propose(12);
  CHECK(changed.link_terminated());
  CHECK(changed.reason == netsec::RejectionReason::NegotiationLocked);
  CHECK(accepted_negotiator.negotiated_key_bytes() ==
        std::optional<std::uint8_t>(16));

  netsec::KeyLengthNegotiator short_negotiator(
      netsec::NegotiationMode::SecureConnections);
  const auto short_result = short_negotiator.propose(15);
  CHECK(short_result.link_terminated());
  CHECK(short_result.reason ==
        netsec::RejectionReason::SecureConnectionsRequiresSixteen);
  CHECK(!short_negotiator.negotiated_key_bytes().has_value());

  netsec::KeyLengthNegotiator floor_negotiator(
      netsec::NegotiationMode::SecureConnections);
  const auto floor_result = floor_negotiator.propose(7);
  CHECK(floor_result.link_terminated());
  CHECK(floor_result.reason ==
        netsec::RejectionReason::SecureConnectionsRequiresSixteen);
  CHECK(!floor_negotiator.negotiated_key_bytes().has_value());

  netsec::KeyLengthNegotiator tiny_negotiator(
      netsec::NegotiationMode::SecureConnections);
  const auto tiny_result = tiny_negotiator.propose(1);
  CHECK(tiny_result.link_terminated());
  CHECK(tiny_result.reason == netsec::RejectionReason::BelowLocalMinimum);
  CHECK(!tiny_negotiator.negotiated_key_bytes().has_value());
}

void test_legacy_accepts_lengths_from_floor_through_maximum() {
  for (std::uint8_t length = netsec::kLocalMinimumKeyBytes;
       length <= netsec::kLocalMaximumKeyBytes; ++length) {
    netsec::KeyLengthNegotiator negotiator;
    const auto result = negotiator.propose(length);
    CHECK(result.accepted());
    CHECK(result.negotiated_key_bytes == std::optional<std::uint8_t>(length));
    CHECK(negotiator.negotiated_key_bytes() ==
          std::optional<std::uint8_t>(length));
  }
}

}

int main() {
  test_one_byte_proposal_rejects_and_terminates();
  test_gradual_downgrade_never_changes_accepted_length();
  test_downgrade_after_floor_value_is_locked();
  test_fourth_round_is_rejected_and_link_terminates();
  test_secure_connections_requires_sixteen_bytes();
  test_legacy_accepts_lengths_from_floor_through_maximum();

  if (failures != 0) {
    std::fprintf(stderr, "%d check(s) failed\n", failures);
    return 1;
  }

  std::printf("all key length negotiation tests passed\n");
  return 0;
}
