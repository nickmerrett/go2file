package ffmpeg

import (
	"fmt"
	"os"
	"os/exec"
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

// HealthCheckResult contains health check information
type HealthCheckResult struct {
	Healthy              bool
	ActiveFFmpegProcesses int
	ExpectedRecordings   int
	NewestRecordingAge   time.Duration
	StreamsWithIssues    []string
	Warnings             []string
}

// shouldProtectFromCleanup determines if a file should be protected from deletion
func shouldProtectFromCleanup(rec CleanupRecordingInfo, streamRecordingCount int, totalRecordingCount int) (bool, string) {
	cfg := GlobalRecordingConfig

	// Check minimum files per stream
	minPerStream := cfg.MinimumFilesPerStream
	if minPerStream <= 0 {
		minPerStream = 5
	}
	if streamRecordingCount <= minPerStream {
		return true, fmt.Sprintf("minimum files per stream (%d)", minPerStream)
	}

	// Check minimum total files
	minTotal := cfg.MinimumTotalFiles
	if minTotal <= 0 {
		minTotal = 10
	}
	if totalRecordingCount <= minTotal {
		return true, fmt.Sprintf("minimum total files (%d)", minTotal)
	}

	// Check recent file protection
	protectRecent := cfg.ProtectRecentFiles
	if protectRecent <= 0 {
		protectRecent = time.Hour
	}
	if time.Since(rec.ModTime) < protectRecent {
		return true, fmt.Sprintf("file is recent (within %v)", protectRecent)
	}

	return false, ""
}

// getStreamRecordingCounts returns recording counts per stream and total
func getStreamRecordingCounts(recordings []CleanupRecordingInfo) (map[string]int, int) {
	streamCounts := make(map[string]int)
	for _, rec := range recordings {
		streamCounts[rec.Stream]++
	}
	return streamCounts, len(recordings)
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

// healthCheckRoutine runs independent health checks at regular intervals
func healthCheckRoutine() {
	// Initial delay to let recordings start
	time.Sleep(time.Minute * 2)

	log.Info().
		Dur("interval", GlobalRecordingConfig.HealthCheckInterval).
		Msg("[health-check] starting independent health check routine")

	ticker := time.NewTicker(GlobalRecordingConfig.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			performHealthCheckAndRecover()
		}
	}
}

// performHealthCheckAndRecover runs health check and attempts recovery if needed
func performHealthCheckAndRecover() {
	healthCheck := performHealthCheck()

	// Log health status
	if healthCheck.Healthy {
		log.Info().
			Int("active_ffmpeg_processes", healthCheck.ActiveFFmpegProcesses).
			Int("expected_recordings", healthCheck.ExpectedRecordings).
			Dur("newest_recording_age", healthCheck.NewestRecordingAge).
			Msg("[health-check] recording system healthy")
	} else {
		log.Warn().
			Bool("healthy", healthCheck.Healthy).
			Int("active_ffmpeg_processes", healthCheck.ActiveFFmpegProcesses).
			Int("expected_recordings", healthCheck.ExpectedRecordings).
			Dur("newest_recording_age", healthCheck.NewestRecordingAge).
			Strs("warnings", healthCheck.Warnings).
			Strs("streams_with_issues", healthCheck.StreamsWithIssues).
			Msg("[health-check] UNHEALTHY recording system detected")

		// Attempt recovery
		attemptRecovery(healthCheck)
	}
}

// runCleanup performs the cleanup operation
func runCleanup() error {
	// Perform health check before cleanup
	healthCheck := performHealthCheck()

	// Log health check results
	log.Info().
		Bool("healthy", healthCheck.Healthy).
		Int("active_ffmpeg_processes", healthCheck.ActiveFFmpegProcesses).
		Int("expected_recordings", healthCheck.ExpectedRecordings).
		Dur("newest_recording_age", healthCheck.NewestRecordingAge).
		Strs("warnings", healthCheck.Warnings).
		Msg("[recording] health check before cleanup")

	// If recording system is not healthy, take recovery actions
	if !healthCheck.Healthy {
		log.Warn().
			Strs("streams_with_issues", healthCheck.StreamsWithIssues).
			Strs("warnings", healthCheck.Warnings).
			Msg("[recording] UNHEALTHY recording system detected - attempting recovery")

		// Attempt to recover failed streams
		attemptRecovery(healthCheck)

		// Skip cleanup to prevent data loss while system is recovering
		log.Warn().Msg("[recording] SKIPPING cleanup due to unhealthy recording system")
		return fmt.Errorf("skipping cleanup: recording system unhealthy")
	}

	// Pre-check: Verify we're not at minimum file thresholds before cleanup
	cfg := GlobalRecordingConfig
	recordings, err := findRecordingFiles(cfg.BasePath)
	if err != nil {
		return fmt.Errorf("failed to find recording files for pre-check: %w", err)
	}

	streamCounts, totalCount := getStreamRecordingCounts(recordings)

	// Check minimum total files
	minTotal := cfg.MinimumTotalFiles
	if minTotal <= 0 {
		minTotal = 10
	}
	if totalCount <= minTotal {
		log.Info().
			Int("total_files", totalCount).
			Int("minimum_required", minTotal).
			Msg("[recording] skipping cleanup - at minimum total file threshold")
		return nil
	}

	// Check if all streams are at minimum
	minPerStream := cfg.MinimumFilesPerStream
	if minPerStream <= 0 {
		minPerStream = 5
	}

	allAtMinimum := true
	for stream, count := range streamCounts {
		if count > minPerStream {
			allAtMinimum = false
		} else {
			log.Debug().
				Str("stream", stream).
				Int("count", count).
				Int("minimum", minPerStream).
				Msg("[cleanup] stream at minimum threshold")
		}
	}

	if allAtMinimum && len(streamCounts) > 0 {
		log.Info().Msg("[recording] skipping cleanup - all streams at minimum file threshold")
		return nil
	}

	_, err = runCleanupWithStats()
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
	
	// Log cleanup configuration for visibility
	retentionDuration := GetRetentionDuration()
	log.Info().
		Str("base_path", cfg.BasePath).
		Dur("retention_duration", retentionDuration).
		Int("max_recordings_per_stream", cfg.MaxRecordings).
		Int64("max_total_size_mb", cfg.MaxTotalSize).
		Bool("move_to_archive", cfg.MoveToArchive).
		Str("archive_path", cfg.ArchivePath).
		Msg("[recording] starting cleanup with configuration")

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
	
	log.Info().
		Int("total_files", len(recordings)).
		Int64("total_size_mb", result.TotalSizeBefore).
		Msg("[recording] found recordings before cleanup")


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
		log.Info().
			Int64("max_total_size_mb", cfg.MaxTotalSize).
			Int64("current_size_mb", result.TotalSizeBefore).
			Msg("[recording] checking global size limit")
			
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
	} else {
		log.Debug().Msg("[recording] global size limit disabled (MaxTotalSize = 0)")
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

	// Log detailed cleanup summary
	log.Info().
		Int("files_deleted", result.FilesDeleted).
		Int("files_archived", result.FilesArchived).
		Int64("space_reclaimed_mb", result.SpaceReclaimed).
		Int64("size_before_mb", result.TotalSizeBefore).
		Int64("size_after_mb", result.TotalSizeAfter).
		Strs("streams_affected", result.StreamsAffected).
		Strs("policies_applied", result.Policies).
		Msg("[recording] cleanup completed with stats")
		
	if result.FilesDeleted == 0 && result.FilesArchived == 0 {
		log.Debug().Msg("[recording] no files needed cleanup")
	}

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

	// Split the relative path into directory components
	parts := strings.Split(relPath, string(filepath.Separator))
	
	// For paths like "upstairs/upstairs_2025-09-22_10-09-19.mp4"
	// parts[0] would be "upstairs" (the directory name)
	if len(parts) > 1 {
		// Stream name is the first directory under base path
		return parts[0]
	}
	
	// If no directory structure, try to extract from filename
	filename := filepath.Base(filePath)
	name := filename[:len(filename)-len(filepath.Ext(filename))]
	
	// Remove timestamp suffixes (assuming format stream_YYYY-MM-DD_HH-MM-SS)
	if idx := len(name) - 19; idx > 0 && idx < len(name) {
		if name[idx] == '_' {
			return name[:idx]
		}
	}
	
	// Final fallback - extract anything before the first underscore
	if underscoreIdx := strings.Index(name, "_"); underscoreIdx > 0 {
		return name[:underscoreIdx]
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

	// Get stream-specific configuration for more accurate limit enforcement
	streamConfig := GetStreamRecordingConfig(streamName)
	maxRecordings := cfg.MaxRecordings
	if streamConfig.MaxRecordings > 0 {
		maxRecordings = streamConfig.MaxRecordings
	}
	
	// Calculate stream size
	var streamSizeMB int64
	for _, rec := range recordings {
		streamSizeMB += rec.Size / 1024 / 1024
	}
	
	log.Info().
		Str("stream", streamName).
		Int("recording_count", len(recordings)).
		Int64("stream_size_mb", streamSizeMB).
		Int("max_recordings_limit", maxRecordings).
		Msg("[recording] processing stream cleanup")

	var toDelete []CleanupRecordingInfo
	// Use per-stream retention if configured, fall back to global
	var retentionDuration time.Duration
	if streamConfig.RetentionHours > 0 {
		retentionDuration = time.Duration(streamConfig.RetentionHours) * time.Hour
	} else if streamConfig.RetentionDays > 0 {
		retentionDuration = time.Duration(streamConfig.RetentionDays) * 24 * time.Hour
	} else {
		retentionDuration = GetRetentionDuration()
	}
	cutoffTime := time.Now().Add(-retentionDuration)

	log.Debug().
		Str("stream", streamName).
		Int("stream_retention_days", streamConfig.RetentionDays).
		Int("stream_retention_hours", streamConfig.RetentionHours).
		Dur("retention_duration", retentionDuration).
		Time("cutoff_time", cutoffTime).
		Msg("[recording] applying retention policy")

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

	// Apply max recordings per stream policy (use stream-specific limit if configured)
	if maxRecordings > 0 && len(recordings) > maxRecordings {
		excess := recordings[:len(recordings)-maxRecordings]
		log.Info().
			Str("stream", streamName).
			Int("current_count", len(recordings)).
			Int("max_allowed", maxRecordings).
			Int("excess_files", len(excess)).
			Msg("[recording] enforcing max recordings limit")
			
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
				log.Debug().
					Str("file", rec.Path).
					Time("recording_time", rec.RecordingTime).
					Msg("[cleanup] marking file for deletion due to max count limit")
				toDelete = append(toDelete, rec)
			}
		}
		result.Policies = append(result.Policies, fmt.Sprintf("max_count_%s", streamName))
	} else if maxRecordings > 0 {
		log.Debug().
			Str("stream", streamName).
			Int("current_count", len(recordings)).
			Int("max_allowed", maxRecordings).
			Msg("[recording] stream within max recordings limit")
	}

	// Get total recording count for protection check
	allRecordings, _ := findRecordingFiles(cfg.BasePath)
	_, totalCount := getStreamRecordingCounts(allRecordings)
	currentStreamCount := len(recordings)

	// Process deletions/archives with protection checks
	protectedCount := 0
	for _, rec := range toDelete {
		// Check if file should be protected
		protected, reason := shouldProtectFromCleanup(rec, currentStreamCount, totalCount)
		if protected {
			log.Info().
				Str("file", rec.Path).
				Str("stream", streamName).
				Str("reason", reason).
				Msg("[cleanup] file protected from deletion")
			protectedCount++
			continue
		}

		result.SpaceReclaimed += rec.Size / 1024 / 1024 // Convert to MB

		if cfg.MoveToArchive && cfg.ArchivePath != "" {
			if err := archiveFile(rec, streamName); err != nil {
				log.Error().Err(err).Str("file", rec.Path).Msg("[recording] failed to archive file")
			} else {
				result.FilesArchived++
				result.ArchivedFiles = append(result.ArchivedFiles, rec.Path)
				currentStreamCount--
				totalCount--
				log.Info().Str("file", rec.Path).Str("stream", streamName).Msg("[recording] archived file")
			}
		} else {
			if err := os.Remove(rec.Path); err != nil {
				log.Error().Err(err).Str("file", rec.Path).Msg("[recording] failed to delete file")
			} else {
				result.FilesDeleted++
				result.DeletedFiles = append(result.DeletedFiles, rec.Path)
				currentStreamCount--
				totalCount--
				log.Info().Str("file", rec.Path).Str("stream", streamName).Msg("[recording] deleted file")
			}
		}
	}

	if protectedCount > 0 {
		log.Info().
			Str("stream", streamName).
			Int("protected", protectedCount).
			Int("deleted", result.FilesDeleted).
			Int("archived", result.FilesArchived).
			Msg("[cleanup] some files were protected from deletion")
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

	log.Debug().
		Int64("total_size_mb", totalSize/1024/1024).
		Int64("max_size_mb", cfg.MaxTotalSize).
		Int64("excess_mb", (totalSize-maxBytes)/1024/1024).
		Msg("[recording] checking global size limit")

	if totalSize <= maxBytes {
		log.Debug().Msg("[recording] total size within global limit")
		return result, nil // Under limit
	}
	
	log.Info().
		Int64("current_size_mb", totalSize/1024/1024).
		Int64("limit_mb", cfg.MaxTotalSize).
		Int64("excess_mb", (totalSize-maxBytes)/1024/1024).
		Msg("[recording] enforcing global size limit")
		
	result.Policies = append(result.Policies, "global_size_limit")

	// Sort by recording time (oldest first)
	sort.Slice(recordings, func(i, j int) bool {
		return recordings[i].RecordingTime.Before(recordings[j].RecordingTime)
	})

	// Get stream counts for protection
	streamCounts, totalCount := getStreamRecordingCounts(recordings)

	// Delete oldest files until under limit, with protection
	protectedCount := 0
	for _, rec := range recordings {
		if totalSize <= maxBytes {
			break
		}

		// Check protection
		streamCount := streamCounts[rec.Stream]
		protected, reason := shouldProtectFromCleanup(rec, streamCount, totalCount)
		if protected {
			log.Info().
				Str("file", rec.Path).
				Str("stream", rec.Stream).
				Str("reason", reason).
				Msg("[cleanup] file protected from global size limit deletion")
			protectedCount++
			continue
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
		streamCounts[rec.Stream]--
		totalCount--
		log.Info().
			Str("file", rec.Path).
			Int64("size_mb", rec.Size/1024/1024).
			Int64("total_mb", totalSize/1024/1024).
			Msg("[recording] removed file for size limit")
	}

	if protectedCount > 0 {
		log.Warn().
			Int("protected_files", protectedCount).
			Int64("remaining_size_mb", totalSize/1024/1024).
			Int64("limit_mb", cfg.MaxTotalSize).
			Msg("[cleanup] some files protected from size limit cleanup - storage may exceed limit")
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

// performHealthCheck verifies the recording system is healthy before cleanup
func performHealthCheck() HealthCheckResult {
	result := HealthCheckResult{
		Healthy:           true,
		Warnings:          []string{},
		StreamsWithIssues: []string{},
	}

	// Get list of streams that should be recording
	streamsToRecord := getStreamsToRecordForHealthCheck()
	result.ExpectedRecordings = len(streamsToRecord)

	// Check 1: Verify FFmpeg processes are running for expected streams
	result.ActiveFFmpegProcesses = countActiveFFmpegProcesses()

	if result.ExpectedRecordings > 0 && result.ActiveFFmpegProcesses == 0 {
		result.Healthy = false
		result.Warnings = append(result.Warnings, "No FFmpeg processes running but recordings are configured")
	} else if result.ActiveFFmpegProcesses < result.ExpectedRecordings {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Only %d/%d expected FFmpeg processes running",
			result.ActiveFFmpegProcesses, result.ExpectedRecordings))
	}

	// Check 2: Verify new recordings are being created
	cfg := GlobalRecordingConfig
	recordings, err := findRecordingFiles(cfg.BasePath)
	if err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Failed to find recordings: %v", err))
	} else if len(recordings) > 0 {
		// Find the newest recording
		var newestTime time.Time
		for _, rec := range recordings {
			if rec.ModTime.After(newestTime) {
				newestTime = rec.ModTime
			}
		}

		result.NewestRecordingAge = time.Since(newestTime)

		// If newest recording is older than 2x segment duration, something may be wrong
		maxExpectedAge := cfg.SegmentDuration * 2
		if maxExpectedAge < 30*time.Minute {
			maxExpectedAge = 30 * time.Minute // At least 30 minutes
		}

		if result.NewestRecordingAge > maxExpectedAge {
			result.Healthy = false
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("Newest recording is %v old (threshold: %v) - recordings may have stopped",
					result.NewestRecordingAge.Round(time.Second), maxExpectedAge.Round(time.Second)))
		}
	} else if result.ExpectedRecordings > 0 {
		// No recordings found but some are expected
		result.Healthy = false
		result.Warnings = append(result.Warnings, "No recording files found but recordings are configured")
	}

	// Check 3: Check each configured stream individually
	for _, streamName := range streamsToRecord {
		streamHealthy := checkStreamHealth(streamName, recordings)
		if !streamHealthy {
			result.StreamsWithIssues = append(result.StreamsWithIssues, streamName)
		}
	}

	// If any streams have issues, mark as unhealthy
	if len(result.StreamsWithIssues) > 0 {
		result.Healthy = false
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("%d stream(s) have recording issues", len(result.StreamsWithIssues)))
	}

	return result
}

