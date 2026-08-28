# Worker base revision is captured at READY, not at Execution start

The Execution records a starting base SHA for auditing, but each Worker captures its own start base when the Issue transitions to READY. A dependency-blocked Issue starts from a newer base that includes its prerequisite's merged code. Otherwise the merged-dependency rule (ADR 0005) would be defeated: the Worker would branch from a base that predates the very code it depends on. The Execution base is an audit snapshot, not an immutable base for every future Worker.
