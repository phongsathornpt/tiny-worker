#!/usr/bin/env python3
"""Print the next stable tiny-worker release tag."""

from __future__ import annotations

import re
import subprocess
import sys


STABLE_TAG = re.compile(r"^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$")
BUMPS = {"patch", "minor", "major"}


def main() -> int:
    if len(sys.argv) != 2 or sys.argv[1] not in BUMPS:
        print("usage: calculate-release-version.py {patch|minor|major}", file=sys.stderr)
        return 2

    bump = sys.argv[1]
    tags = subprocess.run(
        ["git", "tag", "--list", "v*"], check=True, capture_output=True, text=True
    ).stdout.splitlines()
    versions = []
    for tag in tags:
        match = STABLE_TAG.fullmatch(tag)
        if match:
            versions.append(tuple(map(int, match.groups())))

    if not versions:
        print("v0.1.0")
        return 0

    major, minor, patch = max(versions)
    if bump == "major":
        major, minor, patch = major + 1, 0, 0
    elif bump == "minor":
        minor, patch = minor + 1, 0
    else:
        patch += 1

    print(f"v{major}.{minor}.{patch}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
