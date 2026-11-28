#include "ntfs_audit/ntfs_audit.h"

#include <cstdio>
#include <string>
#include <vector>

#include "mft_builder.h"

namespace {

using namespace ntfs_audit;
using namespace mft_test;

int g_failures = 0;
int g_checks = 0;

#define CHECK(cond)                                                     \
    do {                                                                \
        ++g_checks;                                                     \
        if (!(cond)) {                                                  \
            ++g_failures;                                               \
            std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond); \
        }                                                               \
    } while (0)

MftBuilder baseValidRecord() {
    MftBuilder builder;
    builder.appendResident(kAttrStandardInformation,
                           standardInformationContent());
    builder.appendResident(kAttrFileName, fileNameContent());
    const std::vector<uint8_t> payload = {'h', 'e', 'l', 'l', 'o'};
    builder.appendResident(kAttrData, payload);
    builder.appendEndMarker();
    builder.applyFixup();
    return builder;
}

void testValidRecord() {
    MftBuilder builder = baseValidRecord();
    AuditOptions options;
    options.volumeClusterCount = 1000;
    const AuditResult result = AuditRecord(builder.bytes(), options);
    CHECK(result.ok());
    CHECK(result.findings.empty());
    CHECK(result.standardInformation.has_value());
    CHECK(result.standardInformation->fileAttributes == 0x20U);
    CHECK(result.fileNames.size() == 1U);
    CHECK(result.fileNames[0].name == "report.txt");
    CHECK(result.dataStreams.size() == 1U);
    CHECK(!result.dataStreams[0].named);
    CHECK(result.dataStreams[0].resident);
    CHECK(result.dataStreams[0].logicalSize == 5U);
}

void testCustomRecordSize2048() {
    MftBuilder builder(2048);
    builder.appendResident(kAttrStandardInformation,
                           standardInformationContent());
    builder.appendResident(kAttrFileName, fileNameContent());
    builder.appendEndMarker();
    builder.applyFixup(0x5151);
    AuditOptions options;
    options.expectedRecordSize = 2048;
    const AuditResult result = AuditRecord(builder.bytes(), options);
    CHECK(result.ok());
}

void testInvalidMagic() {
    MftBuilder builder = baseValidRecord();
    builder.bytes()[0] = 'X';
    const AuditResult result = AuditRecord(builder.bytes());
    CHECK(!result.ok());
    CHECK(result.has(FindingCode::kInvalidMagic));
}

void testRecordSizeMismatch() {
    MftBuilder builder = baseValidRecord();
    std::vector<uint8_t> truncated(builder.bytes().begin(),
                                   builder.bytes().end() - 1U);
    const AuditResult result = AuditRecord(truncated);
    CHECK(!result.ok());
    CHECK(result.has(FindingCode::kRecordSizeMismatch));
}

void testFixupTampered() {
    MftBuilder builder = baseValidRecord();
    // Corrupt the sequence stored at the first sector end without updating
    // the USA. The fixup consistency check must reject the record.
    builder.at(510) = static_cast<uint8_t>(builder.at(510) ^ 0x01U);
    const AuditResult result = AuditRecord(builder.bytes());
    CHECK(!result.ok());
    CHECK(result.has(FindingCode::kFixupMismatch));
}

void testFixupRestoresSectorEndBytes() {
    MftBuilder builder = baseValidRecord();
    // Place distinctive payload bytes at the first sector boundary before
    // applying the fixup; the USA must retain the original payload bytes.
    builder.at(510) = 0x7E;
    builder.at(511) = 0x3C;
    builder.applyFixup(0x4848);
    CHECK(builder.at(510) == 0x48);
    const AuditResult result = AuditRecord(builder.bytes());
    CHECK(result.ok());
    CHECK(builder.at(44) == 0x7E);
    CHECK(builder.at(45) == 0x3C);
}

void testUsaMalformed() {
    MftBuilder builder = baseValidRecord();
    putU16(builder.bytes(), 0x06, 99);  // impossible USA count
    const AuditResult result = AuditRecord(builder.bytes());
    CHECK(!result.ok());
    CHECK(result.has(FindingCode::kUsaMalformed));
}

