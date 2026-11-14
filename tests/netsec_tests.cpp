#include "netsec.hpp"

#include <cstddef>
#include <iostream>
#include <optional>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace {

class TestRunner {
public:
    void check(bool condition, std::string_view expression, int line) {
        if (!condition) {
            ++failures_;
            std::cerr << "FAIL line " << line << ": " << expression << '\n';
        }
    }

    void check_equal(
        const std::string& actual,
        const std::string& expected,
        std::string_view expression,
        int line) {
        if (actual != expected) {
            ++failures_;
            std::cerr << "FAIL line " << line << ": " << expression << "\n  expected: "
                      << expected << "\n  actual:   " << actual << '\n';
        }
    }

    [[nodiscard]] int failures() const noexcept { return failures_; }

private:
    int failures_{0};
};

netsec::FieldCodec make_codec() {
    return netsec::FieldCodec({
        {"20", netsec::FieldKind::Reference, 16},
        {"32A", netsec::FieldKind::Amount, 18},
        {"70", netsec::FieldKind::SwiftText, 140},
        {"72", netsec::FieldKind::Utf8Text, 8},
    });
}

netsec::FieldCodec make_short_codec() {
    return netsec::FieldCodec({
        {"20", netsec::FieldKind::Reference, 6},
        {"32A", netsec::FieldKind::Amount, 8},
        {"70", netsec::FieldKind::SwiftText, 4},
        {"72", netsec::FieldKind::Utf8Text, 4},
    });
}

void expect_error(
    TestRunner& test,
    const netsec::FieldCodec& codec,
    std::string value,
    netsec::ErrorCode expected,
    int line) {
    netsec::ValidationResult result;
    auto encoded = codec.try_encode(
        std::vector<netsec::Field>{{"70", std::move(value)}},
        &result);
    test.check(!encoded.has_value(), "invalid field must not serialize", line);
    test.check(!result.ok(), "invalid field must produce a validation error", line);
    const auto* error = result.first_error();
    test.check(error != nullptr, "validation result must contain an error", line);
    if (error != nullptr) {
        test.check(error->code == expected, "validation error code must match", line);
        test.check(error->field_index == 0, "validation error must identify the field", line);
    }
}

void test_legal_message_and_roundtrip(TestRunner& test) {
    auto codec = make_codec();
    const std::vector<netsec::Field> fields = {
        {"20", "REF-20261109-01"},
        {"32A", "1250.00"},
        {"70", "Invoice 42 paid in full"},
    };
    const std::string expected =
        ":20:REF-20261109-01\n"
        ":32A:1250.00\n"
        ":70:Invoice 42 paid in full\n";

    test.check(codec.validate(fields).ok(), "legal fields must validate", __LINE__);
    test.check_equal(codec.encode(fields), expected, "legal serialization", __LINE__);

    const auto decoded = codec.decode(expected);
    test.check(decoded.size() == fields.size(), "round trip must preserve field count", __LINE__);
    for (std::size_t index = 0; index < fields.size() && index < decoded.size(); ++index) {
        test.check(decoded[index].tag == fields[index].tag, "round trip must preserve tag order", __LINE__);
        test.check(decoded[index].value == fields[index].value, "round trip must preserve value", __LINE__);
    }
    test.check_equal(codec.encode(decoded), expected, "round trip must be stable", __LINE__);
}

