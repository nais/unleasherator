package controller

import (
	"regexp"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	unleashv1 "github.com/nais/unleasherator/api/v1"
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

// deployedImages returns the distinct images the given instances report running,
// in a stable order. An empty result means nothing has been deployed yet; more
// than one entry means the fleet disagrees about what it is running.
func deployedImages(instances []unleashv1.Unleash) []string {
	seen := make(map[string]struct{}, len(instances))
	images := make([]string, 0, len(instances))

	for _, instance := range instances {
		image := instance.Status.ResolvedReleaseChannelImage
		if image == "" {
			continue
		}
		if _, ok := seen[image]; ok {
			continue
		}
		seen[image] = struct{}{}
		images = append(images, image)
	}

	sort.Strings(images)
	return images
}

// rollbackBaseline returns the one image the whole fleet runs, which is the only
// image a rollback can defensibly send instances back to.
//
// It returns "" when instances disagree. Picking one of several deployed images
// would move instances to a version they were never on, and the pick would come
// down to List order — which says nothing about which image is correct. An empty
// baseline means no PreviousImage is captured, and rollback then refuses outright
// rather than guessing.
func rollbackBaseline(instances []unleashv1.Unleash) string {
	images := deployedImages(instances)
	if len(images) != 1 {
		return ""
	}
	return images[0]
}

// downgradeFrom reports whether the channel's target image is an older version
// than something the fleet already runs, and which image it would move back
// from. Every deployed image is checked, not just the rollback baseline: a fleet
// that disagrees has no baseline, and that must not be the reason a downgrade
// gets through. The oldest image the target undercuts is reported, since that is
// the largest step backwards being asked for.
func (r *ReleaseChannelReconciler) downgradeFrom(releaseChannel *unleashv1.ReleaseChannel, instances []unleashv1.Unleash) (string, bool) {
	if releaseChannel.Spec.AllowDowngrade {
		return "", false
	}

	target := string(releaseChannel.Spec.Image)
	oldest := ""

	for _, deployed := range deployedImages(instances) {
		order, comparable := compareImageVersions(deployed, target)
		if !comparable || order >= 0 {
			continue
		}
		if oldest == "" {
			oldest = deployed
			continue
		}
		if order, comparable := compareImageVersions(oldest, deployed); comparable && order < 0 {
			oldest = deployed
		}
	}

	return oldest, oldest != ""
}
