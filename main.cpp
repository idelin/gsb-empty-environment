#include "layout.hpp"

#include <cctype>
#include <fstream>
#include <iostream>
#include <sstream>
#include <stdexcept>
#include <string>
#include <vector>

using namespace slayout;

namespace {

struct ParseError : std::runtime_error {
    using std::runtime_error::runtime_error;
};

struct Token {
    enum Kind { Ident, Number, LBrace, RBrace, LBracket, RBracket, Equal, End } kind = End;
    std::string text;
    uint64_t number = 0;
    int line = 0;
};

std::vector<Token> tokenize(const std::string& src) {
    std::vector<Token> out;
    size_t i = 0;
    int line = 1;
    while (i < src.size()) {
        char c = src[i];
        if (c == '\n') { ++line; ++i; continue; }
        if (std::isspace(static_cast<unsigned char>(c))) { ++i; continue; }
        if (c == '#') { while (i < src.size() && src[i] != '\n') ++i; continue; }
        Token t;
        t.line = line;
        switch (c) {
        case '{': t.kind = Token::LBrace; out.push_back(t); ++i; continue;
        case '}': t.kind = Token::RBrace; out.push_back(t); ++i; continue;
        case '[': t.kind = Token::LBracket; out.push_back(t); ++i; continue;
        case ']': t.kind = Token::RBracket; out.push_back(t); ++i; continue;
        case '=': t.kind = Token::Equal; out.push_back(t); ++i; continue;
        default: break;
        }
        if (std::isalpha(static_cast<unsigned char>(c)) || c == '_') {
            size_t start = i;
            while (i < src.size() &&
                   (std::isalnum(static_cast<unsigned char>(src[i])) || src[i] == '_'))
                ++i;
            t.kind = Token::Ident;
            t.text = src.substr(start, i - start);
            out.push_back(t);
            continue;
        }
        if (std::isdigit(static_cast<unsigned char>(c))) {
            uint64_t base = 10;
            if (c == '0' && i + 1 < src.size() && (src[i + 1] == 'x' || src[i + 1] == 'X')) {
                base = 16;
                i += 2;
            }
            uint64_t value = 0;
            size_t digits = 0;
            while (i < src.size()) {
                char d = src[i];
                uint64_t digit;
                if (std::isdigit(static_cast<unsigned char>(d))) digit = d - '0';
                else if (base == 16 && d >= 'a' && d <= 'f') digit = d - 'a' + 10;
                else if (base == 16 && d >= 'A' && d <= 'F') digit = d - 'A' + 10;
                else break;
                if (value > (UINT64_MAX - digit) / base)
                    throw ParseError("line " + std::to_string(line) + ": integer literal overflow");
                value = value * base + digit;
                ++digits;
                ++i;
            }
            if (digits == 0)
                throw ParseError("line " + std::to_string(line) + ": malformed number");
            t.kind = Token::Number;
            t.number = value;
            out.push_back(t);
            continue;
        }
        throw ParseError("line " + std::to_string(line) +
                         ": unexpected character '" + std::string(1, c) + "'");
    }
    Token end;
    end.kind = Token::End;
    end.line = line;
    out.push_back(end);
    return out;
}

class Parser {
public:
    explicit Parser(std::vector<Token> toks) : toks_(std::move(toks)) {}

    Type parseTopLevel() {
        Type t = parseType();
        expect(Token::End, "end of file");
        return t;
    }

private:
    const Token& peek() const { return toks_[pos_]; }
    const Token& next() { return toks_[pos_++]; }

    ParseError error(const std::string& msg) const {
        return ParseError("line " + std::to_string(peek().line) + ": " + msg);
    }

    void expect(Token::Kind k, const std::string& what) {
        if (peek().kind != k) throw error("expected " + what);
        next();
    }

    std::string expectIdent(const std::string& what) {
        if (peek().kind != Token::Ident) throw error("expected " + what);
        return next().text;
    }

    uint64_t expectNumber() {
        if (peek().kind != Token::Number) throw error("expected a number");
        return next().number;
    }

    uint64_t expectKeyNumber(const std::string& key) {
        if (peek().kind != Token::Ident || peek().text != key)
            throw error("expected '" + key + "=<n>'");
        next();
        expect(Token::Equal, "'='");
        return expectNumber();
    }

