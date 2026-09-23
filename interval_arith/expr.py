"""Expression trees with two semantics:

* ``interval(env)`` -- plain interval arithmetic, dependency-unaware:
  repeated references to the same variable are treated as independent,
  which is conservative (and we say so in the docs/tests).
* ``affine(env, cert)`` -- affine arithmetic: correlations between
  repeated references to the same variable are tracked exactly in the
  linear part; nonlinear remainders are bounded conservatively.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .core import Interval
from .affine import (AffineForm, affine_add, affine_sub, affine_mul,
                     affine_neg)


class Expr:
    def interval(self, env) -> Interval:
        raise NotImplementedError

    def affine(self, env, cert=None) -> AffineForm:
        raise NotImplementedError


@dataclass(frozen=True)
class Const(Expr):
    value: object

    def interval(self, env):
        return Interval.point(Fraction(self.value))

    def affine(self, env, cert=None):
        return AffineForm.constant(Fraction(self.value), cert)


@dataclass(frozen=True)
class Var(Expr):
    name: str

    def interval(self, env):
        return env[self.name]

    def affine(self, env, cert=None):
        # Same variable name -> same noise symbol -> correlation preserved.
        return AffineForm.from_interval(f"e_{self.name}", env[self.name], cert)


@dataclass(frozen=True)
class Neg(Expr):
    arg: Expr

    def interval(self, env):
        return -self.arg.interval(env)

    def affine(self, env, cert=None):
        return affine_neg(self.arg.affine(env, cert), cert)


@dataclass(frozen=True)
class Add(Expr):
    left: Expr
    right: Expr

    def interval(self, env):
        return self.left.interval(env) + self.right.interval(env)

    def affine(self, env, cert=None):
        return affine_add(self.left.affine(env, cert),
                          self.right.affine(env, cert), cert)


@dataclass(frozen=True)
class Sub(Expr):
    left: Expr
    right: Expr

    def interval(self, env):
        return self.left.interval(env) - self.right.interval(env)

    def affine(self, env, cert=None):
        return affine_sub(self.left.affine(env, cert),
                          self.right.affine(env, cert), cert)


@dataclass(frozen=True)
class Mul(Expr):
    left: Expr
    right: Expr

    def interval(self, env):
        return self.left.interval(env) * self.right.interval(env)

    def affine(self, env, cert=None):
        return affine_mul(self.left.affine(env, cert),
                          self.right.affine(env, cert), cert)


@dataclass(frozen=True)
class Square(Expr):
    arg: Expr

    def interval(self, env):
        return self.arg.interval(env).square()

    def affine(self, env, cert=None):
        f = self.arg.affine(env, cert)
        return affine_mul(f, f, cert)


def var(name: str) -> Var:
    return Var(name)


def const(v) -> Const:
    return Const(Fraction(v))
