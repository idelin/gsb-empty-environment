#include "netsec.hpp"

#include "netsec_crypto.hpp"

#include <algorithm>
#include <array>
#include <ctime>
#include <limits>
#include <map>
#include <mutex>
#include <set>
#include <stdexcept>
#include <utility>

namespace netsec {
namespace {

constexpr std::array<Byte, 4> kEnvelopeMagic = {'N', 'S', 'M', '1'};
constexpr Byte kEnvelopeVersion = 1;
constexpr Byte kAlgorithmChaCha20Poly1305 = 1;
constexpr Byte kPayloadVersion = 1;
constexpr std::size_t kSaltSize = 16;
constexpr std::size_t kNonceSize = 12;
constexpr std::size_t kTagSize = 16;
constexpr std::size_t kSnapshotIdSize = 16;
constexpr std::size_t kFingerprintSize = 32;
constexpr std::size_t kHeaderSize = 4 + 1 + 1 + 2 + kSaltSize + kNonceSize;
constexpr std::size_t kMaximumFieldSize = 4096;
constexpr std::size_t kMaximumKeySize = 4096;

constexpr std::string_view kKdfInfo = "netsec/snapshot-ownership/metadata/v1";

struct Binding {
    std::array<Byte, kSnapshotIdSize> snapshot_id{};
    std::string tenant_id;
    std::string vm_id;
    std::string disk_id;
    std::string owner_subject_id;
    std::uint64_t vm_lifecycle_epoch = 0;
    std::uint64_t sequence = 0;
    std::uint64_t created_at = 0;
    std::array<Byte, kFingerprintSize> key_fingerprint{};
    Bytes data_key;
};

struct VmState {
    VmRecord record;
    bool deleted = false;
    std::uint64_t latest_sequence = 0;
    std::uint64_t minimum_mount_sequence = 1;
};

void append_u16(Bytes& output, std::uint16_t value) {
    output.push_back(static_cast<Byte>(value));
    output.push_back(static_cast<Byte>(value >> 8U));
}

void append_u32(Bytes& output, std::uint32_t value) {
    for (unsigned int index = 0; index < 4; ++index) {
        output.push_back(static_cast<Byte>(value >> (8U * index)));
    }
}

void append_u64(Bytes& output, std::uint64_t value) {
    for (unsigned int index = 0; index < 8; ++index) {
        output.push_back(static_cast<Byte>(value >> (8U * index)));
    }
}

bool append_length_prefixed(Bytes& output, std::string_view value) {
    if (value.size() > kMaximumFieldSize ||
        value.size() > std::numeric_limits<std::uint32_t>::max()) {
        return false;
    }
    append_u32(output, static_cast<std::uint32_t>(value.size()));
    output.insert(output.end(), value.begin(), value.end());
    return true;
}

class Reader {
public:
    Reader(const Byte* data, std::size_t size) : data_(data), size_(size) {}

    bool read_u8(Byte& value) {
        if (remaining() < 1) {
            return false;
        }
        value = data_[position_++];
        return true;
    }

    bool read_u16(std::uint16_t& value) {
        if (remaining() < 2) {
            return false;
        }
        value = static_cast<std::uint16_t>(
            static_cast<std::uint16_t>(data_[position_]) |
            static_cast<std::uint16_t>(
                static_cast<std::uint16_t>(data_[position_ + 1]) << 8U));
        position_ += 2;
        return true;
    }

    bool read_u32(std::uint32_t& value) {
        if (remaining() < 4) {
            return false;
        }
        value = 0;
        for (unsigned int index = 0; index < 4; ++index) {
            value |= static_cast<std::uint32_t>(data_[position_ + index]) << (8U * index);
        }
        position_ += 4;
        return true;
    }

    bool read_u64(std::uint64_t& value) {
        if (remaining() < 8) {
            return false;
        }
        value = 0;
        for (unsigned int index = 0; index < 8; ++index) {
            value |= static_cast<std::uint64_t>(data_[position_ + index]) << (8U * index);
        }
        position_ += 8;
        return true;
    }

