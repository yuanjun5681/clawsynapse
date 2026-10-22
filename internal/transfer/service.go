package transfer

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"clawsynapse/internal/discovery"
	"clawsynapse/internal/identity"
	"clawsynapse/internal/messaging"
	"clawsynapse/internal/natsbus"
	"clawsynapse/internal/protocol"
	"clawsynapse/pkg/types"

	"github.com/nats-io/nats.go"
)

type ReceivedNotifier func(rec TransferRecord)

type Service struct {
	mu          sync.Mutex
	log         *slog.Logger
	peers       *discovery.Registry
	bus         *natsbus.Client
	messaging   *messaging.Service
	nodeID      string
	identity    *identity.Identity
	trustMode   string
	transferDir string
	maxFileSize int64
	ttl         time.Duration
	transfers   map[string]*TransferRecord
	onReceived  ReceivedNotifier
}

func NewService(log *slog.Logger, peers *discovery.Registry, bus *natsbus.Client, msgSvc *messaging.Service, nodeID string, id *identity.Identity, trustMode string, cfg TransferConfig) *Service {
	ttl := parseTTL(cfg.TTL)
	maxFileSize := cfg.MaxFileSize
	if maxFileSize <= 0 {
		maxFileSize = 104857600 // 100MB
	}
	transferDir := cfg.TransferDir
	if transferDir == "" {
		home, _ := os.UserHomeDir()
		transferDir = filepath.Join(home, ".clawsynapse", "transfers")
	}

	return &Service{
		log:         log,
		peers:       peers,
		bus:         bus,
		messaging:   msgSvc,
		nodeID:      nodeID,
		identity:    id,
		trustMode:   trustMode,
		transferDir: transferDir,
		maxFileSize: maxFileSize,
		ttl:         ttl,
		transfers:   make(map[string]*TransferRecord),
	}
}

func (s *Service) OnReceived(fn ReceivedNotifier) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onReceived = fn
}

func (s *Service) Enabled() bool {
	return s.bus.HasJetStream()
}

func (s *Service) Start(ctx context.Context) error {
	if !s.Enabled() {
		s.log.Warn("transfer service disabled: jetstream not available")
		return nil
	}

	if err := os.MkdirAll(s.transferDir, 0o700); err != nil {
		return fmt.Errorf("create transfer dir: %w", err)
	}

	_, err := s.ensureBucket(s.nodeID)
	if err != nil {
		if isJetStreamUnavailable(err) {
			s.log.Warn("transfer service disabled: jetstream unavailable", slog.String("error", err.Error()))
			return nil
		}
		return fmt.Errorf("ensure inbox bucket: %w", err)
	}
	s.log.Info("transfer inbox bucket ready", slog.String("bucket", bucketName(s.nodeID)))

	go s.watchBucket(ctx)

	return nil
}

func (s *Service) watchBucket(ctx context.Context) {
	store, err := s.getBucket(s.nodeID)
	if err != nil {
		s.log.Error("watch bucket failed", slog.String("error", err.Error()))
		return
	}

	watcher, err := store.Watch(nats.IgnoreDeletes())
	if err != nil {
		s.log.Error("start watch failed", slog.String("error", err.Error()))
		return
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case info, ok := <-watcher.Updates():
			if !ok {
				return
			}
			if info == nil {
				continue
			}
			transferID := info.Name
			if s.isAlreadyDownloaded(transferID) {
				continue
			}
			if err := s.pullAndSave(transferID, bucketName(s.nodeID)); err != nil {
				s.log.Warn("watch pull failed",
					slog.String("transferId", transferID),
					slog.String("error", err.Error()),
				)
			}
		}
	}
}

