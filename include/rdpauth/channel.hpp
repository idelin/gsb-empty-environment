#pragma once

#include "rdpauth/types.hpp"

#include <cstdint>
#include <span>
#include <string_view>
#include <vector>

namespace rdp::auth {

class Negotiator;

// Encrypted channel for an accepted negotiation. Credentials may only be
// transmitted through this channel; it is only obtainable from
// Negotiator::establish_channel with an accepted, verifiably bound decision.
// Every transmitted frame is length-prefixed, encrypted with an
// HMAC-derived keystream and authenticated, so cleartext credentials never
// appear on the wire.
class SecureChannel {
public:
    SecureChannel(SecureChannel&&) noexcept = default;
    SecureChannel& operator=(SecureChannel&&) noexcept = default;
    SecureChannel(const SecureChannel&) = delete;
    SecureChannel& operator=(const SecureChannel&) = delete;

    void send_credentials(std::string_view username, std::string_view password);
    void send(std::span<const std::uint8_t> plaintext);

    // Test/diagnostic hook: exact bytes placed on the wire (ciphertext only).
    const std::vector<std::uint8_t>& wire_log() const noexcept { return wire_log_; }
    const SessionBinding& binding() const noexcept { return binding_; }

private:
    friend class Negotiator;
    SecureChannel(SessionBinding binding, Hash256 session_key);

    SessionBinding binding_;
    Hash256 session_key_{};
    std::uint64_t frame_counter_ = 0;
    std::vector<std::uint8_t> wire_log_;
};

} // namespace rdp::auth
