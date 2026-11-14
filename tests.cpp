#include "netsec.hpp"

#include <atomic>
#include <chrono>
#include <filesystem>
#include <fstream>
#include <iostream>
#include <string>
#include <string_view>
#include <vector>

#include <sys/stat.h>
#include <unistd.h>

namespace fs = std::filesystem;
using netsec::ArchiveEntry;
using netsec::EntryType;
using netsec::ErrorCode;
using netsec::ExtractOptions;
using netsec::LinkPolicy;

namespace {

int g_checks = 0;
int g_failures = 0;

void check(bool condition, std::string_view expression, int line) {
    ++g_checks;
    if (!condition) {
        ++g_failures;
        std::cerr << "FAIL line " << line << ": " << expression << '\n';
    }
}

#define CHECK(expression) check((expression), #expression, __LINE__)

std::string read_file(const fs::path& path) {
    std::ifstream input(path, std::ios::binary);
    return std::string(std::istreambuf_iterator<char>(input),
                       std::istreambuf_iterator<char>());
}

void write_file(const fs::path& path, std::string_view data) {
    std::ofstream output(path, std::ios::binary | std::ios::trunc);
    output.write(data.data(), static_cast<std::streamsize>(data.size()));
}

bool has_temp_files(const fs::path& root) {
    std::error_code ec;
    if (!fs::exists(root, ec)) {
        return false;
    }
    for (fs::recursive_directory_iterator it(root, ec), end; it != end;
         it.increment(ec)) {
        if (ec) {
            return true;
        }
        if (it->path().filename().string().starts_with(".netsec-tmp-")) {
            return true;
        }
    }
    return false;
}

mode_t mode_of(const fs::path& path) {
    struct stat st {};
    if (::lstat(path.c_str(), &st) != 0) {
        return 0;
    }
    return st.st_mode & 07777;
}

ino_t inode_of(const fs::path& path) {
    struct stat st {};
    if (::lstat(path.c_str(), &st) != 0) {
        return 0;
    }
    return st.st_ino;
}

ArchiveEntry regular(std::string path, std::string data,
                     std::optional<fs::perms> permissions = std::nullopt) {
    ArchiveEntry entry;
    entry.path = std::move(path);
    entry.type = EntryType::RegularFile;
    entry.data = std::move(data);
    entry.declared_size = entry.data.size();
    entry.permissions = permissions;
    return entry;
}

ArchiveEntry directory(std::string path,
                       std::optional<fs::perms> permissions = std::nullopt) {
    ArchiveEntry entry;
    entry.path = std::move(path);
    entry.type = EntryType::Directory;
    entry.permissions = permissions;
    return entry;
}

ArchiveEntry hard_link(std::string path, std::string target,
                       std::optional<fs::perms> permissions = std::nullopt) {
    ArchiveEntry entry;
    entry.path = std::move(path);
    entry.type = EntryType::HardLink;
    entry.link_target = std::move(target);
    entry.permissions = permissions;
    return entry;
}

ArchiveEntry symbolic_link(std::string path, std::string target) {
    ArchiveEntry entry;
    entry.path = std::move(path);
    entry.type = EntryType::SymbolicLink;
    entry.link_target = std::move(target);
    return entry;
}

class TempTree {
public:
    TempTree() {
        static std::atomic<unsigned long> counter{0};
        const auto stamp = std::chrono::steady_clock::now()
                               .time_since_epoch()
                               .count();
        root_ = fs::temp_directory_path() /
                ("netsec-test-" + std::to_string(::getpid()) + "-" +
                 std::to_string(stamp) + "-" +
                 std::to_string(counter.fetch_add(1)));
        fs::create_directories(root_);
    }

    ~TempTree() {
        std::error_code ec;
        fs::remove_all(root_, ec);
    }

    TempTree(const TempTree&) = delete;
    TempTree& operator=(const TempTree&) = delete;

