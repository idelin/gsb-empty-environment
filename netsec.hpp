#pragma once

#include <array>
#include <chrono>
#include <cstdint>
#include <functional>
#include <list>
#include <optional>
#include <span>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

// 256-bit symmetric key shared between the management station and the agent.
using SecretKey = std::array<std::uint8_t, 32>;
// Response / HMAC-SHA-256 digest length.
inline constexpr std::size_t kResponseLen = 32;
using Response = std::array<std::uint8_t, kResponseLen>;

struct SessionConfig {
  // A challenge is only valid inside this time window.
  std::chrono::seconds challenge_ttl{30};
  // Bounded retransmission: a session is locked after this many failed
  // verification attempts against the current challenge.
  std::uint32_t max_attempts{3};
  // Random challenge nonce length in bytes.
  std::size_t challenge_bytes{32};
};

enum class VerifyResult {
  kOk,
  kUnknownSession,        // e.g. after agent restart or config rollback
  kExpired,               // outside the time window
  kKeyGenerationMismatch, // key rotated since the challenge was issued
  kClientMismatch,        // response presented from a different client address
  kChallengeMismatch,     // substituted / stale challenge nonce
  kSequenceRollback,      // sequence number below the expected one
  kResponseMismatch,      // authenticator does not match this challenge
  kReplay,                // challenge already consumed (one-time use)
  kAttemptsExceeded,      // retransmission bound reached, session locked
};

const char* toString(VerifyResult r) noexcept;

// Server-side challenge bound to one session, one sequence number and one
// key generation. Single-use: a fresh challenge must be issued after every
// successful response.
struct Challenge {
  std::uint64_t session_id = 0;
  std::uint32_t key_generation = 0;
  std::uint64_t sequence = 0;
  std::vector<std::uint8_t> nonce;
  std::chrono::system_clock::time_point expires_at{};
};

// Session authenticator for the device-management agent. All state is
// in-memory: restarting the agent drops every outstanding session, so
// responses captured before a restart can never be replayed afterwards.
class SessionAuthenticator {
 public:
  using Clock = std::function<std::chrono::system_clock::time_point()>;

  explicit SessionAuthenticator(SecretKey key, SessionConfig cfg = {},
                                Clock clock = defaultClock);

  static std::chrono::system_clock::time_point defaultClock();

  // Opens a session bound to `client_addr` and returns the first challenge.
  Challenge beginSession(std::string_view client_addr);

  // Issues the next challenge after a successful response. Returns
  // std::nullopt if the session is gone (expired, locked, invalidated).
  std::optional<Challenge> nextChallenge(std::uint64_t session_id);

  // Verifies a response against the *current* challenge of the session.
  // On kOk the challenge is consumed and cannot validate again.
  VerifyResult verifyResponse(std::uint64_t session_id,
                              std::string_view client_addr,
                              std::uint32_t key_generation,
                              std::uint64_t sequence,
                              std::span<const std::uint8_t> nonce,
                              std::span<const std::uint8_t> response);

  // Key rotation: bumps the key generation and invalidates all sessions.
  void rotateKey(SecretKey new_key);

  // Configuration rollback: invalidates all sessions (keys are restored
  // by the caller to match the rolled-back configuration).
  void notifyConfigRollback();

  std::uint32_t keyGeneration() const noexcept { return key_generation_; }
  std::size_t activeSessions() const;

  // Client-side helper: computes the response for a challenge.
  static Response computeResponse(const SecretKey& key, const Challenge& ch,
                                  std::string_view client_addr);

 private:
  struct Session {
    std::uint64_t id;
    std::string client_addr;
    std::uint32_t key_generation;
    std::uint64_t sequence;
    std::vector<std::uint8_t> nonce;
    std::chrono::system_clock::time_point expires_at;
    bool consumed = false;
    std::uint32_t failed_attempts = 0;
    bool locked = false;
  };

  SecretKey key_;
  SessionConfig cfg_;
  Clock clock_;
  std::uint32_t key_generation_ = 1;
  std::uint64_t next_session_id_ = 1;
  std::list<Session> sessions_;

  Session* find(std::uint64_t session_id);
  Challenge issueFor(Session& s);
};

// HMAC-SHA-256 over the canonical transcript, exposed for tests and for
// the client side of the agent.
Response hmacSha256(std::span<const std::uint8_t> key,
                    std::span<const std::uint8_t> data);

}  // namespace netsec
