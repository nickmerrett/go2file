package ffmpeg

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// StreamHealthState tracks detailed health state per stream
type StreamHealthState struct {
	StreamName          string
	FFmpegPID           int
	ProcessRunning      bool
	LastFileSize        int64
	CurrentFileSize     int64
	LastCheckTime       time.Time
	FileGrowthRate      float64 // bytes per second
	ConsecutiveStalls   int     // count of checks with no growth
	LastRecoveryAttempt time.Time
	RecoveryAttempts    int
	IsStuck             bool
	ActiveFile          string
}

// WatchdogState maintains global watchdog state
type WatchdogState struct {
	StreamStates        map[string]*StreamHealthState
	LastFullCheck       time.Time
	ConsecutiveFailures int
	SystemHealthy       bool
	mu                  sync.RWMutex
}

// Global watchdog state
var globalWatchdogState = &WatchdogState{
	StreamStates:  make(map[string]*StreamHealthState),
	SystemHealthy: true,
}

// StartWatchdog starts the continuous watchdog monitoring
func StartWatchdog() {
	if !GlobalRecordingConfig.WatchdogEnabled {
		log.Info().Msg("[watchdog] watchdog disabled in config")
		return
	}

	go watchdogRoutine()
	log.Info().
		Dur("interval", GlobalRecordingConfig.WatchdogInterval).
		Int("stall_threshold", GlobalRecordingConfig.StallThreshold).
		Int64("min_growth_rate", GlobalRecordingConfig.MinFileGrowthRate).
		Msg("[watchdog] started continuous monitoring")
}

// watchdogRoutine is the main watchdog loop
func watchdogRoutine() {
	// Initial delay for system startup
	time.Sleep(time.Minute)

	interval := GlobalRecordingConfig.WatchdogInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			performWatchdogCheck()
		}
	}
}

// performWatchdogCheck runs a single watchdog check cycle
func performWatchdogCheck() {
	startTime := time.Now()

	globalWatchdogState.mu.Lock()
	defer globalWatchdogState.mu.Unlock()

	// Get expected streams
	streamsToRecord := getStreamsToRecordForHealthCheck()

	stuckCount := 0
	healthyCount := 0

	for _, streamName := range streamsToRecord {
		checkStreamFileGrowth(streamName)

		state := globalWatchdogState.StreamStates[streamName]
		if state != nil && state.IsStuck {
			stuckCount++
		} else {
			healthyCount++
		}
	}

	// Evaluate and take action
	evaluateAndRecover()

	globalWatchdogState.LastFullCheck = time.Now()
	globalWatchdogState.SystemHealthy = stuckCount == 0

	if stuckCount > 0 {
		log.Warn().
			Dur("check_duration", time.Since(startTime)).
			Int("streams_checked", len(streamsToRecord)).
			Int("stuck", stuckCount).
			Int("healthy", healthyCount).
			Msg("[watchdog] check cycle completed - issues detected")
	} else {
		log.Debug().
			Dur("check_duration", time.Since(startTime)).
			Int("streams_checked", len(streamsToRecord)).
			Msg("[watchdog] check cycle completed - all healthy")
	}
}

// checkStreamFileGrowth checks if a stream's recording file is growing
func checkStreamFileGrowth(streamName string) {
	state, exists := globalWatchdogState.StreamStates[streamName]
	if !exists {
		state = &StreamHealthState{
			StreamName:    streamName,
			LastCheckTime: time.Now(),
		}
		globalWatchdogState.StreamStates[streamName] = state
	}

	// Check if FFmpeg process is running for this stream
	state.FFmpegPID = getFFmpegPIDForStream(streamName)
	state.ProcessRunning = state.FFmpegPID > 0

	// Find the current recording file for this stream
	currentFile := findActiveRecordingFile(streamName)
	state.ActiveFile = currentFile

	if currentFile == "" {
		// No active file found
		if state.ProcessRunning {
			// Process running but no file - this is a problem
			state.ConsecutiveStalls++
			log.Debug().
				Str("stream", streamName).
				Int("pid", state.FFmpegPID).
				Int("stalls", state.ConsecutiveStalls).
				Msg("[watchdog] process running but no active file found")
		} else {
			// No process, no file - stream might not be recording
			// Don't increment stalls for this case
			log.Debug().
				Str("stream", streamName).
				Msg("[watchdog] no process and no active file")
		}
		state.IsStuck = state.ConsecutiveStalls >= GlobalRecordingConfig.StallThreshold
		return
	}

	// Get current file size
	stat, err := os.Stat(currentFile)
	if err != nil {
		state.ConsecutiveStalls++
		state.IsStuck = state.ConsecutiveStalls >= GlobalRecordingConfig.StallThreshold
		return
	}

	currentSize := stat.Size()
	timeSinceLastCheck := time.Since(state.LastCheckTime)

	// Calculate growth rate
	if state.LastFileSize > 0 && timeSinceLastCheck > 0 {
		growth := currentSize - state.LastFileSize

		// Handle file rotation - if current size is smaller, a new file was started
		if growth < 0 {
			log.Debug().
				Str("stream", streamName).
				Int64("prev_size", state.LastFileSize).
				Int64("curr_size", currentSize).
				Msg("[watchdog] file rotation detected")
			// Reset for new file, don't count as stall
			state.LastFileSize = currentSize
			state.LastCheckTime = time.Now()
			state.ConsecutiveStalls = 0
			state.IsStuck = false
			return
		}

		state.FileGrowthRate = float64(growth) / timeSinceLastCheck.Seconds()

		minGrowthRate := GlobalRecordingConfig.MinFileGrowthRate
		if minGrowthRate <= 0 {
			minGrowthRate = 1000 // 1KB/s default
		}

		if state.FileGrowthRate < float64(minGrowthRate) {
			state.ConsecutiveStalls++
			log.Debug().
				Str("stream", streamName).
				Float64("growth_rate_bps", state.FileGrowthRate).
				Int64("min_required", minGrowthRate).
				Int("stalls", state.ConsecutiveStalls).
				Msg("[watchdog] insufficient file growth")
		} else {
			// File is growing properly
			if state.ConsecutiveStalls > 0 {
				log.Debug().
					Str("stream", streamName).
					Float64("growth_rate_bps", state.FileGrowthRate).
					Msg("[watchdog] stream recovered - file growing again")
			}
			state.ConsecutiveStalls = 0
		}
	}

	state.LastFileSize = currentSize
	state.CurrentFileSize = currentSize
	state.LastCheckTime = time.Now()
	state.IsStuck = state.ConsecutiveStalls >= GlobalRecordingConfig.StallThreshold

	if state.IsStuck {
		log.Warn().
			Str("stream", streamName).
			Int("consecutive_stalls", state.ConsecutiveStalls).
			Float64("growth_rate_bps", state.FileGrowthRate).
			Int("pid", state.FFmpegPID).
			Str("file", currentFile).
			Msg("[watchdog] stream appears stuck")
	}
}

