# Review is a fresh agent invocation, not a self-review continuation

After quality gates pass, review is a second, independent agent invocation — same workspace, fresh context. The reviewer receives only the diff, issue requirements, repo policy, and gate results. It does not receive the implementation agent's conversation history. Asking the same context that wrote the code to independently assess its correctness is not a review. Separate invocation also enables cross-backend review (Claude implements, Codex reviews) without scheduler changes.
