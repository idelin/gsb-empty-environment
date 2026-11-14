#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <memory>
#include <optional>
#include <span>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

using Byte = std::uint8_t;
using Bytes = std::vector<Byte>;
using Clock = std::function<std::uint64_t()>;

inline constexpr std::size_t kMasterKeyBytes = 32;

// Root key held by the snapshot control plane.  It must never be stored with
// snapshot metadata.  from_secret() accepts an arbitrary-length secret and
// hashes it to a 32-byte key, so a KMS-provided secret can be imported
// directly.
struct MasterKey {
    std::array<Byte, kMasterKeyBytes> bytes{};

    static MasterKey from_secret(std::span<const Byte> secret);
    static MasterKey generate();
};

// Identity established by the caller's authentication layer.  In particular,
// tenant_id and subject_id must come from the verified session, never from a
// mount request body.
struct Principal {
    std::string subject_id;
    std::string tenant_id;
};

// Control-plane view of a VM.  lifecycle_epoch changes when a VM is recreated;
// snapshots from an older epoch are therefore not mountable.  If disk_ids is
// non-empty, snapshots may only be issued for one of those disks.
struct VmRecord {
    std::string tenant_id;
    std::string vm_id;
    std::string owner_subject_id;
    std::uint64_t lifecycle_epoch = 1;
    std::vector<std::string> disk_ids;
};

struct Policy {
    // When true (the secure default), issuing a newer snapshot makes all older
    // snapshots of that VM stale.  This prevents rollback to a pre-patch or
    // pre-deletion disk image.
    bool latest_only = true;

    // 0 disables age-based expiry.  A non-zero value rejects snapshots whose
    // age is greater than this many seconds.
    std::uint64_t max_snapshot_age_seconds = 0;

    // Upper bound for an untrusted serialized metadata object.  It protects the
    // service from attacker-supplied huge blobs.
    std::size_t max_metadata_bytes = 1024U * 1024U;
};

enum class ErrorCode {
    ok = 0,
    invalid_argument,
    invalid_master_key,
    vm_conflict,
    vm_not_found,
    vm_deleted,
    vm_epoch_mismatch,
    vm_mismatch,
    tenant_mismatch,
    principal_not_owner,
    disk_mismatch,
    key_mismatch,
    metadata_malformed,
    metadata_authentication_failed,
    stale_snapshot,
    snapshot_revoked,
    clock_error,
    internal_error,
};

std::string_view to_string(ErrorCode code) noexcept;

struct IssueResult {
    ErrorCode code = ErrorCode::ok;
    Bytes sealed_metadata;
    std::uint64_t sequence = 0;
    std::uint64_t created_at = 0;
    // Opaque, randomly generated reference.  It is not derived from the
    // tenant, VM or disk identifier and cannot be used to enumerate snapshots.
    std::string snapshot_ref;

    bool ok() const noexcept { return code == ErrorCode::ok; }
    explicit operator bool() const noexcept { return ok(); }
};

struct MountRequest {
    Principal principal;

    // Optional caller-supplied target.  If non-empty it must match the VM/disk
    // bound into the encrypted metadata.
    std::string vm_id;
    std::string disk_id;

    // Optional key presented by the mount path (for example, a key fetched by
    // key id).  If present it must match the key bound at snapshot creation.
    // When absent, MountResult::data_key contains the authenticated key.
    std::optional<Bytes> presented_key;
};

struct MountResult {
    ErrorCode code = ErrorCode::ok;
    Bytes data_key;
    std::string snapshot_ref;
    std::uint64_t sequence = 0;

    bool allowed() const noexcept { return code == ErrorCode::ok; }
    explicit operator bool() const noexcept { return allowed(); }
};

// Thread-safe, importable ownership component for a C++20 snapshot service.
//
// The metadata handed to storage is an authenticated ciphertext.  Tenant, VM,
// disk and encryption-key identifiers exist only inside that ciphertext; the
// envelope contains only a version, a random salt, a random nonce, ciphertext
// and an authentication tag.
class SnapshotOwnershipService {
public:
    explicit SnapshotOwnershipService(MasterKey master, Policy policy = {});
    SnapshotOwnershipService(MasterKey master, Policy policy, Clock clock);
    ~SnapshotOwnershipService();

    SnapshotOwnershipService(const SnapshotOwnershipService&) = delete;
    SnapshotOwnershipService& operator=(const SnapshotOwnershipService&) = delete;
    SnapshotOwnershipService(SnapshotOwnershipService&&) noexcept;
    SnapshotOwnershipService& operator=(SnapshotOwnershipService&&) noexcept;

    // Registers a new VM or updates an existing VM at the same epoch.
    // Recreating a deleted VM requires a strictly greater lifecycle_epoch.
    ErrorCode register_vm(const VmRecord& record);

    // Leaves a durable tombstone.  Old snapshots cannot revive the VM.
    ErrorCode delete_vm(std::string_view tenant_id, std::string_view vm_id);

    // Issues and seals a snapshot.  The caller must be the authenticated owner
    // of the VM, and the VM must be active.  The sequence is monotonic per VM.
    IssueResult take_snapshot(const Principal& owner,
                              std::string_view vm_id,
                              std::string_view disk_id,
                              const Bytes& data_key);

    // Authenticates the metadata and verifies that principal, tenant, VM,
    // disk, lifecycle epoch, sequence and key all belong together.  Only an
    // allowed result releases the data key.
    MountResult mount(const Bytes& sealed_metadata, const MountRequest& request) const;
    MountResult mount(const Bytes& sealed_metadata, const Principal& principal) const;

    // Revokes one snapshot after authenticating the caller as its owner.
    ErrorCode revoke_snapshot(const Principal& owner, const Bytes& sealed_metadata);

    // Advances the anti-rollback floor for a VM.  This is a trusted
    // control-plane operation.
    ErrorCode revoke_snapshots_before(std::string_view tenant_id,
                                      std::string_view vm_id,
                                      std::uint64_t sequence);

private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
};

using SnapshotOwnership = SnapshotOwnershipService;

}  // namespace netsec
