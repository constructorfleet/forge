# One Execution per `forge execute` run

A single `forge execute a b c` invocation is one Execution spanning all of its Issues, per CONTEXT.md's definition of Execution as "a user-requested orchestration run over one or more Issues." Ticket 26 initially kept Scheduler additive by dispatching each requested Issue through `engine.Engine.Execute`, which minted one Execution row per Issue. That was an interim implementation detail, not the target domain model.

The 26b refactor introduced `Engine.StartExecution` and `Engine.ExecuteInExecution`: scheduler-driven multi-Issue runs now create one shared Execution before dispatch and run every Worker inside it, while single-Issue `Engine.Execute` still creates its own Execution for the direct API path. Each Worker still captures its own base at READY, so dependency-blocked Issues can branch from a newer base that includes prerequisite code without changing the Execution's audit base.

This shared-Execution behavior is required by ticket 30 (concurrent execution isolation) and ticket 32 (operational CLI), since both rely on one Execution ID addressing a whole multi-Issue run.
