import hashlib
import io
import os
import tempfile
import unittest
import zipfile
from pathlib import Path

import bundle_build


class BuildHelperTests(unittest.TestCase):
    def test_validate_app_version(self) -> None:
        self.assertEqual(bundle_build.validate_app_version(" 1.20.2-beta.1 "), "1.20.2-beta.1")
        for invalid in (
            "",
            "1.2",
            "v1.2.3",
            "01.2.3",
            "1.2.3-alpha.01",
            "1.2.3 bad",
            "1.2.3\nInjected=yes",
        ):
            with self.subTest(invalid=invalid), self.assertRaises(ValueError):
                bundle_build.validate_app_version(invalid)

    def test_validate_build_sha_requires_full_hex_commit(self) -> None:
        uppercase = "ABCDEF0123456789ABCDEF0123456789ABCDEF01"
        self.assertEqual(bundle_build.validate_build_sha(uppercase), uppercase.lower())
        for invalid in ("abc1234", "g" * 40, "a" * 39, "a" * 41):
            with self.subTest(invalid=invalid), self.assertRaises(ValueError):
                bundle_build.validate_build_sha(invalid)

    def test_validate_payload_id_requires_sha256_hex(self) -> None:
        uppercase = "ABCDEF01" * 8
        self.assertEqual(bundle_build.validate_payload_id(uppercase), uppercase.lower())
        for invalid in ("legacy", "g" * 64, "a" * 63, "a" * 65):
            with self.subTest(invalid=invalid), self.assertRaises(ValueError):
                bundle_build.validate_payload_id(invalid)

    def test_validate_update_channel(self) -> None:
        self.assertEqual(bundle_build.validate_update_channel(" Stable "), "stable")
        self.assertEqual(bundle_build.validate_update_channel("EDGE"), "edge")
        self.assertEqual(bundle_build.validate_update_channel("Beta"), "beta")
        # "release" is the user-facing alias for the historical stable value.
        self.assertEqual(bundle_build.validate_update_channel("Release"), "stable")
        with self.assertRaises(ValueError):
            bundle_build.validate_update_channel("nightly")

    def test_channel_qualified_version_marks_only_edge_builds(self) -> None:
        sha = "c539a200c301e5cdf2cf524dea336e1c40094bbd"
        # Edge builds are stamped with the upstream commit they came from,
        # because main and the tag it precedes share a version number.
        self.assertEqual(
            bundle_build.channel_qualified_version("1.23.0-beta.2", "edge", sha),
            "1.23.0-beta.2+edge.c539a20",
        )
        # Tagged channels are the tag, so their number is already honest.
        for channel in ("stable", "release", "beta"):
            with self.subTest(channel=channel):
                self.assertEqual(
                    bundle_build.channel_qualified_version("1.23.0-beta.2", channel, sha),
                    "1.23.0-beta.2",
                )
        # Never stack a second suffix onto a version that already has one.
        self.assertEqual(
            bundle_build.channel_qualified_version("1.23.0-beta.2+edge.abcdef0", "edge", sha),
            "1.23.0-beta.2+edge.abcdef0",
        )
        # The result must remain a version the builder accepts everywhere.
        stamped = bundle_build.channel_qualified_version("1.23.0", "edge", sha)
        self.assertEqual(bundle_build.validate_app_version(stamped), stamped)
        self.assertEqual(
            bundle_build.installer_output_basename(stamped),
            "CodexLB_Installer_1.23.0_edge.c539a20",
        )
        # A malformed upstream sha must not silently produce a bogus version.
        with self.assertRaises(ValueError):
            bundle_build.channel_qualified_version("1.23.0", "edge", "not-a-sha")

    def test_installer_output_basename_is_safe(self) -> None:
        self.assertEqual(
            bundle_build.installer_output_basename("1.20.2-beta.1+build.7"),
            "CodexLB_Installer_1.20.2_beta.1_build.7",
        )

    def test_get_wheel_path_requires_exactly_one_wheel(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            dist_dir = Path(temp_dir)
            with self.assertRaises(RuntimeError):
                bundle_build.get_wheel_path(dist_dir)

            expected = dist_dir / "codex_lb-1.2.3-py3-none-any.whl"
            expected.touch()
            self.assertEqual(bundle_build.get_wheel_path(dist_dir), expected)

            (dist_dir / "other-1.0.0-py3-none-any.whl").touch()
            with self.assertRaises(RuntimeError):
                bundle_build.get_wheel_path(dist_dir)

    def test_validate_python_archive_checks_hash_and_required_files(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            archive_path = Path(temp_dir) / "python.zip"
            abi_tag = bundle_build.python_abi_tag()
            with zipfile.ZipFile(archive_path, "w") as archive:
                archive.writestr("python.exe", b"MZ")
                archive.writestr(f"{abi_tag}._pth", b"")
                archive.writestr(f"{abi_tag}.zip", b"")

            digest = hashlib.sha256(archive_path.read_bytes()).hexdigest()
            bundle_build.validate_python_archive(archive_path, digest)
            with self.assertRaises(ValueError):
                bundle_build.validate_python_archive(archive_path, "0" * 64)

    def test_copy_stream_limited_bounds_and_counts_download(self) -> None:
        output = io.BytesIO()
        self.assertEqual(bundle_build.copy_stream_limited(io.BytesIO(b"abcd"), output, 4, 4), 4)
        self.assertEqual(output.getvalue(), b"abcd")

        with self.assertRaises(ValueError):
            bundle_build.copy_stream_limited(io.BytesIO(b"abcde"), io.BytesIO(), 4)
        with self.assertRaises(ValueError):
            bundle_build.copy_stream_limited(io.BytesIO(b"abcd"), io.BytesIO(), 4, 5)
        with self.assertRaises(ValueError):
            bundle_build.copy_stream_limited(io.BytesIO(b"abc"), io.BytesIO(), 4, 4)

    def test_compute_payload_id_is_deterministic_and_content_addressed(self) -> None:
        with tempfile.TemporaryDirectory() as first_temp, tempfile.TemporaryDirectory() as second_temp:
            first = Path(first_temp)
            second = Path(second_temp)

            (first / "pkg").mkdir()
            (first / "empty").mkdir()
            (first / "pkg" / "module.py").write_bytes(b"answer = 42\n")
            (first / "python.exe").write_bytes(b"MZpayload")

            # Create the same tree in a different order and with different mtimes.
            (second / "python.exe").write_bytes(b"MZpayload")
            (second / "empty").mkdir()
            (second / "pkg").mkdir()
            (second / "pkg" / "module.py").write_bytes(b"answer = 42\n")
            os.utime(second / "python.exe", (1_000_000_000, 1_000_000_000))

            first_id = bundle_build.compute_payload_id(first)
            second_id = bundle_build.compute_payload_id(second)
            self.assertEqual(first_id, second_id)
            self.assertEqual(first_id, "92f94b52fc138058228edc85e3989abd982130a0d12fe7158f580fd469990090")
            self.assertRegex(first_id, r"^[0-9a-f]{64}$")

            (second / "pkg" / "module.py").write_bytes(b"answer = 43\n")
            self.assertNotEqual(first_id, bundle_build.compute_payload_id(second))

            (second / "pkg" / "module.py").write_bytes(b"answer = 42\n")
            (second / "another-empty").mkdir()
            self.assertNotEqual(first_id, bundle_build.compute_payload_id(second))


if __name__ == "__main__":
    unittest.main()
