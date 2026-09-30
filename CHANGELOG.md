# Changelog

## [0.2.0](https://github.com/CWBudde/go-signal/compare/v0.1.0...v0.2.0) (2026-09-30)


### Features

* add administrator group member removal ([17ad352](https://github.com/CWBudde/go-signal/commit/17ad352b6f1674a7c8242ba78bface5f8ac38b93))
* add authenticated loopback daemon API and SSE ([4290a6a](https://github.com/CWBudde/go-signal/commit/4290a6af367a34fd3ecee7891ce0e6c6a3951a1c))
* add group members and approve join requests ([0869479](https://github.com/CWBudde/go-signal/commit/0869479650b1a7b787cb020eccd127cc384c9945))
* add group members and approve join requests ([69008f5](https://github.com/CWBudde/go-signal/commit/69008f508a065d302a47714e0fd5ef855727a45c))
* add group polls and retained inbox results ([a01d35c](https://github.com/CWBudde/go-signal/commit/a01d35ce434316616b1fc566fda55a986d4ffbd6))
* add group polls and retained inbox results ([a998363](https://github.com/CWBudde/go-signal/commit/a998363bfef5d8465b6bc247da3abeca82d38a7d))
* add group renaming functionality ([925073f](https://github.com/CWBudde/go-signal/commit/925073feac6e45d14d0af2bbebee1883947da97b))
* add local daemon API for scripts and bots ([ac4618f](https://github.com/CWBudde/go-signal/commit/ac4618fe878852ebd8ca45db805fc26875dcfe8d))
* add own-profile text updates ([9542076](https://github.com/CWBudde/go-signal/commit/954207618d97566bf6dbc42000350246eef5a364))
* add own-profile text updates ([dc78406](https://github.com/CWBudde/go-signal/commit/dc78406e68147bfd1cc517253c977ad725ca74ca))
* add regression test for hook queue overflow handling ([c84b15b](https://github.com/CWBudde/go-signal/commit/c84b15be5f0a7662c786a46856eb97f12af604d0))
* groups rename command ([5d34fa9](https://github.com/CWBudde/go-signal/commit/5d34fa9776f5ee2ac68a9272613717dea53fd584))
* implement group creation functionality with CLI and API support ([285903f](https://github.com/CWBudde/go-signal/commit/285903f77f7b93ede60140b0ac9dee15a4c85a8b))
* install with go install; README install and MCP sections ([7e7eca9](https://github.com/CWBudde/go-signal/commit/7e7eca9cfab32a78686b110b435bacf952bc4a9e))
* one-line install script attached to every release ([72cda78](https://github.com/CWBudde/go-signal/commit/72cda785c2eedafd143802d267f332052668b457))
* receive sticker images and send stickers ([f62dc7d](https://github.com/CWBudde/go-signal/commit/f62dc7d400fe269bfa6eb72441f814bd6471677a))
* receive sticker images and send stickers ([1498cff](https://github.com/CWBudde/go-signal/commit/1498cff8517721a64a5a5868dbb46bb078308a6d))


### Bug Fixes

* **app:** bound read receipts and allocate unique send timestamps ([bc47065](https://github.com/CWBudde/go-signal/commit/bc470659c5eaa46555dd2bddd9817f4516aa4e84))


### Documentation

* define own-profile text update design ([a76b6a6](https://github.com/CWBudde/go-signal/commit/a76b6a6741e60d6666892f109d000f5d3ccbeecc))
* plan own-profile text updates ([6364190](https://github.com/CWBudde/go-signal/commit/6364190f2ed9c1c8eb5f5b2f54f0b3dbc9eef5bc))
* record own-profile implementation delivery ([e3f14b8](https://github.com/CWBudde/go-signal/commit/e3f14b821dfcc29971eb16a5cfee66ab80cdc3d9))

## 0.1.0 (2026-09-28)


### ⚠ BREAKING CHANGES

* release binaries use the pure-Go libsignal-go backend instead of libsignal through cgo, and `just build` builds it.
* select the pure-Go backend with the libsignal_go tag

### build

* select the pure-Go backend with the libsignal_go tag ([e149cb9](https://github.com/CWBudde/go-signal/commit/e149cb9c5794fed21774dba738c8c581bc57f8ed))


### Features

* account show, devices list and account unlink with plain/JSON output (Phase 2.4) ([f974c0f](https://github.com/CWBudde/go-signal/commit/f974c0fcc3d06b6d8c38cc9daf79eaa6b47d1c7e))
* add attachment upload functionality and enhance message sending ([1ee51c0](https://github.com/CWBudde/go-signal/commit/1ee51c0b7896dfa15149c8dbfa2f93c055e3d1ef))
* Add CI workflow for building and testing, and update submodule references ([d1bf297](https://github.com/CWBudde/go-signal/commit/d1bf29774a447ca796ce0550173175f64af5e04f))
* Add tests and output formatting for Signal events ([21deaef](https://github.com/CWBudde/go-signal/commit/21deaeff19e9808834d461406bd876f321e2a935))
* contacts list/show/block/unblock and name resolution (Phase 4.1) ([97515a9](https://github.com/CWBudde/go-signal/commit/97515a915baf3cbcd5f6c374aa6aee9f78a90ae8))
* **doctor:** warn on CPUs without AES instructions ([b0d6a58](https://github.com/CWBudde/go-signal/commit/b0d6a58939dfeffbd4782a31eaa70bea6ad805a9))
* download attachments on receive (Phase 3.7) ([6244ce0](https://github.com/CWBudde/go-signal/commit/6244ce0e72ae0dbe5d2871295aff4f89b3aa2b34))
* enhance coverage reporting and improve MCP command structure ([83773fc](https://github.com/CWBudde/go-signal/commit/83773fccca92614d45e2881cb4994cd5f02ec5b3))
* enhance development setup and libsignal integration ([214de50](https://github.com/CWBudde/go-signal/commit/214de50c0bc48ecbb23ae5cc0bbb5303e4f29ef0))
* finish send rich content (Phase 3.4) ([b20061d](https://github.com/CWBudde/go-signal/commit/b20061de798e0dc5c37b6f86a806f08444507353))
* **group-send:** implement endorsements and integration tests for multi-recipient sends ([ae593bb](https://github.com/CWBudde/go-signal/commit/ae593bb3c15ec2f67ded8760a090f84ffa00c936))
* groups list/show/leave (Phase 4.2) ([6e13420](https://github.com/CWBudde/go-signal/commit/6e13420594699c521b47fba6d6eeb5924042216a))
* identities, safety numbers and TOFU trust (Phase 4.3) ([c75f2cb](https://github.com/CWBudde/go-signal/commit/c75f2cb5ce719f029f3de4f231b72b4636cd89e4))
* implement app layer for CLI commands and recipient resolution ([e19b23b](https://github.com/CWBudde/go-signal/commit/e19b23bc7e93555f9d95e9f0d75af32fbdc435c4))
* implement reconnect policy and enhance signal handling ([f80ce9a](https://github.com/CWBudde/go-signal/commit/f80ce9abdc06d08f0d76099c20fc88e413aef2dd))
* Implement send functionality with detailed results and error handling ([5da6f05](https://github.com/CWBudde/go-signal/commit/5da6f05d11383249ed664495153811164fb74045))
* implement unlinked account handling ([92de5dd](https://github.com/CWBudde/go-signal/commit/92de5ddbef7f3e238436563aa45b5bf8d477a272))
* initial sync after linking (Phase 3.9) ([ed08ce1](https://github.com/CWBudde/go-signal/commit/ed08ce1aed7dc2a610cdaf56adbfa9a6c0962086))
* link and receive spike on top of signalmeow (Phase 1.4) ([70c7dfe](https://github.com/CWBudde/go-signal/commit/70c7dfe9efcb0c2ed80b6f44528e94112dc74e93))
* MCP server skeleton with stdio transport (Phase 5.2) ([79c6771](https://github.com/CWBudde/go-signal/commit/79c677124e65b231be5f6d61c8960074db387b34))
* **mcp:** docs/mcp.md and a streamable HTTP transport (PLAN.md 5.6) ([f036c9d](https://github.com/CWBudde/go-signal/commit/f036c9d40efd36e4384f72907f315cf8e62cf0df))
* **mcp:** mcp doctor command and doctor tool for health checks (PLAN.md 5.7) ([eea7ec8](https://github.com/CWBudde/go-signal/commit/eea7ec8d60386044383f8e2334a84791bab59e8f))
* **mcp:** receive messages into an inbox for MCP clients (PLAN.md 5.4) ([1a34b9f](https://github.com/CWBudde/go-signal/commit/1a34b9f72a763a3ef95a60651f297dbab544af57))
* **mcp:** run a program for incoming messages (PLAN.md 5.8) ([aac25ab](https://github.com/CWBudde/go-signal/commit/aac25ab0ab0f7260cbf3760135a375da8013154d))
* **mcp:** run a program for incoming messages (PLAN.md 5.8) ([6d08f54](https://github.com/CWBudde/go-signal/commit/6d08f5417163d07c3b92926ecd634b8aebea46ef))
* **mcp:** write tools with an allowlist, read-only mode and confirmation (PLAN.md 5.5) ([e89728a](https://github.com/CWBudde/go-signal/commit/e89728a6c60d132ad6022769f8cc5e38793e64aa))
* multi-account selection with -a/--account (Phase 2.3) ([dc04402](https://github.com/CWBudde/go-signal/commit/dc044028a1f984226ce8fb55e08ea43dd7b02ac3))
* one-shot receive with idle timeout and --max (Phase 3.6) ([187f02e](https://github.com/CWBudde/go-signal/commit/187f02e59434e59239c8a788516d1770400cb441))
* per-account data-dir layout, permissions and account lock (Phase 2.2) ([eb37570](https://github.com/CWBudde/go-signal/commit/eb3757008637f9d80247920c5f673c752cfe686e))
* purego build tag for a cgo-free backend (PLAN.md 7.1, 7.2) ([64d0460](https://github.com/CWBudde/go-signal/commit/64d04600d7ea908ebd49a80a441236e6c25a3c4f))
* reactions, remote delete and read receipts (Phase 3.8) ([da5e32e](https://github.com/CWBudde/go-signal/commit/da5e32eeaa0c811c7ada5a4fbcd6e9bdc4e49e6e))
* release pipeline, static builds and install docs (Phase 6) ([9ad98b9](https://github.com/CWBudde/go-signal/commit/9ad98b946b14cf1af156b16d621439b9e8e412a2))
* release pipeline, static builds and install docs (PLAN.md Phase 6) ([12b4394](https://github.com/CWBudde/go-signal/commit/12b4394e049fc33ffc83526bcb03ce015438b420))
* release pure-Go binaries (PLAN.md 10.3) ([b24e872](https://github.com/CWBudde/go-signal/commit/b24e8726f3e5af1a865f5e78d84edec196d4ce73))
* remote unlink handling (Phase 2.5) ([5a338c2](https://github.com/CWBudde/go-signal/commit/5a338c2f7837cda05fedc11c31b9ce90a64cb366))
* signal client facade with signalmeow backend and test fake (Phase 2.1) ([e27f646](https://github.com/CWBudde/go-signal/commit/e27f646b38863269646cc751b671f5276025aa22))
* update build constraints for purego compatibility and bump dependencies ([3fa3327](https://github.com/CWBudde/go-signal/commit/3fa33271c3fc022651ef5b302ea15c0fc7734437))
* **version:** report the libsignal-go version in purego builds ([9972b01](https://github.com/CWBudde/go-signal/commit/9972b01bd5482115194c96849a24069502824cac))
* **zkgroup:** implement Phase 8.3 changes and add integration tests ([dadedd4](https://github.com/CWBudde/go-signal/commit/dadedd4b49a86e03287fe5fdeecd237d2ffe14e4))


### Bug Fixes

* address PR review comments ([5832765](https://github.com/CWBudde/go-signal/commit/5832765e163dcc35878864ed72a959f445f754c4))
* **ci:** download mautrix-signal before check-libsignal reads its dir ([6a2ad04](https://github.com/CWBudde/go-signal/commit/6a2ad044e8744196701e2ee64243754e62f5f421))
* **deps:** mautrix-signal purego.8 passes libsignal milliseconds ([4f39517](https://github.com/CWBudde/go-signal/commit/4f39517b8190bcdc6ff62254c9a1027d7410332c))
* identity trust, close ordering and group review findings ([483b4c0](https://github.com/CWBudde/go-signal/commit/483b4c0ae8a08f1a3316efeb4752128770a74c69))
* keep block overrides from outliving the phone's changes ([bac41ea](https://github.com/CWBudde/go-signal/commit/bac41ea761ab3fd88edb0ab3b0a2348237f71ceb))
* **mcp:** check the hook settings in mcp doctor ([2e3561a](https://github.com/CWBudde/go-signal/commit/2e3561afbe0d4c2148a38072c652a58647b78250))
* **mcp:** let shutdown cancel the hook's --hook-from lookup ([27d2afe](https://github.com/CWBudde/go-signal/commit/27d2afe8f7e292096273ae5e2c9d2d850264c69f))
* share cmd test constants between send and receive tests; tick 3.3 in PLAN.md ([c019f9d](https://github.com/CWBudde/go-signal/commit/c019f9d756d6b5c9ad993a1f866e2ba90867cb0a))
* verify stored storage-service data and keep unresolved block overrides ([846db7d](https://github.com/CWBudde/go-signal/commit/846db7dd3c862f5cb4d5555fa4f8f49164d56aa5))


### Documentation

* mark Phase 8.2 zkcredential complete ([59d6afc](https://github.com/CWBudde/go-signal/commit/59d6afc3b1da753c3775a89c55e8447219139ee8))
* record completion of Phase 8.1 poksho ([9688267](https://github.com/CWBudde/go-signal/commit/9688267acdc0371dc49f79f65e4fdbc76d4d5083))
* record Phase 8.2 attribute crypto milestone ([d4e3038](https://github.com/CWBudde/go-signal/commit/d4e303804fe3df6e94f865fd5732fe37899b40b9))
* record Phase 8.2 credential proof milestone ([25132a6](https://github.com/CWBudde/go-signal/commit/25132a6d8aeb8f924153664fdef39cff22e5e423))
* record PLAN.md 10.1 CI done ([4d5057a](https://github.com/CWBudde/go-signal/commit/4d5057aa6106419a56ff1a75fb5de7f2df36713e))
* record PLAN.md 10.2 CT-01 and CT-03 fixed ([c3054ce](https://github.com/CWBudde/go-signal/commit/c3054ce55c5fa84d3bae6e8898b360195661ddca))
* record PLAN.md 10.2 CT-02 fixed ([f01d183](https://github.com/CWBudde/go-signal/commit/f01d183b1939a4b6e7548d9174286159f7b29825))
* record PLAN.md 10.2 fork fuzzing done ([d770284](https://github.com/CWBudde/go-signal/commit/d77028450a37912cbd38c4fa2f93c24676acf9ab))
* record PLAN.md 7.3 items 4 and 5 ([674322a](https://github.com/CWBudde/go-signal/commit/674322aa7b7e38800304ca282b3a29eea6b83f47))
* record PLAN.md 7.3 progress ([aaa3831](https://github.com/CWBudde/go-signal/commit/aaa3831930cfad5aca9e65b43b7ddf3126cb03bf))
* record PLAN.md 9.1 Noise handshakes ([391386e](https://github.com/CWBudde/go-signal/commit/391386e1d5189a04cdc5f24ac301a2c1344c2136))
* record PLAN.md 9.2 item 1 (quote v3, PCK chain, CRLs) ([94e96ed](https://github.com/CWBudde/go-signal/commit/94e96ed00237c67034306dc47d4e999d75770965))
* record PLAN.md 9.2 item 4 (recorded attestation vectors) ([c73e619](https://github.com/CWBudde/go-signal/commit/c73e6193af1d31a509c65d3cdb6b9bb811c8c89e))
* record PLAN.md 9.2 items 2 and 3 (TCB policy, MRENCLAVE, expiry) ([bf871df](https://github.com/CWBudde/go-signal/commit/bf871df9f450afdca85a430a716c7e24d1cd0cab))
* record PLAN.md 9.3 progress (enclave handshake, CDS2 client state) ([7a791b2](https://github.com/CWBudde/go-signal/commit/7a791b28eb893cafb3c46a5da911210de5e9d0bd))
* record PLAN.md 9.4 sweep done and 9.3 shim wiring (purego.5) ([b39e8a9](https://github.com/CWBudde/go-signal/commit/b39e8a95c60da6252d92e0d1838a95c03f3d16df))
* record PLAN.md 9.4 sweep progress (HSM enclave, device transfer) ([64434c5](https://github.com/CWBudde/go-signal/commit/64434c52184b5f6396dd60bc047fa651b8810d6b))
* record the blocking and identity trust decisions in PLAN.md ([0d4ec6b](https://github.com/CWBudde/go-signal/commit/0d4ec6bfcdb48660b5225615dc68a5e93d4f2a12))
* record the fork releases in PLAN.md and docs/dev.md ([b036402](https://github.com/CWBudde/go-signal/commit/b036402347cb0550af91607885f0d303ceb6fdb4))
* split PLAN.md 9.3 client state item into subtasks ([d9fbb9b](https://github.com/CWBudde/go-signal/commit/d9fbb9b02e3b182ba4b3f8f8f4e62e64f3410a49))
* update PLAN.md and add constant-time review documentation ([4813b20](https://github.com/CWBudde/go-signal/commit/4813b20a230d67ff059c4d4aae408233f4aff84d))
