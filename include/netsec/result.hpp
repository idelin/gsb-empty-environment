#pragma once

#include <string>
#include <utility>

namespace netsec {

enum class VerifyCode {
    Ok,
    Malformed,
    UnsupportedFormatVersion,
    UnsupportedAlgorithm,
    UnknownKey,
    BadSignature,
    PlatformMismatch,
    ChannelMismatch,
    AlreadyInstalled,
    DowngradeRejected,
    DowngradeAuthorizationInvalid,
    DowngradeAuthorizationExpired,
    DowngradeTargetMismatch,
    ChunkIndexGap,
    ChunkLengthMismatch,
    ChunkHashMismatch,
    PayloadSizeMismatch,
    PayloadHashMismatch,
    ReadbackMismatch,
    IoError,
};

const char* to_string(VerifyCode code) noexcept;

struct Outcome {
    VerifyCode code{VerifyCode::Ok};
    std::string detail;

    bool ok() const noexcept { return code == VerifyCode::Ok; }
    explicit operator bool() const noexcept { return ok(); }
};

template <typename T>
struct ValueOutcome : Outcome {
    T value{};
};

inline Outcome fail(VerifyCode code, std::string detail) {
    return Outcome{code, std::move(detail)};
}

template <typename T>
ValueOutcome<T> fail_value(VerifyCode code, std::string detail) {
    ValueOutcome<T> result;
    result.code = code;
    result.detail = std::move(detail);
    return result;
}

template <typename T>
ValueOutcome<T> success(T value) {
    ValueOutcome<T> result;
    result.value = std::move(value);
    return result;
}

}  // namespace netsec
