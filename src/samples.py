"""Executable examples used by the demo and tests."""

from __future__ import annotations

from automata import EPSILON, FiniteTransducer


def expander() -> FiniteTransducer:
    return FiniteTransducer(
        name="expander",
        states={"S"},
        input_alphabet="abx",
        output_alphabet=("0", "1"),
        transitions={
            "S": {
                "a": ("S", ("0", "1")),
                "b": ("S", ("1", "0")),
                "x": ("S", (EPSILON,)),
            }
        },
        initial="S",
        accepting={"S"},
    )


def pairer() -> FiniteTransducer:
    return FiniteTransducer(
        name="pairer",
        states=("EVEN", "ODD"),
        input_alphabet=("0", "1"),
        output_alphabet=("A0", "A1", "B0", "B1", "M"),
        transitions={
            "EVEN": {
                "0": ("ODD", ("A0",)),
                "1": ("ODD", ("A1",)),
            },
            "ODD": {
                "0": ("EVEN", ("B0", "M")),
                "1": ("EVEN", ("B1", "M")),
            },
        },
        initial="EVEN",
        accepting={"EVEN"},
    )


def grouper() -> FiniteTransducer:
    return FiniteTransducer(
        name="grouper",
        states=("WAIT", "HAVE_A", "HAVE_B"),
        input_alphabet=("A0", "A1", "B0", "B1", "M"),
        output_alphabet=("00", "01", "10", "11", "|", "END"),
        transitions={
            "WAIT": {
                "A0": ("HAVE_A", ()),
                "A1": ("HAVE_A", ()),
                "B0": ("WAIT", ()),
                "B1": ("WAIT", ()),
                "M": ("WAIT", ()),
            },
            "HAVE_A": {
                "A0": ("HAVE_A", (EPSILON,)),
                "A1": ("HAVE_A", (EPSILON,)),
                "B0": ("HAVE_B", ("00",)),
                "B1": ("HAVE_B", ("01",)),
                "M": ("HAVE_A", (EPSILON,)),
            },
            "HAVE_B": {
                "A0": ("HAVE_A", ("|",)),
                "A1": ("HAVE_A", ("|",)),
                "B0": ("WAIT", ("|",)),
                "B1": ("WAIT", ("|",)),
                "M": ("WAIT", ("|",)),
            },
        },
        initial="WAIT",
        accepting={"WAIT"},
        finish_outputs={"WAIT": ("END",)},
    )


def pipeline_stages() -> tuple[FiniteTransducer, ...]:
    return (expander(), pairer(), grouper())


def stopper() -> FiniteTransducer:
    return FiniteTransducer(
        name="stopper",
        states=("RUN", "DONE"),
        input_alphabet="abc",
        output_alphabet=("i", "FOUND"),
        transitions={
            "RUN": {
                "a": ("RUN", ("i", "i")),
                "b": ("DONE", ("i", "FOUND")),
                "c": ("RUN", ("i",)),
            }
        },
        initial="RUN",
        accepting=set(),
        halting={"DONE"},
    )


def stop_signal_source() -> FiniteTransducer:
    return FiniteTransducer(
        name="stop-signal-source",
        states={"S"},
        input_alphabet=("a", "b"),
        output_alphabet=("a", "b", "c"),
        transitions={
            "S": {
                "a": ("S", ("c", "c")),
                "b": ("S", ("b",)),
            }
        },
        initial="S",
        accepting={"S"},
    )
