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

func TestParseUpstreamSHA(t *testing.T) {
	sha := "1bfc5ce0a1b2c3d4e5f60718293a4b5c6d7e8f90"
	tests := map[string]string{
		"Rolling build.\n\nupstream-sha: " + sha: sha,
		"<!-- upstream-sha: " + sha + " -->":     sha,
		"upstream-sha: 1BFC5CE":                  "1bfc5ce",
		"no marker here":                         "",
		"upstream-sha: xyz":                      "",
	}
	for body, want := range tests {
		if got := parseUpstreamSHA(body); got != want {
			t.Fatalf("parseUpstreamSHA(%q) = %q, want %q", body, got, want)
		}
	}
}

func edgeRelease(sha string, assets ...GitHubAsset) GitHubRelease {
	body := "Rolling build of upstream main.\n\nupstream-sha: " + sha
	if sha == "" {
		body = "Rolling build of upstream main."
	}
	return GitHubRelease{TagName: "edge", Prerelease: true, Body: body, Assets: assets}
}

func TestFindEdgeUpdate(t *testing.T) {
	asset := GitHubAsset{Name: "CodexLB_Installer_edge.exe", BrowserDownloadURL: "https://example.com/edge.exe"}
	shaA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	stable := GitHubRelease{TagName: "v1.20.1", Assets: []GitHubAsset{{Name: "CodexLB_Installer_1.20.1.exe", BrowserDownloadURL: "https://example.com/stable.exe"}}}

	// New upstream commit: update offered, identified by short sha.
	cand, exists := findEdgeUpdate([]GitHubRelease{stable, edgeRelease(shaA, asset)}, shaB)
	if !exists || cand == nil {
		t.Fatal("expected an edge update when shas differ")
	}
	if cand.url != asset.BrowserDownloadURL || cand.newVersion != "edge (aaaaaaa)" {
		t.Fatalf("unexpected candidate: %+v", cand)
	}

	// Same upstream commit: up to date.
	if cand, exists := findEdgeUpdate([]GitHubRelease{edgeRelease(shaA, asset)}, shaA); !exists || cand != nil {
		t.Fatalf("expected up-to-date edge, got %+v (exists=%v)", cand, exists)
	}

	// No embedded sha in the running build: still offered (manual first hop).
	if cand, _ := findEdgeUpdate([]GitHubRelease{edgeRelease(shaA, asset)}, ""); cand == nil {
		t.Fatal("expected an edge update when the installed sha is unknown")
	}

	// Unusable edge release (no marker / no asset): no candidate, but exists.
	if cand, exists := findEdgeUpdate([]GitHubRelease{edgeRelease("", asset)}, shaB); !exists || cand != nil {
		t.Fatalf("expected no candidate for a markerless edge release, got %+v", cand)
	}
	if cand, exists := findEdgeUpdate([]GitHubRelease{edgeRelease(shaA)}, shaB); !exists || cand != nil {
		t.Fatalf("expected no candidate for an assetless edge release, got %+v", cand)
	}

	// No edge release at all.
	if cand, exists := findEdgeUpdate([]GitHubRelease{stable}, shaB); exists || cand != nil {
		t.Fatalf("expected no edge release, got %+v (exists=%v)", cand, exists)
	}
}

func TestFindStableUpdateSkipsPrereleasesAndOldVersions(t *testing.T) {
	releases := []GitHubRelease{
		edgeRelease("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", GitHubAsset{Name: "CodexLB_Installer_edge.exe", BrowserDownloadURL: "https://example.com/edge.exe"}),
		{TagName: "v1.20.1", Assets: []GitHubAsset{{Name: "CodexLB_Installer_1.20.1.exe", BrowserDownloadURL: "https://example.com/1.20.1.exe"}}},
		{TagName: "v1.20.2", Assets: []GitHubAsset{{Name: "CodexLB_Installer_1.20.2.exe", BrowserDownloadURL: "https://example.com/1.20.2.exe"}}},
	}

	cand := findStableUpdate(releases, "1.20.2-beta.1")
	if cand == nil || cand.newVersion != "v1.20.2" || cand.url != "https://example.com/1.20.2.exe" {
		t.Fatalf("unexpected stable candidate: %+v", cand)
	}

	if cand := findStableUpdate(releases, "1.20.2"); cand != nil {
		t.Fatalf("expected up to date on 1.20.2, got %+v", cand)
	}
}
