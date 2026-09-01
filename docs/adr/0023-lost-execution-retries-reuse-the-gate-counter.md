# A LOST execution retries against the existing gate-failure counter

ADR 0020 says a `LOST` execution retries the Issue under its existing retry budget (ADR 0007). It must not use a new, separate counter.

The retry budget has three counters: gate, review, and CI. A lost worker is not a review rejection, because no review verdict was reached. It is not a CI failure either, because CI has not run yet: a remote execution is lost before or during the Agent or Quality-Gate attempt.

A lost worker is closest to a gate failure. Both are an incomplete attempt. Both must redo the same work before the Issue can proceed.

`RecoverLostExecution` (`internal/engine/lostrecovery.go`) records a `LOST` retry against the gate counter (`domain.Issue.RecordGateFailure`). It reuses the counter's existing exhaustion and persistence path without change. This choice avoids a fourth counter class, which ADR 0020 rules out.
