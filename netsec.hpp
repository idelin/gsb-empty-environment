#pragma once

#include <array>
#include <compare>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <optional>
#include <span>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace netsec {

using Bytes = std::vector<std::uint8_t>;
using ByteView = std::span<const std::uint8_t>;

// ---------------------------------------------------------------------------
// Errors and result values
// ---------------------------------------------------------------------------

enum class ErrorCode : std::uint8_t {
    None = 0,
    InvalidArgument,
    TransactionTooLarge,
    EmptyTransaction,
    MalformedRlp,
    NonCanonicalRlp,
    UnexpectedFieldCount,
    UnsupportedTransactionType,
    InvalidFieldEncoding,
    IntegerOverflow,
    InvalidChainId,
    ChainMismatch,
    UnprotectedLegacyTransaction,
    InvalidAddress,
    InvalidAddressChecksum,
    InvalidSignatureEncoding,
    InvalidSignature,
    SignatureMismatch,
    InvalidContractData,
    UntrustedContract,
    UnknownContractCall,
    ContractCreationDisallowed,
    InvalidAccessList,
    PriorityFeeExceedsMaxFee,
    FeeLimitExceeded,
    DisplayBindingMismatch,
    DisplayReadbackMismatch,
    SigningFailed,
    InternalError,
};

struct Error {
    ErrorCode code = ErrorCode::None;
    std::string message;
    std::size_t byte_offset = 0;

    [[nodiscard]] explicit operator bool() const noexcept {
        return code != ErrorCode::None;
    }
};

template <typename T>
class Result {
public:
    Result(T value) : value_(std::move(value)) {}
    Result(Error error) : error_(std::move(error)) {}

    [[nodiscard]] bool has_value() const noexcept { return value_.has_value(); }
    [[nodiscard]] explicit operator bool() const noexcept { return has_value(); }

    [[nodiscard]] const T& value() const& { return *value_; }
    [[nodiscard]] T& value() & { return *value_; }
    [[nodiscard]] T&& value() && { return std::move(*value_); }
    [[nodiscard]] const T& operator*() const& { return *value_; }
    [[nodiscard]] T& operator*() & { return *value_; }
    [[nodiscard]] T&& operator*() && { return std::move(*value_); }
    [[nodiscard]] const T* operator->() const { return &*value_; }
    [[nodiscard]] T* operator->() { return &*value_; }

    [[nodiscard]] const Error& error() const noexcept { return error_; }

private:
    std::optional<T> value_;
    Error error_;
};

// ---------------------------------------------------------------------------
// Fixed-width value types
// ---------------------------------------------------------------------------

struct Hash256 {
    std::array<std::uint8_t, 32> bytes{};

    [[nodiscard]] std::string hex() const;
    [[nodiscard]] static std::optional<Hash256> from_hex(std::string_view text);

    friend auto operator<=>(const Hash256&, const Hash256&) = default;
};

struct Uint256 {
    // Network byte order (big endian), matching ABI and RLP integer encoding.
    std::array<std::uint8_t, 32> big_endian{};

    [[nodiscard]] bool is_zero() const noexcept;
    [[nodiscard]] std::string decimal() const;
    [[nodiscard]] static std::optional<Uint256> from_decimal(std::string_view text);
    [[nodiscard]] static Uint256 from_u64(std::uint64_t value);
    [[nodiscard]] std::uint64_t to_u64() const;

    friend auto operator<=>(const Uint256&, const Uint256&) = default;
};

struct Address {
    std::array<std::uint8_t, 20> bytes{};

    // Accepts "0x" + 40 hex digits. Mixed-case input is accepted only when it
    // carries a valid EIP-55 checksum. All-lowercase and all-uppercase input
    // are accepted (they contain no checksum information).
    [[nodiscard]] static std::optional<Address> from_hex(std::string_view text);

    // EIP-55 mixed-case representation, including the 0x prefix.
    [[nodiscard]] std::string checksum_hex() const;
    [[nodiscard]] std::string lowercase_hex() const;
    [[nodiscard]] bool is_zero() const noexcept;

    friend auto operator<=>(const Address&, const Address&) = default;
};

struct CompactSignature {
    // r || s, each 32 bytes, big endian.
    std::array<std::uint8_t, 64> bytes{};
    // Ethereum y parity (0 or 1). The x-overflow recovery ids (2/3) are not
    // used by Ethereum transactions or by this component.
    std::uint8_t recovery_id = 0;