void testHiddenAlternateDataStream() {
    MftBuilder builder;
    builder.appendResident(kAttrStandardInformation,
                           standardInformationContent());
    const std::vector<uint8_t> secret = {0xDE, 0xAD, 0xBE, 0xEF};
    builder.appendResident(kAttrData, secret, utf16(u"secret"));
    builder.appendEndMarker();
    builder.applyFixup();

    const AuditResult warningResult = AuditRecord(builder.bytes());
    CHECK(warningResult.ok());  // ADS defaults to a warning
    CHECK(warningResult.has(FindingCode::kHiddenAlternateDataStream));
    CHECK(warningResult.dataStreams.size() == 1U);
    CHECK(warningResult.dataStreams[0].named);
    CHECK(warningResult.dataStreams[0].name == "secret");

    AuditOptions strict;
    strict.treatAdsAsError = true;
    const AuditResult strictResult = AuditRecord(builder.bytes(), strict);
    CHECK(!strictResult.ok());
    CHECK(strictResult.has(FindingCode::kHiddenAlternateDataStream));
}

void testOverlongStreamName() {
    MftBuilder builder(2048);
    builder.appendResident(kAttrStandardInformation,
                           standardInformationContent());
    const std::u16string longName(200U, u'A');
    const std::vector<uint8_t> payload(8U, 0);
    builder.appendResident(kAttrData, payload, utf16(longName));
    builder.appendEndMarker();
    builder.applyFixup();
    AuditOptions options;
    options.expectedRecordSize = 2048;
    options.maxStreamNameChars = 64;
    const AuditResult result = AuditRecord(builder.bytes(), options);
    CHECK(!result.ok());
    CHECK(result.has(FindingCode::kStreamNameTooLong));
}

void testNonResidentRunsAndOverlap() {
    MftBuilder clean;
    clean.appendResident(kAttrStandardInformation,
                         standardInformationContent());
    // Two distinct, non-overlapping runs: clusters 100..103 and 200..201.
    const std::vector<uint8_t> runs = encodeRuns({{4, 100}, {2, 200}});
    clean.appendNonResident(kAttrData, runs, 4096);
    clean.appendEndMarker();
    clean.applyFixup();

    AuditOptions options;
    options.volumeClusterCount = 1000;
    const AuditResult cleanResult = AuditRecord(clean.bytes(), options);
    CHECK(cleanResult.ok());
    CHECK(cleanResult.dataStreams.size() == 1U);
    CHECK(cleanResult.dataStreams[0].runs.size() == 2U);
    CHECK(cleanResult.dataStreams[0].runs[1].lcn == 200);
    CHECK(!cleanResult.dataStreams[0].resident);

    // Two separate streams whose physical ranges intersect: the second run
    // lands at cluster 102, overlapping 100..103.
    MftBuilder overlap;
    overlap.appendResident(kAttrStandardInformation,
                           standardInformationContent());
    overlap.appendNonResident(kAttrData, encodeRuns({{4, 100}}), 4096);
    overlap.appendNonResident(kAttrData, encodeRuns({{4, 102}}), 4096,
                              utf16(u"evil"));
    overlap.appendEndMarker();
    overlap.applyFixup();
    const AuditResult overlapResult =
        AuditRecord(overlap.bytes(), options);
    CHECK(!overlapResult.ok());
    CHECK(overlapResult.has(FindingCode::kDataRunOverlap));
}

void testNegativeLcn() {
    MftBuilder builder;
    builder.appendResident(kAttrStandardInformation,
                           standardInformationContent());
    // First run lands at LCN 100; the second delta subtracts 200 so the
    // signed difference decoding resolves to a negative LCN.
    const std::vector<uint8_t> runs =
        encodeRunsFromDeltas({{2, 100}, {2, -200}});
    builder.appendNonResident(kAttrData, runs, 4096);
    builder.appendEndMarker();
    builder.applyFixup();
    AuditOptions options;
    options.volumeClusterCount = 1000;
    const AuditResult result = AuditRecord(builder.bytes(), options);
    CHECK(!result.ok());
    CHECK(result.has(FindingCode::kDataRunNegativeLcn));
}

void testRunOutsideVolume() {
    MftBuilder builder;
    builder.appendResident(kAttrStandardInformation,
                           standardInformationContent());
    const std::vector<uint8_t> runs = encodeRuns({{8, 996}});
    builder.appendNonResident(kAttrData, runs, 8192);
    builder.appendEndMarker();
    builder.applyFixup();
    AuditOptions options;
    options.volumeClusterCount = 1000;  // 996..1003 exceeds the volume
    const AuditResult result = AuditRecord(builder.bytes(), options);
    CHECK(!result.ok());
    CHECK(result.has(FindingCode::kDataRunOutsideVolume));
}

