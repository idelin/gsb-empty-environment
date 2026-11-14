#pragma once

#include <array>
#include <chrono>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <initializer_list>
#include <memory>
#include <optional>
#include <set>
#include <string>
#include <string_view>

namespace netsec {

using DisplayId = std::uint32_t;
using ConnectionGeneration = std::uint64_t;
using AccessClock = std::chrono::system_clock;
using AccessTime = AccessClock::time_point;
using AccessDuration = std::chrono::milliseconds;

inline constexpr AccessDuration kDefaultCredentialTtl =
    std::chrono::seconds(30);

// Display access and data-egress permissions are deliberately distinct.
// A display cookie never carries clipboard or file-transfer rights.
enum class Capability : std::uint8_t {
  ViewDisplay = 1u << 0,
  View = ViewDisplay,
  ClipboardRead = 1u << 1,
  ClipboardWrite = 1u << 2,
  Clipboard = (1u << 1) | (1u << 2),
  FileTransfer = 1u << 3,
};

class CapabilitySet {
 public:
  constexpr CapabilitySet() noexcept = default;
  constexpr CapabilitySet(Capability capability) noexcept
      : bits_(mask(capability)) {}

  CapabilitySet(std::initializer_list<Capability> capabilities) noexcept {
    for (Capability capability : capabilities) {
      insert(capability);
    }
  }

  constexpr bool empty() const noexcept { return bits_ == 0; }
  constexpr std::uint8_t bits() const noexcept { return bits_; }

  constexpr bool contains(Capability capability) const noexcept {
    const auto requested = mask(capability);
    return requested != 0 && (bits_ & requested) == requested;
  }

  constexpr void insert(Capability capability) noexcept {
    bits_ = static_cast<std::uint8_t>(bits_ | mask(capability));
  }

  constexpr void erase(Capability capability) noexcept {
    bits_ = static_cast<std::uint8_t>(bits_ & ~mask(capability));
  }

  friend constexpr bool operator==(CapabilitySet lhs,
                                   CapabilitySet rhs) noexcept {
    return lhs.bits_ == rhs.bits_;
  }

 private:
  static constexpr std::uint8_t mask(Capability capability) noexcept {
    return static_cast<std::uint8_t>(capability);
  }

  std::uint8_t bits_{0};
};

// This is the complete security binding of a display credential. The
// generation is the proxy's monotonic connection generation, not a session
// identifier chosen by the client.
struct ConnectionIdentity {
  std::string user;
  std::string client_address;
  DisplayId display{0};
  ConnectionGeneration generation{0};

  friend bool operator==(const ConnectionIdentity&,
                         const ConnectionIdentity&) = default;
};

using CredentialBinding = ConnectionIdentity;
using ClientContext = ConnectionIdentity;

enum class CredentialStatus : std::uint8_t {
  Ok = 0,
  InvalidCookie,
  AlreadyUsed,
  Expired,
  Revoked,
  InvalidRequest,
  UserMismatch,
  AddressMismatch,
  DisplayMismatch,
  DisplayNotAllowed,
  GenerationMismatch,
  StaleGeneration,
  SessionExpired,
  SessionRevoked,
  CapabilityDenied,
  CapacityExceeded,
  EntropyFailure,
};

std::string_view to_string(CredentialStatus status) noexcept;

class AccessGrant;

// A bearer value returned at issuance. It is intentionally opaque: binding
// data is kept in the store, so a leaked value reveals no user, address, or
// display information.
class IssuedCredential {
 public:
  IssuedCredential() = default;

  const std::string& token() const noexcept { return token_; }
  const std::string& cookie() const noexcept { return token_; }
  std::string_view view() const noexcept { return token_; }
  bool valid() const noexcept { return !token_.empty(); }

