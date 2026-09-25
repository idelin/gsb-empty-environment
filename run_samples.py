#!/usr/bin/env python3
"""Replay every scenario and print the decision records as JSON."""
import json

from joinplanner import samples


def main():
    print(json.dumps(samples.run_all(), indent=2, default=str, sort_keys=True))


if __name__ == "__main__":
    main()
