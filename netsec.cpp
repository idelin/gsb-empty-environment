#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cctype>
#include <charconv>
#include <cmath>
#include <cstdint>
#include <cstdlib>
#include <limits>
#include <map>
#include <memory>
#include <optional>
#include <set>
#include <sstream>
#include <string>
#include <string_view>
#include <unordered_map>
#include <unordered_set>
#include <utility>

namespace netsec {
namespace detail {

constexpr bool is_pdf_space(unsigned char c) {
    return c == 0 || c == '\t' || c == '\n' || c == '\f' || c == '\r' || c == ' ';
}

constexpr bool is_pdf_delimiter(unsigned char c) {
    return c == '(' || c == ')' || c == '<' || c == '>' || c == '[' || c == ']' ||
           c == '{' || c == '}' || c == '/' || c == '%';
}

inline bool is_hex_digit(unsigned char c) {
    return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F');
}

inline unsigned hex_value(unsigned char c) {
    if (c >= '0' && c <= '9') {
        return c - '0';
    }
    if (c >= 'a' && c <= 'f') {
        return static_cast<unsigned>(c - 'a' + 10);
    }
    return static_cast<unsigned>(c - 'A' + 10);
}

inline std::string trim_copy(std::string_view text) {
    std::size_t first = 0;
    while (first < text.size() && is_pdf_space(static_cast<unsigned char>(text[first]))) {
        ++first;
    }
    std::size_t last = text.size();
    while (last > first && is_pdf_space(static_cast<unsigned char>(text[last - 1]))) {
        --last;
    }
    return std::string(text.substr(first, last - first));
}

inline std::string quote_json(std::string_view text) {
    std::string out;
    out.reserve(text.size() + 8);
    out.push_back('"');
    for (unsigned char c : text) {
        switch (c) {
            case '"':
                out += "\\\"";
                break;
            case '\\':
                out += "\\\\";
                break;
            case '\b':
                out += "\\b";
                break;
            case '\f':
                out += "\\f";
                break;
            case '\n':
                out += "\\n";
                break;
            case '\r':
                out += "\\r";
                break;
            case '\t':
                out += "\\t";
                break;
            default:
                if (c < 0x20) {
                    static constexpr char digits[] = "0123456789abcdef";
                    out += "\\u00";
                    out.push_back(digits[(c >> 4) & 0x0f]);
                    out.push_back(digits[c & 0x0f]);
                } else {
                    out.push_back(static_cast<char>(c));
                }
                break;
        }
    }
    out.push_back('"');
    return out;
}

inline std::string printable_excerpt(std::string_view text, std::size_t limit = 160) {
    std::string out;
    out.reserve(std::min(text.size(), limit));
    for (unsigned char c : text) {
        if (out.size() >= limit) {
            out += "...";
            break;
        }
        if (c == '\r' || c == '\n' || c == '\t') {
            out.push_back(' ');
        } else if (c < 0x20) {
            out.push_back('?');
        } else {
            out.push_back(static_cast<char>(c));
        }
    }
    return out;
}

struct Value {
    enum class Kind {
        Null,
        Boolean,
        Integer,
        Real,
        String,
        Name,
        Array,
        Dictionary,
        Reference,
    };

    Kind kind = Kind::Null;
    bool boolean = false;
    long long integer = 0;
    double real = 0.0;
    std::string text;
    std::vector<Value> array;
    std::vector<std::pair<std::string, Value>> dictionary;
    std::uint32_t reference_number = 0;
    std::uint16_t reference_generation = 0;
    std::size_t source_begin = 0;
    std::size_t source_end = 0;

    bool is_null() const { return kind == Kind::Null; }
    bool is_integer() const { return kind == Kind::Integer; }
    bool is_real() const { return kind == Kind::Real; }
    bool is_name() const { return kind == Kind::Name; }
    bool is_string() const { return kind == Kind::String; }
    bool is_array() const { return kind == Kind::Array; }
    bool is_dictionary() const { return kind == Kind::Dictionary; }
    bool is_reference() const { return kind == Kind::Reference; }

    const Value* find(std::string_view key) const {
        const Value* result = nullptr;
        for (const auto& [name, value] : dictionary) {
            if (name == key) {
                result = &value;
            }
        }
        return result;
    }

    bool has(std::string_view key) const { return find(key) != nullptr; }

    long long integer_or(long long fallback = 0) const {
        if (kind == Kind::Integer) {
            return integer;
        }
        if (kind == Kind::Real) {
            return static_cast<long long>(real);
        }
        return fallback;
    }
};

inline std::string value_text(const Value& value) {
    switch (value.kind) {
        case Value::Kind::Null:
            return "null";
        case Value::Kind::Boolean:
            return value.boolean ? "true" : "false";
        case Value::Kind::Integer:
            return std::to_string(value.integer);
        case Value::Kind::Real: {
            std::ostringstream stream;
            stream << value.real;
            return stream.str();
        }
        case Value::Kind::String:
            return value.text;
        case Value::Kind::Name:
            return "/" + value.text;
        case Value::Kind::Reference:
            return std::to_string(value.reference_number) + " " +
                   std::to_string(value.reference_generation) + " R";
        case Value::Kind::Array: {
            std::string result = "[";
            for (std::size_t i = 0; i < value.array.size(); ++i) {
                if (i != 0) {
                    result.push_back(' ');
                }
                result += value_text(value.array[i]);
            }
            result.push_back(']');
            return result;
        }
        case Value::Kind::Dictionary: {
            std::string result = "<<";
            for (const auto& [key, item] : value.dictionary) {
                result += "/" + key + " " + value_text(item) + " ";
            }
            result += ">>";
            return result;
        }
    }
    return {};
}

class PdfParser {
public:
    explicit PdfParser(std::string_view data, std::size_t position = 0)
        : data_(data), position_(position) {}

    std::size_t position() const { return position_; }
    void set_position(std::size_t position) { position_ = position; }
    bool at_end() const { return position_ >= data_.size(); }
    std::string_view data() const { return data_; }

    void skip_space() {
        while (position_ < data_.size()) {
            const unsigned char c = static_cast<unsigned char>(data_[position_]);
            if (is_pdf_space(c)) {
                ++position_;
                continue;
            }
            if (c == '%') {
                while (position_ < data_.size() && data_[position_] != '\n' &&
                       data_[position_] != '\r') {
                    ++position_;
                }
                continue;
            }
            break;
        }
    }

    bool consume_keyword(std::string_view keyword) {
        skip_space();
        if (data_.substr(position_, keyword.size()) != keyword) {
            return false;
        }
        const std::size_t after = position_ + keyword.size();
        if (after < data_.size()) {
            const unsigned char c = static_cast<unsigned char>(data_[after]);
            if (!is_pdf_space(c) && !is_pdf_delimiter(c)) {
                return false;
            }
        }
        position_ = after;
        return true;
    }

    bool parse_unsigned(std::uint64_t& out) {
        skip_space();
        const std::size_t begin = position_;
        while (position_ < data_.size() &&
               std::isdigit(static_cast<unsigned char>(data_[position_]))) {
            ++position_;
        }
        if (begin == position_) {
            return false;
        }
        std::uint64_t value = 0;
        const auto result = std::from_chars(data_.data() + begin, data_.data() + position_, value);
        if (result.ec != std::errc{}) {
            position_ = begin;
            return false;
        }
        out = value;
        return true;
    }

    bool parse_number_token(Value& out) {
        skip_space();
        const std::size_t begin = position_;
        if (position_ < data_.size() && (data_[position_] == '+' || data_[position_] == '-')) {
            ++position_;
        }
        bool digits = false;
        while (position_ < data_.size() &&
               std::isdigit(static_cast<unsigned char>(data_[position_]))) {
            digits = true;
            ++position_;
        }
        bool dot = false;
        if (position_ < data_.size() && data_[position_] == '.') {
            dot = true;
            ++position_;
            while (position_ < data_.size() &&
                   std::isdigit(static_cast<unsigned char>(data_[position_]))) {
                digits = true;
                ++position_;
            }
        }
        if (!digits) {
            position_ = begin;
            return false;
        }
        if (position_ < data_.size() &&
            (data_[position_] == 'e' || data_[position_] == 'E')) {
            const std::size_t exponent_begin = position_;
            ++position_;
            if (position_ < data_.size() &&
                (data_[position_] == '+' || data_[position_] == '-')) {
                ++position_;
            }
            bool exponent_digits = false;
            while (position_ < data_.size() &&
                   std::isdigit(static_cast<unsigned char>(data_[position_]))) {
                exponent_digits = true;
                ++position_;
            }
            if (!exponent_digits) {
                position_ = exponent_begin;
            }
        }
        const std::string_view token = data_.substr(begin, position_ - begin);
        if (dot || token.find_first_of("eE") != std::string_view::npos) {
            out.kind = Value::Kind::Real;
            out.real = std::strtod(std::string(token).c_str(), nullptr);
        } else {
            long long number = 0;
            std::string_view digits = token;
            if (!digits.empty() && digits.front() == '+') {
                digits.remove_prefix(1);
            }
            const auto result =
                std::from_chars(digits.data(), digits.data() + digits.size(), number);
            if (result.ec != std::errc{}) {
                position_ = begin;
                return false;
            }
            out.kind = Value::Kind::Integer;
            out.integer = number;
        }
        out.source_begin = begin;
        out.source_end = position_;
        return true;
    }

    bool parse_name_token(Value& out) {
        skip_space();
        if (position_ >= data_.size() || data_[position_] != '/') {
            return false;
        }
        const std::size_t begin = position_;
        ++position_;
        std::string name;
        while (position_ < data_.size()) {
            const unsigned char c = static_cast<unsigned char>(data_[position_]);
            if (is_pdf_space(c) || is_pdf_delimiter(c)) {
                break;
            }
            if (c == '#' && position_ + 2 < data_.size() &&
                is_hex_digit(static_cast<unsigned char>(data_[position_ + 1])) &&
                is_hex_digit(static_cast<unsigned char>(data_[position_ + 2]))) {
                const unsigned value =
                    (hex_value(static_cast<unsigned char>(data_[position_ + 1])) << 4) |
                    hex_value(static_cast<unsigned char>(data_[position_ + 2]));
                name.push_back(static_cast<char>(value));
                position_ += 3;
                continue;
            }
            name.push_back(static_cast<char>(c));
            ++position_;
        }
        out.kind = Value::Kind::Name;
        out.text = std::move(name);
        out.source_begin = begin;
        out.source_end = position_;
        return true;
    }

    bool parse_literal_string(Value& out) {
        if (position_ >= data_.size() || data_[position_] != '(') {
            return false;
        }
        const std::size_t begin = position_;
        ++position_;
        int depth = 1;
        std::string text;
        while (position_ < data_.size()) {
            char c = data_[position_++];
            if (c == '\\') {
                if (position_ >= data_.size()) {
                    break;
                }
                const char escaped = data_[position_++];
                switch (escaped) {
                    case 'n':
                        text.push_back('\n');
                        break;
                    case 'r':
                        text.push_back('\r');
                        break;
                    case 't':
                        text.push_back('\t');
                        break;
                    case 'b':
                        text.push_back('\b');
                        break;
                    case 'f':
                        text.push_back('\f');
                        break;
                    case '(':
                    case ')':
                    case '\\':
                        text.push_back(escaped);
                        break;
                    case '\r':
                        if (position_ < data_.size() && data_[position_] == '\n') {
                            ++position_;
                        }
                        break;
                    case '\n':
                        break;
                    default:
                        if (escaped >= '0' && escaped <= '7') {
                            unsigned octal = static_cast<unsigned>(escaped - '0');
                            int count = 1;
                            while (count < 3 && position_ < data_.size() &&
                                   data_[position_] >= '0' && data_[position_] <= '7') {
                                octal = octal * 8 +
                                        static_cast<unsigned>(data_[position_] - '0');
                                ++position_;
                                ++count;
                            }
                            text.push_back(static_cast<char>(octal & 0xff));
                        } else {
                            text.push_back(escaped);
                        }
                        break;
                }
                continue;
            }
            if (c == '(') {
                ++depth;
            } else if (c == ')') {
                --depth;
                if (depth == 0) {
                    break;
                }
            }
            text.push_back(c);
        }
        out.kind = Value::Kind::String;
        out.text = std::move(text);
        out.source_begin = begin;
        out.source_end = position_;
        return true;
    }

    bool parse_hex_string(Value& out) {
        if (position_ >= data_.size() || data_[position_] != '<' ||
            (position_ + 1 < data_.size() && data_[position_ + 1] == '<')) {
            return false;
        }
        const std::size_t begin = position_;
        ++position_;
        std::string text;
        int high = -1;
        while (position_ < data_.size()) {
            const unsigned char c = static_cast<unsigned char>(data_[position_++]);
            if (c == '>') {
                break;
            }
            if (is_pdf_space(c)) {
                continue;
            }
            if (!is_hex_digit(c)) {
                position_ = begin;
                return false;
            }
            const int value = static_cast<int>(hex_value(c));
            if (high < 0) {
                high = value;
            } else {
                text.push_back(static_cast<char>((high << 4) | value));
                high = -1;
            }
        }
        if (high >= 0) {
            text.push_back(static_cast<char>(high << 4));
        }
        out.kind = Value::Kind::String;
        out.text = std::move(text);
        out.source_begin = begin;
        out.source_end = position_;
        return true;
    }

    bool parse_value(Value& out, int depth = 0) {
        if (depth > 128) {
            return false;
        }
        skip_space();
        if (position_ >= data_.size()) {
            return false;
        }
        const std::size_t begin = position_;
        const char first = data_[position_];

        if (first == '/') {
            return parse_name_token(out);
        }
        if (first == '(') {
            return parse_literal_string(out);
        }
        if (first == '<') {
            if (position_ + 1 < data_.size() && data_[position_ + 1] == '<') {
                position_ += 2;
                out.kind = Value::Kind::Dictionary;
                out.source_begin = begin;
                while (true) {
                    skip_space();
                    if (position_ + 1 < data_.size() && data_[position_] == '>' &&
                        data_[position_ + 1] == '>') {
                        position_ += 2;
                        break;
                    }
                    if (position_ >= data_.size()) {
                        position_ = begin;
                        return false;
                    }
                    Value key;
                    if (!parse_name_token(key)) {
                        position_ = begin;
                        return false;
                    }
                    Value item;
                    if (!parse_value(item, depth + 1)) {
                        position_ = begin;
                        return false;
                    }
                    out.dictionary.emplace_back(std::move(key.text), std::move(item));
                }
                out.source_end = position_;
                return true;
            }
            return parse_hex_string(out);
        }
        if (first == '[') {
            ++position_;
            out.kind = Value::Kind::Array;
            out.source_begin = begin;
            while (true) {
                skip_space();
                if (position_ >= data_.size()) {
                    position_ = begin;
                    return false;
                }
                if (data_[position_] == ']') {
                    ++position_;
                    break;
                }
                Value item;
                if (!parse_value(item, depth + 1)) {
                    position_ = begin;
                    return false;
                }
                out.array.push_back(std::move(item));
            }
            out.source_end = position_;
            return true;
        }

        if (data_.substr(position_, 4) == "true" &&
            (position_ + 4 >= data_.size() ||
             is_pdf_space(static_cast<unsigned char>(data_[position_ + 4])) ||
             is_pdf_delimiter(static_cast<unsigned char>(data_[position_ + 4])))) {
            position_ += 4;
            out.kind = Value::Kind::Boolean;
            out.boolean = true;
            out.source_begin = begin;
            out.source_end = position_;
            return true;
        }
        if (data_.substr(position_, 5) == "false" &&
            (position_ + 5 >= data_.size() ||
             is_pdf_space(static_cast<unsigned char>(data_[position_ + 5])) ||
             is_pdf_delimiter(static_cast<unsigned char>(data_[position_ + 5])))) {
            position_ += 5;
            out.kind = Value::Kind::Boolean;
            out.boolean = false;
            out.source_begin = begin;
            out.source_end = position_;
            return true;
        }
        if (data_.substr(position_, 4) == "null" &&
            (position_ + 4 >= data_.size() ||
             is_pdf_space(static_cast<unsigned char>(data_[position_ + 4])) ||
             is_pdf_delimiter(static_cast<unsigned char>(data_[position_ + 4])))) {
            position_ += 4;
            out.kind = Value::Kind::Null;
            out.source_begin = begin;
            out.source_end = position_;
            return true;
        }

        if (!parse_number_token(out)) {
            return false;
        }
        if (out.kind == Value::Kind::Integer) {
            const long long first_integer = out.integer;
            const std::size_t after_first = position_;
            Value generation;
            if (parse_number_token(generation) && generation.kind == Value::Kind::Integer &&
                parse_keyword("R")) {
                out.kind = Value::Kind::Reference;
                out.reference_number = static_cast<std::uint32_t>(first_integer);
                out.reference_generation = static_cast<std::uint16_t>(generation.integer);
                out.source_begin = begin;
                out.source_end = position_;
                return true;
            }
            position_ = after_first;
        }
        return true;
    }

private:
    bool parse_keyword(std::string_view keyword) {
        skip_space();
        if (data_.substr(position_, keyword.size()) != keyword) {
            return false;
        }
        const std::size_t after = position_ + keyword.size();
        if (after < data_.size()) {
            const unsigned char c = static_cast<unsigned char>(data_[after]);
            if (!is_pdf_space(c) && !is_pdf_delimiter(c)) {
                return false;
            }
        }
        position_ = after;
        return true;
    }

    std::string_view data_;
    std::size_t position_ = 0;
};

struct PdfObject {
    std::uint32_t number = 0;
    std::uint16_t generation = 0;
    Value value;
    bool has_stream = false;
    std::size_t begin = 0;
    std::size_t end = 0;
    std::size_t stream_data_begin = 0;
    std::size_t stream_data_end = 0;
    std::size_t stream_span_begin = 0;
    std::size_t stream_span_end = 0;
    bool valid = false;
};

inline bool parse_indirect_object(std::string_view data, std::size_t offset, PdfObject& out,
                                  int depth = 0) {
    if (depth > 32) {
        return false;
    }
    PdfParser parser(data, offset);
    parser.skip_space();
    const std::size_t begin = parser.position();
    std::uint64_t number = 0;
    std::uint64_t generation = 0;
    if (!parser.parse_unsigned(number) || !parser.parse_unsigned(generation)) {
        return false;
    }
    if (!parser.consume_keyword("obj")) {
        return false;
    }
    Value value;
    if (!parser.parse_value(value)) {
        return false;
    }
    out.number = static_cast<std::uint32_t>(number);
    out.generation = static_cast<std::uint16_t>(generation);
    out.value = std::move(value);
    out.begin = begin;

    parser.skip_space();
    if (out.value.is_dictionary() && parser.consume_keyword("stream")) {
        std::size_t data_begin = parser.position();
        if (data_begin < data.size() && data[data_begin] == '\r') {
            ++data_begin;
            if (data_begin < data.size() && data[data_begin] == '\n') {
                ++data_begin;
            }
        } else if (data_begin < data.size() && data[data_begin] == '\n') {
            ++data_begin;
        } else {
            while (data_begin < data.size() &&
                   is_pdf_space(static_cast<unsigned char>(data[data_begin]))) {
                ++data_begin;
            }
        }

        std::size_t data_end = 0;
        bool found_end = false;
        if (const Value* length = out.value.find("Length");
            length != nullptr && length->is_integer() && length->integer >= 0) {
            const std::uint64_t declared = static_cast<std::uint64_t>(length->integer);
            if (declared <= data.size() - data_begin) {
                PdfParser check(data, data_begin + static_cast<std::size_t>(declared));
                if (check.consume_keyword("endstream")) {
                    data_end = data_begin + static_cast<std::size_t>(declared);
                    found_end = true;
                }
            }
        }
        if (!found_end) {
            std::size_t search = data_begin;
            while (true) {
                const std::size_t candidate = data.find("endstream", search);
                if (candidate == std::string_view::npos) {
                    return false;
                }
                const bool before_ok =
                    candidate == data_begin ||
                    data[candidate - 1] == '\n' || data[candidate - 1] == '\r';
                const bool after_ok =
                    candidate + 9 >= data.size() ||
                    is_pdf_space(static_cast<unsigned char>(data[candidate + 9])) ||
                    is_pdf_delimiter(static_cast<unsigned char>(data[candidate + 9]));
                if (before_ok && after_ok) {
                    data_end = candidate;
                    found_end = true;
                    break;
                }
                search = candidate + 9;
            }
        }
        if (!found_end) {
            return false;
        }
        while (data_end > data_begin &&
               (data[data_end - 1] == '\r' || data[data_end - 1] == '\n')) {
            --data_end;
        }
        out.has_stream = true;
        out.stream_data_begin = data_begin;
        out.stream_data_end = data_end;
        out.stream_span_begin = parser.position();
        parser.set_position(data_end);
        if (!parser.consume_keyword("endstream")) {
            return false;
        }
        out.stream_span_end = parser.position();
    }

    parser.skip_space();
    if (parser.consume_keyword("endobj")) {
        out.end = parser.position();
    } else {
        const std::size_t endobj = data.find("endobj", parser.position());
        out.end = endobj == std::string_view::npos ? parser.position()
                                                    : endobj + std::string_view("endobj").size();
    }
    out.valid = true;
    return true;
}

struct BitReader {
    std::string_view input;
    std::size_t position = 0;
    std::uint32_t buffer = 0;
    int bits = 0;

