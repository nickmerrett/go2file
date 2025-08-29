package ffmpeg

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AlexxIT/go2rtc/internal/app"
)

type StreamRecordingConfig struct {
	// Override global settings per stream
	Enabled          *bool         `yaml:"enabled"`           // Enable recording for this stream
	Source           string        `yaml:"source"`            // Direct RTSP source URL (overrides stream routing)
	PathTemplate     string        `yaml:"path_template"`     // Custom path template for this stream
	FilenameTemplate string        `yaml:"filename_template"` // Custom filename template
	Format           string        `yaml:"format"`            // Output format for this stream
	
	// Stream-specific segmentation
	SegmentDuration  time.Duration `yaml:"segment_duration"`  // Custom segment duration
	MaxFileSize      int64         `yaml:"max_file_size"`     // Custom max file size
	EnableSegments   *bool         `yaml:"enable_segments"`   // Enable/disable segments for this stream
	
	// Stream-specific retention
	RetentionDays    int           `yaml:"retention_days"`    // Custom retention days
	RetentionHours   int           `yaml:"retention_hours"`   // Custom retention hours
	MaxRecordings    int           `yaml:"max_recordings"`    // Custom max recordings
	
	// Stream-specific quality
	Video            string        `yaml:"video"`             // Video codec for this stream
	Audio            string        `yaml:"audio"`             // Audio codec for this stream
	BitrateLimit     string        `yaml:"bitrate_limit"`     // Bitrate limit for this stream
	
	// Stream-specific behavior
	AutoStart        *bool         `yaml:"auto_start"`        // Auto-start for this stream
	RestartOnError   *bool         `yaml:"restart_on_error"`  // Restart behavior for this stream
	
	// Schedule-based recording
	Schedule         string        `yaml:"schedule"`          // Cron-like schedule (future feature)
	RecordOnMotion   bool          `yaml:"record_on_motion"`  // Record only on motion detection
	
	// Quality settings
	Width            int           `yaml:"width"`             // Force specific width
	Height           int           `yaml:"height"`            // Force specific height
	Framerate        int           `yaml:"framerate"`         // Force specific framerate
}

type RecordingConfig struct {
	// Storage settings
	BasePath        string `yaml:"base_path"`         // Base directory for all recordings
	PathTemplate    string `yaml:"path_template"`     // Directory structure template
	FilenameTemplate string `yaml:"filename_template"` // Filename template
	DefaultFormat   string `yaml:"default_format"`    // Default output format
	CreateDirectories bool `yaml:"create_directories"` // Auto-create directories

	// Segmentation settings
	SegmentDuration  time.Duration `yaml:"segment_duration"`  // Duration before starting new file
	MaxFileSize      int64         `yaml:"max_file_size"`     // Max file size in MB before new file
	EnableSegments   bool          `yaml:"enable_segments"`   // Enable automatic segmentation

	// Retention policy
	RetentionDays    int   `yaml:"retention_days"`    // Days to keep recordings
	RetentionHours   int   `yaml:"retention_hours"`   // Hours to keep recordings (more granular)
	MaxRecordings    int   `yaml:"max_recordings"`    // Max recordings per stream
	MaxTotalSize     int64 `yaml:"max_total_size"`    // Max total storage in MB

	// Cleanup settings
	EnableCleanup    bool          `yaml:"enable_cleanup"`    // Enable automatic cleanup
	CleanupInterval  time.Duration `yaml:"cleanup_interval"`  // How often to run cleanup
	MoveToArchive    bool          `yaml:"move_to_archive"`   // Move old files instead of deleting
	ArchivePath      string        `yaml:"archive_path"`      // Archive directory path

	// Recording behavior
	AutoStart        bool          `yaml:"auto_start"`        // Auto-start recording when stream available
	AutoRecordCheckInterval time.Duration `yaml:"auto_record_check_interval"` // How often to check for new streams to record
	RestartOnError   bool          `yaml:"restart_on_error"`  // Restart if FFmpeg fails
	BufferTime       time.Duration `yaml:"buffer_time"`       // Pre-recording buffer duration
	PostRecordingTime time.Duration `yaml:"post_recording_time"` // Continue after stream ends
	
	// Source settings
	DirectSource     string        `yaml:"direct_source"`     // Global direct source template (e.g., "rtsp://camera-{stream}.local/stream1")

	// Quality and codec settings
	DefaultVideo     string        `yaml:"default_video"`     // Default video codec
	DefaultAudio     string        `yaml:"default_audio"`     // Default audio codec
	BitrateLimit     string        `yaml:"bitrate_limit"`     // Bitrate limit for recordings

	// Monitoring
	EnableMetrics    bool          `yaml:"enable_metrics"`    // Enable recording metrics
	MetricsInterval  time.Duration `yaml:"metrics_interval"`  // Metrics collection interval
	
	// Per-stream configuration
	Streams          map[string]StreamRecordingConfig `yaml:"streams"` // Per-stream recording settings
}

