#include "netsec/result.hpp"

namespace netsec {

const char* to_string(VerifyCode code) noexcept {
    switch (code) {
        case VerifyCode::Ok:
            return "ok";
        case VerifyCode::Malformed:
            return "malformed";
        case VerifyCode::UnsupportedFormatVersion:
            return "unsupported_format_version";
        case VerifyCode::UnsupportedAlgorithm:
            return "unsupported_algorithm";
        case VerifyCode::UnknownKey:
            return "unknown_key";
        case VerifyCode::BadSignature:
            return "bad_signature";
        case VerifyCode::PlatformMismatch:
            return "platform_mismatch";
        case VerifyCode::ChannelMismatch:
            return "channel_mismatch";
        case VerifyCode::AlreadyInstalled:
            return "already_installed";
        case VerifyCode::DowngradeRejected:
            return "downgrade_rejected";
        case VerifyCode::DowngradeAuthorizationInvalid:
            return "downgrade_authorization_invalid";
        case VerifyCode::DowngradeAuthorizationExpired:
            return "downgrade_authorization_expired";
        case VerifyCode::DowngradeTargetMismatch:
            return "downgrade_target_mismatch";
        case VerifyCode::ChunkIndexGap:
            return "chunk_index_gap";
        case VerifyCode::ChunkLengthMismatch:
            return "chunk_length_mismatch";
        case VerifyCode::ChunkHashMismatch:
            return "chunk_hash_mismatch";
        case VerifyCode::PayloadSizeMismatch:
            return "payload_size_mismatch";
        case VerifyCode::PayloadHashMismatch:
            return "payload_hash_mismatch";
        case VerifyCode::ReadbackMismatch:
            return "readback_mismatch";
        case VerifyCode::IoError:
            return "io_error";
    }
    return "unknown";
}

}  // namespace netsec
