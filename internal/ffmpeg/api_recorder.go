package ffmpeg

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/AlexxIT/go2rtc/internal/api"
	"github.com/AlexxIT/go2rtc/internal/streams"
)

func apiRecord(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	
	switch r.Method {
	case "GET":
		handleGetRecordings(w, r, query)
	case "POST":
		handleStartRecording(w, r, query)
	case "DELETE":
		handleStopRecording(w, r, query)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleGetRecordings(w http.ResponseWriter, r *http.Request, query url.Values) {
	recordingID := query.Get("id")
	
	if recordingID != "" {
		// Get specific recording - try both regular and segmented
		recording := GetRecordingManager().GetRecording(recordingID)
		if recording != nil {
			api.ResponseJSON(w, recording)
			return
		}
		
		segRecording := GetSegmentedRecordingManager().GetSegmentedRecording(recordingID)
		if segRecording != nil {
			api.ResponseJSON(w, segRecording)
			return
		}
		
		http.Error(w, "Recording not found", http.StatusNotFound)
		return
	} else {
		// List all recordings - combine regular and segmented
		allRecordings := make(map[string]interface{})
		
		regularRecordings := GetRecordingManager().ListRecordings()
		for id, rec := range regularRecordings {
			allRecordings[id] = rec
		}
		
		segmentedRecordings := GetSegmentedRecordingManager().ListSegmentedRecordings()
		for id, rec := range segmentedRecordings {
			allRecordings[id] = rec
		}
		
		api.ResponseJSON(w, allRecordings)
	}
}

func handleStartRecording(w http.ResponseWriter, r *http.Request, query url.Values) {
	streamName := query.Get("src")
	if streamName == "" {
		http.Error(w, "Missing 'src' parameter (stream name)", http.StatusBadRequest)
		return
	}
	
	// Check if stream exists
	stream := streams.Get(streamName)
	if stream == nil {
		http.Error(w, fmt.Sprintf("Stream '%s' not found", streamName), http.StatusNotFound)
		return
	}
	
	// Parse recording configuration
	config := RecordConfig{}
	
	// Required: filename
	config.Filename = query.Get("filename")
	if config.Filename == "" {
		// Generate default filename with timestamp
		timestamp := time.Now().Format("2006-01-02_15-04-05")
		config.Filename = fmt.Sprintf("recordings/%s_%s.mp4", streamName, timestamp)
	}
	
	// Optional: format (auto-detected from extension if not specified)
	config.Format = query.Get("format")
	
	// Optional: duration limit
	if durationStr := query.Get("duration"); durationStr != "" {
		if duration, err := time.ParseDuration(durationStr); err == nil {
			config.Duration = duration
		} else {
			// Try parsing as seconds
			if seconds, err := strconv.Atoi(durationStr); err == nil {
				config.Duration = time.Duration(seconds) * time.Second
			}
		}
	}
	
	// Optional: video codec (will use global config default if not specified)
	config.Video = query.Get("video")
	
	// Optional: audio codec (will use global config default if not specified)  
	config.Audio = query.Get("audio")
	
	// Check for segmented recording
	useSegments := query.Get("segments") == "true" || GlobalRecordingConfig.EnableSegments
	
	// Generate recording ID
	recordingID := fmt.Sprintf("%s_%d", streamName, time.Now().Unix())
	if customID := query.Get("id"); customID != "" {
		recordingID = customID
	}
	
	// Start recording (segmented or regular)
	var response map[string]interface{}
	
	if useSegments {
		// Start segmented recording
		if err := GetSegmentedRecordingManager().StartSegmentedRecording(recordingID, streamName, config); err != nil {
			http.Error(w, fmt.Sprintf("Failed to start segmented recording: %v", err), http.StatusInternalServerError)
			return
		}
		
		// Get the segmented recording for response
		segRecording := GetSegmentedRecordingManager().GetSegmentedRecording(recordingID)
		if segRecording == nil {
			http.Error(w, "Segmented recording not found after creation", http.StatusInternalServerError)
			return
		}
		
		response = map[string]interface{}{
			"id":     recordingID,
			"stream": streamName,
			"type":   "segmented",
			"config": config,
			"status": segRecording.GetStatus(),
		}
	} else {
		// Start regular recording
		if err := GetRecordingManager().StartRecording(recordingID, streamName, config); err != nil {
			http.Error(w, fmt.Sprintf("Failed to start recording: %v", err), http.StatusInternalServerError)
			return
		}
		
		// Get the recording for response
		recording := GetRecordingManager().GetRecording(recordingID)
		if recording == nil {
			http.Error(w, "Recording not found after creation", http.StatusInternalServerError)
			return
		}
		
		response = map[string]interface{}{
			"id":     recordingID,
			"stream": streamName,
			"type":   "single",
			"config": config,
			"status": recording.GetStatus(),
		}
	}
	
	api.ResponseJSON(w, response)
}

func handleStopRecording(w http.ResponseWriter, r *http.Request, query url.Values) {
	recordingID := query.Get("id")
	if recordingID == "" {
		http.Error(w, "Missing 'id' parameter", http.StatusBadRequest)
		return
	}
	
	// Try stopping regular recording first
	err := GetRecordingManager().StopRecording(recordingID)
	if err != nil {
		// If not found, try stopping segmented recording
		err = GetSegmentedRecordingManager().StopSegmentedRecording(recordingID)
		if err != nil {
			http.Error(w, fmt.Sprintf("Recording not found: %v", err), http.StatusNotFound)
			return
		}
	}
	
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
}

func apiRecordingStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stats, err := GetRecordingStats()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get recording stats: %v", err), http.StatusInternalServerError)
		return
	}

	// Add configuration info
	stats["config"] = GlobalRecordingConfig

	api.ResponseJSON(w, stats)
}

func apiRecordingCleanup(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := CleanupNow()
	if err != nil {
		http.Error(w, fmt.Sprintf("Cleanup failed: %v", err), http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"status": "cleanup completed",
		"timestamp": time.Now(),
	}

	api.ResponseJSON(w, response)
}