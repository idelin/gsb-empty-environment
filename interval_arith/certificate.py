"""Derivation certificates.

Every transformation records a step: the rule applied, the domain on
which the rule is valid (``domain_note``), and enough data to re-derive
the result.  ``Certificate.verify()`` replays every step with exact
rational arithmetic and checks the final inclusion claim.

Sampling (see ``sampling.py``) may only be used to search for
counterexamples; it can never prove inclusion.  Only a verified
certificate proves that a claimed bound holds.
"""

from __future__ import annotations

from dataclasses import dataclass, field


@dataclass
class Step:
    rule: str
    domain_note: str
    inputs: tuple = ()
    output: object = None

    def verify(self) -> bool:
        from .affine import (AffineForm, affine_add, affine_mul, affine_neg,
                             affine_scale, affine_sub)
        if self.rule == "const":
            (value,) = self.inputs
            return self.output == AffineForm.constant(value)
        if self.rule == "introduce":
            symbol, iv = self.inputs
            return self.output == AffineForm.from_interval(symbol, iv)
        if self.rule == "affine_add":
            f, g = self.inputs
            return self.output == affine_add(f, g)
        if self.rule == "affine_sub":
            f, g = self.inputs
            return self.output == affine_sub(f, g)
        if self.rule == "affine_neg":
            (f,) = self.inputs
            return self.output == affine_neg(f)
        if self.rule == "affine_scale":
            f, k = self.inputs
            return self.output == affine_scale(f, k)
        if self.rule == "affine_mul":
            f, g = self.inputs
            return self.output == affine_mul(f, g)
        if self.rule == "range":
            (form,) = self.inputs
            return self.output == form.range()
        if self.rule == "inclusion":
            computed, claimed = self.inputs
            return computed.is_subset(claimed)
        raise ValueError(f"unknown rule {self.rule!r}")


@dataclass
class Certificate:
    steps: list = field(default_factory=list)

    def add(self, step: Step) -> Step:
        self.steps.append(step)
        return step

    def verify(self) -> bool:
        return all(step.verify() for step in self.steps)

    def explain(self) -> str:
        return "\n".join(
            f"{i}. [{s.rule}] {s.domain_note}"
            for i, s in enumerate(self.steps, 1)
        )


def certify_range(form, claimed, cert: Certificate):
    """Append the final range + inclusion steps linking an affine form to a
    claimed interval bound.  Returns the certified range."""
    r = form.range()
    cert.add(Step("range",
                  "range = exact linear radius + conservative nonlinear remainder",
                  (form,), r))
    cert.add(Step("inclusion",
                  "claimed bound must contain the certified range",
                  (r, claimed), True))
    return r
