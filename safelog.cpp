#include "safelog.hpp"

#include <array>

namespace safelog {

namespace {

constexpr std::string_view kTruncatedMark = "...[truncated]";

bool is_printable_ascii(unsigned char c) { return c >= 0x20 && c <= 0x7E; }

void append_hex_escape(std::string& out, unsigned char c) {
    constexpr std::array<char, 16> kHex = {'0', '1', '2', '3', '4', '5', '6', '7',
                                           '8', '9', 'A', 'B', 'C', 'D', 'E', 'F'};
    out += "\\x";
    out += kHex[c >> 4];
    out += kHex[c & 0x0F];
}

void truncate_to(std::string& s, std::size_t max_length) {
    if (s.size() <= max_length) return;
    if (max_length <= kTruncatedMark.size()) {
        s.resize(max_length);
        return;
    }
    s.resize(max_length - kTruncatedMark.size());
    s += kTruncatedMark;
}

} // namespace

std::string encode_value(std::string_view value, std::size_t max_length) {
    bool truncated = false;
    if (value.size() > max_length) {
        value = value.substr(0, max_length);
        truncated = true;
    }
    std::string out;
    out.reserve(value.size());
    for (const unsigned char c : value) {
        switch (c) {
        case '\n': out += "\\n"; break;
        case '\r': out += "\\r"; break;
        case '\t': out += "\\t"; break;
        case '\\': out += "\\\\"; break;
        case '"': out += "\\\""; break;
        default:
            if (is_printable_ascii(c)) {
                out += static_cast<char>(c);
            } else {
                append_hex_escape(out, c);
            }
        }
    }
    if (truncated) out += "...";
    return out;
}

std::string format(std::string_view tmpl,
                   const std::vector<std::string_view>& args,
                   const Limits& limits) {
    std::string out;
    out.reserve(tmpl.size());
    std::size_t next_arg = 0;

    for (std::size_t i = 0; i < tmpl.size(); ++i) {
        const char c = tmpl[i];
        if (c == '{' && i + 1 < tmpl.size()) {
            const char next = tmpl[i + 1];
            if (next == '{') {
                out += '{';
                ++i;
                continue;
            }
            if (next == '}') {
                if (next_arg < args.size()) {
                    out += encode_value(args[next_arg], limits.max_value_length);
                    ++next_arg;
                } else {
                    out += "{}";
                }
                ++i;
                continue;
            }
            // "{0}", "{name}", "${...}" and friends are not placeholders:
            // copy the '{' literally and never interpret what follows.
        } else if (c == '}' && i + 1 < tmpl.size() && tmpl[i + 1] == '}') {
            out += '}';
            ++i;
            continue;
        }
        out += c;
    }

    truncate_to(out, limits.max_message_length);
    return out;
}

bool is_valid_key(std::string_view key) {
    if (key.empty() || key.size() > 64) return false;
    const auto is_alpha = [](char c) {
        return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_';
    };
    const auto is_alnum = [&](char c) {
        return is_alpha(c) || (c >= '0' && c <= '9') || c == '.';
    };
    if (!is_alpha(key.front())) return false;
    for (const char c : key) {
        if (!is_alnum(c)) return false;
    }
    return true;
}

std::string format_fields(std::span<const Field> fields, const Limits& limits) {
    std::string out;
    const std::size_t count =
        fields.size() < limits.max_fields ? fields.size() : limits.max_fields;
    for (std::size_t i = 0; i < count; ++i) {
        if (!out.empty()) out += ' ';
        if (is_valid_key(fields[i].key)) {
            out += fields[i].key;
        } else {
            out += "bad_key_" + std::to_string(i);
        }
        out += "=\"";
        out += encode_value(fields[i].value, limits.max_value_length);
        out += '"';
    }
    return out;
}

std::string format_line(std::string_view tmpl,
                        const std::vector<std::string_view>& args,
                        std::span<const Field> fields,
                        const Limits& limits) {
    std::string out = format(tmpl, args, limits);
    const std::string tail = format_fields(fields, limits);
    if (!tail.empty()) {
        out += ' ';
        out += tail;
    }
    truncate_to(out, limits.max_message_length);
    return out;
}

} // namespace safelog
