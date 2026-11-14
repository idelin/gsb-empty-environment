#include "netsec.hpp"

#include <algorithm>
#include <cstdint>
#include <exception>
#include <iostream>
#include <string>
#include <string_view>
#include <vector>

namespace {

int failures = 0;

const std::string signing_key = "test-signing-key-32-bytes-long!";

void check(bool condition, std::string_view description) {
    if (!condition) {
        std::cerr << "FAIL: " << description << '\n';
        ++failures;
    }
}

void check_failure(const netsec::VerificationResult& result,
                   netsec::VerificationFailure failure,
                   std::size_t record_number,
                   std::string_view description) {
    check(!result.ok, description);
    check(result.failure == failure, description);
    if (record_number != 0) {
        check(result.failed_record == record_number, description);
    }
}

std::string record_text(std::string text) {
    if (!text.empty() && text.back() == '\n') {
        text.pop_back();
    }
    return text;
}

std::string join_with_newline(const std::vector<std::string>& lines) {
    std::string log;
    for (const auto& line : lines) {
        log += line;
        log.push_back('\n');
    }
    return log;
}

bool replace_first(std::string& text, std::string_view needle,
                   std::string_view replacement) {
    const std::size_t position = text.find(needle);
    if (position == std::string::npos) {
        return false;
    }
    text.replace(position, needle.size(), replacement);
    return true;
}

void expect_parse_failure(std::string_view line, std::string_view description) {
    bool threw = false;
    try {
        (void)netsec::parse_record(line);
    } catch (const std::exception&) {
        threw = true;
    }
    check(threw, description);
}

void check_one_physical_line(std::string_view line,
                             std::string_view description) {
    check(!line.empty(), description);
    check(line.back() == '\n', description);
    check(std::count(line.begin(), line.end(), '\n') == 1, description);
    check(std::count(line.begin(), line.end(), '\r') == 0, description);
    check(std::count(line.begin(), line.end(), '\t') == 0, description);
    check(std::count(line.begin(), line.end(), '\0') == 0, description);
    check(line.find("\xc2\x85") == std::string_view::npos, description);
    check(line.find("\xe2\x80\xa8") == std::string_view::npos, description);
    check(line.find("\xe2\x80\xa9") == std::string_view::npos, description);
}

void test_reference_crypto_and_legitimate_events() {
    std::int64_t now = 1'700'000'000'000;
    std::string principal = "srv:alice";
    netsec::AuditLogger logger(signing_key, [&]() {
        return netsec::ServerContext{principal, now};
    });

    const std::string first =
        logger.append({"document.read", "report-42", "success"});
    check_one_physical_line(first, "one legitimate event is one line");

    const netsec::AuditRecord record =
        netsec::parse_record(record_text(first));
    check(record.version == netsec::audit_format_version,
          "format version is fixed");
    check(record.sequence == 1, "sequence starts at one");
    check(record.timestamp_ms == 1'700'000'000'000,
          "timestamp comes from the server context");
    check(record.principal == "srv:alice",
          "principal comes from the server context");
    check(record.action == "document.read", "action is preserved");
    check(record.object == "report-42", "object is preserved");
    check(record.outcome == "success", "outcome is preserved");
    check(record.previous_hash == netsec::audit_genesis_hash,
          "first record points at the genesis hash");

    const std::string expected_payload =
        R"({"version":1,"sequence":1,"timestamp_ms":1700000000000,"principal":"srv:alice","action":"document.read","object":"report-42","outcome":"success","prev_hash":")" +
        std::string(netsec::audit_genesis_hash) + "\"}";
    const std::string expected_hash =
        "99f99b046916e242f4c21773379a59fffc7ff2688827abfbbbe8047aa37c3380";
    const std::string expected_signature =
        "c76e17e81eb68d75502d4fc551ae74a04b6d44788156a48b10aed5ae514910cf";

    check(netsec::canonical_payload(record) == expected_payload,
          "canonical payload has the specified field order");
    check(record.hash == expected_hash,
          "SHA-256 matches the independent reference vector");
    check(record.signature == expected_signature,
          "HMAC-SHA256 matches the independent reference vector");

    now += 7;
    principal = "srv:bob";
    const std::string second =
        logger.append({"document.delete", "report-42", "allowed"});
    now += 7;
    const std::string third =
        logger.append({"document.read", "report-43", "success"});
    check_one_physical_line(second, "second legitimate event is one line");
    check_one_physical_line(third, "third legitimate event is one line");

