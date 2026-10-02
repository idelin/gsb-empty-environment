"""minivmap: batch-transform pure single-sample graphs with axis tracking,
rejection of sample-independence violations, transformation certificates and
independently verified reverse-mode gradients.  Stdlib only."""
from . import tensor, graph, batch, grad, verify  # noqa: F401
