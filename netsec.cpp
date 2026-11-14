#include "netsec.hpp"

#include <algorithm>
#include <cstring>
#include <random>

namespace netsec {
namespace {

// ---- SHA-256 (FIPS 180-4) -------------------------------------------------

class Sha256 {
 public:
  Sha256() { reset(); }

  void reset() {
    state_ = {0x6a09e667u, 0xbb67ae85u, 0x3c6ef372u, 0xa54ff53au,
              0x510e527fu, 0x9b05688cu, 0x1f83d9abu, 0x5be0cd19u};
    bit_len_ = 0;
    buf_len_ = 0;
  }

  void update(std::span<const std::uint8_t> data) {
    for (std::uint8_t b : data) {
      buf_[buf_len_++] = b;
      if (buf_len_ == 64) {
        compress(buf_.data());
        bit_len_ += 512;
        buf_len_ = 0;
      }
    }
  }

  std::array<std::uint8_t, 32> final() {
    std::uint64_t total_bits = bit_len_ + buf_len_ * 8;
    buf_[buf_len_++] = 0x80;
    if (buf_len_ > 56) {
      while (buf_len_ < 64) buf_[buf_len_++] = 0;
      compress(buf_.data());
      buf_len_ = 0;
    }
    while (buf_len_ < 56) buf_[buf_len_++] = 0;
    for (int i = 7; i >= 0; --i) {
      buf_[buf_len_++] = static_cast<std::uint8_t>(total_bits >> (i * 8));
    }
    compress(buf_.data());
    std::array<std::uint8_t, 32> out{};
    for (int i = 0; i < 8; ++i) {
      out[i * 4 + 0] = static_cast<std::uint8_t>(state_[i] >> 24);
      out[i * 4 + 1] = static_cast<std::uint8_t>(state_[i] >> 16);
      out[i * 4 + 2] = static_cast<std::uint8_t>(state_[i] >> 8);
      out[i * 4 + 3] = static_cast<std::uint8_t>(state_[i]);
    }
    reset();
    return out;
  }

 private:
  static std::uint32_t rotr(std::uint32_t x, std::uint32_t n) {
    return (x >> n) | (x << (32 - n));
  }

  void compress(const std::uint8_t* block) {
    static constexpr std::uint32_t k[64] = {
        0x428a2f98u, 0x71374491u, 0xb5c0fbcfu, 0xe9b5dba5u, 0x3956c25bu,
        0x59f111f1u, 0x923f82a4u, 0xab1c5ed5u, 0xd807aa98u, 0x12835b01u,
        0x243185beu, 0x550c7dc3u, 0x72be5d74u, 0x80deb1feu, 0x9bdc06a7u,
        0xc19bf174u, 0xe49b69c1u, 0xefbe4786u, 0x0fc19dc6u, 0x240ca1ccu,
        0x2de92c6fu, 0x4a7484aau, 0x5cb0a9dcu, 0x76f988dau, 0x983e5152u,
        0xa831c66du, 0xb00327c8u, 0xbf597fc7u, 0xc6e00bf3u, 0xd5a79147u,
        0x06ca6351u, 0x14292967u, 0x27b70a85u, 0x2e1b2138u, 0x4d2c6dfcu,
        0x53380d13u, 0x650a7354u, 0x766a0abbu, 0x81c2c92eu, 0x92722c85u,
        0xa2bfe8a1u, 0xa81a664bu, 0xc24b8b70u, 0xc76c51a3u, 0xd192e819u,
        0xd6990624u, 0xf40e3585u, 0x106aa070u, 0x19a4c116u, 0x1e376c08u,
        0x2748774cu, 0x34b0bcb5u, 0x391c0cb3u, 0x4ed8aa4au, 0x5b9cca4fu,
        0x682e6ff3u, 0x748f82eeu, 0x78a5636fu, 0x84c87814u, 0x8cc70208u,
        0x90befffau, 0xa4506cebu, 0xbef9a3f7u, 0xc67178f2u};
    std::uint32_t w[64];
    for (int i = 0; i < 16; ++i) {
      w[i] = (static_cast<std::uint32_t>(block[i * 4]) << 24) |
             (static_cast<std::uint32_t>(block[i * 4 + 1]) << 16) |
             (static_cast<std::uint32_t>(block[i * 4 + 2]) << 8) |
             (static_cast<std::uint32_t>(block[i * 4 + 3]));
    }
    for (int i = 16; i < 64; ++i) {
      std::uint32_t s0 = rotr(w[i - 15], 7) ^ rotr(w[i - 15], 18) ^ (w[i - 15] >> 3);
      std::uint32_t s1 = rotr(w[i - 2], 17) ^ rotr(w[i - 2], 19) ^ (w[i - 2] >> 10);
      w[i] = w[i - 16] + s0 + w[i - 7] + s1;
    }
    std::uint32_t a = state_[0], b = state_[1], c = state_[2], d = state_[3];
    std::uint32_t e = state_[4], f = state_[5], g = state_[6], h = state_[7];
    for (int i = 0; i < 64; ++i) {
      std::uint32_t s1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25);
      std::uint32_t ch = (e & f) ^ (~e & g);
      std::uint32_t t1 = h + s1 + ch + k[i] + w[i];
      std::uint32_t s0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22);
      std::uint32_t maj = (a & b) ^ (a & c) ^ (b & c);
      std::uint32_t t2 = s0 + maj;
      h = g;
      g = f;
      f = e;
      e = d + t1;
      d = c;
      c = b;
      b = a;
      a = t1 + t2;
    }
    state_[0] += a;
    state_[1] += b;
    state_[2] += c;
    state_[3] += d;
    state_[4] += e;
    state_[5] += f;
    state_[6] += g;
    state_[7] += h;
  }

