// SPDX-License-Identifier: MIT

#include "dtls13/types.hpp"

namespace dtls13 {

const char* toString(ErrorCode code) noexcept {
    switch (code) {
        case ErrorCode::Ok:
            return "Ok";
        case ErrorCode::MalformedRecord:
            return "MalformedRecord";
        case ErrorCode::UnsupportedHeader:
            return "UnsupportedHeader";
        case ErrorCode::TruncatedDatagram:
            return "TruncatedDatagram";
        case ErrorCode::AeadDecryptFailed:
            return "AeadDecryptFailed";
        case ErrorCode::Replayed:
            return "Replayed";
        case ErrorCode::StaleEpoch:
            return "StaleEpoch";
        case ErrorCode::UnknownEpoch:
            return "UnknownEpoch";
        case ErrorCode::EpochLimitReached:
            return "EpochLimitReached";
        case ErrorCode::FragmentOverflow:
            return "FragmentOverflow";
        case ErrorCode::MessageTooLarge:
            return "MessageTooLarge";
        case ErrorCode::TooManyMessages:
            return "TooManyMessages";
        case ErrorCode::OverlapConflict:
            return "OverlapConflict";
        case ErrorCode::InconsistentHeader:
            return "InconsistentHeader";
        case ErrorCode::ReassemblyPoisoned:
            return "ReassemblyPoisoned";
        case ErrorCode::BadCookie:
            return "BadCookie";
        case ErrorCode::BadArgument:
            return "BadArgument";
    }
    return "Unknown";
}

bool constantTimeEqual(ByteView lhs, ByteView rhs) noexcept {
    if (lhs.size() != rhs.size()) {
        return false;
    }
    unsigned diff = 0;
    for (std::size_t i = 0; i < lhs.size(); ++i) {
        diff |= static_cast<unsigned>(lhs[i] ^ rhs[i]);
    }
    return diff == 0;
}

}  // namespace dtls13
