#include "netsec.hpp"

#include <cassert>
#include <cstdlib>
#include <functional>
#include <iostream>
#include <string>
#include <string_view>

namespace {

using namespace netsec;

constexpr std::string_view kServerIdentity = "profile-server.example";

void check(bool condition, std::string_view message) {
    if (!condition) {
        std::cerr << "check failed: " << message << '\n';
        std::abort();
    }
}

void expect_error(const std::function<void()>& action, ErrorCode expected,
                  std::string_view message) {
    try {
        action();
    } catch (const TransactionError& error) {
        check(error.code() == expected, message);
        return;
    }
    check(false, "expected a TransactionError");
}

struct Fixture {
    PrivateKey root_key;
    PrivateKey intermediate_key;
    PrivateKey server_key;
    Certificate root;
    Certificate intermediate;
    Certificate server;
    std::vector<Certificate> server_chain;

    Fixture()
        : root(make_root_certificate("device-root", root_key, 1, 100, 10'000)),
          intermediate(issue_certificate("device-intermediate",
                                         intermediate_key.public_key(), root,
                                         root_key, 2, 100, 10'000, true)),
          server(issue_certificate(std::string(kServerIdentity),
                                   server_key.public_key(),
                                   intermediate, intermediate_key, 3, 100,
                                   10'000, false)),
          server_chain{server, intermediate} {}

    DownloadRequest make_request(std::string transaction_id, std::string eid,
                                 std::string nonce, std::string profile_id,
                                 std::uint64_t version, Bytes payload,
                                 std::int64_t validation_time = 500) const {
        DownloadRequest request;
        request.transaction_id = std::move(transaction_id);
        request.eid = std::move(eid);
        request.server_identity = std::string(kServerIdentity);
        request.nonce = std::move(nonce);
        request.profile.id = std::move(profile_id);
        request.profile.version = version;
        request.profile.payload = std::move(payload);
        request.certificate_chain = server_chain;
        request.validation_time = validation_time;
        request.signature = sign_download_request(server_key, request);
        return request;
    }

    DownloadTransactionManager make_device(std::string eid) const {
        DownloadTransactionManager manager(std::move(eid));
        manager.add_trust_anchor(root);
        return manager;
    }
};

InstallReceipt install_profile(DownloadTransactionManager& manager,
                               DownloadRequest request) {
    DownloadReceipt receipt = manager.download(request);
    manager.confirm_download(receipt);
    return manager.install(receipt);
}

void test_crypto_vectors() {
    const PrivateKey key = PrivateKey::from_hex(
        "9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60");
    check(to_hex(key.public_key()) ==
              "d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a",
          "RFC 8032 public key");
    check(to_hex(key.sign(std::string_view{})) ==
              "e5564300c360ac729086e2cc806e828a84877f1eb8e5d974d873e06522490155"
              "5fb8821590a33bacc61e39701cf9b46bd25bf5f0595bbe24655141438e7a100b",
          "RFC 8032 signature");
    check(to_hex(sha256(std::string_view{"abc"})) ==
              "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
          "SHA-256 vector");
}

void test_legal_download_and_staged_install() {
    Fixture fixture;
    DownloadTransactionManager target = fixture.make_device("eid-target");
    const std::string nonce = target.issue_nonce();
    const Bytes profile = {1, 2, 3, 4, 5};
    DownloadRequest request = fixture.make_request(
        "txn-legal", "eid-target", nonce, "profile-A", 2, profile);

    DownloadReceipt downloaded = target.download(request);
    check(downloaded.eid == "eid-target", "download receipt is bound to EID");
    check(downloaded.profile_version == 2, "download receipt version");
    check(downloaded.profile_data == profile, "download payload");
    check(target.stage(downloaded.session_id) == DownloadStage::Downloaded,
          "download stage recorded");
    check(target.installed_version("profile-A") == 0,
          "download must not install");

    expect_error(
        [&] { static_cast<void>(target.install(downloaded.session_id)); },
        ErrorCode::StageOutOfOrder,
        "installation before download confirmation must fail");

    downloaded.profile_data = {9, 9, 9};
    target.confirm_download(downloaded);
    check(target.stage(downloaded.session_id) ==
              DownloadStage::DownloadConfirmed,
          "download confirmation stage");

    const InstallReceipt installed = target.install(downloaded);
    check(installed.eid == "eid-target", "install receipt EID");
    check(installed.profile_version == 2, "install receipt version");
    check(target.installed_version("profile-A") == 2, "version committed");
    check(target.installed_profile("profile-A") ==
              std::optional<Bytes>(profile),
          "committed profile bytes");

    expect_error(
        [&] { static_cast<void>(target.install(downloaded.session_id)); },
        ErrorCode::TransactionAlreadyInstalled,
        "installation is one-time");
    expect_error(
        [&] { target.confirm_download(downloaded.session_id); },
        ErrorCode::TransactionAlreadyInstalled,
        "download confirmation is one-time");
}

void test_cross_device_and_eid_replacement_are_rejected() {
    Fixture fixture;
    DownloadTransactionManager target = fixture.make_device("eid-target");
    const std::string nonce = target.issue_nonce();
    DownloadRequest request = fixture.make_request(
        "txn-device", "eid-target", nonce, "profile-A", 1, {7, 8, 9});
    const DownloadRequest original_request = request;

    DownloadTransactionManager attacker = fixture.make_device("eid-attacker");
    expect_error(
        [&] { static_cast<void>(attacker.download(request)); },
        ErrorCode::EidMismatch,
        "transaction cannot cross devices");

    request.eid = "eid-attacker";
    expect_error(
        [&] { static_cast<void>(attacker.download(request)); },
        ErrorCode::InvalidSignature,
        "replacing EID breaks the server signature");

    DownloadReceipt receipt = target.download(original_request);
    expect_error(
        [&] { attacker.confirm_download(receipt); }, ErrorCode::EidMismatch,
        "receipt cannot cross devices");
    expect_error(
        [&] { static_cast<void>(attacker.install(receipt)); },
        ErrorCode::EidMismatch,
        "install receipt cannot cross devices");
    target.confirm_download(receipt);
    static_cast<void>(target.install(receipt));
    check(target.installed_profile("profile-A") ==
              std::optional<Bytes>(Bytes{7, 8, 9}),
          "profile installed only on target");
    check(!attacker.installed_profile("profile-A").has_value(),
          "attacker device holds no profile");
}

void test_replay_and_nonce_reuse_are_rejected() {
    Fixture fixture;
    DownloadTransactionManager target = fixture.make_device("eid-target");
    const std::string nonce = target.issue_nonce();
    DownloadRequest request = fixture.make_request(
        "txn-replay", "eid-target", nonce, "profile-A", 1, {1});
    static_cast<void>(target.download(request));

    expect_error(
        [&] { static_cast<void>(target.download(request)); },
        ErrorCode::TransactionAlreadyUsed,
        "identical transaction replay");

    DownloadRequest same_nonce = fixture.make_request(
        "txn-new", "eid-target", nonce, "profile-B", 1, {2});
    expect_error(
        [&] { static_cast<void>(target.download(same_nonce)); },
        ErrorCode::NonceReplayed,
        "nonce reuse");

    DownloadRequest unissued = fixture.make_request(
        "txn-unissued", "eid-target", "not-issued", "profile-B", 1, {3});
    expect_error(
        [&] { static_cast<void>(target.download(unissued)); },
        ErrorCode::InvalidNonce,
        "unissued nonce");
}

void test_version_rollback_is_rejected_at_both_stages() {
    Fixture fixture;
    DownloadTransactionManager target = fixture.make_device("eid-target");
    const std::string nonce_v3 = target.issue_nonce();
    DownloadRequest request_v3 = fixture.make_request(
        "txn-v3", "eid-target", nonce_v3, "profile-A", 3, {3});
    static_cast<void>(target.download(request_v3));

    const std::string nonce_v4 = target.issue_nonce();
    DownloadRequest request_v4 = fixture.make_request(
        "txn-v4", "eid-target", nonce_v4, "profile-A", 4, {4});
    DownloadReceipt receipt_v4 = target.download(request_v4);
    target.confirm_download(receipt_v4);
    static_cast<void>(target.install(receipt_v4));

    expect_error(
        [&] {
            static_cast<void>(target.install(request_v3.transaction_id));
        },
        ErrorCode::UnknownSession,
        "transaction id is not a session handle");

    const std::string nonce_old = target.issue_nonce();
    DownloadRequest request_old = fixture.make_request(
        "txn-old", "eid-target", nonce_old, "profile-A", 3, {5});
    expect_error(
        [&] { static_cast<void>(target.download(request_old)); },
        ErrorCode::VersionRollback,
        "older version cannot be downloaded");

    const std::string nonce_same = target.issue_nonce();
    DownloadRequest request_same = fixture.make_request(
        "txn-same", "eid-target", nonce_same, "profile-A", 4, {6});
    expect_error(
        [&] { static_cast<void>(target.download(request_same)); },
        ErrorCode::VersionRollback,
        "same version is not monotonic");

    const std::string nonce_v5 = target.issue_nonce();
    DownloadRequest request_v5 = fixture.make_request(
        "txn-v5", "eid-target", nonce_v5, "profile-A", 5, {5, 5});
    const InstallReceipt receipt_v5 =
        install_profile(target, std::move(request_v5));
    check(receipt_v5.profile_version == 5, "newer version installs");
}

void test_pending_older_transaction_fails_at_install() {
    Fixture fixture;
    DownloadTransactionManager target = fixture.make_device("eid-target");

    DownloadRequest newer = fixture.make_request(
        "txn-newer", "eid-target", target.issue_nonce(), "profile-A", 4, {4});
    DownloadReceipt newer_receipt = target.download(newer);
    target.confirm_download(newer_receipt);

    DownloadRequest older = fixture.make_request(
        "txn-older", "eid-target", target.issue_nonce(), "profile-A", 3, {3});
    DownloadReceipt older_receipt = target.download(older);
    target.confirm_download(older_receipt);

    static_cast<void>(target.install(newer_receipt));
    expect_error(
        [&] { static_cast<void>(target.install(older_receipt)); },
        ErrorCode::VersionRollback,
        "pending older profile cannot be installed after a newer one");
    check(target.installed_version("profile-A") == 4,
          "failed rollback leaves version unchanged");
    check(target.installed_profile("profile-A") ==
              std::optional<Bytes>(Bytes{4}),
          "failed rollback leaves profile unchanged");
}

void test_signature_and_certificate_failures() {
    Fixture fixture;
    DownloadTransactionManager target = fixture.make_device("eid-target");

    DownloadRequest expired = fixture.make_request(
        "txn-expired", "eid-target", target.issue_nonce(), "profile-A", 1,
        {1}, 20'000);
    expect_error(
        [&] { static_cast<void>(target.download(expired)); },
        ErrorCode::CertificateExpired,
        "expired certificate");

    DownloadRequest wrong_identity = fixture.make_request(
        "txn-identity", "eid-target", target.issue_nonce(), "profile-A", 1,
        {1});
    wrong_identity.server_identity = "other-server.example";
    expect_error(
        [&] { static_cast<void>(target.download(wrong_identity)); },
        ErrorCode::ServerIdentityMismatch, "server identity mismatch");

    DownloadRequest tampered = fixture.make_request(
        "txn-tampered", "eid-target", target.issue_nonce(), "profile-A", 1,
        {1, 2, 3});
    tampered.profile.payload[1] = 9;
    expect_error(
        [&] { static_cast<void>(target.download(tampered)); },
        ErrorCode::InvalidSignature,
        "payload tampering");

    DownloadRequest time_tampered = fixture.make_request(
        "txn-time", "eid-target", target.issue_nonce(), "profile-A", 1, {1});
    time_tampered.validation_time = 501;
    expect_error(
        [&] { static_cast<void>(target.download(time_tampered)); },
        ErrorCode::InvalidSignature,
        "validation time tampering");

    DownloadRequest untrusted = fixture.make_request(
        "txn-untrusted", "eid-target", target.issue_nonce(), "profile-A", 1,
        {1});
    untrusted.certificate_chain.front().signature[0] ^= 1U;
    expect_error(
        [&] { static_cast<void>(target.download(untrusted)); },
        ErrorCode::InvalidCertificateChain,
        "broken certificate signature");

    PrivateKey rogue_root_key;
    const Certificate rogue_root = make_root_certificate(
        "rogue-root", rogue_root_key, 20, 100, 10'000);
    const Certificate rogue_server = issue_certificate(
        std::string(kServerIdentity), fixture.server_key.public_key(),
        rogue_root, rogue_root_key, 21, 100, 10'000, false);
    DownloadRequest unknown_anchor = fixture.make_request(
        "txn-unknown-anchor", "eid-target", target.issue_nonce(), "profile-A",
        1, {1});
    unknown_anchor.certificate_chain = {rogue_server};
    unknown_anchor.signature =
        sign_download_request(fixture.server_key, unknown_anchor);
    expect_error(
        [&] { static_cast<void>(target.download(unknown_anchor)); },
        ErrorCode::UntrustedCertificate,
        "untrusted certificate chain");
}

void test_persistent_state_blocks_reboot_replay() {
    Fixture fixture;
    DownloadTransactionManager first = fixture.make_device("eid-target");
    DownloadRequest request = fixture.make_request(
        "txn-persist", "eid-target", first.issue_nonce(), "profile-A", 2,
        {2});
    DownloadReceipt receipt = first.download(request);
    first.confirm_download(receipt);
    static_cast<void>(first.install(receipt));
    ManagerState state = first.snapshot();

    DownloadTransactionManager restarted = fixture.make_device("eid-target");
    restarted.restore_state(state);
    expect_error(
        [&] { static_cast<void>(restarted.download(request)); },
        ErrorCode::TransactionAlreadyUsed,
        "transaction replay after restart");

    DownloadRequest rollback = fixture.make_request(
        "txn-after-restart", "eid-target", restarted.issue_nonce(), "profile-A",
        1, {1});
    expect_error(
        [&] { static_cast<void>(restarted.download(rollback)); },
        ErrorCode::VersionRollback,
        "downgrade after restart");

    DownloadTransactionManager other = fixture.make_device("eid-other");
    expect_error(
        [&] { other.restore_state(state); }, ErrorCode::StateMismatch,
        "state cannot cross devices");
}

}  // namespace

int main() {
    test_crypto_vectors();
    test_legal_download_and_staged_install();
    test_cross_device_and_eid_replacement_are_rejected();
    test_replay_and_nonce_reuse_are_rejected();
    test_version_rollback_is_rejected_at_both_stages();
    test_pending_older_transaction_fails_at_install();
    test_signature_and_certificate_failures();
    test_persistent_state_blocks_reboot_replay();
    std::cout << "all netsec tests passed\n";
}
