import hashlib
import os
import re
import shutil
import subprocess
import sys
import tempfile
import tomllib
import urllib.request
import zipfile
from pathlib import Path, PurePosixPath

from release_policy import parse_semver


# Configuration
WORKSPACE_DIR = Path(__file__).parent.resolve()
PYTHON_VERSION = "3.13.5"
PYTHON_ARCHIVE_NAME = f"python-{PYTHON_VERSION}-embed-amd64.zip"
PYTHON_URL = f"https://www.python.org/ftp/python/{PYTHON_VERSION}/{PYTHON_ARCHIVE_NAME}"
PYTHON_ZIP = WORKSPACE_DIR / PYTHON_ARCHIVE_NAME

# SHA-256 from python.org's Sigstore bundle for this exact embeddable ZIP.
# Update it together with PYTHON_VERSION.
PYTHON_SHA256: str | None = "7d2650fd9d1b9d002d4a315d5f354247fd6a44f30517c7ef577b08f57a0fb6d9"
MAX_PYTHON_ARCHIVE_BYTES = 64 * 1024 * 1024

BUNDLE_DIR = WORKSPACE_DIR / "dist" / "bundle"
PYTHON_BUNDLE_DIR = BUNDLE_DIR / "python"
SITE_PACKAGES_DIR = PYTHON_BUNDLE_DIR / "Lib" / "site-packages"
SRC_DIR = WORKSPACE_DIR / "codex-lb-src"
ISCC_PATH = Path("C:/Program Files (x86)/Inno Setup 6/ISCC.exe")

APP_VERSION_PATTERN = re.compile(
    r"^[0-9]+\.[0-9]+\.[0-9]+"
    r"(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?"
    r"(?:\+[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$"
)
BUILD_SHA_PATTERN = re.compile(r"^[0-9a-fA-F]{40}$")
PAYLOAD_ID_PATTERN = re.compile(r"^[0-9a-fA-F]{64}$")
VALID_UPDATE_CHANNELS = frozenset({"stable", "beta", "edge"})
# "release" is the user-facing name for the stable channel; the launcher stores
# and compares the historical "stable" value, so normalize the alias here.
UPDATE_CHANNEL_ALIASES = {"release": "stable"}


def python_abi_tag(version: str = PYTHON_VERSION) -> str:
    parts = version.split(".")
    if len(parts) != 3 or not all(part.isdigit() for part in parts):
        raise ValueError(f"Invalid Python version: {version!r}")
    return f"python{parts[0]}{parts[1]}"


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def _validate_zip_member_name(name: str) -> None:
    if not name or "\\" in name:
        raise ValueError(f"Unsafe path in portable Python archive: {name!r}")
    path = PurePosixPath(name)
    if path.is_absolute() or ".." in path.parts or (path.parts and ":" in path.parts[0]):
        raise ValueError(f"Unsafe path in portable Python archive: {name!r}")


def validate_python_archive(path: Path, expected_sha256: str | None = PYTHON_SHA256) -> None:
    if not path.is_file():
        raise FileNotFoundError(f"Portable Python archive not found: {path}")

    if expected_sha256 is not None:
        expected = expected_sha256.strip().lower()
        if not re.fullmatch(r"[0-9a-f]{64}", expected):
            raise ValueError("PYTHON_SHA256 must be exactly 64 hexadecimal characters")
        actual = sha256_file(path)
        if actual != expected:
            raise ValueError(
                f"Portable Python SHA-256 mismatch: expected {expected}, got {actual}"
            )

    abi_tag = python_abi_tag()
    try:
        with zipfile.ZipFile(path, "r") as archive:
            for member in archive.infolist():
                _validate_zip_member_name(member.filename)
            names = set(archive.namelist())
            required = {"python.exe", f"{abi_tag}._pth", f"{abi_tag}.zip"}
            missing = sorted(required - names)
            if missing:
                raise ValueError(
                    "Portable Python archive is missing required files: "
                    + ", ".join(missing)
                )
            bad_member = archive.testzip()
            if bad_member is not None:
                raise ValueError(f"Portable Python archive failed CRC check: {bad_member}")
    except zipfile.BadZipFile as exc:
        raise ValueError(f"Portable Python archive is invalid: {path}") from exc


