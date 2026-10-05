#include "isel/selector.h"

#include <set>

namespace isel {
namespace {

long long addCost(long long a, long long b) {
    if (a >= kInfCost || b >= kInfCost || a > kInfCost - b) return kInfCost;
    return a + b;
}

unsigned long long addCount(unsigned long long a, unsigned long long b) {
    if (a > kCountCap - b) return kCountCap;
    return a + b;
}

unsigned long long mulCount(unsigned long long a, unsigned long long b) {
    if (a == 0 || b == 0) return 0;
    unsigned __int128 v = static_cast<unsigned __int128>(a) * b;
    return v > kCountCap ? kCountCap : static_cast<unsigned long long>(v);
}

// Backtracking record for one (node, class) state.
struct Choice {
    int pattern = -1;         // index into Problem::patterns
    std::vector<int> phDag;   // dag node per placeholder (pattern preorder)
    std::vector<int> phClass; // chosen delivery class per placeholder
};

struct MatchResult {
    std::vector<std::pair<int, int>> placeholders; // (pattern node idx, dag node)
    std::vector<int> internalDagNodes;             // covered internal nodes (excl. root)
};

// Tries to match pattern node `pn` against dag node `dag`.
// Internal (non-placeholder) dag nodes below the root must have refcount == 1:
// a shared value must remain a tile root so it is computed exactly once.
bool matchNode(const Problem& prob, const Pattern& pat, int pn, int dag, bool isRoot,
               const std::vector<int>& refcount, MatchResult& res) {
    const PatternNode& p = pat.nodes[pn];
    if (p.placeholder) {
        res.placeholders.emplace_back(pn, dag);
        return true;
    }
    const Node& n = prob.nodes[dag];
    if (n.op != p.op || n.operands.size() != p.children.size()) return false;
    if (!isRoot) {
        if (refcount[dag] != 1) return false;
        res.internalDagNodes.push_back(dag);
    }
    for (size_t i = 0; i < p.children.size(); ++i) {
        if (!matchNode(prob, pat, p.children[i], n.operands[i], false, refcount, res))
            return false;
    }
    return true;
}

bool validate(const Problem& prob) {
    const int N = static_cast<int>(prob.nodes.size());
    const int K = prob.numClasses;
    if (K <= 0) return false;
    if (static_cast<int>(prob.copyCost.size()) != K) return false;
    for (const auto& row : prob.copyCost)
        if (static_cast<int>(row.size()) != K) return false;
    for (int i = 0; i < N; ++i)
        for (int c : prob.nodes[i].operands)
            if (c < 0 || c >= i) return false; // must be a DAG in topological order
    std::set<int> ids;
    for (const Pattern& p : prob.patterns) {
        if (p.nodes.empty() || p.nodes[0].placeholder) return false;
        if (p.resultClass < 0 || p.resultClass >= K) return false;
        if (!ids.insert(p.id).second) return false;
        for (const PatternNode& pn : p.nodes) {
            if (pn.placeholder) {
                if (pn.regClass < 0 || pn.regClass >= K) return false;
            } else {
                for (int ch : pn.children)
                    if (ch < 0 || ch >= static_cast<int>(p.nodes.size())) return false;
            }
        }
    }
    for (const auto& o : prob.outputs)
        if (o.first < 0 || o.first >= N || o.second < 0 || o.second >= K) return false;
    return true;
}

} // namespace

Solution select(const Problem& prob) {
    Solution sol;
    const int N = static_cast<int>(prob.nodes.size());
    const int K = prob.numClasses;
    sol.cover.assign(N, NodeCover{});
    if (!validate(prob)) return sol;

    // Use counts: operand uses plus output uses.
    std::vector<int> refcount(N, 0);
    for (const Node& n : prob.nodes)
        for (int c : n.operands) ++refcount[c];
    for (const auto& o : prob.outputs) ++refcount[o.first];

    // Nodes reachable from the outputs.
    std::vector<char> reachable(N, 0);
    {
        std::vector<int> stack;
        for (const auto& o : prob.outputs) stack.push_back(o.first);
        while (!stack.empty()) {
            int n = stack.back();
            stack.pop_back();
            if (reachable[n]) continue;
            reachable[n] = 1;
            for (int c : prob.nodes[n].operands) stack.push_back(c);
        }
    }

    // dp[n][r]: min cost of covering the region below n with n delivered in
    // class r, where shared children contribute only their per-use copy cost
    // (their production cost is accounted once, globally).
    std::vector<std::vector<long long>> dp(N, std::vector<long long>(K, kInfCost));
    std::vector<std::vector<unsigned long long>> cnt(
        N, std::vector<unsigned long long>(K, 0));
    std::vector<std::vector<Choice>> choice(N, std::vector<Choice>(K));
    std::vector<int> sStar(N, -1); // cheapest production class per node

    for (int n = 0; n < N; ++n) {
        for (int r = 0; r < K; ++r) {
            long long best = kInfCost;
            unsigned long long ways = 0;
            Choice bestChoice;
            for (int pi = 0; pi < static_cast<int>(prob.patterns.size()); ++pi) {
                const Pattern& pat = prob.patterns[pi];
                if (pat.resultClass != r) continue;
                MatchResult m;
                if (!matchNode(prob, pat, 0, n, true, refcount, m)) continue;
                long long cost = pat.cost;
                unsigned long long tileWays = 1;
                Choice ch;
                ch.pattern = pi;
                bool ok = true;
                for (const auto& ph : m.placeholders) {
                    const int c = ph.second;
                    const int rc = pat.nodes[ph.first].regClass;
                    ch.phDag.push_back(c);
                    if (refcount[c] > 1) {
                        // Shared child: produced once in sStar[c]; this use pays
                        // an explicit copy. Its own cost/variety is global.
                        const int s = sStar[c];
                        if (s < 0 || dp[c][s] >= kInfCost) { ok = false; break; }
                        cost = addCost(cost, prob.copyCost[s][rc]);
                        ch.phClass.push_back(s);
                    } else {
                        // Tree edge: fold the child's optimal delivery into rc.
                        long long eBest = kInfCost;
                        unsigned long long eWays = 0;
                        int eClass = -1;
                        for (int s = 0; s < K; ++s) {
                            if (dp[c][s] >= kInfCost) continue;
                            const long long v = addCost(dp[c][s], prob.copyCost[s][rc]);
                            if (v < eBest) {
                                eBest = v;
                                eWays = cnt[c][s];
                                eClass = s;
                            } else if (v == eBest) {
                                eWays = addCount(eWays, cnt[c][s]);
                            }
                        }
                        if (eClass < 0) { ok = false; break; }
                        cost = addCost(cost, eBest);
                        tileWays = mulCount(tileWays, eWays);
                        ch.phClass.push_back(eClass);
                    }
                }
                if (!ok || cost >= kInfCost) continue;
                if (cost < best) {
                    best = cost;
                    ways = tileWays;
                    bestChoice = std::move(ch);
                } else if (cost == best) {
                    ways = addCount(ways, tileWays);
                }
            }
            dp[n][r] = best;
            cnt[n][r] = ways;
            choice[n][r] = std::move(bestChoice);
        }
        for (int s = 0; s < K; ++s)
            if (dp[n][s] < kInfCost && (sStar[n] < 0 || dp[n][s] < dp[n][sStar[n]]))
                sStar[n] = s;
    }

    // Total cost: tree outputs contribute their full dp value; shared nodes
    // contribute their production cost exactly once; shared outputs pay one
    // explicit copy into the required output class.
    long long total = 0;
    unsigned long long totalWays = 1;
    bool feasible = true;
    for (const auto& o : prob.outputs) {
        const int n = o.first, rc = o.second;
        if (refcount[n] == 1) {
            if (dp[n][rc] >= kInfCost) { feasible = false; break; }
            total = addCost(total, dp[n][rc]);
            totalWays = mulCount(totalWays, cnt[n][rc]);
        } else {
            const int s = sStar[n];
            if (s < 0) { feasible = false; break; }
            total = addCost(total, prob.copyCost[s][rc]);
        }
    }
    if (feasible) {
        for (int c = 0; c < N; ++c) {
            if (!reachable[c] || refcount[c] <= 1) continue;
            const int s = sStar[c];
            if (s < 0) { feasible = false; break; }
            total = addCost(total, dp[c][s]);
            totalWays = mulCount(totalWays, cnt[c][s]);
        }
    }
    if (total >= kInfCost) feasible = false;
    sol.feasible = feasible;
    sol.totalCost = feasible ? total : 0;
    sol.numOptimal = feasible ? totalWays : 0;
    if (!feasible) return sol;

    // Reconstruct the per-node assignment by following the recorded choices.
    std::vector<char> expanded(N, 0);
    std::vector<std::pair<int, int>> stack;
    auto push = [&](int n, int r) { stack.emplace_back(n, r); };
    for (const auto& o : prob.outputs) {
        const int n = o.first;
        push(n, refcount[n] == 1 ? o.second : sStar[n]);
    }
    for (int c = 0; c < N; ++c)
        if (reachable[c] && refcount[c] > 1) push(c, sStar[c]);
    while (!stack.empty()) {
        const auto [n, r] = stack.back();
        stack.pop_back();
        if (expanded[n]) continue;
        expanded[n] = 1;
        const Choice& ch = choice[n][r];
        const Pattern& pat = prob.patterns[ch.pattern];
        MatchResult m;
        matchNode(prob, pat, 0, n, true, refcount, m);
        sol.cover[n] = NodeCover{true, true, pat.id, r, n};
        for (int d : m.internalDagNodes)
            sol.cover[d] = NodeCover{true, false, pat.id, -1, n};
        for (size_t i = 0; i < ch.phDag.size(); ++i) {
            const int c = ch.phDag[i];
            push(c, refcount[c] > 1 ? sStar[c] : ch.phClass[i]);
        }
    }
    return sol;
}

} // namespace isel