// getStreamsToRecordForHealthCheck returns streams that should be recording
func getStreamsToRecordForHealthCheck() []string {
	cfg := GlobalRecordingConfig
	var streamsToRecord []string

	// If global auto_start with no specific configs, we expect all streams to be recording
	if cfg.AutoStart && len(cfg.Streams) == 0 {
		// Check active recordings to see what's expected
		regularRecordings := GetRecordingManager().ListRecordings()
		for _, recording := range regularRecordings {
			if recording.Active {
				streamsToRecord = append(streamsToRecord, recording.Stream)
			}
		}
		segmentedRecordings := GetSegmentedRecordingManager().ListSegmentedRecordings()
		for _, recording := range segmentedRecordings {
			if recording.Active {
				streamsToRecord = append(streamsToRecord, recording.Stream)
			}
		}
	} else {
		// Check specifically configured streams
		for streamName, streamConfig := range cfg.Streams {
			if streamConfig.Enabled != nil && *streamConfig.Enabled {
				streamsToRecord = append(streamsToRecord, streamName)
			} else if streamConfig.Enabled == nil {
				// Configured but no explicit enabled field
				streamsToRecord = append(streamsToRecord, streamName)
			}
		}
	}

	// Remove duplicates
	streamMap := make(map[string]bool)
	uniqueStreams := []string{}
	for _, stream := range streamsToRecord {
		if !streamMap[stream] {
			streamMap[stream] = true
			uniqueStreams = append(uniqueStreams, stream)
		}
	}

	return uniqueStreams
}

