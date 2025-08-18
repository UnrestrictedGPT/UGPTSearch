# UGPTSearch

A robust Go based local search API that rotates requests across multiple Searx instances for improved reliability and load distribution.

## Features

- **Instance Rotation**: Automatically distributes and rotates search requests across multiple Searx instances
- **Fault Tolerance**: Seamlessly handles instance failures by rotating to available alternatives
- **Load Balancing**: Prevents overloading individual Searx instances
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

Examples:

```bash
curl 'http://localhost:8080/search?q=privacy&format=json'
curl 'http://localhost:8080/search?q=privacy&format=text'
```

The service forwards your request to a random SearXNG instance from the built-in list and returns the response as-is.

## Development

Common tasks:

- `make fmt` – format code
- `make vet` – static analysis
- `make test` – run tests
- `make tidy` – tidy modules

## Python helper (optional)

`scrape_instances.py` can print healthy SearXNG instances from searx.space.

```bash
python3 -m venv .venv && . .venv/bin/activate
pip install -r requirements.txt
python scrape_instances.py
```

## Project structure

- `src/` – Go module root
  - `main.go` – HTTP server and `/search` handler
  - `go.mod` – module metadata
- `Makefile` – build/test/dev commands
- `.github/workflows/ci.yml` – CI pipeline
- `requirements.txt` – Python deps for the helper script
- `README.MD` – documentation

## License

Licensed under the GNU General Public License v3.0 (GPL-3.0). See `LICENSE` for details.

## Notes

- The module path in `go.mod` is currently `UGPTSearch`. If you host this on GitHub, consider updating it to `github.com/<you>/UGPTSearch` for better module compatibility.
