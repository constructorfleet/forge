# Changelog

## [0.9.0](https://github.com/constructorfleet/forge/compare/forge-v0.8.0...forge-v0.9.0) (2026-08-29)


### Features

* Language & server detection (registry + Detected Servers) ([#129](https://github.com/constructorfleet/forge/issues/129)) ([288b8bb](https://github.com/constructorfleet/forge/commit/288b8bb4f23b8606854197e758c7cebf46f6a1c4)), closes [#122](https://github.com/constructorfleet/forge/issues/122)

## [0.8.0](https://github.com/constructorfleet/forge/compare/forge-v0.7.1...forge-v0.8.0) (2026-08-29)


### Features

* lsp: config surface ([#118](https://github.com/constructorfleet/forge/issues/118)) ([d1a6a4c](https://github.com/constructorfleet/forge/commit/d1a6a4c38ca9649a059c057e5f250b503bcd66bc)), closes [#86](https://github.com/constructorfleet/forge/issues/86)

## [0.7.1](https://github.com/constructorfleet/forge/compare/forge-v0.7.0...forge-v0.7.1) (2026-08-29)


### Bug Fixes

* [Feature Request]: Monitor and Rectify PRs After Creation ([#115](https://github.com/constructorfleet/forge/issues/115)) ([eaf8c56](https://github.com/constructorfleet/forge/commit/eaf8c56e88dadf39e44d6c4a907dd835a72841a7)), closes [#109](https://github.com/constructorfleet/forge/issues/109)

## [0.7.0](https://github.com/constructorfleet/forge/compare/forge-v0.6.0...forge-v0.7.0) (2026-08-29)


### Features

* [Feature Request]: `forge execute` Should Respect Issue Dependency Order ([#114](https://github.com/constructorfleet/forge/issues/114)) ([eddb707](https://github.com/constructorfleet/forge/commit/eddb70729401751a2ebeb5a9ab9c270965c8d4d8)), closes [#108](https://github.com/constructorfleet/forge/issues/108)

## [0.6.0](https://github.com/constructorfleet/forge/compare/forge-v0.5.0...forge-v0.6.0) (2026-08-29)


### Features

* commit/PR seam + tracker GraphQL and dependency-parser fixes ([#112](https://github.com/constructorfleet/forge/issues/112)) ([cfb1ada](https://github.com/constructorfleet/forge/commit/cfb1adabbb4fa91cd38dc09586001a55b89e45ec))


### Bug Fixes

* The execute subcommand is not using TDD ([#111](https://github.com/constructorfleet/forge/issues/111)) ([eb2badf](https://github.com/constructorfleet/forge/commit/eb2badf6c5b34ab4492f6783253b765f677bb004)), closes [#105](https://github.com/constructorfleet/forge/issues/105)

## [0.5.0](https://github.com/constructorfleet/forge/compare/forge-v0.4.1...forge-v0.5.0) (2026-08-29)


### Features

* goal init: --from &lt;file&gt; to wrap an existing doc ([#107](https://github.com/constructorfleet/forge/issues/107)) ([6a8ea81](https://github.com/constructorfleet/forge/commit/6a8ea81e9e8dd42c87678597a3ab7f87c3dff0c2)), closes [#100](https://github.com/constructorfleet/forge/issues/100)

## [0.4.1](https://github.com/constructorfleet/forge/compare/forge-v0.4.0...forge-v0.4.1) (2026-08-29)


### Bug Fixes

* **cancel:** forge cancel should handle dead process execution ([#72](https://github.com/constructorfleet/forge/issues/72)) ([0d14843](https://github.com/constructorfleet/forge/commit/0d14843a4df4b239254fd2fc8fa9e663649d70f7))

## [0.4.0](https://github.com/constructorfleet/forge/compare/forge-v0.3.0...forge-v0.4.0) (2026-08-29)


### Features

* **specengine:** 16-18 — Specification + TicketPlan pipeline ([#63](https://github.com/constructorfleet/forge/issues/63)) ([a4d22d2](https://github.com/constructorfleet/forge/commit/a4d22d236bb46071a8dccaf5dbacb37c807995a3))

## [0.3.0](https://github.com/constructorfleet/forge/compare/forge-v0.2.0...forge-v0.3.0) (2026-08-29)


### Features

* **agent:** enforce result envelope via --json-schema ([#67](https://github.com/constructorfleet/forge/issues/67)) ([f7e4d93](https://github.com/constructorfleet/forge/commit/f7e4d93f94917b8be9068dc93ce7893609ca6ee4)), closes [#20](https://github.com/constructorfleet/forge/issues/20)

## [0.2.0](https://github.com/constructorfleet/forge/compare/forge-v0.1.0...forge-v0.2.0) (2026-08-29)


### Features

* **agent:** add codex, opencode, pi, and OpenAI-compatible adapters ([#64](https://github.com/constructorfleet/forge/issues/64)) ([1a695ca](https://github.com/constructorfleet/forge/commit/1a695cab00d9b04baf05c03e968f0d5f1a6573bd))
* **agent:** backend-independent Agent contract and fake adapter ([#16](https://github.com/constructorfleet/forge/issues/16)) ([584ca17](https://github.com/constructorfleet/forge/commit/584ca17b4427be6ea42ede51d69f5790800b7b69))
* **agent:** Claude Code production adapter ([#25](https://github.com/constructorfleet/forge/issues/25)) ([9c8dd1a](https://github.com/constructorfleet/forge/commit/9c8dd1a9200c02fabcfc5907043a057191ad0b81))
* **cli:** forge init deterministic repo-policy discovery ([#29](https://github.com/constructorfleet/forge/issues/29)) ([51e3422](https://github.com/constructorfleet/forge/commit/51e3422ad4bab4c0304f479b95ad988dc7a6681d))
* **config:** load and validate .forge.yaml ([#12](https://github.com/constructorfleet/forge/issues/12)) ([18f82fc](https://github.com/constructorfleet/forge/commit/18f82fc08b63b5434dfcfca5d42778803ab551bb))
* **domain:** project skeleton, domain model, and issue state machine ([#11](https://github.com/constructorfleet/forge/issues/11)) ([8ee912c](https://github.com/constructorfleet/forge/commit/8ee912c6695f3b27a1a2f62e13e574aadbab652e))
* **engine:** gate/review repair loop with separate retry budgets ([#21](https://github.com/constructorfleet/forge/issues/21)) ([2edac92](https://github.com/constructorfleet/forge/commit/2edac92f5980896f5e8b2ed1194f856880ac66e5))
* **engine:** independent review stage after quality gates ([#20](https://github.com/constructorfleet/forge/issues/20)) ([9116afb](https://github.com/constructorfleet/forge/commit/9116afb97e5f2a33cdccbadb2b61a71ecfe5303e))
* **engine:** multi-issue DAG scheduling with bounded concurrency ([#26](https://github.com/constructorfleet/forge/issues/26)) ([7db7767](https://github.com/constructorfleet/forge/commit/7db776774069c31b8f36c28e781d0a7ad8d1dfc5))
* **engine:** needs-info flow with label/comment + forge resume ([#28](https://github.com/constructorfleet/forge/issues/28)) ([a603ff3](https://github.com/constructorfleet/forge/commit/a603ff3cad1356318264b87e18b406edaffe66ed))
* **engine:** single-issue execution engine, forge execute/status ([#18](https://github.com/constructorfleet/forge/issues/18)) ([1c53b18](https://github.com/constructorfleet/forge/commit/1c53b18996776aac82a9067c4dc487ad2ef10777))
* **gate:** quality gate runner wired into IMPLEMENTED path ([#19](https://github.com/constructorfleet/forge/issues/19)) ([fdc5dcf](https://github.com/constructorfleet/forge/commit/fdc5dcf221122186759cf0722e6c747a716f7fd0))
* implement NEEDS_HUMAN checkpoint & pause (Ticket 15a/[#51](https://github.com/constructorfleet/forge/issues/51)) ([#57](https://github.com/constructorfleet/forge/issues/57)) ([57eb551](https://github.com/constructorfleet/forge/commit/57eb551daefde3540ad57ed8f330bcecfce8b26b))
* **repocontext:** compile immutable Repository Context per Execution ([#17](https://github.com/constructorfleet/forge/issues/17)) ([dcd30f9](https://github.com/constructorfleet/forge/commit/dcd30f964c0b1feecc5cff957cc831944c2bf5c0))
* **scheduler:** external dependency observed nodes with merged-PR satisfaction ([#27](https://github.com/constructorfleet/forge/issues/27)) ([50f776c](https://github.com/constructorfleet/forge/commit/50f776c038574482122bf7bdebf031c640524056))
* **storage:** transactional SQLite persistence with event log ([#13](https://github.com/constructorfleet/forge/issues/13)) ([9452942](https://github.com/constructorfleet/forge/commit/945294200bc0edc00aa60f5ca4fbb91801dfc7f4))
* **tracker:** GitHub adapter, strict dependency parsing, DAG + cycle detection ([#14](https://github.com/constructorfleet/forge/issues/14)) ([e613db7](https://github.com/constructorfleet/forge/commit/e613db70c9db53000c9c5df63e6e33b12bf7798e))
* **workspace:** git-worktree Workspace manager with per-worker base capture ([#15](https://github.com/constructorfleet/forge/issues/15)) ([2285a10](https://github.com/constructorfleet/forge/commit/2285a10df3add562df4910eb1bc66f29e396fdca))


### Bug Fixes

* **agent:** collapse ScenarioKey to plain issue ID, deep-copy recorded slices ([#16](https://github.com/constructorfleet/forge/issues/16)) ([7bd8118](https://github.com/constructorfleet/forge/commit/7bd8118640e555a112d516bacab73c5b3d63afc1))
* **agent:** harden Claude adapter per review findings ([ddb48da](https://github.com/constructorfleet/forge/commit/ddb48da1e1868f14f6518277dbb0c703f60e3502))
* **cli:** address thermos review findings on forge init ([#29](https://github.com/constructorfleet/forge/issues/29)) ([6091102](https://github.com/constructorfleet/forge/commit/6091102240cc57fb178290ce1d153700701c33ac))
* **engine:** address thermos review findings on execution engine ([#18](https://github.com/constructorfleet/forge/issues/18)) ([5911733](https://github.com/constructorfleet/forge/commit/5911733773c21692d8c6adb042560e855f7f26b4))
* **engine:** address thermos review findings on needs-info flow ([#28](https://github.com/constructorfleet/forge/issues/28)) ([83d5511](https://github.com/constructorfleet/forge/commit/83d5511f30ca7b82f907d67c7ac2b9f88deb7761))
* **engine:** address thermos review findings on review stage ([#20](https://github.com/constructorfleet/forge/issues/20)) ([122aeec](https://github.com/constructorfleet/forge/commit/122aeec1d097dc28071af42d57b52cf521fb0bcf))
* **engine:** scheduler no-progress detection, safe cancellation, cleanup ([#26](https://github.com/constructorfleet/forge/issues/26)) ([51d55bb](https://github.com/constructorfleet/forge/commit/51d55bbb98dff31b423d74caf563fb5d64d6b4a2))
* **external-deps:** avoid PR JSON type collision ([e20c1f7](https://github.com/constructorfleet/forge/commit/e20c1f7d82c104f5cada58e507540d0295325b6a))
* **external-deps:** harden reachability and stall handling ([#27](https://github.com/constructorfleet/forge/issues/27)) ([06a4286](https://github.com/constructorfleet/forge/commit/06a42863afc825d465214ab525b16cf40252b0df))
* **gate:** address thermos review findings on quality gate runner ([#19](https://github.com/constructorfleet/forge/issues/19)) ([1a3eb5b](https://github.com/constructorfleet/forge/commit/1a3eb5bf7c8c08396cc79328fb884aa0acec52f9))
* **repocontext:** propagate unreadable instruction errors, filter VCS noise ([#17](https://github.com/constructorfleet/forge/issues/17)) ([41467ae](https://github.com/constructorfleet/forge/commit/41467aef99679a8deb36a11d67510d863ed903e7))
* **storage:** address thermos review — schema rigor, CAS, N+1, safe JSON ([6d78233](https://github.com/constructorfleet/forge/commit/6d78233f09dc40a0664a8940b1f83e2c5819e24d))
* **tracker:** address thermos review findings on GitHub adapter ([#14](https://github.com/constructorfleet/forge/issues/14)) ([2228319](https://github.com/constructorfleet/forge/commit/2228319eaac32d92b8ae48986dde50bfa6c17eae))
* **workspace:** address thermos review findings on Workspace manager ([#15](https://github.com/constructorfleet/forge/issues/15)) ([0d32cbc](https://github.com/constructorfleet/forge/commit/0d32cbc133fde3f74a0df8535077a4717aae08a9))
