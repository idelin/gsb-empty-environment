#include "netsec/updater.hpp"

#include <fstream>

namespace netsec {

FileInstaller::FileInstaller(std::filesystem::path destination)
    : destination_(std::move(destination)),
      staging_(destination_.string() + ".staging") {}

std::filesystem::path FileInstaller::staging_path() const {
    return staging_;
}

Outcome FileInstaller::write(std::span<const std::uint8_t> payload) {
    std::error_code error;
    std::filesystem::create_directories(destination_.parent_path(), error);
    if (error) {
        return fail(VerifyCode::IoError,
                    "cannot create destination directory: " + error.message());
    }

    std::ofstream stream(staging_, std::ios::binary | std::ios::trunc);
    if (!stream) {
        return fail(VerifyCode::IoError, "cannot open staging file: " + staging_.string());
    }
    stream.write(reinterpret_cast<const char*>(payload.data()),
                 static_cast<std::streamsize>(payload.size()));
    if (!stream) {
        return fail(VerifyCode::IoError, "failed writing staging file");
    }
    stream.close();
    if (!stream) {
        return fail(VerifyCode::IoError, "failed closing staging file");
    }
    return Outcome{};
}

Outcome FileInstaller::commit() {
    std::error_code error;
    std::filesystem::rename(staging_, destination_, error);
    if (error) {
        return fail(VerifyCode::IoError,
                    "atomic rename failed: " + error.message());
    }
    return Outcome{};
}

void FileInstaller::discard() noexcept {
    std::error_code error;
    std::filesystem::remove(staging_, error);
}

UpdateManager::UpdateManager(InstalledState installed,
                             const IManifestVerifier& manifest_verifier,
                             const IDowngradeTokenVerifier& downgrade_verifier,
                             IClock& clock,
                             IAuditSink& audit)
    : installed_(std::move(installed)),
      manifest_verifier_(&manifest_verifier),
      downgrade_verifier_(&downgrade_verifier),
      clock_(&clock),
      audit_(&audit) {}

ValueOutcome<Manifest> UpdateManager::authenticate(std::string_view signed_wire) const {
    auto wire = parse_signed_manifest(signed_wire);
    if (!wire) {
        return fail_value<Manifest>(wire.code, wire.detail);
    }
    return verify_signed_manifest(wire.value, *manifest_verifier_);
}

ValueOutcome<Manifest> UpdateManager::inspect(std::string_view signed_wire) const {
    return authenticate(signed_wire);
}

Outcome UpdateManager::install_update(
    std::string_view signed_wire,
    std::span<const std::uint8_t> payload,
    FileInstaller& installer) const {
    auto manifest = authenticate(signed_wire);
    if (!manifest) {
        return fail(manifest.code, manifest.detail);
    }

    UpdatePolicy policy(installed_, *clock_, *audit_);
    if (Outcome policy_result = policy.evaluate(manifest.value); !policy_result) {
        installer.discard();
        return policy_result;
    }

    if (Outcome chunk_result = verify_payload_chunks(manifest.value, payload); !chunk_result) {
        installer.discard();
        return chunk_result;
    }
    if (Outcome write_result = installer.write(payload); !write_result) {
        installer.discard();
        return write_result;
    }
    if (Outcome readback = verify_installed_readback(manifest.value, installer.staging_path());
        !readback) {
        installer.discard();
        return readback;
    }
    if (Outcome commit = installer.commit(); !commit) {
        installer.discard();
        return commit;
    }

    AuditEvent event;
    event.timestamp = clock_->now_unix_seconds();
    event.action = "update_committed";
    event.code = VerifyCode::Ok;
    event.installed_version = to_string(installed_.version);
    event.candidate_version = to_string(manifest.value.version);
    event.platform = manifest.value.platform;
    event.channel = manifest.value.channel;
    event.detail = "payload verified by chunks and post-install readback";
    audit_->record(event);
    return Outcome{};
}

Outcome UpdateManager::install_update(
    std::string_view signed_wire,
    std::span<const std::uint8_t> payload,
    FileInstaller& installer,
    std::string_view encoded_downgrade_token) const {
    auto manifest = authenticate(signed_wire);
    if (!manifest) {
        return fail(manifest.code, manifest.detail);
    }

    UpdatePolicy policy(installed_, *clock_, *audit_);
    if (Outcome policy_result =
            policy.evaluate(manifest.value, encoded_downgrade_token, *downgrade_verifier_);
        !policy_result) {
        installer.discard();
        return policy_result;
    }

    if (Outcome chunk_result = verify_payload_chunks(manifest.value, payload); !chunk_result) {
        installer.discard();
        return chunk_result;
    }
    if (Outcome write_result = installer.write(payload); !write_result) {
        installer.discard();
        return write_result;
    }
    if (Outcome readback = verify_installed_readback(manifest.value, installer.staging_path());
        !readback) {
        installer.discard();
        return readback;
    }
    if (Outcome commit = installer.commit(); !commit) {
        installer.discard();
        return commit;
    }

    AuditEvent event;
    event.timestamp = clock_->now_unix_seconds();
    event.action =
        manifest.value.version < installed_.version ? "downgrade_committed" : "update_committed";
    event.code = VerifyCode::Ok;
    event.installed_version = to_string(installed_.version);
    event.candidate_version = to_string(manifest.value.version);
    event.platform = manifest.value.platform;
    event.channel = manifest.value.channel;
    event.detail = "payload verified by chunks and post-install readback";
    audit_->record(event);
    return Outcome{};
}

}  // namespace netsec