void test_boundary_injection(TestRunner& test) {
    auto codec = make_codec();
    expect_error(test, codec, "safe\n:32A:9999.00", netsec::ErrorCode::LineBreak, __LINE__);
    expect_error(test, codec, "safe\r:32A:9999.00", netsec::ErrorCode::LineBreak, __LINE__);
    expect_error(test, codec, "safe\r\n:32A:9999.00", netsec::ErrorCode::LineBreak, __LINE__);
    expect_error(test, codec, "safe:32A:9999.00", netsec::ErrorCode::FieldSeparator, __LINE__);
    expect_error(test, codec, ":32A:9999.00", netsec::ErrorCode::FieldSeparator, __LINE__);
    expect_error(test, codec, "safe\n", netsec::ErrorCode::LineBreak, __LINE__);
    expect_error(test, codec, "safe\r", netsec::ErrorCode::LineBreak, __LINE__);

    netsec::ValidationResult result;
    const std::vector<netsec::Field> fields = {
        {"20", "REF-1"},
        {"32A", "10.00"},
        {"70", "pay\n:32A:9999.00\n:57A:BANK"},
    };
    auto encoded = codec.try_encode(fields, &result);
    test.check(!encoded.has_value(), "field boundary injection must not serialize", __LINE__);
    test.check(result.first_error() != nullptr, "boundary injection must report an error", __LINE__);
    if (result.first_error() != nullptr) {
        test.check(result.first_error()->code == netsec::ErrorCode::LineBreak, "boundary injection code", __LINE__);
        test.check(result.first_error()->field_index == 2, "boundary injection field index", __LINE__);
    }
}

void test_charset_bypass(TestRunner& test) {
    auto codec = make_codec();
    expect_error(test, codec, "safe$value", netsec::ErrorCode::DisallowedCharacter, __LINE__);
    expect_error(test, codec, "safe@value", netsec::ErrorCode::DisallowedCharacter, __LINE__);
    expect_error(test, codec, "safe\tvalue", netsec::ErrorCode::ControlCharacter, __LINE__);
    expect_error(test, codec, "safe\x0Bvalue", netsec::ErrorCode::ControlCharacter, __LINE__);
    expect_error(test, codec, "safe\x0Cvalue", netsec::ErrorCode::ControlCharacter, __LINE__);

    std::string nul_value = "safe";
    nul_value.push_back('\0');
    nul_value += "value";
    netsec::ValidationResult result;
    auto encoded = codec.try_encode(
        std::vector<netsec::Field>{{"70", nul_value}},
        &result);
    test.check(!encoded.has_value(), "NUL must be rejected", __LINE__);
    test.check(result.first_error() != nullptr
            && result.first_error()->code == netsec::ErrorCode::ControlCharacter,
        "NUL error code", __LINE__);

    expect_error(test, codec, "safe\xC2\x85value", netsec::ErrorCode::LineBreak, __LINE__);
    expect_error(test, codec, "safe\xE2\x80\xA8value", netsec::ErrorCode::LineBreak, __LINE__);
    expect_error(test, codec, "safe\xE2\x80\xA9value", netsec::ErrorCode::LineBreak, __LINE__);
    expect_error(test, codec, "safe\xEF\xBC\x9Avalue", netsec::ErrorCode::FieldSeparator, __LINE__);
    expect_error(test, codec, "safe\xE2\x80\x8Bvalue", netsec::ErrorCode::DisallowedCharacter, __LINE__);
    expect_error(test, codec, "safe\xE2\x80\xAEvalue", netsec::ErrorCode::DisallowedCharacter, __LINE__);
    expect_error(test, codec, "safe\xC2\xA0value", netsec::ErrorCode::DisallowedCharacter, __LINE__);
    expect_error(test, codec, "safe\xE4\xBD\xA0value", netsec::ErrorCode::DisallowedCharacter, __LINE__);
    expect_error(test, codec, "safe\xF0\x9F\x98\x80value", netsec::ErrorCode::DisallowedCharacter, __LINE__);
    expect_error(test, codec, "safe\xC0\xAFvalue", netsec::ErrorCode::InvalidUtf8, __LINE__);
    expect_error(test, codec, "safe\xE2\x28\xA1value", netsec::ErrorCode::InvalidUtf8, __LINE__);
    expect_error(test, codec, "safe\xF0\x9Fvalue", netsec::ErrorCode::InvalidUtf8, __LINE__);
    expect_error(test, codec, "safe\xED\xA0\x80value", netsec::ErrorCode::InvalidUtf8, __LINE__);
    expect_error(test, codec, "safe\xF4\x90\x80\x80value", netsec::ErrorCode::InvalidUtf8, __LINE__);

    expect_error(test, codec, "safe\xE4\xBD\xA0value", netsec::ErrorCode::DisallowedCharacter, __LINE__);
    const std::vector<netsec::Field> utf8_fields = {
        {"72", "\xE4\xBD\xA0\xE4\xBD\xA0"},
    };
    test.check(codec.validate(utf8_fields).ok(), "UTF-8 characters are valid in UTF-8 fields", __LINE__);
}

