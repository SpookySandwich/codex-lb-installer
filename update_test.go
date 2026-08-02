package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testFullSHA = "0123456789abcdef0123456789abcdef01234567"
const testWrapperSHA = "fedcba9876543210fedcba9876543210fedcba98"

func noRetrySleep(context.Context, time.Duration) error { return nil }

func validReleaseAsset(tag string) GitHubAsset {
	name := stableInstallerAssetName(tag)
	if strings.EqualFold(tag, edgeTagName) {
		name = "CodexLB_Installer_edge.exe"
	}
	return GitHubAsset{
		Name:               name,
		BrowserDownloadURL: "https://github.com/SpookySandwich/codex-lb-installer/releases/download/" + tag + "/" + name,
		Size:               minInstallerBytes,
		Digest:             "sha256:" + strings.Repeat("a", sha256.Size*2),
	}
}

func validStableRelease(tag string) GitHubRelease {
	return GitHubRelease{
		TagName: tag,
		Body:    "wrapper-sha: " + testWrapperSHA,
		Assets:  []GitHubAsset{validReleaseAsset(tag)},
	}
}

func validEdgeRelease(sha string) GitHubRelease {
	asset := validReleaseAsset(edgeTagName)
	asset.Name = edgeInstallerAssetName(sha, testWrapperSHA)
	asset.BrowserDownloadURL = "https://github.com/SpookySandwich/codex-lb-installer/releases/download/edge/" + asset.Name
	return GitHubRelease{
		TagName:    edgeTagName,
		Prerelease: true,
		Body:       "Rolling build.\n\nupstream-sha: " + sha + "\nwrapper-sha: " + testWrapperSHA,
		Assets:     []GitHubAsset{asset},
	}
}

func TestParseSemanticVersionStrict(t *testing.T) {
	valid := []string{
		"0.0.0",
		"1.2.3",
		"v1.2.3",
		"wv1.2.3",
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-0.3.7",
		"1.0.0-x.7.z.92",
		"1.0.0-x-y-z.--",
		"1.0.0+20130313144700",
		"1.0.0-beta+exp.sha.5114f85",
	}
	for _, version := range valid {
		if got := parseSemanticVersion(version); !got.valid {
			t.Errorf("parseSemanticVersion(%q) unexpectedly rejected a valid version", version)
		}
	}

	invalid := []string{
		"",
		"1",
		"1.2",
		"1.2.3.4",
		"01.2.3",
		"1.02.3",
		"1.2.03",
		"1.2.3-",
		"1.2.3-alpha..1",
		"1.2.3-alpha.01",
		"1.2.3+",
		"1.2.3+build..1",
		"1.2.3_alpha",
		"1.2.3-alpha_beta",
	}
	for _, version := range invalid {
		if got := parseSemanticVersion(version); got.valid {
			t.Errorf("parseSemanticVersion(%q) accepted an invalid version: %+v", version, got)
		}
		if isNewerVersion(version, "1.0.0") {
			t.Errorf("invalid version %q must never be offered as newer", version)
		}
	}
}

func TestSemanticVersionPrecedence(t *testing.T) {
	ordered := []string{
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.11",
		"1.0.0-rc.1",
		"1.0.0",
	}
	for i := 1; i < len(ordered); i++ {
		older, newer := ordered[i-1], ordered[i]
		if got := compareVersions(older, newer); got >= 0 {
			t.Errorf("compareVersions(%q, %q) = %d, want < 0", older, newer, got)
		}
		if !isNewerVersion(newer, older) {
			t.Errorf("isNewerVersion(%q, %q) = false", newer, older)
		}
	}
	if got := compareVersions("1.2.3+build.1", "1.2.3+build.2"); got != 0 {
		t.Fatalf("build metadata affected precedence: got %d", got)
	}
	if !isNewerVersion("1.0.0-999999999999999999999999", "1.0.0-99999999999999999999999") {
		t.Fatal("large numeric prerelease identifiers were not compared without overflow")
	}
	if !isNewerVersion("v1.20.0", "") {
		t.Fatal("a valid version should be newer than a missing installed version")
	}
}

func TestInstallerAssetCanonicalSelection(t *testing.T) {
	canonical := validReleaseAsset("v1.2.3")
	release := GitHubRelease{
		TagName: "v1.2.3",
		Assets: []GitHubAsset{
			{Name: "CodexLB_Portable_1.2.3.exe"},
			{Name: "checksums.txt"},
			canonical,
		},
	}
	got, err := pickInstallerAsset(release)
	if err != nil {
		t.Fatalf("pickInstallerAsset() error = %v", err)
	}
	if got != canonical {
		t.Fatalf("pickInstallerAsset() = %+v, want %+v", got, canonical)
	}

	accepted := []string{
		"CodexLB_Installer_1.2.3.exe",
		"CodexLB_Installer_1.2.3_beta.1.exe",
		"codexlb_installer_EDGE.EXE",
	}
	for _, name := range accepted {
		if !isInstallerAsset(name) {
			t.Errorf("canonical installer name %q was rejected", name)
		}
	}
	rejected := []string{
		"CodexLB_Installer_.exe",
		"CodexLB_Installer_1.2.3.exe.bak",
		"prefix_CodexLB_Installer_1.2.3.exe",
		"CodexLB_Portable_1.2.3.exe",
		"CodexLB_Installer_1/evil.exe",
	}
	for _, name := range rejected {
		if isInstallerAsset(name) {
			t.Errorf("non-canonical installer name %q was accepted", name)
		}
	}

	if _, err := pickInstallerAsset(GitHubRelease{TagName: "v1.2.3", Assets: []GitHubAsset{{Name: "other.exe"}}}); err == nil {
		t.Fatal("release without a canonical installer was accepted")
	}
	ambiguous := release
	ambiguous.Assets = append(ambiguous.Assets, GitHubAsset{Name: "CodexLB_Installer_second.exe"})
	if _, err := pickInstallerAsset(ambiguous); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous release error = %v, want an ambiguity error", err)
	}
}

