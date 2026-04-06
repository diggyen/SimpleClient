package domain

// ScanEventType identifies the kind of scan event emitted.
type ScanEventType string

const (
	EventHostFound    ScanEventType = "host_found"
	EventScanProgress ScanEventType = "scan_progress"
	EventScanComplete ScanEventType = "scan_complete"
)

// ScanEvent carries scan lifecycle updates from the scanner to the UI.
type ScanEvent struct {
	Type       ScanEventType
	Host       *Host // non-nil for EventHostFound
	Scanned    int
	Total      int
	DurationMs int64 // non-zero for EventScanComplete
}
