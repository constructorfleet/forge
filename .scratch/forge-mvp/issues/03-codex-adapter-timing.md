Status: resolved
Type: wayfinder:grilling

## Question

Is the Codex adapter MVP-critical?

## Answer

No. Deferred to post-MVP. Proving two adapters tells us nothing about whether the architecture works. Claude Code is the only MVP backend. The Agent interface must still be correct enough that adding Codex requires no Scheduler changes. When implemented, prefer the Codex SDK over shelling out to the CLI.
