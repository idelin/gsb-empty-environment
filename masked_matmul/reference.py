"""小规模自写稠密参考实现,仅用于核验。"""


def to_dense(matrix):
    dense = [[0] * matrix.cols for _ in range(matrix.rows)]
    for i, row in matrix.rows_items():
        for j, (weight, _count) in row.items():
            dense[i][j] = weight
    return dense


def dense_reference(A, B, mask):
    """完整稠密三重循环,返回与 Engine.query 相同格式的有序元组。"""
    if A.cols != B.rows:
        raise ValueError("内维不一致")
    da = to_dense(A)
    db = to_dense(B)
    m, inner, n = A.rows, A.cols, B.cols
    out = []
    for i in range(m):
        for j in range(n):
            if not mask.contains(i, j):
                continue
            total = 0
            for k in range(inner):
                if da[i][k] != 0 and db[k][j] != 0:
                    total += da[i][k] * db[k][j]
            if total != 0:
                out.append((i, j, total))
    return tuple(out)
