# miniEDR
Mini EDR for Go that captures host telemetry, evaluates rules, and surfaces alerts in a lightweight dashboard.

## Features
- Host telemetry capturers (CPU, memory, disk, network, processes, persistence, file changes).
- Rules-based detections with rate limiting and deduplication.
- Web dashboard with live refresh and alert history.
- Optional JSONL telemetry sink for offline analysis.

## Quick start
Requires Go 1.22+.

Run the dashboard (default: http://localhost:8090):
```bash
go run ./cli -config config.json
```

Run agent-only with telemetry output:
```bash
go run ./cli -dashboard=false -telemetry-file telemetry.jsonl
```

Build a binary:
```bash
go build -o bin/miniedr ./cli
```

## Configuration
`config.json` is auto-detected from:
- the `-config` flag
- `./config.json` in the working directory
- `config.json` next to the built binary

Start from `config.sample.json`:
```json
{
  "capturers": {
    "cpu": {"enabled": true},
    "conn": {"enabled": true, "kind": "all"},
    "disk": {"enabled": true, "paths": ["/path/to/watch"]},
    "filewatch": {"enabled": true, "paths": ["/path/to/watch"], "max_files": 100000},
    "mem": {"enabled": true},
    "net": {"enabled": true},
    "persist": {"enabled": true},
    "proc": {"enabled": true}
  }
}
```

## Dashboard endpoints
- `/` HTML dashboard
- `/events` Server-Sent Events stream used for event refresh
- `/rules` GET/POST rules configuration (writes `rules.json` in the working directory)
- `/metrics` GET/POST custom metrics (writes `metrics.json` in the working directory)

## Output files
- `rules.json` and `metrics.json` are created in the working directory when updated in the UI.
- `-telemetry-file` writes JSON lines and rotates around 5MB.

## Architecture
See `explain.md` for a function-by-function reference and system overview.

## Development
```bash
go test ./...
```
