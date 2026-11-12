#pragma once

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

namespace netsec {
namespace ndef {

// Well-known NFC Forum RTD type names (UTF-8, single byte).
inline constexpr char kTypeUri = 'U';
inline constexpr const char* kTypeSmartPoster = "Sp";

// NDEF Type Name Format values (NDEF spec 2.3).
enum class Tnf : uint8_t {
    kEmpty = 0x00,
    kWellKnown = 0x01,
    kMediaType = 0x02,
    kAbsoluteUri = 0x03,
    kExternal = 0x04,
    kUnknown = 0x05,
    kUnchanged = 0x06,
    kReserved = 0x07,
};

// Every failure mode the parser can report. Parsing is fail-closed: any
// structural inconsistency aborts the whole message and returns no records.
enum class ParseError {
    kNone = 0,
    kEmptyBuffer,
    kTruncatedHeader,
    kTruncatedType,
    kTruncatedId,
    kTruncatedPayload,
    kTruncatedLength,
    kFirstRecordMissingMb,
    kMbAfterFirstRecord,
    kMessageMissingMe,
    kChunkedRecordHasSr,
    kOrphanChunkContinuation,
    kChunkContinuationHasType,
    kChunkContinuationHasId,
    kReservedTnf,
    kEmptyTnfWithContent,
    kLengthOverflow,
    kTrailingDataAfterMe,
    kUriPrefixCodeUnknown,
    kUriRejectedByWhitelist,
    kUriMalformed,
    kSmartPosterDepthExceeded,
    kSmartPosterMalformed,
};

const char* toString(ParseError error) noexcept;

struct Record {
    Tnf tnf = Tnf::kEmpty;
    std::vector<uint8_t> type;
    std::vector<uint8_t> id;
    // For chunked records this is the concatenation of every chunk payload.
    std::vector<uint8_t> payload;
    bool chunked = false;

    bool isWellKnownType(char name) const noexcept;
    bool isWellKnownType(const char* name) const noexcept;
    bool isUriRecord() const noexcept { return isWellKnownType(kTypeUri); }
    bool isSmartPoster() const noexcept {
        return isWellKnownType(kTypeSmartPoster);
    }
};

struct ParseOptions {
    // Schemes (lower case, without trailing ':') that URI records are allowed
    // to use after abbreviation prefix expansion, e.g. {"https", "mailto"}.
    // When empty, every URI record is rejected (fail-closed default).
    std::vector<std::string> allowedSchemes;
    // Maximum nesting depth of Smart Poster records. Top level is depth 0.
    unsigned maxSmartPosterDepth = 8;
    // NDEF messages on a tag must end exactly at the buffer end. Allow extra
    // bytes only when the caller guarantees the buffer contains more than one
    // message; the number of consumed bytes is still reported separately.
    bool allowTrailingData = false;
};

struct ParseResult {
    bool ok() const noexcept { return error == ParseError::kNone; }
    explicit operator bool() const noexcept { return ok(); }

    ParseError error = ParseError::kNone;
    std::vector<Record> records;
    // Bytes consumed by the NDEF message (only meaningful on success).
    std::size_t consumedBytes = 0;
};

// Parses one NDEF message from [data, data+size). Never throws and never
// dereferences outside the buffer. URI policy and Smart Poster depth are
// enforced during parsing, so every returned URI has already passed the
// caller-supplied whitelist.
ParseResult parseMessage(const uint8_t* data,
                         std::size_t size,
                         const ParseOptions& options) noexcept;

ParseResult parseMessage(const std::vector<uint8_t>& data,
                         const ParseOptions& options) noexcept;

// Expands a URI RTD payload (identifier code byte + URI field) into the full
// URI string. Returns false on an unknown identifier code or malformed field.
bool expandUriPayload(const uint8_t* payload,
                      std::size_t payloadSize,
                      std::string& outUri);

bool expandUriPayload(const std::vector<uint8_t>& payload,
                      std::string& outUri);

// Extracts and expands the URI of a URI RTD record. False for any other
// record type or malformed payload.
bool recordUri(const Record& record, std::string& outUri);

// Lower-cased RFC 3986 scheme of uri ("https" for "HTTPS://x"), or empty when
// the string does not begin with a valid scheme.
std::string uriScheme(const std::string& uri);

}  // namespace ndef
}  // namespace netsec
