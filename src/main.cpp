#include "copy_engine.hpp"

#include <chrono>
#include <cstdio>
#include <cstdlib>
#include <iostream>
#include <string>
#include <sys/stat.h>
#include <thread>

namespace {

void usage(const char* argv0) {
    std::fprintf(stderr,
                 "usage: %s <src-root> <dst-root> [--report <file>]\n"
                 "Copies a directory tree preserving in-root hard-link groups.\n"
                 "Symlinks are recreated as symlinks (never followed).\n"
                 "Exit codes: 0 = success, 1 = fatal error, 2 = some items rejected.\n",
                 argv0);
}

// Test hook: if DIRCOPY_TEST_GATE is set, wait (max 30s) for that file to
// appear between the scan phase and the copy phase. This lets tests mutate
// the source tree after scanning to exercise identity-change rejection.
void maybe_wait_for_gate() {
    const char* gate = std::getenv("DIRCOPY_TEST_GATE");
    if (!gate || !*gate) return;
    std::fprintf(stderr, "dircopy: scan complete, waiting for gate '%s'\n", gate);
    std::fflush(stderr);
    for (int i = 0; i < 600; ++i) {
        struct stat st;
        if (::stat(gate, &st) == 0) return;
        std::this_thread::sleep_for(std::chrono::milliseconds(50));
    }
    std::fprintf(stderr, "dircopy: warning: test gate '%s' never appeared\n", gate);
}

} // namespace

int main(int argc, char** argv) {
    std::string src, dst, report_path;
    for (int i = 1; i < argc; ++i) {
        std::string arg = argv[i];
        if (arg == "--report" && i + 1 < argc) {
            report_path = argv[++i];
        } else if (arg == "--help" || arg == "-h") {
            usage(argv[0]);
            return 0;
        } else if (src.empty()) {
            src = arg;
        } else if (dst.empty()) {
            dst = arg;
        } else {
            usage(argv[0]);
            return 1;
        }
    }
    if (src.empty() || dst.empty()) {
        usage(argv[0]);
        return 1;
    }

    struct stat st;
    if (::lstat(dst.c_str(), &st) == 0 || errno != ENOENT) {
        std::fprintf(stderr, "dircopy: destination '%s' already exists\n", dst.c_str());
        return 1;
    }

    // Phase 1: record every in-root (device, inode) before creating anything.
    dircopy::ScanResult scan;
    std::string err;
    if (!dircopy::scan_tree(src, scan, err)) {
        std::fprintf(stderr, "dircopy: scan failed: %s\n", err.c_str());
        return 1;
    }

    maybe_wait_for_gate();

    // Phase 2: create the target tree, reusing the recorded inode set to
    // rebuild hard-link groups strictly inside the copied root.
    dircopy::CopyReport report = dircopy::execute_copy(src, dst, scan);

    std::printf("dirs=%zu files=%zu hardlinks=%zu symlinks=%zu rejected=%zu\n",
                report.dirs_created, report.files_copied, report.hardlinks_created,
                report.symlinks_created, report.rejections.size());

    if (!report.rejections.empty()) {
        auto print = [&](FILE* out) {
            for (const auto& r : report.rejections)
                std::fprintf(out, "REJECT %s: %s\n",
                             r.rel_path.empty() ? "." : r.rel_path.c_str(),
                             r.reason.c_str());
        };
        print(stderr);
        if (!report_path.empty()) {
            if (FILE* f = std::fopen(report_path.c_str(), "w")) {
                print(f);
                std::fclose(f);
            } else {
                std::fprintf(stderr, "dircopy: cannot write report '%s'\n",
                             report_path.c_str());
            }
        }
        return 2;
    }
    return 0;
}
