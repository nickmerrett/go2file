package ffmpeg

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type CleanupRecordingInfo struct {
	Path     string
	ModTime  time.Time
	Size     int64
	Stream   string
}

// cleanupRoutine runs the cleanup process at regular intervals
func cleanupRoutine() {
	ticker := time.NewTicker(GlobalRecordingConfig.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := runCleanup(); err != nil {
				log.Error().Err(err).Msg("[recording] cleanup failed")
			}
		}
	}
}

// runCleanup performs the cleanup operation
func runCleanup() error {
	cfg := GlobalRecordingConfig
	
	log.Debug().Msg("[recording] starting cleanup")

	// Find all recording files
	recordings, err := findRecordingFiles(cfg.BasePath)
	if err != nil {
		return fmt.Errorf("failed to find recording files: %w", err)
	}

	log.Debug().Int("count", len(recordings)).Msg("[recording] found recordings")

	// Group recordings by stream
	streamRecordings := make(map[string][]CleanupRecordingInfo)
	for _, rec := range recordings {
		streamRecordings[rec.Stream] = append(streamRecordings[rec.Stream], rec)
	}

	// Apply cleanup policies
	for stream, recs := range streamRecordings {
		if err := cleanupStream(stream, recs); err != nil {
			log.Error().Err(err).Str("stream", stream).Msg("[recording] failed to cleanup stream")
		}
	}

	// Apply global size limits
	if cfg.MaxTotalSize > 0 {
		if err := enforceGlobalSizeLimit(recordings); err != nil {
			log.Error().Err(err).Msg("[recording] failed to enforce global size limit")
		}
	}

	log.Debug().Msg("[recording] cleanup completed")
	return nil
}

// findRecordingFiles recursively finds all recording files in the base path
func findRecordingFiles(basePath string) ([]CleanupRecordingInfo, error) {
	var recordings []CleanupRecordingInfo

	err := filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		// Check if this looks like a recording file
		ext := filepath.Ext(path)
		if !isRecordingFile(ext) {
			return nil
		}

		// Extract stream name from path
		streamName := extractStreamFromPath(path, basePath)

		recordings = append(recordings, CleanupRecordingInfo{
			Path:    path,
			ModTime: info.ModTime(),
			Size:    info.Size(),
			Stream:  streamName,
		})

		return nil
	})

	return recordings, err
}

// isRecordingFile checks if the file extension indicates a recording
func isRecordingFile(ext string) bool {
	recordingExtensions := []string{".mp4", ".mkv", ".avi", ".mov", ".ts", ".flv", ".webm"}
	for _, validExt := range recordingExtensions {
		if ext == validExt {
			return true
		}
	}
	return false
}

// extractStreamFromPath attempts to extract stream name from file path
func extractStreamFromPath(filePath, basePath string) string {
	relPath, err := filepath.Rel(basePath, filePath)
	if err != nil {
		return "unknown"
	}

	// Try to extract stream name from path structure
	parts := filepath.SplitList(relPath)
	if len(parts) > 0 {
		// Look for stream name in filename
		filename := filepath.Base(filePath)
		name := filename[:len(filename)-len(filepath.Ext(filename))]
		
		// Remove timestamp suffixes (assuming format stream_YYYY-MM-DD_HH-MM-SS)
		if idx := len(name) - 19; idx > 0 && idx < len(name) {
			if name[idx] == '_' {
				return name[:idx]
			}
		}
		
		// Fallback to using directory structure
		if len(parts) > 1 {
			return parts[len(parts)-2] // Assume stream name is parent directory
		}
	}

	return "unknown"
}

// cleanupStream applies cleanup policies to recordings for a specific stream
func cleanupStream(streamName string, recordings []CleanupRecordingInfo) error {
	cfg := GlobalRecordingConfig

	// Sort recordings by modification time (oldest first)
	sort.Slice(recordings, func(i, j int) bool {
		return recordings[i].ModTime.Before(recordings[j].ModTime)
	})

	var toDelete []CleanupRecordingInfo
	retentionDuration := GetRetentionDuration()
	cutoffTime := time.Now().Add(-retentionDuration)

	// Apply retention time policy
	for _, rec := range recordings {
		if rec.ModTime.Before(cutoffTime) {
			toDelete = append(toDelete, rec)
		}
	}

	// Apply max recordings per stream policy
	if cfg.MaxRecordings > 0 && len(recordings) > cfg.MaxRecordings {
		excess := recordings[:len(recordings)-cfg.MaxRecordings]
		for _, rec := range excess {
			// Only add if not already marked for deletion
			found := false
			for _, del := range toDelete {
				if del.Path == rec.Path {
					found = true
					break
				}
			}
			if !found {
				toDelete = append(toDelete, rec)
			}
		}
	}

	// Delete or archive the files
	for _, rec := range toDelete {
		if cfg.MoveToArchive && cfg.ArchivePath != "" {
			if err := archiveFile(rec, streamName); err != nil {
				log.Error().Err(err).Str("file", rec.Path).Msg("[recording] failed to archive file")
			} else {
				log.Info().Str("file", rec.Path).Str("stream", streamName).Msg("[recording] archived file")
			}
		} else {
			if err := os.Remove(rec.Path); err != nil {
				log.Error().Err(err).Str("file", rec.Path).Msg("[recording] failed to delete file")
			} else {
				log.Info().Str("file", rec.Path).Str("stream", streamName).Msg("[recording] deleted file")
			}
		}
	}

	return nil
}

