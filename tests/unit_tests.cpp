// Unit tests for the dircopy copy engine.
#include "../src/copy_engine.hpp"

#include <cassert>
#include <cstdio>
#include <fstream>
#include <iostream>
#include <string>
#include <sys/stat.h>
#include <unistd.h>
#include <unordered_map>

namespace {

int failures = 0;

#define CHECK(cond)                                                                              \
    do {                                                                                         \
        if (!(cond)) {                                                                           \
            std::cerr << "FAIL " << __FILE__ << ":" << __LINE__ << ": " #cond "\n";             \
            ++failures;                                                                          \
        }                                                                                        \
    } while (0)

ino_t inode_of(const std::string& path) {
    struct stat st;
    assert(::lstat(path.c_str(), &st) == 0);
    return st.st_ino;
}

void write_file(const std::string& path, const std::string& data) {
    std::ofstream f(path, std::ios::binary);
    f << data;
}

// Cross-device keying: identical inode numbers on different devices must be
// treated as distinct objects, so hard-link groups can never be conflated
// across filesystem boundaries.
void test_file_id_cross_device() {
    std::unordered_map<dircopy::FileId, int, dircopy::FileIdHash> m;
    m[dircopy::FileId{1, 42}] = 1;
    m[dircopy::FileId{2, 42}] = 2; // same ino, different dev
    m[dircopy::FileId{1, 43}] = 3; // same dev, different ino
    CHECK(m.size() == 3);
    CHECK(m.at(dircopy::FileId{1, 42}) == 1);
    CHECK(m.at(dircopy::FileId{2, 42}) == 2);
    CHECK(m.at(dircopy::FileId{1, 43}) == 3);
    CHECK((dircopy::FileId{1, 42} == dircopy::FileId{1, 42}));
    CHECK((dircopy::FileId{1, 42} != dircopy::FileId{2, 42}));
}

// Engine round-trip: hard-link group inside the root stays linked in the
// target; an equal-content but distinct-inode file does not.
void test_engine_roundtrip(const std::string& base) {
    const std::string src = base + "/src";
    const std::string dst = base + "/dst";
    assert(::mkdir(src.c_str(), 0755) == 0);
    assert(::mkdir((src + "/sub").c_str(), 0755) == 0);
    write_file(src + "/a.txt", "shared-content");
    assert(::link((src + "/a.txt").c_str(), (src + "/b.txt").c_str()) == 0);
    assert(::link((src + "/a.txt").c_str(), (src + "/sub/c.txt").c_str()) == 0);
    write_file(src + "/clone.txt", "shared-content"); // same bytes, own inode
    assert(::symlink("a.txt", (src + "/sym").c_str()) == 0);

    dircopy::ScanResult scan;
    std::string err;
    CHECK(dircopy::scan_tree(src, scan, err));
    // The scan must record exactly one inode group for a/b/c.
    CHECK(scan.inode_to_first_rel.size() == 2); // group + clone.txt

    dircopy::CopyReport rep = dircopy::execute_copy(src, dst, scan);
    CHECK(rep.ok());
    CHECK(rep.files_copied == 2);
    CHECK(rep.hardlinks_created == 2);
    CHECK(rep.symlinks_created == 1);

    // Verify sharing in the target by inode identity, not by bytes.
    const ino_t a = inode_of(dst + "/a.txt");
    CHECK(a == inode_of(dst + "/b.txt"));
    CHECK(a == inode_of(dst + "/sub/c.txt"));
    CHECK(a != inode_of(dst + "/clone.txt"));

    struct stat st;
    CHECK(::lstat((dst + "/sym").c_str(), &st) == 0 && S_ISLNK(st.st_mode));
}

} // namespace

int main() {
    test_file_id_cross_device();
    char tmpl[] = "/tmp/dircopy-unit-XXXXXX";
    std::string base = ::mkdtemp(tmpl);
    test_engine_roundtrip(base);
    if (failures == 0) {
        std::cout << "unit tests: all passed\n";
        return 0;
    }
    std::cerr << "unit tests: " << failures << " failure(s)\n";
    return 1;
}
