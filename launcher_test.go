package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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

func TestInstallerArgsMatchInstallMode(t *testing.T) {
	installerPath := filepath.Join(os.TempDir(), "CodexLB_Update_test.exe")
	visible := installerArgs(installerPath, installVisible)
	if slices.Contains(visible, "/VERYSILENT") || slices.Contains(visible, "/SUPPRESSMSGBOXES") {
		t.Fatalf("visible installer args should not include silent flags: %v", visible)
	}
	if want := "/LOG=" + filepath.Join(os.TempDir(), "CodexLB_Update_test.log"); !slices.Contains(visible, want) {
		t.Fatalf("visible installer args %v missing unique log path %q", visible, want)
	}

	silent := installerArgs(installerPath, installSilent)
	for _, want := range []string{"/VERYSILENT", "/SUPPRESSMSGBOXES"} {
		if !slices.Contains(silent, want) {
			t.Fatalf("silent installer args %v missing %s", silent, want)
		}
	}
}

func TestBundledPythonCandidatesPreferImmutablePayload(t *testing.T) {
	originalPayloadID := payloadID
	t.Cleanup(func() { payloadID = originalPayloadID })

	payloadID = strings.ToUpper(strings.Repeat("ab", 32))
	installDir := filepath.Join("C:\\", "Users", "test", "CodexLB")
	candidates := bundledPythonCandidates(installDir)
	if len(candidates) != 2 {
		t.Fatalf("bundledPythonCandidates() returned %d paths, want 2", len(candidates))
	}
	wantVersioned := filepath.Join(installDir, "versions", strings.Repeat("ab", 32), "python", "python.exe")
	if candidates[0] != wantVersioned {
		t.Fatalf("versioned Python candidate = %q, want %q", candidates[0], wantVersioned)
	}
	if candidates[1] != filepath.Join(installDir, "python", "python.exe") {
		t.Fatalf("legacy Python candidate = %q", candidates[1])
	}

	payloadID = "not-a-payload-digest"
	if got := payloadDirectoryName(payloadID); got != "legacy" {
		t.Fatalf("invalid payload ID normalized to %q, want legacy", got)
	}
}

func TestFindBundledPythonNeverFallsBackToPath(t *testing.T) {
	originalPayloadID := payloadID
	t.Cleanup(func() { payloadID = originalPayloadID })
	payloadID = strings.Repeat("ab", 32)

	installDir := t.TempDir()
	if got, err := findBundledPython(installDir); err == nil || got != "" {
		t.Fatalf("findBundledPython(empty install) = (%q, %v), want an error", got, err)
	}

	legacy := filepath.Join(installDir, "python", "python.exe")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := findBundledPython(installDir); err != nil || got != legacy {
		t.Fatalf("findBundledPython(legacy install) = (%q, %v), want %q", got, err, legacy)
	}

	versioned := bundledPythonCandidates(installDir)[0]
	if err := os.MkdirAll(filepath.Dir(versioned), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(versioned, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := findBundledPython(installDir); err != nil || got != versioned {
		t.Fatalf("findBundledPython(versioned install) = (%q, %v), want %q", got, err, versioned)
	}
}
