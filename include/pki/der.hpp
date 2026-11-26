#pragma once

#include <cstddef>
#include <cstdint>
#include <stdexcept>
#include <string>
#include <vector>

namespace pki::der {

using Byte = std::uint8_t;
using Bytes = std::vector<Byte>;

class ByteView {
public:
    constexpr ByteView() = default;
    constexpr ByteView(const Byte* data, std::size_t size) : data_(data), size_(size) {}
    ByteView(const Bytes& bytes) : data_(bytes.data()), size_(bytes.size()) {}

    [[nodiscard]] constexpr const Byte* data() const noexcept { return data_; }
    [[nodiscard]] constexpr std::size_t size() const noexcept { return size_; }
    [[nodiscard]] constexpr bool empty() const noexcept { return size_ == 0; }

    [[nodiscard]] const Byte* begin() const noexcept { return data_; }
    [[nodiscard]] const Byte* end() const noexcept { return data_ + size_; }

    [[nodiscard]] Byte at(std::size_t index) const;
    [[nodiscard]] Byte operator[](std::size_t index) const { return data_[index]; }

    [[nodiscard]] Bytes to_bytes() const;
    [[nodiscard]] std::string to_latin1() const;

private:
    const Byte* data_ = nullptr;
    std::size_t size_ = 0;
};

class DerError : public std::runtime_error {
public:
    explicit DerError(const std::string& message);
};

enum class TagClass : std::uint8_t {
    Universal = 0,
    Application = 1,
    Context = 2,
    Private = 3,
};

struct Tag {
    TagClass klass = TagClass::Universal;
    bool constructed = false;
    std::uint32_t number = 0;
    Byte first_byte = 0;
};

struct Element {
    Tag tag{};
    ByteView raw;
    ByteView header;
    ByteView content;
};

class Reader {
public:
    explicit Reader(ByteView input) : input_(input) {}

    [[nodiscard]] Element read_element();
    [[nodiscard]] bool eof() const noexcept { return position_ >= input_.size(); }
    [[nodiscard]] std::size_t remaining() const noexcept { return input_.size() - position_; }
    [[nodiscard]] ByteView remaining_view() const noexcept;
    void require_eof() const;

private:
    ByteView input_;
    std::size_t position_ = 0;
};

[[nodiscard]] Element parse_one(ByteView input);
[[nodiscard]] std::vector<Element> parse_children(const Element& element);
Element expect_tag(const Element& element, Byte expected_first_byte);
Element expect_universal(const Element& element, Byte expected_tag_byte);

[[nodiscard]] bool decode_boolean(const Element& element);
[[nodiscard]] std::uint64_t decode_uint(const Element& element);
[[nodiscard]] std::int64_t decode_int64(const Element& element);
[[nodiscard]] std::string decode_oid(const Element& element);
[[nodiscard]] std::string decode_oid(ByteView content);

struct BitString {
    ByteView bytes;
    Byte unused_bits = 0;

    [[nodiscard]] bool bit(std::size_t index) const;
};

[[nodiscard]] BitString decode_bit_string(const Element& element);
[[nodiscard]] Bytes decode_octet_string(const Element& element);
[[nodiscard]] std::string decode_string(const Element& element);

[[nodiscard]] Bytes encode_length(std::size_t length);
[[nodiscard]] Bytes encode_tlv(Byte tag, ByteView content);
[[nodiscard]] Bytes encode_sequence(const Bytes& content);
[[nodiscard]] Bytes encode_set(const Bytes& content);
[[nodiscard]] Bytes encode_boolean(bool value);
[[nodiscard]] Bytes encode_integer(std::uint64_t value);
[[nodiscard]] Bytes encode_integer_signed(std::int64_t value);
[[nodiscard]] Bytes encode_octet_string(ByteView content);
[[nodiscard]] Bytes encode_bit_string(ByteView content, Byte unused_bits = 0);
[[nodiscard]] Bytes encode_oid(const std::string& oid);
[[nodiscard]] Bytes encode_null();
[[nodiscard]] Bytes encode_ia5(const std::string& value);
[[nodiscard]] Bytes encode_utf8(const std::string& value);
[[nodiscard]] Bytes encode_printable(const std::string& value);
[[nodiscard]] Bytes encode_utc_time(const std::string& value);
[[nodiscard]] Bytes encode_generalized_time(const std::string& value);
[[nodiscard]] Bytes encode_context(Byte number, const Bytes& content, bool constructed);
[[nodiscard]] Bytes encode_explicit(Byte number, const Bytes& inner_tlv);

} // namespace pki::der
