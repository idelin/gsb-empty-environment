#pragma once

#include <cstddef>
#include <span>
#include <string>
#include <string_view>
#include <vector>

// safelog: injection-safe log formatting for the audit service.
//
// Security model:
//  - Messages are built from parameterized templates with positional {}
//    placeholders only. User-controlled values are inserted strictly as
//    data: they are never re-scanned for placeholders, never used as
//    lookup names, and never evaluated as expressions.
//  - No name lookup, no remote class loading, no expression evaluation.
//    Protocol prefixes (jndi:, ldap:, rmi:, ...) carry no meaning here
//    and are recorded as plain text.
//  - Structured field keys come from a fixed schema pattern; keys and
//    values are emitted separately so values cannot forge keys.
//  - Output encoding is stable: printable ASCII passes through, every
//    other byte is deterministically escaped.

namespace safelog {

struct Limits {
    std::size_t max_value_length = 256;    // bytes per user value (pre-encoding)
    std::size_t max_message_length = 2048; // bytes of final formatted line
    std::size_t max_fields = 64;           // structured fields per record
};

// Encodes user-controlled data as inert text. Printable ASCII (0x20-0x7E)
// is kept verbatim except '\\' and '"', which are escaped. Control
// characters and non-ASCII bytes become \n \r \t or \xNN (uppercase hex).
// Input longer than max_length is truncated and marked with "...".
std::string encode_value(std::string_view value, std::size_t max_length);

// Formats a parameterized template. Supported syntax:
//   {}   consumes the next argument (substituted as encoded data)
//   {{   literal '{'
//   }}   literal '}'
// Anything else between braces (e.g. {0}, {name}, ${...}) is NOT a
// placeholder: it is copied literally and never triggers lookup or
// evaluation. Missing arguments render as the literal text "{}";
// surplus arguments are ignored. The result is capped at
// limits.max_message_length and marked "...[truncated]" when cut.
std::string format(std::string_view tmpl,
                   const std::vector<std::string_view>& args,
                   const Limits& limits = {});

struct Field {
    std::string_view key;
    std::string_view value;
};

// Fixed schema for structured field keys:
//   [A-Za-z_][A-Za-z0-9_.]{0,63}
bool is_valid_key(std::string_view key);

// Renders structured fields as: key="encoded" key="encoded" ...
// Keys must satisfy the fixed schema; invalid keys are replaced with
// "bad_key_<index>" so a hostile key can never break the record layout.
// Values are encoded with encode_value and always quoted, keeping keys
// and values strictly separated.
std::string format_fields(std::span<const Field> fields,
                          const Limits& limits = {});

// One log record: formatted message followed by structured fields.
std::string format_line(std::string_view tmpl,
                        const std::vector<std::string_view>& args,
                        std::span<const Field> fields,
                        const Limits& limits = {});

// Variadic convenience wrapper. Arguments are stringified once and then
// handled as opaque data.
template <typename... Args>
std::string format(std::string_view tmpl, const Args&... args) {
    const std::vector<std::string> storage{std::string(std::string_view(args))...};
    std::vector<std::string_view> views;
    views.reserve(storage.size());
    for (const auto& s : storage) views.emplace_back(s);
    return format(tmpl, views);
}

} // namespace safelog
