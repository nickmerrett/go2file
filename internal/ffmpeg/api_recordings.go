package ffmpeg

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	
	"github.com/AlexxIT/go2rtc/internal/streams"
)

// RecordingFile represents a recording file with metadata
type RecordingFile struct {
	ID           string    `json:"id"`
	StreamName   string    `json:"stream_name"`
	Filename     string    `json:"filename"`
	Path         string    `json:"path"`
	RelativePath string    `json:"relative_path"`
	Size         int64     `json:"size"`
	SizeHuman    string    `json:"size_human"`
	Duration     string    `json:"duration,omitempty"`
	StartTime    time.Time `json:"start_time"`
	EndTime      time.Time `json:"end_time,omitempty"`
	Format       string    `json:"format"`
	DateGroup    string    `json:"date_group"` // For grouping by date
	DownloadURL  string    `json:"download_url"`
	InfoURL      string    `json:"info_url"`
	StreamURL    string    `json:"stream_url"`
}

// apiRecordings handles recording file listing and download requests
func apiRecordings(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	
	switch r.Method {
	case "GET":
		if query.Get("download") != "" {
			handleDownloadRecording(w, r, query)
		} else if query.Get("info") != "" {
			handleRecordingInfo(w, r, query)
		} else if query.Get("play") != "" {
			handleRecordingStream(w, r, query)
		} else {
			handleListRecordings(w, r, query)
		}
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// getQueryParam is a helper function to get the first value from query params
func getQueryParam(query map[string][]string, key string) string {
	if values, exists := query[key]; exists && len(values) > 0 {
		return values[0]
	}
	return ""
}

// handleListRecordings returns a list of recording files
func handleListRecordings(w http.ResponseWriter, r *http.Request, query map[string][]string) {
	streamName := getQueryParam(query, "stream")
	dateFilter := getQueryParam(query, "date") // Format: YYYY-MM-DD
	limit := 100 // Default limit
	
	if limitStr := getQueryParam(query, "limit"); limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	
	recordings, err := listRecordingFiles(streamName, dateFilter, limit)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to list recordings: %v", err), http.StatusInternalServerError)
		return
	}
	
	// Group recordings by date for easier navigation
	grouped := groupRecordingsByDate(recordings)
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"recordings":      recordings,
		"grouped":         grouped,
		"count":          len(recordings),
		"stream_filter":  streamName,
		"date_filter":    dateFilter,
	})
}

// handleDownloadRecording serves recording files for download
func handleDownloadRecording(w http.ResponseWriter, r *http.Request, query map[string][]string) {
	recordingID := getQueryParam(query, "download")
	if recordingID == "" {
		http.Error(w, "Recording ID required", http.StatusBadRequest)
		return
	}
	
	// Find the recording file by ID
	recordings, err := listRecordingFiles("", "", 10000) // Get all recordings to find by ID
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to find recording: %v", err), http.StatusInternalServerError)
		return
	}
	
	var targetRecording *RecordingFile
	for _, recording := range recordings {
		if recording.ID == recordingID {
			targetRecording = &recording
			break
		}
	}
	
	if targetRecording == nil {
		http.Error(w, "Recording not found", http.StatusNotFound)
		return
	}
	
	// Security check: ensure path is within recordings directory
	basePath := filepath.Clean(GlobalRecordingConfig.BasePath)
	requestedPath := filepath.Clean(targetRecording.Path)
	if !strings.HasPrefix(requestedPath, basePath) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}
	
	// Open the file
	file, err := os.Open(targetRecording.Path)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to open recording: %v", err), http.StatusInternalServerError)
		return
	}
	defer file.Close()
	
	// Get file info
	fileInfo, err := file.Stat()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get file info: %v", err), http.StatusInternalServerError)
		return
	}
	
	// Set headers for download
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", targetRecording.Filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", fileInfo.Size()))
	
	// Stream the file
	io.Copy(w, file)
}

