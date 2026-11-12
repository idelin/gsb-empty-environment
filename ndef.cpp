#include "ndef.hpp"

#include <algorithm>
#include <cctype>
#include <cstring>

namespace netsec {
namespace ndef {

namespace {

// Header bit masks (NFC Forum NDEF spec, record header byte 1).
constexpr uint8_t kFlagMb = 0x80;
constexpr uint8_t kFlagMe = 0x40;
constexpr uint8_t kFlagCf = 0x20;
constexpr uint8_t kFlagSr = 0x10;
constexpr uint8_t kFlagIl = 0x08;
constexpr uint8_t kTnfMask = 0x07;

// Abbreviation prefixes for URI RTD, indexed by identifier code byte.
// Index 30 is "file://", the local-file attack payload.
const char* const kUriPrefixes[] = {
    "",                                   // 0  N/A
    "http://www.",                        // 1
    "https://www.",                       // 2
    "http://",                            // 3
    "https://",                           // 4
    "tel:",                               // 5
    "mailto:",                            // 6
    "ftp://anonymous:anonymous@",         // 7
    "ftp://ftp.",                         // 8
    "ftps://",                            // 9
    "sftp://",                            // 10
    "smb://",                             // 11
    "nfs://",                             // 12
    "ftp://",                             // 13
    "dav://",                             // 14
    "news:",                              // 15
    "telnet://",                          // 16
    "imap:",                              // 17
    "rtsp://",                            // 18
    "urn:",                               // 19
    "pop:",                               // 20
    "sip:",                               // 21
    "sips:",                              // 22
    "tftp:",                              // 23
    "btspp://",                           // 24
    "btl2cap://",                         // 25
    "tcp://",                             // 26
    "btgoep://",                          // 27
    "tcpobex://",                         // 28
    "irdaobex://",                        // 29
    "file://",                            // 30
    "urn:epc:id:",                        // 31
    "urn:epc:tag:",                       // 32
    "urn:epc:pat:",                       // 33
    "urn:epc:raw:",                       // 34
    "urn:epc:",                           // 35
    "urn:nfc:",                           // 36
};
constexpr size_t kUriPrefixCount =
    sizeof(kUriPrefixes) / sizeof(kUriPrefixes[0]);

// Schemes that must never be dispatched from a tag, even if a caller
// misconfigures the whitelist.
const char* const kHardDeniedSchemes[] = {"file"};

// Bounds-checked view over the caller's tag buffer; every read fails closed.
class Cursor {
public:
    Cursor(const uint8_t* data, std::size_t size) noexcept
        : ptr_(data), end_(data + size) {}

    std::size_t remaining() const noexcept {
        return static_cast<std::size_t>(end_ - ptr_);
    }

    bool readU8(uint8_t& value) noexcept {
        if (remaining() < 1) {
            return false;
        }
        value = *ptr_++;
        return true;
    }

    bool readU32(uint32_t& value) noexcept {
        if (remaining() < 4) {
            return false;
        }
        value = (static_cast<uint32_t>(ptr_[0]) << 24) |
                (static_cast<uint32_t>(ptr_[1]) << 16) |
                (static_cast<uint32_t>(ptr_[2]) << 8) |
                static_cast<uint32_t>(ptr_[3]);
        ptr_ += 4;
        return true;
    }

    bool readBytes(std::size_t length, std::vector<uint8_t>& out) {
        if (remaining() < length) {
            return false;
        }
        out.assign(ptr_, ptr_ + length);
        ptr_ += length;
        return true;
    }