void testTruncatedRunList() {
    MftBuilder builder;
    builder.appendResident(kAttrStandardInformation,
                           standardInformationContent());
    const std::vector<uint8_t> runs = {0x11, 0x01};
    const size_t attrStart =
        MftBuilder::kFirstAttrOffset + align8(24U + 48U);
    builder.appendNonResident(kAttrData, runs, 4096);
    builder.appendEndMarker();
    builder.applyFixup();
    const uint32_t originalLength =
        static_cast<uint32_t>(builder.bytes()[attrStart + 4]) |
        (static_cast<uint32_t>(builder.bytes()[attrStart + 5]) << 8) |
        (static_cast<uint32_t>(builder.bytes()[attrStart + 6]) << 16) |
        (static_cast<uint32_t>(builder.bytes()[attrStart + 7]) << 24);
    // Shrink the declared length so the run-list terminator disappears.
    putU32(builder.bytes(), attrStart + 4, originalLength - 1U);
    AuditOptions options;
    options.volumeClusterCount = 1000;
    const AuditResult result = AuditRecord(builder.bytes(), options);
    CHECK(!result.ok());
}

void testZeroLengthAttribute() {
    MftBuilder builder = baseValidRecord();
    const size_t firstAttr = MftBuilder::kFirstAttrOffset;
    putU32(builder.bytes(), firstAttr + 4, 0);
    const AuditResult result = AuditRecord(builder.bytes());
    CHECK(!result.ok());
    CHECK(result.has(FindingCode::kAttributeZeroLength));
}

void testMisalignedAttribute() {
    MftBuilder builder = baseValidRecord();
    putU16(builder.bytes(), 0x14, 58);  // not 8-byte aligned
    const AuditResult result = AuditRecord(builder.bytes());
    CHECK(!result.ok());
    CHECK(result.has(FindingCode::kAttributeOffsetOutOfBounds));
}

void testMaliciousMountPoint() {
    MftBuilder builder;
    builder.appendResident(kAttrStandardInformation,
                           standardInformationContent());
    const std::vector<uint8_t> reparse = reparseContent(
        kReparseTagMountPoint, u"\\??\\C:\\Windows\\System32",
        u"C:\\Windows\\System32");
    builder.appendResident(kAttrReparsePoint, reparse);
    builder.appendEndMarker();
    builder.applyFixup();
    const AuditResult result = AuditRecord(builder.bytes());
    CHECK(!result.ok());
    CHECK(result.reparsePoint.has_value());
    CHECK(result.reparsePoint->mountPoint);
    CHECK(result.has(FindingCode::kReparseUnsafeDevicePrefix));
    CHECK(result.has(FindingCode::kReparseUnsafeDriveAbsolute));
}

void testUncAndTraversalSymlink() {
    MftBuilder builder;
    builder.appendResident(kAttrStandardInformation,
                           standardInformationContent());
    const std::vector<uint8_t> reparse = reparseContent(
        kReparseTagSymlink, u"\\\\attacker.example\\share\\..\\secret",
        u"..\\secret");
    builder.appendResident(kAttrReparsePoint, reparse);
    builder.appendEndMarker();
    builder.applyFixup();
    const AuditResult result = AuditRecord(builder.bytes());
    CHECK(!result.ok());
    CHECK(result.reparsePoint->symlink);
    CHECK(result.has(FindingCode::kReparseUnsafeUnc));
    CHECK(result.has(FindingCode::kReparseUnsafeTraversal));
}

void testBenignRelativeSymlink() {
    MftBuilder builder;
    builder.appendResident(kAttrStandardInformation,
                           standardInformationContent());
    const std::vector<uint8_t> reparse =
        reparseContent(kReparseTagSymlink, u"subdir\\data", u"data");
    builder.appendResident(kAttrReparsePoint, reparse);
    builder.appendEndMarker();
    builder.applyFixup();
    const AuditResult result = AuditRecord(builder.bytes());
    CHECK(result.ok());
    CHECK(result.reparsePoint.has_value());
    CHECK(result.reparsePoint->violations.empty());
    CHECK(result.reparsePoint->substituteName == "subdir\\data");
}

