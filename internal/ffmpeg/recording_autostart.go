package ffmpeg

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/AlexxIT/go2rtc/internal/streams"
)

// AutoRecordingManager handles automatic recording startup
type AutoRecordingManager struct {
	started bool
	failedStreams map[string]time.Time // Track failed streams and when they failed
	mu sync.Mutex
}

var autoRecordingManager = &AutoRecordingManager{
	failedStreams: make(map[string]time.Time),
}

// StartAutoRecordings begins automatic recording for configured streams
func StartAutoRecordings() {
	if autoRecordingManager.started {
		return
	}

	autoRecordingManager.started = true
	
	// Start all enabled recordings immediately in parallel
	go startAllEnabledRecordings()
	
	// Start monitoring routine for ongoing checks
	go monitorAndAutoRecord()

	log.Info().Msg("[recording] auto-recording manager started")
}

// getStreamsToRecord returns list of streams that should be recorded
func getStreamsToRecord() []string {
	cfg := GlobalRecordingConfig
	streamsToRecord := []string{}
	
	// Case 1: Global auto_start with no specific stream configs - record all available streams
	if cfg.AutoStart && len(cfg.Streams) == 0 {
		allStreamNames := streams.GetAllNames()
		log.Debug().
			Strs("all_streams", allStreamNames).
			Msg("[recording] global auto_start mode - will record all available streams")
		return allStreamNames
	}
	
	// Case 2: Specific stream configurations - only record explicitly configured streams
	for streamName, streamConfig := range cfg.Streams {
		if streamConfig.Enabled != nil && *streamConfig.Enabled {
			streamsToRecord = append(streamsToRecord, streamName)
			log.Debug().
				Str("stream", streamName).
				Bool("enabled", *streamConfig.Enabled).
				Str("source", streamConfig.Source).
				Msg("[recording] stream explicitly enabled for recording")
		} else if streamConfig.Enabled == nil {
			// Stream configured but no explicit enabled field - default to enabled
			streamsToRecord = append(streamsToRecord, streamName)
			log.Debug().
				Str("stream", streamName).
				Str("source", streamConfig.Source).
				Msg("[recording] stream configured without explicit enabled, defaulting to enabled")
		}
	}
	
	log.Info().
		Strs("streams_to_record", streamsToRecord).
		Int("total_configured_streams", len(cfg.Streams)).
		Bool("global_auto_start", cfg.AutoStart).
		Msg("[recording] determined streams to record")
	
	return streamsToRecord
}

// startAllEnabledRecordings starts all configured recordings in parallel at startup
func startAllEnabledRecordings() {
	// Longer initial delay to ensure RTSP server and exec module are fully initialized
	log.Info().Msg("[recording] waiting for RTSP server initialization")
	time.Sleep(time.Second * 15)
	
	// Get streams that should be recorded (combination of available streams and configured direct sources)
	streamsToRecord := getStreamsToRecord()
	log.Info().
		Int("stream_count", len(streamsToRecord)).
		Strs("streams", streamsToRecord).
		Msg("[recording] starting auto-recordings for configured streams")
	
	// Start all enabled recordings sequentially to avoid race conditions, then let them run in parallel
	var wg sync.WaitGroup
	for i, streamName := range streamsToRecord {
		// We already filtered for streams that should record, so start all of them
		wg.Add(1)
		// Launch each recording in its own goroutine but with proper synchronization
		go func(stream string, index int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					log.Error().
						Interface("panic", r).
						Str("stream", stream).
						Msg("[recording] recovered from panic in recording goroutine")
				}
			}()
				
			
			// Add a small staggered delay to prevent race conditions
			time.Sleep(time.Millisecond * time.Duration(index * 200))
			
			// Check if recording is already active (including FFmpeg processes)
			if isStreamActuallyRecording(stream) {
				log.Info().
					Str("stream", stream).
					Msg("[recording] stream already recording, skipping")
				return
			}
			
			// Get stream-specific configuration first
			streamConfig := GetStreamRecordingConfig(stream)
			
			// Check if stream is available or if it has a direct source configured
			streamObj := streams.Get(stream)
			if streamObj == nil && streamConfig.Source == "" {
				// No internal stream and no direct source configured
				return
			}
			
			if err := startAutoRecording(stream, streamConfig); err != nil {
				log.Error().Err(err).Str("stream", stream).Msg("[recording] failed to start auto-recording")
			} else {
				log.Info().Str("stream", stream).Msg("[recording] started auto-recording")
			}
		}(streamName, i)
	}
	
	// Wait for all recordings to complete startup
	go func() {
		wg.Wait()
		log.Info().Msg("[recording] auto-recording startup completed")
	}()
}

