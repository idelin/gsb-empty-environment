#include "ndef.hpp"

#include <cstdio>
#include <string>

using namespace netsec::ndef;

namespace {

int g_failures = 0;

void check(bool condition, const std::string& label) {
    if (condition) {
        std::printf("[ PASS ] %s\n", label.c_str());
    } else {
        std::printf("[ FAIL ] %s\n", label.c_str());
        ++g_failures;
    }
}

void expectError(const std::vector<uint8_t>& msg,
                 ParseError expected,
                 const ParseOptions& opts,
                 const std::string& label) {
    const ParseResult result = parseMessage(msg, opts);
    check(!result.ok() && result.error == expected,
          label + " -> " + toString(expected));
}

// Appends one record with every flag/field under caller control so malformed
// messages can be crafted byte by byte.
void appendRecord(std::vector<uint8_t>& out,
                  uint8_t flags,
                  uint8_t tnf,
                  const std::vector<uint8_t>& type,
                  const std::vector<uint8_t>& id,
                  const std::vector<uint8_t>& payload) {
    // Keep every flag bit (MB/ME/CF/SR/IL); TNF occupies the low nibble.
    out.push_back(static_cast<uint8_t>((flags & 0xF8) | (tnf & 0x07)));
    out.push_back(static_cast<uint8_t>(type.size()));
    const bool sr = (flags & 0x10) != 0;
    if (sr) {
        out.push_back(static_cast<uint8_t>(payload.size()));
    } else {
        const uint32_t len = static_cast<uint32_t>(payload.size());
        out.push_back(static_cast<uint8_t>((len >> 24) & 0xFF));
        out.push_back(static_cast<uint8_t>((len >> 16) & 0xFF));
        out.push_back(static_cast<uint8_t>((len >> 8) & 0xFF));
        out.push_back(static_cast<uint8_t>(len & 0xFF));
    }
    if (flags & 0x08) {
        out.push_back(static_cast<uint8_t>(id.size()));
    }
    out.insert(out.end(), type.begin(), type.end());
    if (flags & 0x08) {
        out.insert(out.end(), id.begin(), id.end());
    }
    out.insert(out.end(), payload.begin(), payload.end());
}

std::vector<uint8_t> uriPayload(uint8_t code, const std::string& tail) {
    std::vector<uint8_t> payload{code};
    payload.insert(payload.end(), tail.begin(), tail.end());
    return payload;
}

ParseOptions httpsOnly() {
    ParseOptions opts;
    opts.allowedSchemes = {"https"};
    return opts;
}

}  // namespace

