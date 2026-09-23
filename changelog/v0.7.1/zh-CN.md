# v0.7.1 — 2026-09-23

自替换安装、缓存式圈子成员档案，以及验证两者时发现的问题修复。

## 新增

- **`nitter update` 可安装新版本**：显示版本对比后询问确认（`--confirm` 跳过询问；stdin 非终端时
  必须给出，因此绝不会阻塞管道；拒绝安装退出 0）。安装器只下载本平台归档，用发布的
  `checksums.txt` 校验 SHA-256，再校验暂存二进制报告的版本，全部通过后才替换可执行文件——
  任何校验失败都不会改动现有安装。`go install` 安装会被拒绝并给出对应的 `go install` 行而不做
  替换，避免后续安装静默抹掉更新。Windows 上旧二进制保留为 `<name>.exe.old`，由下次调用清理；
  Unix 上重命名是原子的，不留残留。`update --check` 仍为只读。
  ([13f13b4](https://github.com/shitianyaa/nitter-cli/commit/13f13b4))
- **圈子成员档案（`nitter circle refresh`）**：成员元数据改存机器可读的侧写文件
  （`~/.nitter-cli/profiles.toml`），以小写 handle 为键，分为机器刷新的事实字段
  （`name`、`bio`、`followers_count`、`fetched_at`）与刷新永不覆盖的判断字段
  （`role`、`note`、`noted_at`）。`circle refresh <NAME>` 拉取每个成员的 profile 并合并事实字段；
  单个成员失败时 stderr 警告并继续，其原有事实保留不变。
  ([#2](https://github.com/shitianyaa/nitter-cli/pull/2))

## 变更

- **`nitter update` 不再只打印指引**：不带 `--check` 时它执行检查并提供安装。`--proxy`/`config.proxy`
  现在对 update 的网络请求生效；两者都为空时会读取 `HTTPS_PROXY`/`ALL_PROXY`。
- **`nitter download` 报告输出目录**：在 stderr 输出一行 `note: writing to <dir>`（解析后的绝对路径），
  时机在目录创建之后、写入任何文件之前。stdout、`--json` 与 `--ndjson` 不受影响。
  ([7b5080a](https://github.com/shitianyaa/nitter-cli/commit/7b5080a))
- **`nitter circle show` 改为纯本地**：与缓存的侧写文件 join，零网络请求；因此 `--min-followers`
  改为过滤**缓存**的粉丝数，不再逐个成员拉取 profile。无缓存的成员保留行并以 `-` 占位，
  同时在 stderr 给出一条指向 `circle refresh` 的提示；`--json` 行新增 `name`、`bio`、`fetched_at`、
  `role`、`note`。先跑一次 `nitter circle refresh <NAME>` 填充缓存。
  ([08ad3a6](https://github.com/shitianyaa/nitter-cli/commit/08ad3a6))
- **代理文档与实测行为对齐**：环境变量 `HTTPS_PROXY`/`ALL_PROXY` 对 FxTwitter 快车道与 `update`
  生效，但**从不**作用于 nitter 传输——后者需用 `--proxy` 或配置 `proxy`。此前帮助文本暗示它们
  对所有路径都有效。([d9629ae](https://github.com/shitianyaa/nitter-cli/commit/d9629ae))

## 修复

- **`nitter update --check` 在大型发布页上失败**：响应上限原为 1 MiB，因此发布列表携带
  `assets[]` 的仓库（github.com/cli/cli 单页约 4.7 MB）会报
  `decode response: unexpected EOF`。现上限为 32 MiB——仍有界，但真实页面不再触及。
  ([eb924c8](https://github.com/shitianyaa/nitter-cli/commit/eb924c8))
- **Windows 上 `update` 的安装来源判定**：可执行文件路径被规范化（符号链接与 8.3 短名），
  而 `GOBIN`/`GOPATH` 条目仍按原样比较，导致 `go install` 装出的二进制可能被误判为归档安装，
  从而被自替换覆盖。现两侧统一规范化。
  ([0abcfce](https://github.com/shitianyaa/nitter-cli/commit/0abcfce))

**完整变更**：[v0.7.0...v0.7.1](https://github.com/shitianyaa/nitter-cli/compare/v0.7.0...v0.7.1)