    bool read(int count, std::uint32_t& out) {
        if (count == 0) {
            out = 0;
            return true;
        }
        while (bits < count) {
            if (position >= input.size()) {
                return false;
            }
            buffer |= static_cast<std::uint32_t>(
                          static_cast<unsigned char>(input[position++]))
                      << bits;
            bits += 8;
        }
        out = buffer & ((1u << count) - 1u);
        buffer >>= count;
        bits -= count;
        return true;
    }

    void align() {
        buffer = 0;
        bits = 0;
    }
};

struct HuffmanTable {
    std::array<int, 16> counts{};
    std::vector<int> symbols;
    std::array<int, 16> first_code{};
    std::array<int, 16> first_symbol{};

    bool build(const std::vector<int>& lengths) {
        counts.fill(0);
        for (int length : lengths) {
            if (length < 0 || length > 15) {
                return false;
            }
            if (length > 0) {
                ++counts[static_cast<std::size_t>(length)];
            }
        }
        int code = 0;
        int index = 0;
        for (int length = 1; length <= 15; ++length) {
            first_code[static_cast<std::size_t>(length)] = code;
            first_symbol[static_cast<std::size_t>(length)] = index;
            code += counts[static_cast<std::size_t>(length)];
            index += counts[static_cast<std::size_t>(length)];
            code <<= 1;
        }
        symbols.assign(static_cast<std::size_t>(index), 0);
        std::array<int, 16> next = first_code;
        for (std::size_t symbol = 0; symbol < lengths.size(); ++symbol) {
            const int length = lengths[symbol];
            if (length == 0) {
                continue;
            }
            const int slot = next[static_cast<std::size_t>(length)]++ -
                             first_code[static_cast<std::size_t>(length)];
            symbols[static_cast<std::size_t>(first_symbol[static_cast<std::size_t>(length)] +
                                             slot)] = static_cast<int>(symbol);
        }
        return true;
    }

    bool decode(BitReader& reader, int& symbol) const {
        int code = 0;
        int first = 0;
        int index = 0;
        for (int length = 1; length <= 15; ++length) {
            std::uint32_t bit = 0;
            if (!reader.read(1, bit)) {
                return false;
            }
            code = (code << 1) | static_cast<int>(bit);
            const int count = counts[static_cast<std::size_t>(length)];
            if (code - first < count) {
                const std::size_t position =
                    static_cast<std::size_t>(index + (code - first));
                if (position >= symbols.size()) {
                    return false;
                }
                symbol = symbols[position];
                return true;
            }
            index += count;
            first = (first + count) << 1;
        }
        return false;
    }
};

inline bool inflate_deflate(std::string_view input, std::string& output) {
    BitReader reader{input};
    output.clear();

    static const std::array<int, 19> order = {16, 17, 18, 0, 8, 7, 9, 6, 10, 5,
                                              11, 4, 12, 3, 13, 2, 14, 1, 15};
    static const std::array<int, 29> length_base = {
        3,  4,  5,  6,  7,  8,  9,  10, 11, 13, 15, 17, 19, 23, 27,
        31, 35, 43, 51, 59, 67, 83, 99, 115, 131, 163, 195, 227, 258};
    static const std::array<int, 29> length_extra = {
        0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2,
        2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0};
    static const std::array<int, 30> distance_base = {
        1,    2,    3,    4,    5,    7,    9,    13,   17,   25,
        33,   49,   65,   97,   129,  193,  257,  385,  513,  769,
        1025, 1537, 2049, 3073, 4097, 6145, 8193, 12289, 16385, 24577};
    static const std::array<int, 30> distance_extra = {
        0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6,
        6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13};

    bool final_block = false;
    while (!final_block) {
        std::uint32_t final_bit = 0;
        std::uint32_t block_type = 0;
        if (!reader.read(1, final_bit) || !reader.read(2, block_type)) {
            return false;
        }
        final_block = final_bit != 0;

        if (block_type == 0) {
            reader.align();
            if (reader.position + 4 > input.size()) {
                return false;
            }
            const unsigned len = static_cast<unsigned char>(input[reader.position]) |
                                 (static_cast<unsigned>(static_cast<unsigned char>(
                                      input[reader.position + 1]))
                                  << 8);
            const unsigned nlen = static_cast<unsigned char>(input[reader.position + 2]) |
                                  (static_cast<unsigned>(static_cast<unsigned char>(
                                       input[reader.position + 3]))
                                   << 8);
            if ((len ^ 0xffffu) != nlen) {
                return false;
            }
            reader.position += 4;
            if (reader.position + len > input.size()) {
                return false;
            }
            output.append(input.substr(reader.position, len));
            reader.position += len;
            continue;
        }

        HuffmanTable literal;
        HuffmanTable distance;
        if (block_type == 1) {
            std::vector<int> literal_lengths(288, 0);
            for (int i = 0; i <= 143; ++i) {
                literal_lengths[static_cast<std::size_t>(i)] = 8;
            }
            for (int i = 144; i <= 255; ++i) {
                literal_lengths[static_cast<std::size_t>(i)] = 9;
            }
            for (int i = 256; i <= 279; ++i) {
                literal_lengths[static_cast<std::size_t>(i)] = 7;
            }
            for (int i = 280; i <= 287; ++i) {
                literal_lengths[static_cast<std::size_t>(i)] = 8;
            }
            std::vector<int> distance_lengths(32, 5);
            if (!literal.build(literal_lengths) || !distance.build(distance_lengths)) {
                return false;
            }
        } else if (block_type == 2) {
            std::uint32_t hlit = 0;
            std::uint32_t hdist = 0;
            std::uint32_t hclen = 0;
            if (!reader.read(5, hlit) || !reader.read(5, hdist) || !reader.read(4, hclen)) {
                return false;
            }
            const int literal_count = static_cast<int>(hlit) + 257;
            const int distance_count = static_cast<int>(hdist) + 1;
            const int code_count = static_cast<int>(hclen) + 4;
            std::vector<int> code_lengths(19, 0);
            for (int i = 0; i < code_count; ++i) {
                std::uint32_t length = 0;
                if (!reader.read(3, length)) {
                    return false;
                }
                code_lengths[static_cast<std::size_t>(order[static_cast<std::size_t>(i)])] =
                    static_cast<int>(length);
            }
            HuffmanTable code_table;
            if (!code_table.build(code_lengths)) {
                return false;
            }
            std::vector<int> lengths;
            lengths.reserve(static_cast<std::size_t>(literal_count + distance_count));
            while (static_cast<int>(lengths.size()) < literal_count + distance_count) {
                int symbol = 0;
                if (!code_table.decode(reader, symbol)) {
                    return false;
                }
                if (symbol <= 15) {
                    lengths.push_back(symbol);
                } else if (symbol == 16) {
                    if (lengths.empty()) {
                        return false;
                    }
                    std::uint32_t extra = 0;
                    if (!reader.read(2, extra)) {
                        return false;
                    }
                    const int repeat = static_cast<int>(extra) + 3;
                    const int previous = lengths.back();
                    lengths.insert(lengths.end(), static_cast<std::size_t>(repeat), previous);
                } else if (symbol == 17) {
                    std::uint32_t extra = 0;
                    if (!reader.read(3, extra)) {
                        return false;
                    }
                    lengths.insert(lengths.end(), static_cast<std::size_t>(extra) + 3, 0);
                } else if (symbol == 18) {
                    std::uint32_t extra = 0;
                    if (!reader.read(7, extra)) {
                        return false;
                    }
                    lengths.insert(lengths.end(), static_cast<std::size_t>(extra) + 11, 0);
                } else {
                    return false;
                }
                if (lengths.size() >
                    static_cast<std::size_t>(literal_count + distance_count)) {
                    return false;
                }
            }
            std::vector<int> literal_lengths(lengths.begin(),
                                             lengths.begin() + literal_count);
            std::vector<int> distance_lengths(lengths.begin() + literal_count, lengths.end());
            bool any_distance = false;
            for (int length : distance_lengths) {
                any_distance = any_distance || length != 0;
            }
            if (!literal.build(literal_lengths)) {
                return false;
            }
            if (any_distance) {
                if (!distance.build(distance_lengths)) {
                    return false;
                }
            } else {
                std::vector<int> single(1, 1);
                if (!distance.build(single)) {
                    return false;
                }
            }
        } else {
            return false;
        }

        while (true) {
            int symbol = 0;
            if (!literal.decode(reader, symbol)) {
                return false;
            }
            if (symbol < 256) {
                output.push_back(static_cast<char>(symbol));
                continue;
            }
            if (symbol == 256) {
                break;
            }
            if (symbol < 257 || symbol > 285) {
                return false;
            }
            const std::size_t length_index = static_cast<std::size_t>(symbol - 257);
            std::uint32_t extra = 0;
            if (length_extra[length_index] != 0 &&
                !reader.read(length_extra[length_index], extra)) {
                return false;
            }
            const int length = length_base[length_index] + static_cast<int>(extra);

            int distance_symbol = 0;
            if (!distance.decode(reader, distance_symbol) || distance_symbol < 0 ||
                distance_symbol >= 30) {
                return false;
            }
            std::uint32_t distance_extra_value = 0;
            if (distance_extra[static_cast<std::size_t>(distance_symbol)] != 0 &&
                !reader.read(distance_extra[static_cast<std::size_t>(distance_symbol)],
                             distance_extra_value)) {
                return false;
            }
            const int distance_value =
                distance_base[static_cast<std::size_t>(distance_symbol)] +
                static_cast<int>(distance_extra_value);
            if (distance_value <= 0 ||
                static_cast<std::size_t>(distance_value) > output.size()) {
                return false;
            }
            const std::size_t from = output.size() - static_cast<std::size_t>(distance_value);
            for (int i = 0; i < length; ++i) {
                output.push_back(output[from + static_cast<std::size_t>(i)]);
            }
        }
    }
    return true;
}

inline bool inflate_zlib(std::string_view input, std::string& output) {
    if (input.size() < 2) {
        return false;
    }
    const unsigned char cmf = static_cast<unsigned char>(input[0]);
    const unsigned char flg = static_cast<unsigned char>(input[1]);
    if ((cmf & 0x0f) != 8 || ((static_cast<unsigned>(cmf) << 8) | flg) % 31 != 0) {
        return false;
    }
    std::size_t begin = 2;
    if ((flg & 0x20) != 0) {
        begin += 4;
    }
    if (begin > input.size()) {
        return false;
    }
    return inflate_deflate(input.substr(begin), output);
}

inline bool decode_ascii_hex(std::string_view input, std::string& output) {
    output.clear();
    int high = -1;
    for (unsigned char c : input) {
        if (c == '>') {
            break;
        }
        if (is_pdf_space(c)) {
            continue;
        }
        if (!is_hex_digit(c)) {
            return false;
        }
        const int value = static_cast<int>(hex_value(c));
        if (high < 0) {
            high = value;
        } else {
            output.push_back(static_cast<char>((high << 4) | value));
            high = -1;
        }
    }
    if (high >= 0) {
        output.push_back(static_cast<char>(high << 4));
    }
    return true;
}

inline bool decode_ascii85(std::string_view input, std::string& output) {
    output.clear();
    std::size_t position = 0;
    if (input.size() >= 2 && input[0] == '<' && input[1] == '~') {
        position = 2;
    }
    std::uint64_t accumulator = 0;
    int count = 0;
    for (; position < input.size(); ++position) {
        const unsigned char c = static_cast<unsigned char>(input[position]);
        if (is_pdf_space(c)) {
            continue;
        }
        if (c == '~') {
            break;
        }
        if (c == 'z') {
            if (count != 0) {
                return false;
            }
            output.append(4, '\0');
            continue;
        }
        if (c < '!' || c > 'u') {
            return false;
        }
        accumulator = accumulator * 85 + static_cast<std::uint64_t>(c - '!');
        ++count;
        if (count == 5) {
            for (int shift = 24; shift >= 0; shift -= 8) {
                output.push_back(static_cast<char>((accumulator >> shift) & 0xff));
            }
            accumulator = 0;
            count = 0;
        }
    }
    if (count == 1) {
        return false;
    }
    if (count > 1) {
        const int original = count;
        while (count < 5) {
            accumulator = accumulator * 85 + 84;
            ++count;
        }
        for (int i = 0; i < original - 1; ++i) {
            output.push_back(static_cast<char>((accumulator >> (24 - i * 8)) & 0xff));
        }
    }
    return true;
}

inline bool decode_run_length(std::string_view input, std::string& output) {
    output.clear();
    std::size_t position = 0;
    while (position < input.size()) {
        const unsigned char length = static_cast<unsigned char>(input[position++]);
        if (length == 128) {
            return true;
        }
        if (length <= 127) {
            const std::size_t count = static_cast<std::size_t>(length) + 1;
            if (position + count > input.size()) {
                return false;
            }
            output.append(input.substr(position, count));
            position += count;
        } else {
            if (position >= input.size()) {
                return false;
            }
            const std::size_t count = 257u - length;
            output.append(count, input[position++]);
        }
    }
    return true;
}

inline int paeth_predictor(int left, int above, int upper_left) {
    const int prediction = left + above - upper_left;
    const int pa = std::abs(prediction - left);
    const int pb = std::abs(prediction - above);
    const int pc = std::abs(prediction - upper_left);
    if (pa <= pb && pa <= pc) {
        return left;
    }
    if (pb <= pc) {
        return above;
    }
    return upper_left;
}

inline bool apply_predictor(std::string_view input, int predictor, int colors, int bits,
                            int columns, std::string& output) {
    if (predictor <= 1 || columns <= 0 || colors <= 0 || bits <= 0) {
        output.assign(input);
        return true;
    }
    const std::size_t row_bytes =
        static_cast<std::size_t>((colors * bits * columns + 7) / 8);
    const std::size_t bytes_per_pixel =
        static_cast<std::size_t>(std::max(1, (colors * bits + 7) / 8));
    if (row_bytes == 0) {
        output.assign(input);
        return true;
    }

    if (predictor == 2) {
        output.assign(input);
        if (bits != 8) {
            return true;
        }
        for (std::size_t row = 0; row + row_bytes <= output.size(); row += row_bytes) {
            for (std::size_t i = bytes_per_pixel; i < row_bytes; ++i) {
                output[row + i] = static_cast<char>(
                    static_cast<unsigned char>(output[row + i]) +
                    static_cast<unsigned char>(output[row + i - bytes_per_pixel]));
            }
        }
        return true;
    }

    if (predictor < 10) {
        output.assign(input);
        return true;
    }

    const std::size_t encoded_row = row_bytes + 1;
    output.clear();
    std::string previous(row_bytes, '\0');
    std::size_t position = 0;
    while (position + encoded_row <= input.size()) {
        const int filter = static_cast<unsigned char>(input[position++]);
        std::string row(input.substr(position, row_bytes));
        position += row_bytes;
        for (std::size_t i = 0; i < row_bytes; ++i) {
            const int left = i >= bytes_per_pixel
                                 ? static_cast<unsigned char>(row[i - bytes_per_pixel])
                                 : 0;
            const int above = static_cast<unsigned char>(previous[i]);
            const int upper_left =
                i >= bytes_per_pixel
                    ? static_cast<unsigned char>(previous[i - bytes_per_pixel])
                    : 0;
            int prediction = 0;
            switch (filter) {
                case 0:
                    prediction = 0;
                    break;
                case 1:
                    prediction = left;
                    break;
                case 2:
                    prediction = above;
                    break;
                case 3:
                    prediction = (left + above) >> 1;
                    break;
                case 4:
                    prediction = paeth_predictor(left, above, upper_left);
                    break;
                default:
                    return false;
            }
            row[i] = static_cast<char>((static_cast<unsigned char>(row[i]) + prediction) &
                                       0xff);
        }
        output += row;
        previous = std::move(row);
    }
    return position == input.size();
}

}  // namespace detail
}  // namespace netsec

namespace netsec {
namespace detail {

struct RawXrefEntry {
    std::uint32_t number = 0;
    std::uint32_t generation = 0;
    XrefEntryType type = XrefEntryType::Free;
    std::uint64_t offset = 0;
    std::uint32_t stream_number = 0;
    std::uint32_t stream_index = 0;
    bool from_xref_stream = false;
};

struct XrefSection {
    std::uint64_t offset = 0;
    bool is_xref_stream = false;
    bool is_supplement = false;
    std::map<std::uint32_t, RawXrefEntry> entries;
    Value trailer;
    bool has_trailer = false;
    std::optional<std::uint64_t> previous;
    std::optional<std::uint64_t> xref_stream_offset;
    std::size_t revision = 0;
    std::size_t order = 0;
};

struct Revision {
    std::size_t index = 0;
    RevisionKind kind = RevisionKind::Original;
    std::uint64_t begin = 0;
    std::uint64_t end = 0;
    bool linearized = false;
    std::vector<std::size_t> section_indexes;
};

inline std::optional<std::uint64_t> linearized_file_length(std::string_view data) {
    const std::size_t marker =
        data.substr(0, std::min<std::size_t>(data.size(), 8192)).find("/Linearized");
    if (marker == std::string_view::npos) {
        return std::nullopt;
    }
    const std::size_t dictionary_begin = data.rfind("<<", marker);
    if (dictionary_begin == std::string_view::npos) {
        return std::nullopt;
    }
    PdfParser parser(data, dictionary_begin);
    Value dictionary;
    if (!parser.parse_value(dictionary) || !dictionary.is_dictionary()) {
        return std::nullopt;
    }
    const Value* length = dictionary.find("L");
    if (length == nullptr || !length->is_integer() || length->integer <= 0 ||
        static_cast<std::uint64_t>(length->integer) > data.size()) {
        return std::nullopt;
    }
    return static_cast<std::uint64_t>(length->integer);
}

inline bool decode_stream_data(const Value& dictionary, std::string_view raw,
                               std::string& output, int depth = 0);
inline bool parse_xref_chain(std::string_view data, std::vector<XrefSection>& sections,
                             std::string& error);
inline bool assign_revisions(std::string_view data, std::vector<XrefSection>& sections,
                             std::vector<Revision>& revisions, std::string& error);

struct ObjectVersion {
    std::uint32_t number = 0;
    std::uint16_t generation = 0;
    std::size_t revision = 0;
    std::size_t section_index = 0;
    RawXrefEntry entry;
    bool reused_object_number = false;
    bool parsed = false;
    bool parse_ok = false;
    PdfObject object;
    bool value_ready = false;
    bool value_ok = false;
    Value value;
    std::string type;
    std::string subtype;
    std::vector<std::string> impacts;
};

struct Document {
    std::string_view data;
    std::vector<XrefSection> sections;
    std::vector<Revision> revisions;
    std::vector<ObjectVersion> versions;
    std::map<std::uint32_t, std::size_t> latest_version;
    bool linearized = false;
    bool valid = false;
    std::string error;

