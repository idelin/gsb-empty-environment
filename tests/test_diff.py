import unittest

from abicompat.diff import (
    BECAME_PRIVATE,
    KIND_CHANGED,
    MARKER_REMOVED,
    PARAM_NARROWED,
    PARAM_REMOVED,
    PARAM_REORDERED,
    PARAM_REQUIRED,
    REMOVED,
    RETURN_WIDENED,
    VALUE_WIDENED,
    diff_releases,
)
from abicompat.model import Catalog
from abicompat.typesys import TypeLattice


def make_catalog(releases):
    return Catalog.from_json({"packages": {"p": {"releases": releases}}})


def reasons(catalog, old="1.0.0", new="2.0.0"):
    package = catalog.packages["p"]
    return [
        change.reason
        for change in diff_releases(
            "p",
            package.release(old),
            package.release(new),
            TypeLattice(),
        )
    ]


class DiffTest(unittest.TestCase):
    def test_deleted_public_symbol_breaks_but_private_does_not(self):
        catalog = make_catalog(
            {
                "1.0.0": {
                    "symbols": {
                        "gone": {"kind": "function", "return_type": "int"},
                        "_hidden": {"kind": "function", "return_type": "int"},
                    }
                },
                "2.0.0": {"symbols": {}},
            }
        )
        self.assertEqual(reasons(catalog), [REMOVED])

    def test_symbol_became_private_breaks(self):
        catalog = make_catalog(
            {
                "1.0.0": {
                    "symbols": {"util": {"kind": "function", "private": False}}
                },
                "2.0.0": {
                    "symbols": {"util": {"kind": "function", "private": True}}
                },
            }
        )
        self.assertEqual(reasons(catalog), [BECAME_PRIVATE])

    def test_kind_change_is_a_break(self):
        catalog = make_catalog(
            {
                "1.0.0": {"symbols": {"x": {"kind": "function"}}},
                "2.0.0": {"symbols": {"x": {"kind": "constant"}}},
            }
        )
        self.assertEqual(reasons(catalog), [KIND_CHANGED])

    def test_parameter_widening_compatible_narrowing_breaks(self):
        catalog = make_catalog(
            {
                "1.0.0": {
                    "symbols": {
                        "widen": {
                            "kind": "function",
                            "params": [{"name": "x", "type": "int"}],
                        },
                        "narrow": {
                            "kind": "function",
                            "params": [{"name": "x", "type": "float"}],
                        },
                    }
                },
                "2.0.0": {
                    "symbols": {
                        "widen": {
                            "kind": "function",
                            "params": [{"name": "x", "type": "float"}],
                        },
                        "narrow": {
                            "kind": "function",
                            "params": [{"name": "x", "type": "int"}],
                        },
                    }
                },
            }
        )
        self.assertEqual(reasons(catalog), [PARAM_NARROWED])

    def test_return_narrowing_compatible_widening_breaks(self):
        catalog = make_catalog(
            {
                "1.0.0": {
                    "symbols": {
                        "a": {"kind": "function", "return_type": "int"},
                        "b": {"kind": "function", "return_type": "float"},
                    }
                },
                "2.0.0": {
                    "symbols": {
                        "a": {"kind": "function", "return_type": "float"},
                        "b": {"kind": "function", "return_type": "int"},
                    }
                },
            }
        )
        self.assertEqual(reasons(catalog), [RETURN_WIDENED])

    def test_parameter_lifecycle_and_order(self):
        catalog = make_catalog(
            {
                "1.0.0": {
                    "symbols": {
                        "f": {
                            "kind": "function",
                            "params": [
                                {"name": "a"},
                                {"name": "b", "required": False},
                                {"name": "d", "required": False},
                            ],
                        }
                    }
                },
                "2.0.0": {
                    "symbols": {
                        "f": {
                            "kind": "function",
                            "params": [
                                {"name": "b"},
                                {"name": "a", "required": True},
                                {"name": "c", "required": True},
                            ],
                        }
                    }
                },
            }
        )
        self.assertEqual(
            sorted(reasons(catalog)),
            sorted([PARAM_REMOVED, PARAM_REQUIRED, PARAM_REQUIRED, PARAM_REORDERED]),
        )

    def test_optional_added_parameter_is_compatible(self):
        catalog = make_catalog(
            {
                "1.0.0": {"symbols": {"f": {"kind": "function", "params": []}}},
                "2.0.0": {
                    "symbols": {
                        "f": {
                            "kind": "function",
                            "params": [{"name": "x", "required": False}],
                        }
                    }
                },
            }
        )
        self.assertEqual(reasons(catalog), [])

    def test_constant_widening_and_marker_removal_break(self):
        catalog = make_catalog(
            {
                "1.0.0": {
                    "symbols": {
                        "c": {
                            "kind": "constant",
                            "value_type": "int",
                            "markers": ["immutable"],
                        }
                    }
                },
                "2.0.0": {
                    "symbols": {
                        "c": {"kind": "constant", "value_type": "object"}
                    }
                },
            }
        )
        self.assertEqual(sorted(reasons(catalog)), sorted([VALUE_WIDENED, MARKER_REMOVED]))


if __name__ == "__main__":
    unittest.main()