    const fs::path& root() const { return root_; }

private:
    fs::path root_;
};

void test_hard_link_escape_rejected() {
    TempTree tree;
    const fs::path destination = tree.root() / "extract";
    fs::create_directories(destination);
    write_file(tree.root() / "outside-secret", "do not touch");

    ExtractOptions options;
    options.hard_links = LinkPolicy::AllowWithinArchive;
    netsec::ArchiveExtractor extractor(options);

    const auto absolute = extractor.extract(
        destination,
        {regular("inside.txt", "inside"),
         hard_link("escape", "/etc/passwd")});
    CHECK(!absolute);
    CHECK(absolute.error.code == ErrorCode::LinkTargetOutsideArchive);
    CHECK(!fs::exists(destination / "escape"));

    const auto relative_escape = extractor.extract(
        destination,
        {regular("inside.txt", "inside"),
         hard_link("escape", "../../outside-secret")});
    CHECK(!relative_escape);
    CHECK(relative_escape.error.code == ErrorCode::LinkTargetOutsideArchive);
    CHECK(!fs::exists(destination / "escape"));

    const auto missing_member = extractor.extract(
        destination,
        {regular("inside.txt", "inside"),
         hard_link("escape", "not-in-archive")});
    CHECK(!missing_member);
    CHECK(missing_member.error.code == ErrorCode::LinkTargetMissingFromArchive);
    CHECK(!fs::exists(destination / "escape"));
}

void test_link_extraction_order() {
    TempTree tree;
    const fs::path destination = tree.root() / "extract";

    ExtractOptions options;
    options.hard_links = LinkPolicy::AllowWithinArchive;
    netsec::ArchiveExtractor extractor(options);

    const auto hard = extractor.extract(
        destination,
        {hard_link("first", "target.txt"),
         regular("target.txt", "target")});
    CHECK(!hard);
    CHECK(hard.error.code == ErrorCode::LinkTargetNotExtracted);
    CHECK(!fs::exists(destination / "first"));
    CHECK(!fs::exists(destination / "target.txt"));

    options.symbolic_links = LinkPolicy::AllowWithinArchive;
    netsec::ArchiveExtractor symlink_extractor(options);
    const auto symbolic = symlink_extractor.extract(
        destination,
        {symbolic_link("first-link", "target.txt"),
         regular("target.txt", "target")});
    CHECK(!symbolic);
    CHECK(symbolic.error.code == ErrorCode::LinkTargetNotExtracted);
    CHECK(!fs::exists(destination / "first-link"));
}

void test_symbolic_link_policy_and_containment() {
    TempTree tree;
    const fs::path destination = tree.root() / "extract";
    fs::create_directories(destination);
    write_file(tree.root() / "outside-secret", "secret");

    ExtractOptions reject_options;
    netsec::ArchiveExtractor rejecting(reject_options);
    const auto rejected = rejecting.extract(
        destination,
        {regular("real.txt", "real"),
         symbolic_link("link", "real.txt")});
    CHECK(!rejected);
    CHECK(rejected.error.code == ErrorCode::SymbolicLinkRejected);
    CHECK(!fs::exists(destination / "link"));

    ExtractOptions allow_options;
    allow_options.symbolic_links = LinkPolicy::AllowWithinArchive;
    netsec::ArchiveExtractor allowing(allow_options);

    const auto legal = allowing.extract(
        destination,
        {regular("real.txt", "real"),
         symbolic_link("link", "real.txt")});
    CHECK(legal.ok());
    CHECK(fs::is_symlink(destination / "link"));
    CHECK(fs::read_symlink(destination / "link") == fs::path("real.txt"));

    const auto absolute = allowing.extract(
        destination,
        {regular("real.txt", "real"),
         symbolic_link("absolute", "/etc/passwd")});
    CHECK(!absolute);
    CHECK(absolute.error.code == ErrorCode::LinkTargetOutsideArchive);
    CHECK(!fs::exists(destination / "absolute"));

    const auto relative_escape = allowing.extract(
        destination,
        {regular("real.txt", "real"),
         symbolic_link("nested/escape", "../../outside-secret")});
    CHECK(!relative_escape);
    CHECK(relative_escape.error.code == ErrorCode::LinkTargetOutsideArchive);
    CHECK(!fs::exists(destination / "nested/escape"));
}

void test_existing_symlink_parent_cannot_redirect_write() {
    TempTree tree;
    const fs::path destination = tree.root() / "extract";
    const fs::path outside = tree.root() / "outside";
    fs::create_directories(destination);
    fs::create_directories(outside);
    fs::create_directory_symlink(outside, destination / "redirect");

    const auto result = netsec::extract_archive(
        destination, {regular("redirect/pwned.txt", "escaped")});
    CHECK(!result);
    CHECK(result.error.code == ErrorCode::SymlinkInPath);
    CHECK(!fs::exists(outside / "pwned.txt"));
    CHECK(!has_temp_files(destination));
}

void test_existing_leaf_links_are_replaced_not_followed() {
    TempTree tree;
    const fs::path destination = tree.root() / "extract";
    fs::create_directories(destination);
    write_file(tree.root() / "outside-secret", "outside");

    fs::create_symlink(tree.root() / "outside-secret", destination / "victim");
    const auto via_symlink =
        netsec::extract_archive(destination,
                                {regular("victim", "replacement")});
    CHECK(via_symlink.ok());
    CHECK(!fs::is_symlink(destination / "victim"));
    CHECK(read_file(destination / "victim") == "replacement");
    CHECK(read_file(tree.root() / "outside-secret") == "outside");

    fs::remove(destination / "victim");
    fs::create_hard_link(tree.root() / "outside-secret",
                         destination / "victim");
    const auto via_hardlink =
        netsec::extract_archive(destination,
                                {regular("victim", "second replacement")});
    CHECK(via_hardlink.ok());
    CHECK(read_file(destination / "victim") == "second replacement");
    CHECK(read_file(tree.root() / "outside-secret") == "outside");
    CHECK(!has_temp_files(destination));
}

void test_permission_validation() {
    TempTree tree;
    const fs::path destination = tree.root() / "extract";

    const auto setuid = netsec::extract_archive(
        destination,
        {regular("setuid", "payload",
                 fs::perms::owner_all | fs::perms::set_uid)});
    CHECK(!setuid);
    CHECK(setuid.error.code == ErrorCode::UnsafePermissions);
    CHECK(!fs::exists(destination / "setuid"));

    const auto setgid_dir = netsec::extract_archive(
        destination,
        {directory("privileged-dir",
                   fs::perms::owner_all | fs::perms::set_gid)});
    CHECK(!setgid_dir);
    CHECK(setgid_dir.error.code == ErrorCode::UnsafePermissions);
    CHECK(!fs::exists(destination / "privileged-dir"));

    const auto writable = netsec::extract_archive(
        destination,
        {regular("world-writable", "payload",
                 fs::perms::owner_all | fs::perms::group_all |
                     fs::perms::others_write)});
    CHECK(!writable);
    CHECK(writable.error.code == ErrorCode::UnsafePermissions);

    ExtractOptions relaxed;
    relaxed.allow_group_or_world_writable = true;
    const auto allowed = netsec::extract_archive(
        destination,
        {regular("world-writable", "payload",
                 fs::perms::owner_read | fs::perms::owner_write |
                     fs::perms::group_read |
                     fs::perms::group_write | fs::perms::others_read)},
        relaxed);
    CHECK(allowed.ok());
    CHECK((mode_of(destination / "world-writable") & 0777) == 0664);
}

void test_legal_archive() {
    TempTree tree;
    const fs::path destination = tree.root() / "extract";

    ExtractOptions options;
    options.hard_links = LinkPolicy::AllowWithinArchive;
    options.symbolic_links = LinkPolicy::AllowWithinArchive;

    std::vector<ArchiveEntry> entries;
    entries.push_back(directory(
        "payload", fs::perms::owner_all | fs::perms::group_read |
                       fs::perms::group_exec));
    entries.push_back(regular("payload/data.txt", "archive data",
                              fs::perms::owner_read |
                                  fs::perms::owner_write |
                                  fs::perms::group_read));
    entries.push_back(hard_link(
        "payload/data-hardlink.txt", "payload/data.txt",
        fs::perms::owner_read | fs::perms::owner_write |
            fs::perms::group_read));
    entries.push_back(symbolic_link("payload/data-link.txt",
                                    "data.txt"));
    entries.push_back(symbolic_link("payload/nested/data-link.txt",
                                    "../data.txt"));
    entries.push_back(symbolic_link("payload/nested/odd-link.txt",
                                    "child/../../data.txt"));
    entries.push_back(regular("root.txt", "root",
                              fs::perms::owner_read |
                                  fs::perms::owner_write |
                                  fs::perms::group_read |
                                  fs::perms::others_read));
    entries.push_back(directory("restricted",
                                fs::perms::owner_read |
                                    fs::perms::owner_exec |
                                    fs::perms::group_read |
                                    fs::perms::group_exec |
                                    fs::perms::others_read |
                                    fs::perms::others_exec));
    entries.push_back(regular("restricted/read-only.txt", "read only",
                              fs::perms::owner_read |
                                  fs::perms::group_read |
                                  fs::perms::others_read));
    entries.push_back(regular("./dot-prefixed.txt", "dot"));

    const auto result =
        netsec::extract_archive(destination, entries, options);
    CHECK(result.ok());
    CHECK(result.extracted.size() == entries.size());
    CHECK(read_file(destination / "payload/data.txt") == "archive data");
    CHECK(read_file(destination / "payload/data-hardlink.txt") ==
          "archive data");
    CHECK(inode_of(destination / "payload/data.txt") ==
          inode_of(destination / "payload/data-hardlink.txt"));
    CHECK(fs::is_symlink(destination / "payload/data-link.txt"));
    CHECK(fs::read_symlink(destination / "payload/data-link.txt") ==
          fs::path("data.txt"));
    CHECK(fs::read_symlink(destination / "payload/nested/data-link.txt") ==
          fs::path("../data.txt"));
    CHECK(fs::read_symlink(destination / "payload/nested/odd-link.txt") ==
          fs::path("../data.txt"));
    CHECK((mode_of(destination / "payload") & 0777) == 0750);
    CHECK((mode_of(destination / "payload/data.txt") & 0777) == 0640);
    CHECK((mode_of(destination / "root.txt") & 0777) == 0644);
    CHECK(read_file(destination / "restricted/read-only.txt") == "read only");
    CHECK((mode_of(destination / "restricted") & 0777) == 0555);
    CHECK((mode_of(destination / "restricted/read-only.txt") & 0777) == 0444);
    CHECK(read_file(destination / "dot-prefixed.txt") == "dot");
    CHECK(!has_temp_files(destination));
}

void test_atomic_replacement_and_failure() {
    TempTree tree;
    const fs::path destination = tree.root() / "extract";
    fs::create_directories(destination);
    write_file(destination / "atomic.txt", "old contents");

    ArchiveEntry broken = regular("atomic.txt", "new");
    broken.declared_size = 99;
    const auto failed = netsec::extract_archive(destination, {broken});
    CHECK(!failed);
    CHECK(failed.error.code == ErrorCode::SizeMismatch);
    CHECK(read_file(destination / "atomic.txt") == "old contents");
    CHECK(!has_temp_files(destination));

    const auto replaced = netsec::extract_archive(
        destination,
        {regular("atomic.txt", "replacement contents")});
    CHECK(replaced.ok());
    CHECK(read_file(destination / "atomic.txt") == "replacement contents");
    CHECK(!has_temp_files(destination));
}

void test_path_and_duplicate_validation() {
    TempTree tree;
    const fs::path destination = tree.root() / "extract";

    const auto traversal =
        netsec::extract_archive(destination,
                                {regular("../escape.txt", "bad")});
    CHECK(!traversal);
    CHECK(traversal.error.code == ErrorCode::ParentTraversal);

    const auto absolute =
        netsec::extract_archive(destination,
                                {regular("/escape.txt", "bad")});
    CHECK(!absolute);
    CHECK(absolute.error.code == ErrorCode::AbsolutePath);

    const auto duplicate = netsec::extract_archive(
        destination,
        {regular("same.txt", "one"), regular("same.txt", "two")});
    CHECK(!duplicate);
    CHECK(duplicate.error.code == ErrorCode::DuplicateEntry);

    const auto conflict = netsec::extract_archive(
        destination,
        {regular("parent", "file"), regular("parent/child", "bad")});
    CHECK(!conflict);
    CHECK(conflict.error.code == ErrorCode::EntryConflict);

    const auto normalized = netsec::normalize_archive_path("a/./b");
    CHECK(normalized.ok());
    CHECK(normalized.path == "a/b");
}

void test_hard_link_permission_mismatch() {
    TempTree tree;
    const fs::path destination = tree.root() / "extract";

    ExtractOptions options;
    options.hard_links = LinkPolicy::AllowWithinArchive;

    const auto result = netsec::extract_archive(
        destination,
        {regular("target.txt", "target",
                 fs::perms::owner_read | fs::perms::owner_write |
                     fs::perms::group_read),
         hard_link("hard.txt", "target.txt",
                   fs::perms::owner_read | fs::perms::owner_write)},
        options);
    CHECK(!result);
    CHECK(result.error.code == ErrorCode::PermissionMismatch);
    CHECK(!fs::exists(destination / "hard.txt"));
}

} // namespace

int main() {
    test_hard_link_escape_rejected();
    test_link_extraction_order();
    test_symbolic_link_policy_and_containment();
    test_existing_symlink_parent_cannot_redirect_write();
    test_existing_leaf_links_are_replaced_not_followed();
    test_permission_validation();
    test_legal_archive();
    test_atomic_replacement_and_failure();
    test_path_and_duplicate_validation();
    test_hard_link_permission_mismatch();

    std::cout << g_checks << " checks, " << g_failures << " failures\n";
    return g_failures == 0 ? 0 : 1;
}
