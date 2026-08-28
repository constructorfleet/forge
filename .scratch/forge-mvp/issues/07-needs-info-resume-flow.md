Status: resolved
Type: wayfinder:grilling

## Question

When a human answers a NEEDS_INFO question on the issue, how does Forge detect the answer and resume the Worker? Options include: polling for new comments, GitHub webhook, manual `forge resume`, or some combination.

## Answer

Manual `forge resume <execution-id>` for MVP. On resume, Forge re-fetches the issue and comments, compares against the stored needs-info checkpoint, and detects new human input. If found, transitions NEEDS_INFO → READY and resumes the Worker with only: original issue context + previous question + new comments since needs-info. No polling daemon — a blocked ticket may sit for days. Later: `forge watch` daemon mode with polling and/or GitHub webhooks.
