#pragma once

// Independent dense reference implementation used to cross-check the
// sparse assembler. Deliberately written with plain dense loops so the
// two implementations share no code.

#include <cstddef>
#include <map>
#include <vector>

#include "fem/assembler.hpp"
#include "fem/rational.hpp"

namespace ref {

using fem::Rational;

struct DenseSystem {
    std::size_t n;
    std::vector<std::vector<Rational>> k;
    std::vector<Rational> f;

    explicit DenseSystem(std::size_t ndofs)
        : n(ndofs), k(ndofs, std::vector<Rational>(ndofs)), f(ndofs) {}
};

inline void assemble(DenseSystem& sys, const fem::ElementContribution& c) {
    const std::size_t m = c.dofMap.size();
    for (std::size_t a = 0; a < m; ++a) {
        const std::size_t gi = c.dofMap[a];
        for (std::size_t b = 0; b < m; ++b) sys.k[gi][c.dofMap[b]] += c.ke[a][b];
        if (!c.fe.empty()) sys.f[gi] += c.fe[a];
    }
}

inline void eliminate(DenseSystem& sys, const std::map<std::size_t, Rational>& constraints) {
    std::vector<bool> constrained(sys.n, false);
    for (const auto& kv : constraints) constrained[kv.first] = true;
    // RHS correction from the unmodified stiffness, then zero row/col.
    for (const auto& kv : constraints) {
        const std::size_t d = kv.first;
        const Rational g = kv.second;
        for (std::size_t j = 0; j < sys.n; ++j)
            if (!constrained[j]) sys.f[j] -= sys.k[j][d] * g;
    }
    for (const auto& kv : constraints) {
        const std::size_t d = kv.first;
        for (std::size_t j = 0; j < sys.n; ++j) {
            sys.k[d][j] = Rational(0);
            sys.k[j][d] = Rational(0);
        }
        sys.k[d][d] = Rational(1);
        sys.f[d] = kv.second;
    }
}

// Exact Gaussian elimination with partial pivoting on rationals.
inline std::vector<Rational> solve(DenseSystem sys) {
    const std::size_t n = sys.n;
    std::vector<Rational> x(n);
    for (std::size_t col = 0; col < n; ++col) {
        std::size_t pivot = col;
        while (pivot < n && sys.k[pivot][col].isZero()) ++pivot;
        if (pivot == n) throw std::runtime_error("singular reference system");
        std::swap(sys.k[pivot], sys.k[col]);
        std::swap(sys.f[pivot], sys.f[col]);
        for (std::size_t row = col + 1; row < n; ++row) {
            const Rational factor = sys.k[row][col] / sys.k[col][col];
            if (factor.isZero()) continue;
            for (std::size_t j = col; j < n; ++j) sys.k[row][j] -= factor * sys.k[col][j];
            sys.f[row] -= factor * sys.f[col];
        }
    }
    for (std::size_t i = n; i-- > 0;) {
        Rational s = sys.f[i];
        for (std::size_t j = i + 1; j < n; ++j) s -= sys.k[i][j] * x[j];
        x[i] = s / sys.k[i][i];
    }
    return x;
}

} // namespace ref
