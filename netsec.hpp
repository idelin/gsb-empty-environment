#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <memory>
#include <optional>
#include <span>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

using Byte = std::uint8_t;
using Bytes = std::vector<Byte>;
using Digest = std::array<Byte, 32>;

struct HostId {
  std::string value;

  friend bool operator==(const HostId&, const HostId&) = default;
};

struct VmId {
  std::string value;

  friend bool operator==(const VmId&, const VmId&) = default;
};

struct Endpoint {
  std::string host;
  std::uint16_t port{};

  friend bool operator==(const Endpoint&, const Endpoint&) = default;
};

struct SecretKey {
  std::array<Byte, 32> bytes{};

  static SecretKey from_bytes(std::span<const Byte> input);
  static SecretKey from_hex(std::string_view hex);
  std::string to_hex() const;

  friend bool operator==(const SecretKey&, const SecretKey&) = default;
};

struct TicketBinding {
  HostId source;
  HostId target;
  VmId vm_id;
  std::uint64_t generation{};

  friend bool operator==(const TicketBinding&, const TicketBinding&) = default;
};

struct MigrationTicket {
  std::string id;
  SecretKey secret;
  TicketBinding binding;
};

struct SourceConfig {
  HostId source_id;
  HostId target_id;
  Endpoint target_endpoint;
  SecretKey source_auth_key;
  SecretKey target_auth_key;
  std::uint64_t max_pages{1'000'000};
  std::uint64_t max_page_bytes{16ULL * 1024ULL * 1024ULL};
  std::uint64_t max_total_bytes{16ULL * 1024ULL * 1024ULL * 1024ULL};
};

struct TargetConfig {
  HostId target_id;
  HostId source_id;
  Endpoint endpoint;
  SecretKey source_auth_key;
  SecretKey target_auth_key;
  std::uint64_t max_pages{1'000'000};
  std::uint64_t max_page_bytes{16ULL * 1024ULL * 1024ULL};
  std::uint64_t max_total_bytes{16ULL * 1024ULL * 1024ULL * 1024ULL};
};

struct MemoryPage {
  std::uint64_t index{};
  std::uint64_t guest_address{};
  Bytes data;
};

struct MemoryImage {
  VmId vm_id;
  std::uint64_t generation{};
  std::vector<MemoryPage> pages;
  Digest manifest_checksum{};
};

enum class ErrorCode : std::uint8_t {
  none = 0,
  invalid_config,
  invalid_request,
  transport_error,
  malformed_message,
  authentication_failed,
  endpoint_mismatch,
  binding_mismatch,
  ticket_unknown,
  ticket_consumed,
  page_validation_failed,
  manifest_mismatch,
  completion_mismatch,
  internal_error,
};

struct MigrationResult {
  bool ok() const noexcept { return error == ErrorCode::none; }

  ErrorCode error{ErrorCode::none};
  std::string detail;
  Digest manifest_checksum{};
};

struct TargetMigrationResult {
  bool ok() const noexcept { return error == ErrorCode::none; }

  ErrorCode error{ErrorCode::none};
  std::string detail;
  std::optional<MemoryImage> committed;
};

// A reliable, message-oriented duplex link. One send() produces exactly one
// receive() on the peer, and vice versa. Implementations may block. The
// component never treats transport-supplied addresses as authoritative.
class MigrationChannel {
 public:
  virtual ~MigrationChannel() = default;
  virtual bool send(std::span<const Byte> message) = 0;
  virtual std::optional<Bytes> receive() = 0;
};

// A trusted control-plane issuer. A ticket is a bearer capability for exactly
// one authenticated migration session; the target atomically consumes it.
class TicketAuthority {
 public:
  TicketAuthority();
  ~TicketAuthority();

  TicketAuthority(const TicketAuthority&) = delete;
  TicketAuthority& operator=(const TicketAuthority&) = delete;
  TicketAuthority(TicketAuthority&&) noexcept;
  TicketAuthority& operator=(TicketAuthority&&) noexcept;

  MigrationTicket issue(const TicketBinding& binding);

 private:
  struct Impl;
  std::shared_ptr<Impl> impl_;

  friend struct TicketAuthorityAccess;
  friend class MigrationTarget;
};

struct MigrationRequest;

class MigrationSource {
 public:
  explicit MigrationSource(SourceConfig config);

  MigrationSource(const MigrationSource&) = delete;
  MigrationSource& operator=(const MigrationSource&) = delete;
  MigrationSource(MigrationSource&&) noexcept;
  MigrationSource& operator=(MigrationSource&&) noexcept;
  ~MigrationSource();

  MigrationResult migrate(MigrationChannel& channel,
                          const MigrationRequest& request);

 private:
  struct Impl;
  std::unique_ptr<Impl> impl_;
};

struct MigrationRequest {
  VmId vm_id;
  std::uint64_t generation{};
  MigrationTicket ticket;
  std::vector<MemoryPage> pages;
};

class MigrationTarget {
 public:
  MigrationTarget(TargetConfig config,
                  std::shared_ptr<TicketAuthority> tickets);
  ~MigrationTarget();

  MigrationTarget(const MigrationTarget&) = delete;
  MigrationTarget& operator=(const MigrationTarget&) = delete;
  MigrationTarget(MigrationTarget&&) noexcept;
  MigrationTarget& operator=(MigrationTarget&&) noexcept;

  TargetMigrationResult accept(MigrationChannel& channel);

 private:
  struct Impl;
  std::unique_ptr<Impl> impl_;
};

// The same values are checked by both endpoints. They cover page identity,
// guest address, and contents, so a valid checksum cannot be transplanted.
Digest compute_page_checksum(const VmId& vm_id, std::uint64_t generation,
                             const MemoryPage& page);
Digest compute_manifest_checksum(const VmId& vm_id, std::uint64_t generation,
                                 std::span<const MemoryPage> pages);

std::string to_hex(std::span<const Byte> bytes);

}  // namespace netsec
