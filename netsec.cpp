#include "netsec.hpp"

#include <algorithm>
#include <bit>
#include <cerrno>
#include <cstring>
#include <limits>
#include <map>
#include <mutex>
#include <random>
#include <tuple>
#include <unordered_map>
#include <utility>

#if defined(__APPLE__) || defined(__FreeBSD__) || defined(__OpenBSD__) || \
    defined(__NetBSD__)
#include <cstdlib>
#elif defined(__linux__) && __has_include(<sys/random.h>)
#include <sys/random.h>
#include <sys/types.h>
#endif

namespace netsec {
namespace {

using Digest = std::array<std::uint8_t, 32>;

constexpr std::uint32_t kSha256RoundConstants[64] = {
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
    0x90befffau, 0xa4506cebu, 0xbef9a3f7u, 0xc67178f2u,
};

class Sha256 {
 public:
  Sha256() noexcept { reset(); }

  void update(const std::uint8_t* data, std::size_t size) noexcept {
    total_bytes_ += size;
    while (size != 0) {
      const std::size_t available = 64 - buffer_size_;
      const std::size_t take = std::min(available, size);
      std::memcpy(buffer_.data() + buffer_size_, data, take);
      buffer_size_ += take;
      data += take;
      size -= take;
      if (buffer_size_ == 64) {
        transform(buffer_.data());
        buffer_size_ = 0;
      }
    }
  }

  Digest finish() noexcept {
    const std::uint64_t bit_length = total_bytes_ * 8;

    std::uint8_t continuation = 0x80;
    update(&continuation, 1);
    continuation = 0;
    while (buffer_size_ != 56) {
      update(&continuation, 1);
    }

    std::uint8_t length_bytes[8]{};
    for (std::size_t i = 0; i < 8; ++i) {
      length_bytes[7 - i] =
          static_cast<std::uint8_t>(bit_length >> (i * 8));
    }
    update(length_bytes, sizeof(length_bytes));

    Digest digest{};
    for (std::size_t i = 0; i < state_.size(); ++i) {
      digest[i * 4] = static_cast<std::uint8_t>(state_[i] >> 24);
      digest[i * 4 + 1] = static_cast<std::uint8_t>(state_[i] >> 16);
      digest[i * 4 + 2] = static_cast<std::uint8_t>(state_[i] >> 8);
      digest[i * 4 + 3] = static_cast<std::uint8_t>(state_[i]);
    }
    return digest;
  }

 private:
  void reset() noexcept {
    state_ = {0x6a09e667u, 0xbb67ae85u, 0x3c6ef372u, 0xa54ff53au,
              0x510e527fu, 0x9b05688cu, 0x1f83d9abu, 0x5be0cd19u};
    buffer_.fill(0);
    buffer_size_ = 0;
    total_bytes_ = 0;
  }

