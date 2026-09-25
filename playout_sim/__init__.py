"""Playout buffer, clock estimation and adaptive target delay simulation.

Pure standard library. No audio devices: sample arrays and the sample
rate come from local fixtures, time comes from a discrete event sim.
"""

from .world import World, DEFAULTS
from .report import conservation_report

__all__ = ["World", "DEFAULTS", "conservation_report"]
