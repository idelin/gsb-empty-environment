#pragma once

#include <cstddef>
#include <map>
#include <stdexcept>
#include <utility>
#include <vector>

namespace fem {

using Index = std::size_t;

class AssemblyError : public std::runtime_error {
public:
    explicit AssemblyError(const std::string& msg) : std::runtime_error(msg) {}
};

// Sparse matrix over a scalar type T (double by default; any ring-like type
// with += and * works, e.g. an exact rational type).
// Storage: vector of row maps; absent entries are exactly zero.
template <typename T>
class SparseMatrix {
public:
    explicit SparseMatrix(Index n) : n_(n), rows_(n) {}

    Index size() const { return n_; }

    void add(Index i, Index j, const T& v) {
        if (i >= n_ || j >= n_) throw AssemblyError("SparseMatrix::add index out of range");
        rows_[i][j] += v;
    }

    void set(Index i, Index j, const T& v) {
        if (i >= n_ || j >= n_) throw AssemblyError("SparseMatrix::set index out of range");
        if (v == T{}) {
            rows_[i].erase(j);
        } else {
            rows_[i][j] = v;
        }
    }

    T get(Index i, Index j) const {
        if (i >= n_ || j >= n_) throw AssemblyError("SparseMatrix::get index out of range");
        auto it = rows_[i].find(j);
        return it == rows_[i].end() ? T{} : it->second;
    }

    bool isRowZero(Index i) const {
        if (i >= n_) throw AssemblyError("SparseMatrix::isRowZero index out of range");
        for (const auto& kv : rows_[i]) {
            if (kv.second != T{}) return false;
        }
        return true;
    }

    std::vector<Index> zeroRows() const {
        std::vector<Index> out;
        for (Index i = 0; i < n_; ++i) {
            if (isRowZero(i)) out.push_back(i);
        }
        return out;
    }

    void zeroRow(Index i) {
        if (i >= n_) throw AssemblyError("SparseMatrix::zeroRow index out of range");
        rows_[i].clear();
    }

    void zeroCol(Index j) {
        if (j >= n_) throw AssemblyError("SparseMatrix::zeroCol index out of range");
        for (auto& row : rows_) row.erase(j);
    }

    // Entries (row, value) of column j, in ascending row order.
    std::vector<std::pair<Index, T>> columnEntries(Index j) const {
        if (j >= n_) throw AssemblyError("SparseMatrix::columnEntries index out of range");
        std::vector<std::pair<Index, T>> out;
        for (Index i = 0; i < n_; ++i) {
            auto it = rows_[i].find(j);
            if (it != rows_[i].end() && it->second != T{}) out.emplace_back(i, it->second);
        }
        return out;
    }

    std::vector<std::vector<T>> dense() const {
        std::vector<std::vector<T>> d(n_, std::vector<T>(n_, T{}));
        for (Index i = 0; i < n_; ++i) {
            for (const auto& kv : rows_[i]) d[i][kv.first] = kv.second;
        }
        return d;
    }

private:
    Index n_;
    std::vector<std::map<Index, T>> rows_;
};

// One element's local contribution. The local matrix is square (rows x rows),
// stored row-major; dofs maps local row/col index -> global dof index.
template <typename T>
struct ElementContribution {
    Index rows = 0;
    std::vector<T> matrix;      // rows * rows, row-major
    std::vector<T> rhs;         // rows
    std::vector<Index> dofs;    // rows
};

template <typename T>
class Assembler {
public:
    explicit Assembler(Index num_dofs) : n_(num_dofs), mat_(num_dofs), rhs_(num_dofs, T{}) {}

    Index numDofs() const { return n_; }
    const SparseMatrix<T>& matrix() const { return mat_; }
    const std::vector<T>& rhs() const { return rhs_; }

    // Validate a contribution without applying it. Throws AssemblyError.
    void validate(const ElementContribution<T>& e) const {
        if (e.rows == 0) throw AssemblyError("contribution has zero rows");
        if (e.matrix.size() != e.rows * e.rows)
            throw AssemblyError("local matrix size does not match rows*rows");
        if (e.rhs.size() != e.rows)
            throw AssemblyError("local rhs size does not match rows");
        if (e.dofs.size() != e.rows)
            throw AssemblyError("dof map size does not match rows");
        for (Index k = 0; k < e.dofs.size(); ++k) {
            if (e.dofs[k] >= n_)
                throw AssemblyError("dof map index out of range");
            for (Index m = k + 1; m < e.dofs.size(); ++m) {
                if (e.dofs[k] == e.dofs[m])
                    throw AssemblyError("dof map contains a duplicate index");
            }
        }
    }

    // Add a single contribution (validated first).
    void add(const ElementContribution<T>& e) {
        validate(e);
        apply(e);
    }

    // All-or-nothing: the whole batch is validated before anything is applied.
    // If any contribution is invalid, the global state is left untouched.
    void addBatch(const std::vector<ElementContribution<T>>& batch) {
        for (const auto& e : batch) validate(e);
        for (const auto& e : batch) apply(e);
    }

    // Eliminate prescribed-displacement constraints (Dirichlet dofs).
    // For each constrained dof i with value g:
    //   rhs[j] -= A(j,i) * g   for all j   (column coupling moved to the RHS)
    //   row i and column i are zeroed, A(i,i) = 1, rhs[i] = g
    // Corrections are computed from the matrix as it was before elimination,
    // so couplings between multiple constrained dofs are handled consistently.
    void applyDirichlet(const std::map<Index, T>& prescribed) {
        for (const auto& kv : prescribed) {
            if (kv.first >= n_) throw AssemblyError("prescribed dof out of range");
        }
        std::vector<T> correction(n_, T{});
        for (const auto& kv : prescribed) {
            const T& g = kv.second;
            for (const auto& entry : mat_.columnEntries(kv.first)) {
                correction[entry.first] += entry.second * g;
            }
        }
        for (Index j = 0; j < n_; ++j) rhs_[j] -= correction[j];
        for (const auto& kv : prescribed) {
            Index i = kv.first;
            mat_.zeroRow(i);
            mat_.zeroCol(i);
            mat_.set(i, i, T{} + 1);
            rhs_[i] = kv.second;
        }
    }

    // Regularize structurally zero rows (unconnected dofs) so the system is
    // solvable: A(i,i) = 1, rhs[i] = 0. Returns the rows that were fixed.
    std::vector<Index> regularizeZeroRows() {
        std::vector<Index> fixed = mat_.zeroRows();
        for (Index i : fixed) {
            mat_.set(i, i, T{} + 1);
            rhs_[i] = T{};
        }
        return fixed;
    }

private:
    void apply(const ElementContribution<T>& e) {
        for (Index a = 0; a < e.rows; ++a) {
            Index gi = e.dofs[a];
            rhs_[gi] += e.rhs[a];
            for (Index b = 0; b < e.rows; ++b) {
                mat_.add(gi, e.dofs[b], e.matrix[a * e.rows + b]);
            }
        }
    }

    Index n_;
    SparseMatrix<T> mat_;
    std::vector<T> rhs_;
};

} // namespace fem
