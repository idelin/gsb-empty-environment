#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <cstring>
#include <string>
#include <vector>

#include "machoaudit/sha256.hpp"

namespace machotest {

// Hand-built 64-bit little-endian Mach-O image. No system tooling is used:
// every header, load command, CS_SuperBlob and CS_CodeDirectory byte is
// serialized explicitly so malformed variants can be produced deterministically.

constexpr std::uint32_t MH_MAGIC_64 = 0xfeedfacfU;
constexpr std::uint32_t CPU_TYPE_ARM64 = 0x0100000cU;
constexpr std::uint32_t CPU_SUBTYPE_ARM64_ALL = 0U;
constexpr std::uint32_t MH_EXECUTE = 0x2U;
constexpr std::uint32_t MH_DYLIB = 0x6U;
constexpr std::uint32_t LC_SEGMENT_64 = 0x19U;
constexpr std::uint32_t LC_CODE_SIGNATURE = 0x1dU;

constexpr std::uint32_t VM_PROT_READ = 0x1U;
constexpr std::uint32_t VM_PROT_EXECUTE = 0x4U;
constexpr std::uint32_t VM_PROT_WRITE = 0x2U;

constexpr std::uint32_t CSMAGIC_EMBEDDED_SIGNATURE = 0xfade0cc0U;
constexpr std::uint32_t CSMAGIC_CODEDIRECTORY = 0xfade0c02U;
constexpr std::uint32_t CSMAGIC_REQUIREMENTS = 0xfade0c01U;
constexpr std::uint32_t CSMAGIC_ENTITLEMENTS = 0xfade0c05U;
constexpr std::uint32_t CSMAGIC_EMBEDDED_ENTITLEMENTS_DER = 0xfade0c07U;

constexpr std::uint32_t CS_KILL = 0x200U;
constexpr std::uint32_t CS_RUNTIME = 0x10000U;

constexpr std::size_t kPageSize = 4096U;
constexpr std::size_t kCodeLimit = 0x4800U;  // 1 full page + 1 partial page
constexpr std::size_t kTextSize = 0x4000U;
constexpr std::size_t kDataSize = 0x0800U;
constexpr std::size_t kHashSize = 32U;
constexpr std::size_t kSpecialSlots = 7U;

enum class DangerousKey {
    None,
    GetTaskAllow,
    AllowUnsignedExecutableMemory,
    DisableLibraryValidation
};

struct SampleOptions {
    bool includeRequirements = true;
    bool includeXmlEntitlements = true;
    bool includeDerEntitlements = true;
    bool hardenedRuntime = true;
    bool killFlag = true;
    DangerousKey xmlKey = DangerousKey::None;
    DangerousKey derKey = DangerousKey::None;
    bool dangerousXmlExplicitFalse = false;
    std::uint32_t fileType = MH_EXECUTE;
};

struct SampleLayout {
    std::size_t signatureOffset = 0;
    std::size_t codeDirectoryBase = 0;
    std::size_t requirementsBase = 0;
    std::size_t xmlBase = 0;
    std::size_t derBase = 0;
    std::size_t textSegmentCommand = 0;
    std::size_t dataSegmentCommand = 0;
    std::size_t linkeditSegmentCommand = 0;
    std::size_t signatureCommand = 0;
    std::size_t pageTwoData = 0x4200U;
    std::size_t codeSlotZero = 0;
    std::size_t requirementDigest = 0;
};

class ByteWriter {
public:
    explicit ByteWriter(std::vector<std::uint8_t>& buffer)
        : buffer_(buffer) {}

    void u8(std::uint8_t value) { buffer_.push_back(value); }

    void le16(std::uint16_t value) {
        buffer_.push_back(static_cast<std::uint8_t>(value & 0xffU));
        buffer_.push_back(static_cast<std::uint8_t>((value >> 8) & 0xffU));
    }

    void le32(std::uint32_t value) {
        for (unsigned byte = 0; byte < 4U; ++byte) {
            const unsigned shift = byte * 8U;
            buffer_.push_back(
                static_cast<std::uint8_t>((value >> shift) & 0xffU));
        }
    }

    void le64(std::uint64_t value) {
        for (unsigned byte = 0; byte < 8U; ++byte) {
            const unsigned shift = byte * 8U;
            buffer_.push_back(
                static_cast<std::uint8_t>((value >> shift) & 0xffU));
        }
    }

    void be32(std::uint32_t value) {
        for (unsigned byte = 0; byte < 4U; ++byte) {
            const unsigned shift = (3U - byte) * 8U;
            buffer_.push_back(
                static_cast<std::uint8_t>((value >> shift) & 0xffU));
        }
    }