void test_length_and_fixed_formats(TestRunner& test) {
    auto codec = make_codec();
    auto short_codec = make_short_codec();

    constexpr std::size_t amount_fixture_length = 18;
    test.check(std::string("123456789012345.00").size() == amount_fixture_length,
        "amount fixture length", __LINE__);
    test.check(codec.validate(netsec::Field{"32A", "123456789012345.00"}).ok(),
        "18-character amount must be valid", __LINE__);
    test.check(!codec.validate(netsec::Field{"32A", "1234567890123456.00"}).ok(),
        "19-character amount must be invalid", __LINE__);
    test.check(codec.validate(netsec::Field{"32A", "0.00"}).ok(), "zero amount must be valid", __LINE__);
    test.check(!codec.validate(netsec::Field{"32A", "123.4"}).ok(), "amount requires two decimals", __LINE__);
    test.check(!codec.validate(netsec::Field{"32A", "1,234.56"}).ok(), "amount forbids grouping", __LINE__);
    test.check(!codec.validate(netsec::Field{"32A", "-1.00"}).ok(), "amount forbids sign", __LINE__);
    test.check(!codec.validate(netsec::Field{"32A", "1.000"}).ok(), "amount forbids three decimals", __LINE__);
    test.check(!codec.validate(netsec::Field{"32A", ".99"}).ok(), "amount requires integer digits", __LINE__);
    test.check(!codec.validate(netsec::Field{"32A", "1."}).ok(), "amount requires decimal digits", __LINE__);

    test.check(codec.validate(netsec::Field{"20", "REF-20261109-01"}).ok(),
        "16-character reference must be valid", __LINE__);
    test.check(!codec.validate(netsec::Field{"20", "REF-20261109-0111"}).ok(),
        "17-character reference must be invalid", __LINE__);
    test.check(!codec.validate(netsec::Field{"20", "REF 1"}).ok(), "reference forbids spaces", __LINE__);
    test.check(!codec.validate(netsec::Field{"20", "_REF"}).ok(), "reference forbids leading underscore", __LINE__);
    test.check(!codec.validate(netsec::Field{"20", "REF_1"}).ok(), "reference forbids underscore", __LINE__);
    test.check(!codec.validate(netsec::Field{"20", "REF:1"}).ok(), "reference forbids colon", __LINE__);

    test.check(short_codec.validate(netsec::Field{"70", "ABCD"}).ok(), "length at limit must pass", __LINE__);
    test.check(!short_codec.validate(netsec::Field{"70", "ABCDE"}).ok(), "length above limit must fail", __LINE__);
    test.check(short_codec.validate(netsec::Field{"72", "\xE4\xBD\xA0\xE4\xBD\xA0"}).ok(),
        "UTF-8 length must count characters rather than bytes", __LINE__);
    test.check(!short_codec.validate(
            netsec::Field{"72", "\xE4\xBD\xA0\xE4\xBD\xA0\xE4\xBD\xA0\xE4\xBD\xA0\x41"}).ok(),
        "UTF-8 length overflow must use character count", __LINE__);
    test.check(short_codec.validate(netsec::Field{"72", "\xE4\xBD\xA0\xE4\xBD\xA0\x41\x42"}).ok(),
        "UTF-8 fixture at character limit must pass", __LINE__);
}