// countActiveFFmpegProcesses counts running FFmpeg recording processes
func countActiveFFmpegProcesses() int {
	// Use pgrep to find FFmpeg processes that are recording (contain segment or output file)
	cmd := exec.Command("pgrep", "-f", "ffmpeg.*-f (segment|mp4|matroska|avi|mov)")
	output, err := cmd.Output()
	if err != nil {
		// pgrep returns exit code 1 if no processes found
		return 0
	}

	pids := strings.TrimSpace(string(output))
	if pids == "" {
		return 0
	}

	// Count PIDs
	return len(strings.Split(pids, "\n"))
}

// checkStreamHealth checks if a specific stream is recording properly
func checkStreamHealth(streamName string, allRecordings []CleanupRecordingInfo) bool {
	cfg := GlobalRecordingConfig

	// Find recordings for this stream
	var streamRecordings []CleanupRecordingInfo
	for _, rec := range allRecordings {
		if rec.Stream == streamName {
			streamRecordings = append(streamRecordings, rec)
		}
	}

	if len(streamRecordings) == 0 {
		// No recordings found for this stream
		log.Warn().
			Str("stream", streamName).
			Msg("[health-check] No recordings found for stream")
		return false
	}

	// Check if newest recording is recent enough
	var newestTime time.Time
	for _, rec := range streamRecordings {
		if rec.ModTime.After(newestTime) {
			newestTime = rec.ModTime
		}
	}

	age := time.Since(newestTime)
	maxExpectedAge := cfg.SegmentDuration * 2
	if maxExpectedAge < 30*time.Minute {
		maxExpectedAge = 30 * time.Minute
	}

	if age > maxExpectedAge {
		log.Warn().
			Str("stream", streamName).
			Dur("newest_recording_age", age).
			Dur("threshold", maxExpectedAge).
			Msg("[health-check] Stream recordings may have stopped")
		return false
	}

	// Check if FFmpeg process is running for this stream
	cmd := fmt.Sprintf("pgrep -f 'ffmpeg.*%s'", streamName)
	result, err := exec.Command("sh", "-c", cmd).Output()
	if err != nil || strings.TrimSpace(string(result)) == "" {
		log.Warn().
			Str("stream", streamName).
			Msg("[health-check] No FFmpeg process found for stream")
		return false
	}

	return true
}

