import unittest

from abicompat.typesys import (
    EQUAL,
    SUBTYPE,
    SUPERTYPE,
    TypeLattice,
    UNRELATED,
    UNKNOWN,
)


class TypeLatticeTest(unittest.TestCase):
    def setUp(self):
        self.lattice = TypeLattice()

    def test_numeric_chain(self):
        self.assertEqual(self.lattice.relation("bool", "int"), SUBTYPE)
        self.assertEqual(self.lattice.relation("int", "bool"), SUPERTYPE)
        self.assertTrue(self.lattice.is_subtype("bool", "object"))
        self.assertEqual(self.lattice.relation("int", "int"), EQUAL)
        self.assertEqual(self.lattice.relation("str", "bytes"), UNRELATED)

    def test_input_widening_is_compatible(self):
        # int -> float widens accepted values: callers passing int still work.
        self.assertTrue(self.lattice.parameter_compatible("int", "float"))
        self.assertFalse(self.lattice.parameter_compatible("float", "int"))
        self.assertTrue(self.lattice.parameter_compatible("int", "int"))

    def test_output_narrowing_is_compatible(self):
        # Returning int where float was promised satisfies the old callers.
        self.assertTrue(self.lattice.output_compatible("float", "int"))
        self.assertFalse(self.lattice.output_compatible("int", "float"))

    def test_unknown_types_are_not_silently_compatible(self):
        lattice = TypeLattice({"Widget": ("Gadget",)})
        self.assertEqual(lattice.relation("Widget", "Gadget"), SUBTYPE)
        self.assertEqual(lattice.relation("Mystery", "Gadget"), UNKNOWN)

    def test_object_root_is_known_supertype(self):
        self.assertTrue(self.lattice.parameter_compatible("str", "object"))
        self.assertFalse(self.lattice.output_compatible("str", "object"))


if __name__ == "__main__":
    unittest.main()