void test_parser_structure(TestRunner& test) {
    auto codec = make_codec();

    const auto decoded = codec.decode(":20:REF-1\n:32A:10.00\n:70:hello\n");
    test.check(decoded.size() == 3, "decoder must report field count", __LINE__);
    test.check(decoded[0].tag == "20" && decoded[1].tag == "32A" && decoded[2].tag == "70",
        "decoder must preserve field order", __LINE__);

    const std::vector<std::pair<std::string, netsec::ErrorCode>> malformed = {
        {"20:REF-1\n", netsec::ErrorCode::MalformedMessage},
        {":20:REF-1", netsec::ErrorCode::MalformedMessage},
        {":70\n", netsec::ErrorCode::MalformedMessage},
        {":32a:10.00\n", netsec::ErrorCode::InvalidTag},
        {":99:X\n", netsec::ErrorCode::UnknownField},
        {":70:hello:world\n", netsec::ErrorCode::FieldSeparator},
        {":70:hello\r\n", netsec::ErrorCode::LineBreak},
        {":70:\n", netsec::ErrorCode::EmptyValue},
    };
    for (const auto& item : malformed) {
        netsec::ValidationResult result;
        auto parsed = codec.try_decode(item.first, &result);
        test.check(!parsed.has_value(), "malformed wire must be rejected", __LINE__);
        test.check(result.first_error() != nullptr, "malformed wire must report an error", __LINE__);
        if (result.first_error() != nullptr) {
            test.check(result.first_error()->code == item.second, "malformed wire error code", __LINE__);
        }
    }
}

void test_spec_constructor(TestRunner& test) {
    bool threw = false;
    try {
        netsec::FieldCodec codec({{"2", netsec::FieldKind::SwiftText, 4}});
        (void)codec;
    } catch (const std::invalid_argument&) {
        threw = true;
    }
    test.check(threw, "invalid field tag spec must be rejected", __LINE__);

    threw = false;
    try {
        netsec::FieldCodec codec({{"70", netsec::FieldKind::SwiftText, 0}});
        (void)codec;
    } catch (const std::invalid_argument&) {
        threw = true;
    }
    test.check(threw, "zero max_chars spec must be rejected", __LINE__);

    threw = false;
    try {
        netsec::FieldCodec codec({
            {"70", netsec::FieldKind::SwiftText, 4},
            {"70", netsec::FieldKind::SwiftText, 4},
        });
        (void)codec;
    } catch (const std::invalid_argument&) {
        threw = true;
    }
    test.check(threw, "duplicate spec tag must be rejected", __LINE__);
}

void test_try_api(TestRunner& test) {
    auto codec = make_codec();
    netsec::ValidationResult result;
    const std::vector<netsec::Field> fields = {{"70", "safe remark"}};
    auto encoded = codec.try_encode(fields, &result);
    test.check(encoded.has_value(), "valid try_encode must succeed", __LINE__);
    test.check(result.ok(), "valid try_encode result must be ok", __LINE__);
    if (encoded.has_value()) {
        test.check_equal(*encoded, ":70:safe remark\n", "try_encode output", __LINE__);
        auto decoded = codec.try_decode(*encoded, &result);
        test.check(decoded.has_value(), "valid try_decode must succeed", __LINE__);
        test.check(result.ok(), "valid try_decode result must be ok", __LINE__);
    }
}

}  // namespace

int main() {
    TestRunner test;
    test_legal_message_and_roundtrip(test);
    test_boundary_injection(test);
    test_charset_bypass(test);
    test_length_and_fixed_formats(test);
    test_parser_structure(test);
    test_spec_constructor(test);
    test_try_api(test);

    if (test.failures() != 0) {
        std::cerr << test.failures() << " test assertion(s) failed\n";
        return 1;
    }
    std::cout << "all field codec tests passed\n";
    return 0;
}
