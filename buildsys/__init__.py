"""buildsys: conditional-dependency build set planner.

Plans the minimal concrete build set from local JSON package/build
descriptions. Dependency edges carry target-OS / arch / stage conditions,
host tools and target artifacts are cached separately, and every
condition evaluation is recorded as evidence.
"""

__version__ = "0.1.0"
