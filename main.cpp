// Block encoding selector: pick exactly one candidate per block.
// Minimize total squared error (SSE) subject to:
//   - total bytes <= budget
//   - each chosen candidate's max error <= per-block max error limit
// Exact DP over the byte budget (multiple-choice knapsack).
//
// Tie-break rules (deterministic, applied in order):
//   1. smaller total SSE wins;
//   2. equal SSE -> fewer total bytes wins (larger budget remainder);
//   3. still tied -> the incumbent is kept; candidates are considered in
//      increasing original index order within each block, so the smallest
//      original index achieving the optimum is preferred.
//
// Dominance pruning (per block, after dropping max-err-infeasible options):
//   option A dominates B iff A.bytes <= B.bytes && A.sse <= B.sse.
//   For equal (bytes, sse) pairs only the smallest original index is kept,
//   which preserves tie-break rule 3.
//
// Input (stdin):
//   n budget max_err_limit
//   then for each of n blocks:
//     k
//     k lines: bytes max_err sse
// Output (stdout):
//   line 1: total_sse
//   line 2: total_bytes remainder
//   line 3: chosen original candidate index per block (0-based)
//   or "INFEASIBLE" if no selection satisfies the constraints.

#include <cstdio>
#include <vector>
#include <algorithm>
#include <limits>

struct Option {
    int bytes;
    long long maxErr;
    long long sse;
    int origIndex;
};

int main() {
    int n;
    long long budget, maxErrLimit;
    if (std::scanf("%d %lld %lld", &n, &budget, &maxErrLimit) != 3) return 1;

    const long long INF = std::numeric_limits<long long>::max() / 4;

    std::vector<std::vector<Option>> blocks(n);
    for (int i = 0; i < n; ++i) {
        int k;
        if (std::scanf("%d", &k) != 1) return 1;
        std::vector<Option> opts;
        opts.reserve(k);
        for (int j = 0; j < k; ++j) {
            Option o;
            if (std::scanf("%d %lld %lld", &o.bytes, &o.maxErr, &o.sse) != 3) return 1;
            o.origIndex = j;
            if (o.maxErr <= maxErrLimit) opts.push_back(o);  // feasibility filter
        }
        if (opts.empty()) { std::printf("INFEASIBLE\n"); return 0; }

        // Dominance pruning: sort by (bytes asc, sse asc, origIndex asc),
        // keep an option only if its sse is strictly below the best sse seen
        // at any smaller-or-equal byte count.
        std::sort(opts.begin(), opts.end(), [](const Option& a, const Option& b) {
            if (a.bytes != b.bytes) return a.bytes < b.bytes;
            if (a.sse != b.sse) return a.sse < b.sse;
            return a.origIndex < b.origIndex;
        });
        std::vector<Option> pruned;
        long long bestSse = INF;
        for (const Option& o : opts) {
            if (o.sse < bestSse) {
                bestSse = o.sse;
                pruned.push_back(o);
            }
        }
        // Present candidates in original-index order so DP ties keep the
        // smallest original index (rule 3).
        std::sort(pruned.begin(), pruned.end(),
                  [](const Option& a, const Option& b) { return a.origIndex < b.origIndex; });
        blocks[i] = std::move(pruned);
    }

    // dp[b] = min total SSE using exactly b bytes for processed blocks.
    std::vector<long long> dp(budget + 1, INF), ndp(budget + 1, INF);
    dp[0] = 0;
    // choice[i][b] = position (in blocks[i]) chosen when state b was reached.
    std::vector<std::vector<int>> choice(n, std::vector<int>(budget + 1, -1));

    for (int i = 0; i < n; ++i) {
        std::fill(ndp.begin(), ndp.end(), INF);
        for (long long b = 0; b <= budget; ++b) {
            if (dp[b] == INF) continue;
            for (size_t p = 0; p < blocks[i].size(); ++p) {
                long long nb = b + blocks[i][p].bytes;
                if (nb > budget) continue;
                long long cand = dp[b] + blocks[i][p].sse;
                if (cand < ndp[nb]) {  // strict: ties keep the incumbent
                    ndp[nb] = cand;
                    choice[i][nb] = (int)p;
                }
            }
        }
        dp.swap(ndp);
    }

    // Final tie-break rules 1 & 2: min SSE, then min bytes.
    long long bestB = -1;
    for (long long b = 0; b <= budget; ++b) {
        if (dp[b] == INF) continue;
        if (bestB == -1 || dp[b] < dp[bestB]) bestB = b;
    }
    if (bestB == -1) { std::printf("INFEASIBLE\n"); return 0; }

    std::vector<int> answer(n);
    long long b = bestB;
    for (int i = n - 1; i >= 0; --i) {
        int p = choice[i][b];
        answer[i] = blocks[i][p].origIndex;
        b -= blocks[i][p].bytes;
    }

    std::printf("%lld\n", dp[bestB]);
    std::printf("%lld %lld\n", bestB, budget - bestB);
    for (int i = 0; i < n; ++i)
        std::printf("%d%c", answer[i], i + 1 == n ? '\n' : ' ');
    return 0;
}