    std::size_t revision_count() const { return revisions.size(); }

    const ObjectVersion* version_at(std::uint32_t number, std::size_t revision) const {
        const ObjectVersion* result = nullptr;
        for (const ObjectVersion& version : versions) {
            if (version.number == number && version.revision <= revision) {
                result = &version;
            }
        }
        return result;
    }

    Value trailer_at(std::size_t revision) const {
        Value merged;
        merged.kind = Value::Kind::Dictionary;
        for (const Revision& item : revisions) {
            if (item.index > revision) {
                break;
            }
            for (std::size_t section_index : item.section_indexes) {
                if (section_index >= sections.size() || !sections[section_index].has_trailer) {
                    continue;
                }
                for (const auto& [key, value] : sections[section_index].trailer.dictionary) {
                    if (key == "Prev" || key == "XRefStm") {
                        continue;
                    }
                    bool replaced = false;
                    for (auto& existing : merged.dictionary) {
                        if (existing.first == key) {
                            existing.second = value;
                            replaced = true;
                            break;
                        }
                    }
                    if (!replaced) {
                        merged.dictionary.emplace_back(key, value);
                    }
                }
            }
        }
        return merged;
    }

    const Value* root_reference(std::size_t revision) const {
        Value trailer = trailer_at(revision);
        return trailer.find("Root");
    }
};

inline void build_object_versions(Document& document) {
    document.versions.clear();
    document.latest_version.clear();
    for (const Revision& revision : document.revisions) {
        std::vector<std::size_t> section_indexes = revision.section_indexes;
        std::sort(section_indexes.begin(), section_indexes.end(), [&](std::size_t left,
                                                                      std::size_t right) {
            return document.sections[left].offset < document.sections[right].offset;
        });
        std::map<std::uint32_t, RawXrefEntry> merged;
        for (std::size_t section_index : section_indexes) {
            if (section_index >= document.sections.size()) {
                continue;
            }
            for (const auto& [number, entry] : document.sections[section_index].entries) {
                merged[number] = entry;
            }
        }
        for (const auto& [number, entry] : merged) {
            if (number == 0) {
                continue;
            }
            ObjectVersion version;
            version.number = number;
            version.generation = static_cast<std::uint16_t>(entry.generation);
            version.revision = revision.index;
            version.section_index = section_indexes.empty() ? 0 : section_indexes.back();
            version.entry = entry;
            auto previous = document.latest_version.find(number);
            if (previous != document.latest_version.end() &&
                document.versions[previous->second].revision < revision.index) {
                version.reused_object_number = true;
            }
            document.latest_version[number] = document.versions.size();
            document.versions.push_back(std::move(version));
        }
    }
}

class ObjectResolver {
public:
    ObjectResolver(const Document& document, std::size_t revision)
        : document_(document), revision_(revision) {
        for (const ObjectVersion& version : document_.versions) {
            if (version.revision <= revision_) {
                state_[version.number] = &version;
            }
        }
    }

    std::size_t revision() const { return revision_; }

    const std::map<std::uint32_t, const ObjectVersion*>& state() const { return state_; }

    const ObjectVersion* version(std::uint32_t number) const {
        auto found = state_.find(number);
        return found == state_.end() ? nullptr : found->second;
    }

    std::size_t revision_of(std::uint32_t number) const {
        const ObjectVersion* found = version(number);
        return found == nullptr ? 0 : found->revision;
    }

    const Value* dereference(const Value& value) {
        if (!value.is_reference()) {
            return &value;
        }
        return get(value.reference_number);
    }

    const Value* get(std::uint32_t number) {
        auto cached = resolved_.find(number);
        if (cached != resolved_.end()) {
            return cached->second ? &*cached->second : nullptr;
        }
        if (loading_.count(number) != 0) {
            return nullptr;
        }
        loading_.insert(number);
        struct Guard {
            std::set<std::uint32_t>& set;
            std::uint32_t value;
            ~Guard() { set.erase(value); }
        } guard{loading_, number};

        const ObjectVersion* found = version(number);
        if (found == nullptr || found->entry.type == XrefEntryType::Free) {
            resolved_[number] = std::nullopt;
            return nullptr;
        }

        std::optional<Value> value;
        if (found->entry.type == XrefEntryType::InUse) {
            const PdfObject* object = parse_direct(number);
            if (object != nullptr) {
                value = object->value;
            }
        } else if (found->entry.type == XrefEntryType::Compressed) {
            if (load_object_stream(found->entry.stream_number)) {
                auto iterator = object_stream_values_.find(found->entry.stream_number);
                if (iterator != object_stream_values_.end()) {
                    auto item = iterator->second.find(number);
                    if (item != iterator->second.end()) {
                        value = item->second;
                    }
                }
            }
        }
        resolved_[number] = value;
        return value ? &*resolved_[number] : nullptr;
    }

    bool stream(std::uint32_t number, std::string& output, const Value** dictionary) {
        auto cached = decoded_streams_.find(number);
        if (cached != decoded_streams_.end()) {
            if (dictionary != nullptr) {
                const ObjectVersion* found = version(number);
                *dictionary = found != nullptr ? &found->object.value : nullptr;
            }
            output = cached->second;
            return true;
        }
        const ObjectVersion* found = version(number);
        if (found == nullptr || found->entry.type != XrefEntryType::InUse) {
            return false;
        }
        const PdfObject* object = parse_direct(number);
        if (object == nullptr || !object->has_stream) {
            return false;
        }
        const std::string_view raw =
            document_.data.substr(object->stream_data_begin,
                                  object->stream_data_end - object->stream_data_begin);
        std::string decoded;
        if (!decode_stream_data(object->value, raw, decoded)) {
            return false;
        }
        decoded_streams_[number] = decoded;
        if (dictionary != nullptr) {
            *dictionary = &object->value;
        }
        output = std::move(decoded);
        return true;
    }

    const PdfObject* parse_direct(std::uint32_t number) {
        auto cached = parsed_objects_.find(number);
        if (cached != parsed_objects_.end()) {
            return cached->second ? &*cached->second : nullptr;
        }
        const ObjectVersion* found = version(number);
        if (found == nullptr || found->entry.type != XrefEntryType::InUse) {
            return nullptr;
        }
        PdfObject object;
        if (!parse_indirect_object(document_.data,
                                   static_cast<std::size_t>(found->entry.offset), object)) {
            parsed_objects_[number] = std::nullopt;
            return nullptr;
        }
        parsed_objects_[number] = std::move(object);
        return &*parsed_objects_[number];
    }

private:
    bool load_object_stream(std::uint32_t stream_number) {
        if (object_stream_values_.count(stream_number) != 0) {
            return true;
        }
        if (object_stream_loading_.count(stream_number) != 0) {
            return false;
        }
        object_stream_loading_.insert(stream_number);
        struct Guard {
            std::set<std::uint32_t>& set;
            std::uint32_t value;
            ~Guard() { set.erase(value); }
        } guard{object_stream_loading_, stream_number};

        std::string decoded;
        const Value* dictionary = nullptr;
        if (!stream(stream_number, decoded, &dictionary) || dictionary == nullptr) {
            return false;
        }
        const Value* n_value = dictionary->find("N");
        const Value* first_value = dictionary->find("First");
        if (n_value == nullptr || first_value == nullptr || !n_value->is_integer() ||
            !first_value->is_integer() || n_value->integer < 0 ||
            first_value->integer < 0 ||
            static_cast<std::uint64_t>(first_value->integer) > decoded.size()) {
            return false;
        }
        const std::size_t count = static_cast<std::size_t>(n_value->integer);
        const std::size_t first = static_cast<std::size_t>(first_value->integer);
        if (count > 10000000) {
            return false;
        }
        PdfParser header(std::string_view(decoded).substr(0, first));
        std::vector<std::pair<std::uint32_t, std::size_t>> entries;
        entries.reserve(count);
        for (std::size_t i = 0; i < count; ++i) {
            std::uint64_t object_number = 0;
            std::uint64_t relative = 0;
            if (!header.parse_unsigned(object_number) || !header.parse_unsigned(relative)) {
                return false;
            }
            if (first + relative > decoded.size()) {
                return false;
            }
            entries.emplace_back(static_cast<std::uint32_t>(object_number),
                                 first + static_cast<std::size_t>(relative));
        }
        std::map<std::uint32_t, Value> values;
        for (const auto& [object_number, position] : entries) {
            PdfParser parser(decoded, position);
            Value value;
            if (!parser.parse_value(value)) {
                return false;
            }
            values[object_number] = std::move(value);
        }
        object_stream_values_[stream_number] = std::move(values);
        return true;
    }

    const Document& document_;
    std::size_t revision_ = 0;
    std::map<std::uint32_t, const ObjectVersion*> state_;
    std::map<std::uint32_t, std::optional<Value>> resolved_;
    std::map<std::uint32_t, std::optional<PdfObject>> parsed_objects_;
    std::map<std::uint32_t, std::string> decoded_streams_;
    std::map<std::uint32_t, std::map<std::uint32_t, Value>> object_stream_values_;
    std::set<std::uint32_t> loading_;
    std::set<std::uint32_t> object_stream_loading_;
};

inline Document parse_document(std::string_view data) {
    Document document;
    document.data = data;
    if (data.size() < 8 || data.substr(0, 5) != "%PDF-") {
        document.error = "missing PDF header";
        return document;
    }
    if (!parse_xref_chain(data, document.sections, document.error)) {
        return document;
    }
    std::sort(document.sections.begin(), document.sections.end(),
              [](const XrefSection& left, const XrefSection& right) {
                  return left.offset < right.offset;
              });
    if (!assign_revisions(data, document.sections, document.revisions, document.error)) {
        return document;
    }
    build_object_versions(document);
    document.linearized = !document.revisions.empty() && document.revisions.front().linearized;
    document.valid = true;
    return document;
}

}  // namespace detail
}  // namespace netsec

namespace netsec {
namespace detail {

inline bool decode_stream_data(const Value& dictionary, std::string_view raw,
                               std::string& output, int depth) {
    if (depth > 8) {
        return false;
    }
    output.assign(raw);
    const Value* filter_value = dictionary.find("Filter");
    const Value* parameters_value = dictionary.find("DecodeParms");
    if (filter_value == nullptr) {
        return true;
    }

    std::vector<const Value*> filters;
    std::vector<const Value*> parameters;
    if (filter_value->is_name()) {
        filters.push_back(filter_value);
        if (parameters_value != nullptr) {
            parameters.push_back(parameters_value);
        } else {
            parameters.push_back(nullptr);
        }
    } else if (filter_value->is_array()) {
        for (const Value& filter : filter_value->array) {
            filters.push_back(&filter);
        }
        if (parameters_value != nullptr && parameters_value->is_array()) {
            for (std::size_t i = 0; i < filters.size(); ++i) {
                parameters.push_back(i < parameters_value->array.size()
                                         ? &parameters_value->array[i]
                                         : nullptr);
            }
        } else {
            parameters.assign(filters.size(), parameters_value);
        }
    } else {
        return false;
    }

    for (std::size_t index = 0; index < filters.size(); ++index) {
        const Value* filter = filters[index];
        if (filter == nullptr || !filter->is_name()) {
            return false;
        }
        const std::string& name = filter->text;
        std::string decoded;
        bool decoded_ok = false;
        if (name == "FlateDecode" || name == "Fl") {
            decoded_ok = inflate_zlib(output, decoded);
        } else if (name == "ASCIIHexDecode" || name == "AHx") {
            decoded_ok = decode_ascii_hex(output, decoded);
        } else if (name == "ASCII85Decode" || name == "A85") {
            decoded_ok = decode_ascii85(output, decoded);
        } else if (name == "RunLengthDecode" || name == "RL") {
            decoded_ok = decode_run_length(output, decoded);
        } else if (name == "Crypt") {
            decoded = output;
            decoded_ok = true;
        } else if (name == "DCTDecode" || name == "DCT" || name == "JPXDecode" ||
                   name == "CCITTFaxDecode" || name == "JBIG2Decode") {
            return true;
        } else if (name == "LZWDecode" || name == "LZW") {
            return false;
        } else {
            return false;
        }
        if (!decoded_ok) {
            return false;
        }
        output = std::move(decoded);

        const Value* parms = parameters[index];
        if (parms != nullptr && parms->is_dictionary()) {
            const Value* predictor = parms->find("Predictor");
            if (predictor != nullptr && predictor->is_integer() &&
                predictor->integer > 1) {
                const int colors =
                    static_cast<int>(parms->find("Colors") != nullptr
                                         ? parms->find("Colors")->integer_or(1)
                                         : 1);
                const int bits =
                    static_cast<int>(parms->find("BitsPerComponent") != nullptr
                                         ? parms->find("BitsPerComponent")->integer_or(8)
                                         : 8);
                const int columns =
                    static_cast<int>(parms->find("Columns") != nullptr
                                         ? parms->find("Columns")->integer_or(1)
                                         : 1);
                std::string predicted;
                if (!apply_predictor(output, static_cast<int>(predictor->integer), colors,
                                     bits, columns, predicted)) {
                    return false;
                }
                output = std::move(predicted);
            }
        }
    }
    return true;
}

inline std::uint64_t read_big_endian(std::string_view data, std::size_t position, int width) {
    std::uint64_t result = 0;
    for (int i = 0; i < width; ++i) {
        result = (result << 8) |
                 static_cast<unsigned char>(data[position + static_cast<std::size_t>(i)]);
    }
    return result;
}

inline bool parse_xref_at(std::string_view data, std::uint64_t offset, XrefSection& section,
                          std::string& error) {
    if (offset >= data.size()) {
        error = "cross-reference offset is outside the file";
        return false;
    }
    PdfParser parser(data, static_cast<std::size_t>(offset));
    parser.skip_space();
    section.offset = static_cast<std::uint64_t>(parser.position());

    if (parser.consume_keyword("xref")) {
        section.is_xref_stream = false;
        while (true) {
            parser.skip_space();
            if (parser.position() >= data.size()) {
                error = "unterminated cross-reference table";
                return false;
            }
            if (parser.consume_keyword("trailer")) {
                Value trailer;
                if (!parser.parse_value(trailer) || !trailer.is_dictionary()) {
                    error = "invalid trailer dictionary";
                    return false;
                }
                section.trailer = std::move(trailer);
                section.has_trailer = true;
                break;
            }
            std::uint64_t first = 0;
            std::uint64_t count = 0;
            if (!parser.parse_unsigned(first) || !parser.parse_unsigned(count)) {
                error = "invalid cross-reference subsection header";
                return false;
            }
            if (count > 20000000) {
                error = "unreasonable cross-reference subsection size";
                return false;
            }
            for (std::uint64_t i = 0; i < count; ++i) {
                std::uint64_t entry_offset = 0;
                std::uint64_t generation = 0;
                if (!parser.parse_unsigned(entry_offset) ||
                    !parser.parse_unsigned(generation)) {
                    error = "invalid cross-reference entry";
                    return false;
                }
                parser.skip_space();
                if (parser.position() >= data.size()) {
                    error = "truncated cross-reference entry";
                    return false;
                }
                const char type = data[parser.position()];
                if (type != 'n' && type != 'f') {
                    error = "invalid cross-reference entry type";
                    return false;
                }
                parser.set_position(parser.position() + 1);
                RawXrefEntry entry;
                entry.number = static_cast<std::uint32_t>(first + i);
                entry.generation = static_cast<std::uint32_t>(generation);
                entry.type = type == 'n' ? XrefEntryType::InUse : XrefEntryType::Free;
                entry.offset = entry_offset;
                section.entries[entry.number] = entry;
            }
        }
    } else {
        PdfObject object;
        if (!parse_indirect_object(data, static_cast<std::size_t>(offset), object)) {
            error = "offset does not point to an object or cross-reference table";
            return false;
        }
        if (!object.value.is_dictionary() || !object.has_stream) {
            error = "cross-reference stream object is not a stream";
            return false;
        }
        const Value* type = object.value.find("Type");
        const Value* widths = object.value.find("W");
        if ((type == nullptr || !type->is_name() || type->text != "XRef") &&
            widths == nullptr) {
            error = "object is not a cross-reference stream";
            return false;
        }
        if (widths == nullptr || !widths->is_array() || widths->array.size() < 3) {
            error = "cross-reference stream has no valid /W array";
            return false;
        }
        const Value* size_value = object.value.find("Size");
        const std::uint64_t size =
            size_value != nullptr ? static_cast<std::uint64_t>(size_value->integer_or(0)) : 0;
        std::vector<std::pair<std::uint64_t, std::uint64_t>> ranges;
        if (const Value* index = object.value.find("Index"); index != nullptr) {
            if (!index->is_array() || index->array.size() % 2 != 0) {
                error = "cross-reference stream has an invalid /Index array";
                return false;
            }
            for (std::size_t i = 0; i + 1 < index->array.size(); i += 2) {
                ranges.emplace_back(
                    static_cast<std::uint64_t>(index->array[i].integer_or(0)),
                    static_cast<std::uint64_t>(index->array[i + 1].integer_or(0)));
            }
        } else {
            ranges.emplace_back(0, size);
        }

        std::string decoded;
        const std::string_view raw =
            data.substr(object.stream_data_begin,
                        object.stream_data_end - object.stream_data_begin);
        if (!decode_stream_data(object.value, raw, decoded)) {
            error = "could not decode cross-reference stream";
            return false;
        }
        const int widths_value[3] = {
            static_cast<int>(widths->array[0].integer_or(0)),
            static_cast<int>(widths->array[1].integer_or(0)),
            static_cast<int>(widths->array[2].integer_or(0)),
        };
        if (widths_value[0] < 0 || widths_value[1] < 0 || widths_value[2] < 0 ||
            widths_value[0] > 8 || widths_value[1] > 8 || widths_value[2] > 8) {
            error = "cross-reference stream /W field is too wide";
            return false;
        }
        const std::size_t entry_width =
            static_cast<std::size_t>(widths_value[0] + widths_value[1] + widths_value[2]);
        std::size_t cursor = 0;
        for (const auto& [first, count] : ranges) {
            for (std::uint64_t i = 0; i < count; ++i) {
                if (cursor + entry_width > decoded.size()) {
                    error = "truncated cross-reference stream";
                    return false;
                }
                std::uint64_t fields[3] = {1, 0, 0};
                int field_cursor = 0;
                for (int field = 0; field < 3; ++field) {
                    if (widths_value[field] != 0) {
                        fields[field] =
                            read_big_endian(decoded, cursor + static_cast<std::size_t>(field_cursor),
                                            widths_value[field]);
                        field_cursor += widths_value[field];
                    }
                }
                cursor += entry_width;
                RawXrefEntry entry;
                entry.number = static_cast<std::uint32_t>(first + i);
                entry.from_xref_stream = true;
                entry.type = static_cast<XrefEntryType>(fields[0]);
                if (entry.type == XrefEntryType::Free) {
                    entry.generation = static_cast<std::uint32_t>(fields[2]);
                    entry.offset = 0;
                } else if (entry.type == XrefEntryType::InUse) {
                    entry.offset = fields[1];
                    entry.generation = static_cast<std::uint32_t>(fields[2]);
                } else if (entry.type == XrefEntryType::Compressed) {
                    entry.stream_number = static_cast<std::uint32_t>(fields[1]);
                    entry.stream_index = static_cast<std::uint32_t>(fields[2]);
                } else {
                    continue;
                }
                section.entries[entry.number] = entry;
            }
        }
        section.trailer = object.value;
        section.has_trailer = true;
        section.is_xref_stream = true;
    }

    if (section.has_trailer) {
        if (const Value* previous = section.trailer.find("Prev");
            previous != nullptr && previous->is_integer() && previous->integer >= 0) {
            section.previous = static_cast<std::uint64_t>(previous->integer);
        }
        if (const Value* stream = section.trailer.find("XRefStm");
            stream != nullptr && stream->is_integer() && stream->integer >= 0) {
            section.xref_stream_offset = static_cast<std::uint64_t>(stream->integer);
        }
    }
    return true;
}

inline bool find_last_startxref(std::string_view data, std::uint64_t& offset) {
    std::size_t search = data.size();
    while (search > 0) {
        const std::size_t found = data.rfind("startxref", search);
        if (found == std::string_view::npos) {
            return false;
        }
        PdfParser parser(data, found + 9);
        std::uint64_t candidate = 0;
        if (parser.parse_unsigned(candidate) && candidate < data.size()) {
            offset = candidate;
            return true;
        }
        search = found;
    }
    return false;
}

inline void merge_xref_entries(std::map<std::uint32_t, RawXrefEntry>& destination,
                               const std::map<std::uint32_t, RawXrefEntry>& source) {
    for (const auto& [number, entry] : source) {
        auto found = destination.find(number);
        if (found == destination.end()) {
            destination[number] = entry;
            continue;
        }
        if (found->second.type == XrefEntryType::Free &&
            entry.type != XrefEntryType::Free) {
            found->second = entry;
            continue;
        }
        if (!found->second.from_xref_stream && entry.from_xref_stream) {
            found->second = entry;
        }
    }
}

inline bool parse_xref_chain(std::string_view data, std::vector<XrefSection>& sections,
                             std::string& error) {
    std::uint64_t startxref = 0;
    if (!find_last_startxref(data, startxref)) {
        error = "no usable startxref";
        return false;
    }
    std::set<std::uint64_t> visited;
    std::vector<std::uint64_t> pending{startxref};
    while (!pending.empty()) {
        const std::uint64_t offset = pending.back();
        pending.pop_back();
        if (offset >= data.size() || visited.count(offset) != 0) {
            continue;
        }
        visited.insert(offset);
        XrefSection section;
        if (!parse_xref_at(data, offset, section, error)) {
            continue;
        }
        if (section.xref_stream_offset.has_value()) {
            XrefSection supplement;
            std::string ignored;
            if (parse_xref_at(data, *section.xref_stream_offset, supplement, ignored) &&
                supplement.is_xref_stream) {
                merge_xref_entries(section.entries, supplement.entries);
            }
        }
        if (section.previous.has_value()) {
            pending.push_back(*section.previous);
        }
        sections.push_back(std::move(section));
    }
    if (sections.empty()) {
        error = "no cross-reference sections could be parsed";
        return false;
    }
    return true;
}

inline bool is_inside_spans(std::uint64_t position,
                           const std::vector<std::pair<std::uint64_t, std::uint64_t>>& spans) {
    std::size_t low = 0;
    std::size_t high = spans.size();
    while (low < high) {
        const std::size_t middle = (low + high) / 2;
        if (position < spans[middle].first) {
            high = middle;
        } else if (position >= spans[middle].second) {
            low = middle + 1;
        } else {
            return true;
        }
    }
    return false;
}

inline std::vector<std::pair<std::uint64_t, std::uint64_t>> merge_spans(
    std::vector<std::pair<std::uint64_t, std::uint64_t>> spans) {
    std::sort(spans.begin(), spans.end());
    std::vector<std::pair<std::uint64_t, std::uint64_t>> merged;
    for (const auto& span : spans) {
        if (span.first >= span.second) {
            continue;
        }
        if (merged.empty() || span.first > merged.back().second) {
            merged.push_back(span);
        } else {
            merged.back().second = std::max(merged.back().second, span.second);
        }
    }
    return merged;
}

inline bool assign_revisions(std::string_view data, std::vector<XrefSection>& sections,
                             std::vector<Revision>& revisions, std::string& error) {
    std::vector<std::pair<std::uint64_t, std::uint64_t>> stream_spans;
    for (const XrefSection& section : sections) {
        for (const auto& [number, entry] : section.entries) {
            (void)number;
            if (entry.type != XrefEntryType::InUse || entry.offset >= data.size()) {
                continue;
            }
            PdfObject object;
            if (parse_indirect_object(data, static_cast<std::size_t>(entry.offset), object) &&
                object.has_stream) {
                stream_spans.emplace_back(object.stream_span_begin, object.stream_span_end);
            }
        }
    }
    stream_spans = merge_spans(std::move(stream_spans));

    std::vector<std::uint64_t> eof_ends;
    std::set<std::uint64_t> xref_offsets;
    for (const XrefSection& section : sections) {
        xref_offsets.insert(section.offset);
    }
    std::size_t search = 0;
    while (search < data.size()) {
        const std::size_t found = data.find("%%EOF", search);
        if (found == std::string_view::npos) {
            break;
        }
        search = found + 5;
        if (is_inside_spans(found, stream_spans)) {
            continue;
        }
        const bool starts_line =
            found == 0 || data[found - 1] == '\n' || data[found - 1] == '\r';
        if (!starts_line) {
            continue;
        }
        const std::size_t window_begin =
            found > 2048 ? found - 2048 : 0;
        const std::size_t startxref = data.rfind("startxref", found);
        bool has_matching_startxref = false;
        if (startxref != std::string_view::npos && startxref >= window_begin) {
            PdfParser parser(data, startxref + 9);
            std::uint64_t declared = 0;
            if (parser.parse_unsigned(declared)) {
                has_matching_startxref = xref_offsets.count(declared) != 0;
            }
        }
        if (!xref_offsets.empty() && !has_matching_startxref) {
            continue;
        }
        eof_ends.push_back(static_cast<std::uint64_t>(found + 5));
    }

    const std::optional<std::uint64_t> linearized_end = linearized_file_length(data);
    std::size_t internal_eofs = 1;
    if (linearized_end.has_value()) {
        internal_eofs = 0;
        for (std::uint64_t eof : eof_ends) {
            if (eof <= *linearized_end) {
                ++internal_eofs;
            }
        }
    } else if (eof_ends.empty()) {
        internal_eofs = 0;
    }

    std::size_t maximum_section_revision = 0;
    for (std::size_t index = 0; index < sections.size(); ++index) {
        const std::uint64_t offset = sections[index].offset;
        std::size_t revision = 0;
        for (std::uint64_t eof : eof_ends) {
            if (eof <= offset) {
                ++revision;
            } else {
                break;
            }
        }
        if (linearized_end.has_value()) {
            if (offset < *linearized_end) {
                revision = 0;
            } else if (revision >= internal_eofs) {
                revision = revision - internal_eofs + 1;
            } else {
                revision = 1;
            }
        }
        sections[index].revision = revision;
        sections[index].order = index;
        maximum_section_revision = std::max(maximum_section_revision, revision);
    }

    std::size_t eof_revision_count =
        linearized_end.has_value()
            ? 1 + (eof_ends.size() > internal_eofs
                       ? eof_ends.size() - internal_eofs
                       : 0)
            : std::max<std::size_t>(eof_ends.size(), 1);
    std::size_t revision_count =
        std::max(eof_revision_count, maximum_section_revision + 1);
    if (revision_count == 0) {
        revision_count = 1;
    }
    revisions.clear();
    for (std::size_t index = 0; index < revision_count; ++index) {
        Revision revision;
        revision.index = index;
        revision.kind = index == 0 ? RevisionKind::Original : RevisionKind::Incremental;
        if (index == 0) {
            revision.begin = 0;
            if (linearized_end.has_value()) {
                revision.end = *linearized_end;
            } else if (!eof_ends.empty()) {
                revision.end = eof_ends.front();
            } else {
                revision.end = static_cast<std::uint64_t>(data.size());
            }
            revisions.push_back(revision);
            continue;
        }
        const std::size_t first_increment_eof =
            linearized_end.has_value() ? internal_eofs : 1;
        const std::size_t eof_index = first_increment_eof + index - 1;
        revision.begin =
            index == 1 ? revisions.front().end
                       : (eof_index > 0 && eof_index - 1 < eof_ends.size()
                              ? eof_ends[eof_index - 1]
                              : static_cast<std::uint64_t>(data.size()));
        revision.end =
            eof_index < eof_ends.size() ? eof_ends[eof_index]
                                        : static_cast<std::uint64_t>(data.size());
        revisions.push_back(revision);
    }
    for (std::size_t index = 0; index < sections.size(); ++index) {
        const std::size_t revision = std::min(sections[index].revision, revision_count - 1);
        revisions[revision].section_indexes.push_back(index);
    }

    const bool linearized = data.substr(0, std::min<std::size_t>(data.size(), 4096))
                                .find("/Linearized") != std::string_view::npos;
    revisions.front().linearized = linearized;
    if (revisions.empty()) {
        error = "no revisions";
        return false;
    }
    return true;
}

}  // namespace detail
}  // namespace netsec

