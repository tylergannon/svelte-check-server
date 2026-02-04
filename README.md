# svelte-check-server

A Go CLI that runs `svelte-check --watch` persistently and serves cached results via HTTP over a Unix socket. Get type-checking results in ~5ms instead of 3-5 seconds.

## Installation

```bash
go install github.com/tylergannon/svelte-check-server@latest
```

## Usage

```bash
# Start the server (runs svelte-check --watch in background)
svelte-check-server start -w /path/to/sveltekit/project

# Get cached results (~5ms)
svelte-check-server check -w /path/to/sveltekit/project

# Stop the server
svelte-check-server stop -w /path/to/sveltekit/project
```

## How it works

1. `start` launches `svelte-check --watch` and parses its machine-readable output
2. Results are cached and served via HTTP over a Unix socket
3. `check` retrieves the latest cached results instantly
4. The server automatically restarts `svelte-check` when relevant files change (e.g., `package.json`, git branch switches)

## Requirements

- `svelte-check` installed in your project (`npm install -D svelte-check`)

## Acknowledgments

Inspired by [ampcode/svelte-check-daemon](https://github.com/ampcode/svelte-check-daemon), a TypeScript implementation of the same concept.

## License

MIT
