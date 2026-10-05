#include "checker.h"

#include <cctype>
#include <fstream>
#include <iostream>
#include <stdexcept>
#include <string>
#include <vector>

// Input format (whitespace separated, '#' starts a comment):
//   polygon <n>
//   x1 y1
//   ...            (n lines)
//   triangles <m>
//   ax ay bx by cx cy
//   ...            (m lines)
// Coordinates are exact rationals: integers, decimals (1.25), or fractions
// (p/q with integer p, q != 0).

static std::vector<std::string> tokenize(std::istream& in) {
    std::vector<std::string> toks;
    std::string cur;
    char c;
    while (in.get(c)) {
        if (c == '#') {
            std::string rest;
            std::getline(in, rest);
            if (!cur.empty()) {
                toks.push_back(cur);
                cur.clear();
            }
            continue;
        }
        if (std::isspace(static_cast<unsigned char>(c))) {
            if (!cur.empty()) {
                toks.push_back(cur);
                cur.clear();
            }
        } else {
            cur.push_back(c);
        }
    }
    if (!cur.empty()) toks.push_back(cur);
    return toks;
}

static Rat parseRat(const std::string& tok) {
    size_t slash = tok.find('/');
    if (slash != std::string::npos) {
        BigInt n = BigInt::fromDecimalString(tok.substr(0, slash));
        BigInt d = BigInt::fromDecimalString(tok.substr(slash + 1));
        return Rat(n, d);
    }
    size_t dot = tok.find('.');
    if (dot != std::string::npos) {
        std::string t = tok;
        bool neg = false;
        if (!t.empty() && (t[0] == '-' || t[0] == '+')) {
            neg = t[0] == '-';
            t = t.substr(1);
        }
        dot = t.find('.');
        std::string ip = t.substr(0, dot);
        std::string fp = t.substr(dot + 1);
        if (ip.empty()) ip = "0";
        if (fp.empty()) fp = "0";
        BigInt n = BigInt::fromDecimalString(ip);
        BigInt scale(1);
        for (char c : fp) {
            if (c < '0' || c > '9')
                throw std::runtime_error("bad number: '" + tok + "'");
            n = n * BigInt(10) + BigInt(c - '0');
            scale = scale * BigInt(10);
        }
        if (neg) n = -n;
        return Rat(n, scale);
    }
    return Rat(BigInt::fromDecimalString(tok), BigInt(1));
}

static long parseCount(const std::string& s) {
    size_t used = 0;
    long v = 0;
    try {
        v = std::stol(s, &used);
    } catch (...) {
        throw std::runtime_error("bad count: '" + s + "'");
    }
    if (used != s.size() || v < 0)
        throw std::runtime_error("bad count: '" + s + "'");
    return v;
}

int main(int argc, char** argv) {
    if (argc > 2) {
        std::cerr << "usage: tricheck [input-file]\n";
        return 2;
    }
    std::ifstream file;
    std::istream* in = &std::cin;
    if (argc == 2) {
        file.open(argv[1]);
        if (!file) {
            std::cerr << "error: cannot open " << argv[1] << "\n";
            return 2;
        }
        in = &file;
    }
    try {
        std::vector<std::string> toks = tokenize(*in);
        size_t pos = 0;
        auto next = [&]() -> const std::string& {
            if (pos >= toks.size())
                throw std::runtime_error("unexpected end of input");
            return toks[pos++];
        };
        if (next() != "polygon")
            throw std::runtime_error("expected 'polygon' section");
        long nv = parseCount(next());
        std::vector<Pt> poly(static_cast<size_t>(nv));
        for (auto& p : poly) {
            p.x = parseRat(next());
            p.y = parseRat(next());
        }
        if (next() != "triangles")
            throw std::runtime_error("expected 'triangles' section");
        long nt = parseCount(next());
        std::vector<Tri> tris(static_cast<size_t>(nt));
        for (auto& t : tris)
            for (int k = 0; k < 3; ++k) {
                t.v[k].x = parseRat(next());
                t.v[k].y = parseRat(next());
            }
        if (pos != toks.size())
            throw std::runtime_error("trailing tokens after triangles section");

        CheckResult res = checkTriangulation(poly, tris);
        std::cout << "[info] polygon area = " << res.polygonArea.toString()
                  << ", triangle area sum = "
                  << res.triangleAreaSum.toString() << "\n";
        if (res.valid) {
            std::cout << "VALID: " << nt
                      << " triangles cover the polygon exactly "
                         "(no overlaps, no gaps)\n";
            return 0;
        }
        std::cout << "INVALID: triangulation rejected ("
                  << res.diagnostics.size() << " problem(s)):\n";
        for (const auto& d : res.diagnostics)
            std::cout << "  [" << d.category << "] " << d.detail << "\n";
        return 1;
    } catch (const std::exception& e) {
        std::cerr << "error: " << e.what() << "\n";
        return 2;
    }
}
