# Changelog

## [0.52.0](https://github.com/brettmostert/agent-command/compare/v0.51.0...v0.52.0) (2026-11-14)


### Features

* a Settings window for every setting, and acmd config ([#30](https://github.com/brettmostert/agent-command/issues/30)) ([1d26977](https://github.com/brettmostert/agent-command/commit/1d26977d533cfb521e9a0df41ff1a1d7af1663b4))

## [0.51.0](https://github.com/brettmostert/agent-command/compare/v0.50.1...v0.51.0) (2026-11-14)


### Features

* add Linux Go CLI, daemon, and CI support ([#19](https://github.com/brettmostert/agent-command/issues/19)) ([ac4a1cb](https://github.com/brettmostert/agent-command/commit/ac4a1cb6be25c660533cef8ee35ad99a08fed6d4))
* **app:** one keymap for every view, and a shortcuts list to change it ([#28](https://github.com/brettmostert/agent-command/issues/28)) ([e52d786](https://github.com/brettmostert/agent-command/commit/e52d7863bace1f8b4a9aa5519c7300b7359aeac7))


### Bug Fixes

* **ci:** build libghostty-vt for a baseline CPU on Linux ([#29](https://github.com/brettmostert/agent-command/issues/29)) ([a0de98d](https://github.com/brettmostert/agent-command/commit/a0de98dbf3bbcf657b8e5eb478d67bc869dfbf8a))
* **linux:** isolate macOS-only env, doctor, editor, and ci target paths ([#27](https://github.com/brettmostert/agent-command/issues/27)) ([eea74a9](https://github.com/brettmostert/agent-command/commit/eea74a97aee2123ca6c94eaac235123ae1abe91f))
* **notify:** notify when long background work finishes (PAN-45) ([#25](https://github.com/brettmostert/agent-command/issues/25)) ([037786f](https://github.com/brettmostert/agent-command/commit/037786f5c0b72735f3f1e93fb7e9f82221faebfa))

## [0.50.1](https://github.com/brettmostert/agent-command/compare/v0.50.0...v0.50.1) (2026-11-13)


### Bug Fixes

* **schedule:** ask for the wake password in a dialog when there's no terminal ([#22](https://github.com/brettmostert/agent-command/issues/22)) ([92ba7e9](https://github.com/brettmostert/agent-command/commit/92ba7e9fb64c394c6be11e317172e8eb52f1262b))

## [0.50.0](https://github.com/brettmostert/agent-command/compare/v0.49.0...v0.50.0) (2026-11-13)


### Features

* **schedule:** wake the Mac for scheduled jobs (PAN-61) ([#20](https://github.com/brettmostert/agent-command/issues/20)) ([e55d0dd](https://github.com/brettmostert/agent-command/commit/e55d0ddfdcf9c987dab213c87180e62f28cc1da9))

## [0.49.0](https://github.com/brettmostert/agent-command/compare/v0.48.0...v0.49.0) (2026-11-12)


### Features

* **cli:** manage scheduled jobs with acmd schedule ([#16](https://github.com/brettmostert/agent-command/issues/16)) ([c56caed](https://github.com/brettmostert/agent-command/commit/c56caedeee28dd491d87feaa8154a1e2485d3c96))

## [0.48.0](https://github.com/brettmostert/agent-command/compare/v0.47.1...v0.48.0) (2026-11-12)


### Features

* **app:** choose a job's schedule instead of typing it, and drop the group field ([#14](https://github.com/brettmostert/agent-command/issues/14)) ([68f3772](https://github.com/brettmostert/agent-command/commit/68f3772768eda5e9cbf2e7a9841741bc4582c0bd))

## [0.47.1](https://github.com/brettmostert/agent-command/compare/v0.47.0...v0.47.1) (2026-11-12)


### Bug Fixes

* **app:** don't end the app when a job is saved or the form closes ([#12](https://github.com/brettmostert/agent-command/issues/12)) ([e2b5eff](https://github.com/brettmostert/agent-command/commit/e2b5eff36e1286db87d9958a47c4d7fe745ccd2e))

## [0.47.0](https://github.com/brettmostert/agent-command/compare/v0.46.1...v0.47.0) (2026-11-12)


### Features

* scheduled jobs, set up and managed in the app (PAN-60 phase 1) ([#10](https://github.com/brettmostert/agent-command/issues/10)) ([9e4c0d8](https://github.com/brettmostert/agent-command/commit/9e4c0d8a8eec9148740994fcbdfae7f4f50cccc5))


### Bug Fixes

* **release:** sign by the certificate's fingerprint, and test signing on every PR ([#9](https://github.com/brettmostert/agent-command/issues/9)) ([5d902fd](https://github.com/brettmostert/agent-command/commit/5d902fd18bca2848ee58b906f35c7cf2e0a223f8))

## [0.46.1](https://github.com/brettmostert/agent-command/compare/v0.46.0...v0.46.1) (2026-11-12)


### Bug Fixes

* **app:** draw claude's mark as the claude code crab ([#6](https://github.com/brettmostert/agent-command/issues/6)) ([f9d5401](https://github.com/brettmostert/agent-command/commit/f9d54011509bc8c28d6bc1c098df0c4cf597832c))
* **deps:** build with go 1.27.4 for GO-2026-6859 in net/http ([#7](https://github.com/brettmostert/agent-command/issues/7)) ([e297044](https://github.com/brettmostert/agent-command/commit/e297044f05fca1e3f1712b66a115f86106870970))

## [0.46.0](https://github.com/brettmostert/agent-command/compare/v0.45.1...v0.46.0) (2026-11-10)


### Features

* **app:** go to a session by number, or to the next that needs you ([81bf654](https://github.com/brettmostert/agent-command/commit/81bf654240fb556708ad9bda5cc2e97569001fdc))

## [0.45.1](https://github.com/brettmostert/agent-command/compare/v0.45.0...v0.45.1) (2026-11-09)


### Bug Fixes

* **packaging:** use the cask's current form for the macos requirement ([d35202e](https://github.com/brettmostert/agent-command/commit/d35202eefc190e0fa5eaa1e1d6d63310234af69e))

## [0.45.0](https://github.com/brettmostert/agent-command/compare/v0.44.1...v0.45.0) (2026-11-09)


### Features

* **app:** mark Claude rows, and sum up what needs you in the status bar (PAN-39) ([a40efe0](https://github.com/brettmostert/agent-command/commit/a40efe07ffb3c9bc40e8e3e7a54e5b01f0a8a970))
* **app:** show a header over each pane with its session's details (PAN-39) ([86ac0c6](https://github.com/brettmostert/agent-command/commit/86ac0c6812b6a45cf4f5f048e5b3a82fadaa3784))

## [0.44.1](https://github.com/brettmostert/agent-command/compare/v0.44.0...v0.44.1) (2026-11-09)


### Bug Fixes

* **app:** open links clicked in a terminal ([d27f0da](https://github.com/brettmostert/agent-command/commit/d27f0da73056e2c97f76108d23b0a5559579c99b))

## [0.44.0](https://github.com/brettmostert/agent-command/compare/v0.43.0...v0.44.0) (2026-11-09)


### Features

* **app:** show messages in the popup and the Inbox with their formatting ([d6f27e1](https://github.com/brettmostert/agent-command/commit/d6f27e115400730cacf75cb0bdd6dc64f278f851))

## [0.43.0](https://github.com/brettmostert/agent-command/compare/v0.42.5...v0.43.0) (2026-11-09)


### Features

* **app:** turn Remote Control on or off from the command palette (PAN-59) ([4352504](https://github.com/brettmostert/agent-command/commit/4352504fef17b59842e0a06a66fc0c20b794eb1f))
* **sessions:** make Remote Control a setting, off by default (PAN-59) ([082c73a](https://github.com/brettmostert/agent-command/commit/082c73ad7be409d3be0d9801505991cb45c3981a))
* **status:** show unknown for an agent in a terminal that sends no status (PAN-29) ([4b0c59b](https://github.com/brettmostert/agent-command/commit/4b0c59b8fca2c64686205e825e7d41103959915d))

## [0.42.5](https://github.com/brettmostert/agent-command/compare/v0.42.4...v0.42.5) (2026-11-09)


### Bug Fixes

* **status:** stop showing waiting after a permission is denied (PAN-29) ([3eeb8cb](https://github.com/brettmostert/agent-command/commit/3eeb8cbb37efa3ade5bc8389955a39b03d2ea6cb))

## [0.42.4](https://github.com/brettmostert/agent-command/compare/v0.42.3...v0.42.4) (2026-11-09)


### Bug Fixes

* **speech:** retry the model download, and resume it where it stopped ([a671e05](https://github.com/brettmostert/agent-command/commit/a671e054de79894a56d6340cc7488bb3e489cc6e))

## [0.42.3](https://github.com/brettmostert/agent-command/compare/v0.42.2...v0.42.3) (2026-11-09)


### Bug Fixes

* **env:** give sessions this login's SSH agent, not the one saved at setup ([1ccee5d](https://github.com/brettmostert/agent-command/commit/1ccee5d23d3ac65400083704edf8cb78bf860875))
* **security:** keep the socket fallback out of /tmp, and old backups off disk ([80844f8](https://github.com/brettmostert/agent-command/commit/80844f8f8040e1a65f645813e6f1b4b046d199e8))

## [0.42.2](https://github.com/brettmostert/agent-command/compare/v0.42.1...v0.42.2) (2026-11-07)


### Bug Fixes

* **deps:** ship the sqlite and x/sys updates ([6643aea](https://github.com/brettmostert/agent-command/commit/6643aeaaa7a0cedd32c66b2f408b9a4fc358c4e1))

## [0.42.1](https://github.com/brettmostert/agent-command/compare/v0.42.0...v0.42.1) (2026-11-07)


### Bug Fixes

* keep the reason when speech setup fails ([6b4f458](https://github.com/brettmostert/agent-command/commit/6b4f458179677b929a8578374fe290c17162e1a4))
* remove the app's temp folder on uninstall ([6563fd6](https://github.com/brettmostert/agent-command/commit/6563fd66d37de44cd4cf8c45830ded590a104b36))
* stop the app crashing when the daemon goes away ([13f04fa](https://github.com/brettmostert/agent-command/commit/13f04fa19f7d9f2fb9fa1ac35ec753573be16c80))

## [0.42.0](https://github.com/brettmostert/agent-command/compare/v0.41.1...v0.42.0) (2026-11-07)


### Features

* report bugs and request features, and keep a clean daemon log ([5fc1d7b](https://github.com/brettmostert/agent-command/commit/5fc1d7b2d641c74cffc9d6df578dd9f7102bb7c2))

## [0.41.1](https://github.com/brettmostert/agent-command/compare/v0.41.0...v0.41.1) (2026-11-07)


### Bug Fixes

* offer to resume a session whose host has gone ([02682e2](https://github.com/brettmostert/agent-command/commit/02682e277939d616ad4629f55e7242279cdfd938))

## [0.41.0](https://github.com/brettmostert/agent-command/compare/v0.40.0...v0.41.0) (2026-11-07)


### Features

* keep the app running when its window closes ([c5b39e2](https://github.com/brettmostert/agent-command/commit/c5b39e250f34fe421c92214c4013d3b6cb9a031a))

## [0.40.0](https://github.com/brettmostert/agent-command/compare/v0.39.0...v0.40.0) (2026-11-07)


### Features

* read messages aloud, and rework the popup and Dock ([58dd948](https://github.com/brettmostert/agent-command/commit/58dd948c7db88316fa0ee35b6b5588de97be6691))

## [0.39.0](https://github.com/brettmostert/agent-command/compare/v0.38.0...v0.39.0) (2026-11-07)


### Features

* add a reply popup for what your agents send you ([123bbb5](https://github.com/brettmostert/agent-command/commit/123bbb5bf6b9fa9605da8ba3cfd9fa39fad83e80))

## [0.38.0](https://github.com/brettmostert/agent-command/compare/v0.37.2...v0.38.0) (2026-11-06)


### Features

* add an Inbox for what your agents send you ([c336c2c](https://github.com/brettmostert/agent-command/commit/c336c2c6192dbeddaeba11a7a18a4f53abaa85f8))

## [0.37.2](https://github.com/brettmostert/agent-command/compare/v0.37.1...v0.37.2) (2026-11-06)


### Bug Fixes

* warn when notifications are off and notify after 10s turns ([cf00600](https://github.com/brettmostert/agent-command/commit/cf00600cd51e17bd2000bc38b6e55876a59957e8))

## [0.37.1](https://github.com/brettmostert/agent-command/compare/v0.37.0...v0.37.1) (2026-11-06)


### Bug Fixes

* clear the unseen dot when you look at a session ([c2d0c80](https://github.com/brettmostert/agent-command/commit/c2d0c804fe2269080b70d908add6acd89e907f8f))
* keep status updating after /clear and remove v1's old rows ([64f98aa](https://github.com/brettmostert/agent-command/commit/64f98aa4921ed62e633a8b896e02dfe2b75f33ee))

## [0.37.0](https://github.com/brettmostert/agent-command/compare/v0.36.0...v0.37.0) (2026-11-06)


### Features

* send macOS notifications that open the session (PAN-35) ([b22bf3c](https://github.com/brettmostert/agent-command/commit/b22bf3ccfba3ea7fd3759266ef9025ac470f02df))


### Bug Fixes

* stop a new daemon exiting while an old one shuts down ([01eef27](https://github.com/brettmostert/agent-command/commit/01eef27e1486b86d4734312a0e9d760b98f8f6cf))

## [0.36.0](https://github.com/brettmostert/agent-command/compare/v0.35.0...v0.36.0) (2026-11-06)


### Features

* rename sessions from a popup (PAN-31) ([ecbcb75](https://github.com/brettmostert/agent-command/commit/ecbcb752799236e39cff3802b668066d371b8057))


### Bug Fixes

* stop an uninstall's ended sessions restarting the daemon ([c725ea6](https://github.com/brettmostert/agent-command/commit/c725ea61631f6abcaf9957c455c276c16989d3a6))

## [0.35.0](https://github.com/brettmostert/agent-command/compare/v0.34.0...v0.35.0) (2026-11-06)


### Features

* add a status bar with the version ([74a14a6](https://github.com/brettmostert/agent-command/commit/74a14a69bb9dbeaf5ff7042d37dec926214a74e9))


### Bug Fixes

* treat the acmd link and the app's binary as the same path ([cbd2d7c](https://github.com/brettmostert/agent-command/commit/cbd2d7c633468888bd5bd31da1a2da97720a852c))

## [0.34.0](https://github.com/brettmostert/agent-command/compare/v0.33.0...v0.34.0) (2026-11-06)


### ⚠ BREAKING CHANGES

* the formula becomes a cask, and acmd no longer integrates with Kitty or Ghostty. Run `acmd uninstall` from the old version first, then `brew uninstall agent-command`.

### Features

* replace v1 with the native app (PAN-30) ([84820e0](https://github.com/brettmostert/agent-command/commit/84820e03c14953b5bc1e5cef9c3bf00913d806df))

## [0.33.0](https://github.com/brettmostert/agent-command/compare/v0.32.1...v0.33.0) (2026-11-02)


### Features

* **keys:** group the shortcut sheet into subsections (PAN-19) ([6e23769](https://github.com/brettmostert/agent-command/commit/6e23769f4b3e7e80f9f228b721a67894a7461b00))

## [0.32.1](https://github.com/brettmostert/agent-command/compare/v0.32.0...v0.32.1) (2026-11-02)


### Bug Fixes

* **sidebar:** a click selects a session; Enter shows it ([5bf319f](https://github.com/brettmostert/agent-command/commit/5bf319f7fe090cb2d52d881c7fe70fdce7ab1aa2))

## [0.32.0](https://github.com/brettmostert/agent-command/compare/v0.31.1...v0.32.0) (2026-11-02)


### ⚠ BREAKING CHANGES

* **speech:** remove the Python speech engine; install and uninstall clean up old Python setups

### Features

* **speech:** remove the Python speech engine; install and uninstall clean up old Python setups ([cec7cf3](https://github.com/brettmostert/agent-command/commit/cec7cf33493d71397233885c29a2ddc69c3dec25))

## [0.31.1](https://github.com/brettmostert/agent-command/compare/v0.31.0...v0.31.1) (2026-11-02)


### Bug Fixes

* **view:** deflake TestFailedAttachResumesOnce ([5499424](https://github.com/brettmostert/agent-command/commit/54994246d990d15ff57a1608c09c155c1220f346))

## [0.31.0](https://github.com/brettmostert/agent-command/compare/v0.30.1...v0.31.0) (2026-11-02)


### Features

* **speech:** add a Swift speech engine (FluidAudio Kokoro on Core ML) beside the Python one ([c0218ca](https://github.com/brettmostert/agent-command/commit/c0218ca7f7ab857a456e3fa40447ade75a05d813))

## [0.30.1](https://github.com/brettmostert/agent-command/compare/v0.30.0...v0.30.1) (2026-11-01)


### Bug Fixes

* **sidebar:** cache repo roots in folder picker so navigation is instant ([b4faf19](https://github.com/brettmostert/agent-command/commit/b4faf19c514b8ab59a385877fb03f5994133321e))

## [0.30.0](https://github.com/brettmostert/agent-command/compare/v0.29.0...v0.30.0) (2026-11-01)


### Features

* host sessions only; remove claude --bg, attach and /bg adopt ([bf55934](https://github.com/brettmostert/agent-command/commit/bf55934ace1cd47c922f87bb810db29110c47acc))
* **host:** Harness interface and PTY host for interactive sessions ([cb47a9b](https://github.com/brettmostert/agent-command/commit/cb47a9bd729436a07c9906551ce7e64ad8787107))
* **host:** new sessions run hosted; views attach through the host socket ([dfd5e2d](https://github.com/brettmostert/agent-command/commit/dfd5e2d9544463cfbee74f42256a61e34663c068))
* **host:** pause hosted sessions whose host is gone; sweep stale host files ([3fd8f30](https://github.com/brettmostert/agent-command/commit/3fd8f30a106b6ae2318ff25773c82306ee5c8d97))

## [0.29.0](https://github.com/brettmostert/agent-command/compare/v0.28.1...v0.29.0) (2026-11-01)


### Features

* **sidebar:** folder picker for new sessions that never starts in a worktree ([6e0f293](https://github.com/brettmostert/agent-command/commit/6e0f293254c3c2fc2c2bbb55b7f391727ed53163))

## [0.28.1](https://github.com/brettmostert/agent-command/compare/v0.28.0...v0.28.1) (2026-11-01)


### Bug Fixes

* **popup:** draw code blocks as one box, not a strip per line ([57eea79](https://github.com/brettmostert/agent-command/commit/57eea7915f60fb24d3d8d597cff50dbdb65982c4))

## [0.28.0](https://github.com/brettmostert/agent-command/compare/v0.27.1...v0.28.0) (2026-11-01)


### Features

* **popup:** reopen hotkey is cmd+ctrl+o and opens on an empty queue (PAN-12) ([c7026fd](https://github.com/brettmostert/agent-command/commit/c7026fdc5b398acd3f0f0863b16587c341ff77ab))

## [0.27.1](https://github.com/brettmostert/agent-command/compare/v0.27.0...v0.27.1) (2026-11-01)


### Bug Fixes

* **popup:** keep the open item selected when the queue updates ([031abcc](https://github.com/brettmostert/agent-command/commit/031abcce4ea0c0c49b16ba6dd5554cea01c3c30b))

## [0.27.0](https://github.com/brettmostert/agent-command/compare/v0.26.0...v0.27.0) (2026-11-01)


### Features

* **claudecli:** enable remote control on all launched sessions ([44fadad](https://github.com/brettmostert/agent-command/commit/44fadad19f6f7157902a9123e3f4f56e3df47ca6))


### Bug Fixes

* **cli:** stop TestKeysCommand flaking on a loaded machine ([a4f08ff](https://github.com/brettmostert/agent-command/commit/a4f08ff23b30479b82a276b7ff6a5df229fc1393))

## [0.26.0](https://github.com/brettmostert/agent-command/compare/v0.25.3...v0.26.0) (2026-11-01)


### Features

* **popup:** read the current session's latest response aloud with cmd+ctrl+p ([eaf174b](https://github.com/brettmostert/agent-command/commit/eaf174b4f70e9a449a9c7d427a6bab80e461f49e))

## [0.25.3](https://github.com/brettmostert/agent-command/compare/v0.25.2...v0.25.3) (2026-11-01)


### Bug Fixes

* **adopt:** link a move-in listed before its hook; close the pane it left ([f9dd11e](https://github.com/brettmostert/agent-command/commit/f9dd11e40df9e48d28e34704da872668b905d839))

## [0.25.2](https://github.com/brettmostert/agent-command/compare/v0.25.1...v0.25.2) (2026-11-01)


### Bug Fixes

* send popup replies, keep names on move-in, point the empty view at the sidebar ([af6404f](https://github.com/brettmostert/agent-command/commit/af6404fa45e3cd0b6158b23fce10dc8f16f05154))
* **view:** reset the terminal when attach ends and the view closes ([97a3350](https://github.com/brettmostert/agent-command/commit/97a33505d48a6a640d44cfc6b303815d885029a5))

## [0.25.1](https://github.com/brettmostert/agent-command/compare/v0.25.0...v0.25.1) (2026-11-01)


### Bug Fixes

* **popup:** empty the reply box once a reply is sent ([eee7d20](https://github.com/brettmostert/agent-command/commit/eee7d20ed36377355e6fc4050dd6f5ac577bb2df))

## [0.25.0](https://github.com/brettmostert/agent-command/compare/v0.24.3...v0.25.0) (2026-11-01)


### Features

* **popup:** no popup for the session in the window you're in ([91836e6](https://github.com/brettmostert/agent-command/commit/91836e6853e622af6f9fc6d186e663953f975d56))


### Bug Fixes

* **popup:** replace itself with the new version after an upgrade ([435df25](https://github.com/brettmostert/agent-command/commit/435df2505bc77f826b4a0b7de2c310e124820762))

## [0.24.3](https://github.com/brettmostert/agent-command/compare/v0.24.2...v0.24.3) (2026-11-01)


### Bug Fixes

* **daemon:** keep the pane of sessions in your own windows current ([645d364](https://github.com/brettmostert/agent-command/commit/645d364334122e104419361d71c9fe6165ec6ac4))

## [0.24.2](https://github.com/brettmostert/agent-command/compare/v0.24.1...v0.24.2) (2026-11-01)


### Bug Fixes

* **daemon:** stop even when clients keep a connection open ([f4494e7](https://github.com/brettmostert/agent-command/commit/f4494e77202380016b87b1a15923d1f0a091e50f))

## [0.24.1](https://github.com/brettmostert/agent-command/compare/v0.24.0...v0.24.1) (2026-11-01)


### Bug Fixes

* **popup:** copy and paste in the reply popup ([7de1eb5](https://github.com/brettmostert/agent-command/commit/7de1eb528d98f41b984641a47203ba5bd36cb899))

## [0.24.0](https://github.com/brettmostert/agent-command/compare/v0.23.0...v0.24.0) (2026-11-01)


### Features

* **init:** ask about the reply popup, auto-play and speech ([8f3e07f](https://github.com/brettmostert/agent-command/commit/8f3e07f517cd3d1cd0ba2f76e060d7f59e1031c3))
* **popup:** add the native reply panel (spike) ([e115939](https://github.com/brettmostert/agent-command/commit/e1159396df58e258cf33ed43723f98c73dbd916b))
* **popup:** keep an inbox of finished turns, questions and permissions ([fc1c6b5](https://github.com/brettmostert/agent-command/commit/fc1c6b519942a63763e926819b09ceaeefc6932a))
* **popup:** run the reply popup and answer sessions from it ([96fad83](https://github.com/brettmostert/agent-command/commit/96fad8387e4ca26ab920d228f0946165ee7e90cd))
* **speech:** read text aloud with Kokoro on MLX ([8a37723](https://github.com/brettmostert/agent-command/commit/8a37723e0e7d39b4b39988efc010db067b032497))
* **speech:** stream audio instead of writing files ([c94c54a](https://github.com/brettmostert/agent-command/commit/c94c54a610579e32421cceb6fab54dab5c8d7217))

## [0.23.0](https://github.com/brettmostert/agent-command/compare/v0.22.0...v0.23.0) (2026-11-01)


### Features

* **sessions:** finish sessions, /exit finishes, Claude id in the header ([0851af9](https://github.com/brettmostert/agent-command/commit/0851af9ce39026341988789913012d3bec012791))

## [0.22.0](https://github.com/brettmostert/agent-command/compare/v0.21.0...v0.22.0) (2026-11-01)


### Features

* **statusbar:** show acmd's version at the far right ([fe33317](https://github.com/brettmostert/agent-command/commit/fe33317fee22d22e508267481eb8fe52edacc5e8))

## [0.21.0](https://github.com/brettmostert/agent-command/compare/v0.20.0...v0.21.0) (2026-11-01)


### Features

* **ghostty:** use the same shortcut combos as Kitty ([447ae77](https://github.com/brettmostert/agent-command/commit/447ae77e503c0ade5f6127549f893efa6bbd5c4b))
* **sessions:** add a terminal per session under the Claude pane ([7c7f70f](https://github.com/brettmostert/agent-command/commit/7c7f70f84a4675eb17545fcb7d0fad61e52299b7))
* **view:** add a header bar above the Claude pane ([55d003d](https://github.com/brettmostert/agent-command/commit/55d003d86133a16dfb681354ee5d270773b5e906))


### Bug Fixes

* **sessions:** wait until a resumed session runs before showing it ([a6e8873](https://github.com/brettmostert/agent-command/commit/a6e8873c32748c8e1ba95a6da13ff4e07a13f848))

## [0.20.0](https://github.com/brettmostert/agent-command/compare/v0.19.0...v0.20.0) (2026-11-01)


### Features

* **sessions:** resume on show, and move a pane's session in with cmd+shift+m ([949faf2](https://github.com/brettmostert/agent-command/commit/949faf2e409c9750de1ad5f60c11bf756796ace7))

## [0.19.0](https://github.com/brettmostert/agent-command/compare/v0.18.0...v0.19.0) (2026-11-01)


### Features

* **kitty:** add a full-width status bar under the sidebar and view ([71078ef](https://github.com/brettmostert/agent-command/commit/71078effeb3fff561f309ec5b6df983422623d10))

## [0.18.0](https://github.com/brettmostert/agent-command/compare/v0.17.0...v0.18.0) (2026-11-01)


### Features

* **sidebar:** close the view with q ([b96597e](https://github.com/brettmostert/agent-command/commit/b96597e02a494706eb33041c04e122319ac47fe3))

## [0.17.0](https://github.com/brettmostert/agent-command/compare/v0.16.0...v0.17.0) (2026-11-01)


### Features

* **sidebar:** rename with the r key only, not double-click ([4a11110](https://github.com/brettmostert/agent-command/commit/4a11110554d87031366bb7b0a7a95c78f4d679a4))
* **sidebar:** show messages in a status bar at the bottom ([9e8db05](https://github.com/brettmostert/agent-command/commit/9e8db05ecf801b41f197b1e401c4583d951bccd7))

## [0.16.0](https://github.com/brettmostert/agent-command/compare/v0.15.0...v0.16.0) (2026-11-01)


### Features

* **sessions:** add acmd purge ([b28ce6f](https://github.com/brettmostert/agent-command/commit/b28ce6fa1946a02be3e2e70b363e792ebcbc98bd))

## [0.15.0](https://github.com/brettmostert/agent-command/compare/v0.14.0...v0.15.0) (2026-11-01)


### Features

* **notify:** add macOS notifications ([b2bcb00](https://github.com/brettmostert/agent-command/commit/b2bcb00349c8e30ce5798673ab70901dad7bd655))

## [0.14.0](https://github.com/brettmostert/agent-command/compare/v0.13.0...v0.14.0) (2026-11-01)


### Features

* **sessions:** move sessions into acmd and split them out ([3da3363](https://github.com/brettmostert/agent-command/commit/3da336353df6b6221a37722c8fdcf1e9d29eb637))

## [0.13.0](https://github.com/brettmostert/agent-command/compare/v0.12.1...v0.13.0) (2026-11-01)


### Features

* **ghostty:** add Ghostty views, setup and doctor checks ([de2639a](https://github.com/brettmostert/agent-command/commit/de2639a34032be55d338bbcf24fb46cec981d3e8))

## [0.12.1](https://github.com/brettmostert/agent-command/compare/v0.12.0...v0.12.1) (2026-11-01)


### Bug Fixes

* **kitty:** stop the sidebar width loop; add daemon stop and restart ([24dd4a1](https://github.com/brettmostert/agent-command/commit/24dd4a1c14e83758257c930c9be7267ede67904d))

## [0.12.0](https://github.com/brettmostert/agent-command/compare/v0.11.0...v0.12.0) (2026-10-31)


### Features

* **keys:** add the cheat sheet, session picker and open in editor ([68f3449](https://github.com/brettmostert/agent-command/commit/68f34499e1bed5ee9a867ca8f71e6b5491f44b7e))

## [0.11.0](https://github.com/brettmostert/agent-command/compare/v0.10.0...v0.11.0) (2026-10-31)


### Features

* **sidebar:** add the Bubble Tea sidebar ([dabfbbe](https://github.com/brettmostert/agent-command/commit/dabfbbe884abcc2c36b748aa1dfd44a23b390622))

## [0.10.0](https://github.com/brettmostert/agent-command/compare/v0.9.0...v0.10.0) (2026-10-31)


### Features

* **kitty:** add the view's sidebar split and Kitty shortcuts ([18d4e2a](https://github.com/brettmostert/agent-command/commit/18d4e2aba38e6b23b776f77e8caf59f582aaa7ea))

## [0.9.0](https://github.com/brettmostert/agent-command/compare/v0.8.0...v0.9.0) (2026-10-31)


### Features

* **view:** add the view engine, acmd new, go and resume ([b1ed06b](https://github.com/brettmostert/agent-command/commit/b1ed06b180f38cc71e47ba6e8a2a41dcafcc8f25))

## [0.8.0](https://github.com/brettmostert/agent-command/compare/v0.7.0...v0.8.0) (2026-10-31)


### Features

* **doctor:** add acmd doctor ([714b49f](https://github.com/brettmostert/agent-command/commit/714b49fde970b31670e734667a6550978df8a5fc))

## [0.7.0](https://github.com/brettmostert/agent-command/compare/v0.6.0...v0.7.0) (2026-10-31)


### Features

* **setup:** add acmd init, uninstall and editor commands ([973de90](https://github.com/brettmostert/agent-command/commit/973de903ba9507521337873d4a151ceb159a9df7))

## [0.6.0](https://github.com/brettmostert/agent-command/compare/v0.5.0...v0.6.0) (2026-10-31)


### Features

* **setup:** add config editors for Claude hooks and Kitty ([2235f69](https://github.com/brettmostert/agent-command/commit/2235f6953d96dc928ad03144859e1635521e9b5d))


### Bug Fixes

* **reconcile:** don't pause sessions Claude hasn't listed yet ([7c9d80d](https://github.com/brettmostert/agent-command/commit/7c9d80de21498f1e055ab8be1b0acc8da81d5c5f))

## [0.5.0](https://github.com/brettmostert/agent-command/compare/v0.4.0...v0.5.0) (2026-10-31)


### Features

* **cli:** add list, rename, archive, move, group and view commands ([77080b7](https://github.com/brettmostert/agent-command/commit/77080b7aa62a1e7b8acb069ef7337a323330d41b))
* **reconcile:** add claude CLI wrapper, reconciler and Esc detection ([b952dd6](https://github.com/brettmostert/agent-command/commit/b952dd6fdb4f5ce9736c085d696d81855f071bb1))

## [0.4.0](https://github.com/brettmostert/agent-command/compare/v0.3.0...v0.4.0) (2026-10-31)


### Features

* **daemon:** add daemon, socket protocol and acmd hook ([f731a8f](https://github.com/brettmostert/agent-command/commit/f731a8fa44e3db22a13e5168c8a1fc0869bdbd13))

## [0.3.0](https://github.com/brettmostert/agent-command/compare/v0.2.0...v0.3.0) (2026-10-31)


### Features

* **store:** add SQLite state store with migrations ([f6b1edd](https://github.com/brettmostert/agent-command/commit/f6b1edd5a755c2795931e6427c24f7f9bd0e3efc))

## [0.2.0](https://github.com/brettmostert/agent-command/compare/v0.1.1...v0.2.0) (2026-10-31)


### Features

* **core:** session model and status state machine ([85964b8](https://github.com/brettmostert/agent-command/commit/85964b8c3c5a939fa2dba273c56f7a0d2a8aa7d4))

## [0.1.1](https://github.com/brettmostert/agent-command/compare/v0.1.0...v0.1.1) (2026-10-30)


### Bug Fixes

* **release:** run goreleaser via make so syft is on PATH ([8d31d77](https://github.com/brettmostert/agent-command/commit/8d31d7728f7789380a2130e336d2fe1f434e2b64))

## 0.1.0 (2026-10-30)


### Features

* bootstrap walking skeleton with CI/CD and Homebrew release ([515fb36](https://github.com/brettmostert/agent-command/commit/515fb36942770b0ff45985fe34516750e28e4fe8))