func TestStableInstallerAssetNameMatchesBuilder(t *testing.T) {
	tests := map[string]string{
		"v1.2.3":                  "CodexLB_Installer_1.2.3.exe",
		"wv1.20.2-beta.1":         "CodexLB_Installer_1.20.2_beta.1.exe",
		"1.20.2-beta.1+wrapper.7": "CodexLB_Installer_1.20.2_beta.1_wrapper.7.exe",
	}
	for tag, want := range tests {
		if got := stableInstallerAssetName(tag); got != want {
			t.Errorf("stableInstallerAssetName(%q) = %q, want %q", tag, got, want)
		}
	}
}

func TestCandidateFromReleaseRequiresVerifiableMetadata(t *testing.T) {
	asset := validReleaseAsset("v1.2.3")
	candidate, err := candidateFromRelease(GitHubRelease{TagName: "v1.2.3", Assets: []GitHubAsset{asset}}, stableChannel, "v1.2.3")
	if err != nil {
		t.Fatalf("candidateFromRelease(valid) error = %v", err)
	}
	if candidate.assetName != asset.Name || candidate.url != asset.BrowserDownloadURL || candidate.size != asset.Size || candidate.channel != stableChannel || candidate.buildID != "v1.2.3" {
		t.Fatalf("candidateFromRelease(valid) = %+v", candidate)
	}
	if candidate.sha256 != strings.Repeat("a", sha256.Size*2) {
		t.Fatalf("candidate digest = %q", candidate.sha256)
	}

	tests := []struct {
		name   string
		mutate func(*GitHubAsset)
		want   string
	}{
		{name: "missing size", mutate: func(a *GitHubAsset) { a.Size = 0 }, want: "size"},
		{name: "too small", mutate: func(a *GitHubAsset) { a.Size = minInstallerBytes - 1 }, want: "size"},
		{name: "too large", mutate: func(a *GitHubAsset) { a.Size = maxInstallerBytes + 1 }, want: "size"},
		{name: "missing digest", mutate: func(a *GitHubAsset) { a.Digest = "" }, want: "sha256"},
		{name: "wrong digest algorithm", mutate: func(a *GitHubAsset) { a.Digest = "md5:" + strings.Repeat("a", 32) }, want: "sha256"},
		{name: "bad digest hex", mutate: func(a *GitHubAsset) { a.Digest = "sha256:" + strings.Repeat("z", 64) }, want: "invalid sha256"},
		{name: "missing URL", mutate: func(a *GitHubAsset) { a.BrowserDownloadURL = "" }, want: "HTTPS github.com"},
		{name: "insecure URL", mutate: func(a *GitHubAsset) {
			a.BrowserDownloadURL = strings.Replace(a.BrowserDownloadURL, "https://", "http://", 1)
		}, want: "HTTPS github.com"},
		{name: "wrong host", mutate: func(a *GitHubAsset) {
			a.BrowserDownloadURL = strings.Replace(a.BrowserDownloadURL, "github.com", "example.com", 1)
		}, want: "HTTPS github.com"},
		{name: "host port", mutate: func(a *GitHubAsset) {
			a.BrowserDownloadURL = strings.Replace(a.BrowserDownloadURL, "github.com", "github.com:443", 1)
		}, want: "HTTPS github.com"},
		{name: "wrong repository path", mutate: func(a *GitHubAsset) {
			a.BrowserDownloadURL = strings.Replace(a.BrowserDownloadURL, "/SpookySandwich/codex-lb-installer/", "/someone/else/", 1)
		}, want: "outside"},
		{name: "URL filename mismatch", mutate: func(a *GitHubAsset) {
			a.BrowserDownloadURL = strings.TrimSuffix(a.BrowserDownloadURL, a.Name) + "CodexLB_Installer_other.exe"
		}, want: "filename"},
		{name: "credentials", mutate: func(a *GitHubAsset) {
			a.BrowserDownloadURL = strings.Replace(a.BrowserDownloadURL, "https://", "https://user@", 1)
		}, want: "credentials"},
		{name: "fragment", mutate: func(a *GitHubAsset) { a.BrowserDownloadURL += "#fragment" }, want: "fragment"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			badAsset := asset
			test.mutate(&badAsset)
			candidate, err := candidateFromRelease(GitHubRelease{TagName: "v1.2.3", Assets: []GitHubAsset{badAsset}}, stableChannel, "v1.2.3")
			if err == nil {
				t.Fatalf("candidateFromRelease() = %+v, want error", candidate)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("candidateFromRelease() error = %q, want substring %q", err, test.want)
			}
		})
	}
}

