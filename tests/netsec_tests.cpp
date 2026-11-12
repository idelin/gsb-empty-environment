#include "netsec.hpp"

#include <cstdlib>
#include <filesystem>
#include <fstream>
#include <iostream>
#include <string>
#include <sys/stat.h>
#include <unistd.h>

namespace fs = std::filesystem;
using namespace netsec;

#define CHECK(condition)                                                     \
    do {                                                                     \
        if (!(condition)) {                                                  \
            std::cerr << "CHECK failed: " #condition << " at " << __FILE__   \
                      << ":" << __LINE__ << '\n';                            \
            return false;                                                    \
        }                                                                    \
    } while (false)

namespace {

FileId file_id(const fs::path& path) {
    struct stat status {};
    if (::stat(path.c_str(), &status) != 0) {
        throw std::runtime_error("stat failed: " + path.string());
    }
    return FileId(static_cast<DeviceNumber>(status.st_dev),
                  static_cast<InodeNumber>(status.st_ino));
}

struct TempTree {
    fs::path root;

    TempTree() {
        const char* tmp = std::getenv("TMPDIR");
        fs::path base = tmp != nullptr ? fs::path(tmp) : fs::current_path();
        root = base / ("netsec-tests-" + std::to_string(::getpid()));
        std::error_code error;
        fs::remove_all(root, error);
        fs::create_directories(root);
    }