def copy_stream_limited(
    source,
    destination,
    max_bytes: int,
    expected_size: int | None = None,
) -> int:
    if max_bytes < 0:
        raise ValueError("max_bytes must not be negative")
    if expected_size is not None and (expected_size < 0 or expected_size > max_bytes):
        raise ValueError(
            f"Portable Python download size {expected_size} exceeds the {max_bytes}-byte limit"
        )

    written = 0
    while True:
        remaining = max_bytes - written
        block = source.read(min(1024 * 1024, remaining + 1))
        if not block:
            break
        written += len(block)
        if written > max_bytes:
            raise ValueError(f"Portable Python download exceeds the {max_bytes}-byte limit")
        destination.write(block)

    if expected_size is not None and written != expected_size:
        raise ValueError(
            f"Portable Python download is truncated: expected {expected_size} bytes, got {written}"
        )
    return written


def _response_content_length(response) -> int | None:
    value = response.headers.get("Content-Length")
    if value is None:
        return None
    try:
        length = int(value)
    except (TypeError, ValueError) as exc:
        raise ValueError(f"Invalid Content-Length for portable Python download: {value!r}") from exc
    if length < 0:
        raise ValueError(f"Invalid Content-Length for portable Python download: {value!r}")
    return length


def _download_python_archive(destination: Path) -> None:
    partial = destination.with_name(destination.name + ".part")
    partial.unlink(missing_ok=True)
    try:
        request = urllib.request.Request(
            PYTHON_URL,
            headers={"User-Agent": "CodexLB installer build"},
        )
        with urllib.request.urlopen(request, timeout=60) as response:
            expected_size = _response_content_length(response)
            with partial.open("wb") as output:
                copy_stream_limited(
                    response,
                    output,
                    MAX_PYTHON_ARCHIVE_BYTES,
                    expected_size,
                )
                output.flush()
                os.fsync(output.fileno())
        validate_python_archive(partial)
        os.replace(partial, destination)
    finally:
        partial.unlink(missing_ok=True)


def download_python() -> None:
    if PYTHON_ZIP.exists():
        try:
            validate_python_archive(PYTHON_ZIP)
        except (OSError, ValueError) as exc:
            print(f"Discarding invalid cached portable Python archive: {exc}")
            PYTHON_ZIP.unlink()
        else:
            if PYTHON_SHA256 is None:
                print("Portable Python ZIP validated structurally (no pinned SHA-256 configured).")
            else:
                print("Portable Python ZIP and SHA-256 validated.")
            return

    print(f"Downloading portable Python {PYTHON_VERSION}...")
    _download_python_archive(PYTHON_ZIP)
    if PYTHON_SHA256 is None:
        print("Download completed and ZIP structure/CRC validated (no pinned SHA-256 configured).")
    else:
        print("Download completed and SHA-256 validated.")


def extract_python() -> None:
    validate_python_archive(PYTHON_ZIP)
    if PYTHON_BUNDLE_DIR.exists():
        print("Cleaning previous Python bundle folder...")
        shutil.rmtree(PYTHON_BUNDLE_DIR)

    print("Extracting portable Python...")
    PYTHON_BUNDLE_DIR.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(PYTHON_ZIP, "r") as zip_ref:
        zip_ref.extractall(PYTHON_BUNDLE_DIR)
    print("Extraction completed.")


def configure_pth() -> None:
    abi_tag = python_abi_tag()
    print(f"Configuring {abi_tag}._pth...")
    pth_file = PYTHON_BUNDLE_DIR / f"{abi_tag}._pth"
    pth_content = f"""{abi_tag}.zip
.
Lib/site-packages

# Uncomment to run site.main() automatically
import site
"""
    pth_file.write_text(pth_content, encoding="utf-8")
    print(f"{abi_tag}._pth configured.")


def build_wheel() -> None:
    print("Ensuring latest wheel is built in codex-lb-src...")
    dist_dir = SRC_DIR / "dist"
    if dist_dir.exists():
        shutil.rmtree(dist_dir)

    subprocess.run(["uv", "build"], cwd=SRC_DIR, check=True)
    print("Wheel build completed.")


