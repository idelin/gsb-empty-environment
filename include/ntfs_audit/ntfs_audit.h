// ntfs_audit - Embeddable, zero-dependency C++17 NTFS $MFT FILE record
// security auditing library. All parsing is bounds-checked and the public
// entry point is noexcept: malformed or hostile records produce findings
// instead of exceptions or out-of-bounds memory access.
#pragma once

#include <cstddef>
#include <cstdint>
#include <optional>
#include <string>
#include <vector>

namespace ntfs_audit {

// NTFS attribute type tags and reparse point tags.
constexpr uint32_t kAttrStandardInformation = 0x00000010U;
constexpr uint32_t kAttrFileName            = 0x00000030U;
constexpr uint32_t kAttrData                = 0x00000080U;
constexpr uint32_t kAttrReparsePoint        = 0x000000C0U;
constexpr uint32_t kAttrEndMarker           = 0xFFFFFFFFU;

constexpr uint32_t kReparseTagMountPoint = 0xA0000003U;
constexpr uint32_t kReparseTagSymlink    = 0xA000000CU;

enum class Severity {
    kInfo,
    kWarning,
    kError
};

enum class FindingCode {
    kInvalidMagic,
    kRecordSizeMismatch,
    kUsaMalformed,
    kFixupMismatch,
    kHeaderOutOfBounds,
    kAttributeOffsetOutOfBounds,
    kAttributeNotAligned,
    kAttributeZeroLength,
    kAttributeOutOfBounds,
    kAttributeBadHeader,
    kAttributeNameOutOfBounds,
    kResidentContentOutOfBounds,
    kStreamNameTooLong,
    kHiddenAlternateDataStream,
    kStandardInformationTruncated,
    kFileNameTruncated,
    kDataRunMalformed,
    kDataRunTruncated,
    kDataRunOverflow,
    kDataRunNegativeLcn,
    kDataRunOutsideVolume,
    kDataRunOverlap,
    kDataRunVcnMismatch,
    kReparseTruncated,
    kReparseBadOffset,
    kReparseUnknownTag,
    kReparseUnsafeDriveAbsolute,
    kReparseUnsafeUnc,
    kReparseUnsafeDevicePrefix,
    kReparseUnsafeTraversal
};

const char* ToString(FindingCode code) noexcept;
const char* ToString(Severity severity) noexcept;

struct Finding {
    Severity severity = Severity::kInfo;
    FindingCode code = FindingCode::kInvalidMagic;
    std::string message;
    size_t offset = 0;
    std::string attributeName;
};

struct StandardInformation {
    uint64_t creationTime = 0;
    uint64_t modificationTime = 0;
    uint64_t mftChangeTime = 0;
    uint64_t accessTime = 0;
    uint32_t fileAttributes = 0;
};

struct FileNameInformation {
    uint64_t parentDirectoryReference = 0;
    uint64_t allocatedSize = 0;
    uint64_t realSize = 0;
    uint32_t flags = 0;
    uint8_t nameSpace = 0;
    std::string name;  // UTF-8 transcoded from UTF-16LE
};

struct DataRun {
    uint64_t startVcn = 0;
    uint64_t lengthClusters = 0;
    int64_t lcn = -1;       // -1 for sparse runs
    bool sparse = true;
};

struct DataStream {
    uint32_t attributeId = 0;
    std::string name;       // UTF-8; empty for the default unnamed $DATA
    bool named = false;     // true => alternate data stream (ADS)
    bool nameTooLong = false;
    bool resident = true;
    uint64_t logicalSize = 0;
    std::vector<DataRun> runs;
    size_t residentOffset = 0;  // offset of resident content inside the record
    size_t residentLength = 0;
};

struct ReparsePoint {
    uint32_t tag = 0;
    bool symlink = false;
    bool mountPoint = false;
    std::string substituteName;  // UTF-8
    std::string printName;       // UTF-8
    std::vector<FindingCode> violations;
};

struct AuditOptions {
    // Exact FILE record size expected by the host (must be a non-zero
    // multiple of 512). 1024 is the NTFS default.
    size_t expectedRecordSize = 1024;
    // Total number of clusters in the volume. 0 disables volume-bound
    // checks for non-resident data runs.
    uint64_t volumeClusterCount = 0;
    // Maximum permitted attribute (stream) name length in UTF-16 code
    // units. NTFS limits the on-disk field to 255.
    size_t maxStreamNameChars = 255;
    // When true, every named $DATA stream is promoted to a hard error
    // instead of being reported as a warning.
    bool treatAdsAsError = false;
};

struct AuditResult {
    std::vector<Finding> findings;
    uint16_t recordFlags = 0;
    uint32_t recordNumber = 0;
    std::optional<StandardInformation> standardInformation;
    std::vector<FileNameInformation> fileNames;
    std::vector<DataStream> dataStreams;
    std::optional<ReparsePoint> reparsePoint;

    // True when no Error-severity finding was produced.
    bool ok() const noexcept;
    bool has(FindingCode code) const noexcept;
    const Finding* firstOf(FindingCode code) const noexcept;
};

// Audits a single raw FILE record. The function never throws and never
// retains a reference to the supplied buffer after returning.
AuditResult AuditRecord(const uint8_t* record, size_t size,
                        const AuditOptions& options = {}) noexcept;

// Convenience overload for contiguous byte containers.
inline AuditResult AuditRecord(const std::vector<uint8_t>& record,
                               const AuditOptions& options = {}) noexcept {
    return AuditRecord(record.data(), record.size(), options);
}

}  // namespace ntfs_audit