    ~TempTree() {
        std::error_code error;
        fs::remove_all(root, error);
    }
};

void write_file(const fs::path& path) {
    std::ofstream output(path);
    output << "protected payload\n";
}

bool test_hardlink_alias_and_path_hint() {
    TempTree tree;
    fs::path original = tree.root / "vault.txt";
    fs::path alias = tree.root / "alias.txt";
    write_file(original);
    fs::create_hard_link(original, alias);

    Policy policy;
    policy.protect(file_id(original), original.string());
    AuditRing audit(8);
    HookEngine guard(policy, audit);

    FileId original_id = file_id(original);
    FileId alias_id = file_id(alias);
    CHECK(original_id == alias_id);

    HookResult by_alias =
        guard.on_open(alias_id, alias.string());
    CHECK(by_alias.denied());
    CHECK(by_alias == Decision::Deny);
    CHECK(by_alias.denying_program == kPolicyProgramName);
    CHECK(by_alias.record.inode == original_id.inode);
    CHECK(by_alias.record.device == original_id.device);
    CHECK(by_alias.record.path == alias.string());
    CHECK(by_alias.record.policy_path == original.string());
    CHECK(by_alias.audit_stored);
    CHECK(audit.size() == 1);
    CHECK(audit.total_attempts() == 1);

    fs::path unrelated = tree.root / "unrelated.txt";
    write_file(unrelated);
    HookResult same_path_other_inode =
        guard.on_open(file_id(unrelated), original.string());
    CHECK(same_path_other_inode.allowed());
    CHECK(policy.allows(file_id(unrelated)));
    CHECK(audit.size() == 2);

    return true;
}

bool test_rename_then_access() {
    TempTree tree;
    fs::path original = tree.root / "before.txt";
    fs::path renamed = tree.root / "after.txt";
    write_file(original);

    Policy policy;
    FileId protected_id = file_id(original);
    policy.protect(protected_id, original.string());
    AuditRing audit(8);
    HookEngine guard(policy, audit);

    fs::rename(original, renamed);
    FileId renamed_id = file_id(renamed);
    CHECK(protected_id == renamed_id);

    HookResult result = guard.on_open(renamed_id, renamed.string());
    CHECK(result.denied());
    CHECK(result.denying_program == kPolicyProgramName);
    CHECK(result.inode == protected_id.inode);
    CHECK(result.record.path == renamed.string());
    CHECK(result.record.policy_path == original.string());
    CHECK(audit.size() == 1);
    CHECK(audit.total_attempts() == 1);

    return true;
}

bool test_program_order_and_short_circuit() {
    Policy policy;
    AuditRing audit(8);
    HookEngine guard(policy, audit);

    int first_calls = 0;
    int second_calls = 0;
    int third_calls = 0;

    guard.add_program("first", [&](const HookContext&) {
        ++first_calls;
        return Decision::Allow;
    });
    guard.add_program("second",
                      [&](const FileId&, const std::string&) {
                          ++second_calls;
                          return true;
                      });
    guard.add_program("third",
                      [&](DeviceNumber, InodeNumber, const std::string&) {
                          ++third_calls;
                          return 0;
                      });

    HookResult result = guard.on_open(
        FileId(11, 22), "/renamed/by/attacker");

    CHECK(result.denied());
    CHECK(result.denying_program == "second");
    CHECK(result.first_denying_program == "second");
    CHECK(result.record.denying_program == "second");
    CHECK(result.record.inode == 22);
    CHECK(result.record.device == 11);
    CHECK(first_calls == 1);
    CHECK(second_calls == 1);
    CHECK(third_calls == 0);
    CHECK(result.executed_programs.size() == 2);
    CHECK(result.executed_programs[0] == "first");
    CHECK(result.executed_programs[1] == "second");
    CHECK(audit.size() == 1);
    CHECK(audit.total_attempts() == 1);
    CHECK(guard.total_audits() == 1);

    return true;
}

bool test_ring_full_keeps_denial_and_reports_loss() {
    Policy policy;
    FileId protected_id(7, 99);
    policy.protect(protected_id, "/var/lib/vault");

    AuditRing audit(2);
    HookEngine guard(policy, audit);

    for (int index = 0; index < 4; ++index) {
        HookResult result = guard.on_open(
            protected_id, "/bind/mount/alias-" + std::to_string(index));
        CHECK(result.denied());
        CHECK(result.denying_program == kPolicyProgramName);
        CHECK(result.record.inode == protected_id.inode);
    }

    CHECK(audit.size() == 2);
    CHECK(audit.dropped() == 2);
    CHECK(audit.pending_dropped() == 2);
    CHECK(audit.total_dropped() == 2);
    CHECK(audit.total_attempts() == 4);

    auto oldest = audit.pop();
    CHECK(oldest.has_value());
    CHECK(oldest->decision == Decision::Deny);
    CHECK(oldest->inode == protected_id.inode);
    CHECK(oldest->path == "/bind/mount/alias-0");

    HookResult next = guard.on_open(protected_id, "/bind/mount/alias-4");
    CHECK(next.denied());
    CHECK(next.audit_stored);
    CHECK(next.record.dropped == 2);
    CHECK(next.record.lost == 2);
    CHECK(audit.dropped() == 0);
    CHECK(audit.total_dropped() == 2);
    CHECK(audit.total_attempts() == 5);
    CHECK(audit.back().dropped == 2);
    CHECK(audit.back().path == "/bind/mount/alias-4");

    return true;
}

bool test_audit_one_record_per_call_and_exception_is_deny() {
    Policy policy;
    AuditRing audit(1);
    HookEngine guard(policy, audit);
    int logger_calls = 0;
    int logger_wrong_decisions = 0;
    guard.set_audit_logger(
        [&](const AuditRecord& record) {
            ++logger_calls;
            if (record.decision != Decision::Deny) {
                ++logger_wrong_decisions;
            }
        });

    guard.add_program("throws", []() -> Decision {
        throw std::runtime_error("simulated failure");
    });

    for (int index = 0; index < 3; ++index) {
        HookResult result = guard.on_open(20, 30, "/x");
        CHECK(result.denied());
        CHECK(result.callback_error);
        CHECK(result.denying_program == "throws");
    }

    CHECK(guard.calls() == 3);
    CHECK(audit.total_attempts() == 3);
    CHECK(audit.size() == 1);
    CHECK(audit.total_dropped() == 2);
    CHECK(logger_calls == 3);
    CHECK(logger_wrong_decisions == 0);

    return true;
}

bool test_policy_ignores_path_text() {
    Policy policy;
    policy.protect(FileId(3, 33), "/real/path");
    CHECK(policy.decide(3, 33, "/fake/path") == Decision::Deny);
    CHECK(policy.decide(3, 34, "/real/path") == Decision::Allow);
    CHECK(policy.path_hint(FileId(3, 33)).has_value());
    CHECK(policy.path_hint(FileId(3, 34)).has_value() == false);
    return true;
}

}  // namespace

int main() {
    const bool ok = test_hardlink_alias_and_path_hint() &&
                    test_rename_then_access() &&
                    test_program_order_and_short_circuit() &&
                    test_ring_full_keeps_denial_and_reports_loss() &&
                    test_audit_one_record_per_call_and_exception_is_deny() &&
                    test_policy_ignores_path_text();

    if (!ok) {
        return 1;
    }
    std::cout << "all netsec tests passed\n";
    return 0;
}
