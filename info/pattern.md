# DASH SegmentTimeline Pattern Mechanism in livesim2

## Overview

The pattern mechanism in livesim2 automatically detects repeating patterns in segment durations and represents them efficiently using DASH SegmentTimeline `<Pattern>` elements. This is particularly useful for audio tracks where segment durations vary slightly due to audio frame alignment requirements.

## When Patterns Are Used

Patterns are detected and applied when:
- At least 4 segments are available in the sliding window
- A repeating pattern of 2-12 segments is found
- At least 1.25 cycles of the pattern are present (allows detection with partial repetition)
- The `SegTimelineMode` is set to `SegTimelineModePattern` (for $Time$ addressing) or `SegTimelineModeNrPattern` (for $Number$ addressing)

## Pattern Detection Algorithm

### Step 1: Find Repeating Sequences
The algorithm searches for repeating patterns of length 2 to 12 segments with an improved detection threshold:

**Detection Threshold**: `pattern_length * 1.25 ≤ total_segments`

This allows detection with partial pattern repetition, making it practical for real-world scenarios.

#### Examples:

**Simple 8s Audio Pattern (testpic_2s)**:
```
Durations: [96256, 96256, 96256, 95232, 96256, 96256, 96256, 95232, ...]
Pattern found: [96256, 96256, 96256, 95232] (length 4)
Detection: 4 * 1.25 = 5 segments needed ✓ (8+ segments available)
```

**Complex 24s Audio-Video Cycle**:
```
Video cycle: 4s + 2s = 6s (incompatible with AAC 1024-sample frames)
Audio durations: [192512, 96256, 191488, 96256, 191488, 96256, 192512, 95232, ...]
Pattern found: [192512, 96256, 191488, 96256, 191488, 96256, 192512, 95232] (length 8)
Detection: 8 * 1.25 = 10 segments needed ✓ (30s content = 10 segments)
```

### Step 2: Canonical Pattern Generation
Once a pattern is detected, it's converted to canonical form where the pattern starts with the longest duration:

```
Original: [96256, 95232, 96256, 96256]  # Pattern starting mid-cycle
Canonical: [96256, 96256, 96256, 95232]  # Pattern starting with longest duration
```

### Step 3: Run-Length Encoding
The canonical pattern is encoded using run-length encoding in the `<Pattern>` element:
```xml
<Pattern id="1">
  <P d="96256" r="2"/>  <!-- Duration 96256 used 3 times (r+1) -->
  <P d="95232" r="0"/>  <!-- Duration 95232 used 1 time (r+1) -->
</Pattern>
```

## Pattern Entry (pE) Calculation

The Pattern Entry (PE) value indicates where in the canonical pattern the sliding window starts.

### Exact Pattern Matching Method
Instead of using a simple modulo calculation, livesim2 uses an exact pattern matching approach:

1. **Pattern Matching**: For each possible offset (0 to pattern_length-1), check if the sliding window durations match the canonical pattern exactly
2. **Unique Correct Offset**: There should be exactly one offset where the pattern aligns perfectly with the sliding window
3. **Alignment Verification**: This ensures PE reflects the actual timing alignment with complete pattern verification

### Example with testpic_2s Asset
For testpic_2s with canonical pattern `[96256, 96256, 96256, 95232]`:

- `nowMS=800000` → sliding window starts `[96256, 96256, 96256, 95232, ...]` → exact match at offset 0 → pE = 0
- `nowMS=802000` → sliding window starts `[96256, 96256, 95232, 96256, ...]` → exact match at offset 1 → pE = 1
- `nowMS=804000` → sliding window starts `[96256, 95232, 96256, 96256, ...]` → exact match at offset 2 → pE = 2
- `nowMS=806000` → sliding window starts `[95232, 96256, 96256, 96256, ...]` → exact match at offset 3 → pE = 3

Each sliding window position has exactly one correct PE value where the durations align perfectly with the canonical pattern.

## Generated MPD Structure

When patterns are detected, the SegmentTimeline uses this structure:

### With $Time$ Addressing (SegTimelineModePattern)

```xml
<SegmentTemplate media="$RepresentationID$/$Time$.m4s" ...>
  <SegmentTimeline>
    <Pattern id="1">
      <P d="96256" r="2"/>
      <P d="95232" r="0"/>
    </Pattern>
    <S t="36864000" d="384000" r="14" p="1" pE="2"/>
  </SegmentTimeline>
</SegmentTemplate>
```

### With $Number$ Addressing (SegTimelineModeNrPattern)

