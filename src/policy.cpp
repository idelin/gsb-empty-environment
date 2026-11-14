#include "netsec/policy.hpp"

#include <chrono>
#include <fstream>

#include "netsec/crypto.hpp"
#include "netsec/encoding.hpp"

namespace netsec {

namespace {

std::span<const std::uint8_t> as_bytes(const std::string& text) {
    return std::span<const std::uint8_t>(
        reinterpret_cast<const std::uint8_t*>(text.data()), text.size());
}

std::vector<std::string_view> split_lines_view(std::string_view text) {
    std::vector<std::string_view> lines;
    std::size_t begin = 0;
    while (begin <= text.size()) {
        const std::size_t newline = text.find('\n', begin);
        if (newline == std::string_view::npos) {
            if (begin < text.size()) {
                lines.push_back(text.substr(begin));
            }
            break;
        }
        lines.push_back(text.substr(begin, newline - begin));
        begin = newline + 1;
    }
    return lines;
}

std::string json_escape(const std::string& input) {
    std::string output;
    output.reserve(input.size() + 2);
    for (char ch : input) {
        switch (ch) {
            case '"':
                output += "\\\"";
                break;
            case '\\':
                output += "\\\\";
                break;
            case '\n':
                output += "\\n";
                break;
            case '\r':
                output += "\\r";
                break;
            case '\t':
                output += "\\t";
                break;
            default:
                output.push_back(ch);
        }
    }
    return output;
}

std::string json_field(const char* key, const std::string& value) {
    return std::string("\"") + key + "\":\"" + json_escape(value) + "\"";
}

}  // namespace

std::string DowngradeToken::canonical_body() const {
    std::string out;
    out += "netsec-downgrade:";
    out += format_version;
    out += '\n';
    out += "platform:";
    out += platform;
    out += '\n';
    out += "channel:";
    out += channel;
    out += '\n';
    out += "current-version:";
    out += to_string(current_version);
    out += '\n';
    out += "target-version:";
    out += to_string(target_version);
    out += '\n';
    out += "issued-at:";
    out += std::to_string(issued_at);
    out += '\n';
    out += "expires-at:";
    out += std::to_string(expires_at);
    out += '\n';
    out += "nonce:";
    out += nonce;
    out += '\n';
    return out;
}

std::string DowngradeToken::encode(const std::string& key_id,
                                   const std::string& algorithm,
                                   std::span<const std::uint8_t> signature) const {
    std::string wire = canonical_body();
    wire += "signature:";
    wire += key_id;
    wire += ':';
    wire += algorithm;
    wire += ':';
    wire += to_base64(signature);
    wire += '\n';
    return wire;
}

ValueOutcome<ParsedDowngradeToken> parse_downgrade_token(std::string_view wire) {
    if (wire.empty()) {
        return fail_value<ParsedDowngradeToken>(VerifyCode::Malformed, "empty downgrade token");
    }
    if (wire.back() == '\n') {
        wire.remove_suffix(1);
    }
    const std::size_t last_newline = wire.rfind('\n');
    if (last_newline == std::string_view::npos ||
        wire.substr(last_newline + 1).rfind("signature:", 0) != 0) {
        return fail_value<ParsedDowngradeToken>(
            VerifyCode::Malformed, "downgrade token must end with a signature record");
    }

    ParsedDowngradeToken parsed;
    bool seen_platform = false;
    bool seen_channel = false;
    bool seen_current = false;
    bool seen_target = false;
    bool seen_issued = false;
    bool seen_expires = false;
    bool seen_nonce = false;

    try {
        for (std::string_view line : split_lines_view(wire)) {
            const std::size_t colon = line.find(':');
            if (colon == std::string_view::npos) {
                return fail_value<ParsedDowngradeToken>(
                    VerifyCode::Malformed, "downgrade token line missing ':'");
            }
            const std::string key(line.substr(0, colon));
            const std::string_view value = line.substr(colon + 1);

            if (key == "netsec-downgrade") {
                parsed.token.format_version = std::string(value);
            } else if (key == "platform") {
                seen_platform = true;
                parsed.token.platform = std::string(value);
            } else if (key == "channel") {
                seen_channel = true;
                parsed.token.channel = std::string(value);
            } else if (key == "current-version") {
                seen_current = true;
                parsed.token.current_version = Version::parse(value);
            } else if (key == "target-version") {
                seen_target = true;
                parsed.token.target_version = Version::parse(value);
            } else if (key == "issued-at") {
                seen_issued = true;
                parsed.token.issued_at = std::stoull(std::string(value));
            } else if (key == "expires-at") {
                seen_expires = true;
                parsed.token.expires_at = std::stoull(std::string(value));
            } else if (key == "nonce") {
                seen_nonce = true;
                parsed.token.nonce = std::string(value);
            } else if (key == "signature") {
                const std::size_t c1 = value.find(':');
                const std::size_t c2 = value.find(':', c1 + 1);
                if (c1 == std::string_view::npos || c2 == std::string_view::npos ||
                    value.find(':', c2 + 1) != std::string_view::npos) {
                    return fail_value<ParsedDowngradeToken>(
                        VerifyCode::Malformed, "malformed token signature record");
                }
                parsed.key_id = std::string(value.substr(0, c1));
                parsed.algorithm = std::string(value.substr(c1 + 1, c2 - c1 - 1));
                parsed.signature = from_base64(value.substr(c2 + 1));
            } else {
                return fail_value<ParsedDowngradeToken>(
                    VerifyCode::Malformed, "unknown downgrade token field: " + key);
            }
        }
    } catch (const std::exception& error) {
        return fail_value<ParsedDowngradeToken>(VerifyCode::Malformed, error.what());
    }

    if (parsed.token.format_version != "1" || !seen_platform || !seen_channel ||
        !seen_current || !seen_target || !seen_issued || !seen_expires || !seen_nonce ||
        parsed.signature.size() != kSha256Size) {
        return fail_value<ParsedDowngradeToken>(
            VerifyCode::Malformed, "incomplete downgrade token");
    }
    return success(std::move(parsed));
}

HmacDowngradeAuthority::HmacDowngradeAuthority(std::string key_id,
                                               std::span<const std::uint8_t> key)
    : key_id_(std::move(key_id)), key_(key.begin(), key.end()) {}

std::string HmacDowngradeAuthority::key_id() const {
    return key_id_;
}

std::string HmacDowngradeAuthority::algorithm() const {
    return std::string(kHmacSha256Algorithm);
}

std::vector<std::uint8_t> HmacDowngradeAuthority::sign(
    std::span<const std::uint8_t> canonical_bytes) const {
    const Sha256Digest digest = hmac_sha256(key_, canonical_bytes);
    return std::vector<std::uint8_t>(digest.begin(), digest.end());
}

HmacDowngradeTokenVerifier::HmacDowngradeTokenVerifier(std::span<const std::uint8_t> key)
    : key_(key.begin(), key.end()) {}

Outcome HmacDowngradeTokenVerifier::verify(
    const std::string&,
    const std::string& algorithm,
    std::span<const std::uint8_t> canonical_bytes,
    std::span<const std::uint8_t> signature) const {
    if (algorithm != kHmacSha256Algorithm) {
        return fail(VerifyCode::UnsupportedAlgorithm,
                    "expected algorithm hmac-sha256, got " + algorithm);
    }
    const Sha256Digest expected = hmac_sha256(key_, canonical_bytes);
    if (!constant_time_equal(expected, signature)) {
        return fail(VerifyCode::DowngradeAuthorizationInvalid,
                    "downgrade authorization signature mismatch");
    }
    return Outcome{};
}

ValueOutcome<std::string> issue_downgrade_token(
    const DowngradeToken& token, const IDowngradeAuthority& authority) {
    if (token.platform.empty() || token.channel.empty() || token.nonce.empty()) {
        return fail_value<std::string>(
            VerifyCode::Malformed, "downgrade token requires platform, channel and nonce");
    }
    if (token.target_version >= token.current_version) {
        return fail_value<std::string>(
            VerifyCode::DowngradeTargetMismatch,
            "downgrade token target must be lower than current version");
    }
    if (token.expires_at <= token.issued_at) {
        return fail_value<std::string>(
            VerifyCode::Malformed, "downgrade token must have a finite validity window");
    }
    const std::string body = token.canonical_body();
    const auto signature = authority.sign(as_bytes(body));
    return success(token.encode(authority.key_id(), authority.algorithm(), signature));
}

std::uint64_t SystemClock::now_unix_seconds() const {
    const auto now = std::chrono::system_clock::now();
    return static_cast<std::uint64_t>(
        std::chrono::duration_cast<std::chrono::seconds>(now.time_since_epoch()).count());
}

FileAuditSink::FileAuditSink(std::filesystem::path path) : path_(std::move(path)) {
    std::error_code error;
    std::filesystem::create_directories(path_.parent_path(), error);
}

void FileAuditSink::record(const AuditEvent& event) {
    std::ofstream stream(path_, std::ios::app);
    if (!stream) {
        return;
    }
    stream << "{" << json_field("timestamp", std::to_string(event.timestamp)) << ','
           << json_field("action", event.action) << ','
           << json_field("code", to_string(event.code)) << ','
           << json_field("installed_version", event.installed_version) << ','
           << json_field("candidate_version", event.candidate_version) << ','
           << json_field("platform", event.platform) << ','
           << json_field("channel", event.channel) << ','
           << json_field("detail", event.detail) << "}\n";
}

UpdatePolicy::UpdatePolicy(InstalledState installed, IClock& clock, IAuditSink& audit)
    : installed_(std::move(installed)), clock_(clock), audit_(audit) {}

AuditEvent UpdatePolicy::make_event(const char* action,
                                    VerifyCode code,
                                    const Manifest& manifest,
                                    std::string detail) const {
    AuditEvent event;
    event.timestamp = clock_.now_unix_seconds();
    event.action = action;
    event.code = code;
    event.installed_version = to_string(installed_.version);
    event.candidate_version = to_string(manifest.version);
    event.platform = manifest.platform;
    event.channel = manifest.channel;
    event.detail = std::move(detail);
    return event;
}

Outcome UpdatePolicy::evaluate(const Manifest& manifest) const {
    if (manifest.platform != installed_.platform) {
        const Outcome result = fail(
            VerifyCode::PlatformMismatch,
            "manifest platform '" + manifest.platform + "' does not match installed '" +
                installed_.platform + "'");
        audit_.record(make_event("update_rejected", result.code, manifest, result.detail));
        return result;
    }
    if (manifest.channel != installed_.channel) {
        const Outcome result = fail(
            VerifyCode::ChannelMismatch,
            "manifest channel '" + manifest.channel + "' does not match installed '" +
                installed_.channel + "'");
        audit_.record(make_event("update_rejected", result.code, manifest, result.detail));
        return result;
    }
    if (manifest.version <= installed_.version) {
        const Outcome result = fail(
            VerifyCode::DowngradeRejected,
            "candidate " + to_string(manifest.version) + " is not newer than installed " +
                to_string(installed_.version) + "; explicit authorization required");
        audit_.record(make_event("downgrade_blocked", result.code, manifest, result.detail));
        return result;
    }

    audit_.record(make_event(
        "update_accepted", VerifyCode::Ok, manifest,
        "monotonic version update on matching platform and channel"));
    return Outcome{};
}

Outcome UpdatePolicy::evaluate(
    const Manifest& manifest,
    std::string_view encoded_downgrade_token,
    const IDowngradeTokenVerifier& token_verifier) const {
    if (manifest.platform != installed_.platform) {
        const Outcome result = fail(
            VerifyCode::PlatformMismatch,
            "manifest platform '" + manifest.platform + "' does not match installed '" +
                installed_.platform + "'");
        audit_.record(make_event("update_rejected", result.code, manifest, result.detail));
        return result;
    }
    if (manifest.channel != installed_.channel) {
        const Outcome result = fail(
            VerifyCode::ChannelMismatch,
            "manifest channel '" + manifest.channel + "' does not match installed '" +
                installed_.channel + "'");
        audit_.record(make_event("update_rejected", result.code, manifest, result.detail));
        return result;
    }
    if (manifest.version > installed_.version) {
        audit_.record(make_event(
            "update_accepted", VerifyCode::Ok, manifest,
            "monotonic version update on matching platform and channel"));
        return Outcome{};
    }
    if (manifest.version == installed_.version) {
        const Outcome result = fail(
            VerifyCode::AlreadyInstalled,
            "candidate version " + to_string(manifest.version) + " is already installed");
        audit_.record(make_event("update_rejected", result.code, manifest, result.detail));
        return result;
    }

    auto parsed_token = parse_downgrade_token(encoded_downgrade_token);
    if (!parsed_token) {
        audit_.record(make_event("downgrade_denied", parsed_token.code, manifest,
                                 parsed_token.detail));
        return fail(VerifyCode::DowngradeAuthorizationInvalid,
                    "invalid downgrade authorization: " + parsed_token.detail);
    }

    const DowngradeToken& token = parsed_token.value.token;
    const std::string canonical = token.canonical_body();
    const Outcome signature_result = token_verifier.verify(
        parsed_token.value.key_id, parsed_token.value.algorithm, as_bytes(canonical),
        parsed_token.value.signature);
    if (!signature_result) {
        audit_.record(make_event("downgrade_denied", signature_result.code, manifest,
                                 signature_result.detail));
        return signature_result;
    }

    if (token.platform != installed_.platform || token.channel != installed_.channel) {
        const Outcome result = fail(
            VerifyCode::DowngradeTargetMismatch,
            "downgrade authorization was issued for a different platform or channel");
        audit_.record(make_event("downgrade_denied", result.code, manifest, result.detail));
        return result;
    }
    if (token.current_version != installed_.version ||
        token.target_version != manifest.version) {
        const Outcome result = fail(
            VerifyCode::DowngradeTargetMismatch,
            "downgrade authorization does not name installed " +
                to_string(installed_.version) + " -> candidate " +
                to_string(manifest.version));
        audit_.record(make_event("downgrade_denied", result.code, manifest, result.detail));
        return result;
    }

    const std::uint64_t now = clock_.now_unix_seconds();
    if (now < token.issued_at || now > token.expires_at) {
        const Outcome result = fail(
            VerifyCode::DowngradeAuthorizationExpired,
            "downgrade authorization validity window does not cover current time");
        audit_.record(make_event("downgrade_denied", result.code, manifest, result.detail));
        return result;
    }

    audit_.record(make_event(
        "downgrade_authorized", VerifyCode::Ok, manifest,
        "explicit signed downgrade authorization accepted, nonce=" + token.nonce));
    return Outcome{};
}

}  // namespace netsec
