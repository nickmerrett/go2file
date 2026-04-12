package detection

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// AnalysisJob is a queued recording file waiting to be analysed.
type AnalysisJob struct {
	StreamName string
	FilePath   string
	QueuedAt   time.Time
}

// DetectionResult is the .json sidecar written next to the recording file.
type DetectionResult struct {
	File          string       `json:"file"`
	AnalysedAt    time.Time    `json:"analysed_at"`
	DurationSecs  float64      `json:"duration_secs"`
	FrameInterval int          `json:"frame_interval"`
	FramesChecked int          `json:"frames_checked"`
	Labels        []string     `json:"labels"`   // unique labels found
	Detections    []Detection  `json:"detections"`
}

type Detection struct {
	TimeSecs   float64 `json:"time_secs"`
	Label      string  `json:"label"`
	Confidence float64 `json:"confidence"`
	XMin       int     `json:"x_min,omitempty"`
	YMin       int     `json:"y_min,omitempty"`
	XMax       int     `json:"x_max,omitempty"`
	YMax       int     `json:"y_max,omitempty"`
}

// cpaiResponse is CodeProject.AI / DeepStack detection response.
type cpaiResponse struct {
	Success     bool `json:"success"`
	Predictions []struct {
		Label      string  `json:"label"`
		Confidence float64 `json:"confidence"`
		XMin       int     `json:"x_min"`
		YMin       int     `json:"y_min"`
		XMax       int     `json:"x_max"`
		YMax       int     `json:"y_max"`
	} `json:"predictions"`
	// DeepStack uses "predictions", CodeProject.AI uses the same shape
}

// Analyzer manages the analysis queue and worker goroutines.
type Analyzer struct {
	cfg    *DetectionConfig
	queue  chan AnalysisJob
	status map[string]*StreamStatus
	mu     sync.RWMutex
	client *http.Client
}

type StreamStatus struct {
	LastQueued    time.Time `json:"last_queued"`
	LastAnalysed  time.Time `json:"last_analysed"`
	LastFile      string    `json:"last_file"`
	QueueDepth    int       `json:"queue_depth"`
	TotalAnalysed int       `json:"total_analysed"`
	LastLabels    []string  `json:"last_labels"`
	LastError     string    `json:"last_error,omitempty"`
}