namespace netsec {
namespace detail {

inline std::uint64_t fnv1a(std::string_view input) {
    std::uint64_t hash = 1469598103934665603ULL;
    for (unsigned char c : input) {
        hash ^= c;
        hash *= 1099511628211ULL;
    }
    return hash;
}

inline std::string hex_hash(std::uint64_t value) {
    static constexpr char digits[] = "0123456789abcdef";
    std::string result(16, '0');
    for (int i = 15; i >= 0; --i) {
        result[static_cast<std::size_t>(i)] = digits[value & 0x0f];
        value >>= 4;
    }
    return result;
}

inline void gather_references(const Value& value, std::set<std::uint32_t>& output,
                              int depth = 0) {
    if (depth > 32) {
        return;
    }
    if (value.is_reference()) {
        output.insert(value.reference_number);
    } else if (value.is_array()) {
        for (const Value& item : value.array) {
            gather_references(item, output, depth + 1);
        }
    } else if (value.is_dictionary()) {
        for (const auto& [key, item] : value.dictionary) {
            (void)key;
            gather_references(item, output, depth + 1);
        }
    }
}

inline std::string decode_pdf_text(std::string_view input) {
    std::string output;
    if (input.size() >= 2 && static_cast<unsigned char>(input[0]) == 0xfe &&
        static_cast<unsigned char>(input[1]) == 0xff) {
        output.reserve((input.size() - 2) / 2);
        for (std::size_t i = 2; i + 1 < input.size(); i += 2) {
            const unsigned codepoint =
                (static_cast<unsigned char>(input[i]) << 8) |
                static_cast<unsigned char>(input[i + 1]);
            if (codepoint < 0x80) {
                output.push_back(static_cast<char>(codepoint));
            } else if (codepoint < 0x800) {
                output.push_back(static_cast<char>(0xc0 | (codepoint >> 6)));
                output.push_back(static_cast<char>(0x80 | (codepoint & 0x3f)));
            } else {
                output.push_back(static_cast<char>(0xe0 | (codepoint >> 12)));
                output.push_back(static_cast<char>(0x80 | ((codepoint >> 6) & 0x3f)));
                output.push_back(static_cast<char>(0x80 | (codepoint & 0x3f)));
            }
        }
        return output;
    }
    if (input.size() >= 2 && static_cast<unsigned char>(input[0]) == 0xff &&
        static_cast<unsigned char>(input[1]) == 0xfe) {
        output.reserve((input.size() - 2) / 2);
        for (std::size_t i = 2; i + 1 < input.size(); i += 2) {
            const unsigned codepoint =
                static_cast<unsigned char>(input[i]) |
                (static_cast<unsigned char>(input[i + 1]) << 8);
            if (codepoint < 0x80) {
                output.push_back(static_cast<char>(codepoint));
            } else if (codepoint < 0x800) {
                output.push_back(static_cast<char>(0xc0 | (codepoint >> 6)));
                output.push_back(static_cast<char>(0x80 | (codepoint & 0x3f)));
            } else {
                output.push_back(static_cast<char>(0xe0 | (codepoint >> 12)));
                output.push_back(static_cast<char>(0x80 | ((codepoint >> 6) & 0x3f)));
                output.push_back(static_cast<char>(0x80 | (codepoint & 0x3f)));
            }
        }
        return output;
    }
    output.assign(input);
    return output;
}

inline std::string normalize_text(std::string_view input) {
    std::string output;
    output.reserve(input.size());
    bool pending_space = false;
    for (unsigned char c : input) {
        if (c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f') {
            pending_space = !output.empty();
            continue;
        }
        if (pending_space) {
            output.push_back(' ');
            pending_space = false;
        }
        output.push_back(static_cast<char>(c));
    }
    return output;
}

struct ContentExtraction {
    std::string text;
    std::vector<std::string> xobject_names;
};

inline std::string decode_string_value(const Value& value) {
    return value.is_string() ? decode_pdf_text(value.text) : std::string{};
}

inline ContentExtraction extract_content_text(std::string_view content) {
    ContentExtraction result;
    std::size_t position = 0;
    std::string last_string;
    std::string last_name;
    std::vector<std::string> last_array_strings;

    auto is_word_char = [](unsigned char c) {
        return !is_pdf_space(c) && !is_pdf_delimiter(c);
    };
    auto starts_word = [&](std::size_t at, std::string_view word) {
        if (content.substr(at, word.size()) != word) {
            return false;
        }
        const std::size_t after = at + word.size();
        return after >= content.size() ||
               !is_word_char(static_cast<unsigned char>(content[after]));
    };

    while (position < content.size()) {
        while (position < content.size()) {
            const unsigned char c = static_cast<unsigned char>(content[position]);
            if (is_pdf_space(c)) {
                ++position;
                continue;
            }
            if (c == '%') {
                while (position < content.size() && content[position] != '\n' &&
                       content[position] != '\r') {
                    ++position;
                }
                continue;
            }
            break;
        }
        if (position >= content.size()) {
            break;
        }

        if (starts_word(position, "BI")) {
            const std::size_t id = content.find("ID", position + 2);
            if (id == std::string_view::npos) {
                break;
            }
            std::size_t search = id + 2;
            if (search < content.size() &&
                is_pdf_space(static_cast<unsigned char>(content[search]))) {
                ++search;
            }
            bool skipped = false;
            while (true) {
                const std::size_t ei = content.find("EI", search);
                if (ei == std::string_view::npos) {
                    break;
                }
                const bool before_ok =
                    ei == 0 || is_pdf_space(static_cast<unsigned char>(content[ei - 1]));
                const bool after_ok =
                    ei + 2 >= content.size() ||
                    is_pdf_space(static_cast<unsigned char>(content[ei + 2])) ||
                    is_pdf_delimiter(static_cast<unsigned char>(content[ei + 2]));
                if (before_ok && after_ok) {
                    position = ei + 2;
                    skipped = true;
                    break;
                }
                search = ei + 2;
            }
            if (!skipped) {
                break;
            }
            continue;
        }

        const char first = content[position];
        if (first == '(' || first == '<' || first == '[' || first == '/') {
            PdfParser parser(content, position);
            Value value;
            if (!parser.parse_value(value)) {
                ++position;
                continue;
            }
            position = parser.position();
            if (value.is_string()) {
                last_string = decode_pdf_text(value.text);
                last_array_strings.clear();
            } else if (value.is_name()) {
                last_name = value.text;
                last_array_strings.clear();
            } else if (value.is_array()) {
                last_array_strings.clear();
                for (const Value& item : value.array) {
                    if (item.is_string()) {
                        last_array_strings.push_back(decode_pdf_text(item.text));
                    }
                }
                last_string.clear();
            }
            continue;
        }

        if (first == '+' || first == '-' || first == '.' ||
            std::isdigit(static_cast<unsigned char>(first))) {
            PdfParser parser(content, position);
            Value value;
            if (parser.parse_number_token(value)) {
                position = parser.position();
            } else {
                ++position;
            }
            continue;
        }

        const std::size_t word_begin = position;
        while (position < content.size() &&
               is_word_char(static_cast<unsigned char>(content[position]))) {
            ++position;
        }
        if (word_begin == position) {
            ++position;
            continue;
        }
        const std::string_view word = content.substr(word_begin, position - word_begin);
        if (word == "Tj" || word == "'" || word == "\"") {
            if (!last_string.empty()) {
                if (!result.text.empty()) {
                    result.text.push_back(' ');
                }
                result.text += last_string;
            }
        } else if (word == "TJ") {
            for (const std::string& item : last_array_strings) {
                if (item.empty()) {
                    continue;
                }
                if (!result.text.empty()) {
                    result.text.push_back(' ');
                }
                result.text += item;
            }
        } else if (word == "Do") {
            if (!last_name.empty()) {
                result.xobject_names.push_back(last_name);
            }
        }
        last_string.clear();
        last_name.clear();
        last_array_strings.clear();
    }
    result.text = normalize_text(result.text);
    return result;
}

struct PageSnapshot {
    std::uint32_t object_number = 0;
    std::string text;
    std::string geometry;
    std::string content_hash;
    std::vector<std::uint32_t> annotations;
    std::set<std::uint32_t> dependencies;
};

struct AnnotationSnapshot {
    std::uint32_t object_number = 0;
    std::uint32_t page = 0;
    std::string subtype;
    std::string name;
    std::string contents;
    std::string field_name;
    std::string rect;
    long long flags = 0;
    bool hidden = false;
    bool widget = false;
    bool file_attachment = false;
    bool signature = false;
    std::string fingerprint;
    std::set<std::uint32_t> dependencies;
};

struct FormFieldSnapshot {
    std::uint32_t object_number = 0;
    std::string name;
    std::string field_type;
    std::string value;
    std::string default_value;
    bool signature = false;
    std::string fingerprint;
    std::set<std::uint32_t> dependencies;
};

struct AttachmentSnapshot {
    std::string name;
    std::uint32_t object_number = 0;
    std::string fingerprint;
    std::set<std::uint32_t> dependencies;
};

struct Snapshot {
    bool ok = false;
    std::vector<PageSnapshot> pages;
    std::vector<AnnotationSnapshot> annotations;
    std::vector<FormFieldSnapshot> forms;
    std::vector<AttachmentSnapshot> attachments;
    std::string catalog_fingerprint;
    std::set<std::uint32_t> display_objects;
    std::set<std::uint32_t> annotation_objects;
    std::set<std::uint32_t> form_objects;
    std::set<std::uint32_t> attachment_objects;
};

inline std::string value_display(const Value& value, ObjectResolver& resolver, int depth = 0) {
    if (depth > 16) {
        return "...";
    }
    if (value.is_reference()) {
        const Value* resolved = resolver.get(value.reference_number);
        if (resolved == nullptr) {
            return std::to_string(value.reference_number) + " R";
        }
        return value_display(*resolved, resolver, depth + 1);
    }
    if (value.is_string()) {
        return decode_pdf_text(value.text);
    }
    if (value.is_name()) {
        return value.text;
    }
    if (value.is_dictionary()) {
        if (const Value* byte_range = value.find("ByteRange");
            byte_range != nullptr && byte_range->is_array()) {
            return "signature";
        }
        std::string result;
        for (const auto& [key, item] : value.dictionary) {
            if (!result.empty()) {
                result.push_back(' ');
            }
            result += "/" + key + "=" + value_display(item, resolver, depth + 1);
        }
        return result;
    }
    if (value.is_array()) {
        std::string result;
        for (const Value& item : value.array) {
            if (!result.empty()) {
                result.push_back(' ');
            }
            result += value_display(item, resolver, depth + 1);
        }
        return result;
    }
    return value_text(value);
}

class SnapshotBuilder {
public:
    SnapshotBuilder(const Document& document, std::size_t revision)
        : document_(document), resolver_(document, revision), revision_(revision) {}