// PerformHealthCheckNow performs an immediate health check and returns results
func PerformHealthCheckNow() HealthCheckResult {
	return performHealthCheck()
}

// attemptRecovery tries to recover failed recordings by killing stuck processes and restarting
func attemptRecovery(healthCheck HealthCheckResult) {
	log.Info().
		Int("streams_with_issues", len(healthCheck.StreamsWithIssues)).
		Msg("[recovery] starting automatic recovery")

	recoveredCount := 0
	failedCount := 0

	// Try to restart recordings for streams with issues
	for _, streamName := range healthCheck.StreamsWithIssues {
		log.Info().
			Str("stream", streamName).
			Msg("[recovery] attempting to restart recording for stream")

		// Get stream configuration
		streamConfig := GetStreamRecordingConfig(streamName)

		// Check if stream should be recording
		if streamConfig.Enabled == nil || !*streamConfig.Enabled {
			log.Debug().
				Str("stream", streamName).
				Msg("[recovery] stream not enabled, skipping recovery")
			continue
		}

		// Kill any stuck FFmpeg processes for this stream
		if err := killFFmpegProcessesForStream(streamName); err != nil {
			log.Warn().
				Err(err).
				Str("stream", streamName).
				Msg("[recovery] failed to kill stuck processes")
		}

		// Stop any existing recordings in our internal tracking
		stopExistingRecordings(streamName)

		// Wait for cleanup
		time.Sleep(2 * time.Second)

		// Restart the recording
		recordingID := fmt.Sprintf("recovery_%s_%d", streamName, time.Now().Unix())
		config := RecordConfig{
			Video:    streamConfig.Video,
			Audio:    streamConfig.Audio,
			Format:   streamConfig.Format,
			Duration: 0, // Continuous
		}

		config.Filename = GenerateRecordingPath(streamName, time.Now(), config.Format, 0)

		var err error
		if streamConfig.EnableSegments != nil && *streamConfig.EnableSegments {
			err = GetSegmentedRecordingManager().StartSegmentedRecording(recordingID, streamName, config)
		} else {
			err = GetRecordingManager().StartRecording(recordingID, streamName, config)
		}

		if err != nil {
			log.Error().
				Err(err).
				Str("stream", streamName).
				Msg("[recovery] failed to restart recording")
			failedCount++
		} else {
			log.Info().
				Str("stream", streamName).
				Str("recording_id", recordingID).
				Msg("[recovery] successfully restarted recording")
			recoveredCount++
		}
	}

	// If we have no active FFmpeg processes but expected some, do a full restart
	if healthCheck.ExpectedRecordings > 0 && healthCheck.ActiveFFmpegProcesses == 0 {
		log.Warn().
			Int("expected", healthCheck.ExpectedRecordings).
			Msg("[recovery] no FFmpeg processes running - attempting full recovery")

		// Kill all FFmpeg recording processes
		killAllFFmpegRecordingProcesses()

		// Stop all current recordings in internal tracking
		GetRecordingManager().StopAll()
		GetSegmentedRecordingManager().StopAll()

		// Wait for cleanup
		time.Sleep(3 * time.Second)

		// Restart all auto-recordings
		go func() {
			log.Info().Msg("[recovery] restarting all auto-recordings")
			checkAndStartAutoRecordings()
		}()
	}

	log.Info().
		Int("recovered", recoveredCount).
		Int("failed", failedCount).
		Int("total_issues", len(healthCheck.StreamsWithIssues)).
		Msg("[recovery] automatic recovery completed")
}