func TestSecureRedirectPolicy(t *testing.T) {
	client := newUpdateHTTPClient(time.Second)
	transport, ok := client.Transport.(*http.Transport)
	if !ok || !transport.DisableCompression {
		t.Fatalf("update transport = %#v, want transparent compression disabled", client.Transport)
	}

	for _, rawURL := range []string{
		"https://github.com/SpookySandwich/codex-lb-installer/releases/download/v1.2.3/installer.exe",
		"https://release-assets.githubusercontent.com/github-production-release-asset/123/file?token=opaque",
		"https://objects.githubusercontent.com/github-production-release-asset/file",
		"https://api.github.com/repos/SpookySandwich/codex-lb-installer/releases",
	} {
		request, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := secureRedirectPolicy(request, nil); err != nil {
			t.Errorf("secureRedirectPolicy(%q) error = %v", rawURL, err)
		}
	}

	for _, rawURL := range []string{
		"http://github.com/release.exe",
		"https://example.com/release.exe",
		"https://github.com:443/release.exe",
		"https://user@github.com/release.exe",
	} {
		request, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := secureRedirectPolicy(request, nil); err == nil {
			t.Errorf("secureRedirectPolicy(%q) accepted an unsafe redirect", rawURL)
		}
	}

	request, err := http.NewRequest(http.MethodGet, "https://github.com/release.exe", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := secureRedirectPolicy(request, make([]*http.Request, 5)); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("redirect limit error = %v, want too many redirects", err)
	}
}

func TestParseUpstreamSHARequiresFullCommit(t *testing.T) {
	upper := strings.ToUpper(testFullSHA)
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "plain", body: "Rolling build.\n\nupstream-sha: " + testFullSHA, want: testFullSHA},
		{name: "HTML comment", body: "<!-- upstream-sha: " + upper + " -->", want: testFullSHA},
		{name: "seven chars", body: "upstream-sha: 0123456"},
		{name: "thirty nine chars", body: "upstream-sha: " + testFullSHA[:39]},
		{name: "forty one chars", body: "upstream-sha: " + testFullSHA + "8"},
		{name: "non hex", body: "upstream-sha: " + testFullSHA[:39] + "z"},
		{name: "embedded marker", body: "xupstream-sha: " + testFullSHA},
		{name: "missing", body: "no marker"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := parseUpstreamSHA(test.body); got != test.want {
				t.Fatalf("parseUpstreamSHA(%q) = %q, want %q", test.body, got, test.want)
			}
		})
	}
}

func TestStableChannelTransitions(t *testing.T) {
	releases := []GitHubRelease{
		{TagName: "v99.0.0", Draft: true, Assets: []GitHubAsset{validReleaseAsset("v99.0.0")}},
		{TagName: "v50.0.0-beta.1", Prerelease: true, Assets: []GitHubAsset{validReleaseAsset("v50.0.0-beta.1")}},
		{TagName: "not-semver", Assets: []GitHubAsset{validReleaseAsset("v9.0.0")}},
		validStableRelease("v1.9.0"),
		validStableRelease("v2.0.0"),
	}

	candidate, err := findStableUpdate(releases, "v1.0.0", stableChannel, testWrapperSHA)
	if err != nil {
		t.Fatalf("findStableUpdate() error = %v", err)
	}
	if candidate == nil || candidate.newVersion != "v2.0.0" || candidate.buildID != "v2.0.0" || candidate.channel != stableChannel {
		t.Fatalf("findStableUpdate() = %+v, want latest stable", candidate)
	}

	candidate, err = findStableUpdate(releases, "v2.0.0", stableChannel, testWrapperSHA)
	if err != nil || candidate != nil {
		t.Fatalf("up-to-date stable result = (%+v, %v), want (nil, nil)", candidate, err)
	}
	candidate, err = findStableUpdate(releases, "v2.0.0", stableChannel, strings.Repeat("0", 40))
	if err != nil || candidate == nil || candidate.wrapperSHA != testWrapperSHA {
		t.Fatalf("same-version wrapper update = (%+v, %v), want current release", candidate, err)
	}

	// Opting out of edge must return to stable even when that is a semantic downgrade.
	candidate, err = findStableUpdate(releases, "v99.0.0", edgeChannel, testWrapperSHA)
	if err != nil || candidate == nil || candidate.buildID != "v2.0.0" {
		t.Fatalf("edge-to-stable transition = (%+v, %v), want v2.0.0", candidate, err)
	}

	newestBroken := validStableRelease("v3.0.0")
	newestBroken.Assets[0].Digest = ""
	candidate, err = findStableUpdate(append(releases, newestBroken), "v1.0.0", stableChannel, testWrapperSHA)
	if err == nil || candidate != nil {
		t.Fatalf("broken newest stable result = (%+v, %v), want validation error without fallback", candidate, err)
	}

	mismatchedAsset := validStableRelease("v3.0.0")
	mismatchedAsset.Assets[0].Name = "CodexLB_Installer_2.0.0.exe"
	mismatchedAsset.Assets[0].BrowserDownloadURL = "https://github.com/SpookySandwich/codex-lb-installer/releases/download/v3.0.0/CodexLB_Installer_2.0.0.exe"
	candidate, err = findStableUpdate(append(releases, mismatchedAsset), "v1.0.0", stableChannel, testWrapperSHA)
	if err == nil || candidate != nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("mismatched stable asset result = (%+v, %v), want identity error", candidate, err)
	}
}