    Snapshot build() {
        Snapshot snapshot;
        Value trailer = document_.trailer_at(revision_);
        const Value* root_reference = trailer.find("Root");
        if (root_reference == nullptr) {
            return snapshot;
        }
        std::uint32_t catalog_number = 0;
        const Value* catalog = nullptr;
        if (root_reference->is_reference()) {
            catalog_number = root_reference->reference_number;
            catalog = resolver_.get(catalog_number);
        } else if (root_reference->is_dictionary()) {
            catalog = root_reference;
        }
        if (catalog == nullptr || !catalog->is_dictionary()) {
            return snapshot;
        }
        if (catalog_number != 0) {
            snapshot.display_objects.insert(catalog_number);
        }

        std::string catalog_fingerprint;
        static constexpr std::array<const char*, 14> catalog_keys = {
            "Pages",           "AcroForm",       "Names",       "Outlines",
            "OpenAction",      "PageMode",       "PageLayout",  "ViewerPreferences",
            "StructTreeRoot",  "MarkInfo",       "Lang",        "Metadata",
            "Perms",           "Version",
        };
        for (const char* key : catalog_keys) {
            if (const Value* value = catalog->find(key); value != nullptr) {
                catalog_fingerprint += std::string(key) + "=" + value_text(*value) + ";";
            }
        }
        snapshot.catalog_fingerprint = catalog_fingerprint;

        std::set<std::uint32_t> visited_pages;
        const Value* pages = catalog->find("Pages");
        if (pages != nullptr) {
            walk_page_node(*pages, 0, nullptr, nullptr, nullptr, nullptr, snapshot,
                           visited_pages, 0);
        }
        build_orphan_annotations(snapshot);
        build_forms(*catalog, snapshot);
        build_attachments(*catalog, snapshot);
        snapshot.ok = true;
        return snapshot;
    }

    ObjectResolver& resolver() { return resolver_; }

private:
    struct InheritedPage {
        const Value* media_box = nullptr;
        const Value* crop_box = nullptr;
        const Value* rotate = nullptr;
        const Value* resources = nullptr;
    };

    void mark_display_tree(const Value& value, std::set<std::uint32_t>& dependencies,
                           int depth = 0) {
        if (depth > 16) {
            return;
        }
        if (value.is_reference()) {
            dependencies.insert(value.reference_number);
            return;
        }
        if (value.is_array()) {
            for (const Value& item : value.array) {
                mark_display_tree(item, dependencies, depth + 1);
            }
        } else if (value.is_dictionary()) {
            for (const auto& [key, item] : value.dictionary) {
                (void)key;
                mark_display_tree(item, dependencies, depth + 1);
            }
        }
    }

    std::string page_geometry(const Value* media, const Value* crop,
                              const Value* rotate) {
        std::string geometry;
        if (media != nullptr) {
            geometry += "media=" + value_text(*media) + ";";
        }
        if (crop != nullptr) {
            geometry += "crop=" + value_text(*crop) + ";";
        }
        if (rotate != nullptr) {
            geometry += "rotate=" + value_text(*rotate) + ";";
        }
        return geometry;
    }

    void extract_stream(std::uint32_t object_number, PageSnapshot& page, int depth) {
        if (object_number == 0 || depth > 12 ||
            page.dependencies.count(object_number) != 0) {
            return;
        }
        page.dependencies.insert(object_number);
        std::string decoded;
        if (!resolver_.stream(object_number, decoded, nullptr)) {
            return;
        }
        page.content_hash += hex_hash(fnv1a(decoded));
        ContentExtraction extraction = extract_content_text(decoded);
        if (!extraction.text.empty()) {
            if (!page.text.empty()) {
                page.text.push_back(' ');
            }
            page.text += extraction.text;
        }
        (void)extraction.xobject_names;
    }

    void extract_xobject_resources(const Value* resources, PageSnapshot& page, int depth) {
        if (resources == nullptr || depth > 8) {
            return;
        }
        const Value* resolved = resolver_.dereference(*resources);
        if (resolved == nullptr || !resolved->is_dictionary()) {
            return;
        }
        const Value* xobjects = resolved->find("XObject");
        if (xobjects == nullptr) {
            return;
        }
        const Value* xobject_dictionary = resolver_.dereference(*xobjects);
        if (xobject_dictionary == nullptr || !xobject_dictionary->is_dictionary()) {
            return;
        }
        for (const auto& [name, item] : xobject_dictionary->dictionary) {
            (void)name;
            if (!item.is_reference()) {
                continue;
            }
            const std::uint32_t object_number = item.reference_number;
            page.dependencies.insert(object_number);
            const Value* xobject = resolver_.get(object_number);
            if (xobject == nullptr || !xobject->is_dictionary()) {
                continue;
            }
            const Value* subtype = xobject->find("Subtype");
            if (subtype != nullptr && subtype->is_name() && subtype->text == "Form") {
                extract_stream(object_number, page, depth + 1);
                if (const Value* inner = xobject->find("Resources"); inner != nullptr) {
                    extract_xobject_resources(inner, page, depth + 1);
                }
            }
        }
    }

    void walk_page_node(const Value& node, std::uint32_t object_number, const Value* media,
                        const Value* crop, const Value* rotate, const Value* resources,
                        Snapshot& snapshot, std::set<std::uint32_t>& visited, int depth) {
        if (depth > 64) {
            return;
        }
        const Value* resolved = resolver_.dereference(node);
        if (resolved == nullptr || !resolved->is_dictionary()) {
            return;
        }
        if (object_number != 0) {
            if (visited.count(object_number) != 0) {
                return;
            }
            visited.insert(object_number);
        }
        const Value* type = resolved->find("Type");
        const std::string type_name = type != nullptr && type->is_name() ? type->text : "";
        const Value* own_media = resolved->find("MediaBox");
        const Value* own_crop = resolved->find("CropBox");
        const Value* own_rotate = resolved->find("Rotate");
        const Value* own_resources = resolved->find("Resources");
        const InheritedPage inherited{
            own_media != nullptr ? own_media : media,
            own_crop != nullptr ? own_crop : crop,
            own_rotate != nullptr ? own_rotate : rotate,
            own_resources != nullptr ? own_resources : resources,
        };

        const Value* kids = resolved->find("Kids");
        if (kids != nullptr && !(type != nullptr && type->is_name() && type->text == "Page")) {
            const Value* kids_array = resolver_.dereference(*kids);
            if (kids_array != nullptr && kids_array->is_array()) {
                if (object_number != 0) {
                    snapshot.display_objects.insert(object_number);
                }
                for (const Value& kid : kids_array->array) {
                    std::uint32_t kid_number = 0;
                    if (kid.is_reference()) {
                        kid_number = kid.reference_number;
                    }
                    walk_page_node(kid, kid_number, inherited.media_box, inherited.crop_box,
                                   inherited.rotate, inherited.resources, snapshot, visited,
                                   depth + 1);
                }
            }
            return;
        }

        PageSnapshot page;
        page.object_number = object_number;
        if (object_number != 0) {
            page.dependencies.insert(object_number);
            snapshot.display_objects.insert(object_number);
        }
        page.geometry = page_geometry(inherited.media_box, inherited.crop_box,
                                      inherited.rotate);
        if (inherited.resources != nullptr) {
            mark_display_tree(*inherited.resources, page.dependencies);
        }

        const Value* contents = resolved->find("Contents");
        if (contents != nullptr) {
            if (contents->is_reference()) {
                extract_stream(contents->reference_number, page, 0);
            } else {
                const Value* content_value = resolver_.dereference(*contents);
                if (content_value != nullptr && content_value->is_array()) {
                    for (const Value& item : content_value->array) {
                        if (item.is_reference()) {
                            extract_stream(item.reference_number, page, 0);
                        }
                    }
                }
            }
        }
        extract_xobject_resources(inherited.resources, page, 0);
        page.text = normalize_text(page.text);

        const Value* annots = resolved->find("Annots");
        if (annots != nullptr) {
            const Value* annots_array = resolver_.dereference(*annots);
            if (annots_array != nullptr && annots_array->is_array()) {
                for (const Value& item : annots_array->array) {
                    if (item.is_reference()) {
                        page.annotations.push_back(item.reference_number);
                    }
                }
            }
        }
        for (std::uint32_t dependency : page.dependencies) {
            snapshot.display_objects.insert(dependency);
        }
        snapshot.pages.push_back(std::move(page));
    }

    bool annotation_is_signature(const Value& dictionary, std::uint32_t object_number) {
        (void)object_number;
        const Value* type = dictionary.find("FT");
        if (type != nullptr && type->is_name() && type->text == "Sig") {
            return true;
        }
        const Value* value = dictionary.find("V");
        if (value != nullptr) {
            const Value* resolved = resolver_.dereference(*value);
            if (resolved != nullptr && resolved->is_dictionary() &&
                resolved->find("ByteRange") != nullptr) {
                return true;
            }
        }
        const Value* parent = dictionary.find("Parent");
        if (parent != nullptr) {
            const Value* parent_value = resolver_.dereference(*parent);
            if (parent_value != nullptr && parent_value->is_dictionary()) {
                return annotation_is_signature(*parent_value, 0);
            }
        }
        return false;
    }

    void mark_annotation_support(const Value& value, std::set<std::uint32_t>& dependencies,
                                 int depth, std::set<std::uint32_t>& expanded) {
        if (depth > 6) {
            return;
        }
        if (value.is_reference()) {
            dependencies.insert(value.reference_number);
            if (!expanded.insert(value.reference_number).second) {
                return;
            }
            const Value* target = resolver_.get(value.reference_number);
            if (target != nullptr) {
                mark_annotation_support(*target, dependencies, depth + 1, expanded);
            }
        } else if (value.is_array()) {
            for (const Value& item : value.array) {
                mark_annotation_support(item, dependencies, depth + 1, expanded);
            }
        } else if (value.is_dictionary()) {
            for (const auto& [key, item] : value.dictionary) {
                if (key == "Parent" || key == "P") {
                    continue;
                }
                mark_annotation_support(item, dependencies, depth + 1, expanded);
            }
        }
    }

    void mark_annotation_support(const Value& value,
                                 std::set<std::uint32_t>& dependencies) {
        std::set<std::uint32_t> expanded;
        mark_annotation_support(value, dependencies, 0, expanded);
    }

    AnnotationSnapshot make_annotation(std::uint32_t object_number,
                                       std::uint32_t page_number) {
        AnnotationSnapshot annotation;
        annotation.object_number = object_number;
        annotation.page = page_number;
        const Value* dictionary = resolver_.get(object_number);
        if (dictionary == nullptr || !dictionary->is_dictionary()) {
            return annotation;
        }
        annotation.dependencies.insert(object_number);
        if (const Value* subtype = dictionary->find("Subtype");
            subtype != nullptr && subtype->is_name()) {
            annotation.subtype = subtype->text;
        }
        if (const Value* name = dictionary->find("NM");
            name != nullptr && name->is_string()) {
            annotation.name = decode_pdf_text(name->text);
        }
        if (const Value* contents = dictionary->find("Contents");
            contents != nullptr && contents->is_string()) {
            annotation.contents = decode_pdf_text(contents->text);
        }
        if (const Value* title = dictionary->find("T");
            title != nullptr && title->is_string()) {
            annotation.field_name = decode_pdf_text(title->text);
        }
        if (const Value* rect = dictionary->find("Rect"); rect != nullptr) {
            annotation.rect = value_text(*rect);
        }
        if (const Value* flags = dictionary->find("F"); flags != nullptr) {
            annotation.flags = flags->integer_or(0);
        }
        if (const Value* parent = dictionary->find("Parent"); parent != nullptr) {
            const Value* parent_dictionary = resolver_.dereference(*parent);
            if (parent_dictionary != nullptr && parent_dictionary->is_dictionary()) {
                if (const Value* parent_flags = parent_dictionary->find("F");
                    parent_flags != nullptr) {
                    annotation.flags |= parent_flags->integer_or(0);
                }
            }
        }
        annotation.hidden = (annotation.flags & 2) != 0 || (annotation.flags & 32) != 0;
        annotation.widget = annotation.subtype == "Widget";
        annotation.file_attachment = annotation.subtype == "FileAttachment" ||
                                      dictionary->find("FS") != nullptr;
        annotation.signature = annotation_is_signature(*dictionary, object_number);

        static constexpr std::array<const char*, 10> keys = {
            "Subtype", "Rect", "F", "NM", "T", "Contents", "AP", "A", "FS", "V",
        };
        for (const char* key : keys) {
            if (const Value* value = dictionary->find(key); value != nullptr) {
                annotation.fingerprint += std::string(key) + "=" + value_text(*value) + ";";
                gather_references(*value, annotation.dependencies);
            }
        }
        if (const Value* appearance = dictionary->find("AP"); appearance != nullptr) {
            mark_annotation_support(*appearance, annotation.dependencies);
        }
        if (const Value* resources = dictionary->find("DR"); resources != nullptr) {
            mark_annotation_support(*resources, annotation.dependencies);
        }
        for (std::uint32_t dependency : annotation.dependencies) {
            snapshot_annotation_objects_.insert(dependency);
        }
        return annotation;
    }

    void build_orphan_annotations(Snapshot& snapshot) {
        std::set<std::uint32_t> known;
        for (const PageSnapshot& page : snapshot.pages) {
            known.insert(page.annotations.begin(), page.annotations.end());
        }
        for (const auto& [number, version] : resolver_.state()) {
            (void)version;
            if (known.count(number) != 0) {
                continue;
            }
            const Value* value = resolver_.get(number);
            if (value == nullptr || !value->is_dictionary()) {
                continue;
            }
            const Value* type = value->find("Type");
            if (type == nullptr || !type->is_name() || type->text != "Annot") {
                continue;
            }
            snapshot.annotations.push_back(make_annotation(number, 0));
        }
        for (const PageSnapshot& page : snapshot.pages) {
            for (std::uint32_t annotation : page.annotations) {
                snapshot.annotations.push_back(make_annotation(annotation, page.object_number));
            }
        }
        for (std::uint32_t number : snapshot_annotation_objects_) {
            snapshot.annotation_objects.insert(number);
        }
    }

    void walk_field(const Value& field, std::uint32_t object_number, const std::string& prefix,
                    const std::string& inherited_type, const Value* inherited_value,
                    const Value* inherited_default, Snapshot& snapshot, int depth) {
        if (depth > 32) {
            return;
        }
        const Value* dictionary = resolver_.dereference(field);
        if (dictionary == nullptr || !dictionary->is_dictionary()) {
            return;
        }
        std::string name = prefix;
        if (const Value* title = dictionary->find("T");
            title != nullptr && title->is_string()) {
            if (!name.empty()) {
                name.push_back('.');
            }
            name += decode_pdf_text(title->text);
        }
        std::string field_type = inherited_type;
        if (const Value* type = dictionary->find("FT");
            type != nullptr && type->is_name()) {
            field_type = type->text;
        }
        const Value* value = dictionary->find("V");
        if (value == nullptr) {
            value = inherited_value;
        }
        const Value* default_value = dictionary->find("DV");
        if (default_value == nullptr) {
            default_value = inherited_default;
        }

        const Value* kids = dictionary->find("Kids");
        const Value* kids_array = kids != nullptr ? resolver_.dereference(*kids) : nullptr;
        if (kids_array != nullptr && kids_array->is_array()) {
            if (object_number != 0) {
                snapshot.form_objects.insert(object_number);
            }
            for (const Value& kid : kids_array->array) {
                std::uint32_t kid_number = kid.is_reference() ? kid.reference_number : 0;
                walk_field(kid, kid_number, name, field_type,
                           dictionary->find("V") != nullptr ? dictionary->find("V")
                                                            : inherited_value,
                           dictionary->find("DV") != nullptr ? dictionary->find("DV")
                                                             : inherited_default,
                           snapshot, depth + 1);
            }
            if (field_type != "Sig" && value == nullptr && default_value == nullptr) {
                return;
            }
        }

        FormFieldSnapshot form;
        form.object_number = object_number;
        form.name = name;
        form.field_type = field_type;
        if (value != nullptr) {
            form.value = value_display(*value, resolver_);
        }
        if (default_value != nullptr) {
            form.default_value = value_display(*default_value, resolver_);
        }
        form.signature = field_type == "Sig" || form.value == "signature";
        form.dependencies.insert(object_number);
        if (value != nullptr) {
            gather_references(*value, form.dependencies);
            form.fingerprint += "V=" + value_text(*value) + ";";
        }
        if (default_value != nullptr) {
            gather_references(*default_value, form.dependencies);
            form.fingerprint += "DV=" + value_text(*default_value) + ";";
        }
        form.fingerprint += "FT=" + field_type + ";T=" + name + ";";
        for (std::uint32_t dependency : form.dependencies) {
            snapshot.form_objects.insert(dependency);
        }
        snapshot.forms.push_back(std::move(form));
    }

    void build_forms(const Value& catalog, Snapshot& snapshot) {
        const Value* acroform_value = catalog.find("AcroForm");
        if (acroform_value == nullptr) {
            return;
        }
        if (acroform_value->is_reference()) {
            snapshot.form_objects.insert(acroform_value->reference_number);
        }
        const Value* acroform = resolver_.dereference(*acroform_value);
        if (acroform == nullptr || !acroform->is_dictionary()) {
            return;
        }
        const Value* fields = acroform->find("Fields");
        if (fields == nullptr) {
            return;
        }
        const Value* fields_array = resolver_.dereference(*fields);
        if (fields_array == nullptr || !fields_array->is_array()) {
            return;
        }
        for (const Value& field : fields_array->array) {
            std::uint32_t number = field.is_reference() ? field.reference_number : 0;
            walk_field(field, number, "", "", nullptr, nullptr, snapshot, 0);
        }
    }

    void add_attachment(const std::string& name, const Value& filespec_value,
                        std::uint32_t object_number, Snapshot& snapshot) {
        const Value* filespec = resolver_.dereference(filespec_value);
        if (filespec == nullptr || !filespec->is_dictionary()) {
            return;
        }
        AttachmentSnapshot attachment;
        attachment.name = name;
        attachment.object_number = object_number;
        attachment.dependencies.insert(object_number);
        if (attachment.name.empty()) {
            if (const Value* file_name = filespec->find("UF"); file_name != nullptr &&
                file_name->is_string()) {
                attachment.name = decode_pdf_text(file_name->text);
            } else if (const Value* fallback_name = filespec->find("F");
                       fallback_name != nullptr && fallback_name->is_string()) {
                attachment.name = decode_pdf_text(fallback_name->text);
            }
        }
        const Value* embedded = filespec->find("EF");
        if (embedded != nullptr) {
            embedded = resolver_.dereference(*embedded);
        }
        if (embedded != nullptr && embedded->is_dictionary()) {
            for (const auto& [key, item] : embedded->dictionary) {
                attachment.fingerprint += key + "=" + value_text(item) + ";";
                if (item.is_reference()) {
                    attachment.dependencies.insert(item.reference_number);
                    std::string data;
                    if (resolver_.stream(item.reference_number, data, nullptr)) {
                        attachment.fingerprint += "size=" + std::to_string(data.size()) + ";";
                        attachment.fingerprint += "hash=" + hex_hash(fnv1a(data)) + ";";
                    }
                }
            }
        }
        if (const Value* description = filespec->find("Desc");
            description != nullptr && description->is_string()) {
            attachment.fingerprint += "desc=" + decode_pdf_text(description->text) + ";";
        }
        for (std::uint32_t dependency : attachment.dependencies) {
            snapshot.attachment_objects.insert(dependency);
        }
        snapshot.attachments.push_back(std::move(attachment));
    }

