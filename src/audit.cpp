#include "machoaudit/audit.hpp"

#include <algorithm>
#include <array>
#include <cstring>
#include <limits>

namespace machoaudit {

namespace {

// ---- Mach-O constants ------------------------------------------------------

constexpr std::uint32_t MH_MAGIC_64 = 0xfeedfacfU;
constexpr std::uint32_t MH_CIGAM_64 = 0xcffaedfeU;
constexpr std::uint32_t CPU_ARCH_ABI64 = 0x01000000U;
constexpr std::uint32_t CPU_TYPE_X86_64 = 0x01000007U;
constexpr std::uint32_t CPU_TYPE_ARM64 = 0x0100000cU;
constexpr std::uint32_t MH_EXECUTE = 0x2U;
constexpr std::uint32_t MH_DYLIB = 0x6U;
constexpr std::uint32_t MH_BUNDLE = 0x8U;

constexpr std::uint32_t LC_SEGMENT_64 = 0x19U;
constexpr std::uint32_t LC_CODE_SIGNATURE = 0x1dU;

constexpr std::uint32_t VM_PROT_EXECUTE = 0x4U;

constexpr std::size_t kMachHeader64Size = 32U;
constexpr std::size_t kLoadCommandHeaderSize = 8U;
constexpr std::size_t kSegmentCommand64Size = 72U;
constexpr std::size_t kLinkeditCommandSize = 16U;

// ---- Code signing constants (on-disk blob fields are big-endian) -----------

constexpr std::uint32_t CSMAGIC_EMBEDDED_SIGNATURE = 0xfade0cc0U;
constexpr std::uint32_t CSMAGIC_CODEDIRECTORY = 0xfade0c02U;
constexpr std::uint32_t CSMAGIC_REQUIREMENTS = 0xfade0c01U;
constexpr std::uint32_t CSMAGIC_ENTITLEMENTS = 0xfade0c05U;
constexpr std::uint32_t CSMAGIC_EMBEDDED_ENTITLEMENTS_DER = 0xfade0c07U;

constexpr std::uint32_t CSSLOT_CODEDIRECTORY = 0U;
constexpr std::uint32_t CSSLOT_REQUIREMENTS = 2U;
constexpr std::uint32_t CSSLOT_ENTITLEMENTS = 5U;
constexpr std::uint32_t CSSLOT_ENTITLEMENTS_DER = 7U;

constexpr std::uint32_t CS_HASHTYPE_SHA256 = 2U;
constexpr std::uint32_t CS_KILL = 0x200U;
constexpr std::uint32_t CS_RUNTIME = 0x10000U;

constexpr std::size_t kSha256Size = 32U;

struct SegmentInfo {
    std::array<char, 16> name{};
    std::uint64_t vmAddr = 0;
    std::uint64_t vmSize = 0;
    std::uint64_t fileOff = 0;
    std::uint64_t fileSize = 0;
    std::uint32_t maxProt = 0;
    std::uint32_t initProt = 0;
};

struct BlobEntry {
    std::uint32_t type = 0;
    std::uint64_t offset = 0;
    std::uint64_t length = 0;
};

struct Cursor {
    const std::uint8_t* base;
    std::size_t size;

