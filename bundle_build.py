import urllib.request
import zipfile
import shutil
import subprocess
from pathlib import Path
import os

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

def copy_launcher():
    print("Compiling and copying launcher.exe...")
    # Compile launcher.go if needed, otherwise compile it now
    subprocess.run([
        "go", "build",
        "-ldflags", "-H=windowsgui",
        "-o", str(BUNDLE_DIR / "launcher.exe"),
        "launcher.go"
    ], cwd=WORKSPACE_DIR, check=True)
    print("launcher.exe compiled and copied to bundle.")

def compile_installer():
    print("Compiling Inno Setup installer...")
    if not ISCC_PATH.exists():
        print(f"Error: Inno Setup compiler not found at {ISCC_PATH}")
        print("Please compile installer.iss manually or ensure Inno Setup is installed.")
        return
        
    subprocess.run([
        str(ISCC_PATH),
        "/Q", # Quiet mode
        "installer.iss"
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