int main() {
    constexpr uint8_t MB = 0x80;
    constexpr uint8_t ME = 0x40;
    constexpr uint8_t CF = 0x20;
    constexpr uint8_t SR = 0x10;
    constexpr uint8_t IL = 0x08;
    const std::vector<uint8_t> kTypeU{'U'};
    const std::vector<uint8_t> kTypeSp{'S', 'p'};  // RTD "Sp"

    // 1. Valid short URI record (https) parses and expands the prefix.
    {
        std::vector<uint8_t> msg;
        appendRecord(msg, MB | ME | SR,
                     static_cast<uint8_t>(Tnf::kWellKnown), kTypeU, {},
                     uriPayload(4, "example.com/a"));
        const ParseResult r = parseMessage(msg, httpsOnly());
        check(r.ok() && r.records.size() == 1, "valid https URI parses");
        std::string uri;
        check(r.ok() && recordUri(r.records[0], uri) &&
                  uri == "https://example.com/a",
              "URI prefix code 4 expands to https://");
    }

    // 2. Missing ME: message ends without a terminating record.
    {
        std::vector<uint8_t> msg;
        appendRecord(msg, MB | SR, static_cast<uint8_t>(Tnf::kWellKnown),
                     kTypeU, {}, uriPayload(4, "example.com"));
        expectError(msg, ParseError::kMessageMissingMe, httpsOnly(),
                    "missing ME rejected");
    }

    // 3. Payload length exceeds the whole buffer (declared 16 in an 8-byte
    //    message): cannot be a valid offset, reported as a length overflow.
    {
        std::vector<uint8_t> msg{
            static_cast<uint8_t>(MB | ME | SR |
                                 static_cast<uint8_t>(Tnf::kWellKnown)),
            0x01,  // type length
            0x10,  // SR payload length = 16
            'U', 'a', 'b', 'c', 'd'};
        expectError(msg, ParseError::kLengthOverflow, httpsOnly(),
                    "SR payload length beyond whole buffer rejected");
    }

    // 3b. Declared payload overruns the remaining bytes but stays within the
    //     overall message buffer -> truncated payload, not an overflow.
    {
        std::vector<uint8_t> msg;
        appendRecord(msg, MB | SR, static_cast<uint8_t>(Tnf::kWellKnown),
                     kTypeU, {}, uriPayload(4, "example.com"));
        appendRecord(msg, ME | SR, static_cast<uint8_t>(Tnf::kWellKnown),
                     kTypeU, {}, uriPayload(4, "example.org"));
        msg.pop_back();  // remove the last payload byte of record 2
        expectError(msg, ParseError::kTruncatedPayload, httpsOnly(),
                    "payload overrunning remaining buffer rejected");
    }

    // 4. 4-byte length claiming 0xFFFFFFFF cannot overflow past the buffer.
    {
        std::vector<uint8_t> msg{
            static_cast<uint8_t>(MB | ME |
                                 static_cast<uint8_t>(Tnf::kWellKnown)),
            0x01,
            0xFF, 0xFF, 0xFF, 0xFF,
            'U'};
        expectError(msg, ParseError::kLengthOverflow, httpsOnly(),
                    "huge 4-byte length rejected without overflow");
    }

    // 5. file:// via identifier code 30; the hard deny list wins even if the
    //    caller accidentally whitelists "file".
    {
        std::vector<uint8_t> msg;
        appendRecord(msg, MB | ME | SR,
                     static_cast<uint8_t>(Tnf::kWellKnown), kTypeU, {},
                     uriPayload(30, "etc/passwd"));
        ParseOptions opts;
        opts.allowedSchemes = {"https", "file"};
        expectError(msg, ParseError::kUriRejectedByWhitelist, opts,
                    "file:// prefix code 30 rejected");
    }

    // 6. Raw "file://" smuggled in the URI field (code 0) is rejected too.
    {
        std::vector<uint8_t> msg;
        appendRecord(msg, MB | ME | SR,
                     static_cast<uint8_t>(Tnf::kWellKnown), kTypeU, {},
                     uriPayload(0, "file:///etc/passwd"));
        expectError(msg, ParseError::kUriRejectedByWhitelist, httpsOnly(),
                    "raw file:// in URI field rejected");
    }

    // 7. tel: dialer induction is blocked by the https-only whitelist.
    {
        std::vector<uint8_t> msg;
        appendRecord(msg, MB | ME | SR,
                     static_cast<uint8_t>(Tnf::kWellKnown), kTypeU, {},
                     uriPayload(5, "+10000000000"));
        expectError(msg, ParseError::kUriRejectedByWhitelist, httpsOnly(),
                    "tel: dialer URI rejected by whitelist");
    }

    // 8. Chunked record: start + unchanged continuations reassemble payload.
    {
        std::vector<uint8_t> msg;
        appendRecord(msg, MB | CF, static_cast<uint8_t>(Tnf::kWellKnown),
                     kTypeU, {}, uriPayload(4, "example."));
        // Continuation chunks keep SR=0 and use the 4-byte length field.
        appendRecord(msg, 0, static_cast<uint8_t>(Tnf::kUnchanged), {}, {},
                     std::vector<uint8_t>{'c', 'o', 'm'});
        // Even the final chunk keeps SR=0 per the NDEF chunking rules.
        appendRecord(msg, ME, static_cast<uint8_t>(Tnf::kUnchanged), {}, {},
                     std::vector<uint8_t>{'/', 'x'});
        const ParseResult r = parseMessage(msg, httpsOnly());
        check(r.ok() && r.records.size() == 1 && r.records[0].chunked,
              "valid chunked record reassembles to one record");
        std::string uri;
        check(r.ok() && recordUri(r.records[0], uri) &&
                  uri == "https://example.com/x",
              "reassembled chunk URI expands correctly");
    }

    // 9. A chunk that sets SR is inconsistent and rejected.
    {
        std::vector<uint8_t> msg;
        appendRecord(msg, MB | CF | SR,
                     static_cast<uint8_t>(Tnf::kWellKnown), kTypeU, {},
                     uriPayload(4, "example."));
        expectError(msg, ParseError::kChunkedRecordHasSr, httpsOnly(),
                    "chunked record with SR rejected");
    }

    // 10. Orphan continuation (TNF unchanged without a chunk start).
    {
        std::vector<uint8_t> msg;
        appendRecord(msg, MB | ME | SR,
                     static_cast<uint8_t>(Tnf::kUnchanged), {}, {},
                     std::vector<uint8_t>{'x'});
        expectError(msg, ParseError::kOrphanChunkContinuation, httpsOnly(),
                    "orphan chunk continuation rejected");
    }

    // 11. Continuation chunk carrying a type field is invalid.
    {
        std::vector<uint8_t> msg;
        appendRecord(msg, MB | CF, static_cast<uint8_t>(Tnf::kWellKnown),
                     kTypeU, {}, uriPayload(4, "example."));
        appendRecord(msg, ME | SR, static_cast<uint8_t>(Tnf::kUnchanged),
                     kTypeU, {}, std::vector<uint8_t>{'c'});
        expectError(msg, ParseError::kChunkContinuationHasType, httpsOnly(),
                    "continuation chunk with type rejected");
    }

    // 12. IL=1 promises 4 id bytes but only 2 remain in the buffer.
    {
        std::vector<uint8_t> msg{
            static_cast<uint8_t>(MB | ME | SR | IL |
                                 static_cast<uint8_t>(Tnf::kWellKnown)),
            0x01,  // type length
            0x00,  // payload length
            0x04,  // id length = 4
            'U', 'i', 'd', '?'};  // 3 of 4 id bytes, then buffer ends
        expectError(msg, ParseError::kTruncatedId, httpsOnly(),
                    "truncated id field rejected");
    }

    // 13. First record without MB is rejected.
    {
        std::vector<uint8_t> msg;
        appendRecord(msg, ME | SR, static_cast<uint8_t>(Tnf::kWellKnown),
                     kTypeU, {}, uriPayload(4, "example.com"));
        expectError(msg, ParseError::kFirstRecordMissingMb, httpsOnly(),
                    "first record missing MB rejected");
    }

    // 14. Trailing bytes after ME are rejected by default; consumed bytes are
    //     reported when trailing data is explicitly allowed.
    {
        std::vector<uint8_t> msg;
        appendRecord(msg, MB | ME | SR,
                     static_cast<uint8_t>(Tnf::kWellKnown), kTypeU, {},
                     uriPayload(4, "example.com"));
        msg.push_back(0x00);
        expectError(msg, ParseError::kTrailingDataAfterMe, httpsOnly(),
                    "trailing data after ME rejected");
        ParseOptions permissive = httpsOnly();
        permissive.allowTrailingData = true;
        const ParseResult r = parseMessage(msg, permissive);
        check(r.ok() && r.consumedBytes + 1 == msg.size(),
              "allowTrailingData reports consumed bytes");
    }

    // 15. Unknown URI identifier code (37) is rejected.
    {
        std::vector<uint8_t> msg;
        appendRecord(msg, MB | ME | SR,
                     static_cast<uint8_t>(Tnf::kWellKnown), kTypeU, {},
                     uriPayload(37, "example.com"));
        expectError(msg, ParseError::kUriPrefixCodeUnknown, httpsOnly(),
                    "unknown URI identifier code rejected");
    }

    // 16. Scheme mismatch: prefix yields http:// while only https is allowed.
    {
        std::vector<uint8_t> msg;
        appendRecord(msg, MB | ME | SR,
                     static_cast<uint8_t>(Tnf::kWellKnown), kTypeU, {},
                     uriPayload(3, "example.com"));
        expectError(msg, ParseError::kUriRejectedByWhitelist, httpsOnly(),
                    "http URI rejected under https-only whitelist");
    }

    // 17. Empty buffer and null input fail closed.
    {
        const ParseResult empty =
            parseMessage(std::vector<uint8_t>{}, httpsOnly());
        check(empty.error == ParseError::kEmptyBuffer,
              "empty buffer rejected");
        const ParseResult null = parseMessage(nullptr, 0, httpsOnly());
        check(null.error == ParseError::kEmptyBuffer,
              "null input rejected");
    }

    // 18. Valid Smart Poster (Sp -> URI) passes at depth 1.
    {
        std::vector<uint8_t> inner;
        appendRecord(inner, MB | ME | SR,
                     static_cast<uint8_t>(Tnf::kWellKnown), kTypeU, {},
                     uriPayload(4, "example.com"));
        std::vector<uint8_t> outer;
        appendRecord(outer, MB | ME,
                     static_cast<uint8_t>(Tnf::kWellKnown), kTypeSp, {},
                     inner);
        const ParseResult r = parseMessage(outer, httpsOnly());
        check(r.ok() && r.records.size() == 1 &&
                  r.records[0].isSmartPoster(),
              "valid Smart Poster parses");
    }

    // 19. Smart Poster nesting beyond the configured depth is rejected.
    {
        std::vector<uint8_t> layer;
        appendRecord(layer, MB | ME | SR,
                     static_cast<uint8_t>(Tnf::kWellKnown), kTypeU, {},
                     uriPayload(4, "example.com"));
        for (int i = 0; i < 3; ++i) {
            std::vector<uint8_t> wrapped;
            appendRecord(wrapped, MB | ME,
                         static_cast<uint8_t>(Tnf::kWellKnown), kTypeSp, {},
                         layer);
            layer.swap(wrapped);
        }
        ParseOptions opts = httpsOnly();
        opts.maxSmartPosterDepth = 2;  // wrapped layers reach depth 3
        expectError(layer, ParseError::kSmartPosterDepthExceeded, opts,
                    "Smart Poster depth limit enforced");
    }

    // 20. A file:// URI hidden inside a Smart Poster is still rejected.
    {
        std::vector<uint8_t> inner;
        appendRecord(inner, MB | ME | SR,
                     static_cast<uint8_t>(Tnf::kWellKnown), kTypeU, {},
                     uriPayload(30, "etc/passwd"));
        std::vector<uint8_t> outer;
        appendRecord(outer, MB | ME,
                     static_cast<uint8_t>(Tnf::kWellKnown), kTypeSp, {},
                     inner);
        expectError(outer, ParseError::kUriRejectedByWhitelist,
                    httpsOnly(), "file:// inside Smart Poster rejected");
    }

    // 21. Empty whitelist rejects every URI record (fail-closed default).
    {
        std::vector<uint8_t> msg;
        appendRecord(msg, MB | ME | SR,
                     static_cast<uint8_t>(Tnf::kWellKnown), kTypeU, {},
                     uriPayload(4, "example.com"));
        expectError(msg, ParseError::kUriRejectedByWhitelist,
                    ParseOptions{}, "empty whitelist rejects URI records");
    }

    std::printf("\n%d failure(s)\n", g_failures);
    return g_failures == 0 ? 0 : 1;
}
