"""Shared fixtures for the test suite."""

CORR_STATS = {
    "tables": {
        "T": {
            "row_count": 4,
            "columns": {
                "a": {"domain": [0, 1], "null_count": 0,
                      "histogram": {"0": 2, "1": 2}},
                "b": {"domain": [0, 1], "null_count": 0,
                      "histogram": {"0": 2, "1": 2}},
            },
        }
    }
}
CORR_QUERY = {"select": {"table": "T", "where": {
    "and": [{"eq": ["a", 0]}, {"eq": ["b", 0]}]}}}

DUP_JOIN_STATS = {
    "tables": {
        "L": {
            "row_count": 4,
            "columns": {
                "k": {"domain": [1, 2], "null_count": 0,
                      "histogram": {"1": 3, "2": 1}},
                "v": {"domain": [0, 1], "null_count": 0,
                      "histogram": {"0": 2, "1": 2}},
            },
        },
        "R": {
            "row_count": 3,
            "columns": {
                "k2": {"domain": [1, 2], "null_count": 0,
                       "histogram": {"1": 2, "2": 1}},
            },
        },
    }
}
DUP_JOIN_QUERY = {"join": {
    "left": {"table": "L", "where": {"eq": ["v", 1]}},
    "right": {"table": "R"},
    "on": ["k", "k2"],
}}

ALL_NULL_STATS = {
    "tables": {
        "N": {
            "row_count": 3,
            "columns": {
                "n": {"domain": [1, 2], "null_count": 3, "histogram": {}},
            },
        }
    }
}

JOINT_STATS = {
    "tables": {
        "J": {
            "row_count": 4,
            "columns": {
                "a": {"domain": [0, 1, 2], "null_count": 0,
                      "histogram": {"0": 2, "1": 1, "2": 1}},
                "b": {"domain": [0, 1, 2], "null_count": 0,
                      "histogram": {"0": 1, "1": 2, "2": 1}},
            },
            "joint_counts": [
                {"columns": ["a", "b"],
                 "counts": {"0,0": 1, "0,1": 1, "1,2": 1, "2,1": 1}},
            ],
        }
    }
}
JOINT_QUERY = {"select": {"table": "J", "where": {
    "or": [{"range": ["a", 0, 1]}, {"eq": ["b", 2]}]}}}

INCOMPATIBLE_STATS = {
    "tables": {
        "T": {
            "row_count": 3,
            "columns": {
                "a": {"domain": [0, 1], "null_count": 0,
                      "histogram": {"0": 3}},
                "b": {"domain": [0, 1], "null_count": 0,
                      "histogram": {"0": 3}},
            },
            "joint_counts": [
                {"columns": ["a", "b"], "counts": {"0,0": 1}},
            ],
        }
    }
}

RESUMABLE_STATS = {
    "tables": {
        "S": {
            "row_count": 5,
            "columns": {
                "a": {"domain": [0, 1, 2], "null_count": 0,
                      "histogram": {"0": 1, "1": 2, "2": 2}},
                "b": {"domain": [0, 1, 2], "null_count": 1,
                      "histogram": {"0": 2, "1": 2, "2": 0}},
            },
        }
    }
}
RESUMABLE_QUERY = {"select": {"table": "S", "where": {
    "and": [{"range": ["a", 1, 2]},
            {"or": [{"eq": ["b", 0]}, {"eq": ["b", 1]}]}]}}}

NULL_JOIN_STATS = {
    "tables": {
        "L": {
            "row_count": 3,
            "columns": {
                "k": {"domain": [1], "null_count": 1, "histogram": {"1": 2}},
            },
        },
        "R": {
            "row_count": 2,
            "columns": {
                "k2": {"domain": [1], "null_count": 0, "histogram": {"1": 2}},
            },
        },
    }
}
NULL_JOIN_QUERY = {"join": {
    "left": {"table": "L"}, "right": {"table": "R"}, "on": ["k", "k2"],
}}
