# 🎯 Surveillance Preset Configuration Guide

The **surveillance preset** provides optimized defaults for 24/7 security recording with automatic file segmentation.

---

## 🚀 Quick Start

1. **Copy the preset configuration:**
   ```bash
   cp surveillance_preset.yaml go2rtc.yaml
   ```

2. **Update camera URLs:**
   ```yaml
   streams:
     front_door: rtsp://admin:password@192.168.1.100/stream1
     back_door: rtsp://admin:password@192.168.1.101/stream1
     # Add your cameras here
   ```

3. **Start go2rtc:**
   ```bash
   ./go2rtc
   ```

4. **Recordings automatically start** and are saved to `/var/recordings/`

---

## 🔧 Key Surveillance Features

### **✅ Automatic Segmentation**
```yaml
enable_segments: true
segment_duration: "30m"    # 30-minute files
max_file_size: 1024        # 1GB max per file
```

**Benefits:**
- Easy file management and sharing
- Corruption isolation (one bad segment doesn't ruin entire recording)
- Memory-efficient processing
- Perfect for scrubbing through footage

### **✅ Codec Copying (Minimal CPU)**
```yaml
default_video: "copy"      # No transcoding = minimal CPU usage
default_audio: "copy"      # Preserve original quality
```

**Benefits:**
- Near-zero CPU load
- Original video quality preserved  
- No transcoding delays
- Handles multiple cameras easily

### **✅ Smart Storage Management**
```yaml
retention_days: 30         # Keep 30 days of footage
max_total_size: 512000     # 500GB storage limit
enable_cleanup: true       # Automatic cleanup
cleanup_interval: "1h"     # Check every hour
```

**Benefits:**
- Never run out of disk space
- Automatic old file deletion
- Configurable retention policies
- Optional archiving support

### **✅ Reliability Features**
```yaml
auto_start: true           # All cameras start recording automatically
restart_on_error: true     # Auto-restart failed recordings
buffer_time: "5s"          # Pre-recording buffer
post_recording_time: "10s" # Post-recording buffer
```

**Benefits:**
- Hands-off operation
- Automatic recovery from failures
- Never miss events at segment boundaries

---

## 📁 File Organization

The surveillance preset organizes recordings logically:

```
/var/recordings/
├── 2025/
│   ├── 08/
│   │   ├── 23/
│   │   │   ├── front_door/
│   │   │   │   ├── front_door_2025-08-23_09-00-00.mkv
│   │   │   │   ├── front_door_2025-08-23_09-30-00.mkv
│   │   │   │   └── front_door_2025-08-23_10-00-00.mkv
│   │   │   ├── back_door/
│   │   │   └── garage/
│   │   └── 24/
│   └── 09/
└── security/           # Custom paths for priority cameras
    ├── front_door/
    └── back_door/
```

**Easy to:**
- Find specific dates/times
- Share individual segments
- Archive old footage
- Analyze by camera

---

## 🎛️ Camera-Specific Settings

### **High-Priority Cameras**
```yaml
streams:
  front_door:
    enabled: true
    auto_start: true
    video: "copy"
    retention_days: 60        # Longer retention
    segment_duration: "60m"   # Larger segments
    path_template: "security/front_door/{year}/{month}/{day}"
```

### **Standard Cameras**
```yaml
streams:
  garage:
    enabled: true
    auto_start: true
    video: "copy"
    # Uses global defaults (30-day retention, 30-min segments)
```

### **Privacy Cameras**
```yaml
streams:
  living_room:
    enabled: true
    auto_start: false         # Manual start only
    retention_days: 7         # Shorter retention
    segment_duration: "15m"   # Smaller segments
```

### **Disabled Cameras**
```yaml
streams:
  old_camera:
    enabled: false            # No recording at all
```

---

## 🔍 Monitoring & Management

### **Recording Statistics**
```bash
curl "http://localhost:1984/api/record/stats"
```

### **Manual Recording Control**
```bash
# Start recording with custom duration
curl -X POST "http://localhost:1984/api/record?src=front_door&duration=1h"

# Stop recording
curl -X DELETE "http://localhost:1984/api/record?src=front_door"

# List active recordings
curl "http://localhost:1984/api/record"
```

### **Force Cleanup**
```bash
curl -X POST "http://localhost:1984/api/record/cleanup"
```

---

## ⚙️ Customization Options

### **Segment Duration Guidelines**
```yaml
# For different use cases:
segment_duration: "15m"     # High-activity areas (lots of motion)
segment_duration: "30m"     # Standard surveillance (balanced)
segment_duration: "60m"     # Low-activity areas (minimal motion)
segment_duration: "2h"      # Archive cameras (long-term storage)
```

### **Storage Options**
```yaml
# Conservative (limited storage)
retention_days: 7
max_total_size: 100000      # 100GB

# Balanced (standard surveillance)
retention_days: 30
max_total_size: 512000      # 500GB

# Extensive (large storage array)
retention_days: 90
max_total_size: 2048000     # 2TB
```

### **Quality vs Storage**
```yaml
# Maximum quality (large files)
default_video: "copy"
default_audio: "copy"

# Balanced quality (smaller files)
default_video: "h264"
default_audio: "aac"
bitrate_limit: "2M"

# Storage-optimized (smallest files)
default_video: "h265"
default_audio: "aac"
bitrate_limit: "1M"
```

---

## 🎉 Surveillance Preset Benefits

### **vs Default go2rtc Config:**
- ✅ **24/7 recording** instead of manual start
- ✅ **Automatic segmentation** instead of single large files
- ✅ **Storage management** instead of unlimited growth
- ✅ **Surveillance-focused** file organization
- ✅ **Reliability features** for unattended operation

### **vs Other NVR Solutions:**
- ✅ **Lightweight** - minimal resource usage
- ✅ **Flexible** - works with any RTSP camera
- ✅ **Open source** - no licensing costs
- ✅ **API-driven** - easy integration
- ✅ **Codec copying** - preserves original quality

---

## 🚨 Production Deployment Tips

1. **Storage Location:**
   ```yaml
   base_path: "/mnt/surveillance"  # Use dedicated storage
   ```

2. **Systemd Service:**
   ```bash
   # Create systemd service for auto-start
   sudo systemctl enable go2rtc
   ```

3. **Log Rotation:**
   ```yaml
   log:
     output: "/var/log/go2rtc.log"  # Log to file for rotation
   ```

4. **Backup Strategy:**
   ```yaml
   move_to_archive: true           # Archive instead of delete
   archive_path: "/mnt/backup"     # Network storage
   ```

5. **Monitoring:**
   ```bash
   # Set up alerts for recording failures
   tail -f /var/log/go2rtc.log | grep ERROR
   ```

---

## 🎯 Perfect For

- ✅ **Home security systems**
- ✅ **Business surveillance**  
- ✅ **Multi-camera installations**
- ✅ **24/7 monitoring setups**
- ✅ **Remote locations** with limited maintenance
- ✅ **Budget-conscious** installations
- ✅ **Mixed camera brands/models**

**The surveillance preset gives you enterprise-grade recording features with minimal configuration!** 🚀