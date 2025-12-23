package miniedr

import (
	"fmt"

	"github.com/jaewooli/miniedr/capturer"
)

// DefaultRules returns a conservative set of heuristic rules using existing metrics.
func DefaultRules() []RuleSpec {
	return []RuleSpec{
		RuleCPUHigh(90),
		RuleMemPressure(90, 60),
		RuleProcBurst(10),
		RuleNetSpike(1 * 1024 * 1024), // 1MB/s total
		RuleFileEventBurst(50),        // 50 events in window
		RulePersistenceChange(1),      // any change
	}
}

// RuleCPUHigh fires when total CPU usage exceeds threshold.
func RuleCPUHigh(threshold float64) RuleSpec {
	return RuleSpec{
		ID:       "cpu.high_usage",
		Title:    "High CPU usage",
		Severity: SeverityMedium,
		Eval: func(info capturer.InfoData) []Alert {
			val, ok := metric(info, "cpu.total_pct")
			if !ok || val < threshold {
				return nil
			}
			return []Alert{{
				RuleID:   "cpu.high_usage",
				Message:  fmt.Sprintf("CPU usage %.2f%% exceeds threshold %.0f%%", val, threshold),
				Evidence: map[string]any{"cpu.total_pct": val},
			}}
		},
	}
}

// RuleMemPressure fires when RAM or swap usage is high.
func RuleMemPressure(ramThreshold, swapThreshold float64) RuleSpec {
	return RuleSpec{
		ID:       "mem.pressure",
		Title:    "Memory pressure",
		Severity: SeverityMedium,
		Eval: func(info capturer.InfoData) []Alert {
			ram, hasRAM := metric(info, "mem.ram.used_pct")
			swap, hasSwap := metric(info, "mem.swap.used_pct")
			var alerts []Alert
			if hasRAM && ram >= ramThreshold {
				alerts = append(alerts, Alert{
					RuleID:   "mem.pressure",
					Message:  fmt.Sprintf("RAM usage %.2f%% exceeds %.0f%%", ram, ramThreshold),
					Evidence: map[string]any{"mem.ram.used_pct": ram},
					DedupKey: "mem.pressure|ram",
				})
			}
			if hasSwap && swap >= swapThreshold {
				alerts = append(alerts, Alert{
					RuleID:   "mem.pressure",
					Severity: SeverityLow,
					Message:  fmt.Sprintf("Swap usage %.2f%% exceeds %.0f%%", swap, swapThreshold),
					Evidence: map[string]any{"mem.swap.used_pct": swap},
					DedupKey: "mem.pressure|swap",
				})
			}
			return alerts
		},
	}
}

// RuleProcBurst fires when new processes exceed limit.
func RuleProcBurst(limit int) RuleSpec {
	return RuleSpec{
		ID:       "proc.burst",
		Title:    "Process burst",
		Severity: SeverityMedium,
		Eval: func(info capturer.InfoData) []Alert {
			newCount, ok := metric(info, "proc.new")
			if !ok || int(newCount) < limit {
				return nil
			}
			return []Alert{{
				RuleID:   "proc.burst",
				Message:  fmt.Sprintf("%d new processes detected (limit %d)", int(newCount), limit),
				Evidence: map[string]any{"proc.new": int(newCount)},
			}}
		},
	}
}

// RuleNetSpike fires when combined RX+TX exceeds threshold bytes/sec.
func RuleNetSpike(totalBytesPerSec float64) RuleSpec {
	return RuleSpec{
		ID:       "net.spike",
		Title:    "Network spike",
		Severity: SeverityLow,
		Eval: func(info capturer.InfoData) []Alert {
			rx, okRx := metric(info, "net.rx_bytes_per_sec")
			tx, okTx := metric(info, "net.tx_bytes_per_sec")
			if !okRx && !okTx {
				return nil
			}
			total := rx + tx
			if total < totalBytesPerSec {
				return nil
			}
			return []Alert{{
				RuleID:   "net.spike",
				Message:  fmt.Sprintf("Network throughput %.0fB/s exceeds %.0fB/s", total, totalBytesPerSec),
				Evidence: map[string]any{"net.rx_bytes_per_sec": rx, "net.tx_bytes_per_sec": tx},
			}}
		},
	}
}

// RuleFileEventBurst fires when file change events exceed limit.
func RuleFileEventBurst(limit int) RuleSpec {
	return RuleSpec{
		ID:       "file.events_burst",
		Title:    "File change burst",
		Severity: SeverityLow,
		Eval: func(info capturer.InfoData) []Alert {
			ev, ok := metric(info, "file.events")
			if !ok || int(ev) < limit {
				return nil
			}
			return []Alert{{
				RuleID:   "file.events_burst",
				Message:  fmt.Sprintf("%d file events detected (limit %d)", int(ev), limit),
				Evidence: map[string]any{"file.events": int(ev)},
			}}
		},
	}
}

// RulePersistenceChange fires on autorun/service changes.
func RulePersistenceChange(minChanges int) RuleSpec {
	return RuleSpec{
		ID:       "persist.change",
		Title:    "Persistence modified",
		Severity: SeverityHigh,
		Eval: func(info capturer.InfoData) []Alert {
			added, hasAdded := metric(info, "persist.added")
			changed, hasChanged := metric(info, "persist.changed")
			removed, hasRemoved := metric(info, "persist.removed")
			if !hasAdded && !hasChanged && !hasRemoved {
				return nil
			}
			total := int(added + changed + removed)
			if total < minChanges {
				return nil
			}
			return []Alert{{
				RuleID:   "persist.change",
				Message:  fmt.Sprintf("Persistence entries changed (added=%d changed=%d removed=%d)", int(added), int(changed), int(removed)),
				Evidence: map[string]any{"persist.added": int(added), "persist.changed": int(changed), "persist.removed": int(removed)},
			}}
		},
	}
}

func metric(info capturer.InfoData, key string) (float64, bool) {
	if info.Metrics == nil {
		return 0, false
	}
	v, ok := info.Metrics[key]
	return v, ok
}
