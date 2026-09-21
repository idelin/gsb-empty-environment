import unittest

from buildstate.model import DefinitionError, parse_definition


VALID = {
    "packages": {"lib": {"version": "1.2.3"}},
    "nodes": {
        "build": {
            "inputs": [{"package": "lib", "version": "1.2.3"}],
            "requires_artifacts": ["a.bin"],
        }
    },
}


class ModelTests(unittest.TestCase):
    def test_valid_definition(self):
        definition = parse_definition(VALID)
        self.assertEqual(str(definition.package_map["lib"].version), "1.2.3")
        self.assertEqual(definition.node_map["build"].required_artifacts, ("a.bin",))

    def test_undeclared_package_rejected(self):
        raw = {
            "packages": {},
            "nodes": {"n": {"inputs": [{"package": "ghost", "version": "1.0.0"}],
                            "requires_artifacts": ["x"]}},
        }
        with self.assertRaisesRegex(DefinitionError, "undeclared package"):
            parse_definition(raw)

    def test_input_version_mismatch_rejected(self):
        raw = {
            "packages": {"lib": {"version": "1.2.3"}},
            "nodes": {"n": {"inputs": [{"package": "lib", "version": "1.2.4"}],
                            "requires_artifacts": ["x"]}},
        }
        with self.assertRaisesRegex(DefinitionError, "requests 1.2.4"):
            parse_definition(raw)

    def test_prerelease_input_must_match(self):
        raw = {
            "packages": {"lib": {"version": "0.9.0-rc.2"}},
            "nodes": {"n": {"inputs": [{"package": "lib", "version": "0.9.0-rc.2"}],
                            "requires_artifacts": ["x"]}},
        }
        definition = parse_definition(raw)
        self.assertTrue(definition.package_map["lib"].version.is_prerelease)

    def test_bad_package_version(self):
        raw = {"packages": {"lib": {"version": "not-a-version"}},
               "nodes": {"n": {"inputs": [], "requires_artifacts": ["x"]}}}
        with self.assertRaises(DefinitionError):
            parse_definition(raw)

    def test_node_requires_artifact(self):
        raw = {"packages": {"lib": {"version": "1.2.3"}},
               "nodes": {"n": {"inputs": [], "requires_artifacts": []}}}
        with self.assertRaisesRegex(DefinitionError, "at least one artifact"):
            parse_definition(raw)

    def test_unknown_fields_rejected(self):
        raw = {
            "packages": {"lib": {"version": "1.2.3", "extra": 1}},
            "nodes": {},
        }
        with self.assertRaises(DefinitionError):
            parse_definition(raw)


if __name__ == "__main__":
    unittest.main()