def get_wheel_path(dist_dir: Path | None = None) -> Path:
    wheel_dir = dist_dir if dist_dir is not None else SRC_DIR / "dist"
    wheels = sorted(wheel_dir.glob("*.whl"))
    if len(wheels) != 1:
        found = ", ".join(path.name for path in wheels) or "none"
        raise RuntimeError(
            f"Expected exactly one wheel in {wheel_dir}, found {len(wheels)}: {found}"
        )
    return wheels[0]


def install_dependencies() -> None:
    print("Installing locked dependencies and wheel into portable Python site-packages...")
    SITE_PACKAGES_DIR.mkdir(parents=True, exist_ok=True)

    wheel_path = get_wheel_path()
    python_exe = PYTHON_BUNDLE_DIR / "python.exe"
    lock_path = SRC_DIR / "uv.lock"
    if not lock_path.is_file():
        raise FileNotFoundError(f"Locked dependency graph not found: {lock_path}")

    with tempfile.TemporaryDirectory(prefix="codexlb-requirements-") as temp_dir:
        requirements_path = Path(temp_dir) / "requirements.txt"
        export_cmd = [
            "uv", "export",
            "--quiet",
            "--frozen",
            "--no-dev",
            "--no-emit-project",
            "--output-file", str(requirements_path),
        ]
        subprocess.run(export_cmd, cwd=SRC_DIR, check=True)
        if not requirements_path.is_file() or "--hash=sha256:" not in requirements_path.read_text(
            encoding="utf-8"
        ):
            raise ValueError("uv export did not produce hashed locked requirements")

        dependency_cmd = [
            "uv", "pip", "install",
            "--python", str(python_exe),
            "--target", str(SITE_PACKAGES_DIR),
            "--require-hashes",
            "--requirements", str(requirements_path),
        ]
        subprocess.run(dependency_cmd, check=True)

    wheel_cmd = [
        "uv", "pip", "install",
        "--python", str(python_exe),
        "--target", str(SITE_PACKAGES_DIR),
        "--no-deps",
        str(wheel_path),
    ]
    subprocess.run(wheel_cmd, check=True)
    print("Locked dependencies and wheel installation completed.")


def embed_icon_resource() -> None:
    """Convert PNG icon to ICO and embed as Windows resource via rsrc."""
    icon_png = WORKSPACE_DIR / "codex_lb_icon.png"
    icon_ico = WORKSPACE_DIR / "codex_lb_icon.ico"
    syso_file = WORKSPACE_DIR / "rsrc.syso"

    if not icon_png.exists():
        print("Warning: codex_lb_icon.png not found, skipping icon resource.")
        return

    if not icon_ico.exists() or icon_png.stat().st_mtime > icon_ico.stat().st_mtime:
        print("Converting icon PNG to ICO...")
        try:
            from PIL import Image

            img = Image.open(icon_png)
            if img.mode != "RGBA":
                img = img.convert("RGBA")
            sizes = [(256, 256), (64, 64), (48, 48), (32, 32), (16, 16)]
            img.save(icon_ico, format="ICO", sizes=sizes)
            print("ICO created.")
        except ImportError:
            print("Warning: Pillow not available, skipping ICO conversion.")
            return

    if not syso_file.exists() or icon_ico.stat().st_mtime > syso_file.stat().st_mtime:
        print("Embedding icon as Windows resource...")
        subprocess.run(
            ["rsrc", "-ico", str(icon_ico), "-o", str(syso_file)],
            cwd=WORKSPACE_DIR,
            check=True,
        )
        print("Windows resource created.")


def validate_app_version(version: str) -> str:
    normalized = version.strip()
    if not APP_VERSION_PATTERN.fullmatch(normalized) or parse_semver(normalized) is None:
        raise ValueError(f"Invalid application version: {version!r}")
    return normalized


def validate_build_sha(sha: str) -> str:
    normalized = sha.strip().lower()
    if not BUILD_SHA_PATTERN.fullmatch(normalized):
        raise ValueError(f"Invalid build SHA (expected a full 40-character commit SHA): {sha!r}")
    return normalized


