# memgraph

> File-based knowledge graph for AI agents — query your notes without a database server.

[![Go version](https://img.shields.io/github/go-mod/go-version/prastuvwxyz/memgraph)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/prastuvwxyz/memgraph)](https://github.com/prastuvwxyz/memgraph/releases)

---

```
$ memgraph query "openclaw setup"

Path                                    Score    Tags                  Reason
-------------------------------------------------------------------------- ----
knowledge/openclaw-access.md            0.9241   setup, agent          filename + tag match
memory/2026-03-28.md                    0.7812   openclaw, infra       body match: "openclaw setup"
project_openclaw_access.md              0.6530   openclaw              body match: "access"
```

---

## Features

- **Zero dependencies** — pure file-based SQLite index, no server to run or maintain
- **BM25 full-text search** with filename and tag scoring for relevance that actually makes sense
- **Incremental indexing** — content-hash diffing means only changed files get re-indexed
- **Context scopes** — isolate queries to named directories (work, personal, project)
- **Agent-friendly output** — `--format json` and `--format paths` pipe cleanly into AI pipelines
- **Link graph** — tracks wikilinks between files, exposes outbound links and backlinks per file
- **Gitignore-style exclusions** via `.memgraphignore` or config
- **Cross-platform** — macOS universal binary, Linux amd64/arm64, Windows (WSL recommended)

---

## Install

### One-liner (macOS + Linux)

```sh
curl -sSL https://raw.githubusercontent.com/prastuvwxyz/memgraph/main/install.sh | sh
```

Detects your OS and arch automatically, installs to `/usr/local/bin`. Override with `INSTALL_DIR`:

```sh
INSTALL_DIR=~/.local/bin curl -sSL https://raw.githubusercontent.com/prastuvwxyz/memgraph/main/install.sh | sh
```

### Go install

```sh
go install github.com/prastuvwxyz/memgraph@latest
```

### Manual binary download

Pre-built binaries on the [releases page](https://github.com/prastuvwxyz/memgraph/releases):

```sh
# macOS (universal — works on Apple Silicon + Intel)
curl -sSL https://github.com/prastuvwxyz/memgraph/releases/latest/download/memgraph_$(curl -s https://api.github.com/repos/prastuvwxyz/memgraph/releases/latest | grep tag_name | cut -d'"' -f4)_darwin_all.tar.gz | tar xz
sudo mv memgraph /usr/local/bin/

# Linux amd64
curl -sSL https://github.com/prastuvwxyz/memgraph/releases/latest/download/memgraph_$(curl -s https://api.github.com/repos/prastuvwxyz/memgraph/releases/latest | grep tag_name | cut -d'"' -f4)_linux_amd64.tar.gz | tar xz
sudo mv memgraph /usr/local/bin/
```

### Homebrew

```sh
# Coming soon
brew install prastuvwxyz/tap/memgraph
```

---

## Quick Start

```sh
# 1. Initialize — creates .memgraph/config.toml in your notes folder
memgraph init ~/notes

# 2. Index — scan and index all markdown files
memgraph index ~/notes

# 3. Query — search with BM25 ranking
memgraph query "openclaw setup"

# 4. Query as JSON — for piping into AI agents or scripts
memgraph query "deploy workflow" --format json

# 5. Show links — outbound and backlinks for a specific file
memgraph graph knowledge/openclaw-access.md
```

---

## Usage

### `init [dir]`

Scaffold a `.memgraph/` directory with a default config.

```sh
memgraph init .
memgraph init ~/my-notes
```

### `index [dir]`

Walk a directory and (incrementally) index all markdown files.

```sh
memgraph index .
memgraph index ~/notes --verbose    # print each updated file
```

Only files whose content hash has changed are re-indexed. On a 2,000-file vault, a re-index after editing one file takes under 100ms.

### `query <text>`

Search the index using BM25 full-text search, boosted by filename and tag matches.

```sh
memgraph query "kubernetes ingress"
memgraph query "deploy workflow" --top 10
memgraph query "stella setup" --ctx work
memgraph query "agent pipeline" --format json
memgraph query "daily brief" --format paths
```

| Flag | Default | Description |
|------|---------|-------------|
| `--top N` | 5 | Number of results to return |
| `--ctx NAME` | — | Restrict search to a named context |
| `--format` | table | Output format: `table`, `json`, `paths` |

### `graph <file>`

Show outbound wikilinks and backlinks for a file.

```sh
memgraph graph knowledge/openclaw-access.md
```

```
graph: knowledge/openclaw-access.md

Outbound links (2):
  -> project_openclaw_access.md
  -> knowledge/prastya-com-deploy.md

Backlinks (1):
  <- memory/2026-03-28.md
```

### `stats`

Print index statistics: file count, index size, last modified time.

```sh
memgraph stats
```

### `version`

```sh
memgraph version
```

### Global flags

| Flag | Description |
|------|-------------|
| `--dir PATH` | Override working directory (default: current directory) |

---

## Context Mode

Contexts let you scope queries to a named subdirectory without changing your working directory. Useful when a single vault contains multiple projects or domains.

Define contexts in `.memgraph/config.toml`:

```toml
[contexts.work]
root = "contexts/work"

[contexts.personal]
root = "contexts/personal"

[contexts.nalar]
root = "projects/nalar"
```

Then query within a context:

```sh
memgraph query "deploy pipeline" --ctx work
memgraph query "travel plans" --ctx personal
```

---

## Config

`memgraph init` writes `.memgraph/config.toml` with these defaults:

```toml
# memgraph config
# https://github.com/prastuvwxyz/memgraph

# Number of results to return (default: 5)
top_n = 5

# Output format: "table", "json", or "paths"
format = "table"

# Patterns to exclude from indexing (gitignore-style globs)
exclude = [
  ".git",
  "node_modules",
  "vendor",
  "*.pdf",
  "*.csv",
]

# Named contexts for --ctx flag (optional)
# [contexts.work]
# root = "contexts/work"
#
# [contexts.personal]
# root = "."
```

The index database is stored at `.memgraph/index.db` and is safe to delete — `memgraph index` will rebuild it from scratch.

---

## .memgraphignore

Place a `.memgraphignore` file at your vault root to exclude paths from indexing, using the same gitignore glob syntax as the `exclude` array in config.

```
# .memgraphignore
drafts/
archive/
*.tmp
private-*
```

Patterns in `.memgraphignore` are merged with the `exclude` list in `config.toml`.

---

## For AI Agents

memgraph lets an AI agent retrieve relevant context from a large note vault without loading every file into the prompt.

**Retrieve top files as JSON:**

```sh
memgraph query "openclaw setup" --format json --top 3
```

```json
[
  {
    "path": "knowledge/openclaw-access.md",
    "score": 0.9241,
    "tags": ["setup", "agent"],
    "reason": "filename + tag match"
  },
  {
    "path": "memory/2026-03-28.md",
    "score": 0.7812,
    "tags": ["openclaw", "infra"],
    "reason": "body match: \"openclaw setup\""
  }
]
```

**Retrieve file paths only** (for `cat`-ing relevant files into a prompt):

```sh
memgraph query "openclaw setup" --format paths --top 5
```

```
knowledge/openclaw-access.md
memory/2026-03-28.md
project_openclaw_access.md
```

A typical agent pattern: run `memgraph query` first, read only the returned files, then respond. This keeps context windows tight on large vaults.

---

## License

MIT — see [LICENSE](LICENSE).
