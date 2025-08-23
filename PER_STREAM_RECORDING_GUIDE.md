# Per-Stream Recording Configuration Guide

## ✅ **Yes! You can specify which cameras to record in the config!**

The go2rtc recording feature includes comprehensive **per-stream configuration** that allows you to:

- ✅ **Enable/disable recording** for specific cameras
- ✅ **Auto-start recordings** when streams become available  
- ✅ **Custom settings** per camera (quality, format, retention)
- ✅ **Individual storage paths** and naming schemes
- ✅ **Different codec settings** per stream

---

## 🎯 **Basic Per-Stream Configuration**

### **Simple Example: Enable specific cameras**

```yaml
# go2rtc.yaml
streams:
  camera1: rtsp://admin:password@192.168.1.100/stream1
  camera2: rtsp://admin:password@192.168.1.101/stream1
  doorbell: rtsp://admin:password@192.168.1.102/stream1
  camera3: rtsp://admin:password@192.168.1.103/stream1

recording:
  # Global defaults
  base_path: "recordings"
  default_video: "copy"    # Copy codecs by default
  default_audio: "copy"
  
  # Specify which cameras to record
  streams:
    camera1:
      recording:
        enabled: true        # ✅ Record this camera
        auto_start: true     # Start recording automatically
      
    camera2:
      recording:
        enabled: true        # ✅ Record this camera  
        auto_start: true
      
    doorbell:
      recording:
        enabled: true        # ✅ Record this camera
        auto_start: false    # Don't auto-start (manual only)
      
    camera3:
      recording:
        enabled: false       # ❌ Don't record this camera
```

**Result:** Only `camera1` and `camera2` will auto-record. `doorbell` can be started manually. `camera3` won't record at all.

---

## 🔧 **Advanced Per-Stream Settings**

### **Different Quality Settings Per Camera**

```yaml
recording:
  # Global defaults
  default_video: "copy"
  default_audio: "copy"
  
  streams:
    # High-quality security camera - no transcoding
    security_cam:
      recording:
        enabled: true
        auto_start: true
        video: "copy"         # Copy original codec (best quality, minimal CPU)
        audio: "copy"
        format: "mkv"         # Use MKV for long recordings
        retention_days: 30    # Keep for 30 days
      
    # Lower-quality overview camera - transcode to save space
    overview_cam:
      recording:
        enabled: true  
        auto_start: true
        video: "h264"         # Transcode to H.264
        audio: "aac"          # Transcode to AAC  
        bitrate_limit: "1M"   # Limit to 1 Mbps to save storage
        format: "mp4"
        retention_days: 7     # Keep for only 7 days
      
    # Doorbell - event-based recording
    doorbell:
      recording:
        enabled: true
        auto_start: false     # Manual start only
        video: "copy"         # Best quality for events
        audio: "copy"
        retention_days: 60    # Keep events longer
```

### **Custom Storage Organization**

```yaml
recording:
  streams:
    # Security cameras - organized by location
    front_door:
      enabled: true
      auto_start: true
      path_template: "security/front/{year}/{month}"
      filename_template: "front_door_{timestamp}"
      
    back_yard:
      enabled: true
      auto_start: true  
      path_template: "security/back/{year}/{month}"
      filename_template: "back_yard_{timestamp}"
      
    # Baby monitor - separate organization
    baby_room:
      enabled: true
      auto_start: true
      path_template: "baby_monitor/{year}/{month}/{day}"
      filename_template: "baby_{date}_{time}"
      format: "mp4"
      retention_days: 90    # Keep baby videos longer
```

**File Structure Result:**
```
recordings/
├── security/
│   ├── front/2024/01/
│   │   └── front_door_2024-01-15_14-30-25.mp4
│   └── back/2024/01/
│       └── back_yard_2024-01-15_14-30-25.mp4
└── baby_monitor/2024/01/15/
    └── baby_2024-01-15_14-30-25.mp4
```

### **Segmented Recording Per Stream**

```yaml
recording:
  # Global defaults - no segmentation
  enable_segments: false
  
  streams:
    # 24/7 security camera - segment into 2-hour files
    security_main:
      enabled: true
      auto_start: true
      enable_segments: true
      segment_duration: "2h"    # 2-hour segments
      max_file_size: 2048       # Also split at 2GB
      
    # Motion camera - single files per recording
    motion_detector:
      enabled: true
      auto_start: false         # Triggered manually/by automation
      enable_segments: false    # Single file per recording
      
    # High-activity area - small segments for easier management  
    parking_lot:
      enabled: true
      auto_start: true
      enable_segments: true
      segment_duration: "30m"   # 30-minute segments
      max_file_size: 512        # Split at 512MB
```

---

## 🚀 **Automatic Recording Startup**

### **How Auto-Start Works**

1. **Stream Detection**: go2rtc monitors configured streams
2. **Availability Check**: When a stream becomes available
3. **Auto-Record**: If `enabled: true` and `auto_start: true`, recording starts automatically
4. **Recovery**: If recording fails, it will retry based on `restart_on_error` setting

### **Configuration Options**

```yaml
recording:
  streams:
    camera1:
      enabled: true           # Enable recording for this stream
      auto_start: true        # Start automatically when stream is available
      restart_on_error: true  # Restart if FFmpeg process fails
      
    camera2:
      enabled: true           # Recording is enabled...
      auto_start: false       # ...but only start manually via API
      
    camera3:
      enabled: false          # No recording at all (overrides global settings)
```

### **Global vs Stream Settings**

