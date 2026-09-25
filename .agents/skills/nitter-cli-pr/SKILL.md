---
name: nitter-cli-pr
description: Prepare, create, update, and monitor nitter-cli pull requests using the repository template, verification checklist, and authorization boundaries. Use when drafting a PR body, opening or editing a PR, requesting review, or checking readiness.
---

# nitter-cli Pull Request

按仓库流程准备和维护 GitHub PR。先读 `AGENTS.md`、`CONTRIBUTING.md`（或 `CONTRIBUTING.zh-CN.md`）和
`.github/PULL_REQUEST_TEMPLATE.md`。代码审查使用 `nitter-cli-review`，CI 诊断使用 `nitter-cli-ci`，
版本说明与发布使用 `nitter-cli-release-notes`。

## 授权与范围

- 默认只读。创建或更新 PR、push、请求 reviewer、评论、merge 或关闭前，要取得用户对该动作的明确授权。
- 先确认 branch、base、remote 和 diff 范围，不要带入无关的既有改动。
- PR、commit、日志和检查输出不得包含实例 basic-auth 凭据、代理凭据、私有实例 URL、下载内容或 `~/.nitter-cli/` 本地状态。
- 普通 PR 只写改动、验证与检查清单；不添加版本分类、breaking 标记或隐藏 metadata。

## 准备 PR

1. 收集范围：

   ```bash
   git status --short
   git branch --show-current
   git diff --stat
   git diff --name-status
   git rev-list --left-right --count origin/main...HEAD
   ```

   确认当前不是 detached HEAD，并确认 base 分支（本仓库为 `main`）。
2. 按 `docs/maintainers/agents/review-checklist.md` 检查架构边界、公开契约、文档路由与产品 skill。需要代码审查时运行 `nitter-cli-review`；发现问题先报告，不把修复混进单纯的 PR 准备。
3. PR 正文保留模板的三个部分：`变更点`、`验证步骤`、`检查清单`；`pr-metadata.yml` 会以 `PR template gate` / `PR commands gate` 两个 status 强制校验，清单有未勾选项即失败。
4. Verification 只记录实际运行过的完整命令和结果。按范围选择：

   - 文档或 agent-only：`git diff --check`；
   - Go 或行为改动：聚焦测试、`go test ./... -count=1`、`go vet ./...`、`gofmt -l .`，必要时 `sh scripts/build.sh`；
   - 涉及 e2e 覆盖的契约：`bash e2e/run.sh`；
   - 共享、取数、媒体解析、CLI、SDK：补跑 `go test -race ./... -count=1`。

   未运行真实 FxTwitter 或自建实例检查时，说明原因和剩余风险。
5. 需要让 CI 代跑可复现的验证命令时，在验证步骤里声明恰好一个 ```commands fenced block；命令须在 `tools/verification/command-whitelist.txt` 白名单内（`nitter` 的 `config`/`watch`/`seen`/`update` 与 `--instance`/`--proxy` 被策略拒绝），不会执行 shell。声明无效会让 `PR commands gate` 失败。

## 创建或更新 PR

1. 获得授权后先 push 当前分支，不让 `gh pr create` 隐式 fork 或 push：

   ```bash
   git push --set-upstream origin <branch>
   ```

2. 非交互创建时，把填好的模板写入临时文件，再传给 `--body-file`：

   ```bash
   gh pr create --base main --head <owner>:<branch> --title "<focused title>" --body-file <prepared-body>
   ```

   只有用户明确指定时才添加 reviewer、draft、label、assignee 或 project。
3. 创建或更新后复查模板与脱敏情况：

   ```bash
   gh pr view <number> --json number,title,body,baseRefName,headRefName,url
   gh pr checks <number>
   ```

4. 用 `nitter-cli-ci` 查看 required checks，用 `nitter-cli-review` 处理审查。不要自动 merge；merge 需要单独授权。

## 结果报告

报告 PR URL/编号、head/base/ref、模板是否完整、验证命令和结果、required checks 状态及剩余风险。分别陈述“已创建”“CI 已通过”“已获 review”和“已合并”，不要互相推断。