// killFFmpegProcessesForStream kills all FFmpeg processes recording a specific stream
func killFFmpegProcessesForStream(streamName string) error {
	// Find FFmpeg processes for this stream
	cmd := fmt.Sprintf("pgrep -f 'ffmpeg.*%s'", streamName)
	result, err := exec.Command("sh", "-c", cmd).Output()
	if err != nil {
		// pgrep returns exit code 1 if no processes found, which is fine
		return nil
	}

	pids := strings.TrimSpace(string(result))
	if pids == "" {
		return nil
	}

	// Kill each process
	for _, pidStr := range strings.Split(pids, "\n") {
		pidStr = strings.TrimSpace(pidStr)
		if pidStr == "" {
			continue
		}

		log.Info().
			Str("stream", streamName).
			Str("pid", pidStr).
			Msg("[recovery] killing stuck FFmpeg process")

		// Try graceful kill first (SIGTERM)
		killCmd := exec.Command("kill", pidStr)
		if err := killCmd.Run(); err != nil {
			log.Warn().
				Err(err).
				Str("pid", pidStr).
				Msg("[recovery] SIGTERM failed, trying SIGKILL")

			// Force kill if graceful fails (SIGKILL)
			killCmd = exec.Command("kill", "-9", pidStr)
			if err := killCmd.Run(); err != nil {
				log.Error().
					Err(err).
					Str("pid", pidStr).
					Msg("[recovery] failed to kill process")
			}
		}
	}

	return nil
}