def validate_payload_id(payload_id: str) -> str:
    normalized = payload_id.strip().lower()
    if not PAYLOAD_ID_PATTERN.fullmatch(normalized):
        raise ValueError(f"Invalid payload ID (expected 64 hexadecimal characters): {payload_id!r}")
    return normalized


def compute_payload_id(root: Path = PYTHON_BUNDLE_DIR) -> str:
    """Hash a payload tree independently of traversal order and filesystem metadata.

    The format includes every relative directory/file path, file size, and file
    content with explicit framing. Symlinks and non-file entries are rejected so
    two accepted trees with the same ID have the same installable contents.
    """
    if not root.is_dir():
        raise FileNotFoundError(f"Python payload directory not found: {root}")

    entries = sorted(root.rglob("*"), key=lambda path: path.relative_to(root).as_posix())
    if not entries:
        raise ValueError(f"Python payload directory is empty: {root}")

    digest = hashlib.sha256()
    digest.update(b"CodexLB-Python-Payload-v1\0")
    for entry in entries:
        relative = entry.relative_to(root).as_posix().encode("utf-8")
        if entry.is_symlink():
            raise ValueError(f"Python payload must not contain symlinks: {entry}")
        if entry.is_dir():
            digest.update(b"D")
            digest.update(len(relative).to_bytes(8, "big"))
            digest.update(relative)
            continue
        if not entry.is_file():
            raise ValueError(f"Unsupported entry in Python payload: {entry}")

        size = entry.stat().st_size
        digest.update(b"F")
        digest.update(len(relative).to_bytes(8, "big"))
        digest.update(relative)
        digest.update(size.to_bytes(8, "big"))
        copied = 0
        with entry.open("rb") as handle:
            for block in iter(lambda: handle.read(1024 * 1024), b""):
                copied += len(block)
                digest.update(block)
        if copied != size:
            raise ValueError(f"Python payload changed while it was being hashed: {entry}")
    return digest.hexdigest()


def validate_update_channel(channel: str) -> str:
    normalized = channel.strip().lower()
    normalized = UPDATE_CHANNEL_ALIASES.get(normalized, normalized)
    if normalized not in VALID_UPDATE_CHANNELS:
        allowed = ", ".join(sorted(VALID_UPDATE_CHANNELS))
        raise ValueError(f"Invalid update channel {channel!r}; expected one of: {allowed}")
    return normalized


def get_update_channel() -> str:
    return validate_update_channel(os.environ.get("CODEXLB_UPDATE_CHANNEL", "stable"))


def get_build_sha() -> str:
    """Return the validated upstream commit represented by this bundle."""
    configured_sha = os.environ.get("CODEXLB_UPSTREAM_SHA", "").strip()
    if configured_sha:
        return validate_build_sha(configured_sha)

    try:
        result = subprocess.run(
            ["git", "rev-parse", "--verify", "HEAD^{commit}"],
            cwd=SRC_DIR,
            capture_output=True,
            text=True,
            check=True,
        )
    except (OSError, subprocess.CalledProcessError) as exc:
        raise RuntimeError("Could not determine the codex-lb source commit") from exc
    return validate_build_sha(result.stdout)


def get_wrapper_sha() -> str:
    """Return the validated installer-wrapper commit represented by this bundle."""
    configured_sha = os.environ.get("CODEXLB_WRAPPER_SHA", "").strip()
    if configured_sha:
        return validate_build_sha(configured_sha)

    try:
        result = subprocess.run(
            ["git", "rev-parse", "--verify", "HEAD^{commit}"],
            cwd=WORKSPACE_DIR,
            capture_output=True,
            text=True,
            check=True,
        )
    except (OSError, subprocess.CalledProcessError) as exc:
        raise RuntimeError("Could not determine the installer-wrapper source commit") from exc
    return validate_build_sha(result.stdout)


def get_app_version() -> str:
    """Read and validate the version from codex-lb-src/pyproject.toml."""
    pyproject = SRC_DIR / "pyproject.toml"
    if not pyproject.exists():
        raise FileNotFoundError(f"pyproject.toml not found at {pyproject}")
    with pyproject.open("rb") as handle:
        metadata = tomllib.load(handle)
    try:
        version = metadata["project"]["version"]
    except (KeyError, TypeError) as exc:
        raise ValueError(f"Could not find project.version in {pyproject}") from exc
    if not isinstance(version, str):
        raise ValueError(f"project.version in {pyproject} must be a string")
    return validate_app_version(version)


