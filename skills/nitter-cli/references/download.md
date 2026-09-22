# Download: selection, on-disk behavior and the pipeline

`nitter download <REF>...` resolves status references with the media
command's strategies and writes the planned media files to disk — the write
half `nitter media` deliberately leaves out. It is the skill's only Disk
write command: files land in the user's filesystem on every run. Semantics
are governed by the installed binary's `nitter download --help`.

## Pre-download checklist (agent)

1. State the target directory (`--output DIR`, else the `download_path`
   config key, default `./nitter-media`, cwd-relative) and the exact refs to
   the user and get consent for THIS invocation — disk writes need consent
   every time, authorization never carries over.
2. Check the trust boundary below: unless the run is `--strategy nitter`,
   resolving AND downloading sends the tweet URL through third-party public
   services — public statuses only.
3. Ask what should happen when a file already exists; the default
   `--on-exists refuse` errors instead of replacing. Never pass
   `overwrite`/`skip` unprompted.
4. Expect the network path to matter: downloads ride the configured proxy
   (`--proxy` / config `proxy`) like every other fetch of this CLI — twimg
   hosts may be unreachable directly on restricted networks.
5. Verify flags against `nitter download --help`; do not invent any.

## References, batching and stdin

- REF shapes (the same as `nitter get` and `nitter media`): a bare numeric
  status ID, or a status URL — `x.com`, `twitter.com` or any Nitter instance,
  shape `/<user>/status/<id>`; `/photo/N` and `/video/1` suffixes are
  accepted.
- Multiple REFs run as one batch. With no positional REF and a non-TTY
  stdin, the input is read from stdin and the first non-whitespace byte
  decides the mode: `{` starts strict envelope mode (every non-empty line
  must be a `nitter.pipeline/v1` tweet envelope and each record's `data.url`
  is used as the REF — a malformed envelope is a usage error, exit 2, never
  a skipped line); anything else is plain refs, one per non-empty line.
  Giving refs both as arguments and on stdin is an ambiguity error (exit 2).
- The same filename can be planned twice in one batch (duplicate refs, or
  two refs resolving to one status): the second occurrence meets the file
  the first wrote, so the `--on-exists` mode decides — under `refuse` the
  duplicate fails with the exists error and the batch continues.
- A ref that fails (resolution failure, no media, `--kind cover` on an
  image-only status) produces an in-place error report (a `kind:"error"`
  envelope with `meta.input` = the raw ref, or a stderr line) and the
  remaining refs continue.

## Selection: the video-wins rule

- A status carrying video or GIF downloads its ONE best video file, never
  the images next to it. Empirical basis: a video tweet resolved via xdown
  comes back as several bitrate entries plus a cover image — a consumer that
  "downloads everything" would pull the same video repeatedly in every
  quality, so the plan converges the candidates into one file. The winner is
  ranked over the candidates' bitrates (or xdown's p-numbers) with the exact
  `--quality` semantics of the media command (high = highest, medium = upper
  median non-zero, low = lowest non-zero).
- The winner keeps the remaining candidates as its fallback chain: the
  winner's own fallback link first, then the other candidates in descending
  rank order. The first URL that fails falls through the fallbacks in order,
  and the row reports whichever candidate actually succeeded.
- An image-only status downloads every image (a tweet carries at most four):
  `<id>-1.jpg ... <id>-4.jpg`, one file each.
- `--kind image|video|gif` is a pre-filter applied before those rules;
  `--kind cover` is its own selection mode — it plans exactly the video's
  cover image. An image-only status has no cover and reports `not_found`;
  a video without a captured cover is refused the same way.
- A filter matching nothing yields no files, not an error: the command
  prints `nothing found for <ref>` on stderr and does not count a failure.
- Playlists (HLS `.m3u8`, DASH `.mpd`) are never download candidates — a
  status whose every video encoding is a playlist reports `not_found`
  instead of silently substituting something else.

## Filenames and extensions

- Regular files: `<status-id>-<seq>.<ext>` (seq 1..N); the cover:
  `<status-id>-cover.<ext>`. The status ID comes from the ref, so repeated
  downloads of the same status target the same filenames.
- The extension comes from the resolved URL path when it carries one,
  otherwise from the download response's `Content-Type` (`image/jpeg`→
  `.jpg`, `image/png`→`.png`, `image/webp`→`.webp`, `image/gif`→`.gif`,
  `video/mp4`→`.mp4`), falling back to a kind-based default (`.jpg` for
  images and covers, `.mp4` for videos and GIFs). The extension is decided
  at download time — an m3u8/HLS URL is never planned, so a planned file is
  always a real media file.

## Naming templates

`filename_template` (config key, default `{id}-{seq}` — byte-identical to
the plain naming above) and `--filename-template` (flag, overrides the
config per invocation) render every regular file's name. Placeholders:
`{id}` the status id, `{seq}` the file's 1-based position in the plan,
`{user}` the ref's user segment as given (empty for a bare ID; the
user-less `/i/status/<id>` route reports `i`), `{kind}`
image/video/gif/cover, `{ext}` the planned extension with its leading dot
(empty when the plan carries none — the response's `Content-Type` then
decides at download time and the extension is appended after the final
rendered name; a template without `{ext}` gets the extension appended at
the end).

