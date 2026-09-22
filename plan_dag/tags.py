"""Small tagged-union registry used for typed serialization.

Every serializable object (operator spec or expression) materializes as
``{"@type": <tag>, ...fields}``. Looking up a tag goes through an explicit
allow-list registry; there is no dynamic import, eval, or code execution.
"""

from __future__ import annotations

from plan_dag.errors import InvalidDocumentError, UnknownOperatorError


class TagRegistry:
    def __init__(self, kind: str):
        self.kind = kind
        self._tags: dict[str, type] = {}

    def register(self, cls: type) -> type:
        tag = cls.TAG
        if tag in self._tags:
            raise RuntimeError(f"{self.kind} tag {tag!r} registered twice")
        self._tags[tag] = cls
        return cls

    def resolve(self, tag: object) -> type:
        if not isinstance(tag, str):
            raise InvalidDocumentError(
                f"{self.kind} '@type' must be a string, got {type(tag).__name__}"
            )
        try:
            return self._tags[tag]
        except KeyError:
            raise UnknownOperatorError(
                f"unknown {self.kind} {tag!r}; known: {sorted(self._tags)}"
            ) from None

    def tags(self) -> list[str]:
        return sorted(self._tags)


EXPR_REGISTRY = TagRegistry("expression")
SPEC_REGISTRY = TagRegistry("operator")


def require_object(data: object, what: str) -> dict:
    if not isinstance(data, dict):
        raise InvalidDocumentError(f"{what} must be an object, got {type(data).__name__}")
    return data


def require_tag(data: dict, registry: TagRegistry, what: str) -> tuple[str, type]:
    if "@type" not in data:
        raise InvalidDocumentError(f"{what} is missing '@type' tag")
    tag = data["@type"]
    cls = registry.resolve(tag)
    return tag, cls


def check_type(value: object, expected: type | tuple[type, ...], what: str) -> None:
    if isinstance(value, bool) and not (expected is bool or (isinstance(expected, tuple) and bool in expected)):
        raise InvalidDocumentError(f"{what} must be {expected}, got bool")
    if not isinstance(value, expected):
        names = expected if isinstance(expected, tuple) else (expected,)
        need = "/".join(getattr(t, "__name__", str(t)) for t in names)
        raise InvalidDocumentError(
            f"{what} must be {need}, got {type(value).__name__}"
        )


def require_keys(data: dict, required: set[str], allowed: set[str], what: str) -> None:
    keys = set(data)
    missing = required - keys
    if missing:
        raise InvalidDocumentError(f"{what} missing keys {sorted(missing)}")
    extra = keys - allowed
    if extra:
        raise InvalidDocumentError(f"{what} has unknown keys {sorted(extra)}")


def json_scalar(value: object, what: str) -> object:
    """Validate that a value is a JSON-safe scalar / list / object."""
    if value is None or isinstance(value, (str, bool, int, float)):
        return value
    if isinstance(value, list):
        return [json_scalar(v, f"{what}[{i}]") for i, v in enumerate(value)]
    if isinstance(value, dict):
        return {str(k): json_scalar(v, f"{what}.{k}") for k, v in value.items()}
    raise InvalidDocumentError(f"{what} is not JSON-serializable: {type(value).__name__}")
