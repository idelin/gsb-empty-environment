#include <algorithm>
#include <cstdio>
#include <functional>
#include <iostream>
#include <queue>
#include <string>
#include <utility>
#include <vector>
using namespace std;

using i64 = long long;
using u64 = unsigned long long;
static const i64 INF = (i64)4e18;

static i64 sadd(i64 a, i64 b) {
    if (a >= INF || b >= INF) return INF;
    if (b > 0 && a > INF - b) return INF;
    return a + b;
}

struct PatNode {
    bool wild = false;
    string op;
    int wildClass = -1;
    vector<PatNode> children;
};

struct Pattern {
    i64 cost = 0;
    int resultClass = 0;
    PatNode root;
};

static PatNode parsePatNode() {
    string tok;
    if (!(cin >> tok)) { fprintf(stderr, "error: unexpected EOF in pattern\n"); exit(1); }
    PatNode pn;
    if (tok == "WILD") {
        pn.wild = true;
        cin >> pn.wildClass;
    } else if (tok == "OP") {
        int arity;
        cin >> pn.op >> arity;
        pn.children.resize(arity);
        for (int i = 0; i < arity; ++i) pn.children[i] = parsePatNode();
    } else {
        fprintf(stderr, "error: bad pattern token '%s'\n", tok.c_str());
        exit(1);
    }
    return pn;
}

int N, C, K, R;
vector<string> dagOp;
vector<vector<int>> dagCh;
vector<vector<i64>> copyClos;
vector<Pattern> pats;
vector<int> parentCount;
vector<char> isReqRoot;
vector<pair<int,int>> reqRoots;

vector<char> shared_;
vector<int> repCls;

vector<vector<i64>> dcost;
vector<vector<u64>> dways;
vector<vector<int>> dpat;
vector<vector<i64>> cost;
vector<vector<u64>> ways;
vector<vector<int>> copyFrom;

static bool matchPat(const PatNode& p, int u, bool isRoot, vector<pair<int,int>>& leaves) {
    if (p.wild) { leaves.push_back({u, p.wildClass}); return true; }
    if (!isRoot && shared_[u]) return false;
    if (dagOp[u] != p.op) return false;
    if (dagCh[u].size() != p.children.size()) return false;
    for (size_t i = 0; i < p.children.size(); ++i)
        if (!matchPat(p.children[i], dagCh[u][i], false, leaves)) return false;
    return true;
}

vector<int> ownerOf, patOf, classOf;
vector<char> emitted, isRootNode;
vector<vector<int>> useClasses;

static void emitNode(int u, int r) {
    int rr = r, guard = 0;
    while (copyFrom[u][rr] >= 0 && guard++ <= C) rr = copyFrom[u][rr];
    int pid = dpat[u][rr];
    isRootNode[u] = 1;
    patOf[u] = pid;
    classOf[u] = r;
    function<void(const PatNode&, int)> walk = [&](const PatNode& pn, int v) {
        if (pn.wild) {
            if (shared_[v]) useClasses[v].push_back(pn.wildClass);
            else emitNode(v, pn.wildClass);
            return;
        }
        ownerOf[v] = u;
        for (size_t i = 0; i < pn.children.size(); ++i)
            walk(pn.children[i], dagCh[v][i]);
    };
    walk(pats[pid].root, u);
}

