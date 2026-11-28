#include "ntfs_audit/ntfs_audit.h"

#include <algorithm>
#include <cstring>
#include <limits>

namespace ntfs_audit {

namespace {

constexpr size_t kSectorSize = 512;
constexpr size_t kMinRecordSize = 512;
constexpr size_t kHeaderMinSize = 42;       // fixed FILE record header fields
constexpr size_t kAttrHeaderCommon = 16;    // up to name length/offset/flags
constexpr size_t kAttrHeaderResident = 24;
constexpr size_t kAttrHeaderNonResident = 64;

struct Cursor {
    const uint8_t* base = nullptr;
    size_t size = 0;

    bool fits(size_t offset, size_t len) const noexcept {
        return offset <= size && len <= (size - offset);
    }
    uint16_t u16(size_t offset) const noexcept {
        return static_cast<uint16_t>(
            static_cast<uint16_t>(base[offset]) |
            (static_cast<uint16_t>(base[offset + 1]) << 8));
    }
    uint32_t u32(size_t offset) const noexcept {
        return static_cast<uint32_t>(base[offset]) |
               (static_cast<uint32_t>(base[offset + 1]) << 8) |
               (static_cast<uint32_t>(base[offset + 2]) << 16) |
               (static_cast<uint32_t>(base[offset + 3]) << 24);
    }
    uint64_t u64(size_t offset) const noexcept {
        uint64_t value = 0;
        for (size_t i = 0; i < 8; ++i) {
            value |= static_cast<uint64_t>(base[offset + i]) << (8U * i);
        }
        return value;
    }
};

// ---------------------------------------------------------------------------
// UTF-16LE / UTF-8 helpers (no external dependencies).
// ---------------------------------------------------------------------------
std::string decodeUtf16(const uint8_t* data, size_t byteLength) {
    std::string out;
    const size_t units = byteLength / 2U;
    size_t i = 0;
    auto append = [&out](uint32_t cp) {
        if (cp <= 0x7FU) {
            out.push_back(static_cast<char>(cp));
        } else if (cp <= 0x7FFU) {
            out.push_back(static_cast<char>(0xC0U | (cp >> 6)));
            out.push_back(static_cast<char>(0x80U | (cp & 0x3FU)));
        } else if (cp <= 0xFFFFU) {
            out.push_back(static_cast<char>(0xE0U | (cp >> 12)));
            out.push_back(static_cast<char>(0x80U | ((cp >> 6) & 0x3FU)));
            out.push_back(static_cast<char>(0x80U | (cp & 0x3FU)));
        } else {
            out.push_back(static_cast<char>(0xF0U | (cp >> 18)));
            out.push_back(static_cast<char>(0x80U | ((cp >> 12) & 0x3FU)));
            out.push_back(static_cast<char>(0x80U | ((cp >> 6) & 0x3FU)));
            out.push_back(static_cast<char>(0x80U | (cp & 0x3FU)));
        }
    };
    while (i < units) {
        uint32_t code = static_cast<uint32_t>(data[i * 2U]) |
                        (static_cast<uint32_t>(data[i * 2U + 1]) << 8);
        ++i;
        if (code >= 0xD800U && code <= 0xDBFFU) {
            if (i < units) {
                uint32_t low = static_cast<uint32_t>(data[i * 2U]) |
                               (static_cast<uint32_t>(data[i * 2U + 1]) << 8);
                ++i;
                if (low >= 0xDC00U && low <= 0xDFFFU) {
                    uint32_t cp = 0x10000U + ((code - 0xD800U) << 10) +
                                  (low - 0xDC00U);
                    append(cp);
                    continue;
                }
            }
            append(0xFFFDU);  // unpaired high surrogate
            continue;
        }
        if (code >= 0xDC00U && code <= 0xDFFFU) {
            append(0xFFFDU);  // unexpected low surrogate
            continue;
        }
        append(code);
    }
    return out;
}

std::vector<uint16_t> utf16Units(const uint8_t* data, size_t byteLength) {
    std::vector<uint16_t> units;
    const size_t count = byteLength / 2U;
    units.reserve(count);
    for (size_t i = 0; i < count; ++i) {
        units.push_back(static_cast<uint16_t>(
            static_cast<uint16_t>(data[i * 2U]) |
            (static_cast<uint16_t>(data[i * 2U + 1]) << 8)));
    }
    return units;
}

bool isAsciiAlpha(uint16_t c) noexcept {
    return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z');
}

class Auditor {
public:
    Auditor(const uint8_t* record, size_t size, AuditOptions options) noexcept
        : input_(record), inputSize_(size), options_(options) {}

