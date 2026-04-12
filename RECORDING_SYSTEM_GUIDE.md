# go2rtc Recording System Guide

FFmpeg-based recording with segmentation, per-stream retention, cleanup, scheduling, and post-recording object detection.

## Table of Contents
- [Quick Start](#quick-start)
- [Configuration Reference](#configuration-reference)
- [Per-Stream Configuration](#per-stream-configuration)
- [Object Detection](#object-detection)
- [Scheduling](#scheduling)
- [Cleanup System](#cleanup-system)
- [API Endpoints](#api-endpoints)
- [Troubleshooting](#troubleshooting)

---

## Quick Start

```yaml
recording:
  base_path: "/recordings"
  default_format: "mp4"
  default_video: "copy"
  default_audio: "copy"
  auto_start: true
  enable_segments: true
  segment_duration: "10m"
  retention_days: 7
  enable_cleanup: true
  cleanup_interval: "1h"

  streams:
    frontdoor:
      enabled: true
      source: "rtsp://user:pass@192.168.1.100/stream1"
    backyard:
      enabled: true
      source: "rtsp://user:pass@192.168.1.101/stream1"
```

---

## Configuration Reference

### Global Settings

| Field | Default | Description |
|-------|---------|-------------|
| `base_path` | `recordings` | Root directory for all recordings |
| `path_template` | `{stream}` | Subdirectory structure under base_path |
| `filename_template` | `{stream}_{timestamp}` | File naming pattern |
| `default_format` | `mp4` | Container format |
| `default_video` | `copy` | Video codec (`copy` = no transcoding) |
| `default_audio` | `copy` | Audio codec |
| `auto_start` | `false` | Record all streams automatically |
| `enable_segments` | `true` | Split recordings into segments |
| `segment_duration` | `10m` | Segment length |
| `max_file_size` | `1024` | Max segment size in MB |
| `retention_days` | `7` | Global retention (overridable per stream) |
| `retention_hours` | `0` | Alternative to retention_days (more granular) |
| `max_recordings` | `100` | Max segments per stream |
| `max_total_size` | `10240` | Total storage cap in MB |
| `enable_cleanup` | `true` | Auto-delete old files |
| `cleanup_interval` | `1h` | Cleanup check frequency |
| `direct_source` | — | Global RTSP template, e.g. `rtsp://nvr/{stream}` |
| `restart_on_error` | `true` | Restart FFmpeg on failure |
| `create_directories` | `true` | Auto-create storage directories |

**Path/filename placeholders:** `{stream}`, `{year}`, `{month}`, `{day}`, `{hour}`, `{timestamp}`, `{date}`, `{time}`

---

## Per-Stream Configuration

All global settings can be overridden per stream. Only set what differs from the global defaults.

```yaml
recording:
  retention_days: 7          # global default
  segment_duration: "10m"

  streams:
    frontdoor:
      enabled: true
      source: "rtsp://nvr:pass@10.0.0.1:554/Channels/101"

    shed1:
      enabled: true
      source: "rtsp://nvr:pass@10.0.0.2:554/Channels/101"
      retention_days: 30     # keep longer than global default
      detection: true        # enable post-recording object detection

    backyard:
      enabled: true
      source: "rtsp://nvr:pass@10.0.0.3:554/Channels/101"
      segment_duration: "5m" # shorter segments
      retention_hours: 48    # 2 days only

    indoor:
      enabled: true
      source: "rtsp://nvr:pass@10.0.0.4:554/Channels/101"
      detection: true
      detection_interval: 2  # sample every 2 seconds instead of 1
      detection_labels:       # only care about these classes
        - person
```

### Stream-Specific Fields

| Field | Description |
|-------|-------------|
| `enabled` | Enable/disable recording for this stream |
| `source` | Direct RTSP URL (bypasses internal routing, lower CPU) |
| `format` | Override container format |
| `video` / `audio` | Override codec |
| `segment_duration` | Override segment length |
| `retention_days` | Override global retention |
| `retention_hours` | Override global retention (hours) |
| `max_recordings` | Override max segments |
| `auto_start` | Override auto-start for this stream |
| `width` / `height` / `framerate` | Force resolution/framerate |
| `bitrate_limit` | Cap output bitrate, e.g. `"2M"` |
| `schedule` | Cron expression (see [Scheduling](#scheduling)) |
| `detection` | Enable post-recording detection (bool) |
| `detection_interval` | Seconds between sampled frames (default: global) |
| `detection_labels` | Label filter override for this stream |

### Direct Source vs Internal Routing

Recording source priority:
1. Per-stream `source:` field
2. Global `direct_source:` template (replaces `{stream}` with stream name)
3. Internal RTSP fallback (`rtsp://127.0.0.1:{port}/{stream}`)

Direct source bypasses go2rtc's internal pipeline — lower CPU, recommended when no stream processing is needed.

---

## Object Detection

Post-recording object detection analyses completed segments using [CodeProject.AI](https://www.codeproject.com/AI/docs/) or DeepStack. Results are stored as a `.json` sidecar file next to each recording segment.

### Setup

**1. Run CodeProject.AI:**
```bash
docker run -p 32168:32168 codeproject/ai-server
```

**2. Enable detection in config:**
```yaml
recording:
  detection:
    enabled: true
    backend_url: "http://localhost:32168"
    frame_interval: 1        # analyse 1 frame per second
    min_confidence: 0.45
    labels:                  # global label filter
      - person
      - car
      - truck
      - cat
      - dog
      - bird
    retention_days: 30       # prune old sidecar files

  streams:
    shed1:
      enabled: true
      source: "rtsp://..."
      detection: true        # opt in per stream

    frontdoor:
      enabled: true
      source: "rtsp://..."
      detection: true
      detection_labels:      # override global labels for this stream
        - person
        - car

    backyard:
      enabled: true
      source: "rtsp://..."
      # detection not set — skipped
```

Detection only runs when **both** `recording.detection.enabled: true` (global) **and** `detection: true` on the stream.

### How It Works

1. A recording segment completes (rotation or manual stop)
2. The file is queued for analysis
3. FFmpeg extracts frames at `frame_interval` seconds
4. Each frame is POSTed to the detection backend
5. Results written as `{recording_name}.json` alongside the video file

### Sidecar Format

```json
{
  "file": "shed1_2026-04-12_14-00-00.mp4",
  "analysed_at": "2026-04-12T14:11:05Z",
  "duration_secs": 600,
  "frame_interval": 1,
  "frames_checked": 600,
  "labels": ["person", "cat"],
  "detections": [
    {"time_secs": 12, "label": "person", "confidence": 0.91, "x_min": 120, "y_min": 80, "x_max": 340, "y_max": 480},
    {"time_secs": 47, "label": "cat",    "confidence": 0.72}
  ]
}
```

### UI

The recordings page (`/recordings.html`) shows detected object badges per segment and an **Object** filter dropdown to narrow results by label.

### Backfill Existing Recordings

Re-analyse all un-processed segments for a stream:
```bash
curl -X POST "http://localhost:1984/api/detection/analyze?stream=shed1"
```

Analyse a specific file:
```bash
curl -X POST "http://localhost:1984/api/detection/analyze?file=/recordings/shed1/shed1_2026-04-12_14-00-00.mp4&stream=shed1"
```

---

## Scheduling

Record only during specific time windows using cron syntax.

```yaml
recording:
  streams:
    office:
      enabled: true
      schedule: "0 9 * * 1-5"     # weekdays 9am only

    entrance:
      enabled: true
      schedule: "0 20 * * *"      # every day at 8pm
```

**Cron format:** `minute hour day month weekday`

Supports wildcards (`*`), ranges (`9-17`), lists (`1,3,5`), steps (`*/15`).

**API:**
```bash
# List schedules
curl "http://localhost:1984/api/schedule"

# Test a cron expression (shows next 5 run times)
curl "http://localhost:1984/api/schedule/test?expr=0+9+*+*+1-5"
```

---

## Cleanup System

Cleanup uses filename-embedded timestamps — reliable across restarts regardless of file modification times.

### Protection Rules

Files are protected from deletion if:
- Newer than `protect_recent_files` (default `1h`)
- Deleting would drop below `minimum_files_per_stream` (default `5`)
- Deleting would drop below `minimum_total_files` (default `10`)

### Manual Cleanup

```bash
# Dry run — see what would be deleted
curl -X POST "http://localhost:1984/api/record/cleanup?dry_run=true"

# Force cleanup
curl -X POST "http://localhost:1984/api/record/cleanup"

# Aggressive cleanup bypassing protection rules
curl "http://localhost:1984/api/record/force-cleanup?age_hours=48&dry_run=true"
curl "http://localhost:1984/api/record/force-cleanup?age_hours=48"
```

---

## API Endpoints

### Recordings

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/record` | List active recording processes |
| POST | `/api/record?src=NAME` | Start recording |
| DELETE | `/api/record?id=ID` | Stop recording |
| GET | `/api/record/configured` | List cameras configured for recording |
| GET | `/api/record/stats` | Storage statistics |
| GET | `/api/record/health` | Health check |

### Recording Files

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/recordings` | List recording files (supports `?stream=`, `?date=`, `?limit=`) |
| GET | `/api/recordings?download=ID` | Download a recording |
| GET | `/api/recordings?info=ID` | Detailed ffprobe info |

### Cleanup

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/record/cleanup` | Run cleanup with stats |
| GET | `/api/record/cleanup-info` | Cleanup configuration info |
| GET | `/api/record/force-cleanup` | Aggressive cleanup (bypasses protection) |

### Watchdog

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/record/watchdog` | Watchdog status per stream |
| POST | `/api/record/watchdog/reset` | Reset watchdog counters |

### Scheduling

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/schedule` | List schedules |
| POST | `/api/schedule` | Add schedule |
| DELETE | `/api/schedule` | Remove schedule |
| GET | `/api/schedule/test?expr=...` | Test cron expression |

### Detection

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/detection/status` | Queue depth and per-stream status |
| POST | `/api/detection/analyze?stream=NAME` | Queue all un-analysed files for a stream |
| POST | `/api/detection/analyze?file=PATH` | Queue a specific file |

---

## Troubleshooting

### Enable Debug Logging
```yaml
log:
  level: debug
```

### Stream Not Recording

```bash
# Check which cameras are configured for recording
curl "http://localhost:1984/api/record/configured"

# Check active recording processes
curl "http://localhost:1984/api/record"

# Check stream health
curl "http://localhost:1984/api/record/health"
```

Common causes:
- Stream not listed under `recording.streams`
- `enabled: false` on the stream
- RTSP source unreachable — check `source:` URL

### Per-Stream Retention Not Working

Ensure `retention_days` is set on the stream (not just globally) and `enable_cleanup: true` is set. Cleanup runs on the `cleanup_interval` — check logs for `[recording] processing stream cleanup`.

### Detection Not Running

1. Verify CodeProject.AI is reachable: `curl http://localhost:32168/v1/vision/detection`
2. Check `recording.detection.enabled: true` globally
3. Check `detection: true` on the specific stream
4. Check status: `curl http://localhost:1984/api/detection/status`

### Duplicate Recordings / Multiple FFmpeg Processes

```bash
pgrep -c ffmpeg   # count FFmpeg processes
```

Restart go2rtc to clear stale processes.

### Storage Full

```bash
# Check stats
curl "http://localhost:1984/api/record/stats"

# Emergency cleanup
curl "http://localhost:1984/api/record/force-cleanup?age_hours=24"
```

---

## Performance Tips

- Use `default_video: copy` and `default_audio: copy` — no transcoding, minimal CPU
- Use per-stream `source:` or global `direct_source:` to bypass internal routing
- Set `segment_duration: "10m"` — balances file count vs management overhead
- Enable detection only on cameras where it adds value; each segment queues an FFmpeg frame-extraction job