func TestUpdateCoordinatorReportsPhasesToObserver(t *testing.T) {
	var c updateCoordinator
	var seen []updatePhase
	c.observe(func(phase updatePhase) { seen = append(seen, phase) })

	// Registering must report the current phase immediately, so a tray built
	// while idle does not start out blank.
	if len(seen) != 1 || seen[0] != phaseIdle {
		t.Fatalf("observer bootstrap = %v, want one idle notification", seen)
	}

	if !c.tryBegin() {
		t.Fatal("tryBegin() on an idle coordinator = false")
	}
	c.setPhase(phaseChecking)
	c.setPhase(phaseChecking) // repeated phases must not re-notify
	c.setPhase(phaseInstalling)
	if got := c.currentPhase(); got != phaseInstalling {
		t.Fatalf("currentPhase() = %v, want installing", got)
	}

	// finish() must return to idle so the menu unlocks even when the caller
	// never resets the phase itself.
	c.finish()
	if got := c.currentPhase(); got != phaseIdle {
		t.Fatalf("currentPhase() after finish = %v, want idle", got)
	}

	want := []updatePhase{phaseIdle, phaseChecking, phaseInstalling, phaseIdle}
	if len(seen) != len(want) {
		t.Fatalf("observed phases = %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("observed phases = %v, want %v", seen, want)
		}
	}

	if !c.tryBegin() {
		t.Fatal("coordinator did not become reusable after finish()")
	}
}

func TestInstallConfirmMessageWarnsAboutDowntime(t *testing.T) {
	msg := installConfirmMessage("v2.0.0", stableChannel)
	if !strings.Contains(msg, lang.DowntimeNotice) {
		t.Fatalf("install confirmation %q omits the service-interruption warning", msg)
	}
	if !strings.Contains(msg, "v2.0.0") {
		t.Fatalf("install confirmation %q omits the new version", msg)
	}
}