// findActiveRecordingFile finds the most recent recording file for a stream
func findActiveRecordingFile(streamName string) string {
	cfg := GlobalRecordingConfig
	streamDir := filepath.Join(cfg.BasePath, streamName)

	var newestFile string
	var newestTime time.Time

	// Walk the stream directory
	err := filepath.Walk(streamDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}

		if !isRecordingFile(filepath.Ext(path)) {
			return nil
		}

		// Check if file is being written (modified within last 5 minutes)
		modAge := time.Since(info.ModTime())
		if modAge < 5*time.Minute {
			if info.ModTime().After(newestTime) {
				newestTime = info.ModTime()
				newestFile = path
			}
		}

		return nil
	})

	if err != nil {
		log.Debug().Err(err).Str("stream", streamName).Msg("[watchdog] error walking stream directory")
	}

	return newestFile
}

// getFFmpegPIDForStream returns the PID of FFmpeg process for a stream
func getFFmpegPIDForStream(streamName string) int {
	cmd := fmt.Sprintf("pgrep -f 'ffmpeg.*%s' | head -1", streamName)
	result, err := exec.Command("sh", "-c", cmd).Output()
	if err != nil {
		return 0
	}

	pidStr := strings.TrimSpace(string(result))
	if pidStr == "" {
		return 0
	}

	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0
	}
	return pid
}

// evaluateAndRecover evaluates stream states and triggers recovery if needed
func evaluateAndRecover() {
	for streamName, state := range globalWatchdogState.StreamStates {
		if !state.IsStuck {
			continue
		}

		// Check recovery cooldown
		cooldown := GlobalRecordingConfig.RecoveryCooldown
		if cooldown <= 0 {
			cooldown = 2 * time.Minute
		}

		if time.Since(state.LastRecoveryAttempt) < cooldown {
			log.Debug().
				Str("stream", streamName).
				Dur("cooldown_remaining", cooldown-time.Since(state.LastRecoveryAttempt)).
				Msg("[watchdog] recovery cooldown active")
			continue
		}

		// Check max recovery attempts
		maxAttempts := GlobalRecordingConfig.MaxRecoveryAttempts
		if maxAttempts <= 0 {
			maxAttempts = 5
		}

		if state.RecoveryAttempts >= maxAttempts {
			log.Error().
				Str("stream", streamName).
				Int("attempts", state.RecoveryAttempts).
				Msg("[watchdog] max recovery attempts reached - manual intervention required")
			continue
		}

		// Perform recovery
		performStreamRecovery(streamName, state)
	}
}

