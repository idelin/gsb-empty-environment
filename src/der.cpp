#include "pki/der.hpp"

#include <algorithm>
#include <limits>
#include <sstream>

namespace pki::der {

Byte ByteView::at(std::size_t index) const {
    if (data_ == nullptr || index >= size_) {
        throw DerError("DER byte view index out of range");
    }
    return data_[index];
}

Bytes ByteView::to_bytes() const {
    if (data_ == nullptr || size_ == 0) {
        return {};
    }
    return Bytes(data_, data_ + size_);
}

std::string ByteView::to_latin1() const {
    if (data_ == nullptr || size_ == 0) {
        return {};
    }
    return std::string(reinterpret_cast<const char*>(data_), size_);
}

DerError::DerError(const std::string& message) : std::runtime_error(message) {}

namespace {

[[noreturn]] void fail(const std::string& message) {
    throw DerError(message);
}

Tag read_tag(ByteView input, std::size_t& position) {
    if (position >= input.size()) {
        fail("truncated DER tag");
    }
    const Byte first = input[position++];
    Tag tag;
    tag.first_byte = first;
    tag.klass = static_cast<TagClass>((first >> 6) & 0x03U);
    tag.constructed = (first & 0x20U) != 0;
    tag.number = static_cast<std::uint32_t>(first & 0x1FU);

    if (tag.number != 0x1FU) {
        return tag;
    }

    tag.number = 0;
    bool first_subsequent = true;
    bool saw_continuation = false;
    while (true) {
        if (position >= input.size()) {
            fail("truncated high-tag-number");
        }
        const Byte octet = input[position++];
        if (first_subsequent && octet == 0x80U) {
            fail("non-minimal high-tag-number encoding");
        }
        first_subsequent = false;
        if (tag.number > (std::numeric_limits<std::uint32_t>::max() >> 7U)) {
            fail("tag number overflow");
        }
        tag.number = (tag.number << 7U) | static_cast<std::uint32_t>(octet & 0x7FU);
        if ((octet & 0x80U) == 0) {
            break;
        }
        saw_continuation = true;
    }
    if (!saw_continuation || tag.number < 31) {
        fail("non-minimal high-tag-number form");
    }
    return tag;
}

std::size_t read_length(ByteView input, std::size_t& position) {
    if (position >= input.size()) {
        fail("truncated DER length");
    }
    const Byte first = input[position++];
    if (first == 0x80U) {
        fail("indefinite-length BER encoding is forbidden");
    }
    if ((first & 0x80U) == 0) {
        return static_cast<std::size_t>(first);
    }

    const std::size_t count = static_cast<std::size_t>(first & 0x7FU);
    if (count == 0) {
        fail("indefinite-length BER encoding is forbidden");
    }
    if (count > sizeof(std::uint64_t)) {
        fail("DER length is too large");
    }
    if (position + count > input.size()) {
        fail("truncated DER long-form length");
    }

    std::uint64_t value = 0;
    Byte first_content_octet = 0;
    for (std::size_t i = 0; i < count; ++i) {
        const Byte octet = input[position++];
        if (i == 0) {
            first_content_octet = octet;
        }
        if (value > (std::numeric_limits<std::uint64_t>::max() >> 8U)) {
            fail("DER length overflow");
        }
        value = (value << 8U) | octet;
    }

    if (count > 1 && first_content_octet == 0) {
        fail("non-minimal DER length: leading zero");
    }
    if (count == 1 && value < 128U) {
        fail("non-minimal DER long-form length");
    }
    if (value > static_cast<std::uint64_t>(std::numeric_limits<std::size_t>::max())) {
        fail("DER length exceeds platform size");
    }
    return static_cast<std::size_t>(value);
}

} // namespace

Element Reader::read_element() {
    const std::size_t start = position_;
    Tag tag = read_tag(input_, position_);
    const std::size_t header_end_before_length = position_;
    const std::size_t length = read_length(input_, position_);
    const std::size_t content_start = position_;

    if (length > input_.size() - position_) {
        fail("DER element content is truncated");
    }
    position_ += length;

    Element element;
    element.tag = tag;
    element.content = ByteView(input_.data() + content_start, length);
    element.header = ByteView(input_.data() + start, content_start - start);
    element.raw = ByteView(input_.data() + start, position_ - start);
    (void)header_end_before_length;
    return element;
}

ByteView Reader::remaining_view() const noexcept {
    if (position_ >= input_.size()) {
        return {};
    }
    return ByteView(input_.data() + position_, input_.size() - position_);
}

void Reader::require_eof() const {
    if (position_ != input_.size()) {
        fail("unexpected trailing DER bytes");
    }
}

Element parse_one(ByteView input) {
    Reader reader(input);
    Element element = reader.read_element();
    reader.require_eof();
    return element;
}

std::vector<Element> parse_children(const Element& element) {
    Reader reader(element.content);
    std::vector<Element> children;
    while (!reader.eof()) {
        children.push_back(reader.read_element());
    }
    reader.require_eof();
    return children;
}

Element expect_tag(const Element& element, Byte expected_first_byte) {
    if (element.tag.first_byte != expected_first_byte) {
        fail("unexpected DER tag");
    }
    return element;
}

Element expect_universal(const Element& element, Byte expected_tag_byte) {
    if (element.tag.klass != TagClass::Universal ||
        element.tag.first_byte != expected_tag_byte) {
        fail("unexpected universal DER tag");
    }
    return element;
}

bool decode_boolean(const Element& element) {
    expect_universal(element, 0x01);
    if (element.content.size() != 1) {
        fail("DER BOOLEAN must contain exactly one byte");
    }
    const Byte value = element.content[0];
    if (value != 0x00U && value != 0xFFU) {
        fail("non-canonical DER BOOLEAN value");
    }
    return value == 0xFFU;
}

namespace {

ByteView integer_content(const Element& element) {
    expect_universal(element, 0x02);
    if (element.content.empty()) {
        fail("INTEGER is empty");
    }
    if (element.content.size() > 1) {
        const Byte first = element.content[0];
        const Byte second = element.content[1];
        if (first == 0x00U && (second & 0x80U) == 0) {
            fail("redundant leading 0x00 in INTEGER");
        }
        if (first == 0xFFU && (second & 0x80U) != 0) {
            fail("redundant leading 0xFF in INTEGER");
        }
    }
    return element.content;
}

} // namespace

std::uint64_t decode_uint(const Element& element) {
    const ByteView content = integer_content(element);
    if ((content[0] & 0x80U) != 0) {
        fail("expected non-negative INTEGER");
    }

    std::uint64_t value = 0;
    for (Byte octet : content) {
        if (value > (std::numeric_limits<std::uint64_t>::max() >> 8U)) {
            fail("INTEGER value overflows uint64");
        }
        value = (value << 8U) | octet;
    }
    return value;
}

std::int64_t decode_int64(const Element& element) {
    const ByteView content = integer_content(element);
    std::uint64_t bits = 0;
    for (Byte octet : content) {
        bits = (bits << 8U) | octet;
    }

    const std::size_t bit_count = content.size() * 8U;
    if (bit_count < 64 && (content[0] & 0x80U) != 0) {
        const std::uint64_t sign_extension = std::numeric_limits<std::uint64_t>::max() << bit_count;
        bits |= sign_extension;
    }
    return static_cast<std::int64_t>(bits);
}

std::string decode_oid(ByteView content) {
    if (content.empty()) {
        fail("OBJECT IDENTIFIER is empty");
    }

    std::vector<std::uint64_t> arcs;
    std::uint64_t current = 0;
    bool have_value = false;
    bool first_byte_of_arc = true;

    for (std::size_t i = 0; i < content.size(); ++i) {
        const Byte octet = content[i];
        if (first_byte_of_arc && octet == 0x80U) {
            fail("non-minimal OID base-128 encoding");
        }
        first_byte_of_arc = false;
        if (current > (std::numeric_limits<std::uint64_t>::max() >> 7U)) {
            fail("OID arc overflow");
        }
        current = (current << 7U) | static_cast<std::uint64_t>(octet & 0x7FU);
        have_value = true;
        if ((octet & 0x80U) == 0) {
            arcs.push_back(current);
            current = 0;
            have_value = false;
            first_byte_of_arc = true;
        }
    }
    if (have_value) {
        fail("truncated OID base-128 arc");
    }
    if (arcs.empty()) {
        fail("OBJECT IDENTIFIER has no arcs");
    }

    std::ostringstream out;
    const std::uint64_t first = arcs[0];
    std::uint64_t first_arc = 0;
    std::uint64_t second_arc = first;
    if (first < 40U) {
        first_arc = 0;
        second_arc = first;
    } else if (first < 80U) {
        first_arc = 1;
        second_arc = first - 40U;
    } else {
        first_arc = 2;
        second_arc = first - 80U;
    }
    out << first_arc << '.' << second_arc;
    for (std::size_t i = 1; i < arcs.size(); ++i) {
        out << '.' << arcs[i];
    }
    return out.str();
}

std::string decode_oid(const Element& element) {
    expect_universal(element, 0x06);
    return decode_oid(element.content);
}

BitString decode_bit_string(const Element& element) {
    expect_universal(element, 0x03);
    if (element.content.empty()) {
        fail("BIT STRING is empty");
    }
    const Byte unused = element.content[0];
    if (unused > 7U) {
        fail("BIT STRING unused-bit count is invalid");
    }

    if (element.content.size() >= 2) {
        const Byte last = element.content[element.content.size() - 1];
        const Byte trailing_mask = static_cast<Byte>((1U << unused) - 1U);
        if (unused != 0 && (last & trailing_mask) != 0) {
            fail("BIT STRING unused bits must be zero");
        }
    } else if (unused != 0) {
        fail("empty BIT STRING has non-zero unused-bit count");
    }

    BitString result;
    result.unused_bits = unused;
    result.bytes = ByteView(element.content.data() + 1, element.content.size() - 1);
    return result;
}

bool BitString::bit(std::size_t index) const {
    const std::size_t total_bits = bytes.size() * 8U;
    const std::size_t used_bits = total_bits - unused_bits;
    if (index >= used_bits) {
        return false;
    }
    const std::size_t byte_index = index / 8U;
    const std::size_t bit_index = index % 8U;
    return (bytes[byte_index] & static_cast<Byte>(0x80U >> bit_index)) != 0;
}

Bytes decode_octet_string(const Element& element) {
    expect_universal(element, 0x04);
    return element.content.to_bytes();
}

std::string decode_string(const Element& element) {
    return element.content.to_latin1();
}

Bytes encode_length(std::size_t length) {
    if (length < 128U) {
        return {static_cast<Byte>(length)};
    }

    Bytes big_endian;
    std::size_t value = length;
    while (value > 0) {
        big_endian.push_back(static_cast<Byte>(value & 0xFFU));
        value >>= 8U;
    }
    std::reverse(big_endian.begin(), big_endian.end());
    Bytes result;
    result.push_back(static_cast<Byte>(0x80U | static_cast<unsigned>(big_endian.size())));
    result.insert(result.end(), big_endian.begin(), big_endian.end());
    return result;
}

Bytes encode_tlv(Byte tag, ByteView content) {
    Bytes result;
    result.reserve(1 + sizeof(std::size_t) + content.size());
    result.push_back(tag);
    const Bytes length = encode_length(content.size());
    result.insert(result.end(), length.begin(), length.end());
    result.insert(result.end(), content.begin(), content.end());
    return result;
}

Bytes encode_sequence(const Bytes& content) {
    return encode_tlv(0x30, content);
}

Bytes encode_set(const Bytes& content) {
    return encode_tlv(0x31, content);
}

Bytes encode_boolean(bool value) {
    return encode_tlv(0x01, Bytes{value ? static_cast<Byte>(0xFFU) : static_cast<Byte>(0x00U)});
}

Bytes encode_integer(std::uint64_t value) {
    Bytes bytes;
    if (value == 0) {
        bytes.push_back(0x00);
    } else {
        while (value > 0) {
            bytes.push_back(static_cast<Byte>(value & 0xFFU));
            value >>= 8U;
        }
        std::reverse(bytes.begin(), bytes.end());
        if ((bytes[0] & 0x80U) != 0) {
            bytes.insert(bytes.begin(), 0x00);
        }
    }
    return encode_tlv(0x02, bytes);
}

Bytes encode_integer_signed(std::int64_t value) {
    auto bits = static_cast<std::uint64_t>(value);
    Bytes bytes(8);
    for (std::size_t i = 0; i < 8; ++i) {
        bytes[7 - i] = static_cast<Byte>(bits & 0xFFU);
        bits >>= 8U;
    }
    while (bytes.size() > 1) {
        const Byte first = bytes[0];
        const Byte second = bytes[1];
        const bool redundant_positive = first == 0x00U && (second & 0x80U) == 0;
        const bool redundant_negative = first == 0xFFU && (second & 0x80U) != 0;
        if (!redundant_positive && !redundant_negative) {
            break;
        }
        bytes.erase(bytes.begin());
    }
    return encode_tlv(0x02, bytes);
}

Bytes encode_octet_string(ByteView content) {
    return encode_tlv(0x04, content);
}

Bytes encode_bit_string(ByteView content, Byte unused_bits) {
    Bytes framed;
    framed.push_back(unused_bits);
    framed.insert(framed.end(), content.begin(), content.end());
    return encode_tlv(0x03, framed);
}

Bytes encode_oid(const std::string& oid) {
    std::vector<std::uint64_t> arcs;
    std::size_t start = 0;
    while (start <= oid.size()) {
        const std::size_t dot = oid.find('.', start);
        const std::string part = oid.substr(start, dot == std::string::npos ? std::string::npos : dot - start);
        arcs.push_back(static_cast<std::uint64_t>(std::stoull(part)));
        if (dot == std::string::npos) {
            break;
        }
        start = dot + 1;
    }
    if (arcs.size() < 2) {
        throw DerError("OID requires at least two arcs");
    }

    Bytes content;
    const std::uint64_t first_encoded = arcs[0] * 40U + arcs[1];
    auto append_arc = [&content](std::uint64_t value) {
        Bytes groups;
        groups.push_back(static_cast<Byte>(value & 0x7FU));
        value >>= 7U;
        while (value > 0) {
            groups.push_back(static_cast<Byte>(value & 0x7FU));
            value >>= 7U;
        }
        for (std::size_t i = groups.size(); i-- > 0;) {
            Byte octet = groups[i];
            if (i != 0) {
                octet |= 0x80U;
            }
            content.push_back(octet);
        }
    };
    append_arc(first_encoded);
    for (std::size_t i = 2; i < arcs.size(); ++i) {
        append_arc(arcs[i]);
    }
    return encode_tlv(0x06, content);
}

Bytes encode_null() {
    return encode_tlv(0x05, ByteView());
}

Bytes encode_ia5(const std::string& value) {
    return encode_tlv(0x16, ByteView(reinterpret_cast<const Byte*>(value.data()), value.size()));
}

Bytes encode_utf8(const std::string& value) {
    return encode_tlv(0x0C, ByteView(reinterpret_cast<const Byte*>(value.data()), value.size()));
}

Bytes encode_printable(const std::string& value) {
    return encode_tlv(0x13, ByteView(reinterpret_cast<const Byte*>(value.data()), value.size()));
}

Bytes encode_utc_time(const std::string& value) {
    return encode_tlv(0x17, ByteView(reinterpret_cast<const Byte*>(value.data()), value.size()));
}

Bytes encode_generalized_time(const std::string& value) {
    return encode_tlv(0x18, ByteView(reinterpret_cast<const Byte*>(value.data()), value.size()));
}

Bytes encode_context(Byte number, const Bytes& content, bool constructed) {
    if (number > 30U) {
        throw DerError("builder supports only low context tags");
    }
    Byte tag = static_cast<Byte>(0x80U | number);
    if (constructed) {
        tag |= 0x20U;
    }
    return encode_tlv(tag, content);
}

Bytes encode_explicit(Byte number, const Bytes& inner_tlv) {
    return encode_context(number, inner_tlv, true);
}

} // namespace pki::der