func TestParseUpdateChannelAcceptsStoredAndEmbeddedValues(t *testing.T) {
	cases := map[string]updateChannel{
		"stable":   stableChannel,
		"release":  stableChannel,
		"Release":  stableChannel,
		"":         stableChannel,
		"nonsense": stableChannel,
		"beta":     betaChannel,
		" BETA ":   betaChannel,
		"edge":     edgeChannel,
		"Edge":     edgeChannel,
	}
	for input, want := range cases {
		if got := parseUpdateChannel(input); got != want {
			t.Fatalf("parseUpdateChannel(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestBetaChannelSelectsNewestReleaseOfAnyMaturity(t *testing.T) {
	prerelease := validStableRelease("v2.1.0-beta.1")
	prerelease.Prerelease = true
	releases := []GitHubRelease{
		{TagName: "edge", Prerelease: true, Assets: []GitHubAsset{validReleaseAsset("v9.9.9")}},
		{TagName: "v99.0.0", Draft: true, Assets: []GitHubAsset{validReleaseAsset("v99.0.0")}},
		validStableRelease("v2.0.0"),
		prerelease,
	}

	// Beta takes the newest release even when it is a pre-release.
	candidate, err := findBetaUpdate(releases, "v2.0.0", betaChannel, testWrapperSHA)
	if err != nil {
		t.Fatalf("findBetaUpdate() error = %v", err)
	}
	if candidate == nil || candidate.buildID != "v2.1.0-beta.1" || candidate.channel != betaChannel {
		t.Fatalf("findBetaUpdate() = %+v, want v2.1.0-beta.1 on the beta channel", candidate)
	}

	// The rolling edge release is not a semantic version and must never be
	// selected by a semver-tagged channel.
	if candidate.assetName == "CodexLB_Installer_9.9.9.exe" {
		t.Fatal("beta channel selected the rolling edge release")
	}

	// Stable ignores the same pre-release.
	candidate, err = findStableUpdate(releases, "v2.0.0", stableChannel, testWrapperSHA)
	if err != nil || candidate != nil {
		t.Fatalf("stable result = (%+v, %v), want (nil, nil) with only a newer pre-release available", candidate, err)
	}

	// Already on the newest beta.
	candidate, err = findBetaUpdate(releases, "v2.1.0-beta.1", betaChannel, testWrapperSHA)
	if err != nil || candidate != nil {
		t.Fatalf("up-to-date beta result = (%+v, %v), want (nil, nil)", candidate, err)
	}
}

func TestSameVersionRebuildIsFlaggedForManualInstallOnly(t *testing.T) {
	releases := []GitHubRelease{validStableRelease("v2.0.0")}

	// Same version, different wrapper identity: offered, but marked so the
	// unattended path skips it.
	candidate, err := findStableUpdate(releases, "v2.0.0", stableChannel, strings.Repeat("0", 40))
	if err != nil || candidate == nil {
		t.Fatalf("same-version rebuild = (%+v, %v), want a candidate", candidate, err)
	}
	if !candidate.sameVersionRebuild {
		t.Fatal("same-version rebuild candidate must be flagged so automatic installs skip it")
	}

	// A genuine version bump is installable unattended.
	candidate, err = findStableUpdate(releases, "v1.0.0", stableChannel, testWrapperSHA)
	if err != nil || candidate == nil {
		t.Fatalf("upgrade = (%+v, %v), want a candidate", candidate, err)
	}
	if candidate.sameVersionRebuild {
		t.Fatal("a newer version must not be flagged as a same-version rebuild")
	}
}

func TestEdgeChannelTransitions(t *testing.T) {
	release := validEdgeRelease(testFullSHA)

	candidate, err := findEdgeUpdate(release, testFullSHA, edgeChannel, testWrapperSHA)
	if err != nil || candidate != nil {
		t.Fatalf("same edge build result = (%+v, %v), want (nil, nil)", candidate, err)
	}
	candidate, err = findEdgeUpdate(release, strings.ToUpper(testFullSHA), edgeChannel, strings.ToUpper(testWrapperSHA))
	if err != nil || candidate != nil {
		t.Fatalf("case-insensitive edge identity result = (%+v, %v), want (nil, nil)", candidate, err)
	}
	candidate, err = findEdgeUpdate(release, testFullSHA, edgeChannel, strings.Repeat("0", 40))
	if err != nil || candidate == nil || candidate.wrapperSHA != testWrapperSHA {
		t.Fatalf("wrapper-only edge update = (%+v, %v), want candidate", candidate, err)
	}

	otherSHA := strings.Repeat("f", 40)
	candidate, err = findEdgeUpdate(release, otherSHA, edgeChannel, testWrapperSHA)
	if err != nil || candidate == nil {
		t.Fatalf("new edge build result = (%+v, %v), want candidate", candidate, err)
	}
	if candidate.buildID != testFullSHA+"-"+testWrapperSHA || candidate.channel != edgeChannel || candidate.newVersion != "edge (0123456)" {
		t.Fatalf("new edge candidate = %+v", candidate)
	}

	// Opting into edge always installs the edge build, even if its SHA happens
	// to equal stale metadata carried by a stable build.
	candidate, err = findEdgeUpdate(release, testFullSHA, stableChannel, testWrapperSHA)
	if err != nil || candidate == nil {
		t.Fatalf("stable-to-edge transition = (%+v, %v), want candidate", candidate, err)
	}

	missingIdentity := release
	missingIdentity.Body = "Rolling build without an identity"
	if candidate, err := findEdgeUpdate(missingIdentity, otherSHA, edgeChannel, testWrapperSHA); err == nil || candidate != nil {
		t.Fatalf("markerless edge result = (%+v, %v), want error", candidate, err)
	}
	missingWrapper := release
	missingWrapper.Body = "upstream-sha: " + testFullSHA
	if candidate, err := findEdgeUpdate(missingWrapper, otherSHA, edgeChannel, testWrapperSHA); err == nil || candidate != nil || !strings.Contains(err.Error(), "wrapper") {
		t.Fatalf("wrapperless edge result = (%+v, %v), want wrapper identity error", candidate, err)
	}
	draft := release
	draft.Draft = true
	if candidate, err := findEdgeUpdate(draft, otherSHA, edgeChannel, testWrapperSHA); err == nil || candidate != nil {
		t.Fatalf("draft edge result = (%+v, %v), want error", candidate, err)
	}
	notPrerelease := release
	notPrerelease.Prerelease = false
	if candidate, err := findEdgeUpdate(notPrerelease, otherSHA, edgeChannel, testWrapperSHA); err == nil || candidate != nil {
		t.Fatalf("non-prerelease edge result = (%+v, %v), want error", candidate, err)
	}
	wrongTag := release
	wrongTag.TagName = "edge-old"
	if candidate, err := findEdgeUpdate(wrongTag, otherSHA, edgeChannel, testWrapperSHA); err == nil || candidate != nil {
		t.Fatalf("wrong-tag edge result = (%+v, %v), want error", candidate, err)
	}
	mismatchedAsset := release
	mismatchedAsset.Assets[0].Name = "CodexLB_Installer_edge.exe"
	mismatchedAsset.Assets[0].BrowserDownloadURL = "https://github.com/SpookySandwich/codex-lb-installer/releases/download/edge/CodexLB_Installer_edge.exe"
	if candidate, err := findEdgeUpdate(mismatchedAsset, otherSHA, edgeChannel, testWrapperSHA); err == nil || candidate != nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("mismatched edge asset result = (%+v, %v), want identity error", candidate, err)
	}
}

func TestReleaseFetcherRetryAndNonRetryPolicy(t *testing.T) {
	t.Run("retry transient status", func(t *testing.T) {
		var requests atomic.Int32
		var sleeps atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			call := requests.Add(1)
			if got := r.Header.Get("User-Agent"); !strings.HasPrefix(got, "CodexLB-Updater/") {
				t.Errorf("User-Agent = %q", got)
			}
			if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
				t.Errorf("Accept = %q", got)
			}
			if call < 3 {
				http.Error(w, "temporary", http.StatusServiceUnavailable)
				return
			}
			_, _ = io.WriteString(w, "[]")
		}))
		defer server.Close()

		fetcher := &releaseFetcher{
			client:    server.Client(),
			maxBytes:  64,
			attempts:  3,
			stableURL: server.URL,
			sleep: func(context.Context, time.Duration) error {
				sleeps.Add(1)
				return nil
			},
		}
		releases, err := fetcher.fetchStable(context.Background())
		if err != nil {
			t.Fatalf("fetchStable() error = %v", err)
		}
		if len(releases) != 0 || requests.Load() != 3 || sleeps.Load() != 2 {
			t.Fatalf("fetchStable() releases=%v requests=%d sleeps=%d", releases, requests.Load(), sleeps.Load())
		}
	})

	t.Run("do not retry client status", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			http.Error(w, "missing", http.StatusNotFound)
		}))
		defer server.Close()
		fetcher := &releaseFetcher{client: server.Client(), maxBytes: 64, attempts: 3, stableURL: server.URL, sleep: noRetrySleep}
		_, err := fetcher.fetchStable(context.Background())
		if err == nil {
			t.Fatal("fetchStable() unexpectedly succeeded")
		}
		var statusErr *httpStatusError
		if !errors.As(err, &statusErr) || statusErr.statusCode != http.StatusNotFound {
			t.Fatalf("fetchStable() error = %v, want 404 httpStatusError", err)
		}
		if requests.Load() != 1 {
			t.Fatalf("non-retryable status made %d requests, want 1", requests.Load())
		}
	})

	t.Run("retry transport error", func(t *testing.T) {
		var requests atomic.Int32
		client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			call := requests.Add(1)
			if call < 3 {
				return nil, errors.New("temporary transport failure")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("[]")),
				Request:    request,
			}, nil
		})}
		fetcher := &releaseFetcher{client: client, maxBytes: 64, attempts: 3, stableURL: "https://example.invalid/releases", sleep: noRetrySleep}
		if _, err := fetcher.fetchStable(context.Background()); err != nil {
			t.Fatalf("fetchStable() error = %v", err)
		}
		if requests.Load() != 3 {
			t.Fatalf("transport retry made %d requests, want 3", requests.Load())
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestReleaseFetcherRejectsInvalidBodies(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		maxBytes int64
		want     string
	}{
		{name: "body limit", body: strings.Repeat(" ", 16) + "[]", maxBytes: 8, want: "exceeds"},
		{name: "trailing JSON", body: "[] {}", maxBytes: 64, want: "trailing JSON"},
		{name: "malformed JSON", body: "[{", maxBytes: 64, want: "parse release metadata"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			fetcher := &releaseFetcher{client: server.Client(), maxBytes: test.maxBytes, attempts: 3, stableURL: server.URL, sleep: noRetrySleep}
			_, err := fetcher.fetchStable(context.Background())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("fetchStable() error = %v, want substring %q", err, test.want)
			}
			if requests.Load() != 1 {
				t.Fatalf("invalid response made %d requests, want 1", requests.Load())
			}
		})
	}
}