  std::array<std::uint32_t, 8> state_{};
  std::uint64_t bit_len_ = 0;
  std::array<std::uint8_t, 64> buf_{};
  std::size_t buf_len_ = 0;
};

void appendU64(std::vector<std::uint8_t>& out, std::uint64_t v) {
  for (int i = 7; i >= 0; --i)
    out.push_back(static_cast<std::uint8_t>(v >> (i * 8)));
}

void appendU32(std::vector<std::uint8_t>& out, std::uint32_t v) {
  for (int i = 3; i >= 0; --i)
    out.push_back(static_cast<std::uint8_t>(v >> (i * 8)));
}

std::vector<std::uint8_t> randomBytes(std::size_t n) {
  std::random_device rd;
  std::vector<std::uint8_t> out(n);
  for (auto& b : out) b = static_cast<std::uint8_t>(rd());
  return out;
}

bool constantTimeEqual(std::span<const std::uint8_t> a,
                       std::span<const std::uint8_t> b) {
  if (a.size() != b.size()) return false;
  std::uint8_t diff = 0;
  for (std::size_t i = 0; i < a.size(); ++i) diff |= a[i] ^ b[i];
  return diff == 0;
}

}  // namespace

Response hmacSha256(std::span<const std::uint8_t> key,
                    std::span<const std::uint8_t> data) {
  std::array<std::uint8_t, 64> k{};
  if (key.size() > 64) {
    Sha256 h;
    h.update(key);
    auto d = h.final();
    std::copy(d.begin(), d.end(), k.begin());
  } else {
    std::copy(key.begin(), key.end(), k.begin());
  }
  std::array<std::uint8_t, 64> ipad{}, opad{};
  for (int i = 0; i < 64; ++i) {
    ipad[i] = k[i] ^ 0x36;
    opad[i] = k[i] ^ 0x5c;
  }
  Sha256 inner;
  inner.update(ipad);
  inner.update(data);
  auto inner_digest = inner.final();
  Sha256 outer;
  outer.update(opad);
  outer.update(inner_digest);
  return outer.final();
}

const char* toString(VerifyResult r) noexcept {
  switch (r) {
    case VerifyResult::kOk: return "ok";
    case VerifyResult::kUnknownSession: return "unknown-session";
    case VerifyResult::kExpired: return "expired";
    case VerifyResult::kKeyGenerationMismatch: return "key-generation-mismatch";
    case VerifyResult::kClientMismatch: return "client-mismatch";
    case VerifyResult::kChallengeMismatch: return "challenge-mismatch";
    case VerifyResult::kSequenceRollback: return "sequence-rollback";
    case VerifyResult::kResponseMismatch: return "response-mismatch";
    case VerifyResult::kReplay: return "replay";
    case VerifyResult::kAttemptsExceeded: return "attempts-exceeded";
  }
  return "unknown";
}

SessionAuthenticator::SessionAuthenticator(SecretKey key, SessionConfig cfg,
                                           Clock clock)
    : key_(key), cfg_(cfg), clock_(std::move(clock)) {}