// killAllFFmpegRecordingProcesses kills all FFmpeg recording processes
func killAllFFmpegRecordingProcesses() {
	log.Warn().Msg("[recovery] killing all FFmpeg recording processes")

	// Find all FFmpeg recording processes
	cmd := "pgrep -f 'ffmpeg.*-f (segment|mp4|matroska|avi|mov)'"
	result, err := exec.Command("sh", "-c", cmd).Output()
	if err != nil {
		return
	}

	pids := strings.TrimSpace(string(result))
	if pids == "" {
		return
	}

	// Kill each process
	for _, pidStr := range strings.Split(pids, "\n") {
		pidStr = strings.TrimSpace(pidStr)
		if pidStr == "" {
			continue
		}

		log.Info().
			Str("pid", pidStr).
			Msg("[recovery] killing FFmpeg recording process")

		// Try graceful kill first
		killCmd := exec.Command("kill", pidStr)
		if err := killCmd.Run(); err != nil {
			// Force kill if graceful fails
			exec.Command("kill", "-9", pidStr).Run()
		}
	}

	time.Sleep(1 * time.Second)
}

// stopExistingRecordings stops all existing recordings in internal tracking for a stream
func stopExistingRecordings(streamName string) {
	// Stop regular recordings
	regularRecordings := GetRecordingManager().ListRecordings()
	for id, recording := range regularRecordings {
		if recording.Stream == streamName && recording.Active {
			log.Info().
				Str("stream", streamName).
				Str("recording_id", id).
				Msg("[recovery] stopping existing recording")
			GetRecordingManager().StopRecording(id)
		}
	}

	// Stop segmented recordings
	segmentedRecordings := GetSegmentedRecordingManager().ListSegmentedRecordings()
	for id, recording := range segmentedRecordings {
		if recording.Stream == streamName && recording.Active {
			log.Info().
				Str("stream", streamName).
				Str("recording_id", id).
				Msg("[recovery] stopping existing segmented recording")
			GetSegmentedRecordingManager().StopSegmentedRecording(id)
		}
	}
}