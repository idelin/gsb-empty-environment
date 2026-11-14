#include <algorithm>
#include <cstdio>
#include <filesystem>
#include <fstream>
#include <functional>
#include <span>
#include <string>
#include <vector>

#include "netsec/encoding.hpp"
#include "netsec/updater.hpp"

using namespace netsec;

namespace {

int g_failures = 0;
int g_checks = 0;

void check(bool condition, const std::string& message) {
    ++g_checks;
    if (!condition) {
        ++g_failures;
        std::printf("  FAIL: %s\n", message.c_str());
    }
}

std::vector<std::uint8_t> bytes(std::string_view text) {
    return std::vector<std::uint8_t>(text.begin(), text.end());
}

std::vector<std::uint8_t> make_payload(std::size_t size, char seed = 'A') {
    std::vector<std::uint8_t> payload(size);
    for (std::size_t i = 0; i < size; ++i) {
        payload[i] = static_cast<std::uint8_t>(seed + static_cast<char>(i % 26));
    }
    return payload;
}

Manifest make_manifest(Version version,
                       std::string platform,
                       std::string channel,
                       const std::vector<std::uint8_t>& payload,
                       std::size_t chunk_size) {
    Manifest manifest;
    manifest.format_version = std::string(kManifestFormatVersion);
    manifest.version = version;
    manifest.platform = std::move(platform);
    manifest.channel = std::move(channel);
    manifest.payload_size = payload.size();
    manifest.payload_sha256 = sha256(payload);

    for (std::size_t offset = 0, index = 0; offset < payload.size();
         offset += chunk_size, ++index) {
        const std::size_t length = std::min(chunk_size, payload.size() - offset);
        const auto slice = std::span<const std::uint8_t>(payload.data() + offset, length);
        ChunkInfo chunk;
        chunk.index = index;
        chunk.length = length;
        chunk.sha256 = sha256(slice);
        manifest.chunks.push_back(chunk);
    }
    return manifest;
}

struct Fixture {
    const std::vector<std::uint8_t> manifest_key = bytes("vendor-manifest-key-32-bytes-min!!");
    const std::vector<std::uint8_t> downgrade_key = bytes("vendor-downgrade-key-32-bytes-min!");

