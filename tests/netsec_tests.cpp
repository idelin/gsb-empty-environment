#include "netsec.hpp"

#include <cstdio>
#include <cstdlib>
#include <string_view>

namespace {

using netsec::Challenge;
using netsec::Response;
using netsec::SecretKey;
using netsec::SessionAuthenticator;
using netsec::SessionConfig;
using netsec::VerifyResult;

int failures = 0;

#define CHECK(cond)                                              \
  do {                                                           \
    if (!(cond)) {                                               \
      std::fprintf(stderr, "FAIL %s:%d: %s\n", __FILE__,         \
                   __LINE__, #cond);                             \
      ++failures;                                                \
    }                                                            \
  } while (0)

SecretKey makeKey(std::uint8_t seed) {
  SecretKey k{};
  for (std::size_t i = 0; i < k.size(); ++i)
    k[i] = static_cast<std::uint8_t>(seed + i);
  return k;
}

// Controllable clock so tests can move time deterministically.
struct FakeClock {
  std::chrono::system_clock::time_point now{
      std::chrono::system_clock::time_point{std::chrono::seconds{1'700'000'000}}};
  SessionAuthenticator::Clock fn() {
    return [this] { return now; };
  }
  void advance(std::chrono::seconds s) { now += s; }
};

constexpr std::string_view kClient = "192.0.2.10:53210";

Response respond(const SecretKey& key, const Challenge& ch) {
  return SessionAuthenticator::computeResponse(key, ch, kClient);
}

// RFC 4231 test case 1: HMAC-SHA-256.
void testHmacVector() {
  std::array<std::uint8_t, 20> key{};
  key.fill(0x0b);
  const char data[] = "Hi There";
  auto mac = netsec::hmacSha256(
      std::span<const std::uint8_t>(key),
      std::span<const std::uint8_t>(
          reinterpret_cast<const std::uint8_t*>(data), 8));
  static const std::uint8_t expected[32] = {
      0xb0, 0x34, 0x4c, 0x61, 0xd8, 0xdb, 0x38, 0x53, 0x5c, 0xa8, 0xaf,
      0xce, 0xaf, 0x0b, 0xf1, 0x2b, 0x88, 0x1d, 0xc2, 0x00, 0xc9, 0x83,
      0x3d, 0xa7, 0x26, 0xe9, 0x37, 0x6c, 0x2e, 0x32, 0xcf, 0xf7};
  CHECK(std::equal(mac.begin(), mac.end(), std::begin(expected)));
}

// Legitimate login: challenge/response round trips succeed repeatedly.
void testLegitLogin() {
  FakeClock clock;
  SecretKey key = makeKey(1);
  SessionAuthenticator auth(key, {}, clock.fn());

  Challenge ch = auth.beginSession(kClient);
  CHECK(auth.verifyResponse(ch.session_id, kClient, ch.key_generation,
                            ch.sequence, ch.nonce, respond(key, ch)) ==
        VerifyResult::kOk);

  auto next = auth.nextChallenge(ch.session_id);
  CHECK(next.has_value());
  CHECK(next->sequence == ch.sequence + 1);
  CHECK(next->nonce != ch.nonce);
  CHECK(auth.verifyResponse(next->session_id, kClient, next->key_generation,
                            next->sequence, next->nonce,
                            respond(key, *next)) == VerifyResult::kOk);
}

// Captured response replayed against the same challenge must be rejected.
void testPacketReplay() {
  FakeClock clock;
  SecretKey key = makeKey(2);
  SessionAuthenticator auth(key, {}, clock.fn());

  Challenge ch = auth.beginSession(kClient);
  Response captured = respond(key, ch);  // attacker sniffs the exchange
  CHECK(auth.verifyResponse(ch.session_id, kClient, ch.key_generation,
                            ch.sequence, ch.nonce, captured) ==
        VerifyResult::kOk);

  // Exact replay of the captured packet.
  CHECK(auth.verifyResponse(ch.session_id, kClient, ch.key_generation,
                            ch.sequence, ch.nonce, captured) ==
        VerifyResult::kReplay);

  // Replay against the *next* challenge: nonce and sequence moved on.
  auto next = auth.nextChallenge(ch.session_id);
  CHECK(next.has_value());
  CHECK(auth.verifyResponse(next->session_id, kClient, next->key_generation,
                            next->sequence, next->nonce, captured) ==
        VerifyResult::kResponseMismatch);
  // Replaying with the old nonce against the new challenge fails too.
  CHECK(auth.verifyResponse(next->session_id, kClient, next->key_generation,
                            next->sequence, ch.nonce, captured) ==
        VerifyResult::kChallengeMismatch);
}

// Attacker swaps in a different challenge nonce.
void testChallengeSubstitution() {
  FakeClock clock;
  SecretKey key = makeKey(3);
  SessionAuthenticator auth(key, {}, clock.fn());

  Challenge real = auth.beginSession(kClient);
  Challenge forged = real;
  forged.nonce[0] ^= 0xff;  // attacker substitutes the challenge
  CHECK(auth.verifyResponse(forged.session_id, kClient,
                            forged.key_generation, forged.sequence,
                            forged.nonce, respond(key, forged)) ==
        VerifyResult::kChallengeMismatch);

  // Response computed for the attacker's challenge but presented with the
  // real nonce still fails: it does not match *this* challenge.
  CHECK(auth.verifyResponse(real.session_id, kClient, real.key_generation,
                            real.sequence, real.nonce,
                            respond(key, forged)) ==
        VerifyResult::kResponseMismatch);
}

// Attacker rolls the sequence number back to reuse an old response.
void testSequenceRollback() {
  FakeClock clock;
  SecretKey key = makeKey(4);
  SessionAuthenticator auth(key, {}, clock.fn());

  Challenge ch0 = auth.beginSession(kClient);
  Response r0 = respond(key, ch0);
  CHECK(auth.verifyResponse(ch0.session_id, kClient, ch0.key_generation,
                            ch0.sequence, ch0.nonce, r0) == VerifyResult::kOk);

  auto ch1 = auth.nextChallenge(ch0.session_id);
  CHECK(ch1.has_value());
  CHECK(auth.verifyResponse(ch1->session_id, kClient, ch1->key_generation,
                            ch1->sequence, ch1->nonce,
                            respond(key, *ch1)) == VerifyResult::kOk);

  // Old sequence number with the old response: rollback is detected.
  CHECK(auth.verifyResponse(ch0.session_id, kClient, ch0.key_generation,
                            ch0.sequence, ch0.nonce, r0) ==
        VerifyResult::kSequenceRollback);
}

// Key rotation invalidates in-flight sessions and old responses.
void testKeyRotation() {
  FakeClock clock;
  SecretKey old_key = makeKey(5);
  SecretKey new_key = makeKey(105);
  SessionAuthenticator auth(old_key, {}, clock.fn());

  Challenge ch = auth.beginSession(kClient);
  Response old_response = respond(old_key, ch);

  auth.rotateKey(new_key);
  CHECK(auth.keyGeneration() == 2);
  CHECK(auth.activeSessions() == 0);

  // Old response is worthless after rotation.
  CHECK(auth.verifyResponse(ch.session_id, kClient, ch.key_generation,
                            ch.sequence, ch.nonce, old_response) ==
        VerifyResult::kUnknownSession);

  // New sessions work under the new key and generation.
  Challenge fresh = auth.beginSession(kClient);
  CHECK(fresh.key_generation == 2);
  CHECK(auth.verifyResponse(fresh.session_id, kClient, fresh.key_generation,
                            fresh.sequence, fresh.nonce,
                            respond(new_key, fresh)) == VerifyResult::kOk);
  // A response computed with the retired key is rejected.
  Challenge fresh2 = *auth.nextChallenge(fresh.session_id);
  CHECK(auth.verifyResponse(fresh2.session_id, kClient,
                            fresh2.key_generation, fresh2.sequence,
                            fresh2.nonce, respond(old_key, fresh2)) ==
        VerifyResult::kResponseMismatch);
}

// Agent restart (new authenticator instance) drops all sessions.
void testRestartInvalidates() {
  FakeClock clock;
  SecretKey key = makeKey(6);
  Challenge ch;
  Response captured;
  {
    SessionAuthenticator auth(key, {}, clock.fn());
    ch = auth.beginSession(kClient);
    captured = respond(key, ch);
  }  // agent restarts; in-memory sessions are gone

  SessionAuthenticator auth(key, {}, clock.fn());
  CHECK(auth.verifyResponse(ch.session_id, kClient, ch.key_generation,
                            ch.sequence, ch.nonce, captured) ==
        VerifyResult::kUnknownSession);
}

// Configuration rollback invalidates outstanding sessions.
void testConfigRollback() {
  FakeClock clock;
  SecretKey key = makeKey(7);
  SessionAuthenticator auth(key, {}, clock.fn());

  Challenge ch = auth.beginSession(kClient);
  Response captured = respond(key, ch);

  auth.notifyConfigRollback();
  CHECK(auth.activeSessions() == 0);
  CHECK(auth.verifyResponse(ch.session_id, kClient, ch.key_generation,
                            ch.sequence, ch.nonce, captured) ==
        VerifyResult::kUnknownSession);
}

// Responses outside the time window are rejected.
void testTimeWindow() {
  FakeClock clock;
  SecretKey key = makeKey(8);
  SessionConfig cfg;
  cfg.challenge_ttl = std::chrono::seconds{30};
  SessionAuthenticator auth(key, cfg, clock.fn());

  Challenge ch = auth.beginSession(kClient);
  clock.advance(std::chrono::seconds{31});
  CHECK(auth.verifyResponse(ch.session_id, kClient, ch.key_generation,
                            ch.sequence, ch.nonce, respond(key, ch)) ==
        VerifyResult::kExpired);
}

// Retransmission / guessing is bounded: session locks after max attempts.
void testAttemptsBound() {
  FakeClock clock;
  SecretKey key = makeKey(9);
  SessionConfig cfg;
  cfg.max_attempts = 3;
  SessionAuthenticator auth(key, cfg, clock.fn());

  Challenge ch = auth.beginSession(kClient);
  Response wrong{};
  for (int i = 0; i < 3; ++i) {
    CHECK(auth.verifyResponse(ch.session_id, kClient, ch.key_generation,
                              ch.sequence, ch.nonce, wrong) ==
          VerifyResult::kResponseMismatch);
  }
  // Session is locked now; even the correct response is refused.
  CHECK(auth.verifyResponse(ch.session_id, kClient, ch.key_generation,
                            ch.sequence, ch.nonce, respond(key, ch)) ==
        VerifyResult::kAttemptsExceeded);
}

// Response replayed from a different client address is rejected.
void testClientBinding() {
  FakeClock clock;
  SecretKey key = makeKey(10);
  SessionAuthenticator auth(key, {}, clock.fn());

  Challenge ch = auth.beginSession(kClient);
  Response captured = respond(key, ch);
  CHECK(auth.verifyResponse(ch.session_id, "198.51.100.99:40000",
                            ch.key_generation, ch.sequence, ch.nonce,
                            captured) == VerifyResult::kClientMismatch);
}

}  // namespace

int main() {
  testHmacVector();
  testLegitLogin();
  testPacketReplay();
  testChallengeSubstitution();
  testSequenceRollback();
  testKeyRotation();
  testRestartInvalidates();
  testConfigRollback();
  testTimeWindow();
  testAttemptsBound();
  testClientBinding();
  if (failures == 0) {
    std::puts("all netsec tests passed");
    return 0;
  }
  std::fprintf(stderr, "%d check(s) failed\n", failures);
  return 1;
}
