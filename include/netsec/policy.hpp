#pragma once

#include <chrono>
#include <cstdint>
#include <filesystem>
#include <memory>
#include <span>
#include <string>
#include <vector>

#include "netsec/manifest.hpp"
#include "netsec/result.hpp"
#include "netsec/version.hpp"

namespace netsec {

struct DowngradeToken {
    std::string format_version{"1"};
    std::string platform;
    std::string channel;
    Version current_version{};
    Version target_version{};
    std::uint64_t issued_at{0};
    std::uint64_t expires_at{0};
    std::string nonce;

    [[nodiscard]] std::string canonical_body() const;
    [[nodiscard]] std::string encode(const std::string& key_id,
                                     const std::string& algorithm,
                                     std::span<const std::uint8_t> signature) const;
};

struct ParsedDowngradeToken {
    DowngradeToken token;
    std::string key_id;
    std::string algorithm;
    std::vector<std::uint8_t> signature;
};

ValueOutcome<ParsedDowngradeToken> parse_downgrade_token(std::string_view wire);

class IDowngradeAuthority {
public:
    virtual ~IDowngradeAuthority() = default;

    [[nodiscard]] virtual std::string key_id() const = 0;
    [[nodiscard]] virtual std::string algorithm() const = 0;
    [[nodiscard]] virtual std::vector<std::uint8_t> sign(
        std::span<const std::uint8_t> canonical_bytes) const = 0;
};

class IDowngradeTokenVerifier {
public:
    virtual ~IDowngradeTokenVerifier() = default;

    [[nodiscard]] virtual Outcome verify(const std::string& key_id,
                                         const std::string& algorithm,
                                         std::span<const std::uint8_t> canonical_bytes,
                                         std::span<const std::uint8_t> signature) const = 0;
};

class HmacDowngradeAuthority final : public IDowngradeAuthority {
public:
    HmacDowngradeAuthority(std::string key_id, std::span<const std::uint8_t> key);

    [[nodiscard]] std::string key_id() const override;
    [[nodiscard]] std::string algorithm() const override;
    [[nodiscard]] std::vector<std::uint8_t> sign(
        std::span<const std::uint8_t> canonical_bytes) const override;

private:
    std::string key_id_;
    std::vector<std::uint8_t> key_;
};

class HmacDowngradeTokenVerifier final : public IDowngradeTokenVerifier {
public:
    explicit HmacDowngradeTokenVerifier(std::span<const std::uint8_t> key);

    [[nodiscard]] Outcome verify(const std::string& key_id,
                                 const std::string& algorithm,
                                 std::span<const std::uint8_t> canonical_bytes,
                                 std::span<const std::uint8_t> signature) const override;

private:
    std::vector<std::uint8_t> key_;
};

ValueOutcome<std::string> issue_downgrade_token(const DowngradeToken& token,
                                                const IDowngradeAuthority& authority);

class IClock {
public:
    virtual ~IClock() = default;
    [[nodiscard]] virtual std::uint64_t now_unix_seconds() const = 0;
};

class SystemClock final : public IClock {
public:
    [[nodiscard]] std::uint64_t now_unix_seconds() const override;
};

class FixedClock final : public IClock {
public:
    explicit FixedClock(std::uint64_t now) : now_(now) {}
    void set(std::uint64_t now) { now_ = now; }
    [[nodiscard]] std::uint64_t now_unix_seconds() const override { return now_; }

private:
    std::uint64_t now_;
};

struct InstalledState {
    Version version;
    std::string platform;
    std::string channel;
};

struct AuditEvent {
    std::uint64_t timestamp{0};
    std::string action;
    VerifyCode code{VerifyCode::Ok};
    std::string installed_version;
    std::string candidate_version;
    std::string platform;
    std::string channel;
    std::string detail;
};

class IAuditSink {
public:
    virtual ~IAuditSink() = default;
    virtual void record(const AuditEvent& event) = 0;
};

class FileAuditSink final : public IAuditSink {
public:
    explicit FileAuditSink(std::filesystem::path path);
    void record(const AuditEvent& event) override;

private:
    std::filesystem::path path_;
};

class MemoryAuditSink final : public IAuditSink {
public:
    void record(const AuditEvent& event) override { events.push_back(event); }
    std::vector<AuditEvent> events;
};

class UpdatePolicy {
public:
    UpdatePolicy(InstalledState installed, IClock& clock, IAuditSink& audit);

    [[nodiscard]] Outcome evaluate(const Manifest& manifest) const;

    [[nodiscard]] Outcome evaluate(const Manifest& manifest,
                                   std::string_view encoded_downgrade_token,
                                   const IDowngradeTokenVerifier& token_verifier) const;

private:
    [[nodiscard]] AuditEvent make_event(const char* action,
                                        VerifyCode code,
                                        const Manifest& manifest,
                                        std::string detail) const;

    InstalledState installed_;
    IClock& clock_;
    IAuditSink& audit_;
};

}  // namespace netsec