// performStreamRecovery attempts to recover a stuck stream
func performStreamRecovery(streamName string, state *StreamHealthState) {
	state.RecoveryAttempts++
	state.LastRecoveryAttempt = time.Now()

	log.Info().
		Str("stream", streamName).
		Int("attempt", state.RecoveryAttempts).
		Int("max_attempts", GlobalRecordingConfig.MaxRecoveryAttempts).
		Int("pid", state.FFmpegPID).
		Msg("[watchdog] attempting stream recovery")

	// Step 1: Kill any stuck FFmpeg processes for this stream
	if state.FFmpegPID > 0 {
		log.Info().
			Str("stream", streamName).
			Int("pid", state.FFmpegPID).
			Msg("[watchdog] killing stuck FFmpeg process")

		// Force kill (SIGKILL) since it's stuck
		exec.Command("kill", "-9", strconv.Itoa(state.FFmpegPID)).Run()
	}

	// Also use the broader kill function to catch any we missed
	if err := killFFmpegProcessesForStream(streamName); err != nil {
		log.Warn().Err(err).Str("stream", streamName).Msg("[watchdog] error during process cleanup")
	}

	// Step 2: Stop internal tracking
	stopExistingRecordings(streamName)

	// Step 3: Wait for cleanup
	time.Sleep(3 * time.Second)

	// Step 4: Restart recording
	streamConfig := GetStreamRecordingConfig(streamName)
	recordingID := fmt.Sprintf("watchdog_%s_%d", streamName, time.Now().Unix())

	config := RecordConfig{
		Video:    streamConfig.Video,
		Audio:    streamConfig.Audio,
		Format:   streamConfig.Format,
		Duration: 0,
	}
	config.Filename = GenerateRecordingPath(streamName, time.Now(), config.Format, 0)

	var err error
	if streamConfig.EnableSegments != nil && *streamConfig.EnableSegments {
		err = GetSegmentedRecordingManager().StartSegmentedRecording(recordingID, streamName, config)
	} else {
		err = GetRecordingManager().StartRecording(recordingID, streamName, config)
	}

	if err != nil {
		log.Error().Err(err).Str("stream", streamName).Msg("[watchdog] recovery restart failed")
	} else {
		log.Info().
			Str("stream", streamName).
			Str("recording_id", recordingID).
			Msg("[watchdog] recovery restart successful")
		// Reset stall counter on successful restart
		state.ConsecutiveStalls = 0
		state.IsStuck = false
		state.LastFileSize = 0
		state.CurrentFileSize = 0
	}
}

// GetWatchdogStatus returns current watchdog state for API
func GetWatchdogStatus() map[string]interface{} {
	globalWatchdogState.mu.RLock()
	defer globalWatchdogState.mu.RUnlock()

	streamStatuses := make(map[string]interface{})
	for name, state := range globalWatchdogState.StreamStates {
		streamStatuses[name] = map[string]interface{}{
			"process_running":    state.ProcessRunning,
			"ffmpeg_pid":         state.FFmpegPID,
			"current_file_size":  state.CurrentFileSize,
			"file_growth_rate":   state.FileGrowthRate,
			"consecutive_stalls": state.ConsecutiveStalls,
			"is_stuck":           state.IsStuck,
			"recovery_attempts":  state.RecoveryAttempts,
			"last_check":         state.LastCheckTime,
			"active_file":        state.ActiveFile,
		}
	}

	return map[string]interface{}{
		"enabled":         GlobalRecordingConfig.WatchdogEnabled,
		"interval":        GlobalRecordingConfig.WatchdogInterval.String(),
		"stall_threshold": GlobalRecordingConfig.StallThreshold,
		"last_full_check": globalWatchdogState.LastFullCheck,
		"system_healthy":  globalWatchdogState.SystemHealthy,
		"stream_states":   streamStatuses,
	}
}

// ResetWatchdogState resets recovery counters for a stream (or all streams if empty)
func ResetWatchdogState(streamName string) {
	globalWatchdogState.mu.Lock()
	defer globalWatchdogState.mu.Unlock()

	if streamName == "" {
		// Reset all streams
		for name, state := range globalWatchdogState.StreamStates {
			state.RecoveryAttempts = 0
			state.ConsecutiveStalls = 0
			state.IsStuck = false
			log.Info().Str("stream", name).Msg("[watchdog] state reset")
		}
		log.Info().Msg("[watchdog] all stream states reset")
	} else if state, exists := globalWatchdogState.StreamStates[streamName]; exists {
		state.RecoveryAttempts = 0
		state.ConsecutiveStalls = 0
		state.IsStuck = false
		log.Info().Str("stream", streamName).Msg("[watchdog] state reset")
	}
}

// performAggressiveRecovery does a full reset when multiple failures occur
func performAggressiveRecovery() {
	log.Warn().Msg("[watchdog] performing aggressive recovery - full reset")

	// Step 1: Kill ALL FFmpeg recording processes
	killAllFFmpegRecordingProcesses()

	// Step 2: Clear all internal recording state
	GetRecordingManager().StopAll()
	GetSegmentedRecordingManager().StopAll()

	// Step 3: Reset watchdog state
	globalWatchdogState.mu.Lock()
	for _, state := range globalWatchdogState.StreamStates {
		state.RecoveryAttempts = 0
		state.ConsecutiveStalls = 0
		state.IsStuck = false
		state.LastFileSize = 0
		state.CurrentFileSize = 0
	}
	globalWatchdogState.ConsecutiveFailures = 0
	globalWatchdogState.mu.Unlock()

	// Step 4: Wait for cleanup
	time.Sleep(5 * time.Second)

	// Step 5: Restart all recordings
	log.Info().Msg("[watchdog] restarting all auto-recordings after aggressive recovery")
	go checkAndStartAutoRecordings()
}
