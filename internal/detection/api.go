package detection

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func apiDetectionStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if globalAnalyzer == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"enabled":     false,
			"queue_depth": 0,
			"streams":     map[string]interface{}{},
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"enabled":      GlobalDetectionConfig.Enabled,
		"backend_url":  GlobalDetectionConfig.BackendURL,
		"frame_interval": GlobalDetectionConfig.FrameInterval,
		"queue_depth":  globalAnalyzer.QueueDepth(),
		"streams":      globalAnalyzer.GetStatus(),
	})
}

func apiDetectionAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if globalAnalyzer == nil {
		http.Error(w, "Detection not enabled", http.StatusServiceUnavailable)
		return
	}

	query := r.URL.Query()
	file := query.Get("file")
	stream := query.Get("stream")

	if file != "" {
		// Queue a specific file
		streamName := stream
		if streamName == "" {
			// Guess from path
			streamName = guessStreamFromPath(file)
		}
		globalAnalyzer.Enqueue(streamName, file)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status": "queued",
			"file":   filepath.Base(file),
			"stream": streamName,
		})
		return
	}

	if stream != "" {
		// Queue all un-analysed files for a stream
		basePath := getSidecarBasePath()
		if basePath == "" {
			http.Error(w, "Recording base path not configured", http.StatusInternalServerError)
			return
		}
		streamDir := filepath.Join(basePath, stream)
		queued := 0
		_ = filepath.Walk(streamDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if ext != ".mp4" && ext != ".mkv" && ext != ".avi" {
				return nil
			}
			// Skip if sidecar already exists
			if _, err := os.Stat(sidecarPath(path)); err == nil {
				return nil
			}
			globalAnalyzer.Enqueue(stream, path)
			queued++
			return nil
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "queued",
			"stream": stream,
			"files":  queued,
		})
		return
	}

	http.Error(w, "Provide ?file= or ?stream= parameter", http.StatusBadRequest)
}

func guessStreamFromPath(filePath string) string {
	parts := strings.Split(filepath.Dir(filePath), string(filepath.Separator))
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] != "" {
			return parts[i]
		}
	}
	return "unknown"
}