    bool read_bytes(std::size_t count, std::span<const Byte>& value) {
        if (remaining() < count) {
            return false;
        }
        value = std::span<const Byte>(data_ + position_, count);
        position_ += count;
        return true;
    }

    bool read_string(std::string& value) {
        std::uint32_t length = 0;
        if (!read_u32(length) || length > kMaximumFieldSize || remaining() < length) {
            return false;
        }
        value.assign(reinterpret_cast<const char*>(data_ + position_), length);
        position_ += length;
        return true;
    }

    bool at_end() const noexcept { return position_ == size_; }

private:
    std::size_t remaining() const noexcept { return size_ - position_; }

    const Byte* data_;
    std::size_t size_;
    std::size_t position_ = 0;
};

Bytes serialize_binding(const Binding& binding) {
    Bytes payload;
    payload.reserve(256 + binding.tenant_id.size() + binding.vm_id.size() +
                    binding.disk_id.size() + binding.owner_subject_id.size() +
                    binding.data_key.size());
    payload.push_back(kPayloadVersion);
    payload.push_back(0);  // flags
    append_u16(payload, 0);  // reserved
    payload.insert(payload.end(), binding.snapshot_id.begin(), binding.snapshot_id.end());
    append_u64(payload, binding.created_at);
    append_u64(payload, binding.vm_lifecycle_epoch);
    append_u64(payload, binding.sequence);
    append_length_prefixed(payload, binding.tenant_id);
    append_length_prefixed(payload, binding.vm_id);
    append_length_prefixed(payload, binding.disk_id);
    append_length_prefixed(payload, binding.owner_subject_id);
    payload.insert(payload.end(), binding.key_fingerprint.begin(),
                   binding.key_fingerprint.end());
    append_u32(payload, static_cast<std::uint32_t>(binding.data_key.size()));
    payload.insert(payload.end(), binding.data_key.begin(), binding.data_key.end());
    return payload;
}

bool deserialize_binding(std::span<const Byte> payload, Binding& binding) {
    Reader reader(payload.data(), payload.size());

    Byte version = 0;
    Byte flags = 0;
    std::uint16_t reserved = 0;
    if (!reader.read_u8(version) || !reader.read_u8(flags) || !reader.read_u16(reserved) ||
        version != kPayloadVersion || flags != 0 || reserved != 0) {
        return false;
    }

    std::span<const Byte> snapshot_id;
    if (!reader.read_bytes(kSnapshotIdSize, snapshot_id)) {
        return false;
    }
    std::copy(snapshot_id.begin(), snapshot_id.end(), binding.snapshot_id.begin());

    if (!reader.read_u64(binding.created_at) || !reader.read_u64(binding.vm_lifecycle_epoch) ||
        !reader.read_u64(binding.sequence) || !reader.read_string(binding.tenant_id) ||
        !reader.read_string(binding.vm_id) || !reader.read_string(binding.disk_id) ||
        !reader.read_string(binding.owner_subject_id)) {
        return false;
    }

    std::span<const Byte> fingerprint;
    if (!reader.read_bytes(kFingerprintSize, fingerprint)) {
        return false;
    }
    std::copy(fingerprint.begin(), fingerprint.end(), binding.key_fingerprint.begin());

    std::uint32_t key_size = 0;
    if (!reader.read_u32(key_size) || key_size == 0 || key_size > kMaximumKeySize) {
        return false;
    }
    std::span<const Byte> key;
    if (!reader.read_bytes(key_size, key)) {
        return false;
    }
    binding.data_key.assign(key.begin(), key.end());
    return reader.at_end() && !binding.tenant_id.empty() && !binding.vm_id.empty() &&
           !binding.disk_id.empty() && !binding.owner_subject_id.empty();
}

std::string to_hex(std::span<const Byte> bytes) {
    static constexpr char kHex[] = "0123456789abcdef";
    std::string output;
    output.reserve(bytes.size() * 2);
    for (Byte byte : bytes) {
        output.push_back(kHex[byte >> 4U]);
        output.push_back(kHex[byte & 0x0fU]);
    }
    return output;
}

bool valid_identifier(std::string_view value) {
    return !value.empty() && value.size() <= kMaximumFieldSize;
}

std::array<Byte, kFingerprintSize> fingerprint(std::span<const Byte> key) {
    return detail::sha256(key);
}

std::uint64_t default_clock() {
    return static_cast<std::uint64_t>(std::time(nullptr));
}

}  // namespace

struct SnapshotOwnershipService::Impl {
    MasterKey master;
    Policy policy;
    Clock clock;
    bool valid_master = false;
    mutable std::mutex mutex;
    std::map<std::pair<std::string, std::string>, VmState> vms;
    std::set<std::string> revoked_snapshots;