  void transform(const std::uint8_t* block) noexcept {
    std::uint32_t words[64]{};
    for (std::size_t i = 0; i < 16; ++i) {
      words[i] = (static_cast<std::uint32_t>(block[i * 4]) << 24) |
                 (static_cast<std::uint32_t>(block[i * 4 + 1]) << 16) |
                 (static_cast<std::uint32_t>(block[i * 4 + 2]) << 8) |
                 static_cast<std::uint32_t>(block[i * 4 + 3]);
    }
    for (std::size_t i = 16; i < 64; ++i) {
      const std::uint32_t s0 =
          std::rotr(words[i - 15], 7) ^ std::rotr(words[i - 15], 18) ^
          (words[i - 15] >> 3);
      const std::uint32_t s1 =
          std::rotr(words[i - 2], 17) ^ std::rotr(words[i - 2], 19) ^
          (words[i - 2] >> 10);
      words[i] = words[i - 16] + s0 + words[i - 7] + s1;
    }

    std::uint32_t a = state_[0];
    std::uint32_t b = state_[1];
    std::uint32_t c = state_[2];
    std::uint32_t d = state_[3];
    std::uint32_t e = state_[4];
    std::uint32_t f = state_[5];
    std::uint32_t g = state_[6];
    std::uint32_t h = state_[7];

    for (std::size_t i = 0; i < 64; ++i) {
      const std::uint32_t sum1 =
          std::rotr(e, 6) ^ std::rotr(e, 11) ^ std::rotr(e, 25);
      const std::uint32_t choose = (e & f) ^ ((~e) & g);
      const std::uint32_t temp1 =
          h + sum1 + choose + kSha256RoundConstants[i] + words[i];
      const std::uint32_t sum0 =
          std::rotr(a, 2) ^ std::rotr(a, 13) ^ std::rotr(a, 22);
      const std::uint32_t majority = (a & b) ^ (a & c) ^ (b & c);
      const std::uint32_t temp2 = sum0 + majority;

      h = g;
      g = f;
      f = e;
      e = d + temp1;
      d = c;
      c = b;
      b = a;
      a = temp1 + temp2;
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
  std::array<std::uint8_t, 64> buffer_{};
  std::size_t buffer_size_{0};
  std::uint64_t total_bytes_{0};
};

Digest digest(std::string_view value) noexcept {
  Sha256 sha;
  sha.update(reinterpret_cast<const std::uint8_t*>(value.data()), value.size());
  return sha.finish();
}

std::string hex(const Digest& value) {
  static constexpr char kHexDigits[] = "0123456789abcdef";
  std::string output;
  output.reserve(value.size() * 2);
  for (std::uint8_t byte : value) {
    output.push_back(kHexDigits[byte >> 4]);
    output.push_back(kHexDigits[byte & 0x0f]);
  }
  return output;
}

std::string base64url(const Digest& value) {
  static constexpr char kAlphabet[] =
      "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
  std::string output;
  output.reserve(43);

  std::size_t i = 0;
  for (; i + 2 < value.size(); i += 3) {
    const std::uint32_t block =
        (static_cast<std::uint32_t>(value[i]) << 16) |
        (static_cast<std::uint32_t>(value[i + 1]) << 8) |
        static_cast<std::uint32_t>(value[i + 2]);
    output.push_back(kAlphabet[(block >> 18) & 0x3f]);
    output.push_back(kAlphabet[(block >> 12) & 0x3f]);
    output.push_back(kAlphabet[(block >> 6) & 0x3f]);
    output.push_back(kAlphabet[block & 0x3f]);
  }

  const std::size_t remaining = value.size() - i;
  if (remaining == 1) {
    const std::uint32_t block = static_cast<std::uint32_t>(value[i]) << 16;
    output.push_back(kAlphabet[(block >> 18) & 0x3f]);
    output.push_back(kAlphabet[(block >> 12) & 0x3f]);
  } else if (remaining == 2) {
    const std::uint32_t block =
        (static_cast<std::uint32_t>(value[i]) << 16) |
        (static_cast<std::uint32_t>(value[i + 1]) << 8);
    output.push_back(kAlphabet[(block >> 18) & 0x3f]);
    output.push_back(kAlphabet[(block >> 12) & 0x3f]);
    output.push_back(kAlphabet[(block >> 6) & 0x3f]);
  }
  return output;
}

bool random_bytes(std::uint8_t* destination, std::size_t size) noexcept {
#if defined(__APPLE__) || defined(__FreeBSD__) || defined(__OpenBSD__) || \
    defined(__NetBSD__)
  arc4random_buf(destination, size);
  return true;
#elif defined(__linux__) && __has_include(<sys/random.h>)
  std::size_t received = 0;
  while (received < size) {
    const ssize_t result =
        getrandom(destination + received, size - received, 0);
    if (result < 0) {
      if (errno == EINTR) {
        continue;
      }
      break;
    }
    received += static_cast<std::size_t>(result);
  }
  if (received == size) {
    return true;
  }
#endif

  try {
    std::random_device random;
    for (std::size_t i = 0; i < size; i += sizeof(unsigned int)) {
      const unsigned int word = random();
      const std::size_t chunk = std::min(sizeof(word), size - i);
      std::memcpy(destination + i, &word, chunk);
    }
    return true;
  } catch (...) {
    return false;
  }
}

std::optional<std::string> make_token() {
  Digest entropy{};
  if (!random_bytes(entropy.data(), entropy.size())) {
    return std::nullopt;
  }
  return std::string("nsc1_") + base64url(entropy);
}

using BindingKey = std::tuple<std::string, std::string, DisplayId>;

BindingKey binding_key(const ConnectionIdentity& binding) {
  return {binding.user, binding.client_address, binding.display};
}

bool identity_is_valid(const ConnectionIdentity& binding) noexcept {
  return !binding.user.empty() && !binding.client_address.empty() &&
         binding.generation != 0;
}

std::chrono::milliseconds remaining(AccessTime now, AccessTime expires) {
  if (now >= expires) {
    return std::chrono::milliseconds(0);
  }
  return std::chrono::duration_cast<std::chrono::milliseconds>(expires - now);
}

}  // namespace

std::string_view to_string(CredentialStatus status) noexcept {
  switch (status) {
    case CredentialStatus::Ok:
      return "ok";
    case CredentialStatus::InvalidCookie:
      return "invalid cookie";
    case CredentialStatus::AlreadyUsed:
      return "cookie already used";
    case CredentialStatus::Expired:
      return "cookie expired";
    case CredentialStatus::Revoked:
      return "credential revoked";
    case CredentialStatus::InvalidRequest:
      return "invalid request";
    case CredentialStatus::UserMismatch:
      return "user mismatch";
    case CredentialStatus::AddressMismatch:
      return "client address mismatch";
    case CredentialStatus::DisplayMismatch:
      return "display mismatch";
    case CredentialStatus::DisplayNotAllowed:
      return "display not allowed";
    case CredentialStatus::GenerationMismatch:
      return "connection generation mismatch";
    case CredentialStatus::StaleGeneration:
      return "stale connection generation";
    case CredentialStatus::SessionExpired:
      return "session expired";
    case CredentialStatus::SessionRevoked:
      return "session revoked";
    case CredentialStatus::CapabilityDenied:
      return "capability denied";
    case CredentialStatus::CapacityExceeded:
      return "credential capacity exceeded";
    case CredentialStatus::EntropyFailure:
      return "secure randomness unavailable";
  }
  return "unknown";
}

class AccessCredentialStore::Impl {
 public:
  struct Record {
    ConnectionIdentity binding;
    CapabilitySet capabilities;
    AccessTime expires_at{};
    std::string parent_session;
  };

