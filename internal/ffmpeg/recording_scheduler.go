package ffmpeg

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ScheduleManager handles scheduled recordings
type ScheduleManager struct {
	schedules map[string]*StreamSchedule
	running   bool
}

// StreamSchedule represents a recording schedule for a stream
type StreamSchedule struct {
	StreamName   string
	Schedule     string
	Duration     time.Duration
	Config       RecordConfig
	NextRun      time.Time
	ActiveID     string // ID of currently active scheduled recording
	parsedSchedule *ParsedSchedule
}

// ParsedSchedule represents parsed cron-like schedule
type ParsedSchedule struct {
	Minutes    []int  // 0-59, -1 for *
	Hours      []int  // 0-23, -1 for *
	Days       []int  // 1-31, -1 for *
	Months     []int  // 1-12, -1 for *
	Weekdays   []int  // 0-6 (Sunday=0), -1 for *
	Raw        string
}

var scheduleManager = &ScheduleManager{
	schedules: make(map[string]*StreamSchedule),
}

// StartScheduler begins the recording scheduler
func StartScheduler() {
	if scheduleManager.running {
		return
	}

	scheduleManager.running = true
	go scheduleRoutine()
	log.Info().Msg("[scheduler] recording scheduler started")
}

// StopScheduler stops the recording scheduler
func StopScheduler() {
	if !scheduleManager.running {
		return
	}

	scheduleManager.running = false
	
	// Stop all active scheduled recordings
	for _, schedule := range scheduleManager.schedules {
		if schedule.ActiveID != "" {
			GetRecordingManager().StopRecording(schedule.ActiveID)
			schedule.ActiveID = ""
		}
	}
	
	log.Info().Msg("[scheduler] recording scheduler stopped")
}

// AddSchedule adds a recording schedule for a stream
func AddSchedule(streamName, scheduleStr string, duration time.Duration) error {
	parsedSchedule, err := parseSchedule(scheduleStr)
	if err != nil {
		return fmt.Errorf("invalid schedule format: %v", err)
	}

	streamConfig := GetStreamRecordingConfig(streamName)
	config := RecordConfig{
		Video:    streamConfig.Video,
		Audio:    streamConfig.Audio,
		Format:   streamConfig.Format,
		Duration: duration,
	}

	schedule := &StreamSchedule{
		StreamName:     streamName,
		Schedule:       scheduleStr,
		Duration:       duration,
		Config:         config,
		parsedSchedule: parsedSchedule,
	}

	schedule.NextRun = calculateNextRun(parsedSchedule, time.Now())
	scheduleManager.schedules[streamName] = schedule

	log.Info().
		Str("stream", streamName).
		Str("schedule", scheduleStr).
		Dur("duration", duration).
		Time("next_run", schedule.NextRun).
		Msg("[scheduler] schedule added")

	return nil
}

// RemoveSchedule removes a recording schedule
func RemoveSchedule(streamName string) {
	if schedule, exists := scheduleManager.schedules[streamName]; exists {
		// Stop active recording if any
		if schedule.ActiveID != "" {
			GetRecordingManager().StopRecording(schedule.ActiveID)
		}
		delete(scheduleManager.schedules, streamName)
		log.Info().Str("stream", streamName).Msg("[scheduler] schedule removed")
	}
}

// LoadSchedulesFromConfig loads schedules from stream configurations
func LoadSchedulesFromConfig() {
	cfg := GlobalRecordingConfig
	
	for streamName, streamConfig := range cfg.Streams {
		if streamConfig.Schedule != "" {
			// Default duration if not specified in config
			duration := time.Hour
			if streamConfig.SegmentDuration > 0 {
				duration = streamConfig.SegmentDuration
			}
			
			if err := AddSchedule(streamName, streamConfig.Schedule, duration); err != nil {
				log.Error().
					Err(err).
					Str("stream", streamName).
					Str("schedule", streamConfig.Schedule).
					Msg("[scheduler] failed to add schedule from config")
			}
		}
	}
}

