# Recording Feature Test

## API Documentation

The recording feature adds support for saving streams to files via FFmpeg.

### API Endpoints

#### Start Recording
```
POST /api/record?src=<stream_name>&filename=<output_file>
```

Parameters:
- `src` (required): Name of the stream to record
- `filename` (optional): Output filename (default: recordings/{stream}_{timestamp}.mp4)
- `format` (optional): Output format (default: auto-detect from filename extension)
- `duration` (optional): Recording duration (e.g., "30s", "5m", "1h")
- `video` (optional): Video codec (default: "copy")
- `audio` (optional): Audio codec (default: "copy")
- `id` (optional): Custom recording ID

Response:
```json
{
  "id": "camera1_1672531200",
  "stream": "camera1", 
  "config": {
    "filename": "recordings/camera1_2023-01-01_12-00-00.mp4",
    "format": "mp4",
    "duration": "30s",
    "video": "copy",
    "audio": "copy"
  },
  "status": {
    "id": "camera1_1672531200",
    "stream": "camera1",
    "filename": "recordings/camera1_2023-01-01_12-00-00.mp4",
    "format": "mp4",
    "active": true,
    "start_time": "2023-01-01T12:00:00Z",
    "duration": "5s",
    "max_duration": "30s", 
    "remaining": "25s"
  }
}
```

#### Stop Recording
```
DELETE /api/record?id=<recording_id>
```

Response:
```json
{
  "status": "stopped"
}
```

#### List Recordings
```
GET /api/record
```

Response:
```json
{
  "camera1_1672531200": {
    "id": "camera1_1672531200",
    "stream": "camera1",
    "config": {...},
    "status": {...}
  }
}
```

#### Get Recording Status
```
GET /api/record?id=<recording_id>
```

## Test Examples

### 1. Basic Recording Test
```bash
# Start a 30 second recording of camera1
curl -X POST "http://localhost:1984/api/record?src=camera1&duration=30s"

# List active recordings
curl "http://localhost:1984/api/record"

# Stop recording
curl -X DELETE "http://localhost:1984/api/record?id=camera1_1672531200"
```

### 2. Custom Format and Quality
```bash
# Record with H264 transcoding to ensure compatibility
curl -X POST "http://localhost:1984/api/record?src=camera1&filename=test.mp4&video=h264&audio=aac"
```

### 3. Long-term Recording
```bash
# Start continuous recording (will run until manually stopped)
curl -X POST "http://localhost:1984/api/record?src=camera1&filename=continuous_recording.mkv"
```

## Configuration

You can also configure recording in go2rtc.yaml:

```yaml
streams:
  camera1: rtsp://admin:password@192.168.1.100/stream1

# Recording will be available via API for any configured stream
```

## Features

- **Multiple concurrent recordings** per stream
- **Auto-codec detection** and copying to minimize CPU usage
- **Flexible output formats**: MP4, MKV, AVI, MOV
- **Duration limits** with automatic stop
- **Directory creation** for output files
- **RESTful API** for easy integration
- **Real-time status** monitoring

## Implementation Details

The recording feature:
1. Uses existing FFmpeg integration via exec producer
2. Reads stream via internal RTSP server
3. Writes to file using FFmpeg command
4. Supports all codecs that FFmpeg can handle
5. Provides real-time status and control via HTTP API