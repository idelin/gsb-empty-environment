#pragma once

#include <cstddef>
#include <map>
#include <utility>
#include <vector>

#include "fem/rational.hpp"

namespace fem {

// Sparse matrix of exact rationals in dictionary-of-keys form.
// Entries that cancel to exactly zero are pruned, so structural
// zero rows (untouched DOFs) stay observable.
class SparseMatrix {
public:
    using Key = std::pair<std::size_t, std::size_t>;

    explicit SparseMatrix(std::size_t n) : n_(n) {}

    std::size_t size() const { return n_; }

    void add(std::size_t i, std::size_t j, const Rational& v) {
        if (v.isZero()) return;
        auto it = data_.find({i, j});
        if (it == data_.end()) {
            data_.emplace(Key{i, j}, v);
        } else {
            it->second += v;
            if (it->second.isZero()) data_.erase(it);
        }
    }

    void set(std::size_t i, std::size_t j, const Rational& v) {
        if (v.isZero()) {
            data_.erase({i, j});
        } else {
            data_[{i, j}] = v;
        }
    }

    void remove(std::size_t i, std::size_t j) { data_.erase({i, j}); }

    Rational get(std::size_t i, std::size_t j) const {
        const auto it = data_.find({i, j});
        return it == data_.end() ? Rational(0) : it->second;
    }

    const std::map<Key, Rational>& entries() const { return data_; }

    std::vector<std::vector<Rational>> toDense() const {
        std::vector<std::vector<Rational>> dense(n_, std::vector<Rational>(n_));
        for (const auto& kv : data_) dense[kv.first.first][kv.first.second] = kv.second;
        return dense;
    }

private:
    std::size_t n_;
    std::map<Key, Rational> data_;
};

} // namespace fem
