# Changelog

## [0.19.3](https://github.com/constructorfleet/forge/compare/forge-v0.19.2...forge-v0.19.3) (2026-09-01)


### Bug Fixes

* Review: feed the parent spec into the reviewer prompt (sub-issues lose cross-ticket intent) ([#376](https://github.com/constructorfleet/forge/issues/376)) ([0ed1814](https://github.com/constructorfleet/forge/commit/0ed1814e58ce59d4d94771d2bfff80d463356db5)), closes [#319](https://github.com/constructorfleet/forge/issues/319)
* Stale forge binary silently ignores source fixes in the dogfood loop (add version stamp + rebuild step) ([#373](https://github.com/constructorfleet/forge/issues/373)) ([86e031c](https://github.com/constructorfleet/forge/commit/86e031ce01b460ffaf620a7cddaa2ebe022c0a53)), closes [#321](https://github.com/constructorfleet/forge/issues/321)

## [0.19.2](https://github.com/constructorfleet/forge/compare/forge-v0.19.1...forge-v0.19.2) (2026-09-01)


### Bug Fixes

* internal/gate.Runner default Now uses real time.Now(), causing intermittent test flakiness ([#371](https://github.com/constructorfleet/forge/issues/371)) ([c595fb0](https://github.com/constructorfleet/forge/commit/c595fb0f7b11cf0a8333e9f1d5f075312fb84792)), closes [#327](https://github.com/constructorfleet/forge/issues/327)

## [0.19.1](https://github.com/constructorfleet/forge/compare/forge-v0.19.0...forge-v0.19.1) (2026-09-01)


### Bug Fixes

* CI conflict resolver loops forever: rebases onto stale local base, not fetched remote tip ([#351](https://github.com/constructorfleet/forge/issues/351)) ([9072462](https://github.com/constructorfleet/forge/commit/90724626fb11c0b3c1bad7ca60871e2ca4dcd41f)), closes [#349](https://github.com/constructorfleet/forge/issues/349)

## [0.19.0](https://github.com/constructorfleet/forge/compare/forge-v0.18.0...forge-v0.19.0) (2026-08-31)


### Features

* Provider split · 5: DependencyStore read ([#323](https://github.com/constructorfleet/forge/issues/323)) ([d6c951a](https://github.com/constructorfleet/forge/commit/d6c951a6a4c24090f746bb2c4419e0505b96b085))

## [0.18.0](https://github.com/constructorfleet/forge/compare/forge-v0.17.0...forge-v0.18.0) (2026-08-31)


### Features

* Provider split · 3: composition config + wiring + validation (GitHub) ([#317](https://github.com/constructorfleet/forge/issues/317)) ([40abd4c](https://github.com/constructorfleet/forge/commit/40abd4cc57cb27ce38617376b4e9c2b20dd0c631)), closes [#295](https://github.com/constructorfleet/forge/issues/295)

## [0.17.0](https://github.com/constructorfleet/forge/compare/forge-v0.16.2...forge-v0.17.0) (2026-08-31)


### Features

* **domain:** qualify issue identity by provider ([#315](https://github.com/constructorfleet/forge/issues/315)) ([283281f](https://github.com/constructorfleet/forge/commit/283281f8340ffd1a2a48522687a5b75491ea798a))

## [0.16.2](https://github.com/constructorfleet/forge/compare/forge-v0.16.1...forge-v0.16.2) (2026-08-31)


### Bug Fixes

* bug in cancel ([#312](https://github.com/constructorfleet/forge/issues/312)) ([8bab37e](https://github.com/constructorfleet/forge/commit/8bab37ee22ce23baf35ec887163555a9dd95fbbf))

## [0.16.1](https://github.com/constructorfleet/forge/compare/forge-v0.16.0...forge-v0.16.1) (2026-08-31)


### Bug Fixes

* resume needs-info by marker ([#310](https://github.com/constructorfleet/forge/issues/310)) ([c0b6841](https://github.com/constructorfleet/forge/commit/c0b6841054cc9f45940e187267c74e90b7036f04))

## [0.16.0](https://github.com/constructorfleet/forge/compare/forge-v0.15.5...forge-v0.16.0) (2026-08-31)


### Features

* **tracker/github:** read native issue "blocked by" relationships ([#306](https://github.com/constructorfleet/forge/issues/306)) ([b010c32](https://github.com/constructorfleet/forge/commit/b010c32f883d06ba425e3d3ae3921240438cd75c))

## [0.15.5](https://github.com/constructorfleet/forge/compare/forge-v0.15.4...forge-v0.15.5) (2026-08-31)


### Bug Fixes

* Automatically resolve fixable PR merge conflicts instead of always routing to NEEDS_INFO ([#270](https://github.com/constructorfleet/forge/issues/270)) ([e8e7411](https://github.com/constructorfleet/forge/commit/e8e741184c9f880777a6ef5369de53ab464f67ad))

## [0.15.4](https://github.com/constructorfleet/forge/compare/forge-v0.15.3...forge-v0.15.4) (2026-08-31)


### Bug Fixes

* agent IMPLEMENTED + empty diff hard-FAILs instead of routing to NEEDS_INFO (no-code deliverables can't complete) ([#262](https://github.com/constructorfleet/forge/issues/262)) ([1f884f1](https://github.com/constructorfleet/forge/commit/1f884f1b368479365d4de70e3b240aa496965c61))

## [0.15.3](https://github.com/constructorfleet/forge/compare/forge-v0.15.2...forge-v0.15.3) (2026-08-31)


### Bug Fixes

* **engine:** review VerdictInconclusive routes to retryable FAILED, not NEEDS_INFO ([#260](https://github.com/constructorfleet/forge/issues/260)) ([421543a](https://github.com/constructorfleet/forge/commit/421543a7277816ffa6db37deec3cacf16eba7750))

## [0.15.2](https://github.com/constructorfleet/forge/compare/forge-v0.15.1...forge-v0.15.2) (2026-08-31)


### Bug Fixes

* execution-phase adapters persist transcript_events as they occur + never blank ([#257](https://github.com/constructorfleet/forge/issues/257)) ([#258](https://github.com/constructorfleet/forge/issues/258)) ([65f46ad](https://github.com/constructorfleet/forge/commit/65f46ad5c12f10a9f6448a5fda05c1c94de69af6))

## [0.15.1](https://github.com/constructorfleet/forge/compare/forge-v0.15.0...forge-v0.15.1) (2026-08-31)


### Bug Fixes

* Ticket-plan review repair loop has the same non-determinism hard-fail as spec review ([#254](https://github.com/constructorfleet/forge/issues/254)) ([2096645](https://github.com/constructorfleet/forge/commit/2096645f9ccfd7770f6b284455542328e121c877)), closes [#251](https://github.com/constructorfleet/forge/issues/251)

## [0.15.0](https://github.com/constructorfleet/forge/compare/forge-v0.14.2...forge-v0.15.0) (2026-08-31)


### Features

* spec review can hard-fail forge plan on reviewer non-determinism (budget exhausted with no surviving findings) ([#252](https://github.com/constructorfleet/forge/issues/252)) ([b53892e](https://github.com/constructorfleet/forge/commit/b53892e6f0682ac77ed9d551a8fb9c5624a5ede8)), closes [#249](https://github.com/constructorfleet/forge/issues/249)


### Bug Fixes

* wire all implemented agent providers ([#255](https://github.com/constructorfleet/forge/issues/255)) ([bd12107](https://github.com/constructorfleet/forge/commit/bd1210720ca17d7c04589c2d35d13b65ce7a718c))

## [0.14.2](https://github.com/constructorfleet/forge/compare/forge-v0.14.1...forge-v0.14.2) (2026-08-31)


### Bug Fixes

* Detect and rebase stale PRs onto their target branch during CI_PENDING ([#237](https://github.com/constructorfleet/forge/issues/237)) ([92e9b36](https://github.com/constructorfleet/forge/commit/92e9b36fc06f61c611b08cd89ff0f1f9811e9406)), closes [#233](https://github.com/constructorfleet/forge/issues/233)

## [0.14.1](https://github.com/constructorfleet/forge/compare/forge-v0.14.0...forge-v0.14.1) (2026-08-31)


### Bug Fixes

* CI_PENDING is still not actually waiting for CI status checks to pass ([#234](https://github.com/constructorfleet/forge/issues/234)) ([9dfeae5](https://github.com/constructorfleet/forge/commit/9dfeae582c33749c444954eec8eceb0b5b9fd5d0)), closes [#231](https://github.com/constructorfleet/forge/issues/231)

## [0.14.0](https://github.com/constructorfleet/forge/compare/forge-v0.13.0...forge-v0.14.0) (2026-08-31)


### Features

* [Feature Request] Automatic Self Reporting ([#229](https://github.com/constructorfleet/forge/issues/229)) ([0df930f](https://github.com/constructorfleet/forge/commit/0df930faaa29ffe56a9cdcb05b9dbf05c1554314)), closes [#141](https://github.com/constructorfleet/forge/issues/141)

## [0.13.0](https://github.com/constructorfleet/forge/compare/forge-v0.12.0...forge-v0.13.0) (2026-08-31)


### Features

* forge execute subcommand is not actually waiting for CI_PENDING ([#226](https://github.com/constructorfleet/forge/issues/226)) ([997f4ec](https://github.com/constructorfleet/forge/commit/997f4ec7540473d2bd81df7ed618bce03d96f3fd)), closes [#215](https://github.com/constructorfleet/forge/issues/215)

## [0.12.0](https://github.com/constructorfleet/forge/compare/forge-v0.11.3...forge-v0.12.0) (2026-08-31)


### Features

* [Question] Are review agent findings actually being addressed? ([#223](https://github.com/constructorfleet/forge/issues/223)) ([a113176](https://github.com/constructorfleet/forge/commit/a113176ba7b23b078866e9f38345f12a3b205234)), closes [#222](https://github.com/constructorfleet/forge/issues/222)

## [0.11.3](https://github.com/constructorfleet/forge/compare/forge-v0.11.2...forge-v0.11.3) (2026-08-31)


### Bug Fixes

* Claude adapter: honor ModeStructured (verbatim prompt, per-call --json-schema, result as Summary) ([#211](https://github.com/constructorfleet/forge/issues/211)) ([2b8e0f4](https://github.com/constructorfleet/forge/commit/2b8e0f4d767e3c962759c3713f6746d14f184c6b)), closes [#200](https://github.com/constructorfleet/forge/issues/200)

## [0.11.2](https://github.com/constructorfleet/forge/compare/forge-v0.11.1...forge-v0.11.2) (2026-08-31)


### Bug Fixes

* **tracker:** accept dash/colon-led descriptions on dependency bullets ([#209](https://github.com/constructorfleet/forge/issues/209)) ([076a9a5](https://github.com/constructorfleet/forge/commit/076a9a543a8cca0ce7a759a13d3087ae2987c726))

## [0.11.1](https://github.com/constructorfleet/forge/compare/forge-v0.11.0...forge-v0.11.1) (2026-08-31)


### Bug Fixes

* add agent review mode so the findings envelope survives the claude backend ([#184](https://github.com/constructorfleet/forge/issues/184)) ([33e55c2](https://github.com/constructorfleet/forge/commit/33e55c2b7ef0dda171d435d8671b2e4efd0ef1aa)), closes [#183](https://github.com/constructorfleet/forge/issues/183)

## [0.11.0](https://github.com/constructorfleet/forge/compare/forge-v0.10.0...forge-v0.11.0) (2026-08-31)


### Features

* assurances in axis envelope + assurance-vs-finding tensions ([#181](https://github.com/constructorfleet/forge/issues/181)) ([dceb607](https://github.com/constructorfleet/forge/commit/dceb607a970ba2a275741e65688e5a26b0a92f19)), closes [#176](https://github.com/constructorfleet/forge/issues/176)
* Codex (non-native) MCP channel: forge internal-mcp ([#140](https://github.com/constructorfleet/forge/issues/140)) ([163fbcb](https://github.com/constructorfleet/forge/commit/163fbcbe17f319a81071871a7789d0a9a082b48b)), closes [#127](https://github.com/constructorfleet/forge/issues/127)
* deterministic review synthesizer (dedup, confidence-fold, ranking, tensions) ([#175](https://github.com/constructorfleet/forge/issues/175)) ([7a885b6](https://github.com/constructorfleet/forge/commit/7a885b681ff68646b622e6e984cfbac5880bba10)), closes [#160](https://github.com/constructorfleet/forge/issues/160)
* forge init: lsp coverage Notes + PATH-probe (empty servers) ([#170](https://github.com/constructorfleet/forge/issues/170)) ([ec6bf7e](https://github.com/constructorfleet/forge/commit/ec6bf7e28ef4576dc0ffc8adbee73fd319f4676d))
* generalize gopls driver into language-neutral lspdriver ([#152](https://github.com/constructorfleet/forge/issues/152)) ([#164](https://github.com/constructorfleet/forge/issues/164)) ([02b6233](https://github.com/constructorfleet/forge/commit/02b6233592523a2d120d2238a04186c268110571))
* grow the Reviewer contract (Finding fields + severity map + enriched feedback) ([#163](https://github.com/constructorfleet/forge/issues/163)) ([021c624](https://github.com/constructorfleet/forge/commit/021c62472f2b11afa9853190d85bde2663527c7b))
* per-axis review degradation + escalate incomplete/unsatisfiable reviews to NEEDS_INFO ([#177](https://github.com/constructorfleet/forge/issues/177)) ([2651ab1](https://github.com/constructorfleet/forge/commit/2651ab1831f1c269f1698a823950dda16f168284)), closes [#161](https://github.com/constructorfleet/forge/issues/161)
* persist full per-axis review audit trail + configurable rubric overrides ([#178](https://github.com/constructorfleet/forge/issues/178)) ([3b5054b](https://github.com/constructorfleet/forge/commit/3b5054b8f82016cafba71015ca794b39bc5bc803)), closes [#162](https://github.com/constructorfleet/forge/issues/162)
* run three review axes (bugs/quality/docs) in parallel ([#172](https://github.com/constructorfleet/forge/issues/172)) ([6007a0e](https://github.com/constructorfleet/forge/commit/6007a0eedbce1c0c6ae92f956fb3bff285037a2a))
* single-axis (bugs/security) reviewer wired end-to-end ([#165](https://github.com/constructorfleet/forge/issues/165)) ([eea4009](https://github.com/constructorfleet/forge/commit/eea4009bcfcb9ba75068a6938c206f0c1f668a02))
* thread workspace path into review.Request so axes read the workspace ([#171](https://github.com/constructorfleet/forge/issues/171)) ([2568609](https://github.com/constructorfleet/forge/commit/2568609d3539ba33f771177d36165f2d6fb79b9d))


### Bug Fixes

* claude adapter: multi-ext .lsp.json + command/args split + provisioning-trigger decouple ([#167](https://github.com/constructorfleet/forge/issues/167)) ([1465417](https://github.com/constructorfleet/forge/commit/14654171a45a6377c647c5e137594ce7ecdf30d3))
* de-flake lspdriver didOpen test (read-before-flush race) ([#180](https://github.com/constructorfleet/forge/issues/180)) ([f4d2ed1](https://github.com/constructorfleet/forge/commit/f4d2ed14a93979b410516fd8517ce90f49a6f781))
* deflake TestDriver_DidOpenIsLazyAndPerFile ([#174](https://github.com/constructorfleet/forge/issues/174)) ([430041d](https://github.com/constructorfleet/forge/commit/430041deabab31021ff5974caefe2b010016b894))
* gopls driver: per-capability typed methods + lazy didOpen ([#137](https://github.com/constructorfleet/forge/issues/137)) ([31489dc](https://github.com/constructorfleet/forge/commit/31489dc9c0aa167dc1f63775bf3d6a7d16720399)), closes [#124](https://github.com/constructorfleet/forge/issues/124)
* registry: TS/Python/Rust server rows + profiles + NewRegistry merge-preserve-Profile ([#166](https://github.com/constructorfleet/forge/issues/166)) ([2dc414d](https://github.com/constructorfleet/forge/commit/2dc414d26fd0b14dbfc95227a127d52aab568750))

## [0.10.0](https://github.com/constructorfleet/forge/compare/forge-v0.9.1...forge-v0.10.0) (2026-08-29)


### Features

* Claude native-LSP gopls provisioning ([#135](https://github.com/constructorfleet/forge/issues/135)) ([29cf35f](https://github.com/constructorfleet/forge/commit/29cf35fd74eb9532473e90963df632b8fe42616e)), closes [#128](https://github.com/constructorfleet/forge/issues/128)

## [0.9.1](https://github.com/constructorfleet/forge/compare/forge-v0.9.0...forge-v0.9.1) (2026-08-29)


### Bug Fixes

* gopls driver: subprocess lifecycle, initialize handshake, readiness & restart ([#132](https://github.com/constructorfleet/forge/issues/132)) ([8967c54](https://github.com/constructorfleet/forge/commit/8967c5463f5f7c05ccf710f0f6f2dfdcdab2b81f)), closes [#123](https://github.com/constructorfleet/forge/issues/123)

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
