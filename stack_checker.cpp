// 栈历史检查器：判断一段并发栈历史是否可线性化。
//
// 输入（stdin）：每行一个事件，按真实时间先后排列。
//   call <id> push <addr>:<gen>   -- 压栈（写）
//   call <id> pop                 -- 弹栈（读改写）
//   call <id> top                 -- 读栈顶（读）
//   ret  <id> ok                  -- push 的返回
//   ret  <id> empty               -- pop/top 返回空
//   ret  <id> <addr>:<gen>        -- pop/top 返回的值
// 允许操作未返回（pending）。操作数 <= 20。'#' 之后为注释。
//
// 输出：
//   可线性化     -> LINEARIZABLE 及一个合法线性化顺序；
//   不可线性化   -> NOT LINEARIZABLE 及事件流的最短矛盾前缀。
//
// 值带 (地址, 代次) 标签以抵御 ABA：地址复用时代次递增。
// 选项 --ignore-gen 模拟“只看地址”的朴素检查器，用于暴露旧地址误认。

#include <cstdint>
#include <cstdlib>
#include <array>
#include <iostream>
#include <sstream>
#include <string>
#include <unordered_map>
#include <unordered_set>
#include <vector>

namespace {

bool g_ignoreGen = false;

struct Value {
    std::string addr;
    long long gen = 0;
};

std::string valueStr(const Value& v) {
    return v.addr + ":" + std::to_string(v.gen);
}

bool valueEq(const Value& a, const Value& b) {
    if (a.addr != b.addr) return false;
    return g_ignoreGen || a.gen == b.gen;
}

enum class Kind { Push, Pop, Top };

const char* kindName(Kind k) {
    switch (k) {
        case Kind::Push: return "push";
        case Kind::Pop:  return "pop";
        case Kind::Top:  return "top";
    }
    return "?";
}

struct Op {
    int id = -1;
    Kind kind = Kind::Push;
    Value arg{};            // push 的参数
    bool returned = false;  // 是否有返回事件
    bool retEmpty = false;  // pop/top 返回 empty
    Value retValue{};       // pop/top 返回的值
    int callPos = -1;       // 调用事件在事件流中的下标
    int retPos = -1;        // 返回事件下标（pending 为 -1）
};

std::string describe(const Op& op) {
    std::ostringstream ss;
    ss << "op" << op.id << " " << kindName(op.kind);
    if (op.kind == Kind::Push) ss << "(" << valueStr(op.arg) << ")";
    if (!op.returned) ss << "  [pending]";
    else if (op.kind == Kind::Push) ss << " -> ok";
    else if (op.retEmpty) ss << " -> empty";
    else ss << " -> " << valueStr(op.retValue);
    return ss.str();
}

struct History {
    std::vector<std::string> lines;  // 事件原文（用于打印前缀）
    std::vector<Op> ops;             // 按首次调用顺序
};

// ---------------- 解析 ----------------

bool parseValue(const std::string& tok, Value& v) {
    auto p = tok.find(':');
    if (p == std::string::npos) {
        v.addr = tok;
        v.gen = 0;
    } else {
        v.addr = tok.substr(0, p);
        try {
            v.gen = std::stoll(tok.substr(p + 1));
        } catch (...) {
            return false;
        }
    }
    return !v.addr.empty();
}

std::vector<std::string> tokenize(const std::string& line) {
    std::istringstream ss(line);
    std::vector<std::string> toks;
    std::string t;
    while (ss >> t) toks.push_back(t);
    return toks;
}

bool parseHistory(std::istream& in, History& h, std::string& err) {
    std::unordered_map<int, int> byId;
    std::string raw;
    int pos = 0;
    while (std::getline(in, raw)) {
        auto hash = raw.find('#');
        std::string line = hash == std::string::npos ? raw : raw.substr(0, hash);
        auto toks = tokenize(line);
        if (toks.empty()) continue;

        if (toks[0] == "call") {
            if (toks.size() < 3) { err = "第 " + std::to_string(pos) + " 个事件：call 缺少参数"; return false; }
            int id;
            try { id = std::stoi(toks[1]); } catch (...) { err = "非法操作 id: " + toks[1]; return false; }
            if (byId.count(id)) { err = "操作 " + std::to_string(id) + " 重复调用"; return false; }
            Op op;
            op.id = id;
            op.callPos = pos;
            const std::string& kw = toks[2];
            if (kw == "push") {
                if (toks.size() < 4 || !parseValue(toks[3], op.arg)) {
                    err = "push 需要参数 <addr>:<gen>"; return false;
                }
                op.kind = Kind::Push;
            } else if (kw == "pop") {
                op.kind = Kind::Pop;
            } else if (kw == "top") {
                op.kind = Kind::Top;
            } else {
                err = "未知操作类型: " + kw; return false;
            }
            byId[id] = (int)h.ops.size();
            h.ops.push_back(op);
        } else if (toks[0] == "ret") {
            if (toks.size() < 3) { err = "第 " + std::to_string(pos) + " 个事件：ret 缺少参数"; return false; }
            int id;
            try { id = std::stoi(toks[1]); } catch (...) { err = "非法操作 id: " + toks[1]; return false; }
            auto it = byId.find(id);
            if (it == byId.end()) { err = "操作 " + std::to_string(id) + " 未调用先返回"; return false; }
            Op& op = h.ops[it->second];
            if (op.returned) { err = "操作 " + std::to_string(id) + " 重复返回"; return false; }
            op.returned = true;
            op.retPos = pos;
            if (op.kind == Kind::Push) {
                if (toks[2] != "ok") { err = "push 的返回值应为 ok"; return false; }
            } else if (toks[2] == "empty") {
                op.retEmpty = true;
            } else if (!parseValue(toks[2], op.retValue)) {
                err = "非法返回值: " + toks[2]; return false;
            }
        } else {
            err = "未知事件: " + toks[0]; return false;
        }

        // 事件原文（去掉注释后的规整形式）
        std::string norm;
        for (size_t i = 0; i < toks.size(); ++i) norm += (i ? " " : "") + toks[i];
        h.lines.push_back(norm);
        ++pos;
    }
    if (h.ops.size() > 20) { err = "操作数超过 20（当前 " + std::to_string(h.ops.size()) + "）"; return false; }
    return true;
}

// ---------------- 线性化搜索 ----------------

struct Checker {
    std::vector<Op> ops;
    std::vector<uint32_t> pred;   // pred[j]：必须先于 j 线性化的操作集合
    uint32_t completedMask = 0;   // 已返回（必须出现在线性化中）的操作
    std::vector<int> order;

