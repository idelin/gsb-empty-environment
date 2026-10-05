// 简化链接器：符号裁决 + 归档按需加载（迭代到不动点）
//
// 输入格式（行式文本，# 开头为注释）：
//   object <name>          开始一个普通目标模块（总是加载）
//   archive <name>         开始一个归档（成员按需加载）
//   member <name>          开始归档内的一个成员
//   end                    结束当前 archive
//   strong <sym>           强定义
//   weak <sym>             弱定义
//   undef <sym>            强未定义引用（必须被解析，可触发归档加载）
//   weakundef <sym>        弱未定义引用（允许悬空为 0，不触发归档加载）
//
// 裁决规则：
//   1. 强定义唯一：同一符号出现多个强定义 => 冲突错误（与输入顺序无关，
//      收集全部强定义来源后统一报告，不会被先出现者掩盖）。
//   2. 强定义优先于弱定义；多个弱定义时按模块加载顺序取第一个。
//   3. 强未定义引用若在当前已加载模块中无定义，则按归档顺序扫描成员，
//      加载第一个定义该符号的成员；新成员可能引入新的未定义引用，
//      迭代直到不动点（无新成员被加载）。
//   4. 弱未定义引用不触发归档加载，解析不到时记为 0。
//   5. 已有定义（含弱定义）的符号不会再从归档提取成员。

#include <cstdlib>
#include <fstream>
#include <iostream>
#include <map>
#include <set>
#include <sstream>
#include <string>
#include <vector>

namespace {

enum class DefKind { None, Weak, Strong };

struct Module {
    std::string name;          // 显示名，如 "main.o" 或 "libx.a(mem1.o)"
    bool fromArchive = false;
    std::set<std::string> strongDefs;
    std::set<std::string> weakDefs;
    std::set<std::string> undefs;      // 强未定义引用
    std::set<std::string> weakUndefs;  // 弱未定义引用
};

struct Input {
    std::vector<Module> objects;             // 普通目标模块，按输入顺序
    std::vector<std::vector<Module>> archives; // 每个归档的成员，按输入顺序
};

struct DefInfo {
    DefKind kind = DefKind::None;
    std::string module;  // 来源模块显示名
};

Input parseInput(std::istream& in) {
    Input input;
    Module* cur = nullptr;
    std::vector<Module>* curArchive = nullptr;
    std::string curArchiveName;
    std::string line;
    int lineno = 0;
    auto fail = [&](const std::string& msg) {
        std::cerr << "parse error (line " << lineno << "): " << msg << "\n";
        std::exit(2);
    };
    while (std::getline(in, line)) {
        ++lineno;
        auto hash = line.find('#');
        if (hash != std::string::npos) line = line.substr(0, hash);
        std::istringstream ss(line);
        std::string cmd, arg;
        if (!(ss >> cmd)) continue;
        ss >> arg;
        if (cmd == "object") {
            if (arg.empty()) fail("object requires a name");
            input.objects.push_back(Module{});
            input.objects.back().name = arg;
            cur = &input.objects.back();
            curArchive = nullptr;
        } else if (cmd == "archive") {
            if (arg.empty()) fail("archive requires a name");
            input.archives.push_back({});
            curArchive = &input.archives.back();
            curArchiveName = arg;
            cur = nullptr;
        } else if (cmd == "member") {
            if (!curArchive) fail("member outside archive");
            if (arg.empty()) fail("member requires a name");
            curArchive->push_back(Module{});
            curArchive->back().name = curArchiveName + "(" + arg + ")";
            curArchive->back().fromArchive = true;
            cur = &curArchive->back();
        } else if (cmd == "end") {
            curArchive = nullptr;
            cur = nullptr;
        } else if (cmd == "strong" || cmd == "weak" || cmd == "undef" ||
                   cmd == "weakundef") {
            if (!cur) fail(cmd + " outside module/member");
            if (arg.empty()) fail(cmd + " requires a symbol");
            if (cmd == "strong") cur->strongDefs.insert(arg);
            else if (cmd == "weak") cur->weakDefs.insert(arg);
            else if (cmd == "undef") cur->undefs.insert(arg);
            else cur->weakUndefs.insert(arg);
        } else {
            fail("unknown directive: " + cmd);
        }
    }
    return input;
}

class Linker {
public:
    explicit Linker(Input input) : input_(std::move(input)) {}

