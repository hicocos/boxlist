# Image derivatives

This package manages generated image previews without replacing source files. It owns the persistent job queue, rules, local output files, optional S3 output, and preview metadata.

## Integration

- Initialize through the native bootstrap lifecycle; start workers only after storage initialization.
- Use the manager's APIs for generation, lookup and cleanup. Do not edit queue rows or output files directly.
- Serve previews through authenticated native routes so source permissions remain enforced.
- Use one backend process per database and data directory. Persistent jobs provide restart recovery, not distributed worker coordination.

For administrator configuration, supported sources and operational limits, see [image preview operations](../../../doc/thumb-operations.md) and [S3 output](../../../doc/derivative-s3.md).

## Development checks

Run from the `backend` directory:

```sh
go test ./internal/derivatives ./server/handles ./server
```

The tests cover source identity, permissions, queue transitions, resource budgets, concurrency, storage failures and restart recovery. Native encoder integration also requires libvips with WebP support; see the Dockerfile for the runtime dependency.
