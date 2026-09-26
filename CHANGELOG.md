# Changelog

## 1.0.0 (2026-09-26)


### Features

* account show, devices list and account unlink with plain/JSON output (Phase 2.4) ([f974c0f](https://github.com/CWBudde/go-signal/commit/f974c0fcc3d06b6d8c38cc9daf79eaa6b47d1c7e))
* add attachment upload functionality and enhance message sending ([1ee51c0](https://github.com/CWBudde/go-signal/commit/1ee51c0b7896dfa15149c8dbfa2f93c055e3d1ef))
* Add CI workflow for building and testing, and update submodule references ([d1bf297](https://github.com/CWBudde/go-signal/commit/d1bf29774a447ca796ce0550173175f64af5e04f))
* Add tests and output formatting for Signal events ([21deaef](https://github.com/CWBudde/go-signal/commit/21deaeff19e9808834d461406bd876f321e2a935))
* contacts list/show/block/unblock and name resolution (Phase 4.1) ([97515a9](https://github.com/CWBudde/go-signal/commit/97515a915baf3cbcd5f6c374aa6aee9f78a90ae8))
* download attachments on receive (Phase 3.7) ([6244ce0](https://github.com/CWBudde/go-signal/commit/6244ce0e72ae0dbe5d2871295aff4f89b3aa2b34))
* enhance coverage reporting and improve MCP command structure ([83773fc](https://github.com/CWBudde/go-signal/commit/83773fccca92614d45e2881cb4994cd5f02ec5b3))
* enhance development setup and libsignal integration ([214de50](https://github.com/CWBudde/go-signal/commit/214de50c0bc48ecbb23ae5cc0bbb5303e4f29ef0))
* finish send rich content (Phase 3.4) ([b20061d](https://github.com/CWBudde/go-signal/commit/b20061de798e0dc5c37b6f86a806f08444507353))
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
* remote unlink handling (Phase 2.5) ([5a338c2](https://github.com/CWBudde/go-signal/commit/5a338c2f7837cda05fedc11c31b9ce90a64cb366))
* signal client facade with signalmeow backend and test fake (Phase 2.1) ([e27f646](https://github.com/CWBudde/go-signal/commit/e27f646b38863269646cc751b671f5276025aa22))
* update build constraints for purego compatibility and bump dependencies ([3fa3327](https://github.com/CWBudde/go-signal/commit/3fa33271c3fc022651ef5b302ea15c0fc7734437))
* **version:** report the libsignal-go version in purego builds ([9972b01](https://github.com/CWBudde/go-signal/commit/9972b01bd5482115194c96849a24069502824cac))


### Bug Fixes

* address PR review comments ([5832765](https://github.com/CWBudde/go-signal/commit/5832765e163dcc35878864ed72a959f445f754c4))
* **ci:** download mautrix-signal before check-libsignal reads its dir ([6a2ad04](https://github.com/CWBudde/go-signal/commit/6a2ad044e8744196701e2ee64243754e62f5f421))
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
* record PLAN.md 7.3 progress ([aaa3831](https://github.com/CWBudde/go-signal/commit/aaa3831930cfad5aca9e65b43b7ddf3126cb03bf))
* record the blocking and identity trust decisions in PLAN.md ([0d4ec6b](https://github.com/CWBudde/go-signal/commit/0d4ec6bfcdb48660b5225615dc68a5e93d4f2a12))