func testPayload(size int) []byte {
	if size < 2 {
		panic("test payload must leave room for MZ")
	}
	payload := bytes.Repeat([]byte{0x5a}, size)
	payload[0], payload[1] = 'M', 'Z'
	return payload
}

func payloadDigest(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func testDownloadCandidate(rawURL string, payload []byte) updateCandidate {
	const assetName = "CodexLB_Installer_1.2.3.exe"
	return updateCandidate{
		newVersion: "v1.2.3",
		channel:    stableChannel,
		buildID:    "v1.2.3",
		assetName:  assetName,
		url:        strings.TrimRight(rawURL, "/") + "/" + assetName,
		size:       int64(len(payload)),
		sha256:     payloadDigest(payload),
	}
}

func newTestDownloader(client *http.Client, tempDir string) *installerDownloader {
	return &installerDownloader{
		client:      client,
		tempDir:     tempDir,
		minBytes:    2,
		maxBytes:    1024,
		attempts:    1,
		sleep:       noRetrySleep,
		validateURL: func(string) error { return nil },
	}
}

func assertDirectoryEmpty(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir(%q) error = %v", directory, err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("staging directory leaked artifacts: %v", names)
	}
}

func TestInstallerDownloaderExactSuccess(t *testing.T) {
	payload := testPayload(64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/octet-stream" {
			t.Errorf("Accept = %q", got)
		}
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	tempDir := t.TempDir()
	downloader := newTestDownloader(server.Client(), tempDir)
	path, err := downloader.download(context.Background(), testDownloadCandidate(server.URL, payload))
	if err != nil {
		t.Fatalf("download() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	if filepath.Dir(path) != tempDir || filepath.Ext(path) != ".exe" || !isOwnedUpdateArtifact(filepath.Base(path)) {
		t.Fatalf("download() path = %q, want owned .exe in %q", path, tempDir)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("staged installer differs from the verified response body")
	}
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Fatalf("staging directory entries = %v, want only final installer", entries)
	}
}

