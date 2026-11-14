#include "netsec.hpp"

#include <array>
#include <cstdint>
#include <cstring>
#include <functional>
#include <iostream>
#include <string>
#include <string_view>
#include <vector>

namespace {

int failures = 0;

void check(bool condition, std::string_view description) {
    if (!condition) {
        ++failures;
        std::cerr << "FAIL: " << description << '\n';
    }
}

void expect_error(netsec::ErrorCode expected, std::string_view description,
                  const std::function<void()>& operation) {
    try {
        operation();
    } catch (const netsec::ProtectionError& error) {
        if (error.code() != expected) {
            ++failures;
            std::cerr << "FAIL: " << description << " (expected "
                      << netsec::to_string(expected) << ", got "
                      << netsec::to_string(error.code()) << ")\n";
        }
        return;
    }
    ++failures;
    std::cerr << "FAIL: " << description << " (no exception; expected "
              << netsec::to_string(expected) << ")\n";
}

netsec::Subject alice() {
    return {"S-1-5-21-1111111111-2222222222-3333333333-1001",
            "alice-logon-session-42"};
}

netsec::Subject bob() {
    return {"S-1-5-21-1111111111-2222222222-3333333333-1002",
            "bob-logon-session-77"};
}

netsec::CallerContext caller_for(const netsec::Subject& subject) {
    return {subject, netsec::ProtectionScope::User, false, false};
}

netsec::CallerContext machine_caller_for(const netsec::Subject& subject) {
    return {subject, netsec::ProtectionScope::Machine, true, true};
}

std::string as_string(std::span<const std::uint8_t> value) {
    return std::string(reinterpret_cast<const char*>(value.data()),
                       value.size());
}

void test_legitimate_user_roundtrip() {
    netsec::ProtectionContext context;
    const auto caller = caller_for(alice());
    const std::string secret = "correct horse battery staple";
    const auto envelope = context.protect(caller, secret);
    const auto second_envelope = context.protect(caller, secret);
    check(envelope != second_envelope,
          "each protection uses fresh salt/nonce/revocation state");
    const auto opened = context.unprotect(caller, envelope);

    check(as_string(opened) == secret, "legitimate user roundtrip");
    check(context.inspect(envelope).scope == netsec::ProtectionScope::User,
          "user envelope carries user scope");
    check(context.inspect(envelope).subject == alice(),
          "envelope binds owner identity");
    check(netsec::ProtectionContext::revocation_id_hex(
              context.inspect(envelope).revocation_id)
                  .size() == 32,
          "revocation id is exposed as hex");
    check(netsec::ProtectionContext::revocation_id_from_hex(
              netsec::ProtectionContext::revocation_id_hex(
                  context.inspect(envelope).revocation_id)) ==
              context.inspect(envelope).revocation_id,
          "revocation id hex roundtrips for persistence");
}

void test_cross_user_decryption_is_rejected() {
    netsec::ProtectionContext context;
    const auto alice_caller = caller_for(alice());
    const auto envelope = context.protect(alice_caller, "alice-only-secret");
    const auto bob_caller = caller_for(bob());

    expect_error(netsec::ErrorCode::IdentityMismatch,
                 "another account cannot decrypt",
                 [&] { context.unprotect(bob_caller, envelope); });
}

void test_cross_session_decryption_is_rejected() {
    netsec::ProtectionContext context;
    const auto alice_caller = caller_for(alice());
    const auto envelope = context.protect(alice_caller, "session-bound-secret");

    auto same_sid_other_session = alice_caller;
    same_sid_other_session.subject.logon_session = "alice-logon-session-99";
    expect_error(netsec::ErrorCode::SessionMismatch,
                 "same SID in another logon session cannot decrypt",
                 [&] {
                     context.unprotect(same_sid_other_session, envelope);
                 });
}

void test_scope_escalation_is_rejected() {
    netsec::ProtectionContext context;

    const auto user_caller = caller_for(alice());
    const auto user_envelope = context.protect(user_caller, "user-secret");

    auto escalated = user_caller;
    escalated.requested_scope = netsec::ProtectionScope::Machine;
    escalated.is_service_account = true;
    escalated.machine_scope_explicitly_declared = true;
    expect_error(netsec::ErrorCode::ScopeMismatch,
                 "user envelope cannot be opened through machine scope",
                 [&] { context.unprotect(escalated, user_envelope); });

    const auto service_caller = machine_caller_for(alice());
    const auto machine_envelope =
        context.protect(service_caller, "machine-secret");
    expect_error(netsec::ErrorCode::IdentityMismatch,
                 "another service identity cannot open machine scope",
                 [&] {
                     context.unprotect(machine_caller_for(bob()),
                                       machine_envelope);
                 });

    auto downgraded = service_caller;
    downgraded.requested_scope = netsec::ProtectionScope::User;
    expect_error(netsec::ErrorCode::ScopeMismatch,
                 "machine envelope cannot be opened through user scope",
                 [&] { context.unprotect(downgraded, machine_envelope); });

    auto non_service = service_caller;
    non_service.is_service_account = false;
    expect_error(netsec::ErrorCode::MachineScopeNotAllowed,
                 "non-service account cannot claim machine scope",
                 [&] { context.unprotect(non_service, machine_envelope); });
}

void test_machine_scope_requires_explicit_service_declaration() {
    netsec::ProtectionContext context;
    const auto service = machine_caller_for(alice());

    auto not_explicit = service;
    not_explicit.machine_scope_explicitly_declared = false;
    expect_error(netsec::ErrorCode::MachineScopeNotAllowed,
                 "machine scope requires explicit declaration at protect",
                 [&] { context.protect(not_explicit, "secret"); });

    const auto envelope = context.protect(service, "machine-only-secret");
    const auto opened = context.unprotect(service, envelope);
    check(as_string(opened) == "machine-only-secret",
          "declared service account can use machine scope");

    auto user_claiming_machine = service;
    user_claiming_machine.is_service_account = false;
    expect_error(netsec::ErrorCode::MachineScopeNotAllowed,
                 "ordinary user cannot open machine-scope envelope",
                 [&] { context.unprotect(user_claiming_machine, envelope); });
}

void test_entropy_substitution_is_rejected() {
    netsec::ProtectionContext context;
    const auto caller = caller_for(alice());
    const std::array<std::uint8_t, 6> entropy{{'s', 'a', 'l', 't', '0', '1'}};
    const std::array<std::uint8_t, 6> wrong_entropy{
        {'s', 'a', 'l', 't', '0', '2'}};
    const std::span<const std::uint8_t> entropy_span(entropy);

    const auto envelope = context.protect(caller, "entropy-bound-secret",
                                           entropy_span);
    check(context.inspect(envelope).has_entropy,
          "entropy flag is bound in the envelope");
    check(as_string(context.unprotect(caller, envelope, entropy_span)) ==
              "entropy-bound-secret",
          "correct optional entropy opens the envelope");

    expect_error(netsec::ErrorCode::EntropyMismatch,
                 "substituted entropy cannot decrypt",
                 [&] {
                     context.unprotect(
                         caller, envelope,
                         std::span<const std::uint8_t>(wrong_entropy));
                 });
    expect_error(netsec::ErrorCode::EntropyMismatch,
                 "missing entropy cannot decrypt an entropy-bound envelope",
                 [&] { context.unprotect(caller, envelope); });

    const std::string text_entropy = "text-entropy-v2";
    const auto text_envelope =
        context.protect(caller, "text-entropy-secret", text_entropy);
    check(as_string(context.unprotect(caller, text_envelope, text_entropy)) ==
              "text-entropy-secret",
          "string entropy is accepted through the convenience API");

    const auto literal_envelope =
        context.protect(caller, "literal-entropy-secret", "literal-entropy");
    check(as_string(context.unprotect(caller, literal_envelope,
                                      "literal-entropy")) ==
              "literal-entropy-secret",
          "string literal entropy is accepted through the convenience API");
}

void test_key_rotation_and_revocation() {
    netsec::ProtectionContext context;
    const auto caller = caller_for(alice());

    const auto old_envelope = context.protect(caller, "generation-one");
    check(context.inspect(old_envelope).key_generation == 1,
          "initial envelope uses generation one");

    const std::uint32_t next_generation = context.rotate();
    check(next_generation == 2, "rotation advances the generation");
    check(context.active_generation() == 2, "rotated generation becomes active");

    const auto new_envelope = context.protect(caller, "generation-two");
    check(context.inspect(new_envelope).key_generation == 2,
          "new envelope uses the active generation");

    check(as_string(context.unprotect(caller, old_envelope)) ==
              "generation-one",
          "old generation remains readable after rotation");
    check(as_string(context.unprotect(caller, new_envelope)) ==
              "generation-two",
          "new generation decrypts");

    context.revoke_generation(1);
    expect_error(netsec::ErrorCode::RevokedKeyGeneration,
                 "revoked old generation can no longer decrypt",
                 [&] { context.unprotect(caller, old_envelope); });
    check(as_string(context.unprotect(caller, new_envelope)) ==
              "generation-two",
          "revoking an old generation does not affect the active one");
}

void test_imported_keys_are_protection_scoped() {
    netsec::Key imported{};
    for (std::size_t i = 0; i < imported.size(); ++i) {
        imported[i] = static_cast<std::uint8_t>(0x40 + i);
    }
    netsec::ProtectionContext writer(imported, 7, true);
    netsec::ProtectionContext reader(imported, 7, true);

    const auto caller = caller_for(alice());
    const auto envelope = writer.protect(caller, "imported-key-secret");
    check(as_string(reader.unprotect(caller, envelope)) ==
              "imported-key-secret",
          "a second context can import and use the same key generation");

    netsec::Key wrong = imported;
    wrong[0] ^= 0xff;
    netsec::ProtectionContext impostor(wrong, 7, true);
    expect_error(netsec::ErrorCode::AuthenticationFailed,
                 "a different imported key cannot decrypt",
                 [&] { impostor.unprotect(caller, envelope); });
}

void test_envelope_revocation() {
    netsec::ProtectionContext context;
    const auto caller = caller_for(alice());
    const auto envelope = context.protect(caller, "revocable-secret");
    const auto info = context.inspect(envelope);

    context.revoke_envelope(info.revocation_id);
    check(context.is_envelope_revoked(info.revocation_id),
          "envelope revocation state is visible");
    expect_error(netsec::ErrorCode::RevokedEnvelope,
                 "revoked envelope cannot decrypt",
                 [&] { context.unprotect(caller, envelope); });
}

void test_header_and_ciphertext_tampering_fails() {
    netsec::ProtectionContext context;
    const auto caller = caller_for(alice());
    const auto envelope = context.protect(caller, "tamper-resistant-secret");

    auto bad_version = envelope;
    bad_version[4] = 0;
    bad_version[5] = 2;
    expect_error(netsec::ErrorCode::UnsupportedVersion,
                 "unknown envelope version is rejected",
                 [&] { context.unprotect(caller, bad_version); });

    auto bad_sid = envelope;
    bad_sid[74] ^= 0x01;
    expect_error(netsec::ErrorCode::AuthenticationFailed,
                 "tampered owner SID fails authentication",
                 [&] { context.unprotect(caller, bad_sid); });

    auto bad_ciphertext = envelope;
    bad_ciphertext[bad_ciphertext.size() - 1 - 32] ^= 0x01;
    expect_error(netsec::ErrorCode::AuthenticationFailed,
                 "tampered ciphertext fails authentication",
                 [&] { context.unprotect(caller, bad_ciphertext); });

    auto bad_revocation = envelope;
    bad_revocation[12] ^= 0x01;
    expect_error(netsec::ErrorCode::AuthenticationFailed,
                 "tampered revocation ID fails authentication",
                 [&] { context.unprotect(caller, bad_revocation); });

    auto unknown_generation = envelope;
    unknown_generation[8] = 0;
    unknown_generation[9] = 0;
    unknown_generation[10] = 0;
    unknown_generation[11] = 99;
    expect_error(netsec::ErrorCode::UnknownKeyGeneration,
                 "unknown key generation is rejected",
                 [&] { context.unprotect(caller, unknown_generation); });
}

void test_service_can_seal_but_not_open_for_a_user() {
    netsec::ProtectionContext context;
    const auto bob_caller = caller_for(bob());
    expect_error(netsec::ErrorCode::IdentityMismatch,
                 "caller cannot seal a secret for another account",
                 [&] {
                     context.protect(bob_caller, alice(),
                                     netsec::ProtectionScope::User, "secret");
                 });

    auto service = caller_for(bob());
    service.is_service_account = true;
    const auto envelope = context.protect(
        service, alice(), netsec::ProtectionScope::User, "delegated-secret");
    check(as_string(context.unprotect(caller_for(alice()), envelope)) ==
              "delegated-secret",
          "service may seal material owned by the original user");
    expect_error(netsec::ErrorCode::IdentityMismatch,
                 "the sealing service cannot open the user's material",
                 [&] { context.unprotect(service, envelope); });
}

void test_empty_plaintext_roundtrip() {
    netsec::ProtectionContext context;
    const auto caller = caller_for(alice());
    const std::vector<std::uint8_t> empty;
    const auto envelope = context.protect(caller, empty);
    check(context.unprotect(caller, envelope).empty(),
          "empty plaintext roundtrips");
}

}  // namespace

int main() {
    test_legitimate_user_roundtrip();
    test_cross_user_decryption_is_rejected();
    test_cross_session_decryption_is_rejected();
    test_scope_escalation_is_rejected();
    test_machine_scope_requires_explicit_service_declaration();
    test_entropy_substitution_is_rejected();
    test_key_rotation_and_revocation();
    test_imported_keys_are_protection_scoped();
    test_envelope_revocation();
    test_header_and_ciphertext_tampering_fails();
    test_service_can_seal_but_not_open_for_a_user();
    test_empty_plaintext_roundtrip();

    if (failures != 0) {
        std::cerr << failures << " test(s) failed\n";
        return 1;
    }
    std::cout << "all netsec tests passed\n";
    return 0;
}