    std::size_t position(const uint8_t* start) const noexcept {
        return static_cast<std::size_t>(ptr_ - start);
    }

private:
    const uint8_t* ptr_;
    const uint8_t* end_;
};

bool isAlpha(char c) noexcept {
    return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z');
}

bool isSchemeChar(char c) noexcept {
    return isAlpha(c) || (c >= '0' && c <= '9') || c == '+' || c == '-' ||
           c == '.';
}

bool isControlOrSpace(unsigned char c) noexcept {
    return c <= 0x20 || c == 0x7F;
}

bool inHardDenyList(const std::string& scheme) noexcept {
    for (const char* denied : kHardDeniedSchemes) {
        if (scheme == denied) {
            return true;
        }
    }
    return false;
}

// Validates an expanded URI against the caller whitelist. The scheme is the
// RFC 3986 token before the first ':' and is compared case-insensitively.
ParseError checkUri(const std::string& uri,
                    const ParseOptions& options) noexcept {
    if (uri.empty()) {
        return ParseError::kUriMalformed;
    }
    for (unsigned char c : uri) {
        if (isControlOrSpace(c)) {
            return ParseError::kUriMalformed;
        }
    }

    const std::string scheme = uriScheme(uri);
    if (scheme.empty()) {
        return ParseError::kUriMalformed;
    }
    if (inHardDenyList(scheme)) {
        return ParseError::kUriRejectedByWhitelist;
    }
    for (const std::string& allowed : options.allowedSchemes) {
        if (scheme == allowed) {
            return ParseError::kNone;
        }
    }
    return ParseError::kUriRejectedByWhitelist;
}

ParseError parseImpl(const uint8_t* data,
                     std::size_t size,
                     const ParseOptions& options,
                     unsigned depth,
                     ParseResult& result) noexcept {
    Cursor cursor(data, size);
    bool sawRecord = false;
    bool sawMe = false;
    bool inChunk = false;

    while (cursor.remaining() > 0) {
        uint8_t header = 0;
        if (!cursor.readU8(header)) {
            return ParseError::kTruncatedHeader;
        }

        const bool mb = (header & kFlagMb) != 0;
        const bool me = (header & kFlagMe) != 0;
        const bool cf = (header & kFlagCf) != 0;
        const bool sr = (header & kFlagSr) != 0;
        const bool il = (header & kFlagIl) != 0;
        const Tnf tnf = static_cast<Tnf>(header & kTnfMask);

        // MB/ME positional self-consistency.
        if (!sawRecord && !mb) {
            return ParseError::kFirstRecordMissingMb;
        }
        if (sawRecord && mb) {
            return ParseError::kMbAfterFirstRecord;
        }
        if (me && cf) {
            return ParseError::kMessageMissingMe;
        }

        uint8_t typeLen = 0;
        if (!cursor.readU8(typeLen)) {
            return ParseError::kTruncatedHeader;
        }
        uint32_t payloadLen = 0;
        if (sr) {
            // SR=1: payload length occupies exactly one byte.
            uint8_t shortLen = 0;
            if (!cursor.readU8(shortLen)) {
                return ParseError::kTruncatedLength;
            }
            payloadLen = shortLen;
        } else {
            if (!cursor.readU32(payloadLen)) {
                return ParseError::kTruncatedLength;
            }
        }
        uint8_t idLen = 0;
        if (il) {
            if (!cursor.readU8(idLen)) {
                return ParseError::kTruncatedHeader;
            }
        }

        const bool isContinuation = (tnf == Tnf::kUnchanged);

        // Chunking self-consistency.
        if (isContinuation) {
            if (!inChunk) {
                return ParseError::kOrphanChunkContinuation;
            }
            if (typeLen != 0) {
                return ParseError::kChunkContinuationHasType;
            }
            if (il || idLen != 0) {
                return ParseError::kChunkContinuationHasId;
            }
        }
        if (cf && sr) {
            // Every chunk of a chunked record uses the 4-byte length field.
            return ParseError::kChunkedRecordHasSr;
        }
        if (tnf == Tnf::kReserved) {
            return ParseError::kReservedTnf;
        }

        // Empty (TNF 0) records may carry no content at all.
        if (tnf == Tnf::kEmpty) {
            if (cf || typeLen != 0 || idLen != 0 || payloadLen != 0) {
                return ParseError::kEmptyTnfWithContent;
            }
        }

        // All remaining fields must fit together inside the buffer. uint64
        // arithmetic makes a crafted 4 GB length overflow detectable.
        const uint64_t needed =
            static_cast<uint64_t>(typeLen) + idLen + payloadLen;
        if (needed > cursor.remaining()) {
            if (cursor.remaining() < typeLen) {
                return ParseError::kTruncatedType;
            }
            if (cursor.remaining() - typeLen < idLen) {
                return ParseError::kTruncatedId;
            }
            // Declared payload runs past the buffer. A payload longer than the
            // whole platform buffer is reported as an explicit overflow.
            if (static_cast<uint64_t>(payloadLen) >
                static_cast<uint64_t>(size)) {
                return ParseError::kLengthOverflow;
            }
            return ParseError::kTruncatedPayload;
        }

        std::vector<uint8_t> type;
        if (!cursor.readBytes(typeLen, type)) {
            return ParseError::kTruncatedType;
        }
        std::vector<uint8_t> id;
        if (il && !cursor.readBytes(idLen, id)) {
            return ParseError::kTruncatedId;
        }
        std::vector<uint8_t> payload;
        if (!cursor.readBytes(payloadLen, payload)) {
            return ParseError::kTruncatedPayload;
        }

        if (isContinuation) {
            Record& first = result.records.back();
            first.chunked = true;
            first.payload.insert(first.payload.end(), payload.begin(),
                                 payload.end());
        } else {
            Record record;
            record.tnf = tnf;
            record.type = std::move(type);
            record.id = std::move(id);
            record.payload = std::move(payload);
            result.records.push_back(std::move(record));
        }

        sawRecord = true;
        // A continuation (TNF unchanged) keeps the message inside the same
        // chunked record even when its own CF bit is 0; the chunk ends only
        // when a non-continuation record appears.
        inChunk = cf || isContinuation;

        // Policy checks run once a record is fully assembled. For a chunked
        // record that moment is the final continuation chunk (CF=0, still
        // TNF unchanged); intermediate chunks must skip the checks.
        if (!inChunk) {
            Record& current = result.records.back();

            // Expand the URI identifier-code prefix, then enforce the
            // caller's scheme whitelist before the record reaches application
            // code. The same check applies inside Smart Posters recursively.
            if (current.isUriRecord()) {
                std::string uri;
                if (!expandUriPayload(current.payload, uri)) {
                    return ParseError::kUriPrefixCodeUnknown;
                }
                const ParseError uriError = checkUri(uri, options);
                if (uriError != ParseError::kNone) {
                    return uriError;
                }
            }

            // Smart Poster payload is itself an NDEF message; it must obey
            // the same bounds, URI policy and the nesting limit.
            if (current.isSmartPoster()) {
                const unsigned nextDepth = depth + 1;
                if (nextDepth > options.maxSmartPosterDepth) {
                    return ParseError::kSmartPosterDepthExceeded;
                }
                ParseResult inner;
                const ParseError innerError =
                    parseImpl(current.payload.data(), current.payload.size(),
                              options, nextDepth, inner);
                if (innerError == ParseError::kEmptyBuffer) {
                    return ParseError::kSmartPosterMalformed;
                }
                if (innerError != ParseError::kNone) {
                    // Security policy failures (URI whitelist, malformed URI)
                    // keep their precise error; structural inner problems are
                    // reported as malformed Smart Poster content.
                    if (innerError == ParseError::kSmartPosterDepthExceeded ||
                        innerError == ParseError::kUriRejectedByWhitelist ||
                        innerError == ParseError::kUriMalformed ||
                        innerError == ParseError::kUriPrefixCodeUnknown) {
                        return innerError;
                    }
                    return ParseError::kSmartPosterMalformed;
                }
                if (inner.consumedBytes != current.payload.size()) {
                    return ParseError::kSmartPosterMalformed;
                }
            }
        }

        if (me) {
            sawMe = true;
            result.consumedBytes = cursor.position(data);
            if (!options.allowTrailingData && cursor.remaining() != 0) {
                return ParseError::kTrailingDataAfterMe;
            }
            break;
        }
    }

    if (!sawRecord) {
        return ParseError::kEmptyBuffer;
    }
    if (!sawMe) {
        // Message ran to the buffer edge without a terminating ME.
        return ParseError::kMessageMissingMe;
    }
    return ParseError::kNone;
}

}  // namespace

std::string uriScheme(const std::string& uri) {
    // RFC 3986: scheme = ALPHA *( ALPHA / DIGIT / "+" / "-" / "." )
    if (uri.empty() || !isAlpha(uri[0])) {
        return {};
    }
    std::size_t i = 1;
    while (i < uri.size() && uri[i] != ':') {
        if (!isSchemeChar(uri[i])) {
            return {};
        }
        ++i;
    }
    if (i >= uri.size()) {
        return {};  // no ':' terminator
    }
    std::string scheme = uri.substr(0, i);
    std::transform(scheme.begin(), scheme.end(), scheme.begin(),
                   [](unsigned char c) {
                       return static_cast<char>(std::tolower(c));
                   });
    return scheme;
}

bool expandUriPayload(const uint8_t* payload,
                      std::size_t payloadSize,
                      std::string& outUri) {
    if (payload == nullptr || payloadSize < 1) {
        return false;
    }
    const uint8_t code = payload[0];
    if (code >= kUriPrefixCount) {
        return false;  // reserved/unknown identifier code
    }
    outUri.clear();
    outUri.append(kUriPrefixes[code]);
    outUri.append(reinterpret_cast<const char*>(payload + 1),
                  payloadSize - 1);
    return true;
}

bool expandUriPayload(const std::vector<uint8_t>& payload,
                      std::string& outUri) {
    return expandUriPayload(payload.data(), payload.size(), outUri);
}

bool recordUri(const Record& record, std::string& outUri) {
    if (!record.isUriRecord()) {
        return false;
    }
    return expandUriPayload(record.payload, outUri);
}

bool Record::isWellKnownType(char name) const noexcept {
    return tnf == Tnf::kWellKnown && type.size() == 1 &&
           type[0] == static_cast<uint8_t>(name);
}

bool Record::isWellKnownType(const char* name) const noexcept {
    if (tnf != Tnf::kWellKnown || name == nullptr) {
        return false;
    }
    std::size_t nameLen = 0;
    while (name[nameLen] != '\0') {
        ++nameLen;
    }
    return type.size() == nameLen &&
           std::memcmp(type.data(), name, nameLen) == 0;
}

ParseResult parseMessage(const uint8_t* data,
                         std::size_t size,
                         const ParseOptions& options) noexcept {
    ParseResult result;
    if (data == nullptr || size == 0) {
        result.error = ParseError::kEmptyBuffer;
        return result;
    }
    result.error = parseImpl(data, size, options, /*depth=*/0, result);
    if (result.error != ParseError::kNone) {
        result.records.clear();
        result.consumedBytes = 0;
    }
    return result;
}

ParseResult parseMessage(const std::vector<uint8_t>& data,
                         const ParseOptions& options) noexcept {
    return parseMessage(data.data(), data.size(), options);
}

const char* toString(ParseError error) noexcept {
    switch (error) {
        case ParseError::kNone:
            return "ok";
        case ParseError::kEmptyBuffer:
            return "empty buffer";
        case ParseError::kTruncatedHeader:
            return "truncated record header";
        case ParseError::kTruncatedType:
            return "type field exceeds buffer";
        case ParseError::kTruncatedId:
            return "id field exceeds buffer";
        case ParseError::kTruncatedPayload:
            return "payload length exceeds buffer";
        case ParseError::kTruncatedLength:
            return "truncated length field";
        case ParseError::kFirstRecordMissingMb:
            return "first record missing MB";
        case ParseError::kMbAfterFirstRecord:
            return "MB set on non-first record";
        case ParseError::kMessageMissingMe:
            return "message ends without ME";
        case ParseError::kChunkedRecordHasSr:
            return "chunked record must not use SR";
        case ParseError::kOrphanChunkContinuation:
            return "chunk continuation without a started record";
        case ParseError::kChunkContinuationHasType:
            return "chunk continuation must not carry a type";
        case ParseError::kChunkContinuationHasId:
            return "chunk continuation must not carry an id";
        case ParseError::kReservedTnf:
            return "reserved TNF value 7";
        case ParseError::kEmptyTnfWithContent:
            return "empty TNF record carries content";
        case ParseError::kLengthOverflow:
            return "declared length overflows the buffer";
        case ParseError::kTrailingDataAfterMe:
            return "trailing bytes after ME record";
        case ParseError::kUriPrefixCodeUnknown:
            return "unknown URI identifier code";
        case ParseError::kUriRejectedByWhitelist:
            return "URI scheme not in whitelist";
        case ParseError::kUriMalformed:
            return "malformed URI";
        case ParseError::kSmartPosterDepthExceeded:
            return "Smart Poster nesting depth exceeded";
        case ParseError::kSmartPosterMalformed:
            return "malformed Smart Poster content";
    }
    return "unknown parse error";
}

}  // namespace ndef
}  // namespace netsec
