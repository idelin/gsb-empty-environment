#include "netsec.hpp"

#include <cerrno>
#include <cstdlib>
#include <cstring>
#include <fcntl.h>
#include <filesystem>
#include <iostream>
#include <stdexcept>
#include <string>
#include <string_view>
#include <sys/stat.h>
#include <unistd.h>
#include <utility>
#include <vector>

namespace {

namespace fs = std::filesystem;

int failures = 0;

#define CHECK(condition)                                                       \
    do {                                                                       \
        if (!(condition)) {                                                    \
            std::cerr << __FILE__ << ":" << __LINE__                          \
                      << ": check failed: " #condition << '\n';                \
            ++failures;                                                        \
        }                                                                      \
    } while (false)

class TempDirectory {
public:
    TempDirectory() {
        std::string pattern =
            (fs::temp_directory_path() / "netsec-env-XXXXXX").string();
        std::vector<char> buffer(pattern.begin(), pattern.end());
        buffer.push_back('\0');
        char* created = ::mkdtemp(buffer.data());
        if (created == nullptr) {
            throw std::runtime_error("mkdtemp failed: " +
                                     std::string(std::strerror(errno)));
        }
        path_ = created;
        ::chmod(path_.c_str(), 0700);
    }

    TempDirectory(const TempDirectory&) = delete;
    TempDirectory& operator=(const TempDirectory&) = delete;

    ~TempDirectory() {
        std::error_code error;
        fs::remove_all(path_, error);
    }

