#include "entitlements.h"

#include <algorithm>
#include <cctype>
#include <cstring>
#include <stdexcept>
#include <string_view>

namespace machosec::detail {

void fail(const std::string& message) {
    throw std::runtime_error(message);
}

namespace {

struct XmlNode {
    std::string name;
    std::string text;
    std::vector<XmlNode> children;
};

class XmlParser {
public:
    explicit XmlParser(std::string_view input) : input_(input) {}

    XmlNode parse_document() {
        skip_misc();
        XmlNode root = parse_node();
        skip_misc();
        if (position_ != input_.size()) {
            fail("trailing bytes after XML plist root");
        }
        return root;
    }

private:
    std::string_view input_;
    size_t position_ = 0;

    bool starts_with(std::string_view token) const {
        return position_ + token.size() <= input_.size() &&
               input_.compare(position_, token.size(), token) == 0;
    }

    void advance(size_t count) { position_ += count; }

    void skip_whitespace() {
        while (position_ < input_.size()) {
            const unsigned char ch =
                static_cast<unsigned char>(input_[position_]);
            if (std::isspace(ch) == 0) {
                break;
            }
            ++position_;
        }
    }

    void find_token(std::string_view token) {
        const size_t found = input_.find(token, position_);
        if (found == std::string_view::npos) {
            fail("unterminated XML declaration or comment");
        }
        position_ = found + token.size();
    }

    void skip_doctype() {
        // Skip a <!DOCTYPE ...> marker while respecting quoted '>' bytes.
        bool single_quote = false;
        bool double_quote = false;
        for (; position_ < input_.size(); ++position_) {
            const char ch = input_[position_];
            if (ch == '\'' && !double_quote) {
                single_quote = !single_quote;
            } else if (ch == '"' && !single_quote) {
                double_quote = !double_quote;
            } else if (ch == '>' && !single_quote && !double_quote) {
                ++position_;
                return;
            }
        }
        fail("unterminated XML DOCTYPE");
    }

    void skip_misc() {
        while (true) {
            skip_whitespace();
            if (starts_with("<!--")) {
                advance(4);
                find_token("-->");
            } else if (starts_with("<?")) {
                advance(2);
                find_token("?>");
            } else if (starts_with("<!")) {
                skip_doctype();
            } else {
                return;
            }
        }
    }

    std::string parse_name() {
        size_t begin = position_;
        while (position_ < input_.size()) {
            const char ch = input_[position_];
            const bool name_char =
                std::isalnum(static_cast<unsigned char>(ch)) != 0 ||
                ch == '-' || ch == '_' || ch == '.' || ch == ':';
            if (!name_char) {
                break;
            }
            ++position_;
        }
        if (position_ == begin) {
            fail("invalid XML element name");
        }
        return std::string(input_.substr(begin, position_ - begin));
    }

    void skip_opening_tag_suffix(bool& self_closing) {
        bool single_quote = false;
        bool double_quote = false;
        while (position_ < input_.size()) {
            const char ch = input_[position_];
            if (ch == '\'' && !double_quote) {
                single_quote = !single_quote;
            } else if (ch == '"' && !single_quote) {
                double_quote = !double_quote;
            } else if (!single_quote && !double_quote && ch == '/') {
                if (position_ + 1 < input_.size() &&
                    input_[position_ + 1] == '>') {
                    position_ += 2;
                    self_closing = true;
                    return;
                }
            } else if (!single_quote && !double_quote && ch == '>') {
                ++position_;
                return;
            }
            ++position_;
        }
        fail("unterminated XML opening tag");
    }