    Type maybeArray(Type elem) {
        if (peek().kind != Token::LBracket) return elem;
        next();
        uint64_t n = expectNumber();
        expect(Token::RBracket, "']'");
        Type arr;
        arr.kind = Kind::Array;
        arr.name = elem.name;
        arr.count = n;
        arr.members.push_back(std::move(elem));
        return arr;
    }

    Type parseType() {
        const Token& t = peek();
        if (t.kind != Token::Ident || (t.text != "struct" && t.text != "union"))
            throw error("expected 'struct' or 'union'");
        bool isStruct = t.text == "struct";
        next();
        Type type;
        type.kind = isStruct ? Kind::Struct : Kind::Union;
        type.name = expectIdent("type name");
        if (peek().kind == Token::Ident && (peek().text == "pod" || peek().text == "nonpod")) {
            type.pod = peek().text == "pod";
            next();
        }
        expect(Token::LBrace, "'{'");
        while (peek().kind != Token::RBrace) {
            if (peek().kind == Token::End) throw error("unexpected end of file, expected '}'");
            type.members.push_back(parseMember());
        }
        next();
        return maybeArray(std::move(type));
    }

    Type parseMember() {
        const Token& t = peek();
        if (t.kind == Token::Ident) {
            if (t.text == "field") {
                next();
                Type f;
                f.name = expectIdent("field name");
                f.size = expectKeyNumber("size");
                f.align = expectKeyNumber("align");
                if (peek().kind == Token::Ident && peek().text == "count") {
                    next();
                    expect(Token::Equal, "'='");
                    f.kind = Kind::Array;
                    f.count = expectNumber();
                } else {
                    f.kind = Kind::Scalar;
                }
                return f;
            }
            if (t.text == "pad") {
                next();
                Type p;
                p.kind = Kind::Padding;
                p.name = "<pad>";
                p.size = expectNumber();
                p.align = 1;
                return p;
            }
            if (t.text == "struct" || t.text == "union")
                return parseType();
        }
        throw error("expected member (field / pad / struct / union)");
    }

    std::vector<Token> toks_;
    size_t pos_ = 0;
};

Type parseSpec(const std::string& path) {
    std::ifstream in(path);
    if (!in) throw ParseError("cannot open '" + path + "'");
    std::ostringstream ss;
    ss << in.rdbuf();
    Parser p(tokenize(ss.str()));
    return p.parseTopLevel();
}

void dump(const Layout& l, const std::string& name, int indent) {
    std::string pad(static_cast<size_t>(indent), ' ');
    std::cout << pad << name << ": size=" << l.size << " dsize=" << l.dsize
              << " align=" << l.align << "\n";
    for (size_t i = 0; i < l.fields.size(); ++i) {
        const FieldLayout& f = l.fields[i];
        std::cout << pad << "  +" << f.offset << " " << f.name
                  << " (size=" << f.size << " align=" << f.align << ")\n";
        if (f.kind == Kind::Struct || f.kind == Kind::Union)
            dump(l.children[i], "", indent + 4);
    }
}

int usage(const char* argv0) {
    std::cerr << "usage:\n"
              << "  " << argv0 << " <old.spec> <new.spec>   compare two layouts\n"
              << "  " << argv0 << " --dump <spec>         print computed layout\n";
    return 2;
}

} // namespace

int main(int argc, char** argv) {
    if (argc == 3 && std::string(argv[1]) == "--dump") {
        try {
            Type t = parseSpec(argv[2]);
            Layout l = compute(t);
            if (!l.ok) {
                std::cerr << "layout error: " << l.error << "\n";
                return 2;
            }
            dump(l, t.name, 0);
            return 0;
        } catch (const ParseError& e) {
            std::cerr << "parse error: " << e.what() << "\n";
            return 2;
        }
    }
    if (argc != 3) return usage(argv[0]);
    try {
        Type oldT = parseSpec(argv[1]);
        Type newT = parseSpec(argv[2]);
        Layout oldL = compute(oldT);
        Layout newL = compute(newT);
        if (!oldL.ok) {
            std::cerr << "old layout error: " << oldL.error << "\n";
            return 2;
        }
        if (!newL.ok) {
            std::cerr << "new layout error: " << newL.error << "\n";
            return 2;
        }
        Diff d = firstDiff(oldL, newL, oldT.name);
        std::cout << describe(d) << "\n";
        return d.kind == Diff::Kind::None ? 0 : 1;
    } catch (const ParseError& e) {
        std::cerr << "parse error: " << e.what() << "\n";
        return 2;
    }
}