  struct Retired {
    AccessTime purge_after{};
    CredentialStatus reason{CredentialStatus::InvalidCookie};
  };

  Impl(Policy configured_policy, ClockFunction configured_clock)
      : policy(std::move(configured_policy)),
        clock(std::move(configured_clock)) {
    if (!clock) {
      clock = [] { return AccessClock::now(); };
    }
    if (policy.max_ttl <= AccessDuration::zero()) {
      policy.max_ttl = std::chrono::minutes(5);
    }
    if (policy.max_live_credentials == 0) {
      policy.max_live_credentials = 4096;
    }
  }

  AccessTime now() const noexcept {
    try {
      return clock();
    } catch (...) {
      return AccessClock::now();
    }
  }

  bool display_allowed(DisplayId display) const {
    return policy.allowed_displays.empty() ||
           policy.allowed_displays.contains(display);
  }

  AccessDuration bounded_ttl(AccessDuration requested) const {
    return std::min(requested, policy.max_ttl);
  }

  void purge_locked(AccessTime current) {
    for (auto iterator = credentials.begin();
         iterator != credentials.end();) {
      if (current >= iterator->second.expires_at) {
        retired[iterator->first] = {
            iterator->second.expires_at + std::chrono::hours(1),
            CredentialStatus::Expired};
        iterator = credentials.erase(iterator);
      } else {
        ++iterator;
      }
    }

    for (auto iterator = retired.begin(); iterator != retired.end();) {
      if (current >= iterator->second.purge_after) {
        iterator = retired.erase(iterator);
      } else {
        ++iterator;
      }
    }

    for (auto iterator = sessions.begin(); iterator != sessions.end();) {
      if (current >= iterator->second.expires_at) {
        iterator = sessions.erase(iterator);
      } else {
        ++iterator;
      }
    }
  }

  void revoke_children_locked(std::string_view parent, AccessTime current) {
    for (auto iterator = credentials.begin();
         iterator != credentials.end();) {
      if (iterator->second.parent_session == parent) {
        retired[iterator->first] = {
            current + std::chrono::hours(1), CredentialStatus::Revoked};
        iterator = credentials.erase(iterator);
      } else {
        ++iterator;
      }
    }
  }

  std::string fingerprint(std::string_view token) const {
    return hex(digest(token));
  }