    [[nodiscard]] const fs::path& path() const noexcept { return path_; }

private:
    fs::path path_;
};

void write_file(const fs::path& path, std::string_view contents,
                mode_t mode = 0600) {
    const int fd =
        ::open(path.c_str(), O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC, 0600);
    if (fd < 0) {
        throw std::runtime_error("open for write failed: " +
                                 std::string(std::strerror(errno)));
    }

    std::size_t written = 0;
    while (written < contents.size()) {
        const ssize_t count =
            ::write(fd, contents.data() + written, contents.size() - written);
        if (count < 0) {
            if (errno == EINTR) {
                continue;
            }
            const std::string message = std::strerror(errno);
            ::close(fd);
            throw std::runtime_error("write failed: " + message);
        }
        written += static_cast<std::size_t>(count);
    }

    if (::fchmod(fd, mode) != 0) {
        const std::string message = std::strerror(errno);
        ::close(fd);
        throw std::runtime_error("fchmod failed: " + message);
    }
    ::close(fd);
}

netsec::EnvironmentPolicy policy_with(
    std::initializer_list<std::string_view> allowed) {
    netsec::EnvironmentPolicy policy;
    policy.trusted_owner = ::geteuid();
    for (const std::string_view name : allowed) {
        policy.allowed_variables.emplace_back(name);
    }
    return policy;
}

template <typename Function>
bool fails_with(Function&& function, netsec::EnvironmentLoadError expected) {
    try {
        std::forward<Function>(function)();
    } catch (const netsec::EnvironmentException& error) {
        return error.code() == expected;
    } catch (...) {
        return false;
    }
    return false;
}

bool is_hex_sha256(std::string_view value) {
    if (value.size() != 64) {
        return false;
    }
    for (const char character : value) {
        if (!((character >= '0' && character <= '9') ||
              (character >= 'a' && character <= 'f'))) {
            return false;
        }
    }
    return true;
}

void test_valid_file_and_stable_generation() {
    TempDirectory temporary;
    const fs::path path = temporary.path() / "service.env";
    write_file(path,
               "# service settings\n"
               "SAFE_VALUE=hello world\n"
               "LOG_LEVEL = debug\n"
               "TZ=UTC\n"
               "EMPTY=\n",
               0640);

    netsec::EnvironmentFileLoader loader(
        path, policy_with({"SAFE_VALUE", "LOG_LEVEL", "TZ", "EMPTY"}));
    const netsec::EnvironmentSnapshot snapshot = loader.load();

    CHECK(snapshot.variables.size() == 4);
    CHECK(snapshot.variables.at("SAFE_VALUE") == "hello world");
    CHECK(snapshot.variables.at("LOG_LEVEL") == "debug");
    CHECK(snapshot.variables.at("TZ") == "UTC");
    CHECK(snapshot.variables.at("EMPTY").empty());
    CHECK(snapshot.generation == 1);
    CHECK(is_hex_sha256(snapshot.sha256));
    CHECK(snapshot.sha256 ==
          "880c1b1de7b4a26e8ec8c156ef67c522058d0de7dc8a3eb674d655495b27b3e4");

    struct stat status {};
    CHECK(::stat(path.c_str(), &status) == 0);
    CHECK(snapshot.inode == static_cast<std::uint64_t>(status.st_ino));
    CHECK(snapshot.device == static_cast<std::uint64_t>(status.st_dev));
    CHECK(snapshot.owner == static_cast<std::uint64_t>(status.st_uid));
    CHECK(snapshot.size == static_cast<std::uint64_t>(status.st_size));

    const netsec::EnvironmentSnapshot unchanged = loader.load();
    CHECK(unchanged.generation == 1);
    CHECK(unchanged.inode == snapshot.inode);
    CHECK(unchanged.sha256 == snapshot.sha256);

    const auto current = loader.current();
    CHECK(current.has_value());
    CHECK(current->generation == 1);
    CHECK(current->variables.at("SAFE_VALUE") == "hello world");
}

void test_empty_file_uses_known_sha256() {
    TempDirectory temporary;
    const fs::path path = temporary.path() / "empty.env";
    write_file(path, "");

    netsec::EnvironmentPolicy policy;
    policy.allowed_variables = {"SAFE_VALUE"};
    netsec::EnvironmentFileLoader loader(path, policy);
    const netsec::EnvironmentSnapshot snapshot = loader.load();

    CHECK(snapshot.variables.empty());
    CHECK(snapshot.sha256 ==
          "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855");
}

void test_writable_file_is_rejected_then_permissions_can_be_fixed() {
    TempDirectory temporary;
    const fs::path path = temporary.path() / "service.env";
    write_file(path, "SAFE_VALUE=trusted\n", 0666);

    netsec::EnvironmentFileLoader loader(
        path, policy_with({"SAFE_VALUE"}));
    CHECK(fails_with([&] { (void)loader.load(); },
                     netsec::EnvironmentLoadError::unsafe_permissions));
    CHECK(!loader.current().has_value());

    CHECK(::chmod(path.c_str(), 0600) == 0);
    const netsec::EnvironmentSnapshot snapshot = loader.load();
    CHECK(snapshot.variables.at("SAFE_VALUE") == "trusted");
    CHECK(snapshot.generation == 1);
}

void test_group_writable_change_does_not_replace_snapshot() {
    TempDirectory temporary;
    const fs::path path = temporary.path() / "service.env";
    write_file(path, "SAFE_VALUE=one\n");

    netsec::EnvironmentFileLoader loader(
        path, policy_with({"SAFE_VALUE"}));
    const auto first = loader.load();
    CHECK(first.generation == 1);

    CHECK(::chmod(path.c_str(), 0620) == 0);
    CHECK(fails_with([&] { (void)loader.load(); },
                     netsec::EnvironmentLoadError::unsafe_permissions));

    const auto still_first = loader.current();
    CHECK(still_first.has_value());
    CHECK(still_first->generation == 1);
    CHECK(still_first->variables.at("SAFE_VALUE") == "one");
}

void test_symlink_paths_are_rejected() {
    TempDirectory temporary;
    const fs::path target = temporary.path() / "target.env";
    const fs::path link = temporary.path() / "link.env";
    write_file(target, "SAFE_VALUE=trusted\n", 0600);
    CHECK(::symlink(target.c_str(), link.c_str()) == 0);

    netsec::EnvironmentFileLoader direct(
        link, policy_with({"SAFE_VALUE"}));
    CHECK(fails_with([&] { (void)direct.load(); },
                     netsec::EnvironmentLoadError::symlink));
    CHECK(!direct.current().has_value());

    const fs::path real_directory = temporary.path() / "real";
    const fs::path linked_directory = temporary.path() / "linked";
    CHECK(::mkdir(real_directory.c_str(), 0700) == 0);
    write_file(real_directory / "inside.env", "SAFE_VALUE=trusted\n", 0600);
    CHECK(::symlink(real_directory.c_str(), linked_directory.c_str()) == 0);

    netsec::EnvironmentFileLoader through_directory(
        linked_directory / "inside.env", policy_with({"SAFE_VALUE"}));
    CHECK(fails_with([&] { (void)through_directory.load(); },
                     netsec::EnvironmentLoadError::symlink));
}

void test_dangerous_variables_are_rejected_even_when_allowlisted() {
    TempDirectory temporary;
    const fs::path path = temporary.path() / "service.env";
    const std::vector<std::string> dangerous = {
        "LD_PRELOAD",
        "LD_LIBRARY_PATH",
        "GCONV_PATH",
        "DYLD_INSERT_LIBRARIES",
        "PATH",
        "PYTHONPATH",
        "BASH_ENV",
        "NODE_OPTIONS",
        "JAVA_TOOL_OPTIONS",
    };

    netsec::EnvironmentPolicy policy;
    policy.trusted_owner = ::geteuid();
    for (const std::string& name : dangerous) {
        policy.allowed_variables.push_back(name);
    }
    policy.allowed_variables.push_back("SAFE_VALUE");
    netsec::EnvironmentFileLoader loader(path, policy);

    for (const std::string& name : dangerous) {
        write_file(path, "SAFE_VALUE=before\n" + name + "=/attacker/payload\n");
        CHECK(fails_with([&] { (void)loader.load(); },
                         netsec::EnvironmentLoadError::forbidden_variable));
        CHECK(!loader.current().has_value());
    }

    write_file(path, "SAFE_VALUE=trusted\n");
    const auto snapshot = loader.load();
    CHECK(snapshot.variables.at("SAFE_VALUE") == "trusted");
    CHECK(snapshot.generation == 1);
}

void test_overlong_line_is_rejected_without_partial_load() {
    TempDirectory temporary;
    const fs::path path = temporary.path() / "service.env";
    netsec::EnvironmentPolicy policy = policy_with({"SAFE_VALUE"});
    policy.max_line_bytes = 32;
    netsec::EnvironmentFileLoader loader(path, policy);

    write_file(path, "SAFE_VALUE=" + std::string(40, 'x') + "\n");
    CHECK(fails_with([&] { (void)loader.load(); },
                     netsec::EnvironmentLoadError::line_too_long));
    CHECK(!loader.current().has_value());

    write_file(path, "SAFE_VALUE=short\n");
    const auto snapshot = loader.load();
    CHECK(snapshot.variables.at("SAFE_VALUE") == "short");
}

void test_failed_reload_keeps_last_trusted_snapshot() {
    TempDirectory temporary;
    const fs::path path = temporary.path() / "service.env";
    write_file(path, "SAFE_VALUE=one\nTZ=UTC\n");

    netsec::EnvironmentFileLoader loader(
        path, policy_with({"SAFE_VALUE", "TZ"}));
    const auto first = loader.load();
    CHECK(first.generation == 1);

    write_file(path,
               "SAFE_VALUE=two\n"
               "LD_PRELOAD=/attacker/payload.so\n"
               "TZ=Asia/Shanghai\n");
    CHECK(fails_with([&] { (void)loader.load(); },
                     netsec::EnvironmentLoadError::forbidden_variable));

    const auto retained = loader.current();
    CHECK(retained.has_value());
    CHECK(retained->generation == 1);
    CHECK(retained->variables.at("SAFE_VALUE") == "one");
    CHECK(retained->variables.at("TZ") == "UTC");
    CHECK(retained->variables.find("LD_PRELOAD") == retained->variables.end());

    write_file(path, "SAFE_VALUE=two\nTZ=Asia/Shanghai\n");
    const auto second = loader.load();
    CHECK(second.generation == 2);
    CHECK(second.variables.at("SAFE_VALUE") == "two");
    CHECK(second.variables.at("TZ") == "Asia/Shanghai");
}

void test_inode_replacement_and_hash_changes_advance_generation() {
    TempDirectory temporary;
    const fs::path path = temporary.path() / "service.env";
    const fs::path replacement = temporary.path() / "replacement.env";
    write_file(path, "SAFE_VALUE=same\n");

    netsec::EnvironmentFileLoader loader(
        path, policy_with({"SAFE_VALUE"}));
    const auto first = loader.load();
    CHECK(first.generation == 1);

    write_file(replacement, "SAFE_VALUE=same\n", 0600);
    CHECK(::rename(replacement.c_str(), path.c_str()) == 0);
    const auto replaced = loader.load();
    CHECK(replaced.generation == 2);
    CHECK(replaced.inode != first.inode);
    CHECK(replaced.sha256 == first.sha256);

    write_file(path, "SAFE_VALUE=changed\n");
    const auto changed = loader.load();
    CHECK(changed.generation == 3);
    CHECK(changed.inode == replaced.inode);
    CHECK(changed.sha256 != replaced.sha256);
    CHECK(changed.variables.at("SAFE_VALUE") == "changed");
}

void test_wrong_owner_and_non_regular_files_are_rejected() {
    TempDirectory temporary;
    const fs::path path = temporary.path() / "service.env";
    write_file(path, "SAFE_VALUE=trusted\n");

    netsec::EnvironmentPolicy wrong_owner = policy_with({"SAFE_VALUE"});
    wrong_owner.trusted_owner = static_cast<uid_t>(::geteuid() + 1);
    netsec::EnvironmentFileLoader owner_loader(path, wrong_owner);
    CHECK(fails_with([&] { (void)owner_loader.load(); },
                     netsec::EnvironmentLoadError::wrong_owner));

    netsec::EnvironmentFileLoader directory_loader(
        temporary.path(), policy_with({"SAFE_VALUE"}));
    CHECK(fails_with([&] { (void)directory_loader.load(); },
                     netsec::EnvironmentLoadError::not_regular_file));
}

void test_syntax_allowlist_and_duplicate_failures() {
    TempDirectory temporary;
    const fs::path path = temporary.path() / "service.env";
    netsec::EnvironmentFileLoader loader(
        path, policy_with({"SAFE_VALUE"}));

    write_file(path, "SAFE_VALUE trusted\n");
    CHECK(fails_with([&] { (void)loader.load(); },
                     netsec::EnvironmentLoadError::invalid_syntax));

    write_file(path, "OTHER_VALUE=trusted\n");
    CHECK(fails_with([&] { (void)loader.load(); },
                     netsec::EnvironmentLoadError::variable_not_allowed));

    write_file(path, "SAFE_VALUE=one\nSAFE_VALUE=two\n");
    CHECK(fails_with([&] { (void)loader.load(); },
                     netsec::EnvironmentLoadError::duplicate_variable));
    CHECK(!loader.current().has_value());
}

void test_file_size_limit() {
    TempDirectory temporary;
    const fs::path path = temporary.path() / "service.env";
    write_file(path, "SAFE_VALUE=trusted\n");

    netsec::EnvironmentPolicy policy = policy_with({"SAFE_VALUE"});
    policy.max_file_bytes = 4;
    netsec::EnvironmentFileLoader loader(path, policy);
    CHECK(fails_with([&] { (void)loader.load(); },
                     netsec::EnvironmentLoadError::file_too_large));
}

} // namespace

