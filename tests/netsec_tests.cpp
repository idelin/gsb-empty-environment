#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cstdint>
#include <cstdlib>
#include <iostream>
#include <stdexcept>
#include <string>
#include <vector>

namespace {

using netsec::Byte;
using netsec::Bytes;
using netsec::Cipher;
using netsec::Dialect;
using netsec::Digest512;
using netsec::Error;
using netsec::ErrorCode;
using netsec::Guid;
using netsec::MessageKind;
using netsec::PeerIdentity;

int failures = 0;
int checks = 0;

void check(bool condition, const char* expression, const char* file, int line) {
    ++checks;
    if (!condition) {
        ++failures;
        std::cerr << file << ':' << line << ": check failed: " << expression << '\n';
    }
}

#define CHECK(expr) check(static_cast<bool>(expr), #expr, __FILE__, __LINE__)
#define CHECK_ERROR(expr, expected_code)                                               \
    do {                                                                               \
        try {                                                                          \
            (void)(expr);                                                              \
            ++checks;                                                                  \
            ++failures;                                                                \
            std::cerr << __FILE__ << ':' << __LINE__ << ": expected " << #expected_code \
                      << " from " << #expr << '\n';                                    \
        } catch (const netsec::Error& error) {                                         \
            ++checks;                                                                  \
            if (error.code() != (expected_code)) {                                     \
                ++failures;                                                            \
                std::cerr << __FILE__ << ':' << __LINE__ << ": expected "              \
                          << #expected_code                                            \
                          << ", got " << netsec::to_string(error.code()) << " from "   \
                          << #expr << '\n';                                            \
            }                                                                          \
        }                                                                              \
    } while (false)

void append_le16(Bytes& out, std::uint16_t value) {
    out.push_back(static_cast<Byte>(value & 0xffu));
    out.push_back(static_cast<Byte>((value >> 8) & 0xffu));
}

void append_le32(Bytes& out, std::uint32_t value) {
    out.push_back(static_cast<Byte>(value & 0xffu));
    out.push_back(static_cast<Byte>((value >> 8) & 0xffu));
    out.push_back(static_cast<Byte>((value >> 16) & 0xffu));
    out.push_back(static_cast<Byte>((value >> 24) & 0xffu));
}

void append_le64(Bytes& out, std::uint64_t value) {
    append_le32(out, static_cast<std::uint32_t>(value));
    append_le32(out, static_cast<std::uint32_t>(value >> 32));
}

void write_le32(Bytes& out, std::size_t offset, std::uint32_t value) {
    out[offset] = static_cast<Byte>(value & 0xffu);
    out[offset + 1] = static_cast<Byte>((value >> 8) & 0xffu);
    out[offset + 2] = static_cast<Byte>((value >> 16) & 0xffu);
    out[offset + 3] = static_cast<Byte>((value >> 24) & 0xffu);
}

std::size_t align8(std::size_t value) {
    return (value + 7u) & ~std::size_t{7u};
}

Bytes make_header(std::uint16_t command, bool response, std::uint32_t status,
                  std::uint64_t message_id) {
    Bytes out(64, 0);
    out[0] = 0xfeu;
    out[1] = 'S';
    out[2] = 'M';
    out[3] = 'B';
    out[4] = 64;
    out[8] = static_cast<Byte>(status & 0xffu);
    out[9] = static_cast<Byte>((status >> 8) & 0xffu);
    out[10] = static_cast<Byte>((status >> 16) & 0xffu);
    out[11] = static_cast<Byte>((status >> 24) & 0xffu);
    out[12] = static_cast<Byte>(command & 0xffu);
    out[13] = static_cast<Byte>((command >> 8) & 0xffu);
    out[16] = response ? 1u : 0u;
    out[24] = static_cast<Byte>(message_id & 0xffu);
    out[25] = static_cast<Byte>((message_id >> 8) & 0xffu);
    out[26] = static_cast<Byte>((message_id >> 16) & 0xffu);
    out[27] = static_cast<Byte>((message_id >> 24) & 0xffu);
    return out;
}

Guid make_guid(Byte seed) {
    Guid guid;
    for (std::size_t i = 0; i < guid.bytes.size(); ++i) {
        guid.bytes[i] = static_cast<Byte>(seed + i);
    }
    return guid;
}

Bytes preauth_context(std::vector<std::uint16_t> algorithms, Bytes salt) {
    Bytes data;
    append_le16(data, static_cast<std::uint16_t>(algorithms.size()));
    append_le16(data, static_cast<std::uint16_t>(salt.size()));
    for (std::uint16_t algorithm : algorithms) {
        append_le16(data, algorithm);
    }
    data.insert(data.end(), salt.begin(), salt.end());

    Bytes out;
    append_le16(out, 0x0001u);
    append_le16(out, static_cast<std::uint16_t>(data.size()));
    append_le32(out, 0);
    out.insert(out.end(), data.begin(), data.end());
    while (out.size() % 8u != 0u) {
        out.push_back(0);
    }
    return out;
}

Bytes encryption_context(std::vector<std::uint16_t> ciphers) {
    Bytes data;
    append_le16(data, static_cast<std::uint16_t>(ciphers.size()));
    for (std::uint16_t cipher : ciphers) {
        append_le16(data, cipher);
    }

    Bytes out;
    append_le16(out, 0x0002u);
    append_le16(out, static_cast<std::uint16_t>(data.size()));
    append_le32(out, 0);
    out.insert(out.end(), data.begin(), data.end());
    while (out.size() % 8u != 0u) {
        out.push_back(0);
    }
    return out;
}

Bytes make_negotiate_request(std::vector<Dialect> dialects,
                             const std::vector<Bytes>& contexts,
                             std::uint64_t message_id = 1) {
    Bytes out = make_header(0x0000u, false, 0, message_id);
    Bytes body;
    append_le16(body, 36);
    append_le16(body, static_cast<std::uint16_t>(dialects.size()));
    append_le16(body, 1);  // signing enabled
    append_le16(body, 0);  // reserved
    append_le32(body, 0);  // capabilities
    for (std::size_t i = 0; i < 16; ++i) {
        body.push_back(static_cast<Byte>(0xa0u + i));
    }
    for (std::size_t i = 0; i < 8; ++i) {
        body.push_back(0);
    }
    const std::size_t context_offset_slot = body.size();
    append_le32(body, 0);
    append_le16(body, static_cast<std::uint16_t>(contexts.size()));
    append_le16(body, 0);
    for (Dialect dialect : dialects) {
        append_le16(body, static_cast<std::uint16_t>(dialect));
    }

    const std::size_t absolute_context_offset =
        contexts.empty() ? 0u : align8(64u + body.size());
    while (!contexts.empty() && 64u + body.size() < absolute_context_offset) {
        body.push_back(0);
    }
    write_le32(body, context_offset_slot,
               static_cast<std::uint32_t>(absolute_context_offset));
    for (const Bytes& context : contexts) {
        body.insert(body.end(), context.begin(), context.end());
    }
    out.insert(out.end(), body.begin(), body.end());
    return out;
}

Bytes make_negotiate_response(Dialect dialect, const Guid& server_guid,
                              const std::vector<Bytes>& contexts,
                              std::uint64_t message_id = 2) {
    Bytes out = make_header(0x0000u, true, 0, message_id);
    Bytes body;
    append_le16(body, 65);
    append_le16(body, 1);  // signing enabled
    append_le16(body, static_cast<std::uint16_t>(dialect));
    append_le16(body, static_cast<std::uint16_t>(contexts.size()));
    body.insert(body.end(), server_guid.bytes.begin(), server_guid.bytes.end());
    append_le32(body, 0);  // capabilities
    append_le32(body, 1u << 20);
    append_le32(body, 1u << 20);
    append_le32(body, 1u << 20);
    append_le32(body, 0);  // system time low
    append_le32(body, 0);  // system time high
    append_le32(body, 0);  // server start low
    append_le32(body, 0);  // server start high
    append_le16(body, 0);  // security buffer offset
    append_le16(body, 0);  // security buffer length
    append_le32(body, contexts.empty() ? 0u : 128u);

    CHECK(body.size() == 64);
    out.insert(out.end(), body.begin(), body.end());
    CHECK(out.size() == 128);
    for (const Bytes& context : contexts) {
        out.insert(out.end(), context.begin(), context.end());
    }
    return out;
}

Bytes make_session_setup_request(std::uint64_t message_id = 3) {
    Bytes out = make_header(0x0001u, false, 0, message_id);
    Bytes body;
    append_le16(body, 25);
    body.push_back(0);  // flags
    body.push_back(1);  // security mode: signing
    append_le32(body, 0);
    append_le32(body, 0);
    append_le16(body, 0);
    append_le16(body, 0);
    append_le64(body, 0);
    CHECK(body.size() == 24);
    out.insert(out.end(), body.begin(), body.end());
    return out;
}

Bytes make_session_setup_response(std::uint32_t status, std::uint64_t message_id = 4) {
    Bytes out = make_header(0x0001u, true, status, message_id);
    Bytes body;
    append_le16(body, 9);
    append_le16(body, 0);
    append_le16(body, 0);
    append_le16(body, 0);
    CHECK(body.size() == 8);
    out.insert(out.end(), body.begin(), body.end());
    return out;
}

struct ChannelSpec {
    std::uint64_t id = 0;
    Dialect selected_dialect = Dialect::Smb311;
    std::vector<Dialect> offered_dialects = {Dialect::Smb311};
    Guid server_guid = make_guid(0x10);
    std::vector<std::uint16_t> ciphers = {0x0004};
    PeerIdentity peer{"files.example.test", Bytes{0x01, 0x02, 0x03, 0x04}};
    std::optional<netsec::SigningAlgorithm> signing;
    std::uint64_t message_id = 1;
    std::uint32_t salt_seed = 0;
    bool include_preauth = true;
    bool include_encryption = true;
    std::vector<Bytes> extra_response_contexts;
};

std::vector<Bytes> response_contexts(const ChannelSpec& spec) {
    std::vector<Bytes> contexts;
    if (spec.include_preauth) {
        Bytes salt(16, 0);
        for (std::size_t i = 0; i < salt.size(); ++i) {
            salt[i] = static_cast<Byte>((spec.salt_seed + i) & 0xffu);
        }
        contexts.push_back(preauth_context({0x0001u}, salt));
    }
    if (spec.include_encryption) {
        contexts.push_back(encryption_context(spec.ciphers));
    }
    for (const Bytes& extra : spec.extra_response_contexts) {
        contexts.push_back(extra);
    }
    return contexts;
}

netsec::ChannelTranscript build_channel(const ChannelSpec& spec) {
    std::vector<Bytes> request_contexts;
    if (spec.include_preauth) {
        request_contexts.push_back(preauth_context({0x0001u}, Bytes{0x51, 0x52}));
    }
    if (spec.include_encryption) {
        request_contexts.push_back(encryption_context(spec.ciphers));
    }

    netsec::ChannelOptions options;
    options.channel_id = spec.id;
    options.peer = spec.peer;
    options.selected_signing = spec.signing;
    netsec::ChannelNegotiation negotiation(options);
    negotiation.append(MessageKind::NegotiateRequest,
                       make_negotiate_request(spec.offered_dialects, request_contexts,
                                              spec.message_id));
    negotiation.append(MessageKind::NegotiateResponse,
                       make_negotiate_response(spec.selected_dialect, spec.server_guid,
                                               response_contexts(spec),
                                               spec.message_id + 1));
    negotiation.append(MessageKind::SessionSetupRequest,
                       make_session_setup_request(spec.message_id + 2));
    negotiation.append(MessageKind::SessionSetupResponse,
                       make_session_setup_response(0, spec.message_id + 3));
    return negotiation.finish();
}

Bytes session_key(Byte seed = 0x61) {
    Bytes key(32, 0);
    for (std::size_t i = 0; i < key.size(); ++i) {
        key[i] = static_cast<Byte>((seed + i * 7u) & 0xffu);
    }
    return key;
}

netsec::SessionNegotiation make_session(const Bytes& key, std::uint32_t salt_seed = 0) {
    ChannelSpec spec;
    spec.id = 0;
    spec.salt_seed = salt_seed;
    const netsec::ChannelTranscript channel = build_channel(spec);
    return netsec::SessionNegotiation::establish(channel, key,
                                                 netsec::compute_preauth_proof(key, channel));
}

netsec::ChannelOptions channel_options(std::uint64_t id, const PeerIdentity& peer) {
    netsec::ChannelOptions options;
    options.channel_id = id;
    options.peer = peer;
    return options;
}

// ---------------------------------------------------------------------------

void test_crypto_vectors_and_zero_start() {
    const Digest512 abc =
        netsec::sha512(netsec::to_bytes("abc"));
    CHECK(abc.to_hex() ==
          "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a"
          "2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f");
    CHECK(netsec::sha256(netsec::to_bytes("abc")).to_hex() ==
          "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");
    std::string long_input;
    for (int i = 0; i < 100; ++i) {
        long_input += "0123456789abcdef";
    }
    CHECK(netsec::sha512(netsec::to_bytes(long_input)).to_hex() ==
          "4cfa8cae5079eea8f1a112eeda879a67091957d2461e58ce6f0fb24bd13a75aa"
          "8ca1871576d13f149dd911286df9b5f9de05ed89cfc56ff91fc34dcc650d4337");

    netsec::PreauthIntegrityChain chain;
    CHECK(chain.digest().is_zero());
    CHECK(chain.message_count() == 0);

    const Bytes first = {0x01, 0x02, 0x03, 0x04, 0x05};
    const Bytes second = {0xaa, 0xbb, 0xcc};
    chain.append(MessageKind::NegotiateRequest, first);
    chain.append(MessageKind::NegotiateResponse, second);

    Bytes first_input(64, 0);
    first_input.insert(first_input.end(), first.begin(), first.end());
    const Digest512 after_first = netsec::sha512(first_input);
    Bytes second_input(after_first.bytes.begin(), after_first.bytes.end());
    second_input.insert(second_input.end(), second.begin(), second.end());
    const Digest512 expected = netsec::sha512(second_input);
    CHECK(chain.digest() == expected);
    CHECK(!chain.digest().is_zero());

    const Digest512 before = chain.digest();
    CHECK_ERROR(chain.reset(), ErrorCode::ChainResetNotAllowed);
    CHECK(chain.digest() == before);
}

void test_channel_hashing_exact_raw_bytes() {
    ChannelSpec spec;
    spec.id = 7;
    const netsec::ChannelTranscript channel = build_channel(spec);

    const auto& request_contexts = std::vector<Bytes>{
        preauth_context({0x0001u}, Bytes{0x51, 0x52}),
        encryption_context(spec.ciphers),
    };
    const Bytes request =
        make_negotiate_request(spec.offered_dialects, request_contexts, spec.message_id);
    const Bytes response =
        make_negotiate_response(spec.selected_dialect, spec.server_guid,
                                response_contexts(spec), spec.message_id + 1);
    const Bytes ss_req = make_session_setup_request(spec.message_id + 2);
    const Bytes ss_resp = make_session_setup_response(0, spec.message_id + 3);
    Digest512 rolling{};
    for (const Bytes* message : {&request, &response, &ss_req, &ss_resp}) {
        Bytes material(rolling.bytes.begin(), rolling.bytes.end());
        material.insert(material.end(), message->begin(), message->end());
        rolling = netsec::sha512(material);
    }
    CHECK(channel.preauth_hash == rolling);
}

void test_multiple_session_setup_rounds() {
    ChannelSpec spec;
    netsec::ChannelNegotiation negotiation(
        channel_options(spec.id, spec.peer));
    negotiation.append(
        MessageKind::NegotiateRequest,
        make_negotiate_request(
            spec.offered_dialects,
            {preauth_context({0x0001u}, Bytes{0x51, 0x52}),
             encryption_context(spec.ciphers)},
            spec.message_id));
    negotiation.append(
        MessageKind::NegotiateResponse,
        make_negotiate_response(spec.selected_dialect, spec.server_guid,
                                response_contexts(spec), spec.message_id + 1));
    negotiation.append(MessageKind::SessionSetupRequest,
                       make_session_setup_request(spec.message_id + 2));
    negotiation.append(MessageKind::SessionSetupResponse,
                       make_session_setup_response(0xc0000016u, spec.message_id + 3));
    CHECK(!negotiation.complete());
    negotiation.append(MessageKind::SessionSetupRequest,
                       make_session_setup_request(spec.message_id + 4));
    negotiation.append(MessageKind::SessionSetupResponse,
                       make_session_setup_response(0, spec.message_id + 5));
    CHECK(negotiation.complete());
    const netsec::ChannelTranscript transcript = negotiation.finish();
    CHECK(transcript.message_count == 6);
}

void test_unoffered_cipher_and_request_duplicate_rejected() {
    ChannelSpec spec;
    const Bytes request = make_negotiate_request(
        spec.offered_dialects,
        {preauth_context({0x0001u}, Bytes{0x51}),
         encryption_context({0x0002u})});  // AES-128-GCM
    netsec::ChannelNegotiation negotiation(channel_options(spec.id, spec.peer));
    negotiation.append(MessageKind::NegotiateRequest, request);
    const Digest512 before = negotiation.current_hash();
    const Bytes response = make_negotiate_response(
        Dialect::Smb311, spec.server_guid,
        {preauth_context({0x0001u}, Bytes{0x52}),
         encryption_context({0x0004u})});  // AES-256-GCM was never offered
    CHECK_ERROR(negotiation.append(MessageKind::NegotiateResponse, response),
                ErrorCode::CipherMismatch);
    CHECK(negotiation.current_hash() == before);

    netsec::ChannelNegotiation duplicate_request(
        channel_options(spec.id, spec.peer));
    const Bytes request_with_duplicate = make_negotiate_request(
        spec.offered_dialects,
        {preauth_context({0x0001u}, Bytes{0x51}),
         encryption_context({0x0004u}),
         encryption_context({0x0004u})});
    CHECK_ERROR(duplicate_request.append(MessageKind::NegotiateRequest,
                                         request_with_duplicate),
                ErrorCode::DuplicateNegotiateContext);
    CHECK(duplicate_request.current_hash().is_zero());
}

void test_duplicate_context_and_unknown_algorithm() {
    ChannelSpec spec;
    const std::vector<Bytes> request_contexts = {
        preauth_context({0x0001u}, Bytes{0x51, 0x52}),
        encryption_context(spec.ciphers),
    };
    const Bytes request =
        make_negotiate_request(spec.offered_dialects, request_contexts);

    // Duplicate encryption context in the response.
    ChannelSpec duplicate = spec;
    duplicate.extra_response_contexts = {encryption_context({0x0004u})};
    netsec::ChannelNegotiation duplicate_negotiation;
    duplicate_negotiation.append(MessageKind::NegotiateRequest, request);
    const Digest512 after_request = duplicate_negotiation.current_hash();
    CHECK_ERROR(
        duplicate_negotiation.append(
            MessageKind::NegotiateResponse,
            make_negotiate_response(Dialect::Smb311, duplicate.server_guid,
                                    response_contexts(duplicate))),
        ErrorCode::DuplicateNegotiateContext);
    CHECK(duplicate_negotiation.current_hash() == after_request);

    // Unknown mandatory cipher.
    ChannelSpec unknown = spec;
    unknown.ciphers = {0x0004u, 0xffffu};
    netsec::ChannelNegotiation unknown_negotiation;
    unknown_negotiation.append(MessageKind::NegotiateRequest,
                               make_negotiate_request(unknown.offered_dialects,
                                                      request_contexts));
    const Digest512 unknown_before = unknown_negotiation.current_hash();
    CHECK_ERROR(
        unknown_negotiation.append(
            MessageKind::NegotiateResponse,
            make_negotiate_response(Dialect::Smb311, unknown.server_guid,
                                    response_contexts(unknown))),
        ErrorCode::UnknownCipherAlgorithm);
    CHECK(unknown_negotiation.current_hash() == unknown_before);

    // Unknown mandatory pre-authentication hash algorithm.
    ChannelSpec unknown_preauth = spec;
    unknown_preauth.include_encryption = false;
    netsec::ChannelNegotiation unknown_preauth_negotiation;
    unknown_preauth_negotiation.append(MessageKind::NegotiateRequest,
                                       make_negotiate_request(
                                           unknown_preauth.offered_dialects,
                                           {preauth_context({0x0001u}, Bytes{0x51})}));
    CHECK_ERROR(
        unknown_preauth_negotiation.append(
            MessageKind::NegotiateResponse,
            make_negotiate_response(Dialect::Smb311, unknown_preauth.server_guid,
                                    {preauth_context({0x0007u}, Bytes{0x99})})),
        ErrorCode::UnknownPreauthAlgorithm);
}

void test_truncation_and_order_leave_no_partial_state() {
    ChannelSpec spec;
    const Bytes request = make_negotiate_request(
        spec.offered_dialects,
        {preauth_context({0x0001u}, Bytes{0x51, 0x52}),
         encryption_context(spec.ciphers)});

    netsec::ChannelNegotiation out_of_order;
    CHECK_ERROR(out_of_order.append(MessageKind::NegotiateResponse,
                                    make_negotiate_response(
                                        Dialect::Smb311, spec.server_guid,
                                        response_contexts(spec))),
                ErrorCode::UnexpectedMessage);
    CHECK(out_of_order.current_hash().is_zero());
    CHECK_ERROR(out_of_order.finish(), ErrorCode::NegotiationIncomplete);

    netsec::ChannelNegotiation truncated;
    truncated.append(MessageKind::NegotiateRequest, request);
    const Digest512 before = truncated.current_hash();
    Bytes response =
        make_negotiate_response(Dialect::Smb311, spec.server_guid,
                                response_contexts(spec));
    response.resize(response.size() - 5u);
    CHECK_ERROR(truncated.append(MessageKind::NegotiateResponse, response),
                ErrorCode::MessageTruncated);
    CHECK(truncated.current_hash() == before);
    CHECK_ERROR(truncated.finish(), ErrorCode::NegotiationIncomplete);

    ChannelSpec failed_spec;
    netsec::ChannelNegotiation failed(
        channel_options(failed_spec.id, failed_spec.peer));
    failed.append(MessageKind::NegotiateRequest, request);
    failed.append(MessageKind::NegotiateResponse,
                  make_negotiate_response(Dialect::Smb311, failed_spec.server_guid,
                                          response_contexts(failed_spec)));
    failed.append(MessageKind::SessionSetupRequest,
                  make_session_setup_request(failed_spec.message_id + 2));
    const Digest512 before_failure = failed.current_hash();
    CHECK_ERROR(
        failed.append(MessageKind::SessionSetupResponse,
                      make_session_setup_response(0xc000000du, 99)),
        ErrorCode::SessionSetupFailed);
    CHECK(failed.current_hash() == before_failure);
    CHECK_ERROR(failed.finish(), ErrorCode::NegotiationIncomplete);
}

void test_second_channel_downgrade_rejected() {
    const Bytes key = session_key();
    netsec::SessionNegotiation session = make_session(key, 0x11);
    const std::uint64_t before = session.channel_count();

    ChannelSpec downgrade;
    downgrade.id = 1;
    downgrade.selected_dialect = Dialect::Smb302;
    downgrade.offered_dialects = {Dialect::Smb302, Dialect::Smb311};
    downgrade.ciphers = {0x0004u};
    downgrade.salt_seed = 0x22;
    const netsec::ChannelTranscript channel = build_channel(downgrade);
    const Bytes proof = netsec::compute_preauth_proof(key, channel);

    CHECK_ERROR(session.join(channel, proof), ErrorCode::DialectDowngrade);
    CHECK(session.channel_count() == before);
}

void test_identity_binding_covers_security_fields() {
    const Bytes key = session_key();
    ChannelSpec base_spec;
    base_spec.salt_seed = 0x11;
    const netsec::ChannelTranscript base_channel = build_channel(base_spec);
    const netsec::SessionNegotiation base =
        netsec::SessionNegotiation::establish(
            base_channel, key, netsec::compute_preauth_proof(key, base_channel));

    auto binding_for = [&](ChannelSpec spec) {
        spec.id = 0;
        const netsec::ChannelTranscript channel = build_channel(spec);
        const netsec::SessionNegotiation session =
            netsec::SessionNegotiation::establish(
                channel, key, netsec::compute_preauth_proof(key, channel));
        return session.identity();
    };

    ChannelSpec different_guid = base_spec;
    different_guid.server_guid = make_guid(0x90);
    ChannelSpec different_cipher = base_spec;
    different_cipher.ciphers = {0x0002u};
    ChannelSpec different_signing = base_spec;
    different_signing.signing = netsec::SigningAlgorithm::HmacSha256;
    ChannelSpec different_host = base_spec;
    different_host.peer.host = "other.example.test";
    ChannelSpec different_cert = base_spec;
    different_cert.peer.certificate_sha256 = Bytes{0x09, 0x08, 0x07};
    ChannelSpec different_hash = base_spec;
    different_hash.salt_seed = 0x12;

    const netsec::SessionIdentity guid_identity = binding_for(different_guid);
    const netsec::SessionIdentity cipher_identity = binding_for(different_cipher);
    const netsec::SessionIdentity signing_identity = binding_for(different_signing);
    const netsec::SessionIdentity host_identity = binding_for(different_host);
    const netsec::SessionIdentity cert_identity = binding_for(different_cert);
    const netsec::SessionIdentity hash_identity = binding_for(different_hash);

    CHECK(!(guid_identity.server_guid() == base.identity().server_guid()));
    CHECK(cipher_identity.cipher() == Cipher::Aes128Gcm);
    CHECK(signing_identity.signing() == netsec::SigningAlgorithm::HmacSha256);
    CHECK(host_identity.host() != base.identity().host());
    CHECK(cert_identity.certificate_sha256() != base.identity().certificate_sha256());
    CHECK(hash_identity.preauth_hash() != base.identity().preauth_hash());

    const std::array<netsec::Digest256, 7> bindings = {
        base.identity().binding_digest(), guid_identity.binding_digest(),
        cipher_identity.binding_digest(), signing_identity.binding_digest(),
        host_identity.binding_digest(), cert_identity.binding_digest(),
        hash_identity.binding_digest(),
    };
    for (std::size_t i = 0; i < bindings.size(); ++i) {
        for (std::size_t j = i + 1; j < bindings.size(); ++j) {
            CHECK(bindings[i] != bindings[j]);
        }
    }
}

void test_guid_replacement_rejected() {
    const Bytes key = session_key();
    netsec::SessionNegotiation session = make_session(key, 0x11);
    const std::uint64_t before = session.channel_count();

    ChannelSpec replaced;
    replaced.id = 2;
    replaced.server_guid = make_guid(0x77);
    replaced.salt_seed = 0x33;
    const netsec::ChannelTranscript channel = build_channel(replaced);
    const Bytes proof = netsec::compute_preauth_proof(key, channel);
    CHECK_ERROR(session.join(channel, proof), ErrorCode::ServerGuidMismatch);
    CHECK(session.channel_count() == before);
}

void test_raw_byte_rewrite_breaks_hash_and_proof() {
    const Bytes key = session_key();
    ChannelSpec spec;
    spec.id = 3;
    spec.salt_seed = 0x44;
    const netsec::ChannelTranscript original = build_channel(spec);
    const Bytes proof = netsec::compute_preauth_proof(key, original);
    CHECK(netsec::verify_preauth_proof(key, original, proof));

    ChannelSpec rewritten = spec;
    rewritten.salt_seed = 0x45;  // changes raw NEGOTIATE response bytes
    const netsec::ChannelTranscript changed = build_channel(rewritten);
    CHECK(changed.preauth_hash != original.preauth_hash);
    CHECK(!netsec::verify_preauth_proof(key, changed, proof));

    netsec::SessionNegotiation session = make_session(key, 0x11);
    CHECK_ERROR(session.join(changed, proof), ErrorCode::ProofMismatch);
    CHECK(session.channel_count() == 1);
}

void test_reconnect_keeps_original_identity_and_starts_from_zero() {
    const Bytes key = session_key();
    netsec::SessionNegotiation session = make_session(key, 0x11);
    const netsec::SessionIdentity original = session.identity();

    ChannelSpec reconnect_spec;
    reconnect_spec.id = 10;
    reconnect_spec.salt_seed = 0x55;
    reconnect_spec.message_id = 100;
    const netsec::ChannelTranscript reconnect_channel = build_channel(reconnect_spec);
    CHECK(!reconnect_channel.preauth_hash.is_zero());
    CHECK(reconnect_channel.preauth_hash != original.preauth_hash());

    const Bytes proof = netsec::compute_preauth_proof(key, reconnect_channel);
    netsec::SessionNegotiation reconnected =
        netsec::SessionNegotiation::reconnect(original, reconnect_channel, key, proof);
    CHECK(reconnected.identity() == original);
    CHECK(reconnected.identity().preauth_hash() == original.preauth_hash());
    CHECK(reconnected.channels().size() == 1);
    CHECK(reconnected.channels().front().preauth_hash ==
          reconnect_channel.preauth_hash);
    CHECK(reconnected.channels().front().session_binding == original.binding_digest());

    // A new TCP connection starts from zero, but a reset chain cannot carry
    // another connection's progress or replace the exported identity.
    netsec::PreauthIntegrityChain fresh;
    CHECK(fresh.digest().is_zero());

    ChannelSpec downgrade = reconnect_spec;
    downgrade.id = 11;
    downgrade.selected_dialect = Dialect::Smb302;
    downgrade.offered_dialects = {Dialect::Smb302, Dialect::Smb311};
    const netsec::ChannelTranscript lower = build_channel(downgrade);
    const Bytes lower_proof = netsec::compute_preauth_proof(key, lower);
    CHECK_ERROR(netsec::SessionNegotiation::reconnect(original, lower, key, lower_proof),
                ErrorCode::DialectDowngrade);

    ChannelSpec moved_guid = reconnect_spec;
    moved_guid.id = 12;
    moved_guid.server_guid = make_guid(0x88);
    const netsec::ChannelTranscript moved = build_channel(moved_guid);
    const Bytes moved_proof = netsec::compute_preauth_proof(key, moved);
    CHECK_ERROR(netsec::SessionNegotiation::reconnect(original, moved, key, moved_proof),
                ErrorCode::ServerGuidMismatch);

    CHECK_ERROR(netsec::SessionNegotiation::reconnect(original, reconnect_channel,
                                                      Bytes{0x01, 0x02}, proof),
                ErrorCode::ProofMismatch);
}

void test_async_failure_and_out_of_order_completion() {
    const Bytes key = session_key();
    netsec::ChannelTranscript base = build_channel(ChannelSpec{});
    netsec::AsyncSessionNegotiation async = netsec::AsyncSessionNegotiation::establish(
        base, key, netsec::compute_preauth_proof(key, base));

    ChannelSpec spec;
    spec.id = 20;
    spec.salt_seed = 0x66;
    const netsec::AsyncSessionNegotiation::AttemptId attempt =
        async.begin_channel(channel_options(spec.id, spec.peer));
    const Bytes request = make_negotiate_request(
        spec.offered_dialects,
        {preauth_context({0x0001u}, Bytes{0x51}), encryption_context(spec.ciphers)});
    async.append(attempt, MessageKind::NegotiateRequest, request);

    // An async transport failure kills the attempt and publishes nothing.
    const std::size_t before = async.channels().size();
    async.fail(attempt);
    ErrorCode error = ErrorCode::None;
    CHECK(!async.complete(attempt, Bytes{}, &error));
    CHECK(error == ErrorCode::StaleAttempt);
    CHECK(async.channels().size() == before);

    // Complete-before-messages is out of order and also publishes nothing.
    ChannelSpec early;
    early.id = 21;
    const auto early_attempt =
        async.begin_channel(channel_options(early.id, early.peer));
    CHECK(!async.complete(early_attempt, Bytes{}, &error));
    CHECK(error == ErrorCode::NegotiationIncomplete);
    CHECK(async.channels().size() == before);
}

void test_legitimate_multichannel_shares_security_out_of_order() {
    const Bytes key = session_key();
    netsec::SessionNegotiation session = make_session(key, 0x11);
    const netsec::SessionIdentity identity = session.identity();
    const Digest512 base_hash = identity.preauth_hash();

    std::vector<netsec::ChannelTranscript> channels;
    std::vector<Bytes> proofs;
    for (std::uint64_t i = 1; i <= 4; ++i) {
        ChannelSpec spec;
        spec.id = i;
        spec.salt_seed = static_cast<std::uint32_t>(0x20u * i);
        spec.message_id = i * 10;
        channels.push_back(build_channel(spec));
        proofs.push_back(netsec::compute_preauth_proof(key, channels.back()));
    }

    // Auxiliary transports may complete in any order.
    const std::array<std::size_t, 4> completion_order = {2, 0, 3, 1};
    for (std::size_t index : completion_order) {
        const netsec::BoundChannel bound = session.join(channels[index], proofs[index]);
        CHECK(bound.security == identity.security());
        CHECK(bound.peer == identity.peer());
        CHECK(bound.session_binding == identity.binding_digest());
        CHECK(bound.preauth_hash != base_hash);
    }

    CHECK(session.channel_count() == 5);
    CHECK(session.identity().preauth_hash() == base_hash);
    for (const netsec::BoundChannel& bound : session.channels()) {
        CHECK(bound.security.dialect == Dialect::Smb311);
        CHECK(bound.security.cipher == Cipher::Aes256Gcm);
        CHECK(bound.security.preauth_hash == netsec::PreauthHashAlgorithm::Sha512);
        CHECK(bound.session_binding == identity.binding_digest());
    }

    CHECK_ERROR(session.join(channels.front(), proofs.front()), ErrorCode::DuplicateChannel);
}

void test_async_multichannel_out_of_order_completion() {
    const Bytes key = session_key();
    netsec::ChannelTranscript base = build_channel(ChannelSpec{});
    netsec::AsyncSessionNegotiation async = netsec::AsyncSessionNegotiation::establish(
        base, key, netsec::compute_preauth_proof(key, base));

    auto start = [&](std::uint64_t id, std::uint32_t salt) {
        ChannelSpec spec;
        spec.id = id;
        spec.salt_seed = salt;
        spec.message_id = id * 10;
        const auto attempt =
            async.begin_channel(channel_options(spec.id, spec.peer));
        async.append(attempt, MessageKind::NegotiateRequest,
                     make_negotiate_request(
                         spec.offered_dialects,
                         {preauth_context({0x0001u}, Bytes{0x51, 0x52}),
                          encryption_context(spec.ciphers)},
                         spec.message_id));
        async.append(attempt, MessageKind::NegotiateResponse,
                     make_negotiate_response(spec.selected_dialect, spec.server_guid,
                                             response_contexts(spec),
                                             spec.message_id + 1));
        async.append(attempt, MessageKind::SessionSetupRequest,
                     make_session_setup_request(spec.message_id + 2));
        async.append(attempt, MessageKind::SessionSetupResponse,
                     make_session_setup_response(0, spec.message_id + 3));
        return std::pair<netsec::AsyncSessionNegotiation::AttemptId,
                         netsec::ChannelTranscript>{
            attempt, build_channel(spec)};
    };

    auto first = start(31, 0x31);
    auto second = start(32, 0x32);
    auto third = start(33, 0x33);

    // Complete in reverse order; every channel still inherits the same identity.
    ErrorCode error = ErrorCode::None;
    CHECK(async.complete(third.first, netsec::compute_preauth_proof(key, third.second),
                         &error));
    CHECK(error == ErrorCode::None);
    CHECK(async.complete(first.first, netsec::compute_preauth_proof(key, first.second),
                         &error));
    CHECK(error == ErrorCode::None);
    CHECK(async.complete(second.first, netsec::compute_preauth_proof(key, second.second),
                         &error));
    CHECK(error == ErrorCode::None);
    CHECK(async.channels().size() == 4);
    const Digest512 base_hash = async.identity().preauth_hash();
    for (const netsec::BoundChannel& channel : async.channels()) {
        CHECK(channel.session_binding == async.identity().binding_digest());
        CHECK(channel.security == async.identity().security());
    }
    CHECK(async.identity().preauth_hash() == base_hash);
}

}  // namespace

int main() {
    try {
        test_crypto_vectors_and_zero_start();
        test_channel_hashing_exact_raw_bytes();
        test_multiple_session_setup_rounds();
        test_unoffered_cipher_and_request_duplicate_rejected();
        test_duplicate_context_and_unknown_algorithm();
        test_truncation_and_order_leave_no_partial_state();
        test_second_channel_downgrade_rejected();
        test_identity_binding_covers_security_fields();
        test_guid_replacement_rejected();
        test_raw_byte_rewrite_breaks_hash_and_proof();
        test_reconnect_keeps_original_identity_and_starts_from_zero();
        test_async_failure_and_out_of_order_completion();
        test_legitimate_multichannel_shares_security_out_of_order();
        test_async_multichannel_out_of_order_completion();
    } catch (const std::exception& error) {
        std::cerr << "unexpected exception: " << error.what() << '\n';
        return EXIT_FAILURE;
    }

    if (failures != 0) {
        std::cerr << failures << " of " << checks << " checks failed\n";
        return EXIT_FAILURE;
    }
    std::cout << "all " << checks << " checks passed\n";
    return EXIT_SUCCESS;
}