```yaml
recording:
  # Global setting - applies to all streams without specific config
  auto_start: true
  
  streams:
    camera1:
      # No 'enabled' or 'auto_start' specified
      # Will use global auto_start: true
      
    camera2:
      enabled: false          # Override global setting - don't record
      
    camera3:
      enabled: true           # Override global setting - do record
      auto_start: false       # But don't auto-start
```

---

## 📋 **Complete Configuration Reference**

### **Per-Stream Options**

```yaml
recording:
  streams:
    camera_name:
      recording:
        # Basic Recording Control
        enabled: true                    # Enable/disable recording
        auto_start: true                 # Auto-start when stream available
        restart_on_error: true           # Restart on FFmpeg failures
        
        # Quality Settings
        video: "copy"                    # Video codec: copy, h264, h265, etc.
        audio: "copy"                    # Audio codec: copy, aac, opus, etc.
        bitrate_limit: "2M"              # Bitrate limit (e.g., 2M = 2 Mbps)
        width: 1920                      # Force specific width
        height: 1080                     # Force specific height  
        framerate: 30                    # Force specific framerate
        
        # File Settings
        format: "mp4"                    # Output format: mp4, mkv, avi, mov
        path_template: "{year}/{month}/{stream}"      # Custom directory structure
        filename_template: "{stream}_{timestamp}"     # Custom filename pattern
        
        # Segmentation
        enable_segments: true            # Enable automatic segmentation
        segment_duration: "1h"           # Segment duration
        max_file_size: 1024              # Max file size in MB
        
        # Retention
        retention_days: 14               # Days to keep recordings
        retention_hours: 0               # Hours to keep (alternative to days)
        max_recordings: 50               # Max recordings to keep
        
        # Future Features
        schedule: "0 9-17 * * 1-5"       # Cron-like schedule (future)
        record_on_motion: false          # Motion-triggered recording (future)
```

---

## 🎯 **Common Use Cases**

### **1. Home Security System**

```yaml
streams:
  front_door: rtsp://192.168.1.100/stream1
  back_door: rtsp://192.168.1.101/stream1  
  garage: rtsp://192.168.1.102/stream1
  living_room: rtsp://192.168.1.103/stream1

recording:
  base_path: "/media/security"
  retention_days: 30
  
  streams:
    # Outdoor cameras - always record
    front_door:
      enabled: true
      auto_start: true
      video: "copy"
      retention_days: 60    # Keep outdoor footage longer
      
    back_door:
      enabled: true
      auto_start: true
      video: "copy" 
      retention_days: 60
      
    garage:
      enabled: true
      auto_start: true
      video: "copy"
      
    # Indoor camera - manual recording only
    living_room:
      enabled: true
      auto_start: false     # Privacy - only record when needed
      retention_days: 7     # Shorter retention for indoor
```

### **2. Business Surveillance**

```yaml
recording:
  streams:
    # High-priority areas - best quality
    main_entrance:
      enabled: true
      auto_start: true
      video: "copy"         # Best quality
      audio: "copy"
      enable_segments: true
      segment_duration: "1h"
      retention_days: 90    # Legal requirement
      
    cash_register:
      enabled: true
      auto_start: true
      video: "copy"
      audio: "copy" 
      retention_days: 180   # Financial records
      
    # General areas - compressed to save space  
    warehouse:
      enabled: true
      auto_start: true
      video: "h264"         # Compress to save space
      audio: "aac"
      bitrate_limit: "1M"
      retention_days: 30
      
    break_room:
      enabled: false        # Privacy - no recording
```

### **3. Mixed Environment**

```yaml
recording:
  # Most cameras just copy codecs
  default_video: "copy"
  default_audio: "copy"
  
  streams:
    # Standard cameras - use defaults
    camera1:
      enabled: true
      auto_start: true
      
    camera2:
      enabled: true
      auto_start: true
      
    # Low-bandwidth remote camera
    remote_cabin:
      enabled: true
      auto_start: true
      video: "h264"           # Transcode for smaller files
      bitrate_limit: "500k"   # Very low bitrate
      format: "mp4"
      
    # High-res camera - segment due to large files
    4k_overview:
      enabled: true
      auto_start: true  
      video: "copy"           # Keep original 4K quality
      enable_segments: true
      segment_duration: "30m" # Smaller segments for 4K
      max_file_size: 1024
      
    # Temporary camera
    construction_site:
      enabled: true
      auto_start: true
      retention_days: 3       # Short retention
```

---

## 🔧 **Management via API**

You can also control per-stream recording via the API:

### **Check Stream Configuration**

```bash
# Get recording statistics (includes per-stream info)
curl "http://localhost:1984/api/record/stats"
```

### **Override Stream Settings via API**

```bash
# Start recording with custom settings (overrides config)
curl -X POST "http://localhost:1984/api/record?src=camera1&video=h264&duration=30m"
```

### **Manual Start for Auto-Start Disabled Streams**

```bash  
# Start recording for doorbell (even if auto_start: false)
curl -X POST "http://localhost:1984/api/record?src=doorbell&duration=5m"
```

---

## 🎉 **Summary**

**YES!** You have complete control over which cameras record:

✅ **Enable/disable** recording per camera  
✅ **Auto-start** specific cameras when they come online  
✅ **Custom quality settings** per camera  
✅ **Individual storage paths** and retention policies  
✅ **Mixed recording strategies** (continuous vs event-based)  
✅ **Codec copying by default** for minimal CPU usage  
✅ **Override global settings** per stream  

The configuration is **very flexible** - you can have some cameras recording 24/7 with codec copying, others transcoding to save space, some recording only on demand, and others disabled completely.

**Perfect for your use case where you want to specify exactly which cameras should record!** 🎯