    // 值压缩为小整数 id，匹配表预先算好（含 --ignore-gen 语义）
    std::vector<Value> vals;
    std::vector<std::vector<char>> match;  // match[栈值id][结果值id]
    std::vector<int> argId;                // 每个操作的 push 参数 id（-1 无）
    std::vector<int> retId;                // 每个操作的返回值 id（-1 无）

    static constexpr int kMaxOps = 20;

    // 定长状态键：避免堆分配
    struct StateKey {
        uint32_t mask;
        uint8_t depth;
        std::array<uint16_t, kMaxOps> st;
        bool operator==(const StateKey& o) const {
            return mask == o.mask && depth == o.depth &&
                   std::equal(st.begin(), st.begin() + depth, o.st.begin());
        }
    };
    struct KeyHash {
        size_t operator()(const StateKey& k) const {
            size_t h = k.mask * 2654435761u;
            for (int i = 0; i < k.depth; ++i) h = h * 1315423911u + k.st[i];
            return h;
        }
    };
    std::unordered_set<StateKey, KeyHash> dead;  // 已确认失败的状态 (mask, 栈内容)
    unsigned long long states = 0;               // 统计：探索的状态数

    // 栈用定长数组维护，depth 为栈深
    bool dfs(uint32_t mask, std::array<uint16_t, kMaxOps>& st, int depth) {
        if ((mask & completedMask) == completedMask) return true;
        ++states;
        StateKey key;
        key.mask = mask;
        key.depth = (uint8_t)depth;
        std::copy(st.begin(), st.begin() + depth, key.st.begin());
        if (dead.count(key)) return false;
        int n = (int)ops.size();
        for (int j = 0; j < n; ++j) {
            if (mask >> j & 1u) continue;
            if (pred[j] & ~mask) continue;  // 前驱未全部就位
            const Op& op = ops[j];
            uint32_t nmask = mask | (1u << j);
            bool ok = false;
            switch (op.kind) {
                case Kind::Push:
                    st[depth] = (uint16_t)argId[j];
                    order.push_back(j);
                    ok = dfs(nmask, st, depth + 1);
                    if (!ok) order.pop_back();
                    break;
                case Kind::Pop:
                    if (!op.returned) {
                        // pending pop：结果未知，可弹任意栈顶（空栈则视为将返回 empty）
                        order.push_back(j);
                        ok = dfs(nmask, st, depth > 0 ? depth - 1 : 0);
                        if (!ok) order.pop_back();
                    } else if (op.retEmpty) {
                        if (depth == 0) {
                            order.push_back(j);
                            ok = dfs(nmask, st, depth);
                            if (!ok) order.pop_back();
                        }
                    } else if (depth > 0 && match[st[depth - 1]][retId[j]]) {
                        order.push_back(j);
                        ok = dfs(nmask, st, depth - 1);
                        if (!ok) order.pop_back();
                    }
                    break;
                case Kind::Top:
                    if (!op.returned) {
                        order.push_back(j);
                        ok = dfs(nmask, st, depth);
                        if (!ok) order.pop_back();
                    } else if (op.retEmpty) {
                        if (depth == 0) {
                            order.push_back(j);
                            ok = dfs(nmask, st, depth);
                            if (!ok) order.pop_back();
                        }
                    } else if (depth > 0 && match[st[depth - 1]][retId[j]]) {
                        order.push_back(j);
                        ok = dfs(nmask, st, depth);
                        if (!ok) order.pop_back();
                    }
                    break;
            }
            if (ok) return true;
        }
        dead.insert(std::move(key));
        return false;
    }

