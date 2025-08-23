package ffmpeg

import (
	"encoding/json"
	"net/http"
	"time"
)

// ScheduleInfo represents schedule information for API responses
type ScheduleInfo struct {
	StreamName    string    `json:"stream_name"`
	Schedule      string    `json:"schedule"`
	Duration      string    `json:"duration"`
	NextRun       time.Time `json:"next_run"`
	ActiveID      string    `json:"active_id,omitempty"`
	IsRecording   bool      `json:"is_recording"`
}

// apiScheduler handles scheduler API requests
func apiScheduler(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	
	switch r.Method {
	case "GET":
		handleGetSchedules(w, r, query)
	case "POST":
		handleAddSchedule(w, r, query)
	case "DELETE":
		handleRemoveSchedule(w, r, query)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleGetSchedules returns all active schedules
func handleGetSchedules(w http.ResponseWriter, r *http.Request, query map[string][]string) {
	schedules := GetSchedules()
	var scheduleInfos []ScheduleInfo
	
	for streamName, schedule := range schedules {
		info := ScheduleInfo{
			StreamName:  streamName,
			Schedule:    schedule.Schedule,
			Duration:    schedule.Duration.String(),
			NextRun:     schedule.NextRun,
			ActiveID:    schedule.ActiveID,
			IsRecording: schedule.ActiveID != "",
		}
		scheduleInfos = append(scheduleInfos, info)
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"schedules": scheduleInfos,
		"count":     len(scheduleInfos),
	})
}

// handleAddSchedule adds a new recording schedule
func handleAddSchedule(w http.ResponseWriter, r *http.Request, query map[string][]string) {
	streamName := getQueryParam(query, "stream")
	if streamName == "" {
		http.Error(w, "stream parameter required", http.StatusBadRequest)
		return
	}
	
	scheduleStr := getQueryParam(query, "schedule")
	if scheduleStr == "" {
		http.Error(w, "schedule parameter required", http.StatusBadRequest)
		return
	}
	
	// Parse duration (default to 1 hour)
	duration := time.Hour
	if durationStr := getQueryParam(query, "duration"); durationStr != "" {
		var err error
		duration, err = time.ParseDuration(durationStr)
		if err != nil {
			http.Error(w, "invalid duration format", http.StatusBadRequest)
			return
		}
	}
	
	// Add schedule
	if err := AddSchedule(streamName, scheduleStr, duration); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Schedule added successfully",
		"stream":  streamName,
		"schedule": scheduleStr,
		"duration": duration.String(),
	})
}

// handleRemoveSchedule removes a recording schedule
func handleRemoveSchedule(w http.ResponseWriter, r *http.Request, query map[string][]string) {
	streamName := getQueryParam(query, "stream")
	if streamName == "" {
		http.Error(w, "stream parameter required", http.StatusBadRequest)
		return
	}
	
	RemoveSchedule(streamName)
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Schedule removed successfully",
		"stream":  streamName,
	})
}

// apiSchedulerTest handles schedule testing
func apiSchedulerTest(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	
	scheduleStr := getQueryParam(query, "schedule")
	if scheduleStr == "" {
		http.Error(w, "schedule parameter required", http.StatusBadRequest)
		return
	}
	
	// Parse schedule to validate
	parsed, err := parseSchedule(scheduleStr)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"valid": false,
			"error": err.Error(),
		})
		return
	}
	
	// Calculate next few runs
	now := time.Now()
	var nextRuns []time.Time
	for i := 0; i < 5; i++ {
		nextRun := calculateNextRun(parsed, now)
		nextRuns = append(nextRuns, nextRun)
		now = nextRun.Add(time.Minute)
	}
	
	// Get human-readable description
	description := getScheduleDescription(scheduleStr)
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"valid":       true,
		"schedule":    scheduleStr,
		"description": description,
		"next_runs":   nextRuns,
		"parsed": map[string]interface{}{
			"minutes":  parsed.Minutes,
			"hours":    parsed.Hours,
			"days":     parsed.Days,
			"months":   parsed.Months,
			"weekdays": parsed.Weekdays,
		},
	})
}

// getScheduleDescription returns human-readable description of a schedule
func getScheduleDescription(schedule string) string {
	descriptions := map[string]string{
		"0 9 * * 1-5":   "Daily at 9:00 AM, Monday through Friday",
		"0 22 * * *":    "Daily at 10:00 PM",
		"0 8,20 * * *":  "Daily at 8:00 AM and 8:00 PM",
		"*/15 * * * *":  "Every 15 minutes",
		"0 */2 * * *":   "Every 2 hours",
		"0 0 * * 0":     "Weekly on Sunday at midnight",
		"0 6 * * 1-5":   "Weekdays at 6:00 AM",
		"30 23 * * 6":   "Saturday at 11:30 PM",
		"0 12 1 * *":    "First day of every month at noon",
		"0 0 1 1 *":     "January 1st at midnight",
	}
	
	if desc, exists := descriptions[schedule]; exists {
		return desc
	}
	
	return "Custom schedule (check next runs for details)"
}