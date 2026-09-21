from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Capacity:
    cpu: int
    memory: int


@dataclass(frozen=True)
class Task:
    id: str
    cpu: int
    memory: int
    duration: int
    deps: tuple[str, ...] = ()


@dataclass(frozen=True)
class Package:
    name: str
    version: str


@dataclass(frozen=True)
class BuildInput:
    name: str
    capacity: Capacity
    packages: tuple[Package, ...]
    tasks: tuple[Task, ...]