func TestInstallerDownloaderRejectsInvalidCandidatesBeforeHTTP(t *testing.T) {
	payload := testPayload(64)
	base := testDownloadCandidate("https://example.invalid", payload)
	var requests atomic.Int32
	downloader := newTestDownloader(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, errors.New("HTTP must not be reached")
	})}, t.TempDir())
	downloader.validateURL = func(rawURL string) error {
		if rawURL == "" {
			return errors.New("missing URL")
		}
		return nil
	}

	tests := []struct {
		name   string
		mutate func(*updateCandidate)
		want   string
	}{
		{name: "missing name", mutate: func(c *updateCandidate) { c.assetName = "" }, want: "invalid installer name"},
		{name: "noncanonical name", mutate: func(c *updateCandidate) { c.assetName = "setup.exe" }, want: "invalid installer name"},
		{name: "missing size", mutate: func(c *updateCandidate) { c.size = 0 }, want: "size"},
		{name: "oversized", mutate: func(c *updateCandidate) { c.size = downloader.maxBytes + 1 }, want: "size"},
		{name: "missing digest", mutate: func(c *updateCandidate) { c.sha256 = "" }, want: "digest"},
		{name: "bad digest", mutate: func(c *updateCandidate) { c.sha256 = strings.Repeat("z", 64) }, want: "digest"},
		{name: "missing URL", mutate: func(c *updateCandidate) { c.url = "" }, want: "missing URL"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			test.mutate(&candidate)
			if path, err := downloader.download(context.Background(), candidate); err == nil || path != "" || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("download() = (%q, %v), want empty path and %q error", path, err, test.want)
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid candidates made %d HTTP requests", requests.Load())
	}
}

func TestInstallerDownloaderCleansArtifactsOnFailure(t *testing.T) {
	goodPayload := testPayload(64)
	tests := []struct {
		name            string
		handler         func([]byte) http.HandlerFunc
		mutateCandidate func(*updateCandidate)
		want            string
	}{
		{
			name: "non-OK status",
			handler: func([]byte) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no", http.StatusNotFound) }
			},
			want: "404",
		},
		{
			name: "announced truncation",
			handler: func(payload []byte) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Length", strconv.Itoa(len(payload)-1))
					_, _ = w.Write(payload[:len(payload)-1])
				}
			},
			want: "truncated",
		},
		{
			name: "announced oversize",
			handler: func(payload []byte) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Length", strconv.Itoa(len(payload)+1))
					w.WriteHeader(http.StatusOK)
				}
			},
			want: "exceeds signed size",
		},
		{
			name: "chunked truncation",
			handler: func(payload []byte) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
					_, _ = w.Write(payload[:len(payload)-1])
				}
			},
			want: "truncated",
		},
		{
			name: "chunked oversize",
			handler: func(payload []byte) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
					_, _ = w.Write(append(append([]byte(nil), payload...), 0))
				}
			},
			want: "exceeds signed size",
		},
		{
			name: "checksum mismatch",
			handler: func(payload []byte) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(payload) }
			},
			mutateCandidate: func(candidate *updateCandidate) {
				candidate.sha256 = strings.Repeat("0", sha256.Size*2)
			},
			want: "SHA-256",
		},
		{
			name: "not a PE executable",
			handler: func(payload []byte) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					payload = append([]byte(nil), payload...)
					payload[0], payload[1] = 'N', 'O'
					_, _ = w.Write(payload)
				}
			},
			mutateCandidate: func(candidate *updateCandidate) {
				badPayload := append([]byte(nil), goodPayload...)
				badPayload[0], badPayload[1] = 'N', 'O'
				candidate.sha256 = payloadDigest(badPayload)
			},
			want: "not a Windows executable",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.handler(goodPayload))
			defer server.Close()
			tempDir := t.TempDir()
			downloader := newTestDownloader(server.Client(), tempDir)
			candidate := testDownloadCandidate(server.URL, goodPayload)
			if test.mutateCandidate != nil {
				test.mutateCandidate(&candidate)
			}
			path, err := downloader.download(context.Background(), candidate)
			if err == nil || path != "" || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("download() = (%q, %v), want empty path and %q error", path, err, test.want)
			}
			assertDirectoryEmpty(t, tempDir)
		})
	}
}

func TestInstallerDownloaderRetriesTransientFailureWithoutLeaks(t *testing.T) {
	payload := testPayload(64)
	var requests atomic.Int32
	var sleeps atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			// Force an unknown content length so the first attempt creates a .part
			// file before discovering that the response is truncated.
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			_, _ = w.Write(payload[:len(payload)-1])
			return
		}
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	tempDir := t.TempDir()
	downloader := newTestDownloader(server.Client(), tempDir)
	downloader.attempts = 3
	downloader.sleep = func(context.Context, time.Duration) error {
		sleeps.Add(1)
		return nil
	}
	path, err := downloader.download(context.Background(), testDownloadCandidate(server.URL, payload))
	if err != nil {
		t.Fatalf("download() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	if requests.Load() != 2 || sleeps.Load() != 1 {
		t.Fatalf("download attempts=%d sleeps=%d, want 2 and 1", requests.Load(), sleeps.Load())
	}
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Fatalf("retry left staging entries %v, want only %q", entries, filepath.Base(path))
	}
}

func TestInstallerDownloaderDoesNotRetryIntegrityFailure(t *testing.T) {
	payload := testPayload(64)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	tempDir := t.TempDir()
	downloader := newTestDownloader(server.Client(), tempDir)
	downloader.attempts = 3
	candidate := testDownloadCandidate(server.URL, payload)
	candidate.sha256 = strings.Repeat("0", 64)
	if _, err := downloader.download(context.Background(), candidate); err == nil {
		t.Fatal("download() unexpectedly accepted a checksum mismatch")
	}
	if requests.Load() != 1 {
		t.Fatalf("integrity failure made %d requests, want 1", requests.Load())
	}
	assertDirectoryEmpty(t, tempDir)
}

