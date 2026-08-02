"""Pure release-selection policy shared by the publishing workflow tests.

Keeping this logic out of the workflow YAML makes the publisher's SemVer and
asset-identity contract executable and reviewable without contacting GitHub.
"""

from __future__ import annotations

import functools
import re
from collections.abc import Iterable, Mapping
from typing import Any, Optional


MIN_INSTALLER_BYTES = 1 << 20
MAX_INSTALLER_BYTES = 512 << 20

SEMVER_PATTERN = re.compile(
    r"^(?:[A-Za-z]+)?([0-9]+)\.([0-9]+)\.([0-9]+)"
    r"(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?"
    r"(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$"
)
CANONICAL_INSTALLER_PATTERN = re.compile(
    r"^CodexLB_Installer_[0-9A-Za-z][0-9A-Za-z._-]*\.exe$",
    re.IGNORECASE,
)
SHA256_DIGEST_PATTERN = re.compile(r"^sha256:[0-9a-fA-F]{64}$")

SemanticVersion = tuple[tuple[int, int, int], tuple[str, ...]]


def parse_semver(tag: object) -> Optional[SemanticVersion]:
    if not isinstance(tag, str):
        return None
    match = SEMVER_PATTERN.fullmatch(tag.strip())
    if match is None:
        return None

    raw_numbers = match.group(1, 2, 3)
    if any(len(value) > 1 and value.startswith("0") for value in raw_numbers):
        return None
    prerelease = tuple(match.group(4).split(".")) if match.group(4) else ()
    if any(value.isdigit() and len(value) > 1 and value.startswith("0") for value in prerelease):
        return None
    numbers = (int(raw_numbers[0]), int(raw_numbers[1]), int(raw_numbers[2]))
    return numbers, prerelease


def _compare_prerelease(left: tuple[str, ...], right: tuple[str, ...]) -> int:
    if not left and not right:
        return 0
    if not left:
        return 1
    if not right:
        return -1
    for left_id, right_id in zip(left, right):
        if left_id == right_id:
            continue
        left_numeric = left_id.isdigit()
        right_numeric = right_id.isdigit()
        if left_numeric and right_numeric:
            return 1 if int(left_id) > int(right_id) else -1
        if left_numeric:
            return -1
        if right_numeric:
            return 1
        return 1 if left_id > right_id else -1
    return (len(left) > len(right)) - (len(left) < len(right))


def compare_release_versions(left: Mapping[str, Any], right: Mapping[str, Any]) -> int:
    left_version = parse_semver(left.get("tag_name"))
    right_version = parse_semver(right.get("tag_name"))
    if left_version is None or right_version is None:
        raise ValueError("compare_release_versions received an unsupported tag")
    if left_version[0] != right_version[0]:
        return (left_version[0] > right_version[0]) - (left_version[0] < right_version[0])
    return _compare_prerelease(left_version[1], right_version[1])


def newest_release(releases: Iterable[Mapping[str, Any]]) -> Optional[Mapping[str, Any]]:
    candidates = list(releases)
    if not candidates:
        return None
    return max(candidates, key=functools.cmp_to_key(compare_release_versions))


def release_is_supported(release: Mapping[str, Any]) -> bool:
    return not bool(release.get("draft")) and parse_semver(release.get("tag_name")) is not None


def installer_name_for_tag(tag: object) -> Optional[str]:
    if not isinstance(tag, str) or parse_semver(tag) is None:
        return None
    first_digit = re.search(r"\d", tag)
    if first_digit is None:
        return None
    version = tag[first_digit.start() :]
    safe_version = re.sub(r"[^0-9A-Za-z._]", "_", version)
    return f"CodexLB_Installer_{safe_version}.exe"


def has_verified_installer(
    release: Mapping[str, Any],
    upstream_release_by_tag: Mapping[str, Mapping[str, Any]],
) -> bool:
    tag = release.get("tag_name")
    if not isinstance(tag, str):
        return False
    upstream_release = upstream_release_by_tag.get(tag)
    if (
        bool(release.get("draft"))
        or upstream_release is None
        or bool(release.get("prerelease")) != bool(upstream_release.get("prerelease"))
    ):
        return False

    expected_name = installer_name_for_tag(tag)
    if expected_name is None:
        return False
    assets = release.get("assets")
    if not isinstance(assets, list):
        return False
    installers = [
        asset
        for asset in assets
        if isinstance(asset, Mapping)
        and isinstance(asset.get("name"), str)
        and CANONICAL_INSTALLER_PATTERN.fullmatch(asset["name"])
    ]
    if len(installers) != 1:
        return False

    asset = installers[0]
    size = asset.get("size")
    digest = asset.get("digest")
    return (
        asset.get("name") == expected_name
        and isinstance(size, int)
        and not isinstance(size, bool)
        and MIN_INSTALLER_BYTES <= size <= MAX_INSTALLER_BYTES
        and isinstance(digest, str)
        and SHA256_DIGEST_PATTERN.fullmatch(digest) is not None
    )
