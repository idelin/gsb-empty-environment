#ifndef REGHIVE_AUDIT_HPP
#define REGHIVE_AUDIT_HPP

#include <cstddef>
#include <cstdint>
#include <vector>

namespace reghive_audit {

// All failure modes detected by the raw-byte auditor.  Every value is
// stable and may be switched on by embedders.
enum class AuditStatus {
    Ok = 0,
    TooSmall,
    BadRegfMagic,
    SequenceMismatch,
    HiveBinsSizeMismatch,
    ChecksumMismatch,
    RootOffsetInvalid,
    BadHbinMagic,
    HbinOffsetMisaligned,
    HbinOffsetDiscontinuity,
    HbinSizeInvalid,
    CellSizeZero,
    CellSizeMisaligned,
    CellOutOfBounds,
    CellOverlap,
    CellOffsetUnaligned,
    DanglingReference,
    BadCellSignature,
    DepthLimitExceeded,
    CycleDetected,
    ValueListInvalid,
    InlineDataTooLong,
    ValueDataOutOfBounds,
    BigDataInvalid,
    MetadataDataOverlap,
    TruncatedStructure,
    NullPointer,
};

struct AuditOptions {
    // Maximum nk nesting depth (root == depth 0).  Bounds C++ recursion so
    // a hostile hive can never exhaust the host stack.
    uint32_t max_tree_depth = 512;
};

struct AuditReport {
    AuditStatus status = AuditStatus::Ok;
    bool ok = true;
    const char* message = "";
    // Absolute file offset of the offending bytes when applicable.
    size_t file_offset = 0;
    // Hive-relative offset (relative to the start of the first hbin) of the
    // offending cell when applicable; -1 when not cell related.
    int64_t hive_offset = -1;

    size_t hbin_count = 0;
    size_t cell_count = 0;
    size_t allocated_cell_count = 0;
    size_t nk_count = 0;
    size_t vk_count = 0;
    size_t max_depth_reached = 0;
};

// XOR of the 127 little-endian 32-bit words covering the first 508 bytes of
// the regf base block (offsets 0x000..0x1FB).  Returns 0 when |data| is null
// or |size| is not exactly 508.  Boundary note: 508 zero bytes yield
// 0x00000000 and 508 0xFF bytes yield 0xFFFFFFFF (127 is odd).
uint32_t regf_checksum(const uint8_t* data, size_t size);

// Audits a complete registry hive image held in memory.  Never allocates
// beyond O(n) bookkeeping, never throws on hostile input, and never reads
// outside [data, data + size).
AuditReport audit(const uint8_t* data, size_t size,
                  const AuditOptions& options = AuditOptions{});

// Convenience overload for byte vectors.
AuditReport audit(const std::vector<uint8_t>& data,
                  const AuditOptions& options = AuditOptions{});

// Stable textual name of an AuditStatus value (e.g. "CycleDetected").
const char* status_name(AuditStatus status);

}  // namespace reghive_audit

#endif  // REGHIVE_AUDIT_HPP