    bool valid_master_key() const noexcept {
        return valid_master && policy.max_metadata_bytes >= kHeaderSize + kTagSize;
    }

    Bytes seal(const Binding& binding) const {
        const auto salt = detail::random_bytes(kSaltSize);
        const auto nonce = detail::random_bytes(kNonceSize);
        const std::span<const Byte, kNonceSize> nonce_span(nonce.data(), kNonceSize);

        const auto metadata_key = detail::hkdf_sha256(master.bytes, salt,
                                                      std::span<const Byte>(
                                                          reinterpret_cast<const Byte*>(kKdfInfo.data()),
                                                          kKdfInfo.size()));

        Bytes header;
        header.reserve(kHeaderSize);
        header.insert(header.end(), kEnvelopeMagic.begin(), kEnvelopeMagic.end());
        header.push_back(kEnvelopeVersion);
        header.push_back(kAlgorithmChaCha20Poly1305);
        append_u16(header, 0);
        header.insert(header.end(), salt.begin(), salt.end());
        header.insert(header.end(), nonce.begin(), nonce.end());

        const auto payload = serialize_binding(binding);
        const auto sealed = detail::chacha20_poly1305_seal(
            metadata_key, nonce_span, header, payload);

        Bytes output;
        output.reserve(header.size() + sealed.ciphertext.size() + sealed.tag.size());
        output.insert(output.end(), header.begin(), header.end());
        output.insert(output.end(), sealed.ciphertext.begin(), sealed.ciphertext.end());
        output.insert(output.end(), sealed.tag.begin(), sealed.tag.end());
        return output;
    }

    bool open(const Bytes& sealed_metadata, Binding& binding, ErrorCode& error) const {
        if (!valid_master_key()) {
            error = ErrorCode::invalid_master_key;
            return false;
        }
        if (sealed_metadata.size() < kHeaderSize + kTagSize ||
            sealed_metadata.size() > policy.max_metadata_bytes) {
            error = ErrorCode::metadata_malformed;
            return false;
        }
        if (!std::equal(kEnvelopeMagic.begin(), kEnvelopeMagic.end(), sealed_metadata.begin())) {
            error = ErrorCode::metadata_malformed;
            return false;
        }

        const Byte version = sealed_metadata[4];
        const Byte algorithm = sealed_metadata[5];
        const std::uint16_t reserved = static_cast<std::uint16_t>(
            static_cast<std::uint16_t>(sealed_metadata[6]) |
            static_cast<std::uint16_t>(
                static_cast<std::uint16_t>(sealed_metadata[7]) << 8U));
        if (version != kEnvelopeVersion || algorithm != kAlgorithmChaCha20Poly1305 ||
            reserved != 0) {
            error = ErrorCode::metadata_malformed;
            return false;
        }

        const auto salt = std::span<const Byte>(sealed_metadata.data() + 8, kSaltSize);
        const auto nonce = std::span<const Byte, kNonceSize>(
            sealed_metadata.data() + 8 + kSaltSize, kNonceSize);
        const auto metadata_key = detail::hkdf_sha256(master.bytes, salt,
                                                      std::span<const Byte>(
                                                          reinterpret_cast<const Byte*>(kKdfInfo.data()),
                                                          kKdfInfo.size()));
        const auto header = std::span<const Byte>(sealed_metadata.data(), kHeaderSize);
        const auto ciphertext = std::span<const Byte>(
            sealed_metadata.data() + kHeaderSize,
            sealed_metadata.size() - kHeaderSize - kTagSize);
        const auto tag = std::span<const Byte, kTagSize>(
            sealed_metadata.data() + sealed_metadata.size() - kTagSize, kTagSize);

        Bytes payload;
        if (!detail::chacha20_poly1305_open(metadata_key, nonce, header, ciphertext, tag,
                                            payload)) {
            error = ErrorCode::metadata_authentication_failed;
            return false;
        }
        if (!deserialize_binding(payload, binding)) {
            error = ErrorCode::metadata_malformed;
            return false;
        }
        error = ErrorCode::ok;
        return true;
    }

