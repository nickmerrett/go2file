package ffmpeg

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/AlexxIT/go2rtc/internal/rtsp"
	"github.com/AlexxIT/go2rtc/internal/streams"
	"github.com/AlexxIT/go2rtc/pkg/core"
)

type RecordConfig struct {
	Filename string        `json:"filename"`
	Format   string        `json:"format,omitempty"`
	Duration time.Duration `json:"duration,omitempty"`
	Video    string        `json:"video,omitempty"`
	Audio    string        `json:"audio,omitempty"`
}

type Recording struct {
	ID       string        `json:"id"`
	Config   RecordConfig  `json:"config"`
	Stream   string        `json:"stream"`
	StartTime time.Time     `json:"start_time"`
	Duration  time.Duration `json:"duration,omitempty"`
	Active   bool          `json:"active"`
	
	publisher core.Producer
	mu        sync.Mutex
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
		Str("video_codec", r.Config.Video).
		Str("audio_codec", r.Config.Audio).
		Str("format", r.Config.Format).
		Dur("duration", r.Config.Duration).
		Msg("[recording] starting recording")
	
	if r.Active {
		return fmt.Errorf("recording already active")
	}
	
	cfg := GlobalRecordingConfig

	// Generate filename if not provided
	if r.Config.Filename == "" {
		r.Config.Filename = GenerateRecordingPath(r.Stream, r.StartTime, r.Config.Format, 0)
	}
	
	log.Debug().
		Str("recording_id", r.ID).
		Str("filename", r.Config.Filename).
		Msg("[recording] generated filename")

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
		log.Debug().
			Str("recording_id", r.ID).
			Str("directory", dir).
			Msg("[recording] created output directory")
	}
	
	// Build exec command for recording using FFmpeg
	rtspURL := fmt.Sprintf("rtsp://127.0.0.1:%s/%s", rtsp.Port, r.Stream)
	
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
	execURL := fmt.Sprintf("exec:ffmpeg -i %s", rtspURL)
	
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
			Int("segment_time", segmentTime).
			Str("segment_pattern", segmentPattern).
			Msg("[recording] using FFmpeg segmentation")
	} else {
		execURL += fmt.Sprintf(" -f %s -y %s", format, r.Config.Filename)
	}
	
	log.Info().
		Str("recording_id", r.ID).
		Str("stream", r.Stream).
		Str("exec_url", execURL).
		Str("rtsp_source", rtspURL).
		Str("output_file", r.Config.Filename).
		Msg("[recording] executing ffmpeg command")
	
	// Create the producer using exec
	producer, err := streams.GetProducer(execURL)
	if err != nil {
		log.Error().
			Err(err).
			Str("recording_id", r.ID).
			Str("stream", r.Stream).
			Str("exec_url", execURL).
			Msg("[recording] failed to create exec producer")
		return fmt.Errorf("failed to create exec producer: %w", err)
	}
	
	// Start the producer
	if err := producer.Start(); err != nil {
		log.Error().
			Err(err).
			Str("recording_id", r.ID).
			Str("stream", r.Stream).
			Msg("[recording] failed to start recording")
		return fmt.Errorf("failed to start recording: %w", err)
	}
	
	r.publisher = producer
	r.Active = true
	r.StartTime = time.Now()
	
	log.Info().
		Str("recording_id", r.ID).
		Str("stream", r.Stream).
		Str("output_file", r.Config.Filename).
		Time("start_time", r.StartTime).
		Dur("duration", r.Config.Duration).
		Msg("[recording] recording started successfully")
	
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
		Bool("was_active", r.Active).
		Msg("[recording] stopping recording")
	
	if !r.Active {
		log.Debug().
			Str("recording_id", r.ID).
			Msg("[recording] recording was not active, nothing to stop")
		return nil
	}
	
	duration := time.Since(r.StartTime)
	
	if r.publisher != nil {
		err := r.publisher.Stop()
		r.publisher = nil
		if err != nil {
			log.Error().
				Err(err).
				Str("recording_id", r.ID).
				Str("stream", r.Stream).
				Dur("duration", duration).
				Msg("[recording] failed to stop recording")
			return fmt.Errorf("failed to stop recording: %w", err)
		}
	}
	
	r.Active = false
	r.Duration = duration
	
	log.Info().
		Str("recording_id", r.ID).
		Str("stream", r.Stream).
		Str("output_file", r.Config.Filename).
		Dur("duration", duration).
		Time("start_time", r.StartTime).
		Msg("[recording] recording stopped successfully")
	
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

func (rm *RecordingManager) StopAll() {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	
	for id, recording := range rm.recordings {
		recording.Stop()
		delete(rm.recordings, id)
	}
}