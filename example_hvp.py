
from mini_ad import Tensor, grad, hvp


def main():
    matrix = [
        [2.0, 1.0, 0.0],
        [1.0, 3.0, 1.0],
        [0.0, 1.0, 2.0],
    ]
    start = [0.5, -1.0, 1.5]
    vector = [1.0, 0.0, -1.0]

    x = Tensor(start, requires_grad=True)

    def quadratic(arg):
        product = matrix @ arg
        return product * product

    shared_output = quadratic(x).sum()
    gradient = grad(shared_output, (x,), create_graph=True)[0]
    hessian_vector = hvp(quadratic, x, vector)

    first_product = [
        sum(matrix[row][col] * start[col] for col in range(3))
        for row in range(3)
    ]
    expected_gradient = [
        2.0 * sum(matrix[row][col] * first_product[row] for row in range(3))
        for col in range(3)
    ]
    vector_product = [
        sum(matrix[row][col] * vector[col] for col in range(3))
        for row in range(3)
    ]
    expected_hvp = [
        2.0 * sum(matrix[row][col] * vector_product[row] for row in range(3))
        for col in range(3)
    ]

    print(f"f(x)        = {shared_output.data:.12g}")
    print(f"grad f      = {[round(value, 12) for value in gradient.data]}")
    print(f"expected    = {[round(value, 12) for value in expected_gradient]}")
    print(f"H v         = {[round(value, 12) for value in hessian_vector.data]}")
    print(f"expected Hv = {[round(value, 12) for value in expected_hvp]}")


if __name__ == "__main__":
    main()