    ErrorCode register_vm(const VmRecord& record) {
        if (!valid_identifier(record.tenant_id) || !valid_identifier(record.vm_id) ||
            !valid_identifier(record.owner_subject_id) || record.lifecycle_epoch == 0) {
            return ErrorCode::invalid_argument;
        }
        for (const auto& disk_id : record.disk_ids) {
            if (!valid_identifier(disk_id)) {
                return ErrorCode::invalid_argument;
            }
        }

        std::lock_guard lock(mutex);
        const auto key = std::make_pair(record.tenant_id, record.vm_id);
        auto found = vms.find(key);
        if (found == vms.end()) {
            VmState state;
            state.record = record;
            vms.emplace(key, std::move(state));
            return ErrorCode::ok;
        }

        VmState& state = found->second;
        if (state.deleted) {
            if (record.lifecycle_epoch <= state.record.lifecycle_epoch) {
                return ErrorCode::vm_conflict;
            }
            state.record = record;
            state.deleted = false;
            state.latest_sequence = 0;
            state.minimum_mount_sequence = 1;
            return ErrorCode::ok;
        }

        if (record.lifecycle_epoch < state.record.lifecycle_epoch) {
            return ErrorCode::vm_conflict;
        }
        if (record.lifecycle_epoch > state.record.lifecycle_epoch) {
            state.record = record;
            state.latest_sequence = 0;
            state.minimum_mount_sequence = 1;
            return ErrorCode::ok;
        }

        // Same lifecycle epoch: owner/disk metadata may be refreshed, but the
        // anti-rollback counters are intentionally preserved.
        state.record.owner_subject_id = record.owner_subject_id;
        state.record.disk_ids = record.disk_ids;
        return ErrorCode::ok;
    }

    ErrorCode delete_vm(std::string_view tenant_id, std::string_view vm_id) {
        if (!valid_identifier(tenant_id) || !valid_identifier(vm_id)) {
            return ErrorCode::invalid_argument;
        }
        std::lock_guard lock(mutex);
        const auto found = vms.find(std::make_pair(std::string(tenant_id), std::string(vm_id)));
        if (found == vms.end()) {
            return ErrorCode::vm_not_found;
        }
        found->second.deleted = true;
        return ErrorCode::ok;
    }

