package ffmpeg

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// SegmentedRecording manages a recording that splits into multiple segments
type SegmentedRecording struct {
	ID               string
	Config           RecordConfig
	Stream           string
	StartTime        time.Time
	Active           bool
	
	currentSegment   int
	currentRecording *Recording
	segmentStartTime time.Time
	
	mu sync.Mutex
}

func NewSegmentedRecording(id, streamName string, config RecordConfig) *SegmentedRecording {
	return &SegmentedRecording{
		ID:               id,
		Config:           config,
		Stream:           streamName,
		StartTime:        time.Now(),
		Active:           false,
		currentSegment:   0,
	}
}

func (sr *SegmentedRecording) Start() error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	log.Info().
		Str("recording_id", sr.ID).
		Str("stream", sr.Stream).
		Msg("[segments] starting segmented recording")

	if sr.Active {
		return fmt.Errorf("segmented recording already active")
	}

	// Start first segment
	if err := sr.startNextSegment(); err != nil {
		log.Error().
			Err(err).
			Str("recording_id", sr.ID).
			Str("stream", sr.Stream).
			Msg("[segments] failed to start first segment")
		return err
	}

	sr.Active = true

	// Start segment management routine
	go sr.manageSegments()

	return nil
}

func (sr *SegmentedRecording) Stop() error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	if !sr.Active {
		return nil
	}

	// Stop current segment
	if sr.currentRecording != nil {
		sr.currentRecording.Stop()
		sr.currentRecording = nil
	}

	sr.Active = false
	return nil
}

func (sr *SegmentedRecording) startNextSegment() error {
	cfg := GlobalRecordingConfig
	
	sr.currentSegment++
	
	log.Info().
		Str("recording_id", sr.ID).
		Str("stream", sr.Stream).
		Int("segment", sr.currentSegment).
		Msg("[segments] starting new segment")
	
	// Stop current segment if running
	if sr.currentRecording != nil && sr.currentRecording.Active {
		log.Debug().
			Str("recording_id", sr.ID).
			Int("prev_segment", sr.currentSegment-1).
			Msg("[segments] stopping previous segment")
		sr.currentRecording.Stop()
	}

	// Generate filename for new segment
	now := time.Now()
	ext := filepath.Ext(sr.Config.Filename)
	format := sr.Config.Format
	if format == "" {
		if ext != "" {
			format = ext[1:] // Remove the dot
		} else {
			format = cfg.DefaultFormat
		}
	}

	segmentPath := GenerateRecordingPath(sr.Stream, now, format, sr.currentSegment)
	
	// Create new segment config
	segmentConfig := sr.Config
	segmentConfig.Filename = segmentPath
	segmentConfig.Duration = 0 // Let segment manager handle duration

	// Create and start new segment recording
	segmentID := fmt.Sprintf("%s_seg%d", sr.ID, sr.currentSegment)
	recording := NewRecording(segmentID, sr.Stream, segmentConfig)
	
	if err := recording.Start(); err != nil {
		return fmt.Errorf("failed to start segment %d: %w", sr.currentSegment, err)
	}

	sr.currentRecording = recording
	sr.segmentStartTime = now
	sr.currentSegment++

	log.Info().
		Str("recording", sr.ID).
		Str("segment", segmentPath).
		Int("segment_num", sr.currentSegment-1).
		Msg("[recording] started new segment")

	return nil
}

func (sr *SegmentedRecording) manageSegments() {
	ticker := time.NewTicker(time.Second * 30) // Check every 30 seconds
	defer ticker.Stop()

	for sr.Active {
		select {
		case <-ticker.C:
			sr.mu.Lock()
			shouldSegment := sr.shouldStartNewSegment()
			sr.mu.Unlock()

			if shouldSegment {
				sr.mu.Lock()
				if err := sr.startNextSegment(); err != nil {
					log.Error().Err(err).Str("recording", sr.ID).Msg("[recording] failed to start new segment")
				}
				sr.mu.Unlock()
			}

		case <-time.After(time.Minute):
			// Safety check - ensure recording is still active
			if !sr.Active {
				return
			}
		}
	}
}

func (sr *SegmentedRecording) shouldStartNewSegment() bool {
	cfg := GlobalRecordingConfig
	
	if sr.currentRecording == nil {
		return false
	}

	// Check duration-based segmentation
	if cfg.EnableSegments && cfg.SegmentDuration > 0 {
		segmentDuration := time.Since(sr.segmentStartTime)
		if segmentDuration >= cfg.SegmentDuration {
			return true
		}
	}

	// Check size-based segmentation
	if cfg.MaxFileSize > 0 {
		if stat, err := os.Stat(sr.currentRecording.Config.Filename); err == nil {
			sizeMB := stat.Size() / 1024 / 1024
			if sizeMB >= cfg.MaxFileSize {
				return true
			}
		}
	}

	return false
}

