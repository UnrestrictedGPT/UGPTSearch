# UGPTSearch

A robust Go based local search API that rotates requests across multiple Searx instances for improved reliability and load distribution.

## Features

- **Intelligent Instance Rotation**: Automatically distributes and rotates search requests across multiple SearXNG instances
- **Advanced Rate Limiting Protection**: Smart handling of 429 errors with progressive cooldowns and exponential backoff
- **Instance Health Tracking**: Monitors instance availability and automatically excludes problematic instances
- **Request Spacing**: Built-in delays between requests to prevent overwhelming instances  
- **Fault Tolerance**: Seamlessly handles instance failures by rotating to available alternatives
- **Load Balancing**: Prevents overloading individual SearXNG instances with intelligent distribution
- **Simple API**: Easy-to-use interface for performing searches

## Installation

Prerequisites:

- Go 1.22+

Build and run (via Makefile):

```bash
make build
make run
```

The server listens on port :8080.

Direct Go commands (module lives in `src/`):

```bash
go -C src build ./...
go -C src run .
go -C src test ./...
```

## API

GET `/search`

- `q` (required): The search query
- `format` (optional): `json` (default) or `text`
- `browser_ua` (optional): `true` to use a browser user agent instead of the default "UGPTSearch/1.0"

Examples:

```bash
curl 'http://localhost:8080/search?q=privacy&format=json'
curl 'http://localhost:8080/search?q=privacy&format=text'
curl 'http://localhost:8080/search?q=privacy&format=json&browser_ua=true'
```

The service intelligently forwards your request to an available SearXNG instance from the built-in list, automatically handling rate limits and instance health to ensure reliable search results.

## Development

Common tasks:

- `make fmt` – format code
- `make vet` – static analysis
- `make test` – run tests
- `make tidy` – tidy modules



## Rate Limiting & Reliability

UGPTSearch implements sophisticated rate limiting protection to handle SearXNG instance limitations:

- **Progressive Cooldowns**: Instances returning 429 errors are temporarily excluded with increasing cooldown periods (30s to 5min)
- **Exponential Backoff**: Automatic retry delays with exponential progression (1s, 2s, 4s, 8s) 
- **Request Spacing**: Minimum 100ms delay between requests to the same instance
- **Health Monitoring**: Tracks consecutive errors and automatically excludes problematic instances
- **Smart Recovery**: Successful requests reset error counters and restore instance availability

## Project structure

- `src/` – Go module root
  - `main.go` – HTTP server entry point
  - `internal/` – Internal packages
    - `handlers/` – HTTP request handlers with rate limiting logic
    - `instances/` – Instance management with health tracking
    - `middleware/` – Logging and other middleware
    - `server/` – Server configuration
  - `pkg/` – Public packages and utilities
  - `go.mod` – module metadata
- `Makefile` – build/test/dev commands
- `.github/workflows/ci.yml` – CI pipeline  
- `requirements.txt` – Python deps for the helper script
- `README.MD` – documentation

## License

Licensed under the GNU General Public License v3.0 (GPL-3.0). See `LICENSE` for details.

## Notes

- The module path in `go.mod` is currently `UGPTSearch`. If you host this on GitHub, consider updating it to `github.com/<you>/UGPTSearch` for better module compatibility.
