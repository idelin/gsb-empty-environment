"""Runnable demo of the transactional sparse matrix (Python 3.11+, stdlib only)."""

from sparse_matrix import QuotaExceededError, SparseMatrix


def show(title, mat):
    print(f"{title}\n  {mat!r}")
    for row in mat.to_dense():
        print("  ", row)
    print()


def main():
    # 1. Non-contiguous slice assignment + scalar broadcast.
    mat = SparseMatrix(4, 4)
    mat[[3, 0], [2, 0]] = [[1, 2], [3, 4]]
    mat[1, :] = 7  # broadcast
    show("1. non-contiguous indices + broadcast", mat)

    # 2. Duplicate targets: last write wins.
    mat2 = SparseMatrix(2, 2)
    mat2[[0, 0], [1, 1]] = [[1, 2], [3, 4]]
    print("2. duplicate targets, last write wins ->", mat2[0, 1].tolist())
    print()

    # 3. Overlapping assignment reads the pre-assignment snapshot.
    mat3 = SparseMatrix(1, 4)
    mat3[0, :] = [[1, 2, 3, 4]]
    mat3[[0], [1, 2, 3]] = mat3[[0], [0, 1, 2]]  # shift right
    show("3. overlap: shift right reads pre-assignment snapshot", mat3)

    # 4. Memory quota in row blocks: failure leaves no partial write.
    mat4 = SparseMatrix(3, 3, memory_quota=1)
    mat4[0, 0] = [[1]]
    try:
        mat4[[0, 1], [0, 1]] = [[1, 2], [3, 4]]  # needs 2 row blocks
    except QuotaExceededError as exc:
        print(f"4. quota rejected: {exc}")
    print("   matrix unchanged:", mat4.to_dense())
    print("   rows copied last/total:",
          mat4.rows_copied_last, mat4.rows_copied_total)
    print()

    # 5. History read-only views.
    print("5. history versions:", mat4.version_count)
    view = mat4.history_view(0)
    print("   version 0 (initial):", view.to_dense())
    try:
        view[0, 0] = [[9]]
    except TypeError as exc:
        print("   view is read-only:", exc)
    print()

    # 6. Multi-assignment transaction with rollback.
    mat6 = SparseMatrix(3, 3)
    try:
        with mat6.transaction():
            mat6[0, 0] = [[1]]
            mat6[2, 5] = [[2]]  # out of bounds -> full rollback
    except IndexError as exc:
        print(f"6. transaction rolled back: {exc}")
    print("   matrix after rollback:", mat6.to_dense())
    print("   versions:", mat6.version_count)
    print()

    # 7. Explicit zeros and canonical order survive updates.
    mat7 = SparseMatrix(2, 4)
    mat7[0, [3, 1]] = [[0, 5]]  # explicit zero at col 3
    mat7[0, 0] = [[9]]
    print("7. canonical:", mat7.is_canonical(),
          "| explicit zeros:", mat7.explicit_zero_count(),
          "| row 0:", mat7[0, :].tolist())


if __name__ == "__main__":
    main()