    void walk_name_tree(const Value& node, Snapshot& snapshot, int depth) {
        if (depth > 32) {
            return;
        }
        const Value* dictionary = resolver_.dereference(node);
        if (dictionary == nullptr || !dictionary->is_dictionary()) {
            return;
        }
        if (const Value* names = dictionary->find("Names");
            names != nullptr && names->is_array()) {
            for (std::size_t i = 0; i + 1 < names->array.size(); i += 2) {
                std::string name;
                if (names->array[i].is_string()) {
                    name = decode_pdf_text(names->array[i].text);
                }
                std::uint32_t number = names->array[i + 1].is_reference()
                                           ? names->array[i + 1].reference_number
                                           : 0;
                add_attachment(name, names->array[i + 1], number, snapshot);
            }
        }
        if (const Value* kids = dictionary->find("Kids");
            kids != nullptr && kids->is_array()) {
            for (const Value& kid : kids->array) {
                walk_name_tree(kid, snapshot, depth + 1);
            }
        }
    }

    void build_attachments(const Value& catalog, Snapshot& snapshot) {
        if (const Value* names = catalog.find("Names"); names != nullptr) {
            const Value* names_dictionary = resolver_.dereference(*names);
            if (names_dictionary != nullptr && names_dictionary->is_dictionary()) {
                if (const Value* embedded = names_dictionary->find("EmbeddedFiles");
                    embedded != nullptr) {
                    walk_name_tree(*embedded, snapshot, 0);
                }
            }
        }
        if (const Value* associated = catalog.find("AF");
            associated != nullptr && associated->is_array()) {
            for (const Value& item : associated->array) {
                std::uint32_t number = item.is_reference() ? item.reference_number : 0;
                add_attachment("", item, number, snapshot);
            }
        }
        for (const AnnotationSnapshot& annotation : snapshot.annotations) {
            if (!annotation.file_attachment) {
                continue;
            }
            AttachmentSnapshot attachment;
            attachment.name = "annotation:" + annotation.name;
            attachment.object_number = annotation.object_number;
            attachment.fingerprint = annotation.fingerprint;
            attachment.dependencies = annotation.dependencies;
            snapshot.attachment_objects.insert(annotation.object_number);
            snapshot.attachments.push_back(std::move(attachment));
        }
    }

    const Document& document_;
    ObjectResolver resolver_;
    std::size_t revision_ = 0;
    std::set<std::uint32_t> snapshot_annotation_objects_;
};

}  // namespace detail
}  // namespace netsec

