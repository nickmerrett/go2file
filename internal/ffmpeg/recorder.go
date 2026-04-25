package ffmpeg

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AlexxIT/go2rtc/internal/rtsp"
	"github.com/AlexxIT/go2rtc/internal/streams"
)

// StreamError holds the last known error for a stream's recording process.
type StreamError struct {
	Error     string    `json:"error"`
	Timestamp time.Time `json:"timestamp"`
}

var streamErrors = struct {
	sync.RWMutex
	m map[string]StreamError
}{m: make(map[string]StreamError)}

func setStreamError(streamName, errMsg string) {
	streamErrors.Lock()
	streamErrors.m[streamName] = StreamError{Error: errMsg, Timestamp: time.Now()}
	streamErrors.Unlock()
}

func clearStreamError(streamName string) {
	streamErrors.Lock()
	delete(streamErrors.m, streamName)
	streamErrors.Unlock()
}

// GetStreamErrors returns a snapshot of all current stream recording errors.
func GetStreamErrors() map[string]StreamError {
	streamErrors.RLock()
	defer streamErrors.RUnlock()
	result := make(map[string]StreamError, len(streamErrors.m))
	for k, v := range streamErrors.m {
		result[k] = v
	}
	return result
}

// extractFFmpegError pulls a short human-readable message from ffmpeg stderr.
func extractFFmpegError(stderr string) string {
	knownErrors := []string{
		"Quota exceeded",
		"Host is unreachable",
		"Connection refused",
		"No route to host",
		"Connection timed out",
		"Permission denied",
		"No such file or directory",
		"Invalid data found",
	}
	for _, p := range knownErrors {
		if strings.Contains(stderr, p) {
			return p
		}
	}
	// Fall back to last non-trivial line
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || line == "Conversion failed!" {
			continue
		}
		// Strip ffmpeg log prefix "[tag @ 0x...]"
		if idx := strings.LastIndex(line, "] "); idx >= 0 {
			return strings.TrimSpace(line[idx+2:])
		}
		return line
	}
	return "ffmpeg exited unexpectedly"
}

type RecordConfig struct {
	Filename string        `json:"filename"`
	Format   string        `json:"format,omitempty"`
	Duration time.Duration `json:"duration,omitempty"`
	Video    string        `json:"video,omitempty"`
	Audio    string        `json:"audio,omitempty"`
}

type Recording struct {
	ID        string        `json:"id"`
	Config    RecordConfig  `json:"config"`
	Stream    string        `json:"stream"`
	StartTime time.Time     `json:"start_time"`
	Duration  time.Duration `json:"duration,omitempty"`
	Active    bool          `json:"active"`
	PID       int           `json:"pid,omitempty"`

	cmd *exec.Cmd
	mu  sync.Mutex
}

func NewRecording(id, streamName string, config RecordConfig) *Recording {
	return &Recording{
		ID:        id,
		Config:    config,
		Stream:    streamName,
		StartTime: time.Now(),
		Active:    false,
	}
}