var GlobalRecordingConfig = &RecordingConfig{
	// Default values
	BasePath:          "recordings",
	PathTemplate:      "{year}/{month}/{day}/{stream}",
	FilenameTemplate:  "{stream}_{timestamp}",
	DefaultFormat:     "mp4",
	CreateDirectories: true,

	SegmentDuration:   time.Minute * 10, // 10 minute segments by default
	MaxFileSize:       1024,          // 1GB max file size
	EnableSegments:    true,          // Enabled by default

	RetentionDays:     7,             // Keep for 7 days
	RetentionHours:    0,             // 0 means use RetentionDays
	MaxRecordings:     100,           // Max 100 recordings per stream
	MaxTotalSize:      10240,         // 10GB total limit

	EnableCleanup:     true,          // Enable cleanup by default
	CleanupInterval:   time.Hour,     // Check every hour
	MoveToArchive:     false,         // Delete by default
	ArchivePath:       "archive",

	AutoStart:         false,         // Don't auto-start by default
	AutoRecordCheckInterval: time.Second * 10, // Check every 10 seconds by default
	RestartOnError:    true,          // Restart on errors
	BufferTime:        0,             // No buffer by default
	PostRecordingTime: time.Second * 5, // 5 seconds after stream ends

	DefaultVideo:      "copy",        // Copy video codec by default
	DefaultAudio:      "copy",        // Copy audio codec by default
	BitrateLimit:      "",            // No limit by default

	EnableMetrics:     false,         // Disabled by default
	MetricsInterval:   time.Minute * 5, // Every 5 minutes
	
	// Initialize empty streams map
	Streams:           make(map[string]StreamRecordingConfig),
}

func LoadRecordingConfig() {
	var cfg struct {
		Recording RecordingConfig `yaml:"recording"`
	}

	// Set defaults
	cfg.Recording = *GlobalRecordingConfig

	// Load from YAML config
	app.LoadConfig(&cfg)

	// Update global config
	*GlobalRecordingConfig = cfg.Recording

	// Validate and fix config values
	validateRecordingConfig()

	// Start cleanup routine if enabled
	if GlobalRecordingConfig.EnableCleanup {
		go cleanupRoutine()
	}

	// Log configuration in a more readable format
	log.Info().
		Str("base_path", GlobalRecordingConfig.BasePath).
		Str("default_format", GlobalRecordingConfig.DefaultFormat).
		Str("default_video", GlobalRecordingConfig.DefaultVideo).
		Str("default_audio", GlobalRecordingConfig.DefaultAudio).
		Bool("enable_segments", GlobalRecordingConfig.EnableSegments).
		Dur("segment_duration", GlobalRecordingConfig.SegmentDuration).
		Int64("max_file_size_mb", GlobalRecordingConfig.MaxFileSize).
		Int("retention_days", GlobalRecordingConfig.RetentionDays).
		Bool("auto_start", GlobalRecordingConfig.AutoStart).
		Dur("auto_record_check_interval", GlobalRecordingConfig.AutoRecordCheckInterval).
		Bool("enable_cleanup", GlobalRecordingConfig.EnableCleanup).
		Int("stream_count", len(GlobalRecordingConfig.Streams)).
		Msg("[recording] config loaded")
		
	// Log per-stream configurations
	for streamName, streamConfig := range GlobalRecordingConfig.Streams {
		log.Info().
			Str("stream", streamName).
			Interface("enabled", streamConfig.Enabled).
			Interface("auto_start", streamConfig.AutoStart).
			Str("video", streamConfig.Video).
			Str("source", streamConfig.Source).
			Str("audio", streamConfig.Audio).
			Str("format", streamConfig.Format).
			Interface("enable_segments", streamConfig.EnableSegments).
			Dur("segment_duration", streamConfig.SegmentDuration).
			Int64("max_file_size_mb", streamConfig.MaxFileSize).
			Int("retention_days", streamConfig.RetentionDays).
			Str("schedule", streamConfig.Schedule).
			Msg("[recording] stream config")
	}
}

