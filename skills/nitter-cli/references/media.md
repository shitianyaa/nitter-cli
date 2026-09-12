# Media: resolution strategies, quality and delivery

`nitter media <REF>...` resolves status references into directly
downloadable media links (video mp4 variants, original images, GIFs). The
command resolves; downloading is the caller's job. Semantics are governed by
the installed binary's `nitter media --help`.

## References and batching

- REF shapes (the same as `nitter get`): a bare numeric status ID, or a
  status URL — `x.com`, `twitter.com` or any Nitter instance, shape
  `/<user>/status/<id>`; `/photo/N` and `/video/1` suffixes are accepted.
- Multiple REFs run as one batch, streamed in ref order under `--ndjson`.
  With no positional REF and a non-TTY stdin, refs are read from stdin, one
  per non-empty line (`nitter media < refs.txt`). Passing refs both as
  arguments and on stdin is an ambiguity error (exit 2).
- A ref that fails — including a status with **no media**, classified
  `not_found` — produces an in-place error report (a `kind:"error"` envelope
  with `meta.input` = the raw ref, or a stderr line) and the remaining refs
  continue. Partial failure exits 1 with a
  `media completed with N of M refs failed` summary.
- Bad input (unknown `--strategy`/`--quality`, a malformed ref, refs both
  ways, `--json --ndjson`) exits 2 before any network request.

## Strategies (`--strategy`)

`auto` (default) tries, in order: **fx → vx → syndication → nitter → xdown**
and returns the first strategy that yields media; its name is stamped in
`source`. A strategy whose response parses but carries no media counts as
"empty" and the chain moves on; only when every strategy is exhausted does
the ref fail — `not_found` when they all reported empty (the status has no
media), otherwise the last failure's kind with each attempt named in the
message.

| Strategy | Backend | Where the tweet URL goes |
| --- | --- | --- |
| `fx` | api.fxtwitter.com JSON | third-party service |
| `vx` | api.vxtwitter.com JSON | third-party service |
| `syndication` | cdn.syndication.twimg.com tweet-result | third-party service |
| `nitter` | your own instance's status page | your instance only |
| `xdown` | xdown.app ajaxSearch (POST) | third-party service |

Trust boundary: fx/vx/syndication/xdown are third-party public services that
receive the tweet URL — resolve only public statuses the user is fine
sharing; never feed them private or sensitive links. `nitter` keeps
everything on your own infrastructure and is the only strategy whose links
may be plain http (the instance's own media-proxy links are kept as-is);
the other strategies project https-only links and drop plain-http ones.

An explicit strategy name runs only that one — use it to diagnose which
backend a failure comes from, or when the user objects to the URL leaving
their infrastructure (`--strategy nitter`). The nitter strategy reports a
clear local-state failure when no instance is configured; the auto chain
reports that honestly in its aggregate instead of skipping it silently.

## Quality (`--quality`) and variants

- Images: the pbs URL is rewritten to the tier — `high` → `name=orig`,
  `medium` → `name=large`, `low` → `name=small`.
- Video/GIF: `high` = highest-bitrate variant (ties resolve to the later
  variant), `medium` = upper median of the non-zero bitrates (a two-tier
  video behaves like `high`), `low` = lowest non-zero bitrate. Only the main
  URL changes; every variant stays in `variants` (upstream order,
  https-only).
- When quality lowers a video's main URL below its best variant, the best
  one is kept as `fallback_url`.

## `--probe`

Adds one ranged GET per video/gif URL (a 1 MiB head window): the file size
comes from the `Content-Range` total and the duration from the mp4 movie
header (`mvhd`). Best-effort by contract: any failure leaves
`duration_seconds`/`size_bytes` empty and never fails the run; a
source-reported duration is never overwritten. Images are not probed.

## Output shapes

- Default text: one tab-separated row per entry —
  `<ref> <source> <kind> <url> <label> <duration> <size>`, with `-` for
  absent trailing cells.
- `--json`: a single object when exactly one entry resolved, an array
  otherwise, `[]` when nothing resolved. Fields `ref`, `source`, `kind`,
  `url` are always present; `fallback_url`, `label`, `width`, `height`,
  `duration_seconds`, `size_bytes`, `variants` appear only when non-empty.
- `--ndjson`: one `nitter.pipeline/v1` envelope per entry (`kind:"media"`,
  `id` = the download URL, `meta.input` = the raw ref) plus one
  `kind:"error"` envelope per failed ref. A consumer closing the stream
  early (EPIPE) is a graceful exit 0.

## Downloading and delivering (agent checklist)

- Every projected URL is a direct https link — fetch it with a plain GET
  (curl, wget, or the host's HTTP client). No cookies, no sign-in, no
  special headers.
- If the main URL fails, retry `fallback_url`, then the other entries in
  `variants`, before giving up.
- Use `--probe` to check `size_bytes` before pulling a large video; a
  tens-of-MB mp4 is normal — let the download finish instead of inventing a
  timeout.
- Deliver downloaded files through the host attachment API. If the host
  cannot attach files, share the resolved URL only and never claim the
  media itself was sent.
- Remember the trust boundary above: unless the run was `--strategy nitter`
  only, third-party services saw the tweet URL.

## Exit codes

| Exit code | When |
| --- | --- |
| 0 | All refs resolved; also when the consumer closed the stdout pipe early (EPIPE) |
| 1 | At least one ref failed (error reports in-stream/stderr, remaining refs continue, summary on stderr) |
| 2 | Usage error, no network: unknown `--strategy`/`--quality`, malformed refs, refs both as arguments and on stdin, no refs, `--json --ndjson` together |
