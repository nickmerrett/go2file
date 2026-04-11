package ffmpeg

import (
	"encoding/json"
	"net/http"
)

// apiRecordingHealth handles health check requests for the recording system
func apiRecordingHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Perform health check
	healthCheck := PerformHealthCheckNow()

	// Get watchdog status
	watchdogStatus := GetWatchdogStatus()

	// Set appropriate status code based on health
	statusCode := http.StatusOK
	if !healthCheck.Healthy {
		statusCode = http.StatusServiceUnavailable
	}

	// Build response with enhanced watchdog info
	response := map[string]interface{}{
		"healthy":                 healthCheck.Healthy,
		"active_ffmpeg_processes": healthCheck.ActiveFFmpegProcesses,
		"expected_recordings":     healthCheck.ExpectedRecordings,
		"newest_recording_age":    healthCheck.NewestRecordingAge.String(),
		"warnings":                healthCheck.Warnings,
		"streams_with_issues":     healthCheck.StreamsWithIssues,
		"watchdog":                watchdogStatus,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(response)
}

// apiWatchdog returns the current watchdog status
func apiWatchdog(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	status := GetWatchdogStatus()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// apiWatchdogReset resets watchdog state for a stream or all streams
func apiWatchdogReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	streamName := r.URL.Query().Get("stream")

	// Reset watchdog state (empty string resets all)
	ResetWatchdogState(streamName)

	response := map[string]interface{}{
		"status": "reset",
		"stream": streamName,
	}

	if streamName == "" {
		response["message"] = "All stream watchdog states have been reset"
	} else {
		response["message"] = "Watchdog state reset for stream: " + streamName
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