func validateRecordingConfig() {
	cfg := GlobalRecordingConfig

	// Ensure base path exists
	if cfg.CreateDirectories {
		if err := os.MkdirAll(cfg.BasePath, 0755); err != nil {
			log.Error().Err(err).Str("path", cfg.BasePath).Msg("[recording] failed to create base directory")
		}
	}

	// Validate retention settings
	if cfg.RetentionHours > 0 && cfg.RetentionDays > 0 {
		log.Warn().Msg("[recording] both retention_days and retention_hours set, using retention_hours")
		cfg.RetentionDays = 0
	}

	// Ensure minimum values
	if cfg.CleanupInterval < time.Minute {
		cfg.CleanupInterval = time.Minute
	}
	if cfg.SegmentDuration < time.Minute && cfg.EnableSegments {
		cfg.SegmentDuration = time.Minute
	}

	// Validate templates
	if cfg.PathTemplate == "" {
		cfg.PathTemplate = "{stream}"
	}
	if cfg.FilenameTemplate == "" {
		cfg.FilenameTemplate = "{stream}_{timestamp}"
	}

	// Create archive directory if needed
	if cfg.MoveToArchive && cfg.ArchivePath != "" && cfg.CreateDirectories {
		if err := os.MkdirAll(cfg.ArchivePath, 0755); err != nil {
			log.Error().Err(err).Str("path", cfg.ArchivePath).Msg("[recording] failed to create archive directory")
		}
	}
}

// GenerateRecordingPath creates the full path for a recording file
func GenerateRecordingPath(streamName string, startTime time.Time, format string, segmentNum int) string {
	cfg := GlobalRecordingConfig

	// Process path template
	pathTemplate := cfg.PathTemplate
	pathTemplate = strings.ReplaceAll(pathTemplate, "{stream}", streamName)
	pathTemplate = strings.ReplaceAll(pathTemplate, "{year}", startTime.Format("2006"))
	pathTemplate = strings.ReplaceAll(pathTemplate, "{month}", startTime.Format("01"))
	pathTemplate = strings.ReplaceAll(pathTemplate, "{day}", startTime.Format("02"))
	pathTemplate = strings.ReplaceAll(pathTemplate, "{hour}", startTime.Format("15"))

	// Process filename template
	filenameTemplate := cfg.FilenameTemplate
	filenameTemplate = strings.ReplaceAll(filenameTemplate, "{stream}", streamName)
	filenameTemplate = strings.ReplaceAll(filenameTemplate, "{timestamp}", startTime.Format("2006-01-02_15-04-05"))
	filenameTemplate = strings.ReplaceAll(filenameTemplate, "{date}", startTime.Format("2006-01-02"))
	filenameTemplate = strings.ReplaceAll(filenameTemplate, "{time}", startTime.Format("15-04-05"))

	// Note: No longer adding segment numbers to filenames for cleaner names

	// Add format extension
	if format == "" {
		format = cfg.DefaultFormat
	}
	if !strings.HasPrefix(format, ".") {
		format = "." + format
	}

	filename := filenameTemplate + format
	fullPath := filepath.Join(cfg.BasePath, pathTemplate, filename)

	// Create directory if needed
	if cfg.CreateDirectories {
		dir := filepath.Dir(fullPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Error().Err(err).Str("dir", dir).Msg("[recording] failed to create recording directory")
		}
	}

	return fullPath
}

// GetRetentionDuration returns the retention duration based on config
func GetRetentionDuration() time.Duration {
	cfg := GlobalRecordingConfig
	if cfg.RetentionHours > 0 {
		return time.Duration(cfg.RetentionHours) * time.Hour
	}
	if cfg.RetentionDays > 0 {
		return time.Duration(cfg.RetentionDays) * 24 * time.Hour
	}
	return 7 * 24 * time.Hour // Default to 7 days
}

// ShouldAutoStart returns true if recording should auto-start for the stream
func ShouldAutoStart() bool {
	return GlobalRecordingConfig.AutoStart
}