    std::string decode_entities(std::string_view text) const {
        std::string result;
        result.reserve(text.size());
        for (size_t i = 0; i < text.size(); ++i) {
            if (text[i] != '&') {
                result.push_back(text[i]);
                continue;
            }
            const size_t semi = text.find(';', i + 1);
            if (semi == std::string_view::npos) {
                fail("invalid XML entity");
            }
            std::string_view entity = text.substr(i + 1, semi - i - 1);
            if (entity == "amp") result.push_back('&');
            else if (entity == "lt") result.push_back('<');
            else if (entity == "gt") result.push_back('>');
            else if (entity == "quot") result.push_back('"');
            else if (entity == "apos") result.push_back('\'');
            else if (!entity.empty() && entity[0] == '#') {
                unsigned long codepoint = 0;
                if (entity.size() > 2 && (entity[1] == 'x' || entity[1] == 'X')) {
                    for (size_t j = 2; j < entity.size(); ++j) {
                        const char c = entity[j];
                        unsigned digit = 0;
                        if (c >= '0' && c <= '9') digit = unsigned(c - '0');
                        else if (c >= 'a' && c <= 'f') digit = unsigned(c - 'a' + 10);
                        else if (c >= 'A' && c <= 'F') digit = unsigned(c - 'A' + 10);
                        else fail("invalid numeric XML entity");
                        codepoint = codepoint * 16 + digit;
                    }
                } else {
                    for (size_t j = 1; j < entity.size(); ++j) {
                        if (entity[j] < '0' || entity[j] > '9') {
                            fail("invalid decimal XML entity");
                        }
                        codepoint = codepoint * 10 + unsigned(entity[j] - '0');
                    }
                }
                if (codepoint <= 0x7f) {
                    result.push_back(static_cast<char>(codepoint));
                } else if (codepoint <= 0x7ff) {
                    result.push_back(static_cast<char>(0xc0 | (codepoint >> 6)));
                    result.push_back(static_cast<char>(0x80 | (codepoint & 0x3f)));
                } else if (codepoint <= 0xffff) {
                    result.push_back(static_cast<char>(0xe0 | (codepoint >> 12)));
                    result.push_back(static_cast<char>(0x80 | ((codepoint >> 6) & 0x3f)));
                    result.push_back(static_cast<char>(0x80 | (codepoint & 0x3f)));
                } else {
                    fail("unsupported XML entity codepoint");
                }
            } else {
                fail("unknown XML entity");
            }
            i = semi;
        }
        return result;
    }

    void parse_closing_tag(const std::string& expected) {
        if (!starts_with("</")) {
            fail("expected XML closing tag");
        }
        advance(2);
        const std::string name = parse_name();
        skip_whitespace();
        if (position_ >= input_.size() || input_[position_] != '>') {
            fail("unterminated XML closing tag");
        }
        ++position_;
        if (name != expected) {
            fail("mismatched XML closing tag");
        }
    }