    AuditResult run() noexcept {
        AuditResult result;
        if (input_ == nullptr || inputSize_ < kMinRecordSize ||
            options_.expectedRecordSize < kMinRecordSize ||
            options_.expectedRecordSize % kSectorSize != 0) {
            add(result, Severity::kError, FindingCode::kRecordSizeMismatch,
                "record or configured record size is invalid", 0);
            return result;
        }
        if (inputSize_ != options_.expectedRecordSize) {
            add(result, Severity::kError, FindingCode::kRecordSizeMismatch,
                "buffer size does not match expected FILE record size", 0);
            return result;
        }
        buffer_.assign(input_, input_ + inputSize_);
        cursor_ = Cursor{buffer_.data(), buffer_.size()};

        if (!checkMagic(result) || !applyFixup(result) ||
            !checkHeader(result)) {
            return result;
        }
        result.recordFlags = cursor_.u16(0x16);
        result.recordNumber = cursor_.u32(0x2C);
        parseAttributes(result);
        detectOverlaps(result);
        return result;
    }

private:
    const uint8_t* input_;
    size_t inputSize_;
    AuditOptions options_;
    std::vector<uint8_t> buffer_;
    Cursor cursor_;

    struct Interval {
        uint64_t start;
        uint64_t length;
        std::string streamName;
    };
    std::vector<Interval> intervals_;

    static void add(AuditResult& result, Severity severity,
                    FindingCode code, std::string message,
                    size_t offset) noexcept {
        Finding finding;
        finding.severity = severity;
        finding.code = code;
        finding.message = std::move(message);
        finding.offset = offset;
        result.findings.push_back(std::move(finding));
    }

    static void add(AuditResult& result, Severity severity,
                    FindingCode code, const std::string& message,
                    size_t offset, const std::string& attrName) noexcept {
        Finding finding;
        finding.severity = severity;
        finding.code = code;
        finding.message = message;
        finding.offset = offset;
        finding.attributeName = attrName;
        result.findings.push_back(std::move(finding));
    }

    bool checkMagic(AuditResult& result) noexcept {
        if (std::memcmp(buffer_.data(), "FILE", 4) != 0) {
            add(result, Severity::kError, FindingCode::kInvalidMagic,
                "missing FILE signature magic", 0);
            return false;
        }
        return true;
    }

    // Validates the Update Sequence Array and restores the two real bytes
    // at the end of every 512-byte sector.
    bool applyFixup(AuditResult& result) noexcept {
        const size_t sectorCount = inputSize_ / kSectorSize;
        const uint16_t usaOffset = cursor_.u16(0x04);
        const uint16_t usaCount = cursor_.u16(0x06);
        const uint32_t expectedCount =
            static_cast<uint32_t>(sectorCount) + 1U;
        if (usaCount < 2U ||
            static_cast<uint32_t>(usaCount) != expectedCount) {
            add(result, Severity::kError, FindingCode::kUsaMalformed,
                "USA entry count does not match sector count", 0x06);
            return false;
        }
        const size_t usaBytes = static_cast<size_t>(usaCount) * 2U;
        if (usaOffset < kHeaderMinSize ||
            !cursor_.fits(usaOffset, usaBytes)) {
            add(result, Severity::kError, FindingCode::kUsaMalformed,
                "USA offset/count points outside the record",
                static_cast<size_t>(usaOffset));
            return false;
        }
        const uint16_t sequence = cursor_.u16(usaOffset);
        for (size_t sector = 0; sector < sectorCount; ++sector) {
            const size_t endOffset =
                sector * kSectorSize + (kSectorSize - 2);
            const size_t pairOffset =
                static_cast<size_t>(usaOffset) + 2U + sector * 2U;
            if (cursor_.u16(endOffset) != sequence) {
                add(result, Severity::kError, FindingCode::kFixupMismatch,
                    "sector end does not match update sequence number",
                    endOffset);
                return false;
            }
            buffer_[endOffset] = buffer_[pairOffset];
            buffer_[endOffset + 1] = buffer_[pairOffset + 1];
        }
        return true;
    }

