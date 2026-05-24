import urllib.request
import zipfile
import shutil
import subprocess
from pathlib import Path
import os
import sys
import re

# Configuration
PYTHON_VERSION = "3.13.5"
PYTHON_URL = f"https://www.python.org/ftp/python/{PYTHON_VERSION}/python-3.13.5-embed-amd64.zip"
PYTHON_ZIP = Path("python-3.13.5-embed-amd64.zip")

WORKSPACE_DIR = Path(__file__).parent.resolve()
BUNDLE_DIR = WORKSPACE_DIR / "dist" / "bundle"
PYTHON_BUNDLE_DIR = BUNDLE_DIR / "python"
SITE_PACKAGES_DIR = PYTHON_BUNDLE_DIR / "Lib" / "site-packages"
SRC_DIR = WORKSPACE_DIR / "codex-lb-src"
ISCC_PATH = Path("C:/Program Files (x86)/Inno Setup 6/ISCC.exe")

def download_python():
    if not PYTHON_ZIP.exists():
        print(f"Downloading portable Python {PYTHON_VERSION}...")
        urllib.request.urlretrieve(PYTHON_URL, PYTHON_ZIP)
        print("Download completed.")
    else:
        print("Portable Python ZIP already downloaded.")

def extract_python():
    if PYTHON_BUNDLE_DIR.exists():
        print("Cleaning previous Python bundle folder...")
        shutil.rmtree(PYTHON_BUNDLE_DIR)
    
    print("Extracting portable Python...")
    PYTHON_BUNDLE_DIR.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(PYTHON_ZIP, 'r') as zip_ref:
        zip_ref.extractall(PYTHON_BUNDLE_DIR)
    print("Extraction completed.")

def configure_pth():
    print("Configuring python313._pth...")
    pth_file = PYTHON_BUNDLE_DIR / "python313._pth"
    pth_content = """python313.zip
.
Lib/site-packages

# Uncomment to run site.main() automatically
import site
"""
    pth_file.write_text(pth_content, encoding="utf-8")
    print("python313._pth configured.")

def build_wheel():
    print("Ensuring latest wheel is built in codex-lb-src...")
    # Clean old builds
    dist_dir = SRC_DIR / "dist"
    if dist_dir.exists():
        shutil.rmtree(dist_dir)
        
    subprocess.run(["uv", "build"], cwd=SRC_DIR, check=True)
    print("Wheel build completed.")

def get_wheel_path() -> Path:
    wheels = list((SRC_DIR / "dist").glob("*.whl"))
    if not wheels:
        raise FileNotFoundError("No built wheel found in codex-lb-src/dist")
    return wheels[0]

def install_dependencies():
    print("Installing wheel and dependencies into portable python site-packages...")
    SITE_PACKAGES_DIR.mkdir(parents=True, exist_ok=True)
    
    wheel_path = get_wheel_path()
    python_exe = PYTHON_BUNDLE_DIR / "python.exe"
    
    # We use uv to install the wheel and its dependencies to the target directory.
    # We specify --python so uv matches the python version/platform of the embed environment.
    cmd = [
        "uv", "pip", "install",
        "--python", str(python_exe),
        "--target", str(SITE_PACKAGES_DIR),
        str(wheel_path)
    ]
    print(f"Running command: {' '.join(cmd)}")
    subprocess.run(cmd, check=True)
    print("Dependencies installation completed.")