    friend auto operator<=>(const CompactSignature&, const CompactSignature&) = default;
};

// ---------------------------------------------------------------------------
// Device policy. Everything here is firmware-owned, never host-supplied.
// ---------------------------------------------------------------------------

struct TokenPolicy {
    Address contract;
    std::string symbol;
    std::uint8_t decimals = 18;
    // Optional cap for ERC-20 transfer(address,uint256) amounts, base units.
    std::optional<Uint256> max_transfer;
};

struct TransactionPolicy {
    std::uint64_t chain_id = 1;
    std::string chain_name = "Ethereum";
    std::string native_symbol = "ETH";
    std::uint8_t native_decimals = 18;

    std::vector<TokenPolicy> tokens;
    std::optional<Address> expected_signer;

    // Unknown contract calls and contract creation are dangerous default-deny
    // operations. Firmware may opt in only for explicitly reviewed use cases.
    bool allow_unknown_contract_calls = false;
    bool allow_contract_creation = false;

    // Pre-EIP-155 signatures do not authenticate a chain. They remain disabled
    // unless a specialised firmware policy deliberately enables them.
    bool allow_unprotected_legacy = false;
    bool require_low_s_signature = true;

    std::optional<Uint256> max_fee;
    std::size_t max_transaction_bytes = 128U * 1024U;
    std::size_t max_calldata_bytes = 64U * 1024U;
};

// ---------------------------------------------------------------------------
// Device-produced display
// ---------------------------------------------------------------------------

enum class DisplayKind : std::uint8_t {
    NativeTransfer = 0,
    TokenTransfer,
    ContractCall,
    ContractCreation,
};

struct DisplayField {
    std::string label;
    std::string value;

    friend auto operator<=>(const DisplayField&, const DisplayField&) = default;
};

struct DisplayContent {
    DisplayKind kind = DisplayKind::NativeTransfer;
    std::uint64_t chain_id = 0;
    std::string chain_name;
    std::string transaction_type;
    bool input_was_signed = false;

    // transaction_to is the recipient for native transfers and the contract
    // for token/contract calls. It is empty only for contract creation.
    std::optional<Address> transaction_to;
    // For ERC-20 transfers this is the address inside the calldata.
    std::optional<Address> recipient;
    std::optional<Address> token_contract;

    std::string asset_symbol;
    std::uint8_t asset_decimals = 18;
    std::string amount_base_units;
    std::string amount_display;
    // Native currency attached to a token/contract call. Token transfers show
    // this separately so an attached ETH value cannot be hidden by the token
    // amount.
    std::string native_value_base_units;
    std::string native_value_display;
    std::string max_fee_base_units;
    std::string max_fee_display;

    std::string gas_limit;
    std::string gas_price;
    std::string max_priority_fee_per_gas;
    std::string nonce;
    std::string calldata_selector;
    std::string calldata_hash;
    std::string access_list_hash;
    std::size_t access_list_entries = 0;
    bool unknown_contract_data = false;

    std::vector<DisplayField> fields;
    // Canonical ASCII text. This is exactly what the device must draw and what
    // the readback check covers. No host-supplied string is ever appended.
    std::string text;

    [[nodiscard]] std::optional<std::string> field(std::string_view label) const;
};

// Arbitrary host claims. The parser deliberately accepts them at the transport
// boundary only so firmware can retain protocol metadata; confirm() never reads
// any field in HostMetadata and never includes it in the display or digest.
struct HostMetadata {
    std::string display_text;
    std::string recipient;
    std::string amount;
    std::string fee;
    std::string chain;
    std::string signing_digest;
    Bytes claimed_parsed_result;
};

struct HostRequest {
    Bytes raw_transaction;
    HostMetadata untrusted_metadata;
};

// ---------------------------------------------------------------------------
// Binding and signing
// ---------------------------------------------------------------------------

struct DisplayReadback {
    Hash256 signing_digest;
    Hash256 display_commitment;
    Bytes display_bytes;
};

struct ApprovedTransaction {
    CompactSignature signature;
    Address signer;
    Hash256 signing_digest;
    Hash256 display_commitment;
    Bytes display_bytes;

    [[nodiscard]] bool verify_readback() const;
};

