# Changelog

## [0.47.0](https://github.com/constructorfleet/forge/compare/forge-v0.46.0...forge-v0.47.0) (2026-09-05)


### Features

* TUI: order gate rows into the timeline by finish time ([#661](https://github.com/constructorfleet/forge/issues/661)) ([69a772c](https://github.com/constructorfleet/forge/commit/69a772c4dfce9d625bb9fab474455d81884a14f0)), closes [#530](https://github.com/constructorfleet/forge/issues/530)

## [0.46.0](https://github.com/constructorfleet/forge/compare/forge-v0.45.0...forge-v0.46.0) (2026-09-05)


### Features

* Transcript pane: anchor a pinned selection that slides off by more than one event ([#655](https://github.com/constructorfleet/forge/issues/655)) ([be51ca4](https://github.com/constructorfleet/forge/commit/be51ca4f89cfb6e77694b3d8d4a40798f416fc8a)), closes [#524](https://github.com/constructorfleet/forge/issues/524)
* TUI: the transcript pane has no scroll keys, so scrollback is unreachable ([#658](https://github.com/constructorfleet/forge/issues/658)) ([2d5b17d](https://github.com/constructorfleet/forge/commit/2d5b17d78c1f3350ea6d7cef9188754fa72f1b52)), closes [#523](https://github.com/constructorfleet/forge/issues/523)
* TUI: transcript pane clamps its own height to the viewport ([#659](https://github.com/constructorfleet/forge/issues/659)) ([819938b](https://github.com/constructorfleet/forge/commit/819938be1447903a745eb4379a10528157ace31a)), closes [#519](https://github.com/constructorfleet/forge/issues/519)

## [0.45.0](https://github.com/constructorfleet/forge/compare/forge-v0.44.2...forge-v0.45.0) (2026-09-05)


### Features

* Planning writes no events rows, so the planning phase has no audit log ([#650](https://github.com/constructorfleet/forge/issues/650)) ([ebf6861](https://github.com/constructorfleet/forge/commit/ebf68612e5ecb146f595a9e5124e8c1076e97fe0)), closes [#471](https://github.com/constructorfleet/forge/issues/471)
* TUI: move the on-demand diff read off the event loop into a tea.Cmd ([#652](https://github.com/constructorfleet/forge/issues/652)) ([f2c7ae6](https://github.com/constructorfleet/forge/commit/f2c7ae6665c2806f1384c2d0afbf5e61cc843db4)), closes [#539](https://github.com/constructorfleet/forge/issues/539)
* TUI: TranscriptTailer.Poll double-counts appended events when scrolled back ([#654](https://github.com/constructorfleet/forge/issues/654)) ([9feb37d](https://github.com/constructorfleet/forge/commit/9feb37da0ad20aeaa16fb7abc993c28c18637bb8)), closes [#520](https://github.com/constructorfleet/forge/issues/520)


### Bug Fixes

* Planning leases identify their owner by bare pid only ([#653](https://github.com/constructorfleet/forge/issues/653)) ([0dfbaf5](https://github.com/constructorfleet/forge/commit/0dfbaf56aa3b0fc32e29af8940ffc808ae4ba268)), closes [#557](https://github.com/constructorfleet/forge/issues/557)

## [0.44.2](https://github.com/constructorfleet/forge/compare/forge-v0.44.1...forge-v0.44.2) (2026-09-05)


### Bug Fixes

* forge resume uses any execution error as a signal to retry as a planning resume ([#648](https://github.com/constructorfleet/forge/issues/648)) ([56b72e7](https://github.com/constructorfleet/forge/commit/56b72e7f3cb3a810e2def63721611ef60fb14132)), closes [#472](https://github.com/constructorfleet/forge/issues/472)

## [0.44.1](https://github.com/constructorfleet/forge/compare/forge-v0.44.0...forge-v0.44.1) (2026-09-05)


### Bug Fixes

* Planning Artifact loader (planningfs.FileArtifactLoader) still resolves .forge/features/&lt;feature-id&gt; relative to cwd, not repo root ([#645](https://github.com/constructorfleet/forge/issues/645)) ([db00c24](https://github.com/constructorfleet/forge/commit/db00c24e91ab7dfa867116759083592ee9f7c107))

## [0.44.0](https://github.com/constructorfleet/forge/compare/forge-v0.43.0...forge-v0.44.0) (2026-09-05)


### Features

* Surface transcript-read lag in PlanningModel's pane too ([#638](https://github.com/constructorfleet/forge/issues/638)) ([37ece36](https://github.com/constructorfleet/forge/commit/37ece367a5e5377d7a8c02bf0fab9c32aa709b0c)), closes [#633](https://github.com/constructorfleet/forge/issues/633)


### Bug Fixes

* align TUI transcript glyphs with spec ([#642](https://github.com/constructorfleet/forge/issues/642)) ([4d95313](https://github.com/constructorfleet/forge/commit/4d95313cb5f8bb560412633253b21fc5d4e69d65))
* Same cwd-relative repoRoot/config/db resolution gap exists in execute, resume, cancel, materialize, and plan ([#641](https://github.com/constructorfleet/forge/issues/641)) ([ae2b3c3](https://github.com/constructorfleet/forge/commit/ae2b3c3441102ca0dd19ac1589409d96a1bdf5ce)), closes [#576](https://github.com/constructorfleet/forge/issues/576)

## [0.43.0](https://github.com/constructorfleet/forge/compare/forge-v0.42.2...forge-v0.43.0) (2026-09-05)


### Features

* TUI: a transcript read slower than the poll interval silently thins the transcript refresh rate ([#635](https://github.com/constructorfleet/forge/issues/635)) ([c6ef63d](https://github.com/constructorfleet/forge/commit/c6ef63dbec6cd581362ff00e47b6764d5dc44d4d)), closes [#567](https://github.com/constructorfleet/forge/issues/567)

## [0.42.2](https://github.com/constructorfleet/forge/compare/forge-v0.42.1...forge-v0.42.2) (2026-09-05)


### Bug Fixes

* Reset workers.owner_pid and owner_token when a worker process exits cleanly ([#630](https://github.com/constructorfleet/forge/issues/630)) ([c2ccc44](https://github.com/constructorfleet/forge/commit/c2ccc443473a716fefc62f4a6f39f026b5f409fa)), closes [#563](https://github.com/constructorfleet/forge/issues/563)

## [0.42.1](https://github.com/constructorfleet/forge/compare/forge-v0.42.0...forge-v0.42.1) (2026-09-05)


### Bug Fixes

* Reset workers.owner_pid and owner_token when a worker process exits cleanly ([#628](https://github.com/constructorfleet/forge/issues/628)) ([b374a5f](https://github.com/constructorfleet/forge/commit/b374a5f065c21cd0c7fe404ac0f7b76cdbcd4be5)), closes [#563](https://github.com/constructorfleet/forge/issues/563)

## [0.42.0](https://github.com/constructorfleet/forge/compare/forge-v0.41.0...forge-v0.42.0) (2026-09-05)


### Features

* gate_runs has no AgentRun/attempt scoping column, forcing a time-window heuristic in the TUI ([#621](https://github.com/constructorfleet/forge/issues/621)) ([a51b271](https://github.com/constructorfleet/forge/commit/a51b2712083d87e2beda7c17d7c01b9d0c6d8a33)), closes [#596](https://github.com/constructorfleet/forge/issues/596)


### Bug Fixes

* avoid staticcheck SA4023 in process_identity fallback ([#623](https://github.com/constructorfleet/forge/issues/623)) ([1527f5b](https://github.com/constructorfleet/forge/commit/1527f5b67a4713424a739e12459e4e8ddff60015))
* Reset workers.owner_pid and owner_token when a worker process exits cleanly ([#620](https://github.com/constructorfleet/forge/issues/620)) ([642e276](https://github.com/constructorfleet/forge/commit/642e276fe598d1b8fef10255ae5ea946a7ad9222)), closes [#563](https://github.com/constructorfleet/forge/issues/563)
* Reset workers.owner_pid and owner_token when a worker process exits cleanly ([#626](https://github.com/constructorfleet/forge/issues/626)) ([a0b0eec](https://github.com/constructorfleet/forge/commit/a0b0eec8446f26d9636b97b6bbb662248e149237)), closes [#563](https://github.com/constructorfleet/forge/issues/563)
* TUI wrapWidth does one hard rune-count wrap, not a real terminal-column wrap ([#622](https://github.com/constructorfleet/forge/issues/622)) ([641c521](https://github.com/constructorfleet/forge/commit/641c52198ad4a0c258d11a9ce4fe484106e62945)), closes [#602](https://github.com/constructorfleet/forge/issues/602)
* TUI wrapWidth does one hard rune-count wrap, not a real terminal-column wrap ([#625](https://github.com/constructorfleet/forge/issues/625)) ([201bd17](https://github.com/constructorfleet/forge/commit/201bd17e14c6b005f6a1e55442243104045bbaaf)), closes [#602](https://github.com/constructorfleet/forge/issues/602)

## [0.41.0](https://github.com/constructorfleet/forge/compare/forge-v0.40.0...forge-v0.41.0) (2026-09-05)


### Features

* golangci-lint's bundled type-checker fails on the Go 1.27 stdlib ([#612](https://github.com/constructorfleet/forge/issues/612)) ([b59c37c](https://github.com/constructorfleet/forge/commit/b59c37c79673e35387b46c6bbe7263e723b0c06a)), closes [#461](https://github.com/constructorfleet/forge/issues/461)
* owner_pid and owner_token are never cleared on normal worker exit ([#617](https://github.com/constructorfleet/forge/issues/617)) ([e4987e0](https://github.com/constructorfleet/forge/commit/e4987e0cca40c186b8d56057f96b725d31f44094)), closes [#560](https://github.com/constructorfleet/forge/issues/560)
* Same-second pid reuse can produce an identical owner_token on macOS ([#618](https://github.com/constructorfleet/forge/issues/618)) ([485583e](https://github.com/constructorfleet/forge/commit/485583e4640fc74dca08534c1d2ddf7db137f2c3)), closes [#561](https://github.com/constructorfleet/forge/issues/561)
* Wire internal/planningapprove.Approver into a live planning-phase TUI command ([#616](https://github.com/constructorfleet/forge/issues/616)) ([6d8fb40](https://github.com/constructorfleet/forge/commit/6d8fb405447d44c35c23d9811d2274d4f66cb6ad)), closes [#606](https://github.com/constructorfleet/forge/issues/606)


### Bug Fixes

* TUI wrapWidth does one hard rune-count wrap, not a real terminal-column wrap ([#619](https://github.com/constructorfleet/forge/issues/619)) ([5d5c3bd](https://github.com/constructorfleet/forge/commit/5d5c3bd791c805634dc6e601e3f36bc8b84b1fe1)), closes [#602](https://github.com/constructorfleet/forge/issues/602)

## [0.40.0](https://github.com/constructorfleet/forge/compare/forge-v0.39.0...forge-v0.40.0) (2026-09-05)


### Features

* colorize the TUI transcript and frame ([#609](https://github.com/constructorfleet/forge/issues/609)) ([4cb00d3](https://github.com/constructorfleet/forge/commit/4cb00d321248ddef25c0fcd8e5192a164d085001))
* Implement a production PlanningApprover ([#607](https://github.com/constructorfleet/forge/issues/607)) ([3a70c9f](https://github.com/constructorfleet/forge/commit/3a70c9fb57fa3081aad131a64f302deca3c4d242)), closes [#593](https://github.com/constructorfleet/forge/issues/593)
* PR body is duplicated ([#605](https://github.com/constructorfleet/forge/issues/605)) ([8d0c10b](https://github.com/constructorfleet/forge/commit/8d0c10bb8d7b2e4ab7f2f4b37e6a03bf1c85ef44)), closes [#583](https://github.com/constructorfleet/forge/issues/583)

## [0.39.0](https://github.com/constructorfleet/forge/compare/forge-v0.38.0...forge-v0.39.0) (2026-09-05)


### Features

* TUI: Parallel execution is hard to follow ([#598](https://github.com/constructorfleet/forge/issues/598)) ([a53a46b](https://github.com/constructorfleet/forge/commit/a53a46bdb8710fa1597aaf9b931a454912af9796)), closes [#580](https://github.com/constructorfleet/forge/issues/580)
* TUI: the live view polls only the selected Worker's transcript, so a selection change discards the previous pane ([#600](https://github.com/constructorfleet/forge/issues/600)) ([336538b](https://github.com/constructorfleet/forge/commit/336538b23271d077e9904d8ea60d607fb7a85b45)), closes [#566](https://github.com/constructorfleet/forge/issues/566)
* TUI: the transcript height ignores wrapped and multi-line entries ([#603](https://github.com/constructorfleet/forge/issues/603)) ([735515b](https://github.com/constructorfleet/forge/commit/735515b3f5f79201e0c6df94e0c842b5d73f2777)), closes [#571](https://github.com/constructorfleet/forge/issues/571)

## [0.38.0](https://github.com/constructorfleet/forge/compare/forge-v0.37.1...forge-v0.38.0) (2026-09-05)


### Features

* TUI: answer NEEDS_INFO / Decision (EDITOR) ([#590](https://github.com/constructorfleet/forge/issues/590)) ([d223ea4](https://github.com/constructorfleet/forge/commit/d223ea422798e3ec43910e099ba451326403b29b)), closes [#505](https://github.com/constructorfleet/forge/issues/505)
* TUI: Colorization and layout ([#597](https://github.com/constructorfleet/forge/issues/597)) ([6e93dff](https://github.com/constructorfleet/forge/commit/6e93dffcba91cd9c764b4b5cf2fda5d1823fb863)), closes [#581](https://github.com/constructorfleet/forge/issues/581)
* TUI: planning-phase view ([#594](https://github.com/constructorfleet/forge/issues/594)) ([86fb4d0](https://github.com/constructorfleet/forge/commit/86fb4d0bf9cbd4ceb7abbe8a5def432c53c02820)), closes [#506](https://github.com/constructorfleet/forge/issues/506)
* TUI: Sequential executions only first is shown ([#595](https://github.com/constructorfleet/forge/issues/595)) ([b7f7bac](https://github.com/constructorfleet/forge/commit/b7f7bac84007fe64be353c38fa00aeaf77028029)), closes [#585](https://github.com/constructorfleet/forge/issues/585)

## [0.37.1](https://github.com/constructorfleet/forge/compare/forge-v0.37.0...forge-v0.37.1) (2026-09-05)


### Bug Fixes

* Planning Decision resume filters answers by comment author, silently dropping any answer posted as Forge's own account ([#588](https://github.com/constructorfleet/forge/issues/588)) ([aa6c9f4](https://github.com/constructorfleet/forge/commit/aa6c9f45fadabf79054005ea97dfff05175b548b)), closes [#476](https://github.com/constructorfleet/forge/issues/476)

## [0.37.0](https://github.com/constructorfleet/forge/compare/forge-v0.36.3...forge-v0.37.0) (2026-09-05)


### Features

* planning_executions covers only the wayfinding stage; the row reads COMPLETE while four stages still run ([#582](https://github.com/constructorfleet/forge/issues/582)) ([6aca8b2](https://github.com/constructorfleet/forge/commit/6aca8b2eee0cc67e83500bf67911a5dffde665c7)), closes [#470](https://github.com/constructorfleet/forge/issues/470)
* refreshRetryBase failures other than rebase conflict leave no trace in the store ([#577](https://github.com/constructorfleet/forge/issues/577)) ([9ab3286](https://github.com/constructorfleet/forge/commit/9ab32862d8ee01b34d8f8df4d0c6d056ca823534)), closes [#458](https://github.com/constructorfleet/forge/issues/458)
* TUI approve control (PAGER); fix stacked PR creation after prerequisite merge ([#504](https://github.com/constructorfleet/forge/issues/504)) ([#587](https://github.com/constructorfleet/forge/issues/587)) ([5e0ad56](https://github.com/constructorfleet/forge/commit/5e0ad56256956821cd9bb90a9c845292ea7cbd64))
* TUI: cancel control ([#584](https://github.com/constructorfleet/forge/issues/584)) ([dbc777a](https://github.com/constructorfleet/forge/commit/dbc777ab0d689f84337b889f9fb300859dda80ca)), closes [#502](https://github.com/constructorfleet/forge/issues/502)
* TUI: retry control (detached child) ([#586](https://github.com/constructorfleet/forge/issues/586)) ([3c77a56](https://github.com/constructorfleet/forge/commit/3c77a56da51a2d67ee0460faa8adc257343e6db4)), closes [#503](https://github.com/constructorfleet/forge/issues/503)


### Bug Fixes

* forge retry silently creates an empty DB and defaults its config when run outside the repo root ([#578](https://github.com/constructorfleet/forge/issues/578)) ([5116094](https://github.com/constructorfleet/forge/commit/5116094871b9c37e5b2b2d65bf1cbe8dbb9838e3)), closes [#459](https://github.com/constructorfleet/forge/issues/459)

## [0.36.3](https://github.com/constructorfleet/forge/compare/forge-v0.36.2...forge-v0.36.3) (2026-09-04)


### Bug Fixes

* verify worker owner identity before cancel signals it ([#457](https://github.com/constructorfleet/forge/issues/457)) ([#569](https://github.com/constructorfleet/forge/issues/569)) ([c5c6e01](https://github.com/constructorfleet/forge/commit/c5c6e01924e580d31fc62e7f97d7533f07ec3077))

## [0.36.2](https://github.com/constructorfleet/forge/compare/forge-v0.36.1...forge-v0.36.2) (2026-09-04)


### Bug Fixes

* make retry one atomic claim ([#456](https://github.com/constructorfleet/forge/issues/456)) ([#559](https://github.com/constructorfleet/forge/issues/559)) ([1157baa](https://github.com/constructorfleet/forge/commit/1157baa90e1a4e359fcb6ac130727e7e6a2bc16f))

## [0.36.1](https://github.com/constructorfleet/forge/compare/forge-v0.36.0...forge-v0.36.1) (2026-09-04)


### Bug Fixes

* Canceled-before-output runs persist a blank transcript (sink holds the cancelable run ctx) ([#545](https://github.com/constructorfleet/forge/issues/545)) ([8f3237d](https://github.com/constructorfleet/forge/commit/8f3237d8a4e8f7cea5efc8dc045f00a680d190b2)), closes [#454](https://github.com/constructorfleet/forge/issues/454)

## [0.36.0](https://github.com/constructorfleet/forge/compare/forge-v0.35.0...forge-v0.36.0) (2026-09-04)


### Features

* **tui:** label review axes inline and defer diffs to $PAGER ([#501](https://github.com/constructorfleet/forge/issues/501)) ([#541](https://github.com/constructorfleet/forge/issues/541)) ([dec818a](https://github.com/constructorfleet/forge/commit/dec818a18a67df168734062eb0543378e760ee81))

## [0.35.0](https://github.com/constructorfleet/forge/compare/forge-v0.34.0...forge-v0.35.0) (2026-09-04)


### Features

* **tui:** render quality-gate runs as synthetic timeline rows ([#499](https://github.com/constructorfleet/forge/issues/499)) ([#534](https://github.com/constructorfleet/forge/issues/534)) ([18c6743](https://github.com/constructorfleet/forge/commit/18c6743ec16e2d0acec64ec6fc6f3046b138e7bf))
* **tui:** show attempt dividers in the transcript scrollback ([#500](https://github.com/constructorfleet/forge/issues/500)) ([#535](https://github.com/constructorfleet/forge/issues/535)) ([a5379bc](https://github.com/constructorfleet/forge/commit/a5379bc3a55c73d1126d4b263fed5b66e39eb5e6))

## [0.34.0](https://github.com/constructorfleet/forge/compare/forge-v0.33.0...forge-v0.34.0) (2026-09-04)


### Features

* **tui:** add live transcript tailing pipeline ([#497](https://github.com/constructorfleet/forge/issues/497)) ([#516](https://github.com/constructorfleet/forge/issues/516)) ([4e2ca18](https://github.com/constructorfleet/forge/commit/4e2ca1858930d9e4089711a523c99a7955a5a6e0))
* **tui:** transcript pane collapse/expand + selection ([#498](https://github.com/constructorfleet/forge/issues/498)) ([#526](https://github.com/constructorfleet/forge/issues/526)) ([a60177d](https://github.com/constructorfleet/forge/commit/a60177d6096c6c1f352d8852f2986351f942158e))

## [0.33.0](https://github.com/constructorfleet/forge/compare/forge-v0.32.0...forge-v0.33.0) (2026-09-04)


### Features

* **tui:** add watch/execute live roster entrypoint ([#496](https://github.com/constructorfleet/forge/issues/496)) ([#514](https://github.com/constructorfleet/forge/issues/514)) ([d1517ac](https://github.com/constructorfleet/forge/commit/d1517acb1a950b7c05d6891c33c5bf0a6634c0db))

## [0.32.0](https://github.com/constructorfleet/forge/compare/forge-v0.31.0...forge-v0.32.0) (2026-09-04)


### Features

* **storage:** add Worker last_heartbeat and Issue state_changed_at columns ([#494](https://github.com/constructorfleet/forge/issues/494)) ([#511](https://github.com/constructorfleet/forge/issues/511)) ([042a5c0](https://github.com/constructorfleet/forge/commit/042a5c05fa2b3e27f0b8b614cee5b7fdf695ca0b))
* **tui:** add headless roster frame renderer ([#495](https://github.com/constructorfleet/forge/issues/495)) ([#513](https://github.com/constructorfleet/forge/issues/513)) ([c349a5c](https://github.com/constructorfleet/forge/commit/c349a5c76d22696fb41326f6603c9d19e54fe7a3))

## [0.31.0](https://github.com/constructorfleet/forge/compare/forge-v0.30.1...forge-v0.31.0) (2026-09-04)


### Features

* **domain:** add canonical IssueState coarse grouping ([#490](https://github.com/constructorfleet/forge/issues/490)) ([#508](https://github.com/constructorfleet/forge/issues/508)) ([e1f0449](https://github.com/constructorfleet/forge/commit/e1f0449c320ab1ac1beab70ddacbfffe21db1fae))


### Bug Fixes

* Agent timeout ([#48](https://github.com/constructorfleet/forge/issues/48)) is wired to the claude adapter only — codex/opencode/pi/openai can hang forever ([#469](https://github.com/constructorfleet/forge/issues/469)) ([71f3c95](https://github.com/constructorfleet/forge/commit/71f3c95ca94dd630b49b8e9ef1279e3f2e703f85)), closes [#455](https://github.com/constructorfleet/forge/issues/455)

## [0.30.1](https://github.com/constructorfleet/forge/compare/forge-v0.30.0...forge-v0.30.1) (2026-09-03)


### Bug Fixes

* sync GitLab native dependency links ([#440](https://github.com/constructorfleet/forge/issues/440)) ([dc34e7c](https://github.com/constructorfleet/forge/commit/dc34e7c3ffa55c8fb843040e655cb61fd65ab95b))

## [0.30.0](https://github.com/constructorfleet/forge/compare/forge-v0.29.4...forge-v0.30.0) (2026-09-03)


### Features

* add GitLab SCM and CI adapter ([#435](https://github.com/constructorfleet/forge/issues/435)) ([9d790b8](https://github.com/constructorfleet/forge/commit/9d790b8b2ed866fb728373f836528c47726d00f8))
* add remote worker pool placement ([#438](https://github.com/constructorfleet/forge/issues/438)) ([cbbefeb](https://github.com/constructorfleet/forge/commit/cbbefebdb7096c6803f5e91f3907a71688423aa4)), closes [#292](https://github.com/constructorfleet/forge/issues/292)


### Bug Fixes

* handle provider limits from CLI adapters ([#439](https://github.com/constructorfleet/forge/issues/439)) ([06b3923](https://github.com/constructorfleet/forge/commit/06b39238145e2bea978b0b4ca2f5b3c3bb82875e))

## [0.29.4](https://github.com/constructorfleet/forge/compare/forge-v0.29.3...forge-v0.29.4) (2026-09-02)


### Bug Fixes

* Phase 3 · Workstream A: GitLab Tracker adapter + DependencyStore (+ shared GitLab client/auth) ([#433](https://github.com/constructorfleet/forge/issues/433)) ([b7ea793](https://github.com/constructorfleet/forge/commit/b7ea793043cf96c711c555ec9288d3aa821be253)), closes [#289](https://github.com/constructorfleet/forge/issues/289)

## [0.29.3](https://github.com/constructorfleet/forge/compare/forge-v0.29.2...forge-v0.29.3) (2026-09-02)


### Bug Fixes

* Introduce a dedicated PROVIDER_LIMIT IssueState with automatic bounded backoff retry ([#430](https://github.com/constructorfleet/forge/issues/430)) ([b6d83f8](https://github.com/constructorfleet/forge/commit/b6d83f8717b8df0fc7cab28a75c100839854fd00)), closes [#423](https://github.com/constructorfleet/forge/issues/423)

## [0.29.2](https://github.com/constructorfleet/forge/compare/forge-v0.29.1...forge-v0.29.2) (2026-09-02)


### Bug Fixes

* RepairCIFailure's runRepairLoop error path skips failOut ([#426](https://github.com/constructorfleet/forge/issues/426)) ([05ca141](https://github.com/constructorfleet/forge/commit/05ca141be0ac784f606c961ce311a64f387b8940)), closes [#420](https://github.com/constructorfleet/forge/issues/420)

## [0.29.1](https://github.com/constructorfleet/forge/compare/forge-v0.29.0...forge-v0.29.1) (2026-09-02)


### Bug Fixes

* Detect non-convergent review findings and escalate without burning the full retry budget ([#424](https://github.com/constructorfleet/forge/issues/424)) ([ee34e74](https://github.com/constructorfleet/forge/commit/ee34e748c0f4f9e583dcd5663b039d8a9be843bb)), closes [#375](https://github.com/constructorfleet/forge/issues/375)

## [0.29.0](https://github.com/constructorfleet/forge/compare/forge-v0.28.0...forge-v0.29.0) (2026-09-02)


### Features

* Quality-gate command execution swallows container Exec errors instead of routing to failOut ([#421](https://github.com/constructorfleet/forge/issues/421)) ([cd98b14](https://github.com/constructorfleet/forge/commit/cd98b14a215e5cb0b99c5af10f4435d2c54c048e)), closes [#391](https://github.com/constructorfleet/forge/issues/391)

## [0.28.0](https://github.com/constructorfleet/forge/compare/forge-v0.27.0...forge-v0.28.0) (2026-09-01)


### Features

* wire ExecutionLease claiming/heartbeating into the Remote backend ([#414](https://github.com/constructorfleet/forge/issues/414)) ([63c3097](https://github.com/constructorfleet/forge/commit/63c309773dc6d0143a4b0d968763e36fed35b799)), closes [#405](https://github.com/constructorfleet/forge/issues/405)


### Bug Fixes

* **scheduler:** prevent false unsatisfiable-dependency stall on CI-watched prerequisite ([#418](https://github.com/constructorfleet/forge/issues/418)) ([a9dfd07](https://github.com/constructorfleet/forge/commit/a9dfd0778d5e0e1271d5608c44305435316bdcf7))

## [0.27.0](https://github.com/constructorfleet/forge/compare/forge-v0.26.0...forge-v0.27.0) (2026-09-01)


### Features

* Conflict resolution always goes to NEEDS_INFO ([#412](https://github.com/constructorfleet/forge/issues/412)) ([42aa75e](https://github.com/constructorfleet/forge/commit/42aa75e268f3278c29b27967ad390e07ef5b621b))

## [0.26.0](https://github.com/constructorfleet/forge/compare/forge-v0.25.0...forge-v0.26.0) (2026-09-01)


### Features

* Wire LOST-detection into a periodic/scheduling loop ([#406](https://github.com/constructorfleet/forge/issues/406)) ([aef784c](https://github.com/constructorfleet/forge/commit/aef784cab7033312c6c12db0f9574944df0e4bd9)), closes [#400](https://github.com/constructorfleet/forge/issues/400)
* Wire RecoverLostExecution into an actual controller reconciliation loop ([#409](https://github.com/constructorfleet/forge/issues/409)) ([78c5378](https://github.com/constructorfleet/forge/commit/78c53788a56a67fb6f75892b8076703c7886a7a9)), closes [#398](https://github.com/constructorfleet/forge/issues/398)

## [0.25.0](https://github.com/constructorfleet/forge/compare/forge-v0.24.0...forge-v0.25.0) (2026-09-01)


### Features

* Remote backend · 6: failure vs. loss distinction ([#402](https://github.com/constructorfleet/forge/issues/402)) ([fcb230d](https://github.com/constructorfleet/forge/commit/fcb230d533a73c6ce7819479f6f4bfb8b171c306)), closes [#344](https://github.com/constructorfleet/forge/issues/344)

## [0.24.0](https://github.com/constructorfleet/forge/compare/forge-v0.23.0...forge-v0.24.0) (2026-09-01)


### Features

* Remote backend · 2: clone-in / bundle-out publication transport + credential boundary ([#396](https://github.com/constructorfleet/forge/issues/396)) ([bbf073c](https://github.com/constructorfleet/forge/commit/bbf073c00c0b6d9e3b222c77a83b71490774eda2)), closes [#340](https://github.com/constructorfleet/forge/issues/340)

## [0.23.0](https://github.com/constructorfleet/forge/compare/forge-v0.22.0...forge-v0.23.0) (2026-09-01)


### Features

* Remote backend · 3: ExecutionLease storage + heartbeat + execution-placement record ([#394](https://github.com/constructorfleet/forge/issues/394)) ([a772e20](https://github.com/constructorfleet/forge/commit/a772e201ccf9312a6dbeffe101b3ef205937b844)), closes [#341](https://github.com/constructorfleet/forge/issues/341)
* Remote backend · 5: config, wiring & preflight for backend: remote ([#393](https://github.com/constructorfleet/forge/issues/393)) ([2090fe1](https://github.com/constructorfleet/forge/commit/2090fe13949053f1edc81754d80e90f811c051f1)), closes [#343](https://github.com/constructorfleet/forge/issues/343)

## [0.22.0](https://github.com/constructorfleet/forge/compare/forge-v0.21.0...forge-v0.22.0) (2026-09-01)


### Features

* Container backend · 2: in-container Execute + Agent + RepoContext, credential boundary ([#388](https://github.com/constructorfleet/forge/issues/388)) ([c7e5472](https://github.com/constructorfleet/forge/commit/c7e5472a8be1b6a2b24ede694c7d45f96e93b54d)), closes [#335](https://github.com/constructorfleet/forge/issues/335)

## [0.21.0](https://github.com/constructorfleet/forge/compare/forge-v0.20.0...forge-v0.21.0) (2026-09-01)


### Features

* Container backend · 3: config, wiring & preflight for backend: container ([#386](https://github.com/constructorfleet/forge/issues/386)) ([41a8a62](https://github.com/constructorfleet/forge/commit/41a8a6282ffcaa71fd0d8e5af757a6a590679936)), closes [#336](https://github.com/constructorfleet/forge/issues/336)

## [0.20.0](https://github.com/constructorfleet/forge/compare/forge-v0.19.4...forge-v0.20.0) (2026-09-01)


### Features

* Remote backend · 1: WorkerClient seam + fake worker + Remote backend happy path ([#382](https://github.com/constructorfleet/forge/issues/382)) ([c63fff6](https://github.com/constructorfleet/forge/commit/c63fff6165891d01343c7119cb95dd8de34d7935)), closes [#339](https://github.com/constructorfleet/forge/issues/339)

## [0.19.4](https://github.com/constructorfleet/forge/compare/forge-v0.19.3...forge-v0.19.4) (2026-09-01)


### Bug Fixes

* docs/adr: fix duplicate ADR number 0012 (two files share the same number) ([#379](https://github.com/constructorfleet/forge/issues/379)) ([621b549](https://github.com/constructorfleet/forge/commit/621b549ad6558b11fccc92a9c16417c0d7911330)), closes [#361](https://github.com/constructorfleet/forge/issues/361)

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
