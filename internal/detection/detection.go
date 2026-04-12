package detection

import (
	"time"

	"github.com/AlexxIT/go2rtc/internal/app"
	"github.com/AlexxIT/go2rtc/internal/api"
	"github.com/rs/zerolog"
)

var log zerolog.Logger

// DetectionConfig is the top-level detection configuration block.
type DetectionConfig struct {
	Enabled       bool     `yaml:"enabled"`
	BackendURL    string   `yaml:"backend_url"`    // CodeProject.AI / DeepStack base URL
	FrameInterval int      `yaml:"frame_interval"` // seconds between sampled frames (default 1)
	MinConfidence float64  `yaml:"min_confidence"` // default 0.45
	Labels        []string `yaml:"labels"`         // filter to these classes; empty = all
	RetentionDays int      `yaml:"retention_days"` // prune sidecar JSON older than N days
}

var GlobalDetectionConfig = &DetectionConfig{
	Enabled:       false,
	BackendURL:    "http://127.0.0.1:32168", // CodeProject.AI default port
	FrameInterval: 1,
	MinConfidence: 0.45,
	Labels:        []string{"person", "car", "truck", "motorcycle", "cat", "dog", "bird"},
	RetentionDays: 30,
}

var globalAnalyzer *Analyzer

func Init() {
	log = app.GetLogger("detection")

	var cfg struct {
		Recording struct {
			Detection DetectionConfig `yaml:"detection"`
		} `yaml:"recording"`
	}

	// Apply defaults
	cfg.Recording.Detection = *GlobalDetectionConfig
	app.LoadConfig(&cfg)
	*GlobalDetectionConfig = cfg.Recording.Detection

	if !GlobalDetectionConfig.Enabled {
		log.Info().Msg("[detection] disabled")
		return
	}

	if GlobalDetectionConfig.BackendURL == "" {
		log.Warn().Msg("[detection] backend_url not set, detection disabled")
		return
	}

	// Start analyzer
	globalAnalyzer = NewAnalyzer(GlobalDetectionConfig)
	globalAnalyzer.Start()

	// Register API endpoints
	api.HandleFunc("api/detection/status", apiDetectionStatus)
	api.HandleFunc("api/detection/analyze", apiDetectionAnalyze)

	log.Info().
		Str("backend_url", GlobalDetectionConfig.BackendURL).
		Int("frame_interval", GlobalDetectionConfig.FrameInterval).
		Float64("min_confidence", GlobalDetectionConfig.MinConfidence).
		Strs("labels", GlobalDetectionConfig.Labels).
		Msg("[detection] initialized")

	// Start retention cleanup goroutine
	if GlobalDetectionConfig.RetentionDays > 0 {
		go retentionLoop()
	}
}

// QueueFile enqueues a completed recording file for analysis.
// Called by the ffmpeg package when a segment closes.
func QueueFile(streamName, filePath string) {
	if globalAnalyzer == nil {
		return
	}
	globalAnalyzer.Enqueue(streamName, filePath)
}

// StreamDetectionOverride carries per-stream detection settings read from the
// recording config. Injected via SetStreamConfigReader to avoid circular imports.
type StreamDetectionOverride struct {
	Detection         bool
	DetectionInterval int
	DetectionLabels   []string
}

// getStreamConfig is injected by the ffmpeg package at startup.
var getStreamConfig func(streamName string) StreamDetectionOverride

// SetStreamConfigReader is called by ffmpeg.InitDetection to provide access to
// per-stream recording config without creating a circular import.
func SetStreamConfigReader(fn func(string) StreamDetectionOverride) {
	getStreamConfig = fn
}

// GetEffectiveConfig returns the merged global+stream detection config.
// Per-stream detection flag and overrides come from the recording stream config.
func GetEffectiveConfig(streamName string) (frameInterval int, minConfidence float64, labels []string, enabled bool) {
	cfg := GlobalDetectionConfig
	frameInterval = cfg.FrameInterval
	minConfidence = cfg.MinConfidence
	labels = cfg.Labels
	enabled = false // default off — must be explicitly enabled per stream

	if getStreamConfig != nil {
		sc := getStreamConfig(streamName)
		enabled = cfg.Enabled && sc.Detection
		if sc.DetectionInterval > 0 {
			frameInterval = sc.DetectionInterval
		}
		if len(sc.DetectionLabels) > 0 {
			labels = sc.DetectionLabels
		}
	}
	return
}

func retentionLoop() {
	for {
		time.Sleep(6 * time.Hour)
		if globalAnalyzer != nil {
			pruned := globalAnalyzer.PruneOldSidecars(GlobalDetectionConfig.RetentionDays)
			if pruned > 0 {
				log.Info().Int("pruned", pruned).Msg("[detection] pruned old sidecar files")
			}
		}
	}
}
