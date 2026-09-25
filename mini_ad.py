
class Node:
    def __init__(self, op, inputs, backward):
        self.op = op
        self.inputs = inputs
        self.backward = backward


class Tensor:
    def __init__(self, data, requires_grad=False):
        self.data = _copy(data)
        self.requires_grad = requires_grad
        self.node = None

    @property
    def shape(self):
        return _shape(self.data)

    def __add__(self, other):
        return add(self, other)

    def __radd__(self, other):
        return add(other, self)

    def __mul__(self, other):
        return mul(self, other)

    def __rmul__(self, other):
        return mul(other, self)

    def __matmul__(self, other):
        return matvec(self, other)

    def __rmatmul__(self, other):
        return matvec(other, self)

    @property
    def T(self):
        return transpose(self)

    def sum(self):
        return sum_all(self)


def add(a, b):
    a = _as_tensor(a)
    b = _as_tensor(b)
    if not (a.requires_grad or b.requires_grad):
        return Tensor(_raw_add(a.data, b.data), requires_grad=False)

    def backward(grad, create_graph):
        if create_graph:
            return grad, grad
        return _copy(grad), _copy(grad)

    return _make_node("add", _raw_add(a.data, b.data), (a, b), backward)


def mul(a, b):
    a = _as_tensor(a)
    b = _as_tensor(b)
    if not (a.requires_grad or b.requires_grad):
        return Tensor(_raw_mul(a.data, b.data), requires_grad=False)

    def backward(grad, create_graph):
        if create_graph:
            return mul(grad, b), mul(grad, a)
        return _raw_mul(grad, b.data), _raw_mul(grad, a.data)

    return _make_node("mul", _raw_mul(a.data, b.data), (a, b), backward)


def sum_all(a):
    a = _as_tensor(a)
    if not a.requires_grad:
        return Tensor(_raw_sum_all(a.data), requires_grad=False)
    shape = a.shape

    def backward(grad, create_graph):
        if create_graph:
            return (broadcast_to(grad, shape),)
        return (_raw_broadcast_to(grad, shape),)

    return _make_node("sum", _raw_sum_all(a.data), (a,), backward)


def broadcast_to(a, shape):
    a = _as_tensor(a)
    if not a.requires_grad:
        return Tensor(_raw_broadcast_to(a.data, shape), requires_grad=False)

    def backward(grad, create_graph):
        if create_graph:
            return (sum_all(grad),)
        return (_raw_sum_all(grad),)

    return _make_node(
        "broadcast_to",
        _raw_broadcast_to(a.data, shape),
        (a,),
        backward,
    )


def transpose(a):
    a = _as_tensor(a)
    if not a.requires_grad:
        return Tensor(_raw_transpose(a.data), requires_grad=False)

    def backward(grad, create_graph):
        if create_graph:
            return (transpose(grad),)
        return (_raw_transpose(grad),)

    return _make_node("transpose", _raw_transpose(a.data), (a,), backward)


def matvec(matrix, vector):
    matrix = _as_tensor(matrix)
    vector = _as_tensor(vector)
    if not (matrix.requires_grad or vector.requires_grad):
        return Tensor(_raw_matvec(matrix.data, vector.data), requires_grad=False)

    def backward(grad, create_graph):
        if create_graph:
            return outer(grad, vector), matvec(transpose(matrix), grad)
        return (
            _raw_outer(grad, vector.data),
            _raw_matvec(_raw_transpose(matrix.data), grad),
        )

    return _make_node(
        "matvec",
        _raw_matvec(matrix.data, vector.data),
        (matrix, vector),
        backward,
    )


def outer(a, b):
    a = _as_tensor(a)
    b = _as_tensor(b)
    if not (a.requires_grad or b.requires_grad):
        return Tensor(_raw_outer(a.data, b.data), requires_grad=False)

    def backward(grad, create_graph):
        if create_graph:
            return matvec(grad, b), matvec(transpose(grad), a)
        return (
            _raw_matvec(grad, b.data),
            _raw_matvec(_raw_transpose(grad), a.data),
        )

    return _make_node("outer", _raw_outer(a.data, b.data), (a, b), backward)


