package transfer

import "clawsynapse/internal/blob"

type TransferRecord struct {
	TransferID        string         `json:"transferId"`
	Direction         string         `json:"direction"`
	PeerBot           string         `json:"peerBot"`
	FileName          string         `json:"fileName"`
	FileSize          int64          `json:"fileSize"`
	MimeType          string         `json:"mimeType,omitempty"`
	Checksum          string         `json:"checksum,omitempty"`
	Status            string         `json:"status"`
	LocalPath         string         `json:"localPath,omitempty"`
	Bucket            string         `json:"bucket"`
	Metadata          map[string]any `json:"metadata,omitempty"`
	CreatedAt         int64          `json:"createdAt"`
	CompletedAt       int64          `json:"completedAt,omitempty"`
	BlobDigest        string         `json:"blobDigest,omitempty"`
	NotificationState string         `json:"notificationState,omitempty"`
	Error             string         `json:"error,omitempty"`
}

type SendFileRequest struct {
	TargetBot string
	FilePath  string
	MimeType  string
	Metadata  map[string]any
}

type SendFileResult struct {
	TransferID string
	Bucket     string
	Size       int64
	Checksum   string
}

type PullResult struct {
	TransferID string
	FilePath   string
	Size       int64
	Checksum   string
}

type TransferInfo struct {
	TransferID  string         `json:"transferId"`
	Direction   string         `json:"direction"`
	PeerBot     string         `json:"peerBot"`
	FileName    string         `json:"fileName"`
	FileSize    int64          `json:"fileSize"`
	MimeType    string         `json:"mimeType,omitempty"`
	Checksum    string         `json:"checksum,omitempty"`
	Status      string         `json:"status"`
	LocalPath   string         `json:"localPath,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   int64          `json:"createdAt"`
	CompletedAt int64          `json:"completedAt,omitempty"`
}

type TransferConfig struct {
	TransferDir string
	MaxFileSize int64
	TTL         string
	BotID       string
	Blobs       *blob.Store
	Workspace   string
	QuotaBytes  int64
	// InboxOnly records received files without materializing an execution copy.
	InboxOnly bool
}