func (sr *SegmentedRecording) GetStatus() map[string]interface{} {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	status := map[string]interface{}{
		"id":              sr.ID,
		"stream":          sr.Stream,
		"type":            "segmented",
		"active":          sr.Active,
		"start_time":      sr.StartTime,
		"current_segment": sr.currentSegment,
		"total_duration":  time.Since(sr.StartTime),
	}

	if sr.currentRecording != nil {
		status["current_segment_status"] = sr.currentRecording.GetStatus()
		status["current_segment_file"] = sr.currentRecording.Config.Filename
	}

	return status
}

// GetAllSegments returns information about all segments for this recording
func (sr *SegmentedRecording) GetAllSegments() ([]map[string]interface{}, error) {
	// Find all segment files
	baseDir := filepath.Dir(sr.Config.Filename)
	baseName := filepath.Base(sr.Config.Filename)
	ext := filepath.Ext(baseName)
	nameWithoutExt := baseName[:len(baseName)-len(ext)]

	var segments []map[string]interface{}

	// Find all files that match the segment pattern
	err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		filename := info.Name()
		if !strings.Contains(filename, nameWithoutExt) {
			return nil
		}

		segments = append(segments, map[string]interface{}{
			"path":     path,
			"size":     info.Size(),
			"size_mb":  info.Size() / 1024 / 1024,
			"modified": info.ModTime(),
			"duration": sr.estimateFileDuration(path),
		})

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Sort segments by modification time
	sort.Slice(segments, func(i, j int) bool {
		t1 := segments[i]["modified"].(time.Time)
		t2 := segments[j]["modified"].(time.Time)
		return t1.Before(t2)
	})

	return segments, nil
}

// estimateFileDuration attempts to estimate file duration (basic implementation)
func (sr *SegmentedRecording) estimateFileDuration(filePath string) time.Duration {
	// This is a simple estimation based on file creation/modification patterns
	// In a real implementation, you might want to use ffprobe or similar tools
	cfg := GlobalRecordingConfig
	
	if cfg.EnableSegments && cfg.SegmentDuration > 0 {
		return cfg.SegmentDuration
	}

	// Fallback estimation based on file size (very rough)
	if stat, err := os.Stat(filePath); err == nil {
		sizeMB := stat.Size() / 1024 / 1024
		// Assume roughly 1MB per minute for typical video (very rough estimate)
		return time.Duration(sizeMB) * time.Minute
	}

	return 0
}

// SegmentedRecordingManager manages multiple segmented recordings
type SegmentedRecordingManager struct {
	recordings map[string]*SegmentedRecording
	mu         sync.RWMutex
}

var segmentedRecordingManager = &SegmentedRecordingManager{
	recordings: make(map[string]*SegmentedRecording),
}

func GetSegmentedRecordingManager() *SegmentedRecordingManager {
	return segmentedRecordingManager
}

func (srm *SegmentedRecordingManager) StartSegmentedRecording(id, streamName string, config RecordConfig) error {
	srm.mu.Lock()
	defer srm.mu.Unlock()

	if _, exists := srm.recordings[id]; exists {
		return fmt.Errorf("segmented recording with ID %s already exists", id)
	}

	recording := NewSegmentedRecording(id, streamName, config)
	if err := recording.Start(); err != nil {
		return err
	}

	srm.recordings[id] = recording

	// Auto-cleanup when recording stops
	go func() {
		for recording.Active {
			time.Sleep(time.Second)
		}
		srm.mu.Lock()
		delete(srm.recordings, id)
		srm.mu.Unlock()
	}()

	return nil
}

func (srm *SegmentedRecordingManager) StopSegmentedRecording(id string) error {
	srm.mu.Lock()
	defer srm.mu.Unlock()

	recording, exists := srm.recordings[id]
	if !exists {
		return fmt.Errorf("segmented recording with ID %s not found", id)
	}

	err := recording.Stop()
	delete(srm.recordings, id)
	return err
}

func (srm *SegmentedRecordingManager) GetSegmentedRecording(id string) *SegmentedRecording {
	srm.mu.RLock()
	defer srm.mu.RUnlock()
	return srm.recordings[id]
}

func (srm *SegmentedRecordingManager) ListSegmentedRecordings() map[string]*SegmentedRecording {
	srm.mu.RLock()
	defer srm.mu.RUnlock()

	result := make(map[string]*SegmentedRecording, len(srm.recordings))
	for id, recording := range srm.recordings {
		result[id] = recording
	}
	return result
}