def grad(output, inputs, grad_output=None, create_graph=False):
    if isinstance(inputs, Tensor):
        inputs = (inputs,)
    if grad_output is None:
        seed_data = _raw_broadcast_to(1.0, output.shape)
    elif isinstance(grad_output, Tensor):
        seed_data = grad_output.data
    else:
        seed_data = grad_output
    seed = (
        Tensor(seed_data, requires_grad=True)
        if create_graph
        else _copy(seed_data)
    )

    node_grads = {}
    tensor_grads = {}

    if output.node is not None:
        node_grads[id(output.node)] = seed
    elif output.requires_grad:
        tensor_grads[id(output)] = seed

    def accumulate(mapping, key, value):
        current = mapping.get(key)
        if current is None:
            mapping[key] = value
        elif create_graph:
            mapping[key] = add(current, value)
        else:
            mapping[key] = _raw_add(current, value)

    for node in reversed(collect_nodes(output)):
        current_grad = node_grads.get(id(node))
        if current_grad is None:
            continue
        contributions = node.backward(current_grad, create_graph)
        for inp, contribution in zip(node.inputs, contributions):
            if contribution is None or not inp.requires_grad:
                continue
            if inp.node is None:
                accumulate(tensor_grads, id(inp), contribution)
            else:
                accumulate(node_grads, id(inp.node), contribution)

    results = []
    for inp in inputs:
        if inp.node is None:
            value = tensor_grads.get(id(inp))
        else:
            value = node_grads.get(id(inp.node))
        if value is None:
            value = Tensor(_raw_zeros(inp.shape), requires_grad=False)
        elif not isinstance(value, Tensor):
            value = Tensor(value, requires_grad=False)
        results.append(value)
    return results


def hvp(function, x, vector):
    output = function(x)
    gradient = grad(output, (x,), create_graph=True)[0]
    return grad(gradient, (x,), grad_output=vector, create_graph=False)[0]


def collect_nodes(output):
    if output.node is None:
        return []
    seen = set()
    waiting = [(output.node, False)]
    ordered = []
    while waiting:
        node, expanded = waiting.pop()
        if expanded:
            ordered.append(node)
            continue
        if id(node) in seen:
            continue
        seen.add(id(node))
        waiting.append((node, True))
        for inp in reversed(node.inputs):
            if inp.requires_grad and inp.node is not None:
                waiting.append((inp.node, False))
    return ordered


def _make_node(op, data, inputs, backward):
    node = Node(op, inputs, backward)
    output = Tensor(
        data,
        requires_grad=any(inp.requires_grad for inp in inputs),
    )
    output.node = node
    return output


def _as_tensor(value):
    if isinstance(value, Tensor):
        return value
    return Tensor(value, requires_grad=False)


def _is_number(value):
    return isinstance(value, (int, float)) and not isinstance(value, bool)


def _shape(data):
    if _is_number(data):
        return ()
    if not data:
        return (0,)
    return (len(data),) + _shape(data[0])


def _copy(data):
    if _is_number(data):
        return float(data)
    return [_copy(item) for item in data]


def _raw_zeros(shape):
    if not shape:
        return 0.0
    return [_raw_zeros(shape[1:]) for _ in range(shape[0])]


def _raw_broadcast_to(data, shape):
    if not shape:
        return float(data)
    return [_raw_broadcast_to(data, shape[1:]) for _ in range(shape[0])]


def _raw_add(a, b):
    if _is_number(a):
        return a + b
    return [_raw_add(left, right) for left, right in zip(a, b)]


def _raw_mul(a, b):
    if _is_number(a):
        return a * b
    return [_raw_mul(left, right) for left, right in zip(a, b)]


def _raw_sum_all(data):
    if _is_number(data):
        return float(data)
    return sum(_raw_sum_all(item) for item in data)


def _raw_matvec(matrix, vector):
    return [
        sum(matrix[row][col] * vector[col] for col in range(len(vector)))
        for row in range(len(matrix))
    ]


def _raw_transpose(matrix):
    return [list(row) for row in zip(*matrix)]


def _raw_outer(a, b):
    return [[a[i] * b[j] for j in range(len(b))] for i in range(len(a))]