    void bytes(const std::uint8_t* data, std::size_t length) {
        buffer_.insert(buffer_.end(), data, data + length);
    }

    void zeros(std::size_t count) {
        buffer_.insert(buffer_.end(), count,
                       static_cast<std::uint8_t>(0U));
    }

    void align8() {
        while (buffer_.size() % 8U != 0U) {
            buffer_.push_back(0U);
        }
    }

private:
    std::vector<std::uint8_t>& buffer_;
};

inline std::size_t alignUp8(std::size_t value) {
    return (value + 7U) & ~static_cast<std::size_t>(7U);
}

inline std::vector<std::uint8_t> makeBlob(
    std::uint32_t magic, const std::vector<std::uint8_t>& payload) {
    std::vector<std::uint8_t> blob;
    ByteWriter writer(blob);
    writer.be32(magic);
    writer.be32(static_cast<std::uint32_t>(payload.size() + 8U));
    writer.bytes(payload.data(), payload.size());
    return blob;
}

inline std::vector<std::uint8_t> requirementsBlob() {
    std::vector<std::uint8_t> payload(16U, 0U);
    return makeBlob(CSMAGIC_REQUIREMENTS, payload);
}

inline std::vector<std::uint8_t> xmlEntitlementsBlob(
    const SampleOptions& options) {
    const char* dangerousName = "";
    switch (options.xmlKey) {
        case DangerousKey::GetTaskAllow:
            dangerousName = "com.apple.security.get-task-allow";
            break;
        case DangerousKey::AllowUnsignedExecutableMemory:
            dangerousName =
                "com.apple.security.cs.allow-unsigned-executable-memory";
            break;
        case DangerousKey::DisableLibraryValidation:
            dangerousName =
                "com.apple.security.cs.disable-library-validation";
            break;
        case DangerousKey::None:
            break;
    }
    const bool dangerous = options.xmlKey != DangerousKey::None;
    const char* key = dangerous ? dangerousName
                                : "com.apple.security.app-sandbox";
    const char* value = dangerous ? "<true/>" : "<false/>";
    if (options.dangerousXmlExplicitFalse) {
        key = "com.apple.security.cs.disable-library-validation";
        value = "<false/>";
    }
    std::string plist;
    plist += "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n";
    plist += "<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" "
             "\"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n";
    plist += "<plist version=\"1.0\"><dict><key>";
    plist += key;
    plist += "</key>";
    plist += value;
    plist += "</dict></plist>";
    std::vector<std::uint8_t> payload(plist.begin(), plist.end());
    return makeBlob(CSMAGIC_ENTITLEMENTS, payload);
}

inline std::vector<std::uint8_t> derEntitlementsBlob(
    const SampleOptions& options) {
    std::string key = "com.apple.security.network.client";
    switch (options.derKey) {
        case DangerousKey::GetTaskAllow:
            key = "com.apple.security.get-task-allow";
            break;
        case DangerousKey::AllowUnsignedExecutableMemory:
            key =
                "com.apple.security.cs.allow-unsigned-executable-memory";
            break;
        case DangerousKey::DisableLibraryValidation:
            key = "com.apple.security.cs.disable-library-validation";
            break;
        case DangerousKey::None:
            break;
    }
    std::vector<std::uint8_t> content;
    content.push_back(0x0cU);
    content.push_back(static_cast<std::uint8_t>(key.size()));
    content.insert(content.end(), key.begin(), key.end());
    content.push_back(0x01U);
    content.push_back(0x01U);
    content.push_back(0x01U);
    std::vector<std::uint8_t> payload;
    payload.push_back(0x30U);
    payload.push_back(static_cast<std::uint8_t>(content.size()));
    payload.insert(payload.end(), content.begin(), content.end());
    return makeBlob(CSMAGIC_EMBEDDED_ENTITLEMENTS_DER, payload);
}

struct BuiltSample {
    std::vector<std::uint8_t> image;
    SampleLayout layout;
    std::size_t signatureSize = 0;
};

inline BuiltSample buildSample(const SampleOptions& options = SampleOptions{}) {
    BuiltSample sample;
    SampleLayout& layout = sample.layout;

    std::vector<std::uint8_t> requirements = requirementsBlob();
    std::vector<std::uint8_t> xml = xmlEntitlementsBlob(options);
    std::vector<std::uint8_t> der = derEntitlementsBlob(options);

    const std::size_t codeSlots =
        (kCodeLimit + kPageSize - 1U) / kPageSize;
    const std::size_t identifierLength = 2U;
    const std::size_t codeDirectoryLength = alignUp8(
        44U + (kSpecialSlots + codeSlots) * kHashSize + identifierLength);

    struct SignaturePiece {
        std::uint32_t type;
        const std::vector<std::uint8_t>* blob;
    };
    std::vector<SignaturePiece> pieces;
    pieces.push_back({0U, nullptr});
    if (options.includeRequirements) {
        pieces.push_back({2U, &requirements});
    }
    if (options.includeXmlEntitlements) {
        pieces.push_back({5U, &xml});
    }
    if (options.includeDerEntitlements) {
        pieces.push_back({7U, &der});
    }

    std::size_t blobBytes = codeDirectoryLength;
    if (options.includeRequirements) {
        blobBytes += requirements.size();
    }
    if (options.includeXmlEntitlements) {
        blobBytes += xml.size();
    }
    if (options.includeDerEntitlements) {
        blobBytes += der.size();
    }
    const std::size_t superBlobLength =
        12U + pieces.size() * 8U + blobBytes;
    sample.signatureSize = superBlobLength;

    std::vector<std::uint8_t>& image = sample.image;
    ByteWriter writer(image);

    writer.le32(MH_MAGIC_64);
    writer.le32(CPU_TYPE_ARM64);
    writer.le32(CPU_SUBTYPE_ARM64_ALL);
    writer.le32(options.fileType);
    writer.le32(4U);
    writer.le32(3U * 72U + 16U);
    writer.le32(0U);
    writer.le32(0U);

    auto writeSegment = [&](const char* name, std::uint64_t vmAddr,
                            std::uint64_t vmSize, std::uint64_t fileOff,
                            std::uint64_t fileSize, std::uint32_t maxProt,
                            std::uint32_t initProt) -> std::size_t {
        const std::size_t commandStart = image.size();
        writer.le32(LC_SEGMENT_64);
        writer.le32(72U);
        std::array<char, 16U> nameBuffer{};
        std::strncpy(nameBuffer.data(), name, nameBuffer.size() - 1U);
        writer.bytes(
            reinterpret_cast<const std::uint8_t*>(nameBuffer.data()),
            nameBuffer.size());
        writer.le64(vmAddr);
        writer.le64(vmSize);
        writer.le64(fileOff);
        writer.le64(fileSize);
        writer.le32(maxProt);
        writer.le32(initProt);
        writer.le32(0U);
        writer.le32(0U);
        return commandStart;
    };

    layout.textSegmentCommand = writeSegment(
        "__TEXT", 0x100000000ULL, kTextSize, 0U, kTextSize,
        VM_PROT_READ | VM_PROT_EXECUTE, VM_PROT_READ | VM_PROT_EXECUTE);
    layout.dataSegmentCommand = writeSegment(
        "__DATA", 0x100004000ULL, kPageSize, kTextSize, kDataSize,
        VM_PROT_READ | VM_PROT_WRITE, VM_PROT_READ | VM_PROT_WRITE);
    layout.linkeditSegmentCommand = writeSegment(
        "__LINKEDIT", 0x100005000ULL, superBlobLength, kCodeLimit,
        superBlobLength, VM_PROT_READ, VM_PROT_READ);

    layout.signatureCommand = image.size();
    writer.le32(LC_CODE_SIGNATURE);
    writer.le32(16U);
    writer.le32(static_cast<std::uint32_t>(kCodeLimit));
    writer.le32(static_cast<std::uint32_t>(superBlobLength));

    const std::size_t headerBytes = image.size();
    image.resize(kCodeLimit);
    for (std::size_t offset = headerBytes; offset < kCodeLimit; ++offset) {
        image[offset] =
            static_cast<std::uint8_t>((offset * 31U + 7U) & 0xffU);
    }

    layout.signatureOffset = image.size();
    const std::size_t signatureBase = image.size();
    const std::size_t indexBase = 12U + pieces.size() * 8U;

    std::size_t cursor = indexBase;
    const std::size_t codeDirectoryOffset = cursor;
    cursor += codeDirectoryLength;
    std::size_t requirementsOffset = 0;
    std::size_t xmlOffset = 0;
    std::size_t derOffset = 0;
    if (options.includeRequirements) {
        requirementsOffset = cursor;
        cursor += requirements.size();
    }
    if (options.includeXmlEntitlements) {
        xmlOffset = cursor;
        cursor += xml.size();
    }
    if (options.includeDerEntitlements) {
        derOffset = cursor;
        cursor += der.size();
    }

    // CS_SuperBlob header and big-endian blob index.
    writer.be32(CSMAGIC_EMBEDDED_SIGNATURE);
    writer.be32(static_cast<std::uint32_t>(superBlobLength));
    writer.be32(static_cast<std::uint32_t>(pieces.size()));
    for (const SignaturePiece& piece : pieces) {
        std::size_t pieceOffset = 0;
        if (piece.type == 0U) {
            pieceOffset = codeDirectoryOffset;
        } else if (piece.type == 2U) {
            pieceOffset = requirementsOffset;
        } else if (piece.type == 5U) {
            pieceOffset = xmlOffset;
        } else {
            pieceOffset = derOffset;
        }
        writer.be32(piece.type);
        writer.be32(static_cast<std::uint32_t>(pieceOffset));
    }

    // CS_CodeDirectory with recomputed page and special-slot digests.
    layout.codeDirectoryBase = signatureBase + codeDirectoryOffset;
    const std::size_t hashOffset = 44U;
    const std::size_t codeSlotBase =
        hashOffset + kSpecialSlots * kHashSize;
    layout.codeSlotZero = layout.codeDirectoryBase + codeSlotBase;
    const std::size_t identOffset =
        hashOffset + (kSpecialSlots + codeSlots) * kHashSize;

    std::vector<machoaudit::Sha256::Digest> special(kSpecialSlots);
    auto specialHash = [&](std::uint32_t slot,
                           const std::vector<std::uint8_t>& blob) {
        special[slot - 1U] = machoaudit::Sha256::hashBytes(blob);
    };
    if (options.includeRequirements) {
        specialHash(2U, requirements);
    }
    if (options.includeXmlEntitlements) {
        specialHash(5U, xml);
    }
    if (options.includeDerEntitlements) {
        specialHash(7U, der);
    }
    layout.requirementDigest =
        layout.codeDirectoryBase + hashOffset +
        (2U - 1U) * kHashSize;

    std::vector<machoaudit::Sha256::Digest> pages(codeSlots);
    for (std::size_t slot = 0U; slot < codeSlots; ++slot) {
        const std::size_t start = slot * kPageSize;
        const std::size_t length =
            (kCodeLimit - start) < kPageSize ? (kCodeLimit - start)
                                             : kPageSize;
        pages[slot] = machoaudit::Sha256::hash(image.data() + start, length);
    }

    std::uint32_t flags = 0U;
    if (options.killFlag) {
        flags |= CS_KILL;
    }
    if (options.hardenedRuntime) {
        flags |= CS_RUNTIME;
    }

    writer.be32(CSMAGIC_CODEDIRECTORY);
    writer.be32(static_cast<std::uint32_t>(codeDirectoryLength));
    writer.be32(0x20001U);
    writer.be32(flags);
    writer.be32(static_cast<std::uint32_t>(hashOffset));
    writer.be32(0U);  // identOffset placeholder, fixed below
    writer.be32(static_cast<std::uint32_t>(kSpecialSlots));
    writer.be32(static_cast<std::uint32_t>(codeSlots));
    writer.be32(static_cast<std::uint32_t>(kCodeLimit));
    writer.u8(static_cast<std::uint8_t>(kHashSize));  // hashSize (uint8)
    writer.u8(2U);                                     // hashType SHA-256
    writer.u8(0U);                                     // platform
    writer.u8(12U);                                    // pageSize = 2^12
    writer.be32(0U);                                   // spare2

    for (const machoaudit::Sha256::Digest& digest : special) {
        writer.bytes(digest.data(), digest.size());
    }
    for (const machoaudit::Sha256::Digest& digest : pages) {
        writer.bytes(digest.data(), digest.size());
    }
    image[layout.codeDirectoryBase + 20U] =
        static_cast<std::uint8_t>((identOffset >> 24) & 0xffU);
    image[layout.codeDirectoryBase + 21U] =
        static_cast<std::uint8_t>((identOffset >> 16) & 0xffU);
    image[layout.codeDirectoryBase + 22U] =
        static_cast<std::uint8_t>((identOffset >> 8) & 0xffU);
    image[layout.codeDirectoryBase + 23U] =
        static_cast<std::uint8_t>(identOffset & 0xffU);
    writer.u8(static_cast<std::uint8_t>('a'));
    writer.u8(0U);
    const std::size_t currentDirectoryLength =
        image.size() - layout.codeDirectoryBase;
    writer.zeros(codeDirectoryLength - currentDirectoryLength);

    if (options.includeRequirements) {
        layout.requirementsBase = signatureBase + requirementsOffset;
        writer.bytes(requirements.data(), requirements.size());
    }
    if (options.includeXmlEntitlements) {
        layout.xmlBase = signatureBase + xmlOffset;
        writer.bytes(xml.data(), xml.size());
    }
    if (options.includeDerEntitlements) {
        layout.derBase = signatureBase + derOffset;
        writer.bytes(der.data(), der.size());
    }

    return sample;
}

}  // namespace machotest
