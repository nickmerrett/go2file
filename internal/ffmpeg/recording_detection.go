package ffmpeg

import "github.com/AlexxIT/go2rtc/internal/detection"

// onSegmentComplete is called when a recording segment file is finalised.
// It queues the file for post-recording object detection analysis.
func onSegmentComplete(streamName, filePath string) {
	detection.QueueFile(streamName, filePath)
}

// InitDetection wires the detection package's callbacks so it can read
// per-stream config and the recording base path without circular imports.
func InitDetection() {
	detection.SetSidecarBasePath(func() string {
		return GlobalRecordingConfig.BasePath
	})

	detection.SetStreamConfigReader(func(streamName string) detection.StreamDetectionOverride {
		sc := GetStreamRecordingConfig(streamName)
		return detection.StreamDetectionOverride{
			Detection:         sc.Detection,
			DetectionInterval: sc.DetectionInterval,
			DetectionLabels:   sc.DetectionLabels,
		}
	})
}