    IssueResult take_snapshot(const Principal& owner, std::string_view vm_id,
                              std::string_view disk_id, const Bytes& data_key) {
        IssueResult result;
        if (!valid_master_key()) {
            result.code = ErrorCode::invalid_master_key;
            return result;
        }
        if (!valid_identifier(owner.subject_id) || !valid_identifier(owner.tenant_id) ||
            !valid_identifier(vm_id) || !valid_identifier(disk_id) || data_key.empty() ||
            data_key.size() > kMaximumKeySize) {
            result.code = ErrorCode::invalid_argument;
            return result;
        }

        std::lock_guard lock(mutex);
        const auto found =
            vms.find(std::make_pair(owner.tenant_id, std::string(vm_id)));
        if (found == vms.end()) {
            result.code = ErrorCode::vm_not_found;
            return result;
        }
        VmState& state = found->second;
        if (state.deleted) {
            result.code = ErrorCode::vm_deleted;
            return result;
        }
        if (state.record.tenant_id != owner.tenant_id) {
            result.code = ErrorCode::tenant_mismatch;
            return result;
        }
        if (state.record.owner_subject_id != owner.subject_id) {
            result.code = ErrorCode::principal_not_owner;
            return result;
        }
        if (!state.record.disk_ids.empty() &&
            std::find(state.record.disk_ids.begin(), state.record.disk_ids.end(), disk_id) ==
                state.record.disk_ids.end()) {
            result.code = ErrorCode::disk_mismatch;
            return result;
        }
        if (state.latest_sequence == std::numeric_limits<std::uint64_t>::max()) {
            result.code = ErrorCode::internal_error;
            return result;
        }

        std::uint64_t now = 0;
        try {
            now = clock ? clock() : default_clock();
        } catch (...) {
            result.code = ErrorCode::clock_error;
            return result;
        }

        Binding binding;
        const auto snapshot_id = detail::random_bytes(kSnapshotIdSize);
        std::copy(snapshot_id.begin(), snapshot_id.end(), binding.snapshot_id.begin());
        binding.tenant_id = state.record.tenant_id;
        binding.vm_id = state.record.vm_id;
        binding.disk_id = std::string(disk_id);
        binding.owner_subject_id = state.record.owner_subject_id;
        binding.vm_lifecycle_epoch = state.record.lifecycle_epoch;
        binding.sequence = state.latest_sequence + 1;
        binding.created_at = now;
        binding.data_key = data_key;
        binding.key_fingerprint = fingerprint(data_key);

        try {
            result.sealed_metadata = seal(binding);
        } catch (...) {
            result.code = ErrorCode::internal_error;
            return result;
        }

        state.latest_sequence = binding.sequence;
        if (policy.latest_only) {
            state.minimum_mount_sequence = binding.sequence;
        }

        result.sequence = binding.sequence;
        result.created_at = binding.created_at;
        result.snapshot_ref = to_hex(binding.snapshot_id);
        result.code = ErrorCode::ok;
        return result;
    }