    XmlNode parse_node() {
        skip_misc();
        if (position_ >= input_.size() || input_[position_] != '<') {
            fail("expected XML element");
        }
        ++position_;
        XmlNode node;
        node.name = parse_name();
        bool self_closing = false;
        skip_opening_tag_suffix(self_closing);
        if (self_closing) {
            return node;
        }

        while (position_ < input_.size()) {
            if (input_[position_] == '<') {
                if (starts_with("</")) {
                    parse_closing_tag(node.name);
                    return node;
                }
                node.children.push_back(parse_node());
            } else {
                const size_t begin = position_;
                while (position_ < input_.size() && input_[position_] != '<') {
                    ++position_;
                }
                node.text += decode_entities(input_.substr(begin, position_ - begin));
            }
        }
        fail("missing XML closing tag for " + node.name);
    }
};

std::string trimmed(std::string value) {
    const auto not_space = [](unsigned char ch) { return std::isspace(ch) == 0; };
    value.erase(value.begin(), std::find_if(value.begin(), value.end(), not_space));
    value.erase(std::find_if(value.rbegin(), value.rend(), not_space).base(),
                value.end());
    return value;
}

std::vector<uint8_t> decode_base64(const std::string& input) {
    int accumulator = 0;
    int bits = 0;
    std::vector<uint8_t> output;
    for (char raw_character : input) {
        const auto ch = static_cast<unsigned char>(raw_character);
        if (std::isspace(ch) != 0) continue;
        int value = -1;
        if (ch >= 'A' && ch <= 'Z') value = ch - 'A';
        else if (ch >= 'a' && ch <= 'z') value = ch - 'a' + 26;
        else if (ch >= '0' && ch <= '9') value = ch - '0' + 52;
        else if (ch == '+') value = 62;
        else if (ch == '/') value = 63;
        else if (ch == '=') break;
        else fail("invalid base64 entitlement data");
        accumulator = (accumulator << 6) | value;
        bits += 6;
        if (bits >= 8) {
            bits -= 8;
            output.push_back(static_cast<uint8_t>((accumulator >> bits) & 0xff));
        }
    }
    return output;
}

EntValue xml_value(const XmlNode& node);

void insert_unique(EntValue::Kind /*dict_marker*/,
                    std::map<std::string, EntValue>& dict,
                    const std::string& key,
                    EntValue value) {
    if (!dict.emplace(key, std::move(value)).second) {
        fail("duplicate entitlement key: " + key);
    }
}

EntValue xml_dict(const XmlNode& node) {
    EntValue result;
    result.kind = EntValue::Kind::Dict;
    for (size_t i = 0; i + 1 < node.children.size(); i += 2) {
        const XmlNode& key_node = node.children[i];
        const XmlNode& value_node = node.children[i + 1];
        if (key_node.name != "key") {
            fail("XML entitlement dictionary must contain key/value pairs");
        }
        const std::string key = trimmed(key_node.text);
        insert_unique(EntValue::Kind::Dict, result.dict, key, xml_value(value_node));
    }
    if (node.children.size() % 2 != 0) {
        fail("odd number of XML dictionary nodes");
    }
    return result;
}

EntValue xml_value(const XmlNode& node) {
    EntValue value;
    if (node.name == "dict") {
        return xml_dict(node);
    }
    if (node.name == "array") {
        value.kind = EntValue::Kind::Array;
        for (const XmlNode& child : node.children) {
            value.array.push_back(xml_value(child));
        }
        return value;
    }
    if (node.name == "true") {
        value.kind = EntValue::Kind::Bool;
        value.boolean = true;
        return value;
    }
    if (node.name == "false") {
        value.kind = EntValue::Kind::Bool;
        value.boolean = false;
        return value;
    }
    if (node.name == "null") {
        return value;
    }
    if (node.name == "string") {
        value.kind = EntValue::Kind::String;
        value.text = node.text;
        return value;
    }
    if (node.name == "integer") {
        const std::string text = trimmed(node.text);
        size_t used = 0;
        try {
            value.integer = std::stoll(text, &used, 10);
        } catch (const std::exception&) {
            fail("invalid XML integer entitlement");
        }
        if (used != text.size()) {
            fail("invalid XML integer entitlement");
        }
        value.kind = EntValue::Kind::Integer;
        return value;
    }
    if (node.name == "data") {
        value.kind = EntValue::Kind::Bytes;
        value.bytes = decode_base64(node.text);
        return value;
    }

    // Real-number/date values are not security-sensitive booleans in the
    // entitlement model; preserve their lexical form for full comparisons.
    value.kind = EntValue::Kind::String;
    value.text = trimmed(node.text);
    return value;
}

struct DerTlv {
    uint8_t first_tag = 0;
    ByteView content;
    size_t next_offset = 0;
};

uint64_t read_der_length(ByteView data, size_t& position) {
    if (position >= data.size) fail("truncated DER length");
    uint8_t first = data.data[position++];
    if ((first & 0x80u) == 0) {
        return first;
    }
    const size_t length_bytes = size_t(first & 0x7fu);
    if (length_bytes == 0) fail("indefinite DER length is not valid DER");
    if (length_bytes > 8) fail("unsupported DER length width");
    if (position + length_bytes > data.size) fail("truncated DER long length");
    uint64_t length = 0;
    for (size_t i = 0; i < length_bytes; ++i) {
        length = (length << 8u) | data.data[position++];
    }
    return length;
}

DerTlv parse_der_tlv(ByteView data, size_t offset) {
    size_t position = offset;
    if (position >= data.size) fail("missing DER tag");
    const uint8_t first_tag = data.data[position++];
    if ((first_tag & 0x1fu) == 0x1fu) {
        do {
            if (position >= data.size) fail("truncated high-tag DER tag");
        } while ((data.data[position++] & 0x80u) != 0);
    }
    const uint64_t length = read_der_length(data, position);
    if (length > data.size - position) fail("DER value length exceeds blob");
    const size_t content_offset = position;
    const size_t content_length = static_cast<size_t>(length);
    position += content_length;
    return {first_tag,
            ByteView(data.data + content_offset, content_length),
            position};
}

std::string der_oid_to_string(ByteView content) {
    if (content.size < 1) fail("empty DER OID");
    std::string result;
    result += std::to_string(content.data[0] / 40);
    result.push_back('.');
    result += std::to_string(content.data[0] % 40);
    uint64_t arc = 0;
    for (size_t i = 1; i < content.size; ++i) {
        const uint8_t byte = content.data[i];
        arc = (arc << 7u) | uint64_t(byte & 0x7fu);
        if ((byte & 0x80u) == 0) {
            result.push_back('.');
            result += std::to_string(arc);
            arc = 0;
        }
    }
    return result;
}

EntValue parse_der_value(uint8_t tag, ByteView content);

std::string der_as_string(ByteView content) {
    return std::string(reinterpret_cast<const char*>(content.data), content.size);
}

EntValue parse_der_sequence_like(uint8_t tag, ByteView content) {
    const bool constructed = (tag & 0x20u) != 0;
    if (!constructed) {
        fail("DER sequence tag is primitive");
    }

    std::vector<DerTlv> children;
    size_t position = 0;
    while (position < content.size) {
        DerTlv child = parse_der_tlv(content, position);
        children.push_back(child);
        position = child.next_offset;
    }

    bool looks_like_dict = true;
    for (const DerTlv& child : children) {
        if (child.first_tag != 0x30u) {
            looks_like_dict = false;
            break;
        }
        size_t pair_position = 0;
        DerTlv key_tlv = parse_der_tlv(child.content, pair_position);
        if ((key_tlv.first_tag & 0x1fu) != 0x0cu &&
            key_tlv.first_tag != 0x0c && key_tlv.first_tag != 0x16 &&
            key_tlv.first_tag != 0x13) {
            looks_like_dict = false;
            break;
        }
        pair_position = key_tlv.next_offset;
        DerTlv value_tlv = parse_der_tlv(child.content, pair_position);
        if (value_tlv.next_offset != child.content.size) {
            looks_like_dict = false;
            break;
        }
    }

    EntValue result;
    if (looks_like_dict) {
        result.kind = EntValue::Kind::Dict;
        for (const DerTlv& pair_tlv : children) {
            size_t position = 0;
            DerTlv key_tlv = parse_der_tlv(pair_tlv.content, position);
            EntValue key_value = parse_der_value(key_tlv.first_tag, key_tlv.content);
            if (key_value.kind != EntValue::Kind::String) {
                fail("DER entitlement key is not a string");
            }
            position = key_tlv.next_offset;
            DerTlv value_tlv = parse_der_tlv(pair_tlv.content, position);
            insert_unique(EntValue::Kind::Dict, result.dict, key_value.text,
                          parse_der_value(value_tlv.first_tag, value_tlv.content));
        }
        return result;
    }

    result.kind = EntValue::Kind::Array;
    for (const DerTlv& child : children) {
        result.array.push_back(parse_der_value(child.first_tag, child.content));
    }
    return result;
}

EntValue parse_der_value(uint8_t tag, ByteView content) {
    if ((tag & 0x20u) != 0) {
        return parse_der_sequence_like(tag, content);
    }

    EntValue value;
    switch (tag) {
        case 0x01: {
            if (content.size != 1 ||
                (content.data[0] != 0x00 && content.data[0] != 0xff)) {
                fail("invalid DER boolean");
            }
            value.kind = EntValue::Kind::Bool;
            value.boolean = content.data[0] == 0xff;
            return value;
        }
        case 0x02: {
            if (content.size == 0 || content.size > 8) {
                fail("unsupported DER integer width");
            }
            uint64_t raw = 0;
            for (size_t i = 0; i < content.size; ++i) {
                raw = (raw << 8u) | content.data[i];
            }
            if ((content.data[0] & 0x80u) != 0 && content.size < 8) {
                const uint64_t sign_extension =
                    (~uint64_t(0)) << (content.size * 8u);
                raw |= sign_extension;
            }
            int64_t signed_value = 0;
            std::memcpy(&signed_value, &raw, sizeof(signed_value));
            value.kind = EntValue::Kind::Integer;
            value.integer = signed_value;
            return value;
        }
        case 0x04: {
            value.kind = EntValue::Kind::Bytes;
            value.bytes.assign(content.data, content.data + content.size);
            return value;
        }
        case 0x05: {
            if (content.size != 0) fail("DER NULL has content");
            return value;
        }
        case 0x06: {
            value.kind = EntValue::Kind::String;
            value.text = der_oid_to_string(content);
            return value;
        }
        case 0x03: {
            if (content.size == 0 || content.data[0] != 0) {
                fail("unsupported DER bit string unused bits");
            }
            value.kind = EntValue::Kind::Bytes;
            value.bytes.assign(content.data + 1, content.data + content.size);
            return value;
        }
        case 0x0c:
        case 0x13:
        case 0x14:
        case 0x16:
        case 0x17:
        case 0x18: {
            value.kind = EntValue::Kind::String;
            value.text = der_as_string(content);
            return value;
        }
        default: {
            value.kind = EntValue::Kind::Bytes;
            value.bytes.assign(content.data, content.data + content.size);
            return value;
        }
    }
}

bool is_sensitive_entitlement(const std::string& key) {
    static const std::vector<std::string> exact = {
        "get-task-allow",
        "com.apple.security.get-task-allow",
        "com.apple.security.cs.allow-jit",
        "com.apple.security.cs.allow-unsigned-executable-memory",
        "com.apple.security.cs.allow-dyld-environment-variables",
        "com.apple.security.cs.disable-library-validation",
        "com.apple.security.cs.disable-executable-page-protection",
        "com.apple.security.cs.debugger",
        "com.apple.security.cs.allow-unsigned-entitlements",
    };
    if (std::find(exact.begin(), exact.end(), key) != exact.end()) return true;
    return key.rfind("com.apple.security.", 0) == 0 ||
           key.rfind("com.apple.private.", 0) == 0;
}

EntValue normalized_value(const EntValue& input);

void collect_effective(const EntValue& input,
                       bool only_sensitive,
                       std::map<std::string, EntValue>& output) {
    if (input.kind != EntValue::Kind::Dict) {
        fail("entitlement root is not a dictionary");
    }
    for (const auto& pair : input.dict) {
        if (only_sensitive && !is_sensitive_entitlement(pair.first)) {
            continue;
        }
        EntValue normalized = normalized_value(pair.second);
        if (normalized.kind == EntValue::Kind::Null) {
            continue;
        }
        output.emplace(pair.first, std::move(normalized));
    }
}

EntValue normalized_value(const EntValue& input) {
    EntValue output = input;
    if (input.kind == EntValue::Kind::Bool && !input.boolean) {
        return EntValue{};
    }
    if (input.kind == EntValue::Kind::Dict) {
        output.dict.clear();
        for (const auto& pair : input.dict) {
            EntValue child = normalized_value(pair.second);
            if (child.kind != EntValue::Kind::Null) {
                output.dict.emplace(pair.first, std::move(child));
            }
        }
        if (output.dict.empty()) return EntValue{};
    }
    if (input.kind == EntValue::Kind::Array) {
        output.array.clear();
        for (const EntValue& child : input.array) {
            output.array.push_back(normalized_value(child));
        }
    }
    return output;
}

bool values_equal(const EntValue& a, const EntValue& b) {
    if (a.kind != b.kind) return false;
    switch (a.kind) {
        case EntValue::Kind::Null:
            return true;
        case EntValue::Kind::Bool:
            return a.boolean == b.boolean;
        case EntValue::Kind::Integer:
            return a.integer == b.integer;
        case EntValue::Kind::String:
            return a.text == b.text;
        case EntValue::Kind::Bytes:
            return a.bytes == b.bytes;
        case EntValue::Kind::Array:
            if (a.array.size() != b.array.size()) return false;
            for (size_t i = 0; i < a.array.size(); ++i) {
                if (!values_equal(a.array[i], b.array[i])) return false;
            }
            return true;
        case EntValue::Kind::Dict:
            if (a.dict.size() != b.dict.size()) return false;
            for (const auto& pair : a.dict) {
                const auto found = b.dict.find(pair.first);
                if (found == b.dict.end() ||
                    !values_equal(pair.second, found->second)) {
                    return false;
                }
            }
            return true;
    }
    return false;
}

}  // namespace

EntValue parse_xml_entitlements(ByteView bytes) {
    std::string_view text(reinterpret_cast<const char*>(bytes.data), bytes.size);
    XmlParser parser(text);
    XmlNode root = parser.parse_document();
    if (root.name != "plist") {
        fail("XML entitlements root is not a plist");
    }
    const XmlNode* dict = nullptr;
    for (const XmlNode& child : root.children) {
        if (child.name == "dict") {
            dict = &child;
            break;
        }
    }
    if (dict == nullptr) fail("XML entitlements has no dictionary");
    return xml_dict(*dict);
}

EntValue parse_der_entitlements(ByteView bytes) {
    DerTlv root = parse_der_tlv(bytes, 0);
    if (root.next_offset != bytes.size) {
        fail("trailing bytes after DER entitlements");
    }
    EntValue value = parse_der_value(root.first_tag, root.content);
    if (value.kind == EntValue::Kind::Dict) {
        return value;
    }
    if (value.kind == EntValue::Kind::Array) {
        for (EntValue& child : value.array) {
            if (child.kind == EntValue::Kind::Dict) {
                return std::move(child);
            }
        }
    }
    fail("DER entitlements does not contain a dictionary");
}

bool semantic_entitlements_equal(const EntValue& xml,
                                 const EntValue& der,
                                 bool only_sensitive,
                                 std::string* difference) {
    std::map<std::string, EntValue> xml_effective;
    std::map<std::string, EntValue> der_effective;
    collect_effective(xml, only_sensitive, xml_effective);
    collect_effective(der, only_sensitive, der_effective);

    for (const auto& pair : xml_effective) {
        const auto found = der_effective.find(pair.first);
        if (found == der_effective.end() ||
            !values_equal(pair.second, found->second)) {
            if (difference != nullptr) {
                *difference = "sensitive entitlement mismatch: " + pair.first;
            }
            return false;
        }
    }
    for (const auto& pair : der_effective) {
        if (xml_effective.find(pair.first) == xml_effective.end()) {
            if (difference != nullptr) {
                *difference = "DER-only sensitive entitlement: " + pair.first;
            }
            return false;
        }
    }
    return true;
}

}  // namespace machosec::detail
