#include "netsec.hpp"

#include <algorithm>
#include <atomic>
#include <cstdlib>
#include <filesystem>
#include <fstream>
#include <iostream>
#include <iterator>
#include <string>
#include <string_view>
#include <vector>

namespace {

namespace fs = std::filesystem;
using namespace netsec;

int failures = 0;

void check(bool condition, const char* expression, const char* file, int line) {
    if (!condition) {
        std::cerr << "FAIL " << file << ':' << line << ": " << expression << '\n';
        ++failures;
    }
}

#define CHECK(expression) check((expression), #expression, __FILE__, __LINE__)

template <typename T>
bool expect_ok(const Result<T>& result, const char* what) {
    if (!result) {
        std::cerr << "FAIL " << what << ": " << to_string(result.error().code)
                  << ": " << result.error().message;
        if (!result.error().path.empty()) {
            std::cerr << " [" << result.error().path << ']';
        }
        std::cerr << '\n';
        ++failures;
        return false;
    }
    return true;
}

template <typename T>
void expect_error(const Result<T>& result, ErrorCode code, const char* what) {
    if (result) {
        std::cerr << "FAIL " << what << ": expected error "
                  << to_string(code) << ", got success\n";
        ++failures;
        return;
    }
    if (result.error().code != code) {
        std::cerr << "FAIL " << what << ": expected error " << to_string(code)
                  << ", got " << to_string(result.error().code) << ": "
                  << result.error().message << '\n';
        ++failures;
    }
}

void expect_status_ok(const Status& status, const char* what) {
    if (!status) {
        std::cerr << "FAIL " << what << ": " << to_string(status.error().code)
                  << ": " << status.error().message;
        if (!status.error().path.empty()) {
            std::cerr << " [" << status.error().path << ']';
        }
        std::cerr << '\n';
        ++failures;
    }
}

void expect_status_error(const Status& status, ErrorCode code, const char* what) {
    if (status) {
        std::cerr << "FAIL " << what << ": expected error " << to_string(code)
                  << ", got success\n";
        ++failures;
        return;
    }
    if (status.error().code != code) {
        std::cerr << "FAIL " << what << ": expected error " << to_string(code)
                  << ", got " << to_string(status.error().code) << ": "
                  << status.error().message << '\n';
        ++failures;
    }
}

class TempDir {
public:
    explicit TempDir(std::string_view label) {
        static std::atomic<unsigned long long> counter{0};
        const char* environment = std::getenv("TMPDIR");
        const fs::path base =
            environment != nullptr && *environment != '\0'
                ? fs::path(environment)
                : fs::current_path();
        path_ = base / ("netsec-test-" + std::string(label) + "-" +
                        std::to_string(counter.fetch_add(1)));
        std::error_code error;
        fs::remove_all(path_, error);
        fs::create_directories(path_, error);
    }

    ~TempDir() {
        std::error_code error;
        fs::remove_all(path_, error);
    }