  Policy policy;
  ClockFunction clock;
  mutable std::mutex mutex;
  std::unordered_map<std::string, Record> credentials;
  std::unordered_map<std::string, Retired> retired;
  std::unordered_map<std::string, Record> sessions;
  std::map<BindingKey, ConnectionGeneration> latest_generations;
};

AccessCredentialStore::AccessCredentialStore()
    : impl_(std::make_unique<Impl>(Policy{}, ClockFunction{})) {}

AccessCredentialStore::AccessCredentialStore(Policy policy)
    : impl_(std::make_unique<Impl>(std::move(policy), ClockFunction{})) {}

AccessCredentialStore::AccessCredentialStore(Policy policy,
                                             ClockFunction clock)
    : impl_(std::make_unique<Impl>(std::move(policy), std::move(clock))) {}

AccessCredentialStore::~AccessCredentialStore() = default;

AccessCredentialStore::AccessCredentialStore(
    AccessCredentialStore&&) noexcept = default;

AccessCredentialStore& AccessCredentialStore::operator=(
    AccessCredentialStore&&) noexcept = default;

const AccessCredentialStore::Policy& AccessCredentialStore::policy()
    const noexcept {
  return impl_->policy;
}

void AccessCredentialStore::purge_expired() {
  purge_expired(impl_->now());
}

void AccessCredentialStore::purge_expired(AccessTime now) {
  std::lock_guard lock(impl_->mutex);
  impl_->purge_locked(now);
}

IssuanceResult AccessCredentialStore::issue(
    const ConnectionIdentity& binding, CredentialOptions options) {
  return issue(binding, options, impl_->now());
}

IssuanceResult AccessCredentialStore::issue(
    const ConnectionIdentity& binding, CredentialOptions options,
    AccessTime now) {
  if (!identity_is_valid(binding) || options.ttl <= AccessDuration::zero()) {
    return {CredentialStatus::InvalidRequest, std::nullopt};
  }

  const AccessDuration ttl = impl_->bounded_ttl(options.ttl);
  if (ttl <= AccessDuration::zero()) {
    return {CredentialStatus::InvalidRequest, std::nullopt};
  }

  auto token = make_token();
  if (!token.has_value()) {
    return {CredentialStatus::EntropyFailure, std::nullopt};
  }

  IssuedCredential credential;
  credential.token_ = std::move(*token);
  credential.binding_ = binding;
  credential.capabilities_.insert(Capability::ViewDisplay);
  credential.issued_at_ = now;
  credential.expires_at_ = now + ttl;

  const std::string fingerprint = impl_->fingerprint(credential.token_);
  std::lock_guard lock(impl_->mutex);
  impl_->purge_locked(now);

  if (!impl_->display_allowed(binding.display)) {
    return {CredentialStatus::DisplayNotAllowed, std::nullopt};
  }

  if (impl_->credentials.size() + impl_->sessions.size() >=
      impl_->policy.max_live_credentials) {
    return {CredentialStatus::CapacityExceeded, std::nullopt};
  }

  const BindingKey key = binding_key(binding);
  const auto generation_iterator = impl_->latest_generations.find(key);
  if (generation_iterator != impl_->latest_generations.end()) {
    if (binding.generation < generation_iterator->second) {
      return {CredentialStatus::StaleGeneration, std::nullopt};
    }
    if (binding.generation > generation_iterator->second) {
      generation_iterator->second = binding.generation;
    }
  } else {
    impl_->latest_generations.emplace(key, binding.generation);
  }

  impl_->credentials.emplace(
      fingerprint,
      Impl::Record{binding, credential.capabilities_, credential.expires_at_,
                   {}});
  return {CredentialStatus::Ok, std::move(credential)};
}

IssuanceResult AccessCredentialStore::issue(std::string_view user,
                                            std::string_view client_address,
                                            DisplayId display,
                                            ConnectionGeneration generation,
                                            AccessDuration ttl) {
  return issue(user, client_address, display, generation, ttl, impl_->now());
}

IssuanceResult AccessCredentialStore::issue(
    std::string_view user, std::string_view client_address, DisplayId display,
    ConnectionGeneration generation, AccessDuration ttl, AccessTime now) {
  return issue(ConnectionIdentity{std::string(user),
                                  std::string(client_address), display,
                                  generation},
               CredentialOptions{ttl}, now);
}

RedemptionResult AccessCredentialStore::redeem(
    std::string_view cookie, const ConnectionIdentity& presented) {
  return redeem(cookie, presented, impl_->now());
}

RedemptionResult AccessCredentialStore::redeem(
    std::string_view cookie, const ConnectionIdentity& presented,
    AccessTime now) {
  if (cookie.empty()) {
    return {CredentialStatus::InvalidCookie, std::nullopt};
  }
  if (!identity_is_valid(presented)) {
    return {CredentialStatus::InvalidRequest, std::nullopt};
  }

  const std::string fingerprint = impl_->fingerprint(cookie);
  std::lock_guard lock(impl_->mutex);
  impl_->purge_locked(now);

  const auto credential_iterator = impl_->credentials.find(fingerprint);
  if (credential_iterator == impl_->credentials.end()) {
    const auto retired_iterator = impl_->retired.find(fingerprint);
    if (retired_iterator != impl_->retired.end()) {
      return {retired_iterator->second.reason, std::nullopt};
    }
    return {CredentialStatus::InvalidCookie, std::nullopt};
  }

  const Impl::Record& record = credential_iterator->second;
  if (now >= record.expires_at) {
    impl_->retired[fingerprint] = {
        record.expires_at + std::chrono::hours(1), CredentialStatus::Expired};
    impl_->credentials.erase(credential_iterator);
    return {CredentialStatus::Expired, std::nullopt};
  }

  if (presented.user != record.binding.user) {
    return {CredentialStatus::UserMismatch, std::nullopt};
  }
  if (presented.client_address != record.binding.client_address) {
    return {CredentialStatus::AddressMismatch, std::nullopt};
  }
  if (presented.display != record.binding.display) {
    return {CredentialStatus::DisplayMismatch, std::nullopt};
  }
  if (!impl_->display_allowed(record.binding.display)) {
    return {CredentialStatus::DisplayNotAllowed, std::nullopt};
  }
  if (presented.generation != record.binding.generation) {
    return {CredentialStatus::GenerationMismatch, std::nullopt};
  }

  const auto generation_iterator =
      impl_->latest_generations.find(binding_key(record.binding));
  if (generation_iterator == impl_->latest_generations.end() ||
      generation_iterator->second != record.binding.generation) {
    return {CredentialStatus::StaleGeneration, std::nullopt};
  }

  // Consumption and session creation are one atomic step. No second
  // redemption can observe the cookie as still live.
  Impl::Record consumed = record;
  impl_->credentials.erase(credential_iterator);
  impl_->sessions[fingerprint] = consumed;
  impl_->retired[fingerprint] = {
      consumed.expires_at + std::chrono::hours(1),
      CredentialStatus::AlreadyUsed};

  AccessGrant grant;
  grant.session_id_ = fingerprint;
  grant.binding_ = consumed.binding;
  grant.capabilities_ = consumed.capabilities;
  grant.expires_at_ = consumed.expires_at;
  grant.valid_ = true;
  return {CredentialStatus::Ok, std::move(grant)};
}

RedemptionResult AccessCredentialStore::redeem(
    std::string_view cookie, std::string_view user,
    std::string_view client_address, DisplayId display,
    ConnectionGeneration generation) {
  return redeem(cookie, user, client_address, display, generation,
                impl_->now());
}

RedemptionResult AccessCredentialStore::redeem(
    std::string_view cookie, std::string_view user,
    std::string_view client_address, DisplayId display,
    ConnectionGeneration generation, AccessTime now) {
  return redeem(cookie,
                ConnectionIdentity{std::string(user),
                                   std::string(client_address), display,
                                   generation},
                now);
}

AuthorizationResult AccessCredentialStore::authorize(
    const AccessGrant& grant, Capability capability, DisplayId display) const {
  return authorize(grant, capability, display, impl_->now());
}

AuthorizationResult AccessCredentialStore::authorize(
    const AccessGrant& grant, Capability capability, DisplayId display,
    AccessTime now) const {
  if (!grant.valid_) {
    return {CredentialStatus::InvalidRequest};
  }
  if (now >= grant.expires_at_) {
    return {CredentialStatus::SessionExpired};
  }
  if (display != grant.binding_.display) {
    return {CredentialStatus::DisplayMismatch};
  }
  if (!impl_->display_allowed(display)) {
    return {CredentialStatus::DisplayNotAllowed};
  }
  if (!grant.capabilities_.contains(capability)) {
    return {CredentialStatus::CapabilityDenied};
  }

  std::lock_guard lock(impl_->mutex);
  const auto session_iterator = impl_->sessions.find(grant.session_id_);
  if (session_iterator == impl_->sessions.end()) {
    return {CredentialStatus::SessionRevoked};
  }

  const Impl::Record& session = session_iterator->second;
  if (now >= session.expires_at) {
    return {CredentialStatus::SessionExpired};
  }
  if (session.binding != grant.binding_ ||
      !(session.capabilities == grant.capabilities_) ||
      session.expires_at != grant.expires_at_) {
    return {CredentialStatus::SessionRevoked};
  }

  const auto generation_iterator =
      impl_->latest_generations.find(binding_key(session.binding));
  if (generation_iterator == impl_->latest_generations.end() ||
      generation_iterator->second != session.binding.generation) {
    return {CredentialStatus::StaleGeneration};
  }
  return {CredentialStatus::Ok};
}

IssuanceResult AccessCredentialStore::issue_capability(
    const AccessGrant& session, Capability capability, AccessDuration ttl) {
  return issue_capability(session, capability, ttl, impl_->now());
}

IssuanceResult AccessCredentialStore::issue_capability(
    const AccessGrant& session, Capability capability, AccessDuration ttl,
    AccessTime now) {
  const auto requested_bits =
      static_cast<std::uint8_t>(capability);
  constexpr std::uint8_t kActionBits =
      static_cast<std::uint8_t>(Capability::ClipboardRead) |
      static_cast<std::uint8_t>(Capability::ClipboardWrite) |
      static_cast<std::uint8_t>(Capability::FileTransfer);
  constexpr std::uint8_t kViewBit =
      static_cast<std::uint8_t>(Capability::ViewDisplay);
  const bool is_one_action =
      requested_bits == static_cast<std::uint8_t>(Capability::ClipboardRead) ||
      requested_bits == static_cast<std::uint8_t>(Capability::ClipboardWrite) ||
      requested_bits == static_cast<std::uint8_t>(Capability::Clipboard) ||
      requested_bits == static_cast<std::uint8_t>(Capability::FileTransfer);

  if (!session.valid_ || !is_one_action ||
      (requested_bits & kViewBit) != 0 ||
      (requested_bits & ~kActionBits) != 0 ||
      ttl <= AccessDuration::zero()) {
    return {CredentialStatus::InvalidRequest, std::nullopt};
  }
  if (!session.capabilities_.contains(Capability::ViewDisplay)) {
    return {CredentialStatus::CapabilityDenied, std::nullopt};
  }
  if (now >= session.expires_at_) {
    return {CredentialStatus::SessionExpired, std::nullopt};
  }

  auto token = make_token();
  if (!token.has_value()) {
    return {CredentialStatus::EntropyFailure, std::nullopt};
  }

  const std::string fingerprint = impl_->fingerprint(*token);
  std::lock_guard lock(impl_->mutex);
  impl_->purge_locked(now);

  const auto session_iterator = impl_->sessions.find(session.session_id_);
  if (session_iterator == impl_->sessions.end()) {
    return {CredentialStatus::SessionRevoked, std::nullopt};
  }
  const Impl::Record& active_session = session_iterator->second;
  if (now >= active_session.expires_at) {
    return {CredentialStatus::SessionExpired, std::nullopt};
  }
  if (active_session.binding != session.binding_ ||
      !(active_session.capabilities == session.capabilities_) ||
      active_session.expires_at != session.expires_at_) {
    return {CredentialStatus::SessionRevoked, std::nullopt};
  }

  const auto generation_iterator =
      impl_->latest_generations.find(binding_key(active_session.binding));
  if (generation_iterator == impl_->latest_generations.end() ||
      generation_iterator->second != active_session.binding.generation) {
    return {CredentialStatus::StaleGeneration, std::nullopt};
  }

  const AccessDuration session_remaining =
      remaining(now, active_session.expires_at);
  const AccessDuration bounded =
      std::min(session_remaining, impl_->bounded_ttl(ttl));
  if (bounded <= AccessDuration::zero()) {
    return {CredentialStatus::SessionExpired, std::nullopt};
  }
  if (impl_->credentials.size() + impl_->sessions.size() >=
      impl_->policy.max_live_credentials) {
    return {CredentialStatus::CapacityExceeded, std::nullopt};
  }

  IssuedCredential credential;
  credential.token_ = std::move(*token);
  credential.binding_ = session.binding_;
  credential.capabilities_.insert(capability);
  credential.issued_at_ = now;
  credential.expires_at_ = now + bounded;

  impl_->credentials.emplace(
      fingerprint,
      Impl::Record{credential.binding_, credential.capabilities_,
                   credential.expires_at_, session.session_id_});
  return {CredentialStatus::Ok, std::move(credential)};
}

bool AccessCredentialStore::revoke(std::string_view cookie) {
  if (cookie.empty()) {
    return false;
  }
  const std::string fingerprint = impl_->fingerprint(cookie);
  const AccessTime now = impl_->now();
  std::lock_guard lock(impl_->mutex);

  const auto credential_iterator = impl_->credentials.find(fingerprint);
  if (credential_iterator != impl_->credentials.end()) {
    impl_->retired[fingerprint] = {
        now + std::chrono::hours(1), CredentialStatus::Revoked};
    impl_->revoke_children_locked(fingerprint, now);
    impl_->credentials.erase(credential_iterator);
    return true;
  }

  const auto session_iterator = impl_->sessions.find(fingerprint);
  if (session_iterator != impl_->sessions.end()) {
    impl_->retired[fingerprint] = {
        now + std::chrono::hours(1), CredentialStatus::Revoked};
    impl_->revoke_children_locked(fingerprint, now);
    impl_->sessions.erase(session_iterator);
    return true;
  }
  return false;
}

bool AccessCredentialStore::revoke(const AccessGrant& grant) {
  if (!grant.valid_) {
    return false;
  }
  const AccessTime now = impl_->now();
  std::lock_guard lock(impl_->mutex);
  const auto session_iterator = impl_->sessions.find(grant.session_id_);
  if (session_iterator == impl_->sessions.end()) {
    return false;
  }
  impl_->revoke_children_locked(grant.session_id_, now);
  impl_->sessions.erase(session_iterator);
  return true;
}

ConnectionGeneration AccessCredentialStore::next_generation(
    std::string_view user, std::string_view client_address,
    DisplayId display) {
  if (user.empty() || client_address.empty() ||
      !impl_->display_allowed(display)) {
    return 0;
  }

  std::lock_guard lock(impl_->mutex);
  const BindingKey key{std::string(user), std::string(client_address), display};
  ConnectionGeneration& generation = impl_->latest_generations[key];
  if (generation == 0 ||
      generation == std::numeric_limits<ConnectionGeneration>::max()) {
    generation = 1;
  } else {
    ++generation;
  }
  return generation;
}

bool AccessCredentialStore::end_connection(
    std::string_view user, std::string_view client_address, DisplayId display,
    ConnectionGeneration generation) {
  if (user.empty() || client_address.empty() || generation == 0) {
    return false;
  }

  const AccessTime now = impl_->now();
  std::lock_guard lock(impl_->mutex);
  const BindingKey key{std::string(user), std::string(client_address), display};
  const auto generation_iterator = impl_->latest_generations.find(key);
  if (generation_iterator == impl_->latest_generations.end() ||
      generation_iterator->second != generation) {
    return false;
  }

  for (auto iterator = impl_->credentials.begin();
       iterator != impl_->credentials.end();) {
    if (iterator->second.binding.generation == generation &&
        binding_key(iterator->second.binding) == key) {
      impl_->retired[iterator->first] = {
          now + std::chrono::hours(1), CredentialStatus::Revoked};
      iterator = impl_->credentials.erase(iterator);
    } else {
      ++iterator;
    }
  }
  for (auto iterator = impl_->sessions.begin();
       iterator != impl_->sessions.end();) {
    if (iterator->second.binding.generation == generation &&
        binding_key(iterator->second.binding) == key) {
      iterator = impl_->sessions.erase(iterator);
    } else {
      ++iterator;
    }
  }

  generation_iterator->second =
      generation == std::numeric_limits<ConnectionGeneration>::max()
          ? generation
          : generation + 1;
  return true;
}

std::optional<ConnectionGeneration>
AccessCredentialStore::current_generation(std::string_view user,
                                          std::string_view client_address,
                                          DisplayId display) const {
  std::lock_guard lock(impl_->mutex);
  const auto iterator = impl_->latest_generations.find(
      BindingKey{std::string(user), std::string(client_address), display});
  if (iterator == impl_->latest_generations.end()) {
    return std::nullopt;
  }
  return iterator->second;
}

}  // namespace netsec