func (s *Service) SendFile(req SendFileRequest) (SendFileResult, error) {
	if !s.Enabled() {
		return SendFileResult{}, protocol.NewError(protocol.ErrTransferDisabled, "transfer disabled: jetstream not available")
	}

	peer, ok := s.peers.Get(req.TargetNode)
	if !ok {
		return SendFileResult{}, fmt.Errorf("target peer not found")
	}
	if s.trustMode != "open" {
		if peer.TrustStatus != types.TrustTrusted {
			return SendFileResult{}, protocol.NewError("control.unauthorized", "peer is not trusted")
		}
		if peer.AuthStatus != types.AuthAuthenticated {
			return SendFileResult{}, fmt.Errorf("peer is not authenticated")
		}
	}

	info, err := os.Stat(req.FilePath)
	if err != nil {
		return SendFileResult{}, fmt.Errorf("stat file: %w", err)
	}
	if info.IsDir() {
		return SendFileResult{}, fmt.Errorf("path is a directory")
	}
	if info.Size() > s.maxFileSize {
		return SendFileResult{}, protocol.NewError(protocol.ErrTransferFileTooLarge,
			fmt.Sprintf("file size %d exceeds limit %d", info.Size(), s.maxFileSize))
	}

	store, err := s.ensureBucket(req.TargetNode)
	if err != nil {
		return SendFileResult{}, protocol.NewError(protocol.ErrTransferBucketError, err.Error())
	}

	transferID := newTransferID()
	fileName := filepath.Base(req.FilePath)

	f, err := os.Open(req.FilePath)
	if err != nil {
		return SendFileResult{}, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	meta := &nats.ObjectMeta{
		Name: transferID,
		Headers: nats.Header{
			"X-From":      {s.nodeID},
			"X-File-Name": {fileName},
		},
	}
	if req.MimeType != "" {
		meta.Headers.Set("X-Mime-Type", req.MimeType)
	}
	if len(req.Metadata) > 0 {
		if mdBytes, err := json.Marshal(req.Metadata); err == nil {
			meta.Headers.Set("X-Metadata", string(mdBytes))
		}
	}

	objInfo, err := store.Put(meta, f)
	if err != nil {
		return SendFileResult{}, protocol.NewError(protocol.ErrTransferFailed, fmt.Sprintf("put object: %v", err))
	}

	bucket := bucketName(req.TargetNode)
	checksum := objInfo.Digest

	rec := &TransferRecord{
		TransferID: transferID,
		Direction:  "outbound",
		PeerNode:   req.TargetNode,
		FileName:   fileName,
		FileSize:   info.Size(),
		MimeType:   req.MimeType,
		Checksum:   checksum,
		Status:     "completed",
		Bucket:     bucket,
		Metadata:   req.Metadata,
		CreatedAt:  time.Now().UnixMilli(),
	}
	s.mu.Lock()
	s.transfers[transferID] = rec
	s.mu.Unlock()

	notifyPayload, _ := json.Marshal(map[string]any{
		"transferId": transferID,
		"fileName":   fileName,
		"size":       info.Size(),
		"mimeType":   req.MimeType,
		"bucket":     bucket,
	})
	_, _ = s.messaging.Publish(messaging.PublishRequest{
		TargetNode: req.TargetNode,
		Type:       "transfer.available",
		Payload:    protocol.MessagePayload{Input: notifyPayload},
		Metadata:   req.Metadata,
	})

	s.log.Info("file sent",
		slog.String("transferId", transferID),
		slog.String("targetNode", req.TargetNode),
		slog.String("fileName", fileName),
		slog.Int64("fileSize", info.Size()),
		slog.String("bucket", bucket),
	)

	return SendFileResult{
		TransferID: transferID,
		Bucket:     bucket,
		Size:       info.Size(),
		Checksum:   checksum,
	}, nil
}

func (s *Service) HandleTransferNotification(env protocol.MessageEnvelope) error {
	if !s.Enabled() {
		return fmt.Errorf("transfer service is not available")
	}

	var payload struct {
		TransferID string `json:"transferId"`
		Bucket     string `json:"bucket"`
		FileName   string `json:"fileName"`
		Size       int64  `json:"size"`
		MimeType   string `json:"mimeType"`
	}
	if err := protocol.DecodeStrict(env.Payload.Input, &payload); err != nil {
		s.log.Warn("decode transfer notification failed", slog.String("error", err.Error()))
		return nil
	}

	if payload.TransferID == "" || payload.Bucket == "" {
		s.log.Warn("transfer notification missing fields")
		return nil
	}

	if s.isAlreadyDownloaded(payload.TransferID) {
		return nil
	}

	if err := s.pullAndSave(payload.TransferID, payload.Bucket); err != nil {
		s.log.Warn("pull from notification failed",
			slog.String("transferId", payload.TransferID),
			slog.String("error", err.Error()),
		)
		return err
	}
	return nil
}

func (s *Service) pullAndSave(transferID, bucket string) error {
	if s.isAlreadyDownloaded(transferID) {
		return nil
	}

	js := s.bus.JetStream()
	if js == nil {
		return fmt.Errorf("jetstream not available")
	}

	store, err := js.ObjectStore(bucket)
	if err != nil {
		return fmt.Errorf("get bucket %s: %w", bucket, err)
	}

	objInfo, err := store.GetInfo(transferID)
	if err != nil {
		return fmt.Errorf("get object info: %w", err)
	}

	from := ""
	fileName := transferID
	mimeType := ""
	var metadata map[string]any
	if objInfo.Headers != nil {
		if v := objInfo.Headers.Get("X-From"); v != "" {
			from = v
		}
		if v := objInfo.Headers.Get("X-File-Name"); v != "" {
			fileName = v
		}
		if v := objInfo.Headers.Get("X-Mime-Type"); v != "" {
			mimeType = v
		}
		if v := objInfo.Headers.Get("X-Metadata"); v != "" {
			_ = json.Unmarshal([]byte(v), &metadata)
		}
	}

	if s.trustMode != "open" && from != "" {
		peer, ok := s.peers.Get(from)
		if !ok {
			return fmt.Errorf("sender %s not found", from)
		}
		if peer.TrustStatus != types.TrustTrusted {
			return fmt.Errorf("sender %s not trusted", from)
		}
	}

	result, err := store.Get(transferID)
	if err != nil {
		return fmt.Errorf("get object: %w", err)
	}
	defer result.Close()

	localName := transferID + "-" + fileName
	localPath := filepath.Join(s.transferDir, localName)

	outFile, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("create local file: %w", err)
	}

	written, err := outFile.ReadFrom(result)
	closeErr := outFile.Close()
	if err != nil {
		os.Remove(localPath)
		return fmt.Errorf("write file: %w", err)
	}
	if closeErr != nil {
		os.Remove(localPath)
		return fmt.Errorf("close file: %w", closeErr)
	}

	rec := &TransferRecord{
		TransferID:  transferID,
		Direction:   "inbound",
		PeerNode:    from,
		FileName:    fileName,
		FileSize:    written,
		MimeType:    mimeType,
		Checksum:    objInfo.Digest,
		Status:      "completed",
		LocalPath:   localPath,
		Bucket:      bucket,
		Metadata:    metadata,
		CreatedAt:   objInfo.ModTime.UnixMilli(),
		CompletedAt: time.Now().UnixMilli(),
	}
	s.mu.Lock()
	s.transfers[transferID] = rec
	s.mu.Unlock()

	s.log.Info("file received",
		slog.String("transferId", transferID),
		slog.String("from", from),
		slog.String("fileName", fileName),
		slog.Int64("fileSize", written),
		slog.String("localPath", localPath),
	)

	s.mu.Lock()
	fn := s.onReceived
	s.mu.Unlock()
	if fn != nil {
		go fn(*rec)
	}

	return nil
}

