
import math
import unittest

from mini_ad import Tensor, collect_nodes, grad, hvp


def matvec(matrix, vector):
    return [
        sum(matrix[row][col] * vector[col] for col in range(len(vector)))
        for row in range(len(matrix))
    ]


def quadratic_value(matrix, vector):
    product = matvec(matrix, vector)
    return sum(value * value for value in product)


def numeric_gradient(function, point, epsilon):
    result = []
    for index in range(len(point)):
        upper = point.copy()
        lower = point.copy()
        upper[index] += epsilon
        lower[index] -= epsilon
        result.append(
            (function(upper) - function(lower)) / (2.0 * epsilon)
        )
    return result


def numeric_hvp(matrix, point, vector, epsilon=1e-5, step=1e-2):
    function = lambda candidate: quadratic_value(matrix, candidate)
    upper_point = [
        point[index] + step * vector[index]
        for index in range(len(point))
    ]
    lower_point = [
        point[index] - step * vector[index]
        for index in range(len(point))
    ]
    upper_gradient = numeric_gradient(function, upper_point, epsilon)
    lower_gradient = numeric_gradient(function, lower_point, epsilon)
    return [
        (upper_gradient[index] - lower_gradient[index]) / (2.0 * step)
        for index in range(len(point))
    ]


def assert_close(test_case, actual, expected, tolerance=1e-10):
    test_case.assertEqual(len(actual), len(expected))
    for actual_value, expected_value in zip(actual, expected):
        test_case.assertTrue(
            math.isclose(
                actual_value,
                expected_value,
                rel_tol=tolerance,
                abs_tol=tolerance,
            ),
            msg=f"{actual} != {expected}",
        )


class HessianVectorProductTest(unittest.TestCase):
    def setUp(self):
        self.matrix = [
            [2.0, -1.0, 0.5],
            [0.5, 1.5, -1.0],
        ]
        self.point = [0.75, -1.25, 0.5]
        self.vector = [1.0, -0.5, 2.0]

        def function(x):
            product = self.matrix @ x
            return (product * product).sum()

        self.function = function

    def test_quadratic_hvp_matches_analytic_and_numeric_values(self):
        x = Tensor(self.point, requires_grad=True)
        actual = hvp(self.function, x, self.vector).data

        product = matvec(self.matrix, self.vector)
        analytic = [
            2.0 * sum(
                self.matrix[row][col] * product[row]
                for row in range(len(product))
            )
            for col in range(len(self.vector))
        ]
        numeric = numeric_hvp(
            self.matrix,
            self.point,
            self.vector,
        )

        assert_close(self, actual, analytic, 1e-12)
        assert_close(self, actual, numeric, 2e-7)

    def test_constant_gradient_has_zero_second_derivative(self):
        x = Tensor(self.point, requires_grad=True)
        output = (self.matrix @ x).sum()
        first_gradient = grad(output, (x,), create_graph=True)[0]
        second_gradient = grad(
            first_gradient,
            (x,),
            grad_output=self.vector,
        )[0]

        assert_close(self, second_gradient.data, [0.0] * len(self.point), 1e-12)

    def test_repeated_differentiation_has_no_accumulated_residue(self):
        x = Tensor(self.point, requires_grad=True)
        output = self.function(x)
        first_gradient = grad(output, (x,), create_graph=True)[0]

        first = grad(
            first_gradient,
            (x,),
            grad_output=self.vector,
        )[0].data
        second = grad(
            first_gradient,
            (x,),
            grad_output=self.vector,
        )[0].data
        other_vector = [0.0, 1.0, 0.0]
        other = grad(
            first_gradient,
            (x,),
            grad_output=other_vector,
        )[0].data

        assert_close(self, first, second, 1e-12)

        product = matvec(self.matrix, other_vector)
        expected_other = [
            2.0 * sum(
                self.matrix[row][col] * product[row]
                for row in range(len(product))
            )
            for col in range(len(other_vector))
        ]
        assert_close(self, other, expected_other, 1e-12)

    def test_shared_subexpression_is_traversed_once(self):
        x = Tensor(self.point, requires_grad=True)
        shared = self.matrix @ x
        output = (shared * shared).sum()
        nodes = collect_nodes(output)
        matvec_nodes = [node for node in nodes if node.op == "matvec"]

        self.assertEqual(len(matvec_nodes), 1)
        self.assertIs(shared.node, matvec_nodes[0])

        first_gradient = grad(output, (x,), create_graph=True)[0]
        repeated_gradient = grad(output, (x,), create_graph=True)[0]
        assert_close(
            self,
            first_gradient.data,
            repeated_gradient.data,
            1e-12,
        )

    def test_returned_gradient_buffers_are_independent(self):
        x = Tensor(self.point, requires_grad=True)
        self.assertFalse(hasattr(x, "grad"))

        output = self.function(x)
        first_gradient = grad(output, (x,))[0]
        first_gradient.data[0] = 12345.0
        repeated_gradient = grad(output, (x,))[0]

        product = matvec(self.matrix, self.point)
        expected = [
            2.0 * sum(
                self.matrix[row][col] * product[row]
                for row in range(len(product))
            )
            for col in range(len(self.point))
        ]
        assert_close(self, repeated_gradient.data, expected, 1e-12)
        self.assertFalse(hasattr(x, "grad"))


if __name__ == "__main__":
    unittest.main()