- Covers ignore the filename template BY DESIGN: `--kind cover` always
  lands as `<id>-cover.<ext>`, whatever the template says.
- `directory_template` (config key only, no flag; default empty = flat in
  the output directory root) renders each file's subdirectory from
  `{id}`/`{user}`/`{kind}`. `{seq}` and `{ext}` are forbidden in the
  directory position; `/` separates levels and empty levels are skipped
  (an empty `{user}` contribution disappears). Subdirectories are created
  on demand like the output root itself.
- Rendered names are sanitized: Windows-illegal characters (`\ / : * ? " <
  > |`, control characters) become `_`, and a directory level that renders
  to `.` or `..` is rejected — nothing escapes the output directory.
- An invalid template (unknown or malformed placeholder, forbidden
  placeholder in the directory position, path separator in the filename
  position) never fails the run: ONE stderr `warning:` line names the
  template and the default template (filenames) or flat (directory)
  applies instead. An empty template value IS the default, silently.
- Two planned files of ONE ref that render the same name collide: the
  later one gets a numeric `-2`, `-3`, ... suffix before the extension
  (at the end of the name when the extension is decided at download time)
  plus a warning. Across refs nothing changes: duplicate refs meet the
  same filenames and `--on-exists` decides.

## Output directory and `--on-exists`

- The output directory is `--output DIR`, else the `download_path` config
  key (default `./nitter-media`, relative paths resolve against the working
  directory); it is created on demand (`mkdir -p`).
- `--on-exists` decides what happens when a target file is already on disk
  (default `refuse`):
  - `refuse` — the entry is reported as an error and the batch continues.
  - `skip` — the existing file is kept; its row is reported with the file's
    actual on-disk size and NO sha256 (nothing was re-downloaded, nothing is
    fabricated), and it is never counted as a failure.
  - `overwrite` — the file is re-downloaded through the same atomic
    temp-then-rename flow as any first download.

## Strategies and the privacy boundary

`--strategy` (default `auto`) is the media command's chain: auto tries
fx → nitter → xdown and the first strategy that yields
media wins (`source` stamps which one). For download the trust boundary is
stricter than for `media`, because the fetch itself happens too:

- fx/xdown are THIRD-PARTY public services — resolving AND
  downloading sends the tweet URL through them, so only use them for public
  statuses the user is fine sharing.
- `--strategy nitter` is the fully private path: resolution and download
  both stay on the user's own instance (`[[instances]]` first entry, or
  `--instance`). Its direct links may be plain `http://` links served by the
  user's own instance and are trusted for that.

Download requests ride the configured proxy (`--proxy` / config `proxy`)
like every other fetch of this CLI.

## Reporting: rows, JSON, envelopes

- Default text: one tab-separated row per downloaded file —
  `<ref> <path> <bytes> <kind> <source>`; `path` is the absolute file path
  (also the NDJSON envelope's `id`), `kind` is image/video/gif/cover. Under
  `--on-exists skip` the path cell reads `<path> (skipped)`. An invocation
  that plans and downloads nothing prints `(empty)` on stderr.
- `--json`: the downloaded files as one JSON document — a single object when
  exactly one file, an array otherwise, `[]` when none.
- `--ndjson`: one `nitter.pipeline/v1` envelope per file as it lands
  (`kind:"download"`, `id` = the absolute path, `data` = the record:
  `ref`/`path`/`kind`/`source`/`url`/`bytes`/`sha256`, `meta.input` = the
  raw ref) plus one `kind:"error"` envelope per failed ref (`data.command`
  is `download`, `data.stage` is `resolve`, `plan` or `download`). A skip
  row's envelope carries the on-disk `bytes`, an empty `url` and no
  `sha256`. A consumer closing the stream early (head/tail on a pipe) is a
  graceful exit 0.

## Pipeline example

`watch` and `get` NDJSON streams feed download directly — the tweet
envelopes' `data.url` becomes each ref:

```bash
nitter watch user:NASA --once --ndjson | nitter download --ndjson
```

Inspect the records before piping them in: the stream may carry
`kind:"error"` envelopes (they are NOT valid input — envelope mode demands
tweet envelopes only, a malformed line is a usage error) and tweets you did
not intend to download. A safe form writes the watch stream to a file,
checks it (`jq -c 'select(.kind=="tweet")'` when jq exists), then feeds the
file. Plain ref files work too: `nitter download < refs.txt --ndjson`.

## Exit codes

| Exit code | When |
| --- | --- |
| 0 | Every ref resolved and every planned file downloaded (skips included); also when the consumer closed the stdout pipe early (EPIPE) |
| 1 | At least one ref failed (error reports in-stream/stderr, remaining refs continue, `download completed with N of M refs failed` summary on stderr); a filter matching nothing is NOT a failure |
| 2 | Usage error, no network: unknown `--kind`/`--quality`/`--strategy`/`--on-exists`, bad or missing refs, refs both as arguments and on stdin, malformed stdin envelopes, `--json --ndjson` together |
