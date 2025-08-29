package ffmpeg

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type CleanupRecordingInfo struct {
	Path          string
	ModTime       time.Time    // File modification time
	RecordingTime time.Time    // Actual recording start time from filename
	Size          int64
	Stream        string
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
	_, err := runCleanupWithStats()
	return err
}

// runCleanupWithStats performs cleanup and returns detailed statistics
func runCleanupWithStats() (*CleanupResult, error) {
	cfg := GlobalRecordingConfig
	
	result := &CleanupResult{
		DeletedFiles:  []string{},
		ArchivedFiles: []string{},
		Policies:      []string{},
	}
	

	// Find all recording files
	recordings, err := findRecordingFiles(cfg.BasePath)
	if err != nil {
		return result, fmt.Errorf("failed to find recording files: %w", err)
	}

	// Calculate total size before cleanup
	var totalSizeBefore int64
	for _, rec := range recordings {
		totalSizeBefore += rec.Size
	}
	result.TotalSizeBefore = totalSizeBefore / 1024 / 1024 // MB


	// Group recordings by stream
	streamRecordings := make(map[string][]CleanupRecordingInfo)
	streamsAffectedMap := make(map[string]bool)
	
	for _, rec := range recordings {
		streamRecordings[rec.Stream] = append(streamRecordings[rec.Stream], rec)
	}

	// Apply cleanup policies per stream
	for stream, recs := range streamRecordings {
		streamResult, err := cleanupStreamWithStats(stream, recs)
		if err != nil {
			log.Error().Err(err).Str("stream", stream).Msg("[recording] failed to cleanup stream")
			continue
		}
		
		// Merge stream results
		result.FilesDeleted += streamResult.FilesDeleted
		result.FilesArchived += streamResult.FilesArchived
		result.SpaceReclaimed += streamResult.SpaceReclaimed
		result.DeletedFiles = append(result.DeletedFiles, streamResult.DeletedFiles...)
		result.ArchivedFiles = append(result.ArchivedFiles, streamResult.ArchivedFiles...)
		
		if streamResult.FilesDeleted > 0 || streamResult.FilesArchived > 0 {
			streamsAffectedMap[stream] = true
		}
		
		result.Policies = append(result.Policies, streamResult.Policies...)
	}

	// Apply global size limits
	if cfg.MaxTotalSize > 0 {
		globalResult, err := enforceGlobalSizeLimitWithStats(recordings)
		if err != nil {
			log.Error().Err(err).Msg("[recording] failed to enforce global size limit")
		} else {
			// Merge global results
			result.FilesDeleted += globalResult.FilesDeleted
			result.FilesArchived += globalResult.FilesArchived
			result.SpaceReclaimed += globalResult.SpaceReclaimed
			result.DeletedFiles = append(result.DeletedFiles, globalResult.DeletedFiles...)
			result.ArchivedFiles = append(result.ArchivedFiles, globalResult.ArchivedFiles...)
			result.Policies = append(result.Policies, globalResult.Policies...)
		}
	}

	// Calculate final size
	finalRecordings, err := findRecordingFiles(cfg.BasePath)
	if err == nil {
		var totalSizeAfter int64
		for _, rec := range finalRecordings {
			totalSizeAfter += rec.Size
		}
		result.TotalSizeAfter = totalSizeAfter / 1024 / 1024 // MB
	}

	// Convert streams map to slice
	for stream := range streamsAffectedMap {
		result.StreamsAffected = append(result.StreamsAffected, stream)
	}

	log.Info().
		Int("files_deleted", result.FilesDeleted).
		Int("files_archived", result.FilesArchived).
		Int64("space_reclaimed_mb", result.SpaceReclaimed).
		Strs("streams_affected", result.StreamsAffected).
		Msg("[recording] cleanup completed with stats")

	return result, nil
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

		// Extract recording time from filename
		recordingTime := extractRecordingTimeFromPath(path)
		if recordingTime.IsZero() {
			// Fallback to file modification time if we can't parse filename
			recordingTime = info.ModTime()
		}

		recordings = append(recordings, CleanupRecordingInfo{
			Path:          path,
			ModTime:       info.ModTime(),
			RecordingTime: recordingTime,
			Size:          info.Size(),
			Stream:        streamName,
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

// cleanupStreamWithStats applies cleanup policies and returns statistics
func cleanupStreamWithStats(streamName string, recordings []CleanupRecordingInfo) (*CleanupResult, error) {
	cfg := GlobalRecordingConfig
	result := &CleanupResult{
		DeletedFiles:  []string{},
		ArchivedFiles: []string{},
		Policies:      []string{},
	}

	// Sort recordings by recording time (oldest first)
	sort.Slice(recordings, func(i, j int) bool {
		return recordings[i].RecordingTime.Before(recordings[j].RecordingTime)
	})

	var toDelete []CleanupRecordingInfo
	retentionDuration := GetRetentionDuration()
	cutoffTime := time.Now().Add(-retentionDuration)

	// Apply retention time policy (use recording time, not file modification time)
	for _, rec := range recordings {
		if rec.RecordingTime.Before(cutoffTime) {
			toDelete = append(toDelete, rec)
			log.Debug().
				Str("file", rec.Path).
				Time("recording_time", rec.RecordingTime).
				Time("cutoff_time", cutoffTime).
				Msg("[cleanup] marking file for deletion based on recording time")
		}
	}
	if len(toDelete) > 0 {
		result.Policies = append(result.Policies, fmt.Sprintf("retention_%s", streamName))
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
		result.Policies = append(result.Policies, fmt.Sprintf("max_count_%s", streamName))
	}

	// Process deletions/archives
	for _, rec := range toDelete {
		result.SpaceReclaimed += rec.Size / 1024 / 1024 // Convert to MB
		
		if cfg.MoveToArchive && cfg.ArchivePath != "" {
			if err := archiveFile(rec, streamName); err != nil {
				log.Error().Err(err).Str("file", rec.Path).Msg("[recording] failed to archive file")
			} else {
				result.FilesArchived++
				result.ArchivedFiles = append(result.ArchivedFiles, rec.Path)
				log.Info().Str("file", rec.Path).Str("stream", streamName).Msg("[recording] archived file")
			}
		} else {
			if err := os.Remove(rec.Path); err != nil {
				log.Error().Err(err).Str("file", rec.Path).Msg("[recording] failed to delete file")
			} else {
				result.FilesDeleted++
				result.DeletedFiles = append(result.DeletedFiles, rec.Path)
				log.Info().Str("file", rec.Path).Str("stream", streamName).Msg("[recording] deleted file")
			}
		}
	}

	return result, nil
}

// cleanupStream applies cleanup policies to recordings for a specific stream
func cleanupStream(streamName string, recordings []CleanupRecordingInfo) error {
	_, err := cleanupStreamWithStats(streamName, recordings)
	return err
}

// enforceGlobalSizeLimitWithStats enforces size limits and returns statistics
func enforceGlobalSizeLimitWithStats(recordings []CleanupRecordingInfo) (*CleanupResult, error) {
	cfg := GlobalRecordingConfig
	result := &CleanupResult{
		DeletedFiles:  []string{},
		ArchivedFiles: []string{},
		Policies:      []string{},
	}
	
	maxBytes := cfg.MaxTotalSize * 1024 * 1024 // Convert MB to bytes

	// Calculate total size
	var totalSize int64
	for _, rec := range recordings {
		totalSize += rec.Size
	}

	if totalSize <= maxBytes {
		return result, nil // Under limit
	}
	
	result.Policies = append(result.Policies, "global_size_limit")

	// Sort by recording time (oldest first)
	sort.Slice(recordings, func(i, j int) bool {
		return recordings[i].RecordingTime.Before(recordings[j].RecordingTime)
	})

	// Delete oldest files until under limit
	for _, rec := range recordings {
		if totalSize <= maxBytes {
			break
		}

		result.SpaceReclaimed += rec.Size / 1024 / 1024 // Convert to MB
		
		if cfg.MoveToArchive && cfg.ArchivePath != "" {
			if err := archiveFile(rec, rec.Stream); err != nil {
				log.Error().Err(err).Str("file", rec.Path).Msg("[recording] failed to archive file for size limit")
				continue
			}
			result.FilesArchived++
			result.ArchivedFiles = append(result.ArchivedFiles, rec.Path)
		} else {
			if err := os.Remove(rec.Path); err != nil {
				log.Error().Err(err).Str("file", rec.Path).Msg("[recording] failed to delete file for size limit")
				continue
			}
			result.FilesDeleted++
			result.DeletedFiles = append(result.DeletedFiles, rec.Path)
		}

		totalSize -= rec.Size
		log.Info().
			Str("file", rec.Path).
			Int64("size_mb", rec.Size/1024/1024).
			Int64("total_mb", totalSize/1024/1024).
			Msg("[recording] removed file for size limit")
	}

	return result, nil
}

// enforceGlobalSizeLimit ensures total recording size doesn't exceed limit
func enforceGlobalSizeLimit(recordings []CleanupRecordingInfo) error {
	_, err := enforceGlobalSizeLimitWithStats(recordings)
	return err
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

// CleanupResult contains statistics about cleanup operation
type CleanupResult struct {
	FilesDeleted    int                 `json:"files_deleted"`
	FilesArchived   int                 `json:"files_archived"`
	SpaceReclaimed  int64               `json:"space_reclaimed_mb"`
	DeletedFiles    []string            `json:"deleted_files"`
	ArchivedFiles   []string            `json:"archived_files"`
	StreamsAffected []string            `json:"streams_affected"`
	TotalSizeBefore int64               `json:"total_size_before_mb"`
	TotalSizeAfter  int64               `json:"total_size_after_mb"`
	Policies        []string            `json:"policies_applied"`
}

// CleanupNow triggers an immediate cleanup (useful for API calls)
func CleanupNow() error {
	log.Info().Msg("[recording] manual cleanup triggered")
	return runCleanup()
}

// CleanupNowWithStats triggers cleanup and returns detailed statistics
func CleanupNowWithStats() (*CleanupResult, error) {
	log.Info().Msg("[recording] manual cleanup with stats triggered")
	return runCleanupWithStats()
}

// extractRecordingTimeFromPath extracts the recording start time from filename
// Supports formats like: stream_2025-01-15_14-30-25.mp4, stream_20250115_143025.mp4
func extractRecordingTimeFromPath(filePath string) time.Time {
	filename := filepath.Base(filePath)
	nameWithoutExt := strings.TrimSuffix(filename, filepath.Ext(filename))
	
	// Common timestamp patterns in recording filenames
	patterns := []string{
		// stream_2025-01-15_14-30-25 format
		`(\d{4})-(\d{2})-(\d{2})_(\d{2})-(\d{2})-(\d{2})`,
		// stream_20250115_143025 format
		`(\d{4})(\d{2})(\d{2})_(\d{2})(\d{2})(\d{2})`,
		// stream_2025-01-15T14:30:25 ISO format
		`(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(nameWithoutExt)
		if len(matches) >= 7 {
			// Parse the matched groups
			year, month, day := matches[1], matches[2], matches[3]
			hour, min, sec := matches[4], matches[5], matches[6]
			
			// Construct timestamp string
			timestampStr := fmt.Sprintf("%s-%s-%s %s:%s:%s", year, month, day, hour, min, sec)
			
			// Parse timestamp
			if parsedTime, err := time.ParseInLocation("2006-01-02 15:04:05", timestampStr, time.Local); err == nil {
				log.Debug().
					Str("filename", filename).
					Time("extracted_time", parsedTime).
					Msg("[cleanup] extracted recording time from filename")
				return parsedTime
			}
		}
	}
	
	// If no timestamp found in filename, return zero time
	return time.Time{}
}

// ForceCleanupOldRecordings performs aggressive cleanup ignoring normal retention rules
func ForceCleanupOldRecordings(olderThanDays int, dryRun bool) (*CleanupResult, error) {
	cfg := GlobalRecordingConfig
	
	result := &CleanupResult{
		DeletedFiles:  []string{},
		ArchivedFiles: []string{},
		Policies:      []string{"force_cleanup"},
	}
	
	log.Info().
		Int("older_than_days", olderThanDays).
		Bool("dry_run", dryRun).
		Msg("[cleanup] starting aggressive cleanup")

	// Find all recording files
	recordings, err := findRecordingFiles(cfg.BasePath)
	if err != nil {
		return result, fmt.Errorf("failed to find recording files: %w", err)
	}

	// Calculate total size before cleanup
	var totalSizeBefore int64
	for _, rec := range recordings {
		totalSizeBefore += rec.Size
	}
	result.TotalSizeBefore = totalSizeBefore / 1024 / 1024 // MB
	
	// Calculate cutoff time
	cutoffTime := time.Now().Add(-time.Duration(olderThanDays) * 24 * time.Hour)
	
	// Sort by recording time (oldest first)
	sort.Slice(recordings, func(i, j int) bool {
		return recordings[i].RecordingTime.Before(recordings[j].RecordingTime)
	})
	
	log.Info().
		Int("total_files", len(recordings)).
		Time("cutoff_time", cutoffTime).
		Msg("[cleanup] scanning files for aggressive cleanup")

	// Process each file
	for _, rec := range recordings {
		// Use recording time if available, otherwise fall back to file time
		timeToCheck := rec.RecordingTime
		if timeToCheck.IsZero() {
			timeToCheck = rec.ModTime
		}
		
		if timeToCheck.Before(cutoffTime) {
			result.SpaceReclaimed += rec.Size / 1024 / 1024 // Convert to MB
			
			if dryRun {
				log.Info().
					Str("file", rec.Path).
					Time("recording_time", timeToCheck).
					Int64("size_mb", rec.Size/1024/1024).
					Msg("[cleanup] DRY RUN: would delete file")
				result.DeletedFiles = append(result.DeletedFiles, rec.Path)
			} else {
				// Actually delete the file
				if err := os.Remove(rec.Path); err != nil {
					log.Error().Err(err).Str("file", rec.Path).Msg("[cleanup] failed to delete file")
				} else {
					result.FilesDeleted++
					result.DeletedFiles = append(result.DeletedFiles, rec.Path)
					log.Info().
						Str("file", rec.Path).
						Time("recording_time", timeToCheck).
						Int64("size_mb", rec.Size/1024/1024).
						Msg("[cleanup] force deleted file")
				}
			}
		}
	}

	// Calculate final size
	if !dryRun {
		finalRecordings, err := findRecordingFiles(cfg.BasePath)
		if err == nil {
			var totalSizeAfter int64
			for _, rec := range finalRecordings {
				totalSizeAfter += rec.Size
			}
			result.TotalSizeAfter = totalSizeAfter / 1024 / 1024 // MB
		}
	}

	log.Info().
		Int("files_deleted", result.FilesDeleted).
		Int64("space_reclaimed_mb", result.SpaceReclaimed).
		Bool("dry_run", dryRun).
		Msg("[cleanup] aggressive cleanup completed")

	return result, nil
}