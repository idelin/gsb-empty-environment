#include "netsec.hpp"
#include "netsec_crypto.hpp"

#include <algorithm>
#include <array>
#include <cstdint>
#include <iostream>
#include <numeric>
#include <span>
#include <string>
#include <string_view>
#include <vector>

namespace {

int g_checks = 0;
int g_failures = 0;

void check(bool condition, const char* expression, int line) {
    ++g_checks;
    if (!condition) {
        ++g_failures;
        std::cerr << "FAIL line " << line << ": " << expression << '\n';
    }
}

#define CHECK(condition) check((condition), #condition, __LINE__)

std::string hex(std::span<const std::uint8_t> bytes) {
    static constexpr char digits[] = "0123456789abcdef";
    std::string output;
    output.reserve(bytes.size() * 2);
    for (const auto byte : bytes) {
        output.push_back(digits[byte >> 4U]);
        output.push_back(digits[byte & 0x0fU]);
    }
    return output;
}

netsec::Bytes from_hex(std::string_view value) {
    netsec::Bytes output;
    output.reserve(value.size() / 2);
    for (std::size_t index = 0; index + 1 < value.size(); index += 2) {
        const auto nibble = [](char character) -> std::uint8_t {
            if (character >= '0' && character <= '9') {
                return static_cast<std::uint8_t>(character - '0');
            }
            if (character >= 'a' && character <= 'f') {
                return static_cast<std::uint8_t>(character - 'a' + 10);
            }
            return static_cast<std::uint8_t>(character - 'A' + 10);
        };
        output.push_back(static_cast<std::uint8_t>(
            (nibble(value[index]) << 4U) | nibble(value[index + 1])));
    }
    return output;
}

std::span<const std::uint8_t> span_of(std::string_view value) {
    return {reinterpret_cast<const std::uint8_t*>(value.data()), value.size()};
}

template <std::size_t Size>
std::span<const std::uint8_t> span_of(const std::array<std::uint8_t, Size>& value) {
    return {value.data(), value.size()};
}

bool contains_text(const netsec::Bytes& blob, std::string_view needle) {
    if (needle.empty() || blob.size() < needle.size()) {
        return false;
    }
    return std::search(blob.begin(), blob.end(), needle.begin(), needle.end()) != blob.end();
}

bool contains_bytes(const netsec::Bytes& blob, const netsec::Bytes& needle) {
    if (needle.empty() || blob.size() < needle.size()) {
        return false;
    }
    return std::search(blob.begin(), blob.end(), needle.begin(), needle.end()) != blob.end();
}

netsec::MasterKey test_master() {
    netsec::MasterKey key;
    for (std::size_t index = 0; index < key.bytes.size(); ++index) {
        key.bytes[index] = static_cast<std::uint8_t>(0x31U + index);
    }
    return key;
}

netsec::Bytes test_data_key(std::uint8_t seed) {
    netsec::Bytes key(32);
    for (std::size_t index = 0; index < key.size(); ++index) {
        key[index] = static_cast<std::uint8_t>(seed + index * 7U);
    }
    return key;
}

netsec::Principal principal(std::string subject, std::string tenant) {
    return netsec::Principal{std::move(subject), std::move(tenant)};
}

netsec::VmRecord vm_record(std::string tenant, std::string vm, std::string owner,
                           std::uint64_t epoch = 1) {
    netsec::VmRecord record;
    record.tenant_id = std::move(tenant);
    record.vm_id = std::move(vm);
    record.owner_subject_id = std::move(owner);
    record.lifecycle_epoch = epoch;
    return record;
}

void test_crypto_vectors() {
    CHECK(hex(netsec::detail::sha256(span_of("abc"))) ==
          "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");

    const auto hmac = netsec::detail::hmac_sha256(
        span_of("key"), span_of("The quick brown fox jumps over the lazy dog"));
    CHECK(hex(hmac) ==
          "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8");

    const auto ikm = from_hex(
        "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b");
    const auto salt = from_hex("000102030405060708090a0b0c");
    const auto info = from_hex("f0f1f2f3f4f5f6f7f8f9");
    const auto hkdf = netsec::detail::hkdf_sha256(ikm, salt, info);
    CHECK(hex(hkdf) ==
          "3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf");

    const auto poly_key = from_hex(
        "85d6be7857556d337f4452fe42d506a80103808afb0db2fd4abff6af4149f51b");
    std::array<std::uint8_t, 32> poly_key_array{};
    std::copy(poly_key.begin(), poly_key.end(), poly_key_array.begin());
    const auto poly_tag = netsec::detail::poly1305_mac(
        poly_key_array, span_of("Cryptographic Forum Research Group"));
    CHECK(hex(poly_tag) == "a8061dc1305136c6c22b8baf0c0127a9");

    std::array<std::uint8_t, 32> chacha_key{};
    std::iota(chacha_key.begin(), chacha_key.end(), std::uint8_t{0});
    const std::array<std::uint8_t, 12> chacha_nonce = {0, 0, 0, 9, 0, 0,
                                                        0, 0x4a, 0, 0, 0, 0};
    std::array<std::uint8_t, 64> zeros{};
    std::array<std::uint8_t, 64> key_stream{};
    netsec::detail::chacha20_xor(chacha_key, chacha_nonce, 1U, zeros, key_stream);
    CHECK(hex(key_stream) ==
          "10f1e7e4d13b5915500fdd1fa32071c4c7d1f4c733c068030422aa9ac3d46c4e"
          "d2826446079faa0914c2d705d98b02a2b5129cd1de164eb9cbd083e8a2503c4e");

    std::array<std::uint8_t, 32> aead_key{};
    for (std::size_t index = 0; index < aead_key.size(); ++index) {
        aead_key[index] = static_cast<std::uint8_t>(index * 3U + 1U);
    }
    const std::array<std::uint8_t, 12> aead_nonce = {1, 2, 3, 4, 5, 6,
                                                     7, 8, 9, 10, 11, 12};
    const auto plaintext = from_hex("00112233445566778899aabbccddeeff");
    const auto aad = from_hex("feedface");
    const auto sealed =
        netsec::detail::chacha20_poly1305_seal(aead_key, aead_nonce, aad, plaintext);
    CHECK(hex(sealed.ciphertext) == "6ac139c22252c61b845a524ba78a8088");
    CHECK(hex(sealed.tag) == "a65bbe7014659fe82811800e898a1627");
    netsec::Bytes opened;
    CHECK(netsec::detail::chacha20_poly1305_open(aead_key, aead_nonce, aad,
                                                 sealed.ciphertext, sealed.tag,
                                                 opened));
    CHECK(opened == plaintext);

    auto tampered = sealed.ciphertext;
    tampered[0] ^= 0x80U;
    netsec::Bytes rejected;
    CHECK(!netsec::detail::chacha20_poly1305_open(aead_key, aead_nonce, aad, tampered,
                                                  sealed.tag, rejected));
    CHECK(rejected.empty());
}

void test_legitimate_mount() {
    netsec::SnapshotOwnershipService service(test_master());
    const auto alice = principal("subject-alice", "tenant-a");
    CHECK(service.register_vm(vm_record("tenant-a", "vm-1", "subject-alice")) ==
          netsec::ErrorCode::ok);

    const auto key = test_data_key(0x10);
    const auto issued = service.take_snapshot(alice, "vm-1", "disk-1", key);
    CHECK(issued.ok());
    CHECK(issued.sequence == 1);
    CHECK(!issued.snapshot_ref.empty());

    netsec::MountRequest request;
    request.principal = alice;
    request.vm_id = "vm-1";
    request.disk_id = "disk-1";
    const auto mounted = service.mount(issued.sealed_metadata, request);
    CHECK(mounted.allowed());
    CHECK(mounted.data_key == key);
    CHECK(mounted.snapshot_ref == issued.snapshot_ref);
    CHECK(mounted.sequence == issued.sequence);

    request.presented_key = key;
    CHECK(service.mount(issued.sealed_metadata, request).allowed());

    // Convenience overload still enforces the same principal and tenant checks.
    CHECK(service.mount(issued.sealed_metadata, alice).allowed());
}

void test_cross_tenant_and_enumeration() {
    netsec::SnapshotOwnershipService service(test_master());
    const auto alice = principal("subject-alice-unique", "tenant-alpha-unique");
    const auto bob = principal("subject-bob-unique", "tenant-beta-unique");

    CHECK(service.register_vm(vm_record("tenant-alpha-unique", "vm-secret-unique",
                                        "subject-alice-unique")) == netsec::ErrorCode::ok);
    CHECK(service.register_vm(vm_record("tenant-beta-unique", "vm-secret-unique",
                                        "subject-bob-unique")) == netsec::ErrorCode::ok);

    const auto key = test_data_key(0x23);
    const auto issued = service.take_snapshot(alice, "vm-secret-unique",
                                              "disk-secret-unique", key);
    CHECK(issued.ok());

    netsec::MountRequest cross_tenant;
    cross_tenant.principal = bob;
    cross_tenant.vm_id = "vm-secret-unique";
    cross_tenant.disk_id = "disk-secret-unique";
    const auto denied = service.mount(issued.sealed_metadata, cross_tenant);
    CHECK(denied.code == netsec::ErrorCode::tenant_mismatch);
    CHECK(!denied.allowed());
    CHECK(denied.data_key.empty());
    CHECK(denied.snapshot_ref.empty());
    CHECK(denied.sequence == 0);

    // Even if an attacker claims the owning tenant, the authenticated subject
    // must still match the VM owner.
    auto forged = cross_tenant;
    forged.principal.tenant_id = "tenant-alpha-unique";
    forged.principal.subject_id = "subject-mallory-unique";
    const auto not_owner = service.mount(issued.sealed_metadata, forged);
    CHECK(not_owner.code == netsec::ErrorCode::principal_not_owner);
    CHECK(not_owner.data_key.empty());

    // The stored blob must not disclose any enumerable identifier or key.
    const auto& blob = issued.sealed_metadata;
    CHECK(!contains_text(blob, "tenant-alpha-unique"));
    CHECK(!contains_text(blob, "tenant-beta-unique"));
    CHECK(!contains_text(blob, "vm-secret-unique"));
    CHECK(!contains_text(blob, "disk-secret-unique"));
    CHECK(!contains_text(blob, "subject-alice-unique"));
    CHECK(!contains_bytes(blob, key));

    const auto second = service.take_snapshot(alice, "vm-secret-unique",
                                              "disk-secret-unique", key);
    CHECK(second.ok());
    CHECK(second.sealed_metadata != issued.sealed_metadata);
    CHECK(second.snapshot_ref != issued.snapshot_ref);
    CHECK(!contains_text(second.sealed_metadata, "vm-secret-unique"));

    // Only a random magic/version prefix is visible in the envelope.
    CHECK(blob.size() > 52);
    CHECK(blob[0] == 'N' && blob[1] == 'S' && blob[2] == 'M' && blob[3] == '1');
}

void test_old_snapshot_and_age() {
    netsec::SnapshotOwnershipService service(test_master());
    const auto alice = principal("subject-alice", "tenant-a");
    CHECK(service.register_vm(vm_record("tenant-a", "vm-old", "subject-alice")) ==
          netsec::ErrorCode::ok);

    const auto key = test_data_key(0x44);
    const auto first = service.take_snapshot(alice, "vm-old", "disk-1", key);
    const auto second = service.take_snapshot(alice, "vm-old", "disk-1", key);
    CHECK(first.ok() && second.ok());
    CHECK(first.sequence == 1 && second.sequence == 2);

    CHECK(service.mount(first.sealed_metadata, alice).code ==
          netsec::ErrorCode::stale_snapshot);
    CHECK(service.mount(second.sealed_metadata, alice).allowed());

    CHECK(service.revoke_snapshot(alice, second.sealed_metadata) ==
          netsec::ErrorCode::ok);
    CHECK(service.mount(second.sealed_metadata, alice).code ==
          netsec::ErrorCode::snapshot_revoked);

    std::uint64_t now = 1'000U;
    netsec::Policy age_policy;
    age_policy.max_snapshot_age_seconds = 60;
    netsec::SnapshotOwnershipService aged(test_master(), age_policy,
                                          [&now] { return now; });
    CHECK(aged.register_vm(vm_record("tenant-a", "vm-aged", "subject-alice")) ==
          netsec::ErrorCode::ok);
    const auto snapshot = aged.take_snapshot(alice, "vm-aged", "disk-1", key);
    CHECK(snapshot.ok());
    CHECK(aged.mount(snapshot.sealed_metadata, alice).allowed());
    now += 61;
    CHECK(aged.mount(snapshot.sealed_metadata, alice).code ==
          netsec::ErrorCode::stale_snapshot);
}

void test_anti_rollback_floor() {
    netsec::Policy policy;
    policy.latest_only = false;
    netsec::SnapshotOwnershipService service(test_master(), policy);
    const auto alice = principal("subject-alice", "tenant-a");
    CHECK(service.register_vm(vm_record("tenant-a", "vm-floor", "subject-alice")) ==
          netsec::ErrorCode::ok);

    const auto key = test_data_key(0x55);
    const auto first = service.take_snapshot(alice, "vm-floor", "disk-1", key);
    const auto second = service.take_snapshot(alice, "vm-floor", "disk-1", key);
    CHECK(first.ok() && second.ok());
    CHECK(service.mount(first.sealed_metadata, alice).allowed());
    CHECK(service.mount(second.sealed_metadata, alice).allowed());

    CHECK(service.revoke_snapshots_before("tenant-a", "vm-floor", 2) ==
          netsec::ErrorCode::ok);
    CHECK(service.mount(first.sealed_metadata, alice).code ==
          netsec::ErrorCode::stale_snapshot);
    CHECK(service.mount(second.sealed_metadata, alice).allowed());
}

void test_deleted_vm() {
    netsec::SnapshotOwnershipService service(test_master());
    const auto alice = principal("subject-alice", "tenant-a");
    CHECK(service.register_vm(vm_record("tenant-a", "vm-deleted", "subject-alice")) ==
          netsec::ErrorCode::ok);

    const auto key = test_data_key(0x66);
    const auto snapshot = service.take_snapshot(alice, "vm-deleted", "disk-1", key);
    CHECK(snapshot.ok());
    CHECK(service.mount(snapshot.sealed_metadata, alice).allowed());

    CHECK(service.delete_vm("tenant-a", "vm-deleted") == netsec::ErrorCode::ok);
    CHECK(service.mount(snapshot.sealed_metadata, alice).code ==
          netsec::ErrorCode::vm_deleted);
    CHECK(service.take_snapshot(alice, "vm-deleted", "disk-1", key).code ==
          netsec::ErrorCode::vm_deleted);

    // A tombstone cannot be revived at the same lifecycle epoch.
    CHECK(service.register_vm(vm_record("tenant-a", "vm-deleted", "subject-alice")) ==
          netsec::ErrorCode::vm_conflict);

    // Recreating with a new epoch makes the old snapshot ineligible forever,
    // while a new snapshot of the new VM is legitimate.
    CHECK(service.register_vm(
              vm_record("tenant-a", "vm-deleted", "subject-alice", 2)) ==
          netsec::ErrorCode::ok);
    CHECK(service.mount(snapshot.sealed_metadata, alice).code ==
          netsec::ErrorCode::vm_epoch_mismatch);
    const auto replacement =
        service.take_snapshot(alice, "vm-deleted", "disk-1", key);
    CHECK(replacement.ok());
    CHECK(service.mount(replacement.sealed_metadata, alice).allowed());
}

void test_tampering_and_mismatch() {
    netsec::SnapshotOwnershipService service(test_master());
    const auto alice = principal("subject-alice", "tenant-a");
    CHECK(service.register_vm(vm_record("tenant-a", "vm-tamper", "subject-alice")) ==
          netsec::ErrorCode::ok);

    const auto key = test_data_key(0x77);
    const auto snapshot = service.take_snapshot(alice, "vm-tamper", "disk-1", key);
    CHECK(snapshot.ok());

    auto tampered = snapshot.sealed_metadata;
    tampered[tampered.size() / 2] ^= 0x01U;
    CHECK(service.mount(tampered, alice).code ==
          netsec::ErrorCode::metadata_authentication_failed);

    auto truncated = snapshot.sealed_metadata;
    truncated.resize(40);
    CHECK(service.mount(truncated, alice).code ==
          netsec::ErrorCode::metadata_malformed);

    const netsec::Bytes garbage(80, 0x5a);
    CHECK(service.mount(garbage, alice).code ==
          netsec::ErrorCode::metadata_malformed);

    netsec::SnapshotOwnershipService other_root(test_master());
    // Even with the same VM registration, a different root key cannot open it.
    other_root = netsec::SnapshotOwnershipService(netsec::MasterKey::generate());
    CHECK(other_root.mount(snapshot.sealed_metadata, alice).code ==
          netsec::ErrorCode::metadata_authentication_failed);

    netsec::MountRequest wrong_key;
    wrong_key.principal = alice;
    wrong_key.presented_key = test_data_key(0x99);
    CHECK(service.mount(snapshot.sealed_metadata, wrong_key).code ==
          netsec::ErrorCode::key_mismatch);

    netsec::MountRequest wrong_disk;
    wrong_disk.principal = alice;
    wrong_disk.disk_id = "disk-2";
    CHECK(service.mount(snapshot.sealed_metadata, wrong_disk).code ==
          netsec::ErrorCode::disk_mismatch);

    netsec::MountRequest wrong_vm;
    wrong_vm.principal = alice;
    wrong_vm.vm_id = "vm-other";
    CHECK(service.mount(snapshot.sealed_metadata, wrong_vm).code ==
          netsec::ErrorCode::vm_mismatch);
}

void test_disk_registration() {
    netsec::SnapshotOwnershipService service(test_master());
    const auto alice = principal("subject-alice", "tenant-a");
    auto record = vm_record("tenant-a", "vm-disk", "subject-alice");
    record.disk_ids = {"disk-1", "disk-2"};
    CHECK(service.register_vm(record) == netsec::ErrorCode::ok);

    const auto key = test_data_key(0x88);
    CHECK(service.take_snapshot(alice, "vm-disk", "disk-3", key).code ==
          netsec::ErrorCode::disk_mismatch);
    const auto snapshot = service.take_snapshot(alice, "vm-disk", "disk-1", key);
    CHECK(snapshot.ok());
    CHECK(service.mount(snapshot.sealed_metadata, alice).allowed());
}

}  // namespace

int main() {
    test_crypto_vectors();
    test_legitimate_mount();
    test_cross_tenant_and_enumeration();
    test_old_snapshot_and_age();
    test_anti_rollback_floor();
    test_deleted_vm();
    test_tampering_and_mismatch();
    test_disk_registration();

    if (g_failures == 0) {
        std::cout << "All " << g_checks << " checks passed\n";
        return 0;
    }
    std::cerr << g_failures << " of " << g_checks << " checks failed\n";
    return 1;
}