    bool inRange(std::size_t offset, std::size_t length) const {
        return offset <= size && length <= size - offset;
    }
};

std::uint32_t readBe32(const Cursor& cursor, std::size_t offset) {
    return (static_cast<std::uint32_t>(cursor.base[offset]) << 24) |
           (static_cast<std::uint32_t>(cursor.base[offset + 1]) << 16) |
           (static_cast<std::uint32_t>(cursor.base[offset + 2]) << 8) |
           static_cast<std::uint32_t>(cursor.base[offset + 3]);
}

std::uint32_t readLe32(const Cursor& cursor, std::size_t offset) {
    return (static_cast<std::uint32_t>(cursor.base[offset]) |
            (static_cast<std::uint32_t>(cursor.base[offset + 1]) << 8) |
            (static_cast<std::uint32_t>(cursor.base[offset + 2]) << 16) |
            (static_cast<std::uint32_t>(cursor.base[offset + 3]) << 24));
}

std::uint64_t readLe64(const Cursor& cursor, std::size_t offset) {
    std::uint64_t value = 0;
    for (std::size_t index = 0; index < 8U; ++index) {
        value |= static_cast<std::uint64_t>(cursor.base[offset + index])
                 << (index * 8U);
    }
    return value;
}

bool rangesOverlap(std::uint64_t startA, std::uint64_t lengthA,
                   std::uint64_t startB, std::uint64_t lengthB) {
    if (lengthA == 0U || lengthB == 0U) {
        return false;
    }
    const std::uint64_t endA = startA + lengthA;
    const std::uint64_t endB = startB + lengthB;
    return startA < endB && startB < endA;
}

bool constantTimeEqual(const std::uint8_t* expected,
                       const Sha256::Digest& actual) {
    std::uint8_t difference = 0;
    for (std::size_t index = 0; index < kSha256Size; ++index) {
        difference = static_cast<std::uint8_t>(
            difference | (expected[index] ^ actual[index]));
    }
    return difference == 0U;
}

AuditResult fail(AuditCode code, std::string detail) {
    return AuditResult{code, std::move(detail)};
}

bool findAsciiToken(const std::uint8_t* data, std::size_t size,
                    const char* token) {
    const std::size_t tokenLength = std::strlen(token);
    if (tokenLength == 0U || tokenLength > size) {
        return false;
    }
    for (std::size_t index = 0; index + tokenLength <= size; ++index) {
        bool matched = true;
        for (std::size_t inner = 0; inner < tokenLength; ++inner) {
            if (data[index + inner] !=
                static_cast<std::uint8_t>(token[inner])) {
                matched = false;
                break;
            }
        }
        if (matched) {
            return true;
        }
    }
    return false;
}

// XML plist: a key is dangerous only when explicitly paired with <true/>.
bool xmlGrantsKey(const std::uint8_t* data, std::size_t size,
                  const char* key) {
    const std::size_t keyLength = std::strlen(key);
    if (keyLength == 0U || keyLength > size) {
        return false;
    }
    const char keyOpen[] = "<key>";
    const char trueTag[] = "<true/>";
    const char falseTag[] = "<false/>";
    for (std::size_t index = 0; index + keyLength <= size; ++index) {
        bool matched = true;
        for (std::size_t inner = 0; inner < keyLength; ++inner) {
            if (data[index + inner] !=
                static_cast<std::uint8_t>(key[inner])) {
                matched = false;
                break;
            }
        }
        if (!matched) {
            continue;
        }
        const std::size_t before = index >= 5U ? index - 5U : 0U;
        if (!findAsciiToken(data + before, index - before, keyOpen)) {
            continue;
        }
        const std::size_t afterStart = index + keyLength;
        const std::size_t remaining = size - afterStart;
        const std::size_t window = remaining < 64U ? remaining : 64U;
        const bool hasTrue = findAsciiToken(data + afterStart, window, trueTag);
        const bool hasFalse = findAsciiToken(data + afterStart, window, falseTag);
        if (hasTrue && !hasFalse) {
            return true;
        }
        if (hasTrue && hasFalse) {
            const std::size_t trueAt = static_cast<std::size_t>(
                std::search(data + afterStart, data + afterStart + window,
                            trueTag, trueTag + 7U) -
                data);
            const std::size_t falseAt = static_cast<std::size_t>(
                std::search(data + afterStart, data + afterStart + window,
                            falseTag, falseTag + 8U) -
                data);
            if (trueAt < falseAt) {
                return true;
            }
        }
    }
    return false;
}

// DER plist: minimal DER boolean (01 01 01 = true, 01 01 00 = false).
bool derGrantsKey(const std::uint8_t* data, std::size_t size,
                  const char* key) {
    const std::size_t keyLength = std::strlen(key);
    if (keyLength == 0U || keyLength > size) {
        return false;
    }
    for (std::size_t index = 0; index + keyLength <= size; ++index) {
        bool matched = true;
        for (std::size_t inner = 0; inner < keyLength; ++inner) {
            if (data[index + inner] !=
                static_cast<std::uint8_t>(key[inner])) {
                matched = false;
                break;
            }
        }
        if (!matched) {
            continue;
        }
        const std::size_t valueStart = index + keyLength;
        const std::size_t remaining = size - valueStart;
        const std::size_t window = remaining < 32U ? remaining : 32U;
        for (std::size_t probe = 0; probe + 3U <= window; ++probe) {
            if (data[valueStart + probe] == 0x01U &&
                data[valueStart + probe + 1U] == 0x01U) {
                return data[valueStart + probe + 2U] != 0x00U;
            }
            if (probe > 0U && data[valueStart + probe] == 0x0cU) {
                break;
            }
        }
    }
    return false;
}

constexpr const char* kDangerousKeys[] = {
    "com.apple.security.get-task-allow",
    "com.apple.security.cs.allow-unsigned-executable-memory",
    "com.apple.security.cs.disable-library-validation"};

AuditResult checkEntitlements(const std::vector<BlobEntry>& entries,
                              std::size_t signatureBase,
                              const Cursor& image) {
    struct SlotSpec {
        std::uint32_t slot;
        bool der;
    };
    const SlotSpec specs[] = {{CSSLOT_ENTITLEMENTS, false},
                              {CSSLOT_ENTITLEMENTS_DER, true}};
    for (const SlotSpec& spec : specs) {
        for (const BlobEntry& entry : entries) {
            if (entry.type != spec.slot) {
                continue;
            }
            const std::size_t absolute =
                signatureBase + static_cast<std::size_t>(entry.offset);
            if (!image.inRange(absolute,
                               static_cast<std::size_t>(entry.length))) {
                return fail(AuditCode::BlobOutOfBounds,
                            "entitlement blob exceeds signature area");
            }
            if (entry.length < 8U) {
                return fail(AuditCode::BadBlobMagic,
                            "entitlement blob too short");
            }
            const std::uint8_t* payload = image.base + absolute + 8U;
            const std::size_t payloadSize =
                static_cast<std::size_t>(entry.length) - 8U;
            for (const char* key : kDangerousKeys) {
                const bool granted = spec.der
                                         ? derGrantsKey(payload, payloadSize, key)
                                         : xmlGrantsKey(payload, payloadSize, key);
                if (granted) {
                    return fail(AuditCode::DangerousEntitlement, key);
                }
            }
        }
    }
    return AuditResult{};
}

AuditResult checkSpecialSlots(const std::vector<BlobEntry>& entries,
                              std::size_t signatureBase,
                              const Cursor& image,
                              const std::uint8_t* codeDirectoryBytes,
                              std::uint32_t specialSlots,
                              std::uint32_t hashSize) {
    struct SlotSpec {
        std::uint32_t slot;
        std::uint32_t magic;
    };
    const SlotSpec specs[] = {
        {CSSLOT_REQUIREMENTS, CSMAGIC_REQUIREMENTS},
        {CSSLOT_ENTITLEMENTS, CSMAGIC_ENTITLEMENTS},
        {CSSLOT_ENTITLEMENTS_DER,
         CSMAGIC_EMBEDDED_ENTITLEMENTS_DER}};
    for (const SlotSpec& spec : specs) {
        const auto entryIt =
            std::find_if(entries.begin(), entries.end(),
                         [&spec](const BlobEntry& entry) {
                             return entry.type == spec.slot;
                         });
        if (entryIt == entries.end()) {
            continue;
        }
        if (specialSlots < spec.slot) {
            return fail(AuditCode::MissingSpecialSlot,
                        "CodeDirectory lacks special slot for present blob");
        }
        const std::size_t absolute =
            signatureBase + static_cast<std::size_t>(entryIt->offset);
        if (!image.inRange(absolute,
                           static_cast<std::size_t>(entryIt->length))) {
            return fail(AuditCode::BlobOutOfBounds,
                        "special slot blob exceeds signature area");
        }
        const Cursor blob{image.base + absolute,
                          static_cast<std::size_t>(entryIt->length)};
        if (blob.size < 8U || readBe32(blob, 0U) != spec.magic) {
            return fail(AuditCode::BadBlobMagic,
                        "special slot blob has unexpected magic");
        }
        const std::uint32_t slotIndex = spec.slot - 1U;
        const std::size_t digestOffset =
            static_cast<std::size_t>(slotIndex) *
            static_cast<std::size_t>(hashSize);
        const Sha256::Digest actual =
            Sha256::hash(blob.base, blob.size);
        if (!constantTimeEqual(codeDirectoryBytes + digestOffset, actual)) {
            return fail(AuditCode::SpecialSlotMismatch,
                        "special slot digest does not match blob bytes");
        }
    }
    // Every special slot must be backed by a real indexed blob.
    for (std::uint32_t slot = 1U; slot <= specialSlots; ++slot) {
        const std::uint32_t slotIndex = slot - 1U;
        const std::size_t digestOffset =
            static_cast<std::size_t>(slotIndex) *
            static_cast<std::size_t>(hashSize);
        const std::uint8_t* digest = codeDirectoryBytes + digestOffset;
        const bool zeroDigest =
            std::all_of(digest, digest + hashSize,
                        [](std::uint8_t byte) { return byte == 0U; });
        if (zeroDigest) {
            continue;
        }
        const bool indexed =
            std::any_of(entries.begin(), entries.end(),
                        [&slot](const BlobEntry& entry) {
                            return entry.type == slot;
                        });
        if (!indexed) {
            return fail(AuditCode::MissingSpecialSlot,
                        "non-empty special slot has no indexed blob");
        }
    }
    return AuditResult{};
}

}  // namespace

namespace {

AuditResult checkCodeDirectory(const BlobEntry& directoryEntry,
                               const std::vector<BlobEntry>& entries,
                               std::size_t signatureBase,
                               const Cursor& image,
                               const std::vector<SegmentInfo>& segments,
                               std::size_t signatureStart) {
    const std::size_t directoryAbsolute =
        signatureBase + static_cast<std::size_t>(directoryEntry.offset);
    if (!image.inRange(directoryAbsolute,
                       static_cast<std::size_t>(directoryEntry.length))) {
        return fail(AuditCode::BlobOutOfBounds,
                    "CodeDirectory blob exceeds signature area");
    }
    const Cursor directory{image.base + directoryAbsolute,
                           static_cast<std::size_t>(directoryEntry.length)};
    if (directory.size < 8U) {
        return fail(AuditCode::BadCodeDirectoryMagic,
                    "CodeDirectory too short");
    }
    if (readBe32(directory, 0U) != CSMAGIC_CODEDIRECTORY) {
        return fail(AuditCode::BadCodeDirectoryMagic,
                    "CodeDirectory magic mismatch");
    }
    const std::uint32_t directoryLength = readBe32(directory, 4U);
    if (directoryLength != directory.size || directoryLength < 44U) {
        return fail(AuditCode::BadCodeDirectoryMagic,
                    "CodeDirectory length field inconsistent");
    }
    const std::uint32_t version = readBe32(directory, 8U);
    const std::uint32_t flags = readBe32(directory, 12U);
    const std::uint32_t hashOffset = readBe32(directory, 16U);
    const std::uint32_t identifierOffset = readBe32(directory, 20U);
    const std::uint32_t specialSlots = readBe32(directory, 24U);
    const std::uint32_t codeSlots = readBe32(directory, 28U);
    const std::uint32_t codeLimit32 = readBe32(directory, 32U);
    const std::uint8_t hashSize = directory.base[36U];
    const std::uint8_t hashType = directory.base[37U];
    const std::uint8_t pageSizeField = directory.base[39U];

    if (version < 0x20001U) {
        return fail(AuditCode::UnsupportedCodeDirectoryVersion,
                    "CodeDirectory version below 0x20001");
    }
    if (hashType != CS_HASHTYPE_SHA256 ||
        static_cast<std::uint32_t>(hashSize) != kSha256Size) {
        return fail(AuditCode::UnsupportedHashType,
                    "only SHA-256 CodeDirectories are supported");
    }
    if (pageSizeField == 0U || pageSizeField > 20U) {
        return fail(AuditCode::BadPageSize,
                    "page size exponent must be in [1, 20]");
    }
    const std::uint64_t pageSize =
        std::uint64_t{1} << static_cast<unsigned>(pageSizeField);
    if (identifierOffset >= directoryLength) {
        return fail(AuditCode::BadHashArrayRange,
                    "identifier offset outside CodeDirectory");
    }

    std::uint64_t codeLimit64 = 0;
    if (version >= 0x20100U) {
        if (directoryLength < 48U) {
            return fail(AuditCode::UnsupportedCodeDirectoryVersion,
                        "version 0x20100+ requires codeLimit64 field");
        }
        codeLimit64 = (static_cast<std::uint64_t>(readBe32(directory, 40U))
                       << 32);
    }
    const std::uint64_t codeLimit =
        codeLimit64 | static_cast<std::uint64_t>(codeLimit32);

    const std::uint64_t totalSlots =
        static_cast<std::uint64_t>(specialSlots) +
        static_cast<std::uint64_t>(codeSlots);
    if (totalSlots >
        static_cast<std::uint64_t>(
            std::numeric_limits<std::size_t>::max() / hashSize)) {
        return fail(AuditCode::BadHashArrayRange,
                    "hash slot count overflows address space");
    }
    const std::size_t hashArrayBytes =
        static_cast<std::size_t>(totalSlots) *
        static_cast<std::size_t>(hashSize);
    if (hashOffset == 0U ||
        hashOffset >= directoryLength ||
        hashArrayBytes > directoryLength - hashOffset) {
        return fail(AuditCode::BadHashArrayRange,
                    "hash array extends outside CodeDirectory");
    }

    if (codeLimit > image.size) {
        return fail(AuditCode::CodeLimitOverflow,
                    "codeLimit exceeds image size");
    }
    // codeLimit must reach exactly the start of LC_CODE_SIGNATURE data.
    if (codeLimit != signatureStart) {
        return fail(AuditCode::CodeLimitMismatch,
                    "codeLimit does not end at LC_CODE_SIGNATURE offset");
    }

    // codeLimit must fully cover every executable segment.
    for (const SegmentInfo& segment : segments) {
        if ((segment.maxProt & VM_PROT_EXECUTE) == 0U &&
            (segment.initProt & VM_PROT_EXECUTE) == 0U) {
            continue;
        }
        if (segment.fileSize == 0U) {
            continue;
        }
        const std::uint64_t segmentEnd = segment.fileOff + segment.fileSize;
        if (segmentEnd < segment.fileOff) {
            return fail(AuditCode::BadSegmentRange,
                        "executable segment range overflows");
        }
        if (segmentEnd > codeLimit) {
            return fail(AuditCode::ExecutableSegmentUncovered,
                        "executable segment extends beyond codeLimit");
        }
    }

    const std::uint64_t expectedSlots64 =
        (codeLimit + pageSize - 1U) / pageSize;
    if (expectedSlots64 != static_cast<std::uint64_t>(codeSlots)) {
        return fail(AuditCode::BadCodeSlotCount,
                    "nCodeSlots disagrees with codeLimit/pageSize");
    }

    const std::uint8_t* slotBase =
        directory.base + hashOffset +
        static_cast<std::size_t>(specialSlots) *
            static_cast<std::size_t>(hashSize);
    const std::size_t codeLimitSize = static_cast<std::size_t>(codeLimit);
    for (std::uint32_t slot = 0U; slot < codeSlots; ++slot) {
        const std::size_t pageStart =
            static_cast<std::size_t>(pageSize) * slot;
        const std::size_t remaining = codeLimitSize - pageStart;
        const std::size_t chunk =
            remaining < static_cast<std::size_t>(pageSize)
                ? remaining
                : static_cast<std::size_t>(pageSize);
        const Sha256::Digest actual =
            Sha256::hash(image.base + pageStart, chunk);
        const std::uint8_t* expected =
            slotBase + static_cast<std::size_t>(slot) *
                           static_cast<std::size_t>(hashSize);
        if (!constantTimeEqual(expected, actual)) {
            return fail(AuditCode::PageHashMismatch,
                        "recomputed page hash disagrees with CodeDirectory");
        }
    }

    AuditResult specialResult = checkSpecialSlots(
        entries, signatureBase, image, directory.base + hashOffset,
        specialSlots, hashSize);
    if (!specialResult.ok()) {
        return specialResult;
    }

    if ((flags & CS_KILL) == 0U || (flags & CS_RUNTIME) == 0U) {
        return fail(AuditCode::MissingHardenedRuntimeFlags,
                    "CS_KILL and CS_RUNTIME must both be set");
    }

    return checkEntitlements(entries, signatureBase, image);
}

AuditResult checkSignature(const Cursor& image,
                           const std::vector<SegmentInfo>& segments,
                           std::size_t signatureStart,
                           std::uint32_t signatureSize) {
    if (signatureSize < 12U ||
        !image.inRange(signatureStart, signatureSize)) {
        return fail(AuditCode::SuperBlobTruncated,
                    "LC_CODE_SIGNATURE range exceeds image");
    }
    const Cursor signature{image.base + signatureStart, signatureSize};
    if (readBe32(signature, 0U) != CSMAGIC_EMBEDDED_SIGNATURE) {
        return fail(AuditCode::BadSuperBlobMagic,
                    "CS_SuperBlob magic 0xfade0cc0 not found");
    }
    const std::uint32_t superLength = readBe32(signature, 4U);
    if (superLength != signatureSize) {
        return fail(AuditCode::BadSuperBlobLength,
                    "SuperBlob length differs from LC_CODE_SIGNATURE size");
    }
    const std::uint32_t blobCount = readBe32(signature, 8U);
    if (blobCount == 0U) {
        return fail(AuditCode::MissingCodeDirectory,
                    "SuperBlob contains no indexed blobs");
    }
    const std::uint64_t indexBytes =
        static_cast<std::uint64_t>(blobCount) * 8U;
    if (indexBytes > signature.size - 12U) {
        return fail(AuditCode::BadBlobIndexRange,
                    "blob index array exceeds SuperBlob");
    }

    std::vector<BlobEntry> entries;
    entries.reserve(blobCount);
    std::uint32_t previousType = 0;
    for (std::uint32_t index = 0U; index < blobCount; ++index) {
        const std::size_t entryOffset = 12U + static_cast<std::size_t>(index) * 8U;
        const std::uint32_t type = readBe32(signature, entryOffset);
        const std::uint32_t offset = readBe32(signature, entryOffset + 4U);
        if (index > 0U && type <= previousType) {
            return fail(AuditCode::BadBlobIndexRange,
                        "blob index entries must be strictly ordered");
        }
        previousType = type;
        if (static_cast<std::uint64_t>(offset) + 8U > signature.size) {
            return fail(AuditCode::BadBlobIndexRange,
                        "blob offset header outside SuperBlob");
        }
        const std::uint32_t length = readBe32(signature, offset + 4U);
        if (length < 8U ||
            static_cast<std::uint64_t>(offset) + length > signature.size) {
            return fail(AuditCode::BlobOutOfBounds,
                        "blob length extends outside SuperBlob");
        }
        entries.push_back(BlobEntry{type, offset, length});
    }
    for (std::size_t outer = 0U; outer < entries.size(); ++outer) {
        for (std::size_t inner = outer + 1U; inner < entries.size(); ++inner) {
            if (rangesOverlap(entries[outer].offset, entries[outer].length,
                              entries[inner].offset, entries[inner].length)) {
                return fail(AuditCode::OverlappingBlobs,
                            "indexed blobs overlap inside SuperBlob");
            }
        }
    }

    std::size_t directoryCount = 0;
    BlobEntry codeDirectory{};
    for (const BlobEntry& entry : entries) {
        if (entry.type == CSSLOT_CODEDIRECTORY) {
            ++directoryCount;
            codeDirectory = entry;
        }
    }
    if (directoryCount == 0U) {
        return fail(AuditCode::MissingCodeDirectory,
                    "SuperBlob lacks a CodeDirectory at slot 0");
    }
    if (directoryCount > 1U) {
        return fail(AuditCode::DuplicateCodeDirectory,
                    "multiple primary CodeDirectories at slot 0");
    }
    return checkCodeDirectory(codeDirectory, entries, signatureStart, image,
                              segments, signatureStart);
}

}  // namespace

const char* auditCodeName(AuditCode code) noexcept {
    switch (code) {
        case AuditCode::Ok: return "Ok";
        case AuditCode::Truncated: return "Truncated";
        case AuditCode::BadMagic: return "BadMagic";
        case AuditCode::BadCpuType: return "BadCpuType";
        case AuditCode::UnsupportedFileType: return "UnsupportedFileType";
        case AuditCode::BadLoadCommandRange: return "BadLoadCommandRange";
        case AuditCode::BadSegmentRange: return "BadSegmentRange";
        case AuditCode::OverlappingSegments: return "OverlappingSegments";
        case AuditCode::TrailingData: return "TrailingData";
        case AuditCode::DuplicateSignatureCommand: return "DuplicateSignatureCommand";
        case AuditCode::MissingSignatureCommand: return "MissingSignatureCommand";
        case AuditCode::MissingLinkedit: return "MissingLinkedit";
        case AuditCode::SignatureNotInLinkedit: return "SignatureNotInLinkedit";
        case AuditCode::SignatureNotAtLinkeditTail: return "SignatureNotAtLinkeditTail";
        case AuditCode::SuperBlobTruncated: return "SuperBlobTruncated";
        case AuditCode::BadSuperBlobMagic: return "BadSuperBlobMagic";
        case AuditCode::BadSuperBlobLength: return "BadSuperBlobLength";
        case AuditCode::BadBlobIndexRange: return "BadBlobIndexRange";
        case AuditCode::BlobOutOfBounds: return "BlobOutOfBounds";
        case AuditCode::OverlappingBlobs: return "OverlappingBlobs";
        case AuditCode::MissingCodeDirectory: return "MissingCodeDirectory";
        case AuditCode::DuplicateCodeDirectory: return "DuplicateCodeDirectory";
        case AuditCode::BadCodeDirectoryMagic: return "BadCodeDirectoryMagic";
        case AuditCode::UnsupportedCodeDirectoryVersion: return "UnsupportedCodeDirectoryVersion";
        case AuditCode::UnsupportedHashType: return "UnsupportedHashType";
        case AuditCode::BadPageSize: return "BadPageSize";
        case AuditCode::BadHashArrayRange: return "BadHashArrayRange";
        case AuditCode::BadCodeSlotCount: return "BadCodeSlotCount";
        case AuditCode::CodeLimitOverflow: return "CodeLimitOverflow";
        case AuditCode::CodeLimitMismatch: return "CodeLimitMismatch";
        case AuditCode::ExecutableSegmentUncovered: return "ExecutableSegmentUncovered";
        case AuditCode::PageHashMismatch: return "PageHashMismatch";
        case AuditCode::MissingSpecialSlot: return "MissingSpecialSlot";
        case AuditCode::SpecialSlotMismatch: return "SpecialSlotMismatch";
        case AuditCode::BadBlobMagic: return "BadBlobMagic";
        case AuditCode::MissingHardenedRuntimeFlags: return "MissingHardenedRuntimeFlags";
        case AuditCode::DangerousEntitlement: return "DangerousEntitlement";
        case AuditCode::InternalError: return "InternalError";
    }
    return "Unknown";
}

AuditResult auditMachO(const std::vector<std::uint8_t>& data) {
    if (data.empty()) {
        return fail(AuditCode::Truncated, "empty image");
    }
    return auditMachO(data.data(), data.size());
}

AuditResult auditMachO(const std::uint8_t* data, std::size_t size) {
    if (data == nullptr || size < kMachHeader64Size) {
        return fail(AuditCode::Truncated,
                    "image smaller than mach_header_64");
    }
    const Cursor image{data, size};
    const std::uint32_t magic = readLe32(image, 0U);
    if (magic == MH_CIGAM_64) {
        return fail(AuditCode::BadMagic,
                    "big-endian 64-bit Mach-O is not supported");
    }
    if (magic != MH_MAGIC_64) {
        return fail(AuditCode::BadMagic,
                    "MH_MAGIC_64 (0xfeedfacf) not found");
    }

    const std::uint32_t cpuType = readLe32(image, 4U);
    const std::uint32_t cpuSubtype = readLe32(image, 8U);
    (void)cpuSubtype;
    if ((cpuType & CPU_ARCH_ABI64) == 0U ||
        (cpuType != CPU_TYPE_X86_64 && cpuType != CPU_TYPE_ARM64)) {
        return fail(AuditCode::BadCpuType,
                    "only x86_64 and arm64 64-bit images are supported");
    }
    const std::uint32_t fileType = readLe32(image, 12U);
    if (fileType != MH_EXECUTE && fileType != MH_DYLIB &&
        fileType != MH_BUNDLE) {
        return fail(AuditCode::UnsupportedFileType,
                    "only MH_EXECUTE, MH_DYLIB and MH_BUNDLE are audited");
    }
    const std::uint32_t commandCount = readLe32(image, 16U);
    const std::uint32_t commandSize = readLe32(image, 20U);
    if (static_cast<std::uint64_t>(kMachHeader64Size) + commandSize > size) {
        return fail(AuditCode::Truncated,
                    "load command region exceeds image");
    }

    std::vector<SegmentInfo> segments;
    bool hasLinkedit = false;
    SegmentInfo linkedit{};
    bool hasSignatureCommand = false;
    std::uint32_t signatureOffset = 0;
    std::uint32_t signatureLength = 0;

    std::size_t cursor = kMachHeader64Size;
    const std::size_t commandsEnd =
        kMachHeader64Size + static_cast<std::size_t>(commandSize);
    for (std::uint32_t index = 0U; index < commandCount; ++index) {
        if (cursor + kLoadCommandHeaderSize > commandsEnd) {
            return fail(AuditCode::BadLoadCommandRange,
                        "load command header exceeds command region");
        }
        const std::uint32_t command = readLe32(image, cursor);
        const std::uint32_t commandLength = readLe32(image, cursor + 4U);
        if (commandLength < kLoadCommandHeaderSize) {
            return fail(AuditCode::BadLoadCommandRange,
                        "load command smaller than 8 bytes");
        }
        const std::size_t next = cursor + static_cast<std::size_t>(commandLength);
        if (next > commandsEnd) {
            return fail(AuditCode::BadLoadCommandRange,
                        "load command extends beyond command region");
        }

        if (command == LC_SEGMENT_64) {
            if (commandLength < kSegmentCommand64Size) {
                return fail(AuditCode::BadSegmentRange,
                            "LC_SEGMENT_64 shorter than 72 bytes");
            }
            SegmentInfo segment{};
            std::memcpy(segment.name.data(), image.base + cursor + 8U, 16U);
            segment.vmAddr = readLe64(image, cursor + 24U);
            segment.vmSize = readLe64(image, cursor + 32U);
            segment.fileOff = readLe64(image, cursor + 40U);
            segment.fileSize = readLe64(image, cursor + 48U);
            segment.maxProt = readLe32(image, cursor + 56U);
            segment.initProt = readLe32(image, cursor + 60U);
            if (segment.fileSize > 0U) {
                const std::uint64_t end =
                    segment.fileOff + segment.fileSize;
                if (end < segment.fileOff || end > size) {
                    return fail(AuditCode::BadSegmentRange,
                                "segment file range exceeds image");
                }
            }
            if (std::strncmp(segment.name.data(), "__LINKEDIT", 11U) == 0) {
                if (hasLinkedit) {
                    return fail(AuditCode::BadSegmentRange,
                                "duplicate __LINKEDIT segment");
                }
                hasLinkedit = true;
                linkedit = segment;
            }
            segments.push_back(segment);
        } else if (command == LC_CODE_SIGNATURE) {
            if (hasSignatureCommand) {
                return fail(AuditCode::DuplicateSignatureCommand,
                            "multiple LC_CODE_SIGNATURE commands");
            }
            if (commandLength < kLinkeditCommandSize) {
                return fail(AuditCode::BadLoadCommandRange,
                            "LC_CODE_SIGNATURE shorter than 16 bytes");
            }
            signatureOffset = readLe32(image, cursor + 8U);
            signatureLength = readLe32(image, cursor + 12U);
            hasSignatureCommand = true;
        }
        cursor = next;
    }

    if (!hasSignatureCommand) {
        return fail(AuditCode::MissingSignatureCommand,
                    "LC_CODE_SIGNATURE load command absent");
    }
    if (!hasLinkedit) {
        return fail(AuditCode::MissingLinkedit,
                    "__LINKEDIT segment absent");
    }

    for (std::size_t outer = 0U; outer < segments.size(); ++outer) {
        for (std::size_t inner = outer + 1U; inner < segments.size(); ++inner) {
            if (rangesOverlap(segments[outer].fileOff,
                              segments[outer].fileSize,
                              segments[inner].fileOff,
                              segments[inner].fileSize)) {
                return fail(AuditCode::OverlappingSegments,
                            "LC_SEGMENT_64 file ranges overlap");
            }
        }
    }

    // Reject unsigned trailing bytes: every image byte must belong to a segment.
    std::uint64_t highestEnd = 0;
    for (const SegmentInfo& segment : segments) {
        const std::uint64_t end = segment.fileOff + segment.fileSize;
        if (end < segment.fileOff) {
            return fail(AuditCode::BadSegmentRange,
                        "segment range overflows 64-bit address space");
        }
        if (end > highestEnd) {
            highestEnd = end;
        }
    }
    if (highestEnd != size) {
        return fail(AuditCode::TrailingData,
                    "image contains bytes outside any segment");
    }

    const std::uint64_t linkeditEnd =
        linkedit.fileOff + linkedit.fileSize;
    const std::uint64_t signatureEnd =
        static_cast<std::uint64_t>(signatureOffset) + signatureLength;
    if (signatureEnd < signatureOffset || signatureEnd > size) {
        return fail(AuditCode::SignatureNotInLinkedit,
                    "signature range exceeds image");
    }
    if (signatureOffset < linkedit.fileOff ||
        signatureEnd > linkeditEnd) {
        return fail(AuditCode::SignatureNotInLinkedit,
                    "LC_CODE_SIGNATURE data outside __LINKEDIT");
    }
    if (signatureEnd != linkeditEnd) {
        return fail(AuditCode::SignatureNotAtLinkeditTail,
                    "signature must occupy the exact tail of __LINKEDIT");
    }

    return checkSignature(image, segments, signatureOffset, signatureLength);
}

}  // namespace machoaudit