void testTruncatedReparse() {
    MftBuilder builder;
    builder.appendResident(kAttrStandardInformation,
                           standardInformationContent());
    std::vector<uint8_t> bad =
        reparseContent(kReparseTagSymlink, u"target", u"target");
    // Lie about the data length so name offsets run past the content.
    putU32(bad, 0, static_cast<uint32_t>(bad.size() + 64U));
    builder.appendResident(kAttrReparsePoint, bad);
    builder.appendEndMarker();
    builder.applyFixup();
    const AuditResult result = AuditRecord(builder.bytes());
    CHECK(!result.ok());
    CHECK(result.has(FindingCode::kReparseTruncated) ||
          result.has(FindingCode::kReparseBadOffset));
}

void testMalformedRunHeader() {
    MftBuilder builder;
    builder.appendResident(kAttrStandardInformation,
                           standardInformationContent());
    // 0x09 means a 9-byte cluster-count field, which NTFS forbids.
    const std::vector<uint8_t> runs = {0x09, 0x01};
    builder.appendNonResident(kAttrData, runs, 4096);
    builder.appendEndMarker();
    builder.applyFixup();
    const AuditResult result = AuditRecord(builder.bytes());
    CHECK(!result.ok());
    CHECK(result.has(FindingCode::kDataRunMalformed));
}

void testNamedNonResidentAds() {
    MftBuilder builder;
    builder.appendResident(kAttrStandardInformation,
                           standardInformationContent());
    builder.appendNonResident(kAttrData, encodeRuns({{3, 500}}), 3072,
                              utf16(u"payload"));
    builder.appendEndMarker();
    builder.applyFixup();
    AuditOptions options;
    options.volumeClusterCount = 1000;
    const AuditResult result = AuditRecord(builder.bytes(), options);
    CHECK(result.ok());  // ADS remains a warning, runs are valid
    CHECK(result.has(FindingCode::kHiddenAlternateDataStream));
    CHECK(result.dataStreams.size() == 1U);
    CHECK(result.dataStreams[0].named);
    CHECK(result.dataStreams[0].name == "payload");
    CHECK(!result.dataStreams[0].resident);
    CHECK(result.dataStreams[0].runs[0].lcn == 500);
}

void testResidentContentOutOfBounds() {
    MftBuilder builder = baseValidRecord();
    const size_t firstAttr = MftBuilder::kFirstAttrOffset;
    // Inflate the resident content size so it runs past the attribute end.
    putU32(builder.bytes(), firstAttr + 16, 0xFFFFU);
    const AuditResult result = AuditRecord(builder.bytes());
    CHECK(!result.ok());
    CHECK(result.has(FindingCode::kResidentContentOutOfBounds));
}

void testGarbageNeverCrashes() {
    // Deterministic pseudo-random garbage and every truncation length must
    // be handled without memory errors or exceptions.
    uint32_t state = 0x12345678U;
    auto nextByte = [&state]() {
        state ^= state << 13;
        state ^= state >> 17;
        state ^= state << 5;
        return static_cast<uint8_t>(state);
    };
    std::vector<uint8_t> garbage(1024U);
    for (uint8_t& byte : garbage) {
        byte = nextByte();
    }
    for (size_t len = 1; len <= 1024U; len += 7) {
        std::vector<uint8_t> slice(
            garbage.begin(),
            garbage.begin() + static_cast<std::ptrdiff_t>(len));
        const AuditResult result = AuditRecord(slice);
        CHECK(!result.ok());
    }
    // A zero-length and null buffer call must be rejected, not crash.
    const AuditResult empty = AuditRecord(garbage.data(), 0);
    CHECK(!empty.ok());
    const AuditResult nullResult = AuditRecord(nullptr, 1024);
    CHECK(!nullResult.ok());
}

}  // namespace

int main() {
    testValidRecord();
    testCustomRecordSize2048();
    testInvalidMagic();
    testRecordSizeMismatch();
    testFixupTampered();
    testFixupRestoresSectorEndBytes();
    testUsaMalformed();
    testHiddenAlternateDataStream();
    testOverlongStreamName();
    testNonResidentRunsAndOverlap();
    testNegativeLcn();
    testRunOutsideVolume();
    testTruncatedRunList();
    testZeroLengthAttribute();
    testMisalignedAttribute();
    testMaliciousMountPoint();
    testUncAndTraversalSymlink();
    testBenignRelativeSymlink();
    testTruncatedReparse();
    testMalformedRunHeader();
    testNamedNonResidentAds();
    testResidentContentOutOfBounds();
    testGarbageNeverCrashes();
    std::printf("%d checks, %d failures\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}
