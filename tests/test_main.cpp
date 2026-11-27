#include <cstdint>
#include <cstring>
#include <iostream>
#include <string>
#include <vector>

#include "machoaudit/audit.hpp"
#include "machoaudit/sha256.hpp"
#include "macho_builder.hpp"

namespace {

int g_failures = 0;
int g_checks = 0;

std::string toHex(const std::uint8_t* data, std::size_t size) {
    static const char digits[] = "0123456789abcdef";
    std::string result;
    result.reserve(size * 2U);
    for (std::size_t index = 0; index < size; ++index) {
        result.push_back(digits[(data[index] >> 4) & 0xfU]);
        result.push_back(digits[data[index] & 0xfU]);
    }
    return result;
}

void check(bool condition, const std::string& name,
           const std::string& extra = "") {
    ++g_checks;
    if (!condition) {
        ++g_failures;
        std::cout << "[FAIL] " << name;
        if (!extra.empty()) {
            std::cout << " - " << extra;
        }
        std::cout << '\n';
    }
}

using machoaudit::AuditCode;
using machoaudit::AuditResult;
using machoaudit::Sha256;
using machotest::SampleOptions;
using machotest::buildSample;

void expectOk(const std::vector<std::uint8_t>& image,
              const std::string& name) {
    const AuditResult result = machoaudit::auditMachO(image);
    check(result.ok(), name,
          std::string("unexpected rejection: ") +
              machoaudit::auditCodeName(result.code) + " " + result.detail);
}

void expectCode(const std::vector<std::uint8_t>& image,
                AuditCode expected, const std::string& name) {
    const AuditResult result = machoaudit::auditMachO(image);
    check(result.code == expected, name,
          std::string("got ") + machoaudit::auditCodeName(result.code) +
              " (" + result.detail + "), expected " +
              machoaudit::auditCodeName(expected));
}

void writeLe32(std::vector<std::uint8_t>& image, std::size_t offset,
               std::uint32_t value) {
    for (unsigned shift = 0; shift < 32U; shift += 8U) {
        image[offset + shift / 8U] =
            static_cast<std::uint8_t>((value >> shift) & 0xffU);
    }
}

std::uint32_t readLe32(const std::vector<std::uint8_t>& image,
                       std::size_t offset) {
    std::uint32_t value = 0;
    for (unsigned shift = 0; shift < 32U; shift += 8U) {
        value |= static_cast<std::uint32_t>(image[offset + shift / 8U])
                 << shift;
    }
    return value;
}

void writeBe32(std::vector<std::uint8_t>& image, std::size_t offset,
               std::uint32_t value) {
    for (int shift = 24; shift >= 0; shift -= 8) {
        image[offset + static_cast<std::size_t>((24 - shift) / 8)] =
            static_cast<std::uint8_t>((value >> shift) & 0xffU);
    }
}

void testSha256KnownAnswers() {
    const auto digestOf = [](const std::string& text) {
        return toHex(Sha256::hash(
                         reinterpret_cast<const std::uint8_t*>(text.data()),
                         text.size())
                         .data(),
                     32U);
    };
    check(digestOf("") ==
              "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
          "sha256 empty string KAT");
    check(digestOf("abc") ==
              "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
          "sha256 abc KAT");
    check(digestOf("abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq") ==
              "248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1",
          "sha256 56-byte KAT");

    const std::string million(1000000U, 'a');
    check(digestOf(million) ==
              "cdc76e5c9914fb9281a1c7e284d73e67f1809a48a497200e046d39ccc7112cd0",
          "sha256 one-million-a KAT");

    Sha256 streaming;
    const std::string chunk =
        "abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq";
    for (std::size_t index = 0; index < chunk.size(); ++index) {
        streaming.update(
            reinterpret_cast<const std::uint8_t*>(&chunk[index]), 1U);
    }
    check(toHex(streaming.finalize().data(), 32U) ==
              "248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1",
          "sha256 byte-at-a-time streaming");
}

void testValidSamples() {
    expectOk(buildSample().image, "valid MH_EXECUTE sample");

    SampleOptions dylib;
    dylib.fileType = machotest::MH_DYLIB;
    expectOk(buildSample(dylib).image, "valid MH_DYLIB sample");

    SampleOptions noEntitlements;
    noEntitlements.includeXmlEntitlements = false;
    noEntitlements.includeDerEntitlements = false;
    noEntitlements.includeRequirements = false;
    expectOk(buildSample(noEntitlements).image,
             "valid sample without optional blobs");
}

void testPageTampering() {
    machotest::BuiltSample sample = buildSample();
    sample.image[sample.layout.pageTwoData] ^= 0x80U;
    expectCode(sample.image, AuditCode::PageHashMismatch,
               "page tamper detected via recomputed hash");

    machotest::BuiltSample firstPage = buildSample();
    firstPage.image[0x1000U - 1U] ^= 0x01U;
    expectCode(firstPage.image, AuditCode::PageHashMismatch,
               "first page tamper detected");
}

void testSpecialSlotMismatch() {
    machotest::BuiltSample sample = buildSample();
    sample.image[sample.layout.requirementDigest] ^= 0xffU;
    expectCode(sample.image, AuditCode::SpecialSlotMismatch,
               "requirements special slot digest mismatch");

    machotest::BuiltSample blobMutated = buildSample();
    blobMutated.image[blobMutated.layout.requirementsBase + 12U] ^= 0x01U;
    expectCode(blobMutated.image, AuditCode::SpecialSlotMismatch,
               "requirements blob byte tamper detected");

    machotest::BuiltSample xmlMutated = buildSample();
    const std::size_t xmlPayload = xmlMutated.layout.xmlBase + 8U;
    xmlMutated.image[xmlPayload] ^= 0x01U;
    expectCode(xmlMutated.image, AuditCode::SpecialSlotMismatch,
               "XML entitlement blob tamper detected");

    machotest::BuiltSample derMutated = buildSample();
    derMutated.image[derMutated.layout.derBase + 12U] ^= 0x01U;
    expectCode(derMutated.image, AuditCode::SpecialSlotMismatch,
               "DER entitlement blob tamper detected");
}

void testHardenedRuntimeFlags() {
    SampleOptions noRuntime;
    noRuntime.hardenedRuntime = false;
    expectCode(buildSample(noRuntime).image,
               AuditCode::MissingHardenedRuntimeFlags,
               "missing CS_RUNTIME rejected");

    SampleOptions noKill;
    noKill.killFlag = false;
    expectCode(buildSample(noKill).image,
               AuditCode::MissingHardenedRuntimeFlags,
               "missing CS_KILL rejected");
}

void testDangerousEntitlements() {
    const machotest::DangerousKey keys[] = {
        machotest::DangerousKey::GetTaskAllow,
        machotest::DangerousKey::AllowUnsignedExecutableMemory,
        machotest::DangerousKey::DisableLibraryValidation};
    const char* keyNames[] = {"get-task-allow",
                              "allow-unsigned-executable-memory",
                              "disable-library-validation"};
    for (std::size_t index = 0; index < 3U; ++index) {
        SampleOptions xml;
        xml.xmlKey = keys[index];
        expectCode(buildSample(xml).image,
                   AuditCode::DangerousEntitlement,
                   std::string("XML ") + keyNames[index] + " rejected");

        SampleOptions der;
        der.derKey = keys[index];
        expectCode(buildSample(der).image,
                   AuditCode::DangerousEntitlement,
                   std::string("DER ") + keyNames[index] + " rejected");
    }

    SampleOptions explicitFalse;
    explicitFalse.dangerousXmlExplicitFalse = true;
    expectOk(buildSample(explicitFalse).image,
             "dangerous key paired with explicit false is allowed");
}

void testCodeLimitAndCoverage() {
    machotest::BuiltSample shortened = buildSample();
    const std::size_t cd = shortened.layout.codeDirectoryBase;
    writeBe32(shortened.image, cd + 32U,
              static_cast<std::uint32_t>(machotest::kCodeLimit - 1U));
    expectCode(shortened.image, AuditCode::CodeLimitMismatch,
               "codeLimit not ending at signature rejected");

    machotest::BuiltSample slotCount = buildSample();
    writeBe32(slotCount.image,
              slotCount.layout.codeDirectoryBase + 28U, 1U);
    expectCode(slotCount.image, AuditCode::BadCodeSlotCount,
               "forged nCodeSlots rejected");

    machotest::BuiltSample pageSize = buildSample();
    pageSize.image[pageSize.layout.codeDirectoryBase + 39U] = 14U;
    expectCode(pageSize.image, AuditCode::BadCodeSlotCount,
               "forged page size exponent rejected");

    machotest::BuiltSample tailPoison = buildSample();
    tailPoison.image.push_back(0xccU);
    writeLe32(tailPoison.image,
              tailPoison.layout.linkeditSegmentCommand + 48U,
              readLe32(tailPoison.image,
                       tailPoison.layout.linkeditSegmentCommand + 48U) +
                  1U);
    expectCode(tailPoison.image, AuditCode::SignatureNotAtLinkeditTail,
               "trailing poison outside signature rejected");

    machotest::BuiltSample uncovered = buildSample();
    writeLe32(uncovered.image,
              uncovered.layout.textSegmentCommand + 48U,
              static_cast<std::uint32_t>(machotest::kTextSize + 1U));
    expectCode(uncovered.image, AuditCode::OverlappingSegments,
               "text segment grown into data segment rejected");
}

void testStructuralMalformations() {
    expectCode(std::vector<std::uint8_t>{}, AuditCode::Truncated,
               "empty image rejected");
    const std::vector<std::uint8_t> garbage(64U, 0x41U);
    expectCode(garbage, AuditCode::BadMagic, "garbage magic rejected");

    machotest::BuiltSample truncated = buildSample();
    truncated.image.resize(16U);
    expectCode(truncated.image, AuditCode::Truncated,
               "truncated header rejected");

    machotest::BuiltSample badSuperMagic = buildSample();
    writeBe32(badSuperMagic.image, badSuperMagic.layout.signatureOffset,
              0xdeadbeefU);
    expectCode(badSuperMagic.image, AuditCode::BadSuperBlobMagic,
               "bad SuperBlob magic rejected");

    machotest::BuiltSample badLength = buildSample();
    writeBe32(badLength.image, badLength.layout.signatureOffset + 4U,
              static_cast<std::uint32_t>(badLength.signatureSize - 4U));
    expectCode(badLength.image, AuditCode::BadSuperBlobLength,
               "SuperBlob length mismatch rejected");

    machotest::BuiltSample badCdMagic = buildSample();
    writeBe32(badCdMagic.image, badCdMagic.layout.codeDirectoryBase,
              0xfade0c99U);
    expectCode(badCdMagic.image, AuditCode::BadCodeDirectoryMagic,
               "bad CodeDirectory magic rejected");

    machotest::BuiltSample overlapping = buildSample();
    writeLe32(overlapping.image,
              overlapping.layout.dataSegmentCommand + 40U, 0x3000U);
    expectCode(overlapping.image, AuditCode::OverlappingSegments,
               "overlapping segments rejected");

    machotest::BuiltSample segmentOob = buildSample();
    writeLe32(segmentOob.image,
              segmentOob.layout.dataSegmentCommand + 48U, 0xfffff000U);
    expectCode(segmentOob.image, AuditCode::BadSegmentRange,
               "segment out-of-bounds rejected");

    machotest::BuiltSample signatureOob = buildSample();
    writeLe32(signatureOob.image,
              signatureOob.layout.signatureCommand + 8U, 0xfffff000U);
    expectCode(signatureOob.image, AuditCode::SignatureNotInLinkedit,
               "signature offset outside linkedit rejected");

    machotest::BuiltSample notTail = buildSample();
    writeLe32(notTail.image,
              notTail.layout.linkeditSegmentCommand + 48U,
              static_cast<std::uint32_t>(notTail.signatureSize + 8U));
    writeLe32(notTail.image,
              notTail.layout.linkeditSegmentCommand + 32U,
              static_cast<std::uint32_t>(notTail.signatureSize + 8U));
    notTail.image.resize(notTail.image.size() + 8U);
    expectCode(notTail.image, AuditCode::SignatureNotAtLinkeditTail,
               "signature not at linkedit tail rejected");

    machotest::BuiltSample missingSig = buildSample();
    writeLe32(missingSig.image, 16U, 3U);
    expectCode(missingSig.image, AuditCode::MissingSignatureCommand,
               "missing LC_CODE_SIGNATURE rejected");

    machotest::BuiltSample badCommandRange = buildSample();
    writeLe32(badCommandRange.image, 20U, 0xffffff00U);
    expectCode(badCommandRange.image, AuditCode::Truncated,
               "integer-overflow command size rejected");

    machotest::BuiltSample badCpu = buildSample();
    writeLe32(badCpu.image, 4U, 12U);  // 32-bit ARM cputype without ABI64
    expectCode(badCpu.image, AuditCode::BadCpuType,
               "non-64-bit cputype rejected");

    machotest::BuiltSample badType = buildSample();
    writeLe32(badType.image, 12U, 11U);  // MH_DSYM
    expectCode(badType.image, AuditCode::UnsupportedFileType,
               "unsupported file type rejected");

    machotest::BuiltSample badHashType = buildSample();
    badHashType.image[badHashType.layout.codeDirectoryBase + 37U] = 1U;
    expectCode(badHashType.image, AuditCode::UnsupportedHashType,
               "non-SHA256 hash type rejected");

    machotest::BuiltSample badHashSize = buildSample();
    badHashType.image[badHashType.layout.codeDirectoryBase + 36U] = 20U;
    expectCode(badHashType.image, AuditCode::UnsupportedHashType,
               "unsupported hash size rejected");

    machotest::BuiltSample badBlobIndex = buildSample();
    writeBe32(badBlobIndex.image,
              badBlobIndex.layout.signatureOffset + 12U + 4U,
              0xfffff000U);
    expectCode(badBlobIndex.image, AuditCode::BadBlobIndexRange,
               "blob index offset out of bounds rejected");

    machotest::BuiltSample badBlobLength = buildSample();
    writeBe32(badBlobLength.image,
              badBlobLength.layout.codeDirectoryBase + 4U,
              0xfffff000U);
    expectCode(badBlobLength.image, AuditCode::BlobOutOfBounds,
               "declared blob length overflow rejected");

    machotest::BuiltSample hugePageSize = buildSample();
    hugePageSize.image[hugePageSize.layout.codeDirectoryBase + 39U] = 30U;
    expectCode(hugePageSize.image, AuditCode::BadPageSize,
               "page size exponent overflow rejected");

    machotest::BuiltSample zeroPageSize = buildSample();
    zeroPageSize.image[zeroPageSize.layout.codeDirectoryBase + 39U] = 0U;
    expectCode(zeroPageSize.image, AuditCode::BadPageSize,
               "page size exponent zero rejected");

    machotest::BuiltSample badCommandLength = buildSample();
    writeLe32(badCommandLength.image, 32U + 4U, 4U);
    expectCode(badCommandLength.image, AuditCode::BadLoadCommandRange,
               "undersized load command rejected");
}

}  // namespace

int main() {
    testSha256KnownAnswers();
    testValidSamples();
    testPageTampering();
    testSpecialSlotMismatch();
    testHardenedRuntimeFlags();
    testDangerousEntitlements();
    testCodeLimitAndCoverage();
    testStructuralMalformations();
    if (g_failures != 0) {
        std::cout << g_failures << " of " << g_checks << " checks failed\n";
        return 1;
    }
    std::cout << "all " << g_checks << " checks passed\n";
    return 0;
}
