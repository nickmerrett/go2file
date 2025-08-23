# go2rtc Recording Feature Documentation

## Overview

The go2rtc recording feature provides comprehensive stream recording capabilities with FFmpeg integration. It supports direct codec copying (no transcoding), automatic segmentation, retention policies, and cleanup management.

## Key Features

✅ **Direct Codec Copying** - Record streams without transcoding for minimal CPU usage  
✅ **Automatic Segmentation** - Split recordings into time-based or size-based segments  
✅ **Retention Policies** - Automatic cleanup based on age, count, or total size  
✅ **Flexible Storage** - Configurable directory structures and naming templates  
✅ **Multiple Formats** - Support for MP4, MKV, AVI, MOV, and other FFmpeg formats  
✅ **RESTful API** - Complete API for starting, stopping, and managing recordings  
✅ **Concurrent Recordings** - Multiple simultaneous recordings per stream  
✅ **Error Recovery** - Automatic restart on FFmpeg process failures  

---

## Configuration

Add recording configuration to your `go2rtc.yaml` file:

```yaml
recording:
  # Storage Configuration
  base_path: "recordings"                    # Base directory for recordings
  path_template: "{year}/{month}/{day}/{stream}"  # Directory structure
  filename_template: "{stream}_{timestamp}" # Filename pattern
  default_format: "mp4"                     # Default output format
  create_directories: true                  # Auto-create directories
  
  # Segmentation Settings  
  enable_segments: false                    # Enable automatic segmentation
  segment_duration: "1h"                   # New file every hour
  max_file_size: 1024                      # Max 1GB per file
  
  # Retention Policy
  retention_days: 7                        # Keep recordings for 7 days
  max_recordings: 100                      # Max 100 recordings per stream
  max_total_size: 10240                    # Max 10GB total storage
  
  # Cleanup Settings
  enable_cleanup: true                     # Enable automatic cleanup
  cleanup_interval: "1h"                  # Check every hour
  move_to_archive: false                   # Delete instead of archive
  
  # Recording Behavior
  restart_on_error: true                  # Auto-restart on errors
  default_video: "copy"                   # Copy video codec (no transcoding)
  default_audio: "copy"                   # Copy audio codec (no transcoding)
```

### Template Variables

**Path Templates:**
- `{stream}` - Stream name
- `{year}` - 4-digit year (2024)
- `{month}` - 2-digit month (01-12)  
- `{day}` - 2-digit day (01-31)
- `{hour}` - 2-digit hour (00-23)

**Filename Templates:**
- `{stream}` - Stream name
- `{timestamp}` - Full timestamp (2024-01-15_14-30-25)
- `{date}` - Date only (2024-01-15)
- `{time}` - Time only (14-30-25)

---

## API Usage

### Start Recording

**Basic Recording:**
```bash
curl -X POST "http://localhost:1984/api/record?src=camera1"
```

**Custom Settings:**
```bash
curl -X POST "http://localhost:1984/api/record?src=camera1&filename=custom.mp4&duration=30m&video=h264&audio=aac"
```

**Segmented Recording:**
```bash
curl -X POST "http://localhost:1984/api/record?src=camera1&segments=true"
```

**Parameters:**
- `src` (required) - Stream name to record
- `filename` (optional) - Output filename (auto-generated if omitted)
- `duration` (optional) - Recording duration (e.g., "30s", "5m", "2h")
- `format` (optional) - Output format (mp4, mkv, avi, mov)
- `video` (optional) - Video codec ("copy", "h264", "h265", etc.)
- `audio` (optional) - Audio codec ("copy", "aac", "opus", etc.)
- `segments` (optional) - Enable segmentation ("true"/"false")
- `id` (optional) - Custom recording ID

**Response:**
```json
{
  "id": "camera1_1703174400",
  "stream": "camera1",
  "type": "single",
  "config": {
    "filename": "recordings/2024/01/15/camera1/camera1_2024-01-15_14-30-25.mp4",
    "format": "mp4",
    "duration": "1800s",
    "video": "copy",
    "audio": "copy"
  },
  "status": {
    "active": true,
    "start_time": "2024-01-15T14:30:25Z",
    "duration": "45s",
    "remaining": "1755s"
  }
}
```