  const ConnectionIdentity& binding() const noexcept { return binding_; }
  const std::string& user() const noexcept { return binding_.user; }
  const std::string& client_address() const noexcept {
    return binding_.client_address;
  }
  DisplayId display() const noexcept { return binding_.display; }
  ConnectionGeneration generation() const noexcept {
    return binding_.generation;
  }
  const CapabilitySet& capabilities() const noexcept { return capabilities_; }
  AccessTime issued_at() const noexcept { return issued_at_; }
  AccessTime expires_at() const noexcept { return expires_at_; }

 private:
  friend class AccessCredentialStore;

  std::string token_;
  ConnectionIdentity binding_;
  CapabilitySet capabilities_;
  AccessTime issued_at_{};
  AccessTime expires_at_{};
};

using AccessCookie = IssuedCredential;

// A redeemed, short-lived authorization. It remains bound to exactly one
// display and one connection generation. Clipboard and file transfer are not
// present unless they were issued as a separate capability ticket.
class AccessGrant {
 public:
  AccessGrant() = default;

  bool valid() const noexcept { return valid_; }
  explicit operator bool() const noexcept { return valid_; }

  bool allows(Capability capability) const noexcept {
    return valid_ && capabilities_.contains(capability);
  }

  bool allows(Capability capability, DisplayId display) const noexcept {
    return allows(capability) && display == binding_.display;
  }

  bool allows(Capability capability, DisplayId display,
              AccessTime now) const noexcept {
    return now < expires_at_ && allows(capability, display);
  }

  bool allows_display(DisplayId display) const noexcept {
    return valid_ && display == binding_.display;
  }

  bool allows_display(DisplayId display, AccessTime now) const noexcept {
    return now < expires_at_ && allows_display(display);
  }

  const ConnectionIdentity& binding() const noexcept { return binding_; }
  const std::string& user() const noexcept { return binding_.user; }
  const std::string& client_address() const noexcept {
    return binding_.client_address;
  }
  DisplayId display() const noexcept { return binding_.display; }
  ConnectionGeneration generation() const noexcept {
    return binding_.generation;
  }
  const CapabilitySet& capabilities() const noexcept { return capabilities_; }
  AccessTime expires_at() const noexcept { return expires_at_; }

 private:
  friend class AccessCredentialStore;

  std::string session_id_;
  ConnectionIdentity binding_;
  CapabilitySet capabilities_;
  AccessTime expires_at_{};
  bool valid_{false};
};

struct IssuanceResult {
  CredentialStatus status{CredentialStatus::InvalidRequest};
  std::optional<IssuedCredential> credential;

  bool ok() const noexcept {
    return status == CredentialStatus::Ok && credential.has_value();
  }
  explicit operator bool() const noexcept { return ok(); }
};

struct RedemptionResult {
  CredentialStatus status{CredentialStatus::InvalidCookie};
  std::optional<AccessGrant> grant;

  bool ok() const noexcept {
    return status == CredentialStatus::Ok && grant.has_value();
  }
  explicit operator bool() const noexcept { return ok(); }
};

struct AuthorizationResult {
  CredentialStatus status{CredentialStatus::InvalidRequest};

  bool ok() const noexcept { return status == CredentialStatus::Ok; }
  explicit operator bool() const noexcept { return ok(); }
};

struct CredentialOptions {
  AccessDuration ttl{kDefaultCredentialTtl};
};

class AccessCredentialStore {
 public:
  struct Policy {
    // Empty means that the proxy has not imposed a display allowlist. When
    // non-empty, issuance and redemption are restricted to these displays.
    std::set<DisplayId> allowed_displays;
    AccessDuration max_ttl{std::chrono::minutes(5)};
    std::size_t max_live_credentials{4096};
  };

  using ClockFunction = std::function<AccessTime()>;

  AccessCredentialStore();
  explicit AccessCredentialStore(Policy policy);
  AccessCredentialStore(Policy policy, ClockFunction clock);
  ~AccessCredentialStore();

  AccessCredentialStore(AccessCredentialStore&&) noexcept;
  AccessCredentialStore& operator=(AccessCredentialStore&&) noexcept;
  AccessCredentialStore(const AccessCredentialStore&) = delete;
  AccessCredentialStore& operator=(const AccessCredentialStore&) = delete;

