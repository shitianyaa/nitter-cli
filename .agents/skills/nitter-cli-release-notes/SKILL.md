---
name: nitter-cli-release-notes
description: Maintain nitter-cli bilingual changelog entries and run the release flow (version selection, changelog/vX.Y.Z, immutable tag, GitHub Release). Use for release preparation, changelog writing, or publishing a version.
---

# nitter-cli Release Notes 与发布

普通 PR 使用 `nitter-cli-pr`。本 skill 负责双语版本说明与发布流程。先读 `AGENTS.md`、
`docs/maintainers/development.md`、`changelog/README.md` 和 `.github/workflows/release.yml`。

## 授权与不可变边界

- 检查、审计、preview 和 review 默认只读。
- 写入 `changelog/`、创建或推送 tag、创建或编辑 Release、修改已有 Release body 前，要取得用户对该动作的明确授权。
- 发布授权必须明确给出版本与 tag 范围；版本建议不等于授权。
- `.github/workflows/release.yml` 由 `push.tags: v*` 触发；`workflow_dispatch` 只用于对已授权的既有 tag 做恢复（`inputs.release_tag`，tag 必须在默认分支祖先链上）。不要移动旧 tag，也不要用默认分支内容覆盖不可变 tag。
- 构建矩阵通过后，`approve_release` 会停在 `release-approval` environment 等维护者批准；批准是发布授权的一部分，agent 不得代替。

## Changelog 结构

- `changelog/unreleased/{en,zh-CN}.md` 是可选人工草稿区；release workflow 不读取它。
- 发布版本使用 `changelog/vX.Y.Z/{en.md,zh-CN.md}`，英文与简体中文必须覆盖同一组来源。
- 章节遵循 Keep a Changelog 1.1.0：`Added`、`Changed`、`Deprecated`、`Removed`、`Fixed`、`Security`；空小节省略。
- 每个条目描述结果并附真实来源（PR 号或 commit hash）；没有用户可见影响的内部改动不写条目。
- 同步更新 `changelog/README.md` 的版本入口表（含 compare 链接）。

## 发布准备流程

1. 确认候选范围：上一个 tag 到目标 commit 之间的 merge/direct commit。

   ```bash
   git log --oneline vPREVIOUS..HEAD
   git log --merges --oneline vPREVIOUS..HEAD
   ```

2. 逐项检查来源，结合兼容性决定版本号（SemVer：破坏性 → major，新增能力 → minor，仅修复 → patch；0.x 阶段按实际影响判断）。
3. 获得精确版本与写入授权后，创建 `changelog/vX.Y.Z/en.md` 与 `zh-CN.md`，内容从 `unreleased/` 迁移并按版本语义整理。
4. 更新 `changelog/README.md` 入口表，然后校验：

   ```bash
   go test ./... -count=1
   git diff --check
   ```

5. 对齐版本号：`skills/nitter-cli/SKILL.md` 的 `version` 字段必须与 release 版本一致。
6. 提交 release-prep PR（使用普通三段式模板，不附带机器可读 metadata），required checks 通过后合并。

## Tag 与发布

1. release-prep 合并且默认分支包含 `changelog/vX.Y.Z/` 后，只创建用户已授权的精确 tag：

   ```bash
   git tag -a vX.Y.Z -m "nitter-cli vX.Y.Z"
   git push origin vX.Y.Z
   ```

   不要重写已有 tag。
2. push tag 后由 `release.yml` 自动执行：SemVer 校验 → 双语 changelog 软检查 → 6 平台构建（darwin/linux/windows × amd64/arm64）→ 合并 checksums → 创建草稿 Release。
3. 用 `nitter-cli-ci` 查看对应 run 与 job；验收至少包括：

   - 6 平台压缩包与 `checksums.txt` 属于同一 tag；
   - 草稿 Release 正文填入 `changelog/vX.Y.Z/` 的双语说明（英文在前、中文在后）后发布；
   - `nitter --version` 输出新版本号；
   - 工作流中构建的 `go test` 在全部 6 个平台通过。

失败时保留原始 run/step 错误并暂停，不用手工资产或默认分支内容绕过 gate。

## 结果报告

报告版本、tag 范围、修改的 changelog 文件、PR/Run/Release 链接、验证结果与未运行的真实环境检查。分别陈述候选版本、CI、公开 Release 的状态，不要互相推断。
