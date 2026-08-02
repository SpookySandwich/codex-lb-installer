package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	githubReleasesURL       = "https://api.github.com/repos/SpookySandwich/codex-lb-installer/releases?per_page=100"
	githubEdgeReleaseURL    = "https://api.github.com/repos/SpookySandwich/codex-lb-installer/releases/tags/edge"
	edgeTagName             = "edge"
	maxReleaseResponseBytes = 4 << 20
	minInstallerBytes       = 1 << 20
	maxInstallerBytes       = 512 << 20
	updateHTTPAttempts      = 3
	updateTempPrefix        = "CodexLB_Update_"
	staleUpdateArtifactAge  = 7 * 24 * time.Hour
)

// Build identity and payload metadata are injected by bundle_build.py.
// buildChannel was added after the first updater implementation, so an empty
// value intentionally means stable for compatibility with existing installs.
var (
	currentVersion string
	buildSHA       string
	buildChannel   string
	payloadID      string
	wrapperSHA     string
)

type installMode int

const (
	installVisible installMode = iota
	installSilent
)

type updateChannel string

const (
	// stableChannel keeps the "stable" wire value for compatibility with
	// installs built before the channel was user-selectable; it is presented
	// to users as "Release".
	stableChannel updateChannel = "stable"
	betaChannel   updateChannel = "beta"
	edgeChannel   updateChannel = "edge"
)

// parseUpdateChannel maps stored/embedded values onto a channel, defaulting to
// stable for empty or unrecognised input.
func parseUpdateChannel(value string) updateChannel {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(edgeChannel):
		return edgeChannel
	case string(betaChannel):
		return betaChannel
	case string(stableChannel), "release", "":
		return stableChannel
	default:
		return stableChannel
	}
}

func activeUpdateChannel() updateChannel {
	stored, err := readChannelSetting()
	if err != nil {
		return stableChannel
	}
	return parseUpdateChannel(stored)
}

func installedUpdateChannel() updateChannel {
	return parseUpdateChannel(buildChannel)
}

var payloadIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{64}$`)

func payloadDirectoryName(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if payloadIDPattern.MatchString(normalized) {
		return normalized
	}
	return "legacy"
}

func bundledPythonCandidates(installDir string) []string {
	return []string{
		filepath.Join(installDir, "versions", payloadDirectoryName(payloadID), "python", "python.exe"),
		filepath.Join(installDir, "python", "python.exe"),
	}
}

func findBundledPython(installDir string) (string, error) {
	for _, candidate := range bundledPythonCandidates(installDir) {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no bundled Python runtime exists under %s", installDir)
}

// GitHubAsset is the security-relevant subset of a GitHub release asset.
// GitHub calculates Digest server-side when the asset is uploaded.
type GitHubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"`
}

