// Block coding selector: multiple-choice knapsack with per-block max-error limits.
//
// Input (stdin):
//   N B                      -- number of blocks, byte budget
//   For each block i = 0..N-1:
//     k_i L_i                -- number of candidate options, max-error limit
//     k_i lines: bytes max_err sse
//
// Output (stdout):
//   OPTIMAL / INFEASIBLE, total sse, total bytes, slack, and the chosen
//   option (original index) per block.
//
// Tie-breaking rules (deterministic):
//   1. Minimize total squared error (primary objective).
//   2. Among equal total SSE, minimize total bytes (maximize slack).
//   3. Among still-equal solutions, the first one found in input order
//      wins; duplicate (bytes, sse) options within a block keep the
//      lowest original index.
#include <algorithm>
#include <iostream>
#include <limits>
#include <vector>
using namespace std;

static const long long INF = numeric_limits<long long>::max() / 4;

struct Option {
    long long bytes;
    long long maxErr;
    long long sse;
    int index; // original 0-based index within its block
};

int main() {
    ios::sync_with_stdio(false);
    cin.tie(nullptr);

    int N;
    long long B;
    if (!(cin >> N >> B)) return 0;

    vector<vector<Option>> blocks(N);
    for (int i = 0; i < N; ++i) {
        int k;
        long long limit;
        cin >> k >> limit;
        vector<Option> feasible;
        for (int j = 0; j < k; ++j) {
            Option o;
            cin >> o.bytes >> o.maxErr >> o.sse;
            o.index = j;
            if (o.maxErr <= limit) feasible.push_back(o);
        }
        if (feasible.empty()) {
            cout << "INFEASIBLE\n";
            cout << "block " << i << " has no candidate within max-error limit "
                 << limit << "\n";
            return 0;
        }
        // Dominance pruning: sort by (bytes asc, sse asc, index asc), then keep
        // only options whose sse is strictly lower than every cheaper option.
        // Equal (bytes, sse) duplicates resolve to the lowest original index.
        sort(feasible.begin(), feasible.end(),
             [](const Option& a, const Option& b) {
                 if (a.bytes != b.bytes) return a.bytes < b.bytes;
                 if (a.sse != b.sse) return a.sse < b.sse;
                 return a.index < b.index;
             });
        vector<Option> kept;
        long long bestSse = INF;
        for (const Option& o : feasible) {
            if (o.sse < bestSse) {
                kept.push_back(o);
                bestSse = o.sse;
            }
        }
        // Restore input order so DP ties resolve deterministically by index.
        sort(kept.begin(), kept.end(),
             [](const Option& a, const Option& b) { return a.index < b.index; });
        blocks[i] = std::move(kept);
    }

    // Exact-byte DP: dp[b] = min total SSE using exactly b bytes so far.
    vector<long long> dp(B + 1, INF), ndp(B + 1, INF);
    dp[0] = 0;
    vector<vector<int>> choice(N, vector<int>(B + 1, -1));
    for (int i = 0; i < N; ++i) {
        fill(ndp.begin(), ndp.end(), INF);
        for (long long b = 0; b <= B; ++b) {
            if (dp[b] == INF) continue;
            for (size_t j = 0; j < blocks[i].size(); ++j) {
                long long nb = b + blocks[i][j].bytes;
                if (nb > B) continue;
                long long cand = dp[b] + blocks[i][j].sse;
                if (cand < ndp[nb]) { // strict: first-found wins ties
                    ndp[nb] = cand;
                    choice[i][nb] = (int)j;
                }
            }
        }
        swap(dp, ndp);
    }

    // Best over b <= B; strict '<' with ascending b prefers fewer bytes.
    long long bestSse = INF, bestBytes = -1;
    for (long long b = 0; b <= B; ++b) {
        if (dp[b] < bestSse) {
            bestSse = dp[b];
            bestBytes = b;
        }
    }
    if (bestBytes < 0) {
        cout << "INFEASIBLE\n";
        return 0;
    }

    vector<int> pick(N);
    long long b = bestBytes;
    for (int i = N - 1; i >= 0; --i) {
        int j = choice[i][b];
        pick[i] = j;
        b -= blocks[i][j].bytes;
    }

    cout << "OPTIMAL\n";
    cout << "total_sse " << bestSse << "\n";
    cout << "total_bytes " << bestBytes << "\n";
    cout << "slack " << (B - bestBytes) << "\n";
    for (int i = 0; i < N; ++i) {
        const Option& o = blocks[i][pick[i]];
        cout << "block " << i << ": option " << o.index
             << " (bytes=" << o.bytes << ", max_err=" << o.maxErr
             << ", sse=" << o.sse << ")\n";
    }
    return 0;
}
