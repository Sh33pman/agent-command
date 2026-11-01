# Changelog

## [0.24.1](https://github.com/brettmostert/agent-command/compare/v0.24.0...v0.24.1) (2026-11-01)


### Bug Fixes

* **popup:** copy and paste in the reply popup ([#73](https://github.com/brettmostert/agent-command/issues/73)) ([71e7e06](https://github.com/brettmostert/agent-command/commit/71e7e065735eff070f3a2c02f636e7c2682ae8b8))

## [0.24.0](https://github.com/brettmostert/agent-command/compare/v0.23.0...v0.24.0) (2026-11-01)


### Features

* **init:** ask about the reply popup, auto-play and speech ([#72](https://github.com/brettmostert/agent-command/issues/72)) ([b981a16](https://github.com/brettmostert/agent-command/commit/b981a169a6c3e454ef987f4079c80791e469251b))
* **popup:** add the native reply panel (spike) ([#66](https://github.com/brettmostert/agent-command/issues/66)) ([413f184](https://github.com/brettmostert/agent-command/commit/413f184a71a0440e5a7ea6a5bae6be11c9d6087d))
* **popup:** keep an inbox of finished turns, questions and permissions ([#70](https://github.com/brettmostert/agent-command/issues/70)) ([ac0199f](https://github.com/brettmostert/agent-command/commit/ac0199ff23a854dae78383ca7eec613c74fc9960))
* **popup:** run the reply popup and answer sessions from it ([#71](https://github.com/brettmostert/agent-command/issues/71)) ([47ac13a](https://github.com/brettmostert/agent-command/commit/47ac13a7d53e4fc765f4a3353a8f9942684a58db))
* **speech:** read text aloud with Kokoro on MLX ([#68](https://github.com/brettmostert/agent-command/issues/68)) ([10a1f53](https://github.com/brettmostert/agent-command/commit/10a1f53d0c034e9b389ba26ecbbfaada8b6e2ff5))
* **speech:** stream audio instead of writing files ([#69](https://github.com/brettmostert/agent-command/issues/69)) ([f133cf2](https://github.com/brettmostert/agent-command/commit/f133cf28e81fb563ee47dba7dc92f5ffcaa245b9))

## [0.23.0](https://github.com/brettmostert/agent-command/compare/v0.22.0...v0.23.0) (2026-11-01)


### Features

* **sessions:** finish sessions, /exit finishes, Claude id in the header ([#64](https://github.com/brettmostert/agent-command/issues/64)) ([75fbf67](https://github.com/brettmostert/agent-command/commit/75fbf677550183659c634b5e8cc6294dc482ee28))

## [0.22.0](https://github.com/brettmostert/agent-command/compare/v0.21.0...v0.22.0) (2026-11-01)


### Features

* **statusbar:** show acmd's version at the far right ([#62](https://github.com/brettmostert/agent-command/issues/62)) ([77a94cd](https://github.com/brettmostert/agent-command/commit/77a94cdcfec62268f62b665223ac8e7d4ace451d))

## [0.21.0](https://github.com/brettmostert/agent-command/compare/v0.20.0...v0.21.0) (2026-11-01)


### Features

* **ghostty:** use the same shortcut combos as Kitty ([#59](https://github.com/brettmostert/agent-command/issues/59)) ([e7822ee](https://github.com/brettmostert/agent-command/commit/e7822ee64ae77ebfe5e9d2009f3a136ee30f9b76))
* **sessions:** add a terminal per session under the Claude pane ([#58](https://github.com/brettmostert/agent-command/issues/58)) ([aa21f5e](https://github.com/brettmostert/agent-command/commit/aa21f5e949cc349c019f0c7243a9961ebf6fc82d))
* **view:** add a header bar above the Claude pane ([#57](https://github.com/brettmostert/agent-command/issues/57)) ([a274836](https://github.com/brettmostert/agent-command/commit/a27483616aeca9d550782573977a5203f4c07f7a))


### Bug Fixes

* **sessions:** wait until a resumed session runs before showing it ([#56](https://github.com/brettmostert/agent-command/issues/56)) ([f2a9a6a](https://github.com/brettmostert/agent-command/commit/f2a9a6aef7e75e9908ff0e20c906cada2cd4d9f5))

## [0.20.0](https://github.com/brettmostert/agent-command/compare/v0.19.0...v0.20.0) (2026-11-01)


### Features

* **sessions:** resume on show, and move a pane's session in with cmd+shift+m ([#54](https://github.com/brettmostert/agent-command/issues/54)) ([0f249a6](https://github.com/brettmostert/agent-command/commit/0f249a6250496257031556341c0cc0bbb076be0f))

## [0.19.0](https://github.com/brettmostert/agent-command/compare/v0.18.0...v0.19.0) (2026-11-01)


### Features

* **kitty:** add a full-width status bar under the sidebar and view ([#52](https://github.com/brettmostert/agent-command/issues/52)) ([5efb58c](https://github.com/brettmostert/agent-command/commit/5efb58ccabce4fb60070c70034edcb631bc8e5ad))

## [0.18.0](https://github.com/brettmostert/agent-command/compare/v0.17.0...v0.18.0) (2026-11-01)


### Features

* **sidebar:** close the view with q ([#50](https://github.com/brettmostert/agent-command/issues/50)) ([e51fab1](https://github.com/brettmostert/agent-command/commit/e51fab12ad53d9921a73677550650d93cf4c2819))

## [0.17.0](https://github.com/brettmostert/agent-command/compare/v0.16.0...v0.17.0) (2026-11-01)


### Features

* **sidebar:** rename with the r key only, not double-click ([#48](https://github.com/brettmostert/agent-command/issues/48)) ([a88bae2](https://github.com/brettmostert/agent-command/commit/a88bae211cf3a0c8db05f12dd6a3cf750d4449cd))
* **sidebar:** show messages in a status bar at the bottom ([#47](https://github.com/brettmostert/agent-command/issues/47)) ([25ebc08](https://github.com/brettmostert/agent-command/commit/25ebc0804beeca41b47714ea59932eb2cdb4fb67))

## [0.16.0](https://github.com/brettmostert/agent-command/compare/v0.15.0...v0.16.0) (2026-11-01)


### Features

* **sessions:** add acmd purge ([#40](https://github.com/brettmostert/agent-command/issues/40)) ([826d31e](https://github.com/brettmostert/agent-command/commit/826d31e03421ef7f567d3000f8d4b897480a51db))

## [0.15.0](https://github.com/brettmostert/agent-command/compare/v0.14.0...v0.15.0) (2026-11-01)


### Features

* **notify:** add macOS notifications ([#39](https://github.com/brettmostert/agent-command/issues/39)) ([40e04a5](https://github.com/brettmostert/agent-command/commit/40e04a5f3d6c68ab076207d62813010399047612))

## [0.14.0](https://github.com/brettmostert/agent-command/compare/v0.13.0...v0.14.0) (2026-11-01)


### Features

* **sessions:** move sessions into acmd and split them out ([#38](https://github.com/brettmostert/agent-command/issues/38)) ([41bf60f](https://github.com/brettmostert/agent-command/commit/41bf60f0994fdadf7abf44d0cd5f7c3a0a8feb5e))

## [0.13.0](https://github.com/brettmostert/agent-command/compare/v0.12.1...v0.13.0) (2026-11-01)


### Features

* **ghostty:** add Ghostty views, setup and doctor checks ([#37](https://github.com/brettmostert/agent-command/issues/37)) ([e1b20f2](https://github.com/brettmostert/agent-command/commit/e1b20f2d8ac97839ee449c602fe561af75318f08))

## [0.12.1](https://github.com/brettmostert/agent-command/compare/v0.12.0...v0.12.1) (2026-11-01)


### Bug Fixes

* **kitty:** stop the sidebar width loop; add daemon stop and restart ([#41](https://github.com/brettmostert/agent-command/issues/41)) ([9453ff6](https://github.com/brettmostert/agent-command/commit/9453ff6307370cd551df341aaa50a6fa2e5eae63))

## [0.12.0](https://github.com/brettmostert/agent-command/compare/v0.11.0...v0.12.0) (2026-10-31)


### Features

* **keys:** add the cheat sheet, session picker and open in editor ([#35](https://github.com/brettmostert/agent-command/issues/35)) ([1bff440](https://github.com/brettmostert/agent-command/commit/1bff44010e4d97922137921d3002aca8185ec2f6))

## [0.11.0](https://github.com/brettmostert/agent-command/compare/v0.10.0...v0.11.0) (2026-10-31)


### Features

* **sidebar:** add the Bubble Tea sidebar ([#33](https://github.com/brettmostert/agent-command/issues/33)) ([de49335](https://github.com/brettmostert/agent-command/commit/de49335c2c14e8270811b3f1a0272fd91dbddddc))

## [0.10.0](https://github.com/brettmostert/agent-command/compare/v0.9.0...v0.10.0) (2026-10-31)


### Features

* **kitty:** add the view's sidebar split and Kitty shortcuts ([#30](https://github.com/brettmostert/agent-command/issues/30)) ([a15bb54](https://github.com/brettmostert/agent-command/commit/a15bb543b4e16af6b7e71bb4a15e5481624c322d))

## [0.9.0](https://github.com/brettmostert/agent-command/compare/v0.8.0...v0.9.0) (2026-10-31)


### Features

* **view:** add the view engine, acmd new, go and resume ([#28](https://github.com/brettmostert/agent-command/issues/28)) ([6cf16c2](https://github.com/brettmostert/agent-command/commit/6cf16c22b0bfcb7567dd8d2293b213fd597e797f))

## [0.8.0](https://github.com/brettmostert/agent-command/compare/v0.7.0...v0.8.0) (2026-10-31)


### Features

* **doctor:** add acmd doctor ([#26](https://github.com/brettmostert/agent-command/issues/26)) ([be50326](https://github.com/brettmostert/agent-command/commit/be503260ff4e7088cf997fd9feb4cba41143c951))

## [0.7.0](https://github.com/brettmostert/agent-command/compare/v0.6.0...v0.7.0) (2026-10-31)


### Features

* **setup:** add acmd init, uninstall and editor commands ([#23](https://github.com/brettmostert/agent-command/issues/23)) ([9411cbf](https://github.com/brettmostert/agent-command/commit/9411cbf3193b0656726f7ee02523e64773d9c32a))

## [0.6.0](https://github.com/brettmostert/agent-command/compare/v0.5.0...v0.6.0) (2026-10-31)


### Features

* **setup:** add config editors for Claude hooks and Kitty ([#20](https://github.com/brettmostert/agent-command/issues/20)) ([08a8b8d](https://github.com/brettmostert/agent-command/commit/08a8b8d96162908f171e7acaa770084710f53b9e))


### Bug Fixes

* **reconcile:** don't pause sessions Claude hasn't listed yet ([#22](https://github.com/brettmostert/agent-command/issues/22)) ([349da43](https://github.com/brettmostert/agent-command/commit/349da435c3f188eb4f8077c41a73edee51b4e5f4))

## [0.5.0](https://github.com/brettmostert/agent-command/compare/v0.4.0...v0.5.0) (2026-10-31)


### Features

* **cli:** add list, rename, archive, move, group and view commands ([47171af](https://github.com/brettmostert/agent-command/commit/47171af956219746535a8e2227426f504fb4af73))
* **reconcile:** add claude CLI wrapper, reconciler and Esc detection ([5c7893c](https://github.com/brettmostert/agent-command/commit/5c7893c2cb6965e55c9b73fe3a4897c69380a47d))

## [0.4.0](https://github.com/brettmostert/agent-command/compare/v0.3.0...v0.4.0) (2026-10-31)


### Features

* **daemon:** add daemon, socket protocol and acmd hook ([5d2e74a](https://github.com/brettmostert/agent-command/commit/5d2e74a3ba41c7e87d47d012d28de4a37ae9c294))

## [0.3.0](https://github.com/brettmostert/agent-command/compare/v0.2.0...v0.3.0) (2026-10-31)


### Features

* **store:** add SQLite state store with migrations ([118a968](https://github.com/brettmostert/agent-command/commit/118a9688f7851318aedc886fb903ab727a28f5a4))

## [0.2.0](https://github.com/brettmostert/agent-command/compare/v0.1.1...v0.2.0) (2026-10-31)


### Features

* **core:** session model and status state machine ([b8b8024](https://github.com/brettmostert/agent-command/commit/b8b8024554bfca4b1f0218fe7eacba1b1a991b05))

## [0.1.1](https://github.com/brettmostert/agent-command/compare/v0.1.0...v0.1.1) (2026-10-30)


### Bug Fixes

* **release:** run goreleaser via make so syft is on PATH ([89b0214](https://github.com/brettmostert/agent-command/commit/89b0214f84e19eb66e4aaa5dd5ef7b2c4f1eebfe))

## 0.1.0 (2026-10-30)


### Features

* bootstrap walking skeleton with CI/CD and Homebrew release ([e1bc9da](https://github.com/brettmostert/agent-command/commit/e1bc9daba4ca10931ab0afda6de2b6407327920d))