type GitHubRelease struct {
	TagName    string        `json:"tag_name"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	Body       string        `json:"body"`
	Assets     []GitHubAsset `json:"assets"`
}

type updateCandidate struct {
	newVersion string
	channel    updateChannel
	buildID    string
	wrapperSHA string
	assetName  string
	url        string
	size       int64
	sha256     string
	// sameVersionRebuild marks a candidate that carries the same application
	// version as the running build and differs only in wrapper identity. Those
	// are offered on an explicit user-driven check but never installed
	// automatically, so an unattended start can never swap the payload
	// sideways for one built from different sources.
	sameVersionRebuild bool
}

// versionCore removes historical tag prefixes such as "v" and "wv" while
// preserving semver suffixes for display.
func versionCore(v string) string {
	v = strings.TrimSpace(v)
	for i := 0; i < len(v); i++ {
		if v[i] >= '0' && v[i] <= '9' {
			return v[i:]
		}
	}
	return v
}

func displayVersion(v string) string {
	v = versionCore(v)
	if v == "" {
		return "unknown"
	}
	if v[0] >= '0' && v[0] <= '9' {
		return "v" + v
	}
	return v
}

type semanticVersion struct {
	major      int
	minor      int
	patch      int
	prerelease []string
	valid      bool
}

var semanticVersionPattern = regexp.MustCompile(
	`^(?:[A-Za-z]+)?([0-9]+)\.([0-9]+)\.([0-9]+)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`,
)

func parseSemanticVersion(v string) semanticVersion {
	matches := semanticVersionPattern.FindStringSubmatch(strings.TrimSpace(v))
	if matches == nil {
		return semanticVersion{}
	}

	nums := [3]int{}
	for i := range nums {
		n, err := strconv.Atoi(matches[i+1])
		if err != nil || (len(matches[i+1]) > 1 && matches[i+1][0] == '0') {
			return semanticVersion{}
		}
		nums[i] = n
	}

	var prerelease []string
	if matches[4] != "" {
		prerelease = strings.Split(matches[4], ".")
		for _, identifier := range prerelease {
			if isNumericIdentifier(identifier) && len(identifier) > 1 && identifier[0] == '0' {
				return semanticVersion{}
			}
		}
	}

	return semanticVersion{
		major:      nums[0],
		minor:      nums[1],
		patch:      nums[2],
		prerelease: prerelease,
		valid:      true,
	}
}

func isNumericIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for i := range len(value) {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func parseVersion(v string) (int, int, int) {
	parsed := parseSemanticVersion(v)
	if !parsed.valid {
		return 0, 0, 0
	}
	return parsed.major, parsed.minor, parsed.patch
}

func compareVersions(a, b string) int {
	av := parseSemanticVersion(a)
	bv := parseSemanticVersion(b)
	if !av.valid || !bv.valid {
		return 0
	}
	if av.major != bv.major {
		if av.major > bv.major {
			return 1
		}
		return -1
	}
	if av.minor != bv.minor {
		if av.minor > bv.minor {
			return 1
		}
		return -1
	}
	if av.patch != bv.patch {
		if av.patch > bv.patch {
			return 1
		}
		return -1
	}
	return comparePrerelease(av.prerelease, bv.prerelease)
}

func isNewerVersion(a, b string) bool {
	av := parseSemanticVersion(a)
	if !av.valid {
		return false
	}
	if !parseSemanticVersion(b).valid {
		return true
	}
	return compareVersions(a, b) > 0
}

func comparePrerelease(a, b []string) int {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	if len(a) == 0 {
		return 1
	}
	if len(b) == 0 {
		return -1
	}

	limit := min(len(a), len(b))
	for i := 0; i < limit; i++ {
		if cmp := comparePrereleaseIdentifier(a[i], b[i]); cmp != 0 {
			return cmp
		}
	}
	switch {
	case len(a) > len(b):
		return 1
	case len(a) < len(b):
		return -1
	default:
		return 0
	}
}

func comparePrereleaseIdentifier(a, b string) int {
	aNumeric := isNumericIdentifier(a)
	bNumeric := isNumericIdentifier(b)
	switch {
	case aNumeric && bNumeric:
		if len(a) != len(b) {
			if len(a) > len(b) {
				return 1
			}
			return -1
		}
		return strings.Compare(a, b)
	case aNumeric:
		return -1
	case bNumeric:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

var installerAssetPattern = regexp.MustCompile(`(?i)^CodexLB_Installer_[0-9A-Za-z][0-9A-Za-z._-]*\.exe$`)
var installerAssetUnsafeVersionCharacter = regexp.MustCompile(`[^0-9A-Za-z._]`)

func isInstallerAsset(name string) bool {
	return installerAssetPattern.MatchString(name)
}

// stableInstallerAssetName mirrors bundle_build.py's output naming rule. The
// release tag and attached executable must describe the same build; accepting
// an otherwise canonical executable from a different release can create an
// update loop even when its GitHub-provided digest is valid.
func stableInstallerAssetName(tag string) string {
	version := versionCore(tag)
	version = installerAssetUnsafeVersionCharacter.ReplaceAllString(version, "_")
	return "CodexLB_Installer_" + version + ".exe"
}

func pickInstallerAsset(rel GitHubRelease) (GitHubAsset, error) {
	var selected GitHubAsset
	count := 0
	for _, asset := range rel.Assets {
		if isInstallerAsset(asset.Name) {
			selected = asset
			count++
		}
	}
	if count == 0 {
		return GitHubAsset{}, fmt.Errorf("release %q has no canonical installer asset", rel.TagName)
	}
	if count != 1 {
		return GitHubAsset{}, fmt.Errorf("release %q has %d installer assets; refusing an ambiguous update", rel.TagName, count)
	}
	return selected, nil
}

func parseSHA256Digest(value string) (string, error) {
	algorithm, digest, ok := strings.Cut(strings.TrimSpace(value), ":")
	if !ok || !strings.EqualFold(algorithm, "sha256") || len(digest) != sha256.Size*2 {
		return "", fmt.Errorf("expected a sha256 release-asset digest")
	}
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size {
		return "", fmt.Errorf("invalid sha256 release-asset digest")
	}
	return strings.ToLower(digest), nil
}

func validateReleaseAssetURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid installer URL: %w", err)
	}
	if parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "github.com") || parsed.Port() != "" {
		return fmt.Errorf("installer URL is not an HTTPS github.com release URL")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("installer URL contains forbidden credentials or fragment")
	}
	const releasePath = "/spookysandwich/codex-lb-installer/releases/download/"
	if !strings.HasPrefix(strings.ToLower(parsed.EscapedPath()), releasePath) {
		return fmt.Errorf("installer URL is outside the CodexLB release path")
	}
	return nil
}

func validateAssetURLIdentity(rawURL, assetName string) error {
	if err := validateReleaseAssetURL(rawURL); err != nil {
		return err
	}
	parsed, _ := url.Parse(rawURL)
	if path.Base(parsed.Path) != assetName {
		return fmt.Errorf("installer URL filename does not match release metadata")
	}
	return nil
}

func candidateFromRelease(rel GitHubRelease, channel updateChannel, buildID string) (*updateCandidate, error) {
	asset, err := pickInstallerAsset(rel)
	if err != nil {
		return nil, err
	}
	if asset.Size < minInstallerBytes || asset.Size > maxInstallerBytes {
		return nil, fmt.Errorf("installer asset size %d is outside the allowed range", asset.Size)
	}
	digest, err := parseSHA256Digest(asset.Digest)
	if err != nil {
		return nil, fmt.Errorf("installer asset %q cannot be verified: %w", asset.Name, err)
	}
	if err := validateAssetURLIdentity(asset.BrowserDownloadURL, asset.Name); err != nil {
		return nil, err
	}
	return &updateCandidate{
		newVersion: displayVersion(rel.TagName),
		channel:    channel,
		buildID:    buildID,
		assetName:  asset.Name,
		url:        asset.BrowserDownloadURL,
		size:       asset.Size,
		sha256:     digest,
	}, nil
}

// findStableUpdate selects the newest stable release first and validates that
// exact release. A malformed newest release is an error, never "up to date".
// Switching from another channel to stable intentionally selects the latest
// stable build even when that is a semantic downgrade.
func findStableUpdate(releases []GitHubRelease, current string, installedChannel updateChannel, installedWrapperSHA string) (*updateCandidate, error) {
	return findTaggedUpdate(releases, current, installedChannel, installedWrapperSHA, stableChannel)
}

// findBetaUpdate selects the newest release of any maturity, so beta users
// receive both pre-releases and finals. The rolling "edge" release is excluded
// because its tag is not a semantic version.
func findBetaUpdate(releases []GitHubRelease, current string, installedChannel updateChannel, installedWrapperSHA string) (*updateCandidate, error) {
	return findTaggedUpdate(releases, current, installedChannel, installedWrapperSHA, betaChannel)
}

// findTaggedUpdate implements the shared selection rules for the semver-tagged
// channels. target decides which releases are eligible: stable accepts only
// finals, beta accepts pre-releases too.
func findTaggedUpdate(releases []GitHubRelease, current string, installedChannel updateChannel, installedWrapperSHA string, target updateChannel) (*updateCandidate, error) {
	var best *GitHubRelease
	for i := range releases {
		rel := &releases[i]
		if rel.Draft || !parseSemanticVersion(rel.TagName).valid {
			continue
		}
		if target == stableChannel && rel.Prerelease {
			continue
		}
		if best == nil || isNewerVersion(rel.TagName, best.TagName) {
			best = rel
		}
	}
	if best == nil {
		return nil, nil
	}
	publishedWrapperSHA := parseWrapperSHA(best.Body)
	sameVersionRebuild := false
	if installedChannel == target && parseSemanticVersion(current).valid {
		comparison := compareVersions(best.TagName, current)
		switch {
		case comparison < 0:
			return nil, nil
		case comparison == 0:
			// Rebuilt installers can improve the launcher/updater without an
			// upstream application version bump. A full wrapper identity makes
			// that same-version update explicit and prevents reinstall loops.
			if publishedWrapperSHA == "" || strings.EqualFold(publishedWrapperSHA, installedWrapperSHA) {
				return nil, nil
			}
			sameVersionRebuild = true
		}
	}
	candidate, err := candidateFromRelease(*best, target, best.TagName)
	if err != nil {
		return nil, err
	}
	if candidate.assetName != stableInstallerAssetName(best.TagName) {
		return nil, fmt.Errorf("the installer identity does not match release %s", best.TagName)
	}
	candidate.wrapperSHA = publishedWrapperSHA
	candidate.sameVersionRebuild = sameVersionRebuild
	return candidate, nil
}

var upstreamSHAPattern = regexp.MustCompile(`(?i)(?:^|\s)upstream-sha:\s*([0-9a-f]{40})(?:\s|$)`)
var wrapperSHAPattern = regexp.MustCompile(`(?i)(?:^|\s)wrapper-sha:\s*([0-9a-f]{40})(?:\s|$)`)

func parseUpstreamSHA(body string) string {
	match := upstreamSHAPattern.FindStringSubmatch(body)
	if match == nil {
		return ""
	}
	return strings.ToLower(match[1])
}

func parseWrapperSHA(body string) string {
	match := wrapperSHAPattern.FindStringSubmatch(body)
	if match == nil {
		return ""
	}
	return strings.ToLower(match[1])
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func edgeInstallerAssetName(upstreamSHA, releaseWrapperSHA string) string {
	return "CodexLB_Installer_edge_" + strings.ToLower(upstreamSHA) + "_" + strings.ToLower(releaseWrapperSHA) + ".exe"
}

func findEdgeUpdate(rel GitHubRelease, installedSHA string, installedChannel updateChannel, installedWrapperSHA string) (*updateCandidate, error) {
	if rel.Draft || !rel.Prerelease || !strings.EqualFold(rel.TagName, edgeTagName) {
		return nil, fmt.Errorf("the edge release is unavailable")
	}
	sha := parseUpstreamSHA(rel.Body)
	if sha == "" {
		return nil, fmt.Errorf("the edge release is missing its full upstream commit identity")
	}
	releaseWrapperSHA := parseWrapperSHA(rel.Body)
	if releaseWrapperSHA == "" {
		return nil, fmt.Errorf("the edge release is missing its full wrapper commit identity")
	}
	if installedChannel == edgeChannel && strings.EqualFold(sha, installedSHA) && strings.EqualFold(releaseWrapperSHA, installedWrapperSHA) {
		return nil, nil
	}
	candidate, err := candidateFromRelease(rel, edgeChannel, sha+"-"+releaseWrapperSHA)
	if err != nil {
		return nil, err
	}
	if candidate.assetName != edgeInstallerAssetName(sha, releaseWrapperSHA) {
		return nil, fmt.Errorf("the edge installer identity does not match upstream commit %s", sha)
	}
	candidate.wrapperSHA = releaseWrapperSHA
	candidate.newVersion = fmt.Sprintf("edge (%s)", shortSHA(sha))
	return candidate, nil
}

type httpStatusError struct {
	statusCode int
	status     string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("unexpected HTTP response: %s", e.status)
}

func isRetryableStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooEarly || status == http.StatusTooManyRequests || status >= 500
}

type retryableUpdateError struct{ err error }

func (e *retryableUpdateError) Error() string { return e.err.Error() }
func (e *retryableUpdateError) Unwrap() error { return e.err }

func retryable(err error) error {
	if err == nil {
		return nil
	}
	return &retryableUpdateError{err: err}
}

func isRetryableUpdateError(err error) bool {
	var retryErr *retryableUpdateError
	return errors.As(err, &retryErr)
}

type sleepFunc func(context.Context, time.Duration) error

func sleepWithContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func updateBackoff(attempt int) time.Duration {
	return time.Duration(1<<attempt) * 250 * time.Millisecond
}

func secureRedirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return fmt.Errorf("too many update redirects")
	}
	if req.URL.Scheme != "https" || req.URL.User != nil {
		return fmt.Errorf("update redirect must use credential-free HTTPS")
	}
	allowedHosts := map[string]bool{
		"api.github.com":                       true,
		"github.com":                           true,
		"release-assets.githubusercontent.com": true,
		"objects.githubusercontent.com":        true,
	}
	if !allowedHosts[strings.ToLower(req.URL.Hostname())] || req.URL.Port() != "" {
		return fmt.Errorf("update redirect host %q is not allowed", req.URL.Hostname())
	}
	return nil
}

func newUpdateHTTPClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Asset size and SHA-256 are defined over the uploaded bytes. Prevent the
	// transport from transparently decoding a content encoding before those
	// checks run.
	transport.DisableCompression = true
	transport.ResponseHeaderTimeout = 15 * time.Second
	transport.TLSHandshakeTimeout = 10 * time.Second
	return &http.Client{
		Transport:     transport,
		Timeout:       timeout,
		CheckRedirect: secureRedirectPolicy,
	}
}

type releaseFetcher struct {
	client    *http.Client
	maxBytes  int64
	attempts  int
	sleep     sleepFunc
	stableURL string
	edgeURL   string
}

func newReleaseFetcher() *releaseFetcher {
	return &releaseFetcher{
		client:    newUpdateHTTPClient(20 * time.Second),
		maxBytes:  maxReleaseResponseBytes,
		attempts:  updateHTTPAttempts,
		sleep:     sleepWithContext,
		stableURL: githubReleasesURL,
		edgeURL:   githubEdgeReleaseURL,
	}
}

func (f *releaseFetcher) fetchStable(ctx context.Context) ([]GitHubRelease, error) {
	var releases []GitHubRelease
	if err := f.fetchJSON(ctx, f.stableURL, &releases); err != nil {
		return nil, err
	}
	return releases, nil
}

func (f *releaseFetcher) fetchEdge(ctx context.Context) (GitHubRelease, error) {
	var release GitHubRelease
	if err := f.fetchJSON(ctx, f.edgeURL, &release); err != nil {
		return GitHubRelease{}, err
	}
	return release, nil
}

func (f *releaseFetcher) fetchJSON(ctx context.Context, endpoint string, target any) error {
	attempts := max(1, f.attempts)
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		lastErr = f.fetchJSONOnce(ctx, endpoint, target)
		if lastErr == nil {
			return nil
		}
		if !isRetryableUpdateError(lastErr) || attempt == attempts-1 {
			return lastErr
		}
		if err := f.sleep(ctx, updateBackoff(attempt)); err != nil {
			return err
		}
	}
	return lastErr
}

func (f *releaseFetcher) fetchJSONOnce(ctx context.Context, endpoint string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create release request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "CodexLB-Updater/"+displayVersion(currentVersion))

	response, err := f.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return retryable(fmt.Errorf("request release metadata: %w", err))
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		statusErr := &httpStatusError{statusCode: response.StatusCode, status: response.Status}
		if isRetryableStatus(response.StatusCode) {
			return retryable(statusErr)
		}
		return statusErr
	}

	limit := f.maxBytes
	if limit <= 0 {
		limit = maxReleaseResponseBytes
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return retryable(fmt.Errorf("read release metadata: %w", err))
	}
	if int64(len(body)) > limit {
		return fmt.Errorf("release metadata exceeds %d bytes", limit)
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("parse release metadata: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("release metadata contains trailing JSON")
		}
		return fmt.Errorf("parse trailing release metadata: %w", err)
	}
	return nil
}

var defaultReleaseFetcher = newReleaseFetcher()

func checkForUpdates(ctx context.Context, channel updateChannel) (*updateCandidate, error) {
	switch channel {
	case edgeChannel:
		release, err := defaultReleaseFetcher.fetchEdge(ctx)
		if err != nil {
			return nil, fmt.Errorf("check edge channel: %w", err)
		}
		return findEdgeUpdate(release, buildSHA, installedUpdateChannel(), wrapperSHA)
	case betaChannel:
		releases, err := defaultReleaseFetcher.fetchStable(ctx)
		if err != nil {
			return nil, fmt.Errorf("check beta channel: %w", err)
		}
		return findBetaUpdate(releases, currentVersion, installedUpdateChannel(), wrapperSHA)
	case stableChannel:
		releases, err := defaultReleaseFetcher.fetchStable(ctx)
		if err != nil {
			return nil, fmt.Errorf("check stable channel: %w", err)
		}
		return findStableUpdate(releases, currentVersion, installedUpdateChannel(), wrapperSHA)
	default:
		return nil, fmt.Errorf("unknown update channel %q", channel)
	}
}

type installerDownloader struct {
	client      *http.Client
	tempDir     string
	minBytes    int64
	maxBytes    int64
	attempts    int
	sleep       sleepFunc
	validateURL func(string) error
}

func newInstallerDownloader() *installerDownloader {
	return &installerDownloader{
		client:      newUpdateHTTPClient(10 * time.Minute),
		tempDir:     os.TempDir(),
		minBytes:    minInstallerBytes,
		maxBytes:    maxInstallerBytes,
		attempts:    updateHTTPAttempts,
		sleep:       sleepWithContext,
		validateURL: validateReleaseAssetURL,
	}
}

func (d *installerDownloader) download(ctx context.Context, candidate updateCandidate) (string, error) {
	if err := d.validateCandidate(candidate); err != nil {
		return "", err
	}
	attempts := max(1, d.attempts)
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		path, err := d.downloadOnce(ctx, candidate)
		if err == nil {
			return path, nil
		}
		lastErr = err
		if !isRetryableUpdateError(err) || attempt == attempts-1 {
			return "", err
		}
		if err := d.sleep(ctx, updateBackoff(attempt)); err != nil {
			return "", err
		}
	}
	return "", lastErr
}

func (d *installerDownloader) validateCandidate(candidate updateCandidate) error {
	if candidate.assetName == "" || !isInstallerAsset(candidate.assetName) {
		return fmt.Errorf("update candidate has an invalid installer name")
	}
	minBytes := d.minBytes
	if minBytes <= 0 {
		minBytes = 1
	}
	maxBytes := d.maxBytes
	if maxBytes <= 0 {
		maxBytes = maxInstallerBytes
	}
	if candidate.size < minBytes || candidate.size > maxBytes {
		return fmt.Errorf("installer size %d is outside the allowed range", candidate.size)
	}
	if _, err := parseSHA256Digest("sha256:" + candidate.sha256); err != nil {
		return fmt.Errorf("update candidate digest is invalid: %w", err)
	}
	if d.validateURL == nil {
		return fmt.Errorf("installer URL validation is not configured")
	}
	if err := d.validateURL(candidate.url); err != nil {
		return err
	}
	parsed, err := url.Parse(candidate.url)
	if err != nil || path.Base(parsed.Path) != candidate.assetName {
		return fmt.Errorf("installer URL filename does not match the selected asset")
	}
	return nil
}

func (d *installerDownloader) downloadOnce(ctx context.Context, candidate updateCandidate) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, candidate.url, nil)
	if err != nil {
		return "", fmt.Errorf("create installer request: %w", err)
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("User-Agent", "CodexLB-Updater/"+displayVersion(currentVersion))

	response, err := d.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", retryable(fmt.Errorf("download installer: %w", err))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		statusErr := &httpStatusError{statusCode: response.StatusCode, status: response.Status}
		if isRetryableStatus(response.StatusCode) {
			return "", retryable(statusErr)
		}
		return "", statusErr
	}
	if response.ContentLength >= 0 && response.ContentLength != candidate.size {
		if response.ContentLength < candidate.size {
			return "", retryable(fmt.Errorf("installer is truncated: expected %d bytes, server announced %d", candidate.size, response.ContentLength))
		}
		return "", fmt.Errorf("installer exceeds signed size: expected %d bytes, server announced %d", candidate.size, response.ContentLength)
	}

	if err := os.MkdirAll(d.tempDir, 0o700); err != nil {
		return "", fmt.Errorf("create update staging directory: %w", err)
	}
	partFile, err := os.CreateTemp(d.tempDir, updateTempPrefix+"*.part")
	if err != nil {
		return "", fmt.Errorf("create staged installer: %w", err)
	}
	partPath := partFile.Name()
	finalPath := strings.TrimSuffix(partPath, ".part") + ".exe"
	committed := false
	defer func() {
		_ = partFile.Close()
		if !committed {
			_ = os.Remove(partPath)
			_ = os.Remove(finalPath)
		}
	}()

	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(partFile, hasher), io.LimitReader(response.Body, candidate.size+1))
	if err != nil {
		return "", retryable(fmt.Errorf("write staged installer: %w", err))
	}
	if written < candidate.size {
		return "", retryable(fmt.Errorf("installer is truncated: expected %d bytes, received %d", candidate.size, written))
	}
	if written > candidate.size {
		return "", fmt.Errorf("installer exceeds signed size: expected %d bytes", candidate.size)
	}

	wantDigest, _ := hex.DecodeString(candidate.sha256)
	gotDigest := hasher.Sum(nil)
	if subtle.ConstantTimeCompare(gotDigest, wantDigest) != 1 {
		return "", fmt.Errorf("installer SHA-256 does not match GitHub release metadata")
	}
	if _, err := partFile.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("inspect staged installer: %w", err)
	}
	header := make([]byte, 2)
	if _, err := io.ReadFull(partFile, header); err != nil || header[0] != 'M' || header[1] != 'Z' {
		return "", fmt.Errorf("downloaded artifact is not a Windows executable")
	}
	if err := partFile.Sync(); err != nil {
		return "", fmt.Errorf("flush staged installer: %w", err)
	}
	if err := partFile.Close(); err != nil {
		return "", fmt.Errorf("close staged installer: %w", err)
	}
	if err := os.Rename(partPath, finalPath); err != nil {
		return "", fmt.Errorf("commit staged installer: %w", err)
	}
	committed = true
	return finalPath, nil
}

func installerArgs(installerPath string, mode installMode) []string {
	logName := strings.TrimSuffix(filepath.Base(installerPath), filepath.Ext(installerPath)) + ".log"
	args := []string{
		"/CODEXLBUPDATE",
		"/NORESTART",
		"/LOG=" + filepath.Join(os.TempDir(), logName),
	}
	if executable, err := os.Executable(); err == nil {
		// Bind an in-app update to the installation that launched it. Relying on
		// Inno Setup's remembered directory can target the wrong parallel or
		// relocated install.
		args = append(args, "/DIR="+filepath.Dir(executable))
	}
	if mode == installSilent {
		args = append(args, "/VERYSILENT", "/SUPPRESSMSGBOXES")
	}
	return args
}

type installerRunFunc func(string, installMode) error

func runInstallerProcess(installerPath string, mode installMode) error {
	installer := exec.Command(installerPath, installerArgs(installerPath, mode)...)
	if mode == installSilent {
		installer.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	}
	if err := installer.Start(); err != nil {
		return fmt.Errorf("start installer: %w", err)
	}
	// Waiting matters when Setup is cancelled or fails before PrepareToInstall:
	// the current launcher and backend remain alive, and the staged file can be
	// removed. On a committed update Setup itself stops this launcher.
	if err := installer.Wait(); err != nil {
		return fmt.Errorf("installer did not complete successfully: %w", err)
	}
	return nil
}

type updateInstaller struct {
	downloader *installerDownloader
	run        installerRunFunc
}

func (u *updateInstaller) install(ctx context.Context, candidate updateCandidate, mode installMode) error {
	installerPath, err := u.downloader.download(ctx, candidate)
	if err != nil {
		return err
	}
	defer os.Remove(installerPath)
	if err := u.run(installerPath, mode); err != nil {
		return err
	}
	return nil
}

var defaultUpdateInstaller = &updateInstaller{
	downloader: newInstallerDownloader(),
	run:        runInstallerProcess,
}

func downloadAndRunInstaller(ctx context.Context, candidate updateCandidate, mode installMode) error {
	return defaultUpdateInstaller.install(ctx, candidate, mode)
}

// updatePhase describes what the updater is doing, so the tray can tell the
// user whether codex-lb is about to be interrupted.
type updatePhase int

const (
	phaseIdle updatePhase = iota
	phaseChecking
	phaseInstalling
)

type updateCoordinator struct {
	mu       sync.Mutex
	active   bool
	phase    updatePhase
	observer func(updatePhase)
}

func (c *updateCoordinator) tryBegin() bool {
	c.mu.Lock()
	if c.active {
		c.mu.Unlock()
		return false
	}
	c.active = true
	c.mu.Unlock()
	return true
}

func (c *updateCoordinator) finish() {
	c.mu.Lock()
	c.active = false
	c.mu.Unlock()
	c.setPhase(phaseIdle)
}

// setPhase records the current phase and notifies the observer outside the
// lock, so a slow UI callback can never block an update.
func (c *updateCoordinator) setPhase(phase updatePhase) {
	c.mu.Lock()
	if c.phase == phase {
		c.mu.Unlock()
		return
	}
	c.phase = phase
	observer := c.observer
	c.mu.Unlock()
	if observer != nil {
		observer(phase)
	}
}

func (c *updateCoordinator) currentPhase() updatePhase {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.phase
}

// observe registers the tray callback. It fires immediately with the current
// phase so the menu never starts out stale.
func (c *updateCoordinator) observe(observer func(updatePhase)) {
	c.mu.Lock()
	c.observer = observer
	phase := c.phase
	c.mu.Unlock()
	if observer != nil {
		observer(phase)
	}
}

var updates updateCoordinator

func isOwnedUpdateArtifact(name string) bool {
	if name == "CodexLB_Update.log" {
		return true
	}
	if strings.HasPrefix(name, updateTempPrefix) &&
		(strings.HasSuffix(name, ".part") || strings.HasSuffix(name, ".exe") || strings.HasSuffix(name, ".log")) {
		return true
	}
	// Clean files left by the previous updater implementation as well.
	return strings.HasPrefix(name, "CodexLB_Installer_Update_") && strings.HasSuffix(name, ".exe")
}

func cleanupStaleUpdateArtifacts(directory string, now time.Time, maxAge time.Duration) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var cleanupErrors []error
	for _, entry := range entries {
		if entry.IsDir() || !isOwnedUpdateArtifact(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			cleanupErrors = append(cleanupErrors, err)
			continue
		}
		if now.Sub(info.ModTime()) < maxAge {
			continue
		}
		if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil && !os.IsNotExist(err) {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	return errors.Join(cleanupErrors...)
}

func currentDisplay(channel updateChannel) string {
	display := displayVersion(currentVersion)
	if channel == edgeChannel && installedUpdateChannel() == edgeChannel && buildSHA != "" {
		// Edge builds carry their upstream commit as semver build metadata
		// (e.g. 1.23.0-beta.2+edge.c539a20). Appending the sha again would
		// print it twice, so only add it for builds that predate that.
		if !strings.Contains(display, "+edge.") {
			return fmt.Sprintf("%s (%s)", display, shortSHA(buildSHA))
		}
	}
	return display
}

func installConfirmMessage(newVersion string, channel updateChannel) string {
	return fmt.Sprintf(lang.InstallConfirm, currentDisplay(channel), newVersion) + "\n\n" + lang.DowntimeNotice
}

func recordUpdateError(stage string, err error) {
	if err == nil {
		return
	}
	cacheDir, cacheErr := os.UserCacheDir()
	if cacheErr != nil {
		return
	}
	logDir := filepath.Join(cacheDir, "CodexLB", "logs")
	if os.MkdirAll(logDir, 0o700) != nil {
		return
	}
	file, openErr := os.OpenFile(filepath.Join(logDir, "updater.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if openErr != nil {
		return
	}
	defer file.Close()
	_, _ = fmt.Fprintf(file, "%s [%s] %v\n", time.Now().UTC().Format(time.RFC3339), stage, err)
}
