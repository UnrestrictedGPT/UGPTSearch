# Anubis Bot Detection Implementation

## Overview
This implementation adds real-time Anubis bot detection during SearXNG query execution, eliminating the need for pre-scraping all instances. The system automatically blacklists Anubis-protected instances during runtime while maintaining efficient resource usage.

## Features

### 1. Dynamic Detection
- **Real-time analysis**: Anubis protection is detected during actual search queries
- **Confidence-based scoring**: Uses a weighted scoring system with 40% confidence threshold
- **Multi-factor detection**: Analyzes response content, headers, status codes, and structure

### 2. Instance Management Integration
- **Automatic blacklisting**: Anubis-protected instances are marked and excluded from rotation
- **Health tracking**: Extended `InstanceHealth` structure includes Anubis detection status
- **Circuit breaker compatibility**: Integrates with existing circuit breaker patterns

### 3. Detection Criteria

#### High-Confidence Indicators (30 points max)
- Bot detection signatures (e.g., "bot detection", "verify you are human")
- Challenge-related text (e.g., "challenge required", "please enable javascript")
- Security messages (e.g., "access denied", "security check")

#### JavaScript Challenge Detection (20 points max)
- JavaScript patterns: `document.cookie`, `window.location`, `setTimeout`
- Cloudflare challenges: `cf-challenge`, `jschl_vc`, `jschl_answer`
- Challenge forms and redirects

#### HTTP Status Analysis (15 points max)
- `403 Forbidden`: 10 points
- `429 Too Many Requests`: 8 points
- `503 Service Unavailable`: 6 points

#### Header Analysis (15 points max)
- Suspicious headers: `cf-ray`, `x-protected-by`, `x-security`
- Cloudflare indicators: `cf-cache-status`
- Bot protection headers: `x-bot-protection`

#### Response Structure Analysis (20 points max)
- Unusually short responses (< 500 bytes): 5 points
- Minimal HTML structure (< 10 elements): 5 points
- CAPTCHA indicators: 8 points

### 4. API Endpoints

#### `/anubis-stats`
Provides detailed Anubis detection statistics:
```json
{
  "anubis_detection": {
    "total_instances": 75,
    "anubis_protected": 12,
    "available_instances": 58,
    "anubis_percentage": 16.0
  },
  "timestamp": "2025-08-23T10:30:00Z",
  "detection_info": {
    "confidence_threshold": 40.0,
    "detection_criteria": [...]
  }
}
```

#### `/instances` (Enhanced)
Extended to include Anubis statistics:
```json
{
  "instances": [...],
  "count": 75,
  "available_count": 58,
  "anubis_stats": {
    "total_instances": 75,
    "anubis_protected": 12,
    "available_instances": 58,
    "anubis_percentage": 16.0
  }
}
```

## Implementation Details

### Core Files Modified

#### `/src/internal/instances/manager.go`
- Added `AnubisProtected` and `AnubisDetectedAt` fields to `InstanceHealth`
- Implemented `MarkAnubisProtected()` method for blacklisting
- Added `GetAvailableInstanceCount()` and `GetAnubisStats()` methods
- Modified `GetNextInstance()` to skip Anubis-protected instances

#### `/src/internal/handlers/evasion.go`
- Implemented `DetectAnubisProtection()` function with comprehensive analysis
- Added `IsSearchResultValid()` for response validation
- Created `AnubisDetectionResult` structure for detailed reporting

#### `/src/internal/handlers/handlers.go`
- Integrated Anubis detection into both `Search()` and `SearchJSON()` functions
- Added automatic blacklisting when Anubis is detected
- Implemented response validation before marking instances as successful
- Added `AnubisStats()` endpoint handler

#### `/src/internal/server/server.go` & `/src/internal/server/concurrent_server.go`
- Added `/anubis-stats` route to both server implementations

### Detection Process Flow

1. **Request Processing**: Normal search request initiated
2. **Instance Selection**: `GetNextInstance()` automatically excludes Anubis-protected instances
3. **Response Analysis**: `DetectAnubisProtection()` analyzes the response
4. **Decision Making**: If confidence ≥ 40%, instance is blacklisted
5. **Retry Logic**: System attempts next available instance
6. **Statistics Tracking**: All detection events are recorded for monitoring

### Performance Characteristics

- **Zero Overhead**: No pre-scraping required
- **Fail-Fast**: Anubis detection happens immediately during query processing
- **Memory Efficient**: Only stores detection status, not full response analysis
- **Scalable**: Detection complexity is O(1) per response
- **Circuit Breaker Integration**: Works seamlessly with existing health management

### Error Handling

- **Graceful Degradation**: System continues with remaining instances if some are Anubis-protected
- **Detailed Logging**: All Anubis detections are logged with confidence scores
- **Retry Support**: Failed queries automatically retry with different instances
- **Fallback Mechanisms**: Invalid search results trigger instance rotation

## Benefits Over Pre-Scraping

1. **Reduced Resource Usage**: No upfront HTTP requests to all instances
2. **Real-time Accuracy**: Detection happens with actual search traffic
3. **Dynamic Adaptation**: Automatically adapts to changing instance protection status
4. **Lower Latency**: First successful query isn't delayed by pre-validation
5. **Bandwidth Efficient**: Only analyzes responses from attempted searches

## Monitoring and Observability

- **Live Statistics**: `/anubis-stats` endpoint provides real-time metrics
- **Instance Health**: Enhanced `/instances` endpoint shows availability status
- **Detection Confidence**: Full confidence scoring with detailed reasons
- **Historical Tracking**: Detection timestamps for pattern analysis

This implementation provides a robust, efficient solution for Anubis bot detection that integrates seamlessly with the existing SearXNG proxy architecture while minimizing resource overhead and maximizing search success rates.