func TestUpdateInstallerAlwaysCleansStagedArtifact(t *testing.T) {
	payload := testPayload(64)
	tests := []struct {
		name      string
		runnerErr error
	}{
		{name: "runner success"},
		{name: "runner failure", runnerErr: errors.New("fake start failure")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(payload)
			}))
			defer server.Close()
			tempDir := t.TempDir()
			downloader := newTestDownloader(server.Client(), tempDir)
			var runnerCalls int
			var stagedPath string
			var gotMode installMode
			installer := &updateInstaller{
				downloader: downloader,
				run: func(path string, mode installMode) error {
					runnerCalls++
					stagedPath = path
					gotMode = mode
					if _, err := os.Stat(path); err != nil {
						t.Errorf("fake runner could not see staged artifact: %v", err)
					}
					return test.runnerErr
				},
			}
			err := installer.install(context.Background(), testDownloadCandidate(server.URL, payload), installSilent)
			if !errors.Is(err, test.runnerErr) {
				t.Fatalf("install() error = %v, want %v", err, test.runnerErr)
			}
			if runnerCalls != 1 || gotMode != installSilent {
				t.Fatalf("fake runner calls=%d mode=%v", runnerCalls, gotMode)
			}
			if stagedPath == "" {
				t.Fatal("fake runner did not receive a staged path")
			}
			if _, statErr := os.Stat(stagedPath); !os.IsNotExist(statErr) {
				t.Fatalf("staged artifact still exists after runner returned: %v", statErr)
			}
			assertDirectoryEmpty(t, tempDir)
		})
	}
}

func TestUpdateCoordinatorSerializesConcurrentAttempts(t *testing.T) {
	const goroutines = 64
	coordinator := &updateCoordinator{}
	start := make(chan struct{})
	results := make(chan bool, goroutines)
	var wait sync.WaitGroup
	wait.Add(goroutines)
	for range goroutines {
		go func() {
			defer wait.Done()
			<-start
			results <- coordinator.tryBegin()
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	winners := 0
	for result := range results {
		if result {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent tryBegin() winners = %d, want exactly 1", winners)
	}
	if coordinator.tryBegin() {
		t.Fatal("coordinator admitted a second operation while active")
	}
	coordinator.finish()
	if !coordinator.tryBegin() {
		t.Fatal("coordinator did not become available after finish")
	}
	coordinator.finish()
}

func TestCleanupStaleUpdateArtifacts(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	maxAge := time.Hour
	old := now.Add(-2 * maxAge)
	fresh := now.Add(-maxAge / 2)

	createFile := func(name string, modified time.Time) {
		t.Helper()
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", name, err)
		}
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatalf("Chtimes(%q) error = %v", name, err)
		}
	}

	oldOwned := []string{
		"CodexLB_Update_old.part",
		"CodexLB_Update_old.exe",
		"CodexLB_Update_old.log",
		"CodexLB_Update.log",
		"CodexLB_Installer_Update_legacy.exe",
	}
	for _, name := range oldOwned {
		createFile(name, old)
	}
	preserved := []string{
		"CodexLB_Update_fresh.part",
		"CodexLB_Update_fresh.exe",
		"CodexLB_Update_fresh.log",
		"CodexLB_Update_old.txt",
		"unrelated.exe",
	}
	createFile(preserved[0], fresh)
	createFile(preserved[1], fresh)
	createFile(preserved[2], fresh)
	createFile(preserved[3], old)
	createFile(preserved[4], old)
	ownedDirectory := filepath.Join(directory, "CodexLB_Update_directory.exe")
	if err := os.Mkdir(ownedDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(ownedDirectory, old, old); err != nil {
		t.Fatal(err)
	}
	preserved = append(preserved, filepath.Base(ownedDirectory))

	if err := cleanupStaleUpdateArtifacts(directory, now, maxAge); err != nil {
		t.Fatalf("cleanupStaleUpdateArtifacts() error = %v", err)
	}
	for _, name := range oldOwned {
		if _, err := os.Stat(filepath.Join(directory, name)); !os.IsNotExist(err) {
			t.Errorf("stale owned artifact %q was not removed: %v", name, err)
		}
	}
	for _, name := range preserved {
		if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
			t.Errorf("artifact %q should have been preserved: %v", name, err)
		}

	}
	missing := filepath.Join(directory, "missing")
	if err := cleanupStaleUpdateArtifacts(missing, now, maxAge); err != nil {
		t.Fatalf("cleanup of missing directory error = %v", err)
	}
}

func TestUpdateBackoffIsBoundedAndMonotonicForConfiguredAttempts(t *testing.T) {
	previous := time.Duration(0)
	for attempt := 0; attempt < updateHTTPAttempts; attempt++ {
		got := updateBackoff(attempt)
		if got <= previous {
			t.Fatalf("updateBackoff(%d) = %s, previous = %s", attempt, got, previous)
		}
		if got > 2*time.Second {
			t.Fatalf("updateBackoff(%d) = %s, unexpectedly high", attempt, got)
		}
		previous = got
	}
}

func Example_parseSHA256Digest() {
	digest, err := parseSHA256Digest("SHA256:" + strings.Repeat("A", 64))
	fmt.Println(digest[:8], err)
	// Output: aaaaaaaa <nil>
}