    const netsec::AuditRecord second_record =
        netsec::parse_record(record_text(second));
    check(second_record.sequence == 2, "second sequence follows the first");
    check(second_record.principal == "srv:bob",
          "principal can change only through server context");
    check(second_record.timestamp_ms == 1'700'000'000'007,
          "timestamp can change only through server context");
    check(second_record.previous_hash == record.hash,
          "second record chains to the first hash");

    const std::string log = first + second + third;
    const auto verification = netsec::verify_log(log, signing_key);
    check(verification.ok, "legitimate log verifies");
    check(verification.records_verified == 3,
          "all legitimate records are verified");
    check(verification.next_sequence == 4, "next sequence is four");
    check(verification.last_hash ==
              netsec::parse_record(record_text(third)).hash,
          "verification returns the final hash");

    check(logger.next_sequence() == 4, "logger tracks the next sequence");
    check(logger.last_hash() == verification.last_hash,
          "logger and verifier agree on the chain head");
}

void test_newline_and_control_injection() {
    std::int64_t now = 1'700'000'001'000;
    std::string principal = "alice\n2026-01-01T00:00:00Z admin";
    netsec::AuditLogger logger(signing_key, [&]() {
        return netsec::ServerContext{principal, now};
    });

    std::string dangerous =
        "report.txt\n"
        "{\"version\":1,\"sequence\":42,\"timestamp_ms\":0,"
        "\"principal\":\"root\",\"action\":\"delete\","
        "\"object\":\"all\",\"outcome\":\"success\","
        "\"prev_hash\":\"000000000000000000000000000000000000000000000000"
        "0000000000000000\",\"hash\":\"00000000000000000000000000000000"
        "00000000000000000000000000000000\",\"signature\":\"000000000000"
        "0000000000000000000000000000000000000000000000000000\"}\n"
        "2026-01-01T00:00:00Z admin deleted all\n";
    dangerous.push_back('\t');
    dangerous.push_back('\r');
    dangerous.push_back('\0');
    dangerous.push_back('\x1f');
    dangerous.push_back('\x7f');
    dangerous += "\xc2\x85";
    dangerous += "\xe2\x80\xa8";
    dangerous += " quote\" backslash\\ braces{}";

    const std::string injected =
        logger.append({"document.delete", dangerous, "denied"});
    check_one_physical_line(injected,
                            "newlines and controls remain inside one field");
    check(injected.find("\\n") != std::string::npos,
          "newline is escaped in the encoded field");
    check(injected.find("\\t") != std::string::npos,
          "tab is escaped in the encoded field");
    check(injected.find("\\u0000") != std::string::npos,
          "NUL is escaped in the encoded field");
    check(injected.find("\\u001f") != std::string::npos,
          "C0 control is escaped in the encoded field");
    check(injected.find("\\u007f") != std::string::npos,
          "DEL is escaped in the encoded field");
    check(injected.find("\\u0085") != std::string::npos,
          "C1 control is escaped in the encoded field");
    check(injected.find("\\u2028") != std::string::npos,
          "Unicode line separator is escaped in the encoded field");

    const netsec::AuditRecord record =
        netsec::parse_record(record_text(injected));
    check(record.principal == principal,
          "malicious principal text cannot become a new record");
    check(record.object == dangerous,
          "malicious object text round-trips without splitting the record");
    check(record.sequence == 1, "injected sequence text cannot override seq");
    check(record.timestamp_ms == now,
          "injected timestamp text cannot override trusted time");

    now += 1;
    principal = "srv:carol";
    const std::string legitimate =
        logger.append({"document.read", "report.txt", "success"});
    check_one_physical_line(legitimate, "following event remains separate");
    const std::string injected_log = injected + legitimate;
    check(std::count(injected_log.begin(), injected_log.end(), '\n') == 2,
          "two events produce exactly two physical newlines");

    const auto verification =
        netsec::verify_log(injected_log, signing_key);
    check(verification.ok, "log with escaped hostile values verifies");
    check(verification.records_verified == 2,
          "injection did not add a record");
}

