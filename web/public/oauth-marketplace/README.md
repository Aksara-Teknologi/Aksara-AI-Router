# Local OAuth marketplace source

This directory is served by the modified New API host at `/oauth-marketplace/`.
In Task Plugins → Marketplace → Manage sources, add the current site's origin
followed by `/oauth-marketplace/index.json`. No dashboard domain is compiled in.

The included OAuth Task Example is an installable contract example. Its
`provider.example` URLs and client ID are explicit placeholders. It is not a
working Codex, Antigravity, or Anthropic connector. Replace the provider metadata
and job adapter with your registered provider integration before using it.
After changing source, increment its version and update the SHA-256 in the index.

Provider OAuth credentials are managed by the host, not by this static source.
See `docs/plugin-api/v1.md` in the source repository for configuration and the
`oauth2-pkce@1` contract. A marketplace entry cannot bypass a provider's client
registration or redirect URI rules.
