# Changelog

## [0.41.1](https://github.com/brettmostert/agent-command/compare/v0.41.0...v0.41.1) (2026-11-07)


### Bug Fixes

* offer to resume a session whose host has gone ([#169](https://github.com/brettmostert/agent-command/issues/169)) ([819458e](https://github.com/brettmostert/agent-command/commit/819458e8e3887bbf61e27fc19d22391e488f9edd))

## [0.41.0](https://github.com/brettmostert/agent-command/compare/v0.40.0...v0.41.0) (2026-11-07)


### Features

* keep the app running when its window closes ([#167](https://github.com/brettmostert/agent-command/issues/167)) ([4917472](https://github.com/brettmostert/agent-command/commit/4917472de49c7c236b1f7cc3a457292512ee9578))

## [0.40.0](https://github.com/brettmostert/agent-command/compare/v0.39.0...v0.40.0) (2026-11-07)


### Features

* read messages aloud, and rework the popup and Dock ([#165](https://github.com/brettmostert/agent-command/issues/165)) ([d55368f](https://github.com/brettmostert/agent-command/commit/d55368fba6a0ed5514522fdb054c7744ae83bbda))

## [0.39.0](https://github.com/brettmostert/agent-command/compare/v0.38.0...v0.39.0) (2026-11-07)


### Features

* add a reply popup for what your agents send you ([#163](https://github.com/brettmostert/agent-command/issues/163)) ([62cf019](https://github.com/brettmostert/agent-command/commit/62cf019c6ebc4bb123064f5b9b17d59aff5f9430))

## [0.38.0](https://github.com/brettmostert/agent-command/compare/v0.37.2...v0.38.0) (2026-11-06)


### Features

* add an Inbox for what your agents send you ([#161](https://github.com/brettmostert/agent-command/issues/161)) ([e3a8127](https://github.com/brettmostert/agent-command/commit/e3a81273854a116589d10fdef75655c45fa9a738))

## [0.37.2](https://github.com/brettmostert/agent-command/compare/v0.37.1...v0.37.2) (2026-11-06)


### Bug Fixes

* warn when notifications are off and notify after 10s turns ([#159](https://github.com/brettmostert/agent-command/issues/159)) ([a08f3de](https://github.com/brettmostert/agent-command/commit/a08f3ded2615a1c77fdfa398650aa26fe59bf762))

## [0.37.1](https://github.com/brettmostert/agent-command/compare/v0.37.0...v0.37.1) (2026-11-06)


### Bug Fixes

* clear the unseen dot when you look at a session ([#156](https://github.com/brettmostert/agent-command/issues/156)) ([c5f53de](https://github.com/brettmostert/agent-command/commit/c5f53decf6dc1e36bfff14d5efdb94a77f2b17be))
* keep status updating after /clear and remove v1's old rows ([#158](https://github.com/brettmostert/agent-command/issues/158)) ([f5f90b4](https://github.com/brettmostert/agent-command/commit/f5f90b47e8fe79bc188ced88e155461cfb100ada))

## [0.37.0](https://github.com/brettmostert/agent-command/compare/v0.36.0...v0.37.0) (2026-11-06)


### Features

* send macOS notifications that open the session (PAN-35) ([#152](https://github.com/brettmostert/agent-command/issues/152)) ([0b5bc15](https://github.com/brettmostert/agent-command/commit/0b5bc1569994237490e865cc1c43cf81bfe63d4f))


### Bug Fixes

* stop a new daemon exiting while an old one shuts down ([#154](https://github.com/brettmostert/agent-command/issues/154)) ([46a5ac6](https://github.com/brettmostert/agent-command/commit/46a5ac681aaa81b17d82269b35fb192c6f53dffe))

## [0.36.0](https://github.com/brettmostert/agent-command/compare/v0.35.0...v0.36.0) (2026-11-06)


### Features

* rename sessions from a popup (PAN-31) ([#148](https://github.com/brettmostert/agent-command/issues/148)) ([12011d7](https://github.com/brettmostert/agent-command/commit/12011d7f644bfce55d1a34aabbc7fa1f78b5cd68))


### Bug Fixes

* stop an uninstall's ended sessions restarting the daemon ([#150](https://github.com/brettmostert/agent-command/issues/150)) ([f4a4272](https://github.com/brettmostert/agent-command/commit/f4a4272a961f9c0d31630a09dc2d308701c90bb7))

## [0.35.0](https://github.com/brettmostert/agent-command/compare/v0.34.0...v0.35.0) (2026-11-06)


### Features

* add a status bar with the version ([#146](https://github.com/brettmostert/agent-command/issues/146)) ([5d83928](https://github.com/brettmostert/agent-command/commit/5d8392833386e508990c9013ddaeb61c136e6bb0))


### Bug Fixes

* treat the acmd link and the app's binary as the same path ([#145](https://github.com/brettmostert/agent-command/issues/145)) ([6d5fa20](https://github.com/brettmostert/agent-command/commit/6d5fa20cb7a29de5b421658e617d5e1713b23ecc))

## [0.34.0](https://github.com/brettmostert/agent-command/compare/v0.33.0...v0.34.0) (2026-11-06)


### ⚠ BREAKING CHANGES

* the formula becomes a cask, and acmd no longer integrates with Kitty or Ghostty. Run `acmd uninstall` from the old version first, then `brew uninstall agent-command`.

### Features

* replace v1 with the native app (PAN-30) ([#143](https://github.com/brettmostert/agent-command/issues/143)) ([803eb36](https://github.com/brettmostert/agent-command/commit/803eb36d621457c29890b21b695c124c0cbea09f))

## [0.33.0](https://github.com/brettmostert/agent-command/compare/v0.32.1...v0.33.0) (2026-11-02)


### Features

* **keys:** group the shortcut sheet into subsections (PAN-19) ([#121](https://github.com/brettmostert/agent-command/issues/121)) ([0acb166](https://github.com/brettmostert/agent-command/commit/0acb166b6c1bc6eec5b0c6a982ead1af15eb42ee))

## [0.32.1](https://github.com/brettmostert/agent-command/compare/v0.32.0...v0.32.1) (2026-11-02)


### Bug Fixes

* **sidebar:** a click selects a session; Enter shows it ([#120](https://github.com/brettmostert/agent-command/issues/120)) ([6ae8374](https://github.com/brettmostert/agent-command/commit/6ae83744406204edd8f31d0c12704eb67361a368))

## [0.32.0](https://github.com/brettmostert/agent-command/compare/v0.31.1...v0.32.0) (2026-11-02)


### ⚠ BREAKING CHANGES

* **speech:** remove the Python speech engine; install and uninstall clean up old Python setups ([#118](https://github.com/brettmostert/agent-command/issues/118))

### Features

* **speech:** remove the Python speech engine; install and uninstall clean up old Python setups ([#118](https://github.com/brettmostert/agent-command/issues/118)) ([78fc1d5](https://github.com/brettmostert/agent-command/commit/78fc1d5532256d6193be9af971dabcf50af4d636))

## [0.31.1](https://github.com/brettmostert/agent-command/compare/v0.31.0...v0.31.1) (2026-11-02)


### Bug Fixes

* **view:** deflake TestFailedAttachResumesOnce ([#116](https://github.com/brettmostert/agent-command/issues/116)) ([050c0ea](https://github.com/brettmostert/agent-command/commit/050c0eaf96b026bafcba72c330f9cbaa65027c4e))

## [0.31.0](https://github.com/brettmostert/agent-command/compare/v0.30.1...v0.31.0) (2026-11-02)


### Features

* **speech:** add a Swift speech engine (FluidAudio Kokoro on Core ML) beside the Python one ([#114](https://github.com/brettmostert/agent-command/issues/114)) ([d71e480](https://github.com/brettmostert/agent-command/commit/d71e480d6b2d124b28dc8185d74eab8b65234ee7))

## [0.30.1](https://github.com/brettmostert/agent-command/compare/v0.30.0...v0.30.1) (2026-11-01)


### Bug Fixes

* **sidebar:** cache repo roots in folder picker so navigation is instant ([#111](https://github.com/brettmostert/agent-command/issues/111)) ([29344d0](https://github.com/brettmostert/agent-command/commit/29344d053ef5567e101d8b238848d24245ea9ae5))

## [0.30.0](https://github.com/brettmostert/agent-command/compare/v0.29.0...v0.30.0) (2026-11-01)


### Features

* host sessions only; remove claude --bg, attach and /bg adopt ([#108](https://github.com/brettmostert/agent-command/issues/108)) ([2e52547](https://github.com/brettmostert/agent-command/commit/2e525471a85749e9de5817da256ebc6c2be2403a))
* **host:** Harness interface and PTY host for interactive sessions ([#104](https://github.com/brettmostert/agent-command/issues/104)) ([80197b5](https://github.com/brettmostert/agent-command/commit/80197b575cee8579a71355cbc4dae5b6a35166cb))
* **host:** new sessions run hosted; views attach through the host socket ([#106](https://github.com/brettmostert/agent-command/issues/106)) ([b01c6e6](https://github.com/brettmostert/agent-command/commit/b01c6e66397b0d6496e353d41e50a49da5fb9b84))
* **host:** pause hosted sessions whose host is gone; sweep stale host files ([#107](https://github.com/brettmostert/agent-command/issues/107)) ([fced19a](https://github.com/brettmostert/agent-command/commit/fced19a9483f5fbb48b805a30fc4b1e82e2b12c8))

## [0.29.0](https://github.com/brettmostert/agent-command/compare/v0.28.1...v0.29.0) (2026-11-01)


### Features

* **sidebar:** folder picker for new sessions that never starts in a worktree ([#102](https://github.com/brettmostert/agent-command/issues/102)) ([2f8e59e](https://github.com/brettmostert/agent-command/commit/2f8e59e7c9fd7918020da401a04e29988a8f0408))

## [0.28.1](https://github.com/brettmostert/agent-command/compare/v0.28.0...v0.28.1) (2026-11-01)


### Bug Fixes

* **popup:** draw code blocks as one box, not a strip per line ([#100](https://github.com/brettmostert/agent-command/issues/100)) ([f745614](https://github.com/brettmostert/agent-command/commit/f745614246415777e1b5be333854d28bf86fc35a))

## [0.28.0](https://github.com/brettmostert/agent-command/compare/v0.27.1...v0.28.0) (2026-11-01)


### Features

* **popup:** reopen hotkey is cmd+ctrl+o and opens on an empty queue (PAN-12) ([#98](https://github.com/brettmostert/agent-command/issues/98)) ([bbe472f](https://github.com/brettmostert/agent-command/commit/bbe472f9fc31a096a773072cddb12efe4d6b00c0))

## [0.27.1](https://github.com/brettmostert/agent-command/compare/v0.27.0...v0.27.1) (2026-11-01)


### Bug Fixes

* **popup:** keep the open item selected when the queue updates ([#96](https://github.com/brettmostert/agent-command/issues/96)) ([a4845b8](https://github.com/brettmostert/agent-command/commit/a4845b87085b083a3267e7e1a7bb99810e8c1fd9))

## [0.27.0](https://github.com/brettmostert/agent-command/compare/v0.26.0...v0.27.0) (2026-11-01)


### Features

* **claudecli:** enable remote control on all launched sessions ([#93](https://github.com/brettmostert/agent-command/issues/93)) ([d4d0469](https://github.com/brettmostert/agent-command/commit/d4d0469de1aab833aba8de0de1b97d5bb4d113fe))


### Bug Fixes

* **cli:** stop TestKeysCommand flaking on a loaded machine ([#92](https://github.com/brettmostert/agent-command/issues/92)) ([5401819](https://github.com/brettmostert/agent-command/commit/540181990b53ae27e0922fa54e1a31e698e82d0f))

## [0.26.0](https://github.com/brettmostert/agent-command/compare/v0.25.3...v0.26.0) (2026-11-01)


### Features

* **popup:** read the current session's latest response aloud with cmd+ctrl+p ([#90](https://github.com/brettmostert/agent-command/issues/90)) ([40e9e01](https://github.com/brettmostert/agent-command/commit/40e9e014f94f22f07483fa1e7923065bf8bcbb08))

## [0.25.3](https://github.com/brettmostert/agent-command/compare/v0.25.2...v0.25.3) (2026-11-01)


### Bug Fixes

* **adopt:** link a move-in listed before its hook; close the pane it left ([#88](https://github.com/brettmostert/agent-command/issues/88)) ([f6fc644](https://github.com/brettmostert/agent-command/commit/f6fc6440a40e2847c9ca876468c9309003500c92))

## [0.25.2](https://github.com/brettmostert/agent-command/compare/v0.25.1...v0.25.2) (2026-11-01)


### Bug Fixes

* send popup replies, keep names on move-in, point the empty view at the sidebar ([#84](https://github.com/brettmostert/agent-command/issues/84)) ([d6935b5](https://github.com/brettmostert/agent-command/commit/d6935b52ff0e88f04e2ab8eb12e9b4357ab7c5aa))
* **view:** reset the terminal when attach ends and the view closes ([#87](https://github.com/brettmostert/agent-command/issues/87)) ([d1b8c04](https://github.com/brettmostert/agent-command/commit/d1b8c04b18e8872c7c6a3514ff03cd0739f80435))

## [0.25.1](https://github.com/brettmostert/agent-command/compare/v0.25.0...v0.25.1) (2026-11-01)


### Bug Fixes

* **popup:** empty the reply box once a reply is sent ([#82](https://github.com/brettmostert/agent-command/issues/82)) ([84c15af](https://github.com/brettmostert/agent-command/commit/84c15af776899db1cc552766db63ab2b7445ff17))

## [0.25.0](https://github.com/brettmostert/agent-command/compare/v0.24.3...v0.25.0) (2026-11-01)


### Features

* **popup:** no popup for the session in the window you're in ([#81](https://github.com/brettmostert/agent-command/issues/81)) ([abeee41](https://github.com/brettmostert/agent-command/commit/abeee414e1f83b4f64f8c5e3752babeb99e13ae3))


### Bug Fixes

* **popup:** replace itself with the new version after an upgrade ([#79](https://github.com/brettmostert/agent-command/issues/79)) ([88e4097](https://github.com/brettmostert/agent-command/commit/88e40975ec3ef756a076618c9b7f65bcc044b483))

## [0.24.3](https://github.com/brettmostert/agent-command/compare/v0.24.2...v0.24.3) (2026-11-01)


### Bug Fixes

* **daemon:** keep the pane of sessions in your own windows current ([#77](https://github.com/brettmostert/agent-command/issues/77)) ([b957811](https://github.com/brettmostert/agent-command/commit/b957811d0a62562b1486c828e4a4879e36bf54a1))

## [0.24.2](https://github.com/brettmostert/agent-command/compare/v0.24.1...v0.24.2) (2026-11-01)


### Bug Fixes

* **daemon:** stop even when clients keep a connection open ([#75](https://github.com/brettmostert/agent-command/issues/75)) ([421a810](https://github.com/brettmostert/agent-command/commit/421a81039543948405b929f9099c431ac92e8670))

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