void test_forged_timestamp_and_field_override() {
    std::int64_t now = 1'700'000'002'000;
    netsec::AuditLogger logger(signing_key, [&]() {
        return netsec::ServerContext{"srv:alice", now};
    });

    const std::string line =
        logger.append({"document.read", "report.txt", "success"});
    std::string text = record_text(line);
    const std::size_t timestamp_position = text.find("\"timestamp_ms\":");
    check(timestamp_position != std::string::npos,
          "timestamp field is present");
    const std::size_t digits_position = timestamp_position + 15;
    const std::size_t digits_end = text.find(',', digits_position);
    check(digits_end != std::string::npos, "timestamp has a delimiter");
    check(digits_end - digits_position == 13,
          "reference timestamp has the expected width");
    std::string forged_timestamp = text;
    forged_timestamp.replace(digits_position, digits_end - digits_position,
                             "1800000000000");
    const auto timestamp_result = netsec::verify_log(
        forged_timestamp + "\n", signing_key);
    check_failure(timestamp_result,
                  netsec::VerificationFailure::record_hash_mismatch, 1,
                  "forged timestamp breaks the record hash");

    std::string forged_signature = text;
    const std::size_t signature_position =
        forged_signature.rfind("\"signature\":\"");
    check(signature_position != std::string::npos,
          "signature field is present");
    forged_signature.replace(signature_position + 13, 64, std::string(64, '0'));
    const auto signature_result = netsec::verify_log(
        forged_signature + "\n", signing_key);
    check_failure(signature_result,
                  netsec::VerificationFailure::signature_mismatch, 1,
                  "forged signature is rejected");

    const std::string override_payload =
        "x\",\"sequence\":999,\"principal\":\"root\","
        "\"timestamp_ms\":0,\"prev_hash\":\"" +
        std::string(64, '0') + "\",\"hash\":\"" + std::string(64, '0') +
        "\",\"signature\":\"" + std::string(64, '0') + "\"},";
    now += 1;
    const std::string override_line =
        logger.append({"document.write", override_payload, "success"});
    check_one_physical_line(override_line,
                            "field override payload stays in one string");
    const netsec::AuditRecord record =
        netsec::parse_record(record_text(override_line));
    check(record.object == override_payload,
          "field override text is data, not schema");
    check(record.sequence == 2, "field override cannot change sequence");
    check(record.principal == "srv:alice",
          "field override cannot change principal");
    check(record.timestamp_ms == now,
          "field override cannot change timestamp");

    std::string duplicate_field = text;
    duplicate_field.insert(duplicate_field.size() - 1,
                           R"(,"principal":"root")");
    expect_parse_failure(duplicate_field,
                         "duplicate top-level field is rejected");
    const auto duplicate_result = netsec::verify_log(
        duplicate_field + "\n", signing_key);
    check_failure(duplicate_result,
                  netsec::VerificationFailure::malformed_record, 1,
                  "duplicate field cannot be accepted by the verifier");

    std::string extra_field = text;
    extra_field.insert(extra_field.size() - 1,
                       R"(,"admin":true)");
    expect_parse_failure(extra_field, "extra top-level field is rejected");

    const auto log =
        netsec::verify_log(text + "\n" + override_line, signing_key);
    check(log.ok, "legitimate event following injection still verifies");
}