namespace netsec {
namespace detail {

inline bool normalize_ranges(std::span<const ByteRange> input, std::uint64_t file_size,
                             std::vector<ByteRange>& output) {
    output.clear();
    for (const ByteRange& range : input) {
        if (range.length == 0) {
            continue;
        }
        if (range.offset > file_size || range.length > file_size - range.offset) {
            return false;
        }
        output.push_back(range);
    }
    std::sort(output.begin(), output.end(), [](const ByteRange& left, const ByteRange& right) {
        return left.offset < right.offset;
    });
    std::vector<ByteRange> merged;
    for (const ByteRange& range : output) {
        if (!merged.empty() && range.offset <= merged.back().end()) {
            merged.back().length =
                std::max(merged.back().end(), range.end()) - merged.back().offset;
        } else {
            merged.push_back(range);
        }
    }
    output = std::move(merged);
    return true;
}

inline std::uint64_t interval_overlap(std::uint64_t begin, std::uint64_t end,
                                      const std::vector<ByteRange>& ranges) {
    std::uint64_t covered = 0;
    for (const ByteRange& range : ranges) {
        if (range.end() <= begin) {
            continue;
        }
        if (range.offset >= end) {
            break;
        }
        const std::uint64_t overlap_begin = std::max(begin, range.offset);
        const std::uint64_t overlap_end = std::min(end, range.end());
        if (overlap_end > overlap_begin) {
            covered += overlap_end - overlap_begin;
        }
    }
    return covered;
}

inline bool interval_fully_covered(std::string_view data, std::uint64_t begin,
                                   std::uint64_t end,
                                   const std::vector<ByteRange>& ranges,
                                   bool ignore_whitespace) {
    if (end <= begin) {
        return true;
    }
    std::uint64_t cursor = begin;
    for (const ByteRange& range : ranges) {
        if (range.end() <= cursor) {
            continue;
        }
        if (range.offset > cursor) {
            if (range.offset >= end) {
                break;
            }
            const std::uint64_t gap_end = std::min(range.offset, end);
            if (!ignore_whitespace) {
                return false;
            }
            for (std::uint64_t position = cursor; position < gap_end; ++position) {
                if (position >= data.size() ||
                    !is_pdf_space(static_cast<unsigned char>(data[position]))) {
                    return false;
                }
            }
        }
        cursor = std::max(cursor, std::min(end, range.end()));
        if (cursor >= end) {
            return true;
        }
    }
    if (cursor >= end) {
        return true;
    }
    if (!ignore_whitespace) {
        return false;
    }
    for (std::uint64_t position = cursor; position < end; ++position) {
        if (position >= data.size() ||
            !is_pdf_space(static_cast<unsigned char>(data[position]))) {
            return false;
        }
    }
    return true;
}

inline std::vector<ByteRange> parse_byte_range_array(const Value& value) {
    std::vector<std::uint64_t> numbers;
    if (!value.is_array()) {
        return {};
    }
    for (const Value& item : value.array) {
        if (!item.is_integer() || item.integer < 0) {
            return {};
        }
        numbers.push_back(static_cast<std::uint64_t>(item.integer));
    }
    if (numbers.size() < 4 || numbers.size() % 2 != 0) {
        return {};
    }
    std::vector<ByteRange> ranges;
    for (std::size_t i = 0; i + 1 < numbers.size(); i += 2) {
        if (numbers[i] > std::numeric_limits<std::uint64_t>::max() - numbers[i + 1]) {
            return {};
        }
        ranges.push_back(ByteRange{numbers[i], numbers[i + 1]});
    }
    return ranges;
}

inline std::vector<ByteRange> signature_holes(const Document& document) {
    std::vector<ByteRange> holes;
    for (const ObjectVersion& version : document.versions) {
        if (version.entry.type != XrefEntryType::InUse ||
            version.entry.offset >= document.data.size()) {
            continue;
        }
        PdfObject object;
        if (!parse_indirect_object(document.data,
                                   static_cast<std::size_t>(version.entry.offset), object) ||
            !object.value.is_dictionary()) {
            continue;
        }
        const Value* byte_range_value = object.value.find("ByteRange");
        const Value* contents = object.value.find("Contents");
        if (byte_range_value == nullptr || contents == nullptr ||
            !contents->is_string()) {
            continue;
        }
        const std::vector<ByteRange> ranges = parse_byte_range_array(*byte_range_value);
        if (ranges.size() < 2) {
            continue;
        }
        const std::uint64_t hole_begin = ranges[0].end();
        const std::uint64_t hole_end = ranges[1].offset;
        if (hole_end <= hole_begin) {
            continue;
        }
        if (contents->source_begin >= hole_begin && contents->source_end <= hole_end) {
            holes.push_back(ByteRange{hole_begin, hole_end - hole_begin});
        }
    }
    std::vector<ByteRange> merged;
    if (!normalize_ranges(holes, document.data.size(), merged)) {
        return {};
    }
    return merged;
}

inline bool looks_like_signature_placeholder(std::string_view data, std::uint64_t begin,
                                             std::uint64_t end) {
    if (end <= begin || end > data.size()) {
        return false;
    }
    std::size_t first = static_cast<std::size_t>(begin);
    std::size_t last = static_cast<std::size_t>(end);
    while (first < last && is_pdf_space(static_cast<unsigned char>(data[first]))) {
        ++first;
    }
    while (last > first && is_pdf_space(static_cast<unsigned char>(data[last - 1]))) {
        --last;
    }
    if (last <= first) {
        return false;
    }
    if (data[first] == '(' && data[last - 1] == ')') {
        return true;
    }
    if (data[first] == '<' && data[last - 1] == '>') {
        for (std::size_t i = first + 1; i + 1 < last; ++i) {
            const unsigned char c = static_cast<unsigned char>(data[i]);
            if (!is_pdf_space(c) && !is_hex_digit(c)) {
                return false;
            }
        }
        return true;
    }
    return false;
}

inline std::vector<ByteRange> extract_ranges_from_raw(std::string_view data) {
    std::vector<ByteRange> ranges;
    std::size_t position = 0;
    while (true) {
        const std::size_t found = data.find("/ByteRange", position);
        if (found == std::string_view::npos) {
            break;
        }
        position = found + 10;
        PdfParser parser(data, position);
        Value value;
        if (!parser.parse_value(value)) {
            continue;
        }
        std::vector<ByteRange> parsed = parse_byte_range_array(value);
        ranges.insert(ranges.end(), parsed.begin(), parsed.end());
    }
    return ranges;
}

inline std::string dictionary_name(const Value& dictionary, std::string_view key) {
    const Value* value = dictionary.find(key);
    return value != nullptr && value->is_name() ? value->text : std::string{};
}

inline void add_impact(std::vector<std::string>& impacts, std::string_view impact) {
    if (std::find(impacts.begin(), impacts.end(), impact) == impacts.end()) {
        impacts.push_back(std::string(impact));
    }
}

inline bool has_impact(const std::vector<std::string>& impacts, std::string_view impact) {
    return std::find(impacts.begin(), impacts.end(), impact) != impacts.end();
}

struct Classification {
    std::string type;
    std::string subtype;
    std::vector<std::string> impacts;
    bool is_field = false;
    bool is_annotation = false;
};

inline std::string value_text_ptr(const Value* value) {
    return value == nullptr ? std::string{} : value_text(*value);
}

inline void remove_impact(std::vector<std::string>& impacts, std::string_view impact) {
    impacts.erase(std::remove(impacts.begin(), impacts.end(), impact), impacts.end());
}

inline void apply_support_overrides(Classification& classification, std::uint32_t number,
                                    const Snapshot& snapshot) {
    if (snapshot.display_objects.count(number) != 0) {
        return;
    }
    if (snapshot.annotation_objects.count(number) != 0) {
        remove_impact(classification.impacts, "text");
        remove_impact(classification.impacts, "page");
        remove_impact(classification.impacts, "structure");
        add_impact(classification.impacts, "annotation");
    }
    if (snapshot.form_objects.count(number) != 0) {
        remove_impact(classification.impacts, "text");
        add_impact(classification.impacts, "form");
    }
    if (snapshot.attachment_objects.count(number) != 0) {
        remove_impact(classification.impacts, "text");
        add_impact(classification.impacts, "attachment");
    }
}

inline bool is_metadata_dictionary(const Value& dictionary) {
    static constexpr std::array<const char*, 9> keys = {
        "Producer", "Creator", "Title", "Author", "Subject", "Keywords",
        "CreationDate", "ModDate", "Trapped",
    };
    for (const char* key : keys) {
        if (dictionary.find(key) != nullptr) {
            return true;
        }
    }
    return false;
}

inline Classification classify_value(const Value& value, std::uint32_t number,
                                     const Snapshot& display) {
    Classification result;
    if (!value.is_dictionary()) {
        return result;
    }
    result.type = dictionary_name(value, "Type");
    result.subtype = dictionary_name(value, "Subtype");
    result.is_field = value.find("FT") != nullptr;
    result.is_annotation = result.type == "Annot";

    if (result.type == "Annot") {
        add_impact(result.impacts, "annotation");
    }
    if (result.type == "Page" || result.type == "Pages") {
        add_impact(result.impacts, "page");
    }
    if (result.type == "Catalog") {
        add_impact(result.impacts, "structure");
    }
    if (result.type == "Filespec" || result.type == "EmbeddedFile") {
        add_impact(result.impacts, "attachment");
    }
    if (result.type == "Sig") {
        add_impact(result.impacts, "signature");
    }
    if (result.type == "Font" || result.type == "FontDescriptor" ||
        result.type == "XObject" || result.type == "ExtGState" ||
        result.type == "ColorSpace" || result.type == "Pattern" ||
        result.type == "Shading" || result.type == "CMap") {
        add_impact(result.impacts, "text");
    }
    if (result.type == "Linearized") {
        add_impact(result.impacts, "structure");
    }
    if (result.type == "XRef" || result.type == "ObjStm" ||
        result.type == "Metadata") {
        add_impact(result.impacts, "metadata");
    }

    if (value.find("FT") != nullptr) {
        add_impact(result.impacts, "form");
        const std::string field_type = dictionary_name(value, "FT");
        if (field_type == "Sig") {
            add_impact(result.impacts, "signature");
        }
    }
    if (value.find("V") != nullptr || value.find("DV") != nullptr) {
        add_impact(result.impacts, "form");
    }
    if (value.find("Annots") != nullptr) {
        add_impact(result.impacts, "annotation");
    }
    if (value.find("Contents") != nullptr) {
        if (value.find("ByteRange") != nullptr) {
            add_impact(result.impacts, "signature");
        } else {
            add_impact(result.impacts, "text");
        }
    }
    if (value.find("Kids") != nullptr) {
        add_impact(result.impacts, "page");
    }
    if (value.find("EmbeddedFiles") != nullptr || value.find("EF") != nullptr ||
        value.find("AF") != nullptr || value.find("Filespec") != nullptr ||
        value.find("Collection") != nullptr) {
        add_impact(result.impacts, "attachment");
    }
    if (value.find("AcroForm") != nullptr || value.find("Outlines") != nullptr ||
        value.find("OpenAction") != nullptr || value.find("PageMode") != nullptr ||
        value.find("PageLayout") != nullptr ||
        value.find("ViewerPreferences") != nullptr ||
        value.find("StructTreeRoot") != nullptr) {
        add_impact(result.impacts, "structure");
    }
    if (value.find("ByteRange") != nullptr && value.find("Contents") != nullptr) {
        add_impact(result.impacts, "signature");
    }
    if (result.subtype == "Widget" && value.find("FT") == nullptr) {
        add_impact(result.impacts, "annotation");
    }
    if (result.subtype == "FileAttachment") {
        add_impact(result.impacts, "attachment");
    }

    if (display.display_objects.count(number) != 0) {
        add_impact(result.impacts, "text");
    }
    if (display.annotation_objects.count(number) != 0) {
        add_impact(result.impacts, "annotation");
    }
    if (display.form_objects.count(number) != 0) {
        add_impact(result.impacts, "form");
    }
    if (display.attachment_objects.count(number) != 0) {
        add_impact(result.impacts, "attachment");
    }
    if (result.impacts.empty() && is_metadata_dictionary(value)) {
        add_impact(result.impacts, "metadata");
    }
    apply_support_overrides(result, number, display);
    return result;
}

inline Classification classify_version(const Document& document, const ObjectVersion& version,
                                       const Snapshot& final_snapshot) {
    if (version.entry.type == XrefEntryType::Free) {
        Classification result;
        const ObjectVersion* previous = nullptr;
        for (const ObjectVersion& candidate : document.versions) {
            if (candidate.number == version.number && candidate.revision < version.revision) {
                previous = &candidate;
            }
        }
        if (previous != nullptr) {
            result = classify_version(document, *previous, final_snapshot);
        }
        return result;
    }
    const Value* current = nullptr;
    PdfObject direct_object;
    std::unique_ptr<ObjectResolver> current_resolver;
    if (version.entry.type == XrefEntryType::InUse) {
        if (!parse_indirect_object(document.data,
                                   static_cast<std::size_t>(version.entry.offset),
                                   direct_object)) {
            return {};
        }
        current = &direct_object.value;
    } else {
        current_resolver = std::make_unique<ObjectResolver>(document, version.revision);
        current = current_resolver->get(version.number);
    }
    if (current == nullptr) {
        return {};
    }

    const ObjectVersion* previous_version = nullptr;
    for (const ObjectVersion& candidate : document.versions) {
        if (candidate.number == version.number && candidate.revision < version.revision &&
            candidate.entry.type != XrefEntryType::Free) {
            previous_version = &candidate;
        }
    }
    if (previous_version == nullptr || !current->is_dictionary()) {
        return classify_value(*current, version.number, final_snapshot);
    }

    const Value* previous = nullptr;
    PdfObject previous_object;
    std::unique_ptr<ObjectResolver> previous_resolver;
    if (previous_version->entry.type == XrefEntryType::InUse) {
        if (parse_indirect_object(document.data,
                                  static_cast<std::size_t>(previous_version->entry.offset),
                                  previous_object)) {
            previous = &previous_object.value;
        }
    } else {
        previous_resolver =
            std::make_unique<ObjectResolver>(document, previous_version->revision);
        previous = previous_resolver->get(previous_version->number);
    }
    if (previous == nullptr || !previous->is_dictionary()) {
        return classify_value(*current, version.number, final_snapshot);
    }

    Classification result;
    result.type = dictionary_name(*current, "Type");
    result.subtype = dictionary_name(*current, "Subtype");
    result.is_field = current->find("FT") != nullptr;
    result.is_annotation = result.type == "Annot";
    std::set<std::string> changed;
    std::set<std::string> keys;
    for (const auto& [key, value] : current->dictionary) {
        (void)value;
        keys.insert(key);
    }
    for (const auto& [key, value] : previous->dictionary) {
        (void)value;
        keys.insert(key);
    }
    for (const std::string& key : keys) {
        const Value* now = current->find(key);
        const Value* then = previous->find(key);
        if (value_text_ptr(now) != value_text_ptr(then)) {
            changed.insert(key);
        }
    }
    if (changed.empty()) {
        return result;
    }
    for (const std::string& key : changed) {
        if (result.type == "Annot" &&
            (key == "F" || key == "Rect" || key == "AP" || key == "A" ||
             key == "NM" || key == "P" || key == "Parent" || key == "Contents" ||
             key == "Subtype" || key == "T")) {
            add_impact(result.impacts, "annotation");
            if (key == "Contents" && current->find("ByteRange") != nullptr) {
                add_impact(result.impacts, "signature");
            }
        } else if (key == "Annots") {
            add_impact(result.impacts, "annotation");
        } else if (key == "Contents" || key == "Resources" || key == "Length" ||
                   key == "Filter" || key == "DecodeParms") {
            if (current->find("ByteRange") != nullptr &&
                current->find("Contents") != nullptr) {
                add_impact(result.impacts, "signature");
            } else {
                add_impact(result.impacts, "text");
            }
        } else if (key == "MediaBox" || key == "CropBox" || key == "Rotate" ||
                   key == "Kids" || key == "Count" || key == "Parent") {
            add_impact(result.impacts, "page");
        } else if (key == "V" || key == "DV" || key == "FT" || key == "Ff") {
            add_impact(result.impacts, "form");
            if (key == "FT" && dictionary_name(*current, "FT") == "Sig") {
                add_impact(result.impacts, "signature");
            }
            if (key == "V" && current->find("V") != nullptr) {
                const Value* value = current->find("V");
                if (value != nullptr && value->is_reference()) {
                    ObjectResolver resolver(document, version.revision);
                    const Value* signature = resolver.get(value->reference_number);
                    if (signature != nullptr && signature->is_dictionary() &&
                        signature->find("ByteRange") != nullptr) {
                        add_impact(result.impacts, "signature");
                    }
                }
            }
        } else if (key == "Fields" || key == "AcroForm" || key == "NeedAppearances" ||
                   key == "SigFlags") {
            add_impact(result.impacts, "form");
            bool signature = false;
            if (key == "Fields" || key == "AcroForm") {
                const Value* fields = current->find(key);
                ObjectResolver resolver(document, version.revision);
                const Value* dictionary = resolver.dereference(*fields);
                if (key == "AcroForm" && dictionary != nullptr) {
                    fields = dictionary->find("Fields");
                    dictionary = fields != nullptr ? resolver.dereference(*fields) : nullptr;
                }
                if (dictionary != nullptr && dictionary->is_array()) {
                    for (const Value& item : dictionary->array) {
                        const Value* field = resolver.dereference(item);
                        if (field != nullptr && field->is_dictionary() &&
                            dictionary_name(*field, "FT") == "Sig") {
                            signature = true;
                        }
                    }
                }
            }
            if (signature) {
                add_impact(result.impacts, "signature");
            }
        } else if (key == "Names" || key == "EmbeddedFiles" || key == "EF" ||
                   key == "AF" || key == "FS" || key == "Filespec" || key == "Desc" ||
                   key == "UF" || key == "F") {
            add_impact(result.impacts, "attachment");
        } else if (key == "Pages" || key == "Outlines" || key == "OpenAction" ||
                   key == "PageMode" || key == "PageLayout" ||
                   key == "ViewerPreferences" || key == "StructTreeRoot" ||
                   key == "Perms") {
            add_impact(result.impacts, "structure");
        } else if (key == "Metadata") {
            add_impact(result.impacts, "metadata");
        } else if (key == "Rect" || key == "AP" || key == "A" ||
                   key == "NM" || key == "P") {
            add_impact(result.impacts, "annotation");
        } else if (key == "ByteRange" || key == "SubFilter" || key == "Cert" ||
                   key == "Reference") {
            add_impact(result.impacts, "signature");
        } else if (key == "Type" || key == "Subtype") {
            if (result.type == "Annot") {
                add_impact(result.impacts, "annotation");
            } else if (result.type == "Page" || result.type == "Pages") {
                add_impact(result.impacts, "page");
            } else if (result.type == "Font" || result.type == "XObject" ||
                       result.type == "ExtGState" || result.type == "ColorSpace" ||
                       result.type == "Pattern" || result.type == "Shading") {
                add_impact(result.impacts, "text");
            }
        }
    }
    if (result.impacts.empty() && result.type == "Page") {
        for (const std::string& key : changed) {
            if (key != "Annots") {
                add_impact(result.impacts, "text");
                break;
            }
        }
    }
    if (result.impacts.empty() && final_snapshot.display_objects.count(version.number) != 0 &&
        result.type != "Page" && result.type != "Pages") {
        add_impact(result.impacts, "text");
    }
    if (result.impacts.empty() &&
        final_snapshot.annotation_objects.count(version.number) != 0) {
        add_impact(result.impacts, "annotation");
    }
    if (result.impacts.empty() &&
        final_snapshot.form_objects.count(version.number) != 0) {
        add_impact(result.impacts, "form");
    }
    if (result.impacts.empty() &&
        final_snapshot.attachment_objects.count(version.number) != 0) {
        add_impact(result.impacts, "attachment");
    }
    apply_support_overrides(result, version.number, final_snapshot);
    return result;
}

inline std::size_t latest_revision_for(const Document& document,
                                       const std::set<std::uint32_t>& objects,
                                       std::size_t fallback) {
    std::size_t revision = 0;
    bool found = false;
    for (std::uint32_t object : objects) {
        auto iterator = document.latest_version.find(object);
        if (iterator == document.latest_version.end()) {
            continue;
        }
        revision = std::max(revision,
                            document.versions[iterator->second].revision);
        found = true;
    }
    return found ? revision : fallback;
}

inline Change make_change(ChangeKind kind, std::size_t revision, std::uint32_t object_number,
                          std::string category, std::string description,
                          std::string before, std::string after) {
    Change change;
    change.kind = kind;
    change.revision = revision;
    change.object_number = object_number;
    change.category = std::move(category);
    change.description = std::move(description);
    change.before = std::move(before);
    change.after = std::move(after);
    return change;
}

inline void compare_snapshots(const Snapshot& before, const Snapshot& after,
                              const Document& document, std::vector<Change>& changes) {
    const std::size_t fallback_revision =
        document.revisions.empty() ? 0 : document.revisions.back().index;
    const std::size_t common_pages = std::min(before.pages.size(), after.pages.size());
    for (std::size_t index = 0; index < common_pages; ++index) {
        const PageSnapshot& old_page = before.pages[index];
        const PageSnapshot& new_page = after.pages[index];
        const std::size_t revision =
            latest_revision_for(document, new_page.dependencies, fallback_revision);
        if (old_page.object_number != new_page.object_number) {
            changes.push_back(make_change(
                ChangeKind::Page, revision, new_page.object_number, "page",
                "page " + std::to_string(index + 1) + " was replaced or reordered",
                std::to_string(old_page.object_number),
                std::to_string(new_page.object_number)));
        }
        if (old_page.geometry != new_page.geometry) {
            changes.push_back(make_change(
                ChangeKind::Page, revision, new_page.object_number, "page",
                "page " + std::to_string(index + 1) + " geometry changed",
                old_page.geometry, new_page.geometry));
        }
        if (old_page.text != new_page.text) {
            changes.push_back(make_change(
                ChangeKind::Text, revision, new_page.object_number, "text",
                "page " + std::to_string(index + 1) + " visible text changed",
                printable_excerpt(old_page.text), printable_excerpt(new_page.text)));
        } else if (old_page.content_hash != new_page.content_hash) {
            changes.push_back(make_change(
                ChangeKind::Text, revision, new_page.object_number, "text",
                "page " + std::to_string(index + 1) + " content stream changed",
                old_page.content_hash, new_page.content_hash));
        }
    }
    if (before.pages.size() != after.pages.size()) {
        changes.push_back(make_change(
            ChangeKind::Page, fallback_revision,
            after.pages.empty() ? 0 : after.pages.back().object_number, "page",
            "page count changed", std::to_string(before.pages.size()),
            std::to_string(after.pages.size())));
    }
    for (std::size_t index = common_pages; index < after.pages.size(); ++index) {
        changes.push_back(make_change(
            ChangeKind::Page,
            latest_revision_for(document, after.pages[index].dependencies,
                                fallback_revision),
            after.pages[index].object_number, "page",
            "page " + std::to_string(index + 1) + " was added", "",
            printable_excerpt(after.pages[index].text)));
    }
    for (std::size_t index = common_pages; index < before.pages.size(); ++index) {
        changes.push_back(make_change(
            ChangeKind::Page,
            latest_revision_for(document, before.pages[index].dependencies,
                                fallback_revision),
            before.pages[index].object_number, "page",
            "page " + std::to_string(index + 1) + " was removed",
            printable_excerpt(before.pages[index].text), ""));
    }

    std::map<std::uint32_t, const AnnotationSnapshot*> old_annotations;
    for (const AnnotationSnapshot& annotation : before.annotations) {
        old_annotations[annotation.object_number] = &annotation;
    }
    std::map<std::uint32_t, const AnnotationSnapshot*> new_annotations;
    for (const AnnotationSnapshot& annotation : after.annotations) {
        new_annotations[annotation.object_number] = &annotation;
    }
    for (const auto& [number, annotation] : new_annotations) {
        auto old = old_annotations.find(number);
        if (old == old_annotations.end()) {
            ChangeKind kind =
                annotation->signature ? ChangeKind::Signature : ChangeKind::Annotation;
            Change change = make_change(
                kind, latest_revision_for(document, annotation->dependencies,
                                          fallback_revision),
                number, annotation->signature ? "signature" : "annotation",
                "annotation added: /" +
                    (annotation->subtype.empty() ? std::string("Unknown")
                                                 : annotation->subtype) +
                    (annotation->hidden ? " hidden" : " visible"),
                "", printable_excerpt(annotation->contents));
            change.subtype = annotation->subtype;
            change.annotation_name = annotation->name;
            change.hidden = annotation->hidden;
            changes.push_back(std::move(change));
            continue;
        }
        if (old->second->fingerprint != annotation->fingerprint) {
            ChangeKind kind =
                annotation->signature ? ChangeKind::Signature : ChangeKind::Annotation;
            Change change = make_change(
                kind, latest_revision_for(document, annotation->dependencies,
                                          fallback_revision),
                number, annotation->signature ? "signature" : "annotation",
                "annotation modified: /" +
                    (annotation->subtype.empty() ? std::string("Unknown")
                                                 : annotation->subtype),
                printable_excerpt(old->second->fingerprint),
                printable_excerpt(annotation->fingerprint));
            change.subtype = annotation->subtype;
            change.annotation_name = annotation->name;
            change.hidden = annotation->hidden;
            changes.push_back(std::move(change));
        }
    }
    for (const auto& [number, annotation] : old_annotations) {
        if (new_annotations.count(number) != 0) {
            continue;
        }
        Change change = make_change(
            ChangeKind::Annotation,
            latest_revision_for(document, annotation->dependencies, fallback_revision),
            number, "annotation", "annotation removed",
            printable_excerpt(annotation->contents), "");
        change.subtype = annotation->subtype;
        change.annotation_name = annotation->name;
        change.hidden = annotation->hidden;
        changes.push_back(std::move(change));
    }

    std::map<std::string, const FormFieldSnapshot*> old_forms;
    for (const FormFieldSnapshot& form : before.forms) {
        old_forms[form.name.empty() ? "#" + std::to_string(form.object_number) : form.name] =
            &form;
    }
    std::map<std::string, const FormFieldSnapshot*> new_forms;
    for (const FormFieldSnapshot& form : after.forms) {
        new_forms[form.name.empty() ? "#" + std::to_string(form.object_number) : form.name] =
            &form;
    }
    for (const auto& [name, form] : new_forms) {
        auto old = old_forms.find(name);
        if (old == old_forms.end()) {
            ChangeKind kind =
                form->signature ? ChangeKind::Signature : ChangeKind::FormValue;
            changes.push_back(make_change(
                kind,
                latest_revision_for(document, form->dependencies, fallback_revision),
                form->object_number, form->signature ? "signature" : "form",
                "form field added: " + name, "", printable_excerpt(form->value)));
            continue;
        }
        if (old->second->fingerprint != form->fingerprint) {
            ChangeKind kind =
                form->signature ? ChangeKind::Signature : ChangeKind::FormValue;
            std::string description = "form field changed: " + name;
            std::string before_value = old->second->value;
            std::string after_value = form->value;
            if (old->second->default_value != form->default_value) {
                description += " (default value)";
                before_value = old->second->default_value;
                after_value = form->default_value;
            }
            changes.push_back(make_change(
                kind, latest_revision_for(document, form->dependencies, fallback_revision),
                form->object_number, form->signature ? "signature" : "form",
                std::move(description), printable_excerpt(before_value),
                printable_excerpt(after_value)));
        }
    }
    for (const auto& [name, form] : old_forms) {
        if (new_forms.count(name) != 0) {
            continue;
        }
        changes.push_back(make_change(
            ChangeKind::FormValue,
            latest_revision_for(document, form->dependencies, fallback_revision),
            form->object_number, "form", "form field removed: " + name,
            printable_excerpt(form->value), ""));
    }

    std::map<std::string, const AttachmentSnapshot*> old_files;
    for (const AttachmentSnapshot& attachment : before.attachments) {
        old_files[attachment.name + "#" + std::to_string(attachment.object_number)] =
            &attachment;
    }
    std::map<std::string, const AttachmentSnapshot*> new_files;
    for (const AttachmentSnapshot& attachment : after.attachments) {
        new_files[attachment.name + "#" + std::to_string(attachment.object_number)] =
            &attachment;
    }
    for (const auto& [name, attachment] : new_files) {
        auto old = old_files.find(name);
        if (old == old_files.end()) {
            changes.push_back(make_change(
                ChangeKind::EmbeddedFile,
                latest_revision_for(document, attachment->dependencies, fallback_revision),
                attachment->object_number, "attachment",
                "embedded file added: " + attachment->name, "", attachment->fingerprint));
        } else if (old->second->fingerprint != attachment->fingerprint) {
            changes.push_back(make_change(
                ChangeKind::EmbeddedFile,
                latest_revision_for(document, attachment->dependencies, fallback_revision),
                attachment->object_number, "attachment",
                "embedded file changed: " + attachment->name, old->second->fingerprint,
                attachment->fingerprint));
        }
    }
    for (const auto& [name, attachment] : old_files) {
        if (new_files.count(name) != 0) {
            continue;
        }
        changes.push_back(make_change(
            ChangeKind::EmbeddedFile,
            latest_revision_for(document, attachment->dependencies, fallback_revision),
            attachment->object_number, "attachment",
            "embedded file removed: " + attachment->name, attachment->fingerprint, ""));
    }

    if (before.catalog_fingerprint != after.catalog_fingerprint) {
        changes.push_back(make_change(
            ChangeKind::DocumentStructure, fallback_revision, 0, "structure",
            "document catalog changed", before.catalog_fingerprint,
            after.catalog_fingerprint));
    }
}

inline void apply_document_policy(const Document& document, std::size_t revision,
                                  Policy& policy) {
    ObjectResolver resolver(document, revision);
    Value trailer = document.trailer_at(revision);
    const Value* root_reference = trailer.find("Root");
    const Value* catalog =
        root_reference != nullptr ? resolver.dereference(*root_reference) : nullptr;
    if (catalog == nullptr || !catalog->is_dictionary()) {
        return;
    }
    if (const Value* permissions = catalog->find("Perms");
        permissions != nullptr) {
        const Value* permissions_dictionary = resolver.dereference(*permissions);
        if (permissions_dictionary != nullptr) {
            const Value* docmdp = permissions_dictionary->find("DocMDP");
            const Value* signature =
                docmdp != nullptr ? resolver.dereference(*docmdp) : nullptr;
            const Value* permission = signature != nullptr ? signature->find("P") : nullptr;
            if (permission != nullptr && permission->is_integer()) {
                if (permission->integer >= 2) {
                    policy.allow_countersignatures = true;
                }
                if (permission->integer >= 3) {
                    policy.allow_annotation_increments = true;
                }
            }
        }
    }
    const Value* permitted = catalog->find("PermittedChanges");
    if (permitted == nullptr) {
        permitted = catalog->find("PermittedIncrements");
    }
    const Value* permitted_dictionary =
        permitted != nullptr ? resolver.dereference(*permitted) : nullptr;
    if (permitted_dictionary != nullptr && permitted_dictionary->is_dictionary()) {
        if (const Value* annotations = permitted_dictionary->find("Annotations");
            annotations != nullptr) {
            policy.allow_annotation_increments = true;
            if (annotations->is_name()) {
                policy.allowed_annotation_subtypes.insert(annotations->text);
            } else if (annotations->is_array()) {
                for (const Value& item : annotations->array) {
                    if (item.is_name()) {
                        policy.allowed_annotation_subtypes.insert(item.text);
                    }
                }
            }
        }
        if (const Value* objects = permitted_dictionary->find("Objects");
            objects != nullptr && objects->is_array()) {
            for (const Value& item : objects->array) {
                if (item.is_integer() && item.integer >= 0) {
                    policy.allowed_object_numbers.insert(
                        static_cast<std::uint32_t>(item.integer));
                }
            }
        }
        if (const Value* allow = permitted_dictionary->find("Allow");
            allow != nullptr) {
            std::vector<std::string> values;
            if (allow->is_name()) {
                values.push_back(allow->text);
            } else if (allow->is_array()) {
                for (const Value& item : allow->array) {
                    if (item.is_name()) {
                        values.push_back(item.text);
                    }
                }
            }
            for (const std::string& value : values) {
                if (value == "annotations") {
                    policy.allow_annotation_increments = true;
                } else if (value == "signatures" || value == "countersignatures") {
                    policy.allow_countersignatures = true;
                } else if (value == "text") {
                    policy.allow_text_changes = true;
                } else if (value == "forms") {
                    policy.allow_form_value_changes = true;
                } else if (value == "attachments") {
                    policy.allow_embedded_file_changes = true;
                }
            }
        }
    }
    if (const Value* allowed = catalog->find("PermittedAnnotations");
        allowed != nullptr) {
        policy.allow_annotation_increments = true;
        if (allowed->is_name()) {
            policy.allowed_annotation_subtypes.insert(allowed->text);
        } else if (allowed->is_array()) {
            for (const Value& item : allowed->array) {
                if (item.is_name()) {
                    policy.allowed_annotation_subtypes.insert(item.text);
                }
            }
        }
    }
}

inline bool annotation_allowed(const Change& change, const Policy& policy) {
    if (change.kind == ChangeKind::Signature) {
        return policy.allow_countersignatures ||
               (policy.allow_annotation_increments &&
                (policy.allow_hidden_annotations || !change.hidden));
    }
    if (!policy.allow_annotation_increments) {
        return false;
    }
    if (change.hidden && !policy.allow_hidden_annotations) {
        return false;
    }
    if (change.subtype == "FileAttachment" &&
        !policy.allow_attachment_annotations) {
        return false;
    }
    if (!policy.allowed_annotation_subtypes.empty() &&
        policy.allowed_annotation_subtypes.count(change.subtype) == 0) {
        return false;
    }
    if (!policy.allowed_annotation_names.empty() &&
        policy.allowed_annotation_names.count(change.annotation_name) == 0) {
        return false;
    }
    return true;
}

inline bool change_allowed(Change& change, const Policy& policy) {
    switch (change.kind) {
        case ChangeKind::Text:
            return policy.allow_text_changes;
        case ChangeKind::Page:
            return policy.allow_page_changes;
        case ChangeKind::Annotation:
            return annotation_allowed(change, policy);
        case ChangeKind::Signature:
            return annotation_allowed(change, policy);
        case ChangeKind::FormValue:
            return policy.allow_form_value_changes;
        case ChangeKind::EmbeddedFile:
            return policy.allow_embedded_file_changes;
        case ChangeKind::DocumentStructure:
            return policy.allow_document_structure_changes;
        case ChangeKind::Metadata:
            return true;
        case ChangeKind::Other:
            return policy.allow_unknown_incremental_objects;
    }
    return false;
}

inline std::string change_kind_label(ChangeKind kind) {
    return to_string(kind);
}

inline std::string summarize_changes(const std::vector<Change>& changes) {
    if (changes.empty()) {
        return "no visible differences";
    }
    std::string summary;
    for (const Change& change : changes) {
        if (!summary.empty()) {
            summary.push_back('\n');
        }
        summary += "revision " + std::to_string(change.revision) + " " +
                   change_kind_label(change.kind) + ": " + change.description;
        if (!change.before.empty() || !change.after.empty()) {
            summary += " [";
            summary += change.before;
            summary += " -> ";
            summary += change.after;
            summary += "]";
        }
        if (change.covered_by_signature) {
            summary += " [signed]";
        } else if (change.allowed) {
            summary += " [allowlisted]";
        } else {
            summary += " [UNSIGNED]";
        }
    }
    return summary;
}

inline Assessment assess_impl(std::string_view pdf, std::span<const ByteRange> signed_ranges,
                              Policy policy, bool embedded_signatures) {
    Assessment assessment;
    assessment.file_size = pdf.size();
    Document document = parse_document(pdf);
    if (!document.valid) {
        assessment.decision = Decision::MalformedDocument;
        assessment.reason = document.error.empty() ? "PDF parsing failed" : document.error;
        return assessment;
    }
    assessment.original_revision_size = document.revisions.front().end;
    Value original_trailer_for_encryption = document.trailer_at(0);
    if (original_trailer_for_encryption.find("Encrypt") != nullptr) {
        assessment.decision = Decision::MalformedDocument;
        assessment.reason = "encrypted PDFs are not supported";
        return assessment;
    }

    std::vector<ByteRange> input_ranges(signed_ranges.begin(), signed_ranges.end());
    if (embedded_signatures && input_ranges.empty()) {
        input_ranges = extract_ranges_from_raw(pdf);
    }
    if (input_ranges.empty()) {
        assessment.decision = Decision::NoSignatureRange;
        assessment.reason = "no signature byte range was supplied or found";
        return assessment;
    }
    if (!normalize_ranges(input_ranges, pdf.size(), assessment.signed_ranges)) {
        assessment.decision = Decision::InvalidSignatureScope;
        assessment.reason = "signature byte range is outside the document";
        return assessment;
    }
    const std::vector<ByteRange> candidate_holes = signature_holes(document);
    assessment.allowed_holes.clear();
    for (const ByteRange& hole : candidate_holes) {
        for (std::size_t i = 1; i < assessment.signed_ranges.size(); ++i) {
            if (hole.offset >= assessment.signed_ranges[i - 1].end() &&
                hole.end() <= assessment.signed_ranges[i].offset) {
                assessment.allowed_holes.push_back(hole);
                break;
            }
        }
    }
    for (std::size_t i = 1; i < assessment.signed_ranges.size(); ++i) {
        const ByteRange& previous = assessment.signed_ranges[i - 1];
        const ByteRange& next = assessment.signed_ranges[i];
        if (next.offset > previous.end() &&
            looks_like_signature_placeholder(pdf, previous.end(), next.offset)) {
            assessment.allowed_holes.push_back(
                ByteRange{previous.end(), next.offset - previous.end()});
        }
    }
    std::vector<ByteRange> normalized_holes;
    if (normalize_ranges(assessment.allowed_holes, pdf.size(), normalized_holes)) {
        assessment.allowed_holes = std::move(normalized_holes);
    }

    std::vector<ByteRange> effective_ranges = assessment.signed_ranges;
    effective_ranges.insert(effective_ranges.end(), assessment.allowed_holes.begin(),
                            assessment.allowed_holes.end());
    std::vector<ByteRange> merged_effective;
    normalize_ranges(effective_ranges, pdf.size(), merged_effective);

    assessment.signature_covers_original =
        interval_fully_covered(pdf, 0, assessment.original_revision_size,
                               merged_effective, policy.ignore_trailing_whitespace);
    assessment.signature_covers_entire_document =
        interval_fully_covered(pdf, 0, pdf.size(), merged_effective,
                               policy.ignore_trailing_whitespace);

    std::size_t maximum_section_revision = 0;
    for (const Revision& revision : document.revisions) {
        maximum_section_revision = std::max(maximum_section_revision, revision.index);
    }
    assessment.has_incremental_updates =
        document.revisions.size() > 1 || maximum_section_revision > 0;

    assessment.revisions.clear();
    for (const Revision& revision : document.revisions) {
        RevisionInfo info;
        info.index = revision.index;
        info.kind = revision.kind;
        info.begin = revision.begin;
        info.end = revision.end;
        info.linearized = revision.linearized;
        info.xref_sections = revision.section_indexes.size();
        const std::uint64_t length = revision.end - revision.begin;
        const std::uint64_t covered =
            interval_overlap(revision.begin, revision.end, merged_effective);
        info.covered_fraction =
            length == 0 ? 1.0
                        : static_cast<double>(covered) / static_cast<double>(length);
        info.covered_by_signature =
            interval_fully_covered(pdf, revision.begin, revision.end, merged_effective,
                                   policy.ignore_trailing_whitespace);
        for (const ObjectVersion& version : document.versions) {
            if (version.revision == revision.index) {
                ++info.object_versions;
            }
        }
        assessment.revisions.push_back(info);
    }

    if (policy.require_original_revision_coverage &&
        !assessment.signature_covers_original) {
        assessment.decision = Decision::InvalidSignatureScope;
        assessment.reason =
            "signature byte range does not cover the original revision";
        return assessment;
    }

    Snapshot original = SnapshotBuilder(document, 0).build();
    Snapshot final = SnapshotBuilder(document, document.revisions.size() - 1).build();
    if (policy.derive_from_document) {
        apply_document_policy(document, document.revisions.size() - 1, policy);
    }

    if (original.ok && final.ok) {
        compare_snapshots(original, final, document, assessment.changes);
    }
    Value original_trailer = document.trailer_at(0);
    Value final_trailer = document.trailer_at(document.revisions.size() - 1);
    if (value_text_ptr(original_trailer.find("Encrypt")) !=
        value_text_ptr(final_trailer.find("Encrypt"))) {
        assessment.changes.push_back(make_change(
            ChangeKind::DocumentStructure, document.revisions.size() - 1, 0,
            "structure", "document encryption dictionary changed", "present/old",
            "new"));
    }

    std::set<std::size_t> covered_revisions;
    for (const RevisionInfo& revision : assessment.revisions) {
        if (revision.covered_by_signature) {
            covered_revisions.insert(revision.index);
        }
    }
    for (Change& change : assessment.changes) {
        change.covered_by_signature =
            covered_revisions.count(change.revision) != 0 ||
            assessment.signature_covers_entire_document;
        if (change.covered_by_signature) {
            change.allowed = true;
            continue;
        }
        if (policy.allowed_object_numbers.count(change.object_number) != 0) {
            change.allowed = true;
            continue;
        }
        change.allowed = change_allowed(change, policy);
        if (!change.allowed) {
            assessment.violations.push_back(change.description);
        }
    }

    std::set<std::uint32_t> changed_objects;
    for (const Change& change : assessment.changes) {
        changed_objects.insert(change.object_number);
    }
    for (const ObjectVersion& version : document.versions) {
        if (version.revision == 0 ||
            covered_revisions.count(version.revision) != 0) {
            continue;
        }
        const Classification classification =
            classify_version(document, version, final);
        bool relevant = false;
        for (const std::string& impact : classification.impacts) {
            if (impact != "metadata" && impact != "xref") {
                relevant = true;
            }
        }
        if (!relevant) {
            continue;
        }
        std::vector<std::string> impacts = classification.impacts;
        bool object_allowed = policy.allowed_object_numbers.count(version.number) != 0;
        if (!object_allowed) {
            bool has_annotation = has_impact(impacts, "annotation");
            bool has_signature = has_impact(impacts, "signature");
            bool has_text = has_impact(impacts, "text");
            bool has_page = has_impact(impacts, "page");
            bool has_form = has_impact(impacts, "form");
            bool has_attachment = has_impact(impacts, "attachment");
            bool has_structure = has_impact(impacts, "structure");
            object_allowed =
                (!has_text || policy.allow_text_changes) &&
                (!has_page || policy.allow_page_changes) &&
                (!has_form || policy.allow_form_value_changes || has_signature ||
                 (policy.allow_countersignatures && !classification.is_field)) &&
                (!has_attachment || policy.allow_embedded_file_changes) &&
                (!has_structure || policy.allow_document_structure_changes);
            if (has_annotation) {
                if (classification.type == "Page" || classification.type == "Pages" ||
                    classification.type == "Catalog") {
                    object_allowed = object_allowed &&
                                     policy.allow_annotation_increments;
                } else if (!classification.is_annotation) {
                    object_allowed = object_allowed &&
                                     policy.allow_annotation_increments;
                } else {
                    object_allowed = object_allowed && annotation_allowed(
                        Change{ChangeKind::Annotation, version.revision, version.number,
                               "annotation", "", "", "", classification.subtype, "",
                               false, false, false},
                        policy);
                }
            }
            if (has_signature) {
                object_allowed = object_allowed && policy.allow_countersignatures;
            }
            if (has_attachment && classification.subtype == "FileAttachment") {
                object_allowed = object_allowed &&
                                 policy.allow_attachment_annotations;
            }
        }
        if (!object_allowed && changed_objects.count(version.number) == 0) {
            assessment.violations.push_back(
                "unauthenticated incremental object " +
                std::to_string(version.number) + " (revision " +
                std::to_string(version.revision) + ") changes " +
                (impacts.empty() ? std::string("document content")
                                 : impacts.front()));
        }
    }

    assessment.objects.clear();
    for (const ObjectVersion& version : document.versions) {
        ObjectRevisionInfo info;
        info.object_number = version.number;
        info.generation = version.generation;
        info.revision = version.revision;
        info.xref_type = version.entry.type;
        info.file_offset = version.entry.offset;
        info.compressed = version.entry.type == XrefEntryType::Compressed;
        info.object_stream = version.entry.stream_number;
        info.index_in_object_stream = version.entry.stream_index;
        info.deleted = version.entry.type == XrefEntryType::Free;
        info.reused_object_number = version.reused_object_number;
        info.covered_by_signature =
            covered_revisions.count(version.revision) != 0 ||
            assessment.signature_covers_entire_document;
        const Classification classification =
            classify_version(document, version, final);
        info.type = classification.type;
        info.subtype = classification.subtype;
        info.impacts = classification.impacts;
        if (version.entry.type == XrefEntryType::InUse) {
            PdfObject object;
            if (parse_indirect_object(document.data,
                                      static_cast<std::size_t>(version.entry.offset),
                                      object)) {
                info.byte_length = object.end - object.begin;
            }
        } else if (version.entry.type == XrefEntryType::Compressed) {
            const ObjectVersion* stream_version =
                document.version_at(version.entry.stream_number, version.revision);
            if (stream_version != nullptr &&
                stream_version->entry.type == XrefEntryType::InUse) {
                PdfObject object;
                if (parse_indirect_object(
                        document.data,
                        static_cast<std::size_t>(stream_version->entry.offset),
                        object)) {
                    info.file_offset = object.begin;
                    info.byte_length = object.end - object.begin;
                }
            }
        }
        assessment.objects.push_back(std::move(info));
    }

    if (assessment.violations.empty()) {
        assessment.decision = Decision::Valid;
        if (!assessment.has_incremental_updates) {
            assessment.reason = "signature covers the complete original revision";
        } else if (assessment.signature_covers_entire_document) {
            assessment.reason = "signature covers every revision";
        } else if (assessment.changes.empty()) {
            assessment.reason =
                "incremental xref, trailer, or metadata changes do not affect "
                "displayed content";
        } else {
            bool annotation_only = true;
            for (const Change& change : assessment.changes) {
                if (change.kind != ChangeKind::Annotation &&
                    change.kind != ChangeKind::Signature &&
                    change.kind != ChangeKind::Metadata) {
                    annotation_only = false;
                }
            }
            assessment.reason =
                annotation_only
                    ? "incremental updates are allowlisted annotation-only changes"
                    : "incremental updates are allowed by the configured policy";
        }
    } else {
        assessment.decision = Decision::UnauthenticatedIncrementalUpdate;
        assessment.reason =
            "the signature does not cover content-changing incremental updates";
    }
    assessment.visible_difference_summary = summarize_changes(assessment.changes);
    return assessment;
}

}  // namespace detail

using detail::Document;
using detail::ObjectVersion;
using detail::PdfObject;
using detail::Value;
using detail::parse_byte_range_array;
using detail::parse_indirect_object;
using detail::quote_json;

Policy Policy::pure_annotations(std::initializer_list<std::string> subtypes) {
    Policy policy;
    policy.allow_annotation_increments = true;
    for (const std::string& subtype : subtypes) {
        policy.allowed_annotation_subtypes.insert(subtype);
    }
    return policy;
}

Policy Policy::countersigning() {
    Policy policy;
    policy.allow_annotation_increments = true;
    policy.allow_countersignatures = true;
    policy.allowed_annotation_subtypes.insert("Widget");
    return policy;
}

Assessment assess(std::string_view pdf, std::span<const ByteRange> signed_ranges,
                  const Policy& policy) {
    return detail::assess_impl(pdf, signed_ranges, policy, false);
}

Assessment assess(std::string_view pdf, std::initializer_list<ByteRange> signed_ranges,
                  const Policy& policy) {
    return detail::assess_impl(pdf, std::span<const ByteRange>(signed_ranges.begin(),
                                                              signed_ranges.size()),
                               policy, false);
}

Assessment assess(std::string_view pdf, const std::vector<ByteRange>& signed_ranges,
                  const Policy& policy) {
    return detail::assess_impl(pdf,
                               std::span<const ByteRange>(signed_ranges.data(),
                                                          signed_ranges.size()),
                               policy, false);
}

Assessment assess(std::string_view pdf, const ByteRange& signed_range,
                  const Policy& policy) {
    return detail::assess_impl(pdf, std::span<const ByteRange>(&signed_range, 1), policy,
                               false);
}

Assessment assess(std::string_view pdf, const Policy& policy) {
    return detail::assess_impl(pdf, {}, policy, true);
}

Assessment assess_original_revision_only(std::string_view pdf, const Policy& policy) {
    Document document = detail::parse_document(pdf);
    if (!document.valid || document.revisions.empty()) {
        Assessment assessment;
        assessment.decision = Decision::MalformedDocument;
        assessment.reason = document.error.empty() ? "PDF parsing failed" : document.error;
        assessment.file_size = pdf.size();
        return assessment;
    }
    const ByteRange range{0, document.revisions.front().end};
    return detail::assess_impl(pdf, std::span<const ByteRange>(&range, 1), policy, false);
}

std::vector<ByteRange> extract_signature_byte_ranges(std::string_view pdf) {
    std::vector<ByteRange> ranges = detail::extract_ranges_from_raw(pdf);
    Document document = detail::parse_document(pdf);
    if (document.valid) {
        for (const ObjectVersion& version : document.versions) {
            if (version.entry.type != XrefEntryType::InUse ||
                version.entry.offset >= pdf.size()) {
                continue;
            }
            PdfObject object;
            if (!detail::parse_indirect_object(
                    pdf, static_cast<std::size_t>(version.entry.offset), object)) {
                continue;
            }
            const Value* byte_range_value = object.value.find("ByteRange");
            if (byte_range_value != nullptr) {
                std::vector<ByteRange> parsed =
                    detail::parse_byte_range_array(*byte_range_value);
                ranges.insert(ranges.end(), parsed.begin(), parsed.end());
            }
        }
    }
    std::sort(ranges.begin(), ranges.end(),
              [](const ByteRange& left, const ByteRange& right) {
                  return left.offset < right.offset ||
                         (left.offset == right.offset && left.length < right.length);
              });
    ranges.erase(std::unique(ranges.begin(), ranges.end()), ranges.end());
    return ranges;
}

std::string to_string(Decision decision) {
    switch (decision) {
        case Decision::Valid:
            return "valid";
        case Decision::InvalidSignatureScope:
            return "invalid_signature_scope";
        case Decision::UnauthenticatedIncrementalUpdate:
            return "unauthenticated_incremental_update";
        case Decision::MalformedDocument:
            return "malformed_document";
        case Decision::NoSignatureRange:
            return "no_signature_range";
    }
    return "unknown";
}

std::string to_string(RevisionKind kind) {
    return kind == RevisionKind::Original ? "original" : "incremental";
}

std::string to_string(XrefEntryType type) {
    switch (type) {
        case XrefEntryType::Free:
            return "free";
        case XrefEntryType::InUse:
            return "in_use";
        case XrefEntryType::Compressed:
            return "compressed";
    }
    return "unknown";
}

std::string to_string(ChangeKind kind) {
    switch (kind) {
        case ChangeKind::Text:
            return "text";
        case ChangeKind::Page:
            return "page";
        case ChangeKind::Annotation:
            return "annotation";
        case ChangeKind::FormValue:
            return "form_value";
        case ChangeKind::EmbeddedFile:
            return "embedded_file";
        case ChangeKind::DocumentStructure:
            return "document_structure";
        case ChangeKind::Signature:
            return "signature";
        case ChangeKind::Metadata:
            return "metadata";
        case ChangeKind::Other:
            return "other";
    }
    return "unknown";
}

std::string Assessment::to_json() const {
    std::ostringstream out;
    out << "{";
    out << "\"decision\":" << quote_json(to_string(decision));
    out << ",\"valid\":" << (valid() ? "true" : "false");
    out << ",\"reason\":" << quote_json(reason);
    out << ",\"filesize\":" << file_size;
    out << ",\"original_revision_size\":" << original_revision_size;
    out << ",\"signature_covers_original\":"
        << (signature_covers_original ? "true" : "false");
    out << ",\"signature_covers_entire_document\":"
        << (signature_covers_entire_document ? "true" : "false");
    out << ",\"has_incremental_updates\":"
        << (has_incremental_updates ? "true" : "false");
    out << ",\"signed_ranges\":[";
    for (std::size_t i = 0; i < signed_ranges.size(); ++i) {
        if (i != 0) {
            out << ",";
        }
        out << "{\"offset\":" << signed_ranges[i].offset
            << ",\"length\":" << signed_ranges[i].length << "}";
    }
    out << "],\"revisions\":[";
    for (std::size_t i = 0; i < revisions.size(); ++i) {
        const RevisionInfo& revision = revisions[i];
        if (i != 0) {
            out << ",";
        }
        out << "{\"index\":" << revision.index
            << ",\"kind\":" << quote_json(to_string(revision.kind))
            << ",\"begin\":" << revision.begin << ",\"end\":" << revision.end
            << ",\"covered_by_signature\":"
            << (revision.covered_by_signature ? "true" : "false")
            << ",\"covered_fraction\":" << revision.covered_fraction
            << ",\"linearized\":" << (revision.linearized ? "true" : "false")
            << ",\"xref_sections\":" << revision.xref_sections
            << ",\"object_versions\":" << revision.object_versions << "}";
    }
    out << "],\"objects\":[";
    for (std::size_t i = 0; i < objects.size(); ++i) {
        const ObjectRevisionInfo& object = objects[i];
        if (i != 0) {
            out << ",";
        }
        out << "{\"object\":" << object.object_number
            << ",\"generation\":" << object.generation
            << ",\"revision\":" << object.revision
            << ",\"xref_type\":" << quote_json(to_string(object.xref_type))
            << ",\"offset\":" << object.file_offset
            << ",\"length\":" << object.byte_length
            << ",\"compressed\":" << (object.compressed ? "true" : "false")
            << ",\"object_stream\":" << object.object_stream
            << ",\"stream_index\":" << object.index_in_object_stream
            << ",\"deleted\":" << (object.deleted ? "true" : "false")
            << ",\"reused_object_number\":"
            << (object.reused_object_number ? "true" : "false")
            << ",\"covered_by_signature\":"
            << (object.covered_by_signature ? "true" : "false")
            << ",\"type\":" << quote_json(object.type)
            << ",\"subtype\":" << quote_json(object.subtype)
            << ",\"impacts\":[";
        for (std::size_t j = 0; j < object.impacts.size(); ++j) {
            if (j != 0) {
                out << ",";
            }
            out << quote_json(object.impacts[j]);
        }
        out << "]}";
    }
    out << "],\"changes\":[";
    for (std::size_t i = 0; i < changes.size(); ++i) {
        const Change& change = changes[i];
        if (i != 0) {
            out << ",";
        }
        out << "{\"kind\":" << quote_json(to_string(change.kind))
            << ",\"revision\":" << change.revision
            << ",\"object\":" << change.object_number
            << ",\"category\":" << quote_json(change.category)
            << ",\"description\":" << quote_json(change.description)
            << ",\"before\":" << quote_json(change.before)
            << ",\"after\":" << quote_json(change.after)
            << ",\"subtype\":" << quote_json(change.subtype)
            << ",\"annotation_name\":" << quote_json(change.annotation_name)
            << ",\"hidden\":" << (change.hidden ? "true" : "false")
            << ",\"covered_by_signature\":"
            << (change.covered_by_signature ? "true" : "false")
            << ",\"allowed\":" << (change.allowed ? "true" : "false") << "}";
    }
    out << "],\"violations\":[";
    for (std::size_t i = 0; i < violations.size(); ++i) {
        if (i != 0) {
            out << ",";
        }
        out << quote_json(violations[i]);
    }
    out << "],\"summary\":" << quote_json(visible_difference_summary) << "}";
    return out.str();
}

RevisionOverrideDetector::RevisionOverrideDetector(Policy policy)
    : policy_(std::move(policy)) {}

Assessment RevisionOverrideDetector::assess(std::string_view pdf,
                                            std::span<const ByteRange> signed_ranges) const {
    return netsec::assess(pdf, signed_ranges, policy_);
}

Assessment RevisionOverrideDetector::assess(std::string_view pdf,
                                            const Policy& policy) const {
    return netsec::assess(pdf, policy);
}

Assessment RevisionOverrideDetector::assess(std::string_view pdf) const {
    return netsec::assess(pdf, policy_);
}

}  // namespace netsec
