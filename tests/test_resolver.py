import unittest

from feature_resolver.errors import ConflictError, ModelError, ResolutionError
from feature_resolver.model import Registry, RootRequest
from feature_resolver.resolver import Resolver


def make_pkg(name, version, features=None, deps=None):
    return {
        "name": name,
        "version": version,
        "features": features or {},
        "dependencies": deps or [],
    }


def make_dep(name, **kwargs):
    data = {"name": name}
    data.update(kwargs)
    return data


def make_registry(*packages):
    return Registry.from_dict({"packages": list(packages)})


def make_request(root="app", req="*", features=(), disable=(), default_features=True):
    return RootRequest.from_dict(
        {
            "root": root,
            "req": req,
            "features": list(features),
            "disable": list(disable),
            "default_features": default_features,
        }
    )


def resolve(registry, request):
    return Resolver(registry).resolve(request)


def package_of(resolution, name):
    for package in resolution.packages:
        if package.name == name:
            return package
    raise AssertionError(f"package {name!r} not in closure: {resolution.packages!r}")


class DefaultAndForcedTest(unittest.TestCase):
    def setUp(self):
        self.registry = make_registry(
            make_pkg("app", "1.0.0", features={
                "default": ["cli"],
                "cli": [],
                "net": ["tls"],
                "tls": [],
            })
        )

    def test_default_features_are_seeded(self):
        result = resolve(self.registry, make_request())
        app = package_of(result, "app")
        self.assertEqual(set(app.features), {"default", "cli"})
        self.assertEqual(app.defaulted, ("default",))
        self.assertEqual(app.implied, ("cli",))

    def test_default_features_can_be_turned_off(self):
        result = resolve(self.registry, make_request(default_features=False))
        self.assertEqual(package_of(result, "app").features, ())

    def test_forced_features_and_transitive_implication(self):
        result = resolve(self.registry, make_request(features=["net"]))
        app = package_of(result, "app")
        self.assertIn("tls", app.features)
        self.assertEqual(app.forced, ("net",))
        self.assertIn("tls", app.implied)

    def test_repeated_feature_request_is_idempotent(self):
        once = resolve(self.registry, make_request(features=["net"]))
        twice = resolve(self.registry, make_request(features=["net", "net", "net"]))
        self.assertEqual(once, twice)

    def test_explicitly_disabled_unused_feature_is_reported(self):
        result = resolve(self.registry, make_request(disable=["tls"]))
        app = package_of(result, "app")
        self.assertEqual(app.disabled, ("tls",))
        self.assertNotIn("tls", app.features)


class DependencyActivationTest(unittest.TestCase):
    def setUp(self):
        self.registry = make_registry(
            make_pkg("app", "1.0.0", features={
                "default": [],
                "net": [],
                "minimal": [],
            }, deps=[
                make_dep("http", req=">=1.0.0", optional=True,
                         features=["client"], when="net and not minimal"),
                make_dep("util"),
                make_dep("plugin", optional=True),
            ]),
            make_pkg("http", "1.2.0", features={
                "default": ["core"],
                "core": [],
                "client": ["core"],
            }),
            make_pkg("util", "0.1.0", features={"default": []}),
            make_pkg("plugin", "2.0.0", features={"default": []}),
        )

    def test_unconditional_dependency_always_active(self):
        result = resolve(self.registry, make_request())
        package_of(result, "util")

    def test_boolean_expression_gates_optional_dependency(self):
        result = resolve(self.registry, make_request(features=["net"]))
        http = package_of(result, "http")
        self.assertEqual(set(http.features), {"default", "core", "client"})

    def test_negated_feature_blocks_dependency(self):
        result = resolve(self.registry, make_request(features=["net", "minimal"]))
        names = [package.name for package in result.packages]
        self.assertNotIn("http", names)

    def test_optional_dependency_implicit_feature(self):
        result = resolve(self.registry, make_request(features=["plugin"]))
        package_of(result, "plugin")

    def test_inactive_optional_dependency_is_absent(self):
        result = resolve(self.registry, make_request())
        names = [package.name for package in result.packages]
        self.assertNotIn("http", names)
        self.assertNotIn("plugin", names)


class ConflictTest(unittest.TestCase):
    def test_disable_vs_transitively_required_reports_source_chain(self):
        registry = make_registry(
            make_pkg("app", "1.0.0", features={
                "default": [],
                "net": ["tls"],
                "tls": [],
            })
        )
        with self.assertRaises(ConflictError) as caught:
            resolve(registry, make_request(features=["net"], disable=["tls"]))
        message = str(caught.exception)
        self.assertIn("explicitly disabled by root request", message)
        self.assertIn("'tls' implied by 'net'", message)
        self.assertIn("'net' required by root request", message)

    def test_disable_vs_forced_by_dependency_edge(self):
        registry = make_registry(
            make_pkg("app", "1.0.0", features={"default": [], "a": [], "b": []}, deps=[
                make_dep("child", optional=True, features=["x"], when="a"),
                make_dep("child", optional=True, disable=["x"], when="b"),
            ]),
            make_pkg("child", "1.0.0", features={"default": [], "x": []}),
        )
        with self.assertRaises(ConflictError) as caught:
            resolve(registry, make_request(features=["a", "b"]))
        message = str(caught.exception)
        self.assertIn("dependency 'child' of package 'app'", message)

    def test_disable_vs_default_feature(self):
        registry = make_registry(
            make_pkg("app", "1.0.0", features={"default": ["cli"], "cli": []})
        )
        with self.assertRaises(ConflictError):
            resolve(registry, make_request(disable=["cli"]))


