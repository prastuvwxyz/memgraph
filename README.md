# memgraph

> File-based knowledge graph for AI agents — query your notes without a database server.

[![Go version](https://img.shields.io/github/go-mod/go-version/prastuvwxyz/memgraph)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/prastuvwxyz/memgraph)](https://github.com/prastuvwxyz/memgraph/releases)

---

```
$ memgraph query "kubernetes ingress"

Path                                  Score    Tags                    Reason
----------------------------------------------------------------------------------
knowledge/kubernetes-ingress.md       0.9241   kubernetes, networking  filename + tag match
runbooks/cluster-setup.md             0.7812   kubernetes, infra       body match: "ingress"
projects/api-gateway/design.md        0.6530   kubernetes              body match: "ingress"
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
go install github.com/prastuvwxyz/memgraph/cmd/memgraph@latest
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
memgraph query "kubernetes ingress"

# 4. Query as JSON — for piping into AI agents or scripts
memgraph query "deploy workflow" --format json

# 5. Show links — outbound and backlinks for a specific file
memgraph graph knowledge/kubernetes-ingress.md
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
memgraph query "database migration" --ctx work
memgraph query "agent pipeline" --format json
memgraph query "auth setup" --format paths
```

| Flag | Default | Description |
|------|---------|-------------|
| `--top N` | 5 | Number of results to return |
| `--ctx NAME` | — | Restrict search to a named context |
| `--format` | table | Output format: `table`, `json`, `paths` |

### `graph <file>`

Show outbound wikilinks and backlinks for a file.

```sh
memgraph graph knowledge/kubernetes-ingress.md
```

```
graph: knowledge/kubernetes-ingress.md

Outbound links (2):
  -> runbooks/cluster-setup.md
  -> decisions/networking.md

Backlinks (1):
  <- knowledge/INDEX.md
```

### `serve`

Start a local web server with an interactive force-directed graph UI.

```sh
memgraph serve              # opens http://localhost:7331
memgraph serve --port 8080  # custom port
memgraph serve --open=false # don't auto-open browser
```

Nodes are colored by directory group. Click a node to see its links and tags. Search to highlight matching nodes.

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
root = "work"

[contexts.personal]
root = "personal"

[contexts.infra]
root = "projects/infra"
```

Then query within a context:

```sh
memgraph query "deploy pipeline" --ctx work
memgraph query "travel plans" --ctx personal
memgraph query "database setup" --ctx infra
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
# root = "work"
#
# [contexts.personal]
# root = "personal"
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

## Writing Files for Best Results

memgraph scores results using filename, BM25 full-text, tag bonus, and graph boost. The more structure your files have, the better the ranking.

### Frontmatter tags

Add a `tags:` field to any file you want to surface precisely. Tags get a **+2.0 bonus per exact match** — higher than any BM25 body hit.

```markdown
---
tags: [kubernetes, ingress, nginx, networking]
---

# Kubernetes Ingress Setup
```

Tag naming tips:
- Use lowercase, short terms a query would naturally include
- Name the subject: `[postgres, database, migration]` not `[notes, file, info]`
- 3–6 tags is enough — diminishing returns beyond that

### Staleness header

Add a `<!-- last-verified -->` comment after the title to track how fresh the file is:

```markdown
---
tags: [kubernetes, ingress, nginx]
---

# Kubernetes Ingress Setup
<!-- last-verified: 2026-01-15 | review-by: 2026-04-15 -->
```

memgraph indexes this date and exposes it in `--format json` output.

### Cross-file links (graph)

Links between files power `memgraph graph` and `memgraph serve`. Use standard relative markdown links — memgraph normalizes them to vault-root paths automatically:

```markdown
<!-- From: knowledge/kubernetes-ingress.md -->

See the [cluster setup runbook](../runbooks/cluster-setup.md)
and [networking decisions](../decisions/networking.md).
```

```markdown
<!-- From: INDEX.md (vault root) -->

- [knowledge/kubernetes-ingress.md](knowledge/kubernetes-ingress.md) — ingress configuration guide
- [knowledge/postgres-setup.md](knowledge/postgres-setup.md) — database setup and migrations
- [runbooks/deploy.md](runbooks/deploy.md) — production deploy process
```

Or use `[[wikilinks]]` (Obsidian-style) — both formats are supported:

```markdown
See [[knowledge/kubernetes-ingress]] and [[runbooks/cluster-setup]].
```

**What makes a good hub file:** index files and knowledge summaries should link out to the files they describe. Hub files become highly connected nodes in the graph; detail files become leaves.

### Full example

```markdown
---
tags: [postgres, database, setup, migration, production]
---

# PostgreSQL Setup — Production
<!-- last-verified: 2026-01-15 | review-by: 2026-04-15 -->

Managed via [CloudNativePG](../knowledge/cnpg.md) on Kubernetes.
See [cluster setup](../runbooks/cluster-setup.md) before running migrations.

Related: [backup strategy](../runbooks/postgres-backup.md) · [connection pooling](../knowledge/pgbouncer.md)
```

---

## For AI Agents

memgraph lets an AI agent retrieve relevant context from a large note vault without loading every file into the prompt.

**Retrieve top files as JSON:**

```sh
memgraph query "deploy workflow" --format json --top 3
```

```json
[
  {
    "path": "runbooks/deploy.md",
    "score": 0.9241,
    "tags": ["deploy", "ci", "production"],
    "reason": "filename + tag match"
  },
  {
    "path": "knowledge/kubernetes-ingress.md",
    "score": 0.7812,
    "tags": ["kubernetes", "networking"],
    "reason": "body match: \"deploy workflow\""
  }
]
```

**Retrieve file paths only** (for `cat`-ing relevant files into a prompt):

```sh
memgraph query "deploy workflow" --format paths --top 5
```

```
runbooks/deploy.md
knowledge/kubernetes-ingress.md
decisions/deploy-strategy.md
```

A typical agent pattern: run `memgraph query` first, read only the returned files, then respond. This keeps context windows tight on large vaults.

---

## License

MIT — see [LICENSE](LICENSE).
