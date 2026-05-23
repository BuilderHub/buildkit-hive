package s3

import (
	"regexp"
	"strings"

	"github.com/pkg/errors"
)

// DefaultCacheGroup is the tenant namespace used when group is unset.
const DefaultCacheGroup = "global"

// buildkitBlobsSegment is the fixed path segment under <group>/ for blob objects.
// Full S3 key layout: <bucket>/<group>/buildkitblobs/<digest>
const buildkitBlobsSegment = "buildkitblobs/"

const maxCacheGroupLen = 63

var cacheGroupRegexp = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9_-]*[a-zA-Z0-9])?$`)

// NormalizeCacheGroup returns DefaultCacheGroup when group is empty.
func NormalizeCacheGroup(group string) string {
	group = strings.TrimSpace(group)
	if group == "" {
		return DefaultCacheGroup
	}
	return group
}

// ValidateCacheGroup checks that group is a valid cache tenant identifier.
// An empty group is normalized to DefaultCacheGroup.
func ValidateCacheGroup(group string) (string, error) {
	group = strings.TrimSpace(group)
	if group == "" {
		return DefaultCacheGroup, nil
	}
	if len(group) > maxCacheGroupLen {
		return "", errors.Errorf("cache group must be at most %d characters", maxCacheGroupLen)
	}
	if group == "." || group == ".." || strings.ContainsAny(group, "/\\") {
		return "", errors.Errorf("invalid cache group %q", group)
	}
	if !cacheGroupRegexp.MatchString(group) {
		return "", errors.Errorf("invalid cache group %q (use alphanumeric characters with optional hyphens or underscores)", group)
	}
	return group, nil
}
