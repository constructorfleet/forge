package needsinfo

import (
	"fmt"
	"strings"
)

// KindNeedsInfo marks the execution-phase comment Forge posts when an Issue
// pauses on NEEDS_INFO.
const KindNeedsInfo = "needs-info"

// KindNeedsHuman marks the wayfinding-phase comment Forge posts when a
// Decision pauses on NEEDS_HUMAN.
const KindNeedsHuman = "needs-human"

// CommentMarker returns the hidden marker Forge appends to tracker comments
// so resume can recognize Forge's own prompt without relying on the tracker
// author identity. kind namespaces the marker by pause type (KindNeedsInfo
// for an Issue, KindNeedsHuman for a Decision) so the two never collide.
func CommentMarker(kind, executionID, itemID string) string {
	return fmt.Sprintf("<!-- forge:%s execution=%s item=%s -->", kind, executionID, itemID)
}

// AppendCommentMarker appends Forge's hidden marker of the given kind to body.
func AppendCommentMarker(body, kind, executionID, itemID string) string {
	return body + "\n\n" + CommentMarker(kind, executionID, itemID)
}

// IsForgeComment reports whether body carries Forge's marker of the given
// kind for this execution and item.
func IsForgeComment(body, kind, executionID, itemID string) bool {
	return strings.Contains(body, CommentMarker(kind, executionID, itemID))
}