int main() {
    const std::vector<std::pair<std::string_view, void (*)()>> tests = {
        {"valid file and stable generation",
         test_valid_file_and_stable_generation},
        {"known empty-file hash", test_empty_file_uses_known_sha256},
        {"writable file and permission repair",
         test_writable_file_is_rejected_then_permissions_can_be_fixed},
        {"group-writable change keeps snapshot",
         test_group_writable_change_does_not_replace_snapshot},
        {"symlink rejection", test_symlink_paths_are_rejected},
        {"dangerous variables",
         test_dangerous_variables_are_rejected_even_when_allowlisted},
        {"overlong line", test_overlong_line_is_rejected_without_partial_load},
        {"failed reload atomicity",
         test_failed_reload_keeps_last_trusted_snapshot},
        {"inode/hash/generation binding",
         test_inode_replacement_and_hash_changes_advance_generation},
        {"owner and regular-file checks",
         test_wrong_owner_and_non_regular_files_are_rejected},
        {"syntax, allowlist and duplicates",
         test_syntax_allowlist_and_duplicate_failures},
        {"file size limit", test_file_size_limit},
    };

    for (const auto& [name, test] : tests) {
        const int before = failures;
        try {
            test();
        } catch (const std::exception& error) {
            std::cerr << name << ": unexpected exception: " << error.what()
                      << '\n';
            ++failures;
        }
        std::cout << (failures == before ? "[PASS] " : "[FAIL] ") << name
                  << '\n';
    }

    if (failures != 0) {
        std::cerr << failures << " check(s) failed\n";
        return 1;
    }
    std::cout << "All environment loader tests passed\n";
    return 0;
}