    const std::string platform = "darwin-arm64";
    const std::string channel = "stable";
    const Version installed{1, 4, 2};
    FixedClock clock{1'900'000'000};
    MemoryAuditSink audit;
    HmacManifestSigner signer{"vendor-key-2026", manifest_key};
    HmacManifestVerifier verifier{manifest_key};
    HmacDowngradeAuthority authority{"downgrade-authority-2026", downgrade_key};
    HmacDowngradeTokenVerifier downgrade_verifier{downgrade_key};

    std::string wire_for(Version version,
                         std::string manifest_platform,
                         std::string manifest_channel,
                         const std::vector<std::uint8_t>& payload) {
        Manifest manifest =
            make_manifest(version, std::move(manifest_platform),
                          std::move(manifest_channel), payload, 16);
        auto signed_manifest = sign_manifest(manifest, signer);
        if (!signed_manifest) {
            throw std::runtime_error(signed_manifest.detail);
        }
        return signed_manifest.value.encode();
    }

    std::string valid_downgrade_token(Version target) {
        DowngradeToken token;
        token.platform = platform;
        token.channel = channel;
        token.current_version = installed;
        token.target_version = target;
        token.issued_at = clock.now_unix_seconds() - 60;
        token.expires_at = clock.now_unix_seconds() + 600;
        token.nonce = "nonce-replay-test-0001";
        auto issued = issue_downgrade_token(token, authority);
        if (!issued) {
            throw std::runtime_error(issued.detail);
        }
        return issued.value;
    }
};

UpdateManager make_manager(Fixture& fixture) {
    return UpdateManager(
        InstalledState{fixture.installed, fixture.platform, fixture.channel},
        fixture.verifier, fixture.downgrade_verifier, fixture.clock, fixture.audit);
}

void test_sha256_known_vectors() {
    constexpr std::string_view kEmpty =
        "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855";
    constexpr std::string_view kAbc =
        "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad";
    check(to_hex(sha256(std::string_view{})) == kEmpty, "sha256 empty string vector");
    check(to_hex(sha256(std::string_view{"abc"})) == kAbc, "sha256 abc vector");

    const std::string long_input(1000, 'x');
    const auto expected = sha256(long_input);
    Sha256 hasher;
    for (char ch : long_input) {
        hasher.update(std::string_view(&ch, 1));
    }
    check(constant_time_equal(hasher.finish(), expected),
          "incremental sha256 matches one-shot sha256");
}

void test_legitimate_update_succeeds() {
    Fixture fixture;
    const auto payload = make_payload(40);
    const auto wire = fixture.wire_for(Version{1, 5, 0}, fixture.platform,
                                       fixture.channel, payload);

    UpdateManager manager = make_manager(fixture);
    const auto inspect = manager.inspect(wire);
    check(inspect.ok(), "legitimate update passes signature verification");
    check(inspect.value.version == Version{1, 5, 0}, "parsed candidate version is 1.5.0");

    const auto destination =
        std::filesystem::temp_directory_path() / "netsec_legit_update.bin";
    std::filesystem::remove(destination);
    FileInstaller installer(destination);
    const Outcome result = manager.install_update(wire, payload, installer);
    check(result.ok(), "legitimate update installs: " + result.detail);
    check(std::filesystem::exists(destination), "destination file exists after install");
    check(verify_installed_readback(inspect.value, destination).ok(),
          "post-install readback matches manifest hash");
    check(fixture.audit.events.back().action == "update_committed",
          "legitimate update is audited as committed");
    std::filesystem::remove(destination);
}

void test_replayed_old_manifest_is_rejected() {
    Fixture fixture;
    const auto old_payload = make_payload(37, 'Z');
    const auto old_wire = fixture.wire_for(Version{1, 2, 0}, fixture.platform,
                                           fixture.channel, old_payload);

    const auto destination =
        std::filesystem::temp_directory_path() / "netsec_replay.bin";
    std::filesystem::remove(destination);
    FileInstaller installer(destination);
    UpdateManager manager = make_manager(fixture);
    const Outcome result = manager.install_update(old_wire, old_payload, installer);

    check(result.code == VerifyCode::DowngradeRejected,
          "replayed old manifest is rejected as downgrade");
    check(!std::filesystem::exists(destination),
          "no destination file is written on rejected replay");
    check(!std::filesystem::exists(installer.staging_path()),
          "staging file is discarded on rejected replay");
    check(fixture.audit.events.back().action == "downgrade_blocked",
          "silent replay attempt is recorded as downgrade_blocked");
}

void test_unsigned_and_tampered_manifests_fail() {
    Fixture fixture;
    const auto payload = make_payload(32);
    const auto wire = fixture.wire_for(Version{1, 5, 0}, fixture.platform,
                                       fixture.channel, payload);

    auto tampered = wire;
    tampered.replace(tampered.find("1.5.0"), 5, "9.9.9");
    UpdateManager manager = make_manager(fixture);
    const auto tampered_result = manager.inspect(tampered);
    check(tampered_result.code == VerifyCode::BadSignature,
          "tampered manifest body fails signature verification");

    const auto wrong_key = bytes("some-other-vendor-key-that-is-not-trusted");
    HmacManifestSigner attacker{"vendor-key-2026", wrong_key};
    Manifest manifest = make_manifest(Version{1, 5, 0}, fixture.platform,
                                      fixture.channel, payload, 16);
    const auto forged = sign_manifest(manifest, attacker);
    check(forged.ok(), "attacker can produce a syntactically valid manifest");
    UpdateManager forged_manager = make_manager(fixture);
    const auto forged_result = forged_manager.inspect(forged.value.encode());
    check(forged_result.code == VerifyCode::BadSignature,
          "forged signature with untrusted key is rejected");
}

void test_channel_confusion_is_rejected() {
    Fixture fixture;
    const auto beta_payload = make_payload(35);
    const auto beta_wire = fixture.wire_for(Version{2, 0, 0}, fixture.platform,
                                            "beta", beta_payload);

    const auto destination =
        std::filesystem::temp_directory_path() / "netsec_channel.bin";
    std::filesystem::remove(destination);
    FileInstaller installer(destination);
    UpdateManager manager = make_manager(fixture);
    const Outcome result = manager.install_update(beta_wire, beta_payload, installer);

    check(result.code == VerifyCode::ChannelMismatch,
          "higher version from another channel is rejected");
    check(!std::filesystem::exists(destination), "channel confusion writes no destination");
    check(fixture.audit.events.back().action == "update_rejected",
          "channel confusion is audited as rejected");

    const auto foreign_platform_payload = make_payload(35);
    const auto foreign_wire = fixture.wire_for(
        Version{2, 0, 0}, "linux-x64", fixture.channel, foreign_platform_payload);
    const Outcome platform_result =
        manager.install_update(foreign_wire, foreign_platform_payload, installer);
    check(platform_result.code == VerifyCode::PlatformMismatch,
          "foreign platform manifest is rejected");
}

void test_downgrade_without_authorization_is_never_silent() {
    Fixture fixture;
    const auto payload = make_payload(24);
    const auto wire = fixture.wire_for(Version{1, 1, 0}, fixture.platform,
                                       fixture.channel, payload);

    const auto destination =
        std::filesystem::temp_directory_path() / "netsec_silent_downgrade.bin";
    std::filesystem::remove(destination);
    FileInstaller installer(destination);
    UpdateManager manager = make_manager(fixture);
    const Outcome result = manager.install_update(wire, payload, installer);

    check(result.code == VerifyCode::DowngradeRejected,
          "downgrade without token returns downgrade_rejected");
    check(!std::filesystem::exists(destination),
          "downgrade without authorization cannot install");
    check(fixture.audit.events.back().action == "downgrade_blocked",
          "blocked downgrade is written to the audit trail");
}

void test_explicit_authorized_downgrade_is_allowed_and_audited() {
    Fixture fixture;
    const Version target{1, 1, 0};
    const auto payload = make_payload(24);
    const auto wire = fixture.wire_for(target, fixture.platform, fixture.channel, payload);
    const auto token = fixture.valid_downgrade_token(target);

    const auto destination =
        std::filesystem::temp_directory_path() / "netsec_authorized_downgrade.bin";
    std::filesystem::remove(destination);
    FileInstaller installer(destination);
    UpdateManager manager = make_manager(fixture);
    const Outcome result =
        manager.install_update(wire, payload, installer, token);

    check(result.ok(), "explicitly signed downgrade token allows downgrade");
    check(std::filesystem::exists(destination), "downgraded payload is installed");
    const bool has_authorized =
        std::any_of(fixture.audit.events.begin(), fixture.audit.events.end(),
                    [](const AuditEvent& event) {
                        return event.action == "downgrade_authorized";
                    });
    const bool has_committed =
        std::any_of(fixture.audit.events.begin(), fixture.audit.events.end(),
                    [](const AuditEvent& event) {
                        return event.action == "downgrade_committed";
                    });
    check(has_authorized, "authorized downgrade decision is audited");
    check(has_committed, "authorized downgrade commit is audited");
    std::filesystem::remove(destination);
}

void test_downgrade_token_attacks_are_rejected() {
    Fixture fixture;
    const Version target{1, 1, 0};
    const auto payload = make_payload(24);
    const auto wire = fixture.wire_for(target, fixture.platform, fixture.channel, payload);

    auto tampered_token = fixture.valid_downgrade_token(target);
    tampered_token.replace(tampered_token.find("1.1.0"), 5, "1.0.9");
    const auto destination =
        std::filesystem::temp_directory_path() / "netsec_token_tamper.bin";
    std::filesystem::remove(destination);
    FileInstaller installer(destination);
    UpdateManager manager = make_manager(fixture);
    const Outcome tampered =
        manager.install_update(wire, payload, installer, tampered_token);
    check(tampered.code == VerifyCode::DowngradeAuthorizationInvalid,
          "tampered downgrade token body fails signature verification");

    DowngradeToken expired;
    expired.platform = fixture.platform;
    expired.channel = fixture.channel;
    expired.current_version = fixture.installed;
    expired.target_version = target;
    expired.issued_at = fixture.clock.now_unix_seconds() - 7200;
    expired.expires_at = fixture.clock.now_unix_seconds() - 10;
    expired.nonce = "expired-nonce";
    const auto expired_wire =
        issue_downgrade_token(expired, fixture.authority).value;
    const Outcome expired_result =
        manager.install_update(wire, payload, installer, expired_wire);
    check(expired_result.code == VerifyCode::DowngradeAuthorizationExpired,
          "expired downgrade authorization is rejected");

    DowngradeToken wrong_target = expired;
    wrong_target.target_version = Version{1, 0, 0};
    wrong_target.issued_at = fixture.clock.now_unix_seconds() - 60;
    wrong_target.expires_at = fixture.clock.now_unix_seconds() + 600;
    wrong_target.nonce = "wrong-target-nonce";
    const auto wrong_wire =
        issue_downgrade_token(wrong_target, fixture.authority).value;
    const Outcome wrong_result =
        manager.install_update(wire, payload, installer, wrong_wire);
    check(wrong_result.code == VerifyCode::DowngradeTargetMismatch,
          "token naming a different target version is rejected");

    DowngradeToken wrong_channel = expired;
    wrong_channel.channel = "beta";
    wrong_channel.target_version = target;
    wrong_channel.issued_at = fixture.clock.now_unix_seconds() - 60;
    wrong_channel.expires_at = fixture.clock.now_unix_seconds() + 600;
    wrong_channel.nonce = "wrong-channel-nonce";
    const auto channel_wire =
        issue_downgrade_token(wrong_channel, fixture.authority).value;
    const Outcome channel_result =
        manager.install_update(wire, payload, installer, channel_wire);
    check(channel_result.code == VerifyCode::DowngradeTargetMismatch,
          "token issued for another channel is rejected");
    check(!std::filesystem::exists(destination),
          "no install occurs while downgrade authorization is contested");
}

void test_chunk_and_readback_corruption_are_detected() {
    Fixture fixture;
    auto payload = make_payload(40);
    const Manifest manifest = make_manifest(
        Version{1, 5, 0}, fixture.platform, fixture.channel, payload, 16);

    auto corrupted = payload;
    corrupted[20] = static_cast<std::uint8_t>(corrupted[20] ^ 0xFF);
    const Outcome chunk_result = verify_payload_chunks(manifest, corrupted);
    check(chunk_result.code == VerifyCode::ChunkHashMismatch,
          "flipped payload byte fails chunk sha256 verification");

    ChunkVerifier verifier(manifest);
    const Outcome skipped =
        verifier.verify_chunk(1, std::span<const std::uint8_t>(payload.data() + 16, 16));
    check(skipped.ok(), "a valid individual chunk passes verification");
    check(verifier.finish().code == VerifyCode::ChunkIndexGap,
          "missing chunk is detected at finish");

    const Outcome good = verify_payload_chunks(manifest, payload);
    check(good.ok(), "untampered payload passes every chunk and total hash check");

    const auto destination =
        std::filesystem::temp_directory_path() / "netsec_readback.bin";
    std::filesystem::remove(destination);
    FileInstaller installer(destination);
    check(installer.write(payload).ok(), "staging payload is written");
    check(verify_installed_readback(manifest, installer.staging_path()).ok(),
          "staging file readback matches before commit");

    std::ofstream corruption(destination, std::ios::binary);
    corruption.write("tampered-after-commit", 21);
    corruption.close();
    const Outcome readback = verify_installed_readback(manifest, destination);
    check(readback.code == VerifyCode::ReadbackMismatch,
          "post-install readback detects corrupted installed file");
    std::filesystem::remove(destination);
    std::filesystem::remove(installer.staging_path());
}

void run(const char* name, const std::function<void()>& test) {
    std::printf("[ RUN      ] %s\n", name);
    test();
    std::printf("[       OK ] %s\n", name);
}

}  // namespace

int main() {
    run("sha256_known_vectors", test_sha256_known_vectors);
    run("legitimate_update_succeeds", test_legitimate_update_succeeds);
    run("replayed_old_manifest_is_rejected", test_replayed_old_manifest_is_rejected);
    run("unsigned_and_tampered_manifests_fail", test_unsigned_and_tampered_manifests_fail);
    run("channel_confusion_is_rejected", test_channel_confusion_is_rejected);
    run("downgrade_without_authorization_is_never_silent",
        test_downgrade_without_authorization_is_never_silent);
    run("explicit_authorized_downgrade_is_allowed_and_audited",
        test_explicit_authorized_downgrade_is_allowed_and_audited);
    run("downgrade_token_attacks_are_rejected", test_downgrade_token_attacks_are_rejected);
    run("chunk_and_readback_corruption_are_detected",
        test_chunk_and_readback_corruption_are_detected);

    std::printf("\n%d checks, %d failures\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}
