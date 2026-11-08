package hive

import "time"

// PodpingData is a podcast update notification from the Hive blockchain.
type PodpingData struct {
	// URLs is the list of feed URLs that were updated.
	URLs []string
	// Timestamp is when the update was posted.
	Timestamp time.Time
	// Account is the Hive account that posted the update.
	Account string
	// Medium is the type of content (podcast, music, video, etc.).
	Medium string
	// Reason is why the ping was posted (update, live, liveEnd).
	Reason string
	// TrxID is the blockchain transaction ID.
	TrxID string
	// BlockNum is the blockchain block number.
	BlockNum int64
	// Version is the podping protocol version.
	Version string
}

// NewPodpingData fills in the podping protocol defaults (medium/reason/version).
func NewPodpingData(urls []string, ts time.Time, account, medium, reason, trxID string, blockNum int64, version string) PodpingData {
	if medium == "" {
		medium = "podcast"
	}
	if reason == "" {
		reason = "update"
	}
	if version == "" {
		version = "1.0"
	}
	return PodpingData{
		URLs:      urls,
		Timestamp: ts,
		Account:   account,
		Medium:    medium,
		Reason:    reason,
		TrxID:     trxID,
		BlockNum:  blockNum,
		Version:   version,
	}
}
