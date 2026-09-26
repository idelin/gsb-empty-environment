"""Reparameterised sampling from explicit keys.

All samplers are pure functions of a PRNGKey: sampling never mutates any
state, so the same key + shape + parameters always yields the same draw
(this is what makes checkpoint recomputation exact).

Public distribution formulas
----------------------------
uniform(key, shape):
    Take the top 53 bits w of each 64-bit word and return
        u = w * 2**-53        in [0, 1)

normal(key, shape, mean, std)  (Box-Muller, both outputs used):
    For each pair of uniforms (u1, u2), with u1 clamped to >= 2**-53:
        r  = sqrt(-2 * ln(u1))
        z0 = r * cos(2*pi*u2)
        z1 = r * sin(2*pi*u2)
    The returned value is  mean + std * z.
    Reparameterisation: d/dmean = 1, d/dstd = z.

bernoulli(key, p, shape):
    Returns 1.0 where u < p else 0.0, with u ~ uniform.
"""

from __future__ import annotations

import math

from .prng import PRNGKey, random_bits
from .tensor import Tensor, numel

_TWO_POW_NEG_53 = 2.0**-53
_TWO_PI = 2.0 * math.pi


def _check_shape(shape) -> tuple[int, ...]:
    shape = tuple(shape)
    numel(shape)  # validates non-negative dims
    return shape


def uniform(k: PRNGKey, shape) -> Tensor:
    """iid U[0, 1) samples; see module docstring for the formula."""
    shape = _check_shape(shape)
    n = numel(shape)
    bits = random_bits(k, n)
    return Tensor([(w >> 11) * _TWO_POW_NEG_53 for w in bits], shape)


def normal(k: PRNGKey, shape, mean: float = 0.0, std: float = 1.0) -> Tensor:
    """iid N(mean, std**2) via Box-Muller; see module docstring."""
    if not math.isfinite(mean):
        raise ValueError("mean must be finite")
    if not (math.isfinite(std) and std > 0.0):
        raise ValueError("std must be a positive finite number")
    shape = _check_shape(shape)
    n = numel(shape)
    n_uniforms = n + (n & 1)  # Box-Muller consumes uniforms in pairs
    us = uniform(k, (n_uniforms,)).data
    out: list[float] = []
    for i in range(0, n_uniforms, 2):
        u1 = max(us[i], _TWO_POW_NEG_53)
        u2 = us[i + 1]
        r = math.sqrt(-2.0 * math.log(u1))
        out.append(mean + std * r * math.cos(_TWO_PI * u2))
        if len(out) < n:
            out.append(mean + std * r * math.sin(_TWO_PI * u2))
    return Tensor(out, shape)


def bernoulli(k: PRNGKey, p: float, shape) -> Tensor:
    """iid Bernoulli(p) as 1.0/0.0 floats; see module docstring."""
    if not (0.0 <= p <= 1.0):
        raise ValueError(f"p must be in [0, 1], got {p}")
    shape = _check_shape(shape)
    us = uniform(k, shape)
    return us.map(lambda u: 1.0 if u < p else 0.0)
