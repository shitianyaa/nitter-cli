<!--
提交前确认：不含实例凭据、代理凭据、私有实例 URL、下载内容或本地状态。
Before submitting: no instance credentials, proxy credentials, private instance URLs, downloads, or local state.
-->

<!--
Closes #123
-->

## 变更点 / Changes

<!--
列要点：改动 + 原因。关联 Issue 用 `Closes #123`（合并后关闭）。
Bullet the change and why. Link an issue with `Closes #123` (closes on merge).
-->

## 验证步骤 / Verification

<!--
实际运行的命令和结果。例如 / Commands and results. E.g.
- `go test ./...`
- `sh scripts/build.sh`
- `bash e2e/run.sh`（改动涉及 e2e 覆盖的契约时 / when the change touches a covered contract）
未测试时说明原因。 / If not tested, explain why.

可在下方提供恰好一个 ```commands fenced block，声明合并前 CI 可代跑的默认验证命令；
只允许受信任白名单中的命令与受控管道，不会执行 shell。
Optionally declare exactly one ```commands fenced block below as the default
verification commands for CI. Only trusted-whitelist commands and controlled
pipelines are accepted; no shell is invoked.

\`\`\`commands
nitter search nitter --ndjson | head
\`\`\`
-->

## 检查清单 / Checklist

- [ ] 我没有引入恶意代码 / No malicious code
- [ ] 我没有新增依赖，或已说明新依赖的名称、来源与用途 / No new dependencies, or their name, source, and purpose are stated in Changes
- [ ] 这不是一次破坏性更新，或已在「变更点」标注迁移影响 / Not a breaking change, or migration impact noted in Changes
- [ ] 受影响的文档已按 `docs/maintainers/agents/documentation-guidelines.md` 的变更路由同步 / Affected docs synced per the routing table in `docs/maintainers/agents/documentation-guidelines.md`
- [ ] 改动方向和范围已在实现前与维护者确认 / The direction and scope of this change were confirmed with a maintainer before implementation
