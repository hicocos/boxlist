# Native icon library

`New(absoluteRoot, Hooks)` returns a `*Service` implementing `http.Handler`;
`Close()` releases its SQLite connection and exclusive native-root lock.
`Fault` implements `error` with a **value receiver** (values and pointers work).
`HeadReferences(head)` and `ConvertPNG(bytes)` are exported for testing/reuse.

All three hooks are mandatory. Production must use real native authorization,
fresh database reads of `customize_head`, and exact trusted Origin comparison.
The module itself additionally requires nonempty, single-value Authorization and
Origin, rechecks the identity before commit, and rejects invalid user IDs.
There are no production fixture credentials or auth-bypass environment flags.

## HTTP contract

* GET `/icon/api/health`: `{code:200,data:{version:1}}`
* GET `/icon/api/catalog`: version 1, active `assets`, sorted `hidden_builtins`.
* POST `/icon/api/upload`: raw `application/octet-stream`, explicit Content-Length,
  `X-Upload-Id` (32 lowercase hex), percent-encoded `X-Icon-Name` ending in `.png`.
  Returns `{code:200,data:{asset,reused}}`.
* DELETE `/icon/api/assets/{id}`: no body; returns id/kind/removed.
* GET `/icon/assets/upload-{32 lowercase hex}.webp`: real lossless WebP,
  immutable cache policy, nosniff. Only committed, nondeleted rows are public.

Only GET is public; HEAD, OPTIONS and other methods are rejected. Queries,
encoded aliases, traversal, unknown paths and all private files get JSON errors,
never SPA HTML. The handler does not implement cross-origin CORS.

PNG validation bounds input at 1–5 MiB, dimensions at 4096, pixels at 16 million;
checks framing/CRC/IHDR/IDAT/IEND and full `image/png` decode; rejects APNG control
chunks including single-frame APNG, renamed formats and trailing bytes. Preview
pixels are centered on a transparent 256-square canvas, fit proportionally within
232-square without upscaling, with Lanczos resizing and bounded EXIF orientation.
The pinned MIT `github.com/HugoSmits86/nativewebp v1.3.0` encoder is pure Go VP8L:
no Python, libwebp, subprocess, runtime codec binary or CGO is required.

## Durability and Python migration

The SQLite `assets` and `hidden_builtins` columns, timestamps, ID derivation and
original/preview/trash layout match the Python implementation. Old recognized
Python schemas missing only `deleted_at` are upgraded. Preserve the **whole root**
with a consistent SQLite backup/checkpoint or while the Python process is stopped;
never copy a live `catalog.db` alone and discard its WAL. Stop Python before opening
this root with Go: Python does not participate in `.native.lock`. Do not run both
writers against one root. No migration of production data is performed here.

Startup validates known schema/constraints/rows before narrowly cleaning orphan
uploads and staging files. Unknown schema, unexpected triggers, unknown hidden IDs,
a nonempty file tree without a catalog, unsafe paths and capacity overages fail
closed. Existing directories must be owned by the running user and not writable
by group/others. Root/subdirectories are 0700; private files are 0600, no-follow.
Changing container UID requires deliberate ownership migration first.

Fsynced private staged files are exclusively installed and directory-fsynced before
a FULL-synchronous WAL commit. Uncertain commits retain files for original-key
retry/recovery, never compensating-delete potentially committed material. IDs are
SHA-256(user ID + ':' + request ID), first 32 lowercase hex, with `upload-` prefix.
Same-key retries require the original bytes **and original decoded name**;
changed payload/name yields 409, identical deleted retry yields 410.

Deletion verifies fresh head twice, refuses any known saved reference, and fails
closed on unknown/malformed configuration or hook errors. Local mutations serialize;
pending delete rejects same-ID retry; a second simultaneous delete gets 429.
Uploaded deletion commits a tombstone before moving recoverable private files;
restart completes interrupted moves without exposing the deleted row. Builtin
deletion only records one of the exact 21 hidden IDs, never removes frontend files.
Trash is not purged. All retained rows count toward the 200-item limit and original
**plus preview** bytes count toward 256 MiB (stricter than Python's original-only
byte accounting). Migration of a Python catalog near that bound may be refused.

Resizing/encoding is not byte-identical to Pillow/libwebp. Previously stored WebPs
remain untouched and their immutable IDs keep the same URL filename. New previews
are lossless Go output. Legacy parsing is intentionally stricter when extra legacy
markers/assignments occur outside managed blocks.

## Integration limitations

Local head-read + deletion is not atomic with independent backend settings writes;
external clients can race after the last check. Browser Web Locks only coordinate
participating same-origin clients. Immutable cached previews can remain visible
after origin deletion; bypass cache when verifying the origin's 404.

A handler cannot inspect raw duplicate Host or identical duplicate Content-Length
headers already normalized/accepted by Go's `net/http`, nor retroactively bound
header parsing. The embedding server/reverse proxy must enforce request/header
size and absolute deadlines and reject ambiguous raw framing. This handler rejects
duplicate sensitive headers still represented in `Request.Header`, transfer
encoding/chunked bodies, content encodings, oversized bodies and unknown-length
mutations. Receiving has a 20-second read deadline when the ResponseWriter supports
`http.ResponseController`; the embedding server should also set Header/Read/Write
and idle timeouts. Standalone HTTP/2/CORS server policy is not implemented here.

## Verification

Use Go 1.27 with `GOMAXPROCS=4`:

```
go test -race -count=1 -cover ./internal/iconlibrary
CGO_ENABLED=0 go test ./internal/iconlibrary
go vet ./internal/iconlibrary
CGO_ENABLED=0 go test ./server -run '^$'
```

Tests use real private disk, SQLite, WebP decode and an actual httptest socket with
explicitly isolated hooks. They do not claim live native administrator acceptance
or production deployment. No router or deployment modifications are owned here.