### List Recordings

```bash
curl "http://localhost:1984/api/record"
```

**Response:**
```json
{
  "camera1_1703174400": {
    "id": "camera1_1703174400",
    "stream": "camera1",
    "type": "single",
    "active": true,
    "status": {...}
  },
  "camera2_1703174450": {
    "id": "camera2_1703174450", 
    "stream": "camera2",
    "type": "segmented",
    "active": true,
    "current_segment": 3,
    "status": {...}
  }
}
```

### Get Recording Status

```bash
curl "http://localhost:1984/api/record?id=camera1_1703174400"
```

### Stop Recording

```bash
curl -X DELETE "http://localhost:1984/api/record?id=camera1_1703174400"
```

### Get Recording Statistics

```bash
curl "http://localhost:1984/api/record/stats"
```

**Response:**
```json
{
  "total_recordings": 15,
  "total_size_mb": 2048,
  "oldest_recording": "2024-01-10T10:00:00Z",
  "newest_recording": "2024-01-15T14:30:25Z",
  "streams": {
    "camera1": 8,
    "camera2": 5,
    "doorbell": 2
  },
  "config": {
    "base_path": "recordings",
    "retention_days": 7,
    "max_total_size": 10240
  }
}
```

### Manual Cleanup

```bash
curl -X POST "http://localhost:1984/api/record/cleanup"
```

---

## Codec Copying vs Transcoding

### Codec Copying (Default - Recommended)

**Advantages:**
- ✅ **No quality loss** - Bit-perfect copy of original stream
- ✅ **Minimal CPU usage** - No encoding/decoding overhead  
- ✅ **Fast recording** - Real-time or faster recording speeds
- ✅ **Multiple streams** - Can record many streams simultaneously
- ✅ **Low power consumption** - Especially important for ARM devices

**Usage:**
```bash
# Default behavior - codecs are copied automatically
curl -X POST "http://localhost:1984/api/record?src=camera1"

# Explicitly specify codec copying
curl -X POST "http://localhost:1984/api/record?src=camera1&video=copy&audio=copy"
```

### Transcoding

**When to use:**
- Converting to more compatible codecs (H.264/AAC)
- Reducing file sizes
- Changing resolution or bitrates
- Source codec not supported by target format

**Usage:**
```bash
# Transcode to H.264/AAC for maximum compatibility
curl -X POST "http://localhost:1984/api/record?src=camera1&video=h264&audio=aac"

# Transcode with bitrate limit
curl -X POST "http://localhost:1984/api/record?src=camera1&video=h264&bitrate=2M"
```

---

## Segmentation

Automatic file segmentation splits long recordings into multiple files for easier management.

### Configuration

```yaml
recording:
  enable_segments: true
  segment_duration: "1h"      # New file every hour
  max_file_size: 2048         # New file when reaching 2GB
```

### Time-based Segmentation

Files are automatically split based on duration:
```
recordings/2024/01/15/camera1/
├── camera1_2024-01-15_14-00-00_part001.mp4  # 14:00-15:00
├── camera1_2024-01-15_15-00-00_part002.mp4  # 15:00-16:00
└── camera1_2024-01-15_16-00-00_part003.mp4  # 16:00-17:00
```

### Size-based Segmentation

Files are split when they reach the maximum size:
```bash
# Enable size-based segmentation
curl -X POST "http://localhost:1984/api/record?src=camera1&segments=true"
```

---

## Retention and Cleanup

Automatic cleanup prevents storage from filling up by removing old recordings.

### Retention Policies

1. **Time-based:** Delete recordings older than specified age
2. **Count-based:** Keep only the newest N recordings per stream
3. **Size-based:** Delete oldest recordings when total size exceeds limit

### Configuration Examples

```yaml
# Keep recordings for 30 days
recording:
  retention_days: 30
  enable_cleanup: true
  cleanup_interval: "4h"

# Keep maximum 50 recordings per stream
recording:
  max_recordings: 50

# Keep total storage under 100GB
recording:
  max_total_size: 102400  # 100GB in MB
```