int main() {
    if (!(cin >> N >> C)) { fprintf(stderr, "error: bad header\n"); return 1; }
    dagOp.resize(N); dagCh.resize(N);
    parentCount.assign(N, 0);
    for (int i = 0; i < N; ++i) {
        int arity;
        cin >> dagOp[i] >> arity;
        dagCh[i].resize(arity);
        for (int j = 0; j < arity; ++j) {
            cin >> dagCh[i][j];
            if (dagCh[i][j] >= i || dagCh[i][j] < 0) {
                fprintf(stderr, "error: nodes must be in topological order\n");
                return 1;
            }
            parentCount[dagCh[i][j]]++;
        }
    }
    copyClos.assign(C, vector<i64>(C, INF));
    for (int i = 0; i < C; ++i)
        for (int j = 0; j < C; ++j) {
            i64 w; cin >> w;
            copyClos[i][j] = (w < 0 ? INF : w);
        }
    for (int i = 0; i < C; ++i) copyClos[i][i] = 0;
    for (int k = 0; k < C; ++k)
        for (int i = 0; i < C; ++i)
            for (int j = 0; j < C; ++j)
                copyClos[i][j] = min(copyClos[i][j], sadd(copyClos[i][k], copyClos[k][j]));

    cin >> K;
    pats.resize(K);
    for (int i = 0; i < K; ++i) {
        cin >> pats[i].cost >> pats[i].resultClass;
        pats[i].root = parsePatNode();
        if (pats[i].root.wild) {
            fprintf(stderr, "error: pattern root must be OP\n");
            return 1;
        }
    }
    cin >> R;
    isReqRoot.assign(N, 0);
    for (int i = 0; i < R; ++i) {
        int n, c; cin >> n >> c;
        reqRoots.push_back({n, c});
        isReqRoot[n] = 1;
    }

    shared_.assign(N, 0);
    for (int i = 0; i < N; ++i)
        shared_[i] = (parentCount[i] + (isReqRoot[i] ? 1 : 0)) >= 2;

    dcost.assign(N, vector<i64>(C, INF));
    dways.assign(N, vector<u64>(C, 0));
    dpat.assign(N, vector<int>(C, -1));
    cost.assign(N, vector<i64>(C, INF));
    ways.assign(N, vector<u64>(C, 0));
    copyFrom.assign(N, vector<int>(C, -1));
    repCls.assign(N, -1);

    for (int u = 0; u < N; ++u) {
        for (int r = 0; r < C; ++r) {
            for (int pid = 0; pid < K; ++pid) {
                if (pats[pid].resultClass != r) continue;
                vector<pair<int,int>> leaves;
                if (!matchPat(pats[pid].root, u, true, leaves)) continue;
                i64 c = pats[pid].cost;
                u64 w = 1;
                bool ok = true;
                for (auto& lf : leaves) {
                    int v = lf.first, cls = lf.second;
                    i64 lc; u64 lw;
                    if (shared_[v]) { lc = copyClos[repCls[v]][cls]; lw = 1; }
                    else { lc = cost[v][cls]; lw = ways[v][cls]; }
                    if (lc >= INF) { ok = false; break; }
                    c = sadd(c, lc);
                    w *= lw;
                }
                if (!ok) continue;
                if (c < dcost[u][r]) { dcost[u][r] = c; dways[u][r] = w; dpat[u][r] = pid; }
                else if (c == dcost[u][r]) { dways[u][r] += w; }
            }
        }
        for (int r = 0; r < C; ++r) {
            for (int rp = 0; rp < C; ++rp) {
                if (dcost[u][rp] >= INF) continue;
                i64 tot = sadd(dcost[u][rp], copyClos[rp][r]);
                if (tot < cost[u][r]) { cost[u][r] = tot; ways[u][r] = dways[u][rp]; }
                else if (tot == cost[u][r]) { ways[u][r] += dways[u][rp]; }
            }
            if (dcost[u][r] == cost[u][r]) copyFrom[u][r] = -1;
            else {
                for (int rp = 0; rp < C; ++rp)
                    if (rp != r && dcost[u][rp] < INF &&
                        sadd(dcost[u][rp], copyClos[rp][r]) == cost[u][r]) {
                        copyFrom[u][r] = rp; break;
                    }
            }
        }
        if (shared_[u]) {
            int best = -1;
            for (int r = 0; r < C; ++r)
                if (cost[u][r] < INF && (best < 0 || cost[u][r] < cost[u][best])) best = r;
            repCls[u] = best;
        }
    }

    ownerOf.assign(N, -1);
    patOf.assign(N, -1);
    classOf.assign(N, -1);
    emitted.assign(N, 0);
    isRootNode.assign(N, 0);
    useClasses.assign(N, {});

    bool feasible = true;
    for (auto& rr : reqRoots)
        if (!shared_[rr.first] && cost[rr.first][rr.second] >= INF) feasible = false;
    if (!feasible) { printf("INFEASIBLE\n"); return 0; }

    for (auto& rr : reqRoots) {
        if (shared_[rr.first]) useClasses[rr.first].push_back(rr.second);
        else { emitNode(rr.first, rr.second); emitted[rr.first] = 1; }
    }
    bool progress = true;
    while (progress) {
        progress = false;
        for (int v = 0; v < N; ++v) {
            if (shared_[v] && !emitted[v] && !useClasses[v].empty()) {
                if (repCls[v] < 0) { printf("INFEASIBLE\n"); return 0; }
                emitNode(v, repCls[v]);
                emitted[v] = 1;
                progress = true;
            }
        }
    }

    i64 total = 0;
    u64 totalWays = 1;
    for (auto& rr : reqRoots) {
        int n = rr.first, c = rr.second;
        if (!shared_[n]) { total = sadd(total, cost[n][c]); totalWays *= ways[n][c]; }
        else total = sadd(total, copyClos[repCls[n]][c]);
    }
    for (int v = 0; v < N; ++v) {
        if (!shared_[v] || !emitted[v]) continue;
        if (repCls[v] < 0 || cost[v][repCls[v]] >= INF) { printf("INFEASIBLE\n"); return 0; }
        total = sadd(total, cost[v][repCls[v]]);
        u64 sw = 0;
        for (int r = 0; r < C; ++r) {
            if (cost[v][r] != cost[v][repCls[v]]) continue;
            bool ok = true;
            for (int cls : useClasses[v])
                if (copyClos[r][cls] != copyClos[repCls[v]][cls]) { ok = false; break; }
            if (ok) sw += ways[v][r];
        }
        totalWays *= sw;
    }
    if (total >= INF) { printf("INFEASIBLE\n"); return 0; }

    printf("COST %lld\n", total);
    printf("WAYS %llu\n", totalWays);
    for (int u = 0; u < N; ++u) {
        if (isRootNode[u])
            printf("%d ROOT pattern=%d class=%d%s\n", u, patOf[u], classOf[u],
                   shared_[u] ? " shared" : "");
        else if (ownerOf[u] >= 0)
            printf("%d COVERED owner=%d\n", u, ownerOf[u]);
        else
            printf("%d DEAD\n", u);
    }
    return 0;
}
