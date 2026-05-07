# Roadmap

Planned improvements for memgraph. Contributions welcome — see [CONTRIBUTING.md](CONTRIBUTING.md).

## Near-term

- **Homebrew tap** — `brew install prastuvwxyz/tap/memgraph`
- **`memgraph watch`** — live re-index on file changes (fsnotify-based)
- **`--tag` filter on `similar`** — mirror the tag filter now available on `query`
- **`memgraph lint --min-links N`** — flag files with fewer than N outbound links
- **`memgraph export`** — export index to JSON/CSV for external tooling
- **Namespace stats breakdown** — `memgraph stats` shows per-namespace file and chunk counts

## Medium-term

- **`memgraph diff`** — show what changed in the index since the last run
- **Tag autocomplete** — shell completions for `--tag` flag based on indexed tags
- **Web UI improvements** — filter panel and tag browser in `memgraph serve`
- **MCP server mode** — expose memgraph as an MCP tool for AI agents (deferred until agent-native use cases outgrow the bash subprocess workaround)

## Long-term

- **Multi-vault federation** — query across multiple `.memgraph` indexes in one command
- **Plugin system** — custom scorers and output formatters via Go plugins

## Explicitly out of scope

- **LazyGraphRAG / cluster synthesis** — requires LLM API at query time, breaking the zero-dependency design. Revisit only if memgraph becomes a server-side component queried by agents.