void test_chain_verification_and_strict_canonical_form() {
    std::int64_t now = 1'700'000'003'000;
    netsec::AuditLogger logger(signing_key, [&]() {
        return netsec::ServerContext{"srv:alice", now};
    });

    std::vector<std::string> lines{
        record_text(logger.append({"a", "one", "success"}))};
    now += 1;
    lines.push_back(record_text(logger.append({"b", "two", "success"})));
    now += 1;
    lines.push_back(record_text(logger.append({"c", "three", "success"})));
    const std::string log = join_with_newline(lines);

    const auto whole = netsec::verify_log(log, signing_key);
    check(whole.ok, "three-record chain verifies");
    check(whole.records_verified == 3, "three records were checked");

    const auto removed = netsec::verify_log(
        lines[0] + "\n" + lines[2] + "\n", signing_key);
    check_failure(removed, netsec::VerificationFailure::sequence_mismatch, 2,
                  "removing a middle record is detected");

    const auto inserted = netsec::verify_log(
        lines[0] + "\n" + lines[0] + "\n" + lines[2] + "\n", signing_key);
    check_failure(inserted, netsec::VerificationFailure::sequence_mismatch, 2,
                  "inserting a repeated record is detected");

    const auto reordered =
        netsec::verify_log(lines[1] + "\n" + lines[0] + "\n" + lines[2] + "\n",
                           signing_key);
    check_failure(reordered, netsec::VerificationFailure::sequence_mismatch, 1,
                  "reordering records is detected");

    std::string bad_previous = lines[1];
    const std::size_t previous_position =
        bad_previous.find("\"prev_hash\":\"");
    check(previous_position != std::string::npos,
          "previous hash field is present");
    bad_previous.replace(previous_position + 13, 64, std::string(64, '0'));
    const auto bad_link = netsec::verify_log(
        lines[0] + "\n" + bad_previous + "\n" + lines[2] + "\n", signing_key);
    check_failure(bad_link,
                  netsec::VerificationFailure::previous_hash_mismatch, 2,
                  "broken previous-hash link is detected");

    std::string bad_hash = lines[1];
    const std::size_t hash_position = bad_hash.find("\"hash\":\"");
    check(hash_position != std::string::npos, "hash field is present");
    bad_hash.replace(hash_position + 8, 64, std::string(64, '0'));
    const auto bad_record_hash = netsec::verify_log(
        lines[0] + "\n" + bad_hash + "\n" + lines[2] + "\n", signing_key);
    check_failure(bad_record_hash,
                  netsec::VerificationFailure::record_hash_mismatch, 2,
                  "tampered record hash is detected");

    const auto wrong_key = netsec::verify_log(log, "different-server-key");
    check_failure(wrong_key, netsec::VerificationFailure::signature_mismatch, 1,
                  "wrong signing key is rejected");

    const auto blank_line = netsec::verify_log(
        lines[0] + "\n\n" + lines[1] + "\n", signing_key);
    check_failure(blank_line, netsec::VerificationFailure::malformed_record, 2,
                  "blank physical line is rejected");

    const auto empty = netsec::verify_log("", signing_key);
    check_failure(empty, netsec::VerificationFailure::empty_log, 0,
                  "empty log is rejected");

    netsec::AuditRecord third = netsec::parse_record(lines[2]);
    now += 1;
    netsec::AuditLogger resumed(
        signing_key,
        [&]() { return netsec::ServerContext{"srv:alice", now}; }, 4,
        third.hash);
    const std::string fourth = resumed.append({"d", "four", "success"});
    const std::string extended = log + fourth;
    const auto extended_result = netsec::verify_log(extended, signing_key);
    check(extended_result.ok, "resumed logger extends the same chain");
    check(extended_result.records_verified == 4,
          "resumed logger preserves chain length");

    const auto segment = netsec::verify_log(fourth, signing_key, 4, third.hash);
    check(segment.ok, "chain segment verifies with explicit sequence and head");
    check(segment.records_verified == 1, "segment verified one record");

    const auto segment_without_anchor =
        netsec::verify_log(fourth, signing_key, 4);
    check_failure(segment_without_anchor,
                  netsec::VerificationFailure::previous_hash_mismatch, 1,
                  "segment without the correct chain-head anchor is rejected");

    std::string noncanonical = lines[0];
    check(replace_first(noncanonical, "\"success\"", "\"\\u0073uccess\""),
          "reference outcome was found");
    expect_parse_failure(noncanonical,
                         "non-canonical Unicode escape is rejected");

    std::string leading_space = lines[0];
    leading_space.insert(leading_space.begin(), ' ');
    expect_parse_failure(leading_space, "leading whitespace is rejected");

    std::string trailing_data = lines[0];
    trailing_data += "x";
    expect_parse_failure(trailing_data,
                         "trailing data after a record is rejected");

    std::string uppercase_hash = lines[0];
    const std::size_t uppercase_position =
        uppercase_hash.find("\"hash\":\"");
    uppercase_hash.replace(uppercase_position + 8, 1, "A");
    expect_parse_failure(uppercase_hash,
                         "non-lowercase hash encoding is rejected");
}

}  // namespace

int main() {
    test_reference_crypto_and_legitimate_events();
    test_newline_and_control_injection();
    test_forged_timestamp_and_field_override();
    test_chain_verification_and_strict_canonical_form();

    if (failures != 0) {
        std::cerr << failures << " test assertion(s) failed\n";
        return 1;
    }
    std::cout << "all audit log security tests passed\n";
    return 0;
}
