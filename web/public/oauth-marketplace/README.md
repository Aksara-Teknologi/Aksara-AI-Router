# Aksara OAuth marketplace source

This directory is served by the modified New API host at `/oauth-marketplace/`.
In Task Plugins → Marketplace → Manage sources, add the current site's origin
followed by `/oauth-marketplace/index.json`. No dashboard domain is compiled in.

The index contains Codex OAuth, Claude OAuth, and Antigravity OAuth. Their provider
endpoints and installed-client configuration are supplied by the patched host;
administrators do not enter provider URLs or paste provider tokens.

The Termux personal profile enables these providers and initial account slots
automatically. Other deployments can install the standalone JavaScript sources
from this index after upgrading their host. Run
`node scripts/sync-oauth-marketplace.mjs` from the repository root to regenerate
the standalone copies and hashes from `plugins/personal-tasks/`.

The separate `oauth-example` directory is only an API contract fixture and is not
listed in this marketplace. It is not used by the personal profile.

Provider OAuth credentials are managed by the host, not by this static source.
See `docs/aksara-termux.md` and `docs/plugin-api/v1.md` for configuration and the
`oauth2-pkce@1` contract. A marketplace entry cannot bypass a provider's client
registration or redirect URI rules.
