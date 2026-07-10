package main

import (
	"slices"
	"testing"
)

func TestDisplayVersionNormalizesTagPrefixes(t *testing.T) {
	tests := map[string]string{
		"1.19.0-beta.1": "v1.19.0-beta.1",
		"v1.20.0":       "v1.20.0",
		"wv1.20.0":      "v1.20.0",
		"":              "unknown",
	}

	for input, want := range tests {
		if got := displayVersion(input); got != want {
			t.Fatalf("displayVersion(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestIsNewerVersionHandlesTagsAndPrereleaseSuffixes(t *testing.T) {
	if !isNewerVersion("v1.20.0", "1.19.0-beta.1") {
		t.Fatal("expected v1.20.0 to be newer than 1.19.0-beta.1")
	}
	if !isNewerVersion("v1.19.0", "1.19.0-beta.1") {
		t.Fatal("expected stable release to be newer than matching prerelease")
	}
	if !isNewerVersion("v1.19.0-beta.2", "1.19.0-beta.1") {
		t.Fatal("expected beta.2 to be newer than beta.1")
	}
	if isNewerVersion("v1.19.0-beta.1", "1.19.0") {
		t.Fatal("expected prerelease not to be newer than matching stable release")
	}
	if !isNewerVersion("wv2.0.0", "v1.99.99") {
		t.Fatal("expected prefixed wv2.0.0 tag to parse as 2.0.0")
	}
	if !isNewerVersion("v1.20.0", "") {
		t.Fatal("expected a valid release to be newer than a missing current version")
	}
}

func TestInstallerArgsMatchInstallMode(t *testing.T) {
	visible := installerArgs(installVisible)
	if slices.Contains(visible, "/VERYSILENT") || slices.Contains(visible, "/SUPPRESSMSGBOXES") {
		t.Fatalf("visible installer args should not include silent flags: %v", visible)
	}

	silent := installerArgs(installSilent)
	for _, want := range []string{"/VERYSILENT", "/SUPPRESSMSGBOXES"} {
		if !slices.Contains(silent, want) {
			t.Fatalf("silent installer args %v missing %s", silent, want)
		}
	}
}

func TestInstallerAssetSelection(t *testing.T) {
	if !isInstallerAsset("CodexLB_Installer_1.20.0.exe") {
		t.Fatal("expected installer-named exe to be accepted")
	}
	if isInstallerAsset("CodexLB_Portable_1.20.0.exe") {
		t.Fatal("expected non-installer exe not to be preferred")
	}
}
