---
title: PagePop 1.2 upgrade
description: PagePop fork security build and migration constraints for OpenSandbox 1.2.
---

# PagePop 1.2 upgrade

This fork starts from upstream `release-1.2.0` (`62a64f0b48cdd6b410f36e53c74dc7559c87719a`). Historical migration and managed-process branches are not included. Verify the source and API surface of any existing custom execd artifact before replacing it; one consumer repository cannot prove that every custom API is unused.

## Security builds

The execd, egress, ingress and controller builds use digest-pinned Go 1.26.10 images. Runtime images have explicit distribution digests and a minimum repaired CA package. The egress Python TLS client pins certifi 2026.10.23; the server must be installed from its frozen lock, which pins the same certifi release. Rebuilding from an older lock can undo a host's manual CA repair.

Run `go test .` in `components/internal` with the release toolchain. The offline regression trusts historical roots deliberately: all 14 incident chains must still be rejected, while an ordinary valid certificate remains accepted. Package version checks and this compiler test do not substitute for testing each final image with its actual system, Python and Go TLS clients.

## Migration and activation

Compare existing objects with the new CRD schemas before updating CRDs, then the controller and producers. Validate persistence migration and recovery before switching the server. Provider, network, authentication, process-management and storage changes need their own migration validation; do not silently enable optional 1.2 features as part of a version update.

Confirm effective server authentication and caller credentials without exposing secrets. Do not extend a test-only insecure startup exception to production.

Consumers of execd 1.2 receive text records with their original line terminators and final fragment. Consumers that add missing line terminators must adapt before activation. Validate stdout/stderr, empty lines, CR/CRLF, unterminated output, diagnostic framing, cancellation, non-replay, file transfer, TTL, retained-instance reuse and volume persistence before activation.

The Go SDK fork module version must be published and resolvable before changing a consumer's committed dependency. An immutable Go pseudo-version resolving to an already published fork commit is suitable for a test candidate; record its full source SHA and module checksums. A temporary local module replacement is only a compatibility test and must not enter production source.

No image push, cluster update, host restart or live sandbox creation is part of local validation. Test and production activation require their own authorized release steps. Restore only artifacts that preserve the repaired CA boundary if recovery is required.
