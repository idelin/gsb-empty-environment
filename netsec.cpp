#include "netsec.hpp"

namespace netsec {

KeyLengthNegotiator::KeyLengthNegotiator(NegotiationMode mode) noexcept
    : mode_(mode) {}

ProposalResult KeyLengthNegotiator::propose(
    std::uint8_t peer_key_bytes) noexcept {
  if (state_ == NegotiationState::Terminated) {
    return {ProposalDecision::RejectedTerminated,
            RejectionReason::AlreadyTerminated, std::nullopt,
            round_trips_used_};
  }

  if (state_ == NegotiationState::Completed) {
    return terminate(RejectionReason::NegotiationLocked,
                     round_trips_used_);
  }

  ++round_trips_used_;
  const unsigned round_trip = round_trips_used_;

  if (round_trip > kMaxRoundTripsPerNegotiation) {
    return terminate(RejectionReason::RoundTripLimitExceeded, round_trip);
  }

  if (peer_key_bytes < kLocalMinimumKeyBytes) {
    return terminate(RejectionReason::BelowLocalMinimum, round_trip);
  }

  if (peer_key_bytes > kLocalMaximumKeyBytes) {
    return {ProposalDecision::RejectedRetry,
            RejectionReason::AboveLocalMaximum, std::nullopt, round_trip};
  }

  if (mode_ == NegotiationMode::SecureConnections &&
      peer_key_bytes != kSecureConnectionsKeyBytes) {
    return terminate(RejectionReason::SecureConnectionsRequiresSixteen,
                     round_trip);
  }

  state_ = NegotiationState::Completed;
  negotiated_key_bytes_ = peer_key_bytes;
  return {ProposalDecision::Accepted, RejectionReason::None,
          negotiated_key_bytes_, round_trip};
}

NegotiationMode KeyLengthNegotiator::mode() const noexcept {
  return mode_;
}

NegotiationState KeyLengthNegotiator::state() const noexcept {
  return state_;
}

unsigned KeyLengthNegotiator::round_trips_used() const noexcept {
  return round_trips_used_;
}

unsigned KeyLengthNegotiator::round_trips_remaining() const noexcept {
  if (state_ != NegotiationState::Negotiating) {
    return 0;
  }
  return kMaxRoundTripsPerNegotiation - round_trips_used_;
}

std::optional<std::uint8_t>
KeyLengthNegotiator::negotiated_key_bytes() const noexcept {
  return negotiated_key_bytes_;
}

bool KeyLengthNegotiator::link_active() const noexcept {
  return state_ != NegotiationState::Terminated;
}

ProposalResult KeyLengthNegotiator::terminate(RejectionReason reason,
                                              unsigned round_trip) noexcept {
  state_ = NegotiationState::Terminated;
  return {ProposalDecision::RejectedTerminated, reason, std::nullopt,
          round_trip};
}

}
