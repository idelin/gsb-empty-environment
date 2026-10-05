#include <cstdio>
#include <cstring>
#include <iostream>
#include <string>

#include "dircopy.h"

namespace {

void usage(const char* prog) {
    std::fprintf(stderr,
        "usage: %s [--on-error=skip|abort] [--report FILE] SRC DST\n"
        "\n"
        "Copies the directory tree SRC to DST (which must not exist).\n"
        "Hard-link relationships between files inside SRC are reproduced\n"
        "inside DST; objects outside SRC are never pulled in. Symlinks are\n"
        "copied as symlinks. Items whose identity changes between the scan\n"
        "and copy phases are rejected and listed in the report.\n"
        "\n"
        "exit status: 0 = clean copy, 1 = fatal error, 2 = finished with rejections\n",
        prog);
}

}  // namespace

int main(int argc, char** argv) {
    dircopy::OnError on_error = dircopy::OnError::Skip;
    std::string report_path;
    std::string src, dst;

    for (int i = 1; i < argc; ++i) {
        std::string a = argv[i];
        if (a == "--on-error=skip") {
            on_error = dircopy::OnError::Skip;
        } else if (a == "--on-error=abort") {
            on_error = dircopy::OnError::Abort;
        } else if (a == "--report") {
            if (++i >= argc) { usage(argv[0]); return 1; }
            report_path = argv[i];
        } else if (a.rfind("--report=", 0) == 0) {
            report_path = a.substr(9);
        } else if (a == "-h" || a == "--help") {
            usage(argv[0]);
            return 0;
        } else if (!a.empty() && a[0] == '-') {
            std::fprintf(stderr, "unknown option: %s\n", a.c_str());
            usage(argv[0]);
            return 1;
        } else if (src.empty()) {
            src = a;
        } else if (dst.empty()) {
            dst = a;
        } else {
            usage(argv[0]);
            return 1;
        }
    }
    if (src.empty() || dst.empty()) {
        usage(argv[0]);
        return 1;
    }

    dircopy::CopyReport report;
    bool ok = false;
    try {
        report = dircopy::CopyReport{};
        ok = dircopy::copy_directory(src, dst, report, on_error);
    } catch (const std::exception& e) {
        std::fprintf(stderr, "dircopy: %s\n", e.what());
        return 1;
    }

    const std::string text = dircopy::format_report(report);
    if (!report_path.empty()) {
        if (FILE* f = std::fopen(report_path.c_str(), "w")) {
            std::fwrite(text.data(), 1, text.size(), f);
            std::fclose(f);
        } else {
            std::fprintf(stderr, "dircopy: cannot write report to %s\n",
                         report_path.c_str());
            return 1;
        }
    }
    std::fputs(text.c_str(), stderr);
    return ok ? 0 : 2;
}
