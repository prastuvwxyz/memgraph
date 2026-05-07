# Roadmap

Planned improvements for memgraph. Contributions welcome — see [CONTRIBUTING.md](CONTRIBUTING.md).

## Near-term

- **Homebrew tap** — `brew install prastuvwxyz/tap/memgraph`
- **`memgraph export`** — export index to JSON/CSV for external tooling
- **`memgraph watch`** — live re-index on file changes (fsnotify-based)
- **`--tag` filter on `similar`** — mirror the tag filter now available on `query`
- **`memgraph lint --min-links N`** — flag files with fewer than N outbound links

## Medium-term

- **Namespace stats breakdown** — `memgraph stats --ns` shows per-namespace file and chunk counts
- **`memgraph diff`** — show what changed in the index since last run
- **MCP server mode** — expose memgraph as an MCP tool for Claude and other AI agents
- **Tag autocomplete** — shell completions for `--tag` flag based on indexed tags

## Long-term

- **Multi-vault federation** — query across multiple `.memgraph` indexes in one command
- **Web UI improvements** — filter panel, tag browser, and timeline view in `memgraph serve`
- **Plugin system** — custom scorers and output formatters via Go plugins
