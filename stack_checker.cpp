// 栈历史检查器 (stack history linearizability checker), C++17
//
// 输入历史由三类事件组成（每行一个，'#' 之后为注释）：
//   call <id> push <addr>:<gen>   -- 写操作（入栈）
//   call <id> pop                 -- 读改写操作（弹出栈顶）
//   call <id> peek                -- 读操作（读栈顶，不弹出）
//   ret  <id> ok                  -- push 的返回
//   ret  <id> empty               -- pop/peek 返回“空栈”
//   ret  <id> <addr>:<gen>        -- pop/peek 返回栈顶值
//
// 值带 (地址, 复用代次)：地址被内存分配器复用时代次递增，
// 只有 (addr, gen) 完全相同才视为同一单元，防止旧地址误认。
//
// 约束：操作数 <= 20；允许操作未返回（pending），pending 操作
// 可被丢弃或按任意结果补全，因此不影响可线性化判定，仅在线性化
// 输出中追加一个合法假定结果。
//
// 输出：若历史可线性化，输出一个合法的线性化顺序；否则输出
// 最短矛盾前缀（使历史不可线性化的最短事件前缀）。
//
// 用法：
//   stack_checker [file]            从文件（缺省 stdin）读取历史
//   stack_checker --ignore-gen ...  朴素模式：比较时忽略代次（演示误认）
//   stack_checker --demo            运行内置 ABA 反例演示
#include <cstdio>
#include <iostream>
#include <sstream>
#include <stdexcept>
#include <string>
#include <unordered_map>
#include <unordered_set>
#include <vector>

using namespace std;

static bool g_ignoreGen = false;

struct Val {
    long long addr = 0, gen = 0;
};

static bool valEq(const Val& a, const Val& b) {
    return a.addr == b.addr && (g_ignoreGen || a.gen == b.gen);
}

static string valStr(const Val& v) {
    return to_string(v.addr) + ":" + to_string(v.gen);
}

enum class OpType { Push, Pop, Peek };

struct Op {
    int id = -1;
    OpType type = OpType::Push;
    Val arg;                 // push 的入栈值
    bool hasRet = false;
    bool retEmpty = false;   // pop/peek 返回 empty
    Val retVal;              // pop/peek 返回的值
    int callEv = -1, retEv = -1;
};

struct Event {
    int op;                  // ops 下标
    bool isCall;
    string text;             // 原始行，用于回显
};

static string opCallStr(const Op& op) {
    switch (op.type) {
        case OpType::Push: return "push(" + valStr(op.arg) + ")";
        case OpType::Pop:  return "pop()";
        case OpType::Peek: return "peek()";
    }
    return "?";
}

// ---------------- 解析 ----------------

static Val parseVal(const string& tok, int lineNo) {
    size_t c = tok.find(':');
    if (c == string::npos || c == 0 || c + 1 >= tok.size())
        throw runtime_error("line " + to_string(lineNo) +
                            ": expected <addr>:<gen>, got '" + tok + "'");
    return {stoll(tok.substr(0, c)), stoll(tok.substr(c + 1))};
}

struct History {
    vector<Op> ops;
    vector<Event> evs;
};

static History parseHistory(istream& in) {
    History h;
    unordered_map<int, int> idToIdx;
    string line;
    int lineNo = 0;
    auto fail = [&](const string& msg) -> runtime_error {
        return runtime_error("line " + to_string(lineNo) + ": " + msg);
    };
    while (getline(in, line)) {
        lineNo++;
        size_t hash = line.find('#');
        string text = line.substr(0, hash);
        istringstream ss(text);
        string kw;
        if (!(ss >> kw)) continue;
        if (kw == "call") {
            int id;
            string kind;
            if (!(ss >> id >> kind)) throw fail("bad call syntax");
            if (idToIdx.count(id)) throw fail("duplicate call id " + to_string(id));
            Op op;
            op.id = id;
            if (kind == "push") {
                string v;
                if (!(ss >> v)) throw fail("push needs a value <addr>:<gen>");
                op.type = OpType::Push;
                op.arg = parseVal(v, lineNo);
            } else if (kind == "pop") {
                op.type = OpType::Pop;
            } else if (kind == "peek" || kind == "read") {
                op.type = OpType::Peek;
            } else {
                throw fail("unknown op kind '" + kind + "'");
            }
            string extra;
            if (ss >> extra) throw fail("trailing token '" + extra + "'");
            op.callEv = (int)h.evs.size();
            idToIdx[id] = (int)h.ops.size();
            h.ops.push_back(op);
            h.evs.push_back({(int)h.ops.size() - 1, true, text});
        } else if (kw == "ret") {
            int id;
            string res;
            if (!(ss >> id >> res)) throw fail("bad ret syntax");
            auto it = idToIdx.find(id);
            if (it == idToIdx.end()) throw fail("ret for unknown id " + to_string(id));
            Op& op = h.ops[it->second];
            if (op.hasRet) throw fail("duplicate ret for id " + to_string(id));
            op.hasRet = true;
            if (op.type == OpType::Push) {
                if (res != "ok") throw fail("push must return 'ok'");
            } else if (res == "empty") {
                op.retEmpty = true;
            } else if (res == "ok") {
                throw fail("pop/peek cannot return 'ok'");
            } else {
                op.retVal = parseVal(res, lineNo);
            }
            string extra;
            if (ss >> extra) throw fail("trailing token '" + extra + "'");
            op.retEv = (int)h.evs.size();
            h.evs.push_back({it->second, false, text});
        } else {
            throw fail("unknown keyword '" + kw + "'");
        }
    }
    if (h.ops.size() > 20)
        throw runtime_error("too many operations: " + to_string(h.ops.size()) +
                            " (limit is 20)");
    return h;
}