func NewAnalyzer(cfg *DetectionConfig) *Analyzer {
	return &Analyzer{
		cfg:    cfg,
		queue:  make(chan AnalysisJob, 100),
		status: make(map[string]*StreamStatus),
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *Analyzer) Start() {
	// Single worker — sequential analysis avoids hammering the backend
	go a.worker()
}

func (a *Analyzer) Enqueue(streamName, filePath string) {
	frameInterval, _, _, enabled := GetEffectiveConfig(streamName)
	if !enabled {
		return
	}

	// Skip if sidecar already exists
	if _, err := os.Stat(sidecarPath(filePath)); err == nil {
		log.Debug().Str("file", filePath).Msg("[detection] sidecar exists, skipping")
		return
	}

	job := AnalysisJob{
		StreamName: streamName,
		FilePath:   filePath,
		QueuedAt:   time.Now(),
	}

	select {
	case a.queue <- job:
		a.mu.Lock()
		if _, ok := a.status[streamName]; !ok {
			a.status[streamName] = &StreamStatus{}
		}
		a.status[streamName].LastQueued = time.Now()
		a.status[streamName].QueueDepth++
		a.mu.Unlock()
		log.Info().
			Str("stream", streamName).
			Str("file", filepath.Base(filePath)).
			Int("frame_interval", frameInterval).
			Msg("[detection] queued for analysis")
	default:
		log.Warn().
			Str("stream", streamName).
			Str("file", filePath).
			Msg("[detection] queue full, skipping file")
	}
}

func (a *Analyzer) worker() {
	for job := range a.queue {
		a.mu.Lock()
		if s, ok := a.status[job.StreamName]; ok {
			s.QueueDepth--
		}
		a.mu.Unlock()

		result, err := a.analyzeFile(job)

		a.mu.Lock()
		if _, ok := a.status[job.StreamName]; !ok {
			a.status[job.StreamName] = &StreamStatus{}
		}
		s := a.status[job.StreamName]
		s.LastAnalysed = time.Now()
		s.LastFile = filepath.Base(job.FilePath)
		s.TotalAnalysed++
		if err != nil {
			s.LastError = err.Error()
			log.Error().Err(err).
				Str("stream", job.StreamName).
				Str("file", job.FilePath).
				Msg("[detection] analysis failed")
		} else {
			s.LastLabels = result.Labels
			s.LastError = ""
		}
		a.mu.Unlock()
	}
}

func (a *Analyzer) analyzeFile(job AnalysisJob) (*DetectionResult, error) {
	frameInterval, minConfidence, labelFilter, _ := GetEffectiveConfig(job.StreamName)

	log.Info().
		Str("stream", job.StreamName).
		Str("file", filepath.Base(job.FilePath)).
		Msg("[detection] starting analysis")

	// Get video duration
	duration, err := getVideoDuration(job.FilePath)
	if err != nil {
		return nil, fmt.Errorf("get duration: %w", err)
	}

	// Build temp dir for frames
	tmpDir, err := os.MkdirTemp("", "detection_*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Extract frames at interval using FFmpeg
	framePattern := filepath.Join(tmpDir, "frame_%06d.jpg")
	vfArg := fmt.Sprintf("fps=1/%d", frameInterval)
	cmd := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-i", job.FilePath,
		"-vf", vfArg,
		"-q:v", "3",
		framePattern,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg frame extract: %w: %s", err, string(out))
	}

	// Collect frame files
	frames, err := filepath.Glob(filepath.Join(tmpDir, "frame_*.jpg"))
	if err != nil || len(frames) == 0 {
		return nil, fmt.Errorf("no frames extracted from %s", job.FilePath)
	}

	log.Debug().
		Str("file", filepath.Base(job.FilePath)).
		Int("frames", len(frames)).
		Msg("[detection] frames extracted")

	result := &DetectionResult{
		File:          filepath.Base(job.FilePath),
		AnalysedAt:    time.Now(),
		DurationSecs:  duration,
		FrameInterval: frameInterval,
		FramesChecked: len(frames),
		Detections:    []Detection{},
	}

	labelSet := make(map[string]bool)

	for i, framePath := range frames {
		timeSecs := float64(i * frameInterval)
		detections, err := a.detectFrame(framePath, minConfidence, labelFilter)
		if err != nil {
			log.Warn().Err(err).Str("frame", framePath).Msg("[detection] frame detection failed, skipping")
			continue
		}
		for _, d := range detections {
			labelSet[d.Label] = true
			d.TimeSecs = timeSecs
			result.Detections = append(result.Detections, d)
		}
	}

	// Build unique label list
	for label := range labelSet {
		result.Labels = append(result.Labels, label)
	}

	// Write sidecar JSON
	if err := writeSidecar(job.FilePath, result); err != nil {
		return nil, fmt.Errorf("write sidecar: %w", err)
	}

	log.Info().
		Str("stream", job.StreamName).
		Str("file", filepath.Base(job.FilePath)).
		Strs("labels", result.Labels).
		Int("detections", len(result.Detections)).
		Msg("[detection] analysis complete")

	return result, nil
}

func (a *Analyzer) detectFrame(framePath string, minConfidence float64, labelFilter []string) ([]Detection, error) {
	f, err := os.Open(framePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Build multipart form
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fw, err := w.CreateFormFile("image", filepath.Base(framePath))
	if err != nil {
		return nil, err
	}
	if _, err = io.Copy(fw, f); err != nil {
		return nil, err
	}
	// CodeProject.AI accepts min_confidence as a form field
	_ = w.WriteField("min_confidence", fmt.Sprintf("%.2f", minConfidence))
	w.Close()

	url := strings.TrimRight(a.cfg.BackendURL, "/") + "/v1/vision/detection"
	resp, err := a.client.Post(url, w.FormDataContentType(), &body)
	if err != nil {
		return nil, fmt.Errorf("backend request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("backend returned %d", resp.StatusCode)
	}

	var result cpaiResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if !result.Success {
		return nil, fmt.Errorf("backend returned success=false")
	}

	var detections []Detection
	for _, p := range result.Predictions {
		if p.Confidence < minConfidence {
			continue
		}
		label := strings.ToLower(p.Label)
		if len(labelFilter) > 0 && !containsLabel(labelFilter, label) {
			continue
		}
		detections = append(detections, Detection{
			Label:      label,
			Confidence: p.Confidence,
			XMin:       p.XMin,
			YMin:       p.YMin,
			XMax:       p.XMax,
			YMax:       p.YMax,
		})
	}
	return detections, nil
}

func (a *Analyzer) GetStatus() map[string]*StreamStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()
	// Return copy
	out := make(map[string]*StreamStatus, len(a.status))
	for k, v := range a.status {
		cp := *v
		out[k] = &cp
	}
	return out
}

func (a *Analyzer) QueueDepth() int {
	return len(a.queue)
}

// PruneOldSidecars removes .json sidecar files whose recording no longer exists
// or is older than retentionDays. Returns count pruned.
func (a *Analyzer) PruneOldSidecars(retentionDays int) int {
	basePath := ""
	// Import would be circular; read base path from config via package-level func
	basePath = getSidecarBasePath()
	if basePath == "" {
		return 0
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	pruned := 0
	_ = filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".json" {
			return nil
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(path); err == nil {
				pruned++
			}
		}
		return nil
	})
	return pruned
}

// sidecarPath returns the .json sidecar path for a recording file.
func sidecarPath(filePath string) string {
	ext := filepath.Ext(filePath)
	return strings.TrimSuffix(filePath, ext) + ".json"
}

func writeSidecar(filePath string, result *DetectionResult) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(sidecarPath(filePath), data, 0644)
}

func getVideoDuration(filePath string) (float64, error) {
	cmd := exec.Command("ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		filePath,
	)
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	var dur float64
	_, err = fmt.Sscanf(strings.TrimSpace(string(out)), "%f", &dur)
	return dur, err
}

func containsLabel(filter []string, label string) bool {
	for _, f := range filter {
		if strings.EqualFold(f, label) {
			return true
		}
	}
	return false
}

// getSidecarBasePath returns the recording base path for sidecar pruning.
// Avoids importing the ffmpeg package (circular import).
var getSidecarBasePath = func() string { return "" }

// SetSidecarBasePath allows the ffmpeg package to inject the recording base path
// without creating a circular import.
func SetSidecarBasePath(fn func() string) {
	getSidecarBasePath = fn
}
