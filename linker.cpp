// 简化链接器：符号裁决（强/弱/未定义）+ 归档成员按需加载（迭代到不动点）。
//
// 输入为文本格式的模块描述：
//   普通目标文件（无条件加载）：
//     module <name>
//     def strong <sym>     # 强定义
//     def weak   <sym>     # 弱定义
//     undef      <sym>     # 未定义引用
//   归档文件（成员按需加载）：
//     archive <name>
//     member <name>
//     def strong <sym>
//     undef <sym>
//     end
//     ...
//     end
//
// 裁决规则（仿 ELF）：
//   1. 同一符号出现两个及以上强定义 => 冲突错误，与输入顺序无关。
//   2. 强定义覆盖弱定义；多个弱定义取先出现者。
//   3. 归档成员仅当其强定义能满足当前未定义引用时才被加载
//      （归档中的弱定义不触发加载）。
//   4. 加载新成员可能引入新的未定义引用，迭代直到没有新成员被加载。
//   5. 迭代结束后仍有未定义引用 => 未定义符号错误。
//
// 用法：linker <file1> [file2 ...]   （.o 风格为模块，.a 风格为归档，按内容区分）

#include <cstdlib>
#include <fstream>
#include <iostream>
#include <map>
#include <set>
#include <sstream>
#include <string>
#include <vector>

namespace {

struct Def {
    std::string symbol;
    bool weak = false;
};

struct Module {
    std::string name;
    std::string archive;  // 为空表示独立目标文件
    std::vector<Def> defs;
    std::vector<std::string> undefs;
    bool loaded = false;
    std::string reason;   // 加载原因
};

struct Input {
    std::string path;
    std::vector<Module> modules;  // 目标文件含 1 个模块；归档含若干成员
};

// ---------------- 解析 ----------------

struct ParseError : std::runtime_error {
    using std::runtime_error::runtime_error;
};

Input parseFile(const std::string& path) {
    std::ifstream in(path);
    if (!in) throw ParseError("cannot open " + path);
    Input input;
    input.path = path;

    std::string archiveName;      // 非空表示当前在归档内
    Module* cur = nullptr;
    std::string line;
    int lineno = 0;
    auto fail = [&](const std::string& msg) -> ParseError {
        return ParseError(path + ":" + std::to_string(lineno) + ": " + msg);
    };

    auto finishModule = [&] {
        if (cur) {
            input.modules.push_back(std::move(*cur));
            delete cur;
            cur = nullptr;
        }
    };

    while (std::getline(in, line)) {
        ++lineno;
        auto hash = line.find('#');
        if (hash != std::string::npos) line.erase(hash);
        std::istringstream ss(line);
        std::string kw;
        if (!(ss >> kw)) continue;

        if (kw == "archive") {
            if (!archiveName.empty()) throw fail("nested archive");
            finishModule();
            if (!(ss >> archiveName)) throw fail("archive needs a name");
        } else if (kw == "module") {
            if (!archiveName.empty()) throw fail("use 'member' inside archive");
            finishModule();
            cur = new Module();
            if (!(ss >> cur->name)) throw fail("module needs a name");
        } else if (kw == "member") {
            if (archiveName.empty()) throw fail("'member' outside archive");
            finishModule();
            cur = new Module();
            if (!(ss >> cur->name)) throw fail("member needs a name");
            cur->archive = archiveName;
        } else if (kw == "def") {
            if (!cur) throw fail("'def' outside module/member");
            std::string bind, sym;
            if (!(ss >> bind >> sym)) throw fail("usage: def strong|weak <sym>");
            if (bind == "strong") cur->defs.push_back({sym, false});
            else if (bind == "weak") cur->defs.push_back({sym, true});
            else throw fail("unknown binding '" + bind + "'");
        } else if (kw == "undef") {
            if (!cur) throw fail("'undef' outside module/member");
            std::string sym;
            if (!(ss >> sym)) throw fail("usage: undef <sym>");
            cur->undefs.push_back(sym);
        } else if (kw == "end") {
            if (cur) {
                finishModule();
            } else if (!archiveName.empty()) {
                archiveName.clear();
            } else {
                throw fail("stray 'end'");
            }
        } else {
            throw fail("unknown keyword '" + kw + "'");
        }
    }
    finishModule();
    if (!archiveName.empty())
        throw ParseError(path + ": archive '" + archiveName + "' not closed with 'end'");
    if (input.modules.empty())
        throw ParseError(path + ": no modules found");
    return input;
}

// ---------------- 符号表 ----------------

enum class Kind { None, Weak, Strong };

struct SymInfo {
    Kind kind = Kind::None;
    std::string module;  // 定义该符号的模块（裁决后的来源）
};

const char* kindName(Kind k) {
    switch (k) {
        case Kind::Strong: return "strong";
        case Kind::Weak:   return "weak";
        default:           return "none";
    }
}

std::string moduleLabel(const Module& m) {
    return m.archive.empty() ? m.name : m.archive + "(" + m.name + ")";
}

}  // namespace

