#pragma once

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

#include "machoaudit/sha256.hpp"

namespace machoaudit {

// Machine-readable audit verdict. Every non-Ok code is a hard rejection.
enum class AuditCode : int {
    Ok = 0,
    Truncated,
    BadMagic,
    BadCpuType,
    UnsupportedFileType,
    BadLoadCommandRange,
    BadSegmentRange,
    OverlappingSegments,
    TrailingData,
    DuplicateSignatureCommand,
    MissingSignatureCommand,
    MissingLinkedit,
    SignatureNotInLinkedit,
    SignatureNotAtLinkeditTail,
    SuperBlobTruncated,
    BadSuperBlobMagic,
    BadSuperBlobLength,
    BadBlobIndexRange,
    BlobOutOfBounds,
    OverlappingBlobs,
    MissingCodeDirectory,
    DuplicateCodeDirectory,
    BadCodeDirectoryMagic,
    UnsupportedCodeDirectoryVersion,
    UnsupportedHashType,
    BadPageSize,
    BadHashArrayRange,
    BadCodeSlotCount,
    CodeLimitOverflow,
    CodeLimitMismatch,
    ExecutableSegmentUncovered,
    PageHashMismatch,
    MissingSpecialSlot,
    SpecialSlotMismatch,
    BadBlobMagic,
    MissingHardenedRuntimeFlags,
    DangerousEntitlement,
    InternalError
};

struct AuditResult {
    AuditCode code = AuditCode::Ok;
    std::string detail;

    bool ok() const noexcept { return code == AuditCode::Ok; }
    explicit operator bool() const noexcept { return ok(); }
};

const char* auditCodeName(AuditCode code) noexcept;

// Audits a raw 64-bit Mach-O image (MH_EXECUTE / MH_DYLIB / MH_BUNDLE).
// Never throws on malformed input; every parse step is bounds checked.
AuditResult auditMachO(const std::uint8_t* data, std::size_t size);
AuditResult auditMachO(const std::vector<std::uint8_t>& data);

}  // namespace machoaudit