std::chrono::system_clock::time_point SessionAuthenticator::defaultClock() {
  return std::chrono::system_clock::now();
}

SessionAuthenticator::Session* SessionAuthenticator::find(
    std::uint64_t session_id) {
  for (auto& s : sessions_) {
    if (s.id == session_id) return &s;
  }
  return nullptr;
}

Challenge SessionAuthenticator::issueFor(Session& s) {
  s.nonce = randomBytes(cfg_.challenge_bytes);
  s.expires_at = clock_() + cfg_.challenge_ttl;
  s.consumed = false;
  s.failed_attempts = 0;
  s.key_generation = key_generation_;
  return Challenge{s.id, s.key_generation, s.sequence, s.nonce, s.expires_at};
}

Challenge SessionAuthenticator::beginSession(std::string_view client_addr) {
  auto now = clock_();
  // Opportunistically drop expired sessions.
  sessions_.remove_if([&](const Session& s) { return s.expires_at <= now; });
  Session s;
  s.id = next_session_id_++;
  s.client_addr = std::string(client_addr);
  s.sequence = 0;
  sessions_.push_back(std::move(s));
  return issueFor(sessions_.back());
}

std::optional<Challenge> SessionAuthenticator::nextChallenge(
    std::uint64_t session_id) {
  Session* s = find(session_id);
  if (!s || s->locked || !s->consumed) return std::nullopt;
  ++s->sequence;
  return issueFor(*s);
}

VerifyResult SessionAuthenticator::verifyResponse(
    std::uint64_t session_id, std::string_view client_addr,
    std::uint32_t key_generation, std::uint64_t sequence,
    std::span<const std::uint8_t> nonce,
    std::span<const std::uint8_t> response) {
  Session* s = find(session_id);
  if (!s) return VerifyResult::kUnknownSession;
  if (s->locked) return VerifyResult::kAttemptsExceeded;

  auto fail = [&](VerifyResult r) {
    if (++s->failed_attempts >= cfg_.max_attempts) s->locked = true;
    return r;
  };

  if (clock_() >= s->expires_at) return VerifyResult::kExpired;
  if (key_generation != key_generation_ ||
      s->key_generation != key_generation_)
    return fail(VerifyResult::kKeyGenerationMismatch);
  if (client_addr != s->client_addr) return fail(VerifyResult::kClientMismatch);
  if (sequence < s->sequence) return fail(VerifyResult::kSequenceRollback);
  if (sequence != s->sequence ||
      !constantTimeEqual(nonce, std::span<const std::uint8_t>(s->nonce)))
    return fail(VerifyResult::kChallengeMismatch);
  if (s->consumed) return fail(VerifyResult::kReplay);

  Challenge expected{s->id, s->key_generation, s->sequence, s->nonce,
                     s->expires_at};
  Response want = computeResponse(key_, expected, s->client_addr);
  if (!constantTimeEqual(response,
                         std::span<const std::uint8_t>(want)))
    return fail(VerifyResult::kResponseMismatch);

  s->consumed = true;  // one-time use
  return VerifyResult::kOk;
}

void SessionAuthenticator::rotateKey(SecretKey new_key) {
  key_ = new_key;
  ++key_generation_;
  sessions_.clear();  // old sessions must not survive rotation
}

void SessionAuthenticator::notifyConfigRollback() {
  sessions_.clear();  // rolled-back config invalidates every session
}

std::size_t SessionAuthenticator::activeSessions() const {
  return sessions_.size();
}

Response SessionAuthenticator::computeResponse(const SecretKey& key,
                                               const Challenge& ch,
                                               std::string_view client_addr) {
  static constexpr char kDomain[] = "netsec-session-v1";
  std::vector<std::uint8_t> transcript;
  transcript.insert(transcript.end(), kDomain,
                    kDomain + sizeof(kDomain) - 1);
  appendU32(transcript, static_cast<std::uint32_t>(client_addr.size()));
  transcript.insert(transcript.end(), client_addr.begin(), client_addr.end());
  appendU64(transcript, ch.session_id);
  appendU32(transcript, ch.key_generation);
  appendU64(transcript, ch.sequence);
  appendU32(transcript, static_cast<std::uint32_t>(ch.nonce.size()));
  transcript.insert(transcript.end(), ch.nonce.begin(), ch.nonce.end());
  return hmacSha256(std::span<const std::uint8_t>(key),
                    std::span<const std::uint8_t>(transcript));
}

}  // namespace netsec