    int link() {
        // 第 1 步：加载全部普通目标模块。
        for (const auto& m : input_.objects) loadModule(m, "command line object");

        // 第 2 步：归档按需加载，迭代到不动点。
        int pass = 0;
        bool progress = true;
        while (progress) {
            progress = false;
            ++pass;
            // 快照当前未定义符号集合，保证本趟扫描顺序确定。
            std::vector<std::string> pending(undefSyms_.begin(), undefSyms_.end());
            for (const auto& sym : pending) {
                if (defined_.count(sym)) continue;  // 本趟已被解析
                const Module* mem = findArchiveMember(sym);
                if (!mem) continue;
                std::string reason = "pass " + std::to_string(pass) +
                                     ": defines '" + sym + "', referenced by " +
                                     referrer_[sym];
                loadModule(*mem, reason);
                progress = true;
            }
        }

        report();
        return hasError_ ? 1 : 0;
    }

private:
    void loadModule(const Module& m, const std::string& reason) {
        loadTrace_.push_back("load " + m.name + "  (" + reason + ")");
        loaded_.insert(m.name);

        for (const auto& s : m.strongDefs) {
            strongDefSources_[s].push_back(m.name);  // 全部记录，冲突统一裁决
            define(s, DefKind::Strong, m.name);
        }
        for (const auto& s : m.weakDefs) define(s, DefKind::Weak, m.name);

        for (const auto& s : m.undefs) {
            if (!referrer_.count(s)) referrer_[s] = m.name;
            if (!defined_.count(s)) undefSyms_.insert(s);
        }
        for (const auto& s : m.weakUndefs) {
            if (!referrer_.count(s)) referrer_[s] = m.name;
            weakUndefSyms_.insert(s);
        }
        // 本模块的新定义可能消解既有未定义引用。
        for (const auto& s : m.strongDefs) undefSyms_.erase(s);
        for (const auto& s : m.weakDefs) undefSyms_.erase(s);
    }

    void define(const std::string& sym, DefKind kind, const std::string& mod) {
        auto it = defined_.find(sym);
        if (it == defined_.end()) {
            defined_[sym] = DefInfo{kind, mod};
            return;
        }
        // 强优先于弱；同级（弱-弱）保留先到者；强-强冲突在 report 中统一处理。
        if (it->second.kind == DefKind::Weak && kind == DefKind::Strong)
            it->second = DefInfo{kind, mod};
    }

    const Module* findArchiveMember(const std::string& sym) const {
        for (const auto& arch : input_.archives)
            for (const auto& mem : arch) {
                if (loaded_.count(mem.name)) continue;
                if (mem.strongDefs.count(sym) || mem.weakDefs.count(sym))
                    return &mem;
            }
        return nullptr;
    }

    void report() {
        std::cout << "== Load trace ==\n";
        for (const auto& t : loadTrace_) std::cout << "  " << t << "\n";

        // 强定义冲突：收集所有来源，排序后统一报告，输入顺序不能掩盖。
        std::vector<std::string> conflicts;
        for (const auto& [sym, srcs] : strongDefSources_) {
            if (srcs.size() > 1) {
                std::string msg = "multiple strong definitions of '" + sym + "':";
                for (const auto& s : srcs) msg += " " + s;
                conflicts.push_back(msg);
            }
        }

        std::cout << "\n== Symbol table ==\n";
        std::set<std::string> all;
        for (const auto& [s, _] : defined_) all.insert(s);
        all.insert(undefSyms_.begin(), undefSyms_.end());
        all.insert(weakUndefSyms_.begin(), weakUndefSyms_.end());
        for (const auto& sym : all) {
            auto it = defined_.find(sym);
            if (it != defined_.end()) {
                const char* k = it->second.kind == DefKind::Strong ? "strong" : "weak";
                std::cout << "  " << sym << " : defined (" << k << "), from "
                          << it->second.module << "\n";
            } else if (undefSyms_.count(sym)) {
                std::cout << "  " << sym << " : UNDEFINED (referenced by "
                          << referrer_[sym] << ")\n";
            } else {
                std::cout << "  " << sym
                          << " : undefined weak -> 0 (referenced by "
                          << referrer_[sym] << ")\n";
            }
        }

        if (!conflicts.empty()) {
            std::cout << "\n== Errors: strong definition conflicts ==\n";
            for (const auto& c : conflicts) std::cout << "  error: " << c << "\n";
            hasError_ = true;
        }
        if (!undefSyms_.empty()) {
            std::cout << "\n== Errors: undefined references ==\n";
            for (const auto& s : undefSyms_)
                std::cout << "  error: undefined reference to '" << s
                          << "' (referenced by " << referrer_[s] << ")\n";
            hasError_ = true;
        }
        if (!hasError_) std::cout << "\nlink succeeded.\n";
    }

    Input input_;
    std::map<std::string, DefInfo> defined_;                  // 符号裁决结果
    std::map<std::string, std::vector<std::string>> strongDefSources_;  // 全部强定义来源
    std::set<std::string> undefSyms_;                         // 未解析的强未定义引用
    std::set<std::string> weakUndefSyms_;                     // 弱未定义引用
    std::map<std::string, std::string> referrer_;             // 符号 -> 首个引用者
    std::set<std::string> loaded_;                            // 已加载模块名
    std::vector<std::string> loadTrace_;                      // 加载轨迹（含原因）
    bool hasError_ = false;
};

}  // namespace

int main(int argc, char** argv) {
    Input input;
    if (argc > 1) {
        std::ifstream f(argv[1]);
        if (!f) {
            std::cerr << "cannot open " << argv[1] << "\n";
            return 2;
        }
        input = parseInput(f);
    } else {
        input = parseInput(std::cin);
    }
    Linker linker(std::move(input));
    return linker.link();
}