func (r *Recording) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	log.Info().
		Str("recording_id", r.ID).
		Str("stream", r.Stream).
		Str("filename", r.Config.Filename).
		Msg("[recording] started recording session")
	
	if r.Active {
		return fmt.Errorf("recording already active")
	}
	
	cfg := GlobalRecordingConfig

	// Generate filename if not provided
	if r.Config.Filename == "" {
		r.Config.Filename = GenerateRecordingPath(r.Stream, r.StartTime, r.Config.Format, 0)
	}
	

	// Ensure output directory exists
	dir := filepath.Dir(r.Config.Filename)
	if cfg.CreateDirectories {
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Error().
				Err(err).
				Str("recording_id", r.ID).
				Str("directory", dir).
				Msg("[recording] failed to create output directory")
			return fmt.Errorf("failed to create output directory: %w", err)
		}
	}
	
	// Determine the recording source (direct RTSP or internal routing)
	recordingSource := GetRecordingSource(r.Stream, rtsp.Port)
	
		
	// Check if we're using direct source or need to validate internal stream
	if strings.HasPrefix(recordingSource, "rtsp://127.0.0.1:") {
		// Using internal routing - validate stream exists
		sourceStream := streams.Get(r.Stream)
		if sourceStream == nil {
			log.Error().
				Str("recording_id", r.ID).
				Str("stream", r.Stream).
				Msg("[recording] internal source stream not found")
			return fmt.Errorf("internal source stream '%s' not found", r.Stream)
		}
		log.Info().
			Str("recording_id", r.ID).
			Str("stream", r.Stream).
			Msg("[recording] using internal RTSP routing")
	} else {
		// Using direct source
		log.Info().
			Str("recording_id", r.ID).
			Str("stream", r.Stream).
			Str("source", recordingSource).
			Msg("[recording] using direct RTSP source")
	}
	
	
	// Build FFmpeg exec command
	video := r.Config.Video
	audio := r.Config.Audio
	
	// Use global defaults if not specified
	if video == "" {
		video = cfg.DefaultVideo
	}
	if audio == "" {
		audio = cfg.DefaultAudio
	}
	
	// Create exec URL that uses FFmpeg to record stream to file
	execURL := fmt.Sprintf("exec:ffmpeg -i %s", recordingSource)
	
	// Add video codec
	if video == "copy" {
		execURL += " -c:v copy"
	} else {
		if codec := defaults[video]; codec != "" {
			execURL += " " + codec
		} else {
			execURL += " -c:v " + video
		}
	}
	
	// Add audio codec  
	if audio == "copy" {
		execURL += " -c:a copy"
	} else {
		if codec := defaults[audio]; codec != "" {
			execURL += " " + codec
		} else {
			execURL += " -c:a " + audio
		}
	}
	
	// Add output format and file
	format := r.Config.Format
	if format == "" {
		// Auto-detect format from file extension
		ext := filepath.Ext(r.Config.Filename)
		switch ext {
		case ".mp4":
			format = "mp4"
		case ".mkv":
			format = "matroska"
		case ".avi":
			format = "avi"
		case ".mov":
			format = "mov"
		default:
			format = cfg.DefaultFormat
		}
	}
	
	// Add segmentation parameters if enabled
	streamConfig := GetStreamRecordingConfig(r.Stream)
	if streamConfig.EnableSegments != nil && *streamConfig.EnableSegments {
		// Use FFmpeg segment muxer for automatic file splitting
		segmentTime := int(streamConfig.SegmentDuration.Seconds())
		if segmentTime <= 0 {
			segmentTime = int(cfg.SegmentDuration.Seconds())
		}
		
		// Extract directory and filename parts for segment naming
		dir := filepath.Dir(r.Config.Filename)
		ext := filepath.Ext(r.Config.Filename)
		
		// Create segment filename pattern using strftime for time-based naming
		// This will create files like: stream_2025-01-01_12-00-00.mp4, stream_2025-01-01_12-10-00.mp4, etc.
		segmentPattern := filepath.Join(dir, r.Stream+"_%Y-%m-%d_%H-%M-%S"+ext)
		
		execURL += fmt.Sprintf(" -f segment -segment_time %d -segment_format %s -reset_timestamps 1", segmentTime, format)
		execURL += fmt.Sprintf(" -strftime 1 -y %s", segmentPattern)
		
		log.Info().
			Str("recording_id", r.ID).
			Int("segment_time_seconds", segmentTime).
			Str("segment_pattern", segmentPattern).
			Msg("[SEGMENTATION] Configured for automatic file splitting")
	} else {
		execURL += fmt.Sprintf(" -f %s -y %s", format, r.Config.Filename)
	}
	
	
	
	// Strip "exec:" prefix — we run FFmpeg directly, not via go2rtc's producer pipeline.
	// The producer mechanism expects FFmpeg to feed data back into go2rtc, but recording
	// writes to files only, so we manage the process ourselves.
	ffmpegArgs := strings.TrimPrefix(execURL, "exec:")
	args := strings.Fields(ffmpegArgs)
	if len(args) < 2 {
		return fmt.Errorf("invalid ffmpeg command: %s", ffmpegArgs)
	}

	log.Info().
		Str("recording_id", r.ID).
		Str("stream", r.Stream).
		Str("command", strings.Join(args, " ")).
		Msg("[recording] launching ffmpeg")

	var stderrBuf bytes.Buffer
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = nil
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		log.Error().
			Err(err).
			Str("recording_id", r.ID).
			Str("stream", r.Stream).
			Msg("[recording] failed to start ffmpeg process")
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	r.cmd = cmd
	r.PID = cmd.Process.Pid
	r.Active = true
	r.StartTime = time.Now()
	clearStreamError(r.Stream)

	// Reap the process when it exits so we don't accumulate zombies
	go func() {
		_ = cmd.Wait()
		r.mu.Lock()
		r.Active = false
		r.mu.Unlock()
		if stderrBuf.Len() > 0 {
			errMsg := extractFFmpegError(stderrBuf.String())
			setStreamError(r.Stream, errMsg)
			log.Error().
				Str("recording_id", r.ID).
				Str("stream", r.Stream).
				Str("error", errMsg).
				Str("ffmpeg_stderr", stderrBuf.String()).
				Msg("[recording] ffmpeg exited with output")
		} else {
			log.Debug().
				Str("recording_id", r.ID).
				Str("stream", r.Stream).
				Msg("[recording] ffmpeg process exited")
		}
	}()
	
	log.Info().
		Str("recording_id", r.ID).
		Str("stream", r.Stream).
		Str("output_file", r.Config.Filename).
		Msg("[recording] active and writing to file")
	
	// Handle duration limit
	if r.Config.Duration > 0 {
		log.Debug().
			Str("recording_id", r.ID).
			Dur("duration", r.Config.Duration).
			Msg("[recording] scheduled stop after duration")
		go func() {
			time.Sleep(r.Config.Duration)
			log.Info().
				Str("recording_id", r.ID).
				Dur("duration", r.Config.Duration).
				Msg("[recording] stopping recording after duration limit")
			r.Stop()
		}()
	}
	
	return nil
}