// RecordingInfo represents detailed information about a recording file
type RecordingInfo struct {
	*RecordingFile
	
	// Media information from ffprobe
	Duration     float64                `json:"duration"`     // Duration in seconds
	Bitrate      int64                  `json:"bitrate"`      // Bitrate in bits per second
	FormatName   string                 `json:"format_name"`  // Container format
	
	// Video stream info
	VideoCodec   string                 `json:"video_codec,omitempty"`
	VideoProfile string                 `json:"video_profile,omitempty"`
	VideoLevel   string                 `json:"video_level,omitempty"`
	Width        int                    `json:"width,omitempty"`
	Height       int                    `json:"height,omitempty"`
	FrameRate    string                 `json:"frame_rate,omitempty"`
	PixelFormat  string                 `json:"pixel_format,omitempty"`
	
	// Audio stream info
	AudioCodec   string                 `json:"audio_codec,omitempty"`
	AudioProfile string                 `json:"audio_profile,omitempty"`
	SampleRate   int                    `json:"sample_rate,omitempty"`
	Channels     int                    `json:"channels,omitempty"`
	ChannelLayout string               `json:"channel_layout,omitempty"`
	
	// Additional metadata
	CreationTime string                 `json:"creation_time,omitempty"`
	Encoder      string                 `json:"encoder,omitempty"`
	Tags         map[string]interface{} `json:"tags,omitempty"`
}

// handleRecordingInfo returns detailed information about a specific recording
func handleRecordingInfo(w http.ResponseWriter, r *http.Request, query map[string][]string) {
	recordingID := getQueryParam(query, "info")
	if recordingID == "" {
		http.Error(w, "Recording ID required", http.StatusBadRequest)
		return
	}
	
	// Find the recording file by ID
	recordings, err := listRecordingFiles("", "", 10000) // Get all recordings to find by ID
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to find recording: %v", err), http.StatusInternalServerError)
		return
	}
	
	var targetRecording *RecordingFile
	for _, recording := range recordings {
		if recording.ID == recordingID {
			targetRecording = &recording
			break
		}
	}
	
	if targetRecording == nil {
		http.Error(w, "Recording not found", http.StatusNotFound)
		return
	}
	
	// Get detailed info using ffprobe
	info, err := getRecordingDetailedInfo(targetRecording)
	if err != nil {
		log.Warn().Err(err).Str("recording", recordingID).Msg("[recording] failed to get detailed info, returning basic info")
		// Return basic info if ffprobe fails
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(targetRecording)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

// handleRecordingStream creates a temporary stream source for a recording file
func handleRecordingStream(w http.ResponseWriter, r *http.Request, query map[string][]string) {
	recordingID := getQueryParam(query, "play")
	if recordingID == "" {
		http.Error(w, "Recording ID required", http.StatusBadRequest)
		return
	}
	
	// Find the recording file by ID
	recordings, err := listRecordingFiles("", "", 10000)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to find recording: %v", err), http.StatusInternalServerError)
		return
	}
	
	var targetRecording *RecordingFile
	for _, recording := range recordings {
		if recording.ID == recordingID {
			targetRecording = &recording
			break
		}
	}
	
	if targetRecording == nil {
		http.Error(w, "Recording not found", http.StatusNotFound)
		return
	}
	
	// Create a file:// URL for the recording
	streamName := fmt.Sprintf("recording_%s", recordingID)
	fileURL := fmt.Sprintf("file://%s", targetRecording.Path)
	
	// Check if stream already exists, if not create it
	stream := streams.Get(streamName)
	if stream == nil {
		// Create new dynamic stream with file source
		stream = streams.New(streamName, fileURL)
		if stream == nil {
			http.Error(w, "Failed to create stream", http.StatusInternalServerError)
			return
		}
		log.Info().
			Str("recording_id", recordingID).
			Str("stream_name", streamName).
			Str("file_path", targetRecording.Path).
			Msg("[recording] created new stream for recording")
	} else {
		log.Debug().
			Str("recording_id", recordingID).
			Str("stream_name", streamName).
			Msg("[recording] using existing stream for recording")
	}
	
	// Return stream information
	response := map[string]interface{}{
		"stream_name": streamName,
		"source_url":  fileURL,
		"recording_id": recordingID,
		"filename":    targetRecording.Filename,
		"stream_url":  fmt.Sprintf("stream.html?src=%s", streamName),
		"links_url":   fmt.Sprintf("links.html?src=%s", streamName),
		"status":      "ready",
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// listRecordingFiles scans the recordings directory and returns file information
func listRecordingFiles(streamFilter, dateFilter string, limit int) ([]RecordingFile, error) {
	basePath := GlobalRecordingConfig.BasePath
	var recordings []RecordingFile
	
	err := filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Continue on errors
		}
		
		// Skip directories
		if info.IsDir() {
			return nil
		}
		
		// Check if it's a video file
		ext := strings.ToLower(filepath.Ext(path))
		if !isVideoFile(ext) {
			return nil
		}
		
		// Parse recording information from path and filename
		recording, parseErr := parseRecordingFile(path, info)
		if parseErr != nil {
			return nil // Skip files we can't parse
		}
		
		// Apply stream filter
		if streamFilter != "" && recording.StreamName != streamFilter {
			return nil
		}
		
		// Apply date filter
		if dateFilter != "" {
			recordingDate := recording.StartTime.Format("2006-01-02")
			if recordingDate != dateFilter {
				return nil
			}
		}
		
		recordings = append(recordings, *recording)
		
		// Apply limit
		if len(recordings) >= limit {
			return filepath.SkipDir // Stop walking
		}
		
		return nil
	})
	
	if err != nil {
		return nil, err
	}
	
	// Sort by start time (newest first)
	sort.Slice(recordings, func(i, j int) bool {
		return recordings[i].StartTime.After(recordings[j].StartTime)
	})
	
	return recordings, nil
}