// monitorAndAutoRecord monitors streams and starts recordings for configured streams
func monitorAndAutoRecord() {
	// Initial delay to ensure streams are initialized
	time.Sleep(time.Second * 5)

	// Use configurable check interval
	checkInterval := GlobalRecordingConfig.AutoRecordCheckInterval
	if checkInterval <= 0 {
		checkInterval = time.Second * 10 // Default to 10 seconds
	}

	log.Info().
		Dur("check_interval", checkInterval).
		Msg("[recording] starting auto-record monitoring")

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Error().
							Interface("panic", r).
							Msg("[recording] recovered from panic in monitoring loop")
					}
				}()
				checkAndStartAutoRecordings()
			}()
		}
	}
}

// checkAndStartAutoRecordings checks all configured streams and starts recordings if needed
func checkAndStartAutoRecordings() {
	// Get only the streams that should be recorded
	streamsToCheck := getStreamsToRecord()
	
	// Check each configured stream
	for _, streamName := range streamsToCheck {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Error().
						Interface("panic", r).
						Str("stream", streamName).
						Msg("[recording] recovered from panic during stream processing")
				}
			}()
			
			// We already filtered for streams that should record, so check if already recording
			actuallyRecording := isStreamActuallyRecording(streamName)
				
			if !actuallyRecording {

				// Check if stream is available or if it has a direct source configured
				streamConfig := GetStreamRecordingConfig(streamName)
				stream := streams.Get(streamName)
				if stream == nil && streamConfig.Source == "" {
					// No internal stream and no direct source configured
					return
				}

				if err := startAutoRecording(streamName, streamConfig); err != nil {
					log.Error().Err(err).Str("stream", streamName).Msg("[recording] failed to start auto-recording")
				} else {
					log.Info().Str("stream", streamName).Msg("[recording] started auto-recording")
				}
			} else {
				log.Debug().
					Str("stream", streamName).
					Msg("[recording] stream already recording, skipping")
			}
		}()
	}

	// Note: Removed redundant second loop that was causing duplicate recordings
	// The getStreamsToRecord() function above already handles all configured streams properly
}

// isAlreadyRecording checks if a stream is already being recorded
func isAlreadyRecording(streamName string) bool {
	defer func() {
		if r := recover(); r != nil {
			log.Error().
				Interface("panic", r).
				Str("stream", streamName).
				Msg("[recording] panic in isAlreadyRecording function")
		}
	}()
	
	// Check regular recordings
	regularRecordings := GetRecordingManager().ListRecordings()
		
	for _, recording := range regularRecordings {
		if recording.Stream == streamName && recording.Active {
			return true
		}
	}

	// Check segmented recordings
	segmentedRecordings := GetSegmentedRecordingManager().ListSegmentedRecordings()
		
	for _, recording := range segmentedRecordings {
		if recording.Stream == streamName && recording.Active {
			return true
		}
	}
	return false
}

// isFFmpegProcessRunning checks if any FFmpeg process is recording for the given stream
func isFFmpegProcessRunning(streamName string) bool {
	// Use pgrep to find FFmpeg processes that contain the stream name
	cmd := fmt.Sprintf("pgrep -f 'ffmpeg.*rtsp://.*/%s'", streamName)
	
	result, err := exec.Command("sh", "-c", cmd).Output()
	if err != nil {
		// pgrep returns exit code 1 if no processes found, which is normal
		return false
	}
	
	pids := strings.TrimSpace(string(result))
	if pids != "" {
		return true
	}
	
	return false
}