    MountResult mount(const Bytes& sealed_metadata, const MountRequest& request) const {
        MountResult result;
        if (!valid_identifier(request.principal.subject_id) ||
            !valid_identifier(request.principal.tenant_id)) {
            result.code = ErrorCode::invalid_argument;
            return result;
        }

        Binding binding;
        ErrorCode error = ErrorCode::ok;
        if (!open(sealed_metadata, binding, error)) {
            result.code = error;
            return result;
        }

        std::lock_guard lock(mutex);

        // The authenticated principal must be in the snapshot's tenant.  This
        // check happens before any VM lookup so a caller cannot probe another
        // tenant's VM namespace through this component.
        if (binding.tenant_id != request.principal.tenant_id) {
            result.code = ErrorCode::tenant_mismatch;
            return result;
        }

        const auto found =
            vms.find(std::make_pair(binding.tenant_id, binding.vm_id));
        if (found == vms.end()) {
            result.code = ErrorCode::vm_not_found;
            return result;
        }
        const VmState& state = found->second;
        if (state.deleted) {
            result.code = ErrorCode::vm_deleted;
            return result;
        }
        if (binding.vm_lifecycle_epoch != state.record.lifecycle_epoch) {
            result.code = ErrorCode::vm_epoch_mismatch;
            return result;
        }
        if (state.record.owner_subject_id != binding.owner_subject_id ||
            state.record.owner_subject_id != request.principal.subject_id) {
            result.code = ErrorCode::principal_not_owner;
            return result;
        }
        if (!request.vm_id.empty() && request.vm_id != binding.vm_id) {
            result.code = ErrorCode::vm_mismatch;
            return result;
        }
        if (!request.disk_id.empty() && request.disk_id != binding.disk_id) {
            result.code = ErrorCode::disk_mismatch;
            return result;
        }
        if (!state.record.disk_ids.empty() &&
            std::find(state.record.disk_ids.begin(), state.record.disk_ids.end(),
                      binding.disk_id) == state.record.disk_ids.end()) {
            result.code = ErrorCode::disk_mismatch;
            return result;
        }

        // Old/superseded snapshots cannot be replayed after a newer snapshot
        // or after the anti-rollback floor was advanced.
        if (binding.sequence < state.minimum_mount_sequence ||
            binding.sequence > state.latest_sequence) {
            result.code = ErrorCode::stale_snapshot;
            return result;
        }

        if (revoked_snapshots.find(to_hex(binding.snapshot_id)) !=
            revoked_snapshots.end()) {
            result.code = ErrorCode::snapshot_revoked;
            return result;
        }

        std::uint64_t now = 0;
        try {
            now = clock ? clock() : default_clock();
        } catch (...) {
            result.code = ErrorCode::clock_error;
            return result;
        }
        if (now < binding.created_at) {
            // A clock rollback or a future-dated snapshot is treated as an
            // anti-rollback violation rather than wrapping the age check.
            result.code = ErrorCode::stale_snapshot;
            return result;
        }
        if (policy.max_snapshot_age_seconds != 0 &&
            now - binding.created_at > policy.max_snapshot_age_seconds) {
            result.code = ErrorCode::stale_snapshot;
            return result;
        }

        if (request.presented_key.has_value()) {
            const auto presented_fingerprint = fingerprint(*request.presented_key);
            if (!detail::constant_time_equal(presented_fingerprint,
                                             binding.key_fingerprint)) {
                result.code = ErrorCode::key_mismatch;
                return result;
            }
        }

        result.data_key = binding.data_key;
        result.snapshot_ref = to_hex(binding.snapshot_id);
        result.sequence = binding.sequence;
        result.code = ErrorCode::ok;
        return result;
    }

    ErrorCode revoke_snapshot(const Principal& owner, const Bytes& sealed_metadata) {
        if (!valid_identifier(owner.subject_id) || !valid_identifier(owner.tenant_id)) {
            return ErrorCode::invalid_argument;
        }
        Binding binding;
        ErrorCode error = ErrorCode::ok;
        if (!open(sealed_metadata, binding, error)) {
            return error;
        }
        if (binding.tenant_id != owner.tenant_id ||
            binding.owner_subject_id != owner.subject_id) {
            return ErrorCode::principal_not_owner;
        }
        std::lock_guard lock(mutex);
        revoked_snapshots.insert(to_hex(binding.snapshot_id));
        return ErrorCode::ok;
    }