func (r *Recording) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	log.Info().
		Str("recording_id", r.ID).
		Str("stream", r.Stream).
		Msg("[recording] stopping recording session")
	
	if !r.Active {
		log.Debug().
			Str("recording_id", r.ID).
			Msg("[recording] recording was not active, nothing to stop")
		return nil
	}
	
	duration := time.Since(r.StartTime)

	if r.cmd != nil && r.cmd.Process != nil {
		// Send SIGINT first so FFmpeg can flush/finalise the output file cleanly
		if err := r.cmd.Process.Signal(os.Interrupt); err != nil {
			// Fallback to kill if interrupt not supported (e.g. Windows)
			_ = r.cmd.Process.Kill()
		}
		r.cmd = nil
	}
	
	r.Active = false
	r.Duration = duration
	
	log.Info().
		Str("recording_id", r.ID).
		Str("stream", r.Stream).
		Str("output_file", r.Config.Filename).
		Dur("duration", duration).
		Msg("[recording] recording completed")
	
	return nil
}

func (r *Recording) GetStatus() map[string]interface{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	status := map[string]interface{}{
		"id":        r.ID,
		"stream":    r.Stream,
		"filename":  r.Config.Filename,
		"format":    r.Config.Format,
		"active":    r.Active,
		"start_time": r.StartTime,
	}
	
	if r.Active {
		status["duration"] = time.Since(r.StartTime)
		if r.Config.Duration > 0 {
			status["max_duration"] = r.Config.Duration
			status["remaining"] = r.Config.Duration - time.Since(r.StartTime)
		}
	}
	
	return status
}


// RecordingManager manages multiple concurrent recordings
type RecordingManager struct {
	recordings map[string]*Recording
	mu         sync.RWMutex
}

var recordingManager = &RecordingManager{
	recordings: make(map[string]*Recording),
}

func GetRecordingManager() *RecordingManager {
	return recordingManager
}

func (rm *RecordingManager) StartRecording(id, streamName string, config RecordConfig) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	
	if _, exists := rm.recordings[id]; exists {
		return fmt.Errorf("recording with ID %s already exists", id)
	}
	
	recording := NewRecording(id, streamName, config)
	if err := recording.Start(); err != nil {
		return err
	}
	
	rm.recordings[id] = recording
	
	// Auto-cleanup when recording stops
	go func() {
		for recording.Active {
			time.Sleep(time.Second)
		}
		rm.mu.Lock()
		delete(rm.recordings, id)
		rm.mu.Unlock()
	}()
	
	return nil
}

func (rm *RecordingManager) StopRecording(id string) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	
	recording, exists := rm.recordings[id]
	if !exists {
		return fmt.Errorf("recording with ID %s not found", id)
	}
	
	err := recording.Stop()
	delete(rm.recordings, id)
	return err
}

func (rm *RecordingManager) GetRecording(id string) *Recording {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.recordings[id]
}

func (rm *RecordingManager) ListRecordings() map[string]*Recording {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	
	result := make(map[string]*Recording, len(rm.recordings))
	for id, recording := range rm.recordings {
		result[id] = recording
	}
	return result
}

func (rm *RecordingManager) IsStreamRecording(streamName string) bool {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	for _, recording := range rm.recordings {
		if recording.Stream == streamName && recording.Active {
			return true
		}
	}
	return false
}

func (rm *RecordingManager) StopAll() {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	
	for id, recording := range rm.recordings {
		recording.Stop()
		delete(rm.recordings, id)
	}
}