// scheduleRoutine is the main scheduler loop
func scheduleRoutine() {
	ticker := time.NewTicker(time.Minute) // Check every minute
	defer ticker.Stop()

	for scheduleManager.running {
		select {
		case now := <-ticker.C:
			checkAndExecuteSchedules(now)
		}
	}
}

// checkAndExecuteSchedules checks if any schedules should be executed
func checkAndExecuteSchedules(now time.Time) {
	for streamName, schedule := range scheduleManager.schedules {
		// Check if it's time to start a recording
		if now.After(schedule.NextRun) || now.Equal(schedule.NextRun) {
			if schedule.ActiveID == "" { // Only start if not already recording
				if err := startScheduledRecording(schedule); err != nil {
					log.Error().
						Err(err).
						Str("stream", streamName).
						Msg("[scheduler] failed to start scheduled recording")
				} else {
					log.Info().
						Str("stream", streamName).
						Str("schedule", schedule.Schedule).
						Dur("duration", schedule.Duration).
						Msg("[scheduler] started scheduled recording")
				}
			}
			
			// Calculate next run time
			schedule.NextRun = calculateNextRun(schedule.parsedSchedule, now.Add(time.Minute))
		}
		
		// Check if scheduled recording should stop
		if schedule.ActiveID != "" {
			recording := GetRecordingManager().GetRecording(schedule.ActiveID)
			if recording == nil || !recording.Active {
				schedule.ActiveID = ""
			}
		}
	}
}

// startScheduledRecording starts a scheduled recording
func startScheduledRecording(schedule *StreamSchedule) error {
	recordingID := fmt.Sprintf("sched_%s_%d", schedule.StreamName, time.Now().Unix())
	
	// Generate filename
	schedule.Config.Filename = GenerateRecordingPath(
		schedule.StreamName, 
		time.Now(), 
		schedule.Config.Format, 
		0,
	)
	
	if err := GetRecordingManager().StartRecording(recordingID, schedule.StreamName, schedule.Config); err != nil {
		return err
	}
	
	schedule.ActiveID = recordingID
	return nil
}

// parseSchedule parses cron-like schedule string
// Format: "minute hour day month weekday"
// Examples:
//   "0 9 * * 1-5"     = 9:00 AM, Monday through Friday
//   "0 22 * * *"      = 10:00 PM every day
//   "*/15 9-17 * * *" = Every 15 minutes from 9 AM to 5 PM
//   "0 8,20 * * *"    = 8:00 AM and 8:00 PM every day
func parseSchedule(scheduleStr string) (*ParsedSchedule, error) {
	parts := strings.Fields(scheduleStr)
	if len(parts) != 5 {
		return nil, fmt.Errorf("schedule must have 5 fields: minute hour day month weekday")
	}

	parsed := &ParsedSchedule{Raw: scheduleStr}
	
	var err error
	parsed.Minutes, err = parseField(parts[0], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("invalid minute field: %v", err)
	}
	
	parsed.Hours, err = parseField(parts[1], 0, 23)
	if err != nil {
		return nil, fmt.Errorf("invalid hour field: %v", err)
	}
	
	parsed.Days, err = parseField(parts[2], 1, 31)
	if err != nil {
		return nil, fmt.Errorf("invalid day field: %v", err)
	}
	
	parsed.Months, err = parseField(parts[3], 1, 12)
	if err != nil {
		return nil, fmt.Errorf("invalid month field: %v", err)
	}
	
	parsed.Weekdays, err = parseField(parts[4], 0, 6)
	if err != nil {
		return nil, fmt.Errorf("invalid weekday field: %v", err)
	}

	return parsed, nil
}