    bool checkHeader(AuditResult& result) noexcept {
        if (!cursor_.fits(0, kHeaderMinSize)) {
            add(result, Severity::kError, FindingCode::kHeaderOutOfBounds,
                "record smaller than fixed FILE header", 0);
            return false;
        }
        const uint32_t firstAttribute = cursor_.u16(0x14);
        const uint32_t usedSize = cursor_.u32(0x18);
        if (firstAttribute < kHeaderMinSize ||
            (firstAttribute % 8U) != 0U ||
            firstAttribute >= inputSize_) {
            add(result, Severity::kError,
                FindingCode::kAttributeOffsetOutOfBounds,
                "first attribute offset is out of bounds or misaligned",
                0x14);
            return false;
        }
        if (usedSize != 0U &&
            (usedSize > inputSize_ || usedSize < firstAttribute)) {
            add(result, Severity::kError, FindingCode::kHeaderOutOfBounds,
                "used record size contradicts first attribute offset",
                0x18);
            return false;
        }
        return true;
    }

    void parseAttributes(AuditResult& result) noexcept;
    void parseResident(AuditResult& result, size_t offset, uint32_t length,
                       uint32_t type, uint32_t attributeId,
                       const std::string& streamName,
                       bool nameTooLong) noexcept;
    void parseNonResident(AuditResult& result, size_t offset,
                          uint32_t length, uint32_t type,
                          uint32_t attributeId,
                          const std::string& streamName,
                          bool nameTooLong) noexcept;
    void parseStandardInformation(AuditResult& result, size_t start,
                                  size_t size) noexcept;
    void parseFileName(AuditResult& result, size_t start,
                       size_t size) noexcept;
    void parseReparse(AuditResult& result, size_t start,
                      size_t size) noexcept;
    void addDataStream(AuditResult& result, uint32_t attributeId,
                       const std::string& streamName, bool nameTooLong,
                       bool resident, uint64_t logicalSize,
                       std::vector<DataRun> runs, size_t residentOffset,
                       size_t residentLength) noexcept;
    bool decodeRuns(AuditResult& result, size_t runStart, size_t runEnd,
                    uint64_t startVcn, uint64_t lastVcn,
                    const std::string& streamName,
                    std::vector<DataRun>& runs) noexcept;
    void detectOverlaps(AuditResult& result) noexcept;
};

// Security analysis of a reparse point path expressed in UTF-16LE.
void analyzePath(const std::vector<uint16_t>& units,
                 std::vector<FindingCode>& violations) {
    size_t n = units.size();
    while (n > 0 && units[n - 1] == 0U) {
        --n;  // ignore trailing NUL terminators
    }
    if (n == 0U) {
        return;
    }

    // Drive-absolute target: "C:..." or "C:\..."
    if (n >= 2U && isAsciiAlpha(units[0]) && units[1] == ':') {
        violations.push_back(FindingCode::kReparseUnsafeDriveAbsolute);
    }
    // Device prefix: "\??\" or "\\?\"
    if (n >= 4U && units[0] == '\\' &&
        (units[1] == '?' || units[1] == '\\') && units[2] == '?' &&
        units[3] == '\\') {
        violations.push_back(FindingCode::kReparseUnsafeDevicePrefix);
    }
    // Win32 device prefix: "\\.\" (UNC "\\server" is handled separately)
    if (n >= 4U && units[0] == '\\' && units[1] == '\\' &&
        units[2] == '.' && units[3] == '\\') {
        violations.push_back(FindingCode::kReparseUnsafeDevicePrefix);
    }
    // UNC path: "\\server\share\..." (two leading backslashes not part of
    // a \\?\ or \\.\ prefix).
    if (n >= 2U && units[0] == '\\' && units[1] == '\\' &&
        !(n >= 4U && (units[2] == '?' || units[2] == '.') &&
          units[3] == '\\')) {
        violations.push_back(FindingCode::kReparseUnsafeUnc);
    }
    // Parent-directory traversal component: ".." bounded by separators or
    // the string ends.
    for (size_t i = 0; i + 2U <= n; ++i) {
        if (units[i] == '.' && units[i + 1] == '.') {
            bool leftOk = (i == 0U) || units[i - 1] == '\\' ||
                          units[i - 1] == '/';
            size_t after = i + 2U;
            bool rightOk = after == n || units[after] == '\\' ||
                           units[after] == '/';
            if (leftOk && rightOk) {
                violations.push_back(FindingCode::kReparseUnsafeTraversal);
                break;
            }
        }
    }
}

void Auditor::parseAttributes(AuditResult& result) noexcept {
    size_t offset = static_cast<size_t>(cursor_.u16(0x14));
    const size_t hardEnd =
        std::min(inputSize_, static_cast<size_t>(cursor_.u32(0x1C)));

    while (true) {
        if (!cursor_.fits(offset, 4)) {
            add(result, Severity::kError,
                FindingCode::kAttributeOutOfBounds,
                "attribute header starts outside the record", offset);
            return;
        }
        const uint32_t type = cursor_.u32(offset);
        if (type == kAttrEndMarker) {
            return;
        }
        if ((offset % 8U) != 0U) {
            add(result, Severity::kError,
                FindingCode::kAttributeNotAligned,
                "attribute is not 8-byte aligned", offset);
            return;
        }
        if (!cursor_.fits(offset, kAttrHeaderCommon)) {
            add(result, Severity::kError,
                FindingCode::kAttributeBadHeader,
                "attribute header truncated", offset);
            return;
        }
        const uint32_t length = cursor_.u32(offset + 4);
        if (length == 0U) {
            add(result, Severity::kError,
                FindingCode::kAttributeZeroLength,
                "zero-length attribute would loop forever", offset);
            return;
        }
        if (length < kAttrHeaderCommon ||
            !cursor_.fits(offset, static_cast<size_t>(length))) {
            add(result, Severity::kError,
                FindingCode::kAttributeOutOfBounds,
                "attribute length exceeds record bounds", offset);
            return;
        }
        const uint8_t nonResident = cursor_.base[offset + 8];
        const uint8_t nameLength = cursor_.base[offset + 9];
        const uint16_t nameOffset = cursor_.u16(offset + 10);
        const uint32_t attributeId = cursor_.u16(offset + 14);

        std::string streamName;
        bool nameTooLong = false;
        if (nameLength != 0U) {
            const size_t nameBytes = static_cast<size_t>(nameLength) * 2U;
            if (nameOffset < kAttrHeaderCommon ||
                !cursor_.fits(offset + nameOffset, nameBytes) ||
                nameBytes > length - static_cast<size_t>(nameOffset)) {
                add(result, Severity::kError,
                    FindingCode::kAttributeNameOutOfBounds,
                    "attribute name is outside the attribute record",
                    offset);
                return;
            }
            if (static_cast<size_t>(nameLength) >
                options_.maxStreamNameChars) {
                nameTooLong = true;
                add(result, Severity::kError,
                    FindingCode::kStreamNameTooLong,
                    "attribute/stream name exceeds permitted length",
                    offset);
            }
            streamName = decodeUtf16(buffer_.data() + offset + nameOffset,
                                     nameBytes);
        }

        if (nonResident == 0U) {
            parseResident(result, offset, length, type, attributeId,
                          streamName, nameTooLong);
        } else if (nonResident == 1U) {
            parseNonResident(result, offset, length, type, attributeId,
                             streamName, nameTooLong);
        } else {
            add(result, Severity::kError,
                FindingCode::kAttributeBadHeader,
                "unknown resident flag value", offset + 8);
            return;
        }

        offset += static_cast<size_t>(length);
        if (hardEnd != 0U && offset > hardEnd) {
            add(result, Severity::kError,
                FindingCode::kAttributeOutOfBounds,
                "attributes cross the used size of the record", offset);
            return;
        }
    }
}

void Auditor::parseResident(AuditResult& result, size_t offset,
                            uint32_t length, uint32_t type,
                            uint32_t attributeId,
                            const std::string& streamName,
                            bool nameTooLong) noexcept {
    if (length < kAttrHeaderResident) {
        add(result, Severity::kError,
            FindingCode::kAttributeBadHeader,
            "resident attribute header truncated", offset);
        return;
    }
    const uint32_t contentSize = cursor_.u32(offset + 16);
    const uint16_t contentOffset = cursor_.u16(offset + 20);
    if (contentOffset < kAttrHeaderResident ||
        (contentOffset % 8U) != 0U ||
        contentSize > length - static_cast<size_t>(contentOffset)) {
        add(result, Severity::kError,
            FindingCode::kResidentContentOutOfBounds,
            "resident content range is invalid", offset + 16);
        return;
    }
    const size_t contentStart = offset + contentOffset;

    switch (type) {
        case kAttrStandardInformation:
            parseStandardInformation(result, contentStart, contentSize);
            break;
        case kAttrFileName:
            parseFileName(result, contentStart, contentSize);
            break;
        case kAttrData:
            addDataStream(result, attributeId, streamName, nameTooLong,
                          true, contentSize, {}, contentStart, contentSize);
            break;
        case kAttrReparsePoint:
            parseReparse(result, contentStart, contentSize);
            break;
        default:
            break;  // other attribute types are intentionally skipped
    }
}

void Auditor::parseNonResident(AuditResult& result, size_t offset,
                               uint32_t length, uint32_t type,
                               uint32_t attributeId,
                               const std::string& streamName,
                               bool nameTooLong) noexcept {
    if (length < kAttrHeaderNonResident) {
        add(result, Severity::kError,
            FindingCode::kAttributeBadHeader,
            "non-resident attribute header truncated", offset);
        return;
    }
    const uint64_t startVcn = cursor_.u64(offset + 16);
    const uint64_t lastVcn = cursor_.u64(offset + 24);
    const uint64_t realSize = cursor_.u64(offset + 48);
    const uint16_t runOffset = cursor_.u16(offset + 32);
    if (runOffset < kAttrHeaderNonResident ||
        static_cast<size_t>(runOffset) >= length) {
        add(result, Severity::kError,
            FindingCode::kDataRunMalformed,
            "data run offset is outside the attribute", offset + 32);
        return;
    }

    if (type == kAttrReparsePoint) {
        add(result, Severity::kError,
            FindingCode::kReparseTruncated,
            "non-resident reparse point is not supported", offset);
        return;
    }

    std::vector<DataRun> runs;
    if (!decodeRuns(result, offset + runOffset, offset + length,
                    startVcn, lastVcn, streamName, runs)) {
        return;
    }
    if (type == kAttrData) {
        addDataStream(result, attributeId, streamName, nameTooLong,
                      false, realSize, std::move(runs), 0, 0);
    }
}

bool Auditor::decodeRuns(AuditResult& result, size_t runStart,
                         size_t runEnd, uint64_t startVcn,
                         uint64_t lastVcn,
                         const std::string& streamName,
                         std::vector<DataRun>& runs) noexcept {
    size_t pos = runStart;
    uint64_t vcn = startVcn;
    int64_t previousLcn = 0;
    bool sawRun = false;

    while (pos < runEnd) {
        const uint8_t header = cursor_.base[pos];
        if (header == 0U) {
            if (!sawRun) {
                add(result, Severity::kError,
                    FindingCode::kDataRunMalformed,
                    "empty run list without any data run", pos);
                return false;
            }
            if (vcn != lastVcn + 1U) {
                add(result, Severity::kError,
                    FindingCode::kDataRunVcnMismatch,
                    "decoded run count does not match the declared VCN "
                    "range",
                    pos);
                return false;
            }
            return true;  // run list terminator
        }
        ++pos;
        const unsigned lengthNibble =
            static_cast<unsigned>(header) & 0x0FU;
        const unsigned offsetNibble =
            (static_cast<unsigned>(header) >> 4U) & 0x0FU;
        if (lengthNibble == 0U || lengthNibble > 8U ||
            offsetNibble > 8U) {
            add(result, Severity::kError,
                FindingCode::kDataRunMalformed,
                "data run header nibbles are invalid", pos - 1U);
            return false;
        }
        const size_t fieldBytes =
            static_cast<size_t>(lengthNibble + offsetNibble);
        if (!cursor_.fits(pos, fieldBytes) || pos + fieldBytes > runEnd) {
            add(result, Severity::kError,
                FindingCode::kDataRunTruncated,
                "data run fields cross the attribute boundary", pos);
            return false;
        }

        uint64_t runLength = 0;
        for (unsigned i = 0; i < lengthNibble; ++i) {
            runLength |= static_cast<uint64_t>(cursor_.base[pos + i])
                         << (8U * i);
        }
        if (runLength == 0U) {
            add(result, Severity::kError,
                FindingCode::kDataRunMalformed,
                "data run with zero cluster length", pos);
            return false;
        }
        // Total length must fit in uint64 and remain a valid VCN range.
        if (runLength >
            std::numeric_limits<uint64_t>::max() - vcn) {
            add(result, Severity::kError,
                FindingCode::kDataRunOverflow,
                "VCN accumulation overflows while decoding runs", pos);
            return false;
        }

        DataRun run;
        run.startVcn = vcn;
        run.lengthClusters = runLength;

        if (offsetNibble == 0U) {
            run.sparse = true;
            run.lcn = -1;
        } else {
            uint64_t rawDelta = 0;
            for (unsigned i = 0; i < offsetNibble; ++i) {
                rawDelta |=
                    static_cast<uint64_t>(
                        cursor_.base[pos + lengthNibble + i])
                    << (8U * i);
            }
            // Sign-extend the delta from its actual field width.
            const unsigned signBit = offsetNibble * 8U - 1U;
            int64_t delta = static_cast<int64_t>(rawDelta);
            if ((rawDelta & (uint64_t{1} << signBit)) != 0U) {
                delta -= int64_t{1} << (offsetNibble * 8U);
            }
            // Signed addition overflow check against the extremes.
            if ((delta > 0 &&
                 previousLcn >
                     std::numeric_limits<int64_t>::max() - delta) ||
                (delta < 0 &&
                 previousLcn <
                     std::numeric_limits<int64_t>::min() - delta)) {
                add(result, Severity::kError,
                    FindingCode::kDataRunOverflow,
                    "signed LCN delta overflows int64", pos);
                return false;
            }
            const int64_t lcn = previousLcn + delta;
            if (lcn < 0) {
                add(result, Severity::kError,
                    FindingCode::kDataRunNegativeLcn,
                    "data run resolves to a negative logical cluster "
                    "number",
                    pos);
                return false;
            }
            const uint64_t unsignedLcn = static_cast<uint64_t>(lcn);
            if (options_.volumeClusterCount != 0U) {
                if (unsignedLcn >= options_.volumeClusterCount ||
                    runLength >
                        options_.volumeClusterCount - unsignedLcn) {
                    add(result, Severity::kError,
                        FindingCode::kDataRunOutsideVolume,
                        "data run extends beyond the volume", pos);
                    return false;
                }
            }
            run.sparse = false;
            run.lcn = lcn;
            previousLcn = lcn;
            intervals_.push_back(
                Interval{unsignedLcn, runLength, streamName});
        }

        runs.push_back(run);
        vcn += runLength;
        sawRun = true;
        pos += fieldBytes;
    }

    add(result, Severity::kError,
        FindingCode::kDataRunTruncated,
        "run list is missing its terminating zero byte", runEnd);
    return false;
}

void Auditor::detectOverlaps(AuditResult& result) noexcept {
    for (size_t i = 0; i < intervals_.size(); ++i) {
        for (size_t j = i + 1; j < intervals_.size(); ++j) {
            const Interval& a = intervals_[i];
            const Interval& b = intervals_[j];
            const uint64_t aEnd = a.start + a.length;
            const uint64_t bEnd = b.start + b.length;
            if (a.start < bEnd && b.start < aEnd) {
                add(result, Severity::kError,
                    FindingCode::kDataRunOverlap,
                    "physical cluster ranges of data runs overlap",
                    static_cast<size_t>(a.start), a.streamName);
                return;
            }
        }
    }
}

void Auditor::addDataStream(AuditResult& result, uint32_t attributeId,
                            const std::string& streamName,
                            bool nameTooLong, bool resident,
                            uint64_t logicalSize,
                            std::vector<DataRun> runs,
                            size_t residentOffset,
                            size_t residentLength) noexcept {
    DataStream stream;
    stream.attributeId = attributeId;
    stream.name = streamName;
    stream.named = !streamName.empty();
    stream.nameTooLong = nameTooLong;
    stream.resident = resident;
    stream.logicalSize = logicalSize;
    stream.runs = std::move(runs);
    stream.residentOffset = residentOffset;
    stream.residentLength = residentLength;
    result.dataStreams.push_back(std::move(stream));

    if (!streamName.empty()) {
        const Severity severity = options_.treatAdsAsError
                                      ? Severity::kError
                                      : Severity::kWarning;
        add(result, severity, FindingCode::kHiddenAlternateDataStream,
            "named alternate data stream (ADS) detected",
            residentOffset, streamName);
    }
}

void Auditor::parseStandardInformation(AuditResult& result, size_t start,
                                       size_t size) noexcept {
    // Fixed $STANDARD_INFORMATION layout occupies 48 bytes.
    if (size < 48U) {
        add(result, Severity::kError,
            FindingCode::kStandardInformationTruncated,
            "$STANDARD_INFORMATION content is shorter than 48 bytes",
            start);
        return;
    }
    StandardInformation info;
    info.creationTime = cursor_.u64(start);
    info.modificationTime = cursor_.u64(start + 8);
    info.mftChangeTime = cursor_.u64(start + 16);
    info.accessTime = cursor_.u64(start + 24);
    info.fileAttributes = cursor_.u32(start + 32);
    result.standardInformation = info;
}

void Auditor::parseFileName(AuditResult& result, size_t start,
                            size_t size) noexcept {
    // Fixed $FILE_NAME header is 66 bytes; the name is UTF-16LE afterwards.
    if (size < 66U) {
        add(result, Severity::kError,
            FindingCode::kFileNameTruncated,
            "$FILE_NAME content is shorter than 66 bytes", start);
        return;
    }
    const uint8_t nameLength = cursor_.base[start + 64];
    const uint8_t nameSpace = cursor_.base[start + 65];
    const size_t nameBytes = static_cast<size_t>(nameLength) * 2U;
    if (size < 66U + nameBytes) {
        add(result, Severity::kError,
            FindingCode::kFileNameTruncated,
            "$FILE_NAME name extends beyond the attribute", start + 64);
        return;
    }
    FileNameInformation info;
    info.parentDirectoryReference = cursor_.u64(start);
    info.allocatedSize = cursor_.u64(start + 40);
    info.realSize = cursor_.u64(start + 48);
    info.flags = cursor_.u32(start + 56);
    info.nameSpace = nameSpace;
    info.name = decodeUtf16(buffer_.data() + start + 66, nameBytes);
    result.fileNames.push_back(std::move(info));
}

void Auditor::parseReparse(AuditResult& result, size_t start,
                           size_t size) noexcept {
    // Generic reparse header: ReparseData(4) + ReparseTag(4), then the
    // Microsoft reparse data block begins with a length field.
    if (size < 8U) {
        add(result, Severity::kError,
            FindingCode::kReparseTruncated,
            "$REPARSE_POINT content is truncated", start);
        return;
    }
    const uint32_t dataLength = cursor_.u32(start);
    const uint32_t tag = cursor_.u32(start + 4);
    if (dataLength > size - 8U) {
        add(result, Severity::kError,
            FindingCode::kReparseTruncated,
            "reparse data length exceeds the resident content", start);
        return;
    }

    ReparsePoint point;
    point.tag = tag;
    point.symlink = (tag == kReparseTagSymlink);
    point.mountPoint = (tag == kReparseTagMountPoint);
    if (!point.symlink && !point.mountPoint) {
        add(result, Severity::kWarning,
            FindingCode::kReparseUnknownTag,
            "reparse point uses an unrecognised tag", start + 4);
        result.reparsePoint = std::move(point);
        return;
    }

    // Microsoft reparse data block (offset 8 from content start):
    //   uint16 SubstituteNameOffset; uint16 SubstituteNameLength;
    //   uint16 PrintNameOffset;        uint16 PrintNameLength;
    // followed by tag-specific data and the UTF-16LE path buffer.
    if (dataLength < 8U) {
        add(result, Severity::kError,
            FindingCode::kReparseTruncated,
            "symbolic link/mount point reparse block is truncated",
            start + 8);
        return;
    }
    const size_t blockStart = start + 8;
    const size_t pathBase = blockStart + 8;
    const uint16_t subOffset = cursor_.u16(blockStart);
    const uint16_t subLength = cursor_.u16(blockStart + 2);
    const uint16_t printOffset = cursor_.u16(blockStart + 4);
    const uint16_t printLength = cursor_.u16(blockStart + 6);
    const size_t pathCapacity = dataLength - 8U;

    auto readName = [&](uint16_t off, uint16_t len,
                        std::string& utf8,
                        std::vector<uint16_t>& units) -> bool {
        if (static_cast<size_t>(off) + len > pathCapacity ||
            (len % 2U) != 0U ||
            !cursor_.fits(pathBase + off, len)) {
            return false;
        }
        units = utf16Units(buffer_.data() + pathBase + off, len);
        utf8 = decodeUtf16(buffer_.data() + pathBase + off, len);
        return true;
    };

    std::vector<uint16_t> subUnits;
    std::vector<uint16_t> printUnits;
    if (!readName(subOffset, subLength, point.substituteName, subUnits) ||
        !readName(printOffset, printLength, point.printName,
                  printUnits)) {
        add(result, Severity::kError,
            FindingCode::kReparseBadOffset,
            "reparse path name offset/length is invalid", blockStart);
        return;
    }

    analyzePath(subUnits, point.violations);
    analyzePath(printUnits, point.violations);
    for (FindingCode code : point.violations) {
        const Severity severity =
            (code == FindingCode::kReparseUnknownTag)
                ? Severity::kWarning
                : Severity::kError;
        add(result, severity, code,
            "reparse point target escapes the volume or uses an unsafe "
            "absolute/device/traversal path",
            blockStart);
    }
    result.reparsePoint = std::move(point);
}

}  // namespace

const char* ToString(Severity severity) noexcept {
    switch (severity) {
        case Severity::kInfo:
            return "info";
        case Severity::kWarning:
            return "warning";
        case Severity::kError:
            return "error";
    }
    return "unknown";
}

const char* ToString(FindingCode code) noexcept {
    switch (code) {
        case FindingCode::kInvalidMagic:
            return "invalid-magic";
        case FindingCode::kRecordSizeMismatch:
            return "record-size-mismatch";
        case FindingCode::kUsaMalformed:
            return "usa-malformed";
        case FindingCode::kFixupMismatch:
            return "fixup-mismatch";
        case FindingCode::kHeaderOutOfBounds:
            return "header-out-of-bounds";
        case FindingCode::kAttributeOffsetOutOfBounds:
            return "attribute-offset-out-of-bounds";
        case FindingCode::kAttributeNotAligned:
            return "attribute-not-aligned";
        case FindingCode::kAttributeZeroLength:
            return "attribute-zero-length";
        case FindingCode::kAttributeOutOfBounds:
            return "attribute-out-of-bounds";
        case FindingCode::kAttributeBadHeader:
            return "attribute-bad-header";
        case FindingCode::kAttributeNameOutOfBounds:
            return "attribute-name-out-of-bounds";
        case FindingCode::kResidentContentOutOfBounds:
            return "resident-content-out-of-bounds";
        case FindingCode::kStreamNameTooLong:
            return "stream-name-too-long";
        case FindingCode::kHiddenAlternateDataStream:
            return "hidden-alternate-data-stream";
        case FindingCode::kStandardInformationTruncated:
            return "standard-information-truncated";
        case FindingCode::kFileNameTruncated:
            return "file-name-truncated";
        case FindingCode::kDataRunMalformed:
            return "data-run-malformed";
        case FindingCode::kDataRunTruncated:
            return "data-run-truncated";
        case FindingCode::kDataRunOverflow:
            return "data-run-overflow";
        case FindingCode::kDataRunNegativeLcn:
            return "data-run-negative-lcn";
        case FindingCode::kDataRunOutsideVolume:
            return "data-run-outside-volume";
        case FindingCode::kDataRunOverlap:
            return "data-run-overlap";
        case FindingCode::kDataRunVcnMismatch:
            return "data-run-vcn-mismatch";
        case FindingCode::kReparseTruncated:
            return "reparse-truncated";
        case FindingCode::kReparseBadOffset:
            return "reparse-bad-offset";
        case FindingCode::kReparseUnknownTag:
            return "reparse-unknown-tag";
        case FindingCode::kReparseUnsafeDriveAbsolute:
            return "reparse-unsafe-drive-absolute";
        case FindingCode::kReparseUnsafeUnc:
            return "reparse-unsafe-unc";
        case FindingCode::kReparseUnsafeDevicePrefix:
            return "reparse-unsafe-device-prefix";
        case FindingCode::kReparseUnsafeTraversal:
            return "reparse-unsafe-traversal";
    }
    return "unknown";
}

bool AuditResult::ok() const noexcept {
    for (const Finding& finding : findings) {
        if (finding.severity == Severity::kError) {
            return false;
        }
    }
    return true;
}

bool AuditResult::has(FindingCode code) const noexcept {
    return firstOf(code) != nullptr;
}

const Finding* AuditResult::firstOf(FindingCode code) const noexcept {
    for (const Finding& finding : findings) {
        if (finding.code == code) {
            return &finding;
        }
    }
    return nullptr;
}

AuditResult AuditRecord(const uint8_t* record, size_t size,
                        const AuditOptions& options) noexcept {
    Auditor auditor(record, size, options);
    return auditor.run();
}

}  // namespace ntfs_audit