// ---------------- 可线性化搜索 ----------------
// 状态 = (已消费事件数 pos, 已线性化操作位掩码 mask, 当前栈 stk)。
// 转移：
//   1) 消费下一个事件：call 直接消费；ret 要求对应操作已线性化。
//   2) 线性化一个已调用且未线性化的操作：按栈语义更新 stk；
//      若该操作在前缀内有返回，结果必须与返回记录一致。
// 终止：pos == k 且所有在前缀内有返回的操作都已线性化。

struct Key {
    int pos;
    int mask;
    vector<Val> stk;
    bool operator==(const Key& o) const {
        if (pos != o.pos || mask != o.mask || stk.size() != o.stk.size())
            return false;
        for (size_t i = 0; i < stk.size(); ++i)
            if (!valEq(stk[i], o.stk[i])) return false;
        return true;
    }
};

struct KeyHash {
    size_t operator()(const Key& k) const {
        size_t h = (size_t)k.pos * 1000003u ^ (size_t)k.mask * 31u ^ k.stk.size();
        for (const Val& v : k.stk) {
            h = h * 1315423911u ^ ((size_t)v.addr * 2654435761u);
            if (!g_ignoreGen) h ^= (size_t)v.gen * 40503u;
        }
        return h;
    }
};

struct Choice { int kind, op; }; // kind: 0 = 消费事件, 1 = 线性化操作

struct Solver {
    int k; // 前缀长度（事件数）
    const vector<Event>& evs;
    const vector<Op>& ops;
    int nOps;
    int mustMask = 0;
    unordered_set<Key, KeyHash> failed;
    unordered_map<Key, Choice, KeyHash> path;

    Solver(int k_, const vector<Event>& e, const vector<Op>& o)
        : k(k_), evs(e), ops(o), nOps((int)o.size()) {
        for (int i = 0; i < nOps; ++i)
            if (ops[i].retEv >= 0 && ops[i].retEv < k) mustMask |= 1 << i;
    }

    bool dfs(int pos, int mask, vector<Val>& stk) {
        if (pos == k && (mask & mustMask) == mustMask) return true;
        Key key{pos, mask, stk};
        if (failed.count(key)) return false;
        if (pos < k) {
            const Event& e = evs[pos];
            if ((e.isCall || ((mask >> e.op) & 1)) && dfs(pos + 1, mask, stk)) {
                path[key] = {0, -1};
                return true;
            }
        }
        for (int i = 0; i < nOps; ++i) {
            const Op& op = ops[i];
            if (op.callEv < 0 || op.callEv >= pos || ((mask >> i) & 1)) continue;
            bool retKnown = op.retEv >= 0 && op.retEv < k;
            vector<Val> ns = stk;
            if (op.type == OpType::Push) {
                ns.push_back(op.arg);
            } else if (stk.empty()) {
                if (retKnown && !op.retEmpty) continue;
            } else {
                if (retKnown && (op.retEmpty || !valEq(op.retVal, stk.back())))
                    continue;
                if (op.type == OpType::Pop) ns.pop_back();
            }
            if (dfs(pos, mask | (1 << i), ns)) {
                path[key] = {1, i};
                return true;
            }
        }
        failed.insert(key);
        return false;
    }
};

// ---------------- 输出 ----------------

static string retStr(const Op& op) {
    if (op.type == OpType::Push) return "ok";
    if (op.retEmpty) return "EMPTY";
    return valStr(op.retVal);
}

static void printLinearization(Solver& s, const vector<Op>& ops) {
    cout << "LINEARIZABLE\n";
    cout << "one legal linearization:\n";
    vector<Val> stk;
    int pos = 0, mask = 0, step = 0;
    vector<char> done(ops.size(), 0);
    while (!(pos == s.k && (mask & s.mustMask) == s.mustMask)) {
        Key key{pos, mask, stk};
        Choice c = s.path.at(key);
        if (c.kind == 0) {
            pos++;
            continue;
        }
        const Op& op = ops[c.op];
        if (op.type == OpType::Push) stk.push_back(op.arg);
        else if (op.type == OpType::Pop && !stk.empty()) stk.pop_back();
        done[c.op] = 1;
        mask |= 1 << c.op;
        cout << "  " << ++step << ". #" << op.id << " " << opCallStr(op)
             << " -> " << retStr(op) << "\n";
    }
    // pending 操作（未返回）按调用顺序追加，给出假定结果
    bool header = false;
    for (size_t i = 0; i < ops.size(); ++i) {
        const Op& op = ops[i];
        if (op.hasRet || done[i]) continue;
        if (!header) {
            cout << "pending ops (not returned; appended with assumed results):\n";
            header = true;
        }
        string assumed;
        if (op.type == OpType::Push) {
            stk.push_back(op.arg);
            assumed = "ok";
        } else if (stk.empty()) {
            assumed = "EMPTY";
        } else {
            assumed = valStr(stk.back());
            if (op.type == OpType::Pop) stk.pop_back();
        }
        cout << "  " << ++step << ". #" << op.id << " " << opCallStr(op)
             << " -> " << assumed << "   [pending]\n";
    }
}