def copy_launcher(payload_id: str) -> None:
    print("Compiling and copying launcher.exe...")
    embed_icon_resource()
    app_version = get_app_version()
    build_sha = get_build_sha()
    wrapper_sha = get_wrapper_sha()
    update_channel = get_update_channel()
    payload_id = validate_payload_id(payload_id)
    ldflags = (
        f"-H=windowsgui -X main.currentVersion={app_version} "
        f"-X main.buildSHA={build_sha} -X main.wrapperSHA={wrapper_sha} "
        f"-X main.buildChannel={update_channel} "
        f"-X main.payloadID={payload_id}"
    )
    print(f"Build sha: {build_sha}")
    print(f"Wrapper sha: {wrapper_sha}")
    print(f"Update channel: {update_channel}")
    print(f"Python payload ID: {payload_id}")
    subprocess.run(
        [
            "go", "build",
            "-ldflags", ldflags,
            "-o", str(BUNDLE_DIR / "launcher.exe"),
        ],
        cwd=WORKSPACE_DIR,
        check=True,
    )
    print("launcher.exe compiled and copied to bundle.")


def installer_output_basename(app_version: str) -> str:
    version = validate_app_version(app_version)
    safe_version = re.sub(r"[^0-9A-Za-z._]", "_", version)
    return f"CodexLB_Installer_{safe_version}"


def compile_installer(payload_id: str) -> Path:
    print("Compiling Inno Setup installer...")
    if not ISCC_PATH.is_file():
        raise FileNotFoundError(f"Inno Setup compiler not found at {ISCC_PATH}")

    app_version = get_app_version()
    payload_id = validate_payload_id(payload_id)
    actual_payload_id = compute_payload_id()
    if actual_payload_id != payload_id:
        raise ValueError(
            "Python payload changed after launcher metadata was generated: "
            f"expected {payload_id}, got {actual_payload_id}"
        )
    output_basename = installer_output_basename(app_version)
    expected_output = WORKSPACE_DIR / "dist" / f"{output_basename}.exe"
    iss_path = WORKSPACE_DIR / "installer.iss"
    if not iss_path.is_file():
        raise FileNotFoundError(f"Inno Setup script not found at {iss_path}")

    print(f"App version: {app_version}")
    expected_output.parent.mkdir(parents=True, exist_ok=True)
    expected_output.unlink(missing_ok=True)

    subprocess.run(
        [
            str(ISCC_PATH),
            "/Q",
            f"/DCodexLBVersion={app_version}",
            f"/DCodexLBOutputBaseFilename={output_basename}",
            f"/DCodexLBPayloadId={payload_id}",
            str(iss_path),
        ],
        cwd=WORKSPACE_DIR,
        check=True,
    )

    if not expected_output.is_file():
        raise FileNotFoundError(
            f"Inno Setup completed without producing the expected output: {expected_output}"
        )
    if expected_output.stat().st_size == 0:
        raise ValueError(f"Inno Setup produced an empty installer: {expected_output}")
    with expected_output.open("rb") as installer:
        if installer.read(2) != b"MZ":
            raise ValueError(f"Installer output is not a Windows executable: {expected_output}")

    print(f"Installer compiled and verified: {expected_output}")
    return expected_output


def main() -> None:
    try:
        download_python()
        extract_python()
        configure_pth()
        build_wheel()
        install_dependencies()
        payload_id = compute_payload_id()
        print(f"Python payload ID: {payload_id}")
        copy_launcher(payload_id)
        installer_path = compile_installer(payload_id)
        print("\n=== BUILD PROCESS COMPLETED SUCCESSFULLY ===")
        print(f"Final installer: {installer_path}")
    except Exception as exc:
        print(f"\n!!! BUILD PROCESS FAILED !!!\nError: {exc}")
        sys.exit(1)


if __name__ == "__main__":
    main()
