package rbac

import "strings"

// RouteMatch is the result of matching an HTTP request against the route_permissions rules.
type RouteMatch struct {
	Permission   string
	ResourceType string
	ResourceID   string // first wildcard capture; empty when ResourceType == ""
}

// MatchRoute scans rules in order and returns the first match.
// When the matched rule has ResourceType set, the first wildcard segment of the path
// is captured as ResourceID so the middleware can perform a resource-scoped permission check.
func MatchRoute(rules []RouteRule, method, path string) RouteMatch {
	for _, rule := range rules {
		if rule.Method != "*" && rule.Method != method {
			continue
		}
		if capture, ok := matchAndCapture(rule.Path, path); ok {
			resourceID := ""
			if rule.ResourceType != "" {
				resourceID = capture
			}
			return RouteMatch{
				Permission:   rule.Permission,
				ResourceType: rule.ResourceType,
				ResourceID:   resourceID,
			}
		}
	}
	return RouteMatch{}
}

// RoutePermission is a backwards-compatible wrapper around MatchRoute.
func RoutePermission(rules []RouteRule, method, path string) string {
	return MatchRoute(rules, method, path).Permission
}

// matchAndCapture matches pattern against path and returns the value of the first
// wildcard segment (the part of path that the first '*' matched), plus whether the
// pattern matched at all. Used to extract resource IDs from URL paths.
//
// Pattern rules (same semantics as before):
//   - Literal: exact segment match
//   - "*" (not last): matches exactly one segment
//   - "*" (last): matches one or more remaining segments
//   - "<prefix>*" (last): current segment must start with prefix; absorbs rest
func matchAndCapture(pattern, path string) (firstCapture string, matched bool) {
	pp := strings.Split(strings.Trim(pattern, "/"), "/")
	ap := strings.Split(strings.Trim(path, "/"), "/")

	captured := false
	for i, seg := range pp {
		last := i == len(pp)-1

		if seg == "*" {
			if last {
				if !captured && i < len(ap) {
					firstCapture = ap[i]
				}
				return firstCapture, i < len(ap)
			}
			if i >= len(ap) {
				return "", false
			}
			if !captured {
				firstCapture = ap[i]
				captured = true
			}
			continue
		}

		if strings.HasSuffix(seg, "*") {
			prefix := seg[:len(seg)-1]
			if i >= len(ap) || !strings.HasPrefix(ap[i], prefix) {
				return "", false
			}
			return firstCapture, true
		}

		if i >= len(ap) || ap[i] != seg {
			return "", false
		}
	}
	return firstCapture, len(ap) == len(pp)
}