func (s *Service) isAlreadyDownloaded(transferID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.transfers[transferID]
	return ok && rec.Direction == "inbound" && rec.Status == "completed"
}

func (s *Service) ListTransfers() []TransferInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TransferInfo, 0, len(s.transfers))
	for _, rec := range s.transfers {
		out = append(out, toTransferInfo(rec))
	}
	return out
}

func (s *Service) GetTransfer(id string) (TransferInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.transfers[id]
	if !ok {
		return TransferInfo{}, false
	}
	return toTransferInfo(rec), true
}

func (s *Service) DeleteTransfer(id string) error {
	if !s.Enabled() {
		return protocol.NewError(protocol.ErrTransferDisabled, "transfer disabled")
	}

	s.mu.Lock()
	rec, ok := s.transfers[id]
	s.mu.Unlock()

	if !ok {
		return protocol.NewError(protocol.ErrTransferNotFound, "transfer not found")
	}

	if rec.Bucket != "" {
		js := s.bus.JetStream()
		if js != nil {
			store, err := js.ObjectStore(rec.Bucket)
			if err == nil {
				_ = store.Delete(id)
			}
		}
	}

	if rec.LocalPath != "" {
		_ = os.Remove(rec.LocalPath)
	}

	s.mu.Lock()
	delete(s.transfers, id)
	s.mu.Unlock()

	s.log.Info("transfer deleted", slog.String("transferId", id))
	return nil
}

func toTransferInfo(rec *TransferRecord) TransferInfo {
	return TransferInfo{
		TransferID:  rec.TransferID,
		Direction:   rec.Direction,
		PeerNode:    rec.PeerNode,
		FileName:    rec.FileName,
		FileSize:    rec.FileSize,
		MimeType:    rec.MimeType,
		Checksum:    rec.Checksum,
		Status:      rec.Status,
		LocalPath:   rec.LocalPath,
		Metadata:    rec.Metadata,
		CreatedAt:   rec.CreatedAt,
		CompletedAt: rec.CompletedAt,
	}
}

func newTransferID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("tf-%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func isJetStreamUnavailable(err error) bool {
	return errors.Is(err, nats.ErrJetStreamNotEnabled) ||
		errors.Is(err, nats.ErrNeeds262)
}
