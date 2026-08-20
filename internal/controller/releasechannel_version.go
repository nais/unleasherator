package controller

import (
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// semverCore matches the MAJOR.MINOR.PATCH core of a version. Release tags in
// production embed the version rather than being one — the real format is
// "v7-7.5.1-e309531" — so the core has to be located inside the tag instead of
// handed straight to a semver parser.
var semverCore = regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+`)

// imageTag returns the tag of a container image reference, or "" when the
// reference carries no tag. A colon only separates a tag when it appears after
// the last slash; in "registry:5000/unleash" it is a host port. Digest-pinned
// references have no version to order on at all.
func imageTag(image string) string {
	if image == "" || strings.Contains(image, "@") {
		return ""
	}

	name := image
	if slash := strings.LastIndex(name, "/"); slash != -1 {
		name = name[slash+1:]
	}

	colon := strings.LastIndex(name, ":")
	if colon == -1 {
		return ""
	}
	return name[colon+1:]
}

// imageVersion extracts the orderable version from a container image reference.
//
// Only the MAJOR.MINOR.PATCH core participates in ordering. Whatever the tag
// appends — the "e309531" build hash in "v7-7.5.1-e309531", a prerelease
// qualifier — is deliberately dropped: those suffixes carry no meaningful order
// relative to each other, and ranking a rebuild of the same version below its
// predecessor would refuse rollouts that are not downgrades at all.
func imageVersion(image string) (*semver.Version, bool) {
	tag := imageTag(image)
	if tag == "" {
		return nil, false
	}

	core := semverCore.FindString(tag)
	if core == "" {
		return nil, false
	}

	version, err := semver.NewVersion(core)
	if err != nil {
		return nil, false
	}
	return version, true
}

// compareImageVersions orders target against current the way semver does: -1
// when target is older, 0 when they are the same version, +1 when target is
// newer.
//
// ok is false when either reference has no recognisable version — a mutable tag
// like "latest", a digest pin, an untagged image. No ordering claim can be made
// then, so callers must not read that as a downgrade: refusing every rollout we
// cannot rank would block channels that never used semver tags to begin with.
func compareImageVersions(current, target string) (int, bool) {
	currentVersion, ok := imageVersion(current)
	if !ok {
		return 0, false
	}

	targetVersion, ok := imageVersion(target)
	if !ok {
		return 0, false
	}

	return targetVersion.Compare(currentVersion), true
}