    ErrorCode revoke_snapshots_before(std::string_view tenant_id, std::string_view vm_id,
                                      std::uint64_t sequence) {
        if (!valid_identifier(tenant_id) || !valid_identifier(vm_id)) {
            return ErrorCode::invalid_argument;
        }
        std::lock_guard lock(mutex);
        const auto found = vms.find(std::make_pair(std::string(tenant_id), std::string(vm_id)));
        if (found == vms.end()) {
            return ErrorCode::vm_not_found;
        }
        found->second.minimum_mount_sequence =
            std::max(found->second.minimum_mount_sequence, sequence);
        return ErrorCode::ok;
    }
};

MasterKey MasterKey::from_secret(std::span<const Byte> secret) {
    MasterKey key;
    if (secret.empty()) {
        return key;
    }
    const auto digest = detail::sha256(secret);
    std::copy(digest.begin(), digest.end(), key.bytes.begin());
    return key;
}

MasterKey MasterKey::generate() {
    MasterKey key;
    const auto random = detail::random_bytes(key.bytes.size());
    std::copy(random.begin(), random.end(), key.bytes.begin());
    return key;
}

std::string_view to_string(ErrorCode code) noexcept {
    switch (code) {
        case ErrorCode::ok:
            return "ok";
        case ErrorCode::invalid_argument:
            return "invalid_argument";
        case ErrorCode::invalid_master_key:
            return "invalid_master_key";
        case ErrorCode::vm_conflict:
            return "vm_conflict";
        case ErrorCode::vm_not_found:
            return "vm_not_found";
        case ErrorCode::vm_deleted:
            return "vm_deleted";
        case ErrorCode::vm_epoch_mismatch:
            return "vm_epoch_mismatch";
        case ErrorCode::vm_mismatch:
            return "vm_mismatch";
        case ErrorCode::tenant_mismatch:
            return "tenant_mismatch";
        case ErrorCode::principal_not_owner:
            return "principal_not_owner";
        case ErrorCode::disk_mismatch:
            return "disk_mismatch";
        case ErrorCode::key_mismatch:
            return "key_mismatch";
        case ErrorCode::metadata_malformed:
            return "metadata_malformed";
        case ErrorCode::metadata_authentication_failed:
            return "metadata_authentication_failed";
        case ErrorCode::stale_snapshot:
            return "stale_snapshot";
        case ErrorCode::snapshot_revoked:
            return "snapshot_revoked";
        case ErrorCode::clock_error:
            return "clock_error";
        case ErrorCode::internal_error:
            return "internal_error";
    }
    return "unknown";
}

SnapshotOwnershipService::SnapshotOwnershipService(MasterKey master, Policy policy)
    : SnapshotOwnershipService(std::move(master), std::move(policy), Clock{}) {}

SnapshotOwnershipService::SnapshotOwnershipService(MasterKey master, Policy policy,
                                                   Clock clock)
    : impl_(std::make_unique<Impl>()) {
    impl_->master = master;
    impl_->policy = policy;
    impl_->clock = clock ? std::move(clock) : Clock([] { return default_clock(); });
    impl_->valid_master =
        std::any_of(master.bytes.begin(), master.bytes.end(),
                    [](Byte byte) { return byte != 0; });
}

SnapshotOwnershipService::~SnapshotOwnershipService() = default;
SnapshotOwnershipService::SnapshotOwnershipService(SnapshotOwnershipService&&) noexcept =
    default;
SnapshotOwnershipService& SnapshotOwnershipService::operator=(
    SnapshotOwnershipService&&) noexcept = default;

ErrorCode SnapshotOwnershipService::register_vm(const VmRecord& record) {
    return impl_->register_vm(record);
}

ErrorCode SnapshotOwnershipService::delete_vm(std::string_view tenant_id,
                                              std::string_view vm_id) {
    return impl_->delete_vm(tenant_id, vm_id);
}

IssueResult SnapshotOwnershipService::take_snapshot(const Principal& owner,
                                                    std::string_view vm_id,
                                                    std::string_view disk_id,
                                                    const Bytes& data_key) {
    return impl_->take_snapshot(owner, vm_id, disk_id, data_key);
}

MountResult SnapshotOwnershipService::mount(const Bytes& sealed_metadata,
                                            const MountRequest& request) const {
    return impl_->mount(sealed_metadata, request);
}

MountResult SnapshotOwnershipService::mount(const Bytes& sealed_metadata,
                                            const Principal& principal) const {
    MountRequest request;
    request.principal = principal;
    return impl_->mount(sealed_metadata, request);
}

ErrorCode SnapshotOwnershipService::revoke_snapshot(const Principal& owner,
                                                    const Bytes& sealed_metadata) {
    return impl_->revoke_snapshot(owner, sealed_metadata);
}

ErrorCode SnapshotOwnershipService::revoke_snapshots_before(std::string_view tenant_id,
                                                            std::string_view vm_id,
                                                            std::uint64_t sequence) {
    return impl_->revoke_snapshots_before(tenant_id, vm_id, sequence);
}

}  // namespace netsec
