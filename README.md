# miniEDR
Mini EDR for Go that captures host telemetry, evaluates rules, and surfaces alerts in a lightweight dashboard.

## Features
- Host telemetry capturers (CPU, memory, disk, network, processes, persistence, file changes).
- Rules-based detections with rate limiting and deduplication.
- Web dashboard with live refresh, alert history, and IOC matching.
- Optional JSONL telemetry + alert sinks for offline analysis.

## Quick start
Requires Go 1.22+.

Run the dashboard (default: http://localhost:8090):
```bash
go run ./cli -config config.json
```

Run the dashboard with auth token:
```bash
go run ./cli -config config.json -dashboard-token "changeme"
# open http://localhost:8090/?token=changeme
```

Run agent-only with telemetry output:
```bash
go run ./cli -dashboard=false -telemetry-file telemetry.jsonl
```

Run agent with IOC matching + alert storage:
```bash
go run ./cli -dashboard=false -ioc ioc.sample.json -alert-file alerts.jsonl
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
When `-dashboard-token` is set, include `?token=...` on first load or send `X-EDR-Token`/`Authorization: Bearer` headers.

## Output files
- `rules.json` and `metrics.json` are created in the working directory when updated in the UI.
- `-telemetry-file` writes JSON lines and rotates around 5MB.
- `-alert-file` writes alert JSON lines and rotates around 5MB.

## IOC matching
Use `ioc.sample.json` as a starting point. Indicators support simple case-insensitive substring matching
for process names/paths/cmdlines, file paths, and exact matches for remote IPs.

Example:
```bash
go run ./cli -dashboard=false -ioc ioc.sample.json -alerts-stdout
```

## Response actions
`-response-kill` attempts to terminate processes referenced by alerts (uses evidence field `pid`).
Use `-response-kill-dry-run=false` to actually kill; default is dry-run for safety.
Use `-alerts-stdout` to write alert JSON lines to stdout.

## Architecture
See `explain.md` for a function-by-function reference and system overview.

## Development
```bash
go test ./...
```
