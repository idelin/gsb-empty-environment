// Tests for the dircopy library. Each test builds a fixture tree in a
// private temp directory and verifies destination *sharing relationships*
// via (dev, ino) from stat(2) -- never by comparing bytes alone.

#include <cerrno>
#include <cstdlib>
#include <cstdio>
#include <cstring>
#include <iostream>
#include <memory>
#include <string>
#include <sys/stat.h>
#include <unistd.h>
#include <fcntl.h>

#include "../src/dircopy.h"

namespace {

int g_failures = 0;
int g_checks = 0;

#define CHECK(cond)                                                        \
    do {                                                                   \
        ++g_checks;                                                        \
        if (!(cond)) {                                                     \
            ++g_failures;                                                  \
            std::fprintf(stderr, "FAIL %s:%d: %s\n", __FILE__, __LINE__, \
                         #cond);                                           \
        }                                                                  \
    } while (0)

std::string g_base;  // per-run temp root

std::string join(const std::string& a, const std::string& b) { return a + "/" + b; }

void write_file(const std::string& path, const std::string& data) {
    int fd = ::open(path.c_str(), O_WRONLY | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) { perror(("open " + path).c_str()); std::exit(2); }
    const char* p = data.data();
    size_t left = data.size();
    while (left) {
        ssize_t w = ::write(fd, p, left);
        if (w < 0) { perror("write"); std::exit(2); }
        p += w; left -= static_cast<size_t>(w);
    }
    ::close(fd);
}

std::string read_file(const std::string& path) {
    int fd = ::open(path.c_str(), O_RDONLY);
    if (fd < 0) return {};
    std::string out;
    char buf[4096];
    ssize_t n;
    while ((n = ::read(fd, buf, sizeof buf)) > 0) out.append(buf, static_cast<size_t>(n));
    ::close(fd);
    return out;
}

dircopy::FileId id_of(const std::string& path, bool follow = false) {
    struct stat st;
    if ((follow ? ::stat(path.c_str(), &st) : ::lstat(path.c_str(), &st)) != 0)
        return dircopy::FileId{0, 0};
    return dircopy::FileId{st.st_dev, st.st_ino};
}

nlink_t nlink_of(const std::string& path) {
    struct stat st;
    return ::lstat(path.c_str(), &st) == 0 ? st.st_nlink : 0;
}

mode_t mode_of(const std::string& path) {
    struct stat st;
    return ::lstat(path.c_str(), &st) == 0 ? (st.st_mode & 07777) : 0;
}

bool exists(const std::string& p) {
    struct stat st;
    return ::lstat(p.c_str(), &st) == 0;
}

void rm_rf(const std::string& root) {
    struct stat st;
    if (::lstat(root.c_str(), &st) != 0) return;
    if (S_ISDIR(st.st_mode)) {
        std::string cmd = "chmod -R u+rwx '" + root + "' 2>/dev/null";
        std::system(cmd.c_str());
        std::string cmd2 = "rm -rf '" + root + "'";
        std::system(cmd2.c_str());
    } else {
        ::unlink(root.c_str());
    }
}

struct Fixture {
    std::string dir;
    explicit Fixture(const std::string& name) : dir(join(g_base, name)) {
        if (::mkdir(dir.c_str(), 0755) != 0) { perror("mkdir fixture"); std::exit(2); }
    }
    ~Fixture() { rm_rf(dir); }
    std::string p(const std::string& rel) const { return join(dir, rel); }
};

// ---------------------------------------------------------------- tests

// Hard links inside the root must stay linked in the destination.
void test_hardlinks_preserved() {
    Fixture f("hardlinks");
    write_file(f.p("a"), "shared-payload");
    ::link(f.p("a").c_str(), f.p("b").c_str());
    ::mkdir(f.p("sub").c_str(), 0755);
    ::link(f.p("a").c_str(), f.p("sub/c").c_str());

    dircopy::CopyReport rep;
    CHECK(dircopy::copy_directory(f.dir, f.p("dst"), rep));
    CHECK(rep.rejected.empty());
    CHECK(rep.files_copied == 1);
    CHECK(rep.hardlinks_created == 2);

    dircopy::FileId ia = id_of(f.p("dst/a"));
    dircopy::FileId ib = id_of(f.p("dst/b"));
    dircopy::FileId ic = id_of(f.p("dst/sub/c"));
    CHECK(ia == ib && ib == ic);            // sharing reproduced...
    CHECK(nlink_of(f.p("dst/a")) == 3);     // ...with exactly 3 links
    CHECK(!(ia == id_of(f.p("a"))));        // but it is a fresh inode
    CHECK(read_file(f.p("dst/sub/c")) == "shared-payload");
}

// Identical bytes in different inodes must NOT be merged.
void test_same_content_distinct_inodes() {
    Fixture f("dedup");
    write_file(f.p("x"), "identical");
    write_file(f.p("y"), "identical");

    dircopy::CopyReport rep;
    CHECK(dircopy::copy_directory(f.dir, f.p("dst"), rep));
    CHECK(rep.files_copied == 2);
    CHECK(rep.hardlinks_created == 0);
    CHECK(!(id_of(f.p("dst/x")) == id_of(f.p("dst/y"))));
}

// A hard link whose inode also lives outside the root must not pull the
// outside object in, and the destination must not stay linked to it.
void test_outside_root_inode_not_adopted() {
    Fixture f("outside");
    ::mkdir(f.p("external").c_str(), 0755);
    write_file(f.p("external/o"), "outside-data");
    ::mkdir(f.p("src").c_str(), 0755);
    ::link(f.p("external/o").c_str(), f.p("src/h").c_str());
    write_file(f.p("src/plain"), "plain");

    dircopy::CopyReport rep;
    CHECK(dircopy::copy_directory(f.p("src"), f.p("dst"), rep));
    CHECK(rep.rejected.empty());
    CHECK(exists(f.p("dst/h")));
    CHECK(nlink_of(f.p("dst/h")) == 1);            // not linked to anything
    CHECK(!(id_of(f.p("dst/h")) == id_of(f.p("external/o"))));
    CHECK(read_file(f.p("dst/h")) == "outside-data");
    CHECK(!exists(f.p("dst/external")));           // outside tree not copied
    CHECK(nlink_of(f.p("external/o")) == 2);       // source untouched
}

// Identity change between scan and copy: reject the item, keep the report.
void test_toctou_identity_change_rejected() {
    Fixture f("toctou");
    ::mkdir(f.p("src").c_str(), 0755);
    write_file(f.p("src/keep"), "keep");
    write_file(f.p("src/victim"), "original");

    dircopy::ScanResult scan = dircopy::scan_tree(f.p("src"));

    // Attacker/racer replaces victim with a brand-new inode...
    ::unlink(f.p("src/victim").c_str());
    write_file(f.p("src/victim"), "replaced");

    dircopy::CopyReport rep;
    CHECK(!dircopy::copy_tree(scan, f.p("src"), f.p("dst"), rep));
    CHECK(rep.rejected.size() == 1);
    CHECK(rep.rejected[0].path == "victim");
    CHECK(!exists(f.p("dst/victim")));
    CHECK(read_file(f.p("dst/keep")) == "keep");

    // ...or swaps in a hard link to an outside object: still rejected,
    // never silently adopted.
    ::mkdir(f.p("external").c_str(), 0755);
    write_file(f.p("external/o"), "outside");
    ::unlink(f.p("src/victim").c_str());
    ::link(f.p("external/o").c_str(), f.p("src/victim").c_str());

    dircopy::CopyReport rep2;
    CHECK(!dircopy::copy_tree(scan, f.p("src"), f.p("dst2"), rep2));
    CHECK(rep2.rejected.size() == 1);
    CHECK(rep2.rejected[0].path == "victim");
    CHECK(!exists(f.p("dst2/victim")));
}

// Symlinks are copied as symlinks, targets verbatim, never followed.
void test_symlinks_copied_as_symlinks() {
    Fixture f("links");
    ::mkdir(f.p("src").c_str(), 0755);
    write_file(f.p("src/real"), "real-data");
    ::symlink("real", f.p("src/rel").c_str());              // relative, internal
    ::symlink("no/such/target", f.p("src/dangling").c_str());
    write_file(f.p("outside_target"), "outside");
    ::symlink(f.p("outside_target").c_str(), f.p("src/abs").c_str());

    dircopy::CopyReport rep;
    CHECK(dircopy::copy_directory(f.p("src"), f.p("dst"), rep));
    CHECK(rep.symlinks_copied == 3);

    struct stat st;
    CHECK(::lstat(f.p("dst/rel").c_str(), &st) == 0 && S_ISLNK(st.st_mode));
    char buf[512];
    ssize_t n = ::readlink(f.p("dst/rel").c_str(), buf, sizeof buf - 1);
    CHECK(n > 0 && std::string(buf, static_cast<size_t>(n)) == "real");
    n = ::readlink(f.p("dst/dangling").c_str(), buf, sizeof buf - 1);
    CHECK(n > 0 && std::string(buf, static_cast<size_t>(n)) == "no/such/target");
    n = ::readlink(f.p("dst/abs").c_str(), buf, sizeof buf - 1);
    CHECK(n > 0 && std::string(buf, static_cast<size_t>(n)) == f.p("outside_target"));
    CHECK(!exists(f.p("dst/outside_target")));  // target not pulled in
}

// A symlink swapped after the scan is rejected too.
void test_toctou_symlink_changed() {
    Fixture f("toctou_link");
    ::mkdir(f.p("src").c_str(), 0755);
    ::symlink("one", f.p("src/l").c_str());
    dircopy::ScanResult scan = dircopy::scan_tree(f.p("src"));
    ::unlink(f.p("src/l").c_str());
    ::symlink("two", f.p("src/l").c_str());

    dircopy::CopyReport rep;
    CHECK(!dircopy::copy_tree(scan, f.p("src"), f.p("dst"), rep));
    CHECK(rep.rejected.size() == 1 && rep.rejected[0].path == "l");
    CHECK(!exists(f.p("dst/l")));
}

// Abort mode: first failure stops the copy and removes the partial tree.
void test_abort_cleans_partial_tree() {
    Fixture f("abort");
    ::mkdir(f.p("src").c_str(), 0755);
    write_file(f.p("src/00_good"), "good");
    write_file(f.p("src/99_bad"), "bad");
    ::chmod(f.p("src/99_bad").c_str(), 0000);

    dircopy::CopyReport rep;
    CHECK(!dircopy::copy_directory(f.p("src"), f.p("dst"), rep,
                                   dircopy::OnError::Abort));
    CHECK(rep.aborted);
    CHECK(!rep.rejected.empty());
    CHECK(!exists(f.p("dst")));  // partial tree fully removed
}

// Skip mode: failure is reported, the rest of the tree survives, and no
// partial destination file is left behind for the failed item.
void test_skip_keeps_partial_tree() {
    Fixture f("skip");
    ::mkdir(f.p("src").c_str(), 0755);
    write_file(f.p("src/00_good"), "good");
    write_file(f.p("src/99_bad"), "bad");
    ::chmod(f.p("src/99_bad").c_str(), 0000);

    dircopy::CopyReport rep;
    CHECK(!dircopy::copy_directory(f.p("src"), f.p("dst"), rep,
                                   dircopy::OnError::Skip));
    CHECK(!rep.aborted);
    CHECK(rep.rejected.size() == 1 && rep.rejected[0].path == "99_bad");
    CHECK(exists(f.p("dst")));
    CHECK(read_file(f.p("dst/00_good")) == "good");
    CHECK(!exists(f.p("dst/99_bad")));
}

// Permissions survive the copy (dirs get their final modes after the copy).
void test_modes_preserved() {
    Fixture f("modes");
    ::mkdir(f.p("src").c_str(), 0755);
    ::mkdir(f.p("src/d").c_str(), 0750);
    write_file(f.p("src/d/x"), "x");
    ::chmod(f.p("src/d/x").c_str(), 0600);

    dircopy::CopyReport rep;
    CHECK(dircopy::copy_directory(f.p("src"), f.p("dst"), rep));
    CHECK(mode_of(f.p("dst/d")) == 0750);
    CHECK(mode_of(f.p("dst/d/x")) == 0600);
}

// Cross-device copy: internal hard links must still be reproduced in the
// destination even though no link can span devices. Uses a RAM disk when
// one can be created; skips gracefully otherwise.
std::string run_cmd(const std::string& cmd) {
    std::string out;
    FILE* p = ::popen(cmd.c_str(), "r");
    if (!p) return out;
    char buf[512];
    while (::fgets(buf, sizeof buf, p)) out += buf;
    ::pclose(p);
    while (!out.empty() && (out.back() == '\n' || out.back() == ' ')) out.pop_back();
    return out;
}

void test_cross_device() {
    // A directory on a second device can be supplied externally; otherwise
    // we try to create a RAM disk ourselves.
    const char* env = std::getenv("DIRCOPY_TEST_DEV2_DIR");
    std::string other = env ? env : "/Volumes/dircopy-test-vol";
    std::string dev;
    bool have = false;
    if (env) {
        have = true;
    } else {
#if defined(__APPLE__)
        dev = run_cmd("hdiutil attach -nomount ram://8192 2>/dev/null");
        have = !dev.empty() && dev[0] == '/';
        if (have) have = ::system(("newfs_hfs -v dircopy-test-vol " + dev + " >/dev/null 2>&1").c_str()) == 0;
        if (have) have = ::system(("hdiutil attach -mountpoint " + other + " " + dev + " >/dev/null 2>&1").c_str()) == 0;
#else
        have = ::system("mkdir -p /tmp/dircopy-tmpfs && "
                        "mount -t tmpfs tmpfs /tmp/dircopy-tmpfs 2>/dev/null") == 0;
        other = "/tmp/dircopy-tmpfs";
#endif
    }
    struct stat st_a, st_b;
    if (have) {
        have = ::stat(g_base.c_str(), &st_a) == 0 && ::stat(other.c_str(), &st_b) == 0 &&
               st_a.st_dev != st_b.st_dev;
    }
    if (!have) {
        std::fprintf(stderr, "SKIP cross-device: no second device available "
                             "(set DIRCOPY_TEST_DEV2_DIR to enable)\n");
        if (!dev.empty()) ::system(("hdiutil detach " + dev + " >/dev/null 2>&1").c_str());
        return;
    }

    std::string dst_root = join(other, "dst-cross");
    {
        Fixture f("xdev");
        ::mkdir(f.p("src").c_str(), 0755);
        write_file(f.p("src/p"), "cross-device-payload");
        ::link(f.p("src/p").c_str(), f.p("src/q").c_str());

        dircopy::CopyReport rep;
        CHECK(dircopy::copy_directory(f.p("src"), dst_root, rep));
        CHECK(rep.files_copied == 1 && rep.hardlinks_created == 1);
        CHECK(id_of(join(dst_root, "p")) == id_of(join(dst_root, "q")));
        CHECK(nlink_of(join(dst_root, "p")) == 2);
        CHECK(read_file(join(dst_root, "q")) == "cross-device-payload");
        struct stat ds;
        CHECK(::stat(join(dst_root, "p").c_str(), &ds) == 0 && ds.st_dev == st_b.st_dev);
    }
    rm_rf(dst_root);
#if defined(__APPLE__)
    if (!dev.empty()) ::system(("hdiutil detach " + dev + " >/dev/null 2>&1").c_str());
#else
    if (!env) ::system("umount /tmp/dircopy-tmpfs 2>/dev/null");
#endif
}

}  // namespace

int main() {
    char tmpl[] = "/tmp/dircopy-test-XXXXXX";
    if (!::mkdtemp(tmpl)) { perror("mkdtemp"); return 2; }
    g_base = tmpl;

    test_hardlinks_preserved();
    test_same_content_distinct_inodes();
    test_outside_root_inode_not_adopted();
    test_toctou_identity_change_rejected();
    test_symlinks_copied_as_symlinks();
    test_toctou_symlink_changed();
    test_abort_cleans_partial_tree();
    test_skip_keeps_partial_tree();
    test_modes_preserved();
    test_cross_device();

    rm_rf(g_base);
    std::fprintf(stderr, "%d checks, %d failures\n", g_checks, g_failures);
    return g_failures ? 1 : 0;
}
