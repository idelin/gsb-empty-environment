#pragma once

#include <filesystem>
#include <memory>
#include <span>
#include <string_view>

#include "netsec/manifest.hpp"
#include "netsec/policy.hpp"
#include "netsec/result.hpp"
#include "netsec/verification.hpp"

namespace netsec {

class FileInstaller {
public:
    explicit FileInstaller(std::filesystem::path destination);

    [[nodiscard]] std::filesystem::path staging_path() const;

    [[nodiscard]] Outcome write(std::span<const std::uint8_t> payload);

    [[nodiscard]] Outcome commit();

    void discard() noexcept;

private:
    std::filesystem::path destination_;
    std::filesystem::path staging_;
};

class UpdateManager {
public:
    UpdateManager(InstalledState installed,
                  const IManifestVerifier& manifest_verifier,
                  const IDowngradeTokenVerifier& downgrade_verifier,
                  IClock& clock,
                  IAuditSink& audit);

    UpdateManager(const UpdateManager&) = delete;
    UpdateManager& operator=(const UpdateManager&) = delete;
    UpdateManager(UpdateManager&&) = default;
    UpdateManager& operator=(UpdateManager&&) = default;

    [[nodiscard]] ValueOutcome<Manifest> inspect(std::string_view signed_wire) const;

    [[nodiscard]] Outcome install_update(std::string_view signed_wire,
                                         std::span<const std::uint8_t> payload,
                                         FileInstaller& installer) const;

    [[nodiscard]] Outcome install_update(
        std::string_view signed_wire,
        std::span<const std::uint8_t> payload,
        FileInstaller& installer,
        std::string_view encoded_downgrade_token) const;

private:
    [[nodiscard]] ValueOutcome<Manifest> authenticate(std::string_view signed_wire) const;

    InstalledState installed_;
    const IManifestVerifier* manifest_verifier_;
    const IDowngradeTokenVerifier* downgrade_verifier_;
    IClock* clock_;
    IAuditSink* audit_;
};

}  // namespace netsec