    bool run(std::vector<int>& lin) {
        int n = (int)ops.size();
        pred.assign(n, 0);
        completedMask = 0;
        for (int i = 0; i < n; ++i)
            if (ops[i].returned) completedMask |= 1u << i;
        // 实时序：i 的返回早于 j 的调用 => i 必须先线性化
        for (int i = 0; i < n; ++i)
            if (ops[i].returned)
                for (int j = 0; j < n; ++j)
                    if (ops[i].retPos < ops[j].callPos) pred[j] |= 1u << i;
        // 值 -> 小整数 id
        std::unordered_map<std::string, int> idOf;
        auto intern = [&](const Value& v) {
            std::string k = g_ignoreGen ? v.addr
                                        : v.addr + "#" + std::to_string(v.gen);
            auto it = idOf.find(k);
            if (it != idOf.end()) return it->second;
            int id = (int)vals.size();
            idOf[k] = id;
            vals.push_back(v);
            return id;
        };
        vals.clear();
        argId.assign(n, -1);
        retId.assign(n, -1);
        for (int i = 0; i < n; ++i) {
            if (ops[i].kind == Kind::Push) argId[i] = intern(ops[i].arg);
            if (ops[i].returned && ops[i].kind != Kind::Push && !ops[i].retEmpty)
                retId[i] = intern(ops[i].retValue);
        }
        match.assign(vals.size(), std::vector<char>(vals.size(), 0));
        for (size_t a = 0; a < vals.size(); ++a)
            for (size_t b = 0; b < vals.size(); ++b)
                match[a][b] = valueEq(vals[a], vals[b]) ? 1 : 0;
        // 必要条件预检（O(n)，可快速拒绝一大类矛盾）：
        // 每个值被已返回 pop 弹出的次数不能超过它被压入的次数；
        // 已返回 top 读到的值至少被压入过一次。
        {
            std::vector<int> pushes(vals.size(), 0), pops(vals.size(), 0),
                tops(vals.size(), 0);
            for (int i = 0; i < n; ++i) {
                if (argId[i] >= 0) pushes[argId[i]]++;
                if (retId[i] >= 0) {
                    if (ops[i].kind == Kind::Pop) pops[retId[i]]++;
                    else tops[retId[i]]++;
                }
            }
            for (size_t v = 0; v < vals.size(); ++v)
                if (pops[v] > pushes[v] || (tops[v] > 0 && pushes[v] == 0))
                    return false;
        }
        std::array<uint16_t, kMaxOps> st{};
        order.clear();
        dead.clear();
        states = 0;
        if (!dfs(0, st, 0)) return false;
        lin = order;
        return true;
    }
};

// 取事件流前 k 个事件构成的子历史：调用落在前缀内的操作保留，
// 返回事件也落在前缀内才算已返回，否则视为 pending。
Checker subChecker(const History& h, size_t k) {
    Checker c;
    for (const auto& op : h.ops) {
        if (op.callPos < 0 || (size_t)op.callPos >= k) continue;
        Op o = op;
        if (o.retPos < 0 || (size_t)o.retPos >= k) {
            o.returned = false;
            o.retPos = -1;
            o.retEmpty = false;
        }
        c.ops.push_back(o);
    }
    return c;
}

void printUsage(const char* prog) {
    std::cerr << "用法: " << prog << " [--ignore-gen] < history.txt\n"
              << "  --ignore-gen  忽略代次只比对地址（朴素模式，用于演示旧地址误认）\n";
}

}  // namespace

