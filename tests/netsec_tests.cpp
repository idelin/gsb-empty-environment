#include "netsec.hpp"

#include <algorithm>
#include <cstddef>
#include <iostream>
#include <string>
#include <string_view>
#include <vector>

namespace {

int failures = 0;

void expect(bool condition, std::string_view message) {
    if (!condition) {
        ++failures;
        std::cerr << "FAIL: " << message << '\n';
    }
}

bool same_bytes(const netsec::Bytes& left, const netsec::Bytes& right) {
    return left == right;
}

bool contains(const netsec::Bytes& haystack, const netsec::Bytes& needle) {
    if (needle.empty()) {
        return true;
    }
    return std::search(haystack.begin(), haystack.end(),
                       needle.begin(), needle.end()) != haystack.end();
}

struct JoinedDevice {
    std::string id;
    std::string secret;
    netsec::Bytes key;
    std::uint64_t generation = 0;
};

JoinedDevice join_device(netsec::TrustCenter& center,
                         const std::string& device_id) {
    const auto registered = center.register_device(device_id);
    expect(registered == netsec::RegistrationStatus::Registered,
           "device registration should succeed");

    const auto issued = center.issue_install_code(device_id);
    expect(issued.ok(), "install code should be issued");

    const auto result = center.join(device_id, issued.code);
    expect(result.accepted(), "legitimate device should join");
    expect(result.network_key.has_value(), "legitimate join returns a network key");
    expect(result.generation == center.current_generation(),
           "join reports the active generation");
    expect(!result.device_secret.empty(),
           "legitimate join returns a per-device credential");
    return {device_id, result.device_secret, *result.network_key,
            result.generation};
}

void expect_no_secret(const netsec::JoinResult& result,
                      std::string_view message) {
    expect(!result.network_key.has_value(), message);
    expect(result.device_secret.empty(), "rejected joins expose no credential");
    expect(!result.accepted(), "rejected join must not be accepted");
}

void test_unknown_devices_and_malformed_codes() {
    netsec::TrustCenter center("deterministic-test-root-material");

    const auto unknown = center.join("ghost", "nic1.00.00");
    expect(unknown.status == netsec::JoinStatus::UnknownDevice,
           "unknown device is rejected before code validation");
    expect_no_secret(unknown, "unknown device receives no network key");

    expect(center.issue_install_code("ghost").status ==
               netsec::IssueStatus::UnknownDevice,
           "unknown device cannot be issued an install code");

    expect(center.register_device("") ==
               netsec::RegistrationStatus::EmptyDeviceId,
           "empty identity is rejected");

    (void)center.register_device("known");
    const auto malformed = center.join("known", "not-an-install-code");
    expect(malformed.status == netsec::JoinStatus::InvalidInstallCode,
           "malformed install code is rejected");
    expect_no_secret(malformed, "malformed code receives no network key");
}

void test_one_time_identity_bound_install_codes() {
    netsec::TrustCenter center("identity-binding-test-root");
    (void)center.register_device("alpha");
    (void)center.register_device("beta");

    const auto alpha_code = center.issue_install_code("alpha");
    const auto beta_code = center.issue_install_code("beta");
    expect(alpha_code.ok() && beta_code.ok(), "codes are issued");

    const auto stolen = center.join("beta", alpha_code.code);
    expect(stolen.status == netsec::JoinStatus::InstallCodeDeviceMismatch,
           "a code cannot be replayed under another identity");
    expect_no_secret(stolen, "identity mismatch leaks no key");
    expect(!center.is_joined("alpha"),
           "a mismatched attempt must not consume the rightful code");

    const auto accepted = center.join("alpha", alpha_code.code);
    expect(accepted.accepted(), "original device can still use its code");
    expect_no_secret(center.join("alpha", alpha_code.code),
                     "a consumed code cannot be replayed");
    expect(center.join("alpha", alpha_code.code).status ==
               netsec::JoinStatus::DeviceAlreadyJoined,
           "duplicate join is rejected");

    expect(center.issue_install_code("alpha").status ==
               netsec::IssueStatus::DeviceAlreadyJoined,
           "joined devices cannot receive additional codes");

    const auto beta_accepted = center.join("beta", beta_code.code);
    expect(beta_accepted.accepted(), "independent device joins normally");
    expect(same_bytes(*accepted.network_key, *beta_accepted.network_key),
           "devices in the same generation receive the same network key");
    expect(accepted.device_secret != beta_accepted.device_secret,
           "per-device credentials are distinct");
}

void test_replacement_and_clone_rejection() {
    netsec::TrustCenter center("clone-rejection-test-root");
    (void)center.register_device("device");

    const auto first_code = center.issue_install_code("device");
    const auto replacement_code = center.issue_install_code("device");
    expect(first_code.ok() && replacement_code.ok(),
           "replacement code is issued");

    const auto superseded = center.join("device", first_code.code);
    expect(superseded.status == netsec::JoinStatus::InstallCodeAlreadyUsed,
           "replaced code is invalidated");
    expect_no_secret(superseded, "superseded code leaks no key");

    const auto genuine = center.join("device", replacement_code.code);
    expect(genuine.accepted(), "replacement code joins");
    const std::string secret = genuine.device_secret;
    const netsec::Bytes key = *genuine.network_key;

    const auto clone_same_identity =
        center.join("device", replacement_code.code);
    expect(clone_same_identity.status ==
               netsec::JoinStatus::DeviceAlreadyJoined,
           "a cloned identity cannot join twice");
    expect_no_secret(clone_same_identity, "cloned join leaks no key");

    (void)center.register_device("clone-of-device");
    const auto clone_other_identity =
        center.join("clone-of-device", replacement_code.code);
    expect(clone_other_identity.status ==
               netsec::JoinStatus::InstallCodeDeviceMismatch,
           "a cloned code is useless under another identity");
    expect_no_secret(clone_other_identity, "cloned code leaks no key");

    expect(!secret.empty() && key.size() == netsec::kNetworkKeySize,
           "genuine join produced usable credentials");
}

void test_rotation_confirmation_and_per_device_wrapping() {
    netsec::TrustCenter center("rotation-test-root-material");
    const JoinedDevice alpha = join_device(center, "alpha");
    const JoinedDevice beta = join_device(center, "beta");
    expect(alpha.key == beta.key, "generation zero key is shared");

    (void)center.register_device("late-device");
    const auto stale_code = center.issue_install_code("late-device");
    expect(stale_code.ok(), "pre-rotation code is issued");

    const auto started = center.begin_key_rotation();
    expect(started.status == netsec::RotationStatus::AwaitingConfirmation,
           "rotation waits for confirmations");
    expect(started.generation == 1, "rotation advances exactly one generation");
    expect(started.deliveries.size() == 2,
           "one delivery is produced per joined device");
    expect(center.pending_generation() == std::uint64_t{1},
           "pending generation is visible to the coordinator");
    expect(center.pending_confirmation_count() == 2,
           "both devices must confirm");

    const netsec::KeyDelivery* alpha_delivery = nullptr;
    const netsec::KeyDelivery* beta_delivery = nullptr;
    for (const auto& delivery : started.deliveries) {
        if (delivery.device_id == "alpha") {
            alpha_delivery = &delivery;
        } else if (delivery.device_id == "beta") {
            beta_delivery = &delivery;
        }
    }
    expect(alpha_delivery != nullptr && beta_delivery != nullptr,
           "deliveries are addressed per device");
    if (alpha_delivery == nullptr || beta_delivery == nullptr) {
        return;
    }

    const auto alpha_new_key =
        netsec::open_key_update(*alpha_delivery, alpha.secret);
    const auto beta_new_key =
        netsec::open_key_update(*beta_delivery, beta.secret);
    expect(alpha_new_key.has_value() && beta_new_key.has_value(),
           "each device opens its own delivery");
    if (!alpha_new_key || !beta_new_key) {
        return;
    }
    expect(same_bytes(*alpha_new_key, *beta_new_key),
           "all devices recover the same new network key");
    expect(*alpha_new_key != alpha.key,
           "rotation produces a different network key");
    expect((*alpha_new_key).size() == netsec::kNetworkKeySize,
           "new network key has the expected size");
    expect(alpha_delivery->ciphertext != beta_delivery->ciphertext,
           "ciphertext is unique per device");
    expect(alpha_delivery->tag != beta_delivery->tag,
           "authentication tag is unique per device");
    expect(!contains(alpha_delivery->ciphertext, *alpha_new_key),
           "plaintext network key is absent from the delivery");
    expect(!netsec::open_key_update(*alpha_delivery, beta.secret).has_value(),
           "another device credential cannot open the delivery");

    auto tampered_tag = *alpha_delivery;
    tampered_tag.tag[0] = static_cast<std::uint8_t>(tampered_tag.tag[0] ^ 1U);
    expect(!netsec::open_key_update(tampered_tag, alpha.secret).has_value(),
           "modified authentication tag is rejected");

    auto tampered_generation = *alpha_delivery;
    tampered_generation.generation = 2;
    expect(!netsec::open_key_update(tampered_generation, alpha.secret).has_value(),
           "modified generation is rejected by the authenticated envelope");

    auto tampered_identity = *alpha_delivery;
    tampered_identity.device_id = "beta";
    expect(!netsec::open_key_update(tampered_identity, beta.secret).has_value(),
           "delivery cannot be replayed under another device identity");

    const auto second_start = center.begin_key_rotation();
    expect(second_start.status == netsec::RotationStatus::AlreadyInProgress,
           "concurrent rotation is rejected");
    expect(second_start.deliveries.empty(),
           "rejected rotation produces no new deliveries");

    const auto future_confirmation =
        center.confirm_key_update("alpha", 2, alpha.secret);
    expect(future_confirmation.status ==
               netsec::ConfirmationStatus::GenerationMismatch,
           "confirmation for a non-pending generation is rejected");
    expect(center.pending_confirmation_count() == 2,
           "generation mismatch does not advance the rotation");

    const auto wrong_secret = center.confirm_key_update(
        "alpha", 1, "0000000000000000000000000000000000000000000000000000000000000000");
    expect(wrong_secret.status ==
               netsec::ConfirmationStatus::AuthenticationFailed,
           "unauthenticated confirmation is rejected");
    expect(center.pending_confirmation_count() == 2,
           "failed authentication does not advance the rotation");

    const auto rollback_confirmation =
        center.confirm_key_update("alpha", 0, alpha.secret);
    expect(rollback_confirmation.status ==
               netsec::ConfirmationStatus::GenerationRollbackBlocked,
           "old-generation confirmation is rejected");
    expect(center.current_generation() == 0,
           "rollback attempt does not change the committed generation");
    expect(!center.rollback_generation(0),
           "explicit rollback request is denied");

    const auto first_confirmation =
        center.confirm_key_update("alpha", 1, alpha.secret);
    expect(first_confirmation.status ==
               netsec::ConfirmationStatus::AwaitingConfirmation,
           "rotation remains pending after one confirmation");
    expect(center.pending_confirmation_count() == 1,
           "confirmed device is removed from the pending set");

    const auto committed = center.confirm_key_update("beta", 1, beta.secret);
    expect(committed.status == netsec::ConfirmationStatus::Committed,
           "last confirmation commits the rotation");
    expect(center.current_generation() == 1, "generation is committed");
    expect(center.pending_generation() == std::nullopt,
           "no rotation remains pending");

    const auto repeated =
        center.confirm_key_update("alpha", 1, alpha.secret);
    expect(repeated.status != netsec::ConfirmationStatus::Committed,
           "a repeated confirmation cannot recommit or roll back");
    expect(!center.rollback_generation(0),
           "committed generation cannot be rolled back");

    const auto stale = center.join("late-device", stale_code.code);
    expect(stale.status == netsec::JoinStatus::StaleInstallCode,
           "unused pre-rotation install code is stale");
    expect_no_secret(stale, "stale install code leaks no current key");

    const JoinedDevice fresh = join_device(center, "fresh-device");
    expect(fresh.generation == 1,
           "post-rotation join reports the committed generation");
    expect(fresh.key == *alpha_new_key,
           "a device joining after rotation gets the current key");
    expect(center.current_generation() == 1,
           "post-rotation join uses the committed generation");

    const netsec::Bytes old_key = alpha.key;
    expect(!contains(alpha_delivery->ciphertext, old_key),
           "old key is not present in the rotated delivery");
}

void test_rotation_blocks_joins_and_abort_preserves_state() {
    netsec::TrustCenter center("rotation-locking-test-root");
    const JoinedDevice existing = join_device(center, "existing");
    (void)center.register_device("pending-device");
    const auto pending_code = center.issue_install_code("pending-device");
    expect(pending_code.ok(), "pending device receives a code");

    const auto started = center.begin_key_rotation();
    expect(started.status == netsec::RotationStatus::AwaitingConfirmation,
           "rotation starts");
    expect(center.issue_install_code("pending-device").status ==
               netsec::IssueStatus::RotationInProgress,
           "new install codes are blocked while a rotation is pending");
    const auto blocked = center.join("pending-device", pending_code.code);
    expect(blocked.status == netsec::JoinStatus::RotationInProgress,
           "join is blocked while the key set is unsettled");
    expect_no_secret(blocked, "blocked join leaks no key");

    expect(center.abort_key_rotation(), "pending rotation can be aborted");
    expect(center.current_generation() == 0,
           "aborting does not change the committed generation");
    expect(center.pending_generation() == std::nullopt,
           "aborting clears the pending rotation");

    const auto after_abort = center.join("pending-device", pending_code.code);
    expect(after_abort.accepted(),
           "an aborted rotation leaves the current-generation code usable");
    expect(same_bytes(*after_abort.network_key, existing.key),
           "join after abort receives the still-current key");
}

void test_revocation() {
    netsec::TrustCenter center("revocation-test-root-material");
    const JoinedDevice device = join_device(center, "device");
    const JoinedDevice keeper = join_device(center, "keeper");
    const std::string secret = device.secret;

    expect(center.revoke_device("device"), "joined device is revoked");
    expect(!center.revoke_device("device"), "double revocation is reported");
    expect(!center.is_joined("device"), "revoked device is no longer joined");
    expect(center.is_revoked("device"), "revocation is visible");
    expect(center.issue_install_code("device").status ==
               netsec::IssueStatus::DeviceRevoked,
           "revoked device cannot get a new code");

    const auto rotation = center.begin_key_rotation();
    expect(rotation.status == netsec::RotationStatus::AwaitingConfirmation,
           "remaining device controls confirmation");
    expect(rotation.deliveries.size() == 1,
           "revoked device receives no key delivery");
    expect(center.confirm_key_update("device", 1, secret).status ==
               netsec::ConfirmationStatus::DeviceRevoked,
           "revoked device cannot confirm updates");
    expect(center.confirm_key_update("keeper", 1, keeper.secret).committed(),
           "remaining device can complete the rotation");
}

void test_empty_network_rotation() {
    netsec::TrustCenter center("empty-network-test-root");
    const auto rotation = center.begin_key_rotation();
    expect(rotation.status == netsec::RotationStatus::Committed,
           "rotation commits immediately with no active devices");
    expect(rotation.deliveries.empty(), "no deliveries for an empty network");
    expect(center.current_generation() == 1, "empty-network generation advances");
}

void test_generation_zero_rollback_and_no_pending_rotation() {
    netsec::TrustCenter center("no-pending-rotation-test-root");
    const auto rotation = center.begin_key_rotation();
    expect(rotation.status == netsec::RotationStatus::Committed,
           "first empty-network rotation commits");

    const auto no_pending =
        center.confirm_key_update("nobody", 1, std::string(64, '0'));
    expect(no_pending.status == netsec::ConfirmationStatus::NoPendingRotation,
           "confirmation without a pending rotation is rejected");
    expect(center.current_generation() == 1,
           "rejected confirmation changes nothing");
}

}  // namespace

int main() {
    test_unknown_devices_and_malformed_codes();
    test_one_time_identity_bound_install_codes();
    test_replacement_and_clone_rejection();
    test_rotation_confirmation_and_per_device_wrapping();
    test_rotation_blocks_joins_and_abort_preserves_state();
    test_revocation();
    test_empty_network_rotation();
    test_generation_zero_rollback_and_no_pending_rotation();

    if (failures != 0) {
        std::cerr << failures << " test assertion(s) failed\n";
        return 1;
    }
    std::cout << "all netsec tests passed\n";
    return 0;
}