    const fs::path& path() const { return path_; }

private:
    fs::path path_;
};

Bytes file_bytes(std::string_view text) {
    return to_bytes(text);
}

Bytes read_all(const fs::path& path) {
    std::ifstream input(path, std::ios::binary);
    std::string data((std::istreambuf_iterator<char>(input)),
                     std::istreambuf_iterator<char>());
    return Bytes(data.begin(), data.end());
}

const CanonicalEntry* find_entry(const VerifiedPackage& package,
                                 std::string_view path) {
    for (const CanonicalEntry& entry : package.entries()) {
        if (entry.path == path) {
            return &entry;
        }
    }
    return nullptr;
}

PackageFile* find_file(PackageSource& source, std::string_view path) {
    for (PackageFile& file : source.files) {
        if (file.path == path) {
            return &file;
        }
    }
    return nullptr;
}

constexpr std::string_view kKey = "package-test-key";
constexpr std::string_view kLfScript =
    "#!/bin/sh\n"
    "set -eu\n"
    "echo \"h\xc3\xa9llo\"\n";
constexpr std::string_view kCrlfScript =
    "#!/bin/sh\r\n"
    "set -eu\r\n"
    "echo \"h\xc3\xa9llo\"\r\n";
constexpr std::string_view kConfig = "mode=strict\n";
constexpr std::string_view kCrlfConfigWithBom =
    "\xef\xbb\xbfmode=strict\r\n";

PackageDraft legitimate_draft() {
    PackageDraft draft;
    draft.package_id = "demo-package";
    draft.version = "1.0.0";
    draft.files.push_back(
        {"scripts/install.sh", EntryRole::Script, file_bytes(kLfScript)});
    draft.files.push_back(
        {"share/config.txt", EntryRole::Config, file_bytes(kConfig)});
    draft.files.push_back(
        {"share/payload.bin", EntryRole::Data,
         Bytes{0x00, 0x01, 0x02, 0x0d, 0x0a, 0xff, 0x7f}});
    return draft;
}

void test_sha256_and_hmac_vectors() {
    CHECK(sha256(std::string_view{}).hex() ==
          "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855");
    CHECK(sha256(std::string_view("abc")).hex() ==
          "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");

    Bytes key(20, 0x0b);
    const Bytes tag = hmac_sha256(key, as_bytes("Hi There"));
    const Bytes expected = {
        0xb0, 0x34, 0x4c, 0x61, 0xd8, 0xdb, 0x38, 0x53,
        0x5c, 0xa8, 0xaf, 0xce, 0xaf, 0x0b, 0xf1, 0x2b,
        0x88, 0x1d, 0xc2, 0x00, 0xc9, 0x83, 0x3d, 0xa7,
        0x26, 0xe9, 0x37, 0x6c, 0x2e, 0x32, 0xcf, 0xf7,
    };
    CHECK(constant_time_equal(ByteView(expected.data(), expected.size()),
                              ByteView(tag.data(), tag.size())));
}

void test_canonical_text() {
    const auto crlf = canonicalize_text(std::string_view("a\r\nb\rc\n"));
    CHECK(expect_ok(crlf, "canonicalize CRLF"));
    if (crlf) {
        CHECK(crlf.value() == "a\nb\nc\n");
    }

    const auto bom = canonicalize_text(std::string_view("\xef\xbb\xbfhello\r\n"));
    CHECK(expect_ok(bom, "canonicalize BOM"));
    if (bom) {
        CHECK(bom.value() == "hello\n");
    }

    const auto middle_bom =
        canonicalize_text(std::string_view("abc\xef\xbb\xbf" "def"));
    expect_error(middle_bom, ErrorCode::InvalidText, "reject middle BOM");

    const Bytes utf16 = {0xff, 0xfe, '#', 0x00, '!', 0x00};
    expect_error(canonicalize_text(utf16), ErrorCode::UnsupportedEncoding,
                 "reject UTF-16 BOM");

    const Bytes overlong = {0xc0, 0xaf};
    expect_error(canonicalize_text(overlong), ErrorCode::InvalidUtf8,
                 "reject overlong UTF-8");

    const Bytes surrogate = {0xed, 0xa0, 0x80};
    expect_error(canonicalize_text(surrogate), ErrorCode::InvalidUtf8,
                 "reject UTF-8 surrogate");

    const Bytes truncated = {0xe2, 0x82};
    expect_error(canonicalize_text(truncated), ErrorCode::InvalidUtf8,
                 "reject truncated UTF-8");

    const Bytes too_high = {0xf4, 0x90, 0x80, 0x80};
    expect_error(canonicalize_text(too_high), ErrorCode::InvalidUtf8,
                 "reject code point above U+10FFFF");

    const Bytes nul = {'a', 0x00, 'b'};
    expect_error(canonicalize_text(nul), ErrorCode::ControlCharacter,
                 "reject NUL");

    const Bytes c1_control = {0xc2, 0x85};
    expect_error(canonicalize_text(c1_control), ErrorCode::ControlCharacter,
                 "reject C1 control");

    const Bytes zero_width = {0xe2, 0x80, 0x8b};
    expect_error(canonicalize_text(zero_width), ErrorCode::ControlCharacter,
                 "reject zero width space");

    const Bytes tabs = {'a', '\t', 'b', '\n'};
    const auto tab_result = canonicalize_text(tabs);
    CHECK(expect_ok(tab_result, "allow tab"));
    if (tab_result) {
        CHECK(as_string_view(tab_result.value()) == "a\tb\n");
    }
}

void test_canonical_paths() {
    const auto variant = canonicalize_path("./scripts//install.sh");
    CHECK(expect_ok(variant, "canonicalize path variant"));
    if (variant) {
        CHECK(variant.value() == "scripts/install.sh");
    }

    const auto dot = canonicalize_path("scripts/./run.sh");
    CHECK(expect_ok(dot, "canonicalize dot segment"));
    if (dot) {
        CHECK(dot.value() == "scripts/run.sh");
    }

    expect_error(canonicalize_path("../etc/passwd"), ErrorCode::PathTraversal,
                 "reject parent traversal");
    expect_error(canonicalize_path("scripts/../../etc/passwd"),
                 ErrorCode::PathTraversal, "reject nested traversal");
    expect_error(canonicalize_path("/etc/passwd"), ErrorCode::AbsolutePath,
                 "reject absolute path");
    expect_error(canonicalize_path("scripts\\install.sh"),
                 ErrorCode::InvalidPath, "reject backslash");
    expect_error(canonicalize_path("scripts/%2e%2e/passwd"),
                 ErrorCode::InvalidPath, "reject percent escape");
    expect_error(canonicalize_path("C:/Windows/system32"),
                 ErrorCode::InvalidPath, "reject drive/colon");
    expect_error(canonicalize_path("scripts/path\xe5\xbe\x84.sh"),
                 ErrorCode::InvalidPath, "reject non-ASCII path");
    expect_error(canonicalize_path("scripts/"), ErrorCode::InvalidPath,
                 "reject trailing slash");
    expect_error(canonicalize_path("."), ErrorCode::InvalidPath,
                 "reject empty canonical path");
    expect_error(canonicalize_path("scripts/name."), ErrorCode::InvalidPath,
                 "reject trailing dot");
}

void test_legitimate_package() {
    const PackageDraft draft = legitimate_draft();
    const auto built = build_hmac_package(draft, kKey);
    CHECK(expect_ok(built, "build legitimate package"));
    if (!built) {
        return;
    }

    const auto verified = verify_package(built.value(), hmac_verify_function(kKey));
    CHECK(expect_ok(verified, "verify legitimate package"));
    if (!verified) {
        return;
    }
    const auto hmac_verified = verify_hmac_package(built.value(), kKey);
    CHECK(expect_ok(hmac_verified, "verify HMAC convenience wrapper"));

    CHECK(verified.value().package_id() == "demo-package");
    CHECK(verified.value().version() == "1.0.0");
    CHECK(verified.value().entries().size() == 3);
    CHECK(sha256(verified.value().signing_payload()) ==
          verified.value().root_digest());
    CHECK(sha256(verified.value().manifest_bytes()) ==
          verified.value().manifest_digest());

    const CanonicalEntry* script = find_entry(verified.value(), "scripts/install.sh");
    CHECK(script != nullptr);
    if (script != nullptr) {
        CHECK(as_string_view(script->bytes) == kLfScript);
        CHECK(sha256(script->bytes) == script->content_digest);
        CHECK(sha256(as_bytes(script->path)) == script->path_digest);
    }

    const CanonicalEntry* payload = find_entry(verified.value(), "share/payload.bin");
    CHECK(payload != nullptr);
    if (payload != nullptr) {
        const Bytes expected = {0x00, 0x01, 0x02, 0x0d, 0x0a, 0xff, 0x7f};
        CHECK(payload->bytes == expected);
    }

    TempDir destination("legit-install");
    expect_status_ok(install_package(verified.value(), destination.path()),
                     "install legitimate package");

    CHECK(read_all(destination.path() / "scripts/install.sh") ==
          file_bytes(kLfScript));
    CHECK(read_all(destination.path() / "share/config.txt") == file_bytes(kConfig));
    const Bytes expected_payload = {0x00, 0x01, 0x02, 0x0d, 0x0a, 0xff, 0x7f};
    CHECK(read_all(destination.path() / "share/payload.bin") == expected_payload);
    CHECK(!fs::exists(destination.path() / "manifest.txt"));

    TempDir composed("verify-and-install");
    expect_status_ok(
        verify_and_install(built.value(), hmac_verify_function(kKey),
                           composed.path()),
        "verify-and-install composition");
    CHECK(read_all(composed.path() / "scripts/install.sh") ==
          file_bytes(kLfScript));

    TempDir with_manifest("legit-manifest");
    InstallOptions options;
    options.write_manifest = true;
    expect_status_ok(
        install_package(verified.value(), with_manifest.path(), options),
        "install legitimate package with manifest");
    CHECK(read_all(with_manifest.path() / "manifest.txt") ==
          verified.value().manifest_bytes());
}

void test_crlf_difference() {
    const auto built = build_hmac_package(legitimate_draft(), kKey);
    CHECK(expect_ok(built, "build package for CRLF"));
    if (!built) {
        return;
    }

    PackageSource variant = built.value();
    PackageFile* script = find_file(variant, "scripts/install.sh");
    CHECK(script != nullptr);
    if (script != nullptr) {
        script->bytes = file_bytes(kCrlfScript);
    }

    std::string manifest_text(as_string_view(variant.manifest));
    std::string crlf_manifest;
    crlf_manifest.reserve(manifest_text.size() + 64);
    for (char ch : manifest_text) {
        if (ch == '\n') {
            crlf_manifest += "\r\n";
        } else {
            crlf_manifest += ch;
        }
    }
    variant.manifest = to_bytes(std::string("\xef\xbb\xbf") + crlf_manifest);

    const auto verified = verify_package(variant, hmac_verify_function(kKey));
    CHECK(expect_ok(verified, "CRLF/BOM variant verifies over canonical bytes"));
    if (!verified) {
        return;
    }

    const CanonicalEntry* canonical_script =
        find_entry(verified.value(), "scripts/install.sh");
    CHECK(canonical_script != nullptr);
    if (canonical_script != nullptr) {
        CHECK(as_string_view(canonical_script->bytes) == kLfScript);
    }

    TempDir destination("crlf-install");
    expect_status_ok(install_package(verified.value(), destination.path()),
                     "install CRLF variant");
    CHECK(read_all(destination.path() / "scripts/install.sh") ==
          file_bytes(kLfScript));

    PackageSource role_variant = built.value();
    PackageFile* role_script = find_file(role_variant, "scripts/install.sh");
    CHECK(role_script != nullptr);
    if (role_script != nullptr) {
        role_script->role = EntryRole::Data;  // untrusted hint must be ignored
        role_script->bytes = file_bytes(kCrlfScript);
    }
    const auto role_verified =
        verify_package(role_variant, hmac_verify_function(kKey));
    CHECK(expect_ok(role_verified,
                    "manifest role, not source role, controls canonicalization"));
    if (role_verified) {
        const CanonicalEntry* canonical =
            find_entry(role_verified.value(), "scripts/install.sh");
        CHECK(canonical != nullptr);
        if (canonical != nullptr) {
            CHECK(as_string_view(canonical->bytes) == kLfScript);
        }
    }

    PackageSource different = built.value();
    PackageFile* different_script = find_file(different, "scripts/install.sh");
    CHECK(different_script != nullptr);
    if (different_script != nullptr) {
        different_script->bytes = file_bytes(
            "#!/bin/sh\r\nset -eu\r\necho \"h\xc3\xa9llo\"\r\necho hacked\r\n");
    }
    expect_error(verify_package(different, hmac_verify_function(kKey)),
                 ErrorCode::DigestMismatch,
                 "CRLF must not hide a content change");
}

void test_bom_and_encoding_replacement() {
    PackageDraft draft;
    draft.package_id = "encoding-package";
    draft.version = "2.0.0";
    draft.files.push_back(
        {"scripts/run.sh", EntryRole::Script, file_bytes(kCrlfScript)});
    draft.files.push_back(
        {"share/config.txt", EntryRole::Config, file_bytes(kCrlfConfigWithBom)});

    const auto built = build_hmac_package(draft, kKey);
    CHECK(expect_ok(built, "build BOM/CRLF package"));
    if (!built) {
        return;
    }

    const auto verified = verify_package(built.value(), hmac_verify_function(kKey));
    CHECK(expect_ok(verified, "verify BOM/CRLF package"));
    if (!verified) {
        return;
    }

    const CanonicalEntry* script = find_entry(verified.value(), "scripts/run.sh");
    const CanonicalEntry* config = find_entry(verified.value(), "share/config.txt");
    CHECK(script != nullptr);
    CHECK(config != nullptr);
    if (script != nullptr) {
        CHECK(as_string_view(script->bytes) == kLfScript);
    }
    if (config != nullptr) {
        CHECK(as_string_view(config->bytes) == kConfig);
    }

    PackageSource latin1 = built.value();
    PackageFile* latin1_script = find_file(latin1, "scripts/run.sh");
    CHECK(latin1_script != nullptr);
    if (latin1_script != nullptr) {
        std::string data = "#!/bin/sh\necho caf";
        data.push_back(static_cast<char>(0xe9));
        data.push_back('\n');
        latin1_script->bytes = to_bytes(data);
    }
    expect_error(verify_package(latin1, hmac_verify_function(kKey)),
                 ErrorCode::InvalidUtf8,
                 "Latin-1 re-encoding is rejected, not transcoded");

    PackageSource utf16 = built.value();
    PackageFile* utf16_script = find_file(utf16, "scripts/run.sh");
    CHECK(utf16_script != nullptr);
    if (utf16_script != nullptr) {
        utf16_script->bytes = Bytes{0xff, 0xfe, '#', 0x00, '!', 0x00};
    }
    expect_error(verify_package(utf16, hmac_verify_function(kKey)),
                 ErrorCode::UnsupportedEncoding,
                 "UTF-16 re-encoding is rejected");

    PackageSource nfd = built.value();
    PackageFile* nfd_script = find_file(nfd, "scripts/run.sh");
    CHECK(nfd_script != nullptr);
    if (nfd_script != nullptr) {
        nfd_script->bytes = file_bytes(
            "#!/bin/sh\nset -eu\necho \"he\xcc\x81llo\"\n");
    }
    expect_error(verify_package(nfd, hmac_verify_function(kKey)),
                 ErrorCode::DigestMismatch,
                 "different Unicode encoding is new canonical content");
}

void test_path_variants_in_package() {
    const auto built = build_hmac_package(legitimate_draft(), kKey);
    CHECK(expect_ok(built, "build package for path variants"));
    if (!built) {
        return;
    }

    PackageSource variant = built.value();
    PackageFile* script = find_file(variant, "scripts/install.sh");
    CHECK(script != nullptr);
    if (script != nullptr) {
        script->path = "./scripts//install.sh";
    }

    const auto verified = verify_package(variant, hmac_verify_function(kKey));
    CHECK(expect_ok(verified, "canonical source path variant verifies"));
    if (!verified) {
        return;
    }

    TempDir destination("path-variant-install");
    expect_status_ok(install_package(verified.value(), destination.path()),
                     "install canonical path");
    CHECK(fs::exists(destination.path() / "scripts/install.sh"));
    CHECK(!fs::exists(destination.path() / "scripts" / "scripts"));

    PackageSource duplicate = built.value();
    duplicate.files.push_back(
        {"scripts/install.sh", EntryRole::Script, file_bytes(kLfScript)});
    expect_error(verify_package(duplicate, hmac_verify_function(kKey)),
                 ErrorCode::DuplicatePath,
                 "duplicate canonical source paths are rejected");

    PackageSource case_collision = built.value();
    for (PackageFile& file : case_collision.files) {
        if (file.path == "scripts/install.sh") {
            file.path = "scripts/install.sh";
        }
    }
    case_collision.files.push_back(
        {"Scripts/install.sh", EntryRole::Script, file_bytes(kLfScript)});
    expect_error(verify_package(case_collision, hmac_verify_function(kKey)),
                 ErrorCode::PathCollision,
                 "case-only path collision is rejected");

    PackageDraft duplicate_manifest;
    duplicate_manifest.package_id = "duplicate";
    duplicate_manifest.version = "1";
    duplicate_manifest.files.push_back(
        {"a/run.sh", EntryRole::Script, file_bytes(kLfScript)});
    duplicate_manifest.files.push_back(
        {"./a//run.sh", EntryRole::Script, file_bytes(kLfScript)});
    expect_error(build_package(duplicate_manifest), ErrorCode::DuplicatePath,
                 "duplicate manifest paths are rejected");

    PackageDraft non_adjacent_collision;
    non_adjacent_collision.package_id = "case-collision";
    non_adjacent_collision.version = "1";
    non_adjacent_collision.files.push_back(
        {"A/run.sh", EntryRole::Script, file_bytes(kLfScript)});
    non_adjacent_collision.files.push_back(
        {"B/run.sh", EntryRole::Script, file_bytes(kLfScript)});
    non_adjacent_collision.files.push_back(
        {"a/run.sh", EntryRole::Script, file_bytes(kLfScript)});
    expect_error(build_package(non_adjacent_collision),
                 ErrorCode::PathCollision,
                 "non-adjacent case-only paths are rejected");

    PackageDraft traversal;
    traversal.package_id = "traversal";
    traversal.version = "1";
    traversal.files.push_back(
        {"../outside.sh", EntryRole::Script, file_bytes(kLfScript)});
    expect_error(build_package(traversal), ErrorCode::PathTraversal,
                 "traversal path cannot be packaged");
}

void test_tampering_and_digests() {
    const auto built = build_hmac_package(legitimate_draft(), kKey);
    CHECK(expect_ok(built, "build package for tampering"));
    if (!built) {
        return;
    }

    PackageSource bad_signature = built.value();
    bad_signature.signature[0] ^= 0xffu;
    expect_error(verify_package(bad_signature, hmac_verify_function(kKey)),
                 ErrorCode::SignatureInvalid, "tampered signature");

    PackageSource bad_manifest = built.value();
    std::string manifest_text(as_string_view(bad_manifest.manifest));
    const std::size_t package_position = manifest_text.find("package\tdemo-package");
    CHECK(package_position != std::string::npos);
    if (package_position != std::string::npos) {
        manifest_text.replace(package_position, 20, "package\tother-package");
        bad_manifest.manifest = to_bytes(manifest_text);
        expect_error(verify_package(bad_manifest, hmac_verify_function(kKey)),
                     ErrorCode::SignatureInvalid,
                     "manifest metadata is covered by the signature");
    }

    PackageSource bad_content_digest = built.value();
    std::string digest_text(as_string_view(bad_content_digest.manifest));
    const std::string marker = "entry\tscript\t";
    const std::size_t entry_position = digest_text.find(marker);
    CHECK(entry_position != std::string::npos);
    if (entry_position != std::string::npos) {
        const std::size_t content_digest_position = entry_position + marker.size() + 65;
        CHECK(content_digest_position + 64 <= digest_text.size());
        digest_text.replace(content_digest_position, 64, std::string(64, '0'));
        bad_content_digest.manifest = to_bytes(digest_text);
        expect_error(
            verify_package(bad_content_digest, hmac_verify_function(kKey)),
            ErrorCode::SignatureInvalid,
            "manifest content digest is covered by the signature");
    }

    PackageSource bad_path_digest = built.value();
    std::string path_text(as_string_view(bad_path_digest.manifest));
    const std::size_t path_entry_position = path_text.find(marker);
    CHECK(path_entry_position != std::string::npos);
    if (path_entry_position != std::string::npos) {
        const std::size_t path_digest_position = path_entry_position + marker.size();
        path_text.replace(path_digest_position, 64, std::string(64, '0'));
        bad_path_digest.manifest = to_bytes(path_text);
        expect_error(verify_package(bad_path_digest, hmac_verify_function(kKey)),
                     ErrorCode::DigestMismatch,
                     "manifest path digest is checked");
    }

    PackageSource changed_content = built.value();
    PackageFile* script = find_file(changed_content, "scripts/install.sh");
    CHECK(script != nullptr);
    if (script != nullptr) {
        script->bytes = file_bytes("#!/bin/sh\nset -eu\necho hacked\n");
    }
    expect_error(verify_package(changed_content, hmac_verify_function(kKey)),
                 ErrorCode::DigestMismatch,
                 "changed canonical content is rejected");

    PackageSource missing = built.value();
    missing.files.erase(
        std::remove_if(missing.files.begin(), missing.files.end(),
                       [](const PackageFile& file) {
                           return file.path == "scripts/install.sh";
                       }),
        missing.files.end());
    expect_error(verify_package(missing, hmac_verify_function(kKey)),
                 ErrorCode::MissingEntry, "missing manifest entry");

    PackageSource unlisted = built.value();
    unlisted.files.push_back(
        {"scripts/extra.sh", EntryRole::Script, file_bytes("echo extra\n")});
    expect_error(verify_package(unlisted, hmac_verify_function(kKey)),
                 ErrorCode::UnlistedEntry, "unlisted file is rejected");
}

void test_directory_roundtrip() {
    const auto built = build_hmac_package(legitimate_draft(), kKey);
    CHECK(expect_ok(built, "build package for directory roundtrip"));
    if (!built) {
        return;
    }

    TempDir package_directory("package-dir");
    expect_status_ok(write_package(built.value(), package_directory.path()),
                     "write package directory");
    {
        std::ofstream extra(package_directory.path() / "scripts/extra.sh",
                            std::ios::binary);
        extra << "echo unlisted\n";
    }

    const auto loaded = load_package(package_directory.path());
    CHECK(expect_ok(loaded, "load package directory"));
    if (!loaded) {
        return;
    }

    const auto verified = verify_package(loaded.value(), hmac_verify_function(kKey));
    CHECK(expect_ok(verified, "verify loaded package"));
    if (!verified) {
        return;
    }

    TempDir destination("directory-install");
    expect_status_ok(install_package(verified.value(), destination.path()),
                     "install loaded package");
    CHECK(read_all(destination.path() / "scripts/install.sh") ==
          file_bytes(kLfScript));
    CHECK(read_all(destination.path() / "share/config.txt") == file_bytes(kConfig));
    CHECK(!fs::exists(destination.path() / "scripts/extra.sh"));

    PackageSource variant = built.value();
    PackageFile* variant_script = find_file(variant, "scripts/install.sh");
    PackageFile* variant_config = find_file(variant, "share/config.txt");
    CHECK(variant_script != nullptr);
    CHECK(variant_config != nullptr);
    if (variant_script != nullptr) {
        variant_script->bytes = to_bytes(std::string("\xef\xbb\xbf") +
                                        std::string(kCrlfScript));
    }
    if (variant_config != nullptr) {
        variant_config->bytes = file_bytes(kCrlfConfigWithBom);
    }
    std::string variant_manifest(as_string_view(variant.manifest));
    std::string crlf_manifest;
    for (char ch : variant_manifest) {
        if (ch == '\n') {
            crlf_manifest += "\r\n";
        } else {
            crlf_manifest += ch;
        }
    }
    variant.manifest = to_bytes(std::string("\xef\xbb\xbf") + crlf_manifest);

    TempDir variant_directory("variant-package-dir");
    expect_status_ok(write_package(variant, variant_directory.path()),
                     "write CRLF/BOM package directory");
    const auto variant_loaded = load_package(variant_directory.path());
    CHECK(expect_ok(variant_loaded, "load CRLF/BOM package directory"));
    if (!variant_loaded) {
        return;
    }
    const auto variant_verified =
        verify_package(variant_loaded.value(), hmac_verify_function(kKey));
    CHECK(expect_ok(variant_verified, "verify CRLF/BOM package directory"));
    if (!variant_verified) {
        return;
    }

    TempDir variant_destination("variant-directory-install");
    expect_status_ok(
        install_package(variant_verified.value(), variant_destination.path()),
        "install CRLF/BOM package directory");
    CHECK(read_all(variant_destination.path() / "scripts/install.sh") ==
          file_bytes(kLfScript));
    CHECK(read_all(variant_destination.path() / "share/config.txt") ==
          file_bytes(kConfig));
}

void test_install_rejects_symlink() {
    const auto built = build_hmac_package(legitimate_draft(), kKey);
    CHECK(expect_ok(built, "build package for symlink test"));
    if (!built) {
        return;
    }
    const auto verified = verify_package(built.value(), hmac_verify_function(kKey));
    CHECK(expect_ok(verified, "verify package for symlink test"));
    if (!verified) {
        return;
    }

    TempDir destination("symlink-install");
    fs::create_directories(destination.path() / "scripts");
    const fs::path outside = destination.path() / "outside.txt";
    {
        std::ofstream output(outside, std::ios::binary);
        output << "outside";
    }
    std::error_code error;
    fs::create_symlink(outside, destination.path() / "scripts/install.sh", error);
    if (error) {
        std::cerr << "SKIP symlink test: cannot create symlink\n";
        return;
    }

    expect_status_error(
        install_package(verified.value(), destination.path()), ErrorCode::IoError,
        "install refuses a symlink at the destination");
    CHECK(read_all(outside) == file_bytes("outside"));
}

}  // namespace

int main() {
    test_sha256_and_hmac_vectors();
    test_canonical_text();
    test_canonical_paths();
    test_legitimate_package();
    test_crlf_difference();
    test_bom_and_encoding_replacement();
    test_path_variants_in_package();
    test_tampering_and_digests();
    test_directory_roundtrip();
    test_install_rejects_symlink();

    if (failures == 0) {
        std::cout << "All netsec content-consistency tests passed\n";
        return 0;
    }
    std::cerr << failures << " test assertion(s) failed\n";
    return 1;
}
