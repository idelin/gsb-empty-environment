"""Shared fixtures: a synthetic US-style DST zone."""
import datetime as dt

from chronodur import SyntheticZone, Transition

EPOCH = dt.datetime(1970, 1, 1)
HOUR = 3600


def utc_seconds(year, month, day, hour=0, minute=0):
    delta = dt.datetime(year, month, day, hour, minute) - EPOCH
    return delta.days * 86400 + delta.seconds


def us_style_zone():
    """US-style DST: spring forward 2024-03-10, fall back 2024-11-03, etc."""
    return SyntheticZone("US-like", [
        Transition(utc_seconds(2024, 3, 10, 7), -4 * HOUR, "EDT"),
        Transition(utc_seconds(2024, 11, 3, 6), -5 * HOUR, "EST"),
        Transition(utc_seconds(2025, 3, 9, 7), -4 * HOUR, "EDT"),
        Transition(utc_seconds(2025, 11, 2, 6), -5 * HOUR, "EST"),
    ], base_offset_seconds=-5 * HOUR)