// GetDefaultCodecs returns the default video and audio codecs
func GetDefaultCodecs() (video, audio string) {
	cfg := GlobalRecordingConfig
	video = cfg.DefaultVideo
	audio = cfg.DefaultAudio
	if video == "" {
		video = "copy"
	}
	if audio == "" {
		audio = "copy"
	}
	return
}

// IsStreamRecordingEnabled checks if recording is enabled for a specific stream
func IsStreamRecordingEnabled(streamName string) bool {
	cfg := GlobalRecordingConfig
	
	// Check if stream is explicitly configured for recording
	if streamConfig, exists := cfg.Streams[streamName]; exists {
		// If explicitly set for this stream, use that setting
		if streamConfig.Enabled != nil {
			log.Debug().
				Str("stream", streamName).
				Bool("enabled", *streamConfig.Enabled).
				Msg("[recording] stream has explicit enabled setting")
			return *streamConfig.Enabled
		}
		
		// Stream is in recording config but no explicit enabled field
		// This means user configured it for recording, so default to true
		log.Debug().
			Str("stream", streamName).
			Msg("[recording] stream configured for recording without explicit enabled, defaulting to true")
		return true
	}
	
	// Stream is NOT in recording configuration at all
	// Only record if global auto_start is enabled AND there are no specific stream configs
	if cfg.AutoStart && len(cfg.Streams) == 0 {
		// Global auto_start mode - record all streams
		log.Debug().
			Str("stream", streamName).
			Msg("[recording] global auto_start enabled with no specific stream configs")
		return true
	}
	
	// If there are specific stream configurations, only record those explicitly configured
	log.Debug().
		Str("stream", streamName).
		Int("configured_streams", len(cfg.Streams)).
		Bool("global_auto_start", cfg.AutoStart).
		Msg("[recording] stream not in recording config and specific streams configured, not recording")
	return false
}

// GetStreamRecordingConfig returns the effective configuration for a stream
func GetStreamRecordingConfig(streamName string) StreamRecordingConfig {
	cfg := GlobalRecordingConfig
	
	// Start with defaults based on global config
	streamConfig := StreamRecordingConfig{
		Format:          cfg.DefaultFormat,
		Video:           cfg.DefaultVideo,
		Audio:           cfg.DefaultAudio,
		BitrateLimit:    cfg.BitrateLimit,
		SegmentDuration: cfg.SegmentDuration,
		MaxFileSize:     cfg.MaxFileSize,
		RetentionDays:   cfg.RetentionDays,
		RetentionHours:  cfg.RetentionHours,
		MaxRecordings:   cfg.MaxRecordings,
		PathTemplate:    cfg.PathTemplate,
		FilenameTemplate: cfg.FilenameTemplate,
		// Source will be resolved after stream-specific overrides
	}
	
	// Set default boolean pointers
	enabled := cfg.AutoStart
	enableSegments := cfg.EnableSegments
	restartOnError := cfg.RestartOnError
	
	streamConfig.Enabled = &enabled
	streamConfig.EnableSegments = &enableSegments
	streamConfig.AutoStart = &enabled
	streamConfig.RestartOnError = &restartOnError
	
	// Override with stream-specific settings if they exist
	if specificConfig, exists := cfg.Streams[streamName]; exists {
		if specificConfig.Enabled != nil {
			streamConfig.Enabled = specificConfig.Enabled
		}
		if specificConfig.Source != "" {
			streamConfig.Source = specificConfig.Source // Override with stream-specific source
		}
		if specificConfig.Format != "" {
			streamConfig.Format = specificConfig.Format
		}
		if specificConfig.Video != "" {
			streamConfig.Video = specificConfig.Video
		}
		if specificConfig.Audio != "" {
			streamConfig.Audio = specificConfig.Audio
		}
		if specificConfig.BitrateLimit != "" {
			streamConfig.BitrateLimit = specificConfig.BitrateLimit
		}
		if specificConfig.SegmentDuration > 0 {
			streamConfig.SegmentDuration = specificConfig.SegmentDuration
		}
		if specificConfig.MaxFileSize > 0 {
			streamConfig.MaxFileSize = specificConfig.MaxFileSize
		}
		if specificConfig.EnableSegments != nil {
			streamConfig.EnableSegments = specificConfig.EnableSegments
		}
		if specificConfig.RetentionDays > 0 {
			streamConfig.RetentionDays = specificConfig.RetentionDays
		}
		if specificConfig.RetentionHours > 0 {
			streamConfig.RetentionHours = specificConfig.RetentionHours
		}
		if specificConfig.MaxRecordings > 0 {
			streamConfig.MaxRecordings = specificConfig.MaxRecordings
		}
		if specificConfig.AutoStart != nil {
			streamConfig.AutoStart = specificConfig.AutoStart
		}
		if specificConfig.RestartOnError != nil {
			streamConfig.RestartOnError = specificConfig.RestartOnError
		}
		if specificConfig.PathTemplate != "" {
			streamConfig.PathTemplate = specificConfig.PathTemplate
		}
		if specificConfig.FilenameTemplate != "" {
			streamConfig.FilenameTemplate = specificConfig.FilenameTemplate
		}
		if specificConfig.Width > 0 {
			streamConfig.Width = specificConfig.Width
		}
		if specificConfig.Height > 0 {
			streamConfig.Height = specificConfig.Height
		}
		if specificConfig.Framerate > 0 {
			streamConfig.Framerate = specificConfig.Framerate
		}
		if specificConfig.Schedule != "" {
			streamConfig.Schedule = specificConfig.Schedule
		}
		streamConfig.RecordOnMotion = specificConfig.RecordOnMotion
	}
	
	// Resolve direct source after all overrides (this ensures stream-specific sources take priority)
	if streamConfig.Source == "" {
		streamConfig.Source = ResolveDirectSource(streamName)
	}
	
	// Debug logging for direct source resolution
	if streamConfig.Source != "" {
		log.Debug().
			Str("stream", streamName).
			Str("resolved_source", streamConfig.Source).
			Msg("[config] resolved direct RTSP source for stream")
	}
	
	return streamConfig
}