// parseField parses a single cron field
func parseField(field string, min, max int) ([]int, error) {
	if field == "*" {
		return []int{-1}, nil // -1 indicates wildcard
	}
	
	var result []int
	
	// Handle ranges and lists
	parts := strings.Split(field, ",")
	for _, part := range parts {
		if strings.Contains(part, "/") {
			// Handle step values like */15 or 9-17/2
			stepParts := strings.Split(part, "/")
			if len(stepParts) != 2 {
				return nil, fmt.Errorf("invalid step format: %s", part)
			}
			
			step, err := strconv.Atoi(stepParts[1])
			if err != nil {
				return nil, fmt.Errorf("invalid step value: %s", stepParts[1])
			}
			
			var start, end int
			if stepParts[0] == "*" {
				start, end = min, max
			} else if strings.Contains(stepParts[0], "-") {
				rangeParts := strings.Split(stepParts[0], "-")
				if len(rangeParts) != 2 {
					return nil, fmt.Errorf("invalid range format: %s", stepParts[0])
				}
				start, err = strconv.Atoi(rangeParts[0])
				if err != nil {
					return nil, fmt.Errorf("invalid range start: %s", rangeParts[0])
				}
				end, err = strconv.Atoi(rangeParts[1])
				if err != nil {
					return nil, fmt.Errorf("invalid range end: %s", rangeParts[1])
				}
			} else {
				start, err = strconv.Atoi(stepParts[0])
				if err != nil {
					return nil, fmt.Errorf("invalid step start: %s", stepParts[0])
				}
				end = start
			}
			
			for i := start; i <= end; i += step {
				if i >= min && i <= max {
					result = append(result, i)
				}
			}
		} else if strings.Contains(part, "-") {
			// Handle ranges like 9-17
			rangeParts := strings.Split(part, "-")
			if len(rangeParts) != 2 {
				return nil, fmt.Errorf("invalid range format: %s", part)
			}
			
			start, err := strconv.Atoi(rangeParts[0])
			if err != nil {
				return nil, fmt.Errorf("invalid range start: %s", rangeParts[0])
			}
			
			end, err := strconv.Atoi(rangeParts[1])
			if err != nil {
				return nil, fmt.Errorf("invalid range end: %s", rangeParts[1])
			}
			
			for i := start; i <= end; i++ {
				if i >= min && i <= max {
					result = append(result, i)
				}
			}
		} else {
			// Handle single values
			value, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("invalid value: %s", part)
			}
			
			if value >= min && value <= max {
				result = append(result, value)
			} else {
				return nil, fmt.Errorf("value %d out of range [%d, %d]", value, min, max)
			}
		}
	}
	
	return result, nil
}

// calculateNextRun calculates the next time a schedule should run
func calculateNextRun(schedule *ParsedSchedule, from time.Time) time.Time {
	// Start from the next minute to avoid immediate re-triggering
	current := time.Date(from.Year(), from.Month(), from.Day(), from.Hour(), from.Minute()+1, 0, 0, from.Location())
	
	// Look ahead up to 2 years to find next match
	for i := 0; i < 366*24*60*2; i++ {
		if matchesSchedule(schedule, current) {
			return current
		}
		current = current.Add(time.Minute)
	}
	
	// Fallback - should never happen with valid schedules
	return from.Add(24 * time.Hour)
}

// matchesSchedule checks if a time matches a schedule
func matchesSchedule(schedule *ParsedSchedule, t time.Time) bool {
	return matchesField(schedule.Minutes, t.Minute()) &&
		matchesField(schedule.Hours, t.Hour()) &&
		matchesField(schedule.Days, t.Day()) &&
		matchesField(schedule.Months, int(t.Month())) &&
		matchesField(schedule.Weekdays, int(t.Weekday()))
}

// matchesField checks if a time field matches a schedule field
func matchesField(scheduleField []int, timeValue int) bool {
	if len(scheduleField) == 1 && scheduleField[0] == -1 {
		return true // wildcard
	}
	
	for _, value := range scheduleField {
		if value == timeValue {
			return true
		}
	}
	
	return false
}

// GetSchedules returns all active schedules
func GetSchedules() map[string]*StreamSchedule {
	result := make(map[string]*StreamSchedule)
	for k, v := range scheduleManager.schedules {
		result[k] = v
	}
	return result
}