int main(int argc, char** argv) {
    for (int i = 1; i < argc; ++i) {
        std::string a = argv[i];
        if (a == "--ignore-gen") g_ignoreGen = true;
        else if (a == "--help" || a == "-h") { printUsage(argv[0]); return 0; }
        else { std::cerr << "未知参数: " << a << "\n"; printUsage(argv[0]); return 2; }
    }

    History h;
    std::string err;
    if (!parseHistory(std::cin, h, err)) {
        std::cerr << "INPUT ERROR: " << err << "\n";
        return 2;
    }
    if (h.ops.empty()) {
        std::cout << "LINEARIZABLE\n（空历史）\n";
        return 0;
    }
    if (g_ignoreGen)
        std::cout << "[朴素模式] 忽略代次，仅按地址比对值\n";

    bool showStats = std::getenv("STACK_CHECKER_STATS") != nullptr;
    auto check = [&](size_t k, std::vector<int>& lin) {
        Checker c = subChecker(h, k);
        bool ok = c.run(lin);
        if (showStats)
            std::cerr << "[stats] 前缀 " << k << ": " << c.states << " 状态, "
                      << (ok ? "可线性化" : "矛盾") << "\n";
        return ok;
    };

    // 完整历史检查
    {
        std::vector<int> lin;
        if (check(h.lines.size(), lin)) {
            std::cout << "LINEARIZABLE\n一个合法线性化顺序：\n";
            Checker c = subChecker(h, h.lines.size());
            for (size_t i = 0; i < lin.size(); ++i)
                std::cout << "  " << (i + 1) << ". " << describe(c.ops[lin[i]]) << "\n";
            return 0;
        }
    }

    // 不可线性化：非线性化性对前缀单调，二分找最短矛盾前缀
    std::cout << "NOT LINEARIZABLE\n";
    size_t m = h.lines.size();
    size_t lo = 1, hi = m;  // hi 已知矛盾
    while (lo < hi) {
        size_t mid = lo + (hi - lo) / 2;
        std::vector<int> lin;
        if (!check(mid, lin)) hi = mid;
        else lo = mid + 1;
    }
    size_t k = lo;
    std::cout << "最短矛盾前缀：前 " << k << " 个事件\n";
    for (size_t i = 0; i < k; ++i)
        std::cout << "  " << (i + 1) << ": " << h.lines[i] << "\n";
    std::cout << "（前 " << (k - 1) << " 个事件仍可线性化；第 " << k
              << " 个事件加入后不存在合法线性化顺序）\n";
    return 1;
}