// parseRecordingFile extracts metadata from a recording file
func parseRecordingFile(filePath string, info os.FileInfo) (*RecordingFile, error) {
	basePath := GlobalRecordingConfig.BasePath
	relativePath, err := filepath.Rel(basePath, filePath)
	if err != nil {
		return nil, err
	}
	
	filename := info.Name()
	
	// Extract stream name from path or filename
	streamName := extractStreamName(filePath, filename)
	
	// Extract timestamp from filename (prefer this over file mod time)
	startTime, endTime := extractTimeFromFilename(filename, info.ModTime())
	
	// Check if file is currently being written to (active recording)
	isActive := isActiveRecording(filePath, info)
	if isActive {
		// For active recordings, don't set an end time
		endTime = time.Time{}
	}
	
	// Generate unique ID for this recording
	id := generateRecordingID(filePath, startTime)
	
	// Determine format
	format := strings.TrimPrefix(strings.ToLower(filepath.Ext(filename)), ".")
	
	// Calculate duration
	var durationStr string
	if endTime.IsZero() || isActive {
		// Active recording - show current duration
		elapsed := time.Since(startTime)
		if elapsed < time.Minute {
			durationStr = "Recording..."
		} else {
			durationStr = fmt.Sprintf("Recording... (%dm)", int(elapsed.Minutes()))
		}
	} else {
		// Completed recording
		duration := endTime.Sub(startTime)
		if duration < time.Minute {
			durationStr = fmt.Sprintf("%.0fs", duration.Seconds())
		} else {
			durationStr = fmt.Sprintf("%.0fm", duration.Minutes())
		}
	}

	recording := &RecordingFile{
		ID:           id,
		StreamName:   streamName,
		Filename:     filename,
		Path:         filePath,
		RelativePath: relativePath,
		Size:         info.Size(),
		SizeHuman:    formatFileSize(info.Size()),
		Duration:     durationStr,
		StartTime:    startTime,
		EndTime:      endTime,
		Format:       format,
		DateGroup:    startTime.Format("2006-01-02"),
		DownloadURL:  fmt.Sprintf("/api/recordings?download=%s", id),
		InfoURL:      fmt.Sprintf("/api/recordings?info=%s", id),
		StreamURL:    fmt.Sprintf("stream.html?src=recording_%s", id),
	}
	
	return recording, nil
}

// extractStreamName tries to extract stream name from path components
func extractStreamName(filePath, filename string) string {
	// Try to extract from directory structure
	parts := strings.Split(filepath.Dir(filePath), string(filepath.Separator))
	
	// Look for stream name in path components (skip common directory names)
	skipDirs := map[string]bool{
		"recordings": true,
		"archive":    true,
		"security":   true,
		"indoor":     true,
	}
	
	for i := len(parts) - 1; i >= 0; i-- {
		part := parts[i]
		if part != "" && !isDateComponent(part) && !skipDirs[part] {
			return part
		}
	}
	
	// Try to extract from filename
	if streamName := extractStreamFromFilename(filename); streamName != "" {
		return streamName
	}
	
	return "unknown"
}

