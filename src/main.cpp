// Command-line driver for the instruction selector.
// Input format (see README.md):
//   classes K
//   copy FROM TO COST
//   node ID OP C1 C2 ...        (IDs assigned in order 0..N-1, operands < ID)
//   output NODE CLASS
//   pattern ID COST RESULTCLASS (op (op ?0 ...) ?1 ...)
#include "isel/selector.h"

#include <cctype>
#include <iostream>
#include <sstream>
#include <stdexcept>
#include <string>

using namespace isel;

namespace {

struct PatternParser {
    const std::string& s;
    size_t pos = 0;
    Pattern& pat;

    void skipws() {
        while (pos < s.size() && std::isspace(static_cast<unsigned char>(s[pos]))) ++pos;
    }
    std::string parseSym() {
        skipws();
        size_t start = pos;
        while (pos < s.size()) {
            char c = s[pos];
            if (std::isspace(static_cast<unsigned char>(c)) || c == '(' || c == ')') break;
            ++pos;
        }
        if (start == pos) throw std::runtime_error("expected symbol in pattern");
        return s.substr(start, pos - start);
    }
    int parseNode() {
        skipws();
        if (pos >= s.size()) throw std::runtime_error("unexpected end of pattern");
        if (s[pos] == '(') {
            ++pos;
            PatternNode pn;
            pn.placeholder = false;
            pn.op = parseSym();
            int idx = static_cast<int>(pat.nodes.size());
            pat.nodes.push_back(std::move(pn));
            while (true) {
                skipws();
                if (pos >= s.size()) throw std::runtime_error("missing ')' in pattern");
                if (s[pos] == ')') { ++pos; break; }
                const int child = parseNode(); // may reallocate pat.nodes
                pat.nodes[idx].children.push_back(child);
            }
            return idx;
        }
        if (s[pos] == '?') {
            ++pos;
            skipws();
            size_t start = pos;
            while (pos < s.size() && std::isdigit(static_cast<unsigned char>(s[pos]))) ++pos;
            if (start == pos) throw std::runtime_error("expected class id after '?'");
            PatternNode pn;
            pn.placeholder = true;
            pn.regClass = std::stoi(s.substr(start, pos - start));
            pat.nodes.push_back(std::move(pn));
            return static_cast<int>(pat.nodes.size()) - 1;
        }
        // Bare symbol: nullary internal op (e.g. a constant or a leaf load).
        PatternNode pn;
        pn.placeholder = false;
        pn.op = parseSym();
        pat.nodes.push_back(std::move(pn));
        return static_cast<int>(pat.nodes.size()) - 1;
    }
};

Pattern parsePattern(const std::string& text) {
    Pattern pat;
    PatternParser parser{text, 0, pat};
    parser.parseNode();
    parser.skipws();
    if (parser.pos != text.size()) throw std::runtime_error("trailing garbage in pattern");
    return pat;
}

} // namespace

int main() {
    Problem prob;
    bool haveClasses = false;
    std::string line;
    int lineNo = 0;
    try {
        while (std::getline(std::cin, line)) {
            ++lineNo;
            const size_t hash = line.find('#');
            if (hash != std::string::npos) line.erase(hash);
            std::istringstream iss(line);
            std::string kw;
            if (!(iss >> kw)) continue;
            if (kw == "classes") {
                if (!(iss >> prob.numClasses) || prob.numClasses <= 0)
                    throw std::runtime_error("bad classes count");
                prob.copyCost.assign(prob.numClasses,
                                     std::vector<long long>(prob.numClasses, kInfCost));
                for (int i = 0; i < prob.numClasses; ++i) prob.copyCost[i][i] = 0;
                haveClasses = true;
            } else if (kw == "copy") {
                int from, to;
                long long cost;
                if (!haveClasses || !(iss >> from >> to >> cost))
                    throw std::runtime_error("bad copy entry");
                prob.copyCost.at(from).at(to) = cost;
            } else if (kw == "node") {
                int id;
                std::string op;
                if (!(iss >> id >> op)) throw std::runtime_error("bad node entry");
                if (id != static_cast<int>(prob.nodes.size()))
                    throw std::runtime_error("node ids must be assigned in order");
                Node n;
                n.op = op;
                int c;
                while (iss >> c) n.operands.push_back(c);
                prob.nodes.push_back(std::move(n));
            } else if (kw == "output") {
                int n, rc;
                if (!(iss >> n >> rc)) throw std::runtime_error("bad output entry");
                prob.outputs.emplace_back(n, rc);
            } else if (kw == "pattern") {
                if (!haveClasses) throw std::runtime_error("classes must come first");
                int id, rc;
                long long cost;
                if (!(iss >> id >> cost >> rc)) throw std::runtime_error("bad pattern header");
                std::string rest;
                std::getline(iss, rest);
                Pattern pat = parsePattern(rest);
                pat.id = id;
                pat.cost = cost;
                pat.resultClass = rc;
                prob.patterns.push_back(std::move(pat));
            } else if (kw == "nodes" || kw == "outputs" || kw == "patterns") {
                // Advisory counts; ignored.
            } else {
                throw std::runtime_error("unknown keyword: " + kw);
            }
        }
    } catch (const std::exception& e) {
        std::cerr << "parse error on line " << lineNo << ": " << e.what() << "\n";
        return 1;
    }

    Solution sol = select(prob);
    if (!sol.feasible) {
        std::cout << "infeasible\n";
        return 0;
    }
    std::cout << "cost " << sol.totalCost << "\n";
    std::cout << "solutions " << sol.numOptimal;
    if (sol.numOptimal == kCountCap) std::cout << " (saturated)";
    std::cout << "\n";
    for (size_t i = 0; i < sol.cover.size(); ++i) {
        const NodeCover& cov = sol.cover[i];
        std::cout << "node " << i << ":";
        if (!cov.used) {
            std::cout << " unused\n";
        } else if (cov.isTileRoot) {
            std::cout << " root pattern=" << cov.patternId << " class=" << cov.resultClass
                      << "\n";
        } else {
            std::cout << " internal root=" << cov.tileRoot << " pattern=" << cov.patternId
                      << "\n";
        }
    }
    return 0;
}
