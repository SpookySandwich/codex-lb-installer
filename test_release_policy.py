import unittest

import bundle_build
import release_policy


class ReleasePolicyTests(unittest.TestCase):
    def test_semver_precedence_matches_runtime_contract(self) -> None:
        ordered = [
            "1.0.0-alpha",
            "1.0.0-alpha.1",
            "1.0.0-alpha.beta",
            "1.0.0-beta",
            "1.0.0-beta.2",
            "1.0.0-beta.11",
            "1.0.0-rc.1",
            "1.0.0",
            "2.0.0",
            "10.0.0-alpha.1",
            "10.0.0",
        ]
        releases = [{"tag_name": tag} for tag in reversed(ordered)]
        self.assertEqual(release_policy.newest_release(releases), {"tag_name": "10.0.0"})

        for tag in ordered:
            self.assertIsNotNone(release_policy.parse_semver(tag), tag)
        for tag in (None, "", "1.2", "01.2.3", "1.2.3-alpha.01", "1.2.3_bad"):
            self.assertIsNone(release_policy.parse_semver(tag), tag)

    def test_installer_name_matches_builder(self) -> None:
        for prefix in ("", "v", "wv"):
            version = "1.20.2-beta.1+build.7"
            self.assertEqual(
                release_policy.installer_name_for_tag(prefix + version),
                bundle_build.installer_output_basename(version) + ".exe",
            )

    def test_verified_installer_requires_exact_unambiguous_asset(self) -> None:
        tag = "v1.2.3"
        upstream = {tag: {"tag_name": tag, "prerelease": False}}
        asset = {
            "name": "CodexLB_Installer_1.2.3.exe",
            "size": release_policy.MIN_INSTALLER_BYTES,
            "digest": "sha256:" + "a" * 64,
        }
        release = {"tag_name": tag, "prerelease": False, "assets": [asset]}
        self.assertTrue(release_policy.has_verified_installer(release, upstream))

        mutations = {
            "draft": {**release, "draft": True},
            "wrong prerelease flag": {**release, "prerelease": True},
            "wrong asset identity": {
                **release,
                "assets": [{**asset, "name": "CodexLB_Installer_9.9.9.exe"}],
            },
            "ambiguous assets": {
                **release,
                "assets": [asset, {**asset, "name": "CodexLB_Installer_extra.exe"}],
            },
            "missing digest": {**release, "assets": [{**asset, "digest": None}]},
            "too small": {**release, "assets": [{**asset, "size": 1}]},
        }
        for name, mutated in mutations.items():
            with self.subTest(name=name):
                self.assertFalse(release_policy.has_verified_installer(mutated, upstream))


if __name__ == "__main__":
    unittest.main()