def embed_icon_resource():
    """Convert PNG icon to ICO and embed as Windows resource via rsrc."""
    icon_png = WORKSPACE_DIR / "codex_lb_icon.png"
    icon_ico = WORKSPACE_DIR / "codex_lb_icon.ico"
    syso_file = WORKSPACE_DIR / "rsrc.syso"

    if not icon_png.exists():
        print("Warning: codex_lb_icon.png not found, skipping icon resource.")
        return

    # Convert PNG to ICO using Pillow
    if not icon_ico.exists() or icon_png.stat().st_mtime > icon_ico.stat().st_mtime:
        print("Converting icon PNG to ICO...")
        try:
            from PIL import Image
            img = Image.open(icon_png)
            if img.mode != 'RGBA':
                img = img.convert('RGBA')
            sizes = [(256, 256), (64, 64), (48, 48), (32, 32), (16, 16)]
            img.save(icon_ico, format='ICO', sizes=sizes)
            print("ICO created.")
        except ImportError:
            print("Warning: Pillow not available, skipping ICO conversion.")
            return

    # Generate Windows resource .syso using rsrc
    if not syso_file.exists() or icon_ico.stat().st_mtime > syso_file.stat().st_mtime:
        print("Embedding icon as Windows resource...")
        subprocess.run(
            ["rsrc", "-ico", str(icon_ico), "-o", str(syso_file)],
            cwd=WORKSPACE_DIR, check=True
        )
        print("Windows resource created.")


def copy_launcher():
    print("Compiling and copying launcher.exe...")
    embed_icon_resource()
    # Compile launcher.go — rsrc.syso is auto-linked when present in the package
    subprocess.run([
        "go", "build",
        "-ldflags", "-H=windowsgui",
        "-o", str(BUNDLE_DIR / "launcher.exe"),
    ], cwd=WORKSPACE_DIR, check=True)
    print("launcher.exe compiled and copied to bundle.")

def get_app_version() -> str:
    """Read version from codex-lb-src/pyproject.toml."""
    pyproject = SRC_DIR / "pyproject.toml"
    if not pyproject.exists():
        raise FileNotFoundError(f"pyproject.toml not found at {pyproject}")
    content = pyproject.read_text(encoding="utf-8")
    match = re.search(r'^version\s*=\s*["\']([^"\']+)["\']', content, re.MULTILINE)
    if not match:
        raise ValueError(f"Could not find version in {pyproject}")
    return match.group(1)


def compile_installer():
    print("Compiling Inno Setup installer...")
    if not ISCC_PATH.exists():
        print(f"Error: Inno Setup compiler not found at {ISCC_PATH}")
        print("Please compile installer.iss manually or ensure Inno Setup is installed.")
        return

    # Dynamically set AppVersion from the source pyproject.toml
    app_version = get_app_version()
    print(f"App version: {app_version}")

    # Read installer.iss and update version fields
    iss_path = WORKSPACE_DIR / "installer.iss"
    iss_content = iss_path.read_text(encoding="utf-8")
    iss_content = re.sub(
        r'^AppVersion=.*$',
        f'AppVersion={app_version}',
        iss_content,
        flags=re.MULTILINE
    )
    iss_content = re.sub(
        r'^AppVerName=.*$',
        f'AppVerName=CodexLB {app_version}',
        iss_content,
        flags=re.MULTILINE
    )
    # Also update OutputBaseFilename to include version
    safe_version = app_version.replace("-", "_")
    iss_content = re.sub(
        r'^OutputBaseFilename=.*$',
        f'OutputBaseFilename=CodexLB_Installer_{safe_version}',
        iss_content,
        flags=re.MULTILINE
    )
    iss_path.write_text(iss_content, encoding="utf-8")
    print(f"Updated installer.iss with AppVersion={app_version}")

    subprocess.run([
        str(ISCC_PATH),
        "/Q", # Quiet mode
        str(iss_path)
    ], cwd=WORKSPACE_DIR, check=True)
    print("Installer compiled successfully.")

def main():
    try:
        download_python()
        extract_python()
        configure_pth()
        build_wheel()
        install_dependencies()
        copy_launcher()
        compile_installer()
        print("\n=== BUILD PROCESS COMPLETED SUCCESSFULLY ===")
        print("Final installer is located in the dist/ folder.")
    except Exception as e:
        print(f"\n!!! BUILD PROCESS FAILED !!!\nError: {e}")
        sys.exit(1)

if __name__ == "__main__":
    main()
