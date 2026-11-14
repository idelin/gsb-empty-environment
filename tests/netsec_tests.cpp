#include "netsec.hpp"
#include "netsec_crypto.hpp"

#include <algorithm>
#include <array>
#include <cstdint>
#include <cstdlib>
#include <iostream>
#include <optional>
#include <span>
#include <string>
#include <string_view>
#include <vector>

namespace {

int failures = 0;

void check(bool condition, std::string_view message, int line) {
    if (!condition) {
        ++failures;
        std::cerr << "FAIL line " << line << ": " << message << '\n';
    }
}

#define CHECK(condition) check((condition), #condition, __LINE__)
#define CHECK_MESSAGE(condition, message) check((condition), (message), __LINE__)

std::uint8_t nibble(char character) {
    if (character >= '0' && character <= '9') {
        return static_cast<std::uint8_t>(character - '0');
    }
    if (character >= 'a' && character <= 'f') {
        return static_cast<std::uint8_t>(character - 'a' + 10);
    }
    if (character >= 'A' && character <= 'F') {
        return static_cast<std::uint8_t>(character - 'A' + 10);
    }
    std::abort();
}

netsec::Bytes hex(std::string_view text) {
    if (text.size() >= 2 && text[0] == '0' &&
        (text[1] == 'x' || text[1] == 'X')) {
        text.remove_prefix(2);
    }
    if (text.size() % 2 != 0) {
        std::abort();
    }
    netsec::Bytes output(text.size() / 2);
    for (std::size_t index = 0; index < output.size(); ++index) {
        output[index] = static_cast<std::uint8_t>(
            (nibble(text[index * 2]) << 4) | nibble(text[index * 2 + 1]));
    }
    return output;
}

netsec::Address address(std::string_view text) {
    const auto parsed = netsec::Address::from_hex(text);
    if (!parsed.has_value()) {
        std::cerr << "bad test address: " << text << '\n';
        std::abort();
    }
    return *parsed;
}

netsec::ByteView view(const netsec::Bytes& bytes) {
    return netsec::ByteView(bytes.data(), bytes.size());
}

netsec::ByteView view(std::string_view text) {
    return netsec::ByteView(
        reinterpret_cast<const std::uint8_t*>(text.data()), text.size());
}

netsec::Bytes rlp_length(std::size_t length,
                         std::uint8_t short_base,
                         std::uint8_t long_base) {
    if (length <= 55) {
        return {static_cast<std::uint8_t>(short_base + length)};
    }
    std::vector<std::uint8_t> encoded;
    std::size_t remaining = length;
    while (remaining != 0) {
        encoded.insert(encoded.begin(),
                       static_cast<std::uint8_t>(remaining & 0xffU));
        remaining >>= 8U;
    }
    netsec::Bytes output{static_cast<std::uint8_t>(long_base + encoded.size())};
    output.insert(output.end(), encoded.begin(), encoded.end());
    return output;
}

netsec::Bytes rlp_string(netsec::ByteView bytes) {
    if (bytes.size() == 1 && bytes[0] < 0x80) {
        return {bytes[0]};
    }
    netsec::Bytes output = rlp_length(bytes.size(), 0x80, 0xb7);
    output.insert(output.end(), bytes.begin(), bytes.end());
    return output;
}

netsec::Bytes rlp_string(const netsec::Bytes& bytes) {
    return rlp_string(view(bytes));
}

netsec::Bytes rlp_uint(std::uint64_t value) {
    if (value == 0) {
        return {0x80};
    }
    netsec::Bytes bytes;
    while (value != 0) {
        bytes.insert(bytes.begin(), static_cast<std::uint8_t>(value & 0xffU));
        value >>= 8U;
    }
    return rlp_string(bytes);
}

netsec::Bytes rlp_uint256(const netsec::Uint256& value) {
    std::size_t first = 0;
    while (first < value.big_endian.size() && value.big_endian[first] == 0) {
        ++first;
    }
    return rlp_string(netsec::ByteView(value.big_endian).subspan(first));
}

netsec::Bytes rlp_list(const std::vector<netsec::Bytes>& items) {
    std::size_t payload = 0;
    for (const auto& item : items) {
        payload += item.size();
    }
    netsec::Bytes output = rlp_length(payload, 0xc0, 0xf7);
    for (const auto& item : items) {
        output.insert(output.end(), item.begin(), item.end());
    }
    return output;
}

netsec::Bytes typed_transaction(std::uint8_t type,
                                const std::vector<netsec::Bytes>& fields) {
    netsec::Bytes output{type};
    const netsec::Bytes body = rlp_list(fields);
    output.insert(output.end(), body.begin(), body.end());
    return output;
}

netsec::Bytes dynamic_fee_transaction(std::uint64_t chain_id,
                                      std::uint64_t nonce,
                                      std::uint64_t priority_fee,
                                      std::uint64_t max_fee,
                                      std::uint64_t gas_limit,
                                      const netsec::Address& to,
                                      const netsec::Uint256& value,
                                      const netsec::Bytes& data) {
    return typed_transaction(
        0x02,
        {rlp_uint(chain_id), rlp_uint(nonce), rlp_uint(priority_fee),
         rlp_uint(max_fee), rlp_uint(gas_limit), rlp_string(to.bytes),
         rlp_uint256(value), rlp_string(data), netsec::Bytes{0xc0}});
}

netsec::Bytes access_list_transaction(std::uint64_t chain_id,
                                      std::uint64_t nonce,
                                      std::uint64_t gas_price,
                                      std::uint64_t gas_limit,
                                      const netsec::Address& to,
                                      const netsec::Uint256& value,
                                      const netsec::Bytes& data,
                                      const netsec::Bytes& access_list) {
    return typed_transaction(
        0x01,
        {rlp_uint(chain_id), rlp_uint(nonce), rlp_uint(gas_price),
         rlp_uint(gas_limit), rlp_string(to.bytes), rlp_uint256(value),
         rlp_string(data), access_list});
}

netsec::Bytes access_list_entry(const netsec::Address& contract,
                                const std::vector<netsec::Bytes>& keys) {
    std::vector<netsec::Bytes> encoded_keys;
    encoded_keys.reserve(keys.size());
    for (const auto& key : keys) {
        encoded_keys.push_back(rlp_string(key));
    }
    return rlp_list(
        {rlp_string(contract.bytes), rlp_list(encoded_keys)});
}

netsec::Bytes legacy_unsigned_eip155(std::uint64_t nonce,
                                     std::uint64_t gas_price,
                                     std::uint64_t gas_limit,
                                     const netsec::Address& to,
                                     const netsec::Uint256& value,
                                     const netsec::Bytes& data,
                                     std::uint64_t chain_id) {
    return rlp_list({rlp_uint(nonce), rlp_uint(gas_price), rlp_uint(gas_limit),
                     rlp_string(to.bytes), rlp_uint256(value),
                     rlp_string(data), rlp_uint(chain_id), netsec::Bytes{0x80},
                     netsec::Bytes{0x80}});
}

netsec::Bytes transfer_calldata(const netsec::Address& recipient,
                                const netsec::Uint256& amount,
                                bool wrong_address_padding = false,
                                std::size_t extra_bytes = 0,
                                std::uint32_t selector = 0xa9059cbbU) {
    netsec::Bytes data;
    data.push_back(static_cast<std::uint8_t>(selector >> 24));
    data.push_back(static_cast<std::uint8_t>(selector >> 16));
    data.push_back(static_cast<std::uint8_t>(selector >> 8));
    data.push_back(static_cast<std::uint8_t>(selector));
    for (int index = 0; index < 12; ++index) {
        data.push_back(0);
    }
    if (wrong_address_padding) {
        data[4] = 1;
    }
    data.insert(data.end(), recipient.bytes.begin(), recipient.bytes.end());
    data.insert(data.end(), amount.big_endian.begin(), amount.big_endian.end());
    for (std::size_t index = 0; index < extra_bytes; ++index) {
        data.push_back(0);
    }
    return data;
}

netsec::TransactionPolicy ethereum_policy() {
    netsec::TransactionPolicy policy;
    policy.chain_id = 1;
    policy.chain_name = "Ethereum";
    policy.native_symbol = "ETH";
    return policy;
}

void check_error(const netsec::Result<netsec::Confirmation>& result,
                 netsec::ErrorCode code,
                 std::string_view label) {
    if (result.has_value()) {
        CHECK_MESSAGE(false, std::string(label) + " unexpectedly succeeded");
        return;
    }
    if (result.error().code != code) {
        std::cerr << label << ": got error " << static_cast<int>(result.error().code)
                  << " (" << result.error().message << "), wanted "
                  << static_cast<int>(code) << '\n';
    }
    CHECK(result.error().code == code);
}

void test_keccak_and_address_encoding() {
    using netsec::Address;
    using netsec::detail::keccak256;

    const auto empty = keccak256(view(std::string_view{}));
    CHECK(empty.hex() ==
          "0xc5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470");
    const auto abc = keccak256(view(std::string_view{"abc"}));
    CHECK(abc.hex() ==
          "0x4e03657aea45a94fc7d47ba826c8d667c0d1e6e33a64a036ec44f58fa12d6c45");

    const std::array<std::string_view, 4> vectors = {
        "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed",
        "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359",
        "0xdbF03B407c01E7cD3CBea99509d93f8DDDC8C6FB",
        "0xD1220A0cf47c7B9Be7A2E6BA89F429762e7b9aDb",
    };
    for (const std::string_view vector : vectors) {
        const auto parsed = Address::from_hex(vector);
        CHECK(parsed.has_value());
        if (parsed.has_value()) {
            CHECK(parsed->checksum_hex() == vector);
            CHECK(Address::from_hex(parsed->lowercase_hex()).has_value());
        }
    }

    const auto lowered = Address::from_hex(
        "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed");
    CHECK(lowered.has_value());
    CHECK(!Address::from_hex(
               "0x5AAeb6053F3E94C9b9A09f33669435E7Ef1BeAed")
               .has_value());
    CHECK(!Address::from_hex("0x1234").has_value());
}

void test_signed_eip155_vector() {
    const netsec::Bytes raw = hex(
        "0xf86c098504a817c800825208943535353535353535353535353535353535353535"
        "880de0b6b3a76400008025a028ef61340bd939bc2195fe537567866003e1a15d3c"
        "71ff63e1590620aa636276a067cbe9d8997f761aecb703304b3800ccf555c9f3dc"
        "64214b297fb1966a3b6d83");

    netsec::TransactionPolicy policy = ethereum_policy();
    policy.expected_signer =
        address("0x9d8A62f656a8d1615C1294fd71e9CFb3E4855A4F");

    const auto result = netsec::confirm_transaction(view(raw), policy);
    CHECK(result.has_value());
    if (!result.has_value()) {
        std::cerr << "EIP-155 vector: " << result.error().message << '\n';
        return;
    }
    const auto& confirmation = *result;
    CHECK(confirmation.input_was_signed());
    CHECK(confirmation.recovered_sender().has_value());
    if (confirmation.recovered_sender().has_value()) {
        CHECK(*confirmation.recovered_sender() == policy.expected_signer);
    }
    CHECK(confirmation.signing_digest().hex() ==
          "0xdaf5a779ae972f972197303d7b574746c7ef83eadac0f2791ad23db92e4c8e53");
    CHECK(confirmation.display().chain_id == 1);
    CHECK(confirmation.display().kind == netsec::DisplayKind::NativeTransfer);
    CHECK(confirmation.display().amount_base_units == "1000000000000000000");
    CHECK(confirmation.display().max_fee_base_units == "420000000000000");
    CHECK(confirmation.display().amount_display == "1 ETH");
    CHECK(confirmation.display().max_fee_display == "0.00042 ETH");
    CHECK(confirmation.display().transaction_to.has_value());
    if (confirmation.display().transaction_to.has_value()) {
        CHECK(confirmation.display().transaction_to->checksum_hex() ==
              "0x3535353535353535353535353535353535353535");
    }
    CHECK(confirmation.display_text().find(
              "Sender (recovered): 0x9d8A62f656a8d1615C1294fd71e9CFb3E4855A4F") !=
          std::string_view::npos);
    CHECK(confirmation.display_text().find(confirmation.signing_digest().hex()) !=
          std::string_view::npos);

    const auto readback = confirmation.readback();
    CHECK(readback.has_value());
    if (readback.has_value()) {
        CHECK(netsec::verify_display_readback(*readback));
        netsec::Bytes tampered = readback->display_bytes;
        CHECK(!tampered.empty());
        if (!tampered.empty()) {
            tampered[tampered.size() / 2] ^= 1;
            CHECK(!confirmation.verify_readback(view(tampered)));
            CHECK(!netsec::verify_display_readback(
                readback->signing_digest, readback->display_commitment,
                view(tampered)));
        }
    }
}

void test_dynamic_fee_display_and_host_substitution() {
    const netsec::Address recipient =
        address("0x1111111111111111111111111111111111111111");
    const netsec::Address attacker =
        address("0x2222222222222222222222222222222222222222");
    const netsec::Uint256 value = *netsec::Uint256::from_decimal("1234567890123456789");
    const netsec::Bytes raw =
        dynamic_fee_transaction(1, 7, 1000000000ULL, 30000000000ULL, 21000,
                                recipient, value, {});

    netsec::HostMetadata host;
    host.display_text =
        "To: 0x2222222222222222222222222222222222222222\nValue: 0 ETH";
    host.recipient = attacker.checksum_hex();
    host.amount = "0 ETH";
    host.fee = "0 ETH";
    host.chain = "Polygon (137)";
    host.signing_digest =
        "0x0000000000000000000000000000000000000000000000000000000000000000";
    host.claimed_parsed_result = {0xde, 0xad, 0xbe, 0xef};

    netsec::HostRequest request{raw, host};
    netsec::TransactionConfirmer confirmer(ethereum_policy());
    const auto result = confirmer.confirm(request);
    CHECK(result.has_value());
    if (!result.has_value()) {
        std::cerr << "dynamic fee: " << result.error().message << '\n';
        return;
    }
    const auto& confirmation = *result;
    CHECK(!confirmation.input_was_signed());
    CHECK(confirmation.display().kind == netsec::DisplayKind::NativeTransfer);
    CHECK(confirmation.display().chain_id == 1);
    CHECK(confirmation.display_text().find("Chain: Ethereum (1)") !=
          std::string_view::npos);
    CHECK(confirmation.display_text().find("Polygon") == std::string_view::npos);
    CHECK(confirmation.display_text().find(attacker.checksum_hex()) ==
          std::string_view::npos);
    CHECK(confirmation.display_text().find(recipient.checksum_hex()) !=
          std::string_view::npos);
    CHECK(confirmation.display_text().find(host.display_text) ==
          std::string_view::npos);
    CHECK(confirmation.display().amount_base_units == "1234567890123456789");
    CHECK(confirmation.display().amount_display == "1.234567890123456789 ETH");
    CHECK(confirmation.display().max_fee_base_units == "630000000000000");
    CHECK(confirmation.display().max_fee_display == "0.00063 ETH");
    CHECK(confirmation.display().gas_price == "30000000000");
    CHECK(confirmation.display().max_priority_fee_per_gas == "1000000000");

    const auto recipient_field = confirmation.display().field("Recipient");
    CHECK(recipient_field.has_value());
    if (recipient_field.has_value()) {
        CHECK(recipient_field->size() == 42);
        CHECK(*recipient_field == recipient.checksum_hex());
        CHECK(recipient_field->find("...") == std::string::npos);
    }
}

void test_chain_replacement() {
    const netsec::Address recipient =
        address("0x1111111111111111111111111111111111111111");
    const netsec::Uint256 value = netsec::Uint256::from_u64(1);
    const netsec::Bytes foreign =
        dynamic_fee_transaction(137, 7, 1000000000ULL, 30000000000ULL, 21000,
                                recipient, value, {});
    const netsec::Bytes local =
        dynamic_fee_transaction(1, 7, 1000000000ULL, 30000000000ULL, 21000,
                                recipient, value, {});

    netsec::TransactionConfirmer confirmer(ethereum_policy());
    check_error(confirmer.confirm(view(foreign)), netsec::ErrorCode::ChainMismatch,
                "foreign chain id");

    netsec::HostMetadata host;
    host.chain = "Polygon (137)";
    host.recipient = "0x2222222222222222222222222222222222222222";
    const auto accepted = confirmer.confirm(netsec::HostRequest{local, host});
    CHECK(accepted.has_value());
    if (accepted.has_value()) {
        CHECK(accepted->display().chain_id == 1);
        CHECK(accepted->display_text().find("Chain: Ethereum (1)") !=
              std::string_view::npos);
    }

    // Legacy EIP-155 v encodes the chain. Changing 37 (chain 1) to 39
    // (chain 2) must be rejected before any display is produced.
    netsec::Bytes chain_two = hex(
        "0xf86c098504a817c800825208943535353535353535353535353535353535353535"
        "880de0b6b3a76400008027a028ef61340bd939bc2195fe537567866003e1a15d3c"
        "71ff63e1590620aa636276a067cbe9d8997f761aecb703304b3800ccf555c9f3dc"
        "64214b297fb1966a3b6d83");
    check_error(confirmer.confirm(view(chain_two)),
                netsec::ErrorCode::ChainMismatch, "legacy chain replacement");

    // A six-field legacy transaction carries no chain field. The device must
    // inject its own policy chain into the EIP-155 preimage.
    const netsec::Address legacy_recipient =
        address("0x3535353535353535353535353535353535353535");
    const netsec::Bytes bare_legacy = rlp_list(
        {rlp_uint(9), rlp_uint(20000000000ULL), rlp_uint(21000),
         rlp_string(legacy_recipient.bytes),
         rlp_uint256(*netsec::Uint256::from_decimal("1000000000000000000")),
         netsec::Bytes{0x80}});
    const auto bare = confirmer.confirm(view(bare_legacy));
    CHECK(bare.has_value());
    if (bare.has_value()) {
        CHECK(bare->signing_digest().hex() ==
              "0xdaf5a779ae972f972197303d7b574746c7ef83eadac0f2791ad23db92e4c8e53");
        CHECK(bare->display().chain_id == 1);
    }
}

void test_access_list_transaction() {
    const netsec::Address recipient =
        address("0x1111111111111111111111111111111111111111");
    const netsec::Address storage_contract =
        address("0x5555555555555555555555555555555555555555");
    const netsec::Bytes storage_key = hex(
        "00000000000000000000000000000000000000000000000000000000000000aa");
    const netsec::Bytes access_list =
        rlp_list({access_list_entry(storage_contract, {storage_key})});
    const netsec::Bytes raw = access_list_transaction(
        1, 4, 25000000000ULL, 50000, recipient,
        netsec::Uint256::from_u64(0), {}, access_list);

    netsec::TransactionConfirmer confirmer(ethereum_policy());
    const auto result = confirmer.confirm(view(raw));
    CHECK(result.has_value());
    if (!result.has_value()) {
        std::cerr << "access list: " << result.error().message << '\n';
        return;
    }
    CHECK(result->display().transaction_type == "EIP-2930 (0x01)");
    CHECK(result->display().access_list_entries == 1);
    CHECK(result->display().access_list_hash != "not applicable");
    CHECK(result->display_text().find("Access list entries: 1") !=
          std::string_view::npos);

    const netsec::Bytes short_key(31, 0x11);
    const netsec::Bytes malformed = access_list_transaction(
        1, 4, 25000000000ULL, 50000, recipient,
        netsec::Uint256::from_u64(0), {},
        rlp_list({access_list_entry(storage_contract, {short_key})}));
    check_error(confirmer.confirm(view(malformed)),
                netsec::ErrorCode::InvalidAccessList,
                "31-byte access list storage key");

    const netsec::Bytes wrong_list = access_list_transaction(
        1, 4, 25000000000ULL, 50000, recipient,
        netsec::Uint256::from_u64(0), {}, rlp_string(netsec::Bytes{0x01}));
    check_error(confirmer.confirm(view(wrong_list)),
                netsec::ErrorCode::InvalidAccessList,
                "access list is not a list");
}

void test_signed_typed_transaction() {
    // EIP-2930 transaction signed by the EIP-155 example key
    // (0x46...46). The digest was produced over the canonical typed preimage.
    const netsec::Bytes raw = hex(
        "0x01f86601048505d21dba0082c350941111111111111111111111111111111111111"
        "1118080c001a008f4f37e2d8f74e18c1b8fde2374d5f28402fb8ab7fd1cc5b786aa"
        "40851a70cba015be10f009a2de9daa16fa6f1a36ba73e00e20237ebfe0c171b2301"
        "f18f8df2b");
    netsec::TransactionPolicy policy = ethereum_policy();
    policy.expected_signer =
        address("0x9d8A62f656a8d1615C1294fd71e9CFb3E4855A4F");

    netsec::TransactionConfirmer confirmer(policy);
    const auto result = confirmer.confirm(view(raw));
    CHECK(result.has_value());
    if (!result.has_value()) {
        std::cerr << "signed EIP-2930: " << result.error().message << '\n';
        return;
    }
    CHECK(result->input_was_signed());
    CHECK(result->recovered_sender().has_value() &&
          *result->recovered_sender() == policy.expected_signer);
    CHECK(result->signing_digest().hex() ==
          "0xc6d0ae86392e38258cee6a12e20a41bf10d3f83bbcf34d4028b7448cff552d64");
    CHECK(result->display().transaction_type == "EIP-2930 (0x01)");
    CHECK(result->display().gas_price == "25000000000");
    CHECK(result->display().max_fee_base_units == "1250000000000000");
    CHECK(result->display().max_fee_display == "0.00125 ETH");

    netsec::TransactionPolicy other = policy;
    other.expected_signer =
        address("0x2222222222222222222222222222222222222222");
    netsec::TransactionConfirmer other_confirmer(other);
    check_error(other_confirmer.confirm(view(raw)),
                netsec::ErrorCode::SignatureMismatch,
                "typed signature wrong signer");
}

void test_token_transfer_and_contract_data() {
    const netsec::Address token =
        address("0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48");
    const netsec::Address recipient =
        address("0x3333333333333333333333333333333333333333");
    const netsec::Uint256 amount = netsec::Uint256::from_u64(1234567);
    const netsec::Bytes calldata = transfer_calldata(recipient, amount);
    const netsec::Bytes raw =
        dynamic_fee_transaction(1, 3, 1000000000ULL, 40000000000ULL, 60000,
                                token, netsec::Uint256::from_u64(0), calldata);

    netsec::TransactionPolicy policy = ethereum_policy();
    netsec::TokenPolicy token_policy;
    token_policy.contract = token;
    token_policy.symbol = "USDC";
    token_policy.decimals = 6;
    policy.tokens.push_back(token_policy);

    netsec::TransactionConfirmer confirmer(policy);
    const auto result = confirmer.confirm(view(raw));
    CHECK(result.has_value());
    if (!result.has_value()) {
        std::cerr << "token transfer: " << result.error().message << '\n';
        return;
    }
    const auto& confirmation = *result;
    CHECK(confirmation.display().kind == netsec::DisplayKind::TokenTransfer);
    CHECK(confirmation.display().token_contract.has_value() &&
          *confirmation.display().token_contract == token);
    CHECK(confirmation.display().recipient.has_value() &&
          *confirmation.display().recipient == recipient);
    CHECK(confirmation.display().amount_display == "1.234567 USDC");
    CHECK(confirmation.display().amount_base_units == "1234567");
    CHECK(confirmation.display_text().find("To (token contract): " +
                                           token.checksum_hex()) !=
          std::string_view::npos);
    CHECK(confirmation.display_text().find("Recipient: " +
                                           recipient.checksum_hex()) !=
          std::string_view::npos);
    CHECK(confirmation.display_text().find("Native value: 0 ETH (0 wei)") !=
          std::string_view::npos);

    const netsec::Uint256 attached_value =
        *netsec::Uint256::from_decimal("5000000000000000");
    const netsec::Bytes with_native_value =
        dynamic_fee_transaction(1, 3, 1000000000ULL, 40000000000ULL, 60000,
                                token, attached_value, calldata);
    const auto with_value = confirmer.confirm(view(with_native_value));
    CHECK(with_value.has_value());
    if (with_value.has_value()) {
        CHECK(with_value->display().amount_display == "1.234567 USDC");
        CHECK(with_value->display().native_value_display == "0.005 ETH");
        CHECK(with_value->display_text().find(
                  "Native value: 0.005 ETH (5000000000000000 wei)") !=
              std::string_view::npos);
    }

    const netsec::Bytes bad_padding =
        dynamic_fee_transaction(1, 3, 1000000000ULL, 40000000000ULL, 60000,
                                token, netsec::Uint256::from_u64(0),
                                transfer_calldata(recipient, amount, true));
    check_error(confirmer.confirm(view(bad_padding)),
                netsec::ErrorCode::InvalidContractData,
                "non-canonical ABI address word");

    const netsec::Bytes extra =
        dynamic_fee_transaction(1, 3, 1000000000ULL, 40000000000ULL, 60000,
                                token, netsec::Uint256::from_u64(0),
                                transfer_calldata(recipient, amount, false, 1));
    check_error(confirmer.confirm(view(extra)),
                netsec::ErrorCode::InvalidContractData,
                "transfer calldata length");

    const netsec::Bytes wrong_selector =
        dynamic_fee_transaction(1, 3, 1000000000ULL, 40000000000ULL, 60000,
                                token, netsec::Uint256::from_u64(0),
                                transfer_calldata(recipient, amount, false, 0,
                                                  0x095ea7b3U));
    check_error(confirmer.confirm(view(wrong_selector)),
                netsec::ErrorCode::UnknownContractCall,
                "unapproved token selector");

    const netsec::Address other_contract =
        address("0x4444444444444444444444444444444444444444");
    const netsec::Bytes untrusted =
        dynamic_fee_transaction(1, 3, 1000000000ULL, 40000000000ULL, 60000,
                                other_contract, netsec::Uint256::from_u64(0),
                                calldata);
    check_error(confirmer.confirm(view(untrusted)),
                netsec::ErrorCode::UntrustedContract,
                "untrusted token contract");

    policy.allow_unknown_contract_calls = true;
    netsec::TransactionConfirmer permissive(policy);
    const netsec::Bytes generic_raw =
        dynamic_fee_transaction(1, 3, 1000000000ULL, 40000000000ULL, 60000,
                                other_contract, netsec::Uint256::from_u64(0),
                                hex("0x12345678"));
    const auto generic = permissive.confirm(view(generic_raw));
    CHECK(generic.has_value());
    if (generic.has_value()) {
        CHECK(generic->display().kind == netsec::DisplayKind::ContractCall);
        CHECK(generic->display().unknown_contract_data);
        CHECK(generic->display_text().find("unverified contract data") !=
              std::string_view::npos);
    }
    check_error(permissive.confirm(view(untrusted)),
                netsec::ErrorCode::UntrustedContract,
                "unknown-call opt-in cannot bypass token validation");

    // Contract creation uses an empty "to" field, not the zero address.
    netsec::Bytes real_creation =
        typed_transaction(0x02,
                          {rlp_uint(1), rlp_uint(3), rlp_uint(1000000000ULL),
                           rlp_uint(40000000000ULL), rlp_uint(100000),
                           rlp_string(netsec::ByteView{}),
                           rlp_uint256(netsec::Uint256::from_u64(0)),
                           rlp_string(hex("6001600055")),
                           netsec::Bytes{0xc0}});
    check_error(confirmer.confirm(view(real_creation)),
                netsec::ErrorCode::ContractCreationDisallowed,
                "contract creation default deny");
    policy.allow_contract_creation = true;
    netsec::TransactionConfirmer creation_allowed(policy);
    const auto created = creation_allowed.confirm(view(real_creation));
    CHECK(created.has_value());
    if (created.has_value()) {
        CHECK(created->display().kind == netsec::DisplayKind::ContractCreation);
        CHECK(created->display_text().find("(contract creation)") !=
              std::string_view::npos);
    }
}

void test_readback_signing_and_binding() {
    const netsec::Address recipient =
        address("0x3535353535353535353535353535353535353535");
    const netsec::Bytes unsigned_raw = legacy_unsigned_eip155(
        9, 20000000000ULL, 21000, recipient,
        *netsec::Uint256::from_decimal("1000000000000000000"), {}, 1);

    netsec::TransactionPolicy policy = ethereum_policy();
    const netsec::Address signer =
        address("0x9d8A62f656a8d1615C1294fd71e9CFb3E4855A4F");
    policy.expected_signer = signer;

    netsec::HostMetadata host;
    host.recipient = "0x2222222222222222222222222222222222222222";
    host.amount = "0 ETH";
    host.display_text = "RECIPIENT 0x2222222222222222222222222222222222222222";
    netsec::TransactionConfirmer confirmer(policy);
    const auto confirmation_result =
        confirmer.confirm(netsec::HostRequest{unsigned_raw, host});
    CHECK(confirmation_result.has_value());
    if (!confirmation_result.has_value()) {
        std::cerr << "unsigned EIP-155: " << confirmation_result.error().message
                  << " offset=" << confirmation_result.error().byte_offset
                  << '\n';
        return;
    }
    const auto& confirmation = *confirmation_result;
    CHECK(confirmation.signing_digest().hex() ==
          "0xdaf5a779ae972f972197303d7b574746c7ef83eadac0f2791ad23db92e4c8e53");

    const auto before = confirmation.readback();
    CHECK(before.has_value());

    // Signature from the EIP-155 example: r||s, v=37 -> recovery id 0.
    const netsec::CompactSignature known_signature = [] {
        netsec::CompactSignature signature;
        const netsec::Bytes rs = hex(
            "28ef61340bd939bc2195fe537567866003e1a15d3c71ff63e1590620aa636276"
            "67cbe9d8997f761aecb703304b3800ccf555c9f3dc64214b297fb1966a3b6d83");
        std::copy(rs.begin(), rs.end(), signature.bytes.begin());
        signature.recovery_id = 0;
        return signature;
    }();

    const auto recovered =
        netsec::recover_signer(confirmation.signing_digest(), known_signature);
    CHECK(recovered.has_value() && *recovered == signer);

    bool callback_saw_digest = false;
    bool callback_saw_commitment = false;
    const auto signed_result = confirmation.sign(
        [&](const netsec::Hash256& digest, const netsec::Hash256& commitment) {
            callback_saw_digest = digest == confirmation.signing_digest();
            callback_saw_commitment =
                commitment == confirmation.display_commitment();
            return known_signature;
        });
    CHECK(callback_saw_digest);
    CHECK(callback_saw_commitment);
    CHECK(signed_result.has_value());
    if (signed_result.has_value()) {
        CHECK(signed_result->signer == signer);
        CHECK(signed_result->signing_digest == confirmation.signing_digest());
        CHECK(before.has_value() &&
              signed_result->display_bytes == before->display_bytes);
        CHECK(signed_result->verify_readback());
        CHECK(netsec::verify_display_readback(
            signed_result->signing_digest, signed_result->display_commitment,
            view(signed_result->display_bytes)));
    }

    const auto wrong_signer = confirmation.sign(
        [&](const netsec::Hash256&, const netsec::Hash256&) {
            return known_signature;
        },
        address("0x2222222222222222222222222222222222222222"));
    CHECK(!wrong_signer.has_value());
    if (!wrong_signer.has_value()) {
        CHECK(wrong_signer.error().code == netsec::ErrorCode::SignatureMismatch);
    }

    // The same valid signature must not be accepted for a different digest.
    const netsec::Address other = address("0x1111111111111111111111111111111111111111");
    const netsec::Bytes other_raw =
        dynamic_fee_transaction(1, 8, 1, 30000000000ULL, 21000, other,
                                netsec::Uint256::from_u64(1), {});
    const auto other_confirmation = confirmer.confirm(view(other_raw));
    CHECK(other_confirmation.has_value());
    if (other_confirmation.has_value()) {
        const auto mismatch = other_confirmation->sign(
            [&](const netsec::Hash256&, const netsec::Hash256&) {
                return known_signature;
            },
            signer);
        CHECK(!mismatch.has_value());
        if (!mismatch.has_value()) {
            CHECK(mismatch.error().code == netsec::ErrorCode::SignatureMismatch);
        }
    }
}

void test_signature_rejection() {
    const netsec::TransactionPolicy policy = ethereum_policy();
    const std::string signed_vector =
        "0xf86c098504a817c800825208943535353535353535353535353535353535353535"
        "880de0b6b3a76400008025a028ef61340bd939bc2195fe537567866003e1a15d3c"
        "71ff63e1590620aa636276a067cbe9d8997f761aecb703304b3800ccf555c9f3dc"
        "64214b297fb1966a3b6d83";
    const std::string high_s =
        "0x98341627668089e51348fccfb4c7ff31c55912f2d2e47ef09652acf665fad3be";

    // Keep r unchanged, replace s with n-s, and flip the parity to v=38.
    netsec::Bytes high_s_raw = hex(signed_vector);
    // Rebuild v byte (0x25 -> 0x26).
    high_s_raw[high_s_raw.size() - 65] = 0x26;
    const netsec::Bytes s_bytes = hex(high_s);
    std::copy(s_bytes.begin(), s_bytes.end(),
              high_s_raw.end() - static_cast<std::ptrdiff_t>(s_bytes.size()));
    netsec::TransactionConfirmer confirmer(policy);
    check_error(confirmer.confirm(view(high_s_raw)),
                netsec::ErrorCode::InvalidSignature, "high-s signature");

    // Tampering with r while requiring the known signer must be rejected.
    netsec::Bytes tampered = hex(signed_vector);
    tampered[tampered.size() - 64] ^= 0x01;
    netsec::TransactionPolicy expected = policy;
    expected.expected_signer =
        address("0x9d8A62f656a8d1615C1294fd71e9CFb3E4855A4F");
    netsec::TransactionConfirmer checking(expected);
    const auto rejected = checking.confirm(view(tampered));
    CHECK(!rejected.has_value());
    if (!rejected.has_value()) {
        CHECK(rejected.error().code == netsec::ErrorCode::SignatureMismatch ||
              rejected.error().code == netsec::ErrorCode::InvalidSignature);
    }

    // A pre-EIP-155 signature is not chain bound and is rejected by default.
    netsec::Bytes unprotected = hex(
        "0xf86c098504a817c800825208943535353535353535353535353535353535353535"
        "880de0b6b3a7640000801ba028ef61340bd939bc2195fe537567866003e1a15d3c"
        "71ff63e1590620aa636276a067cbe9d8997f761aecb703304b3800ccf555c9f3dc"
        "64214b297fb1966a3b6d83");
    check_error(confirmer.confirm(view(unprotected)),
                netsec::ErrorCode::UnprotectedLegacyTransaction,
                "unprotected legacy signature");
}

void test_malformed_transactions() {
    const netsec::TransactionPolicy policy = ethereum_policy();
    netsec::TransactionConfirmer confirmer(policy);
    const netsec::Address recipient =
        address("0x1111111111111111111111111111111111111111");

    check_error(confirmer.confirm(view(netsec::Bytes{})),
                netsec::ErrorCode::EmptyTransaction, "empty transaction");
    check_error(confirmer.confirm(view(hex("0x03c0"))),
                netsec::ErrorCode::UnsupportedTransactionType,
                "unsupported type");
    check_error(confirmer.confirm(view(hex("0x00c0"))),
                netsec::ErrorCode::UnsupportedTransactionType,
                "unknown typed envelope");

    netsec::Bytes truncated = hex("0xf86c098504a817c80082520894");
    check_error(confirmer.confirm(view(truncated)),
                netsec::ErrorCode::MalformedRlp, "truncated RLP");

    netsec::Bytes trailing = hex(
        "0xec098504a817c80082520894353535353535353535353535353535353535353588"
        "0de0b6b3a76400008001808000");
    check_error(confirmer.confirm(view(trailing)),
                netsec::ErrorCode::MalformedRlp, "trailing bytes");

    // RLP integer 0x00 is not a canonical encoding of zero.
    const netsec::Address to =
        address("0x3535353535353535353535353535353535353535");
    const netsec::Uint256 one_ether =
        *netsec::Uint256::from_decimal("1000000000000000000");
    const auto common_prefix = [&] {
        return std::vector<netsec::Bytes>{
            rlp_uint(9),
            rlp_uint(20000000000ULL),
            rlp_uint(21000),
            rlp_string(to.bytes),
            rlp_uint256(one_ether),
            netsec::Bytes{0x80}};
    };
    std::vector<netsec::Bytes> leading_zero_fields = common_prefix();
    leading_zero_fields.push_back(netsec::Bytes{0x00});
    leading_zero_fields.push_back(netsec::Bytes{0x80});
    leading_zero_fields.push_back(netsec::Bytes{0x80});
    const netsec::Bytes leading_zero = rlp_list(leading_zero_fields);
    check_error(confirmer.confirm(view(leading_zero)),
                netsec::ErrorCode::NonCanonicalRlp,
                "non-canonical chain id");

    // Single byte 0x01 encoded as 0x81 0x01.
    std::vector<netsec::Bytes> single_fields = common_prefix();
    single_fields.push_back(netsec::Bytes{0x81, 0x01});
    single_fields.push_back(netsec::Bytes{0x80});
    single_fields.push_back(netsec::Bytes{0x80});
    const netsec::Bytes noncanonical_single = rlp_list(single_fields);
    check_error(confirmer.confirm(view(noncanonical_single)),
                netsec::ErrorCode::NonCanonicalRlp,
                "non-canonical single byte");

    netsec::TransactionPolicy small = policy;
    small.max_transaction_bytes = 10;
    small.max_calldata_bytes = 10;
    netsec::TransactionConfirmer limited(small);
    check_error(limited.confirm(view(trailing)),
                netsec::ErrorCode::TransactionTooLarge,
                "transaction size limit");

    const netsec::Bytes bad_priority = dynamic_fee_transaction(
        1, 1, 30000000000ULL, 20000000000ULL, 21000, recipient,
        netsec::Uint256::from_u64(1), {});
    check_error(confirmer.confirm(view(bad_priority)),
                netsec::ErrorCode::PriorityFeeExceedsMaxFee,
                "priority fee above max fee");

    netsec::TransactionPolicy capped = policy;
    capped.max_fee = netsec::Uint256::from_u64(1000);
    netsec::TransactionConfirmer fee_capped(capped);
    const netsec::Bytes expensive = dynamic_fee_transaction(
        1, 1, 1000000000ULL, 30000000000ULL, 21000, recipient,
        netsec::Uint256::from_u64(1), {});
    check_error(fee_capped.confirm(view(expensive)),
                netsec::ErrorCode::FeeLimitExceeded,
                "fee policy cap");
}

void test_display_commitment_is_digest_bound() {
    const netsec::Bytes raw = dynamic_fee_transaction(
        1, 1, 1, 20000000000ULL, 21000,
        address("0x1111111111111111111111111111111111111111"),
        netsec::Uint256::from_u64(1), {});
    netsec::TransactionConfirmer confirmer(ethereum_policy());
    const auto result = confirmer.confirm(view(raw));
    CHECK(result.has_value());
    if (!result.has_value()) {
        return;
    }
    const auto readback = result->readback();
    CHECK(readback.has_value());
    if (!readback.has_value()) {
        return;
    }

    netsec::Hash256 wrong_digest = readback->signing_digest;
    wrong_digest.bytes[0] ^= 1;
    CHECK(!netsec::verify_display_readback(
        wrong_digest, readback->display_commitment, view(readback->display_bytes)));
    CHECK(!result->verify_readback(
        netsec::DisplayReadback{wrong_digest, readback->display_commitment,
                                readback->display_bytes}));

    netsec::Hash256 wrong_commitment = readback->display_commitment;
    wrong_commitment.bytes[31] ^= 1;
    CHECK(!result->verify_readback(
        netsec::DisplayReadback{readback->signing_digest, wrong_commitment,
                                readback->display_bytes}));
    CHECK(!netsec::verify_display_readback(
        readback->signing_digest, wrong_commitment, view(readback->display_bytes)));
}

void test_policy_sanitisation() {
    netsec::TransactionPolicy policy = ethereum_policy();
    policy.chain_name = "Ethereum\nFAKE RECIPIENT";
    netsec::TransactionConfirmer confirmer(policy);
    const auto result = confirmer.confirm(view(hex("0xc0")));
    CHECK(!result.has_value());
    if (!result.has_value()) {
        CHECK(result.error().code == netsec::ErrorCode::InvalidArgument);
    }

    netsec::TransactionPolicy duplicate = ethereum_policy();
    netsec::TokenPolicy token;
    token.contract = address("0x1111111111111111111111111111111111111111");
    token.symbol = "A";
    duplicate.tokens.push_back(token);
    token.symbol = "B";
    duplicate.tokens.push_back(token);
    netsec::TransactionConfirmer duplicate_confirmer(duplicate);
    const auto duplicate_result = duplicate_confirmer.confirm(view(hex("0xc0")));
    CHECK(!duplicate_result.has_value());
    if (!duplicate_result.has_value()) {
        CHECK(duplicate_result.error().code == netsec::ErrorCode::InvalidArgument);
    }
}

}  // namespace

int main() {
    test_keccak_and_address_encoding();
    test_signed_eip155_vector();
    test_dynamic_fee_display_and_host_substitution();
    test_chain_replacement();
    test_access_list_transaction();
    test_signed_typed_transaction();
    test_token_transfer_and_contract_data();
    test_readback_signing_and_binding();
    test_signature_rejection();
    test_malformed_transactions();
    test_display_commitment_is_digest_bound();
    test_policy_sanitisation();

    if (failures != 0) {
        std::cerr << failures << " test(s) failed\n";
        return 1;
    }
    std::cout << "all netsec tests passed\n";
    return 0;
}