// enforceGlobalSizeLimit ensures total recording size doesn't exceed limit
func enforceGlobalSizeLimit(recordings []CleanupRecordingInfo) error {
	cfg := GlobalRecordingConfig
	maxBytes := cfg.MaxTotalSize * 1024 * 1024 // Convert MB to bytes

	// Calculate total size
	var totalSize int64
	for _, rec := range recordings {
		totalSize += rec.Size
	}

	if totalSize <= maxBytes {
		return nil // Under limit
	}

	// Sort by modification time (oldest first)
	sort.Slice(recordings, func(i, j int) bool {
		return recordings[i].ModTime.Before(recordings[j].ModTime)
	})

	// Delete oldest files until under limit
	for _, rec := range recordings {
		if totalSize <= maxBytes {
			break
		}

		if cfg.MoveToArchive && cfg.ArchivePath != "" {
			if err := archiveFile(rec, rec.Stream); err != nil {
				log.Error().Err(err).Str("file", rec.Path).Msg("[recording] failed to archive file for size limit")
				continue
			}
		} else {
			if err := os.Remove(rec.Path); err != nil {
				log.Error().Err(err).Str("file", rec.Path).Msg("[recording] failed to delete file for size limit")
				continue
			}
		}

		totalSize -= rec.Size
		log.Info().
			Str("file", rec.Path).
			Int64("size_mb", rec.Size/1024/1024).
			Int64("total_mb", totalSize/1024/1024).
			Msg("[recording] removed file for size limit")
	}

	return nil
}

// archiveFile moves a file to the archive directory
func archiveFile(rec CleanupRecordingInfo, streamName string) error {
	cfg := GlobalRecordingConfig
	
	// Create archive path structure similar to original
	archiveSubPath := filepath.Join(streamName, rec.ModTime.Format("2006/01/02"))
	archiveDir := filepath.Join(cfg.ArchivePath, archiveSubPath)
	
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return fmt.Errorf("failed to create archive directory: %w", err)
	}

	archivePath := filepath.Join(archiveDir, filepath.Base(rec.Path))
	
	// Move the file
	if err := os.Rename(rec.Path, archivePath); err != nil {
		return fmt.Errorf("failed to move file to archive: %w", err)
	}

	return nil
}

// GetRecordingStats returns statistics about recordings
func GetRecordingStats() (map[string]interface{}, error) {
	cfg := GlobalRecordingConfig
	
	recordings, err := findRecordingFiles(cfg.BasePath)
	if err != nil {
		return nil, err
	}

	// Calculate statistics
	stats := map[string]interface{}{
		"total_recordings": len(recordings),
		"total_size_mb":    int64(0),
		"oldest_recording": time.Time{},
		"newest_recording": time.Time{},
		"streams":          make(map[string]int),
	}

	var totalSize int64
	var oldestTime, newestTime time.Time

	for i, rec := range recordings {
		totalSize += rec.Size
		
		if i == 0 {
			oldestTime = rec.ModTime
			newestTime = rec.ModTime
		} else {
			if rec.ModTime.Before(oldestTime) {
				oldestTime = rec.ModTime
			}
			if rec.ModTime.After(newestTime) {
				newestTime = rec.ModTime
			}
		}

		streamStats := stats["streams"].(map[string]int)
		streamStats[rec.Stream]++
	}

	stats["total_size_mb"] = totalSize / 1024 / 1024
	if len(recordings) > 0 {
		stats["oldest_recording"] = oldestTime
		stats["newest_recording"] = newestTime
	}

	return stats, nil
}

// CleanupNow triggers an immediate cleanup (useful for API calls)
func CleanupNow() error {
	log.Info().Msg("[recording] manual cleanup triggered")
	return runCleanup()
}