// extractStreamFromFilename tries to extract stream name from filename
func extractStreamFromFilename(filename string) string {
	// Remove extension
	baseName := strings.TrimSuffix(filename, filepath.Ext(filename))
	
	// Common patterns:
	// stream_2025-01-01_12-00-00
	// front_door_2025-01-01_12-00-00
	// camera1_part001_2025-01-01_12-00-00
	
	// Remove common suffixes
	baseName = regexp.MustCompile(`_\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}$`).ReplaceAllString(baseName, "")
	baseName = regexp.MustCompile(`_part\d+$`).ReplaceAllString(baseName, "")
	
	if baseName != "" {
		return baseName
	}
	
	return ""
}

// extractTimeFromFilename tries to extract timestamp from filename
func extractTimeFromFilename(filename string, fallback time.Time) (time.Time, time.Time) {
	// Remove extension
	baseName := strings.TrimSuffix(filename, filepath.Ext(filename))
	
	// Look for timestamp patterns
	patterns := []string{
		`(\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2})`,         // 2025-01-01_12-00-00
		`(\d{4}\d{2}\d{2}_\d{2}\d{2}\d{2})`,             // 20250101_120000
		`(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})`,         // 2025-01-01T12:00:00
	}
	
	timeFormats := []string{
		"2006-01-02_15-04-05",
		"20060102_150405",
		"2006-01-02T15:04:05",
	}
	
	for i, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(baseName)
		if len(matches) > 1 {
			if parsedTime, err := time.ParseInLocation(timeFormats[i], matches[1], time.Local); err == nil {
				// For segmented recordings, assume duration based on filename or default
				duration := estimateDuration(filename)
				endTime := parsedTime.Add(duration)
				return parsedTime, endTime
			}
		}
	}
	
	// Fallback to file modification time
	return fallback, fallback
}

// isActiveRecording checks if a recording file is currently being written to
func isActiveRecording(filePath string, info os.FileInfo) bool {
	// Check if file was modified recently (within last 2 minutes)
	// This indicates it might be an active recording
	modTime := info.ModTime()
	if time.Since(modTime) < 2*time.Minute {
		return true
	}
	
	// Additional check: very small files might be just starting
	if info.Size() < 1024*1024 { // Less than 1MB
		return true
	}
	
	return false
}

// estimateDuration estimates recording duration from filename or returns default
func estimateDuration(filename string) time.Duration {
	// Look for segment indicators
	if strings.Contains(filename, "_part") {
		// Assume segment duration from global config
		return GlobalRecordingConfig.SegmentDuration
	}
	
	// Default assumption for non-segmented files
	return 30 * time.Minute
}

// isDateComponent checks if a string looks like a date component
func isDateComponent(s string) bool {
	patterns := []string{`^\d{4}$`, `^\d{2}$`, `^\d{1,2}$`} // Year, month, day
	for _, pattern := range patterns {
		if matched, _ := regexp.MatchString(pattern, s); matched {
			return true
		}
	}
	return false
}

// isVideoFile checks if the file extension indicates a video file
func isVideoFile(ext string) bool {
	videoExtensions := map[string]bool{
		".mp4":  true,
		".mkv":  true,
		".avi":  true,
		".mov":  true,
		".wmv":  true,
		".flv":  true,
		".webm": true,
		".m4v":  true,
		".3gp":  true,
		".ts":   true,
	}
	return videoExtensions[ext]
}

// generateRecordingID generates a unique ID for a recording
func generateRecordingID(filePath string, startTime time.Time) string {
	// Use a combination of path hash and timestamp for uniqueness
	pathHash := fmt.Sprintf("%x", []byte(filePath))[:8]
	timeStr := startTime.Format("20060102150405")
	return fmt.Sprintf("%s_%s", pathHash, timeStr)
}

