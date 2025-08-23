package ffmpeg

import (
	"fmt"
	"time"

	"github.com/AlexxIT/go2rtc/internal/streams"
)

// AutoRecordingManager handles automatic recording startup
type AutoRecordingManager struct {
	started bool
}

var autoRecordingManager = &AutoRecordingManager{}

// StartAutoRecordings begins automatic recording for configured streams
func StartAutoRecordings() {
	if autoRecordingManager.started {
		return
	}

	autoRecordingManager.started = true
	
	// Start monitoring routine
	go monitorAndAutoRecord()

	log.Info().Msg("[recording] auto-recording manager started")
}

// monitorAndAutoRecord monitors streams and starts recordings for configured streams
func monitorAndAutoRecord() {
	// Initial delay to ensure streams are initialized
	time.Sleep(time.Second * 5)

	ticker := time.NewTicker(time.Second * 30) // Check every 30 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			checkAndStartAutoRecordings()
		}
	}
}

// checkAndStartAutoRecordings checks all configured streams and starts recordings if needed
func checkAndStartAutoRecordings() {
	// Get all stream names
	allStreamNames := streams.GetAllNames()
	
	log.Debug().
		Strs("available_streams", allStreamNames).
		Msg("[auto-record] checking streams for auto-recording")
	
	// Check each stream
	for _, streamName := range allStreamNames {
		if ShouldAutoStartRecording(streamName) {
			// Check if recording is already active
			if isAlreadyRecording(streamName) {
				log.Debug().
					Str("stream", streamName).
					Msg("[auto-record] stream already recording, skipping")
				continue
			}

			// Check if stream is actually available
			stream := streams.Get(streamName)
			if stream == nil {
				log.Debug().
					Str("stream", streamName).
					Msg("[auto-record] stream not available, skipping")
				continue
			}

			// Start recording with stream-specific configuration
			streamConfig := GetStreamRecordingConfig(streamName)
			
			log.Info().
				Str("stream", streamName).
				Bool("segments_enabled", streamConfig.EnableSegments != nil && *streamConfig.EnableSegments).
				Dur("segment_duration", streamConfig.SegmentDuration).
				Str("video_codec", streamConfig.Video).
				Str("audio_codec", streamConfig.Audio).
				Msg("[auto-record] starting auto-recording")
				
			if err := startAutoRecording(streamName, streamConfig); err != nil {
				log.Error().Err(err).Str("stream", streamName).Msg("[auto-record] failed to start auto-recording")
			} else {
				log.Info().Str("stream", streamName).Msg("[auto-record] started auto-recording successfully")
			}
		} else {
			log.Debug().
				Str("stream", streamName).
				Msg("[auto-record] stream not configured for auto-recording")
		}
	}

	// Also check specifically configured streams that might not be in the global list
	streamsToRecord := GetStreamsToAutoRecord()
	for _, streamName := range streamsToRecord {
		if isAlreadyRecording(streamName) {
			continue
		}

		stream := streams.Get(streamName)
		if stream == nil {
			log.Debug().Str("stream", streamName).Msg("[recording] configured stream not available yet")
			continue
		}

		streamConfig := GetStreamRecordingConfig(streamName)
		if err := startAutoRecording(streamName, streamConfig); err != nil {
			log.Error().Err(err).Str("stream", streamName).Msg("[recording] failed to start configured auto-recording")
		} else {
			log.Info().Str("stream", streamName).Msg("[recording] started configured auto-recording")
		}
	}
}

// isAlreadyRecording checks if a stream is already being recorded
func isAlreadyRecording(streamName string) bool {
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

// startAutoRecording starts recording for a stream using its specific configuration
func startAutoRecording(streamName string, streamConfig StreamRecordingConfig) error {
	// Generate recording ID
	recordingID := fmt.Sprintf("auto_%s_%d", streamName, time.Now().Unix())

	// Build recording configuration
	config := RecordConfig{
		Video:    streamConfig.Video,
		Audio:    streamConfig.Audio,
		Format:   streamConfig.Format,
		Duration: 0, // Continuous recording
	}

	// Generate filename using stream-specific templates
	config.Filename = GenerateRecordingPathWithTemplates(
		streamName, 
		time.Now(), 
		streamConfig.Format,
		0,
		streamConfig.PathTemplate,
		streamConfig.FilenameTemplate,
	)

	// Start recording (segmented or regular based on configuration)
	if streamConfig.EnableSegments != nil && *streamConfig.EnableSegments {
		return GetSegmentedRecordingManager().StartSegmentedRecording(recordingID, streamName, config)
	} else {
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