class CycleTest(unittest.TestCase):
    def setUp(self):
        self.registry = make_registry(
            make_pkg("app", "1.0.0", features={
                "default": [],
                "seed": ["ping"],
                "ping": ["pong"],
                "pong": ["ping"],
                "loop": [],
            }, deps=[
                make_dep("left", optional=True, when="loop"),
                make_dep("right", optional=True, when="loop"),
            ]),
            make_pkg("left", "1.0.0", features={"default": [], "spin": []}, deps=[
                make_dep("right", optional=True, features=["spin"], when="spin"),
            ]),
            make_pkg("right", "1.0.0", features={"default": [], "spin": []}, deps=[
                make_dep("left", optional=True, features=["spin"], when="spin"),
            ]),
        )

    def test_unseeded_cycle_cannot_activate_and_is_reported(self):
        result = resolve(self.registry, make_request(features=["loop"]))
        left = package_of(result, "left")
        right = package_of(result, "right")
        self.assertNotIn("spin", left.features)
        self.assertNotIn("spin", right.features)
        warnings = [d for d in result.diagnostics if d.level == "warning"]
        spin_warnings = [
            d for d in warnings
            if set(d.cycle) == {"feature left/spin", "feature right/spin"}
        ]
        self.assertEqual(len(spin_warnings), 1)
        self.assertIn("without a root seed", spin_warnings[0].message)

    def test_seeded_cycle_activates(self):
        result = resolve(self.registry, make_request(features=["seed"]))
        app = package_of(result, "app")
        self.assertIn("ping", app.features)
        self.assertIn("pong", app.features)
        infos = [d for d in result.diagnostics if d.level == "info"]
        self.assertTrue(
            any(set(d.cycle) == {"feature app/ping", "feature app/pong"} for d in infos)
        )

    def test_cycle_without_any_seed_stays_dormant(self):
        result = resolve(self.registry, make_request())
        names = [package.name for package in result.packages]
        self.assertNotIn("left", names)
        self.assertNotIn("right", names)


class VersionSelectionTest(unittest.TestCase):
    def setUp(self):
        self.registry = make_registry(
            make_pkg("app", "1.0.0", features={"default": []}, deps=[
                make_dep("lib", req=">=1.0.0, <2.0.0"),
            ]),
            make_pkg("lib", "0.9.0", features={"default": []}),
            make_pkg("lib", "1.0.0", features={"default": []}),
            make_pkg("lib", "1.5.0", features={"default": []}),
            make_pkg("lib", "1.6.0-rc.1", features={"default": []}),
            make_pkg("lib", "2.0.0", features={"default": []}),
        )

    def test_highest_matching_version_wins(self):
        result = resolve(self.registry, make_request())
        self.assertEqual(package_of(result, "lib").version, "1.6.0-rc.1")

    def test_prerelease_ordering_within_range(self):
        registry = make_registry(
            make_pkg("app", "1.0.0", features={"default": []}, deps=[
                make_dep("lib", req=">=1.0.0, <1.6.0-rc.1"),
            ]),
            make_pkg("lib", "1.5.0", features={"default": []}),
            make_pkg("lib", "1.6.0-rc.1", features={"default": []}),
        )
        result = resolve(registry, make_request())
        self.assertEqual(package_of(result, "lib").version, "1.5.0")

    def test_unsatisfiable_constraint_reports_sources(self):
        registry = make_registry(
            make_pkg("app", "1.0.0", features={"default": []}, deps=[
                make_dep("lib", req=">=3.0.0"),
            ]),
            make_pkg("lib", "2.0.0", features={"default": []}),
        )
        with self.assertRaises(ResolutionError) as caught:
            resolve(registry, make_request())
        self.assertIn("dependency 'lib' of package 'app'", str(caught.exception))

    def test_unknown_package(self):
        registry = make_registry(make_pkg("app", "1.0.0", features={"default": []}))
        with self.assertRaises(ResolutionError):
            resolve(registry, make_request(root="ghost"))

    def test_unknown_feature_in_request(self):
        registry = make_registry(make_pkg("app", "1.0.0", features={"default": []}))
        with self.assertRaises(ResolutionError):
            resolve(registry, make_request(features=["nope"]))

    def test_root_version_requirement(self):
        registry = make_registry(
            make_pkg("app", "1.0.0", features={"default": []}),
            make_pkg("app", "2.1.0", features={"default": []}),
        )
        result = resolve(registry, make_request(req=">=2.0.0"))
        self.assertEqual(package_of(result, "app").version, "2.1.0")


class ModelValidationTest(unittest.TestCase):
    def test_unknown_implied_feature_rejected(self):
        with self.assertRaises(ModelError):
            make_registry(make_pkg("app", "1.0.0", features={"a": ["ghost"]}))

    def test_when_expression_validated_against_features(self):
        with self.assertRaises(ModelError):
            make_registry(make_pkg("app", "1.0.0", features={"a": []}, deps=[
                make_dep("x", optional=True, when="ghost"),
            ]))

    def test_duplicate_version_rejected(self):
        with self.assertRaises(ModelError):
            make_registry(
                make_pkg("app", "1.0.0"),
                make_pkg("app", "1.0.0"),
            )


if __name__ == "__main__":
    unittest.main()
