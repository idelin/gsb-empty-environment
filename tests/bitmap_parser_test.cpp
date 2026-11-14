#include "netsec.hpp"

#include <cstdlib>
#include <initializer_list>
#include <iostream>
#include <string>

namespace {

int g_failures = 0;

#define CHECK(cond)                                                  \
    do {                                                             \
        if (!(cond)) {                                               \
            ++g_failures;                                            \
            std::cerr << "FAIL " << __FILE__ << ':' << __LINE__      \
                      << ": " #cond << '\n';                         \
        }                                                            \
    } while (0)

std::string make_bitmap(std::initializer_list<int> fields) {
    bool secondary = false;
    for (int f : fields) {
        if (f > 64) secondary = true;
    }
    std::string bitmap(secondary ? 16 : 8, '\0');
    auto set_bit = [&bitmap](int f) {
        bitmap[static_cast<std::size_t>(f - 1) / 8] =
            static_cast<char>(static_cast<unsigned char>(
                bitmap[static_cast<std::size_t>(f - 1) / 8]) |
                (0x80u >> ((f - 1) % 8)));
    };
    if (secondary) set_bit(1);
    for (int f : fields) set_bit(f);
    return bitmap;
}

netsec::BitmapParser make_parser(bool negotiate_field70) {
    std::array<std::optional<netsec::FieldSpec>, netsec::kMaxFieldNumber + 1> specs{};
    std::array<netsec::SemanticRule, netsec::kMaxFieldNumber + 1> semantics{};
    std::bitset<netsec::kMaxFieldNumber + 1> negotiated;

    using netsec::FieldClass;
    using netsec::FieldEncoding;
    using netsec::FieldSpec;
    using netsec::SemanticRule;

    specs[2] = FieldSpec{FieldClass::LLVar, FieldEncoding::Numeric, 19, false};
    semantics[2] = SemanticRule::Account;
    specs[3] = FieldSpec{FieldClass::Fixed, FieldEncoding::Numeric, 6, false};
    specs[4] = FieldSpec{FieldClass::Fixed, FieldEncoding::Numeric, 12, true};
    semantics[4] = SemanticRule::Amount;
    specs[7] = FieldSpec{FieldClass::Fixed, FieldEncoding::Numeric, 10, false};
    specs[11] = FieldSpec{FieldClass::Fixed, FieldEncoding::Numeric, 6, false};
    specs[41] = FieldSpec{FieldClass::Fixed, FieldEncoding::AlphaNumeric, 8, false};
    specs[49] = FieldSpec{FieldClass::Fixed, FieldEncoding::Numeric, 3, true};
    semantics[49] = SemanticRule::Currency;
    specs[70] = FieldSpec{FieldClass::Fixed, FieldEncoding::Numeric, 3, false};

    for (int f : {2, 3, 4, 7, 11, 41, 49}) negotiated.set(f);
    if (negotiate_field70) negotiated.set(70);

    return netsec::BitmapParser(std::move(specs), std::move(semantics), negotiated);
}

std::string valid_message() {
    return make_bitmap({2, 3, 4, 7, 11, 41, 49}) +
           "16" + "4111111111111111" +  // field 2: account (Luhn valid)
           "000000" +                   // field 3
           "000000010000" +             // field 4: amount 100.00
           "1101120000" +               // field 7
           "000123" +                   // field 11
           "TERM0001" +                 // field 41
           "156";                       // field 49: CNY
}

void test_valid_message() {
    auto parser = make_parser(false);
    auto result = parser.parse(valid_message());
    CHECK(result);
    CHECK(result.error == netsec::ParseError::None);
    if (!result) return;
    const auto& msg = *result.message;
    CHECK(!msg.secondary_bitmap_present);
    CHECK(msg.consumed == valid_message().size());
    CHECK(msg.fields.size() == 7);
    CHECK(msg.fields.at(2).bytes == "4111111111111111");
    CHECK(msg.fields.at(4).bytes == "000000010000");
    CHECK(msg.fields.at(49).bytes == "156");
}

void test_valid_message_with_secondary_bitmap() {
    auto parser = make_parser(true);
    std::string wire = make_bitmap({2, 3, 4, 7, 11, 41, 49, 70}) +
                       "16" + "4111111111111111" + "000000" + "000000010000" +
                       "1101120000" + "000123" + "TERM0001" + "156" + "001";
    auto result = parser.parse(wire);
    CHECK(result);
    if (!result) return;
    CHECK(result.message->secondary_bitmap_present);
    CHECK(result.message->consumed == wire.size());
    CHECK(result.message->fields.at(70).bytes == "001");
}

void test_unnegotiated_field_rejected() {
    auto parser = make_parser(false);
    // Field 70 is a known spec but was not negotiated for this session.
    std::string wire = make_bitmap({3, 4, 49, 70}) + "000000" + "000000010000" +
                       "156" + "001";
    auto result = parser.parse(wire);
    CHECK(!result);
    CHECK(result.error == netsec::ParseError::UnnegotiatedField);
    CHECK(result.field == 70);
}

void test_unknown_field_rejected() {
    auto parser = make_parser(false);
    // Field 5 has no spec at all.
    std::string wire = make_bitmap({3, 4, 5, 49}) + "000000" + "000000010000" +
                       "1" + "156";
    auto result = parser.parse(wire);
    CHECK(!result);
    CHECK(result.error == netsec::ParseError::UnknownField);
    CHECK(result.field == 5);
}

void test_length_out_of_bounds_rejected() {
    auto parser = make_parser(false);
    // Field 2 is LLVar with max 19; attacker declares 25 to read past the
    // field into subsequent message content.
    std::string wire = make_bitmap({2, 3, 4, 49}) + "25" + "4111111111111111" +
                       "000000" + "000000010000" + "156";
    auto result = parser.parse(wire);
    CHECK(!result);
    CHECK(result.error == netsec::ParseError::LengthOutOfBounds);
    CHECK(result.field == 2);
}

void test_non_numeric_length_prefix_rejected() {
    auto parser = make_parser(false);
    std::string wire = make_bitmap({2, 3, 4, 49}) + "1A" + "4111111111111111" +
                       "000000" + "000000010000" + "156";
    auto result = parser.parse(wire);
    CHECK(!result);
    CHECK(result.error == netsec::ParseError::InvalidEncoding);
    CHECK(result.field == 2);
}

void test_duplicate_amount_rejected() {
    auto parser = make_parser(false);
    // Attacker appends a second amount field after the declared fields,
    // hoping the gateway re-parses field 4. The parser must consume the
    // buffer exactly once and reject the trailing bytes.
    std::string wire = valid_message() + "999999999999";
    auto result = parser.parse(wire);
    CHECK(!result);
    CHECK(result.error == netsec::ParseError::TrailingBytes);

    // The legitimate message parses field 4 exactly once.
    auto ok = parser.parse(valid_message());
    CHECK(ok);
    CHECK(ok.message->fields.count(4) == 1);
}

void test_truncated_messages_rejected() {
    auto parser = make_parser(false);

    auto short_bitmap = parser.parse(std::string(4, '\0'));
    CHECK(!short_bitmap);
    CHECK(short_bitmap.error == netsec::ParseError::TruncatedBitmap);

    // Secondary bitmap announced (bit 1 set) but missing from the buffer.
    std::string primary_only(8, '\0');
    primary_only[0] = static_cast<char>(0x80);
    auto missing_secondary = parser.parse(primary_only);
    CHECK(!missing_secondary);
    CHECK(missing_secondary.error == netsec::ParseError::TruncatedBitmap);

    // Length prefix cut off.
    auto cut_prefix = parser.parse(make_bitmap({2}) + "1");
    CHECK(!cut_prefix);
    CHECK(cut_prefix.error == netsec::ParseError::TruncatedLengthPrefix);
    CHECK(cut_prefix.field == 2);

    // Field body cut off.
    std::string wire = valid_message();
    wire.resize(wire.size() - 2);
    auto cut_field = parser.parse(wire);
    CHECK(!cut_field);
    CHECK(cut_field.error == netsec::ParseError::TruncatedField);
    CHECK(cut_field.field == 49);
}

void test_semantic_validation() {
    auto parser = make_parser(false);

    // Bad Luhn check digit in the account field.
    std::string bad_account = make_bitmap({2, 3, 4, 49}) + "16" +
                              "4111111111111112" + "000000" + "000000010000" +
                              "156";
    auto r1 = parser.parse(bad_account);
    CHECK(!r1);
    CHECK(r1.error == netsec::ParseError::InvalidAccount);
    CHECK(r1.field == 2);

    // Currency code not in the accepted ISO 4217 set.
    std::string bad_currency = make_bitmap({3, 4, 49}) + "000000" +
                               "000000010000" + "999";
    auto r2 = parser.parse(bad_currency);
    CHECK(!r2);
    CHECK(r2.error == netsec::ParseError::InvalidCurrency);
    CHECK(r2.field == 49);

    // Amount field failing the amount rule (wrong digit count via LLVar spec).
    std::array<std::optional<netsec::FieldSpec>, netsec::kMaxFieldNumber + 1> specs{};
    std::array<netsec::SemanticRule, netsec::kMaxFieldNumber + 1> semantics{};
    std::bitset<netsec::kMaxFieldNumber + 1> negotiated;
    specs[4] = netsec::FieldSpec{netsec::FieldClass::LLVar,
                                 netsec::FieldEncoding::Numeric, 12, true};
    semantics[4] = netsec::SemanticRule::Amount;
    negotiated.set(4);
    netsec::BitmapParser amount_parser(std::move(specs), std::move(semantics),
                                       negotiated);
    auto r3 = amount_parser.parse(make_bitmap({4}) + "05" + "12345");
    CHECK(!r3);
    CHECK(r3.error == netsec::ParseError::InvalidAmount);
    CHECK(r3.field == 4);
}

void test_missing_required_field_rejected() {
    auto parser = make_parser(false);
    std::string wire = make_bitmap({3, 11}) + "000000" + "000123";
    auto result = parser.parse(wire);
    CHECK(!result);
    CHECK(result.error == netsec::ParseError::MissingRequiredField);
    CHECK(result.field == 4);
}

void test_parse_result_is_unique() {
    auto parser = make_parser(false);
    const std::string wire = valid_message();
    auto first = parser.parse(wire);
    auto second = parser.parse(wire);
    CHECK(first && second);
    CHECK(first.message->consumed == wire.size());
    CHECK(first.message->fields.size() == second.message->fields.size());
    for (const auto& [field, value] : first.message->fields) {
        CHECK(second.message->fields.at(field).bytes == value.bytes);
    }
}

}  // namespace

int main() {
    test_valid_message();
    test_valid_message_with_secondary_bitmap();
    test_unnegotiated_field_rejected();
    test_unknown_field_rejected();
    test_length_out_of_bounds_rejected();
    test_non_numeric_length_prefix_rejected();
    test_duplicate_amount_rejected();
    test_truncated_messages_rejected();
    test_semantic_validation();
    test_missing_required_field_rejected();
    test_parse_result_is_unique();

    if (g_failures != 0) {
        std::cerr << g_failures << " check(s) failed\n";
        return EXIT_FAILURE;
    }
    std::cout << "all bitmap parser tests passed\n";
    return EXIT_SUCCESS;
}