// isStreamActuallyRecording combines internal state and process checks
func isStreamActuallyRecording(streamName string) bool {
	// First check internal recording state
	internalRecording := isAlreadyRecording(streamName)
	
	// Then check actual FFmpeg processes
	processRunning := isFFmpegProcessRunning(streamName)
	
	// Stream is recording if either internal state shows active OR FFmpeg process is running
	return internalRecording || processRunning
}

// startAutoRecording starts recording for a stream using its specific configuration
func startAutoRecording(streamName string, streamConfig StreamRecordingConfig) error {
	// Generate recording ID
	recordingID := fmt.Sprintf("auto_%s_%d", streamName, time.Now().Unix())
	
	// Create recording configuration
	config := RecordConfig{
		Video:    streamConfig.Video,
		Audio:    streamConfig.Audio,
		Format:   streamConfig.Format,
		Duration: 0, // Continuous recording
	}
	
	// Generate filename if not provided
	if config.Filename == "" {
		config.Filename = GenerateRecordingPath(streamName, time.Now(), config.Format, 0)
	}
	
	// Start recording using the appropriate manager
	if streamConfig.EnableSegments != nil && *streamConfig.EnableSegments {
		// Start segmented recording
		return GetSegmentedRecordingManager().StartSegmentedRecording(recordingID, streamName, config)
	} else {
		// Start regular recording
		return GetRecordingManager().StartRecording(recordingID, streamName, config)
	}
}

// GenerateRecordingPathWithTemplates creates the full path using custom templates
func GenerateRecordingPathWithTemplates(streamName string, startTime time.Time, format string, segmentNum int, pathTemplate, filenameTemplate string) string {
	cfg := GlobalRecordingConfig

	// Use custom templates if provided, otherwise fall back to global
	if pathTemplate == "" {
		pathTemplate = cfg.PathTemplate
	}
	if filenameTemplate == "" {
		filenameTemplate = cfg.FilenameTemplate
	}

	// Use the existing function but with custom templates
	originalPathTemplate := cfg.PathTemplate
	originalFilenameTemplate := cfg.FilenameTemplate

	// Temporarily override templates
	cfg.PathTemplate = pathTemplate
	cfg.FilenameTemplate = filenameTemplate

	// Generate path
	path := GenerateRecordingPath(streamName, startTime, format, segmentNum)

	// Restore original templates
	cfg.PathTemplate = originalPathTemplate
	cfg.FilenameTemplate = originalFilenameTemplate

	return path
}

// StopAutoRecordings stops all auto-started recordings
func StopAutoRecordings() {
	log.Info().Msg("[recording] stopping all auto-recordings")

	// Stop all recordings that were auto-started (have "auto_" prefix)
	regularRecordings := GetRecordingManager().ListRecordings()
	for id, recording := range regularRecordings {
		if recording.Active && len(id) > 5 && id[:5] == "auto_" {
			if err := GetRecordingManager().StopRecording(id); err != nil {
				log.Error().Err(err).Str("id", id).Msg("[recording] failed to stop auto-recording")
			} else {
				log.Info().Str("id", id).Msg("[recording] stopped auto-recording")
			}
		}
	}

	segmentedRecordings := GetSegmentedRecordingManager().ListSegmentedRecordings()
	for id, recording := range segmentedRecordings {
		if recording.Active && len(id) > 5 && id[:5] == "auto_" {
			if err := GetSegmentedRecordingManager().StopSegmentedRecording(id); err != nil {
				log.Error().Err(err).Str("id", id).Msg("[recording] failed to stop auto segmented recording")
			} else {
				log.Info().Str("id", id).Msg("[recording] stopped auto segmented recording")
			}
		}
	}

	autoRecordingManager.started = false
}