  // Issues a one-time display credential with ViewDisplay only. The identity
  // must contain a non-zero generation; use next_generation() to advance it
  // when a new graphical connection starts.
  IssuanceResult issue(const ConnectionIdentity& binding,
                       CredentialOptions options = {});
  IssuanceResult issue(const ConnectionIdentity& binding,
                       CredentialOptions options, AccessTime now);

  IssuanceResult issue(std::string_view user, std::string_view client_address,
                       DisplayId display, ConnectionGeneration generation,
                       AccessDuration ttl = kDefaultCredentialTtl);
  IssuanceResult issue(std::string_view user, std::string_view client_address,
                       DisplayId display, ConnectionGeneration generation,
                       AccessDuration ttl, AccessTime now);

  // Redeems a cookie exactly once. A failed binding check does not consume
  // the cookie, so an off-path attacker cannot deny service to its owner.
  RedemptionResult redeem(std::string_view cookie,
                          const ConnectionIdentity& presented);
  RedemptionResult redeem(std::string_view cookie,
                          const ConnectionIdentity& presented, AccessTime now);
  RedemptionResult redeem(std::string_view cookie, std::string_view user,
                          std::string_view client_address, DisplayId display,
                          ConnectionGeneration generation);
  RedemptionResult redeem(std::string_view cookie, std::string_view user,
                          std::string_view client_address, DisplayId display,
                          ConnectionGeneration generation, AccessTime now);

  // Enforces the grant's lifetime, display, capability, and current
  // generation. Proxy code should call this immediately before exposing a
  // display, clipboard, or file-transfer operation.
  AuthorizationResult authorize(const AccessGrant& grant,
                                Capability capability,
                                DisplayId display) const;
  AuthorizationResult authorize(const AccessGrant& grant,
                                Capability capability, DisplayId display,
                                AccessTime now) const;

  // Creates a separately authorized, one-time ticket for clipboard or file
  // transfer. It inherits the session's user/address/display/generation and
  // can never extend past the session lifetime.
  IssuanceResult issue_capability(const AccessGrant& session,
                                  Capability capability,
                                  AccessDuration ttl = kDefaultCredentialTtl);
  IssuanceResult issue_capability(const AccessGrant& session,
                                  Capability capability, AccessDuration ttl,
                                  AccessTime now);

  IssuanceResult authorize_action(const AccessGrant& session,
                                  Capability capability,
                                  AccessDuration ttl = kDefaultCredentialTtl) {
    return issue_capability(session, capability, ttl);
  }
  IssuanceResult authorize_action(const AccessGrant& session,
                                  Capability capability, AccessDuration ttl,
                                  AccessTime now) {
    return issue_capability(session, capability, ttl, now);
  }

  // Revocation is immediate. Revoking a cookie removes the pending bearer
  // value; revoking a grant ends the display session.
  bool revoke(std::string_view cookie);
  bool revoke(const AccessGrant& grant);
  bool close(const AccessGrant& grant) { return revoke(grant); }

  // Atomically advances the generation for a user/address/display tuple and
  // makes older grants stale.
  ConnectionGeneration next_generation(std::string_view user,
                                       std::string_view client_address,
                                       DisplayId display);
  // Ends a connection now. Pending cookies and grants for this generation
  // stop working, and the next connection must use a newer generation.
  bool end_connection(std::string_view user, std::string_view client_address,
                      DisplayId display, ConnectionGeneration generation);
  std::optional<ConnectionGeneration> current_generation(
      std::string_view user, std::string_view client_address,
      DisplayId display) const;

  void purge_expired();
  void purge_expired(AccessTime now);

  const Policy& policy() const noexcept;

 private:
  class Impl;
  std::unique_ptr<Impl> impl_;
};

using CredentialStore = AccessCredentialStore;
using ValidationResult = RedemptionResult;

}  // namespace netsec