```xml
<SegmentTemplate media="$RepresentationID$/$Number$.m4s" startNumber="384" ...>
  <SegmentTimeline>
    <Pattern id="1">
      <P d="96256" r="2"/>
      <P d="95232" r="0"/>
    </Pattern>
    <S t="36864000" d="384000" r="14" p="1" pE="2"/>
  </SegmentTimeline>
</SegmentTemplate>
```

**Important**: The SegmentTimeline is identical for both addressing modes. The distinction is in:
- **Media template**: `$Time$` vs `$Number$`
- **startNumber attribute**: Required for `$Number$` addressing

Where:
- `d="384000"`: Total duration of the pattern (8 seconds at 48kHz)
- `r="14"`: Number of pattern repetitions (15 total patterns)
- `p="1"`: Reference to Pattern id="1"
- `pE="2"`: Pattern Entry offset (starts at position 2 in the canonical pattern)
- `t="36864000"`: Start time (present in SegmentTimeline for both modes, describes timing)

## Audio Frame Alignment

Patterns commonly occur due to audio frame alignment with video segments:

### AAC-LC (1024 samples/frame at 48kHz)
- Frame duration: 1024/48000 = 21.333... ms
- 2s video segment = 96,000 timescale units
- Fits exactly 93.75 AAC-LC frames per 2s segment → slight duration variations

### AC-3 (1536 samples/frame at 48kHz)
- Frame duration: 1536/48000 = 32 ms
- 2s video segment = 96,000 timescale units
- Fits exactly 62.5 AC-3 frames → creates [96768, 95232] pattern

### HE-AAC (1024 samples/frame at base AAC-LC layer 24kHz, but enhanced to 48kHz by spectral band replication)
- Frame duration: 1024/24000 = 2048/48000 = 42.666... ms (double that of AAC-LC)
- 2s video segment = 96,000 timescale units
- Fits exactly 46.875 HE-AAC frames per 2s segment (half of AAC-LC) → duration variations

## Complex Video-Audio Cycles

### Alternating Segment Durations
When video uses alternating segment durations (e.g., 4s + 2s), the Least Common Multiple (LCM) with audio frame alignment creates longer patterns:

#### Example: 4s/2s Video with AAC-LC Audio
- **Video cycle**: 4s + 2s = 6s
- **Audio incompatibility**: 6s doesn't align with AAC 1024-sample frames
- **LCM calculation**: 24s is the shortest cycle where both video pattern and audio frames align
- **Pattern result**: 8-segment pattern spanning exactly 24 seconds
- **Real durations**: `[192512, 96256, 191488, 96256, 191488, 96256, 192512, 95232]`

#### Detection Requirements
- **30s content**: Provides 1.25 cycles (10 segments) of the 24s pattern (8 segments)
- **Threshold**: 8 × 1.25 = 10 segments needed ✓
- **Practical benefit**: Enables pattern detection without requiring 48s+ content

## URL Options for Pattern Support

### SegmentTimeline with $Time$ and Pattern
- URL parameter: `segtimeline_pattern/`
- Use case: Time-based segment addressing with pattern compression for audio
- Example: `/livesim2/segtimeline_pattern/testpic_2s/Manifest.mpd`

### SegmentTimeline with $Number$ and Pattern
- URL parameter: `segtimelinenr_pattern/`
- Use case: Number-based segment addressing with pattern compression for audio
- Example: `/livesim2/segtimelinenr_pattern/testpic_2s/Manifest.mpd`

Both options apply pattern detection only to audio tracks, allowing for efficient representation of audio segment duration variations while maintaining compatibility with different addressing modes.

## Benefits

1. **Compact Representation**: Reduces MPD size for long sliding windows
2. **Consistent Output**: Same canonical pattern regardless of sliding window position
3. **Standards Compliance**: Uses official DASH SegmentTimeline Pattern specification
4. **Automatic Detection**: No manual configuration required
5. **Practical Detection Threshold**: 1.25 cycle requirement enables pattern detection with minimal content duration
6. **Complex Cycle Support**: Handles video-audio LCM patterns up to 24s cycles with 30s content
7. **Addressing Mode Flexibility**: Support for both $Time$ and $Number$ addressing with pattern compression

## Implementation Files

- `cmd/livesim2/app/livempd.go`: Pattern detection and generation logic
- `cmd/livesim2/app/livempd_test.go`: Comprehensive test suite including PE validation
- Pattern length is computed mathematically from video/audio parameters when possible; brute-force fallback limited to 12 segments (`maxPatternLengthBruteForce`)