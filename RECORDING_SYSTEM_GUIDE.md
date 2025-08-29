# go2rtc Recording System Guide

A comprehensive guide to the enhanced recording system with FFmpeg integration, direct RTSP source recording, intelligent cleanup, and per-stream configuration.

## Table of Contents
- [Quick Start](#quick-start)
- [Configuration](#configuration)
- [Direct RTSP Source Recording](#direct-rtsp-source-recording)
- [Per-Stream Configuration](#per-stream-configuration)
- [Cleanup System](#cleanup-system)
- [API Endpoints](#api-endpoints)
- [Troubleshooting](#troubleshooting)

## Quick Start

### Basic Recording Configuration
```yaml
recording:
  auto_start: true
  base_path: "recordings"
  default_format: "mp4"
  default_video: "copy"  # No transcoding - direct codec copy
  default_audio: "copy"  # No transcoding - direct codec copy
  enable_segments: true
  segment_duration: 10m
  retention_days: 7
  
  streams:
    camera1:
      enabled: true
    camera2:
      enabled: false
```

## Configuration

### Global Settings
- `auto_start`: Enable automatic recording when streams become available
- `base_path`: Directory where recordings are stored
- `default_format`: Output format (mp4, mkv, avi)
- `default_video`/`default_audio`: Codec settings ("copy" for no transcoding)
- `enable_segments`: Automatic file segmentation
- `segment_duration`: Duration before starting new file
- `retention_days`: How long to keep recordings
- `enable_cleanup`: Automatic cleanup of old files

### Path and Filename Templates
```yaml
recording:
  path_template: "{year}/{month}/{day}/{stream}"
  filename_template: "{stream}_{timestamp}"
```

**Available placeholders:**
- `{stream}`: Stream name
- `{year}`, `{month}`, `{day}`, `{hour}`: Date/time components
- `{timestamp}`: Full timestamp (2006-01-02_15-04-05)
- `{date}`: Date only (2006-01-02)
- `{time}`: Time only (15-04-05)

## Direct RTSP Source Recording

Record directly from camera RTSP streams, bypassing go2rtc's internal processing for lower CPU usage.

### Global Template (Recommended)
```yaml
recording:
  direct_source: "rtsp://admin:password@camera-{stream}.local/stream1"
  streams:
    kitchen: 
      enabled: true
    garage:
      enabled: true
```

### Per-Stream Sources
```yaml
recording:
  streams:
    camera1:
      enabled: true
      source: "rtsp://admin:password@192.168.1.100/stream1"
    camera2:
      enabled: true
      source: "rtsp://admin:password@192.168.1.101/stream1"
```

## Per-Stream Configuration

### Stream-Specific Overrides
```yaml
recording:
  # Global defaults
  default_format: "mp4"
  default_video: "copy"
  segment_duration: 10m
  
  streams:
    highres_camera:
      enabled: true
      format: "mkv"              # Override format
      segment_duration: 5m       # Shorter segments
      retention_days: 14         # Keep longer
      source: "rtsp://..."       # Direct source
      
    motion_camera:
      enabled: true
      record_on_motion: true     # Future feature
      retention_hours: 48        # 2 days only
      
    audio_only:
      enabled: true
      video: "none"              # Audio only
      audio: "aac"               # Transcode audio
```

### Quality Settings
```yaml
recording:
  streams:
    mobile_stream:
      enabled: true
      width: 1280
      height: 720
      framerate: 15
      bitrate_limit: "2M"
```

## Cleanup System

The cleanup system uses filename-based timestamps for reliable cleanup across restarts.

### Automatic Cleanup
```yaml
recording:
  enable_cleanup: true
  cleanup_interval: 1h
  retention_days: 7
  max_recordings: 100
  max_total_size: 10240  # MB
```

### Manual Cleanup Commands

#### Force Cleanup Old Recordings
```bash
# Dry run - see what would be deleted
curl "http://localhost:1984/api/record/force-cleanup?age_hours=48&dry_run=true"

# Actually delete files older than 48 hours
curl "http://localhost:1984/api/record/force-cleanup?age_hours=48"

# Delete files older than 7 days
curl "http://localhost:1984/api/record/force-cleanup?age_days=7"
```

### Cleanup Behavior
- Uses filename timestamps, not file modification times
- Survives go2rtc restarts
- Supports multiple timestamp formats
- Provides detailed cleanup statistics

## API Endpoints

### Recording Management
```bash
# List active recordings
curl "http://localhost:1984/api/record"

# Start recording
curl -X POST "http://localhost:1984/api/record?src=camera1"

# Stop recording  
curl -X DELETE "http://localhost:1984/api/record?id=recording_id"
```

### Segmented Recording
```bash
# Start segmented recording
curl -X POST "http://localhost:1984/api/record/segmented?src=camera1&duration=600"

# List segmented recordings
curl "http://localhost:1984/api/record/segmented"
```

### Cleanup Operations
```bash
# Get cleanup info
curl "http://localhost:1984/api/record/cleanup-info"

# Force cleanup with dry run
curl "http://localhost:1984/api/record/force-cleanup?age_hours=24&dry_run=true"

# Force cleanup (delete files)
curl "http://localhost:1984/api/record/force-cleanup?age_hours=24"
```

## Recording Logic

### Stream Recording Behavior
The system follows these rules for determining which streams to record:

1. **Explicit Configuration Mode** (Recommended):
   ```yaml
   recording:
     streams:
       camera1: { enabled: true }   # Will record
       camera2: { enabled: false }  # Won't record
       camera3: {}                  # Will record (configured = enabled)
   ```

2. **Global Auto-Start Mode**:
   ```yaml
   recording:
     auto_start: true
     # No streams section = record ALL available streams
   ```

3. **No Recording**:
   ```yaml
   recording:
     auto_start: false
     # No streams section = no recording
   ```

### Direct Source vs Internal Routing
- **Direct Source**: Records directly from camera RTSP URL (lower CPU)
- **Internal Routing**: Records from go2rtc's internal RTSP server (allows stream processing)

The system automatically chooses:
1. Per-stream `source` field (if configured)
2. Global `direct_source` template (if configured) 
3. Internal RTSP routing (fallback)

## Troubleshooting

### Debug Logging
```yaml
log:
  level: debug
```

### Common Issues

#### Stream Not Recording
1. Check if stream is in recording configuration:
   ```bash
   curl "http://localhost:1984/api/record" | grep "your_stream"
   ```

2. Verify stream is available:
   ```bash
   curl "http://localhost:1984/api/streams"
   ```

3. Check logs for errors:
   ```
   [recording] failed to start recording
   ```

#### Direct Source Not Working
1. Verify RTSP URL is accessible
2. Check debug logs for source resolution:
   ```
   [config] using per-stream direct source
   [recording] using direct RTSP source
   ```

#### Duplicate Recordings
This was a bug that has been fixed. If you still see duplicates:
1. Restart go2rtc
2. Check for multiple FFmpeg processes: `pgrep -f ffmpeg`

#### Cleanup Not Working
1. Check cleanup is enabled: `enable_cleanup: true`
2. Verify retention settings are reasonable
3. Use manual cleanup to test: `/api/record/force-cleanup`

### Configuration Validation
The system validates configuration on startup:
- Creates directories if missing
- Sets minimum values for intervals
- Warns about conflicting settings

## Performance Tips

### CPU Optimization
```yaml
recording:
  default_video: "copy"  # No transcoding
  default_audio: "copy"  # No transcoding
```

### Storage Optimization
```yaml
recording:
  enable_segments: true
  segment_duration: 10m  # Balance file size vs management overhead
  max_file_size: 1024    # 1GB max per file
```

### Network Optimization
```yaml
recording:
  # Use direct sources to avoid double streaming
  direct_source: "rtsp://camera-{stream}.local/stream1"
```

## Migration from Previous Versions

If upgrading from earlier recording implementations:

1. **Update Configuration**: Use new `recording:` section format
2. **Check Stream Names**: Ensure recording config matches stream names
3. **Review Cleanup**: New filename-based cleanup may behave differently
4. **Test Direct Sources**: Verify RTSP URLs are correct

## Security Considerations

- Store RTSP credentials securely
- Restrict access to recording directories
- Use network segmentation for camera access
- Regular cleanup to manage storage usage

---

*This recording system provides professional-grade surveillance capabilities with minimal CPU overhead through codec copying and intelligent direct source recording.*