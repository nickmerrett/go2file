# Verifying Codec Copy Functionality

## How Codec Copying Works

The go2rtc recording feature uses **codec copying by default** to avoid CPU-intensive transcoding.

## FFmpeg Command Generated

When you start a recording with default settings, go2rtc generates an FFmpeg command like this:

```bash
ffmpeg -i rtsp://127.0.0.1:8554/camera1 -c:v copy -c:a copy -f mp4 -y recording.mp4
```

The key parts:
- `-c:v copy` = Copy video codec (no transcoding)
- `-c:a copy` = Copy audio codec (no transcoding)
- `-f mp4` = Output format
- `-y` = Overwrite existing file

## Testing Codec Copy

### 1. Start a recording with default settings:
```bash
curl -X POST "http://localhost:1984/api/record?src=your_stream&filename=test_copy.mp4"
```

### 2. Monitor CPU usage:
```bash
top -p $(pgrep ffmpeg)
```

### 3. Check the actual FFmpeg process:
```bash
ps aux | grep ffmpeg
```
You should see `-c:v copy -c:a copy` in the command line.

### 4. Compare file sizes and creation speed:
Codec copying should be:
- **Faster**: File creation at near real-time speed
- **Lower CPU**: Minimal CPU usage (usually <5%)
- **Identical quality**: No quality loss since no re-encoding occurs

## Advanced Codec Copying Options

### Copy specific codecs only:
```bash
# Copy video, encode audio to AAC (maybe source audio is incompatible)
curl -X POST "http://localhost:1984/api/record?src=camera1&video=copy&audio=aac"

# Copy audio, encode video to H264 (maybe for compatibility)
curl -X POST "http://localhost:1984/api/record?src=camera1&video=h264&audio=copy"
```

### Check source stream codecs first:
```bash
# Get stream info to see what codecs are available for copying
curl "http://localhost:1984/api/streams?src=camera1"
```

## Troubleshooting

If codec copying doesn't work, it might be because:

1. **Container incompatibility**: Source codec might not be compatible with output format
   - Solution: Use a different output format (e.g., `.mkv` instead of `.mp4`)

2. **Source stream issues**: Source might have codec issues
   - Solution: Check source stream with `ffprobe` or similar tools

3. **Format constraints**: Some formats don't support certain codecs
   - Solution: Use `mkv` format which supports almost all codecs

## Benefits of Codec Copying

- ✅ **No quality loss** (bit-perfect copy)
- ✅ **Minimal CPU usage** (no encoding/decoding)
- ✅ **Fast recording** (real-time or faster)
- ✅ **Lower power consumption**
- ✅ **Supports multiple concurrent recordings**

## When to Use Transcoding Instead

You might want to transcode (not copy) when:
- Converting to a more compatible codec (H264)
- Reducing file size
- Changing resolution
- Adding filters or effects
- Source codec is not supported by target format