int main(int argc, char** argv) {
    if (argc < 2) {
        std::cerr << "usage: " << argv[0] << " <object/archive files...>\n";
        return 2;
    }

    std::vector<Input> inputs;
    try {
        for (int i = 1; i < argc; ++i) inputs.push_back(parseFile(argv[i]));
    } catch (const ParseError& e) {
        std::cerr << "link: parse error: " << e.what() << "\n";
        return 2;
    }

    // 所有模块（目标文件模块 + 归档成员），归档成员保持输入顺序。
    std::vector<Module*> all;
    std::vector<Module*> archiveMembers;
    for (auto& in : inputs)
        for (auto& m : in.modules) {
            all.push_back(&m);
            if (!m.archive.empty()) archiveMembers.push_back(&m);
        }

    // 第 0 步：独立目标文件无条件加载。
    for (auto* m : all)
        if (m->archive.empty()) {
            m->loaded = true;
            m->reason = "explicit object file (loaded unconditionally)";
        }

    std::map<std::string, SymInfo> table;                     // 符号裁决结果
    std::map<std::string, std::vector<std::string>> strongDefs;  // 符号 -> 所有强定义来源
    std::map<std::string, std::vector<std::string>> refs;     // 未定义符号 -> 引用它的模块

    // 把新加载模块的定义并入符号表。强定义冲突全部记录，不因顺序而掩盖。
    auto absorbDefs = [&](Module* m) {
        for (const auto& d : m->defs) {
            auto& info = table[d.symbol];
            if (!d.weak) {
                strongDefs[d.symbol].push_back(moduleLabel(*m));
                if (info.kind != Kind::Strong) info.module = moduleLabel(*m);
                info.kind = Kind::Strong;  // 强定义总是覆盖弱定义
            } else if (info.kind == Kind::None) {
                info.kind = Kind::Weak;    // 首个弱定义生效，后续弱定义忽略
                info.module = moduleLabel(*m);
            }
        }
    };

    for (auto* m : all)
        if (m->loaded) absorbDefs(m);

    // 按需加载归档成员，迭代到不动点。
    int iteration = 0;
    bool progress = true;
    while (progress) {
        progress = false;
        ++iteration;

        // 收集当前未定义引用：被已加载模块引用、但尚无定义的符号。
        refs.clear();
        for (auto* m : all) {
            if (!m->loaded) continue;
            for (const auto& u : m->undefs)
                if (table.count(u) == 0) refs[u].push_back(moduleLabel(*m));
        }
        if (refs.empty()) break;

        // 按输入顺序扫描归档成员：强定义了某个未定义符号 => 加载。
        for (auto* m : archiveMembers) {
            if (m->loaded) continue;
            for (const auto& d : m->defs) {
                if (!d.weak && refs.count(d.symbol)) {
                    m->loaded = true;
                    m->reason = "extracted from archive to resolve '" + d.symbol +
                                "' (referenced by " + refs[d.symbol].front() +
                                (refs[d.symbol].size() > 1 ? " and others" : "") +
                                "), pass " + std::to_string(iteration);
                    absorbDefs(m);
                    progress = true;
                    break;  // 一个成员只需一个加载理由
                }
            }
        }
    }

    // 汇总结果。
    bool ok = true;

    std::cout << "== Loaded modules ==\n";
    for (auto* m : all)
        if (m->loaded)
            std::cout << "  " << moduleLabel(*m) << "\n      reason: " << m->reason << "\n";

    std::cout << "== Not loaded (archive members) ==\n";
    for (auto* m : all)
        if (!m->loaded)
            std::cout << "  " << moduleLabel(*m) << "  (no strong definition needed)\n";

    std::cout << "== Symbol resolution ==\n";
    for (const auto& [sym, info] : table)
        std::cout << "  " << sym << ": " << kindName(info.kind) << " definition from "
                  << info.module << "\n";

    if (!strongDefs.empty()) {
        for (const auto& [sym, mods] : strongDefs) {
            if (mods.size() > 1) {
                ok = false;
                std::cerr << "link: error: multiple strong definitions of '" << sym << "':";
                for (const auto& m : mods) std::cerr << " " << m;
                std::cerr << "\n";
            }
        }
    }

    if (!refs.empty()) {
        ok = false;
        for (const auto& [sym, users] : refs) {
            std::cerr << "link: error: undefined reference to '" << sym << "' (referenced by";
            for (const auto& u : users) std::cerr << " " << u;
            std::cerr << ")\n";
        }
    }

    if (ok) std::cout << "link: success\n";
    return ok ? 0 : 1;
}
