#pragma once

#include <cstdint>
#include <optional>

namespace netsec {

inline constexpr std::uint8_t kLocalMinimumKeyBytes = 7;
inline constexpr std::uint8_t kLocalMaximumKeyBytes = 16;
inline constexpr std::uint8_t kSecureConnectionsKeyBytes = 16;
inline constexpr unsigned kMaxRoundTripsPerNegotiation = 3;

enum class NegotiationMode {
  BrEdr,
  SecureConnections,
};

enum class NegotiationState {
  Negotiating,
  Completed,
  Terminated,
};

enum class ProposalDecision {
  Accepted,
  RejectedRetry,
  RejectedTerminated,
};

enum class RejectionReason {
  None,
  BelowLocalMinimum,
  AboveLocalMaximum,
  SecureConnectionsRequiresSixteen,
  RoundTripLimitExceeded,
  NegotiationLocked,
  AlreadyTerminated,
};

struct ProposalResult {
  ProposalDecision decision = ProposalDecision::RejectedTerminated;
  RejectionReason reason = RejectionReason::None;
  std::optional<std::uint8_t> negotiated_key_bytes;
  unsigned round_trip = 0;

  bool accepted() const noexcept {
    return decision == ProposalDecision::Accepted;
  }

  bool rejected() const noexcept {
    return !accepted();
  }

  bool link_terminated() const noexcept {
    return decision == ProposalDecision::RejectedTerminated;
  }
};

class KeyLengthNegotiator {
 public:
  explicit KeyLengthNegotiator(
      NegotiationMode mode = NegotiationMode::BrEdr) noexcept;

  ProposalResult propose(std::uint8_t peer_key_bytes) noexcept;

  NegotiationMode mode() const noexcept;
  NegotiationState state() const noexcept;
  unsigned round_trips_used() const noexcept;
  unsigned round_trips_remaining() const noexcept;
  std::optional<std::uint8_t> negotiated_key_bytes() const noexcept;
  bool link_active() const noexcept;

 private:
  ProposalResult terminate(RejectionReason reason,
                           unsigned round_trip) noexcept;

  NegotiationMode mode_;
  NegotiationState state_ = NegotiationState::Negotiating;
  unsigned round_trips_used_ = 0;
  std::optional<std::uint8_t> negotiated_key_bytes_;
};

}
