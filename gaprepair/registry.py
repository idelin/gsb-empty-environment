"""Model version registry with rollback (withdrawal) support."""

from dataclasses import dataclass

from .config import RepairConfig


@dataclass(frozen=True)
class ModelVersion:
    name: str
    config: RepairConfig
    calibration: object = None
    note: str = ""


class ModelRegistry:
    def __init__(self):
        self._versions = {}
        self._history = []

    def register(self, version: ModelVersion) -> ModelVersion:
        if version.name in self._versions:
            raise ValueError(f"duplicate model version: {version.name}")
        self._versions[version.name] = version
        self._history.append(version.name)
        return version

    @property
    def active(self) -> ModelVersion:
        if not self._history:
            raise LookupError("no model version registered")
        return self._versions[self._history[-1]]

    def activate(self, name: str) -> ModelVersion:
        if name not in self._versions:
            raise KeyError(name)
        if name in self._history:
            self._history.remove(name)
        self._history.append(name)
        return self.active

    def rollback(self, name: str = None) -> ModelVersion:
        """Withdraw the active version, or roll back to a named version."""
        if name is not None:
            return self.activate(name)
        if len(self._history) < 2:
            raise LookupError("no earlier model version to roll back to")
        self._history.pop()
        return self.active

    @property
    def history(self) -> list:
        return list(self._history)
