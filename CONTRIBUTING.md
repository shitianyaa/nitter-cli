# Contributing to nitter-cli

English | [简体中文](CONTRIBUTING.zh-CN.md)

Thanks for helping improve `nitter-cli`. Focused bug reports, documentation fixes, tests, and well-scoped features are welcome.

## Before you start

- Search existing issues and pull requests before opening a duplicate.
- Discuss large features, public API changes, new dependencies, or changes to the fetch/credential model before implementation.
- Keep changes focused. Unrelated cleanup is easier to review as a separate pull request.

**Never include secrets or private data.** Issues, fixtures, commits, and CI logs must not carry instance basic-auth credentials, proxy credentials, `~/.nitter-cli/` contents (config, circles, seen state), or private instance URLs you do not want published.

## Development environment

The supported source build uses the Go version declared in `go.mod` and a standard Go toolchain; there are no C or native dependencies.

Build and test from the repository root:

```bash
go test ./...
sh scripts/build.sh
./nitter --version
```

The default verification is fully offline and needs no instances or credentials. See the [development guide (Simplified Chinese)](docs/maintainers/development.md) for the offline e2e gate, opt-in live checks, and platform details.

## Architecture guardrails

- `cmd/nitter` only delegates; `internal/cli/root.go` owns the command tree, streams, and composition. Command packages do not import `internal/cli`.
- Data acquisition in commands goes only through the top-level `sdk/` (package `nitter`). Do not import `internal/nitter/*`, `internal/fxtwitter`, or `internal/media` protocol details from a command package.
- `internal/cli/client` is the only CLI-layer package allowed to import `internal/{nitter/*,fxtwitter,media}` (R11).
- `internal/common` takes no `io.Writer` and carries no user copy; `internal/cli/result` encodes no JSON, builds no commands, and calls no SDK.
- Keep files focused on one responsibility or a few tightly related responsibilities.

Read [the architecture guide (Simplified Chinese)](docs/maintainers/architecture.md) and the repository [AGENTS.md](AGENTS.md) before changing these boundaries.

## Develop with tests

Use a red-green-refactor loop for code changes:

1. Add a focused test that fails for the intended behavioral reason.
2. Implement the smallest coherent change that makes it pass.
3. Refactor without changing the verified public behavior.
4. Run the focused tests, then the relevant regression suite.

Test public behavior through the public boundary whenever practical. Never hide a real fetch, parsing, filesystem, or network failure behind an empty success result or a silent fallback, and never add an unfounded fixed timeout, truncation, result cap, retry limit, or hidden downgrade.

Exit codes are part of the contract: an unknown subcommand exits 1, and an invalid argument, flag, or input contract value exits 2 through `invocation.UsageError`. Add the usage-error (exit 2) test when you add a flag.

Live fetches against third-party services (FxTwitter) and self-hosted Nitter instances are opt-in; never run them against an account or instance a user has not authorized.

## Documentation

Update documentation in the same pull request when changing a command, flag, SDK API, configuration key, environment variable, output contract, exit-code behavior, or known limitation.

- Keep `README.md` and `README.zh-CN.md` behaviorally aligned.
- Keep both locale versions under `docs/en/` and `docs/zh-CN/` behaviorally aligned; never use untranslated placeholder content.
- Maintainer docs under `docs/maintainers/` have one canonical version only.
- Update `docs/{en,zh-CN}/sdk.md` or `docs/maintainers/architecture.md` when an SDK signature or a package boundary changes.
- Release notes live in `changelog/` as matching English and Simplified Chinese files; user-visible changes get an entry, purely internal cleanups do not. See [changelog/README.md](changelog/README.md).
- Check `skills/nitter-cli/` when CLI commands, flags, or safety semantics change.

Keep stable rules in one authoritative document and link to them elsewhere instead of copying large sections. The full routing table is in [`docs/maintainers/agents/documentation-guidelines.md`](docs/maintainers/agents/documentation-guidelines.md).

## Pull request checklist

Before requesting review:

- [ ] The change is focused and its user-visible behavior is explained.
- [ ] New or changed code has focused tests that first demonstrated the failure.
- [ ] `go test ./... -count=1` passes.
- [ ] `go vet ./...` passes.
- [ ] `gofmt -l .` prints nothing.
- [ ] `sh scripts/build.sh` passes.
- [ ] `bash e2e/run.sh` passes when the change touches a contract it covers.
- [ ] `git diff --check` passes.
- [ ] English and Simplified Chinese documentation are synchronized where required.
- [ ] No credential, local state, or machine-specific artifact is included.

Conventional Commits are used for commit messages — one line, lowercase English subject, for example `feat(circle): add circle suggest` or `fix(media): derive file extensions from the decoded URL path`. The project does not require a CLA, DCO sign-off, or signed commits unless a future policy explicitly says otherwise.

## License

By contributing, you agree that your contribution may be distributed under the repository's [MIT License](LICENSE).
