# Creator circles (`nitter circle`)

## Two files, two schemas

- `~/.nitter-cli/circles.toml` — the roster (machine-owned): circle → member handles.
  The **section key keeps its case** (`circle add MyCircle …` stores `MyCircle`);
  lookup falls back case-insensitively in four steps: exact key → exact Name →
  lowercased key → lowercased Name.
- `~/.nitter-cli/profiles.toml` — the profile cache (split ownership): machine-fetched
  facts (`name`, `bio`, `followers_count`, `fetched_at`), the machine-managed
  `handle`, and judgement you record
  (`role`, `note`, `noted_at`). **Its keys are always lowercase**: a hand-written
  mixed-case key is normalized on load and the file is **never silently rewritten** —
  but the next `refresh`/`add` that writes this file stores lowercase keys.

## Privacy boundary

- `circle refresh <NAME>` and `circle add` (when it actually adds a new member) fetch
  straight from the **FxTwitter fast lane** (`api.fxtwitter.com`), completely bypassing
  `--instance` and your instance/basic-auth config: their profile fetches have **no
  instance path**.
- `circle suggest`: **all of its profile fetches** (the seed probe and the retweet-only
  candidate enrichment) plus its **following list** also go to the FxTwitter fast lane
  (bypassing `--instance`). **Only its timeline lane** is affected by `fetch_backend`,
  and that lane **is** pinned to an instance by `--instance`.
- `circle run`: the default `fetch_backend = mix` is **fx-first** — every member's
  handle goes to `api.fxtwitter.com` **first**, and instances are only the fallback.
  **Do not assume it uses your own instance.**
- `--instance URL` forces the instance path for that one call (overriding even
  `fetch_backend = fx`). Inside `circle` that affects the **timeline lane of
  `circle run` and `circle suggest`**;
  `refresh`/`add` are **completely unaffected**.
- `circle show` / `circle list` are **local-only** — zero network.

## Easily-confused semantics

- `--min-followers`: on `show` it filters **rows**; on `suggest` it filters only the
  **top-matches section** (absent in `--json` mode, where the flag has no effect).
- `circle add` on an existing member: the **member set is unchanged** and nothing
  touches the **network**, yet the success line still prints — and `circles.toml`
  is still rewritten in its canonical `[circles.<key>]` layout.
- `circle refresh` writes fact fields only — it **never** touches `role` / `note` /
  `noted_at`.
- `circle remove` edits only the roster; the profile record (your judgement included)
  is kept.
- `circle run` has **snapshot semantics**: no dedup, no incremental state — use
  `watch` for incremental tracking.
- A corrupt `profiles.toml` is **never silently reset or rewritten**; `show`/`refresh`
  error out, `add` only warns.
- `--media-type` is validated before any network (invalid → exit 2).

## Authoritative source

- The flag surface is `nitter circle <sub> --help`; this file deliberately does not
  copy the flag table.