static void printBadPrefix(const History& h, int k) {
    cout << "NOT LINEARIZABLE\n";
    cout << "shortest contradictory prefix: " << k << " of " << h.evs.size()
         << " event(s)\n";
    for (int i = 0; i < k; ++i) {
        cout << "  " << i + 1 << ": " << h.evs[i].text;
        if (i == k - 1) {
            const Event& e = h.evs[i];
            if (!e.isCall)
                cout << "   <-- return of #" << h.ops[e.op].id
                     << " cannot be matched by any legal linearization";
        }
        cout << "\n";
    }
}

// 返回 true 表示（完整历史）可线性化
static bool check(const History& h) {
    int n = (int)h.evs.size();
    if (n == 0) {
        cout << "LINEARIZABLE\n(empty history)\n";
        return true;
    }
    for (int k = 1; k <= n; ++k) {
        Solver s(k, h.evs, h.ops);
        vector<Val> stk;
        if (!s.dfs(0, 0, stk)) {
            printBadPrefix(h, k);
            return false;
        }
        if (k == n) printLinearization(s, h.ops);
    }
    return true;
}

// ---------------- ABA 反例演示 ----------------

static const char* kAbaHistory = R"(
call 1 push 7:0
ret 1 ok
call 2 pop
ret 2 7:0
call 3 push 7:1
ret 3 ok
call 4 pop
ret 4 7:0
)";

static void runDemo() {
    cout << "=== ABA counterexample: stale-address misidentification ===\n";
    cout << "Address 7 is pushed with generation 0, popped (cell freed),\n"
            "then reused by the allocator with generation bumped to 1.\n"
            "Op #4's pop returns the STALE cell 7:0 although the stack top\n"
            "can only be 7:1 -- impossible for a real sequential stack.\n";
    cout << "-----------------------------------------------------------\n";
    cout << "history:\n";
    {
        istringstream ss(kAbaHistory);
        string line;
        while (getline(ss, line))
            if (!line.empty()) cout << "  " << line << "\n";
    }
    cout << "-----------------------------------------------------------\n";

    istringstream in1(kAbaHistory);
    History h = parseHistory(in1);

    cout << "[generation-aware check]\n";
    g_ignoreGen = false;
    bool ok = check(h);

    cout << "-----------------------------------------------------------\n";
    cout << "[naive check, --ignore-gen]\n";
    g_ignoreGen = true;
    bool naiveOk = check(h);
    g_ignoreGen = false;

    cout << "-----------------------------------------------------------\n";
    if (!ok && naiveOk)
        cout << "As expected: the generation-aware checker REJECTS the history,\n"
                "while the naive checker that ignores reuse generations accepts\n"
                "it -- it misidentifies the stale cell 7:0 as the live cell 7:1.\n"
                "Always compare (addr, gen) pairs, never the raw address alone.\n";
}

// ---------------- main ----------------

static void usage() {
    cout << "usage: stack_checker [--ignore-gen] [--demo] [file]\n"
            "  reads a stack history (call/ret events) from file or stdin,\n"
            "  prints a linearization or the shortest contradictory prefix.\n";
}

int main(int argc, char** argv) {
    string file;
    bool demo = false;
    for (int i = 1; i < argc; ++i) {
        string a = argv[i];
        if (a == "--ignore-gen") g_ignoreGen = true;
        else if (a == "--demo") demo = true;
        else if (a == "-h" || a == "--help") { usage(); return 0; }
        else file = a;
    }
    try {
        if (demo) {
            runDemo();
            return 0;
        }
        History h;
        if (file.empty()) {
            h = parseHistory(cin);
        } else {
            FILE* f = fopen(file.c_str(), "r");
            if (!f) throw runtime_error("cannot open " + file);
            string content;
            char buf[4096];
            size_t r;
            while ((r = fread(buf, 1, sizeof buf, f)) > 0)
                content.append(buf, r);
            fclose(f);
            istringstream ss(content);
            h = parseHistory(ss);
        }
        if (g_ignoreGen)
            cout << "(naive mode: reuse generations ignored)\n";
        return check(h) ? 0 : 1;
    } catch (const exception& e) {
        cerr << "error: " << e.what() << "\n";
        return 2;
    }
}
