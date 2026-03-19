package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/debug"
	"github.com/steveyegge/beads/internal/telemetry"
)

var (
	backupMetrics     *backupInstrumentation
	backupMetricsOnce sync.Once
)

type backupInstrumentation struct {
	gitPushSuppressed metric.Int64Counter
}

func getBackupMetrics() *backupInstrumentation {
	backupMetricsOnce.Do(func() {
		if !telemetry.Enabled() {
			backupMetrics = &backupInstrumentation{}
			return
		}
		m := telemetry.Meter("github.com/steveyegge/beads/backup")
		gitPushSuppressed, _ := m.Int64Counter("bd.backup.git_push_suppressed",
			metric.WithDescription("Count of backup git push operations suppressed by stealth mode (no-git-ops)"),
		)
		backupMetrics = &backupInstrumentation{
			gitPushSuppressed: gitPushSuppressed,
		}
	})
	return backupMetrics
}

// isBackupAutoEnabled returns whether backup should run.
// If user explicitly configured backup.enabled, use that.
// Otherwise, auto-enable when a git remote exists.
func isBackupAutoEnabled() bool {
	if config.GetValueSource("backup.enabled") != config.SourceDefault {
		return config.GetBool("backup.enabled")
	}
	return primeHasGitRemote()
}

// isBackupGitPushEnabled returns whether git push should run after backup.
// If user explicitly configured backup.git-push, use that.
// Otherwise, enable when backup is auto-enabled.
// Always disabled in stealth mode (no-git-ops) — stealth means no git operations.
func isBackupGitPushEnabled() bool {
	if config.GetBool("no-git-ops") {
		return false
	}
	if config.GetValueSource("backup.git-push") != config.SourceDefault {
		return config.GetBool("backup.git-push")
	}
	return isBackupAutoEnabled()
}

// maybeAutoBackup runs a JSONL backup if enabled and the throttle interval has passed.
// Called from PersistentPostRun after auto-commit.
func maybeAutoBackup(ctx context.Context) {
	if !isBackupAutoEnabled() {
		return
	}
	if store == nil || store.IsClosed() {
		return
	}

	dir, err := backupDir()
	if err != nil {
		debug.Logf("backup: failed to get backup dir: %v\n", err)
		return
	}

	state, err := loadBackupState(dir)
	if err != nil {
		debug.Logf("backup: failed to load state: %v\n", err)
		return
	}

	// Throttle: skip if we backed up recently
	interval := config.GetDuration("backup.interval")
	if interval == 0 {
		interval = 15 * time.Minute
	}
	if !state.Timestamp.IsZero() && time.Since(state.Timestamp) < interval {
		debug.Logf("backup: throttled (last backup %s ago, interval %s)\n",
			time.Since(state.Timestamp).Round(time.Second), interval)
		return
	}

	// Change detection: skip if nothing changed
	currentCommit, err := store.GetCurrentCommit(ctx)
	if err != nil {
		debug.Logf("backup: failed to get current commit: %v\n", err)
		return
	}
	if currentCommit == state.LastDoltCommit && state.LastDoltCommit != "" {
		debug.Logf("backup: no changes since last backup\n")
		return
	}

	// Run the export (force=true since we already checked change detection above)
	newState, err := runBackupExport(ctx, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: auto-backup failed: %v\n", err)
		return
	}

	debug.Logf("backup: exported %d issues, %d events, %d comments\n",
		newState.Counts.Issues, newState.Counts.Events, newState.Counts.Comments)

	// Optional git push
	if isBackupGitPushEnabled() {
		if err := gitBackup(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: backup git push failed: %v\n", err)
		}
	} else if config.GetBool("no-git-ops") {
		// Emit telemetry when git push is suppressed due to stealth mode.
		// This distinguishes intentional stealth-mode suppression from other reasons
		// git push might be disabled (e.g., explicit backup.git-push=false).
		metrics := getBackupMetrics()
		if metrics.gitPushSuppressed != nil {
			metrics.gitPushSuppressed.Add(ctx, 1, metric.WithAttributes(
				attribute.String("reason", "stealth_mode"),
			))
		}
		debug.Logf("backup: git push suppressed (stealth mode active)\n")
	}
}
