package needsinfo

import (
	"fmt"
	"strings"
)

// CommentMarker returns the hidden marker Forge appends to NEEDS_INFO tracker
// comments so resume can recognize Forge's own prompt without relying on the
// tracker author identity.
func CommentMarker(executionID, issueID string) string {
	return fmt.Sprintf("<!-- forge:needs-info execution=%s issue=%s -->", executionID, issueID)
}

// AppendCommentMarker appends Forge's hidden NEEDS_INFO marker to body.
func AppendCommentMarker(body, executionID, issueID string) string {
	return body + "\n\n" + CommentMarker(executionID, issueID)
}

// IsForgeComment reports whether body carries Forge's NEEDS_INFO marker for
// this execution and Issue.
func IsForgeComment(body, executionID, issueID string) bool {
	return strings.Contains(body, CommentMarker(executionID, issueID))
}
