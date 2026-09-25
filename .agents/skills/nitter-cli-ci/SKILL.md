---
name: nitter-cli-ci
description: Diagnose, verify, and monitor nitter-cli GitHub Actions runs and local CI gates (scope classification, Quality gate with Unix race + Windows parity, PR metadata gates, offline e2e, release matrix). Use when checks fail or hang, a PR needs readiness verification, or a run needs root-cause analysis.
---

# nitter-cli CI 与 workflow

按仓库维护流程诊断和验证 CI。先读取 `AGENTS.md`、`docs/maintainers/development.md`、目标 workflow 与
`e2e/run.sh` 的退出码契约；默认只读，先获得证据再决定是否需要改代码或重跑。

## 安全边界

- 查看 PR、run、job、step、日志和本地门禁是只读操作；重跑、取消、dispatch、修改 workflow、push 或修复代码前取得用户对具体动作的明确授权。
- 不把实例凭据、代理凭据、私有实例 URL、下载内容或 `~/.nitter-cli/` 状态写入日志、issue、PR 或最终报告；日志中发现敏感值时只报告脱敏位置。
- 不用 rerun 把代码或策略失败伪装成通过。重跑成功只能证明该次执行通过，必须保留原失败原因和 run/attempt 链接。
- `.github/workflows/release.yml` 只接受 `push.tags: v*`；不要对它调用 `gh workflow run`，不要移动旧 tag，也不要用默认分支内容覆盖不可变 tag。
- 不为解决“运行较久”凭空增加 timeout、retry、跳过条件或 fallback；区分真实无响应、基础设施故障、代码失败和正常长任务。

## 识别准确的 run 和提交

1. 先确定仓库、PR/branch、workflow、run ID、attempt、head SHA 和默认分支；不要根据标题猜 run：

   ```bash
   gh pr checks <number> --json name,state,bucket,workflow,link
   gh run list --workflow <workflow-file> --limit 20
   gh run view <run-id> --json name,event,headBranch,headSha,status,conclusion,jobs,url
   ```

2. 对单个 run：

   ```bash
   gh run watch <run-id> --compact --exit-status
   gh run view <run-id> --log-failed
   gh run view <run-id> --json jobs --jq '.jobs[] | {name, databaseId, status, conclusion}'
   ```

   记录失败的 job、step、命令、head SHA 和首次错误；不要只摘最后一行。将 GitHub API 的网络/权限失败与 workflow 本身失败分开报告。

## 本地门禁选择

先根据 diff 分类，不要无依据地把所有门禁都扩展到每个小改动：

| 范围 | 最小验证 |
| --- | --- |
| README/docs/agent-only | `git diff --check` |
| Go 或行为代码 | 聚焦测试；随后 `go test ./... -count=1`、`go vet ./...`、`gofmt -l .`，构建相关时运行 `sh scripts/build.sh` |
| 取数、媒体解析、CLI、SDK、共享代码 | 另跑 `go test -race ./... -count=1` |
| `e2e/` 覆盖的契约 | `bash e2e/run.sh`（退出码 2 = 只有 skip，属软通过） |
| `.github/workflows/**` 或 `release.yml` 矩阵 | 核对 workflow 变换与平台矩阵条目；本仓库尚无 workflow 策略测试脚本 |

当前 workflow 职责对应关系：

- `ci.yml` → `scope`（变更范围分类：分类器从 base 提交运行，PR 无法改写评判自己的规则）+ `Quality gate`（gofmt / vet / test / race / build / offline e2e）+ `Windows parity`（test + build 的平台一致性）。纯文档改动（见 `.github/ci-change-scope.gitignore`）时后两个 job 以 skipped 结束，但 check 名仍存在，分支保护保持稳定。
- `pr-metadata.yml` → 校验 PR 模板三段与清单勾选，发布 `PR template gate` / `PR commands gate` 两个稳定 status；维护"正文失效 7 天自动关闭"的 age state。
- `pr-triage.yml` / `auto-assign.yml` → 路径打标、指派维护者、外部 PR 自动请求 review。
- `pr-invalid-close.yml` → 每日 cron 关闭正文长期无效且未修改的 PR。
- `release.yml` → tag 触发的 SemVer 校验、双语 changelog 软检查、6 平台构建与草稿 Release。

本地分类器自检（改动范围规则时）：

```bash
bash scripts/classify-change-scope.sh --base <base-sha> --head <head-sha>
```

## 按故障类型诊断

- **测试/构建失败**：读取完整失败 step，使用同一 ref/SHA 在本地重现；检查 gofmt 对齐、平台差异（路径分隔符、`USERPROFILE` vs `HOME`、文件权限、CRLF）与依赖。修复代码需要用户明确请求，CI skill 默认只诊断。
- **e2e 契约失败**：核对是真实契约破坏还是脚本假设变化；exit 1 才是失败，exit 2 是仅 skip 的软通过。
- **docs-only 与跳过**：`ci.yml` 由 `scripts/classify-change-scope.sh` 依据 `.github/ci-change-scope.gitignore` 分类；docs-only 时 `Quality gate` / `Windows parity` 整体 skipped，job 被有意跳过不等于通过。分类器失败（scope job 红）会让两个门禁 job 显式失败，不会静默放行。
- **缺失、skipped 或 pending check**：区分有意的范围跳过、job 未创建、权限不足和真正卡住；不得把 pending 当成功。
- **基础设施/瞬态失败**：只有日志证据支持 runner、网络或 GitHub API 异常时才建议 rerun；若同一失败重复出现，停止重跑并报告共同根因。
- **release 失败**：保持 immutable tag 与同一 source commit；不要从默认分支补文件或混用不同 run 的 artifact。

## 受控重跑

1. 诊断完成且用户明确授权后，优先只重跑失败 job：

   ```bash
   gh run rerun <run-id> --failed
   gh run watch <run-id> --compact --exit-status
   ```

   保存原 run、rerun attempt、授权理由和最终结果。代码或契约失败先修复并提交新的 commit，不要重复 rerun。
2. 除非 workflow 明确声明 `workflow_dispatch`，否则不要手动触发。

## 结果报告

按以下顺序报告：

- PR/run/workflow、event、attempt、head SHA、job/step 和 URL；
- 原始失败命令与第一处错误；
- 本地复现结果；
- 根因分类：代码、契约、配置、权限、基础设施或未知；
- 是否重跑、授权依据与结果；
- 未运行的真实环境检查、剩余风险和下一步最小动作。

不要只写“CI 通过/失败”；说明哪些 required checks 已完成、哪些被有意跳过、哪些仍 pending。