using SignCallback = std::function<CompactSignature(
    const Hash256& signing_digest,
    const Hash256& display_commitment)>;

// Domain-separated commitment: H("netsec.display.v1" || digest || u32be(len) ||
// exact display bytes). The exact bytes contain the full EIP-55 address,
// amount, fee and chain, so changing a drawn character changes the commitment.
[[nodiscard]] Hash256 make_display_commitment(const Hash256& signing_digest,
                                              ByteView display_bytes);

// Checks a frame buffer read back from the device (or a DisplayReadback
// structure) against its digest and commitment.
[[nodiscard]] bool verify_display_readback(const DisplayReadback& readback);
[[nodiscard]] bool verify_display_readback(const Hash256& signing_digest,
                                           const Hash256& expected_commitment,
                                           ByteView display_bytes);

[[nodiscard]] Result<Address> recover_signer(const Hash256& signing_digest,
                                             const CompactSignature& signature,
                                             bool require_low_s = true);

class TransactionConfirmer;

class Confirmation {
public:
    Confirmation(const Confirmation&) = default;
    Confirmation(Confirmation&&) noexcept = default;
    Confirmation& operator=(const Confirmation&) = default;
    Confirmation& operator=(Confirmation&&) noexcept = default;

    [[nodiscard]] const DisplayContent& display() const noexcept { return display_; }
    [[nodiscard]] std::string_view display_text() const noexcept { return display_.text; }
    [[nodiscard]] const Hash256& signing_digest() const noexcept { return signing_digest_; }
    [[nodiscard]] const Hash256& display_commitment() const noexcept {
        return display_commitment_;
    }
    [[nodiscard]] const Bytes& raw_transaction() const noexcept { return raw_transaction_; }
    [[nodiscard]] bool input_was_signed() const noexcept { return input_was_signed_; }
    // Present only when the input transaction already carried a signature and
    // that signature was verified/recovered successfully.
    [[nodiscard]] const std::optional<Address>& recovered_sender() const noexcept {
        return recovered_sender_;
    }

    [[nodiscard]] Result<DisplayReadback> readback() const;
    [[nodiscard]] bool verify_readback(const DisplayReadback& readback) const;
    [[nodiscard]] bool verify_readback(ByteView frame_bytes) const;

    // The callback receives only the digest and the device-computed display
    // commitment. It never receives a display buffer or any host claim.
    // The returned signature is recovered and must belong to expected_signer,
    // proving that the secret-bearing operation covered the digest that the
    // device displayed.
    // The one-argument form uses TransactionPolicy::expected_signer.
    [[nodiscard]] Result<ApprovedTransaction> sign(
        const SignCallback& callback) const;
    [[nodiscard]] Result<ApprovedTransaction> sign(const SignCallback& callback,
                                                   const Address& expected_signer) const;

private:
    friend class TransactionConfirmer;

    Confirmation(Bytes raw_transaction,
                 DisplayContent display,
                 Hash256 signing_digest,
                 Hash256 display_commitment,
                 std::optional<Address> recovered_sender,
                 std::optional<Address> expected_signer,
                 bool input_was_signed,
                 bool require_low_s);

    Bytes raw_transaction_;
    DisplayContent display_;
    Hash256 signing_digest_{};
    Hash256 display_commitment_{};
    std::optional<Address> recovered_sender_;
    std::optional<Address> expected_signer_;
    bool input_was_signed_ = false;
    bool require_low_s_ = true;
};

class TransactionConfirmer {
public:
    explicit TransactionConfirmer(TransactionPolicy policy);

    [[nodiscard]] const TransactionPolicy& policy() const noexcept { return policy_; }

    // Reads only raw_transaction. HostRequest::untrusted_metadata is ignored.
    [[nodiscard]] Result<Confirmation> confirm(const HostRequest& request) const;
    [[nodiscard]] Result<Confirmation> confirm(ByteView raw_transaction) const;

private:
    TransactionPolicy policy_;
    std::optional<Error> policy_error_;
};

// Convenience entry point. untrusted_host_metadata is intentionally inert.
[[nodiscard]] Result<Confirmation> confirm_transaction(
    ByteView raw_transaction,
    const TransactionPolicy& policy,
    const HostMetadata& untrusted_host_metadata = {});

}  // namespace netsec