// formatFileSize converts bytes to human-readable format
func formatFileSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// groupRecordingsByDate groups recordings by date for easier navigation
func groupRecordingsByDate(recordings []RecordingFile) map[string][]RecordingFile {
	grouped := make(map[string][]RecordingFile)
	
	for _, recording := range recordings {
		date := recording.DateGroup
		grouped[date] = append(grouped[date], recording)
	}
	
	return grouped
}

// getRecordingDetailedInfo uses ffprobe to extract detailed media information
func getRecordingDetailedInfo(recording *RecordingFile) (*RecordingInfo, error) {
	
	info := &RecordingInfo{
		RecordingFile: recording,
	}
	
	// Use ffprobe to get detailed information
	cmd := exec.Command("ffprobe", 
		"-v", "quiet",
		"-print_format", "json", 
		"-show_format", 
		"-show_streams",
		recording.Path,
	)
	
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}
	
	var probeResult struct {
		Format struct {
			Filename       string            `json:"filename"`
			FormatName     string            `json:"format_name"`
			FormatLongName string            `json:"format_long_name"`
			Duration       string            `json:"duration"`
			Size           string            `json:"size"`
			BitRate        string            `json:"bit_rate"`
			Tags           map[string]string `json:"tags"`
		} `json:"format"`
		Streams []struct {
			Index              int               `json:"index"`
			CodecName          string            `json:"codec_name"`
			CodecLongName      string            `json:"codec_long_name"`
			Profile            string            `json:"profile"`
			CodecType          string            `json:"codec_type"`
			Width              int               `json:"width"`
			Height             int               `json:"height"`
			PixFmt             string            `json:"pix_fmt"`
			Level              int               `json:"level"`
			RFrameRate         string            `json:"r_frame_rate"`
			AvgFrameRate       string            `json:"avg_frame_rate"`
			SampleRate         string            `json:"sample_rate"`
			Channels           int               `json:"channels"`
			ChannelLayout      string            `json:"channel_layout"`
			Tags               map[string]string `json:"tags"`
		} `json:"streams"`
	}
	
	if err := json.Unmarshal(output, &probeResult); err != nil {
		return nil, fmt.Errorf("failed to parse ffprobe output: %w", err)
	}
	
	// Extract format information
	info.FormatName = probeResult.Format.FormatName
	if probeResult.Format.Duration != "" {
		if duration, err := strconv.ParseFloat(probeResult.Format.Duration, 64); err == nil {
			info.Duration = duration
		}
	}
	if probeResult.Format.BitRate != "" {
		if bitrate, err := strconv.ParseInt(probeResult.Format.BitRate, 10, 64); err == nil {
			info.Bitrate = bitrate
		}
	}
	
	// Extract creation time and encoder from tags
	if probeResult.Format.Tags != nil {
		info.CreationTime = probeResult.Format.Tags["creation_time"]
		info.Encoder = probeResult.Format.Tags["encoder"]
		// Convert to map[string]interface{} for JSON
		info.Tags = make(map[string]interface{})
		for k, v := range probeResult.Format.Tags {
			info.Tags[k] = v
		}
	}
	
	// Extract stream information
	for _, stream := range probeResult.Streams {
		switch stream.CodecType {
		case "video":
			info.VideoCodec = stream.CodecName
			info.VideoProfile = stream.Profile
			if stream.Level > 0 {
				info.VideoLevel = fmt.Sprintf("%.1f", float64(stream.Level)/10.0)
			}
			info.Width = stream.Width
			info.Height = stream.Height
			info.PixelFormat = stream.PixFmt
			
			// Use average frame rate if available, otherwise r_frame_rate
			if stream.AvgFrameRate != "" && stream.AvgFrameRate != "0/0" {
				info.FrameRate = stream.AvgFrameRate
			} else if stream.RFrameRate != "" && stream.RFrameRate != "0/0" {
				info.FrameRate = stream.RFrameRate
			}
			
		case "audio":
			info.AudioCodec = stream.CodecName
			info.AudioProfile = stream.Profile
			info.Channels = stream.Channels
			info.ChannelLayout = stream.ChannelLayout
			
			if stream.SampleRate != "" {
				if sampleRate, err := strconv.Atoi(stream.SampleRate); err == nil {
					info.SampleRate = sampleRate
				}
			}
		}
	}
	
	return info, nil
}