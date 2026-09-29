# v0.9.0 — 2026-09-29

Adds the `download --clean-temp` maintenance mode for pruning aged temporary downloads and emptied subdirectories, documents the circle reference and troubleshooting guidance, and pins circle error exit contracts.

## Added

- **`nitter download --clean-temp <DIR> [--older-than DURATION]`**: an explicit maintenance mode that removes regular files older than the window from a named directory (default `168h`; `0s` removes everything) and prunes the subdirectories it emptied. It never downloads, never reads stdin, and never creates directories. Guardrails refuse root directories (`/`, drive roots such as `C:\`, and UNC share roots) and symlink/junction roots with usage errors (exit 2) or refusal (exit 1), and skip symlinks inside the tree without failing the run. Conflicts with REFs or download-only flags exit 2.
  ([#20](https://github.com/shitianyaa/nitter-cli/pull/20))

## Changed

- **Circle reference routing and troubleshooting guidance**: added dedicated documentation for creator circle operations (`skills/nitter-cli/references/circle.md`), added circle-specific warnings and recovery rows to the troubleshooting guide, and pinned the circle exit code contracts for invalid handles and execution failures.
  ([#19](https://github.com/shitianyaa/nitter-cli/pull/19))

**Full Changelog**: [v0.8.2...v0.9.0](https://github.com/shitianyaa/nitter-cli/compare/v0.8.2...v0.9.0)