// GetStreamsToAutoRecord returns a list of streams that should be auto-recorded
func GetStreamsToAutoRecord() []string {
	cfg := GlobalRecordingConfig
	var streamsToRecord []string
	
	// Check each configured stream
	for streamName, streamConfig := range cfg.Streams {
		if streamConfig.Enabled != nil && *streamConfig.Enabled {
			streamsToRecord = append(streamsToRecord, streamName)
		} else if streamConfig.AutoStart != nil && *streamConfig.AutoStart {
			streamsToRecord = append(streamsToRecord, streamName)
		}
	}
	
	// If global auto_start is enabled and no specific streams are configured,
	// we'll need to get the list from the streams module (done elsewhere)
	
	return streamsToRecord
}

// ShouldAutoStartRecording returns true if recording should auto-start for the stream
func ShouldAutoStartRecording(streamName string) bool {
	return IsStreamRecordingEnabled(streamName)
}

// ResolveDirectSource resolves the direct source URL for a stream
func ResolveDirectSource(streamName string) string {
	cfg := GlobalRecordingConfig
	
	// Check if there's a stream-specific direct source
	if streamConfig, exists := cfg.Streams[streamName]; exists && streamConfig.Source != "" {
		log.Debug().
			Str("stream", streamName).
			Str("source", streamConfig.Source).
			Msg("[config] using per-stream direct source")
		return streamConfig.Source
	}
	
	// Check if there's a global direct source template
	if cfg.DirectSource != "" {
		// Replace {stream} placeholder with actual stream name
		directSource := strings.ReplaceAll(cfg.DirectSource, "{stream}", streamName)
		log.Debug().
			Str("stream", streamName).
			Str("template", cfg.DirectSource).
			Str("resolved", directSource).
			Msg("[config] using global direct source template")
		return directSource
	}
	
	// No direct source configured, will use go2rtc internal routing
	log.Debug().
		Str("stream", streamName).
		Msg("[config] no direct source configured, using internal RTSP")
	return ""
}

// GetRecordingSource returns the source URL for recording (either direct or internal RTSP)
func GetRecordingSource(streamName string, internalRTSPPort string) string {
	directSource := ResolveDirectSource(streamName)
	
	if directSource != "" {
		// Use direct source
		return directSource
	}
	
	// Use internal RTSP server
	return fmt.Sprintf("rtsp://127.0.0.1:%s/%s", internalRTSPPort, streamName)
}