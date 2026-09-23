# 未发布

> 可选的人工草稿区。发布 workflow 不读取本文件；发布前请把定稿的双语条目移入目标
> `changelog/vX.Y.Z/` 目录。

## 新增

- **`nitter update` 可安装新版本**：显示版本对比后询问确认（`--confirm` 跳过询问；stdin 非终端时
  必须给出，因此绝不会阻塞管道；拒绝安装退出 0）。安装器只下载本平台归档，用发布的
  `checksums.txt` 校验 SHA-256，再校验暂存二进制报告的版本，全部通过后才替换可执行文件——
  任何校验失败都不会改动现有安装。`go install` 安装会被拒绝并给出对应的 `go install` 行而不做
  替换，避免后续安装静默抹掉更新。`--proxy`/`config.proxy` 现在对 update 的网络请求生效
  （此前不生效：检查用的是无代理的 client）。
- **圈子成员侧写（`nitter circle refresh`）**：成员元数据改存机器可读的侧写文件
  （`~/.nitter-cli/profiles.toml`），以 handle 为键，分为机器刷新的事实字段
  （`name`、`bio`、`followers_count`、`fetched_at`）与刷新永不覆盖的判断字段
  （`role`、`note`、`noted_at`）。`circle refresh <NAME>` 拉取每个成员的 profile 并合并事实字段；
  单个成员失败时 stderr 警告并继续，其原有事实保留不变。

## 变更

- **`nitter update` 不再只打印指引**：不带 `--check` 时它执行检查并提供安装。`nitter update --check`
  行为不变，仍是只读。
- **`nitter download` 报告输出目录**：在 stderr 输出一行 `note: writing to <dir>`（解析后的绝对路径），
  时机在目录创建之后、写入任何文件之前。stdout、`--json` 与 `--ndjson` 不受影响。
- **`nitter circle show` 改为纯本地**：与缓存的侧写文件 join，零网络请求；因此 `--min-followers`
  改为过滤**缓存**的粉丝数，不再逐个成员拉取 profile。无缓存的成员保留行并以 `-` 占位，
  同时在 stderr 给出一条指向 `circle refresh` 的提示；`--json` 行新增 `name`、`bio`、`fetched_at`、
  `role`、`note`。先跑一次 `nitter circle refresh <NAME>` 填充缓存。

## 弃用

## 移除

## 修复

## 安全