### Archiving vs Deletion

```yaml
# Move old files to archive instead of deleting
recording:
  move_to_archive: true
  archive_path: "/backup/recordings/archive"
```

---

## Use Cases and Examples

### 1. Home Security System

```yaml
recording:
  base_path: "/media/security"
  path_template: "{year}/{month}/{stream}"
  retention_days: 14
  enable_segments: true
  segment_duration: "2h"
  auto_start: true            # Auto-record all streams
  default_video: "copy"
  default_audio: "copy"
```

### 2. Business Surveillance

```yaml
recording:
  base_path: "/storage/surveillance"  
  path_template: "{year}/{month}/{day}/{hour}/{stream}"
  retention_days: 90          # 90-day retention for compliance
  max_total_size: 1048576     # 1TB storage limit
  enable_segments: true
  segment_duration: "1h"      # Hourly segments
  cleanup_interval: "30m"     # Frequent cleanup checks
  move_to_archive: true       # Archive for long-term storage
  archive_path: "/archive/surveillance"
```

### 3. Event Recording

```bash
# Record specific event for 30 minutes
curl -X POST "http://localhost:1984/api/record?src=camera1&duration=30m&filename=event_$(date +%Y%m%d_%H%M%S).mp4"
```

### 4. High-Quality Archival

```bash
# Record with minimal compression for archival
curl -X POST "http://localhost:1984/api/record?src=camera1&video=copy&audio=copy&format=mkv"
```

---

## Troubleshooting

### Common Issues

**Recording fails to start:**
- Check that the stream exists and is active
- Verify FFmpeg is installed and accessible
- Check disk space and permissions

**High CPU usage:**
- Ensure codecs are set to "copy" (default)
- Check if transcoding is accidentally enabled

**Files not cleaned up:**
- Verify `enable_cleanup: true` in configuration
- Check `cleanup_interval` setting
- Ensure retention policies are properly configured

**Segmented recordings not working:**
- Enable segments with `enable_segments: true`
- Set appropriate `segment_duration`
- Check that directories can be created

### Logs and Monitoring

Monitor recording activity in go2rtc logs:
```bash
# Check go2rtc logs for recording activity
journalctl -u go2rtc -f | grep recording
```

View recording statistics:
```bash
curl "http://localhost:1984/api/record/stats" | jq
```

---

## Performance Considerations

### CPU Usage

- **Codec copying:** ~1-5% CPU per stream
- **H.264 transcoding:** ~20-50% CPU per stream  
- **Hardware encoding:** ~5-15% CPU per stream (if available)

### Disk I/O

- **Write speed required:** Match stream bitrate (typically 2-8 Mbps)
- **Concurrent recordings:** Ensure storage can handle multiple streams
- **Cleanup frequency:** Balance between storage efficiency and I/O load

### Network Impact

- **Local streams:** No additional network load
- **Remote streams:** Recording doesn't add network overhead beyond normal streaming

---

## Integration Examples

### Home Assistant

```yaml
# automation.yaml
- id: start_recording_on_motion
  trigger:
    platform: state
    entity_id: binary_sensor.motion_detector
    to: 'on'
  action:
    service: rest_command.start_recording
    data:
      stream_name: "{{ trigger.entity_id.split('.')[1] }}"
      duration: "5m"

# rest_command.yaml  
rest_command:
  start_recording:
    url: "http://go2rtc:1984/api/record"
    method: POST
    payload: "src={{ stream_name }}&duration={{ duration }}"
```

### Shell Scripts

```bash
#!/bin/bash
# record_all_cameras.sh
CAMERAS=("camera1" "camera2" "doorbell")

for camera in "${CAMERAS[@]}"; do
  curl -X POST "http://localhost:1984/api/record?src=$camera&duration=1h"
  echo "Started recording for $camera"
done
```

---

This comprehensive recording feature provides production-ready stream recording capabilities with enterprise-level management features while maintaining the simplicity and performance that go2rtc is known for.